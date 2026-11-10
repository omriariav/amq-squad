//go:build unix

package seatcommit

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// The quarantine on unix: one descriptor of the quarantine directory and
// the openat family relative to it (golang.org/x/sys/unix, already a
// dependency through atomicfile). Nothing follows a link: the directory
// and every subdirectory open O_DIRECTORY|O_NOFOLLOW, every file
// O_NOFOLLOW, and a type check runs on the descriptor, never on a path.

// ownedByCaller refuses a quarantine directory another user owns (a planted
// directory the copy would land in).
func ownedByCaller(dir string, fi os.FileInfo) error {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if int(st.Uid) != os.Getuid() {
		return fmt.Errorf("the quarantine directory %s is owned by uid %d, not %d", dir, st.Uid, os.Getuid())
	}
	return nil
}

// readNoFollow reads a source file (the ledger, its head) without following
// a link: the descriptor must be a regular file. present is false when
// nothing is at path; a link or another type is an error that names it.
func readNoFollow(path string) (data []byte, present bool, err error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, false, linkError(path, err)
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, true, err
	}
	if !fi.Mode().IsRegular() {
		return nil, true, fmt.Errorf("%s is not a regular file", path)
	}
	data, err = io.ReadAll(f)
	return data, true, err
}

// linkError maps an open error: ENOENT stays fs.ErrNotExist; a refusal
// of the open (ELOOP for a link under O_NOFOLLOW; ENOTDIR, which macOS
// also gives a link under O_DIRECTORY|O_NOFOLLOW) names the path and what
// is there. The Lstat here only words the error; the open already refused.
func linkError(path string, err error) error {
	switch {
	case errors.Is(err, unix.ENOENT):
		return fs.ErrNotExist
	case errors.Is(err, unix.ELOOP), errors.Is(err, unix.EMLINK), errors.Is(err, unix.ENOTDIR):
		return fmt.Errorf("%s %s", path, whatIsAt(path, "is not a directory"))
	}
	return fmt.Errorf("%s: %w", path, err)
}

// whatIsAt words a refused open: "is a symlink" for a link, else fallback.
func whatIsAt(path, fallback string) string {
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		return "is a symlink"
	}
	return fallback
}

// slotTaken reports a rename refused because the target slot exists (a
// directory that is not empty, or something that is not a directory).
func slotTaken(err error) bool {
	return errors.Is(err, unix.EEXIST) || errors.Is(err, unix.ENOTEMPTY) || errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.EISDIR)
}

// atDir is a directory held by descriptor.
type atDir struct {
	f    *os.File
	path string
}

func openDirNoFollow(path string) (slotDir, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.EMLINK) || errors.Is(err, unix.ENOTDIR) {
			return nil, fmt.Errorf("the quarantine directory %s %s", path, whatIsAt(path, "is not a directory"))
		}
		return nil, fmt.Errorf("the quarantine directory %s: %w", path, err)
	}
	return &atDir{f: os.NewFile(uintptr(fd), path), path: path}, nil
}

func (d *atDir) fd() int                    { return int(d.f.Fd()) }
func (d *atDir) name() string               { return d.path }
func (d *atDir) stat() (fs.FileInfo, error) { return d.f.Stat() }
func (d *atDir) close() error               { return d.f.Close() }
func (d *atDir) mkdir(name string) error    { return unix.Mkdirat(d.fd(), name, 0o700) }

func (d *atDir) sync() error {
	trace("fsync", d.path)
	return d.f.Sync()
}

func (d *atDir) rename(from, to string) error {
	trace("renameat", d.path+"/"+from+" -> "+d.path+"/"+to)
	return unix.Renameat(d.fd(), from, d.fd(), to)
}

func (d *atDir) names() ([]string, error) {
	if _, err := d.f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	ents, err := d.f.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return names, nil
}

func (d *atDir) create(name string, data []byte) error {
	fd, err := unix.Openat(d.fd(), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return linkError(d.path+"/"+name, err)
	}
	f := os.NewFile(uintptr(fd), name)
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func (d *atDir) read(name string) ([]byte, bool, error) {
	fd, err := unix.Openat(d.fd(), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		err = linkError(d.path+"/"+name, err)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, true, err
	}
	if !fi.Mode().IsRegular() {
		return nil, true, fmt.Errorf("%s/%s is not a regular file", d.path, name)
	}
	data, err := io.ReadAll(f)
	return data, true, err
}

func (d *atDir) open(name string) (slotDir, error) {
	fd, err := unix.Openat(d.fd(), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, linkError(d.path+"/"+name, err)
	}
	return &atDir{f: os.NewFile(uintptr(fd), name), path: d.path + "/" + name}, nil
}

func (d *atDir) remove(name string, isDir bool) error {
	flags := 0
	if isDir {
		flags = unix.AT_REMOVEDIR
	}
	return unix.Unlinkat(d.fd(), name, flags)
}

// openAppendNoFollow opens a seat's hold release file for appending,
// created 0600 when absent, without following a link and without blocking
// on a FIFO (O_NOFOLLOW|O_NONBLOCK, Codex on 50986a71): the descriptor
// must be a regular file. It is opened for reading too, so the caller
// checks the file's last byte through the same descriptor it writes
// with; nothing is checked by path.
func openAppendNoFollow(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_APPEND|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0o600)
	if err != nil {
		if errors.Is(err, unix.ENXIO) {
			return nil, fmt.Errorf("%s is not a regular file", path)
		}
		return nil, linkError(path, err)
	}
	f := os.NewFile(uintptr(fd), path)
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	return f, nil
}
