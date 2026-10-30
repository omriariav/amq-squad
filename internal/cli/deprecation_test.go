package cli

import (
	"errors"
	"io"
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
		{"team", "launch"},
		{"team", "init"},
		{"team", "rules", "init"},
		{"team", "sync"},
		{"team", "operator", "set"},
	}
	for _, args := range cases {
		assertRemovedUsageError(t, args)
	}
}

// MODERN verbs that depend on the relocated launcher/replay/preview bodies
// must keep working unchanged. These guard against the silent-breakage trap:
// removing the dispatch must not strip the logic the modern verbs call.

// silence unused-import warnings when none of the tests above reference io.
var _ = io.Discard
