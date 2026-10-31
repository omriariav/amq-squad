package seatcommit

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// v3-5-verifier, item 1 of the third round: CleanSeat holds no lock the tick
// takes across its git work. A git stand-in that takes 10 s in the checkout
// runs while a store read (a tick's) completes at once, and the seat being
// cleaned is refused a lease meanwhile (its reservation).
func TestCleanSeatHoldsNoStoreLockAcrossGit(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n\n// left over\n")
	marker := filepath.Join(f.dir, "in-checkout")
	stand := filepath.Join(f.dir, "git-standin")
	body := "#!/bin/sh\ncase \"$*\" in *\"read-tree -u\"*) : > " + marker + "; /bin/sleep 10;; esac\nexec " + gitBin(t) + " \"$@\"\n"
	if err := os.WriteFile(stand, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	other, err := f.store.Add(task.AddInput{Title: "for the cleaned seat", Class: "summarize"}, f.now)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := CleanSeat(f.root, f.handle, stand, nil, f.now)
		done <- err
	}()
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(marker); err == nil {
			break
		}
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("CleanSeat never reached its checkout")
	}
	start := time.Now()
	read := make(chan error, 1)
	go func() {
		_, err := f.store.List()
		read <- err
	}()
	select {
	case err := <-read:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		<-done
		t.Fatal("a store read waited for CleanSeat's git work")
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("a store read took %s during CleanSeat's git work", took.Round(time.Millisecond))
	}
	if _, err := f.store.Claim(other.ID, f.handle, nil, time.Hour, f.now); !errors.Is(err, task.ErrSeatUnclean) {
		t.Fatalf("a claim by the seat being cleaned: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("CleanSeat: %v", err)
	}
	if _, err := f.store.Claim(other.ID, f.handle, nil, time.Hour, f.now); err != nil {
		t.Fatalf("a claim after the clean: %v", err)
	}
}
