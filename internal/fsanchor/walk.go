// Package fsanchor opens directories from a trusted anchor one component at
// a time with O_NOFOLLOW and reads and writes through the opened
// descriptors, so no symlink below the anchor is followed. It is 2b-3's
// anchored directory (branch v3-2b-3 at 3cc7b779:
// internal/registry/runtime_open_unix.go, runtime_open_other.go, and
// walkAnchored and readEntry from runtime.go), lifted under 4-1's rule:
// bodies unchanged, every identifier exported by capitalizing its first
// letter, the manifest-name skip of WalkAnchored turned into a parameter.
package fsanchor

import (
	"fmt"
	"io"
	"os"
	"sort"
)

// WalkAnchored visits every entry below d through descriptors. visit gets an
// open file for regular entries (and owns it); directory entries are
// descended after visit accepts them. skip names top-level entries that are
// not visited.
func WalkAnchored(d *AnchoredDir, prefix string, skip []string, visit func(rel string, f *os.File, kind EntryKind, size int64, mode uint32) error) error {
	names, err := d.Names()
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		if prefix == "" && contains(skip, name) {
			continue
		}
		rel := name
		if prefix != "" {
			rel = prefix + "/" + name
		}
		f, kind, size, mode, err := d.OpenEntry(name)
		if err != nil {
			return err
		}
		if f == nil {
			return fmt.Errorf("runtime entry %q is not a regular file or directory", rel)
		}
		switch kind {
		case KindDir:
			if err := visit(rel, nil, kind, size, mode); err != nil {
				f.Close()
				return err
			}
			sub, err := AsDir(f)
			if err != nil {
				return err
			}
			err = WalkAnchored(sub, rel, skip, visit)
			sub.Close()
			if err != nil {
				return err
			}
		case KindRegular:
			if err := visit(rel, f, kind, size, mode); err != nil {
				f.Close()
				return err
			}
		default:
			f.Close()
			return visit(rel, nil, kind, size, mode)
		}
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ReadEntry reads a regular file opened through d.
func ReadEntry(d *AnchoredDir, name string) ([]byte, error) {
	f, kind, _, _, err := d.OpenEntry(name)
	if err != nil {
		return nil, err
	}
	if f == nil || kind != KindRegular {
		if f != nil {
			f.Close()
		}
		return nil, fmt.Errorf("%s is not a regular file", name)
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, 64<<20))
}
