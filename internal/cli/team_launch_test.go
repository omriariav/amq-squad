package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestBuildTmuxLaunchPlanUsesCatalogOrderAndLaunchCommands(t *testing.T) {
	tm := team.Team{
		Project: "/repo",
		Members: []team.Member{
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: ""},
			{Role: "cto", Binary: "codex", Handle: "cto", Session: ""},
		},
	}
	plan := buildTmuxLaunchPlan(tm, teamLaunchOptions{
		SquadBin:        "/bin/amq-squad",
		TerminalSession: "amq-squad-repo",
		Target:          "new-session",
		Layout:          "vertical",
		Stagger:         750 * time.Millisecond,
		Workstream:      "repo",
		Trust:           trustModeTrusted,
	})
	if plan.Session != "amq-squad-repo" {
		t.Fatalf("Session = %q", plan.Session)
	}
	if len(plan.Panes) != 2 {
		t.Fatalf("got %d panes, want 2", len(plan.Panes))
	}
	if plan.Panes[0].Role != "cto" || plan.Panes[1].Role != "fullstack" {
		t.Fatalf("pane roles = %s, %s; want cto, fullstack", plan.Panes[0].Role, plan.Panes[1].Role)
	}
	for _, want := range []string{
		"cd /repo",
		"/bin/amq-squad agent up codex",
		"--role cto",
		"--session repo",
		"-- --dangerously-bypass-approvals-and-sandbox",
	} {
		if !strings.Contains(plan.Panes[0].Command, want) {
			t.Errorf("cto command missing %q in %s", want, plan.Panes[0].Command)
		}
	}
}

func TestRunTeamLaunchHelpListsNewDXFlags(t *testing.T) {
	_, stderr, err := captureOutput(t, func() error {
		return runTeamLaunch([]string{"--help"})
	})
	if err != nil {
		t.Fatalf("runTeamLaunch --help: %v", err)
	}
	for _, want := range []string{"--trust", "--model", "--force-duplicate", "--no-gitignore"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("team launch --help missing %q in:\n%s", want, stderr)
		}
	}
}

func TestRegisteredTeamLaunchTerminalsIncludesTmux(t *testing.T) {
	// tmux is the baseline/default backend and must always be present. Opt-in
	// backends (e.g. tmux-session) may also be registered, so assert membership
	// rather than exact equality.
	found := false
	for _, name := range registeredTeamLaunchTerminals() {
		if name == "tmux" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("registeredTeamLaunchTerminals = %v, want it to include tmux", registeredTeamLaunchTerminals())
	}
}

func TestCommandProfileArgOmitsDefaultAndQuotesNamedProfile(t *testing.T) {
	if got := commandProfileArg(team.DefaultProfile); got != "" {
		t.Fatalf("default profile arg = %q, want empty", got)
	}
	if got := commandProfileArg("release review"); got != " --profile 'release review'" {
		t.Fatalf("named profile arg = %q", got)
	}
}

func TestRunTeamLaunchRejectsUnsupportedTerminalWithRegistry(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return runTeamLaunch([]string{"--terminal", "bogus", "--dry-run"})
	})
	if err == nil {
		t.Fatal("runTeamLaunch succeeded, want unsupported terminal error")
	}
	for _, want := range []string{`unsupported terminal "bogus"`, "supported terminals: "} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestTmuxDryRunLinesShowPaneFlow(t *testing.T) {
	plan := tmuxLaunchPlan{
		Session:    "amq-squad-repo", // tmux session name (targets)
		Workstream: "repo",           // AMQ workstream (pane-title identity)
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
		// Pane titles carry the name-first jump token amq:<workstream>:<role> —
		// the WORKSTREAM, not the tmux session name, so it matches the resolver.
		"tmux select-pane -t 'amq-squad-repo:0.0' -T 'amq:repo:cto'",
		"tmux send-keys -t 'amq-squad-repo:0.0'",
		"sleep 0.75",
		"pane_1=$(tmux split-window -P -F '#{pane_id}' -t 'amq-squad-repo:0' -h -c /repo)",
		"tmux select-layout -t 'amq-squad-repo:0' even-horizontal",
		`tmux select-pane -t "$pane_1" -T 'amq:repo:qa'`,
		`tmux send-keys -t "$pane_1"`,
		"# attach later with: tmux attach-session -t amq-squad-repo",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("dry-run output missing %q in:\n%s", want, got)
		}
	}
}

func TestTmuxDryRunLinesCanTargetCurrentWindow(t *testing.T) {
	plan := tmuxLaunchPlan{
		Session:    "ignored", // current-window ignores the session name
		Workstream: "repo",    // but the pane-title identity is the workstream
		Target:     "current-window",
		Layout:     "vertical",
		Panes: []teamLaunchPane{
			{Role: "cto", CWD: "/repo", Command: "cd /repo && amq-squad agent up codex"},
			{Role: "qa", CWD: "/repo", Command: "cd /repo && amq-squad agent up claude"},
		},
		StartDelay: 750 * time.Millisecond,
	}
	got := strings.Join(tmuxDryRunLines(plan), "\n")
	for _, want := range []string{
		// Anchored on the launching shell's own pane ($TMUX_PANE), not the
		// focused window, so panes never hijack an unrelated tab (#40).
		`window=$(tmux display-message -p -t "${TMUX_PANE:?run amq-squad up from inside a tmux pane}" '#{session_name}:#{window_index}')`,
		// Pane titles carry the name-first jump token amq:<workstream>:<role>.
		`pane_0=$(tmux split-window -P -F '#{pane_id}' -t "$window" -h -c /repo)`,
		`tmux select-pane -t "$pane_0" -T 'amq:repo:cto'`,
		`pane_1=$(tmux split-window -P -F '#{pane_id}' -t "$window" -h -c /repo)`,
		`tmux send-keys -t "$pane_0"`,
		`tmux send-keys -t "$pane_1"`,
		"# using current tmux window; no attach needed",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("dry-run output missing %q in:\n%s", want, got)
		}
	}
}

func TestRunTeamLaunchDryRunDefaultsToCurrentWindow(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})
	if err := team.Write(dir, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: ""},
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: ""},
		},
	}); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := captureOutput(t, func() error {
		return runTeamLaunch([]string{"--dry-run", "--no-bootstrap"})
	})
	if err != nil {
		t.Fatalf("runTeamLaunch: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		"# amq-squad team launch - tmux",
		"# target:  current-window",
		"# layout:  vertical",
		"# workstream: ",
		"tmux split-window",
		" -h -c ",
		"tmux select-layout -t \"$window\" even-horizontal",
		"agent up codex",
		"agent up claude",
		"--no-bootstrap --me cto",
		"--no-bootstrap --me fullstack",
		"--trust approve-for-me",
		"--sandbox workspace-write",
		"--ask-for-approval on-request",
		"approvals_reviewer=\"auto_review\"",
		"-- --permission-mode auto",
		"--session ",
		"# using current tmux window; no attach needed",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("dry-run output missing %q in:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "POC") {
		t.Errorf("dry-run output should not mention POC:\n%s", stdout)
	}
}

func TestRunTeamLaunchDryRunUsesExplicitSharedWorkstream(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})
	if err := team.Write(dir, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: ""},
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: ""},
		},
	}); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := captureOutput(t, func() error {
		return runTeamLaunch([]string{"--session", "issue-96", "--dry-run", "--no-bootstrap"})
	})
	if err != nil {
		t.Fatalf("runTeamLaunch: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		"# workstream: issue-96",
		"--session issue-96 --team-workstream",
		"agent up codex",
		"agent up claude",
		"--no-bootstrap --me cto",
		"--no-bootstrap --me fullstack",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("dry-run output missing %q in:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "--session cto") || strings.Contains(stdout, "--session fullstack") {
		t.Fatalf("dry-run used role-per-session routing instead of shared workstream:\n%s", stdout)
	}
}

func TestRunTeamLaunchDryRunUsesBinaryArgs(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})
	if err := team.Write(dir, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: ""},
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: ""},
		},
	}); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := captureOutput(t, func() error {
		return runTeamLaunch([]string{"--dry-run", "--no-bootstrap", "--trust", "trusted", "--codex-args=--enable goals", "--claude-args=--chrome"})
	})
	if err != nil {
		t.Fatalf("runTeamLaunch: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		"agent up codex",
		"--codex-args=",
		"-- --dangerously-bypass-approvals-and-sandbox --enable goals",
		"agent up claude",
		"--claude-args=--chrome",
		"-- --permission-mode auto --chrome",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("dry-run output missing %q in:\n%s", want, stdout)
		}
	}
}

func TestRunTeamLaunchFreshRejectsExistingWorkstream(t *testing.T) {
	dir := t.TempDir()
	base := setupFakeAMQSessionRoots(t)
	if err := os.MkdirAll(filepath.Join(base, "issue-96"), 0o755); err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})
	if err := team.Write(dir, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: ""},
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: ""},
		},
	}); err != nil {
		t.Fatal(err)
	}

	_, _, err = captureOutput(t, func() error {
		return runTeamLaunch([]string{"--session", "issue-96", "--fresh", "--dry-run", "--no-bootstrap"})
	})
	if err == nil || !strings.Contains(err.Error(), `workstream session "issue-96" already exists`) {
		t.Fatalf("runTeamLaunch error = %v, want existing workstream rejection", err)
	}
}

func TestRunTeamLaunchDryRunUsesSharedWorkstreamAcrossMemberCWDs(t *testing.T) {
	dir := t.TempDir()
	sibling := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})
	if err := team.Write(dir, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: ""},
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "", CWD: sibling},
		},
	}); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := captureOutput(t, func() error {
		return runTeamLaunch([]string{"--session", "issue-96", "--dry-run", "--no-bootstrap"})
	})
	if err != nil {
		t.Fatalf("runTeamLaunch: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		" -c " + sibling,
		"--role cto --session issue-96 --team-workstream",
		"--role fullstack --session issue-96 --team-workstream",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("dry-run output missing %q in:\n%s", want, stdout)
		}
	}
}

func TestRunTeamLaunchDryRunNewSessionDoesNotAutoAttach(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})
	if err := team.Write(dir, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: ""},
		},
	}); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := captureOutput(t, func() error {
		return runTeamLaunch([]string{"--target", "new-session", "--dry-run", "--no-bootstrap"})
	})
	if err != nil {
		t.Fatalf("runTeamLaunch: %v\nstderr:\n%s", err, stderr)
	}
	if strings.Contains(stdout, "\ntmux attach-session") {
		t.Fatalf("new-session should not auto-attach:\n%s", stdout)
	}
	if !strings.Contains(stdout, "# attach later with: tmux attach-session") {
		t.Fatalf("new-session should print manual attach hint:\n%s", stdout)
	}
}

func TestRunTmuxLaunchPlanCurrentWindowRequiresTmux(t *testing.T) {
	t.Setenv("TMUX", "")
	err := runTmuxLaunchPlan(tmuxLaunchPlan{
		Target: "current-window",
		Layout: "vertical",
		Panes: []teamLaunchPane{
			{Role: "cto", CWD: "/repo", Command: "true"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "requires running inside tmux") {
		t.Fatalf("runTmuxLaunchPlan error = %v, want current-window tmux requirement", err)
	}
}

func TestTmuxLayoutMapping(t *testing.T) {
	cases := []struct {
		layout       string
		split        string
		selectLayout string
	}{
		{layout: "vertical", split: "-h", selectLayout: "even-horizontal"},
		{layout: "horizontal", split: "-v", selectLayout: "even-vertical"},
		{layout: "tiled", split: "-h", selectLayout: "tiled"},
	}
	for _, tc := range cases {
		if got := tmuxSplitDirection(tc.layout); got != tc.split {
			t.Errorf("tmuxSplitDirection(%q) = %q, want %q", tc.layout, got, tc.split)
		}
		if got := tmuxSelectLayout(tc.layout); got != tc.selectLayout {
			t.Errorf("tmuxSelectLayout(%q) = %q, want %q", tc.layout, got, tc.selectLayout)
		}
	}
}

func TestParseTmuxClientsReturnsControlModeClients(t *testing.T) {
	got := parseTmuxClients("/dev/ttys001\t1\tattached,control-mode,pause-after=120\n/dev/ttys002\t0\tattached\n")
	if len(got) != 1 {
		t.Fatalf("got %d clients, want 1", len(got))
	}
	if got[0].TTY != "/dev/ttys001" || !strings.Contains(got[0].Flags, "pause-after=120") {
		t.Fatalf("client = %+v", got[0])
	}
}

// filterMembersBySession tests

func TestFilterMembersBySessionReturnsAllWhenUnpinned(t *testing.T) {
	members := []team.Member{
		{Role: "cto", Session: ""},
		{Role: "fullstack", Session: ""},
	}
	active, skipped := filterMembersBySession(members, "v2-3-0")
	if len(active) != 2 {
		t.Fatalf("active = %d, want 2", len(active))
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped = %d, want 0", len(skipped))
	}
}

func TestFilterMembersBySessionReturnsMatchingAndUnpinned(t *testing.T) {
	members := []team.Member{
		{Role: "go-dev", Session: "v2-3-0"},
		{Role: "architect", Session: ""},
		{Role: "pm-copilot", Session: "pm-copilot"},
	}
	active, skipped := filterMembersBySession(members, "v2-3-0")
	if len(active) != 2 {
		t.Fatalf("active = %d, want 2 (go-dev + architect)", len(active))
	}
	if active[0].Role != "go-dev" || active[1].Role != "architect" {
		t.Fatalf("active roles = %v, want [go-dev architect]", []string{active[0].Role, active[1].Role})
	}
	if len(skipped) != 1 || skipped[0].Role != "pm-copilot" {
		t.Fatalf("skipped = %v, want [pm-copilot]", skipped)
	}
}

func TestFilterMembersBySessionSkipsAllCrossSession(t *testing.T) {
	members := []team.Member{
		{Role: "pm-a", Session: "pm-copilot"},
		{Role: "pm-b", Session: "pm-copilot"},
	}
	active, skipped := filterMembersBySession(members, "v2-3-0")
	if len(active) != 0 {
		t.Fatalf("active = %d, want 0", len(active))
	}
	if len(skipped) != 2 {
		t.Fatalf("skipped = %d, want 2", len(skipped))
	}
}

func TestRunTeamLaunchDryRunMixedSessionFiltersOutCrossSessionMembers(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})
	if err := team.Write(dir, team.Team{
		Members: []team.Member{
			{Role: "go-dev", Binary: "claude", Handle: "go-dev", Session: "v2-3-0"},
			{Role: "architect", Binary: "codex", Handle: "architect", Session: "v2-3-0"},
			{Role: "pm-copilot", Binary: "claude", Handle: "pm-copilot", Session: "pm-copilot"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := captureOutput(t, func() error {
		return runTeamLaunch([]string{"--session", "v2-3-0", "--dry-run", "--no-bootstrap"})
	})
	if err != nil {
		t.Fatalf("runTeamLaunch: %v\nstderr:\n%s", err, stderr)
	}
	if strings.Contains(stdout, "pm-copilot") {
		t.Errorf("dry-run should not include cross-session member pm-copilot:\n%s", stdout)
	}
	for _, want := range []string{"--me go-dev", "--me architect"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("dry-run missing session member %q:\n%s", want, stdout)
		}
	}
	if !strings.Contains(stderr, `skipping pm-copilot`) {
		t.Errorf("stderr missing skip notice for pm-copilot:\n%s", stderr)
	}
}

func TestRunTeamLaunchDryRunMixedSessionIncludesUnpinnedMembers(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})
	if err := team.Write(dir, team.Team{
		Members: []team.Member{
			{Role: "go-dev", Binary: "claude", Handle: "go-dev", Session: "v2-3-0"},
			{Role: "shared-bot", Binary: "claude", Handle: "shared-bot", Session: ""},
			{Role: "pm-copilot", Binary: "claude", Handle: "pm-copilot", Session: "pm-copilot"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := captureOutput(t, func() error {
		return runTeamLaunch([]string{"--session", "v2-3-0", "--dry-run", "--no-bootstrap"})
	})
	if err != nil {
		t.Fatalf("runTeamLaunch: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{"--me go-dev", "--me shared-bot"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("dry-run missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "pm-copilot") {
		t.Errorf("dry-run should not include cross-session member pm-copilot:\n%s", stdout)
	}
}

func TestRunTeamLaunchAllCrossSessionErrors(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})
	if err := team.Write(dir, team.Team{
		Members: []team.Member{
			{Role: "pm-a", Binary: "claude", Handle: "pm-a", Session: "pm-copilot"},
			{Role: "pm-b", Binary: "claude", Handle: "pm-b", Session: "pm-copilot"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	_, _, err = captureOutput(t, func() error {
		return runTeamLaunch([]string{"--session", "v2-3-0", "--dry-run", "--no-bootstrap"})
	})
	if err == nil || !strings.Contains(err.Error(), `no team members are pinned to session "v2-3-0"`) {
		t.Fatalf("runTeamLaunch error = %v, want all-cross-session rejection", err)
	}
}

func TestDefaultTmuxSessionNameSanitizesProject(t *testing.T) {
	got := defaultTmuxSessionName("/Users/me/My Project:API")
	want := "amq-squad-my-project-api"
	if got != want {
		t.Fatalf("defaultTmuxSessionName = %q, want %q", got, want)
	}
}
