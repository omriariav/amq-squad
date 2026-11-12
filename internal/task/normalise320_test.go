package task

import (
	"testing"
	"time"
)

var normalise320T = time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)

func legacy320Task(t *testing.T, s Store, title string, grantedAt, expiresAt time.Time) Task {
	t.Helper()
	tk, err := s.Add(AddInput{Title: title, Class: "implement"}, grantedAt)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.Claim(tk.ID, "dev-"+tk.ID, nil, DefaultLeaseDuration, grantedAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.mutate(tk.ID, grantedAt, func(tk *Task, _ *tasks) error {
		tk.Budget = 0
		tk.Lease.GrantedAt = grantedAt
		tk.Lease.ExpiresAt = expiresAt
		tk.Lease.Attempt = 0
		tk.Lease.BudgetEndsAt = time.Time{}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return claimed
}

func TestLegacyBudgetEndUsesLaterOfGrantBudgetAndOneLease(t *testing.T) {
	tests := []struct {
		name      string
		grantedAt time.Time
		wantEnd   time.Time
	}{
		{name: "old grant gets one lease", grantedAt: normalise320T.Add(-100 * time.Minute), wantEnd: normalise320T.Add(DefaultLeaseDuration)},
		{name: "young grant keeps its budget", grantedAt: normalise320T.Add(-5 * time.Minute), wantEnd: normalise320T.Add(85 * time.Minute)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore(t)
			claimed := legacy320Task(t, s, tt.name, tt.grantedAt, normalise320T.Add(5*time.Minute))
			n, err := s.NormalizeLeases(normalise320T)
			if err != nil || len(n) != 1 || !n[0].BudgetEndsAt.Equal(tt.wantEnd) {
				t.Fatalf("normalization = %+v, err %v; want budget end %s", n, err, tt.wantEnd)
			}
			got, err := s.Get(claimed.ID)
			if err != nil || !got.Lease.BudgetEndsAt.Equal(tt.wantEnd) || !got.Lease.ExpiresAt.Equal(normalise320T.Add(5*time.Minute)) {
				t.Fatalf("normalized lease = %+v, err %v", got.Lease, err)
			}
			if expired, err := s.ExpireDue(normalise320T); err != nil || len(expired) != 0 {
				t.Fatalf("first expiry pass = %+v, err %v", expired, err)
			}
		})
	}
}

func TestLegacyBudgetNormalizationIsIdempotentAcrossCallers(t *testing.T) {
	s := newStore(t)
	claimed := legacy320Task(t, s, "idempotent", normalise320T.Add(-100*time.Minute), normalise320T.Add(5*time.Minute))
	if n, err := s.NormalizeLeases(normalise320T); err != nil || len(n) != 1 {
		t.Fatalf("first normalization = %+v, err %v", n, err)
	}
	if n, err := s.NormalizeLeases(normalise320T.Add(3 * time.Minute)); err != nil || len(n) != 0 {
		t.Fatalf("second normalization = %+v, err %v", n, err)
	}
	if n, err := s.NormalizeLeasesNoWait(normalise320T.Add(4 * time.Minute)); err != nil || len(n) != 0 {
		t.Fatalf("no-wait normalization = %+v, err %v", n, err)
	}
	got, err := s.Get(claimed.ID)
	if err != nil || !got.Lease.BudgetEndsAt.Equal(normalise320T.Add(DefaultLeaseDuration)) {
		t.Fatalf("budget end moved: lease = %+v, err %v", got.Lease, err)
	}
}

func TestLegacyLeasePastItsOwnExpiryIsNotRevived(t *testing.T) {
	s := newStore(t)
	claimed := legacy320Task(t, s, "already expired", normalise320T.Add(-100*time.Minute), normalise320T.Add(-time.Minute))
	if n, err := s.NormalizeLeases(normalise320T); err != nil || len(n) != 1 {
		t.Fatalf("normalization = %+v, err %v", n, err)
	}
	expired, err := s.ExpireDue(normalise320T)
	if err != nil || len(expired) != 1 || expired[0].ID != claimed.ID || len(expired[0].Attempts) != 1 {
		t.Fatalf("expiry = %+v, err %v", expired, err)
	}
	want := "lease expired at " + normalise320T.Add(-time.Minute).Format(time.RFC3339)
	if expired[0].Attempts[0].Detail != want {
		t.Fatalf("expiry detail = %q, want %q", expired[0].Attempts[0].Detail, want)
	}
}

func TestLegacyGraceEndsUnlessTheLeadExtendsIt(t *testing.T) {
	s := newStore(t)
	oldGrant := normalise320T.Add(-100 * time.Minute)
	expires := normalise320T.Add(5 * time.Minute)
	plain := legacy320Task(t, s, "plain grace", oldGrant, expires)
	extended := legacy320Task(t, s, "extended grace", oldGrant, expires)
	if n, err := s.NormalizeLeases(normalise320T); err != nil || len(n) != 2 {
		t.Fatalf("normalization = %+v, err %v", n, err)
	}
	for _, tk := range []Task{plain, extended} {
		if _, renewed, err := s.RenewAttempt("dev-"+tk.ID, tk.Attempt, DefaultLeaseDuration, 0, normalise320T.Add(4*time.Minute)); err != nil || !renewed {
			t.Fatalf("renew %s inside grace: renewed=%v err=%v", tk.ID, renewed, err)
		}
	}
	if _, err := s.ExtendBudget(extended.ID, "cto", 30*time.Minute, normalise320T.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, renewed, err := s.RenewAttempt("dev-"+extended.ID, extended.Attempt, DefaultLeaseDuration, 0, normalise320T.Add(6*time.Minute)); err != nil || !renewed {
		t.Fatalf("renew extended task: renewed=%v err=%v", renewed, err)
	}
	expired, err := s.ExpireDue(normalise320T.Add(DefaultLeaseDuration))
	if err != nil || len(expired) != 1 || expired[0].ID != plain.ID || len(expired[0].Attempts) != 1 || expired[0].Attempts[0].Detail != "task budget of 1h30m0s spent" {
		t.Fatalf("expiry at grace end = %+v, err %v", expired, err)
	}
	got, err := s.Get(extended.ID)
	if err != nil || got.State != StateInProgress || !got.Lease.BudgetEndsAt.Equal(normalise320T.Add(40*time.Minute)) {
		t.Fatalf("extended task = %+v, err %v", got, err)
	}
}

func TestExtendBeforeNormalizationAddsToOneLeaseGrace(t *testing.T) {
	s := newStore(t)
	claimed := legacy320Task(t, s, "extend first", normalise320T.Add(-3*time.Hour), normalise320T.Add(5*time.Minute))
	got, err := s.ExtendBudget(claimed.ID, "cto", 30*time.Minute, normalise320T)
	if err != nil {
		t.Fatal(err)
	}
	want := normalise320T.Add(DefaultLeaseDuration + 30*time.Minute)
	if !got.Lease.BudgetEndsAt.Equal(want) {
		t.Fatalf("extended budget end = %s, want %s", got.Lease.BudgetEndsAt, want)
	}
	if n, err := s.NormalizeLeases(normalise320T.Add(time.Minute)); err != nil || len(n) != 1 || !n[0].BudgetEndsAt.Equal(want) {
		t.Fatalf("normalization after extension = %+v, err %v", n, err)
	}
}
