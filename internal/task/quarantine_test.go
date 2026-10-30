package task

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// A quarantined seat (global_config_changed) gets no new lease, by grant or
// by pull, until the owner clears it; the refusal names the reason.
func TestQuarantinedSeatGetsNoLease(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	a, _ := s.Add(AddInput{Title: "a", Class: "implement"}, now)
	if _, err := s.Add(AddInput{Title: "b", Class: "implement"}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Quarantine(Quarantine{Handle: "s1", Reason: "global_config_changed", Detail: "~/.claude/settings.json changed", TaskID: "t9", At: now}); err != nil {
		t.Fatal(err)
	}
	_, err = s.Grant(a.ID, "s1", nil, time.Minute, now)
	if !errors.Is(err, ErrQuarantined) || !strings.Contains(err.Error(), "global_config_changed") {
		t.Fatalf("grant to a quarantined seat: %v", err)
	}
	if _, err := s.Claim("", "s1", nil, time.Minute, now); !errors.Is(err, ErrQuarantined) {
		t.Fatalf("pull by a quarantined seat: %v", err)
	}
	if got, _ := s.Get(a.ID); got.State != StatePending || got.Lease != nil {
		t.Fatalf("a refused grant changed the task: %+v", got)
	}
	if qs, _ := Quarantined(s.Dir); len(qs) != 1 || qs[0].Handle != "s1" || qs[0].TaskID != "t9" {
		t.Fatalf("quarantine list: %+v", qs)
	}
	if _, err := s.Grant(a.ID, "s2", nil, time.Minute, now); err != nil {
		t.Fatalf("another seat: %v", err)
	}
	if q, err := s.ClearQuarantine("s1"); err != nil || q == nil {
		t.Fatalf("clear: %+v %v", q, err)
	}
	if _, err := s.Claim("", "s1", nil, time.Minute, now); err != nil {
		t.Fatalf("after the clear: %v", err)
	}
}
