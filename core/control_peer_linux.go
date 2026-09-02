package core

import (
	"net"

	"golang.org/x/sys/unix"
)

// controlPeerUID reads the connecting process's user id from the socket itself.
func controlPeerUID(connection *net.UnixConn) (int, error) {
	raw, err := connection.SyscallConn()
	if err != nil {
		return -1, err
	}
	var uid int
	var readErr error
	if err := raw.Control(func(fd uintptr) {
		credentials, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
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
