package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/drafter"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestBriefRejectsGoalAndSeedFromTogether(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	if err := team.Write(dir, team.Team{Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}}}); err != nil {
		t.Fatal(err)
	}
	err := runBrief([]string{"--goal", "ship it", "--seed-from", "file:./x.md", "--session", "issue-96"})
	if err == nil || !strings.Contains(err.Error(), "--goal and --seed-from are mutually exclusive") {
		t.Fatalf("want mutual-exclusivity error, got %v", err)
	}
}

func TestBriefRequiresGoalOrSeedFrom(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	if err := team.Write(dir, team.Team{Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}}}); err != nil {
		t.Fatal(err)
	}
	err := runBrief([]string{"--session", "issue-96"})
	if err == nil || !strings.Contains(err.Error(), "brief requires --goal TEXT or --seed-from REF") {
		t.Fatalf("want missing-source error, got %v", err)
	}
}

func TestBriefSeedFromFileWritesBrief(t *testing.T) {
	dir := canonicalFilesystemPath(t.TempDir())
	chdir(t, dir)
	if err := team.Write(dir, team.Team{Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}}}); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(source, []byte("do the thing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := captureOutput(t, func() error {
		return runBrief([]string{"--seed-from", "file:" + source, "--session", "issue-96"})
	})
	if err != nil {
		t.Fatalf("brief --seed-from: %v", err)
	}
	briefPath := squadnamespace.BriefPath(dir, team.DefaultProfile, "issue-96")
	if !strings.Contains(stdout, "wrote brief "+briefPath) {
		t.Errorf("stdout missing wrote-brief line: %s", stdout)
	}
	if !strings.Contains(stdout, "amq-squad plan issue-96") {
		t.Errorf("stdout missing next-step hint: %s", stdout)
	}
	got, err := os.ReadFile(briefPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "do the thing") || !strings.Contains(string(got), "source: file:"+source) {
		t.Fatalf("seeded brief = %q, want the body plus provenance frontmatter", got)
	}
}

func TestBriefSeedFromRefusesOverwriteWithoutForce(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	if err := team.Write(dir, team.Team{Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}}}); err != nil {
		t.Fatal(err)
	}
	briefPath := squadnamespace.BriefPath(dir, team.DefaultProfile, "issue-96")
	if err := os.MkdirAll(filepath.Dir(briefPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(briefPath, []byte("# existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(source, []byte("new content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := runBrief([]string{"--seed-from", "file:" + source, "--session", "issue-96"})
	if err == nil || !strings.Contains(err.Error(), "already exists") || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("want already-exists refusal naming --force, got %v", err)
	}
	got, _ := os.ReadFile(briefPath)
	if string(got) != "# existing\n" {
		t.Fatalf("existing brief must be untouched without --force, got %q", got)
	}
	if err := runBrief([]string{"--seed-from", "file:" + source, "--session", "issue-96", "--force"}); err != nil {
		t.Fatalf("brief --force: %v", err)
	}
	got, _ = os.ReadFile(briefPath)
	if !strings.Contains(string(got), "new content") {
		t.Fatalf("--force did not overwrite; got %q", got)
	}
}

func TestBriefDryRunPrintsWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	if err := team.Write(dir, team.Team{Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}}}); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(source, []byte("dry run body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := captureOutput(t, func() error {
		return runBrief([]string{"--seed-from", "file:" + source, "--session", "issue-96", "--dry-run"})
	})
	if err != nil {
		t.Fatalf("brief --dry-run: %v", err)
	}
	if !strings.Contains(stdout, "dry run body") {
		t.Fatalf("dry-run stdout missing candidate body: %s", stdout)
	}
	briefPath := squadnamespace.BriefPath(dir, team.DefaultProfile, "issue-96")
	if _, err := os.Stat(briefPath); !os.IsNotExist(err) {
		t.Fatalf("--dry-run must not write the brief: %v", err)
	}
}

func TestBriefGoalDraftsAndWritesReviewedDocument(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	member := team.Member{Role: "dev", Handle: "dev", Binary: "codex", Session: "work"}
	if err := team.Write(dir, team.Team{Members: []team.Member{member}}); err != nil {
		t.Fatal(err)
	}
	document := validSimpleStartBriefDraft("work", "ship it", member)
	deps := simpleStartDependencies{
		ResolveDrafter: func(*drafter.Config) (drafter.Resolution, error) {
			return drafter.Resolution{Config: &drafter.Config{Chain: []string{drafter.BackendClaude}}, Source: drafter.SourceGlobal}, nil
		},
		RunDrafter: func(context.Context, *drafter.Config, drafter.Request) (drafter.Result, error) {
			return drafter.Result{Text: document, Evidence: drafter.Evidence{Backend: drafter.BackendClaude, ExitCode: 0}}, nil
		},
	}
	stdout, _, err := captureOutput(t, func() error {
		return runBriefWithDependencies([]string{"--goal", "ship it", "--session", "work"}, deps)
	})
	if err != nil {
		t.Fatalf("brief --goal: %v\n%s", err, stdout)
	}
	briefPath := squadnamespace.BriefPath(dir, team.DefaultProfile, "work")
	got, err := os.ReadFile(briefPath)
	if err != nil || string(got) != document {
		t.Fatalf("drafted brief = %q, %v; want the validated document", got, err)
	}
}

func TestBriefGoalPrintsManualPromptAndRefusesWhenInSessionOnly(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	member := team.Member{Role: "dev", Handle: "dev", Binary: "codex", Session: "work"}
	if err := team.Write(dir, team.Team{Members: []team.Member{member}}); err != nil {
		t.Fatal(err)
	}
	deps := simpleStartDependencies{
		ResolveDrafter: func(*drafter.Config) (drafter.Resolution, error) {
			return drafter.Resolution{Source: drafter.SourceInSession}, nil
		},
		RunDrafter: func(context.Context, *drafter.Config, drafter.Request) (drafter.Result, error) {
			return drafter.Result{
				UseInSession: true, Reason: "no external drafter is configured", Remedy: "complete the filled prompt in session",
				Evidence: drafter.Evidence{Backend: drafter.BackendInSession, ExitCode: 0},
			}, nil
		},
	}
	stdout, _, err := captureOutput(t, func() error {
		return runBriefWithDependencies([]string{"--goal", "ship it", "--session", "work"}, deps)
	})
	if err == nil || !strings.Contains(err.Error(), "in-session completion") {
		t.Fatalf("want in-session-completion refusal, got %v", err)
	}
	for _, want := range []string{"No brief was staged.", "Manual drafting prompt:", "ship it"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
	briefPath := squadnamespace.BriefPath(dir, team.DefaultProfile, "work")
	if _, err := os.Stat(briefPath); !os.IsNotExist(err) {
		t.Fatalf("manual-drafting path must not stage a brief: %v", err)
	}
}
