package mergetrain

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
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
	req := Request{Schema: 1, Repository: fixtureRepo, Base: "main", PRs: []Item{{Number: 1}}}
	f.realRun(&req)
	advanceBase(f, map[string]string{"base.txt": "advance\n"})
	f.train.DryRun = true
	plan, err := f.train.Run(context.Background(), req)
	if err != nil || plan.PRs[0].Status != "planned" || !plan.PRs[0].RebaseForCI || !strings.Contains(plan.PRs[0].RebaseReason, RuleStrictUpToDate) {
		t.Fatalf("strict dry plan: %v %+v", err, plan.PRs)
	}
	f.train.DryRun = false
	res, err := f.train.Run(context.Background(), req)
	if err != nil || res.PRs[0].Status != "merged" || res.PRs[0].Head == plan.PRs[0].Head || len(res.Digests) < 2 || res.Digests[0] != plan.Digest {
		t.Fatalf("strict run: %v plan=%s result=%+v digests=%v", err, plan.Digest, res.PRs, res.Digests)
	}
	bound, _ := f.tasks().Get(id)
	if bound.PRHead != res.PRs[0].Head || strings.Count(f.calls(), "commits/"+res.PRs[0].Head+"/check-runs") < 2 {
		t.Fatalf("rebind or CI wait missing: task=%+v calls=\n%s", bound, f.calls())
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
		return r.Rule == RuleStrictUpToDate && strings.Contains(r.Detail, RuleStrictUpToDate)
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
