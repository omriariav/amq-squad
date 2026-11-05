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
