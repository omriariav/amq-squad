package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func seedForkTeam(t *testing.T, dir string) {
	t.Helper()
	if err := team.Write(dir, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "issue-96"},
		},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRunForkRequiresFromAndAs(t *testing.T) {
	dir := t.TempDir()
	resumeChdir(t, dir)
	seedForkTeam(t, dir)

	cases := [][]string{
		nil,
		{"--from", "src"},
		{"--as", "dst"},
	}
	for _, args := range cases {
		_, _, err := captureOutput(t, func() error { return runFork(args) })
		if err == nil {
			t.Fatalf("fork %v should fail without both --from and --as", args)
		}
		if _, ok := err.(UsageError); !ok {
			t.Fatalf("fork %v: want UsageError, got %T: %v", args, err, err)
		}
	}
}

func TestRunForkRejectsInvalidSessionNames(t *testing.T) {
	dir := t.TempDir()
	resumeChdir(t, dir)
	seedForkTeam(t, dir)
	_, _, err := captureOutput(t, func() error {
		return runFork([]string{"--from", "BadCase", "--as", "issue-97"})
	})
	if err == nil || !strings.Contains(err.Error(), "invalid --from") {
		t.Fatalf("want invalid --from error, got %v", err)
	}
	_, _, err = captureOutput(t, func() error {
		return runFork([]string{"--from", "issue-96", "--as", "bad case"})
	})
	if err == nil || !strings.Contains(err.Error(), "invalid --as") {
		t.Fatalf("want invalid --as error, got %v", err)
	}
}

func TestRunForkRejectsSameSourceAndTarget(t *testing.T) {
	dir := t.TempDir()
	resumeChdir(t, dir)
	seedForkTeam(t, dir)
	_, _, err := captureOutput(t, func() error {
		return runFork([]string{"--from", "issue-96", "--as", "issue-96"})
	})
	if err == nil || !strings.Contains(err.Error(), "must differ") {
		t.Fatalf("want same-source/target error, got %v", err)
	}
}

func TestRunForkRejectsSourceWithoutState(t *testing.T) {
	dir := t.TempDir()
	setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)
	seedForkTeam(t, dir)
	_, _, err := captureOutput(t, func() error {
		return runFork([]string{"--from", "issue-96", "--as", "issue-97"})
	})
	if err == nil || !strings.Contains(err.Error(), "nothing to fork from") {
		t.Fatalf("want missing-source error, got %v", err)
	}
}

func TestRunForkPlansFreshIntoTargetWithSourceRecord(t *testing.T) {
	dir := t.TempDir()
	base := setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)
	seedForkTeam(t, dir)
	// SOURCE has a restorable record for one configured member.
	writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", StartedAt: time.Now(),
	})

	stdout, _, err := captureOutput(t, func() error {
		return runFork([]string{"--from", "issue-96", "--as", "issue-97", "--no-bootstrap"})
	})
	if err != nil {
		t.Fatalf("fork: %v", err)
	}
	for _, want := range []string{
		"# amq-squad fork",
		"# from:       issue-96",
		"# to:         issue-97",
		"--session issue-97 --team-workstream",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output missing %q in:\n%s", want, stdout)
		}
	}
	// Source must never become the launch session for emitted commands.
	if strings.Contains(stdout, "--session issue-96 --team-workstream") {
		t.Errorf("fork leaked SOURCE as launch session:\n%s", stdout)
	}
	// fork must not surface legacy command labels.
	if strings.Contains(stdout, "amq-squad team resume") {
		t.Errorf("fork leaked 'amq-squad team resume' header:\n%s", stdout)
	}
	if strings.Contains(stdout, "team launch") {
		t.Errorf("fork must suggest 'up', not 'team launch':\n%s", stdout)
	}
	if !strings.Contains(stdout, "up --fresh --session issue-97") {
		t.Errorf("fork footer should suggest 'up --fresh --session TARGET':\n%s", stdout)
	}
}

func TestRunForkProjectTargetsOtherDir(t *testing.T) {
	project := t.TempDir()
	other := t.TempDir()
	base := setupFakeAMQSessionRoots(t)
	resumeChdir(t, other)
	seedForkTeam(t, project)
	writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
		CWD: project, Binary: "codex", Role: "cto", StartedAt: time.Now(),
	})

	stdout, _, err := captureOutput(t, func() error {
		return runFork([]string{"--project", project, "--from", "issue-96", "--as", "issue-97", "--no-bootstrap"})
	})
	if err != nil {
		t.Fatalf("fork --project: %v", err)
	}
	if !strings.Contains(stdout, "cd "+shellQuote(project)) {
		t.Errorf("fork --project should emit commands for requested project:\n%s", stdout)
	}
	if strings.Contains(stdout, "cd "+shellQuote(other)) {
		t.Errorf("fork --project should not emit commands for current cwd:\n%s", stdout)
	}
}

func TestRunForkRefusesExistingTargetUnlessForced(t *testing.T) {
	dir := t.TempDir()
	base := setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)
	seedForkTeam(t, dir)
	writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", StartedAt: time.Now(),
	})
	// Pre-create the TARGET workstream root so the existing-workstream guard
	// trips when --force-duplicate is absent.
	if err := os.MkdirAll(filepath.Join(base, "issue-97"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, _, err := captureOutput(t, func() error {
		return runFork([]string{"--from", "issue-96", "--as", "issue-97"})
	})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("want existing-target refusal, got %v", err)
	}
	_, _, err = captureOutput(t, func() error {
		return runFork([]string{"--from", "issue-96", "--as", "issue-97", "--force-duplicate"})
	})
	if err != nil {
		t.Fatalf("--force-duplicate should let fork proceed, got %v", err)
	}
}

func TestRunForkPropagatesFreshLaunchFlags(t *testing.T) {
	dir := t.TempDir()
	base := setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)
	seedForkTeam(t, dir)
	writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", StartedAt: time.Now(),
	})

	stdout, _, err := captureOutput(t, func() error {
		return runFork([]string{
			"--from", "issue-96", "--as", "issue-97",
			"--no-bootstrap",
			"--trust", "trusted",
			"--model", "cto=gpt-5",
			"--codex-args=--profile fast",
			"--claude-args=--chrome",
		})
	})
	if err != nil {
		t.Fatalf("fork: %v\noutput:\n%s", err, stdout)
	}
	for _, want := range []string{
		"--trust trusted",
		"--model gpt-5",
		"--codex-args='--profile fast'",
		"--claude-args=--chrome",
		"--no-bootstrap",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("fork did not propagate %q:\n%s", want, stdout)
		}
	}
}

// TestRunForkAcceptsSourceWithLocalRoot covers the second leg of source-state
// detection: the SOURCE workstream root exists on disk even without
// restorable records matching the configured team.
func TestRunForkAcceptsSourceWithLocalRoot(t *testing.T) {
	dir := t.TempDir()
	base := setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)
	seedForkTeam(t, dir)
	// SOURCE workstream root exists (e.g. an agent ran there with a different
	// handle) but no records match the current team. Fork should still
	// accept SOURCE.
	if err := os.MkdirAll(filepath.Join(base, "issue-96", "agents", "stranger"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, _, err := captureOutput(t, func() error {
		return runFork([]string{"--from", "issue-96", "--as", "issue-97"})
	})
	if err != nil {
		t.Fatalf("fork with on-disk SOURCE root failed: %v", err)
	}
}
