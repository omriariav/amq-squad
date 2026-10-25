//go:build !unix

package migrate

import "os"

// unlinkUnder off unix: Lstat-checked, not race-free (as internal/fsanchor).
func unlinkUnder(project, path string) error {
	if err := noSymlinkBelow(project, path); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
