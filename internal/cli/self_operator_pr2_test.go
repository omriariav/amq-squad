package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestRunStartPreflightSelfOperatorExactPolicy(t *testing.T) {
	dir := t.TempDir()
	base := runStartPreflightInput{Project: dir, Profile: "default", Session: "s", Roles: "cto", Lead: "cto", OperatorMode: "self_operator", OperatorModeSet: true, SelfOperatorLead: "cto", SelfOperatorAllow: "merge", SelfOperatorPolicySet: true}
	if got := runStartPreflight(base); got.Err() != nil {
		t.Fatalf("valid policy: %v", got.Err())
	}
	base.SelfOperatorAllow = "spawn"
	if got := runStartPreflight(base); got.Err() == nil || !strings.Contains(got.Err().Error(), "spawn remains human-only") {
		t.Fatalf("spawn policy = %v", got.Err())
	}
}

func TestTeamInitWritesExactSessionSelfOperatorPolicy(t *testing.T) {
	dir := t.TempDir()
	if err := runTeam([]string{"init", "--project", dir, "--roles", "cto", "--session", "s", "--orchestrated", "--lead", "cto", "--operator-mode", "self_operator", "--self-operator-lead", "cto", "--self-operator-allow", "merge"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := team.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	view := team.EffectiveSelfOperator(cfg, "s")
	if !view.Enabled || view.LeadRole != "cto" || strings.Join(view.AllowedGateKinds, ",") != "merge" || team.EffectiveSelfOperator(cfg, "other").Enabled {
		t.Fatalf("policy=%+v other=%+v", view, team.EffectiveSelfOperator(cfg, "other"))
	}
}

func TestRunStartExistingSelfOperatorUsesAuthoritativeExactSessionPolicy(t *testing.T) {
	dir := t.TempDir()
	op := team.DefaultOperator()
	op.InteractionMode = team.OperatorInteractionSelfOperator
	op.SelfOperator = &team.SelfOperatorPolicy{LeadRole: "cto", PolicyRevision: 2, Sessions: map[string]team.SelfOperatorSessionPolicy{"s": {Enabled: true, AllowedGateKinds: []string{"merge"}}}}
	if err := team.Write(dir, team.Team{Operator: &op, Orchestrated: true, Lead: "cto", Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: "s"}}}); err != nil {
		t.Fatal(err)
	}
	input := runStartPreflightInput{Project: dir, Profile: "default", Session: "s", OperatorMode: "self_operator", OperatorModeSet: true}
	if got := runStartPreflight(input); got.Err() != nil {
		t.Fatalf("authoritative policy rejected: %v", got.Err())
	}
	input.Session = "other"
	if got := runStartPreflight(input); got.Err() == nil {
		t.Fatalf("missing exact policy unexpectedly passed")
	}
}
