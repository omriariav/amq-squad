package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/internal/team"
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

func TestRunUpDryRunJSONEnvelope(t *testing.T) {
	seedTeam(t, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "issue-96"},
		},
	})
	stdout, _, err := captureOutput(t, func() error {
		return runUp([]string{"--dry-run", "--json", "--no-bootstrap"})
	})
	if err != nil {
		t.Fatalf("up --dry-run --json: %v", err)
	}
	env := decodeJSONEnvelope[teamPlan](t, stdout)
	if env.Kind != "team_plan" {
		t.Errorf("kind = %q, want team_plan", env.Kind)
	}
	if env.Data.Workstream != "issue-96" {
		t.Errorf("workstream = %q, want issue-96", env.Data.Workstream)
	}
	if env.Data.Members != 2 {
		t.Errorf("members = %d, want 2", env.Data.Members)
	}
	if len(env.Data.Plan) != 2 {
		t.Fatalf("plan = %d entries, want 2", len(env.Data.Plan))
	}
	// Per-member commands must be present and use the modern `agent up`
	// surface (legacy `launch <binary>` is deprecated and must not appear
	// in fresh team-plan output).
	for _, m := range env.Data.Plan {
		if !strings.Contains(m.Command, "agent up") {
			t.Errorf("plan member %s missing 'agent up' surface: %q", m.Role, m.Command)
		}
		if strings.Contains(m.Command, " launch ") || strings.HasSuffix(m.Command, " launch") {
			t.Errorf("plan member %s leaked deprecated 'launch' surface: %q", m.Role, m.Command)
		}
	}
	// Trust default must be present so callers can inspect it.
	if env.Data.Trust == "" {
		t.Errorf("trust missing from team_plan: %+v", env.Data)
	}
}

func TestRunUpJSONRejectsWithoutDryRun(t *testing.T) {
	seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "main"},
		},
	})
	_, _, err := captureOutput(t, func() error {
		return runUp([]string{"--json", "--no-bootstrap"})
	})
	if err == nil {
		t.Fatal("up --json without --dry-run must error")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "--dry-run") {
		t.Errorf("error should mention --dry-run: %v", err)
	}
}

// Regression: up --dry-run --json --seed-from must use a single captured
// timestamp for both the brief frontmatter's generated_at and the JSON
// envelope's generated_at. Sampling the clock twice can let them drift.
func TestRunUpDryRunJSONSeedFromUsesOneClockReading(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	})
	// Advance the clock by one second on each call. If the code samples
	// seedNow() twice, the two timestamps will differ by a second.
	var calls int
	base := time.Date(2026, 5, 17, 13, 0, 0, 0, time.UTC)
	prev := seedNow
	seedNow = func() time.Time {
		t := base.Add(time.Duration(calls) * time.Second)
		calls++
		return t
	}
	t.Cleanup(func() { seedNow = prev })

	source := dir + "/brief.md"
	if err := writeStringFile(source, "# Seeded\n"); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := captureOutput(t, func() error {
		return runUp([]string{"--dry-run", "--json", "--seed-from", "file:" + source})
	})
	if err != nil {
		t.Fatalf("up --dry-run --json --seed-from: %v", err)
	}
	env := decodeJSONEnvelope[briefCandidate](t, stdout)
	want := base.UTC().Format(time.RFC3339)
	if env.Data.GeneratedAt != want {
		t.Errorf("envelope generated_at = %q, want %q", env.Data.GeneratedAt, want)
	}
	// The brief content's frontmatter must carry the same timestamp.
	if !strings.Contains(env.Data.Content, "generated_at: "+want) {
		t.Errorf("brief frontmatter generated_at does not match envelope:\n%s", env.Data.Content)
	}
}

func TestRunUpDryRunJSONWithSeedFromEmitsBriefCandidate(t *testing.T) {
	swapSeedClock(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	})
	source := dir + "/brief.md"
	if err := writeStringFile(source, "# Seeded\n\nbody.\n"); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := captureOutput(t, func() error {
		return runUp([]string{"--dry-run", "--json", "--seed-from", "file:" + source})
	})
	if err != nil {
		t.Fatalf("up --dry-run --json --seed-from: %v", err)
	}
	env := decodeJSONEnvelope[briefCandidate](t, stdout)
	if env.Kind != "brief_candidate" {
		t.Errorf("kind = %q, want brief_candidate", env.Kind)
	}
	if env.Data.Source != "file:"+source {
		t.Errorf("source = %q, want file:%s", env.Data.Source, source)
	}
	if env.Data.Generator != "deterministic" {
		t.Errorf("generator = %q, want deterministic", env.Data.Generator)
	}
	if !strings.Contains(env.Data.Content, "# Seeded") {
		t.Errorf("content does not include seeded body: %q", env.Data.Content)
	}
	// Raw markdown must not leak outside the JSON envelope.
	if strings.HasPrefix(stdout, "---") {
		t.Errorf("seed body leaked outside JSON envelope:\n%s", stdout)
	}
}

func TestRunTeamShowJSONMatchesUpDryRunPlan(t *testing.T) {
	seedTeam(t, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	})
	showOut, _, err := captureOutput(t, func() error {
		return runTeamShow([]string{"--json", "--no-bootstrap"})
	})
	if err != nil {
		t.Fatalf("team show --json: %v", err)
	}
	upOut, _, err := captureOutput(t, func() error {
		return runUp([]string{"--dry-run", "--json", "--no-bootstrap"})
	})
	if err != nil {
		t.Fatalf("up --dry-run --json: %v", err)
	}
	if showOut != upOut {
		t.Fatalf("team show --json and up --dry-run --json diverged.\nteam show:\n%s\nup:\n%s", showOut, upOut)
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
	if env.Data.TeamHome != dir {
		t.Errorf("status envelope team_home = %q, want %q", env.Data.TeamHome, dir)
	}
	if env.Data.Workstream != "issue-96" {
		t.Errorf("status envelope workstream = %q, want issue-96", env.Data.Workstream)
	}
}

func writeStringFile(path, body string) error {
	return writeOrFail(path, body)
}
