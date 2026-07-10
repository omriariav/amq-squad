package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestOrchestrationRulesOperatorPollingMatchesInteractionMode(t *testing.T) {
	for _, tc := range []struct {
		name, mode, want, avoid string
	}{
		{name: "lead pane", mode: team.OperatorInteractionLeadPane, want: "No separate operator poll loop is required for lead-pane interaction", avoid: "owns the required operator poll loop"},
		{name: "separate terminal", mode: team.OperatorInteractionSeparateTerminal, want: "The human operator owns the required poll loop", avoid: "No separate operator poll loop"},
		{name: "noc", mode: team.OperatorInteractionNOC, want: "The NOC/global orchestrator owns the required operator poll loop", avoid: "No separate operator poll loop"},
		{name: "legacy unspecified", want: "Legacy operator delivery requires the operator or parent orchestrator", avoid: "No separate operator poll loop"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			op := team.DefaultOperator()
			op.InteractionMode = tc.mode
			body, err := renderTeamRules(team.Team{
				Project: t.TempDir(), Operator: &op, Orchestrated: true, Lead: "cto",
				Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-393"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(body, tc.want) {
				t.Fatalf("rules missing %q:\n%s", tc.want, body)
			}
			if tc.avoid != "" && strings.Contains(body, tc.avoid) {
				t.Fatalf("rules unexpectedly contain %q:\n%s", tc.avoid, body)
			}
		})
	}
}
