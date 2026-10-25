//go:build !unix

package uninstall

import (
	"os"
	"path/filepath"

	"github.com/omriariav/amq-squad/v3/internal/fsanchor"
)

// sameEntry: this platform has no descriptor-relative stat; it compares the
// directories by path (the lstat-checked twin of the anchored walk).
func sameEntry(parent *fsanchor.AnchoredDir, name string, wd *fsanchor.AnchoredDir) (bool, error) {
	a, err := os.Lstat(filepath.Join(parent.Path(), name))
	if err != nil {
		return false, err
	}
	b, err := os.Stat(wd.Path())
	if err != nil {
		return false, err
	}
	return os.SameFile(a, b), nil
}

// rmdirEntry removes the empty directory name below parent.
func rmdirEntry(parent *fsanchor.AnchoredDir, name string) error {
	return os.Remove(filepath.Join(parent.Path(), name))
}
