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

func TestBoundDependencyWaitsForItsRecordedMerge(t *testing.T) {
	s := newStore(t)
	dep := mustAdd(t, s, AddInput{Title: "dependency"})
	head, merged := strings.Repeat("a", 40), strings.Repeat("b", 40)
	if _, err := s.BindPR(dep.ID, "o/r", 7, head, t0); err != nil {
		t.Fatal(err)
	}
	child := mustAdd(t, s, AddInput{Title: "child", DependsOn: []string{dep.ID}})
	if _, err := s.Claim(dep.ID, "impl-1", nil, time.Minute, t0); err != nil {
		t.Fatal(err)
	}
	harnessPass(t, s, dep.ID, t0)
	if _, err := s.Done(dep.ID, "impl-1", "done", head, t0); err != nil {
		t.Fatal(err)
	}
	want := "waiting for t1's PR #7 to merge"
	all, _ := s.List()
	if got := Blocker(child, all); got != want {
		t.Fatalf("completed bound dependency blocker = %q, want %q", got, want)
	}
	if _, err := s.RecordPRMerge(dep.ID, "o/r", 7, merged, t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	all, _ = s.List()
	if got := Blocker(child, all); got != "" {
		t.Fatalf("recorded merge did not release dependent: %q", got)
	}
	if _, err := s.RecordPRMerge(dep.ID, "o/r", 7, strings.Repeat("c", 40), t0); err == nil {
		t.Fatal("a conflicting merge commit replaced the recorded fact")
	}
	if _, err := s.BindPR(dep.ID, "o/r", 7, strings.Repeat("d", 40), t0); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(dep.ID); got.PRMergeCommit != "" {
		t.Fatalf("a changed binding retained stale merge proof: %+v", got)
	}
}

func TestCancelledBoundDependencyStaysWaiting(t *testing.T) {
	s := newStore(t)
	dep := mustAdd(t, s, AddInput{Title: "dependency"})
	if _, err := s.BindPR(dep.ID, "o/r", 12, strings.Repeat("a", 40), t0); err != nil {
		t.Fatal(err)
	}
	child := mustAdd(t, s, AddInput{Title: "child", DependsOn: []string{dep.ID}})
	if _, err := s.Cancel(dep.ID, "lead", "abandoned", t0); err != nil {
		t.Fatal(err)
	}
	all, _ := s.List()
	if got, want := Blocker(child, all), "waiting for t1's PR #12 to merge"; got != want {
		t.Fatalf("cancelled bound dependency blocker = %q, want %q", got, want)
	}
}
