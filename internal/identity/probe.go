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

// MountPointOf returns the mount point containing path: the highest
// ancestor still on path's device.
func MountPointOf(path string) (string, error) {
	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
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

// System returns the prober for the running platform.
func System() Prober { return systemProber() }

func withBudget(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, ProbeBudget)
}
