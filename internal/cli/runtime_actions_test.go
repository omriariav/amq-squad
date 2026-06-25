package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
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

// TestCompareCWD locks in the dynamic-team fix: a member with no pinned cwd (the
// `team member add` default) on a team with no project pin yields an empty
// EffectiveCWD; the launch record's cwd must be used so the pane guard has a real
// dir to match instead of "" (which rejects every live pane).
func TestCompareCWD(t *testing.T) {
	if got := compareCWD("/pinned", "/recorded"); got != "/pinned" {
		t.Errorf("a pinned member cwd wins: got %q", got)
	}
	if got := compareCWD("", "/recorded"); got != "/recorded" {
		t.Errorf("no pinned cwd must fall back to the record cwd: got %q", got)
	}
	if got := compareCWD("  ", "/recorded"); got != "/recorded" {
		t.Errorf("blank pinned cwd must fall back to the record cwd: got %q", got)
	}
	if got := compareCWD("", ""); got != "" {
		t.Errorf("no cwd anywhere stays empty: got %q", got)
	}
}

// TestResolveControlTargetDynamicTeamNoPinnedCWD reproduces the 2.0 dogfood
// failure: an orchestrated team with no project pin and a roster-added worker
// with no cwd. With EffectiveCWD empty, the record's cwd backstops the guard so
// send/focus resolve the live pane instead of always rejecting it.
func TestResolveControlTargetDynamicTeamNoPinnedCWD(t *testing.T) {
	// memberRuntime as resolveMemberRuntime would build it after the compareCWD
	// fix: member pins no cwd, so CWD comes from the launch record.
	mr := memberRuntime{
		Member: team.Member{Role: "frontend-dev", Binary: "codex"}, Handle: "frontend-dev",
		CWD:       compareCWD("", "/Users/me/tmp/squad-dogfood-2"),
		HasRecord: true,
		Record:    launch.Record{CWD: "/Users/me/tmp/squad-dogfood-2", Tmux: &launch.TmuxInfo{PaneID: "%311"}},
	}
	if mr.CWD == "" {
		t.Fatal("guard precondition: member cwd must be backfilled from the record, not empty")
	}
	panes := []tmuxpane.TmuxPane{{PaneID: "%311", Session: "squad-dogfood-2", Window: "1", Pane: "0", CWD: "/Users/me/tmp/squad-dogfood-2", Command: "codex"}}
	id, _, ok := resolveControlTarget(mr, "squad-dogfood-2", panes)
	if !ok || id != "%311" {
		t.Fatalf("a roster worker with no pinned cwd must still resolve its live pane, got id=%q ok=%v", id, ok)
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

// TestResolveControlTargetDirectInspectUnderCCScanMiss proves the iTerm2 tmux -CC
// fix (#140): when the global list-panes scan misses the recorded pane (returned
// nothing, or failed wholesale so the caller degraded to nil), the recorded pane
// id is inspected directly and still resolves — while the cwd/title safety guards
// are preserved.
func TestResolveControlTargetDirectInspectUnderCCScanMiss(t *testing.T) {
	mr := memberRuntime{
		Member: team.Member{Role: "cto", Binary: "codex"}, Handle: "cto", CWD: "/repo",
		HasRecord: true, Record: launch.Record{Tmux: &launch.TmuxInfo{PaneID: "%265"}},
	}
	restore := statusPaneInspector
	defer func() { statusPaneInspector = restore }()

	// Scan list is empty (the caller degraded after a -CC scan failure); direct
	// inspection of the recorded id returns the live pane -> resolves.
	statusPaneInspector = func(id string) (tmuxpane.TmuxPane, bool) {
		if id != "%265" {
			return tmuxpane.TmuxPane{}, false
		}
		return tmuxpane.TmuxPane{PaneID: "%265", Session: "main", Window: "0", Pane: "1", CWD: "/repo", Command: "codex"}, true
	}
	if id, _, ok := resolveControlTarget(mr, "issue-96", nil); !ok || id != "%265" {
		t.Fatalf("recorded pane must resolve via direct inspection when the scan misses, got id=%q ok=%v", id, ok)
	}

	// Safety preserved: a directly-inspected pane in a DIFFERENT cwd (reused id
	// after a tmux restart) must NOT be trusted.
	statusPaneInspector = func(string) (tmuxpane.TmuxPane, bool) {
		return tmuxpane.TmuxPane{PaneID: "%265", CWD: "/somewhere/else", Command: "codex"}, true
	}
	if _, _, ok := resolveControlTarget(mr, "issue-96", nil); ok {
		t.Fatal("a directly-inspected pane in a different cwd must not be trusted")
	}

	// Pane truly gone: direct inspection returns not-found -> unresolved.
	statusPaneInspector = func(string) (tmuxpane.TmuxPane, bool) { return tmuxpane.TmuxPane{}, false }
	if _, _, ok := resolveControlTarget(mr, "issue-96", nil); ok {
		t.Fatal("a gone pane must not resolve")
	}

	// Safety preserved: a directly-inspected pane in the right cwd but TITLED for
	// a different agent (reused id after a tmux restart) must NOT be trusted —
	// `send` must never deliver to a sibling. This guards the direct path the same
	// way the scan path is guarded.
	statusPaneInspector = func(string) (tmuxpane.TmuxPane, bool) {
		return tmuxpane.TmuxPane{PaneID: "%265", CWD: "/repo", Command: "codex", Title: "amq:issue-96:qa"}, true
	}
	if _, _, ok := resolveControlTarget(mr, "issue-96", nil); ok {
		t.Fatal("a directly-inspected pane titled for a different agent must be rejected")
	}

	// Happy path: when the scan ALREADY has the pane, the direct inspector must
	// NOT be consulted (no extra tmux call).
	called := false
	statusPaneInspector = func(string) (tmuxpane.TmuxPane, bool) { called = true; return tmuxpane.TmuxPane{}, false }
	panes := []tmuxpane.TmuxPane{{PaneID: "%265", Session: "main", Window: "0", Pane: "1", CWD: "/repo", Command: "codex"}}
	if _, _, ok := resolveControlTarget(mr, "issue-96", panes); !ok {
		t.Fatal("scan hit should resolve")
	}
	if called {
		t.Error("direct inspector must not be called when the scan already found the pane")
	}
}

// TestResolveControlTargetNoRecordedIDNilScan proves the -CC degrade path is safe
// for a member with NO recorded pane id: runSend/focusTarget pass nil panes after
// a scan failure, and the cwd+engine fallback resolver must handle that cleanly
// (unresolved, never a panic or a wrong-pane guess).
func TestResolveControlTargetNoRecordedIDNilScan(t *testing.T) {
	mr := memberRuntime{
		Member: team.Member{Role: "cto", Binary: "codex"}, Handle: "cto", CWD: "/repo",
		HasRecord: true, Record: launch.Record{AgentPID: 4242}, // no Tmux block -> no recorded pane id
	}
	if _, _, ok := resolveControlTarget(mr, "issue-96", nil); ok {
		t.Fatal("no recorded pane id + nil scan must resolve to not-found, not panic or guess")
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
	if len(acts) != 6 {
		t.Fatalf("want 6 actions, got %d", len(acts))
	}
	byKind := map[string]runtimeActionJSON{}
	for _, a := range acts {
		byKind[a.Kind] = a
	}
	if !byKind["focus"].Available || !byKind["send"].Available {
		t.Errorf("focus/send should be available when the pane is alive")
	}
	if !byKind["resume"].Available || !byKind["status"].Available || !byKind["dispatch"].Available || !byKind["task_list"].Available {
		t.Errorf("resume/status/dispatch/task_list should always be available")
	}
	// #7 schema: each action carries a label, an agent scope, and mutate/confirm
	// metadata so a client can render a confirm-gated executable action.
	wantMeta := map[string]struct {
		mutates, confirm bool
		scope            string
	}{
		"focus":     {false, false, "agent"},   // has --role
		"send":      {true, true, "agent"},     // has --role
		"dispatch":  {true, true, "agent"},     // has --role
		"resume":    {true, true, "session"},   // session resume (no --role)
		"status":    {false, false, "session"}, // session board (no --role)
		"task_list": {false, false, "session"},
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
	for _, k := range []string{"focus", "send", "resume", "status", "dispatch", "task_list"} {
		cmd := byKind[k].Command
		verb := k
		if k == "task_list" {
			verb = "task list"
		}
		if !strings.HasPrefix(cmd, "amq-squad "+verb) {
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
	// Dead pane -> focus/send unavailable WITH a reason; other actions stay
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

func TestErrTmuxAccessDenied(t *testing.T) {
	got := strings.ToLower(errTmuxAccessDenied().Error())
	for _, want := range []string{"operation not permitted", "sandboxed", "full access"} {
		if !strings.Contains(got, want) {
			t.Errorf("access-denied error must mention %q; got: %s", want, got)
		}
	}
	// It must NOT be the misleading message it replaces.
	if strings.Contains(got, "no live tmux pane") {
		t.Error("access-denied error must not read as 'no live tmux pane found'")
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
	if byKind["stop"].Command != "amq-squad stop --project /Code/app --session issue-96 --all" {
		t.Errorf("stop command = %q", byKind["stop"].Command)
	}
	if byKind["stop_close_panes"].Command != "amq-squad stop --project /Code/app --session issue-96 --all --close-panes" {
		t.Errorf("stop_close_panes command = %q", byKind["stop_close_panes"].Command)
	}
	if !strings.HasSuffix(byKind["resume_new_session"].Command, "--exec --target new-session") {
		t.Errorf("resume_new_session should omit --terminal-session: %q", byKind["resume_new_session"].Command)
	}
	// Profile: default omitted, named included.
	if strings.Contains(byKind["status"].Command, "--profile") {
		t.Errorf("default profile must be omitted: %q", byKind["status"].Command)
	}
	if !strings.Contains(sessionActions("/Code/app", "review", "issue-96", "")[0].Command, "--profile review") {
		t.Error("named profile must appear in session action commands")
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
