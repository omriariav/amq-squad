// Package atomicfile is the one durable file writer of amq-squad: the gate
// store, the rules pointer stubs and squad.yaml write through it. A temp file
// is created with os.CreateTemp in the target directory (O_EXCL with a random
// name, so a planted name or symlink is never followed), written, chmodded,
// fsynced and renamed over the target, and the directory is fsynced.
//
// Tx writes several files as one change: every file is staged first (which
// proves every directory writable), the caller's preconditions run, and only
// then are the temps renamed in order; a failed rename restores every file
// already replaced.
package atomicfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// rename is os.Rename; tests make it fail.
var rename = os.Rename

// Write replaces path durably with data and mode.
func Write(path string, data []byte, mode os.FileMode) error {
	var tx Tx
	if err := tx.Stage(path, data, mode); err != nil {
		return err
	}
	return tx.Commit(nil)
}

type staged struct {
	path, tmp string
	old       []byte // the content before the change
	existed   bool
	mode      os.FileMode // the mode before the change, for a rollback
}

// Tx is a set of staged writes.
type Tx struct{ files []staged }

// Stage writes data to a fresh temp file beside path. Nothing visible changes.
func (tx *Tx) Stage(path string, data []byte, mode os.FileMode) error {
	old, err := os.ReadFile(path)
	existed := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	tmp, err := writeTemp(path, data, mode)
	if err != nil {
		tx.Abort()
		return fmt.Errorf("stage %s: %w", path, err)
	}
	oldMode := mode
	if info, err := os.Stat(path); err == nil {
		oldMode = info.Mode().Perm()
	}
	tx.files = append(tx.files, staged{path: path, tmp: tmp, old: old, existed: existed, mode: oldMode})
	return nil
}

func writeTemp(path string, data []byte, mode os.FileMode) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return "", err
	}
	_, werr := f.Write(data)
	err = errors.Join(werr, f.Chmod(mode), f.Sync(), f.Close())
	if err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// Abort removes the staged temps; no target changes.
func (tx *Tx) Abort() {
	for _, f := range tx.files {
		_ = os.Remove(f.tmp)
	}
	tx.files = nil
}

// Commit runs check (nil for none) with everything staged, then renames each
// temp over its target. On a failure it restores the targets already
// replaced, newest first, and reports both errors.
func (tx *Tx) Commit(check func() error) error {
	defer tx.Abort()
	if check != nil {
		if err := check(); err != nil {
			return err
		}
	}
	for i, f := range tx.files {
		if err := rename(f.tmp, f.path); err != nil {
			return errors.Join(fmt.Errorf("write %s: %w", f.path, err), tx.rollback(i))
		}
		if err := SyncDir(filepath.Dir(f.path)); err != nil {
			return errors.Join(fmt.Errorf("sync %s: %w", filepath.Dir(f.path), err), tx.rollback(i+1))
		}
	}
	return nil
}

// rollback restores the first n files to their content before the change.
func (tx *Tx) rollback(n int) error {
	var errs []error
	for i := n - 1; i >= 0; i-- {
		f := tx.files[i]
		if !f.existed {
			errs = append(errs, os.Remove(f.path))
			continue
		}
		tmp, err := writeTemp(f.path, f.old, f.mode)
		if err == nil {
			err = os.Rename(tmp, f.path)
		}
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("rollback: %w", err)
	}
	return nil
}
