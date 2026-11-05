package seatcommit

import (
	"sync"
	"testing"
	"time"
)

func expectedArchiveSnapshot(t *testing.T, f *fixture) (string, string) {
	t.Helper()
	ledger, err := ReadLedger(f.root, f.handle)
	if err != nil {
		t.Fatal(err)
	}
	sh(t, f.wt, "add", "-A")
	tree := sh(t, f.wt, "write-tree")
	sh(t, f.wt, "reset", "-q")
	return ledger.Parent(), tree
}

func closeOnce(ch chan struct{}) func() {
	var once sync.Once
	return func() { once.Do(func() { close(ch) }) }
}

func waitForArchiveRelease(release <-chan struct{}) {
	select {
	case <-release:
	case <-time.After(3 * time.Second):
	}
}

func cleanupArchiveHook(t *testing.T, release func()) {
	t.Helper()
	t.Cleanup(func() {
		release()
		beforeArchiveAttemptWrite = nil
	})
}
