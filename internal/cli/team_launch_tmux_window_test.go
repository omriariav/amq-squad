package cli

import (
	"strings"
	"testing"
)

func TestTmuxBackendAcceptsNewWindowTarget(t *testing.T) {
	b := tmuxTeamLaunchBackend{}
	for _, tgt := range []string{"current-window", "new-window", "new-session"} {
		if err := b.Validate(teamLaunchOptions{Target: tgt, Layout: "vertical"}); err != nil {
			t.Errorf("target %q should be valid: %v", tgt, err)
		}
	}
	if err := b.Validate(teamLaunchOptions{Target: "bogus", Layout: "vertical"}); err == nil {
		t.Error("an unknown target must be rejected")
	}
}

func TestTmuxDryRunNewWindowOneWindowPerAgent(t *testing.T) {
	plan := tmuxLaunchPlan{
		Session:    "amq-squad-proj",
		Workstream: "issue-96",
		Target:     "new-window",
		Layout:     "vertical",
		Panes: []teamLaunchPane{
			{Role: "cto", CWD: "/repo", Command: "cd /repo && amq-squad agent up codex --role cto"},
			{Role: "qa", CWD: "/repo", Command: "cd /repo && amq-squad agent up codex --role qa"},
		},
	}
	joined := strings.Join(tmuxDryRunLines(plan), "\n")

	// Window-per-agent: a new-window per role, and NO pane splitting/layout.
	if strings.Contains(joined, "split-window") {
		t.Errorf("new-window must not split panes:\n%s", joined)
	}
	if strings.Contains(joined, "select-layout") {
		t.Errorf("new-window has no pane-layout step:\n%s", joined)
	}
	if c := strings.Count(joined, "tmux new-window"); c != 2 {
		t.Errorf("expected 2 tmux new-window invocations (one per agent), got %d:\n%s", c, joined)
	}
	if c := strings.Count(joined, "tmux send-keys"); c != 2 {
		t.Errorf("expected one send-keys per agent, got %d:\n%s", c, joined)
	}
	// Each agent still gets its deterministic pane-title token (so focus/send
	// resolve identically to the pane backends) and a human window name.
	for _, role := range []string{"cto", "qa"} {
		// Title uses the WORKSTREAM (issue-96), not the terminal session name
		// (amq-squad-proj) — so it matches what the resolver expects.
		token := "amq:issue-96:" + role
		if !strings.Contains(joined, "-T '"+token+"'") && !strings.Contains(joined, "-T "+token) {
			t.Errorf("missing pane title token for %q:\n%s", role, joined)
		}
		if !strings.Contains(joined, "-n '"+role+"'") && !strings.Contains(joined, "-n "+role) {
			t.Errorf("missing window name for %q:\n%s", role, joined)
		}
	}
}

func TestTmuxWindowName(t *testing.T) {
	if got := tmuxWindowName("cto"); got != "cto" {
		t.Errorf("tmuxWindowName(cto) = %q, want cto", got)
	}
	if got := tmuxWindowName(""); got != "agent" {
		t.Errorf("empty role should fall back to %q, got %q", "agent", got)
	}
}
