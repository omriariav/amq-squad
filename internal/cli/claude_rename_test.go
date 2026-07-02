package cli

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/launch"
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
	var gotExe string
	var gotArgs []string
	claudeRenameHelperStart = func(exe string, args []string) error {
		gotExe = exe
		gotArgs = append([]string(nil), args...)
		return nil
	}

	rec := launch.Record{
		Binary:  "claude",
		Role:    "fullstack",
		Handle:  "fullstack",
		Session: "issue-96",
		Tmux:    &launch.TmuxInfo{PaneID: "%42"},
	}
	if err := maybeScheduleClaudeSessionRename(rec); err != nil {
		t.Fatalf("maybeScheduleClaudeSessionRename: %v", err)
	}
	if gotExe != "/opt/amq-squad" {
		t.Fatalf("helper exe = %q", gotExe)
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

func TestMaybeScheduleClaudeSessionRenameSkipsCodexAndMissingPane(t *testing.T) {
	oldStart := claudeRenameHelperStart
	t.Cleanup(func() { claudeRenameHelperStart = oldStart })
	calls := 0
	claudeRenameHelperStart = func(string, []string) error {
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
