package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/team"
)

func seedOperatorGuardTeam(t *testing.T) string {
	t.Helper()
	return seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
		Orchestrated:  true,
		Lead:          "cto",
		ExecutionMode: executionModeProjectLead,
	})
}

func assertOperatorMailboxOnlyError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("want operator mailbox-only refusal, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"operator", "non-runnable mailbox participant", "gate/<topic>"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("operator guard error missing %q: %v", want, err)
		}
	}
}

func assertNotOperatorMailboxOnlyError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	if strings.Contains(err.Error(), "non-runnable mailbox participant") {
		t.Fatalf("got unexpected operator mailbox-only refusal: %v", err)
	}
}

func TestRoleControlCommandsAlwaysReserveUserWithNoOperator(t *testing.T) {
	op := team.DisabledOperator()
	seedTeam(t, team.Team{
		Operator: &op,
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	})
	_, _, err := captureOutput(t, func() error {
		return runFocus([]string{"--session", "issue-96", "--role", "user"})
	})
	assertOperatorMailboxOnlyError(t, err)
}
