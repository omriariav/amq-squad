//go:build !unix

package seatlaunch

import (
	"errors"
	"net"
)

// PrivateSocket needs a Unix socket; seats and the scheduler run on unix
// hosts only (R45).
type PrivateSocket struct {
	Listener *net.UnixListener
	Path     string
}

func ListenPrivate(root, name string) (*PrivateSocket, error) {
	return nil, errors.New("the decision socket needs a unix host")
}

func (p *PrivateSocket) Close() {}

func PeerPID(conn *net.UnixConn) (int, bool) { return 0, false }
