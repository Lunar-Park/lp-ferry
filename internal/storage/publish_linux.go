//go:build linux

package storage

import "golang.org/x/sys/unix"

func renameNoReplace(fromfd int, from string, tofd int, to string) error {
	return unix.Renameat2(fromfd, from, tofd, to, unix.RENAME_NOREPLACE)
}
