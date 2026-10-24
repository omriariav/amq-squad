//go:build unix

package fsanchor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/atomicfile"
	"golang.org/x/sys/unix"
)

// AnchoredDir is a directory opened from a trusted anchor (the home
// directory, opened as given) one component at a time with O_NOFOLLOW, so
// no symlink below the anchor is ever followed and nothing is reached by
// pathname after the open: entries are listed, opened and checked through
// the descriptor.
type AnchoredDir struct {
	fd   int
	path string // for messages only
}

// OpenAnchored opens base, then each component of rel with
// O_DIRECTORY|O_NOFOLLOW. With mkdir, a missing component is created (0700)
// relative to its parent descriptor. base itself is opened from / one
// component at a time with O_NOFOLLOW, so a symlinked anchor or a symlinked
// ancestor of it is refused, not followed; base must be absolute and clean.
func OpenAnchored(base string, rel []string, mkdir bool) (*AnchoredDir, error) {
	if !filepath.IsAbs(base) || filepath.Clean(base) != base {
		return nil, fmt.Errorf("anchor %q is not an absolute clean path", base)
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open /: %w", err)
	}
	d := &AnchoredDir{fd: fd, path: ""}
	for _, part := range strings.Split(strings.TrimPrefix(base, "/"), "/") {
		if part == "" {
			continue
		}
		next, err := d.Sub(part)
		d.Close()
		if err != nil {
			return nil, fmt.Errorf("%s/%s: %w (a symlinked or missing anchor component)", d.path, part, err)
		}
		d = next
	}
	if d.path == "" {
		d.path = "/"
	}
	for _, part := range rel {
		if !CleanName(part) {
			d.Close()
			return nil, fmt.Errorf("path component %q is not clean", part)
		}
		next, err := d.Sub(part)
		if err != nil && mkdir && err == unix.ENOENT {
			if err = unix.Mkdirat(d.fd, part, 0o700); err == nil || err == unix.EEXIST {
				next, err = d.Sub(part)
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

func CleanName(s string) bool { return s != "" && s != "." && s != ".." && !ContainsSlash(s) }

func ContainsSlash(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			return true
		}
	}
	return false
}

func (d *AnchoredDir) Close() error { return unix.Close(d.fd) }

// Fd is the directory's descriptor, for *at calls (2b-2: the proof socket
// is checked and bound relative to it).
func (d *AnchoredDir) Fd() int { return d.fd }

// Sub opens a child directory without following a symlink.
func (d *AnchoredDir) Sub(name string) (*AnchoredDir, error) {
	fd, err := unix.Openat(d.fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return &AnchoredDir{fd: fd, path: d.path + "/" + name}, nil
}

// EntryKind is what an opened directory entry is.
type EntryKind int

const (
	KindOther EntryKind = iota
	KindDir
	KindRegular
)

// OpenEntry opens a child without following a symlink and without blocking
// (a FIFO opens and is then refused by type), and reports its type, size and
// permission bits from the descriptor. A symlink fails to open (ELOOP).
func (d *AnchoredDir) OpenEntry(name string) (*os.File, EntryKind, int64, uint32, error) {
	fd, err := unix.Openat(d.fd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, KindOther, 0, 0, fmt.Errorf("%s/%s: %w (a symlink or unreadable)", d.path, name, err)
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		unix.Close(fd)
		return nil, KindOther, 0, 0, err
	}
	kind := KindOther
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFDIR:
		kind = KindDir
	case unix.S_IFREG:
		kind = KindRegular
		// Regular files ignore O_NONBLOCK; clear it anyway before reading.
		if flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0); err == nil {
			unix.FcntlInt(uintptr(fd), unix.F_SETFL, flags&^unix.O_NONBLOCK)
		}
	}
	return os.NewFile(uintptr(fd), d.path+"/"+name), kind, st.Size, uint32(st.Mode & 0o777), nil
}

// AsDir turns an opened directory entry into an AnchoredDir.
func AsDir(f *os.File) (*AnchoredDir, error) {
	fd, err := unix.Dup(int(f.Fd()))
	f.Close()
	if err != nil {
		return nil, err
	}
	return &AnchoredDir{fd: fd, path: f.Name()}, nil
}

// Names lists the directory through its descriptor.
func (d *AnchoredDir) Names() ([]string, error) {
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

// WriteAtomic writes name through internal/atomicfile's WriteAt relative to
// the descriptor: a temp file with O_EXCL|O_NOFOLLOW, written, chmodded 0600
// and fsynced, renamed over name (a symlink at name is replaced, never
// followed), and the directory fsynced (4-1 rebase after 2b-5: every
// descriptor-anchored write ends up on atomicfile).
func (d *AnchoredDir) WriteAtomic(name string, data []byte) error {
	return atomicfile.WriteAt(d.fd, d.path, name, data, 0o600)
}
