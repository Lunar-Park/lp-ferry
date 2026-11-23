//go:build linux

package identity

import "golang.org/x/sys/unix"

func mountPointOf(p string) (string, error) { return devWalkMountPoint(p) }

func readOnly(mp string) bool {
	var st unix.Statfs_t
	return unix.Statfs(mp, &st) == nil && st.Flags&unix.ST_RDONLY != 0
}
