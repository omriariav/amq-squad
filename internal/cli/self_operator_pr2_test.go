package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

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
