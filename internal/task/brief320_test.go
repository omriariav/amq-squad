package task

import (
	"errors"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/flock"
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

func TestPreUpgradeLeaseIsNormalizedExactlyOnce(t *testing.T) {
	s := newStore(t)
	tk := mustAdd(t, s, AddInput{Title: "legacy"})
	claimed, err := s.Claim(tk.ID, "dev", nil, DefaultLeaseDuration, t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.mutate(tk.ID, t0.Add(time.Minute), func(tk *Task, _ *tasks) error {
		tk.Budget = 0
		tk.Lease.Attempt = 0
		tk.Lease.BudgetEndsAt = time.Time{}
		tk.Lease.ExpiresAt = t0.Add(2 * DefaultTaskBudget)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	n, err := s.NormalizeLeases(t0.Add(2 * time.Minute))
	if err != nil || len(n) != 1 || n[0].TaskID != tk.ID || n[0].Attempt != claimed.Attempt || !n[0].BudgetEndsAt.Equal(t0.Add(DefaultTaskBudget)) {
		t.Fatalf("normalization = %+v, err %v", n, err)
	}
	got, _ := s.Get(tk.ID)
	if got.Budget != DefaultTaskBudget || !got.Lease.ExpiresAt.Equal(t0.Add(DefaultTaskBudget)) {
		t.Fatalf("normalized lease = %+v, budget %s", got.Lease, got.Budget)
	}
	if again, err := s.NormalizeLeases(t0.Add(3 * time.Minute)); err != nil || len(again) != 0 {
		t.Fatalf("second normalization = %+v, err %v", again, err)
	}
}

func TestNoLegacyLeaseAvoidsTheNormalizationWriteLock(t *testing.T) {
	s := newStore(t)
	tk := mustAdd(t, s, AddInput{Title: "current"})
	claimed, err := s.Claim(tk.ID, "dev", nil, DefaultLeaseDuration, t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, renewed, err := s.RenewAttempt("dev", claimed.Attempt, DefaultLeaseDuration, 0, t0.Add(time.Second)); err != nil || !renewed {
		t.Fatalf("seed renewal: renewed=%v err=%v", renewed, err)
	}
	lock, acquired, err := flock.TryExclusive(filepath.Join(s.Dir, ".lock"), true)
	if err != nil || !acquired {
		t.Skipf("exclusive locks unavailable: acquired=%v err=%v", acquired, err)
	}
	defer lock.Close()
	if got, err := s.NormalizeLeasesNoWait(t0.Add(2 * time.Second)); err != nil || len(got) != 0 {
		t.Fatalf("current leases attempted the busy write lock: normalization=%v err=%v", got, err)
	}
	if _, renewed, err := s.RenewNoWaitActivity("dev", t0.Add(2*time.Second), DefaultLeaseDuration, time.Minute, t0.Add(2*time.Second)); err != nil || renewed {
		t.Fatalf("an event needing neither normalization nor renewal was skipped: renewed=%v err=%v", renewed, err)
	}
}

func TestEveryExportedRenewEntryPointBindsAnAttempt(t *testing.T) {
	typ := reflect.TypeOf(Store{})
	var got []string
	for i := 0; i < typ.NumMethod(); i++ {
		if name := typ.Method(i).Name; strings.HasPrefix(name, "Renew") {
			got = append(got, name)
		}
	}
	sort.Strings(got)
	want := []string{"RenewAttempt", "RenewNoWaitActivity", "RenewNoWaitAttempt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("exported renewal entry points = %v, want attempt-bound %v", got, want)
	}
}
