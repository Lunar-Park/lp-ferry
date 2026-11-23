//go:build darwin

package identity

import (
	"bytes"
	"os"

	"golang.org/x/sys/unix"
)

func mountPointOf(p string) (string, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(p, &st); err != nil {
		return "", &os.PathError{Op: "statfs", Path: p, Err: err}
	}
	b := st.Mntonname[:]
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	if len(b) == 0 {
		return devWalkMountPoint(p)
	}
	return string(b), nil
}

func readOnly(mp string) bool {
	var st unix.Statfs_t
	return unix.Statfs(mp, &st) == nil && st.Flags&unix.MNT_RDONLY != 0
}
