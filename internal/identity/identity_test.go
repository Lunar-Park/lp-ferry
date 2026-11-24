package identity

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestSanitizeShareStripsCredentials(t *testing.T) {
	cases := []struct{ dev, fs, want string }{
		{"//alice:hunter2@NAS.local/media", "smbfs", "nas.local/media"},
		{"//alice@nas.local/Media/sub", "smbfs", "nas.local/Media/sub"},
		{"//nas/share", "cifs", "nas/share"},
		{"nas.lan:/export/footage", "nfs4", "nas.lan/export/footage"},
	}
	for _, c := range cases {
		id := ShareIdentityFromDevice(c.dev, c.fs, "test")
		if id == nil || id.Value != c.want || id.Confidence != Strong || id.Kind != KindServerShare {
			t.Fatalf("%s: %+v", c.dev, id)
		}
		if contains(id.Value, "hunter2") || contains(id.Value, "alice") {
			t.Fatalf("credentials leaked: %s", id.Value)
		}
	}
	for _, c := range []struct{ dev, fs string }{
		{"/dev/disk3s1", "apfs"},
		{"//nas", "smbfs"},
		{"/local/path", "nfs"},
	} {
		if id := ShareIdentityFromDevice(c.dev, c.fs, "t"); id != nil {
			t.Fatalf("%s/%s produced %+v", c.dev, c.fs, id)
		}
	}
}

func contains(s, sub string) bool { return len(sub) > 0 && len(s) >= len(sub) && indexOf(s, sub) >= 0 }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

const plist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>BusProtocol</key>
	<string>USB</string>
	<key>DiskUUID</key>
	<string>AAAA-BBBB</string>
	<key>Ejectable</key>
	<true/>
	<key>Internal</key>
	<false/>
	<key>SolidState</key>
	<true/>
	<key>Partitions</key>
	<array><dict><key>VolumeUUID</key><string>NESTED-IGNORED</string></dict></array>
	<key>VolumeUUID</key>
	<string>0F1E2D3C-AAAA-BBBB-CCCC-000000000001</string>
</dict>
</plist>`

func TestParseDiskutilPlist(t *testing.T) {
	info, ok := ParseDiskutilPlist([]byte(plist))
	if !ok {
		t.Fatal("not parsed")
	}
	if info.VolumeUUID != "0f1e2d3c-aaaa-bbbb-cccc-000000000001" || info.DiskUUID != "aaaa-bbbb" {
		t.Fatalf("%+v", info)
	}
	id := IdentityFromDiskutil(info, "/Volumes/X")
	if id.Kind != KindVolumeUUID || id.Confidence != Strong {
		t.Fatalf("%+v", id)
	}
	info.VolumeUUID = ""
	if id := IdentityFromDiskutil(info, "/Volumes/X"); id.Kind != KindDiskUUID || id.Confidence != Medium {
		t.Fatalf("disk uuid must be medium: %+v", id)
	}
	if ClassFromDiskutil(info) != ClassSSD {
		t.Fatalf("class %s", ClassFromDiskutil(info))
	}
	if _, ok := ParseDiskutilPlist([]byte("garbage")); ok {
		t.Fatal("garbage parsed")
	}
}

func TestParseMountTables(t *testing.T) {
	mac := "/dev/disk3s1s1 on / (apfs, sealed, local, read-only, journaled)\n" +
		"//alice@nas.local/media on /Volumes/media (smbfs, nodev, nosuid, mounted by alice)\n" +
		"/dev/disk5s1 on /Volumes/A on B (exfat, local, nodev)\n"
	m := ParseMacOSMountTable(mac)
	if m["/Volumes/media"].FSType != "smbfs" || m["/Volumes/A on B"].FSType != "exfat" || m["/"].FSType != "apfs" {
		t.Fatalf("%+v", m)
	}
	lin := "36 35 98:0 / /mnt/my\\040drive rw,noatime master:1 - ext4 /dev/sdb1 rw\n" +
		"40 35 0:50 / /mnt/nas rw - cifs //bob:pw@nas/share rw\n"
	es := ParseLinuxMountinfo(lin)
	if len(es) != 2 || es[0].MountPoint != "/mnt/my drive" || es[1].FSType != "cifs" {
		t.Fatalf("%+v", es)
	}
	id := LinuxIdentity(es[0], map[string]string{"/dev/sdb1": "ABCD-1234"})
	if id == nil || id.Value != "abcd-1234" || id.Kind != KindVolumeUUID {
		t.Fatalf("%+v", id)
	}
	if id := LinuxIdentity(es[1], nil); id == nil || id.Value != "nas/share" {
		t.Fatalf("%+v", id)
	}
}

func TestClassFromSysfs(t *testing.T) {
	if ClassFromSysfs("0", "0", "nvme0n1") != ClassSSD || ClassFromSysfs("1", "0", "sda") != ClassHDD ||
		ClassFromSysfs("0", "0", "mmcblk0") != ClassRemovable || ClassFromSysfs("0", "1", "sdc") != ClassRemovable {
		t.Fatal("sysfs class mapping wrong")
	}
}

func strongID(v string) VolumeIdentity {
	return VolumeIdentity{Kind: KindVolumeUUID, Value: v, Confidence: Strong}
}

func TestResolve(t *testing.T) {
	base := t.TempDir()
	mount := filepath.Join(base, "Shuttle")
	if err := os.MkdirAll(filepath.Join(mount, "Footage"), 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(base, "Other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	saved := Saved{Identity: strongID("u1"), SubPath: "Footage", LastBinding: filepath.Join(mount, "Footage")}
	id := strongID("u1")
	wrong := strongID("u2")

	r := Resolve(saved, []Observation{{MountPath: mount, Identity: &id, Writable: true}})
	if r.State != Available || r.Binding != filepath.Join(mount, "Footage") {
		t.Fatalf("available: %+v", r)
	}
	if r := Resolve(saved, []Observation{{MountPath: mount, Identity: &id}}); r.State != Unwritable {
		t.Fatalf("read-only: %+v", r)
	}
	if r := Resolve(saved, []Observation{{MountPath: mount, Identity: &id, Writable: true}, {MountPath: other, Identity: &id, Writable: true}}); r.State != Ambiguous {
		t.Fatalf("two strong: %+v", r)
	}
	med := VolumeIdentity{Kind: KindVolumeUUID, Value: "u1", Confidence: Medium}
	if r := Resolve(saved, []Observation{{MountPath: mount, Identity: &med, Writable: true}}); r.State != NeedsConfirmation {
		t.Fatalf("medium: %+v", r)
	}
	// A14: a different disk mounted where ours used to be.
	if r := Resolve(saved, []Observation{{MountPath: mount, Identity: &wrong, Writable: true}}); r.State != NeedsConfirmation {
		t.Fatalf("occupant: %+v", r)
	}
	// A12: nothing mounted, leftover mount dir on the local disk.
	if r := Resolve(saved, nil); r.State != NeedsConfirmation {
		t.Fatalf("lingering dir: %+v", r)
	}
	gone := Saved{Identity: strongID("u1"), LastBinding: filepath.Join(base, "absent")}
	if r := Resolve(gone, nil); r.State != Offline {
		t.Fatalf("offline: %+v", r)
	}
	// Subfolder missing on the matched volume.
	missing := Saved{Identity: strongID("u1"), SubPath: "Nope"}
	if r := Resolve(missing, []Observation{{MountPath: mount, Identity: &id, Writable: true}}); r.State != NeedsConfirmation {
		t.Fatalf("missing subfolder: %+v", r)
	}
	// Subfolder is a symlink out of the volume.
	if err := os.Symlink(other, filepath.Join(mount, "Escape")); err != nil {
		t.Fatal(err)
	}
	esc := Saved{Identity: strongID("u1"), SubPath: "Escape"}
	if r := Resolve(esc, []Observation{{MountPath: mount, Identity: &id, Writable: true}}); r.State != NeedsConfirmation {
		t.Fatalf("escape: %+v", r)
	}
	// Weak saved identity never resolves available.
	weak := Saved{Identity: *PathOnly(mount)}
	if r := Resolve(weak, []Observation{{MountPath: mount, Identity: PathOnly(mount), Writable: true}}); r.State == Available {
		t.Fatalf("weak saved resolved available: %+v", r)
	}
	// Share alias on a different host.
	sh := Saved{Identity: VolumeIdentity{Kind: KindServerShare, Value: "nas.local/media", Confidence: Strong}}
	alias := VolumeIdentity{Kind: KindServerShare, Value: "10.0.0.5/media", Confidence: Strong}
	if r := Resolve(sh, []Observation{{MountPath: mount, Identity: &alias, Writable: true}}); r.State != NeedsConfirmation {
		t.Fatalf("alias: %+v", r)
	}
}

func TestSystemProbeIsTimeBoundedAndHonest(t *testing.T) {
	dir := t.TempDir()
	mp, err := MountPointOf(dir)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	res := System().Identify(context.Background(), []string{mp})
	if time.Since(start) > ProbeBudget+2*time.Second {
		t.Fatalf("probe exceeded its budget: %s", time.Since(start))
	}
	info, ok := res.Mounts[mp]
	if !ok && !res.Degraded {
		t.Fatalf("no answer and not degraded: %+v", res)
	}
	if ok && info.Identity == nil {
		t.Fatalf("mount reported without identity evidence: %+v", info)
	}
	t.Logf("%s %s: %+v class=%s warnings=%v", runtime.GOOS, mp, info.Identity, info.Class, res.Warnings)
}
