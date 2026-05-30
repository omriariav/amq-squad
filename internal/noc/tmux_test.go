package noc

import (
	"errors"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/state"
)

func TestResolveTmuxTarget_MatchesByCWDAndEngine(t *testing.T) {
	projectDir := t.TempDir()
	panes := []TmuxPane{
		// Wrong CWD: must be ignored even though command matches.
		{Session: "other", Window: "0", Pane: "0", PID: 100, Command: "codex", CWD: "/elsewhere"},
		// Wrong engine in the right CWD: must be ignored.
		{Session: "shell", Window: "0", Pane: "0", PID: 200, Command: "zsh", CWD: projectDir},
		// The match: right CWD + right engine.
		{Session: "work", Window: "1", Pane: "2", PID: 300, Command: "codex", CWD: projectDir},
	}
	agent := state.Agent{Handle: "codex", Engine: "codex", AgentPID: 999}

	got, ok := ResolveTmuxTarget(agent, projectDir, panes, nil)
	if !ok {
		t.Fatal("expected a match by cwd+engine, got ok=false")
	}
	want := TmuxTarget{Session: "work", Window: "1", Pane: "2"}
	if got != want {
		t.Fatalf("target = %+v, want %+v", got, want)
	}
}

func TestResolveTmuxTarget_PrefersPIDSubtreeMatch(t *testing.T) {
	projectDir := t.TempDir()
	// Two candidate panes, both right CWD + engine. Only the second pane's
	// process subtree contains the agent PID, so it must win regardless of order.
	panes := []TmuxPane{
		{Session: "a", Window: "0", Pane: "0", PID: 1000, Command: "codex", CWD: projectDir},
		{Session: "b", Window: "3", Pane: "1", PID: 2000, Command: "codex", CWD: projectDir},
	}
	agent := state.Agent{Handle: "codex", Engine: "codex", AgentPID: 2500}

	// pidTree: pane 2000 -> 2400 -> 2500 (the agent). Pane 1000 is barren.
	pidTree := func(pid int) []int {
		switch pid {
		case 2000:
			return []int{2400}
		case 2400:
			return []int{2500}
		default:
			return nil
		}
	}

	got, ok := ResolveTmuxTarget(agent, projectDir, panes, pidTree)
	if !ok {
		t.Fatal("expected a match, got ok=false")
	}
	want := TmuxTarget{Session: "b", Window: "3", Pane: "1"}
	if got != want {
		t.Fatalf("pid-subtree match should win: target = %+v, want %+v", got, want)
	}
}

func TestResolveTmuxTarget_ToleratesStaleAgentPID(t *testing.T) {
	projectDir := t.TempDir()
	// Resurrect rotated the PID: launch.json recorded 70241 but the live pane is
	// pid 68476 and its subtree no longer contains 70241. The cwd+engine signal
	// must still resolve the pane (ok=true) despite the stale PID.
	panes := []TmuxPane{
		{Session: "squad", Window: "0", Pane: "0", PID: 68476, Command: "claude", CWD: projectDir},
	}
	agent := state.Agent{Handle: "claude", Engine: "claude", AgentPID: 70241}

	// pidTree for the live pane never contains the stale recorded PID.
	pidTree := func(pid int) []int {
		if pid == 68476 {
			return []int{68999} // some other live child, not 70241
		}
		return nil
	}

	got, ok := ResolveTmuxTarget(agent, projectDir, panes, pidTree)
	if !ok {
		t.Fatal("stale AgentPID must not prevent a cwd+engine match")
	}
	want := TmuxTarget{Session: "squad", Window: "0", Pane: "0"}
	if got != want {
		t.Fatalf("target = %+v, want %+v", got, want)
	}
}

func TestResolveTmuxTarget_NoMatchReturnsFalse(t *testing.T) {
	projectDir := t.TempDir()
	panes := []TmuxPane{
		{Session: "x", Window: "0", Pane: "0", PID: 1, Command: "vim", CWD: projectDir},
		{Session: "y", Window: "0", Pane: "0", PID: 2, Command: "codex", CWD: "/somewhere/else"},
	}
	agent := state.Agent{Handle: "codex", Engine: "codex", AgentPID: 5}

	if _, ok := ResolveTmuxTarget(agent, projectDir, panes, nil); ok {
		t.Fatal("expected ok=false when no pane matches cwd+engine")
	}
}

func TestResolveTmuxTargetForSession_SessionNameBonus(t *testing.T) {
	projectDir := t.TempDir()
	// Two equally-valid cwd+engine panes; the amq session name breaks the tie.
	panes := []TmuxPane{
		{Session: "random", Window: "0", Pane: "0", PID: 10, Command: "codex", CWD: projectDir},
		{Session: "issue-96", Window: "4", Pane: "1", PID: 20, Command: "codex", CWD: projectDir},
	}
	agent := state.Agent{Handle: "cto", Engine: "codex"}

	got, ok := ResolveTmuxTargetForSession(agent, "issue-96", projectDir, panes, nil)
	if !ok {
		t.Fatal("expected a match")
	}
	want := TmuxTarget{Session: "issue-96", Window: "4", Pane: "1"}
	if got != want {
		t.Fatalf("session-name bonus should win: target = %+v, want %+v", got, want)
	}
}

func TestSuggestJump_FormatsTarget(t *testing.T) {
	got := SuggestJump(TmuxTarget{Session: "squad", Window: "1", Pane: "2"})
	want := "tmux switch-client -t squad:1.2"
	if got != want {
		t.Fatalf("SuggestJump = %q, want %q", got, want)
	}
}

func TestSwitchTo_InsideTmuxRunsSwitchClient(t *testing.T) {
	var gotName string
	var gotArgs []string
	restoreExec := swapSwitchExec(func(name string, args ...string) error {
		gotName = name
		gotArgs = args
		return nil
	})
	defer restoreExec()
	restoreEnv := swapTmuxEnv(func() string { return "/tmp/tmux-1000/default,1234,0" })
	defer restoreEnv()

	if err := SwitchTo(TmuxTarget{Session: "squad", Window: "1", Pane: "2"}); err != nil {
		t.Fatalf("SwitchTo inside tmux: %v", err)
	}
	if gotName != "tmux" {
		t.Fatalf("exec name = %q, want tmux", gotName)
	}
	wantArgs := []string{"switch-client", "-t", "squad:1.2"}
	if len(gotArgs) != len(wantArgs) {
		t.Fatalf("args = %v, want %v", gotArgs, wantArgs)
	}
	for i := range wantArgs {
		if gotArgs[i] != wantArgs[i] {
			t.Fatalf("args = %v, want %v", gotArgs, wantArgs)
		}
	}
}

func TestSwitchTo_OutsideTmuxReturnsTypedError(t *testing.T) {
	var calls [][]string
	restoreExec := swapSwitchExec(func(name string, args ...string) error {
		calls = append(calls, append([]string{name}, args...))
		return nil
	})
	defer restoreExec()
	restoreEnv := swapTmuxEnv(func() string { return "" }) // not in tmux
	defer restoreEnv()

	target := TmuxTarget{Session: "squad", Window: "1", Pane: "2"}
	err := SwitchTo(target)
	if err == nil {
		t.Fatal("expected a typed NotInTmuxError when outside tmux")
	}
	var nit *NotInTmuxError
	if !errors.As(err, &nit) {
		t.Fatalf("error = %T (%v), want *NotInTmuxError", err, err)
	}
	if nit.Command != SuggestJump(target) {
		t.Fatalf("NotInTmuxError.Command = %q, want %q", nit.Command, SuggestJump(target))
	}
	// Outside tmux we still attempt a best-effort select-window so an iTerm2 -CC
	// window raises.
	if len(calls) != 1 || calls[0][1] != "select-window" {
		t.Fatalf("expected a single select-window call, got %v", calls)
	}
}

func TestParsePanes_SkipsMalformedRows(t *testing.T) {
	out := "" +
		"sess\t0\t1\t1234\tcodex\t/work\n" +
		"too\tfew\tfields\n" + // skipped
		"\n" + // blank, skipped
		"sess2\t2\t3\tnotanint\tclaude\t/work2\r\n" // CRLF + non-int pid -> pid 0
	panes := parsePanes(out)
	if len(panes) != 2 {
		t.Fatalf("expected 2 parsed panes, got %d: %+v", len(panes), panes)
	}
	if panes[0] != (TmuxPane{Session: "sess", Window: "0", Pane: "1", PID: 1234, Command: "codex", CWD: "/work"}) {
		t.Fatalf("pane[0] = %+v", panes[0])
	}
	if panes[1].PID != 0 || panes[1].CWD != "/work2" || panes[1].Command != "claude" {
		t.Fatalf("pane[1] = %+v (CRLF + bad pid handling)", panes[1])
	}
}

// swapSwitchExec replaces the package-level switchExec seam for a test and
// returns a restore func.
func swapSwitchExec(fn execRunner) func() {
	prev := switchExec
	switchExec = fn
	return func() { switchExec = prev }
}

// swapTmuxEnv replaces the package-level tmuxEnv seam for a test and returns a
// restore func.
func swapTmuxEnv(fn func() string) func() {
	prev := tmuxEnv
	tmuxEnv = fn
	return func() { tmuxEnv = prev }
}
