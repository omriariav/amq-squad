package cli

import (
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/team"
)

// Moved from bootstrap_test.go when 2b-2 deleted the kept-dead bootstrap
// prompt; actorExecutionContractForTeam is live.
func TestActorExecutionContractRejectsRoleAndHandleCaseDrift(t *testing.T) {
	tm := team.Team{
		Members: []team.Member{
			{Role: "cto", Handle: "cto", Binary: "codex", ActorMode: team.ActorModeReview},
			{Role: "platform-dev", Handle: "platform-dev", Binary: "codex", ActorMode: team.ActorModeImplementation},
		},
		Orchestrated: true,
		Lead:         "cto",
		LeadMode:     team.LeadModePlanner,
	}
	contract := executionModeData{Mode: executionModeProjectLead, ImplementationAllowed: false}
	for _, tc := range []struct {
		name   string
		role   string
		handle string
	}{
		{name: "role", role: "Platform-Dev", handle: "platform-dev"},
		{name: "handle", role: "platform-dev", handle: "Platform-Dev"},
		{name: "lead-role", role: "CTO", handle: "cto"},
		{name: "lead-handle", role: "cto", handle: "CTO"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actor := actorExecutionContractForTeam(tm, tc.role, tc.handle, contract)
			if actor.IsLead || actor.ImplementationAllowedForYou || actor.DelegationAllowedForYou {
				t.Fatalf("case-drifted actor gained capability: %+v", actor)
			}
		})
	}
}
