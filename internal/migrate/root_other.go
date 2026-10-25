//go:build !unix

package migrate

import (
	"os"
	"path/filepath"

	"github.com/omriariav/amq-squad/v3/internal/fsanchor"
)

// The non-unix twins of root_unix.go: path-based below the checked directory
// (3.0 hosts are macOS, plan 8), as internal/fsanchor's own non-unix variants.
// Not race-free; the identity check is a no-op (a descriptor holds no inode
// here).

func dirIdentity(*fsanchor.AnchoredDir, string) (uint64, uint64, error) { return 0, 0, nil }

func pathIdentity(string) (uint64, uint64, error) { return 0, 0, nil }

func unlinkAt(d *fsanchor.AnchoredDir, name string) error {
	return os.Remove(filepath.Join(d.Path(), name))
}

func linkAt(d *fsanchor.AnchoredDir, old, new string) error {
	return os.Link(filepath.Join(d.Path(), old), filepath.Join(d.Path(), new))
}
