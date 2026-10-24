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

type rolesEnvelopeTestData struct {
	Roles []roleEnvelope `json:"roles"`
}

func TestRunRolesJSONEnvelope(t *testing.T) {
	stdout, _, err := captureOutput(t, func() error {
		return Run([]string{"roles", "--json"}, "v-test")
	})
	if err != nil {
		t.Fatalf("roles --json: %v", err)
	}
	env := decodeJSONEnvelope[rolesEnvelopeTestData](t, stdout)
	if env.Kind != "roles" || len(env.Data.Roles) < 2 {
		t.Fatalf("roles envelope = %+v", env)
	}
	if r := env.Data.Roles[1]; r.ID != "cto" || r.PreferredBinary == "" || r.Source != "catalog" {
		t.Fatalf("roles[1] = %+v, want catalog cto", r)
	}
}

func TestRunRolesJSONEnvelopeIncludesStagedCustomRoles(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	rolesDir := filepath.Join(dir, ".amq-squad", "roles")
	if err := os.MkdirAll(rolesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rolesDir, "probe-officer.md"), []byte("# Role: Probe Officer\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := captureOutput(t, func() error {
		return Run([]string{"roles", "--json"}, "v-test")
	})
	if err != nil {
		t.Fatalf("roles --json: %v", err)
	}
	roles := decodeJSONEnvelope[rolesEnvelopeTestData](t, stdout).Data.Roles
	last := roles[len(roles)-1]
	if last.ID != "probe-officer" || !strings.HasSuffix(last.Source, filepath.Join(".amq-squad", "roles", "probe-officer.md")) {
		t.Fatalf("staged role = %+v", last)
	}
}
