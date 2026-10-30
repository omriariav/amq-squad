package task

import (
	"errors"
	"testing"
	"time"
)

// Amendment 26 review: a lead resets only its own squad's task and never an
// owner-routed block; the store checks the caller and the block route.
func TestResetByChecksCallerAndRoute(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	block := func(createdBy, route string) Task {
		t.Helper()
		a, err := s.Add(AddInput{Title: "x", Class: "implement", CreatedBy: createdBy}, now)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Claim(a.ID, "pi-1", nil, DefaultLeaseDuration, now); err != nil {
			t.Fatal(err)
		}
		b, err := s.Block(a.ID, "pi-1", "outside_declared_files", "calc.go", route, now)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	own := block("pi-lead", RouteLead)
	if r, err := s.ResetBy(own.ID, "pi-lead", nil, now); err != nil || r.State != StatePending {
		t.Fatalf("the lead could not reset its own refused task: %v %+v", err, r)
	}
	other := block("other-lead", RouteLead)
	if _, err := s.ResetBy(other.ID, "pi-lead", nil, now); !errors.Is(err, ErrResetRefused) {
		t.Fatalf("a lead reset another squad's task: %v", err)
	}
	owner := block("pi-lead", RouteOwner)
	if _, err := s.ResetBy(owner.ID, "pi-lead", nil, now); !errors.Is(err, ErrResetRefused) {
		t.Fatalf("a lead reset an owner-routed block: %v", err)
	}
	if _, err := s.ResetBy(own.ID, "", nil, now); !errors.Is(err, ErrResetRefused) {
		t.Fatalf("a reset without a caller was accepted: %v", err)
	}
	legacy := block("", RouteLead)
	if _, err := s.ResetBy(legacy.ID, "pi-lead", nil, now); err != nil {
		t.Fatalf("a task without a recorded creator could not be reset: %v", err)
	}
}
