// Package identity answers one narrow question about mounted storage: what
// durable identifier can the platform tell us about this mount right now?
// It returns evidence with a confidence level, never a decision; whether a
// saved destination rebinds is the resolver's call, and weak evidence never
// authorizes it.
//
// Parsers over captured platform text are pure and build on every OS; the
// thin probes that shell out or read /proc are per-platform and run under a
// strict time budget so a hung SMB share never stalls the daemon.
package identity

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// ProbeBudget bounds every platform probe.
const ProbeBudget = 5 * time.Second

// Identity kinds.
const (
	KindVolumeUUID  = "volume_uuid"
	KindDiskUUID    = "disk_uuid"
	KindServerShare = "server_share"
	KindPathOnly    = "path_only"
)

// Confidence levels.
const (
	Strong = "strong"
	Medium = "medium"
	Weak   = "weak"
)

// VolumeIdentity is one piece of identity evidence.
type VolumeIdentity struct {
	Kind       string `json:"kind"`
	Value      string `json:"value"`
	Confidence string `json:"confidence"`
	Provenance string `json:"provenance"`
}

// StorageClass is a coarse performance class the scheduler uses.
type StorageClass string

// Storage classes.
const (
	ClassUnknown   StorageClass = "unknown"
	ClassSSD       StorageClass = "ssd"       // NVMe / SATA SSD / APFS internal
	ClassHDD       StorageClass = "hdd"       // rotational
	ClassRemovable StorageClass = "removable" // SD card, USB flash, card reader
	ClassNetwork   StorageClass = "network"   // SMB / NFS / AFP
)

// MountInfo is what one probe learned about one mount path.
type MountInfo struct {
	MountPath string          `json:"mount_path"`
	FSType    string          `json:"fs_type,omitempty"`
	Identity  *VolumeIdentity `json:"identity,omitempty"`
	Class     StorageClass    `json:"class"`
}

// ProbeResult is one discovery pass, including what it could not learn.
// Warnings are recoverable: manual folder selection stays usable.
type ProbeResult struct {
	Mounts   map[string]MountInfo `json:"mounts"`
	Warnings []string             `json:"warnings,omitempty"`
	Degraded bool                 `json:"degraded,omitempty"`
}

// Prober is the boundary between the resolver and the platform.
type Prober interface {
	Identify(ctx context.Context, mountPaths []string) ProbeResult
}

// PathOnly is the honest fallback: we know where it is mounted and nothing
// else. Weak by construction; the resolver never rebinds on it.
func PathOnly(mountPath string) *VolumeIdentity {
	return &VolumeIdentity{
		Kind:       KindPathOnly,
		Value:      mountPath,
		Confidence: Weak,
		Provenance: runtime.GOOS + ".path-only",
	}
}

// MountPointOf returns the mount point of the filesystem holding path,
// as the kernel reports it (on macOS this sees through firmlinks: a path
// under /private/var lives on /System/Volumes/Data, not the sealed "/").
func MountPointOf(path string) (string, error) {
	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return mountPointOf(p)
}

// devWalkMountPoint is the portable fallback: the highest ancestor still
// on path's device.
func devWalkMountPoint(p string) (string, error) {
	var st unix.Stat_t
	if err := unix.Stat(p, &st); err != nil {
		return "", &os.PathError{Op: "stat", Path: p, Err: err}
	}
	dev := st.Dev
	for {
		parent := filepath.Dir(p)
		if parent == p {
			return p, nil
		}
		var pst unix.Stat_t
		if err := unix.Stat(parent, &pst); err != nil || pst.Dev != dev {
			return p, nil
		}
		p = parent
	}
}

// RelWithin expresses real (an absolute, resolved path) relative to the
// mount point mp. A firmlinked path (macOS /private/var on the Data
// volume) is not lexically under its mount point, so it is tried as
// mp + real as well.
func RelWithin(mp, real string) (string, bool) {
	if rel, err := filepath.Rel(mp, real); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
		if rel == "." {
			rel = ""
		}
		return filepath.ToSlash(rel), true
	}
	if _, err := os.Stat(filepath.Join(mp, real)); err == nil {
		return strings.TrimPrefix(filepath.ToSlash(real), "/"), true
	}
	return "", false
}

// System returns the prober for the running platform.
func System() Prober { return systemProber() }

func withBudget(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, ProbeBudget)
}

// MountPoints lists mounted volumes a destination could live on (cheap: no
// identity probe).
func MountPoints(ctx context.Context) []string { return mountPoints(ctx) }

// Observe identifies every mounted volume a destination could live on.
func Observe(ctx context.Context, p Prober) ([]Observation, ProbeResult) {
	mounts := mountPoints(ctx)
	res := p.Identify(ctx, mounts)
	var out []Observation
	for _, mp := range mounts {
		info, ok := res.Mounts[mp]
		if !ok {
			continue
		}
		// Writability of the mount is the read-only flag, not whether this
		// user may write the mount's root directory.
		out = append(out, Observation{MountPath: mp, Identity: info.Identity, Writable: !readOnly(mp)})
	}
	return out, res
}

// Snapshot probes the volume holding path and returns its mount info.
func Snapshot(ctx context.Context, p Prober, path string) (MountInfo, string, error) {
	mp, err := MountPointOf(path)
	if err != nil {
		return MountInfo{}, "", err
	}
	res := p.Identify(ctx, []string{mp})
	info, ok := res.Mounts[mp]
	if !ok {
		return MountInfo{MountPath: mp, Identity: PathOnly(mp), Class: ClassUnknown}, mp, nil
	}
	return info, mp, nil
}
