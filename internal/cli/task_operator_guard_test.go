package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

// TestTaskOperatorGuardRefusesBareUserWithNoTeam covers the actor side (--me)
// and the assignment side (--assign) for the always-reserved "user" handle,
// even when no team.json is present. The guard sits before the store access,
// so a non-existent task id is irrelevant: every path must refuse first.
func TestTaskOperatorGuardRefusesBareUserWithNoTeam(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "add --assign user", args: []string{"add", "--title", "x", "--assign", "user", "--session", "s"}},
		{name: "claim --me user", args: []string{"claim", "t1", "--me", "user", "--session", "s"}},
		{name: "done --me user", args: []string{"done", "t1", "--me", "user", "--session", "s"}},
		{name: "fail --me user", args: []string{"fail", "t1", "--me", "user", "--session", "s"}},
		{name: "block --me user", args: []string{"block", "t1", "--me", "user", "--session", "s"}},
		{name: "reset --me user", args: []string{"reset", "t1", "--me", "user", "--session", "s"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chdir(t, t.TempDir())
			withFixedTaskNow(t)
			_, _, err := captureOutput(t, func() error { return runTask(tc.args) })
			assertOperatorMailboxOnlyError(t, err)
		})
	}
}

// TestTaskOperatorGuardRefusesConfiguredOperatorHandle proves EffectiveOperator
// (not just the bare "user" literal) drives the guard: a custom operator handle
// is refused on both surfaces, and "user" stays reserved alongside it.
func TestTaskOperatorGuardRefusesConfiguredOperatorHandle(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "add --assign operator", args: []string{"add", "--title", "x", "--assign", "operator", "--session", "s"}},
		{name: "claim --me operator", args: []string{"claim", "t1", "--me", "operator", "--session", "s"}},
		{name: "add --assign user still reserved", args: []string{"add", "--title", "x", "--assign", "user", "--session", "s"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			op := team.OperatorConfig{Enabled: true, Handle: "operator"}
			seedTeam(t, team.Team{
				Operator: &op,
				Members:  []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"}},
			})
			withFixedTaskNow(t)
			_, _, err := captureOutput(t, func() error { return runTask(tc.args) })
			assertOperatorMailboxOnlyError(t, err)
		})
	}
}

// TestTaskOperatorGuardLeavesStoreUnchanged proves the guard fires before any
// store mutation: a refused operator add creates no task, and a refused
// operator claim leaves the existing task pending and unassigned.
func TestTaskOperatorGuardLeavesStoreUnchanged(t *testing.T) {
	chdir(t, t.TempDir())
	withFixedTaskNow(t)

	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"add", "--title", "real work", "--session", "s"})
	}); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"add", "--title", "operator work", "--assign", "user", "--session", "s"})
	}); err == nil {
		t.Fatal("add --assign user should be refused")
	}

	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"claim", "t1", "--me", "user", "--session", "s"})
	}); err == nil {
		t.Fatal("claim --me user should be refused")
	}

	stdout, _, err := captureOutput(t, func() error {
		return runTask([]string{"list", "--json", "--session", "s"})
	})
	if err != nil {
		t.Fatalf("task list --json: %v", err)
	}
	env := decodeJSONEnvelope[tasksEnvelopeData](t, stdout)
	if len(env.Data.Tasks) != 1 {
		t.Fatalf("want exactly 1 task (operator add must not create one), got %d: %+v", len(env.Data.Tasks), env.Data.Tasks)
	}
	only := env.Data.Tasks[0]
	if only.ID != "t1" || only.Status != "pending" || only.AssignedTo != "" {
		t.Fatalf("t1 must be untouched by the refused operator claim: %+v", only)
	}
}

// TestTaskOperatorGuardAllowsNormalHandle is the over-match guard: an ordinary
// role/handle must pass the operator guard on both surfaces and never hit the
// mailbox-only error.
func TestTaskOperatorGuardAllowsNormalHandle(t *testing.T) {
	chdir(t, t.TempDir())
	withFixedTaskNow(t)

	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"add", "--title", "assigned", "--assign", "cto", "--session", "s"})
	}); err != nil {
		t.Fatalf("add --assign cto should pass the operator guard: %v", err)
	}

	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"add", "--title", "plain", "--session", "s"})
	}); err != nil {
		t.Fatalf("add plain task: %v", err)
	}

	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"claim", "t2", "--me", "cto", "--session", "s"})
	}); err != nil {
		if strings.Contains(err.Error(), "non-runnable mailbox participant") {
			t.Fatalf("claim --me cto wrongly hit the operator guard: %v", err)
		}
		t.Fatalf("claim --me cto should succeed: %v", err)
	}
}
