package seatcommit

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// gitStandin is a git that takes action in the checkout CleanSeat runs
// (read-tree -u), then execs the real git (or not, when action exits).
func (f *fixture) gitStandin(name, action string) string {
	f.t.Helper()
	p := filepath.Join(f.dir, name)
	body := "#!/bin/sh\ncase \"$*\" in *\"read-tree -u\"*) " + action + ";; esac\nexec " + gitBin(f.t) + " \"$@\"\n"
	if err := os.WriteFile(p, []byte(body), 0o700); err != nil {
		f.t.Fatal(err)
	}
	return p
}

// 3.2.0-2b-1: ReleaseSeat is deferred on every clean path, including a git
// failure, so a failed cleaner never strands its own marker.
func TestInterruptedCleanReleasesItsMarker(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n\n// interrupted\n")
	tk, err := f.store.Add(task.AddInput{Title: "after the clean", Class: "summarize"}, f.now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CleanSeat(f.root, f.handle, f.gitStandin("git-dies", "exit 1"), nil, f.now); err == nil {
		t.Fatal("the interrupted clean succeeded")
	}
	if _, err := f.store.Claim(tk.ID, f.handle, nil, time.Hour, f.now); err != nil {
		t.Fatalf("the failed cleaner stranded its marker: %v", err)
	}
}

// Item 1, the live cleaner: a cleaner still running past the TTL keeps the
// seat's ledger, so no second cleaner takes the marker over and nobody leases
// the seat until it ends.
func TestLiveCleanerPastTheTTLHoldsTheSeat(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n\n// slow\n")
	marker := filepath.Join(f.dir, "in-checkout")
	tk, err := f.store.Add(task.AddInput{Title: "after the clean", Class: "summarize"}, f.now)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := CleanSeat(f.root, f.handle, f.gitStandin("git-slow", ": > "+marker+"; /bin/sleep 5"), nil, f.now)
		done <- err
	}()
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(marker); err == nil {
			break
		}
	}
	later := f.now.Add(task.SeatReservationTTL + time.Minute)
	if _, err := f.store.Claim(tk.ID, f.handle, nil, time.Hour, later); !errors.Is(err, task.ErrSeatUnclean) {
		t.Fatalf("a claim during a live clean past the TTL: %v", err)
	}
	if _, err := CleanSeat(f.root, f.handle, gitBin(t), nil, later); !errors.Is(err, ErrLedgerBusy) {
		t.Fatalf("a second cleaner during a live clean: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("the live clean: %v", err)
	}
	if _, err := f.store.Claim(tk.ID, f.handle, nil, time.Hour, later); err != nil {
		t.Fatalf("the lease after the live clean: %v", err)
	}
}
