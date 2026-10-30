//go:build unix

package seatlaunch

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

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

// socketOwnerUID is the uid a dialed socket must belong to (a test seam: a
// test cannot plant a foreign-uid socket without root).
var socketOwnerUID = os.Getuid

// privateDialTimeout bounds DialPrivate's connect.
const privateDialTimeout = 5 * time.Second

// DialPrivate connects to a socket ListenPrivate bound under root and
// recorded as recordedPath (5-ci-D ruling D1). The recorded path is the
// walked path: under root's launch directory, which on Linux can exceed
// sun_path (108 bytes) because the listener binds through
// /proc/self/fd/N/name, or darwin's private temp directory when that path
// reached 100 bytes. The dial is bounded the same way the bind is: the
// directory is opened through the anchored walk and checked private (0700,
// own), the name is stat'ed through the descriptor without following a
// link and must be a socket owned by this user, and the connect goes
// through /proc/self/fd/<dirfd>/<name> on Linux and the walked path
// elsewhere (the seat proof dial, seatexec.DialProof, does the same).
func DialPrivate(root, recordedPath, name string) (*net.UnixConn, error) {
	if !fsanchor.CleanName(name) || filepath.Base(recordedPath) != name {
		return nil, fmt.Errorf("the recorded socket %s is not named %s", recordedPath, name)
	}
	dir := filepath.Dir(recordedPath)
	var (
		d   *fsanchor.AnchoredDir
		err error
	)
	launchRel := []string{"extensions", "amq-squad", "launch"}
	// The listener's root reached the bind through the anchored walk, which
	// refuses an unclean or symlinked anchor, so the recorded directory is
	// canonical. The dialer's root comes from another resolver (task
	// widen's --root, AM_ROOT or the AMQ context) and is canonicalised the
	// same way before the compare: absolute, clean, symlinks resolved.
	canon := root
	if abs, aerr := filepath.Abs(root); aerr == nil {
		canon = abs
		if real, rerr := filepath.EvalSymlinks(abs); rerr == nil {
			canon = real
		}
	}
	switch {
	case dir == filepath.Join(append([]string{canon}, launchRel...)...):
		d, err = fsanchor.OpenAnchored(canon, launchRel, false)
	case runtime.GOOS != "linux" && filepath.IsAbs(dir) && strings.HasPrefix(filepath.Base(dir), "amqsq-"):
		// darwin's temp fallback (proofDir): a private directory of its own
		d, err = fsanchor.OpenAnchored(dir, nil, false)
	default:
		return nil, fmt.Errorf("the recorded socket %s is neither in %s's launch directory nor in a private socket directory", recordedPath, root)
	}
	if err != nil {
		return nil, err
	}
	defer d.Close()
	var st unix.Stat_t
	if err := unix.Fstat(d.Fd(), &st); err != nil || int(st.Uid) != os.Getuid() || st.Mode&0o077 != 0 {
		return nil, fmt.Errorf("%s is not a private (0700, own) directory", d.Path())
	}
	if err := unix.Fstatat(d.Fd(), name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return nil, fmt.Errorf("stat %s: %w", recordedPath, err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFSOCK || int(st.Uid) != socketOwnerUID() {
		return nil, fmt.Errorf("%s is not a socket owned by this user", recordedPath)
	}
	c, err := net.DialTimeout("unix", socketAddr(d, name), privateDialTimeout)
	if err != nil {
		return nil, err
	}
	return c.(*net.UnixConn), nil
}
