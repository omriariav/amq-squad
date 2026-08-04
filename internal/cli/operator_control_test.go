package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestOperatorSendPreviewIsReadOnly(t *testing.T) {
	project, _, _ := seedNotifyProject(t, team.DefaultOperator())
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-operator to cto\n")

	stdout, _, err := captureOutput(t, func() error {
		return runOperator([]string{
			"send", "--project", project, "--session", "s", "--to", "cto",
			"--subject", "Review", "--body", "Please review.",
		})
	})
	if err != nil {
		t.Fatalf("operator send preview: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("preview invoked AMQ: %+v", *calls)
	}
	for _, want := range []string{"Preview only", "--to cto", "--thread p2p/cto__user", "--yes"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("preview missing %q:\n%s", want, stdout)
		}
	}
	if artifacts := v228ReceiptArtifactPaths(t, project); len(artifacts) > 0 {
		t.Fatalf("preview wrote delivery receipt artifacts: %v", artifacts)
	}
}

func TestOperatorSendYesIsAcceptedAndThreadVisibleWithoutReceipt(t *testing.T) {
	project, _, _ := seedNotifyProject(t, team.DefaultOperator())
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-operator to cto\n")

	stdout, stderr, err := captureOutput(t, func() error {
		return runOperator([]string{
			"send", "--project", project, "--session", "s", "--to", "cto",
			"--subject", "Review", "--body", "Please review.", "--yes", "--json",
		})
	})
	if err != nil {
		t.Fatalf("operator send: %v\nstderr:\n%s", err, stderr)
	}
	if len(*calls) != 1 {
		t.Fatalf("AMQ calls = %d, want 1", len(*calls))
	}
	call := (*calls)[0]
	for _, want := range []string{"send", "--me", "user", "--to", "cto", "--thread", "p2p/cto__user", "--kind", "status", "--subject", "Review", "--body", "Please review."} {
		if !argListContains(call.Arg, want) {
			t.Fatalf("send args missing %q: %v", want, call.Arg)
		}
	}
	env := decodeJSONEnvelope[mutationResult](t, stdout)
	if env.Kind != "operator_send" || env.Data.MessageID != "msg-operator" || env.Data.Thread != "p2p/cto__user" {
		t.Fatalf("operator send envelope = %+v", env)
	}
	if env.Data.Root == "" {
		t.Fatalf("operator send missing authoritative AMQ root: %+v", env.Data)
	}
	if artifacts := v228ReceiptArtifactPaths(t, project); len(artifacts) > 0 {
		t.Fatalf("operator send wrote delivery receipt artifacts: %v", artifacts)
	}
}

func TestOperatorSendRejectsUnknownOrOperatorTargetBeforeAMQ(t *testing.T) {
	project, _, _ := seedNotifyProject(t, team.DefaultOperator())
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent should-not-run to x\n")
	for _, target := range []string{"missing", "user"} {
		t.Run(target, func(t *testing.T) {
			_, _, err := captureOutput(t, func() error {
				return runOperator([]string{
					"send", "--project", project, "--session", "s", "--to", target,
					"--subject", "x", "--body", "body", "--yes",
				})
			})
			if err == nil {
				t.Fatalf("target %q should be rejected", target)
			}
		})
	}
	if len(*calls) != 0 {
		t.Fatalf("invalid target invoked AMQ: %+v", *calls)
	}
}

func TestOperatorSendCannotBypassGateAnswerAuthority(t *testing.T) {
	project, _, _ := seedNotifyProject(t, team.DefaultOperator())
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent should-not-run to cto\n")
	_, _, err := captureOutput(t, func() error {
		return runOperator([]string{
			"send", "--project", project, "--session", "s", "--to", "cto",
			"--thread", "gate/release", "--kind", "answer",
			"--subject", "APPROVED", "--body", "yes", "--yes",
		})
	})
	if err == nil || !strings.Contains(err.Error(), "operator answer") {
		t.Fatalf("gate bypass error = %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("gate bypass invoked AMQ: %+v", *calls)
	}
}

func TestBroadcastPreviewAndConfirmedTransportAcceptance(t *testing.T) {
	project := t.TempDir()
	cfg := team.Team{
		Project: project, Workstream: "s",
		Members: []team.Member{
			{Role: "qa", Handle: "qa", Binary: "codex", Session: "s"},
			{Role: "cto", Handle: "cto", Binary: "codex", Session: "s"},
		},
	}
	if err := team.Write(project, cfg); err != nil {
		t.Fatal(err)
	}
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-broadcast to cto,qa\n")

	preview, _, err := captureOutput(t, func() error {
		return runBroadcast([]string{
			"--project", project, "--session", "s",
			"--subject", "All hands", "--body", "Join now.",
		})
	})
	if err != nil {
		t.Fatalf("broadcast preview: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("broadcast preview invoked AMQ: %+v", *calls)
	}
	for _, want := range []string{"Recipients: cto,qa", "Thread: broadcast/all-hands", "--yes"} {
		if !strings.Contains(preview, want) {
			t.Fatalf("broadcast preview missing %q:\n%s", want, preview)
		}
	}

	stdout, stderr, err := captureOutput(t, func() error {
		return runBroadcast([]string{
			"--project", project, "--session", "s",
			"--subject", "All hands", "--body", "Join now.", "--yes", "--json",
		})
	})
	if err != nil {
		t.Fatalf("broadcast send: %v\nstderr:\n%s", err, stderr)
	}
	if len(*calls) != 1 {
		t.Fatalf("AMQ calls = %d, want 1", len(*calls))
	}
	args := (*calls)[0].Arg
	for _, want := range []string{"--to", "cto,qa", "--thread", "broadcast/all-hands", "--kind", "status", "--subject", "All hands"} {
		if !argListContains(args, want) {
			t.Fatalf("broadcast args missing %q: %v", want, args)
		}
	}
	env := decodeJSONEnvelope[mutationResult](t, stdout)
	if env.Data.MessageID != "msg-broadcast" || env.Data.Thread != "broadcast/all-hands" || env.Data.Root == "" {
		t.Fatalf("broadcast envelope = %+v", env)
	}
	if artifacts := v228ReceiptArtifactPaths(t, project); len(artifacts) > 0 {
		t.Fatalf("broadcast wrote delivery receipt artifacts: %v", artifacts)
	}
}

func TestBroadcastRecipientSetIsDeterministic(t *testing.T) {
	cfg := team.Team{Members: []team.Member{
		{Handle: "qa"}, {Handle: "cto"}, {Handle: "qa"}, {Handle: "user"}, {},
	}}
	if got := strings.Join(operatorBroadcastRecipients(cfg, "user"), ","); got != "cto,qa" {
		t.Fatalf("recipients = %q, want cto,qa", got)
	}
}
