//go:build unix

package uninstall

import (
	"github.com/omriariav/amq-squad/v3/internal/fsanchor"
	"golang.org/x/sys/unix"
)

// sameEntry reports whether the entry name below parent (not followed if it
// is a link) is, by device and inode, the directory the descriptor holds.
func sameEntry(parent *fsanchor.AnchoredDir, name string, wd *fsanchor.AnchoredDir) (bool, error) {
	var a, b unix.Stat_t
	if err := unix.Fstatat(parent.Fd(), name, &a, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return false, err
	}
	if err := unix.Fstat(wd.Fd(), &b); err != nil {
		return false, err
	}
	return a.Dev == b.Dev && a.Ino == b.Ino, nil
}

// rmdirEntry removes the empty directory name below parent.
func rmdirEntry(parent *fsanchor.AnchoredDir, name string) error {
	return unix.Unlinkat(parent.Fd(), name, unix.AT_REMOVEDIR)
}
