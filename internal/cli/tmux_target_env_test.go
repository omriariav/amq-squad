package cli

import (
	"strings"
	"testing"
)

func TestWithTmuxTargetEnvPrefixesExportedTarget(t *testing.T) {
	cmd := "cd /repo && amq-squad agent up codex --role cto"
	got := withTmuxTargetEnv("current-window", cmd)
	want := "export " + envTmuxTarget + "=current-window; " + cmd
	if got != want {
		t.Fatalf("withTmuxTargetEnv = %q, want %q", got, want)
	}
	// The exported assignment must precede the command so the amq-squad process
	// inherits it (a plain `VAR=val cmd` would scope it to `cd` only).
	if !strings.HasPrefix(got, "export "+envTmuxTarget+"=") {
		t.Fatalf("target env not exported before command: %q", got)
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
	got := withTmuxTargetEnv("new-session", "cmd")
	if !strings.Contains(got, envTmuxTarget+"=new-session;") {
		t.Fatalf("unexpected quoting/shape: %q", got)
	}
}
