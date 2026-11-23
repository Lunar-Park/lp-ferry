package storage

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// Strategy is how a verified temp file becomes visible at its final name.
type Strategy string

// Publish strategies, strongest first. The choice is probed per destination
// (never guessed from the filesystem type) and recorded in the receipt.
const (
	// StrategyHardlink links the temp to the final name (atomic, EEXIST-safe),
	// then unlinks the temp.
	StrategyHardlink Strategy = "hardlink"
	// StrategyRenameNoReplace renames with RENAME_NOREPLACE / RENAME_EXCL.
	StrategyRenameNoReplace Strategy = "rename_noreplace"
	// StrategyReserveRename reserves the final name with O_EXCL before the
	// copy and renames the verified temp over that own zero-byte reservation.
	// Weaker (a placeholder is visible during the copy) and only chosen when
	// the probe proves neither stronger primitive works.
	StrategyReserveRename Strategy = "reserve_rename"
)

// Valid reports a known strategy.
func (s Strategy) Valid() bool {
	return s == StrategyHardlink || s == StrategyRenameNoReplace || s == StrategyReserveRename
}

// unsupported reports errnos that mean "this filesystem cannot do that
// primitive", as opposed to a transient fault. EPERM/EACCES appear on SMB
// for hard links; EINVAL is renameat2 on a filesystem without the flag.
func unsupported(err error) bool {
	for _, e := range []error{unix.EXDEV, unix.EMLINK, unix.ENOSYS, unix.EPERM, unix.EACCES, unix.ENOTSUP, unix.EOPNOTSUPP, unix.EINVAL} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}

func probeName(tag string) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return ".ferry-publish-probe." + hex.EncodeToString(b[:]) + "." + tag
}

// ProbeStrategy tries each primitive inside d and returns the strongest
// that works. It leaves nothing behind. A directory where nothing works
// returns ErrPublicationUnsupported; Ferry never falls back to a
// clobbering rename.
func ProbeStrategy(d *Dir) (Strategy, error) {
	src := probeName("src")
	f, err := d.CreateExcl(src)
	if err != nil {
		return "", fmt.Errorf("publish probe in %s: %w", d.DirPath(), err)
	}
	_ = f.Close()
	defer func() { _ = d.Remove(src) }()

	link := probeName("link")
	if err := unix.Linkat(d.fd, src, d.fd, link, 0); err == nil {
		_ = d.Remove(link)
		return StrategyHardlink, nil
	} else if !unsupported(err) {
		return "", &os.PathError{Op: "link probe", Path: d.Path(link), Err: err}
	}

	renamed := probeName("rn")
	if err := renameNoReplace(d.fd, src, d.fd, renamed); err == nil {
		if err := unix.Renameat(d.fd, renamed, d.fd, src); err != nil {
			_ = d.Remove(renamed)
		}
		return StrategyRenameNoReplace, nil
	} else if !unsupported(err) {
		return "", &os.PathError{Op: "rename probe", Path: d.Path(renamed), Err: err}
	}

	res := probeName("res")
	rf, err := d.CreateExcl(res)
	if err != nil {
		return "", fmt.Errorf("%w: %s cannot reserve names: %w", ErrPublicationUnsupported, d.DirPath(), err)
	}
	_ = rf.Close()
	if err := unix.Renameat(d.fd, src, d.fd, res); err != nil {
		_ = d.Remove(res)
		return "", fmt.Errorf("%w: %s cannot rename: %w", ErrPublicationUnsupported, d.DirPath(), err)
	}
	// src now lives at res; put it back so the deferred cleanup finds it.
	if err := unix.Renameat(d.fd, res, d.fd, src); err != nil {
		_ = d.Remove(res)
	}
	return StrategyReserveRename, nil
}

// Reservation is a zero-byte O_EXCL placeholder Ferry created at a final
// name for the reserve_rename strategy.
type Reservation struct {
	Name string
	Ino  uint64
}

// Reserve creates the placeholder. An existing name fails with
// ErrDestinationExists — the same condition the link strategy reports.
func Reserve(d *Dir, name string) (*Reservation, error) {
	f, err := d.CreateExcl(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	fi, err := Fstat(f)
	if err != nil {
		_ = d.Remove(name)
		return nil, err
	}
	if err := f.Sync(); err != nil {
		_ = d.Remove(name)
		return nil, err
	}
	return &Reservation{Name: name, Ino: fi.Ino}, nil
}

// OwnsReservation reports whether name is still exactly our zero-byte
// placeholder (same inode, empty, regular).
func OwnsReservation(d *Dir, name string, ino uint64) bool {
	fi, err := d.Lstat(name)
	return err == nil && fi.IsRegular() && fi.Size == 0 && fi.Ino == ino
}

// Publish makes part visible as final without ever replacing someone
// else's file, then fsyncs the directory.
func Publish(d *Dir, part, final string, s Strategy, res *Reservation) error {
	switch s {
	case StrategyHardlink:
		if err := unix.Linkat(d.fd, part, d.fd, final, 0); err != nil {
			if errors.Is(err, unix.EEXIST) {
				return fmt.Errorf("%w: %s", ErrDestinationExists, d.Path(final))
			}
			if unsupported(err) {
				return fmt.Errorf("%w: link %s: %w", ErrPublicationUnsupported, d.Path(final), err)
			}
			return &os.PathError{Op: "link", Path: d.Path(final), Err: err}
		}
		if err := d.Remove(part); err != nil {
			// Published; the leftover temp carries our suffix and our
			// recorded name, so it is reconciled later, never adopted.
			_ = d.Sync()
			return nil
		}
	case StrategyRenameNoReplace:
		if err := renameNoReplace(d.fd, part, d.fd, final); err != nil {
			if errors.Is(err, unix.EEXIST) {
				return fmt.Errorf("%w: %s", ErrDestinationExists, d.Path(final))
			}
			if unsupported(err) {
				return fmt.Errorf("%w: rename_noreplace %s: %w", ErrPublicationUnsupported, d.Path(final), err)
			}
			return &os.PathError{Op: "rename", Path: d.Path(final), Err: err}
		}
	case StrategyReserveRename:
		if res == nil || res.Name != final {
			return fmt.Errorf("%w: reserve_rename without a reservation for %s", ErrPublicationUnsupported, d.Path(final))
		}
		if !OwnsReservation(d, final, res.Ino) {
			return fmt.Errorf("%w: reservation at %s was replaced by someone else", ErrDestinationExists, d.Path(final))
		}
		if err := unix.Renameat(d.fd, part, d.fd, final); err != nil {
			return &os.PathError{Op: "rename", Path: d.Path(final), Err: err}
		}
	default:
		return fmt.Errorf("%w: unknown strategy %q", ErrPublicationUnsupported, s)
	}
	return d.Sync()
}
