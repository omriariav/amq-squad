package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/internal/team"
)

func TestMemberActions(t *testing.T) {
	acts := memberActions("/Code/app", team.DefaultProfile, "issue-96", "cto", true)
	if len(acts) != 4 {
		t.Fatalf("want 4 actions, got %d", len(acts))
	}
	byKind := map[string]runtimeActionJSON{}
	for _, a := range acts {
		byKind[a.Kind] = a
	}
	if !byKind["focus"].Available || !byKind["send"].Available {
		t.Errorf("focus/send should be available when the pane is alive")
	}
	if !byKind["resume"].Available || !byKind["status"].Available {
		t.Errorf("resume/status should always be available")
	}
	for _, k := range []string{"focus", "send", "resume", "status"} {
		cmd := byKind[k].Command
		if !strings.HasPrefix(cmd, "amq-squad "+k) {
			t.Errorf("%s command = %q, want it to start with the verb", k, cmd)
		}
		if !strings.Contains(cmd, "--session issue-96") || !strings.Contains(cmd, "--project /Code/app") {
			t.Errorf("%s command missing scope: %q", k, cmd)
		}
	}
	if !strings.Contains(byKind["send"].Command, "--body-file -") {
		t.Errorf("send command should default to stdin body: %q", byKind["send"].Command)
	}
	// A non-default profile is included in scope.
	named := memberActions("/Code/app", "review", "issue-96", "cto", false)
	if !strings.Contains(named[0].Command, "--profile review") {
		t.Errorf("named profile not in command: %q", named[0].Command)
	}
	// Dead pane -> focus/send unavailable.
	dead := memberActions("/Code/app", team.DefaultProfile, "issue-96", "cto", false)
	for _, a := range dead {
		if (a.Kind == "focus" || a.Kind == "send") && a.Available {
			t.Errorf("%s should be unavailable for a dead pane", a.Kind)
		}
	}
}

func TestReadPromptBody(t *testing.T) {
	// --body wins.
	got, err := readPromptBody("hello", "", true, false, strings.NewReader("ignored"), false)
	if err != nil || got != "hello" {
		t.Fatalf("--body: got %q err %v", got, err)
	}
	// --body-file - reads stdin even when interactive (explicit request).
	got, err = readPromptBody("", "-", false, true, strings.NewReader("from stdin\nline2"), true)
	if err != nil || got != "from stdin\nline2" {
		t.Fatalf("--body-file -: got %q err %v", got, err)
	}
	// bare stdin when neither flag set and stdin is piped (not a TTY).
	got, err = readPromptBody("", "", false, false, strings.NewReader("piped"), false)
	if err != nil || got != "piped" {
		t.Fatalf("stdin: got %q err %v", got, err)
	}
	// bare stdin on an interactive TTY -> usage error, never blocks.
	if _, err := readPromptBody("", "", false, false, strings.NewReader(""), true); err == nil {
		t.Fatal("interactive stdin with no body should be a usage error")
	} else if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError for interactive stdin, got %T", err)
	}
	// both flags -> usage error.
	if _, err := readPromptBody("x", "f", true, true, strings.NewReader(""), false); err == nil {
		t.Fatal("--body + --body-file should error")
	}
	// empty body -> error.
	if _, err := readPromptBody("   ", "", true, false, strings.NewReader(""), false); err == nil {
		t.Fatal("empty --body should error")
	}
	// empty stdin -> error.
	if _, err := readPromptBody("", "", false, false, strings.NewReader("  \n"), false); err == nil {
		t.Fatal("empty stdin should error")
	}
}

func TestSendRequiresRole(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return runSend([]string{"--session", "issue-96", "--body", "hi"})
	})
	if err == nil {
		t.Fatal("send without --role should fail")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
}
