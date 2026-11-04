package mergetrain

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestMismatchedReconcileContinuesWithOtherPRs(t *testing.T) {
	f := newFixture(t)
	staleHead, goodHead := f.head("pr1"), f.head("pr2")
	staleTask := f.bindTask(1, staleHead, []string{"pkg/a.go"})
	goodTask := f.bindTask(2, goodHead, []string{"pkg/b.go", "shared.txt"})
	s := f.tasks()
	req := Request{Schema: 1, Repository: fixtureRepo, Base: "main", PRs: []Item{{Number: 1}, {Number: 2}}}
	f.realRun(&req)
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

func TestStaleJournalIntentIsAuditedBeforeForgeCall(t *testing.T) {
	f := newFixture(t)
	oldHead, boundHead := f.head("pr1"), f.head("pr2")
	id := f.bindTask(1, boundHead, []string{"pkg/b.go", "shared.txt"})
	f.edit(func(s *fakeState) { s.PRs["1"].Head, s.PRs["1"].State = "pr2", "CLOSED" })
	req := Request{Schema: 1, Repository: fixtureRepo, Base: "main", PRs: []Item{{Number: 1}}}
	f.realRun(&req)
	if err := f.train.Audit.Append(Entry{Kind: "merge_intent", Repository: fixtureRepo, Base: "main", PR: 1, Task: id, Head: oldHead}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.train.Run(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if reads := strings.Count(f.calls(), "pr view 1 "); reads != 2 {
		t.Fatalf("stale intent reached the forge before its audit: %d PR reads", reads)
	}
}

func TestPart2AHandMergedPRKeepsBaseBehavior(t *testing.T) {
	f := newFixture(t)
	head, commit := f.head("pr1"), f.head("main")
	f.edit(func(s *fakeState) { s.PRs["1"].State, s.PRs["1"].MergeCommit = "MERGED", commit })
	id := f.bindTask(1, head, []string{"pkg/a.go"})
	req := Request{Schema: 1, Repository: fixtureRepo, Base: "main", PRs: []Item{{Number: 1}}}
	f.realRun(&req)
	if _, err := f.train.Run(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	got, _ := f.tasks().Get(id)
	if got.PRMergeHead != "" || got.PRMergeCommit != "" {
		t.Fatalf("part 2a recorded a hand merge: %+v", got)
	}
	if strings.Contains(f.calls(), "-X PUT") {
		t.Fatalf("part 2a called merge for a hand-merged PR:\n%s", f.calls())
	}
}
