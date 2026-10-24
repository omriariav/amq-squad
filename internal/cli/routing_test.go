package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/registry"
	"github.com/omriariav/amq-squad/v3/internal/routing"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/status"
	taskstore "github.com/omriariav/amq-squad/v3/internal/task"
)

func copyFixtureLog(t *testing.T, root string) {
	raw, err := os.ReadFile(filepath.Join("..", "routing", "testdata", "outcomes-week.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	p := routing.OutcomePath(root)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// Acceptance 6 through the verb: one proposal per bucket, the excluded
// runtime named, nothing applied.
func TestRoutingReportWeekOnTheFixtureLog(t *testing.T) {
	root := t.TempDir()
	copyFixtureLog(t, root) // before chdir: the fixture path is relative to the package
	chdir(t, t.TempDir())
	before, _ := os.ReadFile(routing.OutcomePath(root))
	args := []string{"routing", "report", "--week", "--now", "2026-10-24T12:00:00Z", "--root", root}
	out, _, err := captureOutput(t, func() error { return Run(args, "test") })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, "  proposal: ") != 8 || !strings.Contains(out, "excluded runtime codex") || !strings.Contains(out, "none is applied") {
		t.Fatalf("report:\n%s", out)
	}
	js, _, err := captureOutput(t, func() error { return Run(append(args, "--json"), "test") })
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Data routing.Report `json:"data"`
	}
	if err := json.Unmarshal([]byte(js), &env); err != nil || len(env.Data.Buckets) != 8 || env.Data.Excluded[0].Runtime != "codex" {
		t.Fatalf("json (%v): %s", err, js)
	}
	after, _ := os.ReadFile(routing.OutcomePath(root))
	if string(before) != string(after) {
		t.Fatal("the report changed the outcome log")
	}
	if _, _, err := captureOutput(t, func() error { return Run([]string{"routing", "report", "--root", root}, "test") }); err == nil || !strings.Contains(err.Error(), "--week") {
		t.Fatalf("no --week: %v", err)
	}
	if _, _, err := captureOutput(t, func() error { return Run([]string{"routing", "apply"}, "test") }); err == nil {
		t.Fatal("unknown verb accepted")
	}
}

// A routing.yaml outside the strict subset stops the report.
func TestRoutingReportRefusesABadRepositoryFile(t *testing.T) {
	root, repo := t.TempDir(), t.TempDir()
	chdir(t, repo)
	mustWrite(t, filepath.Join(repo, ".amq-squad", "routing.yaml"), "schema: 1\nexploration_share: &x 0.1\n")
	_, _, err := captureOutput(t, func() error {
		return Run([]string{"routing", "report", "--week", "--root", root}, "test")
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported YAML") {
		t.Fatalf("err = %v", err)
	}
}

// Acceptance 7: status --json names the wait reason of a task with no
// passing entry.
func TestStatusNamesTheRoutingWaitReason(t *testing.T) {
	root, repo := t.TempDir(), t.TempDir()
	chdir(t, repo)
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	prevNow := statusNow
	statusNow = func() time.Time { return now }
	t.Cleanup(func() { statusNow = prevNow })
	store, _ := taskstore.Open(root)
	if _, err := store.Add(taskstore.AddInput{Title: "rotate the vault key", Class: "implement", DeclaredFiles: []string{"internal/secrets/vault.go"}}, now); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, ".amq-squad", "routing.yaml"), "schema: 1\nprivacy_paths:\n  - glob: internal/secrets/\n    class: local\n")
	tbl, err := routing.Load(t.TempDir(), repo)
	if err != nil {
		t.Fatal(err)
	}
	reg := registry.Registry{Schema: 1, Entries: []registry.Entry{
		{Runtime: "claude", Provider: "anthropic", Model: "sonnet", Effort: "medium", Verified: true, State: registry.StateReady, PrivacyClass: "vendor", ConcurrencyCap: 6},
		{Runtime: "codex", Provider: "openai", Model: "gpt-5.6-terra", Effort: "medium", Verified: true, State: registry.StateReady, PrivacyClass: "vendor", ConcurrencyCap: 4},
	}}
	r, err := routing.New(routing.Options{Table: tbl, Registry: reg, Grants: grants.Defaults(), Root: root, Now: func() time.Time { return now },
		Tasks: func() ([]taskstore.Task, error) { return taskstore.Snapshot(store.Dir) }})
	if err != nil {
		t.Fatal(err)
	}
	s := &scheduler.Scheduler{Store: store, Now: func() time.Time { return now }, Router: r, Config: scheduler.Config{MaxSeats: 4}}
	if _, err := s.Tick(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AM_ROOT", root)
	stdout, _, err := captureOutput(t, func() error { return runStatusWithVersion([]string{"--json"}, "3.0.0-test") })
	if err != nil {
		t.Fatal(err)
	}
	var doc status.Status
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Tasks) != 1 || doc.Tasks[0].WaitReason != taskstore.WaitPrivacyClass || doc.Tasks[0].ReasonCode != taskstore.WaitPrivacyClass ||
		doc.Tasks[0].Pool != nil || doc.Tasks[0].State != status.StateWaiting {
		t.Fatalf("task row = %+v", doc.Tasks)
	}
	found := false
	for _, a := range doc.Attention {
		if a.ID == "task:t1:wait:privacy_class" && strings.Contains(a.Subject, "privacy_class") {
			found = true
		}
	}
	if !found {
		t.Fatalf("attention = %+v", doc.Attention)
	}
}

// doctor's routing row: an invalid file fails; a per-day budget with an
// unpriced verified vendor entry in the chains warns and names it.
func TestDoctorRoutingRow(t *testing.T) {
	home, repo := t.TempDir(), t.TempDir()
	if c := routingCheck(home, repo); c.Status != doctorOK || !strings.Contains(c.Detail, "13 chain entries") {
		t.Fatalf("default: %+v", c)
	}
	mustWrite(t, filepath.Join(repo, ".amq-squad", "routing.yaml"), "schema: 1\nclasses:\n  plan:\n    tier: task\n")
	if c := routingCheck(home, repo); c.Status != doctorFail || c.Kind != "routing_invalid" {
		t.Fatalf("invalid: %+v", c)
	}
	repo = t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".amq-squad"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".amq-squad", "grants.yaml"), []byte("schema: 1\ncost_per_day_usd: 5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg := registry.Registry{Schema: 1, Entries: []registry.Entry{
		{Runtime: "claude", Provider: "anthropic", Model: "sonnet", Effort: "medium", Verified: true, State: registry.StateReady, PrivacyClass: "vendor"},
		{Runtime: "claude", Provider: "anthropic", Model: "opus", Effort: "high", Verified: true, State: registry.StateReady, PrivacyClass: "vendor"},
	}}
	if err := registry.Write(registry.Path(home), reg); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, routing.UserPath(home), "schema: 1\nprices:\n  - entry: claude:opus:high\n    input_usd_per_mtok: 15\n    output_usd_per_mtok: 75\n")
	c := routingCheck(home, repo)
	if c.Status != doctorWarn || !strings.Contains(c.Detail, "claude:sonnet:medium") || strings.Contains(c.Detail, "claude:opus:high,") {
		t.Fatalf("unpriced: %+v", c)
	}
}
