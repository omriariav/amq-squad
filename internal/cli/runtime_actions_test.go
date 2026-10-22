package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/team"
)

func TestSameResolvedDir(t *testing.T) {
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "lk")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if !sameResolvedDir(link, real) {
		t.Errorf("a symlink and its target must compare equal")
	}
	if sameResolvedDir(real, real+"-other") {
		t.Errorf("different dirs must not compare equal")
	}
	if sameResolvedDir("", real) {
		t.Errorf("empty path must not match")
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

func TestSessionActions(t *testing.T) {
	acts := sessionActions("/Code/app", team.DefaultProfile, "issue-96", "")
	byKind := map[string]runtimeActionJSON{}
	for _, a := range acts {
		byKind[a.Kind] = a
	}
	want := []string{"status", "resume_preview", "resume_current_window", "resume_new_session", "stop", "stop_close_panes", "task_list"}
	if len(acts) != len(want) {
		t.Fatalf("want %d session actions, got %d", len(want), len(acts))
	}
	for _, k := range want {
		a, ok := byKind[k]
		if !ok {
			t.Fatalf("missing session action %q", k)
		}
		if a.Scope != "session" || a.Label == "" {
			t.Errorf("%s scope/label wrong: scope=%q label=%q", k, a.Scope, a.Label)
		}
		if a.NamespaceID != "default/issue-96" {
			t.Errorf("%s namespace_id = %q, want default/issue-96", k, a.NamespaceID)
		}
		if !strings.Contains(a.Command, "--session issue-96") || !strings.Contains(a.Command, "--project /Code/app") {
			t.Errorf("%s command missing scope: %q", k, a.Command)
		}
		if strings.Contains(a.Command, "--role") {
			t.Errorf("a session action must not carry --role: %q", a.Command)
		}
	}
	// Read-only vs mutating + confirmation.
	if byKind["status"].Mutates || byKind["resume_preview"].Mutates {
		t.Error("status/resume_preview must not mutate")
	}
	for _, k := range []string{"resume_current_window", "resume_new_session", "stop", "stop_close_panes"} {
		if !byKind[k].Mutates || !byKind[k].NeedsConfirmation {
			t.Errorf("%s must mutate and need confirmation", k)
		}
	}
	// Commands map to real verbs; new-session omits --terminal-session (derived).
	if byKind["stop"].Command != "amq-squad down --project /Code/app --session issue-96 --all" {
		t.Errorf("stop command = %q", byKind["stop"].Command)
	}
	if byKind["stop_close_panes"].Command != "amq-squad down --project /Code/app --session issue-96 --all --close-panes" {
		t.Errorf("stop_close_panes command = %q", byKind["stop_close_panes"].Command)
	}
	if !strings.HasSuffix(byKind["resume_new_session"].Command, "--exec --target new-session") {
		t.Errorf("resume_new_session should omit --terminal-session: %q", byKind["resume_new_session"].Command)
	}
	// Profile: default omitted, named included.
	if strings.Contains(byKind["status"].Command, "--profile") {
		t.Errorf("default profile must be omitted: %q", byKind["status"].Command)
	}
	namedSession := sessionActions("/Code/app", "review", "issue-96", "")
	if !strings.Contains(namedSession[0].Command, "--profile review") {
		t.Error("named profile must appear in session action commands")
	}
	if namedSession[0].NamespaceID != "review/issue-96" {
		t.Errorf("named session action namespace_id = %q, want review/issue-96", namedSession[0].NamespaceID)
	}
	// With no live tmux session, attach_control is ABSENT (no target to attach).
	if _, ok := byKind["attach_control"]; ok {
		t.Errorf("attach_control must be omitted when tmuxSession is empty: %+v", acts)
	}

	// With a live tmux session, attach_control is APPENDED: it is a raw
	// `tmux -CC attach -t <session>` command, session-scoped, non-mutating, and
	// carries no --role.
	withTmux := sessionActions("/Code/app", team.DefaultProfile, "issue-96", "main")
	var attach *runtimeActionJSON
	for i := range withTmux {
		if withTmux[i].Kind == "attach_control" {
			attach = &withTmux[i]
		}
	}
	if attach == nil {
		t.Fatalf("attach_control must be present when tmuxSession is non-empty: %+v", withTmux)
	}
	if attach.Command != "tmux -CC attach -t main" {
		t.Errorf("attach_control command = %q, want %q", attach.Command, "tmux -CC attach -t main")
	}
	if attach.Scope != "session" {
		t.Errorf("attach_control scope = %q, want session", attach.Scope)
	}
	if attach.Mutates || attach.NeedsConfirmation {
		t.Errorf("attach_control must not mutate or need confirmation: %+v", attach)
	}
	if !attach.Available {
		t.Errorf("attach_control must be available when tmuxSession is non-empty: %+v", attach)
	}
	if attach.Label == "" {
		t.Errorf("attach_control must carry a label")
	}
	if strings.Contains(attach.Command, "--role") {
		t.Errorf("attach_control is session-scoped and must not carry --role: %q", attach.Command)
	}
	// A session token needing shell quoting is quoted consistently.
	quoted := sessionActions("/Code/app", team.DefaultProfile, "issue-96", "my session")
	var quotedAttach string
	for _, a := range quoted {
		if a.Kind == "attach_control" {
			quotedAttach = a.Command
		}
	}
	if quotedAttach != "tmux -CC attach -t 'my session'" {
		t.Errorf("attach_control should shell-quote the session token: %q", quotedAttach)
	}
}

func TestFirstLiveTmuxSession(t *testing.T) {
	// No rows / no tmux identity -> "".
	if got := firstLiveTmuxSession(nil); got != "" {
		t.Errorf("nil rows: got %q, want empty", got)
	}
	rows := []statusRecord{
		{Role: "cto"}, // no tmux block
		{Role: "qa", Tmux: &tmuxRuntimeJSON{Session: "dead", PaneID: "%1", PaneAlive: false}},
	}
	if got := firstLiveTmuxSession(rows); got != "" {
		t.Errorf("no live pane: got %q, want empty", got)
	}
	// First live-pane row's session wins, even if a later row is also live.
	rows = append(rows,
		statusRecord{Role: "fs", Tmux: &tmuxRuntimeJSON{Session: "alpha", PaneID: "%2", PaneAlive: true}},
		statusRecord{Role: "be", Tmux: &tmuxRuntimeJSON{Session: "beta", PaneID: "%3", PaneAlive: true}},
	)
	if got := firstLiveTmuxSession(rows); got != "alpha" {
		t.Errorf("first live-pane session: got %q, want alpha", got)
	}
}
