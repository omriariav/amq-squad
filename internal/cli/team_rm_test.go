package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func teamRemoveFixture(t *testing.T, dir, profile string) string {
	t.Helper()
	cfg := team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-1"},
			{Role: "qa", Binary: "claude", Handle: "qa", Session: "issue-1"},
		},
	}
	return seedProfile(t, dir, profile, cfg)
}

func TestExecuteTeamRemoveDryRunKeepsProfile(t *testing.T) {
	dir := t.TempDir()
	path := teamRemoveFixture(t, dir, team.DefaultProfile)
	var out bytes.Buffer

	if err := executeTeamRemove(teamRemoveExecution{
		ProjectDir: dir,
		Profile:    team.DefaultProfile,
		DryRun:     true,
		Out:        &out,
	}); err != nil {
		t.Fatalf("executeTeamRemove dry-run: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("profile should remain after dry-run: %v", err)
	}
	for _, want := range []string{"Team profile removal preview", "profile:  default", "Dry run: no files removed."} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, out.String())
		}
	}
}

func TestExecuteTeamRemoveDeclinedKeepsProfile(t *testing.T) {
	dir := t.TempDir()
	path := teamRemoveFixture(t, dir, "review")
	var out bytes.Buffer

	if err := executeTeamRemove(teamRemoveExecution{
		ProjectDir: dir,
		Profile:    "review",
		Confirm:    strings.NewReader("n\n"),
		Out:        &out,
	}); err != nil {
		t.Fatalf("executeTeamRemove declined: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("profile should remain after decline: %v", err)
	}
	if !strings.Contains(out.String(), "Delete team profile review? [y/N]") ||
		!strings.Contains(out.String(), "aborted; no files removed") {
		t.Fatalf("decline output missing confirm/abort:\n%s", out.String())
	}
}

func TestExecuteTeamRemoveYesDeletesOnlySelectedProfile(t *testing.T) {
	dir := t.TempDir()
	defaultPath := teamRemoveFixture(t, dir, team.DefaultProfile)
	reviewPath := teamRemoveFixture(t, dir, "review")
	var out bytes.Buffer

	if err := executeTeamRemove(teamRemoveExecution{
		ProjectDir: dir,
		Profile:    "review",
		Yes:        true,
		Out:        &out,
	}); err != nil {
		t.Fatalf("executeTeamRemove --yes: %v", err)
	}
	if _, err := os.Stat(reviewPath); !os.IsNotExist(err) {
		t.Fatalf("review profile should be removed, stat err=%v", err)
	}
	if _, err := os.Stat(defaultPath); err != nil {
		t.Fatalf("default profile should remain: %v", err)
	}
	if !strings.Contains(out.String(), "Removed team profile review") {
		t.Fatalf("remove output missing success:\n%s", out.String())
	}
}

func TestRunTeamRemoveAcceptsPositionalProfile(t *testing.T) {
	dir := t.TempDir()
	path := teamRemoveFixture(t, dir, "review")

	if err := runTeamRemove([]string{"review", "--project", dir, "--yes"}); err != nil {
		t.Fatalf("runTeamRemove positional: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("review profile should be removed, stat err=%v", err)
	}
}
