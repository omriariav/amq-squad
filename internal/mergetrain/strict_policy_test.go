package mergetrain

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

const strictRuleset = `[{"type":"non_fast_forward","ruleset_id":7},{"type":"pull_request","ruleset_id":7},{"type":"required_status_checks","ruleset_id":7,"parameters":{"strict_required_status_checks_policy":true,"required_status_checks":[{"context":"ci","integration_id":15368}]}}]`

func setStrict(f *fixture) { f.edit(func(s *fakeState) { s.Rules = json.RawMessage(strictRuleset) }) }

func advanceBase(f *fixture, files map[string]string) {
	f.branch("advance", "main", files)
	mustGit(f.t, f.work, "push", "-q", "origin", "advance:main")
}

func TestStrictBehindUsesPinnedCompareAndRebases(t *testing.T) {
	f := newFixture(t)
	setStrict(f)
	id := f.bindTask(1, f.head("pr1"), []string{"pkg/a.go"})
	store := f.tasks()
	dependent, err := store.Add(task.AddInput{Title: "after updated #1", Class: "implement", DependsOn: []string{id}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	req := Request{Schema: 1, Repository: fixtureRepo, Base: "main", PRs: []Item{{Number: 1}}}
	f.realRun(&req)
	advanceBase(f, map[string]string{"base.txt": "advance\n"})
	f.edit(func(s *fakeState) { s.AdvanceBaseBeforeRebase = []string{"1"} })
	f.train.DryRun = true
	plan, err := f.train.Run(context.Background(), req)
	if err != nil || plan.PRs[0].Status != "planned" || !plan.PRs[0].RebaseForCI || plan.PRs[0].RebaseRule != RuleStrictUpToDate || plan.PRs[0].RebaseReason != "GitHub requires the branch to be up to date before merging" {
		t.Fatalf("strict dry plan: %v %+v", err, plan.PRs)
	}
	f.train.DryRun = false
	res, err := f.train.Run(context.Background(), req)
	if err != nil || res.PRs[0].Status != "merged" || res.PRs[0].Head == plan.PRs[0].Head || len(res.Digests) < 2 || res.Digests[0] != plan.Digest || res.PRs[0].PatchProof == nil || !res.PRs[0].PatchProof.Match {
		t.Fatalf("strict run: %v plan=%s result=%+v digests=%v", err, plan.Digest, res.PRs, res.Digests)
	}
	bound, _ := store.Get(id)
	if bound.PRHead != res.PRs[0].Head || strings.Count(f.calls(), "commits/"+res.PRs[0].Head+"/check-runs") < 2 {
		t.Fatalf("rebind or CI wait missing: task=%+v calls=\n%s", bound, f.calls())
	}
	all, _ := store.List()
	if why := task.Blocker(dependent, all); why != "" {
		t.Fatalf("updated merge did not release dependent: %s", why)
	}
}

func TestChangedPatchAfterUpdateIsRefusedWithoutRebind(t *testing.T) {
	f := newFixture(t)
	setStrict(f)
	id := f.bindTask(1, f.head("pr1"), []string{"pkg/a.go"})
	old := f.head("pr1")
	req := Request{Schema: 1, Repository: fixtureRepo, Base: "main", PRs: []Item{{Number: 1}}}
	f.realRun(&req)
	advanceBase(f, map[string]string{"base.txt": "advance\n"})
	f.edit(func(s *fakeState) { s.AlterAfterRebase = []string{"1"} })
	res, err := f.train.Run(context.Background(), req)
	if err != nil || res.PRs[0].Status != "refused" || res.PRs[0].GateID != "" || res.PRs[0].PatchProof == nil || res.PRs[0].PatchProof.Match || res.PRs[0].Refusals[0].Rule != RuleRebasePatch || res.PRs[0].Refusals[0].Detail != "its change moved during the update" {
		t.Fatalf("changed patch was not refused in words: %v %+v", err, res.PRs[0])
	}
	bound, _ := f.tasks().Get(id)
	if bound.PRHead != old || strings.Contains(f.calls(), "-X PUT") {
		t.Fatalf("changed patch was rebound or merged: task=%+v\n%s", bound, f.calls())
	}
}

func TestIncompleteCompareRefusesWithoutRebind(t *testing.T) {
	f := newFixture(t)
	setStrict(f)
	id := f.bindTask(1, f.head("pr1"), []string{"pkg/a.go"})
	old := f.head("pr1")
	req := Request{Schema: 1, Repository: fixtureRepo, Base: "main", PRs: []Item{{Number: 1}}}
	f.realRun(&req)
	advanceBase(f, map[string]string{"base.txt": "advance\n"})
	f.edit(func(s *fakeState) { s.TruncateAfterRebase = []string{"1"} })
	res, err := f.train.Run(context.Background(), req)
	if err != nil || res.PRs[0].Status != "refused" || res.PRs[0].Refusals[0].Detail != "the update could not be proved: GitHub returned an incomplete diff" {
		t.Fatalf("incomplete compare was not refused in words: %v %+v", err, res.PRs[0])
	}
	bound, _ := f.tasks().Get(id)
	if bound.PRHead != old || strings.Contains(f.calls(), "-X PUT") {
		t.Fatalf("incomplete compare was rebound or merged: task=%+v\n%s", bound, f.calls())
	}
}

type failBehindForge struct {
	Forge
	calls, failAt int
}

func (f *failBehindForge) Behind(ctx context.Context, repository, base, head string) (bool, error) {
	f.calls++
	if f.calls == f.failAt {
		return false, errors.New("HTTP 429: compare unavailable")
	}
	return f.Forge.Behind(ctx, repository, base, head)
}

func TestStrictOffDoesNotCallFailingCompare(t *testing.T) {
	f := newFixture(t)
	f.req.PRs = f.req.PRs[:1]
	advanceBase(f, map[string]string{"base.txt": "advance\n"})
	forge := &failBehindForge{Forge: f.gh, failAt: 1}
	f.train.Forge = forge
	res, err := f.train.Run(context.Background(), f.req)
	if err != nil || res.PRs[0].Status != "merged" || forge.calls != 0 {
		t.Fatalf("strict-off run called compare: err=%v calls=%d result=%+v", err, forge.calls, res.PRs)
	}
}

func TestLaterStrictCompareErrorStopsInWords(t *testing.T) {
	f := newFixture(t)
	setStrict(f)
	f.bindTask(1, f.head("pr1"), []string{"pkg/a.go"})
	f.bindTask(2, f.head("pr2"), []string{"pkg/b.go", "shared.txt"})
	req := Request{Schema: 1, Repository: fixtureRepo, Base: "main", PRs: []Item{{Number: 1}, {Number: 2}}}
	f.realRun(&req)
	forge := &failBehindForge{Forge: f.gh, failAt: 4}
	f.train.Forge = forge
	res, err := f.train.Run(context.Background(), req)
	if err == nil || res.PRs[0].Status != "merged" || !strings.HasPrefix(res.Stopped, "#2: the train stopped while comparing PR #2 with the current base\nrule: branch freshness\ncompare error: HTTP 429") {
		t.Fatalf("later compare stop: err=%v stopped=%q prs=%+v calls=%d", err, res.Stopped, res.PRs, forge.calls)
	}
	entries, readErr := f.train.Audit.Read()
	if readErr != nil || len(entries) == 0 || entries[len(entries)-1].Kind != "stop" || entries[len(entries)-1].Detail != res.Stopped {
		t.Fatalf("compare stop journal: %v %+v", readErr, entries)
	}
}

func TestNamedDryRunBindsTheSameMovedHeadAsRealRun(t *testing.T) {
	f := newFixture(t)
	id := f.bindTask(1, f.head("pr1"), []string{"pkg/a.go"})
	dry := Request{Schema: 1, Repository: fixtureRepo, Base: "main", PRs: []Item{{Number: 1, Task: id}}}
	real := Request{Schema: 1, Repository: fixtureRepo, Base: "main", PRs: []Item{{Number: 1}}}
	store := f.tasks()
	if err := BindDryRun(store, fixtureRepo, &dry); err != nil {
		t.Fatal(err)
	}
	if err := BindTasks(store, fixtureRepo, &real); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(dry.PRs, real.PRs) || dry.PRs[0].BoundHead == "" {
		t.Fatalf("dry binding %+v differs from real binding %+v", dry.PRs, real.PRs)
	}
	f.branch("moved", "main", map[string]string{"pkg/a.go": "package pkg\n\nfunc MovedA() {}\n"})
	f.edit(func(s *fakeState) { s.PRs["1"].Head = "moved" })
	f.train.DryRun = true
	dryPlan, err := f.train.Run(context.Background(), dry)
	if err != nil {
		t.Fatal(err)
	}
	f.train.DryRun = false
	f.realRun(&real)
	realRun, err := f.train.Run(context.Background(), real)
	if err != nil {
		t.Fatal(err)
	}
	dryRefusals, realRefusals := dryPlan.PRs[0].Refusals, realRun.PRs[0].Refusals
	if dryPlan.PRs[0].Status != "refused" || realRun.PRs[0].Status != "refused" || !reflect.DeepEqual(dryRefusals, realRefusals) || !slices.ContainsFunc(dryRefusals, func(r Refusal) bool { return r.Rule == RulePRHeadMoved }) {
		t.Fatalf("named dry and PR-only real drift differ: dry=%+v real=%+v", dryPlan.PRs[0], realRun.PRs[0])
	}
}

func TestStrictFreshnessIsRecheckedBeforeEveryLanding(t *testing.T) {
	f := newFixture(t)
	setStrict(f)
	oldSecond := f.head("pr2")
	f.bindTask(1, f.head("pr1"), []string{"pkg/a.go"})
	secondTask := f.bindTask(2, oldSecond, []string{"pkg/b.go", "shared.txt"})
	req := Request{Schema: 1, Repository: fixtureRepo, Base: "main", PRs: []Item{{Number: 1}, {Number: 2}}}
	f.realRun(&req)
	res, err := f.train.Run(context.Background(), req)
	if err != nil || len(res.PRs) != 2 || res.PRs[0].Status != "merged" || res.PRs[1].Status != "merged" || res.PRs[1].Head == oldSecond {
		t.Fatalf("strict train did not refresh second PR: %v %+v", err, res.PRs)
	}
	bound, _ := f.tasks().Get(secondTask)
	calls := f.calls()
	if bound.PRHead != res.PRs[1].Head || strings.Count(calls, "pr view 2 -R owner/fixture --json id") != 1 || strings.Contains(calls, "pr view 1 -R owner/fixture --json id") || strings.Count(calls, "commits/"+res.PRs[1].Head+"/check-runs") < 2 {
		t.Fatalf("second PR did not rebase, rebind, wait, and re-check: task=%+v calls=\n%s", bound, calls)
	}
}

func TestStrictOffBehindGreenKeepsCurrentBehavior(t *testing.T) {
	f := newFixture(t)
	f.req.PRs = f.req.PRs[:1]
	advanceBase(f, map[string]string{"base.txt": "advance\n"})
	res, err := f.train.Run(context.Background(), f.req)
	if err != nil || res.PRs[0].Status != "merged" || strings.Contains(f.calls(), "updatePullRequestBranch") {
		t.Fatalf("non-strict run changed behavior: %v %+v\n%s", err, res.PRs, f.calls())
	}
}

func TestStrictBehindConflictDryRunRefuses(t *testing.T) {
	f := newFixture(t)
	setStrict(f)
	f.req.PRs = f.req.PRs[:1]
	advanceBase(f, map[string]string{"pkg/a.go": "package pkg\n\nfunc BaseA() {}\n"})
	f.train.DryRun = true
	plan, err := f.train.Run(context.Background(), f.req)
	if err != nil || plan.PRs[0].Status != "refused" || !slices.ContainsFunc(plan.PRs[0].Refusals, func(r Refusal) bool {
		return r.Rule == RuleStrictUpToDate && r.Detail == "the branch is behind and the train cannot bring it up to date: the PR conflicts with the current base" && !strings.Contains(r.Detail, RuleStrictUpToDate)
	}) {
		t.Fatalf("strict conflict plan: %v %+v", err, plan.PRs)
	}
	f.train.DryRun = false
	if _, err := f.train.Run(context.Background(), f.req); err != nil || strings.Contains(f.calls(), "-X PUT") {
		t.Fatalf("strict conflict reached merge: %v\n%s", err, f.calls())
	}
}

func TestStrictBehindMovedHeadDryRunRefuses(t *testing.T) {
	f := newFixture(t)
	setStrict(f)
	f.bindTask(1, f.head("pr1"), []string{"pkg/"})
	req := Request{Schema: 1, Repository: fixtureRepo, Base: "main", PRs: []Item{{Number: 1}}}
	f.realRun(&req)
	advanceBase(f, map[string]string{"base.txt": "advance\n"})
	f.edit(func(s *fakeState) { s.PRs["1"].Head = "pr2" })
	f.train.DryRun = true
	plan, err := f.train.Run(context.Background(), req)
	if err != nil || !slices.ContainsFunc(plan.PRs[0].Refusals, func(r Refusal) bool { return r.Rule == RuleStrictUpToDate }) {
		t.Fatalf("strict moved-head plan: %v %+v", err, plan.PRs)
	}
}

func TestStrictUpToDateDoesNotRebase(t *testing.T) {
	f := newFixture(t)
	setStrict(f)
	f.req.PRs = f.req.PRs[:1]
	f.train.DryRun = true
	plan, err := f.train.Run(context.Background(), f.req)
	if err != nil || plan.PRs[0].RebaseForCI || plan.PRs[0].RebaseReason != "" {
		t.Fatalf("up-to-date strict plan rebased: %v %+v", err, plan.PRs)
	}
}

func TestProtectionParsesStrictPolicy(t *testing.T) {
	t.Run("ruleset", func(t *testing.T) {
		var rules []ruleJSON
		if err := json.Unmarshal([]byte(strictRuleset), &rules); err != nil {
			t.Fatal(err)
		}
		if p := evaluateProtection(rules, map[int]int{7: 0}, nil); !p.Strict {
			t.Fatalf("strict ruleset not parsed: %+v", p)
		}
	})
	t.Run("classic", func(t *testing.T) {
		var classic classicJSON
		if err := json.Unmarshal([]byte(`{"required_status_checks":{"strict":true,"checks":[{"context":"ci","app_id":15368}]},"required_pull_request_reviews":{},"enforce_admins":{"enabled":true}}`), &classic); err != nil {
			t.Fatal(err)
		}
		if p := evaluateProtection(nil, nil, &classic); !p.Strict {
			t.Fatalf("classic strict policy not parsed: %+v", p)
		}
	})
}

func TestMaxFileCompareRefusesWithoutRebind(t *testing.T) {
	assertIncompleteCompareSignalRefused(t, func(s *fakeState) { s.CapCompareAfterRebase = []string{"1"} })
}

func TestHTTP422CompareRefusesWithoutRebind(t *testing.T) {
	assertIncompleteCompareSignalRefused(t, func(s *fakeState) { s.RejectCompareAfterRebase = []string{"1"} })
}

func assertIncompleteCompareSignalRefused(t *testing.T, signal func(*fakeState)) {
	t.Helper()
	f := newFixture(t)
	setStrict(f)
	id := f.bindTask(1, f.head("pr1"), []string{"pkg/a.go"})
	old := f.head("pr1")
	req := Request{Schema: 1, Repository: fixtureRepo, Base: "main", PRs: []Item{{Number: 1}}}
	f.realRun(&req)
	advanceBase(f, map[string]string{"base.txt": "advance\n"})
	f.edit(signal)
	res, err := f.train.Run(context.Background(), req)
	if err != nil || res.PRs[0].Status != "refused" || res.PRs[0].Refusals[0].Detail != "the update could not be proved: GitHub returned an incomplete diff" {
		t.Fatalf("incomplete compare was not refused in words: %v %+v", err, res.PRs[0])
	}
	bound, _ := f.tasks().Get(id)
	if bound.PRHead != old || strings.Contains(f.calls(), "-X PUT") {
		t.Fatalf("incomplete compare was rebound or merged: task=%+v\n%s", bound, f.calls())
	}
}
