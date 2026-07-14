package flock

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestTryExclusiveMissingReadOnlyDoesNotCreate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.lock")
	lock, acquired, err := TryExclusive(path, false)
	if err != nil || !acquired || lock != nil {
		t.Fatalf("missing no-create = lock=%v acquired=%t err=%v", lock, acquired, err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("read-only probe created lock path: %v", err)
	}
}

func TestTryExclusiveHeldThenReleased(t *testing.T) {
	path := filepath.Join(t.TempDir(), "held.lock")
	first, acquired, err := TryExclusive(path, true)
	if err != nil || !acquired || first == nil {
		t.Fatalf("first acquire = lock=%v acquired=%t err=%v", first, acquired, err)
	}
	second, acquired, err := TryExclusive(path, false)
	if err != nil || acquired || second != nil {
		t.Fatalf("held probe = lock=%v acquired=%t err=%v", second, acquired, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, acquired, err := TryExclusive(path, false)
	if err != nil || !acquired || third == nil {
		t.Fatalf("released probe = lock=%v acquired=%t err=%v", third, acquired, err)
	}
	if err := third.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSharedAdmissionsExcludeExclusiveButCompose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "admission.lock")
	first, acquired, err := TryShared(path, true)
	if err != nil || !acquired || first == nil {
		t.Fatalf("first shared admission: lock=%v acquired=%t err=%v", first, acquired, err)
	}
	defer first.Close()
	second, acquired, err := TryShared(path, false)
	if err != nil || !acquired || second == nil {
		t.Fatalf("second shared admission: lock=%v acquired=%t err=%v", second, acquired, err)
	}
	if lock, acquired, err := TryExclusive(path, false); err != nil || acquired || lock != nil {
		t.Fatalf("exclusive crossed shared admissions: lock=%v acquired=%t err=%v", lock, acquired, err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	lock, acquired, err := TryExclusive(path, false)
	if err != nil || !acquired || lock == nil {
		t.Fatalf("exclusive after shared release: lock=%v acquired=%t err=%v", lock, acquired, err)
	}
	_ = lock.Close()
}

func TestTrySharedRefusesExclusiveHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "admission.lock")
	exclusive, err := AcquireExclusive(path)
	if err != nil {
		t.Fatal(err)
	}
	defer exclusive.Close()
	shared, acquired, err := TryShared(path, false)
	if err != nil || acquired || shared != nil {
		t.Fatalf("shared crossed exclusive admission: lock=%v acquired=%t err=%v", shared, acquired, err)
	}
}

func TestTryExclusiveUnsupportedFailsClosed(t *testing.T) {
	original := tryExclusiveAttempt
	tryExclusiveAttempt = func(*os.File) (bool, error) { return false, ErrUnsupported }
	t.Cleanup(func() { tryExclusiveAttempt = original })
	path := filepath.Join(t.TempDir(), "unsupported.lock")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	lock, acquired, err := TryExclusive(path, false)
	if lock != nil || acquired || !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unsupported = lock=%v acquired=%t err=%v", lock, acquired, err)
	}
}

func TestTryExclusiveOpenError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-parent", "lock")
	lock, acquired, err := TryExclusive(path, true)
	if lock != nil || acquired || err == nil {
		t.Fatalf("open error = lock=%v acquired=%t err=%v", lock, acquired, err)
	}
}
