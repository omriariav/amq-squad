package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/internal/role"
	"github.com/omriariav/amq-squad/internal/team"
)

// TestNewTeamCustomRoleDryRunJSON covers the NOC contract: a custom role that
// is not in the built-in catalog is accepted when it carries an explicit
// --binary entry, and it appears in the dry-run JSON plan just like a built-in
// member (role, handle, binary, session populated).
func TestNewTeamCustomRoleDryRunJSON(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	stdout, stderr, err := captureOutput(t, func() error {
		return runNew([]string{
			"team",
			"--roles", "researcher,reviewer",
			"--binary", "researcher=codex,reviewer=claude",
			"--session", "issue-96",
			"--dry-run", "--json",
		})
	})
	if err != nil {
		t.Fatalf("new team custom roles dry-run: %v\nstderr:\n%s", err, stderr)
	}
	env := decodeJSONEnvelope[teamProfilePlan](t, stdout)
	if env.Data.Members != 2 || len(env.Data.Plan) != 2 {
		t.Fatalf("members/plan = %d/%d, want 2/2", env.Data.Members, len(env.Data.Plan))
	}
	want := map[string]string{"researcher": "codex", "reviewer": "claude"}
	for _, m := range env.Data.Plan {
		bin, ok := want[m.Role]
		if !ok {
			t.Fatalf("unexpected role in plan: %q", m.Role)
		}
		if m.Handle != m.Role {
			t.Errorf("role %s handle = %q, want %q", m.Role, m.Handle, m.Role)
		}
		if m.Binary != bin {
			t.Errorf("role %s binary = %q, want %q", m.Role, m.Binary, bin)
		}
		if m.Session != "issue-96" {
			t.Errorf("role %s session = %q, want issue-96", m.Role, m.Session)
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

// TestNewProfileCustomRoleDryRun confirms `new profile NAME` accepts custom
// roles too (it delegates to team init with a named profile).
func TestNewProfileCustomRoleDryRun(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	stdout, stderr, err := captureOutput(t, func() error {
		return runNew([]string{
			"profile", "discovery",
			"--roles", "researcher",
			"--binary", "researcher=codex",
			"--session", "issue-96",
			"--dry-run", "--json",
		})
	})
	if err != nil {
		t.Fatalf("new profile custom role dry-run: %v\nstderr:\n%s", err, stderr)
	}
	env := decodeJSONEnvelope[teamProfilePlan](t, stdout)
	if env.Data.Profile != "discovery" {
		t.Fatalf("profile = %q, want discovery", env.Data.Profile)
	}
	if env.Data.Members != 1 || len(env.Data.Plan) != 1 || env.Data.Plan[0].Role != "researcher" {
		t.Fatalf("expected single researcher member, got %+v", env.Data.Plan)
	}
}

// TestCustomRoleMissingBinaryFails: a custom role without a --binary entry is
// rejected with actionable guidance.
func TestCustomRoleMissingBinaryFails(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	_, _, err := captureOutput(t, func() error {
		return runNew([]string{"team", "--roles", "researcher", "--dry-run"})
	})
	if err == nil {
		t.Fatal("custom role without --binary should fail")
	}
	want := `custom role "researcher" requires --binary researcher=<cli>`
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want it to contain %q", err, want)
	}
}

// TestCustomRoleBinaryForOtherRoleFails: a --binary entry for a role not in
// --roles does not satisfy the custom role's binary requirement.
func TestCustomRoleBinaryForOtherRoleFails(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	_, _, err := captureOutput(t, func() error {
		return runNew([]string{"team", "--roles", "researcher", "--binary", "other=codex", "--dry-run"})
	})
	if err == nil {
		t.Fatal("custom role with unrelated --binary should fail")
	}
	if !strings.Contains(err.Error(), `custom role "researcher" requires --binary`) {
		t.Fatalf("error = %v, want custom-role binary guidance", err)
	}
}

// TestCustomAndBuiltinMix: built-in roles keep their catalog default binary
// while custom roles use the supplied binary.
func TestCustomAndBuiltinMix(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	stdout, stderr, err := captureOutput(t, func() error {
		return runNew([]string{
			"team",
			"--roles", "cto,researcher",
			"--binary", "researcher=claude",
			"--session", "issue-96",
			"--dry-run", "--json",
		})
	})
	if err != nil {
		t.Fatalf("mixed roles dry-run: %v\nstderr:\n%s", err, stderr)
	}
	env := decodeJSONEnvelope[teamProfilePlan](t, stdout)
	bins := map[string]string{}
	for _, m := range env.Data.Plan {
		bins[m.Role] = m.Binary
	}
	if bins["cto"] != "codex" { // cto's catalog default is codex
		t.Errorf("cto binary = %q, want catalog default codex", bins["cto"])
	}
	if bins["researcher"] != "claude" {
		t.Errorf("researcher binary = %q, want claude", bins["researcher"])
	}
}

// TestInvalidCustomSlugFails: a custom role token that is not a valid slug is
// rejected by the existing role-id validation rules.
func TestInvalidCustomSlugFails(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	_, _, err := captureOutput(t, func() error {
		return runNew([]string{"team", "--roles", "Bad Role", "--binary", "Bad Role=codex", "--dry-run"})
	})
	if err == nil {
		t.Fatal("invalid custom slug should fail")
	}
	if !strings.Contains(err.Error(), "invalid role") {
		t.Fatalf("error = %v, want slug-validation error", err)
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

// TestRoleFileFrontmatterBinarySatisfiesRequirement: a markdown role file whose
// frontmatter sets binary needs no --binary, and lands as a first-class member.
func TestRoleFileFrontmatterBinarySatisfiesRequirement(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	rf := writeRoleFile(t, dir, "researcher.md", `---
id: researcher
binary: codex
---
# Role: Research Engineer

## Description
Owns investigation.
`)

	stdout, stderr, err := captureOutput(t, func() error {
		return runNew([]string{"team", "--role-file", rf, "--roles", "cto", "--session", "issue-96", "--dry-run", "--json"})
	})
	if err != nil {
		t.Fatalf("role-file dry-run: %v\nstderr:\n%s", err, stderr)
	}
	env := decodeJSONEnvelope[teamProfilePlan](t, stdout)
	bins := map[string]string{}
	for _, m := range env.Data.Plan {
		bins[m.Role] = m.Binary
	}
	if bins["researcher"] != "codex" {
		t.Fatalf("researcher binary = %q, want codex (from frontmatter); plan=%+v", bins["researcher"], env.Data.Plan)
	}
	if bins["cto"] != "codex" {
		t.Errorf("cto binary = %q, want catalog default", bins["cto"])
	}
}

// TestRoleFileInlinePathStagesVerbatimDoc: an inline path in --roles is loaded
// as a custom role; the authored markdown is staged verbatim under
// .amq-squad/roles/<id>.md and the agent role stub picks it up.
func TestRoleFileInlinePathStagesVerbatimDoc(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	body := "# Role: Scribe\n\n## Description\nKeeps the written record.\n"
	writeRoleFile(t, dir, "scribe.md", body)

	_, stderr, err := captureOutput(t, func() error {
		return runNew([]string{"team", "--roles", "cto,./scribe.md", "--binary", "scribe=claude", "--session", "issue-96"})
	})
	if err != nil {
		t.Fatalf("inline role file: %v\nstderr:\n%s", err, stderr)
	}
	cfg, err := team.Read(dir)
	if err != nil {
		t.Fatalf("read team: %v", err)
	}
	found := false
	for _, m := range cfg.Members {
		if m.Role == "scribe" {
			found = true
			if m.Binary != "claude" {
				t.Errorf("scribe binary = %q, want claude", m.Binary)
			}
		}
	}
	if !found {
		t.Fatalf("scribe not in members: %+v", cfg.Members)
	}
	staged, err := os.ReadFile(team.CustomRolePath(dir, "scribe"))
	if err != nil {
		t.Fatalf("staged role doc missing: %v", err)
	}
	if !strings.Contains(string(staged), "Keeps the written record.") {
		t.Errorf("staged doc not verbatim:\n%s", staged)
	}

	// And the launch-time seed copies the staged doc into the agent's role.md.
	agentDir := t.TempDir()
	if err := seedRoleStub(agentDir, "scribe", dir); err != nil {
		t.Fatalf("seedRoleStub from staged doc: %v", err)
	}
	got, err := os.ReadFile(role.Path(agentDir))
	if err != nil {
		t.Fatalf("read seeded role.md: %v", err)
	}
	if !strings.Contains(string(got), "Keeps the written record.") {
		t.Errorf("seeded role.md did not use staged doc:\n%s", got)
	}
}

// TestRoleFileMissingBinaryFails: a role file with neither a binary frontmatter
// field nor a --binary override is rejected.
func TestRoleFileMissingBinaryFails(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	rf := writeRoleFile(t, dir, "ghost.md", "# Role: Ghost\n\n## Description\nNo binary anywhere.\n")

	_, _, err := captureOutput(t, func() error {
		return runNew([]string{"team", "--role-file", rf, "--dry-run"})
	})
	if err == nil {
		t.Fatal("role file without a binary should fail")
	}
	if !strings.Contains(err.Error(), `custom role "ghost" requires --binary`) {
		t.Fatalf("error = %v, want binary guidance", err)
	}
}

// TestRoleFileYAMLRendersStubDoc: a metadata-only YAML file stages a rendered
// role.md (no verbatim body) and uses its binary field.
func TestRoleFileYAMLRendersStubDoc(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	rf := writeRoleFile(t, dir, "sre.yaml", `id: sre
label: Site Reliability Engineer
binary: claude
description: Owns reliability and on-call.
`)

	_, stderr, err := captureOutput(t, func() error {
		return runNew([]string{"team", "--role-file", rf, "--session", "issue-96"})
	})
	if err != nil {
		t.Fatalf("yaml role file: %v\nstderr:\n%s", err, stderr)
	}
	cfg, err := team.Read(dir)
	if err != nil {
		t.Fatalf("read team: %v", err)
	}
	if len(cfg.Members) != 1 || cfg.Members[0].Role != "sre" || cfg.Members[0].Binary != "claude" {
		t.Fatalf("unexpected members: %+v", cfg.Members)
	}
	staged, err := os.ReadFile(team.CustomRolePath(dir, "sre"))
	if err != nil {
		t.Fatalf("staged role doc missing: %v", err)
	}
	if !strings.Contains(string(staged), "# Role: Site Reliability Engineer") ||
		!strings.Contains(string(staged), "Owns reliability and on-call.") {
		t.Errorf("rendered staged doc wrong:\n%s", staged)
	}
}
