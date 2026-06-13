package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

// TestRunUpLiveCreatesTeamHomeBriefOnce proves the live up path creates the
// team-home brief on first run and preserves an existing brief on rerun.
func TestRunUpLiveCreatesTeamHomeBriefOnce(t *testing.T) {
	useFakeBackend(t)
	setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "issue-96"},
		},
	})
	if _, _, err := captureOutput(t, func() error {
		return runUp([]string{"--terminal", "fake", "--session", "issue-96", "--no-bootstrap"})
	}); err != nil {
		t.Fatalf("up: %v", err)
	}
	brief := filepath.Join(dir, ".amq-squad", "briefs", "issue-96.md")
	first, err := os.ReadFile(brief)
	if err != nil {
		t.Fatalf("brief not created at %s: %v", brief, err)
	}
	if !strings.Contains(string(first), "# issue-96") {
		t.Errorf("brief stub missing session heading:\n%s", first)
	}

	// Hand-edit the brief, then rerun up. The body must survive.
	customized := "# Hand-edited brief\n\nKeep me.\n"
	if err := os.WriteFile(brief, []byte(customized), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runUp([]string{"--terminal", "fake", "--session", "issue-96", "--no-bootstrap"})
	}); err != nil {
		t.Fatalf("up rerun: %v", err)
	}
	got, err := os.ReadFile(brief)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != customized {
		t.Errorf("existing brief modified on rerun:\n%s", got)
	}
}

// TestUpDryRunDoesNotCreateBrief proves --dry-run is read-only: no brief
// is written to disk even though the dry-run output names the same session.
func TestUpDryRunDoesNotCreateBrief(t *testing.T) {
	useFakeBackend(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	})
	if _, _, err := captureOutput(t, func() error {
		return runUp([]string{"--dry-run", "--session", "issue-96", "--no-bootstrap"})
	}); err != nil {
		t.Fatalf("up --dry-run: %v", err)
	}
	briefDir := filepath.Join(dir, ".amq-squad", "briefs")
	if _, err := os.Stat(briefDir); err == nil {
		entries, _ := os.ReadDir(briefDir)
		t.Errorf("dry-run created briefs dir; entries: %v", entries)
	}
}

// TestTeamLaunchDryRunDoesNotCreateBrief covers the team-launch backend
// dry-run path (separate flag set with --dry-run meaning tmux-backend
// preview). It must also stay read-only.
func TestTeamLaunchDryRunDoesNotCreateBrief(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	})
	if _, _, err := captureOutput(t, func() error {
		return runTeamLaunch([]string{"--dry-run", "--session", "issue-96", "--no-bootstrap"})
	}); err != nil {
		t.Fatalf("team launch --dry-run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".amq-squad", "briefs")); err == nil {
		t.Error("team launch --dry-run created briefs dir")
	}
}

// TestRunUpLiveBriefIsTeamHomeOnly checks that multi-cwd members all point
// at one team-home brief and no per-member-cwd briefs are created.
func TestRunUpLiveBriefIsTeamHomeOnly(t *testing.T) {
	useFakeBackend(t)
	setupFakeAMQSessionRoots(t)
	teamDir := t.TempDir()
	memberDir := t.TempDir()
	chdir(t, teamDir)
	if err := team.Write(teamDir, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "issue-96", CWD: memberDir},
		},
	}); err != nil {
		t.Fatal(err)
	}

	if _, _, err := captureOutput(t, func() error {
		return runUp([]string{"--terminal", "fake", "--session", "issue-96", "--no-bootstrap"})
	}); err != nil {
		t.Fatalf("up: %v", err)
	}
	if _, err := os.Stat(filepath.Join(teamDir, ".amq-squad", "briefs", "issue-96.md")); err != nil {
		t.Fatalf("team-home brief missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(memberDir, ".amq-squad", "briefs")); err == nil {
		t.Errorf("per-member-cwd briefs dir created at %s; multi-cwd should fan in to team-home only", memberDir)
	}
}
