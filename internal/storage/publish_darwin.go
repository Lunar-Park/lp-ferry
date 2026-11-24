//go:build darwin

package storage

import "golang.org/x/sys/unix"

func renameNoReplace(fromfd int, from string, tofd int, to string) error {
	return unix.RenameatxNp(fromfd, from, tofd, to, unix.RENAME_EXCL)
}
