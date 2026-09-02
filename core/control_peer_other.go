//go:build !darwin && !linux

package core

import (
	"errors"
	"net"
)

// controlPeerUID fails closed where the platform exposes no peer credentials:
// without them the socket has no authorization boundary, so no connection is
// admitted at all.
func controlPeerUID(*net.UnixConn) (int, error) {
	return -1, errors.New("auf dieser Plattform sind die Anmeldedaten der Gegenstelle nicht prüfbar")
}
