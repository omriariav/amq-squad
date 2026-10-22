package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/team"
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

func TestRunTeamProfilesJSONEnvelope(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	if err := team.WriteProfile(dir, team.DefaultProfile, team.Team{
		Workstream: "main",
		Members:    []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "main"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := team.WriteProfile(dir, "review", team.Team{
		Workstream: "review",
		Members:    []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "review"}},
	}); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := captureOutput(t, func() error {
		return runTeamProfiles([]string{"--json"})
	})
	if err != nil {
		t.Fatalf("team profiles --json: %v", err)
	}
	env := decodeJSONEnvelope[teamProfilesEnvelopeData](t, stdout)
	if env.Kind != "team_profiles" {
		t.Errorf("kind = %q, want team_profiles", env.Kind)
	}
	if len(env.Data.Profiles) != 2 {
		t.Fatalf("profiles = %d, want 2", len(env.Data.Profiles))
	}
	if env.Data.Profiles[0].Profile != team.DefaultProfile {
		t.Errorf("profiles[0] = %q, want default first", env.Data.Profiles[0].Profile)
	}
	for _, p := range env.Data.Profiles {
		if !p.Operator.Enabled || p.Operator.Handle != team.DefaultOperatorHandle || p.Operator.Runnable {
			t.Errorf("profile %s operator = %+v, want default non-runnable user", p.Profile, p.Operator)
		}
		if !p.Operator.Participant || p.Operator.Kind != "operator" || p.Operator.Assignable || p.Operator.WakeSupported || !p.Operator.PollRequired {
			t.Errorf("profile %s operator participant = %+v, want first-class mailbox operator", p.Profile, p.Operator)
		}
		if !p.Capabilities.OperatorGates {
			t.Errorf("profile %s missing operator_gates capability", p.Profile)
		}
	}
}

func TestRunTeamProfilesJSONLegacySchema2AdvertisesImplicitOperatorGates(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	if err := os.MkdirAll(filepath.Dir(team.Path(dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{
  "schema": 2,
  "members": [{"role":"cto","binary":"codex","handle":"cto","session":"main"}]
}`
	if err := writeStringFile(team.Path(dir), legacy); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := captureOutput(t, func() error {
		return runTeamProfiles([]string{"--json"})
	})
	if err != nil {
		t.Fatalf("team profiles --json: %v", err)
	}
	env := decodeJSONEnvelope[teamProfilesEnvelopeData](t, stdout)
	if len(env.Data.Profiles) != 1 {
		t.Fatalf("profiles = %+v, want one", env.Data.Profiles)
	}
	p := env.Data.Profiles[0]
	if !p.Operator.Enabled || p.Operator.Handle != team.DefaultOperatorHandle || p.Operator.Runnable {
		t.Fatalf("legacy operator = %+v, want enabled compatibility handle user", p.Operator)
	}
	if !p.Operator.Participant || p.Operator.Kind != "operator" || p.Operator.Assignable || p.Operator.WakeSupported || !p.Operator.PollRequired {
		t.Fatalf("legacy operator participant = %+v, want compatibility mailbox operator", p.Operator)
	}
	if !p.Capabilities.OperatorGates {
		t.Fatal("legacy schema 2 profile must advertise implicit operator_gates")
	}
}

// Regression: a corrupt default profile (`.amq-squad/team.json`) must
// warn on stderr just like a corrupt named profile, and stdout must stay
// a valid team_profiles envelope.
func TestRunTeamProfilesJSONStdoutValidWhenDefaultUnreadable(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	if err := team.WriteProfile(dir, team.DefaultProfile, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "main"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := team.WriteProfile(dir, "review", team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "review"}},
	}); err != nil {
		t.Fatal(err)
	}
	// Corrupt the default team.json file directly.
	if err := writeStringFile(team.Path(dir), "{this is not json"); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := captureOutput(t, func() error {
		return runTeamProfiles([]string{"--json"})
	})
	if err != nil {
		t.Fatalf("team profiles --json: %v", err)
	}
	if !strings.Contains(stderr, "warning: read profile default") {
		t.Errorf("expected stderr warning for unreadable default profile, got:\n%s", stderr)
	}
	env := decodeJSONEnvelope[teamProfilesEnvelopeData](t, stdout)
	if env.Kind != "team_profiles" {
		t.Errorf("envelope kind = %q, want team_profiles", env.Kind)
	}
	// Only the still-readable review profile should appear.
	if len(env.Data.Profiles) != 1 || env.Data.Profiles[0].Profile != "review" {
		t.Fatalf("profiles = %+v, want only review", env.Data.Profiles)
	}
}

// Regression: a corrupt named profile must not poison the team_profiles
// envelope. The unreadable-profile warning lands on stderr while stdout
// remains a valid envelope containing the profiles we did read.
func TestRunTeamProfilesJSONStdoutValidWhenProfileUnreadable(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	if err := team.WriteProfile(dir, team.DefaultProfile, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "main"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := team.WriteProfile(dir, "review", team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "review"}},
	}); err != nil {
		t.Fatal(err)
	}
	// Corrupt a named profile after writing.
	badPath := team.ProfilePath(dir, "review")
	if err := writeStringFile(badPath, "{this is not json"); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := captureOutput(t, func() error {
		return runTeamProfiles([]string{"--json"})
	})
	if err != nil {
		t.Fatalf("team profiles --json: %v", err)
	}
	if !strings.Contains(stderr, "warning: read profile review") {
		t.Errorf("expected stderr warning for unreadable profile, got:\n%s", stderr)
	}
	env := decodeJSONEnvelope[teamProfilesEnvelopeData](t, stdout)
	if env.Kind != "team_profiles" {
		t.Errorf("envelope kind = %q, want team_profiles", env.Kind)
	}
	// Default profile is still listed; the broken review profile is skipped.
	if len(env.Data.Profiles) != 1 || env.Data.Profiles[0].Profile != team.DefaultProfile {
		t.Fatalf("profiles = %+v, want only default after skip", env.Data.Profiles)
	}
}

func TestRunStatusJSONHasNoHumanComments(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	_ = base
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	})
	var buf bytes.Buffer
	if err := executeStatus(statusExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(nil, nil, frozenClock()),
		Out:              &buf,
	}); err != nil {
		t.Fatalf("status: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "# ") {
		t.Errorf("status --json leaked human comment lines on stdout:\n%s", out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	wantTeamHome := canonicalFilesystemPath(dir)
	if env.Data.TeamHome != wantTeamHome {
		t.Errorf("status envelope team_home = %q, want %q", env.Data.TeamHome, wantTeamHome)
	}
	if env.Data.Workstream != "issue-96" {
		t.Errorf("status envelope workstream = %q, want issue-96", env.Data.Workstream)
	}
	if !env.Data.Operator.Enabled || env.Data.Operator.Handle != team.DefaultOperatorHandle {
		t.Errorf("status operator = %+v, want default enabled user", env.Data.Operator)
	}
	if !env.Data.Operator.Participant || env.Data.Operator.Kind != "operator" || env.Data.Operator.Runnable || env.Data.Operator.Assignable || env.Data.Operator.WakeSupported || !env.Data.Operator.PollRequired {
		t.Errorf("status operator participant = %+v, want first-class mailbox operator", env.Data.Operator)
	}
	if env.Data.Operator.CanonicalInbox == nil || env.Data.Operator.CanonicalInbox.Handle != team.DefaultOperatorHandle || env.Data.Operator.CanonicalInbox.Session != "issue-96" || env.Data.Operator.CanonicalInbox.Root == "" {
		t.Errorf("status operator canonical_inbox = %+v, want routeable user inbox for issue-96", env.Data.Operator.CanonicalInbox)
	}
	if env.Data.Operator.Poll == nil || !env.Data.Operator.Poll.Required || env.Data.Operator.Poll.Owner != "operator_or_parent" {
		t.Errorf("status operator poll = %+v, want legacy operator_or_parent poll contract", env.Data.Operator.Poll)
	}
	if !env.Data.OperatorDelivery.Enabled || env.Data.OperatorDelivery.Handle != team.DefaultOperatorHandle || !env.Data.OperatorDelivery.PollRequired || env.Data.OperatorDelivery.WakeSupported || !env.Data.OperatorDelivery.DurableAMQ {
		t.Errorf("status operator_delivery = %+v, want virtual operator poll-required durable AMQ", env.Data.OperatorDelivery)
	}
	if !env.Data.Capabilities.OperatorGates {
		t.Errorf("status capabilities = %+v, want operator_gates", env.Data.Capabilities)
	}
}

func writeStringFile(path, body string) error {
	return writeOrFail(path, body)
}
