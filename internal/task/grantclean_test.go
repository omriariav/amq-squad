package task

import (
	"errors"
	"testing"
	"time"
)

// #909 (the plan reviewer's condition 2): the lead's grant refuses a seat
// with an uncleaned cancelled attempt inside the same locked mutation that
// grants, in the reviewer's words, and grants once the clean is recorded.
// Reverted (the check left out of the mutation), the grant lands over the
// cancelled attempt.
func TestGrantCleanRefusesAnUncleanedSeatInsideItsMutation(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	a, err := s.Add(AddInput{Title: "cancelled", Class: "script"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Grant(a.ID, "seat-1", nil, time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Cancel(a.ID, "lead", "stop", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	b, err := s.Add(AddInput{Title: "next", Class: "script"}, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.GrantClean(b.ID, "seat-1", nil, time.Hour, now.Add(2*time.Second))
	var uncleaned *UncleanedSeatError
	if !errors.As(err, &uncleaned) || err.Error() != "seat seat-1 holds an uncleaned cancelled attempt of t1; run seat clean --seat seat-1 first" {
		t.Fatalf("grant over an uncleaned cancellation: %v", err)
	}
	if got, _ := s.Get(b.ID); got.State != StatePending || got.Lease != nil {
		t.Fatalf("the refused grant changed the task: %+v", got)
	}
	if _, err := s.MarkAttemptCleaned(a.ID, 1, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GrantClean(b.ID, "seat-1", nil, time.Hour, now.Add(3*time.Second)); err != nil || got.Lease == nil || got.Lease.Holder != "seat-1" {
		t.Fatalf("the grant after the clean: %+v, %v", got.Lease, err)
	}
}

// A cancelled attempt stops blocking the seat after the seat has held and
// ended a later lease. The later lease is proof that the old cancellation is
// no longer the worktree occupant; it does not retroactively mark that old
// attempt cleaned. Without the LaterLease clause in UnresolvedCancellation,
// the third grant is refused as if the first attempt were still current.
func TestGrantCleanAllowsASeatThatWorkedPastAnUncleanedCancellation(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	first, err := s.Add(AddInput{Title: "cancelled", Class: "script"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Grant(first.ID, "seat-1", nil, time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Cancel(first.ID, "lead", "stop", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	second, err := s.Add(AddInput{Title: "later lease", Class: "script"}, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Grant(second.ID, "seat-1", nil, time.Hour, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Fail(second.ID, "seat-1", "finished later work", now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}

	third, err := s.Add(AddInput{Title: "third", Class: "script"}, now.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	granted, err := s.GrantClean(third.ID, "seat-1", nil, time.Hour, now.Add(4*time.Second))
	if err != nil || granted.Lease == nil || granted.Lease.Holder != "seat-1" {
		t.Fatalf("grant after the later lease: lease=%+v err=%v", granted.Lease, err)
	}
	old, err := s.Get(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(old.Attempts) != 1 || old.Attempts[0].CleanedAt != nil {
		t.Fatalf("the old cancelled attempt was changed: %+v", old.Attempts)
	}
}

// The plan reviewer's condition (c) on #909: a seat reserved for a
// cancelled attempt's close and clean (ReserveCancel) is refused by both
// grants inside their store mutations, the scheduler's (Grant) and the
// lead's (GrantClean), in the reviewer's words; the reservation is named
// before the uncleaned attempt. Released, the lead's grant names the
// uncleaned attempt; recorded clean, it grants. Reverted (the reserved
// marker read as any marker), the refusal is not the reviewer's words.
func TestAReservedSeatIsRefusedByBothGrantsInTheReviewersWords(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	a, err := s.Add(AddInput{Title: "cancelled", Class: "script"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Grant(a.ID, "seat-1", nil, time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Cancel(a.ID, "lead", "stop", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	b, err := s.Add(AddInput{Title: "next", Class: "script"}, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	later, nonce, _, err := s.ReserveCancel("seat-1", a.ID, 1, now.Add(2*time.Second))
	if err != nil || later != "" || nonce == "" {
		t.Fatalf("reserve = %q, %q, %v", later, nonce, err)
	}
	const words = "seat seat-1 is being closed and cleaned; dispatch again after it finishes"
	var reserved *ReservedSeatError
	if _, err := s.Grant(b.ID, "seat-1", nil, time.Hour, now.Add(2*time.Second)); !errors.As(err, &reserved) || err.Error() != words {
		t.Fatalf("the scheduler's grant of a reserved seat: %v", err)
	}
	if _, err := s.GrantClean(b.ID, "seat-1", nil, time.Hour, now.Add(2*time.Second)); !errors.As(err, &reserved) || err.Error() != words {
		t.Fatalf("the lead's grant of a reserved seat: %v", err)
	}
	if err := s.DispatchRefusal("seat-1"); err == nil || err.Error() != words {
		t.Fatalf("the dispatch's early refusal of a reserved seat: %v", err)
	}
	if err := s.ReleaseSeat("seat-1", nonce); err != nil {
		t.Fatal(err)
	}
	var uncleaned *UncleanedSeatError
	if _, err := s.GrantClean(b.ID, "seat-1", nil, time.Hour, now.Add(3*time.Second)); !errors.As(err, &uncleaned) {
		t.Fatalf("the lead's grant after the release: %v", err)
	}
	if _, err := s.MarkAttemptCleaned(a.ID, 1, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GrantClean(b.ID, "seat-1", nil, time.Hour, now.Add(4*time.Second)); err != nil || got.Lease == nil {
		t.Fatalf("the grant after the clean: %+v, %v", got.Lease, err)
	}
}
