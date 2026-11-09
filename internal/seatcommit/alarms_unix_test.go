//go:build unix

package seatcommit

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Codex on 50986a71: the append opens the hold release file through one
// descriptor that follows no link and does not block on a FIFO, and
// writes only to a regular file. The path replaced by a symlink before the
// append: the append is refused naming the link, and nothing is written to
// the file the link points at. Replaced by a FIFO: the append is refused at
// once, not blocked while the seat's ledger lock is held. The path a
// regular file again: the append works. Reverted (a plain OpenFile), the
// line is written through the link.
func TestTheAppendWritesOnlyToTheSeatsOwnRegularFile(t *testing.T) {
	root := t.TempDir()
	at := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	d := AlarmDecision{Seat: "s1", Alarm: "aaaaaaa1", Reason: "r", At: at, Lead: "cto"}
	if err := appendAlarm(root, d); err != nil {
		t.Fatal(err)
	}
	path := AlarmsPath(root, "s1")
	outside := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.WriteFile(outside, []byte("not the seat's\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	d.Alarm = "bbbbbbb2"
	if err := appendAlarm(root, d); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("the append through a symlink: %v", err)
	}
	if got := readFile(t, outside); string(got) != "not the seat's\n" {
		t.Fatalf("the append wrote outside: %q", got)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- appendAlarm(root, d) }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("the append to a FIFO: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the append blocked on a FIFO")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := appendAlarm(root, d); err != nil {
		t.Fatalf("the append to a regular file: %v", err)
	}
	if log, err := ReadAlarms(root, "s1"); err != nil || !log.Released("bbbbbbb2") {
		t.Fatalf("after the append: %v %+v", err, log)
	}
}
