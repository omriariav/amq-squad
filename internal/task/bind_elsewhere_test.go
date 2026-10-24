package task

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// The second rebase of 2b-2 milestone B: 3-2's BindPR is the one binding,
// and a PR is never bound to two tasks (pr_bound_elsewhere, inside the same
// transaction). The rule is keyed on the repository and the number: another
// repository's PR with the same number binds; CompleteBound applies the same
// rule with the completion, and a refused completion writes nothing.
func TestBindPRRefusesAPRAnotherTaskHolds(t *testing.T) {
	s := newStore(t)
	a, _ := s.Add(AddInput{Title: "a", Class: "implement"}, t0)
	b, _ := s.Add(AddInput{Title: "b", Class: "implement"}, t0)
	head := strings.Repeat("a", 40)
	if _, err := s.BindPR(a.ID, "o/r", 7, head, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BindPR(b.ID, "O/R", 7, head, t0); !errors.Is(err, ErrPRBoundElsewhere) {
		t.Fatalf("a PR bound to %s was bound to %s: %v", a.ID, b.ID, err)
	}
	if _, err := s.BindPR(b.ID, "other/repo", 7, head, t0); err != nil {
		t.Fatalf("another repository's #7: %v", err)
	}
	if _, err := s.BindPR(a.ID, "o/r", 7, strings.Repeat("b", 40), t0); err != nil {
		t.Fatalf("the same task re-bound after a rebase: %v", err)
	}
	c, _ := s.Add(AddInput{Title: "c", Class: "implement", DeclaredFiles: []string{"a/"}}, t0)
	if _, err := s.Claim(c.ID, "s1", nil, time.Hour, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Done(c.ID, "s1", "ok", "", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteBound(c.ID, "o/r", 7, head, t0); !errors.Is(err, ErrPRBoundElsewhere) {
		t.Fatalf("CompleteBound took a PR bound elsewhere: %v", err)
	}
	if got, _ := s.Get(c.ID); got.State == StateCompleted || got.PRNumber != 0 {
		t.Fatalf("a refused completion wrote: %+v", got)
	}
}
