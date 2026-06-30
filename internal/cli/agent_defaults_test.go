package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestClaudeInScopePreauthAllowlistContents(t *testing.T) {
	allow := claudeInScopePreauthAllowlist("v2-14-0")
	// Narrowed slice (#296): PR creation ONLY. Feature-branch push is deliberately
	// not pre-authorized (no safe Claude pattern form), so the list is exactly one
	// PR-domain pattern and cannot — by construction — authorize push/tags/etc.
	if len(allow) != 1 || allow[0] != "Bash(gh pr create:*)" {
		t.Fatalf("allowlist = %v, want exactly [Bash(gh pr create:*)]", allow)
	}
	joined := strings.Join(allow, "\n")
	for _, forbidden := range []string{
		"git push", "origin main", "git tag", "gh release", "--tags", "--follow-tags",
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
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"},
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "s"},
			{Role: "senior-dev", Binary: "codex", Handle: "senior-dev", Session: "s"},
		},
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
		// Unknown/ad-hoc role not configured as a team member: rejected.
		{"unknown role rejected", "scratch", "claude", false},
		// Role configured for codex must not be pre-authorized when launched as claude.
		{"codex-configured role launched as claude", "senior-dev", "claude", false},
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
