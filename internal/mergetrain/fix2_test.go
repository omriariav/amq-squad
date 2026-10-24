package mergetrain

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func protectionOf(t *testing.T, rulesJSON, classicJSONText string) Protection {
	t.Helper()
	var rules []ruleJSON
	if err := json.Unmarshal([]byte(rulesJSON), &rules); err != nil {
		t.Fatal(err)
	}
	var classic *classicJSON
	if classicJSONText != "" {
		classic = &classicJSON{}
		if err := json.Unmarshal([]byte(classicJSONText), classic); err != nil {
			t.Fatal(err)
		}
	}
	return evaluateProtection(rules, map[int]int{7: 0}, classic)
}

// P1-1 (fix 2): a required check that names no app accepts any app's
// success, so it does not count: unmet, ci_app_unbound, and the owner is told
// to bind it to its app.
func TestRequiredCheckWithoutAnAppIsUnmet(t *testing.T) {
	p := protectionOf(t, `[{"type":"pull_request","ruleset_id":7},{"type":"required_status_checks","ruleset_id":7,"parameters":{"required_status_checks":[{"context":"ci"}]}}]`, "")
	if p.Met() || len(p.Checks) != 0 || !strings.Contains(strings.Join(p.Notes, ";"), "ci_app_unbound: ruleset 7 requires ci from any app") {
		t.Fatalf("an app-less check counted: %+v", p)
	}
}

// N2: classic protection lists a legacy app-less context beside the
// app-bound check; only the app-bound check counts.
func TestClassicContextsAddNoWildcardCheck(t *testing.T) {
	p := protectionOf(t, `[]`, `{"required_status_checks":{"contexts":["ci"],"checks":[{"context":"ci","app_id":15368}]},"required_pull_request_reviews":{},"enforce_admins":{"enabled":true}}`)
	if !p.Met() || !slices.Equal(p.Checks, []RequiredCheck{{Context: "ci", AppID: 15368}}) {
		t.Fatalf("checks %+v", p.Checks)
	}
	p = protectionOf(t, `[]`, `{"required_status_checks":{"contexts":["ci"],"checks":[{"context":"ci","app_id":null}]},"required_pull_request_reviews":{},"enforce_admins":{"enabled":true}}`)
	if p.Met() || len(p.Checks) != 0 {
		t.Fatalf("an app-less classic check counted: %+v", p)
	}
}

// P1-2 (fix 2): classic bypass_pull_request_allowances let an identity push
// without a PR: unmet.
func TestClassicPRBypassAllowancesAreUnmet(t *testing.T) {
	p := protectionOf(t, `[]`, `{"required_status_checks":{"checks":[{"context":"ci","app_id":15368}]},"required_pull_request_reviews":{"bypass_pull_request_allowances":{"users":[{"login":"omriariav"}],"teams":[],"apps":[]}},"enforce_admins":{"enabled":true}}`)
	if p.Met() || p.DirectPushRefused || !strings.Contains(strings.Join(p.Notes, ";"), "bypass_pull_request_allowances") {
		t.Fatalf("a PR bypass allowance counted as refusing direct push: %+v", p)
	}
}

// P1-4 (fix 2): a real run reads each PR's task through the harness binding
// (pr_number, pr_head). No bound task is task_unbound; a head that is not the
// harness's pushed head (nor the train's own rebase) is pr_head_moved.
func TestRealRunReadsTheTaskBoundToThePR(t *testing.T) {
	f := newFixture(t)
	dir := filepath.Join(f.dir, "root", "extensions", "amq-squad", "tasks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeTask := func(id string, pr int, head string, files []string) {
		raw, _ := json.Marshal(map[string]any{"id": id, "declared_files": files, "pr_number": pr, "pr_head": head})
		if err := os.WriteFile(filepath.Join(dir, id+".json"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeTask("t1", 1, f.head("pr1"), []string{"pkg/a.go"})
	writeTask("t3", 3, strings.Repeat("0", 40), []string{"pkg/c.go"}) // the PR's head is not the pushed one
	req := Request{Schema: 1, Repository: fixtureRepo, Base: "main", PRs: []Item{{Number: 1}, {Number: 2}, {Number: 3}}}
	if err := BindTasks(dir, &req); err != nil {
		t.Fatal(err)
	}
	if req.PRs[0].Task != "t1" || req.PRs[1].Unbound == "" || req.PRs[2].BoundHead == "" {
		t.Fatalf("binding %+v", req.PRs)
	}
	res, err := f.train.Run(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	unbound := func(p PlanPR) bool {
		return p.Status == "refused" && slices.ContainsFunc(p.Refusals, func(r Refusal) bool { return r.Rule == RuleTaskUnbound })
	}
	moved := slices.ContainsFunc(res.PRs[2].Refusals, func(r Refusal) bool { return r.Rule == RulePRHeadMoved })
	if res.PRs[0].Status != "merged" || !unbound(res.PRs[1]) || !moved || res.PRs[2].Status != "refused" {
		t.Fatalf("PRs %+v", res.PRs)
	}
}

// N3: the first baseline format (releases as tag names) is read once and
// rewritten in the object form; a baseline that does not decode stops the
// run with drift_baseline_invalid.
func TestDriftBaselineMigratesOnceAndAnInvalidOneStops(t *testing.T) {
	f := newFixture(t)
	root := filepath.Join(f.dir, "root")
	f.train.ReleaseGates, f.train.Root, f.train.DryRun = &f.store, root, true
	f.req.PRs = f.req.PRs[:1]
	mustGit(t, f.bare, "tag", "v1", "main")
	f.edit(func(s *fakeState) { s.Releases = []Release{{ID: 11, Tag: "v1"}} })
	bpath := filepath.Join(AlertsDir(root), "baseline-owner--fixture.json")
	writeFile(t, bpath, `{"repository":"owner/fixture","at":"2026-10-24T13:00:00Z","tags":[{"name":"v1","sha":"`+f.head("main")+`"}],"releases":["v1"]}`)
	res, err := f.train.Run(context.Background(), f.req)
	if err != nil || len(res.Alerts) != 0 {
		t.Fatalf("old-format baseline: %v %+v", err, res.Alerts)
	}
	raw, _ := os.ReadFile(bpath)
	if !strings.Contains(string(raw), `"releases":[{"id":11,"tag":"v1"}]`) {
		t.Fatalf("baseline not rewritten: %s", raw)
	}
	writeFile(t, bpath, `{"repository":`)
	if _, err := f.train.Run(context.Background(), f.req); !errors.Is(err, ErrBaselineInvalid) {
		t.Fatalf("an invalid baseline did not stop the run: %v", err)
	}
}
