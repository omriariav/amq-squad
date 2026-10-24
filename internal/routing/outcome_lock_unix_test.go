//go:build unix

package routing

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/flock"
)

// The writer holds the flock on the outcome log for its read-then-append: a
// held lock keeps it waiting. (On APFS one O_APPEND write of 512 KiB stays
// whole even without the lock, so the eight-writer test alone cannot see
// it; the lock also covers the torn-tail check before the write.)
func TestAppendWaitsForTheLock(t *testing.T) {
	root := t.TempDir()
	path := OutcomePath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	held, err := flock.AcquireExclusive(path)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- AppendRow(root, Row{Task: "t1", Attempt: 1}) }()
	select {
	case err := <-done:
		held.Close()
		t.Fatalf("append finished while the lock was held (err %v)", err)
	case <-time.After(300 * time.Millisecond):
	}
	held.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if rows, _, _ := ReadRows(path); len(rows) != 1 {
		t.Fatalf("rows %+v", rows)
	}
}
