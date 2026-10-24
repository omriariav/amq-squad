//go:build unix

package routing

import (
	"os"
	"syscall"
	"testing"
	"time"
)

// Pass 4, new defect: a FIFO at the seed path must not hang router
// construction or the report: the open does not block, and fstat refuses a
// file that is not regular.
func TestFix4SeedFIFODoesNotHang(t *testing.T) {
	h := newFix3(t, "")
	os.Remove(SeedPath(h.root))
	if err := syscall.Mkfifo(SeedPath(h.root), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 2)
	go func() { _, err := New(Options{Table: Default(), Root: h.root}); done <- err }()
	go func() { _, _, _, err := VerifySessionLog(h.root); done <- err }()
	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if !IsInvalid(err) {
				t.Fatalf("FIFO seed: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("opening a FIFO seed hung")
		}
	}
}
