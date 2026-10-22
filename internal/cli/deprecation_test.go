package cli

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// The legacy verbs (top-level launch/restore/list and
// team show/launch) are fully removed in 2.0. Each must now return a
// UsageError (exit 1); the modern replacements are documented in MIGRATION.md
// and the top-level --help "Removed in 2.0" note. These tests pin the exit
// classification so a removed verb can never silently succeed.

// assertRemovedUsageError runs args through the public Run dispatcher and
// asserts it returns a UsageError (exit 1).
func assertRemovedUsageError(t *testing.T, args []string) {
	t.Helper()
	_, _, err := captureOutput(t, func() error { return Run(args, "test") })
	if err == nil {
		t.Fatalf("Run %v: want UsageError, got nil", args)
	}
	var ue UsageError
	if !errors.As(err, &ue) {
		t.Fatalf("Run %v: want UsageError (exit 1), got %T: %v", args, err, err)
	}
	if code := ExitCode(err); code != ExitUser {
		t.Errorf("Run %v: want exit %d, got %d", args, ExitUser, code)
	}
}

func TestRemovedVerbsReturnUsageError(t *testing.T) {
	cases := [][]string{
		{"launch", "codex"},
		{"restore", "--exec", "--role", "cto"},
		{"list"},
		{"team", "show"},
		{"team", "launch"},
	}
	for _, args := range cases {
		assertRemovedUsageError(t, args)
	}
}

// MODERN verbs that depend on the relocated launcher/replay/preview bodies
// must keep working unchanged. These guard against the silent-breakage trap:
// removing the dispatch must not strip the logic the modern verbs call.

// Completion scripts must include the modern 'agent' verb and must NOT
// complete the removed legacy verbs (launch, restore, list) or the removed
// team subcommands (show, launch).
func TestCompletionDropsRemovedVerbs(t *testing.T) {
	stdout, _, err := captureOutput(t, func() error { return runCompletion([]string{"bash"}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"team", "start"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("bash completion missing modern verb %q", want)
		}
	}
	// Top-level removed verbs must be gone from the top-command list.
	for _, gone := range []string{"launch", "restore", "list", "global", "run", "up", "stop"} {
		if containsString(completionTopCommands, gone) {
			t.Errorf("completionTopCommands should not list removed verb %q", gone)
		}
	}
	// Removed team subcommands must be gone from the team-subcommand list.
	for _, gone := range []string{"show", "launch"} {
		if containsString(completionTeamSubcommands, gone) {
			t.Errorf("completionTeamSubcommands should not list removed subcommand %q", gone)
		}
	}
}

// silence unused-import warnings when none of the tests above reference io.
var _ = io.Discard
