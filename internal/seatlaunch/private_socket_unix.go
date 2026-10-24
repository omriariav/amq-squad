//go:build unix

package seatlaunch

import (
	"net"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/omriariav/amq-squad/v3/internal/fsanchor"
)

// PrivateSocket is a Unix socket bound the way the seat proof socket is
// (2b-2 milestone B R4: the scheduler's decision socket uses the same
// machinery): in the core-owned 0700 launch directory under the session
// root, reached through the anchored walk (a private temp directory when
// that path is too long for sun_path), 0600, a pre-bound or replaced name
// refused as socket_occupied. The kernel names each connecting process
// (PeerPID).
type PrivateSocket struct {
	Listener *net.UnixListener
	Path     string
	dir      *fsanchor.AnchoredDir
	name     string
	tmp      string
}

// ListenPrivate binds name in the session root's launch directory.
func ListenPrivate(root, name string) (*PrivateSocket, error) {
	d, tmp, err := proofDir(root, name)
	if err != nil {
		return nil, err
	}
	ln, _, err := bindPrivate(d, name)
	if err != nil {
		d.Close()
		if tmp != "" {
			os.RemoveAll(tmp)
		}
		return nil, err
	}
	return &PrivateSocket{Listener: ln, Path: filepath.Join(d.Path(), name), dir: d, name: name, tmp: tmp}, nil
}

// Close closes the listener and removes the socket through its directory.
func (p *PrivateSocket) Close() {
	p.Listener.Close()
	unix.Unlinkat(p.dir.Fd(), p.name, 0)
	p.dir.Close()
	if p.tmp != "" {
		os.RemoveAll(p.tmp)
	}
}

// PeerPID is the pid of the process on the other end of conn, from the
// kernel; known is false where the platform does not say.
func PeerPID(conn *net.UnixConn) (int, bool) { return peerPID(conn) }
