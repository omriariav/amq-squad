package mergetrain

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/grants"
)

// Acceptance 1: a clean set of five PRs lands in order under the owner's gh;
// the audit log holds the batch digest; PR 4's rebase after PR 2 changes its
// tree, changes the digest and re-runs the check.
func TestAcceptance1FiveCleanPRsLandInOrder(t *testing.T) {
	f := newFixture(t)
	oldHead4 := f.head("pr4")
	res, err := f.train.Run(context.Background(), f.req)
	if err != nil {
		t.Fatalf("run: %v (%+v)", err, res)
	}
	for i, p := range res.PRs {
		if p.Status != "merged" || p.Number != i+1 {
			t.Fatalf("PR %d: %+v", i+1, p)
		}
	}
	// main carries five squash commits in the lead's order
	log := mustGit(t, f.bare, "log", "--format=%s", "main")
	if want := "squash #5\nsquash #4\nsquash #3\nsquash #2\nsquash #1\nbase"; log != want {
		t.Fatalf("main log:\n%s\nwant:\n%s", log, want)
	}
	// PR 4 was ordered after PR 2 (shared.txt) and rebased
	p4 := res.PRs[3]
	if !slices.Equal(p4.After, []int{2}) || p4.Head == oldHead4 {
		t.Fatalf("PR 4: after %v head %s (old %s)", p4.After, p4.Head, oldHead4)
	}
	entries, err := f.train.Audit.Read()
	if err != nil {
		t.Fatal(err)
	}
	var batches []Entry
	var rebase *Entry
	merges := 0
	for i, e := range entries {
		switch e.Kind {
		case "batch":
			batches = append(batches, e)
		case "rebase":
			rebase = &entries[i]
		case "merge":
			merges++
		}
	}
	if merges != 5 || rebase == nil || rebase.PR != 4 || rebase.Head != oldHead4 || rebase.NewHead != p4.Head {
		t.Fatalf("merges %d rebase %+v", merges, rebase)
	}
	if len(batches) != 2 {
		t.Fatalf("batch entries %d, want the plan and the post-rebase digest: %+v", len(batches), batches)
	}
	first := batches[0]
	if len(first.Pairs) != 5 || first.Digest != BatchDigest(fixtureRepo, first.BaseSHA, first.Pairs) {
		t.Fatalf("first batch %+v", first)
	}
	second := batches[1]
	if second.Digest == first.Digest || second.Pairs[0].Number != 4 || second.Pairs[0].Head != p4.Head ||
		second.Digest != BatchDigest(fixtureRepo, second.BaseSHA, second.Pairs) {
		t.Fatalf("second batch %+v", second)
	}
	// the check re-ran on the new head: its CI was read after the rebase
	calls := f.calls()
	if !strings.Contains(calls, "commits/"+p4.Head+"/check-runs") || !strings.Contains(calls, "id=PR_4") {
		t.Fatalf("no re-check at the rebased head:\n%s", calls)
	}
	// the train's own rebase is a force push, and it is not refused
	if len(p4.Refusals) != 0 {
		t.Fatalf("PR 4 refusals after the rebase: %+v", p4.Refusals)
	}
}

// Acceptance 2: one refusal per rule. Each raises a gate request for the
// exact head and rule, and merges nothing.
func TestAcceptance2Refusals(t *testing.T) {
	cases := []struct {
		name string
		rule string
		prep func(f *fixture) Item
	}{
		{"ci red", RuleCIGreen, func(f *fixture) Item {
			f.edit(func(s *fakeState) { s.RedCI = []string{f.head("pr1")} })
			return f.req.PRs[0]
		}},
		{"size cap", RuleSizeCap, func(f *fixture) Item {
			f.train.Grants.Merge[0].SizeCapLines = 1
			return f.req.PRs[0]
		}},
		{"size cap files", RuleSizeCap, func(f *fixture) Item {
			f.train.Grants.Merge[0].SizeCapFiles = 1
			return f.req.PRs[1] // pkg/b.go and shared.txt
		}},
		{"force push", RuleForcePush, func(f *fixture) Item {
			f.edit(func(s *fakeState) {
				s.PRs["1"].ForcePushes = []ForcePush{{Before: strings.Repeat("1", 40), After: f.head("pr1"), Actor: "seat"}}
			})
			return f.req.PRs[0]
		}},
		{"protected path", RuleProtectedPath, func(f *fixture) Item {
			f.branch("pr6", "main", map[string]string{".amq-squad/team-rules.md": "seat rules\n"})
			f.edit(func(s *fakeState) { s.PRs["6"] = &fakePR{Head: "pr6", Base: "main", Draft: true, State: "OPEN"} })
			return Item{Number: 6, Task: "T6", DeclaredFiles: []string{".amq-squad/team-rules.md"}}
		}},
		{"wrong base", RuleBaseBranch, func(f *fixture) Item {
			f.branch("pr7", "dev", map[string]string{"pkg/a.go": "package pkg\n// dev\n"})
			f.edit(func(s *fakeState) { s.PRs["7"] = &fakePR{Head: "pr7", Base: "dev", Draft: true, State: "OPEN"} })
			return Item{Number: 7, Task: "T7", DeclaredFiles: []string{"pkg/a.go"}}
		}},
		{"file outside the task", RuleTaskScope, func(f *fixture) Item {
			return Item{Number: 2, Task: "T2", DeclaredFiles: []string{"pkg/b.go"}} // shared.txt is outside
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			it := tc.prep(f)
			mainBefore := f.head("main")
			req := f.req
			req.PRs = []Item{it}
			res, err := f.train.Run(context.Background(), req)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			p := res.PRs[0]
			if p.Status != "refused" || !slices.ContainsFunc(p.Refusals, func(r Refusal) bool { return r.Rule == tc.rule }) {
				t.Fatalf("want refusal %s, got %+v", tc.rule, p)
			}
			if len(rules(p.Refusals)) != 1 {
				t.Fatalf("the fixture should fail one rule only: %+v", p.Refusals)
			}
			rec, err := f.store.Load(p.GateID)
			if err != nil {
				t.Fatalf("no gate request %q: %v", p.GateID, err)
			}
			if rec.Class != "spawn" || rec.TypedClass != "grow" || !strings.Contains(rec.Target, "rules="+tc.rule) || !strings.Contains(rec.Target, "head="+p.Head) {
				t.Fatalf("gate %+v", rec)
			}
			if f.head("main") != mainBefore || strings.Contains(f.calls(), "/merge") || len(res.Pairs) != 0 {
				t.Fatalf("a refused PR merged something: pairs %v\n%s", res.Pairs, f.calls())
			}
		})
	}
}

// Acceptance 5: a fixture repository with no ruleset makes the train refuse
// to start (and a ruleset the owner can bypass, or classic protection that
// exempts admins, is no protection).
func TestAcceptance5NoRulesetRefusesToStart(t *testing.T) {
	for name, prep := range map[string]func(s *fakeState){
		"no ruleset": func(s *fakeState) { s.Rules = json.RawMessage(`[]`) },
		"ci but direct push": func(s *fakeState) {
			s.Rules = json.RawMessage(`[{"type":"required_status_checks","ruleset_id":7,"parameters":{"required_status_checks":[{"context":"ci","integration_id":15368}]}}]`)
		},
		"classic admin exempt": func(s *fakeState) {
			s.Rules = json.RawMessage(`[]`)
			s.Classic = json.RawMessage(`{"required_status_checks":{"contexts":["ci"],"checks":[{"context":"ci","app_id":15368}]},"required_pull_request_reviews":{},"enforce_admins":{"enabled":false}}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.edit(prep)
			mainBefore := f.head("main")
			res, err := f.train.Run(context.Background(), f.req)
			if !errors.Is(err, ErrProtection) || res.ProtectionMet || res.Stopped == "" {
				t.Fatalf("want ErrProtection, got %v %+v", err, res)
			}
			if f.head("main") != mainBefore || strings.Contains(f.calls(), "pr view") {
				t.Fatalf("the train started:\n%s", f.calls())
			}
		})
	}
	// classic protection with enforce_admins and required reviews is enough
	f := newFixture(t)
	f.edit(func(s *fakeState) {
		s.Rules = json.RawMessage(`[]`)
		s.Classic = json.RawMessage(`{"required_status_checks":{"contexts":["ci"],"checks":[{"context":"ci","app_id":15368}]},"required_pull_request_reviews":{},"enforce_admins":{"enabled":true}}`)
	})
	f.req.PRs = f.req.PRs[:1]
	if res, err := f.train.Run(context.Background(), f.req); err != nil || res.PRs[0].Status != "merged" {
		t.Fatalf("classic protection with enforce_admins: %v %+v", err, res)
	}
}

// The dry run plans, orders and checks, and raises and merges nothing.
func TestDryRunMergesAndRaisesNothing(t *testing.T) {
	f := newFixture(t)
	f.edit(func(s *fakeState) { s.Rules = json.RawMessage(`[]`) }) // a dry run reads an unprotected repository
	f.train.DryRun = true
	f.req.RequiredChecks = []string{"ci"}
	f.req.PRs[1].DeclaredFiles = []string{"pkg/b.go"}
	mainBefore := f.head("main")
	res, err := f.train.Run(context.Background(), f.req)
	if err != nil {
		t.Fatal(err)
	}
	if res.ProtectionMet || len(res.Pairs) != 4 || !slices.Equal(res.Refused(), []int{2}) || res.PRs[1].GateID != "" ||
		!strings.HasPrefix(res.PRs[1].GateError, "dry run: would raise grow merge owner/fixture#2") {
		t.Fatalf("plan %+v", res)
	}
	// the predicted pair is still reported with PR 2 refused
	if !slices.Equal(res.PRs[3].After, []int{2}) || !slices.Equal(res.PRs[3].Overlap, []string{"shared.txt"}) {
		t.Fatalf("PR 4 after %v", res.PRs[3].After)
	}
	ids, _ := f.store.IDs()
	if f.head("main") != mainBefore || strings.Contains(f.calls(), "/merge") || strings.Contains(f.calls(), "updatePullRequestBranch") || len(ids) != 0 {
		t.Fatalf("dry run changed something: gates %v\n%s", ids, f.calls())
	}
}

// An owner-accepted gate for the exact head and rules lifts the refusal; a
// gate for another head does not.
func TestAcceptedGateLiftsExactlyThatRefusal(t *testing.T) {
	f := newFixture(t)
	f.train.Grants.Merge[0].SizeCapLines = 1
	head := f.head("pr1")
	var asked []string
	f.train.Gates = AcceptedFunc(func(action, target string) (gatestore.Record, bool) {
		asked = append(asked, target)
		return gatestore.Record{GateID: "merge-ok"}, target == "grow merge owner/fixture#1 head="+head+" rules=size_cap"
	})
	f.req.PRs = f.req.PRs[:2]
	res, err := f.train.Run(context.Background(), f.req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.PRs[0].Lifted || res.PRs[0].Status != "merged" || res.PRs[1].Status != "refused" {
		t.Fatalf("PR 1 %+v PR 2 %+v (asked %v)", res.PRs[0], res.PRs[1], asked)
	}
}

// Manual mode keeps the per-merge stop.
func TestManualModeStopsBeforeEachMerge(t *testing.T) {
	f := newFixture(t)
	f.train.Manual = true
	var steps []Step
	f.train.Confirm = func(s Step) bool { steps = append(steps, s); return len(steps) < 2 }
	res, err := f.train.Run(context.Background(), f.req)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 || res.PRs[0].Status != "merged" || res.PRs[1].Status != "planned" || res.Stopped != "manual stop before #2" {
		t.Fatalf("steps %v %+v", steps, res)
	}
	if log := mustGit(t, f.bare, "log", "--format=%s", "main"); log != "squash #1\nbase" {
		t.Fatalf("main %s", log)
	}
}

// A real run from a seat process is refused before anything is read.
func TestSeatProcessHoldsNoMergePath(t *testing.T) {
	f := newFixture(t)
	f.train.InSeat = true
	if _, err := f.train.Run(context.Background(), f.req); !errors.Is(err, ErrSeat) || f.calls() != "" {
		t.Fatalf("seat run: %v\n%s", err, f.calls())
	}
}

// hookForge runs hook before the n-th PR read (1-based), once.
type hookForge struct {
	*GH
	n, calls int
	hook     func()
}

func (h *hookForge) PR(ctx context.Context, repository string, number int) (PRInfo, error) {
	h.calls++
	if h.calls == h.n && h.hook != nil {
		h.hook()
	}
	return h.GH.PR(ctx, repository, number)
}

func (f *fixture) seatPush(branch string) {
	mustGit(f.t, f.work, "checkout", "-q", branch)
	writeFile(f.t, f.work+"/pkg/x.go", "package pkg\n")
	mustGit(f.t, f.work, "add", "-A")
	mustGit(f.t, f.work, "commit", "-q", "-m", "seat push")
	mustGit(f.t, f.work, "push", "-q", "origin", branch)
}

// A seat's push after the plan changes the head: the train re-checks the new
// head, with a new digest, and refuses it on its new file.
func TestNewHeadAfterPlanIsRechecked(t *testing.T) {
	f := newFixture(t)
	f.req.PRs = f.req.PRs[:1]
	f.train.Forge = &hookForge{GH: f.gh, n: 3, hook: func() { f.seatPush("pr1") }} // plan reads PR twice
	res, err := f.train.Run(context.Background(), f.req)
	if err != nil {
		t.Fatal(err)
	}
	p := res.PRs[0]
	if p.Status != "refused" || !slices.ContainsFunc(p.Refusals, func(r Refusal) bool { return r.Rule == RuleTaskScope }) || len(res.Digests) < 2 {
		t.Fatalf("the new head was not re-checked: %+v digests %v", p, res.Digests)
	}
}

// P1-3: the files, the CI and the head are one snapshot. A push between the
// head read and the checks makes the check read the PR again; the diff comes
// from the compare endpoint for the pinned pair, never the live files list.
func TestHeadMovingDuringTheCheckIsReadAgain(t *testing.T) {
	f := newFixture(t)
	f.req.PRs = f.req.PRs[:1]
	f.train.DryRun = true
	f.train.Forge = &hookForge{GH: f.gh, n: 2, hook: func() { f.seatPush("pr1") }} // the re-read after the checks
	res, err := f.train.Run(context.Background(), f.req)
	if err != nil {
		t.Fatal(err)
	}
	if p := res.PRs[0]; p.Head != f.head("pr1") || p.Status != "refused" || !slices.Contains(p.Files, "pkg/x.go") {
		t.Fatalf("the check did not follow the moved head: %+v", p)
	}
	if calls := f.calls(); strings.Contains(calls, "/files") || !strings.Contains(calls, "compare/") {
		t.Fatalf("diff not from the compare endpoint:\n%s", calls)
	}
}

// P2-9: manual mode asks after the final rebase, check and digest, for the
// head that merges; a push after the answer is refused by the pinned head.
func TestManualConfirmationIsForTheHeadThatMerges(t *testing.T) {
	f := newFixture(t)
	f.req.PRs = []Item{f.req.PRs[1], f.req.PRs[3]} // PR 4 rebases after PR 2
	f.train.Manual = true
	var steps []Step
	f.train.Confirm = func(s Step) bool { steps = append(steps, s); return true }
	res, err := f.train.Run(context.Background(), f.req)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 || steps[1].Head != res.PRs[1].Head || steps[1].Digest != res.Digest || res.PRs[1].Status != "merged" {
		t.Fatalf("PR 4 was confirmed for %+v, merged %+v", steps, res.PRs[1])
	}
	g := newFixture(t)
	g.req.PRs = g.req.PRs[:1]
	g.train.Manual = true
	g.train.Confirm = func(Step) bool { g.seatPush("pr1"); return true }
	if _, err := g.train.Run(context.Background(), g.req); err == nil || strings.Contains(mustGit(t, g.bare, "log", "--format=%s", "main"), "squash") {
		t.Fatalf("a head pushed after the confirmation merged: %v", err)
	}
}

// The batch digest is S2's: gate s2-merge-01 over amq-noc #78 at f1f5a84 on
// base 9bedf33 is sha256:46f31e9a87fc27f3...b764a.
func TestBatchDigestIsTheS2Formula(t *testing.T) {
	d := BatchDigest("omriariav/amq-noc", "9bedf33502bc84fb24c916ab583090f8a246bda9",
		[]Pair{{Number: 78, Head: "f1f5a842597302586d983cab11c418ba294819f9"}})
	if !strings.HasPrefix(d, "sha256:46f31e9a87fc27f3") || !strings.HasSuffix(d, "b764a") {
		t.Fatalf("digest %s", d)
	}
	a := BatchDigest("o/r", "base", []Pair{{1, "h1"}, {2, "h2"}})
	if a == BatchDigest("o/r", "base", []Pair{{2, "h2"}, {1, "h1"}}) || a == BatchDigest("o/r", "base2", []Pair{{1, "h1"}, {2, "h2"}}) ||
		a == BatchDigest("o/r", "base", []Pair{{1, "h1"}, {2, "h3"}}) {
		t.Fatal("reorder, new base or new head must change the digest")
	}
}

func TestDeclaredAndProtectedMatchers(t *testing.T) {
	for _, tc := range []struct {
		path  string
		globs []string
		want  bool
	}{
		{"cmd/sheets.go", []string{"cmd/sheets.go"}, true},
		{"cmd/sheets_test.go", []string{"cmd/sheets.go"}, false},
		{"internal/console/testdata/x.golden", []string{"internal/console/testdata/"}, true},
		{"internal/console/noc.go", []string{"internal/console/*_test.go"}, false},
		{"a/b/c/d.go", []string{"a/**/d.go"}, true},
		{"a/d.go", []string{"a/**/d.go"}, true},
		{"x.go", []string{"**"}, true},
	} {
		if got := Declared(tc.path, tc.globs); got != tc.want {
			t.Errorf("Declared(%q, %v) = %v", tc.path, tc.globs, got)
		}
	}
	entries := grants.Defaults().ProtectedEntries()
	for p, want := range map[string]bool{".amq-squad/team-rules.md": true, "CLAUDE.md": true, ".claude/settings.local.json": true,
		".claude/hooks/x.sh": true, "squad.yaml": true, "cmd/root.go": false, "docs/CLAUDE.md.txt": false} {
		if _, got := protectedHit(p, entries); got != want {
			t.Errorf("protectedHit(%q) = %v", p, got)
		}
	}
}

func TestEvaluateProtection(t *testing.T) {
	var rules []ruleJSON
	if err := json.Unmarshal([]byte(ciOnlyRuleset), &rules); err != nil {
		t.Fatal(err)
	}
	p := evaluateProtection(rules, map[int]int{7: 0}, nil)
	if !p.Met() || !p.ForcePushRefused || !slices.Equal(p.Checks, []RequiredCheck{{Context: "ci", AppID: 15368}}) {
		t.Fatalf("S2 ruleset: %+v", p)
	}
	if evaluateProtection(rules, map[int]int{7: 1}, nil).Met() {
		t.Fatal("a ruleset with a bypass actor refuses no direct push by the owner")
	}
}

// The S6 amq-noc shape: no required check ran at the head and the base moved
// on (the CI workflow landed after the PR was opened). The plan marks it
// rebase-for-CI, not refused; the real run rebases it, CI runs on the new
// head, the check re-runs and it lands.
func TestCIAbsentOnABehindBaseIsRebasedNotRefused(t *testing.T) {
	f := newFixture(t)
	f.req.PRs = []Item{f.req.PRs[0], f.req.PRs[2]}
	f.edit(func(s *fakeState) { s.NoCI = []string{f.head("pr1")} })
	f.branch("advance", "main", map[string]string{"ci.yml": "ci\n"})
	mustGit(t, f.work, "push", "-q", "origin", "advance:main")
	f.train.DryRun = true
	plan, err := f.train.Run(context.Background(), f.req)
	if err != nil || !plan.PRs[0].RebaseForCI || plan.PRs[0].Status != "planned" || plan.PRs[1].RebaseForCI {
		t.Fatalf("plan %v %+v", err, plan.PRs)
	}
	f.train.DryRun = false
	res, err := f.train.Run(context.Background(), f.req)
	if err != nil || res.PRs[0].Status != "merged" || res.PRs[0].Head == plan.PRs[0].Head {
		t.Fatalf("run %v %+v", err, res.PRs)
	}
	// with no check run and a current base, CI absent is a refusal
	g := newFixture(t)
	g.req.PRs = g.req.PRs[:1]
	g.edit(func(s *fakeState) { s.NoCI = []string{g.head("pr1")} })
	if res, err := g.train.Run(context.Background(), g.req); err != nil || res.PRs[0].Status != "refused" {
		t.Fatalf("CI absent on a current base: %v %+v", err, res.PRs)
	}
}

// Ruling 12 detection: a tag or release that appears after the baseline
// with no accepted owner gate is an owner alert, at every run; an accepted
// gate for it (the release flow's or a seat's gated command) covers it.
func TestDriftAlertsOnUngatedTagsAndReleases(t *testing.T) {
	f := newFixture(t)
	mustGit(t, f.bare, "tag", "v0", "main")
	f.train.ReleaseGates, f.train.Root = &f.store, filepath.Join(f.dir, "root")
	f.train.DryRun = true
	f.req.PRs = f.req.PRs[:1]
	if res, err := f.train.Run(context.Background(), f.req); err != nil || len(res.Alerts) != 0 {
		t.Fatalf("first run records the baseline: %v %+v", err, res.Alerts)
	}
	head := f.head("main")
	mustGit(t, f.bare, "tag", "v1", "main")
	f.edit(func(s *fakeState) { s.Releases = []Release{{ID: 11, Tag: "v1"}} })
	res, err := f.train.Run(context.Background(), f.req)
	if err != nil || len(res.Alerts) != 2 || res.Alerts[0].Kind != "tag" || res.Alerts[0].SHA != head || res.Alerts[1].Kind != "release" {
		t.Fatalf("ungated tag and release: %v %+v", err, res.Alerts)
	}
	if got := ReadAlerts(f.train.Root); len(got) != 2 {
		t.Fatalf("alerts on disk %+v", got)
	}
	// an accepted gate for the tag covers it; the release stays an alert
	rec, err := gatestore.Render(gatestore.Spec{GateID: "rel-v1", Action: "tag", Target: "git tag v1", RaisedBy: "seat"}, time.Now(), gateIdentity())
	if err != nil || f.store.Create(rec) != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.dir, "root", "extensions", "amq-squad", "consumed", "gates", "rel-v1"), "approved test\n")
	res, err = f.train.Run(context.Background(), f.req)
	if err != nil || len(res.Alerts) != 1 || res.Alerts[0].Kind != "release" {
		t.Fatalf("after the tag's gate: %v %+v", err, res.Alerts)
	}
	entries, _ := f.train.Audit.Read()
	alerts := 0
	for _, e := range entries {
		if e.Kind == "alert" {
			alerts++
		}
	}
	if alerts != 3 {
		t.Fatalf("audit alert entries %d", alerts)
	}
}
