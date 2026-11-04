package mergetrain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Phase 5 Codex re-pass on 3-2: one test row per item.

// P1-2: the force-push timeline is read whole, page by page with the cursor:
// after 150 audited train rebases an unauthorized force push is still seen.
// A read the page bound or a rate limit leaves partial is history_incomplete.
func TestRepassForcePushTimelineIsReadWhole(t *testing.T) {
	setup := func(t *testing.T) *fixture {
		f := newFixture(t)
		var pushes []ForcePush
		for i := 1; i <= 150; i++ {
			after := fmt.Sprintf("%040x", i)
			if err := f.train.Audit.Append(Entry{Kind: "rebase", Repository: fixtureRepo, Base: "main", PR: 1, NewHead: after}); err != nil {
				t.Fatal(err)
			}
			pushes = append(pushes, ForcePush{Before: fmt.Sprintf("%040x", i+1000), After: after, Actor: "owner"})
		}
		pushes = append(pushes, ForcePush{Before: strings.Repeat("1", 40), After: f.head("pr1"), Actor: "seat"})
		f.edit(func(s *fakeState) { s.PRs["1"].ForcePushes = pushes })
		f.req.PRs = f.req.PRs[:1]
		return f
	}
	refusedFor := func(t *testing.T, f *fixture, rule string) {
		t.Helper()
		res, err := f.train.Run(context.Background(), f.req)
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if p := res.PRs[0]; p.Status != "refused" || !slices.Contains(rules(p.Refusals), rule) {
			t.Fatalf("PR 1: %s %v, want %s", p.Status, rules(p.Refusals), rule)
		}
		if got := mustGit(t, f.bare, "log", "--format=%s", "-1", "main"); got != "base" {
			t.Fatalf("main moved: %s", got)
		}
	}
	t.Run("whole timeline", func(t *testing.T) { refusedFor(t, setup(t), RuleForcePush) })
	t.Run("page bound", func(t *testing.T) {
		f := setup(t)
		f.gh.MaxTimelinePages = 1
		refusedFor(t, f, RuleHistoryIncomplete)
	})
	t.Run("rate limit", func(t *testing.T) {
		f := setup(t)
		f.edit(func(s *fakeState) { s.RateLimitPage = 2 })
		refusedFor(t, f, RuleHistoryIncomplete)
	})
}

// crashForge loses the merge response and then the connection: the PR
// read after the merge fails too, so the run stops with only the intent in
// the journal (a crash between the PUT and the audit append).
type crashForge struct {
	Forge
	merged bool
}

func (c *crashForge) Merge(ctx context.Context, repository string, number int, head string) (string, error) {
	_, err := c.Forge.Merge(ctx, repository, number, head)
	c.merged = true
	return "", errors.Join(errors.New("connection reset"), err)
}

func (c *crashForge) PR(ctx context.Context, repository string, number int) (PRInfo, error) {
	if c.merged {
		return PRInfo{}, errors.New("connection reset")
	}
	return c.Forge.PR(ctx, repository, number)
}

// P2-5: GitHub merges and the response is lost. The intent is journaled
// before the PUT; the same run reconciles the merge from the PR's state,
// and a run that crashed before it reconciles at its next start: the PR's
// last journal entry is its merge, never a refusal.
func TestRepassLostMergeResponseIsReconciled(t *testing.T) {
	lastFor := func(t *testing.T, f *fixture, pr int) Entry {
		t.Helper()
		entries, err := f.train.Audit.Read()
		if err != nil {
			t.Fatal(err)
		}
		var last Entry
		for _, e := range entries {
			if e.PR == pr && !e.DryRun {
				last = e
			}
		}
		return last
	}
	f := newFixture(t)
	f.req.PRs = f.req.PRs[:1]
	f.edit(func(s *fakeState) { s.LoseMergeResponse = true })
	res, err := f.train.Run(context.Background(), f.req)
	if err != nil || res.PRs[0].Status != "merged" || res.PRs[0].Merged != f.head("main") {
		t.Fatalf("same run: %v %+v", err, res.PRs)
	}
	if e := lastFor(t, f, 1); e.Kind != "merge" || !strings.Contains(e.Detail, "reconciled") || e.Merged != f.head("main") {
		t.Fatalf("same run, last entry: %+v", e)
	}

	g := newFixture(t)
	g.req.PRs = g.req.PRs[:1]
	plain := g.train.Forge
	g.train.Forge = &crashForge{Forge: plain}
	if _, err := g.train.Run(context.Background(), g.req); err == nil {
		t.Fatal("the crashed run reported success")
	}
	g.train.Forge = plain
	res, err = g.train.Run(context.Background(), g.req)
	if err != nil || res.PRs[0].Status != "merged" || len(res.PRs[0].Refusals) != 0 {
		t.Fatalf("next run: %v %+v", err, res.PRs)
	}
	if e := lastFor(t, g, 1); e.Kind != "merge" || e.Merged != g.head("main") || !strings.Contains(e.Detail, "reconciled at the next run") {
		t.Fatalf("next run, last entry: %+v", e)
	}
}

func TestStaleJournalMergeDoesNotReleaseAReboundDependency(t *testing.T) {
	for _, kind := range []string{"merge", "merge_intent"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			journalHead, boundHead := f.head("pr1"), strings.Repeat("b", 40)
			first := f.bindTask(1, boundHead, []string{"pkg/a.go"})
			s := f.tasks()
			child, err := s.Add(task.AddInput{Title: "after #1", Class: "implement", DependsOn: []string{first}}, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			f.bindTask(2, f.head("pr2"), []string{"pkg/b.go", "shared.txt"})
			req := Request{Schema: 1, Repository: fixtureRepo, Base: "main", PRs: []Item{{Number: 1}, {Number: 2}}}
			f.realRun(&req)
			merged := f.head("main")
			if kind == "merge_intent" {
				f.edit(func(s *fakeState) {
					s.PRs["1"].State, s.PRs["1"].MergeCommit = "MERGED", merged
				})
			}
			if err := f.train.Audit.Append(Entry{Kind: kind, Repository: fixtureRepo, Base: "main", PR: 1, Task: first, Head: journalHead, Merged: merged}); err != nil {
				t.Fatal(err)
			}
			res, err := f.train.Run(context.Background(), req)
			if err != nil || len(res.PRs) != 2 || res.PRs[1].Status != "merged" {
				t.Fatalf("reconcile aborted before the next PR: %v %+v", err, res.PRs)
			}
			got, err := s.Get(first)
			if err != nil || got.PRMergeHead != "" || got.PRMergeCommit != "" {
				t.Fatalf("stale journal merge was recorded: %v %+v", err, got)
			}
			all, _ := s.List()
			if why := task.Blocker(child, all); why != task.DependencyMergeWait(first, 1) {
				t.Fatalf("stale merge released dependent: %q", why)
			}
			entries, _ := f.train.Audit.Read()
			source := strings.ReplaceAll(kind, "_", " ")
			if !slices.ContainsFunc(entries, func(e Entry) bool {
				return e.Kind == "reconcile" && strings.HasPrefix(e.Detail, "the journaled "+source+" does not match task "+first+"'s bound head") && strings.Contains(e.Detail, journalHead) && strings.Contains(e.Detail, boundHead)
			}) {
				t.Fatalf("words-first mismatch audit missing: %+v", entries)
			}
		})
	}
}

// P2-6: a torn last line (no newline) is terminated under the append lock
// before the next entry, so the entry stays whole and Read keeps it.
func TestRepassAuditTornTailIsRepaired(t *testing.T) {
	a := Audit{Path: filepath.Join(t.TempDir(), "audit.jsonl")}
	if err := a.Append(Entry{Kind: "protection"}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(a.Path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"schema":"` + AuditSchema + `","kind":"mer`)
	f.Close()
	if err := a.Append(Entry{Kind: "merge", PR: 7}); err != nil {
		t.Fatal(err)
	}
	got, err := a.Read()
	if err != nil || len(got) != 2 || got[1].Kind != "merge" || got[1].PR != 7 {
		t.Fatalf("entries after a torn tail: %+v %v", got, err)
	}
}
