package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/role"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

func TestSeedGenericRoleStubUsesNeutralCatalogText(t *testing.T) {
	dir := t.TempDir()
	if err := seedRoleStub(dir, "lead", ""); err != nil {
		t.Fatalf("seed lead role: %v", err)
	}
	body, err := os.ReadFile(role.Path(dir))
	if err != nil {
		t.Fatalf("read lead role.md: %v", err)
	}
	got := string(body)
	for _, want := range []string{
		"# Role: Lead",
		"raises and tracks operator gates",
		"Does not assume merge, release, or external-action authority",
		"- agent",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("lead role.md missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "handles operator gates") {
		t.Fatalf("lead role.md uses ambiguous gate wording:\n%s", got)
	}

	agentDir := t.TempDir()
	if err := seedRoleStub(agentDir, "agent", ""); err != nil {
		t.Fatalf("seed agent role: %v", err)
	}
	body, err = os.ReadFile(role.Path(agentDir))
	if err != nil {
		t.Fatalf("read agent role.md: %v", err)
	}
	got = string(body)
	for _, want := range []string{
		"# Role: Agent",
		"asks when scope or authority is unclear",
		"Carries no domain-specific persona",
		"- lead",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("agent role.md missing %q:\n%s", want, got)
		}
	}
}

// TestTeamInitCustomRoleLiveWrite confirms a custom role persists to team.json
// as a first-class member through the `team init` path.
func TestTeamInitCustomRoleLiveWrite(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	_, stderr, err := captureOutput(t, func() error {
		return runTeamInit([]string{
			"--roles", "cto,researcher",
			"--binary", "researcher=codex",
			"--session", "issue-96",
		})
	})
	if err != nil {
		t.Fatalf("team init custom role: %v\nstderr:\n%s", err, stderr)
	}
	cfg, err := team.Read(dir)
	if err != nil {
		t.Fatalf("read team config: %v", err)
	}
	var researcher *team.Member
	for i := range cfg.Members {
		if cfg.Members[i].Role == "researcher" {
			researcher = &cfg.Members[i]
		}
	}
	if researcher == nil {
		t.Fatalf("custom role not persisted; members = %+v", cfg.Members)
	}
	if researcher.Binary != "codex" || researcher.Handle != "researcher" || researcher.Session != "issue-96" {
		t.Fatalf("custom member fields wrong: %+v", *researcher)
	}
}

// TestSeedRoleStubCustomRoleFallbackText: the role.md stub seeded for a custom
// role uses the built-in custom-role fallback copy, not a built-in persona's
// description.
func TestSeedRoleStubCustomRoleFallbackText(t *testing.T) {
	agentDir := t.TempDir()
	if err := seedRoleStub(agentDir, "researcher", ""); err != nil {
		t.Fatalf("seedRoleStub: %v", err)
	}
	body, err := os.ReadFile(role.Path(agentDir))
	if err != nil {
		t.Fatalf("read role.md: %v", err)
	}
	got := string(body)
	if !strings.Contains(got, "# Role: researcher") {
		t.Errorf("role.md missing custom label header:\n%s", got)
	}
	if !strings.Contains(got, "No catalog description is configured for this custom role") {
		t.Errorf("role.md missing custom-role fallback description:\n%s", got)
	}
}

// --- Phase 2: file-based custom roles ---

func writeRoleFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func writeStagedRoleFile(t *testing.T, dir, id, body string) string {
	t.Helper()
	path := team.CustomRolePath(dir, id)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
