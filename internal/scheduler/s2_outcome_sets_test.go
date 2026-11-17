package scheduler

import (
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// S2-1 (3.3.0): the three outcome sets task.OutcomeSets declares with a
// reader in this package, each tied to its reader. Every one of the eight
// outcomes is given to the reader in one ended attempt; the reader's answer
// must equal the declared membership, each conditional entry driven both
// ways.

func s2Set(t *testing.T, name, reader string, conditionals int) task.OutcomeSetContract {
	t.Helper()
	for _, set := range task.OutcomeSets() {
		if set.Name == name {
			if set.ReaderPackage != "internal/scheduler" || set.ReaderFunc != reader || len(set.Conditional) != conditionals {
				t.Fatalf("outcome set %s: reader %s.%s with %d conditionals, want internal/scheduler.%s with %d", name, set.ReaderPackage, set.ReaderFunc, len(set.Conditional), reader, conditionals)
			}
			return set
		}
	}
	t.Fatalf("no outcome set %s", name)
	return task.OutcomeSetContract{}
}

func s2In(values []string, v string) bool {
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
}

var s2T0 = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

func s2Ended(outcome string, granted time.Time) task.Task {
	return task.Task{ID: "t1", State: task.StatePending, Pool: &sonnet, Attempt: 1, Attempts: []task.Attempt{{
		N: 1, Holder: "seat-a", Pool: &sonnet, GrantedAt: granted, EndedAt: granted.Add(time.Minute), Outcome: outcome,
	}}}
}

// retry_budget: DefaultRouter.Route counts an attempt toward MaxAttempts
// exactly when its outcome is in the set (MaxAttempts 1: one counted
// attempt blocks the task).
func TestS2RetryBudgetSetIsItsReader(t *testing.T) {
	set := s2Set(t, "retry_budget", "DefaultRouter.Route", 0)
	for _, outcome := range task.Outcomes() {
		r := DefaultRouter{MaxAttempts: 1}.Route(s2Ended(outcome, s2T0))
		if got, want := r.Block == "retries_exhausted", s2In(set.Outcomes, outcome); got != want {
			t.Errorf("Route after one %s attempt blocks: %v, the set says %v (route %+v)", outcome, got, want, r)
		}
	}
}

type s2Exits struct{}

func (s2Exits) CheckExit(string, string, string) ExitResult { return ExitResult{} }

type s2Cleaner struct{}

func (s2Cleaner) PrepareSeat(string) error                       { return nil }
func (s2Cleaner) CleanAttempt(string, string, int, string) error { return nil }

// exit_scan: scanAttemptsLog queues an item for an ended attempt exactly when
// its outcome is in the set, driven both ways for each part of the
// conditional entry: with no Preparer and with a Preparer that cleans
// attempts (the squad's), and with the attempt recorded cleaned or not. A
// cancelled attempt not yet cleaned, under a cleaner, is queued as a
// close-and-clean item (exits.go:479-521, 3.2.0-2b-1 item 1). The condition's text is compared with the one this test
// drives.
func TestS2ExitScanSetIsItsReader(t *testing.T) {
	set := s2Set(t, "exit_scan", "scanAttemptsLog", 1)
	const cleans = "the scheduler's Preparer cleans attempts (AttemptCleaner) and the attempt is not recorded cleaned (CleanedAt unset); it is queued as a close-and-clean item"
	if c := set.Conditional[0]; c.Outcome != task.OutcomeCancelled || c.Condition != cleans {
		t.Fatalf("exit_scan's conditional entry is %+v; this test drives cancelled on %q", c, cleans)
	}
	for _, cleaner := range []bool{false, true} {
		for _, cleaned := range []bool{false, true} {
			for _, outcome := range task.Outcomes() {
				s := &Scheduler{Exits: s2Exits{}}
				if cleaner {
					s.Preparer = s2Cleaner{}
				}
				ended := s2Ended(outcome, s2T0)
				if cleaned {
					at := s2T0.Add(2 * time.Minute)
					ended.Attempts[0].CleanedAt = &at
				}
				if err := s.scanAttempts([]task.Task{ended}, s2T0.Add(time.Hour)); err != nil {
					t.Fatal(err)
				}
				want := s2In(set.Outcomes, outcome) && (outcome != task.OutcomeCancelled || cleaner && !cleaned)
				if got := len(s.exitQueue) == 1; got != want {
					t.Errorf("cleaner %v, cleaned %v: a %s attempt queued an item: %v, the set says %v (queue %+v)", cleaner, cleaned, outcome, got, want, s.exitQueue)
				}
				if got := len(s.exitQueue) == 1 && s.exitQueue[0].Close; got != (outcome == task.OutcomeCancelled && cleaner && !cleaned) {
					t.Errorf("cleaner %v, cleaned %v: a %s attempt's item is a close: %v", cleaner, cleaned, outcome, got)
				}
			}
		}
	}
}

// seat_lapsed: idleSeatExcept passes over the attempt's holder exactly when
// the outcome is in the set, for an attempt granted in the seat's current
// join; an attempt granted before the seat joined lapses no outcome.
func TestS2SeatLapsedSetIsItsReader(t *testing.T) {
	set := s2Set(t, "seat_lapsed", "idleSeatExcept", 0)
	for _, outcome := range task.Outcomes() {
		for _, c := range []struct {
			name    string
			joined  time.Time
			granted time.Time
			counts  bool
		}{
			{"no join recorded", time.Time{}, s2T0, true},
			{"granted in this join", s2T0, s2T0.Add(time.Second), true},
			{"granted before this join", s2T0.Add(time.Hour), s2T0, false},
		} {
			s := &Scheduler{seats: map[string]*seat{"seat-a": {pool: sonnet, joinedAt: c.joined}}}
			tk := s2Ended(outcome, c.granted)
			got := s.idleSeatExcept([]task.Task{tk}, tk, sonnet, nil) == ""
			if want := c.counts && s2In(set.Outcomes, outcome); got != want {
				t.Errorf("%s, %s: the holder is passed over: %v, the set says %v", outcome, c.name, got, want)
			}
		}
	}
}
