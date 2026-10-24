package routing

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

var updateFixture = flag.Bool("update-fixture", false, "rewrite testdata/outcomes-week.jsonl")

// ReportNow is the end of the fixture week.
var ReportNow = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func i64(v int64) *int64     { return &v }
func f64(v float64) *float64 { return &v }
func iptr(v int) *int        { return &v }

// fixtureWeek is the fixture log of acceptance 6: seven buckets, each built
// to call for a different proposal, plus a runtime (codex) whose rows lack
// token evidence and an entry (local pi) with no owner price.
func fixtureWeek() []Row {
	var rows []Row
	at := ReportNow.Add(-3 * 24 * time.Hour)
	n := 0
	add := func(class, tier, entry string, files int, outcome string, cost *float64, tokens bool, waitMS int64, waits []string) Row {
		n++
		r := Row{Schema: RowSchema, At: at, Task: fmt.Sprintf("t%d", n), Attempt: 1, Class: class,
			Features:  Features{DeclaredFiles: files, SizeBand: SizeBand(files), Privacy: "vendor"},
			TableTier: tier, UsedTier: tier, Entry: entry, Runtime: strings.SplitN(entry, ":", 2)[0], Skipped: []Skip{},
			Policy: PolicyTable, Propensity: 0.95, ExplorationShare: 0.05, CostUSD: cost, WallMS: 60_000, WaitMS: waitMS,
			WaitReasons: append([]string{}, waits...), Outcome: outcome, ConsumedSmolOutputs: []string{},
			VerifierResult: verifierResult(Default().Classes[class].Verifier, outcome), Holder: "seat", GrantedAt: at, EndedAt: at.Add(time.Minute)}
		if tokens {
			r.TokensIn, r.TokensOut = i64(1000), i64(200)
		}
		at = at.Add(time.Hour)
		return r
	}
	retry := func(r Row, tier, entry string, cost float64) Row {
		r2 := r
		r2.Attempt, r2.UsedTier, r2.Entry, r2.Outcome, r2.CostUSD = 2, tier, entry, task.OutcomeCompleted, f64(cost)
		r2.Skipped = []Skip{{Entry: r.Entry, Reason: SkipUsedBefore}}
		r2.VerifierResult = VerifierPass
		r2.GrantedAt, r2.EndedAt = r.EndedAt.Add(time.Minute), r.EndedAt.Add(3*time.Minute)
		return r2
	}
	// A: extract smol band 1, half redone on task: raise.
	for i := 0; i < 4; i++ {
		r := add("extract", TierSmol, "claude:haiku:low", 1, task.OutcomeCompleted, f64(0.001), true, 1_000, nil)
		if i < 2 {
			r.Outcome, r.VerifierResult = task.OutcomeFailed, VerifierFail
			rows = append(rows, r, retry(r, TierTask, "claude:sonnet:medium", 0.01))
			continue
		}
		rows = append(rows, r)
	}
	// B: script smol band 2-3 costs more a task than script on task: dominated.
	for i := 0; i < 4; i++ {
		rows = append(rows, add("script", TierSmol, "claude:haiku:low", 2, task.OutcomeCompleted, f64(0.01), true, 1_000, nil))
	}
	for i := 0; i < 3; i++ {
		r := add("script", TierTask, "claude:sonnet:medium", 2, task.OutcomeCompleted, f64(0.005), true, 1_000, nil)
		r.Policy, r.Propensity, r.TableTier = PolicyExplore, 0.05, TierSmol
		rows = append(rows, r)
	}
	// C: summarize smol band 1 on the local model (no owner price); a review
	// that consumed two of its outputs found 4 issues.
	var smolKeys []string
	for i := 0; i < 3; i++ {
		r := add("summarize", TierSmol, "pi:ollama/qwen3.6:27b:low", 1, task.OutcomeCompleted, nil, true, 1_000, nil)
		rows = append(rows, r)
		if i < 2 {
			smolKeys = append(smolKeys, key(r.Task, r.Attempt))
		}
	}
	rv := add("review", TierAdvisor, "claude:opus:xhigh", 1, task.OutcomeCompleted, f64(0.2), true, 1_000, nil)
	rv.ReviewFindings, rv.ConsumedSmolOutputs = iptr(4), smolKeys
	rows = append(rows, rv)
	// D: implement task band 2-3 waits 20 minutes on the concurrency cap.
	for i := 0; i < 3; i++ {
		rows = append(rows, add("implement", TierTask, "claude:sonnet:medium", 3, task.OutcomeCompleted, f64(0.05), true, 20*60_000,
			[]string{task.WaitConcurrencyCap}))
	}
	// E: implement task band 1, five clean tasks: try a tier down.
	for i := 0; i < 5; i++ {
		rows = append(rows, add("implement", TierTask, "claude:sonnet:medium", 1, task.OutcomeCompleted, f64(0.02), true, 1_000, nil))
	}
	// F: plan on Codex with no token evidence: the runtime is excluded.
	for i := 0; i < 3; i++ {
		rows = append(rows, add("plan", TierPlan, "codex:gpt-5.6-sol:high", 1, task.OutcomeCompleted, nil, false, 1_000, nil))
	}
	// Outside the window: ignored.
	old := add("plan", TierPlan, "claude:opus:high", 1, task.OutcomeCompleted, f64(1), true, 0, nil)
	old.EndedAt = ReportNow.Add(-8 * 24 * time.Hour)
	rows = append(rows, old)
	return rows
}

func fixturePath() string { return filepath.Join("testdata", "outcomes-week.jsonl") }

// The committed fixture log is the generator's output (rewrite it with
// go test -run TestFixtureLogIsCurrent -update-fixture ./internal/routing/).
func TestFixtureLogIsCurrent(t *testing.T) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	for _, r := range fixtureWeek() {
		if err := enc.Encode(r); err != nil {
			t.Fatal(err)
		}
	}
	if *updateFixture {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fixturePath(), buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(fixturePath())
	if err != nil || !bytes.Equal(got, buf.Bytes()) {
		t.Fatalf("testdata/outcomes-week.jsonl is stale (rerun with -update-fixture): %v", err)
	}
}

// Acceptance 6: the weekly report on a fixture log prints one proposal per
// bucket and excludes runtimes with incomplete evidence.
func TestAcceptance6WeeklyReport(t *testing.T) {
	rows, skipped, err := ReadRows(fixturePath())
	if err != nil || skipped != 0 {
		t.Fatalf("fixture: %v, %d skipped", err, skipped)
	}
	rep := Weekly(rows, ReportNow, Default())
	if rep.Rows != len(rows)-1 || len(rep.Excluded) != 1 || rep.Excluded[0].Runtime != "codex" || rep.Excluded[0].Incomplete != 3 {
		t.Fatalf("window %d rows, excluded %+v", rep.Rows, rep.Excluded)
	}
	if rep.Used != rep.Rows-3 || len(rep.Unpriced) != 1 || rep.Unpriced[0] != "pi:ollama/qwen3.6:27b:low" {
		t.Fatalf("used %d, unpriced %v", rep.Used, rep.Unpriced)
	}
	want := map[string]string{
		"extract smol band 1":     "raise extract size band 1 from smol to task: 2 of 4 tasks redone",
		"extract task band 1":     "no change: 2 task(s)",
		"script smol band 2-3":    "raise script size band 2-3 from smol to task: cost with redo 0.0100 USD a task against 0.0050 USD one tier up",
		"script task band 2-3":    "no change: 3 tasks, no signal",
		"summarize smol band 1":   "raise summarize size band 1 from smol to task: 4 review finding(s) traced",
		"implement task band 1":   "try implement size band 1 on smol, one tier down: 5 of 5 tasks passed their verifier, no redo",
		"implement task band 2-3": "change the task chain: mean wait 20m0s, mostly concurrency_cap",
		"review advisor band 1":   "no change: 1 task(s)",
	}
	if len(rep.Buckets) != len(want) {
		t.Fatalf("%d buckets: %+v", len(rep.Buckets), rep.Buckets)
	}
	for _, b := range rep.Buckets {
		w, ok := want[b.Bucket.String()]
		if !ok || !strings.HasPrefix(b.Proposal, w) {
			t.Errorf("%s: proposal %q, want %q", b.Bucket, b.Proposal, w)
		}
		if b.Class == "plan" {
			t.Errorf("an excluded runtime's bucket is in the report: %+v", b)
		}
	}
	var out bytes.Buffer
	rep.Print(&out)
	text := out.String()
	if strings.Count(text, "  proposal: ") != len(rep.Buckets) || !strings.Contains(text, "excluded runtime codex: 3 of 3 rows lack token") ||
		!strings.Contains(text, "none is applied") || !strings.Contains(text, "no owner price") {
		t.Fatalf("printed report:\n%s", text)
	}
	if p := os.Getenv("ROUTING_REPORT_SAMPLE"); p != "" {
		os.WriteFile(p, out.Bytes(), 0o644)
	}
}

// Redo is counted only when a later attempt ran on a higher tier, and a
// task with an unpriced attempt has no known cost.
func TestReportRedoAndCostRules(t *testing.T) {
	base := Row{Schema: RowSchema, Task: "t1", Class: "extract", UsedTier: TierSmol, Entry: "claude:haiku:low", Runtime: "claude",
		TokensIn: i64(1), TokensOut: i64(1), CostUSD: f64(0.1), EndedAt: ReportNow.Add(-time.Hour)}
	fail := base
	fail.Attempt, fail.Outcome = 1, task.OutcomeFailed
	same := base
	same.Attempt, same.Outcome, same.Entry = 2, task.OutcomeCompleted, "codex:gpt-5.6-luna:low"
	same.Runtime = "codex"
	rep := Weekly([]Row{fail, same}, ReportNow, Default())
	if len(rep.Buckets) != 1 || rep.Buckets[0].Redone != 0 || rep.Buckets[0].CostUSD == nil || *rep.Buckets[0].CostUSD != 0.2 {
		t.Fatalf("same-tier retry: %+v", rep.Buckets)
	}
	same.CostUSD = nil
	rep = Weekly([]Row{fail, same}, ReportNow, Default())
	if rep.Buckets[0].CostUSD != nil {
		t.Fatalf("unpriced attempt counted: %v", *rep.Buckets[0].CostUSD)
	}
	// Five tasks with no redo and no success never earn a tier down (Codex
	// pass 1, P2-8): only verifier-passing completions count as clean.
	var rows []Row
	for i := 0; i < 5; i++ {
		r := Row{Schema: RowSchema, Task: fmt.Sprintf("t%d", i), Attempt: 1, Class: "implement", UsedTier: TierTask,
			Entry: "claude:sonnet:medium", Runtime: "claude", TokensIn: i64(1), TokensOut: i64(1), CostUSD: f64(0.01),
			Outcome: task.OutcomeExpired, VerifierResult: VerifierNotRun, EndedAt: ReportNow.Add(-time.Hour)}
		rows = append(rows, r)
	}
	rep = Weekly(rows, ReportNow, Default())
	if len(rep.Buckets) != 1 || rep.Buckets[0].Passed != 0 || strings.HasPrefix(rep.Buckets[0].Proposal, "try ") {
		t.Fatalf("no success, proposal %q (%+v)", rep.Buckets[0].Proposal, rep.Buckets[0])
	}
}
