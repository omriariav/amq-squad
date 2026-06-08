package cli

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/internal/team"
)

// withStubTmuxSessionBinary makes Validate believe (or disbelieve) the
// tmux-session wrapper is on PATH WITHOUT touching the real PATH or spawning
// anything. Critical for honesty: the LIVE path drives iTerm2 -CC, which CI
// cannot run, so every test here is emission/validation-only.
func withStubTmuxSessionBinary(t *testing.T, found bool) {
	t.Helper()
	prev := tmuxSessionLookPath
	t.Cleanup(func() { tmuxSessionLookPath = prev })
	if found {
		tmuxSessionLookPath = func(string) (string, error) { return "/stub/bin/tmux-session", nil }
		return
	}
	tmuxSessionLookPath = func(name string) (string, error) {
		return "", errors.New("exec: \"" + name + "\": not found")
	}
}

// A 3-agent plan under --terminal tmux-session must emit exactly one
// `tmux-session --session <ws> --create <role> <cwd>` per agent, with the right
// roles/cwds, the amq:<session>:<role> title token, and a final --resume focus
// line. This is the core contract for the window-per-agent backend.
func TestTmuxSessionDryRunLines_WindowPerAgent(t *testing.T) {
	plan := tmuxSessionLaunchPlan{
		Workstream: "issue-96",
		Panes: []teamLaunchPane{
			{Role: "cto", CWD: "/repo", Command: "cd /repo && amq-squad agent up codex"},
			{Role: "cpo", CWD: "/repo", Command: "cd /repo && amq-squad agent up codex"},
			{Role: "fullstack", CWD: "/sibling", Command: "cd /sibling && amq-squad agent up claude"},
		},
		StartDelay: 750 * time.Millisecond,
	}
	lines := tmuxSessionDryRunLines(plan)
	joined := strings.Join(lines, "\n")

	// Exactly one --create per agent.
	if got := strings.Count(joined, "tmux-session --session issue-96 --create"); got != 3 {
		t.Fatalf("expected 3 --create lines, got %d in:\n%s", got, joined)
	}
	// Per-agent create with the right role + cwd.
	for _, want := range []string{
		"tmux-session --session issue-96 --create cto /repo",
		"tmux-session --session issue-96 --create cpo /repo",
		"tmux-session --session issue-96 --create fullstack /sibling",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("dry-run missing create line %q in:\n%s", want, joined)
		}
	}
	// Agent command typed into each agent's OWN window (<session>:<role>). The
	// tmux target contains a ':' so shellQuote wraps it, matching the live argv.
	for _, want := range []string{
		"tmux send-keys -t 'issue-96:cto' 'cd /repo && amq-squad agent up codex' C-m",
		"tmux send-keys -t 'issue-96:cpo' 'cd /repo && amq-squad agent up codex' C-m",
		"tmux send-keys -t 'issue-96:fullstack' 'cd /sibling && amq-squad agent up claude' C-m",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("dry-run missing send-keys line %q in:\n%s", want, joined)
		}
	}
	// Name-first jump token stamped per agent via --rename. cpo and cto share
	// repo+engine yet still get distinct tokens (the disambiguation bug).
	for _, role := range []string{"cto", "cpo", "fullstack"} {
		// The amq:<session>:<role> title contains ':' so shellQuote wraps it.
		want := "tmux-session --session issue-96 --rename " + role + " 'amq:issue-96:" + role + "'"
		if !strings.Contains(joined, want) {
			t.Errorf("dry-run missing rename/title line %q in:\n%s", want, joined)
		}
	}
	// Exactly one final --resume focus line, and it is last.
	if got := strings.Count(joined, "tmux-session --session issue-96 --resume"); got != 1 {
		t.Fatalf("expected exactly 1 --resume line, got %d in:\n%s", got, joined)
	}
	if last := lines[len(lines)-1]; last != "tmux-session --session issue-96 --resume" {
		t.Fatalf("expected --resume as the last line, got %q", last)
	}
}

// The per-agent create argv is a pure, standalone function so emission is
// testable without exec. Assert its exact shape.
func TestTmuxSessionCreateArgv_Shape(t *testing.T) {
	got := tmuxSessionCreateArgv("issue-96", "cto", "/repo")
	want := []string{"--session", "issue-96", "--create", "cto", "/repo"}
	if len(got) != len(want) {
		t.Fatalf("argv = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("argv[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

// The rename argv stamps the deterministic name-first token.
func TestTmuxSessionRenameArgv_StampsTitleToken(t *testing.T) {
	got := tmuxSessionRenameArgv("issue-96", "cto")
	want := []string{"--session", "issue-96", "--rename", "cto", "amq:issue-96:cto"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rename argv[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

// buildPlan must carry the real team's ordered panes and shared workstream into
// the emitted window-per-agent plan (built off the same buildTeamLaunchPanes as
// the default backend, so command shape is identical between backends).
func TestTmuxSessionBuildPlanUsesSharedWorkstreamAndOrder(t *testing.T) {
	tm := team.Team{
		Project: "/repo",
		Members: []team.Member{
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "fullstack"},
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "cto"},
		},
	}
	plan := tmuxSessionTeamLaunchBackend{}.buildPlan(tm, teamLaunchOptions{
		SquadBin:   "/bin/amq-squad",
		Workstream: "issue-96",
		Trust:      trustModeTrusted,
		Stagger:    750 * time.Millisecond,
	})
	if plan.Workstream != "issue-96" {
		t.Fatalf("Workstream = %q, want issue-96", plan.Workstream)
	}
	if len(plan.Panes) != 2 {
		t.Fatalf("got %d panes, want 2", len(plan.Panes))
	}
	// Catalog order: cto before fullstack (matches the default tmux backend).
	if plan.Panes[0].Role != "cto" || plan.Panes[1].Role != "fullstack" {
		t.Fatalf("pane roles = %s, %s; want cto, fullstack", plan.Panes[0].Role, plan.Panes[1].Role)
	}
}

// Validate fails fast with an actionable message naming the recovery path when
// tmux-session is absent from PATH.
func TestTmuxSessionValidateMissingBinary(t *testing.T) {
	withStubTmuxSessionBinary(t, false)
	err := tmuxSessionTeamLaunchBackend{}.Validate(teamLaunchOptions{})
	if err == nil {
		t.Fatal("Validate succeeded, want missing-binary error")
	}
	for _, want := range []string{"tmux-session not found on PATH", "--terminal tmux"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err.Error(), want)
		}
	}
}

// Validate passes when the binary resolves and stagger is non-negative.
func TestTmuxSessionValidatePresentBinary(t *testing.T) {
	withStubTmuxSessionBinary(t, true)
	if err := (tmuxSessionTeamLaunchBackend{}).Validate(teamLaunchOptions{Stagger: time.Second}); err != nil {
		t.Fatalf("Validate = %v, want nil", err)
	}
}

func TestTmuxSessionValidateRejectsNegativeStagger(t *testing.T) {
	withStubTmuxSessionBinary(t, true)
	err := tmuxSessionTeamLaunchBackend{}.Validate(teamLaunchOptions{Stagger: -time.Second})
	if err == nil || !strings.Contains(err.Error(), "--stagger cannot be negative") {
		t.Fatalf("Validate error = %v, want negative-stagger rejection", err)
	}
}

// The opt-in backend registers under exactly "tmux-session" and is offered in
// the supported-terminals list alongside the default tmux backend.
func TestTmuxSessionBackendRegistered(t *testing.T) {
	if _, ok := teamLaunchBackends["tmux-session"]; !ok {
		t.Fatal("tmux-session backend not registered")
	}
	got := strings.Join(registeredTeamLaunchTerminals(), ",")
	if got != "tmux,tmux-session" {
		t.Fatalf("registeredTeamLaunchTerminals = %q, want tmux,tmux-session", got)
	}
}

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
