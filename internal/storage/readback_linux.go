//go:build linux

package storage

import (
	"os"

	"golang.org/x/sys/unix"
)

// ReadbackMode names how readback bypassed the page cache on this OS.
const ReadbackMode = "fadvise_dontneed"

// OpenReadback opens name for physical readback. The caller has already
// fsynced the file, so its pages are clean and FADV_DONTNEED drops them;
// the reads that follow come from the device.
func (d *Dir) OpenReadback(name string) (*os.File, string, error) {
	f, err := d.OpenRead(name)
	if err != nil {
		return nil, "", err
	}
	if err := unix.Fdatasync(int(f.Fd())); err != nil {
		_ = f.Close()
		return nil, "", &os.PathError{Op: "fdatasync", Path: f.Name(), Err: err}
	}
	if err := unix.Fadvise(int(f.Fd()), 0, 0, unix.FADV_DONTNEED); err != nil {
		_ = f.Close()
		return nil, "", &os.PathError{Op: "fadvise DONTNEED", Path: f.Name(), Err: err}
	}
	return f, ReadbackMode, nil
}

// AdviseSource marks a source as read once, sequentially.
func AdviseSource(f *os.File) {
	_ = unix.Fadvise(int(f.Fd()), 0, 0, unix.FADV_SEQUENTIAL)
}
