//go:build !unix

package fsanchor

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/omriariav/amq-squad/v3/internal/atomicfile"
)

// AnchoredDir off unix (3.0 hosts are macOS, plan 8): every component is
// checked with Lstat before it is used. This is not race-free.
type AnchoredDir struct{ path string }

func OpenAnchored(base string, rel []string, mkdir bool) (*AnchoredDir, error) {
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
	return &AnchoredDir{path: cur}, nil
}

func (d *AnchoredDir) Close() error { return nil }

type EntryKind int

const (
	KindOther EntryKind = iota
	KindDir
	KindRegular
)

func (d *AnchoredDir) OpenEntry(name string) (*os.File, EntryKind, int64, uint32, error) {
	p := filepath.Join(d.path, name)
	st, err := os.Lstat(p)
	if err != nil {
		return nil, KindOther, 0, 0, err
	}
	kind := KindOther
	switch {
	case st.Mode()&os.ModeSymlink != 0:
		return nil, KindOther, 0, 0, fmt.Errorf("%s is a symlink", p)
	case st.IsDir():
		kind = KindDir
	case st.Mode().IsRegular():
		kind = KindRegular
	default:
		return nil, KindOther, 0, 0, nil
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, KindOther, 0, 0, err
	}
	return f, kind, st.Size(), uint32(st.Mode().Perm()), nil
}

func AsDir(f *os.File) (*AnchoredDir, error) {
	name := f.Name()
	f.Close()
	return &AnchoredDir{path: name}, nil
}

func (d *AnchoredDir) Names() ([]string, error) {
	f, err := os.Open(d.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Readdirnames(-1)
}

// WriteAtomic writes name below the checked directory through
// internal/atomicfile (4-1 rebase after 2b-5).
func (d *AnchoredDir) WriteAtomic(name string, data []byte) error {
	return atomicfile.Write(filepath.Join(d.path, name), data, 0o600)
}
