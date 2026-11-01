package schedproc

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/flock"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
)

// Codex P2 on brief 31-1's lead lock: two starts at once. Exactly one takes
// the lock; the other, which may look before the winner has published
// lead.json, waits for the winner's fresh record and names the winner's
// pid and pane, never pid 0 or a previous holder's. Repeated, so the race
// window is exercised.
func TestTwoSimultaneousLeadStartsOneWinsTheOtherNamesIt(t *testing.T) {
	pid := os.Getpid()
	stamp, ok := procinfo.StartToken(pid)
	if !ok {
		t.Skip("no start stamp on this platform")
	}
	// The winner holds the window between the lock and its record open, so
	// the loser always looks before the record exists.
	afterLeadLock = func() { time.Sleep(50 * time.Millisecond) }
	t.Cleanup(func() { afterLeadLock = nil })
	for round := 0; round < 5; round++ {
		root := t.TempDir()
		var wg sync.WaitGroup
		start := make(chan struct{})
		locks := make([]*LeadLock, 2)
		errs := make([]error, 2)
		for i, pane := range []string{"%1", "%2"} {
			wg.Add(1)
			go func(i int, pane string) {
				defer wg.Done()
				<-start
				locks[i], errs[i] = AcquireLeadLock(root, LeadRecord{PID: pid, Start: stamp, Pane: pane})
			}(i, pane)
		}
		close(start)
		wg.Wait()
		winner := -1
		for i := range errs {
			if errs[i] == nil {
				if winner >= 0 {
					t.Fatalf("round %d: both starts took the lock", round)
				}
				winner = i
			}
		}
		if winner < 0 {
			t.Fatalf("round %d: neither start took the lock: %v", round, errs)
		}
		var running *LeadRunningError
		if !errors.As(errs[1-winner], &running) || running.Starting || running.Record.PID != pid || running.Record.Pane != []string{"%1", "%2"}[winner] {
			t.Fatalf("round %d: the loser's refusal %v does not name the winner %s", round, errs[1-winner], []string{"%1", "%2"}[winner])
		}
		locks[winner].Release()
	}
}

// A start that finds the lock held and only a previous holder's record (its
// pid gone), the holder not yet published, waits briefly, then refuses
// lead_running "starting, pid unknown", never the stale pid.
func TestALeadStillStartingIsRefusedAsStarting(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(Dir(root), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(Dir(root), LeadRecordName), LeadRecord{Schema: 1, PID: 4242, Start: "old", Pane: "%9", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	held, ok, err := flock.TryExclusive(filepath.Join(Dir(root), LeadLockName), true)
	if err != nil || !ok {
		t.Fatalf("hold the lock: %v %v", ok, err)
	}
	defer held.Close()
	prev := leadRecordWait
	leadRecordWait = 100 * time.Millisecond
	t.Cleanup(func() { leadRecordWait = prev })
	_, err = AcquireLeadLock(root, LeadRecord{PID: os.Getpid(), Start: "s", Pane: "%2"})
	var running *LeadRunningError
	if !errors.As(err, &running) || !running.Starting || running.Record.PID != 0 || err.Error() != ReasonLeadRunning+": a lead of "+root+" is starting, pid unknown; one lead per root" {
		t.Fatalf("a starting lead: %v", err)
	}
}
