package mergetrain

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
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

func recoveryReadFixture(t *testing.T) (*fixture, Request) {
	t.Helper()
	f := newFixture(t)
	h1, h2 := f.head("pr1"), f.head("pr2")
	f.bindTask(1, h1, []string{"pkg/a.go"})
	f.bindTask(2, h2, []string{"pkg/b.go", "shared.txt"})
	req := Request{Schema: 1, Repository: fixtureRepo, Base: "main", PRs: []Item{{Number: 1}, {Number: 2}}}
	f.realRun(&req)
	return f, req
}

func failRecoveryRead(f *fixture, readErr error) {
	f.train.Forge = &failOnePR{Forge: f.gh, number: 1, err: readErr}
}

func assertReadSkippedAndOtherLanded(t *testing.T, f *fixture, req Request, phase string) {
	t.Helper()
	res, err := f.train.Run(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.PRs) != 1 || res.PRs[0].Number != 2 || res.PRs[0].Status != "merged" {
		t.Fatalf("other PR did not land: %+v", res.PRs)
	}
	entries, _ := f.train.Audit.Read()
	if !slices.ContainsFunc(entries, func(e Entry) bool {
		return e.Kind == "reconcile_read_failed" && e.PR == 1 && strings.Contains(e.Detail, phase) && strings.Contains(e.Detail, "this PR was skipped and the run continued")
	}) {
		t.Fatalf("no per-PR read audit for %q: %+v", phase, entries)
	}
}

// G4: every unproved read error skips one PR and lets the rest of the run
// land. This intentionally enumerates the documented 5xx, transport,
// deadline, bare EOF, killed-gh and unknown forms, plus a definite 4xx that
// is neither an auth error nor a missing PR.
func TestRecoveryReadErrorSkipsOnlyThatPR(t *testing.T) {
	exit1 := errors.New("exit status 1")
	cases := []struct {
		name string
		err  error
	}{
		{"5xx wrapper", ghFailure(exit1, "gh: Bad Gateway (HTTP 502)")},
		{"bare gh 502", ghFailure(exit1, "gh: HTTP 502")},
		{"transport", ghFailure(exit1, "error connecting to api.github.com: read: connection reset by peer")},
		{"deadline", context.DeadlineExceeded},
		{"bare EOF", io.EOF},
		{"killed", ghFailure(errors.New("signal: killed"), "")},
		{"unknown wording", errors.New("the forge returned an unfamiliar read failure")},
		{"unwrapped 403", errors.New("gh pr view: gh: Forbidden (HTTP 403)")},
		{"other 4xx", ghFailure(exit1, "gh: Validation Failed (HTTP 422)")},
		{"GraphQL other 4xx", ghFailure(exit1, "GraphQL: Validation Failed (HTTP 422) (repository.pullRequest)")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, req := recoveryReadFixture(t)
			failRecoveryRead(f, tc.err)
			assertReadSkippedAndOtherLanded(t, f, req, "reconciling it")
		})
	}
}

// G4b: exact gh wrappers prove the three client statuses that still end the
// run. Auth failures carry an actionable sentence; a missing PR keeps the
// old read-error behavior.
func TestDefiniteRecoveryReadFailureStopsTheRun(t *testing.T) {
	exit1 := errors.New("exit status 1")
	cases := []struct {
		name, stderr, words string
	}{
		{"401 gh wrapper", "gh: Unauthorized (HTTP 401)", "check the owner's gh authentication and repository access"},
		{"403 GraphQL wrapper", "GraphQL: Forbidden (HTTP 403) (repository.pullRequest)", "check the owner's gh authentication and repository access"},
		{"404 gh wrapper", "gh: Not Found (HTTP 404)", "read PR #1 while reconciling it"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, req := recoveryReadFixture(t)
			failRecoveryRead(f, ghFailure(exit1, tc.stderr))
			if _, err := f.train.Run(context.Background(), req); err == nil || !strings.Contains(err.Error(), tc.words) {
				t.Fatalf("definite client error did not stop in words: %v", err)
			}
			entries, _ := f.train.Audit.Read()
			if slices.ContainsFunc(entries, func(e Entry) bool { return e.Kind == "reconcile_read_failed" }) {
				t.Fatalf("definite client error was mislabeled as a skip: %+v", entries)
			}
		})
	}
}

func TestUpdateRecoveryReadFailureSkipsOnlyThatPR(t *testing.T) {
	f, req := recoveryReadFixture(t)
	it := req.PRs[0]
	if err := f.train.Audit.Append(Entry{Run: "mt-earlier", Kind: "update_requested", Repository: fixtureRepo, Base: "main", PR: it.Number, Task: it.Task, Head: it.BoundHead, BaseSHA: f.head("main")}); err != nil {
		t.Fatal(err)
	}
	failRecoveryRead(f, ghFailure(errors.New("exit status 1"), "gh: Bad Gateway (HTTP 502)"))
	assertReadSkippedAndOtherLanded(t, f, req, "recovering its update")
}

func TestRebindRecoveryReadFailureSkipsOnlyThatPR(t *testing.T) {
	f, req := recoveryReadFixture(t)
	it := req.PRs[0]
	if err := f.train.Audit.Append(Entry{Run: "mt-earlier", Kind: "rebind", Repository: fixtureRepo, Base: "main", PR: it.Number, Task: it.Task, Head: it.BoundHead, NewHead: strings.Repeat("e", 40), Detail: "patch-id: proof\nfirst parent: " + f.head("main")}); err != nil {
		t.Fatal(err)
	}
	failRecoveryRead(f, ghFailure(errors.New("exit status 1"), "gh: Bad Gateway (HTTP 502)"))
	assertReadSkippedAndOtherLanded(t, f, req, "recovering its re-bind")
}

func TestMergedElsewhereTerminalSkipPrecedesEveryRecoveryRead(t *testing.T) {
	f := newFixture(t)
	serverHead, _ := markMerged(f, 1)
	boundHead := strings.Repeat("d", 40)
	f.bindTask(1, boundHead, []string{"pkg/a.go"})
	req := Request{Schema: 1, Repository: fixtureRepo, Base: "main", PRs: []Item{{Number: 1}}}
	f.realRun(&req)
	if _, err := f.train.Run(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	it := req.PRs[0]
	if err := f.train.Audit.Append(Entry{Run: "mt-earlier", Kind: "rebind", Repository: fixtureRepo, Base: "main", PR: 1, Task: it.Task, Head: it.BoundHead, NewHead: serverHead, Detail: "patch-id: proof\nfirst parent: " + f.head("main")}); err != nil {
		t.Fatal(err)
	}
	if err := f.train.Audit.Append(Entry{Run: "mt-earlier", Kind: "update_requested", Repository: fixtureRepo, Base: "main", PR: 1, Task: it.Task, Head: it.BoundHead, BaseSHA: f.head("main")}); err != nil {
		t.Fatal(err)
	}
	reads := strings.Count(f.calls(), "pr view 1 ")
	res, err := f.train.Run(context.Background(), req)
	if err != nil || len(res.PRs) != 1 || res.PRs[0].Status != "refused" || strings.Count(f.calls(), "pr view 1 ") != reads {
		t.Fatalf("terminal refusal was read by recovery: %v %+v reads %d -> %d", err, res.PRs, reads, strings.Count(f.calls(), "pr view 1 "))
	}
}

// A cheap source guard keeps all three recovery functions on the shared
// reader. A new direct Forge.PR call in one of them would bypass the
// terminal refusal and read-error rules.
func TestEveryRecoveryPRReadUsesSharedReader(t *testing.T) {
	targets := map[string]bool{"reconcileRebinds": true, "reconcileUpdates": true, "reconcileMerges": true}
	for _, name := range []string{"train.go", "update.go"} {
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !targets[fn.Name.Name] {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				forge, direct := sel.X.(*ast.SelectorExpr)
				if direct && sel.Sel.Name == "PR" && forge.Sel.Name == "Forge" {
					t.Errorf("%s in %s reads Forge.PR directly; use recoveryPRReader.read", fn.Name.Name, name)
				}
				return true
			})
		}
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
