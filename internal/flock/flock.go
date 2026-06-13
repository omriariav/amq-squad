// Package flock provides a tiny advisory exclusive file lock for serializing
// read-modify-write cycles on a shared on-disk file (e.g. team.json) across
// concurrent amq-squad processes. The lock is taken on a sidecar "<path>.lock"
// file so it survives the atomic rename the writer does on the real file.
package flock

import (
	"fmt"
	"os"
)

// WithLock acquires an exclusive advisory lock on lockPath (creating it if
// needed), runs fn, then releases the lock. The lock is held for the whole
// fn, so a read-modify-write inside fn is serialized against other holders.
// On platforms without flock it degrades to a best-effort no-op (fn still
// runs) — the writer's atomic rename keeps the file valid regardless.
func WithLock(lockPath string, fn func() error) error {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open lock %s: %w", lockPath, err)
	}
	defer f.Close()
	if err := lockExclusive(f); err != nil {
		return fmt.Errorf("lock %s: %w", lockPath, err)
	}
	// Explicit unlock for clarity; Close() would also release the advisory
	// lock. Defers run LIFO, so unlock runs before Close — both are safe.
	defer unlock(f)
	return fn()
}
