//go:build !unix

package registry

import (
	"fmt"
	"os"
	"path/filepath"
)

// anchoredDir off unix (3.0 hosts are macOS, plan 8): every component is
// checked with Lstat before it is used. This is not race-free.
type anchoredDir struct{ path string }

func openAnchored(base string, rel []string, mkdir bool) (*anchoredDir, error) {
	cur := base
	for _, part := range rel {
		if part == "" || part == "." || part == ".." {
			return nil, fmt.Errorf("path component %q is not clean", part)
		}
		cur = filepath.Join(cur, part)
		st, err := os.Lstat(cur)
		if os.IsNotExist(err) && mkdir {
			if err = os.Mkdir(cur, 0o700); err == nil {
				st, err = os.Lstat(cur)
			}
		}
		if err != nil {
			return nil, err
		}
		if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
			return nil, fmt.Errorf("%s is a symlink or not a directory", cur)
		}
	}
	return &anchoredDir{path: cur}, nil
}

func (d *anchoredDir) Close() error { return nil }

type entryKind int

const (
	kindOther entryKind = iota
	kindDir
	kindRegular
)

func (d *anchoredDir) openEntry(name string) (*os.File, entryKind, int64, uint32, error) {
	p := filepath.Join(d.path, name)
	st, err := os.Lstat(p)
	if err != nil {
		return nil, kindOther, 0, 0, err
	}
	kind := kindOther
	switch {
	case st.Mode()&os.ModeSymlink != 0:
		return nil, kindOther, 0, 0, fmt.Errorf("%s is a symlink", p)
	case st.IsDir():
		kind = kindDir
	case st.Mode().IsRegular():
		kind = kindRegular
	default:
		return nil, kindOther, 0, 0, nil
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, kindOther, 0, 0, err
	}
	return f, kind, st.Size(), uint32(st.Mode().Perm()), nil
}

func asDir(f *os.File) (*anchoredDir, error) {
	name := f.Name()
	f.Close()
	return &anchoredDir{path: name}, nil
}

func (d *anchoredDir) names() ([]string, error) {
	f, err := os.Open(d.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Readdirnames(-1)
}

func (d *anchoredDir) writeAtomic(name string, data []byte) error {
	tmp, err := os.CreateTemp(d.path, "."+name+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(d.path, name))
}
