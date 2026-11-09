package mergetrain

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"path/filepath"
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
	want := mergedElsewhereDetail(Item{Number: 1, Task: id, BoundHead: boundHead}, serverHead, commit)
	if p.Status != "refused" || p.Refusals[0].Rule != RulePRState || p.Refusals[0].Detail != want || p.Merged != commit {
		t.Fatalf("merged-elsewhere refusal: %+v", p)
	}
	if !strings.Contains(p.Refusals[0].Detail, "server head "+serverHead) || !strings.Contains(p.Refusals[0].Detail, "merge "+commit) {
		t.Fatalf("merged-elsewhere words do not distinguish server head %s and merge commit %s: %q", serverHead, commit, p.Refusals[0].Detail)
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

// Every direct Forge read is declared as either pre-mutation, where its
// caller routes a non-definite failure through preMutationReadFailure, or
// post-mutation, where an unreadable state ends the run in recovery words.
// A planted read in any other function or method fails this guard.
func TestEveryForgeReadDeclaresItsMutationBoundary(t *testing.T) {
	readMethods := map[string]bool{"PR": true, "Diff": true, "Behind": true, "PatchID": true, "Checks": true, "BranchHead": true, "FirstParent": true, "Protection": true}
	declared := map[string]string{
		"train.go:Run:Protection:1":              "pre",
		"train.go:Run:BranchHead:1":              "pre",
		"train.go:Run:Behind:1":                  "pre",
		"train.go:checkPR:PR:1":                  "phase",
		"train.go:checkPR:Diff:1":                "phase",
		"train.go:checkPR:Checks:1":              "phase",
		"train.go:checkPR:PR:2":                  "phase",
		"train.go:land:PR:1":                     "pre",
		"train.go:land:Behind:1":                 "pre",
		"train.go:land:BranchHead:1":             "post",
		"train.go:land:FirstParent:1":            "post",
		"train.go:read:PR:1":                     "pre",
		"train.go:preMutationPatchID:PatchID:1":  "pre",
		"train.go:postMutationPatchID:PatchID:1": "post",
		"train.go:mergedAt:PR:1":                 "post",
		"train.go:waitCI:Checks:1":               "post",
		"update.go:readUpdatedHead:PR:1":         "post",
		"update.go:proveUpdate:FirstParent:1":    "pre",
		"update.go:proveUpdate:PatchID:1":        "pre",
		"update.go:proveUpdate:PatchID:2":        "pre",
		"rebind.go:Rebind:PR:1":                  "pre",
		"rebind.go:Rebind:BranchHead:1":          "pre",
	}
	seen := map[string]bool{}
	checkPhases := map[string]bool{}
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	names = append(names, filepath.Join("..", "cli", "merge_train.go"))
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			calls := map[string]int{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if name == "train.go" && sel.Sel.Name == "checkPR" {
					want := map[string]string{"Run": "readPhasePreMutation", "land": "readPhasePostMutation"}[fn.Name.Name]
					phase, phaseOK := call.Args[len(call.Args)-1].(*ast.Ident)
					if want == "" || !phaseOK || phase.Name != want {
						t.Errorf("%s calls checkPR with phase %v, want %s", fn.Name.Name, call.Args[len(call.Args)-1], want)
					} else {
						checkPhases[fn.Name.Name] = true
					}
				}
				if !readMethods[sel.Sel.Name] || !forgeReceiver(sel.X) {
					return true
				}
				calls[sel.Sel.Name]++
				key := name + ":" + fn.Name.Name + ":" + sel.Sel.Name + ":" + fmt.Sprint(calls[sel.Sel.Name])
				if path := declared[key]; path != "pre" && path != "post" {
					if path != "phase" || !hasReadPhaseParameter(fn) {
						t.Errorf("%s has an undeclared Forge read %s at call site %d", fn.Name.Name, sel.Sel.Name, calls[sel.Sel.Name])
					}
				} else {
					seen[key] = true
				}
				if declared[key] == "phase" && hasReadPhaseParameter(fn) {
					seen[key] = true
				}
				return true
			})
		}
	}
	for key := range declared {
		if !seen[key] {
			t.Errorf("declared Forge read is stale or no longer direct: %s", key)
		}
	}
	if !checkPhases["Run"] || !checkPhases["land"] {
		t.Errorf("checkPR call phases are incomplete: %v", checkPhases)
	}
}

func hasReadPhaseParameter(fn *ast.FuncDecl) bool {
	for _, field := range fn.Type.Params.List {
		id, ok := field.Type.(*ast.Ident)
		if !ok || id.Name != "readPhase" {
			continue
		}
		for _, name := range field.Names {
			if name.Name == "phase" {
				return true
			}
		}
	}
	return false
}

func forgeReceiver(expr ast.Expr) bool {
	for {
		if id, ok := expr.(*ast.Ident); ok {
			return id.Name == "forge"
		}
		sel, ok := expr.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		if sel.Sel.Name == "Forge" {
			return true
		}
		expr = sel.X
	}
}

type failPlanningForge struct {
	Forge
	method, head string
	failAt, hits int
	err          error
}

type prReadResult struct {
	info PRInfo
	err  error
}

type sequencePRForge struct {
	Forge
	results []prReadResult
	last    PRInfo
	calls   int
}

func (f *sequencePRForge) PR(context.Context, string, int) (PRInfo, error) {
	f.calls++
	if len(f.results) == 0 {
		return f.last, nil
	}
	r := f.results[0]
	f.results = f.results[1:]
	if r.err == nil {
		f.last = r.info
	}
	return r.info, r.err
}

func TestUpdatedHeadDeadlineHasThreeOutcomes(t *testing.T) {
	pinned, moved := strings.Repeat("a", 40), strings.Repeat("b", 40)
	readErr := ghFailure(errors.New("exit status 1"), "gh: Bad Gateway (HTTP 502)")
	for _, tc := range []struct {
		name    string
		results []prReadResult
		moved   bool
		err     bool
	}{
		{"head seen moved after an earlier read error", []prReadResult{{err: readErr}, {info: PRInfo{HeadSHA: moved}}}, true, false},
		{"every successful poll saw the head unmoved", []prReadResult{{info: PRInfo{HeadSHA: pinned}}}, false, false},
		{"a non-definite read failed and no move was seen", []prReadResult{{err: readErr}, {info: PRInfo{HeadSHA: pinned}}}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Unix(100, 0)
			forge := &sequencePRForge{results: tc.results, last: PRInfo{HeadSHA: pinned}}
			train := &Train{Forge: forge, UpdateWait: 3 * time.Second, Now: func() time.Time { return now }, Sleep: func(d time.Duration) { now = now.Add(d) }}
			cur, gotMoved, err := train.readUpdatedHead(context.Background(), fixtureRepo, 1, pinned)
			if gotMoved != tc.moved || (err != nil) != tc.err || (gotMoved && cur.HeadSHA != moved) {
				t.Fatalf("outcome = head %s moved %t err %v after %d reads", cur.HeadSHA, gotMoved, err, forge.calls)
			}
		})
	}
}

func (f *failPlanningForge) hit(method string) error {
	if f.method != method {
		return nil
	}
	f.hits++
	if f.hits == f.failAt {
		return f.err
	}
	return nil
}

func (f *failPlanningForge) PR(ctx context.Context, repository string, number int) (PRInfo, error) {
	if number == 1 {
		if err := f.hit("PR"); err != nil {
			return PRInfo{}, err
		}
	}
	return f.Forge.PR(ctx, repository, number)
}

func (f *failPlanningForge) Diff(ctx context.Context, repository, base, head string) ([]FileChange, error) {
	if head == f.head {
		if err := f.hit("Diff"); err != nil {
			return nil, err
		}
	}
	return f.Forge.Diff(ctx, repository, base, head)
}

func (f *failPlanningForge) Checks(ctx context.Context, repository, head string) ([]CheckRun, error) {
	if head == f.head {
		if err := f.hit("Checks"); err != nil {
			return nil, err
		}
	}
	return f.Forge.Checks(ctx, repository, head)
}

func (f *failPlanningForge) Behind(ctx context.Context, repository, base, head string) (bool, error) {
	if head == f.head {
		if err := f.hit("Behind"); err != nil {
			return false, err
		}
	}
	return f.Forge.Behind(ctx, repository, base, head)
}

type failRecoveryProofForge struct {
	Forge
	head   string
	err    error
	failed bool
}

func (f *failRecoveryProofForge) FirstParent(ctx context.Context, repository, head string) (string, error) {
	if head == f.head && !f.failed {
		f.failed = true
		return "", f.err
	}
	return f.Forge.FirstParent(ctx, repository, head)
}

func TestRecoveryProofReadFailureSkipsOnlyThatPR(t *testing.T) {
	f, req := recoveryReadFixture(t)
	it := req.PRs[0]
	old := it.BoundHead
	advanceBase(f, map[string]string{"recovery-proof.txt": "advance\n"})
	if err := f.train.Audit.Append(Entry{Run: "mt-earlier", Kind: "update_requested", Repository: fixtureRepo, Base: "main", PR: it.Number, Task: it.Task, Head: old, BaseSHA: f.head("main")}); err != nil {
		t.Fatal(err)
	}
	_, moved := moveBranch(f, "pr1", nil, true)
	f.train.Forge = &failRecoveryProofForge{Forge: f.gh, head: moved, err: ghFailure(errors.New("exit status 1"), "gh: Bad Gateway (HTTP 502)")}
	res, err := f.train.Run(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := f.train.Audit.Read()
	if slices.ContainsFunc(res.PRs, func(p PlanPR) bool { return p.Number == 1 }) || !slices.ContainsFunc(res.PRs, func(p PlanPR) bool { return p.Number == 2 && p.Status == "merged" }) {
		t.Fatalf("recovery proof failure did not skip only #1: %+v entries=%+v", res.PRs, entries)
	}
	if !slices.ContainsFunc(entries, func(e Entry) bool {
		return e.Kind == "reconcile_read_failed" && e.PR == 1 && strings.Contains(e.Detail, "proving its journaled update")
	}) {
		t.Fatalf("recovery proof read was not audited: %+v", entries)
	}
}

type failWaitCIForge struct {
	Forge
	mutated bool
	failed  bool
	err     error
}

func (f *failWaitCIForge) Rebase(ctx context.Context, repository string, number int, expect string) (string, error) {
	out, err := f.Forge.Rebase(ctx, repository, number, expect)
	f.mutated = true
	return out, err
}

func (f *failWaitCIForge) Checks(ctx context.Context, repository, head string) ([]CheckRun, error) {
	if f.mutated && !f.failed {
		f.failed = true
		return nil, f.err
	}
	return f.Forge.Checks(ctx, repository, head)
}

func TestWaitCINonDefiniteReadFailureStopsInRecoveryWords(t *testing.T) {
	f, _, _, req := strictBound(t)
	f.train.Forge = &failWaitCIForge{Forge: f.gh, err: ghFailure(errors.New("exit status 1"), "gh: Bad Gateway (HTTP 502)")}
	now := time.Now()
	f.train.Now = func() time.Time { return now }
	f.train.Sleep = func(d time.Duration) { now = now.Add(d) }
	res, err := f.train.Run(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "PR #1's state after the update or merge could not be read; the next run recovers it from the journal") {
		t.Fatalf("waitCI read did not stop in recovery words: %v %+v", err, res.PRs)
	}
}

type failPostUpdateCheckPRForge struct {
	Forge
	mutated bool
	reads   int
	err     error
}

func (f *failPostUpdateCheckPRForge) Rebase(ctx context.Context, repository string, number int, expect string) (string, error) {
	out, err := f.Forge.Rebase(ctx, repository, number, expect)
	f.mutated = true
	return out, err
}

func (f *failPostUpdateCheckPRForge) PR(ctx context.Context, repository string, number int) (PRInfo, error) {
	if f.mutated {
		f.reads++
		if f.reads == 2 {
			return PRInfo{}, f.err
		}
	}
	return f.Forge.PR(ctx, repository, number)
}

func TestPostUpdateCheckPRReadFailureStopsInRecoveryWords(t *testing.T) {
	f, _, _, req := strictBound(t)
	forge := &failPostUpdateCheckPRForge{Forge: f.gh, err: ghFailure(errors.New("exit status 1"), "gh: Bad Gateway (HTTP 502)")}
	f.train.Forge = forge
	res, err := f.train.Run(context.Background(), req)
	if err == nil || forge.reads < 2 || !strings.Contains(err.Error(), "PR #1's state after the update or merge could not be read; the next run recovers it from the journal") {
		t.Fatalf("post-update checkPR read did not stop in recovery words: reads %d, err %v, PRs %+v", forge.reads, err, res.PRs)
	}
}

func TestEveryPlanningReadFailureSkipsOnlyThatPR(t *testing.T) {
	readErrs := []struct {
		name string
		err  error
	}{
		{"502", ghFailure(errors.New("exit status 1"), "gh: Bad Gateway (HTTP 502)")},
		{"EOF", io.EOF},
	}
	reads := []struct {
		name, method string
		failAt       int
	}{
		{"PR", "PR", 2},
		{"Diff", "Diff", 1},
		{"Checks", "Checks", 1},
		{"PR re-read", "PR", 3},
		{"Behind", "Behind", 1},
	}
	for _, read := range reads {
		for _, failure := range readErrs {
			t.Run(read.name+"/"+failure.name, func(t *testing.T) {
				f, req := recoveryReadFixture(t)
				setStrict(f)
				head := f.head("pr1")
				f.train.Forge = &failPlanningForge{Forge: f.gh, method: read.method, head: head, failAt: read.failAt, err: failure.err}
				res, err := f.train.Run(context.Background(), req)
				if err != nil {
					t.Fatal(err)
				}
				if !slices.ContainsFunc(res.PRs, func(p PlanPR) bool { return p.Number == 2 && p.Status == "merged" }) || slices.ContainsFunc(res.PRs, func(p PlanPR) bool { return p.Number == 1 }) {
					t.Fatalf("planning read did not skip only #1: %+v", res.PRs)
				}
				entries, _ := f.train.Audit.Read()
				if !slices.ContainsFunc(entries, func(e Entry) bool { return e.Kind == "reconcile_read_failed" && e.PR == 1 }) {
					t.Fatalf("planning read failure was not audited: %+v", entries)
				}
			})
		}
	}
}

type failAfterMutationForge struct {
	Forge
	mutated, failed, repeat bool
	err                     error
}

func (f *failAfterMutationForge) Rebase(ctx context.Context, repository string, number int, expect string) (string, error) {
	out, err := f.Forge.Rebase(ctx, repository, number, expect)
	if number == 1 {
		f.mutated = true
	}
	return out, err
}

func (f *failAfterMutationForge) Merge(ctx context.Context, repository string, number int, head string) (string, error) {
	out, err := f.Forge.Merge(ctx, repository, number, head)
	if number == 1 {
		f.mutated = true
	}
	return out, err
}

func (f *failAfterMutationForge) PR(ctx context.Context, repository string, number int) (PRInfo, error) {
	if number == 1 && f.mutated && (!f.failed || f.repeat) {
		f.failed = true
		return PRInfo{}, f.err
	}
	return f.Forge.PR(ctx, repository, number)
}

func TestReadAfterUpdateFailureStopsAndNextRunRecovers(t *testing.T) {
	f, req := recoveryReadFixture(t)
	setStrict(f)
	advanceBase(f, map[string]string{"base.txt": "advance\n"})
	forge := &failAfterMutationForge{Forge: f.gh, repeat: true, err: ghFailure(errors.New("exit status 1"), "gh: Bad Gateway (HTTP 502)")}
	f.train.Forge = forge
	now := time.Now()
	f.train.Now = func() time.Time { return now }
	f.train.Sleep = func(d time.Duration) { now = now.Add(d) }
	first, err := f.train.Run(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "PR #1's state after the update or merge could not be read; the next run recovers it from the journal") || slices.ContainsFunc(first.PRs, func(p PlanPR) bool { return p.Number == 2 && p.Status == "merged" }) {
		t.Fatalf("post-update read did not stop in words: %v %+v", err, first.PRs)
	}
	forge.repeat = false
	if second, err := f.train.Run(context.Background(), req); err != nil || !slices.ContainsFunc(second.PRs, func(p PlanPR) bool { return p.Number == 2 && p.Status == "merged" }) {
		t.Fatalf("next run did not recover update and continue: %v %+v", err, second.PRs)
	}
}

func TestReadAfterMergeFailureStopsAndNextRunRecovers(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"EOF", io.EOF},
		{"502", ghFailure(errors.New("exit status 1"), "gh: Bad Gateway (HTTP 502)")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, req := recoveryReadFixture(t)
			f.edit(func(s *fakeState) { s.LoseMergeResponse = true })
			forge := &failAfterMutationForge{Forge: f.gh, err: tc.err}
			f.train.Forge = forge
			first, err := f.train.Run(context.Background(), req)
			if err == nil || !strings.Contains(err.Error(), "PR #1's state after the update or merge could not be read; the next run recovers it from the journal") || slices.ContainsFunc(first.PRs, func(p PlanPR) bool { return p.Number == 2 && p.Status == "merged" }) {
				t.Fatalf("post-merge read did not stop in words: %v %+v", err, first.PRs)
			}
			f.edit(func(s *fakeState) { s.LoseMergeResponse = false })
			if second, err := f.train.Run(context.Background(), req); err != nil || !slices.ContainsFunc(second.PRs, func(p PlanPR) bool { return p.Number == 2 && p.Status == "merged" }) {
				t.Fatalf("next run did not recover merge and continue: %v %+v", err, second.PRs)
			}
		})
	}
}

func TestPendingIntentMergedElsewhereIsTerminalBeforeDiffAndChecks(t *testing.T) {
	f := newFixture(t)
	serverHead, merge := markMerged(f, 1)
	boundHead := strings.Repeat("d", 40)
	taskID := f.bindTask(1, boundHead, []string{"pkg/a.go"})
	req := Request{Schema: 1, Repository: fixtureRepo, Base: "main", PRs: []Item{{Number: 1}}}
	f.realRun(&req)
	if err := f.train.Audit.Append(Entry{Run: "mt-earlier", Kind: "merge_intent", Repository: fixtureRepo, Base: "main", PR: 1, Task: taskID, Head: boundHead}); err != nil {
		t.Fatal(err)
	}
	beforeDiff := strings.Count(f.calls(), "compare/")
	beforeChecks := strings.Count(f.calls(), "/check-runs")
	res, err := f.train.Run(context.Background(), req)
	if err != nil || len(res.PRs) != 1 || res.PRs[0].Status != "refused" || res.PRs[0].Head != serverHead || res.PRs[0].Merged != merge {
		t.Fatalf("pending intent mismatch was not terminal: %v %+v", err, res.PRs)
	}
	if strings.Count(f.calls(), "compare/") != beforeDiff || strings.Count(f.calls(), "/check-runs") != beforeChecks {
		t.Fatalf("merged-elsewhere intent reached Diff or Checks:\n%s", f.calls())
	}
	entries, _ := f.train.Audit.Read()
	if !slices.ContainsFunc(entries, func(e Entry) bool { return e.Kind == "reconcile" && e.PR == 1 }) || !slices.ContainsFunc(entries, func(e Entry) bool { return e.Kind == "refusal" && e.PR == 1 && e.Merged == merge }) {
		t.Fatalf("missing reconcile/refusal audit: %+v", entries)
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
