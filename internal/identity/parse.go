package identity

import (
	"bytes"
	"encoding/xml"
	"regexp"
	"strconv"
	"strings"
)

// NetworkFilesystems are filesystem type names that mean "network share".
var NetworkFilesystems = map[string]bool{
	"smbfs": true, "cifs": true, "nfs": true, "nfs4": true, "afpfs": true,
	"webdav": true, "ftp": true, "smb3": true,
}

var credentials = regexp.MustCompile(`^[^/@]*@`)

// SanitizeShare normalizes server/share and drops any user:pass@ prefix.
// Credentials are never identity (the same share mounted by two people is
// the same share) and must never be stored. Hosts are lowercased because
// DNS is case-insensitive; share names are not, because servers differ.
func SanitizeShare(server, share string) string {
	host := strings.ToLower(strings.Trim(strings.TrimSpace(credentials.ReplaceAllString(server, "")), "/"))
	name := strings.Trim(strings.TrimSpace(share), "/")
	if name == "" {
		return host
	}
	return host + "/" + name
}

// ShareHost is the host half of a sanitized server/share value.
func ShareHost(v string) string { h, _, _ := strings.Cut(v, "/"); return h }

// ShareName is the share half of a sanitized server/share value.
func ShareName(v string) string { _, s, _ := strings.Cut(v, "/"); return s }

// ShareIdentityFromDevice parses //[user@]server/share (SMB/AFP) or
// server:/export (NFS). Anything else is not a share, and inventing an
// identity from it would be the "mount path as identity" mistake.
func ShareIdentityFromDevice(device, fstype, provenance string) *VolumeIdentity {
	if !NetworkFilesystems[strings.ToLower(fstype)] {
		return nil
	}
	text := strings.TrimSpace(device)
	var value string
	switch {
	case strings.HasPrefix(text, "//"):
		server, share, ok := strings.Cut(text[2:], "/")
		if !ok {
			return nil
		}
		value = SanitizeShare(server, share)
	case strings.Contains(text, ":") && !strings.HasPrefix(text, "/"):
		server, export, _ := strings.Cut(text, ":")
		value = SanitizeShare(server, export)
	default:
		return nil
	}
	if ShareHost(value) == "" {
		return nil
	}
	return &VolumeIdentity{Kind: KindServerShare, Value: value, Confidence: Strong, Provenance: provenance}
}

// DiskutilInfo is the subset of `diskutil info -plist` Ferry reads.
type DiskutilInfo struct {
	VolumeUUID   string
	DiskUUID     string
	SolidState   *bool
	Removable    bool
	Internal     *bool
	BusProtocol  string
	FilesystemID string
}

// ParseDiskutilPlist reads the top-level dict of a diskutil XML plist.
func ParseDiskutilPlist(payload []byte) (DiskutilInfo, bool) {
	dec := xml.NewDecoder(bytes.NewReader(payload))
	var info DiskutilInfo
	depth := 0
	inTop := false
	var key string
	ok := false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			name := t.Name.Local
			if name == "dict" && depth == 2 {
				inTop, ok = true, true
				continue
			}
			if !inTop || depth != 3 {
				continue
			}
			switch name {
			case "key":
				var s string
				if dec.DecodeElement(&s, &t) == nil {
					key = s
				}
				depth--
			case "string":
				var s string
				if dec.DecodeElement(&s, &t) == nil {
					switch key {
					case "VolumeUUID":
						info.VolumeUUID = strings.ToLower(strings.TrimSpace(s))
					case "DiskUUID":
						info.DiskUUID = strings.ToLower(strings.TrimSpace(s))
					case "BusProtocol":
						info.BusProtocol = s
					case "FilesystemType", "FilesystemUserVisibleName":
						if info.FilesystemID == "" {
							info.FilesystemID = s
						}
					}
				}
				key = ""
				depth--
			case "true", "false":
				v := name == "true"
				switch key {
				case "SolidState":
					info.SolidState = &v
				case "Removable", "RemovableMedia", "RemovableMediaOrExternalDevice", "Ejectable":
					info.Removable = info.Removable || v
				case "Internal":
					info.Internal = &v
				}
				key = ""
			default:
				key = ""
			}
		case xml.EndElement:
			depth--
		}
	}
	return info, ok
}

// IdentityFromDiskutil: VolumeUUID is the filesystem (strong); DiskUUID is
// the media and survives a reformat, so it is only medium.
func IdentityFromDiskutil(info DiskutilInfo, mountPath string) *VolumeIdentity {
	if info.VolumeUUID != "" {
		return &VolumeIdentity{Kind: KindVolumeUUID, Value: info.VolumeUUID, Confidence: Strong,
			Provenance: "macos.diskutil:VolumeUUID:" + mountPath}
	}
	if info.DiskUUID != "" {
		return &VolumeIdentity{Kind: KindDiskUUID, Value: info.DiskUUID, Confidence: Medium,
			Provenance: "macos.diskutil:DiskUUID:" + mountPath}
	}
	return nil
}

// ClassFromDiskutil maps diskutil facts to a storage class.
func ClassFromDiskutil(info DiskutilInfo) StorageClass {
	bus := strings.ToLower(info.BusProtocol)
	switch {
	case strings.Contains(bus, "secure digital"), strings.Contains(bus, "sd card"):
		return ClassRemovable
	case info.SolidState != nil && !*info.SolidState:
		return ClassHDD
	case info.Removable && strings.Contains(bus, "usb") && (info.SolidState == nil):
		return ClassRemovable
	case info.SolidState != nil && *info.SolidState:
		return ClassSSD
	}
	return ClassUnknown
}

// MountEntry is one mount from a platform mount table.
type MountEntry struct {
	MountPoint string
	FSType     string
	Source     string
}

// ParseMacOSMountTable parses `/sbin/mount` output:
//
//	//alice@nas.local/media on /Volumes/media (smbfs, nodev, nosuid)
//
// Device strings (/dev/diskN, //user@host/share with %20 escapes) do not
// contain " on ", but mount points can ("/Volumes/A on B"), so the device
// ends at the first " on " and the options start at the last " (".
func ParseMacOSMountTable(text string) map[string]MountEntry {
	out := map[string]MountEntry{}
	for _, line := range strings.Split(text, "\n") {
		i := strings.Index(line, " on ")
		if i < 0 {
			continue
		}
		device, rest := line[:i], line[i+4:]
		j := strings.LastIndex(rest, " (")
		if j < 0 {
			continue
		}
		mp := strings.TrimSpace(rest[:j])
		fstype := strings.TrimSpace(strings.TrimRight(strings.Split(rest[j+2:], ",")[0], ")"))
		if mp != "" {
			out[mp] = MountEntry{MountPoint: mp, FSType: fstype, Source: strings.TrimSpace(device)}
		}
	}
	return out
}

// ParseLinuxMountinfo parses /proc/self/mountinfo. The mount point is field
// 5 and octal-escaped (\040 for space); after the " - " separator come the
// fstype and source.
func ParseLinuxMountinfo(text string) []MountEntry {
	var out []MountEntry
	for _, line := range strings.Split(text, "\n") {
		left, right, ok := strings.Cut(line, " - ")
		if !ok {
			continue
		}
		lf, rf := strings.Fields(left), strings.Fields(right)
		if len(lf) < 5 || len(rf) < 2 {
			continue
		}
		out = append(out, MountEntry{MountPoint: unescapeOctal(lf[4]), FSType: rf[0], Source: unescapeOctal(rf[1])})
	}
	return out
}

var octal = regexp.MustCompile(`\\([0-7]{3})`)

func unescapeOctal(s string) string {
	return octal.ReplaceAllStringFunc(s, func(m string) string {
		n, _ := strconv.ParseUint(m[1:], 8, 8)
		return string([]byte{byte(n)})
	})
}

// LinuxIdentity identifies one Linux mount from its share or by-uuid map.
func LinuxIdentity(e MountEntry, uuidByDevice map[string]string) *VolumeIdentity {
	if s := ShareIdentityFromDevice(e.Source, e.FSType, "linux.mountinfo:"+e.MountPoint); s != nil {
		return s
	}
	if u := uuidByDevice[e.Source]; u != "" {
		return &VolumeIdentity{Kind: KindVolumeUUID, Value: strings.ToLower(strings.TrimSpace(u)), Confidence: Strong,
			Provenance: "linux.by-uuid:" + e.MountPoint}
	}
	return nil
}
