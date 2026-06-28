package cli

import (
	"strings"
	"testing"
)

func TestWithTmuxTargetEnvPrefixesExportedTarget(t *testing.T) {
	t.Setenv("TMUX_PANE", "%9")
	cmd := "cd /repo && amq-squad agent up codex --role cto"
	got := withTmuxTargetEnv("current-window", cmd)
	want := "(export " + envTmuxTarget + "=current-window " + envTmuxLauncherPane + "='%9'; " + cmd + ")"
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
	if !strings.Contains(got, envTmuxTarget+"=new-session "+envTmuxLauncherPane+"='%9'; ") {
		t.Fatalf("unexpected quoting/shape: %q", got)
	}
}
