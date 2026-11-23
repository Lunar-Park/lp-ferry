//go:build darwin

package ipc

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// samePeerUser refuses a connection from another user, as a second line
// behind the socket's 0600 mode.
func samePeerUser(c net.Conn) error {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return nil
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return err
	}
	var cred *unix.Xucred
	var gerr error
	if err := raw.Control(func(fd uintptr) { cred, gerr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED) }); err != nil {
		return err
	}
	if gerr != nil {
		return gerr
	}
	if int(cred.Uid) != os.Getuid() {
		return fmt.Errorf("peer uid %d is not this user", cred.Uid)
	}
	return nil
}
