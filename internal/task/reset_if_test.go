package task

import (
	"errors"
	"testing"
)

// Codex on b250b951: the scheduler's release resets a task only while it
// is blocked for the reason it read. A block of another reason, a pending
// task, an unknown id and a block the caller's match refuses (the plan
// reviewer on af1a6124: the block must be the caller's own) are refused
// (ErrBlockChanged, ErrNotFound) and nothing moves; the reason it names,
// matched, is reset to pending with the block's fields cleared. Reverted
// (the reason not checked), the other block is cleared.
func TestResetIfBlockedResetsOnlyItsReason(t *testing.T) {
	s := newStore(t)
	other := mustAdd(t, s, AddInput{Title: "other"})
	if _, err := s.Block(other.ID, "lead", "lead_hold", "another decision", RouteLead, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResetIfBlocked(other.ID, "ledger_tampered", nil, t0); !errors.Is(err, ErrBlockChanged) {
		t.Fatalf("another block's reset: %v", err)
	}
	if got, _ := s.Get(other.ID); got.State != StateBlocked || got.BlockReason != "lead_hold" || got.BlockDetail != "another decision (by lead)" {
		t.Fatalf("the other block moved: %+v", got)
	}
	pending := mustAdd(t, s, AddInput{Title: "pending"})
	if _, err := s.ResetIfBlocked(pending.ID, "ledger_tampered", nil, t0); !errors.Is(err, ErrBlockChanged) {
		t.Fatalf("a pending task's reset: %v", err)
	}
	if _, err := s.ResetIfBlocked("t-none", "ledger_tampered", nil, t0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unknown id: %v", err)
	}
	held := mustAdd(t, s, AddInput{Title: "held"})
	if _, err := s.Block(held.ID, "", "ledger_tampered", "seat s: quarantined", RouteLead, t0); err != nil {
		t.Fatal(err)
	}
	// the caller's match runs inside the mutation: another cause is refused
	if _, err := s.ResetIfBlocked(held.ID, "ledger_tampered", func(t Task) bool { return t.BlockDetail == "seat s: quarantined at another slot" }, t0); !errors.Is(err, ErrBlockChanged) {
		t.Fatalf("a block the match refuses: %v", err)
	}
	if got, _ := s.Get(held.ID); got.State != StateBlocked {
		t.Fatalf("a refused match moved the task: %+v", got)
	}
	got, err := s.ResetIfBlocked(held.ID, "ledger_tampered", func(t Task) bool { return t.BlockDetail == "seat s: quarantined" }, t0)
	if err != nil || got.State != StatePending || got.BlockReason != "" || got.BlockRoute != "" || got.BlockDetail != "" {
		t.Fatalf("the named block's reset: %v %+v", err, got)
	}
}
