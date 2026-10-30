//go:build !windows

package atomicfile

import (
	"errors"
	"os"
)

// SyncDir fsyncs a directory so a created or renamed entry is durable.
func SyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}
