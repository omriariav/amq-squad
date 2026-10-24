//go:build unix

package registry

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// anchoredDir is a directory opened from a trusted anchor (the home
// directory, opened as given) one component at a time with O_NOFOLLOW, so
// no symlink below the anchor is ever followed and nothing is reached by
// pathname after the open: entries are listed, opened and checked through
// the descriptor.
type anchoredDir struct {
	fd   int
	path string // for messages only
}

// openAnchored opens base, then each component of rel with
// O_DIRECTORY|O_NOFOLLOW. With mkdir, a missing component is created (0700)
// relative to its parent descriptor.
func openAnchored(base string, rel []string, mkdir bool) (*anchoredDir, error) {
	fd, err := unix.Open(base, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", base, err)
	}
	d := &anchoredDir{fd: fd, path: base}
	for _, part := range rel {
		if !cleanName(part) {
			d.Close()
			return nil, fmt.Errorf("path component %q is not clean", part)
		}
		next, err := d.sub(part)
		if err != nil && mkdir && err == unix.ENOENT {
			if err = unix.Mkdirat(d.fd, part, 0o700); err == nil || err == unix.EEXIST {
				next, err = d.sub(part)
			}
		}
		d.Close()
		if err != nil {
			return nil, fmt.Errorf("%s/%s: %w (a symlinked or missing component)", d.path, part, err)
		}
		d = next
	}
	return d, nil
}

func cleanName(s string) bool { return s != "" && s != "." && s != ".." && !containsSlash(s) }

func containsSlash(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			return true
		}
	}
	return false
}

func (d *anchoredDir) Close() error { return unix.Close(d.fd) }

// sub opens a child directory without following a symlink.
func (d *anchoredDir) sub(name string) (*anchoredDir, error) {
	fd, err := unix.Openat(d.fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return &anchoredDir{fd: fd, path: d.path + "/" + name}, nil
}

// entryKind is what an opened directory entry is.
type entryKind int

const (
	kindOther entryKind = iota
	kindDir
	kindRegular
)

// openEntry opens a child without following a symlink and without blocking
// (a FIFO opens and is then refused by type), and reports its type, size and
// permission bits from the descriptor. A symlink fails to open (ELOOP).
func (d *anchoredDir) openEntry(name string) (*os.File, entryKind, int64, uint32, error) {
	fd, err := unix.Openat(d.fd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, kindOther, 0, 0, fmt.Errorf("%s/%s: %w (a symlink or unreadable)", d.path, name, err)
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		unix.Close(fd)
		return nil, kindOther, 0, 0, err
	}
	kind := kindOther
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFDIR:
		kind = kindDir
	case unix.S_IFREG:
		kind = kindRegular
		// Regular files ignore O_NONBLOCK; clear it anyway before reading.
		if flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0); err == nil {
			unix.FcntlInt(uintptr(fd), unix.F_SETFL, flags&^unix.O_NONBLOCK)
		}
	}
	return os.NewFile(uintptr(fd), d.path+"/"+name), kind, st.Size, uint32(st.Mode & 0o777), nil
}

// asDir turns an opened directory entry into an anchoredDir.
func asDir(f *os.File) (*anchoredDir, error) {
	fd, err := unix.Dup(int(f.Fd()))
	f.Close()
	if err != nil {
		return nil, err
	}
	return &anchoredDir{fd: fd, path: f.Name()}, nil
}

// names lists the directory through its descriptor.
func (d *anchoredDir) names() ([]string, error) {
	fd, err := unix.Dup(d.fd)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), d.path)
	defer f.Close()
	if _, err := f.Seek(0, 0); err != nil {
		return nil, err
	}
	return f.Readdirnames(-1)
}

// writeAtomic creates a temp file with O_EXCL relative to the descriptor,
// writes and fsyncs it, renames it over name (a symlink at name is
// replaced, never followed), and fsyncs the directory.
func (d *anchoredDir) writeAtomic(name string, data []byte) error {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return err
	}
	tmp := "." + name + "-" + hex.EncodeToString(b[:])
	fd, err := unix.Openat(d.fd, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), tmp)
	_, werr := f.Write(data)
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = unix.Renameat(d.fd, tmp, d.fd, name)
	}
	if werr != nil {
		unix.Unlinkat(d.fd, tmp, 0)
		return werr
	}
	return unix.Fsync(d.fd)
}
