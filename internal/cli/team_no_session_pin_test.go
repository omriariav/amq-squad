package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/team"
)

func chdirNoSessionPinTest(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})
}

func TestRunTeamInitNoSessionPinLeavesMembersUnpinned(t *testing.T) {
	dir := t.TempDir()
	chdirNoSessionPinTest(t, dir)

	if err := runTeamInit([]string{"--profile", "pm-squad", "--personas", "cto,fullstack", "--no-session-pin"}); err != nil {
		t.Fatalf("runTeamInit: %v", err)
	}
	got, err := team.ReadProfile(dir, "pm-squad")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Members) != 2 {
		t.Fatalf("members = %v, want two", got.Members)
	}
	for _, m := range got.Members {
		if m.Session != "" {
			t.Fatalf("member %s session = %q, want empty (unpinned template)", m.Role, m.Session)
		}
	}
}

func TestRunTeamInitNoSessionPinRejectsExplicitSession(t *testing.T) {
	dir := t.TempDir()
	chdirNoSessionPinTest(t, dir)

	err := runTeamInit([]string{"--profile", "pm-squad", "--personas", "cto", "--no-session-pin", "--session", "issue-96"})
	if err == nil || !strings.Contains(err.Error(), "--no-session-pin") {
		t.Fatalf("expected a --no-session-pin/--session conflict error, got %v", err)
	}
}
