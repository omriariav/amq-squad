package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunVersionJSONEnvelope(t *testing.T) {
	stdout, _, err := captureOutput(t, func() error {
		return Run([]string{"version", "--json"}, "1.0.0-test")
	})
	if err != nil {
		t.Fatalf("version --json: %v", err)
	}
	env := decodeJSONEnvelope[versionEnvelopeData](t, stdout)
	if env.Kind != "version" {
		t.Errorf("kind = %q, want version", env.Kind)
	}
	if env.Data.Version != "1.0.0-test" {
		t.Errorf("version = %q, want 1.0.0-test", env.Data.Version)
	}
}

func TestRunVersionTextStillWorks(t *testing.T) {
	// Bare version, -v, --version all keep the legacy human line so old
	// scripts grep'ing for "amq-squad <v>" don't break.
	for _, args := range [][]string{{"version"}, {"-v"}, {"--version"}} {
		stdout, _, err := captureOutput(t, func() error {
			return Run(args, "9.9.9")
		})
		if err != nil {
			t.Fatalf("Run %v: %v", args, err)
		}
		if !strings.Contains(stdout, "amq-squad 9.9.9") {
			t.Errorf("Run %v stdout = %q, want 'amq-squad 9.9.9'", args, stdout)
		}
		if strings.Contains(stdout, "schema_version") {
			t.Errorf("Run %v should be text-only, got JSON:\n%s", args, stdout)
		}
	}
}

func TestRunRolesJSONEnvelope(t *testing.T) {
	stdout, _, err := captureOutput(t, func() error {
		return Run([]string{"roles", "--json"}, "v-test")
	})
	if err != nil {
		t.Fatalf("roles --json: %v", err)
	}
	env := decodeJSONEnvelope[rolesEnvelopeData](t, stdout)
	if env.Kind != "roles" {
		t.Errorf("kind = %q, want roles", env.Kind)
	}
	if len(env.Data.Roles) < 2 {
		t.Fatalf("roles = %d, want catalog entries", len(env.Data.Roles))
	}
	if env.Data.Roles[0].Number != 1 || env.Data.Roles[0].ID != "cpo" {
		t.Fatalf("roles[0] = %+v, want number 1 cpo", env.Data.Roles[0])
	}
	if env.Data.Roles[1].Number != 2 || env.Data.Roles[1].ID != "cto" {
		t.Fatalf("roles[1] = %+v, want number 2 cto", env.Data.Roles[1])
	}
	if env.Data.Roles[1].PreferredBinary == "" || env.Data.Roles[1].Profile == "" {
		t.Fatalf("roles[1] should include default CLI and profile copy: %+v", env.Data.Roles[1])
	}
}

func TestRunRolesJSONEnvelopeIncludesStagedCustomRoles(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	rolesDir := filepath.Join(dir, ".amq-squad", "roles")
	if err := os.MkdirAll(rolesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rolesDir, "probe-officer.md"), []byte(`---
label: Probe Officer
binary: codex
description: Investigates runtime probes.
skills: [/probe]
peers: [cto]
---
# Role: Probe Officer
`), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := captureOutput(t, func() error {
		return Run([]string{"roles", "--json"}, "v-test")
	})
	if err != nil {
		t.Fatalf("roles --json: %v", err)
	}
	env := decodeJSONEnvelope[rolesEnvelopeData](t, stdout)
	if len(env.Data.CustomRoles) != 1 {
		t.Fatalf("custom_roles = %+v, want one staged role", env.Data.CustomRoles)
	}
	got := env.Data.CustomRoles[0]
	if got.ID != "probe-officer" || got.Label != "Probe Officer" || got.PreferredBinary != "codex" {
		t.Fatalf("custom role = %+v, want probe-officer metadata", got)
	}
	if got.Description != "Investigates runtime probes." {
		t.Fatalf("custom role description = %q", got.Description)
	}
	if got.Profile != "" {
		t.Fatalf("custom role profile = %q, want empty profile metadata", got.Profile)
	}
	if len(got.Skills) != 1 || got.Skills[0] != "/probe" || len(got.DefaultPeers) != 1 || got.DefaultPeers[0] != "cto" {
		t.Fatalf("custom role skills/peers = %+v / %+v", got.Skills, got.DefaultPeers)
	}
}
