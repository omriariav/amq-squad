//go:build !windows

package gatestore

import (
	"errors"
	"os"
)

// syncDir fsyncs a directory so a created or renamed entry is durable.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}
