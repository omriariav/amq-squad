package cli

import (
	"strings"
	"testing"
)

// The emitted (dry-run) tmux launch plan must stamp a deterministic, parseable
// pane title token "amq:<session>:<role>" for EACH agent pane via select-pane -T.
// This is the launch half of the name-first jump fix: the resolver in
// internal/tmuxpane/tmux.go matches panes by exactly this token, so cpo and cto in the
// same repo+engine still resolve to distinct panes.
func TestTmuxDryRunLines_StampsDeterministicPaneTitles(t *testing.T) {
	plan := tmuxLaunchPlan{
		Session:    "beta-tmux",
		Workstream: "beta",
		Target:     "new-session",
		Layout:     "tiled",
		Panes: []teamLaunchPane{
			{Role: "cpo", CWD: "/repo", Command: "codex"},
			{Role: "cto", CWD: "/repo", Command: "codex"},
		},
	}
	joined := strings.Join(tmuxDryRunLines(plan), "\n")

	if !strings.Contains(joined, "select-pane") {
		t.Fatalf("expected select-pane lines in emitted plan, got:\n%s", joined)
	}
	for _, role := range []string{"cpo", "cto"} {
		token := "amq:beta:" + role
		// The token is shell-quoted as the -T argument; assert the token appears
		// after a -T so we are matching the title, not an incidental substring.
		if !strings.Contains(joined, "-T "+token) && !strings.Contains(joined, "-T '"+token+"'") {
			t.Fatalf("expected pane title token %q after -T for role %q, got:\n%s", token, role, joined)
		}
	}
}

// paneTitleToken is the single source of truth for the stamped title and MUST
// mirror the resolver's expectedPaneToken. Format: amq:<session>:<role>.
func TestPaneTitleToken_Format(t *testing.T) {
	if got := paneTitleToken("beta", "cpo"); got != "amq:beta:cpo" {
		t.Fatalf("paneTitleToken = %q, want amq:beta:cpo", got)
	}
	if got := paneTitleToken("issue-96", "cto"); got != "amq:issue-96:cto" {
		t.Fatalf("paneTitleToken = %q, want amq:issue-96:cto", got)
	}
}
