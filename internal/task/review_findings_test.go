package task

import (
	"errors"
	"testing"
	"time"
)

// Codex review of bc48a0e1, findings 2 to 5 (the store side).

// Finding 2: a widening must keep the file-lock exclusion the grant gave.
func TestFinding2WidenRefusesAnOverlapWithAnotherActiveTask(t *testing.T) {
	s := newStore(t)
	mustAdd(t, s, AddInput{Title: "a", DeclaredFiles: []string{"a.go"}})
	mustAdd(t, s, AddInput{Title: "b", DeclaredFiles: []string{"b.go"}})
	if _, err := s.Claim("t1", "seat-a", nil, time.Hour, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim("t2", "seat-b", nil, time.Hour, t0); err != nil {
		t.Fatal(err)
	}
	_, err := s.Widen("t1", "lead", "needs b.go too", []string{"b.go"}, 0, t0)
	var refusal *WidenRefusal
	if !errors.As(err, &refusal) || refusal.ReasonCode != RefusalFileLock || len(refusal.Conflicts) != 1 || refusal.Conflicts[0] != "t2" {
		t.Fatalf("widen onto an active task's files: %v", err)
	}
	if got, _ := s.Get("t1"); len(got.DeclaredFiles) != 1 || len(got.ScopeChanges) != 0 {
		t.Fatalf("a refused widening changed the task: %+v", got)
	}
	// The refused-report path: a commit refusal re-armed by a widening must
	// not re-arm into a conflict either.
	if _, err := s.Done("t1", "seat-a", "e", "", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RefuseCommit("t1", "outside_declared_files", "b.go", RouteLead, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Widen("t1", "lead", "b.go belongs to it", []string{"b.go"}, 0, t0); !errors.As(err, &refusal) {
		t.Fatalf("re-arm through a conflict: %v", err)
	}
	if got, _ := s.Get("t1"); got.State != StateBlocked || got.Lease != nil {
		t.Fatalf("a refused widening re-armed the commit: %+v", got)
	}
}

// Finding 3: an expired lease cannot be claimed, extended or reported on,
// even before the scheduler's next tick (now >= expires_at is expired).
func TestFinding3ExpiredLeaseCannotBeClaimedOrReported(t *testing.T) {
	s := newStore(t)
	mustAdd(t, s, AddInput{Title: "a"})
	mustAdd(t, s, AddInput{Title: "b"})
	if _, err := s.Grant("t1", "seat-a", nil, 10*time.Minute, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim("t2", "seat-b", nil, 10*time.Minute, t0); err != nil {
		t.Fatal(err)
	}
	at := t0.Add(10 * time.Minute) // exactly expires_at
	for name, call := range map[string]func() error{
		"claim by id":         func() error { _, err := s.Claim("t1", "seat-a", nil, time.Hour, at); return err },
		"claim the own lease": func() error { _, err := s.Claim("", "seat-a", nil, time.Hour, at); return err },
		"done":                func() error { _, err := s.Done("t2", "seat-b", "ok", "", at); return err },
		"fail":                func() error { _, err := s.Fail("t2", "seat-b", "x", at); return err },
		"block":               func() error { _, err := s.Block("t2", "seat-b", "seat_blocked", "", "", at); return err },
	} {
		if err := call(); !errors.Is(err, ErrLeaseExpired) {
			t.Errorf("%s at expires_at: %v, want ErrLeaseExpired", name, err)
		}
	}
	if got, _ := s.Get("t1"); got.Lease.ClaimedAt != nil || !got.Lease.ExpiresAt.Equal(at) {
		t.Fatalf("an expired lease was revived: %+v", got.Lease)
	}
	if got, _ := s.Get("t2"); got.State != StateInProgress || got.Report != nil {
		t.Fatalf("an expired lease was reported on: %+v", got)
	}
}

// Finding 4: a lock-free reader that interleaves with a whole transaction
// never sees a state that never existed.
func TestFinding4SnapshotNeverMixesTransactions(t *testing.T) {
	s := newStore(t)
	mustAdd(t, s, AddInput{Title: "a"})
	mustAdd(t, s, AddInput{Title: "b"})
	for _, id := range []string{"t1", "t2"} {
		if _, err := s.Grant(id, "seat-"+id, nil, time.Minute, t0); err != nil {
			t.Fatal(err)
		}
	}
	fired := false
	snapshotFileHook = func(name string) {
		if name == "t1.json" && !fired {
			fired = true // the reader has read t1; a writer expires t1 and t2 and removes the journal
			if exp, err := s.ExpireDue(t0.Add(time.Hour)); err != nil || len(exp) != 2 {
				t.Errorf("writer: %v %v", exp, err)
			}
		}
	}
	defer func() { snapshotFileHook = nil }()
	list, err := Snapshot(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if !fired || list[0].State != list[1].State {
		t.Fatalf("snapshot mixed two transactions: t1 %s, t2 %s (writer ran: %v)", list[0].State, list[1].State, fired)
	}
}

// Finding 5: the renewal interval is rechecked against the locked lease, and
// an older timestamp never shortens a committed extension.
func TestFinding5RenewalRechecksTheIntervalUnderTheLock(t *testing.T) {
	s := newStore(t)
	mustAdd(t, s, AddInput{Title: "a"})
	if _, err := s.Claim("t1", "h", nil, 10*time.Minute, t0); err != nil {
		t.Fatal(err)
	}
	aAt := t0.Add(70 * time.Second)
	renewAfterPrecheck = func() {
		renewAfterPrecheck = nil
		if _, renewed, err := s.Renew("h", 10*time.Minute, time.Minute, aAt); err != nil || !renewed {
			t.Errorf("process A renewal: %v %v", renewed, err)
		}
	}
	defer func() { renewAfterPrecheck = nil }()
	// Process B read the same due renewal (precheck at T+65 s against the
	// claim at T) and takes the lock after A committed.
	_, renewed, err := s.Renew("h", 10*time.Minute, time.Minute, t0.Add(65*time.Second))
	if err != nil || renewed {
		t.Fatalf("B renewed inside the interval after A: renewed %v err %v", renewed, err)
	}
	got, _ := s.Get("t1")
	if !got.Lease.ExpiresAt.Equal(aAt.Add(10*time.Minute)) || !got.Lease.RenewedAt.Equal(aAt) || got.Lease.Renewals != 1 {
		t.Fatalf("lease after the race: %+v (want A's extension kept)", got.Lease)
	}
}
