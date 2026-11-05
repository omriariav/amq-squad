package task

import (
	"errors"
	"testing"
	"time"
)

func TestClaimOfferedAcceptsOnlyTheLiveOfferForItsHolderAndPool(t *testing.T) {
	s := newStore(t)
	sol := &Pool{Runtime: "codex", Model: "gpt-5.6-sol", Effort: "high"}
	terra := &Pool{Runtime: "codex", Model: "gpt-5.6-terra", Effort: "medium"}
	a := mustAdd(t, s, AddInput{Title: "offered", Pool: sol})
	if _, err := s.Grant(a.ID, "seat-a", sol, time.Minute, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimOffered(a.ID, "seat-b", sol, time.Minute, t0.Add(time.Second)); !errors.Is(err, ErrClaimOfferRequired) {
		t.Fatalf("seat B claimed seat A's offer: %v", err)
	}
	if _, err := s.ClaimOffered(a.ID, "seat-a", terra, time.Minute, t0.Add(time.Second)); !errors.Is(err, ErrClaimPoolMismatch) {
		t.Fatalf("an out-of-pool seat claimed the offer: %v", err)
	}
	got, err := s.ClaimOffered(a.ID, "seat-a", sol, time.Minute, t0.Add(time.Second))
	if err != nil || got.Lease == nil || got.Lease.ClaimedAt == nil || got.Pool == nil || *got.Pool != *sol {
		t.Fatalf("seat A's offered claim: %+v, %v", got, err)
	}
}

func TestClaimOfferedRefusesAnExpiredOfferAndActivityNeverRenewsIt(t *testing.T) {
	s := newStore(t)
	a := mustAdd(t, s, AddInput{Title: "offer"})
	if _, err := s.Grant(a.ID, "seat-a", nil, time.Minute, t0); err != nil {
		t.Fatal(err)
	}
	if _, renewed, err := s.RenewNoWaitActivity("seat-a", t0.Add(time.Second), time.Hour, 0, t0.Add(30*time.Second)); !errors.Is(err, ErrNoLease) || renewed {
		t.Fatalf("activity renewed an unclaimed offer: renewed %v, err %v", renewed, err)
	}
	if _, err := s.ClaimOffered(a.ID, "seat-a", nil, time.Hour, t0.Add(time.Minute)); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("claim at expires_at: %v", err)
	}
	got, _ := s.Get(a.ID)
	if got.Lease == nil || got.Lease.ClaimedAt != nil || !got.Lease.ExpiresAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("expired offer changed: %+v", got.Lease)
	}
}

func TestClaimOfferedRefusesEveryNonOfferWithTheNamedSentinel(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, Store, Task) Task
	}{
		{"pending without lease", func(_ *testing.T, _ Store, a Task) Task { return a }},
		{"waiting", func(t *testing.T, s Store, a Task) Task {
			if _, err := s.SetWait(a.ID, WaitDependencies, t0); err != nil {
				t.Fatal(err)
			}
			return a
		}},
		{"blocked", func(t *testing.T, s Store, a Task) Task {
			if _, err := s.mutate(a.ID, t0, func(task *Task, _ *tasks) error {
				task.State = StateBlocked
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			return a
		}},
		{"already claimed", func(t *testing.T, s Store, a Task) Task {
			if _, err := s.Claim(a.ID, "seat-a", nil, time.Minute, t0); err != nil {
				t.Fatal(err)
			}
			return a
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore(t)
			a := tt.setup(t, s, mustAdd(t, s, AddInput{Title: tt.name}))
			if _, err := s.ClaimOffered(a.ID, "seat-a", nil, time.Minute, t0.Add(time.Second)); !errors.Is(err, ErrClaimOfferRequired) {
				t.Fatalf("ClaimOffered refusal = %v, want %s", err, RefusalClaimOfferRequired)
			}
		})
	}
}

func TestClaimServingFailsClosedOnUnknownPoolButClaimsUnpinnedWork(t *testing.T) {
	s := newStore(t)
	sol := &Pool{Runtime: "codex", Model: "gpt-5.6-sol", Effort: "high"}
	pinned := mustAdd(t, s, AddInput{Title: "pinned", Pool: sol})
	if _, err := s.ClaimServing(pinned.ID, "seat-a", nil, time.Minute, t0); !errors.Is(err, ErrClaimPoolUnknown) {
		t.Fatalf("unknown pool claimed pinned task: %v", err)
	}
	unpinned := mustAdd(t, s, AddInput{Title: "unpinned"})
	got, err := s.ClaimServing(unpinned.ID, "seat-a", nil, time.Minute, t0)
	if err != nil || got.ID != unpinned.ID || got.Lease == nil || got.Lease.ClaimedAt == nil {
		t.Fatalf("unknown pool did not claim unpinned task: %+v, %v", got, err)
	}
}

func TestClaimServingChecksAConcurrentRepinInsideTheClaimUpdate(t *testing.T) {
	s := newStore(t)
	sol := &Pool{Runtime: "codex", Model: "gpt-5.6-sol", Effort: "high"}
	terra := &Pool{Runtime: "codex", Model: "gpt-5.6-terra", Effort: "medium"}
	a := mustAdd(t, s, AddInput{Title: "re-pinned", Pool: sol})
	if before, err := s.Get(a.ID); err != nil || before.Pool == nil || *before.Pool != *sol {
		t.Fatalf("compatibility pre-read: %+v, %v", before, err)
	}
	if _, err := s.Set(a.ID, nil, terra, t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimServing(a.ID, "seat-a", sol, time.Minute, t0.Add(2*time.Second)); !errors.Is(err, ErrClaimPoolMismatch) {
		t.Fatalf("claim after re-pin = %v, want %s", err, RefusalClaimPoolMismatch)
	}
	got, _ := s.Get(a.ID)
	if got.State != StatePending || got.Pool == nil || *got.Pool != *terra || got.Lease != nil {
		t.Fatalf("refused claim changed the re-pinned task: %+v", got)
	}
}

func TestClaimServingWithoutIDPullsOnlyWorkTheSeatServes(t *testing.T) {
	s := newStore(t)
	sol := &Pool{Runtime: "codex", Model: "gpt-5.6-sol", Effort: "high"}
	terra := &Pool{Runtime: "codex", Model: "gpt-5.6-terra", Effort: "medium"}
	other := mustAdd(t, s, AddInput{Title: "other", Pool: terra, Priority: 2})
	want := mustAdd(t, s, AddInput{Title: "served", Pool: sol, Priority: 1})
	got, err := s.ClaimServing("", "seat-a", sol, time.Minute, t0)
	if err != nil || got.ID != want.ID {
		t.Fatalf("pull selected %+v, %v; want %s", got, err, want.ID)
	}
	left, _ := s.Get(other.ID)
	if left.State != StatePending || left.Lease != nil {
		t.Fatalf("pull changed out-of-pool task: %+v", left)
	}
}

func TestClaimNeverMovesAnExistingTaskPool(t *testing.T) {
	s := newStore(t)
	sol := &Pool{Runtime: "codex", Model: "gpt-5.6-sol", Effort: "high"}
	terra := &Pool{Runtime: "codex", Model: "gpt-5.6-terra", Effort: "medium"}
	a := mustAdd(t, s, AddInput{Title: "pinned", Pool: sol})
	if _, err := s.Claim(a.ID, "seat-a", terra, time.Minute, t0); !errors.Is(err, ErrClaimPoolMismatch) {
		t.Fatalf("out-of-pool claim: %v", err)
	}
	got, _ := s.Get(a.ID)
	if got.State != StatePending || got.Pool == nil || *got.Pool != *sol {
		t.Fatalf("refused claim moved the task: %+v", got)
	}

	piTask := &Pool{Runtime: "pi", Model: "qwen", Effort: "high"}
	piSeat := &Pool{Runtime: "pi"}
	b := mustAdd(t, s, AddInput{Title: "served by generic pi", Pool: piTask})
	claimed, err := s.Claim(b.ID, "seat-pi", piSeat, time.Minute, t0)
	if err != nil {
		t.Fatalf("compatible serving pool claim: %v", err)
	}
	if claimed.Pool == nil || *claimed.Pool != *piTask {
		t.Fatalf("compatible claim moved the task pool: %+v", claimed.Pool)
	}
}
