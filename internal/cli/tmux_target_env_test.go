package cli

import (
	"os"
	"strings"
	"testing"
)

func TestWithTmuxTargetEnvPrefixesExportedTarget(t *testing.T) {
	t.Setenv("TMUX_PANE", "%9")
	cmd := "cd /repo && amq-squad agent up codex --role cto"
	got := withTmuxTargetEnv("current-window", cmd)
	// #577 finding 4 added an explicit PATH carry, because respawn-pane runs the command
	// under a fresh non-interactive shell that never sources the operator's profile. The
	// expectation is composed from the same environment the function reads rather than frozen,
	// so this pins the CONTRACT (target, launcher pane, PATH, exported subshell) instead of a
	// literal that has to be re-typed whenever a required variable is added.
	want := "(export " + envTmuxTarget + "=current-window " + envTmuxLauncherPane + "='%9' PATH=" + shellQuote(os.Getenv("PATH")) + "; " + cmd + ")"
	if got != want {
		t.Fatalf("withTmuxTargetEnv = %q, want %q", got, want)
	}
	// The assignment is exported (so the amq-squad process inherits it; a plain
	// `VAR=val cmd` would scope it to `cd` only) but wrapped in a subshell so it
	// does not leak into the operator's pane shell after the agent exits.
	if !strings.HasPrefix(got, "(export "+envTmuxTarget+"=") || !strings.HasSuffix(got, ")") {
		t.Fatalf("target env not wrapped in an exported subshell: %q", got)
	}
}

func TestWithTmuxTargetEnvEmptyTargetUnchanged(t *testing.T) {
	cmd := "cd /repo && amq-squad agent up codex"
	if got := withTmuxTargetEnv("", cmd); got != cmd {
		t.Fatalf("empty target must leave command unchanged, got %q", got)
	}
	if got := withTmuxTargetEnv("   ", cmd); got != cmd {
		t.Fatalf("blank target must leave command unchanged, got %q", got)
	}
}

func TestWithTmuxTargetEnvQuotesValue(t *testing.T) {
	// Defense in depth: the value is a controlled enum, but it is shell-quoted
	// so it can never inject shell syntax into the sent command.
	t.Setenv("TMUX_PANE", "%9")
	got := withTmuxTargetEnv("new-session", "cmd")
	// The trailing "; " moved when #577 appended the PATH carry, so this asserts the QUOTING
	// of each assignment rather than that the launcher pane is last. Pinning the end of the
	// list makes every future required variable a test failure with nothing wrong.
	if !strings.Contains(got, envTmuxTarget+"=new-session "+envTmuxLauncherPane+"='%9'") {
		t.Fatalf("unexpected quoting/shape: %q", got)
	}
	// PATH is quoted too: it is the one assignment whose value comes from outside this
	// process's control, so it is the one that could actually carry shell syntax.
	if !strings.Contains(got, "PATH="+shellQuote(os.Getenv("PATH"))+"; ") {
		t.Fatalf("PATH must be shell-quoted and last before the command: %q", got)
	}
}
