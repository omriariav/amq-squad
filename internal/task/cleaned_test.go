package task

import (
	"strings"
	"testing"
	"time"
)

// 3.2.0-2b-1 (the plan reviewer's note 1 on Codex's P1): the record of a
// cancelled attempt's finished clean lives on the attempt itself. Setting
// it is idempotent (the first time stands); an attempt that did not end
// cancelled, or one that does not exist, refuses.
func TestMarkAttemptCleanedRecordsOnceOnTheCancelledAttempt(t *testing.T) {
	s := newStore(t)
	a := mustAdd(t, s, AddInput{Title: "cancelled", Class: "summarize"})
	if _, err := s.Claim(a.ID, "seat-1", nil, time.Hour, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Cancel(a.ID, "lead", "stop", t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(a.ID); got.Attempts[0].CleanedAt != nil {
		t.Fatalf("the cancel itself recorded a clean: %+v", got.Attempts[0])
	}
	got, err := s.MarkAttemptCleaned(a.ID, 1, t0.Add(2*time.Second))
	if err != nil || got.Attempts[0].CleanedAt == nil || !got.Attempts[0].CleanedAt.Equal(t0.Add(2*time.Second)) {
		t.Fatalf("mark = %+v, %v", got.Attempts[0], err)
	}
	again, err := s.MarkAttemptCleaned(a.ID, 1, t0.Add(3*time.Second))
	if err != nil || !again.Attempts[0].CleanedAt.Equal(t0.Add(2*time.Second)) {
		t.Fatalf("a second mark moved the record: %+v, %v", again.Attempts[0], err)
	}
	if _, err := s.MarkAttemptCleaned(a.ID, 2, t0); err == nil || !strings.Contains(err.Error(), "no attempt 2") {
		t.Fatalf("a missing attempt: %v", err)
	}
	b := mustAdd(t, s, AddInput{Title: "failed", Class: "summarize"})
	if _, err := s.Claim(b.ID, "seat-1", nil, time.Hour, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Fail(b.ID, "seat-1", "red", t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkAttemptCleaned(b.ID, 1, t0); err == nil || !strings.Contains(err.Error(), "not cancelled") {
		t.Fatalf("a failed attempt: %v", err)
	}
}
