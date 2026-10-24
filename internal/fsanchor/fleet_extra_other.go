//go:build !unix

package fsanchor

// Non-unix twin of fleet_extra.go (4-3). Lstat-checked, not race-free, as
// open_other.go.

import (
	"fmt"
	"os"
	"path/filepath"
)

// OpenLock opens, creating it when absent, the regular file name under d.
func (d *AnchoredDir) OpenLock(name string) (*os.File, error) {
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name {
		return nil, fmt.Errorf("file name %q is not clean", name)
	}
	p := filepath.Join(d.path, name)
	if st, err := os.Lstat(p); err == nil && !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", p)
	}
	return os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0o600)
}

// Path is the directory's path, for messages.
func (d *AnchoredDir) Path() string { return d.path }
