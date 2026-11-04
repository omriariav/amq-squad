package mergetrain

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

func markMerged(f *fixture, pr int) (head, commit string) {
	f.t.Helper()
	head, commit = f.head("pr"+string(rune('0'+pr))), f.head("main")
	f.edit(func(s *fakeState) {
		s.PRs[string(rune('0'+pr))].State = "MERGED"
		s.PRs[string(rune('0'+pr))].MergeCommit = commit
	})
	return head, commit
}

func mergeCalls(f *fixture) int {
	return strings.Count(f.calls(), "-X PUT")
}

func TestStaleJournalMergeDoesNotReleaseAReboundDependency(t *testing.T) {
	f := newFixture(t)
	oldHead := f.head("pr1")
	dependency := f.bindTask(1, oldHead, []string{"pkg/a.go"})
	s := f.tasks()
	newHead := strings.Repeat("d", 40)
	if _, err := s.BindPR(dependency, fixtureRepo, 1, newHead, time.Now()); err != nil {
		t.Fatal(err)
	}
	child, err := s.Add(task.AddInput{Title: "after #1", Class: "implement", DependsOn: []string{dependency}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.train.Audit.Append(Entry{Kind: "merge", Repository: fixtureRepo, Base: "main", PR: 1, Task: dependency,
		Head: oldHead, Merged: strings.Repeat("e", 40)}); err != nil {
		t.Fatal(err)
	}
	req := Request{Schema: 1, Repository: fixtureRepo, Base: "main", PRs: []Item{{Number: 1}}}
	f.realRun(&req)
	if _, err := f.train.Run(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(dependency)
	if got.PRMergeHead != "" || got.PRMergeCommit != "" {
		t.Fatalf("stale journal merge was recorded: %+v", got)
	}
	all, _ := s.List()
	if why := task.Blocker(child, all); why != task.DependencyMergeWait(dependency, 1) {
		t.Fatalf("stale merge released dependent: %q", why)
	}
}

func TestMismatchedReconcileContinuesWithOtherPRs(t *testing.T) {
	f := newFixture(t)
	staleHead, goodHead := f.head("pr1"), f.head("pr2")
	staleTask := f.bindTask(1, staleHead, []string{"pkg/a.go"})
	goodTask := f.bindTask(2, goodHead, []string{"pkg/b.go", "shared.txt"})
	s := f.tasks()
	req := Request{Schema: 1, Repository: fixtureRepo, Base: "main", PRs: []Item{{Number: 1}, {Number: 2}}}
	f.realRun(&req)
	// The request cached #1's binding, then the task was rebound before the
	// journal replay. The stale replay must not abort reconciliation of #2.
	if _, err := s.BindPR(staleTask, fixtureRepo, 3, strings.Repeat("d", 40), time.Now()); err != nil {
		t.Fatal(err)
	}
	goodMerge := strings.Repeat("e", 40)
	for _, e := range []Entry{
		{Kind: "merge", Repository: fixtureRepo, Base: "main", PR: 1, Task: staleTask, Head: staleHead, Merged: strings.Repeat("c", 40)},
		{Kind: "merge", Repository: fixtureRepo, Base: "main", PR: 2, Task: goodTask, Head: goodHead, Merged: goodMerge},
	} {
		if err := f.train.Audit.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	res, err := f.train.Run(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(res.PRs, func(p PlanPR) bool { return p.Number == 2 && p.Status == "merged" && p.Merged == goodMerge }) {
		t.Fatalf("other PR was not reconciled: %+v", res.PRs)
	}
	good, _ := s.Get(goodTask)
	if good.PRMergeHead != goodHead || good.PRMergeCommit != goodMerge {
		t.Fatalf("other task merge was not recorded: %+v", good)
	}
}

func TestRealRunRecordsAHandMergedPRWithoutAMergeCall(t *testing.T) {
	f := newFixture(t)
	head, commit := markMerged(f, 1)
	id := f.bindTask(1, head, []string{"pkg/a.go"})
	req := Request{Schema: 1, Repository: fixtureRepo, Base: "main", PRs: []Item{{Number: 1}}}
	f.realRun(&req)
	res, err := f.train.Run(context.Background(), req)
	if err != nil || res.PRs[0].Status != "merged" || res.PRs[0].Merged != commit {
		t.Fatalf("hand merge was not reconciled: %v %+v", err, res.PRs)
	}
	got, _ := f.tasks().Get(id)
	if got.PRMergeHead != head || got.PRMergeCommit != commit {
		t.Fatalf("hand merge was not recorded: %+v", got)
	}
	if calls := mergeCalls(f); calls != 0 {
		t.Fatalf("hand-merged PR got %d merge calls:\n%s", calls, f.calls())
	}
}

func TestServerMergedAtAnotherHeadIsAuditedAndNotRecorded(t *testing.T) {
	f := newFixture(t)
	serverHead, _ := markMerged(f, 1)
	boundHead := strings.Repeat("d", 40)
	id := f.bindTask(1, boundHead, []string{"pkg/a.go"})
	req := Request{Schema: 1, Repository: fixtureRepo, Base: "main", PRs: []Item{{Number: 1}}}
	f.realRun(&req)
	if _, err := f.train.Run(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	got, _ := f.tasks().Get(id)
	if got.PRMergeHead != "" || got.PRMergeCommit != "" {
		t.Fatalf("mismatched server merge was recorded: %+v", got)
	}
	entries, _ := f.train.Audit.Read()
	if !slices.ContainsFunc(entries, func(e Entry) bool {
		return e.Kind == "reconcile" && e.PR == 1 && e.Task == id && e.Head == serverHead && e.NewHead == boundHead &&
			strings.Contains(e.Detail, serverHead) && strings.Contains(e.Detail, boundHead)
	}) {
		t.Fatalf("mismatched heads were not audited: %+v", entries)
	}
}

func TestDryRunDoesNotRecordAnAlreadyMergedPR(t *testing.T) {
	f := newFixture(t)
	head, _ := markMerged(f, 1)
	id := f.bindTask(1, head, []string{"pkg/a.go"})
	req := Request{Schema: 1, Repository: fixtureRepo, Base: "main", PRs: []Item{{Number: 1}}}
	f.realRun(&req)
	f.train.DryRun = true
	before, _ := os.ReadFile(filepath.Join(f.tasks().Dir, id+".json"))
	if _, err := f.train.Run(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(f.tasks().Dir, id+".json"))
	if string(after) != string(before) {
		t.Fatal("dry run changed the task record")
	}
}

func TestRecordedMergeReconcileIsIdempotent(t *testing.T) {
	f := newFixture(t)
	head, _ := markMerged(f, 1)
	id := f.bindTask(1, head, []string{"pkg/a.go"})
	req := Request{Schema: 1, Repository: fixtureRepo, Base: "main", PRs: []Item{{Number: 1}}}
	f.realRun(&req)
	if _, err := f.train.Run(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.tasks().Dir, id+".json")
	before, _ := os.ReadFile(path)
	beforeCalls := mergeCalls(f)
	if _, err := f.train.Run(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("idempotent reconcile rewrote the task")
	}
	if calls := mergeCalls(f); calls != beforeCalls {
		t.Fatalf("idempotent reconcile made another merge call: %d -> %d", beforeCalls, calls)
	}
}
