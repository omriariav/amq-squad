//go:build unix

package fsanchor

// 4-3 additions beyond the lifted set (fleet registry): kept in their own
// file so the merge with 4-1's lift is additive. fleet_extra_other.go is the
// non-unix twin.

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// OpenLock opens, creating it 0600 when absent, the regular file name
// relative to the descriptor for an advisory lock, without following a
// symlink and without replacing an existing file (so a holder's lock on it
// stays valid).
func (d *AnchoredDir) OpenLock(name string) (*os.File, error) {
	if !CleanName(name) {
		return nil, fmt.Errorf("file name %q is not clean", name)
	}
	// Darwin can fail a create that races another create of the same name
	// with ENOENT (measured: 8 concurrent first opens in a fresh directory);
	// the file then exists, so a retry opens it.
	var fd int
	var err error
	for try := 0; try < 8; try++ {
		if fd, err = unix.Openat(d.fd, name, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600); err != unix.ENOENT {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("%s/%s: %w (a symlink or unwritable)", d.path, name, err)
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFREG {
		unix.Close(fd)
		return nil, fmt.Errorf("%s/%s is not a regular file", d.path, name)
	}
	return os.NewFile(uintptr(fd), d.path+"/"+name), nil
}

// Path is the directory's path, for messages.
func (d *AnchoredDir) Path() string { return d.path }
