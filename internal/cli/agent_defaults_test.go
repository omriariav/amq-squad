package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestClaudeInScopePreauthAllowlistContents(t *testing.T) {
	allow := claudeInScopePreauthAllowlist("v2-14-0")
	joined := strings.Join(allow, "\n")
	for _, want := range []string{
		"Bash(gh pr create:*)",
		"Bash(git push origin codex/v2-14-0-:*)",
		"Bash(git push -u origin codex/v2-14-0-:*)",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("allowlist missing %q:\n%s", want, joined)
		}
	}
	// Must NOT pre-authorize main push, tags, releases, or broad git/gh/Bash.
	for _, forbidden := range []string{
		"origin main", "git tag", "gh release", "--tags", "--follow-tags",
		"Bash(git push:*)", "Bash(git:*)", "Bash(gh:*)", "Bash(:*)", "Bash(*)",
	} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("allowlist must never include %q:\n%s", forbidden, joined)
		}
	}
	if claudeInScopePreauthAllowlist("") != nil {
		t.Fatal("empty session must yield no allowlist")
	}
}

func TestClaudePreauthChildArgs(t *testing.T) {
	if claudePreauthChildArgs(nil) != nil {
		t.Fatal("empty allowlist must yield no child args (back-compat)")
	}
	args := claudePreauthChildArgs([]string{"Bash(gh pr create:*)", "Bash(git push origin codex/s-:*)"})
	if len(args) != 2 || args[0] != "--allowedTools" {
		t.Fatalf("child args = %v, want --allowedTools + comma-joined value", args)
	}
	if !strings.Contains(args[1], "gh pr create") || !strings.Contains(args[1], "git push origin codex/s-") {
		t.Fatalf("allowedTools value missing patterns: %q", args[1])
	}
}

func TestClaudeWorkerPreauthEligible(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Members:      []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"}, {Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "s"}},
		Orchestrated: true,
		Lead:         "cto",
	})
	cases := []struct {
		name   string
		role   string
		binary string
		want   bool
	}{
		{"claude non-lead worker", "fullstack", "claude", true},
		{"claude lead excluded", "cto", "claude", false},
		{"codex worker out of scope", "fullstack", "codex", false},
		{"empty role", "", "claude", false},
	}
	for _, tc := range cases {
		if got := claudeWorkerPreauthEligible(dir, "", tc.role, tc.binary); got != tc.want {
			t.Errorf("%s: eligible=%v, want %v", tc.name, got, tc.want)
		}
	}

	// Non-orchestrated team: never eligible (conservative default).
	flat := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "s"}},
	})
	if claudeWorkerPreauthEligible(flat, "", "fullstack", "claude") {
		t.Fatal("non-orchestrated team must not pre-authorize")
	}
}
