//go:build linux

package identity

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

func systemProber() Prober {
	return &LinuxProber{Mountinfo: "/proc/self/mountinfo", ByUUID: "/dev/disk/by-uuid", SysBlock: "/sys/class/block"}
}

// LinuxProber reads mountinfo, /dev/disk/by-uuid and sysfs. Every read is
// bounded by ProbeBudget (a hung NFS mount can block a stat forever).
type LinuxProber struct {
	Mountinfo string
	ByUUID    string
	SysBlock  string
}

// Identify implements Prober.
func (p *LinuxProber) Identify(ctx context.Context, mountPaths []string) ProbeResult {
	ctx, cancel := withBudget(ctx)
	defer cancel()
	done := make(chan ProbeResult, 1)
	go func() { done <- p.identify(mountPaths) }()
	select {
	case r := <-done:
		return r
	case <-ctx.Done():
		return ProbeResult{Mounts: map[string]MountInfo{}, Warnings: []string{"identity probe timed out after " + ProbeBudget.String()}, Degraded: true}
	}
}

func (p *LinuxProber) identify(mountPaths []string) ProbeResult {
	res := ProbeResult{Mounts: map[string]MountInfo{}}
	text, err := os.ReadFile(p.Mountinfo)
	if err != nil {
		res.Warnings = append(res.Warnings, "could not read "+p.Mountinfo+": "+err.Error())
		res.Degraded = true
		return res
	}
	uuids := ReadUUIDMap(p.ByUUID)
	byMount := map[string]MountEntry{}
	for _, e := range ParseLinuxMountinfo(string(text)) {
		byMount[e.MountPoint] = e
	}
	for _, mp := range mountPaths {
		e, ok := byMount[mp]
		info := MountInfo{MountPath: mp, FSType: e.FSType, Class: ClassUnknown}
		if ok {
			info.Identity = LinuxIdentity(e, uuids)
			if NetworkFilesystems[strings.ToLower(e.FSType)] {
				info.Class = ClassNetwork
			} else {
				info.Class = p.classOf(e.Source)
			}
		}
		if info.Identity == nil {
			info.Identity = PathOnly(mp)
			res.Warnings = append(res.Warnings, mp+" has no filesystem UUID available; it can only be recognized by explicit confirmation")
		}
		res.Mounts[mp] = info
	}
	return res
}

// ReadUUIDMap maps resolved device path -> filesystem UUID.
func ReadUUIDMap(dir string) map[string]string {
	out := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if dev, err := filepath.EvalSymlinks(filepath.Join(dir, e.Name())); err == nil {
			out[dev] = e.Name()
		}
	}
	return out
}

// classOf reads ROTA/removable for a /dev/<name> block device, walking up
// from a partition to its parent disk.
func (p *LinuxProber) classOf(source string) StorageClass {
	if !strings.HasPrefix(source, "/dev/") {
		return ClassUnknown
	}
	dev := filepath.Base(source)
	sys, err := filepath.EvalSymlinks(filepath.Join(p.SysBlock, dev))
	if err != nil {
		return ClassUnknown
	}
	for _, dir := range []string{sys, filepath.Dir(sys)} {
		rot, err1 := os.ReadFile(filepath.Join(dir, "queue", "rotational"))
		rem, _ := os.ReadFile(filepath.Join(dir, "removable"))
		if err1 != nil {
			continue
		}
		return ClassFromSysfs(strings.TrimSpace(string(rot)), strings.TrimSpace(string(rem)), dev)
	}
	return ClassUnknown
}
