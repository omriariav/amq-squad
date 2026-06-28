package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestRunBriefReadsExisting(t *testing.T) {
	project := t.TempDir()
	writeTestBrief(t, project, "issue-96", "# issue-96\n\nShip the brief reader.\n")

	stdout, stderr, err := captureOutput(t, func() error {
		return runBrief([]string{"--project", project, "--session", "issue-96"})
	})
	if err != nil {
		t.Fatalf("runBrief: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		"# amq-squad brief",
		"# project: " + project,
		"# session: issue-96",
		"# path: " + briefPath(project, "issue-96"),
		"# kind: real",
		"Ship the brief reader.",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("brief output missing %q:\n%s", want, stdout)
		}
	}
}

func TestRunBriefMissingIsReadable(t *testing.T) {
	project := t.TempDir()
	stdout, stderr, err := captureOutput(t, func() error {
		return runBrief([]string{"--project", project, "--session", "ghost"})
	})
	if err != nil {
		t.Fatalf("runBrief missing: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		"# kind: none",
		"# path: " + briefPath(project, "ghost"),
		"(no brief)",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("missing brief output missing %q:\n%s", want, stdout)
		}
	}
}

func TestRunBriefJSON(t *testing.T) {
	project := t.TempDir()
	content := "# issue-96\n\nShip the JSON reader.\n"
	writeTestBrief(t, project, "issue-96", content)

	stdout, stderr, err := captureOutput(t, func() error {
		return runBrief([]string{"--project", project, "--session", "issue-96", "--json"})
	})
	if err != nil {
		t.Fatalf("runBrief --json: %v\nstderr:\n%s", err, stderr)
	}
	env := decodeJSONEnvelope[briefEnvelopeData](t, stdout)
	if env.Kind != "brief" {
		t.Fatalf("kind = %q, want brief", env.Kind)
	}
	if env.Data.ProjectDir != project || env.Data.Session != "issue-96" || env.Data.Path != briefPath(project, "issue-96") {
		t.Fatalf("brief data mismatch: %+v", env.Data)
	}
	if env.Data.Kind != "real" || !env.Data.Exists || env.Data.Content != content {
		t.Fatalf("brief JSON classification/content mismatch: %+v", env.Data)
	}
}

func TestRunBriefJSONCarriesNamedProfileNamespace(t *testing.T) {
	project := t.TempDir()
	content := "# issue-96\n\nShip the profile-aware JSON reader.\n"
	writeTestBrief(t, project, "issue-96", content)

	stdout, stderr, err := captureOutput(t, func() error {
		return runBrief([]string{"--project", project, "--profile", "release", "--session", "issue-96", "--json"})
	})
	if err != nil {
		t.Fatalf("runBrief --profile --json: %v\nstderr:\n%s", err, stderr)
	}
	env := decodeJSONEnvelope[briefEnvelopeData](t, stdout)
	if env.Data.Profile != "release" || env.Data.Namespace.ID != "release/issue-96" {
		t.Fatalf("brief namespace/profile mismatch: %+v", env.Data)
	}
}

func TestRunBriefDuplicateProfileSessionUsesNamespacedStore(t *testing.T) {
	project := t.TempDir()
	for _, profile := range []string{"product", "release"} {
		if err := team.WriteProfile(project, profile, team.Team{
			Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "main"}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	releasePath := briefPathForProfile(project, "release", "main")
	if err := os.MkdirAll(filepath.Dir(releasePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(releasePath, []byte("# main\n\nRelease profile brief.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := captureOutput(t, func() error {
		return runBrief([]string{"--project", project, "--profile", "release", "--session", "main", "--json"})
	})
	if err != nil {
		t.Fatalf("duplicate profile/session should use namespaced brief store: %v", err)
	}
	env := decodeJSONEnvelope[briefEnvelopeData](t, stdout)
	if env.Data.Path != releasePath || !strings.Contains(env.Data.Content, "Release profile brief.") {
		t.Fatalf("brief envelope used wrong store: %+v", env.Data)
	}
}

func TestRunBriefRequiresSession(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return runBrief([]string{"--project", t.TempDir()})
	})
	if err == nil {
		t.Fatal("runBrief without --session should fail")
	}
	if !strings.Contains(err.Error(), "requires --session") {
		t.Fatalf("error should mention required session, got %v", err)
	}
}

func TestRunBriefSeedDryRunDoesNotWrite(t *testing.T) {
	swapSeedClock(t)
	project := t.TempDir()
	source := filepath.Join(project, "brief.md")
	if err := os.WriteFile(source, []byte("# Seeded\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := captureOutput(t, func() error {
		return runBrief([]string{"seed", "--project", project, "--session", "issue-96", "--seed-from", "file:" + source, "--dry-run"})
	})
	if err != nil {
		t.Fatalf("brief seed --dry-run: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		"# amq-squad brief seed",
		"# mode: dry-run",
		"source: file:" + source,
		"generated_at: 2026-05-17T12:00:00Z",
		"# Seeded",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("brief seed dry-run output missing %q:\n%s", want, stdout)
		}
	}
	if _, err := os.Stat(briefPath(project, "issue-96")); err == nil {
		t.Fatal("brief seed --dry-run wrote a brief")
	}
}

func TestRunBriefSeedWritesBrief(t *testing.T) {
	swapSeedClock(t)
	project := t.TempDir()
	source := filepath.Join(project, "brief.md")
	if err := os.WriteFile(source, []byte("# Seeded\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := captureOutput(t, func() error {
		return runBrief([]string{"seed", "--project", project, "--session", "issue-96", "--seed-from", "file:" + source})
	})
	if err != nil {
		t.Fatalf("brief seed: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "# mode: written") {
		t.Fatalf("brief seed output should report written mode:\n%s", stdout)
	}
	got, err := os.ReadFile(briefPath(project, "issue-96"))
	if err != nil {
		t.Fatalf("seeded brief missing: %v", err)
	}
	for _, want := range []string{
		"source: file:" + source,
		"generated_at: 2026-05-17T12:00:00Z",
		"# Seeded",
	} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("seeded brief missing %q:\n%s", want, got)
		}
	}
}

func TestRunBriefSeedJSON(t *testing.T) {
	swapSeedClock(t)
	project := t.TempDir()
	source := filepath.Join(project, "brief.md")
	if err := os.WriteFile(source, []byte("# Seeded JSON\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := captureOutput(t, func() error {
		return runBrief([]string{"seed", "--project", project, "--session", "issue-96", "--seed-from", "file:" + source, "--dry-run", "--json"})
	})
	if err != nil {
		t.Fatalf("brief seed --json: %v\nstderr:\n%s", err, stderr)
	}
	env := decodeJSONEnvelope[briefSeedEnvelopeData](t, stdout)
	if env.Kind != "brief_seed" {
		t.Fatalf("kind = %q, want brief_seed", env.Kind)
	}
	if env.Data.ProjectDir != project || env.Data.Session != "issue-96" || env.Data.Source != "file:"+source {
		t.Fatalf("brief_seed data mismatch: %+v", env.Data)
	}
	if !env.Data.DryRun || env.Data.Written || env.Data.GeneratedAt != "2026-05-17T12:00:00Z" {
		t.Fatalf("brief_seed dry-run metadata mismatch: %+v", env.Data)
	}
	if !strings.Contains(env.Data.Content, "# Seeded JSON") {
		t.Fatalf("brief_seed content missing body: %+v", env.Data)
	}
}

func TestRunBriefSeedJSONCarriesNamedProfileNamespace(t *testing.T) {
	swapSeedClock(t)
	project := t.TempDir()
	source := filepath.Join(project, "brief.md")
	if err := os.WriteFile(source, []byte("# Seeded Profile JSON\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := captureOutput(t, func() error {
		return runBrief([]string{"seed", "--project", project, "--profile", "release", "--session", "issue-96", "--seed-from", "file:" + source, "--dry-run", "--json"})
	})
	if err != nil {
		t.Fatalf("brief seed --profile --json: %v\nstderr:\n%s", err, stderr)
	}
	env := decodeJSONEnvelope[briefSeedEnvelopeData](t, stdout)
	if env.Data.Profile != "release" || env.Data.Namespace.ID != "release/issue-96" {
		t.Fatalf("brief_seed namespace/profile mismatch: %+v", env.Data)
	}
}

func TestRunBriefSeedRefusesExistingWithoutForce(t *testing.T) {
	swapSeedClock(t)
	project := t.TempDir()
	writeTestBrief(t, project, "issue-96", "KEEP ME\n")
	source := filepath.Join(project, "brief.md")
	if err := os.WriteFile(source, []byte("REPLACE ME\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := captureOutput(t, func() error {
		return runBrief([]string{"seed", "--project", project, "--session", "issue-96", "--seed-from", "file:" + source})
	})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("brief seed should refuse existing brief without --force, got %v", err)
	}
	got, err := os.ReadFile(briefPath(project, "issue-96"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "KEEP ME\n" {
		t.Fatalf("refused seed modified brief:\n%s", got)
	}
}

func TestRunBriefSeedForceOverwrites(t *testing.T) {
	swapSeedClock(t)
	project := t.TempDir()
	writeTestBrief(t, project, "issue-96", "OLD\n")
	source := filepath.Join(project, "brief.md")
	if err := os.WriteFile(source, []byte("NEW\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runBrief([]string{"seed", "--project", project, "--session", "issue-96", "--seed-from", "file:" + source, "--force"})
	}); err != nil {
		t.Fatalf("brief seed --force: %v", err)
	}
	got, err := os.ReadFile(briefPath(project, "issue-96"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "NEW") || strings.Contains(string(got), "OLD") {
		t.Fatalf("force did not replace brief:\n%s", got)
	}
}

// decision tests

var fixedDecisionTime = time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)

func withFixedDecisionTime(t *testing.T) {
	t.Helper()
	prev := decisionNow
	decisionNow = func() time.Time { return fixedDecisionTime }
	t.Cleanup(func() { decisionNow = prev })
}

func TestFormatDecisionEntryWithTitle(t *testing.T) {
	got := formatDecisionEntry(fixedDecisionTime, "stdlib only", "No external deps.")
	want := "### 2026-06-21 — stdlib only\nNo external deps.\n"
	if got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestFormatDecisionEntryWithoutTitle(t *testing.T) {
	got := formatDecisionEntry(fixedDecisionTime, "", "No external deps.")
	want := "### 2026-06-21\nNo external deps.\n"
	if got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestAppendBriefDecisionCreatesDecisionsSection(t *testing.T) {
	project := t.TempDir()
	writeTestBrief(t, project, "s1", "# s1\n\nGoal: ship.\n")

	path, err := appendBriefDecision(project, "s1", "stdlib only", "No external deps.", fixedDecisionTime)
	if err != nil {
		t.Fatalf("appendBriefDecision: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(got)
	for _, want := range []string{
		"## Decisions",
		"### 2026-06-21 — stdlib only",
		"No external deps.",
		"Goal: ship.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
	if strings.Index(body, "## Decisions") < strings.Index(body, "Goal: ship.") {
		t.Errorf("## Decisions should come after existing content, not before")
	}
}

func TestAppendBriefDecisionAppendsToExistingSection(t *testing.T) {
	project := t.TempDir()
	writeTestBrief(t, project, "s2", "# s2\n\n## Decisions\n\n### 2026-06-20 — first\nFirst decision.\n")

	_, err := appendBriefDecision(project, "s2", "second", "Second decision.", fixedDecisionTime)
	if err != nil {
		t.Fatalf("appendBriefDecision: %v", err)
	}

	got, err := os.ReadFile(briefPath(project, "s2"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(got)
	if !strings.Contains(body, "### 2026-06-20 — first") {
		t.Errorf("first entry should be preserved:\n%s", body)
	}
	if !strings.Contains(body, "### 2026-06-21 — second") {
		t.Errorf("second entry missing:\n%s", body)
	}
	if strings.Index(body, "first") > strings.Index(body, "second") {
		t.Errorf("first entry should appear before second in file")
	}
	if strings.Count(body, "## Decisions") != 1 {
		t.Errorf("## Decisions header should appear exactly once:\n%s", body)
	}
}

func TestAppendBriefDecisionCreatesFileWhenMissing(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".amq-squad", "briefs"), 0o755); err != nil {
		t.Fatal(err)
	}

	path, err := appendBriefDecision(project, "new-session", "first", "Body text.", fixedDecisionTime)
	if err != nil {
		t.Fatalf("appendBriefDecision on missing brief: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(got)
	if !strings.Contains(body, "## Decisions") || !strings.Contains(body, "### 2026-06-21 — first") {
		t.Fatalf("unexpected content for new file:\n%s", body)
	}
}

func TestAppendBriefDecisionWithoutTitleOmitsDash(t *testing.T) {
	project := t.TempDir()
	writeTestBrief(t, project, "s3", "# s3\n")

	_, err := appendBriefDecision(project, "s3", "", "No title here.", fixedDecisionTime)
	if err != nil {
		t.Fatalf("appendBriefDecision: %v", err)
	}

	got, _ := os.ReadFile(briefPath(project, "s3"))
	if strings.Contains(string(got), " — ") {
		t.Errorf("no-title entry should not contain ' — ':\n%s", got)
	}
	if !strings.Contains(string(got), "### 2026-06-21\n") {
		t.Errorf("no-title entry should use bare date heading:\n%s", got)
	}
}

func TestAppendBriefDecisionDoesNotDuplicateHeader(t *testing.T) {
	project := t.TempDir()
	writeTestBrief(t, project, "s4", "# s4\n\n## Decisions\n\n")

	for i := 0; i < 3; i++ {
		if _, err := appendBriefDecision(project, "s4", "d", "body", fixedDecisionTime); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	got, _ := os.ReadFile(briefPath(project, "s4"))
	if count := strings.Count(string(got), "## Decisions"); count != 1 {
		t.Errorf("## Decisions appears %d times, want 1:\n%s", count, got)
	}
}

func TestRunBriefDecisionAppendsEntry(t *testing.T) {
	withFixedDecisionTime(t)
	project := t.TempDir()
	writeTestBrief(t, project, "issue-96", "# issue-96\n\nGoal: ship.\n")

	_, stderr, err := captureOutput(t, func() error {
		return runBrief([]string{"decision",
			"--project", project,
			"--session", "issue-96",
			"--title", "stdlib only",
			"--body", "No external deps.",
		})
	})
	if err != nil {
		t.Fatalf("runBrief decision: %v\nstderr:\n%s", err, stderr)
	}

	got, _ := os.ReadFile(briefPath(project, "issue-96"))
	body := string(got)
	if !strings.Contains(body, "## Decisions") || !strings.Contains(body, "### 2026-06-21 — stdlib only") {
		t.Errorf("decision not appended:\n%s", body)
	}
	if !strings.Contains(stderr, "appended decision to") {
		t.Errorf("success notice missing from stderr:\n%s", stderr)
	}
}

func TestRunBriefDecisionRequiresSession(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return runBrief([]string{"decision", "--body", "x"})
	})
	if err == nil || !strings.Contains(err.Error(), "--session") {
		t.Fatalf("missing --session should error, got %v", err)
	}
}

func TestRunBriefDecisionRequiresBody(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return runBrief([]string{"decision", "--session", "s"})
	})
	if err == nil || !strings.Contains(err.Error(), "--body") {
		t.Fatalf("missing --body should error, got %v", err)
	}
}

func writeTestBrief(t *testing.T, project, session, content string) {
	t.Helper()
	path := filepath.Join(project, ".amq-squad", "briefs", session+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
