package cli

import (
	"bytes"
	"os"
	"path/filepath"
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

func TestExecuteTeamRemoveAllProfilesRequiresProjectNameAndKeepsNonProfileFiles(t *testing.T) {
	dir := t.TempDir()
	defaultPath := teamRemoveFixture(t, dir, team.DefaultProfile)
	reviewPath := teamRemoveFixture(t, dir, "review")
	rulesPath := filepath.Join(dir, team.DirName, "team-rules.md")
	if err := os.WriteFile(rulesPath, []byte("# Team Rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer

	if err := executeTeamRemove(teamRemoveExecution{
		ProjectDir:  dir,
		AllProfiles: true,
		Confirm:     strings.NewReader("wrong\n"),
		Out:         &out,
	}); err != nil {
		t.Fatalf("executeTeamRemove all declined: %v", err)
	}
	if _, err := os.Stat(defaultPath); err != nil {
		t.Fatalf("default profile should remain after wrong project name: %v", err)
	}
	if _, err := os.Stat(reviewPath); err != nil {
		t.Fatalf("review profile should remain after wrong project name: %v", err)
	}
	if !strings.Contains(out.String(), "mode:     all team config") ||
		!strings.Contains(out.String(), "aborted; no files removed") {
		t.Fatalf("all-profile decline output missing preview/abort:\n%s", out.String())
	}

	out.Reset()
	if err := executeTeamRemove(teamRemoveExecution{
		ProjectDir:  dir,
		AllProfiles: true,
		Confirm:     strings.NewReader(filepath.Base(dir) + "\n"),
		Out:         &out,
	}); err != nil {
		t.Fatalf("executeTeamRemove all confirmed: %v", err)
	}
	if _, err := os.Stat(defaultPath); !os.IsNotExist(err) {
		t.Fatalf("default profile should be removed, stat err=%v", err)
	}
	if _, err := os.Stat(reviewPath); !os.IsNotExist(err) {
		t.Fatalf("review profile should be removed, stat err=%v", err)
	}
	if _, err := os.Stat(rulesPath); err != nil {
		t.Fatalf("team-rules should remain: %v", err)
	}
	if !strings.Contains(out.String(), "Removed 2 team profile files") {
		t.Fatalf("all-profile remove output missing success:\n%s", out.String())
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
