package fleet

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/flock"
	"github.com/omriariav/amq-squad/v3/internal/fsanchor"
)

// The registry file is reached through descriptors (internal/fsanchor):
// the directory is opened from an anchor one component at a time with
// O_NOFOLLOW, and the lock, the read, the temp file and the rename all go
// relative to that descriptor, with fsync of the file and the directory.
// Under the home directory the anchor is the home directory itself (so
// ~/.amq-squad cannot be a symlink); a --fleet-file outside it is anchored
// at its deepest real ancestor directory.

// anchorFor splits path into an anchor and the components below it.
func anchorFor(path string) (base string, rel []string, name string, err error) {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return "", nil, "", fmt.Errorf("fleet registry path %q must be absolute", path)
	}
	dir, name := filepath.Split(path)
	dir = filepath.Clean(dir)
	if home, herr := os.UserHomeDir(); herr == nil && home != "" {
		home = filepath.Clean(home)
		if r, rerr := filepath.Rel(home, dir); rerr == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			if r != "." {
				rel = strings.Split(r, string(filepath.Separator))
			}
			return home, rel, name, nil
		}
	}
	// outside the home directory: the deepest ancestor that is a real
	// directory (Lstat, so a symlink is never the anchor) is the anchor, and
	// every component below it is opened with O_NOFOLLOW
	for p := dir; ; p = filepath.Dir(p) {
		if st, err := os.Lstat(p); err == nil && st.IsDir() {
			return p, rel, name, nil
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", nil, "", fmt.Errorf("fleet registry path %q has no existing ancestor directory", path)
		}
		rel = append([]string{filepath.Base(p)}, rel...)
	}
}

func openDir(path string, mkdir bool) (*fsanchor.AnchoredDir, string, error) {
	base, rel, name, err := anchorFor(path)
	if err != nil {
		return nil, "", err
	}
	d, err := fsanchor.OpenAnchored(base, rel, mkdir)
	if err != nil {
		return nil, "", fmt.Errorf("fleet registry directory: %w", err)
	}
	return d, name, nil
}

// Read reads and validates the registry at path. A missing file or
// directory is fs.ErrNotExist; a symlinked component or file is refused.
func Read(path string) (Registry, error) {
	d, name, err := openDir(path, false)
	if err != nil {
		return Registry{}, err
	}
	defer d.Close()
	return readIn(d, name, path)
}

func readIn(d *fsanchor.AnchoredDir, name, path string) (Registry, error) {
	raw, err := fsanchor.ReadEntry(d, name)
	if err != nil {
		return Registry{}, err
	}
	r, err := Parse(string(raw), path)
	if err != nil {
		return Registry{}, err
	}
	return r, r.Validate()
}

// Update applies mutate to the registry at path under an exclusive lock on
// <name>.lock and replaces the file atomically. A missing file starts empty;
// missing directories are created 0700. A mutation that fails, or leaves an
// invalid registry, writes nothing.
func Update(path string, mutate func(*Registry) error) error {
	d, name, err := openDir(path, true)
	if err != nil {
		return err
	}
	defer d.Close()
	lock, err := d.OpenLock(name + ".lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	return flock.WithFile(lock, name+".lock", func() error {
		r, err := readIn(d, name, path)
		if errors.Is(err, fs.ErrNotExist) {
			r, err = Registry{Schema: Schema}, nil
		}
		if err != nil {
			return err
		}
		if err := mutate(&r); err != nil {
			return err
		}
		if err := r.Validate(); err != nil {
			return err
		}
		return d.WriteAtomic(name, []byte(Render(r)))
	})
}
