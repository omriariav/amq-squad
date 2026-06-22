package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func teamMembers(t *testing.T, dir string) []team.Member {
	t.Helper()
	cfg, err := team.Read(dir)
	if err != nil {
		t.Fatalf("read team: %v", err)
	}
	return cfg.Members
}

func TestTeamMemberAddAppendsAndPersists(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "claude", Handle: "cto", Session: "issue-96"}},
	})
	out, _, err := captureOutput(t, func() error {
		return runTeamMember([]string{"add", "researcher", "--binary", "codex"})
	})
	if err != nil {
		t.Fatalf("member add: %v", err)
	}
	ms := teamMembers(t, dir)
	if len(ms) != 2 {
		t.Fatalf("want 2 members, got %d: %+v", len(ms), ms)
	}
	got := ms[1]
	if got.Role != "researcher" || got.Binary != "codex" || got.Handle != "researcher" {
		t.Fatalf("appended member = %+v, want researcher/codex/researcher", got)
	}
	// Inherits the existing members' shared session so it joins one workstream.
	if got.Session != "issue-96" {
		t.Errorf("session = %q, want inherited issue-96", got.Session)
	}
	if !strings.Contains(out, "agent up codex --role researcher") {
		t.Errorf("add should print the agent up hint, got:\n%s", out)
	}
}

func TestTeamMemberAddRecordsSpawnDepthAndRejectsChildSpawn(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	})
	t.Setenv("AM_ME", "cto")
	if _, _, err := captureOutput(t, func() error {
		return runTeamMember([]string{"add", "qa", "--binary", "codex"})
	}); err != nil {
		t.Fatalf("lead member add: %v", err)
	}
	got := teamMembers(t, dir)[1]
	if got.SpawnOrigin != "cto" || got.SpawnDepth != 1 {
		t.Fatalf("spawn metadata = origin %q depth %d, want cto/1", got.SpawnOrigin, got.SpawnDepth)
	}

	t.Setenv("AM_ME", "qa")
	if _, _, err := captureOutput(t, func() error {
		return runTeamMember([]string{"add", "reviewer", "--binary", "codex"})
	}); err == nil || !strings.Contains(err.Error(), "child-spawns-child") {
		t.Fatalf("child add error = %v, want child-spawns-child guard", err)
	}
}

func TestTeamMemberAddNormalizesCaseAndPrintsFaithfulHint(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "claude", Handle: "cto", Session: "issue-96"}},
	})
	// Mixed-case role + handle are normalized to lowercase slugs (not rejected
	// by the slug validator), and the printed hint carries --model so it is
	// copy-paste faithful.
	out, _, err := captureOutput(t, func() error {
		return runTeamMember([]string{"add", "Researcher", "--binary", "claude", "--handle", "Researcher", "--model", "sonnet"})
	})
	if err != nil {
		t.Fatalf("member add (mixed case): %v", err)
	}
	got := teamMembers(t, dir)[1]
	if got.Role != "researcher" || got.Handle != "researcher" {
		t.Fatalf("role/handle not normalized: %+v", got)
	}
	if !strings.Contains(out, "agent up claude --role researcher --session issue-96 --model sonnet --me researcher") {
		t.Errorf("hint should be a faithful copy-paste command with --model, got:\n%s", out)
	}
}

func TestTeamMemberAddRequiresValidBinary(t *testing.T) {
	seedTeam(t, team.Team{Members: []team.Member{{Role: "cto", Binary: "claude", Handle: "cto", Session: "s"}}})
	for _, args := range [][]string{
		{"add", "qa"},                    // no --binary
		{"add", "qa", "--binary", "gpt"}, // invalid binary
	} {
		if _, _, err := captureOutput(t, func() error { return runTeamMember(args) }); err == nil ||
			!strings.Contains(err.Error(), "binary") {
			t.Errorf("runTeamMember(%v) = %v, want a --binary usage error", args, err)
		}
	}
}

func TestTeamMemberAddRejectsDuplicateRoleAndHandle(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "claude", Handle: "cto", Session: "s"}},
	})
	if _, _, err := captureOutput(t, func() error {
		return runTeamMember([]string{"add", "cto", "--binary", "codex"})
	}); err == nil || !strings.Contains(err.Error(), "already on the team") {
		t.Fatalf("duplicate role: want 'already on the team', got %v", err)
	}
	// Distinct role, but colliding handle.
	if _, _, err := captureOutput(t, func() error {
		return runTeamMember([]string{"add", "qa", "--binary", "codex", "--handle", "cto"})
	}); err == nil || !strings.Contains(err.Error(), "handle") {
		t.Fatalf("duplicate handle: want a handle error, got %v", err)
	}
	if n := len(teamMembers(t, dir)); n != 1 {
		t.Fatalf("rejected adds must not persist; got %d members", n)
	}
}

func TestTeamMemberAddBinaryMismatchedArgsRejected(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "claude", Handle: "cto", Session: "s"}},
	})
	// codex member carrying claude_args must be rejected by team validation
	// (WriteProfile re-validates), and must not persist.
	if _, _, err := captureOutput(t, func() error {
		return runTeamMember([]string{"add", "worker", "--binary", "codex", "--claude-args", "--settings x.json"})
	}); err == nil {
		t.Fatal("codex member with claude_args should be rejected by validation")
	}
	if n := len(teamMembers(t, dir)); n != 1 {
		t.Fatalf("invalid add must not persist; got %d members", n)
	}
}

func TestTeamMemberRmRemovesAndPersists(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "claude", Handle: "cto", Session: "s"},
			{Role: "qa", Binary: "claude", Handle: "qa", Session: "s"},
		},
	})
	out, _, err := captureOutput(t, func() error {
		return runTeamMember([]string{"rm", "qa"})
	})
	if err != nil {
		t.Fatalf("member rm: %v", err)
	}
	ms := teamMembers(t, dir)
	if len(ms) != 1 || ms[0].Role != "cto" {
		t.Fatalf("after rm want [cto], got %+v", ms)
	}
	if !strings.Contains(out, "stop --role qa") {
		t.Errorf("rm should print the stop hint, got:\n%s", out)
	}
}

func TestTeamMemberRmUnknownRoleErrors(t *testing.T) {
	seedTeam(t, team.Team{Members: []team.Member{{Role: "cto", Binary: "claude", Handle: "cto", Session: "s"}}})
	if _, _, err := captureOutput(t, func() error {
		return runTeamMember([]string{"rm", "ghost"})
	}); err == nil || !strings.Contains(err.Error(), "not a team member") {
		t.Fatalf("want 'not a team member', got %v", err)
	}
}

func TestTeamMemberRmRefusesOrchestrationLead(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "claude", Handle: "cto", Session: "s"},
			{Role: "qa", Binary: "claude", Handle: "qa", Session: "s"},
		},
	})
	if _, _, err := captureOutput(t, func() error {
		return runTeamMember([]string{"rm", "cto"})
	}); err == nil || !strings.Contains(err.Error(), "lead") {
		t.Fatalf("removing the lead should be refused, got %v", err)
	}
	if n := len(teamMembers(t, dir)); n != 2 {
		t.Fatalf("refused rm must not persist; got %d members", n)
	}
}

func TestTeamMemberListShowsRoster(t *testing.T) {
	seedTeam(t, team.Team{
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"},
			{Role: "qa", Binary: "claude", Handle: "qa", Session: "s", Model: "sonnet"},
		},
	})
	out, _, err := captureOutput(t, func() error {
		return runTeamMember([]string{"list"})
	})
	if err != nil {
		t.Fatalf("member list: %v", err)
	}
	for _, want := range []string{"cto", "qa", "sonnet", "lead"} {
		if !strings.Contains(out, want) {
			t.Errorf("roster table missing %q in:\n%s", want, out)
		}
	}
	// JSON envelope.
	stdout, _, err := captureOutput(t, func() error {
		return runTeamMember([]string{"list", "--json"})
	})
	if err != nil {
		t.Fatalf("member list --json: %v", err)
	}
	if !strings.Contains(stdout, "team_roster") || !strings.Contains(stdout, "\"lead\": \"cto\"") {
		t.Errorf("roster json unexpected:\n%s", stdout)
	}
}

func TestTeamMemberListFlatTeamAndEmptyAndAlias(t *testing.T) {
	// Flat (non-orchestrated) team: no LEAD column, and --json omits "lead".
	seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "claude", Handle: "cto", Session: "s"}},
	})
	out, _, err := captureOutput(t, func() error { return runTeamMember([]string{"ls"}) }) // ls alias
	if err != nil {
		t.Fatalf("member ls: %v", err)
	}
	if strings.Contains(out, "LEAD") {
		t.Errorf("flat team should not show a LEAD column:\n%s", out)
	}
	stdout, _, err := captureOutput(t, func() error { return runTeamMember([]string{"list", "--json"}) })
	if err != nil {
		t.Fatalf("member list --json: %v", err)
	}
	if strings.Contains(stdout, "\"lead\"") {
		t.Errorf("flat team json should omit the lead key:\n%s", stdout)
	}
}

func TestTeamMemberListEmptyRoster(t *testing.T) {
	seedTeam(t, team.Team{Members: nil})
	out, _, err := captureOutput(t, func() error { return runTeamMember([]string{"list"}) })
	if err != nil || !strings.Contains(out, "(no members)") {
		t.Fatalf("empty roster: want '(no members)', got %q / %v", out, err)
	}
}

func TestTeamMemberRequiresExistingTeam(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	if _, _, err := captureOutput(t, func() error {
		return runTeamMember([]string{"add", "qa", "--binary", "claude"})
	}); err == nil || !strings.Contains(err.Error(), "no team configured") {
		t.Fatalf("want 'no team configured', got %v", err)
	}
}

func TestTeamMemberRequiresSubcommandAndRole(t *testing.T) {
	seedTeam(t, team.Team{Members: []team.Member{{Role: "cto", Binary: "claude", Handle: "cto", Session: "s"}}})
	if _, _, err := captureOutput(t, func() error { return runTeamMember([]string{"add", "--binary", "codex"}) }); err == nil ||
		!strings.Contains(err.Error(), "role is required") {
		t.Errorf("add with no role: want 'role is required', got %v", err)
	}
	if _, _, err := captureOutput(t, func() error { return runTeamMember([]string{"bogus"}) }); err == nil ||
		!strings.Contains(err.Error(), "unknown 'team member' subcommand") {
		t.Errorf("bad subcommand: want unknown-subcommand error, got %v", err)
	}
}
