package storage

import (
	"os"

	"golang.org/x/sys/unix"
)

// FreeBytes reports space available to this user on path's filesystem.
func FreeBytes(path string) (int64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, &os.PathError{Op: "statfs", Path: path, Err: err}
	}
	return int64(st.Bavail * uint64(st.Bsize)), nil
}
