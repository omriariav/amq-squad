//go:build unix

package seatexec

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// anchor is an open directory reached from "/" one component at a time
// with O_NOFOLLOW, so no symlink in the path is ever followed (plan 4.3:
// "follows no symlink in the initial path"). Directories at and below the
// AMQ root must be owned by the seat's user and not group- or
// world-writable; a directory that seat-exec creates is made 0700.
type anchor struct {
	fd   int
	path string
	uid  int
}

const dirFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC

// openRoot walks an absolute, clean path from "/" without following any
// symlink. Ancestors of the root are only checked for symlinks; the root
// itself gets the owner and mode check.
func openRoot(path string, uid int) (*anchor, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, refusef("root_invalid", "AMQ root %q is not a clean absolute path", path)
	}
	fd, err := unix.Open("/", dirFlags, 0)
	if err != nil {
		return nil, refusef("root_unreadable", "open /: %v", err)
	}
	a := &anchor{fd: fd, path: "/", uid: uid}
	for _, name := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if name == "" {
			continue
		}
		next, err := a.open(name, false)
		a.close()
		if err != nil {
			return nil, err
		}
		a = next
	}
	if err := a.check(); err != nil {
		a.close()
		return nil, err
	}
	return a, nil
}

func (a *anchor) close() { unix.Close(a.fd) }

func (a *anchor) open(name string, checked bool) (*anchor, error) {
	if name == "" || name == "." || name == ".." || strings.Contains(name, "/") {
		return nil, refusef("path_invalid", "bad path component %q under %s", name, a.path)
	}
	fd, err := unix.Openat(a.fd, name, dirFlags, 0)
	full := filepath.Join(a.path, name)
	if err == unix.ELOOP || err == unix.ENOTDIR {
		return nil, refusef("symlink_in_path", "%s is a symlink or not a directory", full)
	}
	if err != nil {
		return nil, &notExist{path: full, err: err}
	}
	next := &anchor{fd: fd, path: full, uid: a.uid}
	if checked {
		if err := next.check(); err != nil {
			next.close()
			return nil, err
		}
	}
	return next, nil
}

// check refuses a directory with the wrong owner or group/world write bits.
func (a *anchor) check() error {
	var st unix.Stat_t
	if err := unix.Fstat(a.fd, &st); err != nil {
		return refusef("stat_failed", "stat %s: %v", a.path, err)
	}
	if int(st.Uid) != a.uid {
		return refusef("wrong_owner", "%s is owned by uid %d, not %d", a.path, st.Uid, a.uid)
	}
	if st.Mode&0o022 != 0 {
		return refusef("dir_writable_by_others", "%s has mode %o (group or world writable)", a.path, st.Mode&0o777)
	}
	return nil
}

// walk opens a relative path below a, checking every directory; with create
// set, a missing directory is made 0700.
func (a *anchor) walk(rel string, create bool) (*anchor, error) {
	cur := a
	for _, name := range strings.Split(rel, "/") {
		next, err := cur.open(name, true)
		if _, missing := err.(*notExist); missing && create {
			if mkErr := unix.Mkdirat(cur.fd, name, 0o700); mkErr != nil && mkErr != unix.EEXIST {
				err = refusef("mkdir_failed", "mkdir %s: %v", filepath.Join(cur.path, name), mkErr)
			} else {
				next, err = cur.open(name, true)
			}
		}
		if cur != a {
			cur.close()
		}
		if err != nil {
			return nil, err
		}
		cur = next
	}
	return cur, nil
}

// readFile reads a regular file in a without following a symlink.
func (a *anchor) readFile(name string, limit int64) ([]byte, error) {
	fd, err := unix.Openat(a.fd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		if err == unix.ELOOP {
			return nil, refusef("symlink_in_path", "%s is a symlink", filepath.Join(a.path, name))
		}
		return nil, &notExist{path: filepath.Join(a.path, name), err: err}
	}
	f := os.NewFile(uintptr(fd), filepath.Join(a.path, name))
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(b)) > limit {
		err = fmt.Errorf("%s exceeds %d bytes", f.Name(), limit)
	}
	return b, err
}

// writeFile publishes data as name in a: an O_EXCL|O_NOFOLLOW temp file,
// 0600, then renameat over the old file.
func (a *anchor) writeFile(name string, data []byte) error {
	tmp := fmt.Sprintf(".%s.%d.tmp", name, os.Getpid())
	fd, err := unix.Openat(a.fd, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return refusef("write_failed", "create %s: %v", filepath.Join(a.path, tmp), err)
	}
	f := os.NewFile(uintptr(fd), tmp)
	_, werr := f.Write(data)
	if serr := f.Sync(); werr == nil {
		werr = serr
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = unix.Renameat(a.fd, tmp, a.fd, name)
	}
	if werr != nil {
		unix.Unlinkat(a.fd, tmp, 0)
		return refusef("write_failed", "publish %s: %v", filepath.Join(a.path, name), werr)
	}
	return nil
}

type notExist struct {
	path string
	err  error
}

func (e *notExist) Error() string { return fmt.Sprintf("%s: %v", e.path, e.err) }
