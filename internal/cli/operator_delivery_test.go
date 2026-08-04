package cli

import (
	"strings"
	"testing"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestOperatorDeliveryModeContracts(t *testing.T) {
	for _, tc := range []struct {
		name, mode, surface, owner, contract string
		poll                                 bool
	}{
		{name: "unspecified", surface: "legacy operator mailbox", poll: true, owner: "operator_or_parent", contract: "legacy compatibility"},
		{name: "lead pane", mode: team.OperatorInteractionLeadPane, surface: "lead pane", owner: "none", contract: "lead mirrors decisions"},
		{name: "separate terminal", mode: team.OperatorInteractionSeparateTerminal, surface: "separate operator terminal", poll: true, owner: "operator", contract: "operator terminal"},
		{name: "noc", mode: team.OperatorInteractionNOC, surface: "NOC/global board", poll: true, owner: "noc", contract: "NOC/global orchestrator"},
		{name: "self operator", mode: team.OperatorInteractionSelfOperator, surface: "delegated lead with human override", poll: true, owner: "operator", contract: "exact-session allowlisted gates"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			op := team.DefaultOperator()
			op.InteractionMode = tc.mode
			cfg := team.Team{Operator: &op}
			got := operatorDeliveryForTeam(cfg)
			view := team.EffectiveOperator(cfg)
			if got.InteractionMode != team.EffectiveOperatorInteractionMode(tc.mode) || got.ApprovalSurface != tc.surface || got.PollRequired != tc.poll || got.PollOwner != tc.owner || !got.DurableAMQ || got.WakeSupported {
				t.Fatalf("delivery = %+v", got)
			}
			if view.PollRequired != got.PollRequired {
				t.Fatalf("view/delivery poll split: view=%+v delivery=%+v", view, got)
			}
			status := statusOperatorForTeam(cfg, squadnamespace.Ref{})
			if status.Poll == nil || status.Poll.Required != got.PollRequired || status.Poll.Owner != got.PollOwner {
				t.Fatalf("status/delivery split: status=%+v delivery=%+v", status.Poll, got)
			}
			loop := operatorLoopForDelivery(got)
			if loop.PollRequired != got.PollRequired || loop.Owner != got.PollOwner {
				t.Fatalf("loop/delivery split: loop=%+v delivery=%+v", loop, got)
			}
			if !strings.Contains(strings.ToLower(got.Contract), strings.ToLower(tc.contract)) {
				t.Fatalf("contract %q missing %q", got.Contract, tc.contract)
			}
			if strings.Contains(got.Guidance, "amq drain --include-body") {
				t.Fatalf("namespace-agnostic guidance must not teach a literal drain without an exact root: %q", got.Guidance)
			}
		})
	}
}

func TestOperatorDeliveryAtRootPinsSeparateTerminalPollIdentity(t *testing.T) {
	op := team.DefaultOperator()
	op.Handle = "release operator"
	op.InteractionMode = team.OperatorInteractionSeparateTerminal
	cfg := team.Team{Operator: &op}
	root := "/tmp/mail root/release"

	got := operatorDeliveryForTeamAtRoot(cfg, root)
	want := "amq drain --include-body --root " + shellQuote(root) + " --me " + shellQuote(op.Handle)
	if !strings.Contains(got.Guidance, want) {
		t.Fatalf("rooted operator guidance missing %q: %q", want, got.Guidance)
	}
	for _, supported := range []string{"amq-squad status --json", "amq-squad operator status --json"} {
		if !strings.Contains(got.Guidance, supported) {
			t.Fatalf("operator guidance missing supported inspection %q: %q", supported, got.Guidance)
		}
	}
}
