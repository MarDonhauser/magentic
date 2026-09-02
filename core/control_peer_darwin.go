package core

import (
	"net"

	"golang.org/x/sys/unix"
)

// controlPeerUID reads the connecting process's effective user id from the
// socket itself. There is no token: a process that can reach the socket already
// runs as this user, so the credentials are the whole authorization.
func controlPeerUID(connection *net.UnixConn) (int, error) {
	raw, err := connection.SyscallConn()
	if err != nil {
		return -1, err
	}
	var uid int
	var readErr error
	if err := raw.Control(func(fd uintptr) {
		credentials, err := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if err != nil {
			readErr = err
			return
		}
		uid = int(credentials.Uid)
	}); err != nil {
		return -1, err
	}
	return uid, readErr
}
