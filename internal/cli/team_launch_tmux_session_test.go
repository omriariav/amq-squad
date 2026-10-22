package cli

import (
	"strings"
	"testing"
	"time"
)

// Regression guard: the DEFAULT --terminal tmux still emits the OLD split-pane
// plan (split-window + select-layout + pane-id targeting), proving the new
// backend did not perturb the default path.
func TestDefaultTmuxBackendStillEmitsSplitPanePlan(t *testing.T) {
	plan := tmuxLaunchPlan{
		Session:    "amq-squad-repo",
		Workstream: "repo",
		Target:     "new-session",
		Layout:     "vertical",
		Panes: []teamLaunchPane{
			{Role: "cto", CWD: "/repo", Command: "cd /repo && amq-squad agent up codex"},
			{Role: "qa", CWD: "/repo", Command: "cd /repo && amq-squad agent up claude"},
		},
		StartDelay: 750 * time.Millisecond,
	}
	got := strings.Join(tmuxDryRunLines(plan), "\n")
	for _, want := range []string{
		"tmux new-session -d -s amq-squad-repo -n squad -c /repo",
		"pane_1=$(tmux split-window -P -F '#{pane_id}' -t 'amq-squad-repo:0' -h -c /repo)",
		"tmux select-layout -t 'amq-squad-repo:0' even-horizontal",
		"tmux select-pane -t 'amq-squad-repo:0.0' -T 'amq:repo:cto'",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("default tmux plan regressed, missing %q in:\n%s", want, got)
		}
	}
	// The default backend must NOT emit the window-per-agent wrapper calls.
	if strings.Contains(got, "tmux-session --session") {
		t.Errorf("default tmux plan leaked tmux-session wrapper calls:\n%s", got)
	}
}
