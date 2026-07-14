// Package flock provides a tiny advisory exclusive file lock for serializing
// read-modify-write cycles on a shared on-disk file (e.g. team.json) across
// concurrent amq-squad processes. The lock is taken on a sidecar "<path>.lock"
// file so it survives the atomic rename the writer does on the real file.
package flock

import (
	"errors"
	"fmt"
	"os"
)

// ErrUnsupported is returned when a caller requires a provable advisory lock
// on a platform where this package cannot provide one. Best-effort locking is
// acceptable for legacy atomic writers, but namespace migration must fail
// closed when ownership cannot be proven.
var ErrUnsupported = errors.New("advisory file locks are unsupported on this platform")

var tryExclusiveAttempt = tryLockExclusive
var trySharedAttempt = tryLockShared

// Exclusive is a held advisory exclusive lock. Close releases the lock. It is
// intentionally small so higher-level transactions can hold several locks in
// a deterministic order without nesting callbacks.
type Exclusive struct {
	f *os.File
}

// AcquireExclusive acquires and holds lockPath, waiting for the current owner
// when necessary. Callers must Close the returned handle.
func AcquireExclusive(lockPath string) (*Exclusive, error) {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock %s: %w", lockPath, err)
	}
	return AcquireExclusiveFile(f)
}

// AcquireExclusiveFile acquires a required exclusive lock on an already-open
// sidecar. Ownership of f transfers to the returned lock or is closed on error.
func AcquireExclusiveFile(f *os.File) (*Exclusive, error) {
	if f == nil {
		return nil, fmt.Errorf("lock file is nil")
	}
	if err := lockExclusiveRequired(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock %s: %w", f.Name(), err)
	}
	return &Exclusive{f: f}, nil
}

// TryShared attempts a shared admission without waiting. It lets namespace
// writers refuse immediately when a migration already owns the exclusive
// admission instead of waking after commit with stale pre-lock resolution.
func TryShared(lockPath string, create bool) (lock *Exclusive, acquired bool, err error) {
	var f *os.File
	if create {
		f, err = os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	} else {
		f, err = os.Open(lockPath)
		if os.IsNotExist(err) {
			return nil, true, nil
		}
	}
	if err != nil {
		return nil, false, fmt.Errorf("open lock %s: %w", lockPath, err)
	}
	return TrySharedFile(f)
}

// TrySharedFile attempts a shared lock on an already-open sidecar. Ownership
// transfers to the returned lock or is closed when refused/error.
func TrySharedFile(f *os.File) (lock *Exclusive, acquired bool, err error) {
	if f == nil {
		return nil, false, fmt.Errorf("lock file is nil")
	}
	ok, lockErr := trySharedAttempt(f)
	if lockErr != nil {
		_ = f.Close()
		return nil, false, fmt.Errorf("try shared lock %s: %w", f.Name(), lockErr)
	}
	if !ok {
		_ = f.Close()
		return nil, false, nil
	}
	return &Exclusive{f: f}, true, nil
}

// Close releases the lock and closes the sidecar file.
func (l *Exclusive) Close() error {
	if l == nil || l.f == nil {
		return nil
	}
	unlock(l.f)
	err := l.f.Close()
	l.f = nil
	return err
}

// TryExclusive attempts to acquire lockPath without waiting. When create is
// false, a missing lock path means no holder and returns (nil, true, nil)
// without creating filesystem state; this is used by read-only previews.
// acquired=false means another process holds the lock.
func TryExclusive(lockPath string, create bool) (lock *Exclusive, acquired bool, err error) {
	var f *os.File
	if create {
		f, err = os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	} else {
		f, err = os.Open(lockPath)
		if os.IsNotExist(err) {
			return nil, true, nil
		}
	}
	if err != nil {
		return nil, false, fmt.Errorf("open lock %s: %w", lockPath, err)
	}
	return TryExclusiveFile(f)
}

// TryExclusiveFile attempts an exclusive lock on an already-open sidecar.
// Ownership transfers to the returned lock or is closed when refused/error.
func TryExclusiveFile(f *os.File) (lock *Exclusive, acquired bool, err error) {
	if f == nil {
		return nil, false, fmt.Errorf("lock file is nil")
	}
	ok, lockErr := tryExclusiveAttempt(f)
	if lockErr != nil {
		_ = f.Close()
		return nil, false, fmt.Errorf("try lock %s: %w", f.Name(), lockErr)
	}
	if !ok {
		_ = f.Close()
		return nil, false, nil
	}
	return &Exclusive{f: f}, true, nil
}

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
