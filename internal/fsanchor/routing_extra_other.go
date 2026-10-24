//go:build !unix

package fsanchor

// Non-unix twin of routing_extra.go (2b-4). Path-based, not race-free, as
// open_other.go.

import (
	"fmt"
	"os"
	"path/filepath"
)

// Readlink reads the target of the symlink name under d without following it.
func (d *AnchoredDir) Readlink(name string) (string, error) {
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name {
		return "", fmt.Errorf("file name %q is not clean", name)
	}
	return os.Readlink(filepath.Join(d.path, name))
}
