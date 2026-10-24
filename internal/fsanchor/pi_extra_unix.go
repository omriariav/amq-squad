//go:build unix

package fsanchor

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// The descriptor-relative writes 4-1's cold install of the pi runtime stages
// through (review 2, item 3): every directory is made and every file created
// relative to an open parent descriptor with O_NOFOLLOW, and the finished
// tree is renamed into place relative to the same descriptor, so a symlink
// swapped in on the path cannot redirect a write. The names carry a Stage
// prefix so they never collide with 4-3's fleet additions.

// StageMkdir creates name (mode) relative to d and opens it.
func (d *AnchoredDir) StageMkdir(name string, mode uint32) (*AnchoredDir, error) {
	if !CleanName(name) {
		return nil, errors.New("not a clean name: " + name)
	}
	if err := unix.Mkdirat(d.fd, name, mode); err != nil && err != unix.EEXIST {
		return nil, err
	}
	return d.Sub(name)
}

// StageCreate creates a new regular file relative to d (O_EXCL, O_NOFOLLOW).
func (d *AnchoredDir) StageCreate(name string, mode uint32) (*os.File, error) {
	if !CleanName(name) {
		return nil, errors.New("not a clean name: " + name)
	}
	fd, err := unix.Openat(d.fd, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, mode)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), d.path+"/"+name), nil
}

// StageLock opens (creating) a lock file relative to d without following a
// symlink.
func (d *AnchoredDir) StageLock(name string) (*os.File, error) {
	if !CleanName(name) {
		return nil, errors.New("not a clean name: " + name)
	}
	// Concurrent creators of one name can see ENOENT from openat(O_CREAT) on
	// Darwin (the race 4-3 met on its fleet lock); retry a few times.
	var fd int
	var err error
	for i := 0; i < 8; i++ {
		fd, err = unix.Openat(d.fd, name, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
		if err != unix.ENOENT {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("lock %s/%s: %w", d.path, name, err)
	}
	return os.NewFile(uintptr(fd), d.path+"/"+name), nil
}

// StageChmod sets the directory's own mode through its descriptor.
func (d *AnchoredDir) StageChmod(mode uint32) error { return unix.Fchmod(d.fd, mode) }

// StageRename renames from to to, both relative to d.
func (d *AnchoredDir) StageRename(from, to string) error {
	if !CleanName(from) || !CleanName(to) {
		return errors.New("not a clean name")
	}
	return unix.Renameat(d.fd, from, d.fd, to)
}

// StageRemoveAll removes name below d and everything in it, through
// descriptors, never following a symlink (a symlink is unlinked, not
// followed).
func (d *AnchoredDir) StageRemoveAll(name string) error {
	if !CleanName(name) {
		return errors.New("not a clean name: " + name)
	}
	if err := unix.Unlinkat(d.fd, name, 0); err == nil || err == unix.ENOENT {
		return nil
	}
	sub, err := d.Sub(name)
	if err != nil {
		return err
	}
	_ = unix.Fchmod(sub.fd, 0o700)
	names, err := sub.Names()
	if err == nil {
		for _, n := range names {
			if err = sub.StageRemoveAll(n); err != nil {
				break
			}
		}
	}
	sub.Close()
	if err != nil {
		return err
	}
	return unix.Unlinkat(d.fd, name, unix.AT_REMOVEDIR)
}
