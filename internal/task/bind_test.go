package task

import (
	"strings"
	"testing"
	"time"
)

// 3-2 fix 4, item 5: BindPR writes the PR binding inside the store's
// transaction and the fields survive later mutations (a lease grant, claim
// and renewal).
func TestBindPRSurvivesMutations(t *testing.T) {
	s := newStore(t)
	a := mustAdd(t, s, AddInput{Title: "a", DeclaredFiles: []string{"x.go"}})
	head := strings.Repeat("a", 40)
	if _, err := s.BindPR(a.ID, "o/r", 7, head, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Grant(a.ID, "impl-1", nil, time.Minute, t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(a.ID, "impl-1", nil, time.Minute, t0.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Renew("impl-1", time.Minute, 0, t0.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(a.ID)
	if err != nil || got.PRRepository != "o/r" || got.PRNumber != 7 || got.PRHead != head || got.Lease == nil || got.Lease.RenewedAt == nil {
		t.Fatalf("binding or lease lost: %v %+v", err, got)
	}
	for _, bad := range []struct {
		repo string
		pr   int
		head string
	}{{"nope", 7, head}, {"o/r", 0, head}, {"o/r", 7, "abc"}} {
		if _, err := s.BindPR(a.ID, bad.repo, bad.pr, bad.head, t0); err == nil {
			t.Errorf("BindPR(%q, %d, %q) accepted", bad.repo, bad.pr, bad.head)
		}
	}
}
