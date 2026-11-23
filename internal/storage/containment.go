// Package storage is Ferry's filesystem boundary: containment, exclusive
// publication and physical readback. It has no database or network
// dependencies.
//
// Every directory below a root is reached by openat(O_NOFOLLOW) one
// component at a time, so a symlink anywhere under the root — leaf or
// ancestor — is refused rather than followed, and a path checked here is
// the same path written to (no check-then-use window on the name).
package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// TempSuffix marks job-owned temporary siblings. Ferry only ever removes
// files carrying this suffix whose exact names it recorded itself.
const TempSuffix = ".ferry-part"

// Errors callers branch on.
var (
	ErrUnsafePath             = errors.New("unsafe path")
	ErrDestinationExists      = errors.New("destination already exists")
	ErrPublicationUnsupported = errors.New("filesystem cannot publish exclusively")
	ErrBindingChanged         = errors.New("storage under the destination root changed")
	ErrSourceChanged          = errors.New("source changed during copy")
	ErrChecksumMismatch       = errors.New("checksum mismatch")
	ErrNotRegular             = errors.New("not a regular file")
)

// ValidateRel checks a slash-separated relative path before any I/O and
// returns its components. It refuses absolute and drive paths, "..",
// NUL bytes, backslashes and names using Ferry's temp suffix.
func ValidateRel(rel string) ([]string, error) {
	if rel == "" {
		return nil, fmt.Errorf("%w: empty relative path", ErrUnsafePath)
	}
	if strings.ContainsRune(rel, 0) {
		return nil, fmt.Errorf("%w: path contains NUL: %q", ErrUnsafePath, rel)
	}
	if strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`) {
		return nil, fmt.Errorf("%w: must be relative: %q", ErrUnsafePath, rel)
	}
	if len(rel) >= 2 && rel[1] == ':' && ((rel[0] >= 'a' && rel[0] <= 'z') || (rel[0] >= 'A' && rel[0] <= 'Z')) {
		return nil, fmt.Errorf("%w: must not be a drive path: %q", ErrUnsafePath, rel)
	}
	if strings.ContainsRune(rel, '\\') {
		return nil, fmt.Errorf("%w: backslash in path: %q", ErrUnsafePath, rel)
	}
	var parts []string
	for _, p := range strings.Split(rel, "/") {
		switch p {
		case "", ".":
			continue
		case "..":
			return nil, fmt.Errorf("%w: must not contain '..': %q", ErrUnsafePath, rel)
		}
		if strings.HasSuffix(p, TempSuffix) {
			return nil, fmt.Errorf("%w: %q uses Ferry's reserved temp suffix", ErrUnsafePath, rel)
		}
		parts = append(parts, p)
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("%w: path names no file: %q", ErrUnsafePath, rel)
	}
	return parts, nil
}

// Root is a resolved destination or source root plus the identity of the
// directory it resolved to. Dev+Ino is the binding: if a share unmounts and
// leaves its mount directory behind, the path still exists but names a
// different directory (different inode, usually a different device).
type Root struct {
	Path string // as configured
	Real string // symlinks in the root path itself resolved once
	Dev  uint64
	Ino  uint64
}

// OpenRoot resolves path and records its binding.
func OpenRoot(path string) (*Root, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("%w: root must be absolute: %q", ErrUnsafePath, path)
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	var st unix.Stat_t
	if err := unix.Lstat(real, &st); err != nil {
		return nil, &os.PathError{Op: "lstat", Path: real, Err: err}
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return nil, fmt.Errorf("%w: root is not a directory: %s", ErrUnsafePath, path)
	}
	return &Root{Path: path, Real: real, Dev: devOf(&st), Ino: st.Ino}, nil
}

// Check re-verifies the binding by path. It is the cheap check run right
// before every publication.
func (r *Root) Check() error {
	var st unix.Stat_t
	if err := unix.Lstat(r.Real, &st); err != nil {
		return fmt.Errorf("%w: %s is no longer reachable: %w", ErrBindingChanged, r.Path, err)
	}
	if devOf(&st) != r.Dev || st.Ino != r.Ino {
		return fmt.Errorf("%w: %s now names device %d inode %d (bound to device %d inode %d); "+
			"an unmounted share's leftover mount directory is never written into",
			ErrBindingChanged, r.Path, st.Dev, st.Ino, r.Dev, r.Ino)
	}
	return nil
}

// Dir is an open directory reached from a Root without following symlinks.
type Dir struct {
	fd   int
	path string // display path
	root *Root
}

const dirFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC

// OpenDir walks relDir under root one component at a time. With create,
// missing components are made (0o755). Any component that is a symlink or
// not a directory fails with ErrUnsafePath and nothing is created beyond it.
func OpenDir(root *Root, relDir []string, create bool) (*Dir, error) {
	fd, err := unix.Open(root.Real, dirFlags, 0)
	if err != nil {
		return nil, fmt.Errorf("%w: open root %s: %w", ErrBindingChanged, root.Path, err)
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	if devOf(&st) != root.Dev || st.Ino != root.Ino {
		_ = unix.Close(fd)
		return nil, root.Check()
	}
	cur := root.Real
	for _, name := range relDir {
		next, err := openChildDir(fd, cur, name, create)
		_ = unix.Close(fd)
		if err != nil {
			return nil, err
		}
		fd = next
		cur = filepath.Join(cur, name)
	}
	return &Dir{fd: fd, path: cur, root: root}, nil
}

func openChildDir(parent int, parentPath, name string, create bool) (int, error) {
	full := filepath.Join(parentPath, name)
	for attempt := 0; attempt < 3; attempt++ {
		fd, err := unix.Openat(parent, name, dirFlags, 0)
		if err == nil {
			return fd, nil
		}
		switch {
		case errors.Is(err, unix.ENOENT) && create:
			if mkErr := unix.Mkdirat(parent, name, 0o755); mkErr != nil && !errors.Is(mkErr, unix.EEXIST) {
				return -1, &os.PathError{Op: "mkdir", Path: full, Err: mkErr}
			}
			continue
		case errors.Is(err, unix.ELOOP), errors.Is(err, unix.ENOTDIR), errors.Is(err, unix.EMLINK):
			return -1, describeNonDir(parent, name, full)
		default:
			return -1, &os.PathError{Op: "open", Path: full, Err: err}
		}
	}
	return -1, fmt.Errorf("%w: %s kept changing while being created", ErrUnsafePath, full)
}

func describeNonDir(parent int, name, full string) error {
	var st unix.Stat_t
	if err := unix.Fstatat(parent, name, &st, unix.AT_SYMLINK_NOFOLLOW); err == nil && st.Mode&unix.S_IFMT == unix.S_IFLNK {
		return fmt.Errorf("%w: %s is a symlink; Ferry never follows links under a root", ErrUnsafePath, full)
	}
	return fmt.Errorf("%w: %s is not a directory", ErrUnsafePath, full)
}

// Path returns the display path of name inside d.
func (d *Dir) Path(name string) string { return filepath.Join(d.path, name) }

// DirPath is d's own display path.
func (d *Dir) DirPath() string { return d.path }

// Root is the root d was opened from.
func (d *Dir) Root() *Root { return d.root }

// Close releases the directory descriptor.
func (d *Dir) Close() error { return unix.Close(d.fd) }

// Sync fsyncs the directory so a link/rename/unlink inside it is durable.
func (d *Dir) Sync() error { return unix.Fsync(d.fd) }

// CheckBinding verifies both the open descriptor and the root path still
// name the bound directory tree.
func (d *Dir) CheckBinding() error {
	if err := d.root.Check(); err != nil {
		return err
	}
	var st unix.Stat_t
	if err := unix.Fstat(d.fd, &st); err != nil {
		return fmt.Errorf("%w: %w", ErrBindingChanged, err)
	}
	if devOf(&st) != d.root.Dev {
		return fmt.Errorf("%w: %s moved to device %d", ErrBindingChanged, d.path, st.Dev)
	}
	return nil
}

// FileInfo is the subset of lstat Ferry records.
type FileInfo struct {
	Mode    uint32
	Size    int64
	MtimeNS int64
	Dev     uint64
	Ino     uint64
}

// IsRegular reports a regular file.
func (fi FileInfo) IsRegular() bool { return fi.Mode&unix.S_IFMT == unix.S_IFREG }

// IsSymlink reports a symbolic link.
func (fi FileInfo) IsSymlink() bool { return fi.Mode&unix.S_IFMT == unix.S_IFLNK }

// IsDir reports a directory.
func (fi FileInfo) IsDir() bool { return fi.Mode&unix.S_IFMT == unix.S_IFDIR }

func infoOf(st *unix.Stat_t) FileInfo {
	return FileInfo{
		Mode:    modeOf(st),
		Size:    st.Size,
		MtimeNS: unix.TimespecToNsec(st.Mtim),
		Dev:     devOf(st),
		Ino:     st.Ino,
	}
}

// Lstat stats name inside d without following a symlink.
func (d *Dir) Lstat(name string) (FileInfo, error) {
	var st unix.Stat_t
	if err := unix.Fstatat(d.fd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return FileInfo{}, &os.PathError{Op: "lstat", Path: d.Path(name), Err: err}
	}
	return infoOf(&st), nil
}

// Fstat stats an open file.
func Fstat(f *os.File) (FileInfo, error) {
	var st unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &st); err != nil {
		return FileInfo{}, &os.PathError{Op: "fstat", Path: f.Name(), Err: err}
	}
	return infoOf(&st), nil
}

// CheckFree reports why name cannot be published to: ErrDestinationExists
// for anything present, ErrUnsafePath if that something is a symlink.
func (d *Dir) CheckFree(name string) error {
	fi, err := d.Lstat(name)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil
		}
		return err
	}
	if fi.IsSymlink() {
		return fmt.Errorf("%w: %s is a symlink", ErrUnsafePath, d.Path(name))
	}
	return fmt.Errorf("%w: %s", ErrDestinationExists, d.Path(name))
}

// CreateExcl creates name with O_EXCL|O_NOFOLLOW. An existing file is
// never opened, truncated or adopted.
func (d *Dir) CreateExcl(name string) (*os.File, error) {
	fd, err := unix.Openat(d.fd, name, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		if errors.Is(err, unix.EEXIST) {
			return nil, fmt.Errorf("%w: %s", ErrDestinationExists, d.Path(name))
		}
		return nil, &os.PathError{Op: "create", Path: d.Path(name), Err: err}
	}
	return os.NewFile(uintptr(fd), d.Path(name)), nil
}

// OpenRead opens a regular file for reading without following a symlink.
func (d *Dir) OpenRead(name string) (*os.File, error) {
	fd, err := unix.Openat(d.fd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, fmt.Errorf("%w: %s is a symlink", ErrUnsafePath, d.Path(name))
		}
		return nil, &os.PathError{Op: "open", Path: d.Path(name), Err: err}
	}
	f := os.NewFile(uintptr(fd), d.Path(name))
	fi, err := Fstat(f)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !fi.IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("%w: %s", ErrNotRegular, d.Path(name))
	}
	return f, nil
}

// Remove unlinks name inside d. Callers only ever pass names they own.
func (d *Dir) Remove(name string) error {
	if err := unix.Unlinkat(d.fd, name, 0); err != nil {
		return &os.PathError{Op: "unlink", Path: d.Path(name), Err: err}
	}
	return nil
}

// Chtimes sets name's atime/mtime (nanoseconds) without following links.
func (d *Dir) Chtimes(name string, atimeNS, mtimeNS int64) error {
	ts := []unix.Timespec{unix.NsecToTimespec(atimeNS), unix.NsecToTimespec(mtimeNS)}
	if err := unix.UtimesNanoAt(d.fd, name, ts, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return &os.PathError{Op: "utimes", Path: d.Path(name), Err: err}
	}
	return nil
}

// OpenFileIn validates rel against root and returns the parent Dir and leaf
// name. With create, missing parent directories are made.
func OpenFileIn(root *Root, rel string, create bool) (*Dir, string, error) {
	parts, err := ValidateRel(rel)
	if err != nil {
		return nil, "", err
	}
	d, err := OpenDir(root, parts[:len(parts)-1], create)
	if err != nil {
		return nil, "", err
	}
	return d, parts[len(parts)-1], nil
}

// devOf and modeOf widen stat fields whose types differ by OS (Dev is
// int32 and Mode uint16 on darwin, uint64/uint32 on linux).
func devOf(st *unix.Stat_t) uint64 { return uint64(st.Dev) } //nolint:unconvert // int32 on darwin

func modeOf(st *unix.Stat_t) uint32 { return uint32(st.Mode) } //nolint:unconvert // uint16 on darwin
