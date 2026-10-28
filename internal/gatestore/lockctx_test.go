package gatestore

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/flock"
)

// flipCtx reports no error for its first n Err calls, then Canceled: the
// deadline passes while the lock is being taken.
type flipCtx struct {
	context.Context
	left atomic.Int32
}

func (c *flipCtx) Err() error {
	if c.left.Add(-1) >= 0 {
		return nil
	}
	return context.Canceled
}

// 5-amq-B fix round 2, item 4: the bounded lock checks its context before
// every attempt and after it takes the lock; a lock taken after the
// deadline is released and the caller gets ErrLockTimeout, so no write runs
// under an expired verification.
func TestGateLockChecksTheContextAroundEveryAttempt(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(s.Dir("consumed", "locks"), 0o700); err != nil {
		t.Fatal(err)
	}
	// expired before the first attempt: no lock is taken
	done, cancel := context.WithCancel(context.Background())
	cancel()
	if l, err := s.WithContext(done).lockGate("g1"); !errors.Is(err, ErrLockTimeout) || l != nil {
		t.Fatalf("expired context: %v %v", l, err)
	}
	// expired between the attempt and its use: the lock is released
	fc := &flipCtx{Context: context.Background()}
	fc.left.Store(1)
	if l, err := s.WithContext(fc).lockGate("g1"); !errors.Is(err, ErrLockTimeout) || l != nil {
		t.Fatalf("expired after acquisition: %v %v", l, err)
	}
	l, ok, err := flock.TryExclusive(s.Dir("consumed", "locks", "g1.lock"), true)
	if err != nil || !ok {
		t.Fatalf("the lock taken after the deadline is still held: %v %v", ok, err)
	}
	_ = l.Close()
	// a live context takes it
	ctx, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	l2, err := s.WithContext(ctx).lockGate("g1")
	if err != nil {
		t.Fatal(err)
	}
	_ = l2.Close()
	// a held lock and an expired context: the check before the next attempt
	// ends the wait at once
	held, err := flock.AcquireExclusive(s.Dir("consumed", "locks", "g2.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	start := time.Now()
	if _, err := s.WithContext(done).lockGate("g2"); !errors.Is(err, ErrLockTimeout) || time.Since(start) > time.Second {
		t.Fatalf("held lock, expired context: %v after %s", err, time.Since(start))
	}
	// a gate-store write path (Recover) under an expired context runs nothing
	if err := s.WithContext(done).Recover("g1"); !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("Recover under an expired context: %v", err)
	}
}
