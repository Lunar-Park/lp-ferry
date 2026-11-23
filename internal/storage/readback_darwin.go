//go:build darwin

package storage

import (
	"os"

	"golang.org/x/sys/unix"
)

// ReadbackMode names how readback bypassed the page cache on this OS.
const ReadbackMode = "f_nocache"

// OpenReadback opens name for physical readback: F_NOCACHE makes reads go
// to the drive, controller or SMB server instead of the unified buffer
// cache that still holds the bytes just written.
func (d *Dir) OpenReadback(name string) (*os.File, string, error) {
	f, err := d.OpenRead(name)
	if err != nil {
		return nil, "", err
	}
	if _, err := unix.FcntlInt(f.Fd(), unix.F_NOCACHE, 1); err != nil {
		_ = f.Close()
		return nil, "", &os.PathError{Op: "fcntl F_NOCACHE", Path: f.Name(), Err: err}
	}
	return f, ReadbackMode, nil
}

// AdviseSource tells the kernel a source is read once, sequentially, so a
// 500 GB card offload does not evict the rest of the machine's cache.
func AdviseSource(f *os.File) {
	_, _ = unix.FcntlInt(f.Fd(), unix.F_NOCACHE, 1)
}
