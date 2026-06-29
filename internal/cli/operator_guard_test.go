package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
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

func TestAgentUpRefusesOperatorRoleAndHandle(t *testing.T) {
	seedOperatorGuardTeam(t)
	setupFakeAMQSessionRoots(t)
	withOutputPolicy(t, outputPolicy{})

	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "role", args: []string{"codex", "--session", "issue-96", "--role", "user", "--dry-run", "--no-bootstrap"}},
		{name: "handle", args: []string{"codex", "--session", "issue-96", "--role", "cto", "--me", "user", "--dry-run", "--no-bootstrap"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := captureOutput(t, func() error {
				return runAgentUp(tc.args)
			})
			assertOperatorMailboxOnlyError(t, err)
		})
	}
}

func TestAgentUpAlwaysReservesUserAndConfiguredOperator(t *testing.T) {
	for _, tc := range []struct {
		name string
		op   team.OperatorConfig
		args []string
	}{
		{name: "disabled still reserves user", op: team.DisabledOperator(), args: []string{"codex", "--session", "issue-96", "--role", "user", "--dry-run", "--no-bootstrap"}},
		{name: "custom operator handle", op: team.OperatorConfig{Enabled: true, Handle: "operator"}, args: []string{"codex", "--session", "issue-96", "--role", "operator", "--dry-run", "--no-bootstrap"}},
		{name: "custom profile still reserves user", op: team.OperatorConfig{Enabled: true, Handle: "operator"}, args: []string{"codex", "--session", "issue-96", "--role", "user", "--dry-run", "--no-bootstrap"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seedTeam(t, team.Team{
				Operator: &tc.op,
				Members: []team.Member{
					{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
				},
			})
			setupFakeAMQSessionRoots(t)
			withOutputPolicy(t, outputPolicy{})
			_, _, err := captureOutput(t, func() error {
				return runAgentUp(tc.args)
			})
			assertOperatorMailboxOnlyError(t, err)
		})
	}
}

func TestOperatorGuardAllowsNormalRole(t *testing.T) {
	seedOperatorGuardTeam(t)
	setupFakeAMQSessionRoots(t)
	withOutputPolicy(t, outputPolicy{})

	_, _, err := captureOutput(t, func() error {
		return runAgentUp([]string{"codex", "--session", "issue-96", "--role", "cto", "--dry-run", "--no-bootstrap"})
	})
	if err != nil {
		t.Fatalf("normal agent up role should pass operator guard: %v", err)
	}

	_, _, err = captureOutput(t, func() error {
		return runFocus([]string{"--session", "issue-96", "--role", "cto"})
	})
	assertNotOperatorMailboxOnlyError(t, err)
}

func TestRoleControlCommandsRefuseOperatorTarget(t *testing.T) {
	seedOperatorGuardTeam(t)
	setupFakeAMQSessionRoots(t)
	withOutputPolicy(t, outputPolicy{})

	cases := []struct {
		name string
		run  func() error
	}{
		{name: "focus", run: func() error { return runFocus([]string{"--session", "issue-96", "--role", "user"}) }},
		{name: "send", run: func() error { return runSend([]string{"--session", "issue-96", "--role", "user", "--body", "hello"}) }},
		{name: "dispatch", run: func() error {
			return runDispatch([]string{"--session", "issue-96", "--role", "user", "--subject", "X", "--body", "y"})
		}},
		{name: "goal deliver", run: func() error {
			return runGoal([]string{"deliver", "--session", "issue-96", "--role", "user", "--goal", "ship"})
		}},
		{name: "stop", run: func() error { return runStop([]string{"--session", "issue-96", "--role", "user"}) }},
		{name: "resume", run: func() error { return runResume([]string{"--session", "issue-96", "--role", "user"}) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := captureOutput(t, tc.run)
			assertOperatorMailboxOnlyError(t, err)
		})
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
