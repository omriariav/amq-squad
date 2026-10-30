package mergetrain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
)

// P1-1: a required check counts only from the app its rule pins and only
// that app's latest result; a rule with no app needs every same-named result
// green.
func TestRequiredCheckIsBoundToItsAppAndLatestResult(t *testing.T) {
	t.Parallel()
	trusted := []RequiredCheck{{Context: "ci", AppID: 77}}
	for _, tc := range []struct {
		name     string
		required []RequiredCheck
		runs     []CheckRun
		green    bool
	}{
		{"trusted app fails, other app succeeds", trusted, []CheckRun{{ID: 1, Name: "ci", AppID: 77, Status: "completed", Conclusion: "failure"}, {ID: 2, Name: "ci", AppID: 88, Status: "completed", Conclusion: "success"}}, false},
		{"only another app reports", trusted, []CheckRun{{ID: 2, Name: "ci", AppID: 88, Status: "completed", Conclusion: "success"}}, false},
		{"trusted app's latest result wins", trusted, []CheckRun{{ID: 5, Name: "ci", AppID: 77, Status: "completed", Conclusion: "failure"}, {ID: 9, Name: "ci", AppID: 77, Status: "completed", Conclusion: "success"}}, true},
		{"trusted app's latest is a failure", trusted, []CheckRun{{ID: 9, Name: "ci", AppID: 77, Status: "completed", Conclusion: "success"}, {ID: 12, Name: "ci", AppID: 77, Status: "completed", Conclusion: "failure"}}, false},
		{"no pinned app, one source fails", []RequiredCheck{{Context: "ci"}}, []CheckRun{{ID: 1, Name: "ci", AppID: 77, Status: "completed", Conclusion: "success"}, {ID: 2, Name: "ci", AppID: 88, Status: "completed", Conclusion: "failure"}}, false},
		{"no pinned app, all green", []RequiredCheck{{Context: "ci"}}, []CheckRun{{ID: 1, Name: "ci", AppID: 77, Status: "completed", Conclusion: "success"}, {Name: "ci", Status: "completed", Conclusion: "success"}}, true},
	} {
		if why, _ := ciRed(tc.required, tc.runs); (why == "") != tc.green {
			t.Errorf("%s: %q", tc.name, why)
		}
	}
	var rules []ruleJSON
	if err := json.Unmarshal([]byte(`[{"type":"pull_request","ruleset_id":7},{"type":"required_status_checks","ruleset_id":7,"parameters":{"required_status_checks":[{"context":"ci","integration_id":77}]}}]`), &rules); err != nil {
		t.Fatal(err)
	}
	if p := evaluateProtection(rules, map[int]int{7: 0}, nil); !slices.Equal(p.Checks, trusted) {
		t.Fatalf("integration id not read: %+v", p.Checks)
	}
	// end to end on the fixture: the trusted app fails, a second app's
	// same-named success does not make CI green
	f := newSubprocessFixture(t) // subprocess row: gh through the real exec path
	f.edit(func(s *fakeState) {
		s.Rules = json.RawMessage(`[{"type":"pull_request","ruleset_id":7},{"type":"required_status_checks","ruleset_id":7,"parameters":{"required_status_checks":[{"context":"ci","integration_id":77}]}}]`)
		s.CIApp, s.RedCI = 77, []string{f.head("pr1")}
		s.ExtraRuns = map[string][]CheckRun{f.head("pr1"): {{ID: 500, Name: "ci", AppID: 88, Status: "completed", Conclusion: "success"}}}
	})
	f.req.PRs = f.req.PRs[:1]
	if res, err := f.train.Run(context.Background(), f.req); err != nil || res.PRs[0].Status != "refused" {
		t.Fatalf("another app's success passed: %v %+v", err, res.PRs)
	}
}

// P1-2: protection the owner can bypass does not count: a ruleset with
// bypass actors contributes no required check, classic protection without
// enforce_admins neither, and Met says no with the reason.
func TestBypassableProtectionIsUnmet(t *testing.T) {
	var rules []ruleJSON
	if err := json.Unmarshal([]byte(`[{"type":"pull_request","ruleset_id":7},{"type":"required_status_checks","ruleset_id":8,"parameters":{"required_status_checks":[{"context":"ci","integration_id":15368}]}}]`), &rules); err != nil {
		t.Fatal(err)
	}
	p := evaluateProtection(rules, map[int]int{7: 0, 8: 1}, nil)
	if p.Met() || len(p.Checks) != 0 || !strings.Contains(strings.Join(p.Notes, ";"), "ruleset 8 has bypass actors") {
		t.Fatalf("bypassable CI ruleset counted: %+v", p)
	}
	var classic classicJSON
	if err := json.Unmarshal([]byte(`{"required_status_checks":{"checks":[{"context":"ci","app_id":15368}]}}`), &classic); err != nil {
		t.Fatal(err)
	}
	p = evaluateProtection(rules[:1], map[int]int{7: 0}, &classic)
	if p.Met() || len(p.Checks) != 0 || !strings.Contains(strings.Join(p.Notes, ";"), "enforce_admins off") {
		t.Fatalf("classic checks without enforce_admins counted: %+v", p)
	}
}

// P2-8: protected paths compare case-folded.
func TestProtectedPathsAreCaseFolded(t *testing.T) {
	entries := []string{"squad.yaml", ".amq-squad/team-rules.md", ".claude/settings*.json"}
	for _, p := range []string{"Squad.yaml", ".AMQ-squad/Team-Rules.md", ".Claude/Settings.local.JSON"} {
		if _, hit := protectedHit(p, entries); !hit {
			t.Errorf("%s passed as unprotected", p)
		}
	}
}

// P2-11: an audit append that fails stops the run before any merge.
func TestAuditFailureStopsBeforeAnyMerge(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.req.PRs = []Item{f.req.PRs[0], {Number: 2, Task: "T2", DeclaredFiles: []string{"pkg/b.go"}}} // PR 2 is refused in the plan
	audit := f.train.Audit.Path
	f.train.Forge = &hookForge{GH: f.gh, n: 3, hook: func() { // before PR 2's plan read: the log becomes unwritable
		if err := os.Remove(audit); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(audit, 0o700); err != nil {
			t.Fatal(err)
		}
	}}
	_, err := f.train.Run(context.Background(), f.req)
	if err == nil || !strings.Contains(err.Error(), "audit log") || strings.Contains(mustGit(t, f.bare, "log", "--format=%s", "main"), "squash") || strings.Contains(f.calls(), "/merge") {
		t.Fatalf("the run merged after an audit failure: %v\n%s", err, f.calls())
	}
}

// P2-12: one accepted gate covers one object. A release deleted and
// recreated under the same tag has a new id and needs a new gate; a tag
// moved to another commit too.
func TestDriftBindsAGateToOneReleaseAndOneTagCommit(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	root := filepath.Join(f.dir, "root")
	f.train.ReleaseGates, f.train.Root, f.train.DryRun = &f.store, root, true
	f.req.PRs = f.req.PRs[:1]
	run := func() []Alert {
		t.Helper()
		res, err := f.train.Run(context.Background(), f.req)
		if err != nil {
			t.Fatal(err)
		}
		return res.Alerts
	}
	run() // baseline
	mustGit(t, f.bare, "tag", "v1", "main")
	f.edit(func(s *fakeState) { s.Releases = []Release{{ID: 11, Tag: "v1"}} })
	for i, g := range []gatestore.Spec{{GateID: "g-tag", Action: "tag", Target: "git tag v1"}, {GateID: "g-rel", Action: "github_release", Target: "gh release create v1"}} {
		g.RaisedBy, g.Squad = "seat", SquadOf(root)
		rec, err := gatestore.Render(g, time.Now(), gateIdentity())
		if err != nil || f.store.Create(rec) != nil {
			t.Fatal(i, err)
		}
		writeFile(t, filepath.Join(root, "extensions", "amq-squad", "consumed", "gates", g.GateID), "approved test\n")
	}
	if a := run(); len(a) != 0 {
		t.Fatalf("covered tag and release alerted: %+v", a)
	}
	f.edit(func(s *fakeState) { s.Releases = []Release{{ID: 12, Tag: "v1"}} }) // deleted and recreated
	if a := run(); len(a) != 1 || a[0].Kind != "release" {
		t.Fatalf("recreated release: %+v", a)
	}
	f.branch("other", "main", map[string]string{"z.txt": "z\n"})
	mustGit(t, f.bare, "tag", "-f", "v1", "other") // moved
	if a := run(); !slices.ContainsFunc(a, func(x Alert) bool { return x.Kind == "tag" && x.Name == "v1" }) {
		t.Fatalf("moved tag: %+v", a)
	}
}
