//go:build unix

package migrate

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/omriariav/amq-squad/v3/internal/fsanchor"
)

// dirIdentity is the device and inode of the directory a descriptor holds.
func dirIdentity(d *fsanchor.AnchoredDir, _ string) (uint64, uint64, error) {
	var st unix.Stat_t
	if err := unix.Fstat(d.Fd(), &st); err != nil {
		return 0, 0, err
	}
	return uint64(st.Dev), st.Ino, nil
}

// pathIdentity is the device and inode the path names now.
func pathIdentity(path string) (uint64, uint64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, 0, err
	}
	st := info.Sys().(*syscall.Stat_t)
	return uint64(st.Dev), st.Ino, nil
}

// unlinkAt removes the file name (not a directory) relative to d; it removes a
// symlink itself and never follows one.
func unlinkAt(d *fsanchor.AnchoredDir, name string) error {
	return unix.Unlinkat(d.Fd(), name, 0)
}

// linkAt hard-links old to new in d; it fails with EEXIST when new exists.
func linkAt(d *fsanchor.AnchoredDir, old, new string) error {
	return unix.Linkat(d.Fd(), old, d.Fd(), new, 0)
}
