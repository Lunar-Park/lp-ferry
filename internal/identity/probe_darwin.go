//go:build darwin

package identity

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func systemProber() Prober { return &DarwinProber{} }

// DarwinProber uses the mount table for shares and diskutil for volumes.
type DarwinProber struct{}

// Identify implements Prober.
func (p *DarwinProber) Identify(ctx context.Context, mountPaths []string) ProbeResult {
	res := ProbeResult{Mounts: map[string]MountInfo{}}
	table := map[string]MountEntry{}
	if out, err := run(ctx, "/sbin/mount"); err != nil {
		res.Warnings = append(res.Warnings, "could not read the mount table: "+err.Error())
		res.Degraded = true
	} else {
		table = ParseMacOSMountTable(string(out))
	}
	for _, mp := range mountPaths {
		e := table[mp]
		info := MountInfo{MountPath: mp, FSType: e.FSType, Class: ClassUnknown}
		if s := ShareIdentityFromDevice(e.Source, e.FSType, "macos.mount:"+mp); s != nil {
			info.Identity, info.Class = s, ClassNetwork
			res.Mounts[mp] = info
			continue
		}
		out, err := run(ctx, "/usr/sbin/diskutil", "info", "-plist", mp)
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("identity probe failed for %s: %v", mp, err))
			res.Degraded = true
			continue
		}
		du, _ := ParseDiskutilPlist(out)
		info.Class = ClassFromDiskutil(du)
		if id := IdentityFromDiskutil(du, mp); id != nil {
			info.Identity = id
		} else {
			info.Identity = PathOnly(mp)
			res.Warnings = append(res.Warnings, mp+" reports no volume UUID; it can only be recognized by explicit confirmation")
		}
		res.Mounts[mp] = info
	}
	return res
}

func run(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := withBudget(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // callers pass fixed system binaries (/sbin/mount, /usr/sbin/diskutil)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("timed out after %s", ProbeBudget)
	}
	if err != nil {
		if s := strings.TrimSpace(stderr.String()); s != "" {
			return nil, fmt.Errorf("%s: %s", name, s)
		}
		return nil, err
	}
	return out, nil
}
