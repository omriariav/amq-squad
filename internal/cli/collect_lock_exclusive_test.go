package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLockCollectJournalExclusiveContextBoundsStaleSentinel(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".lock")
	if err := os.WriteFile(path, []byte("orphaned collector"), collectJournalFilePerm); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	unlock, err := lockCollectJournalExclusiveContext(context.Background(), root, 25*time.Millisecond)
	elapsed := time.Since(started)
	if unlock != nil {
		unlock()
		t.Fatal("stale sentinel unexpectedly acquired")
	}
	if err == nil || !strings.Contains(err.Error(), "collect already running") ||
		!strings.Contains(err.Error(), "stale lock") || !strings.Contains(err.Error(), path) {
		t.Fatalf("stale sentinel error = %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("stale sentinel wait was not bounded: %s", elapsed)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("stale sentinel must remain for explicit inspection: %v", statErr)
	}
}
