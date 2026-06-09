package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/internal/launch"
	"github.com/omriariav/amq-squad/internal/team"
	"github.com/omriariav/amq-squad/internal/tmuxpane"
)

// TestResolveControlTargetSymlinkedCWD reproduces the live-test failure: the
// member cwd is a symlinked path (like macOS /var/folders TMPDIR) while tmux
// reports the resolved real path for the pane. The cwd guard must still match.
func TestResolveControlTargetSymlinkedCWD(t *testing.T) {
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	// Member cwd is the symlink; tmux reports the resolved path for the pane.
	mr := memberRuntime{
		Member: team.Member{Role: "cto", Binary: "codex"}, Handle: "cto", CWD: link,
		HasRecord: true, Record: launch.Record{Tmux: &launch.TmuxInfo{PaneID: "%104"}},
	}
	panes := []tmuxpane.TmuxPane{{PaneID: "%104", Session: "s", Window: "0", Pane: "0", CWD: real, Command: "codex"}}
	id, _, ok := resolveControlTarget(mr, "issue-96", panes)
	if !ok || id != "%104" {
		t.Fatalf("symlinked member cwd must still resolve the recorded pane, got id=%q ok=%v", id, ok)
	}
}

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

func TestResolveControlTargetExactRecordedPane(t *testing.T) {
	mr := memberRuntime{
		Member: team.Member{Role: "cto", Binary: "codex"}, Handle: "cto", CWD: "/repo",
		HasRecord: true, Record: launch.Record{Tmux: &launch.TmuxInfo{PaneID: "%265"}},
	}
	// Recorded pane is live AND its cwd matches -> trusted exactly.
	panes := []tmuxpane.TmuxPane{{PaneID: "%265", Session: "main", Window: "0", Pane: "1", CWD: "/repo", Command: "codex"}}
	id, _, ok := resolveControlTarget(mr, "issue-96", panes)
	if !ok || id != "%265" {
		t.Fatalf("recorded pane should resolve exactly, got id=%q ok=%v", id, ok)
	}
	// Recorded pane id reused in a DIFFERENT cwd (tmux restart) -> not trusted;
	// no other pane matches the member -> unresolved rather than wrong pane.
	stale := []tmuxpane.TmuxPane{{PaneID: "%265", Session: "main", Window: "0", Pane: "1", CWD: "/somewhere/else", Command: "codex"}}
	if _, _, ok := resolveControlTarget(mr, "issue-96", stale); ok {
		t.Fatal("reused pane id in a different cwd must not be trusted")
	}
}

func TestResolveControlTargetRejectsReusedPaneTitledForOther(t *testing.T) {
	// The recorded pane id is alive and in the right cwd, but tmux restarted and
	// %42 is now a SIBLING agent's pane (titled for qa, same repo). cto's send
	// must NOT target it.
	mr := memberRuntime{
		Member: team.Member{Role: "cto", Binary: "codex"}, Handle: "cto", CWD: "/repo",
		HasRecord: true, Record: launch.Record{Tmux: &launch.TmuxInfo{PaneID: "%42"}},
	}
	reused := []tmuxpane.TmuxPane{
		{PaneID: "%42", Session: "main", Window: "0", Pane: "1", CWD: "/repo", Command: "codex", Title: "amq:issue-96:qa"},
	}
	if _, _, ok := resolveControlTarget(mr, "issue-96", reused); ok {
		t.Fatal("a recorded pane reused by a different agent (amq title for another role) must be rejected")
	}
	// Same pane id, but titled for cto (or untitled) -> trusted.
	for _, title := range []string{"amq:issue-96:cto", "", "zsh"} {
		ours := []tmuxpane.TmuxPane{{PaneID: "%42", CWD: "/repo", Command: "codex", Title: title}}
		if _, _, ok := resolveControlTarget(mr, "issue-96", ours); !ok {
			t.Errorf("pane titled %q (ours / untitled) should be trusted", title)
		}
	}
}

func TestResolveControlTargetFallbackReturnsPaneID(t *testing.T) {
	// No recorded pane; the neutral resolver matches by cwd+engine. The returned
	// target must be the exact pane_id, NOT the pane index (which tmux would
	// resolve against the current window).
	mr := memberRuntime{Member: team.Member{Role: "cto", Binary: "codex"}, Handle: "cto", CWD: "/repo"}
	panes := []tmuxpane.TmuxPane{
		{PaneID: "%77", Session: "main", Window: "0", Pane: "3", CWD: "/repo", Command: "codex"},
	}
	id, _, ok := resolveControlTarget(mr, "issue-96", panes)
	if !ok {
		t.Fatal("resolver should match by cwd+engine")
	}
	if id != "%77" {
		t.Fatalf("fallback must return the exact pane_id, got %q (pane index would be \"3\")", id)
	}
}

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
	// #7 schema: each action carries a label, an agent scope, and mutate/confirm
	// metadata so a client can render a confirm-gated executable action.
	wantMeta := map[string]struct {
		mutates, confirm bool
		scope            string
	}{
		"focus":  {false, false, "agent"},   // has --role
		"send":   {true, true, "agent"},     // has --role
		"resume": {true, true, "session"},   // session resume (no --role)
		"status": {false, false, "session"}, // session board (no --role)
	}
	for k, want := range wantMeta {
		a := byKind[k]
		if a.Label == "" || a.Scope != want.scope {
			t.Errorf("%s action label/scope wrong: got scope %q label %q", k, a.Scope, a.Label)
		}
		if a.Mutates != want.mutates || a.NeedsConfirmation != want.confirm {
			t.Errorf("%s mutates/needs_confirmation = %v/%v, want %v/%v", k, a.Mutates, a.NeedsConfirmation, want.mutates, want.confirm)
		}
		// Scope accuracy: an agent-scoped action's command must carry --role; a
		// session-scoped one must not.
		hasRole := strings.Contains(a.Command, "--role ")
		if want.scope == "agent" && !hasRole {
			t.Errorf("%s is agent-scoped but command lacks --role: %q", k, a.Command)
		}
		if want.scope == "session" && hasRole {
			t.Errorf("%s is session-scoped but command has --role: %q", k, a.Command)
		}
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
	// Dead pane -> focus/send unavailable WITH a reason; resume/status stay
	// available with no reason.
	dead := memberActions("/Code/app", team.DefaultProfile, "issue-96", "cto", false)
	for _, a := range dead {
		switch a.Kind {
		case "focus", "send":
			if a.Available {
				t.Errorf("%s should be unavailable for a dead pane", a.Kind)
			}
			if a.Reason == "" {
				t.Errorf("%s should carry a reason when unavailable", a.Kind)
			}
		default:
			if !a.Available || a.Reason != "" {
				t.Errorf("%s should stay available with no reason: %+v", a.Kind, a)
			}
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

func TestResumeExecRejectsNonTmuxTerminal(t *testing.T) {
	// resume --exec runs the built-in tmux plan; it must reject --terminal
	// tmux-session (rather than validating it and silently running the default
	// tmux backend). Window-per-agent on resume is via --target new-window.
	tm := team.Team{Project: "/p", Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto"}}}
	plans := []resumePlan{{Role: "cto", Action: resumeRestore, Command: "cd /p && amq-squad agent up codex --role cto"}}
	err := execResumePlan(tm, "issue-96", plans,
		resumeExecOptions{Enabled: true, Terminal: "tmux-session", Target: "current-window", Layout: "vertical"}, false)
	if err == nil || !strings.Contains(err.Error(), "not supported on resume") {
		t.Fatalf("want rejection of tmux-session terminal on resume --exec, got %v", err)
	}
	if !strings.Contains(err.Error(), "new-window") {
		t.Errorf("error should point to --target new-window: %v", err)
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

func TestSessionActions(t *testing.T) {
	acts := sessionActions("/Code/app", team.DefaultProfile, "issue-96")
	byKind := map[string]runtimeActionJSON{}
	for _, a := range acts {
		byKind[a.Kind] = a
	}
	want := []string{"status", "resume_preview", "resume_current_window", "resume_new_session", "stop"}
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
	for _, k := range []string{"resume_current_window", "resume_new_session", "stop"} {
		if !byKind[k].Mutates || !byKind[k].NeedsConfirmation {
			t.Errorf("%s must mutate and need confirmation", k)
		}
	}
	// Commands map to real verbs; new-session omits --terminal-session (derived).
	if byKind["stop"].Command != "amq-squad stop --project /Code/app --session issue-96 --all" {
		t.Errorf("stop command = %q", byKind["stop"].Command)
	}
	if !strings.HasSuffix(byKind["resume_new_session"].Command, "--exec --target new-session") {
		t.Errorf("resume_new_session should omit --terminal-session: %q", byKind["resume_new_session"].Command)
	}
	// Profile: default omitted, named included.
	if strings.Contains(byKind["status"].Command, "--profile") {
		t.Errorf("default profile must be omitted: %q", byKind["status"].Command)
	}
	if !strings.Contains(sessionActions("/Code/app", "review", "issue-96")[0].Command, "--profile review") {
		t.Error("named profile must appear in session action commands")
	}
}
