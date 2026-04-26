package catalog

import "testing"

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
