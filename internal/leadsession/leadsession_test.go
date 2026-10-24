package leadsession

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/grants"
)

const ownerFile = `schema: 1
mode: auto
max_seats: 8
fleet:
  max_squads: 2
  cost_per_day_usd: 50
  permitted_repositories:
    - owner/app
`

func grantFixtures(t *testing.T) (squad, fleet grants.Resolved) {
	t.Helper()
	dir := t.TempDir()
	owner := filepath.Join(dir, "grants.yaml")
	if err := os.WriteFile(owner, []byte(ownerFile), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(dir, "app")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	var err error
	if squad, err = grants.LoadFrom(owner, repo); err != nil {
		t.Fatal(err)
	}
	if fleet, err = grants.LoadFrom(owner, ""); err != nil {
		t.Fatal(err)
	}
	return squad, fleet
}

// Acceptance 1 (R39): a squad lead and the operator start from the same
// entrypoint with two grant fixtures; the only difference in their config is
// the grant, and it alone gives the scope.
func TestSameProgramDifferentGrants(t *testing.T) {
	squadGrant, fleetGrant := grantFixtures(t)
	program := Program{Runtime: "pi", Model: "registry-default", Root: "/r/fleet", ProjectPath: "/r", Handle: "lead"}
	leadCfg := Config{Program: program, Grant: squadGrant}
	opCfg := Config{Program: program, Grant: fleetGrant}

	a, b := leadCfg, opCfg
	a.Grant, b.Grant = grants.Resolved{}, grants.Resolved{}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("the configs differ outside the grant:\n%+v\n%+v", a, b)
	}
	if reflect.DeepEqual(leadCfg.Grant, opCfg.Grant) {
		t.Fatal("the two grant fixtures are equal")
	}

	lead, err := Start(leadCfg)
	if err != nil {
		t.Fatal(err)
	}
	op, err := Start(opCfg)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(lead.Program, op.Program) {
		t.Fatalf("two programs: %+v / %+v", lead.Program, op.Program)
	}
	if lead.Scope != ScopeSquad || lead.Mayor || lead.Admits("status --fleet") || lead.Admits("operator start-squad") {
		t.Fatalf("squad grant gave %+v", lead)
	}
	if op.Scope != ScopeFleet || !op.Mayor || !op.Admits("status --fleet") || !op.Admits("operator start-squad") {
		t.Fatalf("fleet grant gave %+v", op)
	}
	for _, v := range LeadVerbs {
		if !lead.Admits(v) || !op.Admits(v) {
			t.Errorf("lead verb %q is not in both sessions", v)
		}
	}
	if squadGrant.Fleet.MaxSquads != 0 || fleetGrant.Fleet.MaxSquads != 2 {
		t.Fatalf("a repository's grant must carry no fleet grant: squad %+v fleet %+v", squadGrant.Fleet, fleetGrant.Fleet)
	}
}

// Manual mode and an insecure owner file give no mayor; insecure gives no
// fleet scope at all.
func TestFleetScopeNarrowing(t *testing.T) {
	_, fleetGrant := grantFixtures(t)
	program := Program{Runtime: "pi", Root: "/r/fleet", Handle: "lead"}
	manual := fleetGrant
	manual.Mode = grants.ModeManual
	if s, _ := Start(Config{Program: program, Grant: manual}); s.Scope != ScopeFleet || s.Mayor {
		t.Fatalf("manual fleet grant: %+v", s)
	}
	insecure := fleetGrant
	insecure.Insecure = true
	if s, _ := Start(Config{Program: program, Grant: insecure}); s.Scope != ScopeSquad || s.Mayor {
		t.Fatalf("insecure grant: %+v", s)
	}
	if _, err := Start(Config{Grant: fleetGrant}); err == nil {
		t.Fatal("an empty program started")
	}
}
