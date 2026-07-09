package catalog

import (
	"reflect"
	"strings"
	"testing"
)

func TestAllReturnsCopy(t *testing.T) {
	a := All()
	if len(a) == 0 {
		t.Fatal("catalog is empty")
	}
	// Mutating the returned slice must not affect internal state.
	a[0].Label = "MUTATED"
	b := All()
	if b[0].Label == "MUTATED" {
		t.Error("All() exposes internal slice; expected a copy")
	}
}

func TestLookupKnownAndUnknown(t *testing.T) {
	r := Lookup("designer")
	if r == nil {
		t.Fatal("expected designer in catalog")
	}
	if r.PreferredBinary != "claude" {
		t.Errorf("designer binary = %q, want claude", r.PreferredBinary)
	}
	if len(r.Skills) == 0 {
		t.Error("designer should have skills")
	}
	if Lookup("not-a-role") != nil {
		t.Error("Lookup returned non-nil for unknown role")
	}
}

func TestMarketPersonas(t *testing.T) {
	cases := map[string]struct {
		label  string
		binary string
	}{
		"senior-dev":   {label: "Senior Developer", binary: "codex"},
		"frontend-dev": {label: "Frontend Developer", binary: "claude"},
		"backend-dev":  {label: "Backend Developer", binary: "codex"},
		"mobile-dev":   {label: "Mobile Developer", binary: "claude"},
		"junior-dev":   {label: "Junior Developer", binary: "codex"},
		"lead":         {label: "Lead", binary: "codex"},
		"agent":        {label: "Agent", binary: "codex"},
	}
	for id, want := range cases {
		got := Lookup(id)
		if got == nil {
			t.Fatalf("persona %s missing from catalog", id)
		}
		if got.Label != want.label || got.PreferredBinary != want.binary {
			t.Errorf("persona %s = (%q, %q), want (%q, %q)", id, got.Label, got.PreferredBinary, want.label, want.binary)
		}
		if got.Profile == "" || got.Description == "" {
			t.Errorf("persona %s should have profile and description", id)
		}
	}
}

func TestGenericRolesAreNeutral(t *testing.T) {
	lead := Lookup("lead")
	if lead == nil {
		t.Fatal("lead role missing")
	}
	for _, want := range []string{
		"Generic orchestrator",
		"raises and tracks operator gates",
		"Does not assume merge, release, or external-action authority",
	} {
		if !strings.Contains(lead.Profile+" "+lead.Description, want) {
			t.Fatalf("lead role missing %q: %+v", want, *lead)
		}
	}
	if strings.Contains(lead.Description, "handles operator gates") {
		t.Fatalf("lead role uses ambiguous gate wording: %q", lead.Description)
	}

	agent := Lookup("agent")
	if agent == nil {
		t.Fatal("agent role missing")
	}
	for _, want := range []string{
		"Generic individual contributor",
		"asks when scope or authority is unclear",
		"Carries no domain-specific persona",
	} {
		if !strings.Contains(agent.Profile+" "+agent.Description, want) {
			t.Fatalf("agent role missing %q: %+v", want, *agent)
		}
	}
}

func TestEveryRoleIsConsistent(t *testing.T) {
	for _, r := range All() {
		if r.ID == "" || r.Label == "" {
			t.Errorf("role %+v missing ID or Label", r)
		}
		if r.Profile == "" {
			t.Errorf("role %s missing profile", r.ID)
		}
		if r.PreferredBinary != "claude" && r.PreferredBinary != "codex" {
			t.Errorf("role %s has unexpected binary %q", r.ID, r.PreferredBinary)
		}
		// DefaultPeers must reference known persona IDs so team show doesn't
		// emit stale role references.
		for _, p := range r.DefaultPeers {
			if Lookup(p) == nil {
				t.Errorf("role %s declares unknown peer %q", r.ID, p)
			}
		}
	}
}

func TestIDsMatchAllOrder(t *testing.T) {
	ids := IDs()
	all := All()
	if len(ids) != len(all) {
		t.Fatalf("IDs len %d, All len %d", len(ids), len(all))
	}
	for i := range ids {
		if ids[i] != all[i].ID {
			t.Errorf("IDs[%d] = %q, All[%d].ID = %q", i, ids[i], i, all[i].ID)
		}
	}
}

func TestResolveSelection(t *testing.T) {
	got, err := ResolveSelection("junior-dev,2")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"junior-dev", "cto"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ResolveSelection = %v, want %v", got, want)
	}

	got, err = ResolveSelection("all")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, IDs()) {
		t.Fatalf("ResolveSelection all = %v, want catalog IDs", got)
	}

	got, err = ResolveSelection("all,qa")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(IDs())+1 || got[len(got)-1] != "qa" {
		t.Fatalf("ResolveSelection all,qa = %v, want all IDs plus qa", got)
	}

	if _, err := ResolveSelection("999"); err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("ResolveSelection 999 error = %v, want out of range", err)
	}
	if _, err := ResolveSelection("banana"); err == nil || !strings.Contains(err.Error(), "banana") {
		t.Fatalf("ResolveSelection banana error = %v, want unknown persona", err)
	}

	got, err = ResolveSelection("lead,agent")
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"lead", "agent"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ResolveSelection lead,agent = %v, want %v", got, want)
	}
}

func TestResolveSelectionAllowingCustom(t *testing.T) {
	// Unknown slugs pass through verbatim (lowercased) as custom roles.
	got, err := ResolveSelectionAllowingCustom("cto,Researcher,2")
	if err != nil {
		t.Fatalf("ResolveSelectionAllowingCustom error = %v", err)
	}
	want := []string{"cto", "researcher", "cto"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ResolveSelectionAllowingCustom = %v, want %v", got, want)
	}
	// "all" still expands to the catalog, not treated as a custom role.
	got, err = ResolveSelectionAllowingCustom("all")
	if err != nil || !reflect.DeepEqual(got, IDs()) {
		t.Fatalf("ResolveSelectionAllowingCustom all = %v (err %v), want catalog IDs", got, err)
	}
	// Out-of-range numbers still error; only non-numeric unknowns are custom.
	if _, err := ResolveSelectionAllowingCustom("999"); err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("ResolveSelectionAllowingCustom 999 error = %v, want out of range", err)
	}
}
