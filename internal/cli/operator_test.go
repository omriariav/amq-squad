package cli

import (
	"path/filepath"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/state"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

func TestOperatorAnswerSendsApprovedGateAnswer(t *testing.T) {
	project, _, _ := seedNotifyProject(t)
	seedLegacyOperatorQuestion(t, project, squadnamespace.DefaultProfile, "s", "cto", "gate/release")
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-answer to cto\n")

	stdout, stderr, err := captureOutput(t, func() error {
		return runOperator([]string{
			"answer",
			"--project", project,
			"--session", "s",
			"--gate", "release",
			"--to", "cto",
			"--approved",
			"--reason", "checks passed",
			"--json",
		})
	})
	if err != nil {
		t.Fatalf("operator answer: %v\nstderr:\n%s", err, stderr)
	}
	if len(*calls) != 1 {
		t.Fatalf("amq calls = %d, want 1", len(*calls))
	}
	args := (*calls)[0].Arg
	for _, want := range []string{"send", "--me", "user", "--to", "cto", "--thread", "gate/release", "--kind", "answer", "--subject", "APPROVED: release", "--body", "checks passed"} {
		if !argListContains(args, want) {
			t.Fatalf("operator answer args missing %q: %v", want, args)
		}
	}
	env := decodeJSONEnvelope[mutationResult](t, stdout)
	if env.Kind != "operator_send" || env.Data.MessageID != "msg-answer" || env.Data.Thread != "gate/release" {
		t.Fatalf("operator answer json = %+v", env)
	}
}

func TestOperatorAnswerSendsDeniedGateAnswerWithGatePrefix(t *testing.T) {
	project, _, _ := seedNotifyProject(t)
	seedLegacyOperatorQuestion(t, project, squadnamespace.DefaultProfile, "s", "cto", "gate/release")
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-denied to cto\n")

	_, stderr, err := captureOutput(t, func() error {
		return runOperator([]string{
			"answer",
			"--project", project,
			"--session", "s",
			"--gate", "gate/release",
			"--to", "cto",
			"--denied",
		})
	})
	if err != nil {
		t.Fatalf("operator denied answer: %v\nstderr:\n%s", err, stderr)
	}
	args := (*calls)[0].Arg
	for _, want := range []string{"--thread", "gate/release", "--subject", "DENIED: release", "--body", ""} {
		if !argListContains(args, want) {
			t.Fatalf("operator denied args missing %q: %v", want, args)
		}
	}
}

func seedLegacyOperatorQuestion(t *testing.T, project, profile, session, from, gate string) {
	t.Helper()
	base := filepath.Join(project, ".agent-mail")
	if profile != squadnamespace.DefaultProfile {
		base = filepath.Join(base, profile)
	}
	root := filepath.Join(base, session)
	seedAgentRecord(t, base, session, from, launch.Record{CWD: project, Binary: "codex", Role: from, Handle: from, Session: session, Root: root, TeamProfile: profile, AgentPID: seededDeadPID})
	seedNotifyMessageToDir(t, filepath.Join(root, "agents", team.DefaultOperatorHandle), "new", notifyMsg{ID: "legacy-question", From: from, To: team.DefaultOperatorHandle, Thread: gate, Subject: "APPROVAL: legacy", Kind: string(state.KindQuestion), Created: notifyNow})
}

func TestOperatorCommandsRefuseOperatorTarget(t *testing.T) {
	project, _, _ := seedNotifyProject(t)
	seedLegacyOperatorQuestion(t, project, squadnamespace.DefaultProfile, "s", "cto", "gate/release")
	_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg to user\n")

	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "answer", args: []string{"answer", "--project", project, "--session", "s", "--gate", "release", "--to", "user", "--approved"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := captureOutput(t, func() error { return runOperator(tc.args) })
			assertOperatorMailboxOnlyError(t, err)
		})
	}
}

func argListContains(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
