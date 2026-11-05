package task

import (
	"errors"
	"testing"
	"time"
)

// Brief 3.2.0 row 2: both the waiting and no-wait renewal paths obey the
// store's absolute task budget. Reverting the bound lets either lease run
// past t0+budget.
func TestActivityRenewalIsBoundedByTaskBudgetOnBothPaths(t *testing.T) {
	for _, tc := range []struct {
		name  string
		renew func(Store, string, int, time.Duration, time.Duration, time.Time) (Task, bool, error)
	}{
		{"renew", func(s Store, h string, a int, d, m time.Duration, n time.Time) (Task, bool, error) {
			return s.RenewAttempt(h, a, d, m, n)
		}},
		{"renew no wait", func(s Store, h string, a int, d, m time.Duration, n time.Time) (Task, bool, error) {
			return s.RenewNoWaitAttempt(h, a, d, m, n)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			tk := mustAdd(t, s, AddInput{Title: "bounded", Budget: 25 * time.Minute})
			claimed, err := s.Claim(tk.ID, "dev", nil, DefaultLeaseDuration, t0)
			if err != nil {
				t.Fatal(err)
			}
			var got Task
			var renewed bool
			for _, at := range []time.Duration{9 * time.Minute, 18 * time.Minute, 24 * time.Minute} {
				got, renewed, err = tc.renew(s, "dev", claimed.Attempt, DefaultLeaseDuration, 0, t0.Add(at))
				if err != nil || !renewed {
					t.Fatalf("renew at %s: renewed=%v lease=%+v err=%v", at, renewed, got.Lease, err)
				}
			}
			if !got.Lease.ExpiresAt.Equal(t0.Add(25 * time.Minute)) {
				t.Fatalf("lease was not capped at budget: %+v", got.Lease)
			}
			if _, renewed, err = tc.renew(s, "dev", claimed.Attempt, DefaultLeaseDuration, 0, t0.Add(25*time.Minute)); renewed || !errors.Is(err, ErrBudgetSpent) {
				t.Fatalf("renew at budget end: renewed=%v err=%v", renewed, err)
			}
		})
	}
}

// Brief 3.2.0 row 3: activity is tied to the attempt observed by the runtime.
// Late and earlier-attempt activity can neither revive nor extend a lease.
func TestActivityRenewalIsAttemptBound(t *testing.T) {
	s := newStore(t)
	tk := mustAdd(t, s, AddInput{Title: "attempt", Budget: time.Hour})
	claimed, err := s.Claim(tk.ID, "dev", nil, DefaultLeaseDuration, t0)
	if err != nil {
		t.Fatal(err)
	}
	before := claimed.Lease.ExpiresAt
	if _, renewed, err := s.RenewAttempt("dev", claimed.Attempt+1, DefaultLeaseDuration, 0, t0.Add(time.Minute)); renewed || !errors.Is(err, ErrNoLease) {
		t.Fatalf("wrong attempt renewed=%v err=%v", renewed, err)
	}
	got, _ := s.Get(tk.ID)
	if !got.Lease.ExpiresAt.Equal(before) || got.Lease.Renewals != 0 {
		t.Fatalf("wrong attempt changed lease: %+v", got.Lease)
	}
	if _, renewed, err := s.RenewAttempt("dev", claimed.Attempt, DefaultLeaseDuration, 0, before); renewed || !errors.Is(err, ErrNoLease) {
		t.Fatalf("activity at expiry renewed=%v err=%v", renewed, err)
	}
}

func TestLeadCanExtendTaskBudget(t *testing.T) {
	s := newStore(t)
	tk := mustAdd(t, s, AddInput{Title: "extend", Budget: 20 * time.Minute})
	claimed, err := s.Claim(tk.ID, "dev", nil, DefaultLeaseDuration, t0)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.ExtendBudget(tk.ID, "cto", 30*time.Minute, t0.Add(5*time.Minute))
	if err != nil || got.Budget != 50*time.Minute || !got.Lease.BudgetEndsAt.Equal(t0.Add(50*time.Minute)) || len(got.BudgetExtensions) != 1 {
		t.Fatalf("extend: %+v err=%v", got, err)
	}
	for _, at := range []time.Duration{9, 18, 27, 36, 45} {
		if _, renewed, err := s.RenewAttempt("dev", claimed.Attempt, DefaultLeaseDuration, 0, t0.Add(at*time.Minute)); err != nil || !renewed {
			t.Fatalf("renew at %dm inside extension: renewed=%v err=%v", at, renewed, err)
		}
	}
}
