package cli

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

func TestClaudeSessionRenameNameUsesRoleAndSession(t *testing.T) {
	rec := launch.Record{
		Binary:  "/opt/bin/claude",
		Handle:  "worker",
		Role:    "Full Stack",
		Session: "Issue 96",
	}
	if got, want := claudeSessionRenameName(rec), "full-stack-issue-96"; got != want {
		t.Fatalf("claudeSessionRenameName = %q, want %q", got, want)
	}
}

func TestMaybeScheduleClaudeSessionRenameStartsDetachedHelper(t *testing.T) {
	oldExecutable := claudeRenameHelperExecutable
	oldStart := claudeRenameHelperStart
	t.Cleanup(func() {
		claudeRenameHelperExecutable = oldExecutable
		claudeRenameHelperStart = oldStart
	})

	claudeRenameHelperExecutable = func() (string, error) { return "/opt/amq-squad", nil }
	var gotExe, gotControlRoot string
	var gotArgs []string
	claudeRenameHelperStart = func(exe, controlRoot string, args []string) error {
		gotExe = exe
		gotControlRoot = controlRoot
		gotArgs = append([]string(nil), args...)
		return nil
	}

	rec := launch.Record{
		Binary:   "claude",
		Role:     "fullstack",
		Handle:   "fullstack",
		Session:  "issue-96",
		TeamHome: "/proj/team-home",
		Tmux:     &launch.TmuxInfo{PaneID: "%42"},
	}
	if err := maybeScheduleClaudeSessionRename(rec); err != nil {
		t.Fatalf("maybeScheduleClaudeSessionRename: %v", err)
	}
	if gotExe != "/opt/amq-squad" {
		t.Fatalf("helper exe = %q", gotExe)
	}
	if gotControlRoot != "/proj/team-home" {
		t.Fatalf("helper control root = %q", gotControlRoot)
	}
	wantArgs := []string{
		claudeRenameHelperCommand,
		"--pane", "%42",
		"--name", "fullstack-issue-96",
		"--delay", defaultClaudeRenameDelay.String(),
	}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("helper args\n got: %#v\nwant: %#v", gotArgs, wantArgs)
	}
}

func TestMaybeScheduleClaudeSessionRenameFallsBackToCWDControlRoot(t *testing.T) {
	oldExecutable := claudeRenameHelperExecutable
	oldStart := claudeRenameHelperStart
	t.Cleanup(func() {
		claudeRenameHelperExecutable = oldExecutable
		claudeRenameHelperStart = oldStart
	})
	claudeRenameHelperExecutable = func() (string, error) { return "/opt/amq-squad", nil }
	var gotControlRoot string
	claudeRenameHelperStart = func(_, controlRoot string, _ []string) error {
		gotControlRoot = controlRoot
		return nil
	}

	rec := launch.Record{
		Binary: "claude", Role: "qa", Handle: "qa", Session: "issue-96",
		CWD: "/proj/cwd-only", Tmux: &launch.TmuxInfo{PaneID: "%9"},
	}
	if err := maybeScheduleClaudeSessionRename(rec); err != nil {
		t.Fatalf("maybeScheduleClaudeSessionRename: %v", err)
	}
	if gotControlRoot != "/proj/cwd-only" {
		t.Fatalf("helper control root = %q, want CWD fallback", gotControlRoot)
	}
}

func TestMaybeScheduleClaudeSessionRenameSkipsNoneMode(t *testing.T) {
	oldExecutable := claudeRenameHelperExecutable
	oldStart := claudeRenameHelperStart
	started := false
	claudeRenameHelperExecutable = func() (string, error) { return "/tmp/amq-squad", nil }
	claudeRenameHelperStart = func(string, string, []string) error {
		started = true
		return nil
	}
	t.Cleanup(func() {
		claudeRenameHelperExecutable = oldExecutable
		claudeRenameHelperStart = oldStart
	})

	err := maybeScheduleClaudeSessionRename(launch.Record{
		Binary: "claude", Role: "qa", Session: "issue-96",
		WakeInjectMode: "none", Tmux: &launch.TmuxInfo{PaneID: "%7"},
	})
	if err != nil {
		t.Fatalf("none-mode rename scheduling: %v", err)
	}
	if started {
		t.Fatal("none mode must suppress delayed Claude /rename injection")
	}
}

func TestMaybeScheduleClaudeSessionRenameSkipsCodexAndMissingPane(t *testing.T) {
	oldStart := claudeRenameHelperStart
	t.Cleanup(func() { claudeRenameHelperStart = oldStart })
	calls := 0
	claudeRenameHelperStart = func(string, string, []string) error {
		calls++
		return errors.New("must not start")
	}

	for _, rec := range []launch.Record{
		{Binary: "codex", Role: "cto", Session: "issue-96", Tmux: &launch.TmuxInfo{PaneID: "%1"}},
		{Binary: "claude", Role: "qa", Session: "issue-96"},
		{Binary: "claude", Role: "qa", Session: "issue-96", Tmux: &launch.TmuxInfo{}},
	} {
		if err := maybeScheduleClaudeSessionRename(rec); err != nil {
			t.Fatalf("maybeScheduleClaudeSessionRename(%+v): %v", rec, err)
		}
	}
	if calls != 0 {
		t.Fatalf("helper start calls = %d, want 0", calls)
	}
}

func TestRunClaudeSessionRenameDeliversSingleLineSlashCommand(t *testing.T) {
	oldSend := sendPromptToPane
	t.Cleanup(func() { sendPromptToPane = oldSend })
	var gotPane, gotPrompt string
	sendPromptToPane = func(paneID, prompt string) error {
		gotPane = paneID
		gotPrompt = prompt
		return nil
	}

	if err := runClaudeSessionRename([]string{"--pane", "%7", "--name", "Full Stack / Issue 96", "--delay", "0s"}); err != nil {
		t.Fatalf("runClaudeSessionRename: %v", err)
	}
	if gotPane != "%7" {
		t.Fatalf("pane = %q", gotPane)
	}
	if gotPrompt != "/rename full-stack-issue-96" {
		t.Fatalf("prompt = %q", gotPrompt)
	}
	if strings.Contains(gotPrompt, "\n") {
		t.Fatalf("rename prompt must stay single-line: %q", gotPrompt)
	}
}

func TestRunClaudeSessionRenameTreatsDeadPaneAsBenignNoOp(t *testing.T) {
	oldSend := sendPromptToPane
	t.Cleanup(func() { sendPromptToPane = oldSend })
	sendPromptToPane = func(paneID, _ string) error {
		return &tmuxpane.DeadPaneError{PaneID: paneID, Err: errors.New("display-message returned no pane")}
	}

	stdout, stderr, err := captureOutput(t, func() error {
		return runClaudeSessionRename([]string{"--pane", "%999", "--name", "gone", "--delay", "0s"})
	})
	if err != nil {
		t.Fatalf("runClaudeSessionRename on a dead pane must be a benign no-op, got: %v", err)
	}
	if stdout != "" || stderr != "" {
		t.Fatalf("dead-pane rename must produce no output (#525): stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestRunClaudeSessionRenamePropagatesOtherSendErrors(t *testing.T) {
	oldSend := sendPromptToPane
	t.Cleanup(func() { sendPromptToPane = oldSend })
	sendPromptToPane = func(string, string) error {
		return errors.New("some other tmux failure")
	}

	if err := runClaudeSessionRename([]string{"--pane", "%7", "--name", "x", "--delay", "0s"}); err == nil {
		t.Fatal("non-dead-pane send errors must still propagate")
	}
}

func TestClaudeRenameHelperIsDispatchableButHiddenFromHelp(t *testing.T) {
	if _, ok := lookupCommand(claudeRenameHelperCommand, "v-test"); !ok {
		t.Fatalf("hidden helper command is not dispatchable")
	}
	stdout, _, err := captureOutput(t, func() error { return Run([]string{"--help"}, "v-test") })
	if err != nil {
		t.Fatalf("Run --help: %v", err)
	}
	if strings.Contains(stdout, claudeRenameHelperCommand) {
		t.Fatalf("hidden helper leaked into help:\n%s", stdout)
	}
}
