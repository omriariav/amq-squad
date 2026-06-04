package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
