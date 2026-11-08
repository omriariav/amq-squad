package mergetrain

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
)

func strictUnsafeFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	setStrict(f)
	bound := f.head("pr1")
	f.bindTask(1, bound, []string{"pkg/", "shared.txt"})
	f.req = Request{Schema: 1, Repository: fixtureRepo, Base: "main", PRs: []Item{{Number: 1}}}
	f.realRun(&f.req)
	advanceBase(f, map[string]string{"base.txt": "advance\n"})
	f.edit(func(s *fakeState) { s.PRs["1"].Head = "pr2" })
	return f
}

func assertNoGate(t *testing.T, f *fixture, p PlanPR) {
	t.Helper()
	ids, err := f.store.IDs()
	if err != nil {
		t.Fatal(err)
	}
	if p.GateID != "" || p.GateError != "" || p.Lifted || len(ids) != 0 {
		t.Fatalf("unactionable refusal exposed a gate: %+v gates=%v", p, ids)
	}
}

// G1: GitHub's strict policy is not something an owner answer can override.
func TestStrictRefusalHasWordsAndNoGate(t *testing.T) {
	f := strictUnsafeFixture(t)
	res, err := f.train.Run(context.Background(), f.req)
	if err != nil {
		t.Fatal(err)
	}
	p := res.PRs[0]
	if p.Status != "refused" || !slices.ContainsFunc(p.Refusals, func(r Refusal) bool {
		return r.Rule == RuleStrictUpToDate && strings.Contains(r.Detail, "No gate overrides GitHub's rule")
	}) {
		t.Fatalf("strict refusal: %+v", p)
	}
	assertNoGate(t, f, p)
}

// G1b: a legacy accepted grow-merge record is ignored for an unliftable rule.
func TestAcceptedLegacyGateDoesNotLiftStrictRefusal(t *testing.T) {
	f := strictUnsafeFixture(t)
	asked := 0
	f.train.Gates = AcceptedFunc(func(string, string) (gatestore.Record, bool) {
		asked++
		return gatestore.Record{GateID: "legacy-approved"}, true
	})
	res, err := f.train.Run(context.Background(), f.req)
	if err != nil {
		t.Fatal(err)
	}
	p := res.PRs[0]
	if p.Status != "refused" || asked != 0 {
		t.Fatalf("legacy approval was consulted or lifted: asked=%d %+v", asked, p)
	}
	assertNoGate(t, f, p)
}

// G2: an asynchronous update that stays conflicting has its own typed rule.
func TestBranchUpdateConflictIsTypedAndUngated(t *testing.T) {
	f := newFixture(t)
	f.req.PRs = f.req.PRs[:1]
	advanceBase(f, map[string]string{"pkg/a.go": "package pkg\n\nfunc BaseA() {}\n"})
	f.edit(func(s *fakeState) { s.AsyncRejected = []string{"1"} })
	now := time.Now()
	f.train.Now = func() time.Time { return now }
	f.train.Sleep = func(d time.Duration) { now = now.Add(d) }
	res, err := f.train.Run(context.Background(), f.req)
	if err != nil {
		t.Fatal(err)
	}
	p := res.PRs[0]
	if p.Status != "refused" || len(p.Refusals) != 1 || p.Refusals[0].Rule != RuleUpdateConflict || p.Refusals[0].Detail != updateConflictDetail {
		t.Fatalf("update conflict: %+v", p)
	}
	assertNoGate(t, f, p)
}

// G3: a merge at another head is terminal for that task binding. The first
// run audits and refuses it; a second run neither reads nor audits it again.
func TestMergedElsewhereIsTerminalForTheBoundHead(t *testing.T) {
	f := newFixture(t)
	serverHead, commit := markMerged(f, 1)
	boundHead := strings.Repeat("d", 40)
	id := f.bindTask(1, boundHead, []string{"pkg/a.go"})
	req := Request{Schema: 1, Repository: fixtureRepo, Base: "main", PRs: []Item{{Number: 1}}}
	f.realRun(&req)
	first, err := f.train.Run(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	p := first.PRs[0]
	want := mergedElsewhereDetail(Item{Number: 1, Task: id, BoundHead: boundHead}, serverHead)
	if p.Status != "refused" || p.Refusals[0].Rule != RulePRState || p.Refusals[0].Detail != want || p.Merged != commit {
		t.Fatalf("merged-elsewhere refusal: %+v", p)
	}
	assertNoGate(t, f, p)
	entries, _ := f.train.Audit.Read()
	firstReconcile := len(slices.DeleteFunc(slices.Clone(entries), func(e Entry) bool { return e.Kind != "reconcile" || e.PR != 1 }))
	reads := strings.Count(f.calls(), "pr view 1 ")
	second, err := f.train.Run(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	entries, _ = f.train.Audit.Read()
	secondReconcile := len(slices.DeleteFunc(slices.Clone(entries), func(e Entry) bool { return e.Kind != "reconcile" || e.PR != 1 }))
	if second.PRs[0].Refusals[0].Detail != want || strings.Count(f.calls(), "pr view 1 ") != reads || secondReconcile != firstReconcile {
		t.Fatalf("terminal refusal was re-read or re-audited: reads %d -> %d reconcile %d -> %d", reads, strings.Count(f.calls(), "pr view 1 "), firstReconcile, secondReconcile)
	}
}

// G3b: strict is not terminal because a later base may make it actionable.
func TestStrictRefusalIsRecheckedOnTheNextRun(t *testing.T) {
	f := strictUnsafeFixture(t)
	if _, err := f.train.Run(context.Background(), f.req); err != nil {
		t.Fatal(err)
	}
	reads := strings.Count(f.calls(), "pr view 1 ")
	if _, err := f.train.Run(context.Background(), f.req); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(f.calls(), "pr view 1 "); got <= reads {
		t.Fatalf("strict refusal was treated as terminal: reads %d -> %d", reads, got)
	}
}

type failOnePR struct {
	Forge
	number int
	err    error
	failed bool
}

func (f *failOnePR) PR(ctx context.Context, repository string, number int) (PRInfo, error) {
	if number == f.number && !f.failed {
		f.failed = true
		return PRInfo{}, f.err
	}
	return f.Forge.PR(ctx, repository, number)
}

func transientReadFixture(t *testing.T, readErr error) (*fixture, Request) {
	t.Helper()
	f := newFixture(t)
	h1, h2 := f.head("pr1"), f.head("pr2")
	f.bindTask(1, h1, []string{"pkg/a.go"})
	f.bindTask(2, h2, []string{"pkg/b.go", "shared.txt"})
	req := Request{Schema: 1, Repository: fixtureRepo, Base: "main", PRs: []Item{{Number: 1}, {Number: 2}}}
	f.realRun(&req)
	f.train.Forge = &failOnePR{Forge: f.gh, number: 1, err: readErr}
	return f, req
}

// G4: transient reads skip one PR and let the rest of the run land.
func TestTransientReconcileReadSkipsOnlyThatPR(t *testing.T) {
	exit1 := errors.New("exit status 1")
	cases := map[string]error{
		"5xx":        ghFailure(exit1, "gh: Bad Gateway (HTTP 502)"),
		"transport":  ghFailure(exit1, "error connecting to api.github.com: read: connection reset by peer"),
		"deadline":   context.DeadlineExceeded,
		"rate limit": ghFailure(exit1, "gh: API rate limit exceeded for user ID 1 (HTTP 403)"),
		"killed":     ghFailure(errors.New("signal: killed"), ""),
	}
	for name, readErr := range cases {
		t.Run(name, func(t *testing.T) {
			f, req := transientReadFixture(t, readErr)
			res, err := f.train.Run(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if len(res.PRs) != 1 || res.PRs[0].Number != 2 || res.PRs[0].Status != "merged" {
				t.Fatalf("other PR did not land: %+v", res.PRs)
			}
			entries, _ := f.train.Audit.Read()
			if !slices.ContainsFunc(entries, func(e Entry) bool {
				return e.Kind == "reconcile_read_failed" && e.PR == 1 && strings.Contains(e.Detail, "this PR was skipped and the run continued")
			}) {
				t.Fatalf("no transient audit entry: %+v", entries)
			}
		})
	}
}

// G4b: a definite permission failure remains a run-wide error.
func TestDefiniteReconcileReadFailureStopsTheRun(t *testing.T) {
	f, req := transientReadFixture(t, ghFailure(errors.New("exit status 1"), "gh: Forbidden (HTTP 403)"))
	if _, err := f.train.Run(context.Background(), req); err == nil || !strings.Contains(err.Error(), "read PR #1 while reconciling") {
		t.Fatalf("403 did not stop the run: %v", err)
	}
	entries, _ := f.train.Audit.Read()
	if slices.ContainsFunc(entries, func(e Entry) bool { return e.Kind == "reconcile_read_failed" }) {
		t.Fatalf("403 was mislabeled transient: %+v", entries)
	}
}

// G5: refusals outside the unliftable set keep their growth gate.
func TestActionableRefusalStillRaisesGrowthGate(t *testing.T) {
	f := newFixture(t)
	f.req.PRs = f.req.PRs[:1]
	f.train.Grants.Merge[0].SizeCapLines = 1
	res, err := f.train.Run(context.Background(), f.req)
	if err != nil {
		t.Fatal(err)
	}
	p := res.PRs[0]
	if p.Status != "refused" || p.Refusals[0].Rule != RuleSizeCap || p.GateID == "" {
		t.Fatalf("actionable refusal lost its gate: %+v", p)
	}
}
