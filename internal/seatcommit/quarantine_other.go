//go:build !unix

package seatcommit

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Off unix (no openat family) the quarantine works by path, with a link
// check before each open (3.0 hosts are macOS and Linux; this keeps the
// package building elsewhere).

func ownedByCaller(string, os.FileInfo) error { return nil }

func lstatRegular(path string) (present bool, err error) {
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return true, fmt.Errorf("%s is a symlink", path)
	}
	if !fi.Mode().IsRegular() {
		return true, fmt.Errorf("%s is not a regular file", path)
	}
	return true, nil
}

func readNoFollow(path string) ([]byte, bool, error) {
	present, err := lstatRegular(path)
	if err != nil || !present {
		return nil, present, err
	}
	data, err := os.ReadFile(path)
	return data, true, err
}

func slotTaken(err error) bool { return errors.Is(err, fs.ErrExist) || err != nil }

type pathDir struct{ path string }

func openDirNoFollow(path string) (slotDir, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("the quarantine directory %s is a symlink", path)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("the quarantine directory %s is not a directory", path)
	}
	return &pathDir{path: path}, nil
}

func (d *pathDir) name() string               { return d.path }
func (d *pathDir) stat() (fs.FileInfo, error) { return os.Lstat(d.path) }
func (d *pathDir) close() error               { return nil }
func (d *pathDir) mkdir(name string) error    { return os.Mkdir(filepath.Join(d.path, name), 0o700) }

// sync is a no-op off unix (no directory fsync by path).
func (d *pathDir) sync() error {
	trace("fsync", d.path)
	return nil
}

func (d *pathDir) rename(from, to string) error {
	trace("renameat", filepath.Join(d.path, from)+" -> "+filepath.Join(d.path, to))
	if _, err := os.Lstat(filepath.Join(d.path, to)); err == nil {
		return fs.ErrExist
	}
	return os.Rename(filepath.Join(d.path, from), filepath.Join(d.path, to))
}

func (d *pathDir) names() ([]string, error) {
	ents, err := os.ReadDir(d.path)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return names, nil
}

func (d *pathDir) create(name string, data []byte) error {
	p := filepath.Join(d.path, name)
	if present, err := lstatRegular(p); err != nil {
		return err
	} else if present {
		return fs.ErrExist
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func (d *pathDir) read(name string) ([]byte, bool, error) {
	return readNoFollow(filepath.Join(d.path, name))
}

func (d *pathDir) open(name string) (slotDir, error) {
	p := filepath.Join(d.path, name)
	fi, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s is a symlink", p)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", p)
	}
	return &pathDir{path: p}, nil
}

func (d *pathDir) remove(name string, _ bool) error { return os.Remove(filepath.Join(d.path, name)) }
