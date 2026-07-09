package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

// seedProfile writes a profile config into projectDir without changing the
// process cwd. Returns the resolved profile path so the caller can assert
// on placement (default profile lives at .amq-squad/team.json; named
// profiles under .amq-squad/teams/<name>.json).
func seedProfile(t *testing.T, projectDir, profile string, cfg team.Team) string {
	t.Helper()
	if err := team.WriteProfile(projectDir, profile, cfg); err != nil {
		t.Fatal(err)
	}
	return team.ProfilePath(projectDir, profile)
}

func TestResolveProfileFlag(t *testing.T) {
	cases := map[string]struct {
		in      string
		want    string
		wantErr bool
	}{
		"empty":   {in: "", want: team.DefaultProfile},
		"default": {in: "default", want: team.DefaultProfile},
		"named":   {in: "review", want: "review"},
		"bad":     {in: "Bad/Name", wantErr: true},
	}
	for name, tc := range cases {
		got, err := resolveProfileFlag(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s: want error, got %q", name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected err %v", name, err)
		}
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}

func TestRunTeamProfilesListsDefaultFirstThenNamedSorted(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	seedProfile(t, dir, team.DefaultProfile, team.Team{
		Workstream: "main",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "main"},
		},
	})
	for _, p := range []string{"review", "alpha"} {
		seedProfile(t, dir, p, team.Team{
			Workstream: p,
			Members: []team.Member{
				{Role: "cto", Binary: "codex", Handle: "cto", Session: p},
				{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: p},
			},
		})
	}
	stdout, _, err := captureOutput(t, func() error { return runTeamProfiles(nil) })
	if err != nil {
		t.Fatalf("team profiles: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) < 4 {
		t.Fatalf("expected 4 lines (header + 3 profiles), got %d:\n%s", len(lines), stdout)
	}
	// Header is line 0.
	want := []string{"default", "alpha", "review"}
	for i, name := range want {
		if !strings.HasPrefix(lines[i+1], name) {
			t.Errorf("line %d = %q, want prefix %q", i+1, lines[i+1], name)
		}
	}
	// Members column for review profile should be 2.
	if !strings.Contains(stdout, "main") || !strings.Contains(stdout, "review") {
		t.Errorf("output missing workstream columns:\n%s", stdout)
	}
}

func TestRunTeamProfilesInfersSharedMemberSession(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	seedProfile(t, dir, team.DefaultProfile, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "sleep", Handle: "cto", Session: "operator-smoke"},
			{Role: "qa", Binary: "sleep", Handle: "qa", Session: "operator-smoke"},
		},
	})

	stdout, _, err := captureOutput(t, func() error { return runTeamProfiles(nil) })
	if err != nil {
		t.Fatalf("team profiles: %v", err)
	}
	if !strings.Contains(stdout, "operator-smoke") {
		t.Fatalf("team profiles should show inferred shared member session:\n%s", stdout)
	}
	if strings.Contains(stdout, "(default)") {
		t.Fatalf("team profiles should not fall back to default when members share a session:\n%s", stdout)
	}
}

func TestRunTeamProfilesProjectTargetsOtherDir(t *testing.T) {
	project := t.TempDir()
	other := t.TempDir()
	chdir(t, other)
	seedProfile(t, project, team.DefaultProfile, team.Team{
		Workstream: "remote",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "remote"},
		},
	})

	stdout, _, err := captureOutput(t, func() error {
		return runTeamProfiles([]string{"--project", project})
	})
	if err != nil {
		t.Fatalf("team profiles --project: %v", err)
	}
	if !strings.Contains(stdout, team.ProfilePath(project, team.DefaultProfile)) {
		t.Errorf("profiles output should point at requested project:\n%s", stdout)
	}
	if strings.Contains(stdout, other) {
		t.Errorf("profiles output should not inspect current cwd:\n%s", stdout)
	}
}

func TestRunTeamProfilesEmptyDirSilent(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	stdout, stderr, err := captureOutput(t, func() error { return runTeamProfiles(nil) })
	if err != nil {
		t.Fatalf("team profiles: %v", err)
	}
	if stdout != "" {
		t.Errorf("empty project should print nothing to stdout, got: %s", stdout)
	}
	if !strings.Contains(stderr, "No team profiles configured") {
		t.Errorf("stderr should advise next step:\n%s", stderr)
	}
}

func TestRunUpProfileDryRunUsesNamedRoster(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	// Default profile = different roster than the named one.
	seedProfile(t, dir, team.DefaultProfile, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "main"},
		},
	})
	seedProfile(t, dir, "review", team.Team{
		Members: []team.Member{
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "review"},
			{Role: "qa", Binary: "claude", Handle: "qa", Session: "review"},
		},
	})

	stdout, _, err := captureOutput(t, func() error {
		return runUp([]string{"--dry-run", "--no-bootstrap", "--profile", "review"})
	})
	if err != nil {
		t.Fatalf("up --dry-run --profile review: %v", err)
	}
	for _, want := range []string{"agent up claude", "--me fullstack", "--me qa", "--team-profile review"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output missing %q in:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "--me cto") {
		t.Errorf("named profile output leaked default roster:\n%s", stdout)
	}
}

func TestEmittedCommandsCarryDefaultProfileImplicitly(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	seedProfile(t, dir, team.DefaultProfile, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "main"},
		},
	})
	stdout, _, err := captureOutput(t, func() error {
		return runUp([]string{"--dry-run", "--no-bootstrap"})
	})
	if err != nil {
		t.Fatalf("up --dry-run: %v", err)
	}
	// Default profile must NOT emit --team-profile (legacy launch.json
	// readers should not see a flag they didn't ship with).
	if strings.Contains(stdout, "--team-profile") {
		t.Errorf("default profile must not emit --team-profile:\n%s", stdout)
	}
}

func TestRunUpProfileUnknownErrors(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	seedProfile(t, dir, team.DefaultProfile, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "main"},
		},
	})
	_, _, err := captureOutput(t, func() error {
		return runUp([]string{"--dry-run", "--profile", "missing"})
	})
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("want unknown-profile error, got %v", err)
	}
}

func TestRunDownProfileScopedToNamedRoster(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	seedProfile(t, dir, team.DefaultProfile, team.Team{
		Members: []team.Member{
			{Role: "default-only", Binary: "codex", Handle: "default-only", Session: "main"},
		},
	})
	seedProfile(t, dir, "review", team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "review"},
		},
	})
	_, err := runDownExec(t, downExecution{
		ProjectDir:       dir,
		RequestedSession: "review",
		ExplicitSession:  true,
		Role:             "default-only",
		Profile:          "review",
		Terminator:       &recordingTerminator{},
		Probe:            downFakeProbe(nil, nil),
	})
	if err == nil || !strings.Contains(err.Error(), `unknown role "default-only"`) {
		t.Fatalf("named-profile down should not see default-only role: got %v", err)
	}
}

func TestRunStatusProfileScopedToNamedRoster(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := t.TempDir()
	chdir(t, dir)
	seedProfile(t, dir, "review", team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "review"},
		},
	})
	// Default profile holds a different member that should be ignored.
	seedProfile(t, dir, team.DefaultProfile, team.Team{
		Members: []team.Member{
			{Role: "stranger", Binary: "claude", Handle: "stranger", Session: "main"},
		},
	})
	_ = base

	var buf bytes.Buffer
	err := executeStatus(statusExecution{
		ProjectDir:       dir,
		RequestedSession: "review",
		ExplicitSession:  true,
		Profile:          "review",
		JSON:             true,
		Probe:            statusProbe(nil, nil, time.Now()),
		Out:              &buf,
	})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, buf.String())
	if env.Kind != "status" {
		t.Errorf("envelope kind = %q, want status", env.Kind)
	}
	rows := env.Data.Records
	if len(rows) != 1 || rows[0].Role != "cto" {
		t.Fatalf("status under --profile review should yield exactly cto; got %+v", rows)
	}
}

func TestTeamInitProfileWritesToTeamsDir(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	_, _, err := captureOutput(t, func() error {
		return runTeamInit([]string{"--profile", "review", "--roles", "cto", "--binary", "cto=codex", "--session", "review", "--trust", "sandboxed"})
	})
	if err != nil {
		t.Fatalf("team init --profile: %v", err)
	}
	wantPath := filepath.Join(dir, ".amq-squad", "teams", "review.json")
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("named-profile team.json not at %s: %v", wantPath, err)
	}
	// Default profile must NOT have been written.
	if _, err := os.Stat(filepath.Join(dir, ".amq-squad", "team.json")); err == nil {
		t.Errorf("named --profile init unexpectedly created default team.json")
	}
}

func TestTeamInitProjectTargetsOtherDir(t *testing.T) {
	parent := t.TempDir()
	project := filepath.Join(parent, "project")
	member := filepath.Join(parent, "member")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(member, 0o755); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	chdir(t, other)

	_, _, err := captureOutput(t, func() error {
		return runTeamInit([]string{
			"--project", project,
			"--roles", "qa",
			"--cwd", "qa=../member",
			"--session", "remote",
		})
	})
	if err != nil {
		t.Fatalf("team init --project: %v", err)
	}
	cfg, err := team.Read(project)
	if err != nil {
		t.Fatalf("read requested project team: %v", err)
	}
	gotCWD := ""
	if len(cfg.Members) == 1 {
		gotCWD, err = filepath.EvalSymlinks(cfg.Members[0].CWD)
		if err != nil {
			t.Fatalf("resolve member cwd: %v", err)
		}
	}
	wantCWD, err := filepath.EvalSymlinks(member)
	if err != nil {
		t.Fatalf("resolve wanted member cwd: %v", err)
	}
	if len(cfg.Members) != 1 || gotCWD != wantCWD {
		t.Fatalf("relative --cwd should resolve from --project dir; members = %+v", cfg.Members)
	}
	if _, err := os.Stat(filepath.Join(other, ".amq-squad", "team.json")); err == nil {
		t.Errorf("team init --project wrote team.json in current cwd")
	}
}

func TestTeamInitExistingProfileRefusedWithoutForce(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	seedProfile(t, dir, "review", team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "review"}},
	})
	_, _, err := captureOutput(t, func() error {
		return runTeamInit([]string{"--profile", "review", "--roles", "cto", "--binary", "cto=codex", "--session", "review"})
	})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("existing profile init without --force should fail, got %v", err)
	}
}

func TestBootstrapCurrentTeamReadsLaunchProfile(t *testing.T) {
	dir := t.TempDir()
	seedProfile(t, dir, "review", team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "review"},
		},
	})
	seedProfile(t, dir, team.DefaultProfile, team.Team{
		Members: []team.Member{
			{Role: "stranger", Binary: "claude", Handle: "stranger", Session: "main"},
		},
	})
	rec := launch.Record{
		Role: "cto", Handle: "cto", Binary: "codex", Session: "review",
		CWD: dir, TeamProfile: "review",
	}
	members, warnings := bootstrapCurrentTeam(rec, dir)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(members) != 1 || members[0].Role != "cto" {
		t.Errorf("bootstrap routing should come from named profile, got %+v", members)
	}
}

func TestHistoryHasNoProfileFlag(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	setupFakeAMQSessionRoots(t)
	_, _, err := captureOutput(t, func() error {
		return runHistory([]string{"--profile", "review"})
	})
	if err == nil {
		t.Fatal("history should not accept --profile")
	}
	if !strings.Contains(err.Error(), "profile") && !strings.Contains(err.Error(), "not defined") {
		t.Errorf("history --profile error should reference the unknown flag, got: %v", err)
	}
}

// Regression: an explicit --profile naming a missing profile must fail
// loudly. Without this guard, sync silently wrote the managed block into
// the team-home cwd even though the user selected another profile.
func TestTeamSyncExplicitMissingProfileErrors(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	if err := os.MkdirAll(filepath.Join(dir, ".amq-squad"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".amq-squad", "team-rules.md"), []byte("# Team Rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Default profile present.
	seedProfile(t, dir, team.DefaultProfile, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "main"},
		},
	})

	_, _, err := captureOutput(t, func() error {
		return runTeamSync([]string{"--apply", "--profile", "missing"})
	})
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("team sync --profile missing should error, got %v", err)
	}
	// Importantly: nothing should have been written to the team-home cwd
	// because the explicit --profile selection was wrong.
	if _, statErr := os.Stat(filepath.Join(dir, "CLAUDE.md")); statErr == nil {
		t.Error("team sync --profile missing wrote CLAUDE.md to team-home despite failure")
	}
}

// Regression for P1 (1): a named profile whose members do not live in
// team-home must NOT cause team sync to touch the team-home cwd. The
// locked Step 9A semantics say --profile NAME walks that profile's
// member cwds exactly.
func TestTeamSyncProfileLeavesTeamHomeAloneWhenNoMemberThere(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	if err := os.MkdirAll(filepath.Join(dir, ".amq-squad"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".amq-squad", "team-rules.md"), []byte("# Team Rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	memberCWD := t.TempDir()
	seedProfile(t, dir, "review", team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "review", CWD: memberCWD},
		},
	})

	if _, _, err := captureOutput(t, func() error {
		return runTeamSync([]string{"--apply", "--allow-outside", "--profile", "review"})
	}); err != nil {
		t.Fatalf("team sync --profile review --apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(memberCWD, "CLAUDE.md")); err != nil {
		t.Errorf("member cwd should have CLAUDE.md after profile sync: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); err == nil {
		t.Errorf("team-home should NOT be touched when no profile member lives there")
	}
}

// Regression for P1 (2): top-level resume/fork footer must carry the
// selected profile so the suggested command does not silently fall back
// to the default profile when run.
func TestResumeFooterCarriesProfile(t *testing.T) {
	dir := t.TempDir()
	base := setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)
	seedProfile(t, dir, "review", team.Team{
		Workstream: "review",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "review"},
		},
	})
	// No restorable record for the review profile -> all-fresh plan ->
	// footer should be emitted, and must reference --profile review.
	_ = base
	stdout, _, err := captureOutput(t, func() error {
		return runResume([]string{"--profile", "review"})
	})
	if err != nil {
		t.Fatalf("resume --profile review: %v", err)
	}
	if !strings.Contains(stdout, "up --session review --profile review") {
		t.Errorf("resume footer should carry --profile review:\n%s", stdout)
	}
}

func TestResumeNamedProfileBlocksLegacySessionRootConflict(t *testing.T) {
	dir := t.TempDir()
	setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)
	seedProfile(t, dir, "review", team.Team{
		Workstream: "main",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "main"},
		},
	})
	legacyAgentDir := filepath.Join(dir, ".agent-mail", "main", "agents", "cto")
	if err := os.MkdirAll(legacyAgentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyAgentDir, "inbox"), []byte("legacy durable state\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := captureOutput(t, func() error {
		return runResume([]string{"--profile", "review", "--session", "main"})
	})
	if err == nil || !strings.Contains(err.Error(), "legacy/default session root") {
		t.Fatalf("resume should fail closed on legacy conflict, got %v", err)
	}

	stdout, _, err := captureOutput(t, func() error {
		return runResume([]string{"--profile", "review", "--session", "main", "--json"})
	})
	if err != nil {
		t.Fatalf("resume --json should expose conflict without mutating: %v", err)
	}
	env := decodeJSONEnvelope[resumeEnvelopeData](t, stdout)
	if env.Data.NamespaceConflict == nil || env.Data.NamespaceConflict.Kind != "legacy_session_root" {
		t.Fatalf("namespace conflict missing: %+v", env.Data.NamespaceConflict)
	}
	if len(env.Data.Plan) != 1 || env.Data.Plan[0].Action != string(resumeBlocked) || env.Data.Plan[0].Command != "" {
		t.Fatalf("resume plan should be blocked without command: %+v", env.Data.Plan)
	}
}

func TestNamedProfileConflictBlocksDirectRuntimeCommands(t *testing.T) {
	dir := t.TempDir()
	setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)
	seedProfile(t, dir, "review", team.Team{
		Workstream:   "main",
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "main"},
		},
	})
	legacyAgentDir := filepath.Join(dir, ".agent-mail", "main", "agents", "cto")
	if err := os.MkdirAll(legacyAgentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyAgentDir, "inbox"), []byte("legacy durable state\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		run  func() error
	}{
		{"focus", func() error { return runFocus([]string{"--profile", "review", "--session", "main", "--role", "cto"}) }},
		{"send", func() error {
			return runSend([]string{"--profile", "review", "--session", "main", "--role", "cto", "--body", "hello"})
		}},
		{"dispatch", func() error {
			return runDispatch([]string{"--profile", "review", "--session", "main", "--role", "cto", "--subject", "X", "--body", "y"})
		}},
		{"goal deliver", func() error {
			return runGoal([]string{"deliver", "--profile", "review", "--session", "main", "--role", "cto", "--goal", "ship"})
		}},
		{"stop", func() error { return runStop([]string{"--profile", "review", "--session", "main", "--all"}) }},
		{"up", func() error {
			return runUp([]string{"--profile", "review", "--session", "main", "--terminal", "fake", "--no-bootstrap"})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := captureOutput(t, tc.run)
			if err == nil || !strings.Contains(err.Error(), "legacy/default session root") {
				t.Fatalf("%s should fail closed on namespace conflict, got %v", tc.name, err)
			}
		})
	}
}

func TestUnprofiledDispatchRefusesBeforeWritingLegacyRootWhenNamedProfileOwnsSession(t *testing.T) {
	dir := t.TempDir()
	resumeChdir(t, dir)
	seedProfile(t, dir, team.DefaultProfile, team.Team{
		Workstream:   "main",
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "main"},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "main"},
		},
	})
	seedProfile(t, dir, "release", team.Team{
		Workstream:   "main",
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "main"},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "main"},
		},
	})
	namedRoot := filepath.Join(dir, ".agent-mail", "release", "main")
	if err := os.MkdirAll(filepath.Join(namedRoot, "agents", "qa"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(namedRoot, "agents", "qa", "inbox.md"), []byte("named durable state\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	legacyRoot := filepath.Join(dir, ".agent-mail", "main")

	_, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "main", "--role", "qa", "--subject", "X", "--body", "y"})
	})
	if err == nil {
		t.Fatal("unprofiled dispatch should refuse before writing legacy root")
	}
	for _, want := range []string{"default-profile", "release", "--profile release", "--profile default", "refusing before write"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("dispatch error missing %q:\n%v", want, err)
		}
	}
	if _, statErr := os.Stat(legacyRoot); !os.IsNotExist(statErr) {
		t.Fatalf("refused unprofiled dispatch must not create legacy root %s; stat err = %v", legacyRoot, statErr)
	}
}

func TestExplicitDefaultDispatchEscapesNamedProfileShadow(t *testing.T) {
	dir := t.TempDir()
	resumeChdir(t, dir)
	seedProfile(t, dir, team.DefaultProfile, team.Team{
		Workstream:   "main",
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "main"},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "main"},
		},
	})
	seedProfile(t, dir, "release", team.Team{
		Workstream: "main",
		Members: []team.Member{
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "main"},
		},
	})
	namedRoot := filepath.Join(dir, ".agent-mail", "release", "main")
	if err := os.MkdirAll(filepath.Join(namedRoot, "agents", "qa"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(namedRoot, "agents", "qa", "inbox.md"), []byte("named durable state\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-default to qa\n")

	stdout, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--profile", "default", "--session", "main", "--role", "qa", "--subject", "X", "--body", "y", "--no-wake", "--json"})
	})
	if err != nil {
		t.Fatalf("explicit default dispatch should proceed: %v\n%s", err, stdout)
	}
	env := decodeJSONEnvelope[mutationResult](t, stdout)
	if env.Data.MessageID != "msg-default" {
		t.Fatalf("dispatch result = %+v, want msg-default", env.Data)
	}
	if len(*calls) != 1 {
		t.Fatalf("amq calls = %d, want 1", len(*calls))
	}
	gotArgs := strings.Join((*calls)[0].Arg, " ")
	if want := "--root " + filepath.Join(resolveDir(dir), ".agent-mail", "main"); !strings.Contains(gotArgs, want) {
		t.Fatalf("explicit default dispatch should target legacy root %q, got args: %s", want, gotArgs)
	}
}

func TestNamedProfileDispatchConflictIncludesConcreteRecoveryCommands(t *testing.T) {
	dir := t.TempDir()
	resumeChdir(t, dir)
	seedProfile(t, dir, "release", team.Team{
		Workstream:   "main",
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "main"},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "main"},
		},
	})
	legacyRoot := filepath.Join(dir, ".agent-mail", "main")
	if err := os.MkdirAll(filepath.Join(legacyRoot, "agents", "qa"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyRoot, "agents", "qa", "inbox.md"), []byte("legacy durable state\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--profile", "release", "--session", "main", "--role", "qa", "--subject", "X", "--body", "y"})
	})
	if err == nil {
		t.Fatal("named-profile dispatch should refuse legacy root conflict")
	}
	for _, want := range []string{
		"legacy/default session root",
		"--override-namespace-conflict --reason <why>",
		coldNamespaceMigrationIssueURL,
		"amq-squad archive main --project " + shellQuote(resolveDir(dir)) + " --profile default",
		"amq-squad rm main --project " + shellQuote(resolveDir(dir)) + " --profile default",
		"amq send --root " + shellQuote(filepath.Join(resolveDir(dir), ".agent-mail", "main")),
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("dispatch error missing recovery %q:\n%v", want, err)
		}
	}
}

func TestRootHasDurableStateIgnoresEmptyMailboxSkeletonDebris(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".agent-mail", "review")
	for _, dir := range []string{
		filepath.Join(root, "agents", "cto", "inbox"),
		filepath.Join(root, "agents", "cto", "inbox", "new"),
		filepath.Join(root, "agents", "cto", "inbox", "cur"),
		filepath.Join(root, "agents", "cto", "inbox", "tmp"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if rootHasDurableState(root) {
		t.Fatalf("empty mailbox skeleton should not count as durable state")
	}
	if err := os.WriteFile(filepath.Join(root, "agents", "cto", "inbox", "new", "msg.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !rootHasDurableState(root) {
		t.Fatalf("message file in mailbox skeleton should count as durable state")
	}
}

func TestNamedProfileDispatchNamespaceOverrideQueuesAndAudits(t *testing.T) {
	dir := t.TempDir()
	resumeChdir(t, dir)
	t.Setenv("AM_ME", "operator-shell")
	seedProfile(t, dir, "release", team.Team{
		Project:      dir,
		Workstream:   "main",
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "main"},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "main"},
		},
	})
	legacyRoot := filepath.Join(dir, ".agent-mail", "main")
	if err := os.MkdirAll(filepath.Join(legacyRoot, "agents", "qa"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyRoot, "agents", "qa", "inbox.md"), []byte("legacy durable state\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-override to qa\n")

	stdout, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--profile", "release", "--session", "main", "--role", "qa", "--subject", "X", "--body", "y", "--no-wake", "--json", "--override-namespace-conflict", "--reason", "recover live collided run"})
	})
	if err != nil {
		t.Fatalf("dispatch override: %v\n%s", err, stdout)
	}
	env := decodeJSONEnvelope[mutationResult](t, stdout)
	if env.Data.MessageID != "msg-override" || env.Data.Root != filepath.Join(resolveDir(dir), ".agent-mail", "release", "main") {
		t.Fatalf("dispatch result = %+v", env.Data)
	}
	if len(*calls) != 1 {
		t.Fatalf("amq calls = %d, want 1", len(*calls))
	}
	audit, err := os.ReadFile(filepath.Join(dir, ".amq-squad", "namespace-audit", "main.jsonl"))
	if err != nil {
		t.Fatalf("read namespace audit: %v", err)
	}
	for _, want := range []string{`"operation":"dispatch"`, `"kind":"legacy_session_root"`, `"actor":"operator-shell"`, `"actor_env_set":true`, `"reason":"recover live collided run"`, coldNamespaceMigrationIssueURL} {
		if !strings.Contains(string(audit), want) {
			t.Fatalf("audit missing %q:\n%s", want, string(audit))
		}
	}
}

func TestNamespaceOverrideRequiresReasonBeforeDispatch(t *testing.T) {
	dir := t.TempDir()
	resumeChdir(t, dir)
	seedProfile(t, dir, "release", team.Team{
		Project:      dir,
		Workstream:   "main",
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "main"},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "main"},
		},
	})
	legacyRoot := filepath.Join(dir, ".agent-mail", "main")
	if err := os.MkdirAll(filepath.Join(legacyRoot, "agents", "qa"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyRoot, "agents", "qa", "inbox.md"), []byte("legacy durable state\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-override to qa\n")

	_, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--profile", "release", "--session", "main", "--role", "qa", "--subject", "X", "--body", "y", "--override-namespace-conflict"})
	})
	if err == nil || !strings.Contains(err.Error(), "dispatch --override-namespace-conflict requires --reason") {
		t.Fatalf("override without reason should fail closed, got %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("dispatch should not send AMQ before override reason is supplied, calls = %d", len(*calls))
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".amq-squad", "namespace-audit", "main.jsonl")); !os.IsNotExist(statErr) {
		t.Fatalf("missing-reason override should not write audit, stat err = %v", statErr)
	}
}

func TestGoalDeliverNamespaceOverrideDeliversAndAudits(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	dir := t.TempDir()
	resumeChdir(t, dir)
	t.Setenv("AM_ME", "")
	seedProfile(t, dir, "release", team.Team{
		Project:       dir,
		Workstream:    "main",
		Orchestrated:  true,
		Lead:          "cto",
		ExecutionMode: executionModeProjectLead,
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "main"},
		},
	})
	legacyRoot := filepath.Join(dir, ".agent-mail", "main")
	if err := os.MkdirAll(filepath.Join(legacyRoot, "agents", "cto"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyRoot, "agents", "cto", "inbox.md"), []byte("legacy durable state\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	namedRoot := filepath.Join(dir, ".agent-mail", "release", "main")
	agentDir := filepath.Join(namedRoot, "agents", "cto")
	if err := launch.Write(agentDir, launch.Record{
		CWD:         dir,
		Binary:      "codex",
		Handle:      "cto",
		Role:        "cto",
		Session:     "main",
		Root:        namedRoot,
		TeamProfile: "release",
		Tmux:        &launch.TmuxInfo{PaneID: "%9"},
	}); err != nil {
		t.Fatal(err)
	}
	oldLister := statusPaneLister
	statusPaneLister = func() ([]tmuxpane.TmuxPane, error) {
		return []tmuxpane.TmuxPane{{PaneID: "%9", CWD: dir, Command: "codex", Title: "amq:main:cto"}}, nil
	}
	oldSend := sendPromptToPane
	var sent []string
	sendPromptToPane = func(paneID, prompt string) error {
		sent = append(sent, paneID+"\x00"+prompt)
		return nil
	}
	t.Cleanup(func() {
		statusPaneLister = oldLister
		sendPromptToPane = oldSend
	})

	stdout, _, err := captureOutput(t, func() error {
		return runGoal([]string{"deliver", "--profile", "release", "--session", "main", "--role", "cto", "--goal", "ship", "--json", "--override-namespace-conflict", "--reason", "recover visible lead"})
	})
	if err != nil {
		t.Fatalf("goal deliver override: %v\n%s", err, stdout)
	}
	if len(sent) != 1 || !strings.Contains(sent[0], "/goal --goal") || !strings.Contains(sent[0], "--profile release") {
		t.Fatalf("goal deliver sent = %+v", sent)
	}
	env := decodeJSONEnvelope[mutationResult](t, stdout)
	if env.Kind != "goal_deliver" || env.Data.Status != "native_goal_delivered" {
		t.Fatalf("goal deliver envelope = %+v", env)
	}
	audit, err := os.ReadFile(filepath.Join(dir, ".amq-squad", "namespace-audit", "main.jsonl"))
	if err != nil {
		t.Fatalf("read namespace audit: %v", err)
	}
	for _, want := range []string{`"operation":"goal deliver"`, `"actor":""`, `"actor_env_set":false`, `"actor_source":"unset"`, `"reason":"recover visible lead"`} {
		if !strings.Contains(string(audit), want) {
			t.Fatalf("audit missing %q:\n%s", want, string(audit))
		}
	}
}

func TestUnprofiledDispatchRefusesMultipleNamedProfileOwners(t *testing.T) {
	dir := t.TempDir()
	resumeChdir(t, dir)
	seedProfile(t, dir, team.DefaultProfile, team.Team{
		Workstream:   "main",
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "main"},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "main"},
		},
	})
	for _, profile := range []string{"product", "release"} {
		seedProfile(t, dir, profile, team.Team{
			Workstream: "main",
			Members: []team.Member{
				{Role: "qa", Binary: "codex", Handle: "qa", Session: "main"},
			},
		})
		root := filepath.Join(dir, ".agent-mail", profile, "main")
		if err := os.MkdirAll(filepath.Join(root, "agents", "qa"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "agents", "qa", "inbox.md"), []byte("named durable state\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	_, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "main", "--role", "qa", "--subject", "X", "--body", "y"})
	})
	if err == nil {
		t.Fatal("unprofiled dispatch should refuse multiple named profile owners")
	}
	for _, want := range []string{"multiple named profiles", "product", "release", "--profile <name>", "--profile default"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("dispatch error missing %q:\n%v", want, err)
		}
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".agent-mail", "main")); !os.IsNotExist(statErr) {
		t.Fatalf("refused dispatch must not create default root; stat err = %v", statErr)
	}
}

func TestDefaultProfileShadowRecoveryDoesNotAdvertiseInertOverride(t *testing.T) {
	dir := t.TempDir()
	resumeChdir(t, dir)
	seedProfile(t, dir, team.DefaultProfile, team.Team{
		Workstream:   "main",
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "main"},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "main"},
		},
	})
	seedProfile(t, dir, "release", team.Team{
		Workstream: "main",
		Members: []team.Member{
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "main"},
		},
	})
	namedRoot := filepath.Join(dir, ".agent-mail", "release", "main")
	if err := os.MkdirAll(filepath.Join(namedRoot, "agents", "qa"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(namedRoot, "agents", "qa", "inbox.md"), []byte("named durable state\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "main", "--role", "qa", "--subject", "X", "--body", "y"})
	})
	if err == nil {
		t.Fatal("unprofiled dispatch should refuse when named profile owns session")
	}
	for _, want := range []string{
		"explicit default-profile escape (acknowledged, not audited)",
		"amq-squad dispatch --project " + shellQuote(resolveDir(dir)) + " --profile default --session main",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("dispatch error missing %q:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), "--profile default --session main --role <role> --subject <subject> --body <body> --override-namespace-conflict") {
		t.Fatalf("shadow recovery must not print inert override flags:\n%v", err)
	}
}

func TestSameProfileSessionUpStatusDispatchNoNamespaceConflict(t *testing.T) {
	backend := useFakeBackend(t)
	setupFakeAMQSessionRoots(t)
	dir := t.TempDir()
	resumeChdir(t, dir)
	seedProfile(t, dir, "review", team.Team{
		Project:      dir,
		Workstream:   "review",
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "review"},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "review"},
		},
	})

	_, _, err := captureOutput(t, func() error {
		return runUp([]string{"--profile", "review", "--session", "review", "--terminal", "fake", "--no-bootstrap", "--no-attach"})
	})
	if err != nil {
		t.Fatalf("up profile==session: %v", err)
	}
	if len(backend.launches) != 1 {
		t.Fatalf("backend.Launch calls = %d, want 1", len(backend.launches))
	}
	if got := backend.launches[0].Workstream; got != "review" {
		t.Fatalf("up workstream = %q, want review", got)
	}
	if got := backend.launches[0].Profile; got != "review" {
		t.Fatalf("up profile = %q, want review", got)
	}

	namedRoot := filepath.Join(dir, ".agent-mail", "review", "review")
	namedAgentDir := filepath.Join(namedRoot, "agents", "qa")
	if err := os.MkdirAll(namedAgentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(namedAgentDir, "inbox"), []byte("named durable state\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolvedNamedRoot, err := filepath.EvalSymlinks(namedRoot)
	if err != nil {
		t.Fatal(err)
	}

	stdout, _, err := captureOutput(t, func() error {
		return runStatus([]string{"--profile", "review", "--session", "review", "--json"})
	})
	if err != nil {
		t.Fatalf("status profile==session: %v\n%s", err, stdout)
	}
	statusEnv := decodeJSONEnvelope[statusEnvelopeData](t, stdout)
	if statusEnv.Data.NamespaceConflict != nil {
		t.Fatalf("status should not report namespace conflict: %+v", statusEnv.Data.NamespaceConflict)
	}

	calls := withAMQCommandSeams(t, amqEnv{Root: namedRoot, BaseRoot: filepath.Dir(namedRoot)}, "Sent msg-same to qa\n")
	stdout, _, err = captureOutput(t, func() error {
		return runDispatch([]string{"--profile", "review", "--session", "review", "--role", "qa", "--subject", "X", "--body", "y", "--no-wake", "--json"})
	})
	if err != nil {
		t.Fatalf("dispatch profile==session: %v\n%s", err, stdout)
	}
	dispatchEnv := decodeJSONEnvelope[mutationResult](t, stdout)
	if dispatchEnv.Data.Status != "queued" || dispatchEnv.Data.MessageID != "msg-same" {
		t.Fatalf("bad dispatch result: %+v", dispatchEnv.Data)
	}
	if len(*calls) != 1 {
		t.Fatalf("amq calls = %d, want 1", len(*calls))
	}
	gotArgs := strings.Join((*calls)[0].Arg, " ")
	for _, want := range []string{"send", "--root " + resolvedNamedRoot, "--me cto", "--to qa"} {
		if !strings.Contains(gotArgs, want) {
			t.Fatalf("dispatch args missing %q: %s", want, gotArgs)
		}
	}
}

func TestForkFooterCarriesProfile(t *testing.T) {
	dir := t.TempDir()
	base := setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)
	seedProfile(t, dir, "review", team.Team{
		Workstream: "review",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: ""},
		},
	})
	// Seed a SOURCE root on disk so fork's source-state check passes.
	if err := os.MkdirAll(filepath.Join(base, "review", "agents", "cto"), 0o755); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := captureOutput(t, func() error {
		return runFork([]string{"--profile", "review", "--from", "review", "--as", "review-x"})
	})
	if err != nil {
		t.Fatalf("fork --profile review: %v", err)
	}
	if !strings.Contains(stdout, "up --fresh --session review-x") || !strings.Contains(stdout, "--profile review") {
		t.Errorf("fork footer should carry --fresh --session TARGET --profile NAME:\n%s", stdout)
	}
}

// Regression for P2 (3): emitCommand (restore) round-trips
// launch.Record.TeamProfile through a --team-profile flag so a restored
// agent retains its profile identity.
func TestEmitCommandRoundTripsTeamProfile(t *testing.T) {
	rec := launch.Record{
		CWD: "/repo", Binary: "codex", Session: "review", Handle: "cto",
		Role: "cto", Root: "/repo/.agent-mail/review", BaseRoot: "/repo/.agent-mail",
		TeamProfile: "review",
	}
	got := emitCommand(rec)
	if !strings.Contains(got, "--team-profile review") {
		t.Errorf("emitCommand should round-trip --team-profile, got:\n%s", got)
	}
}

func TestEmitCommandDefaultProfileNotEmitted(t *testing.T) {
	rec := launch.Record{
		CWD: "/repo", Binary: "codex", Session: "main", Handle: "cto",
		Role: "cto", Root: "/repo/.agent-mail/main", BaseRoot: "/repo/.agent-mail",
		TeamProfile: "",
	}
	got := emitCommand(rec)
	if strings.Contains(got, "--team-profile") {
		t.Errorf("default profile must not emit --team-profile:\n%s", got)
	}
}

func TestTeamSyncProfileScopedToSelectedCWDs(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	// Seed team-rules.md so sync has a body to write.
	if err := os.MkdirAll(filepath.Join(dir, ".amq-squad"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".amq-squad", "team-rules.md"), []byte("# Team Rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Two profiles, distinct member cwds.
	reviewCWD := t.TempDir()
	defaultCWD := t.TempDir()
	seedProfile(t, dir, "review", team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "review", CWD: reviewCWD},
		},
	})
	seedProfile(t, dir, team.DefaultProfile, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "main", CWD: defaultCWD},
		},
	})

	_, _, err := captureOutput(t, func() error {
		return runTeamSync([]string{"--apply", "--allow-outside", "--profile", "review"})
	})
	if err != nil {
		t.Fatalf("team sync --profile review --apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(reviewCWD, "CLAUDE.md")); err != nil {
		t.Errorf("review profile cwd should have CLAUDE.md after sync: %v", err)
	}
	if _, err := os.Stat(filepath.Join(defaultCWD, "CLAUDE.md")); err == nil {
		t.Errorf("default profile cwd should NOT be touched by --profile review sync")
	}
}

func TestTeamSyncProjectWritesRequestedTeamHome(t *testing.T) {
	project := t.TempDir()
	other := t.TempDir()
	chdir(t, other)
	if err := os.MkdirAll(filepath.Join(project, ".amq-squad"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".amq-squad", "team-rules.md"), []byte("# Team Rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedProfile(t, project, team.DefaultProfile, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "remote"},
		},
	})

	_, _, err := captureOutput(t, func() error {
		return runTeamSync([]string{"--project", project, "--apply"})
	})
	if err != nil {
		t.Fatalf("team sync --project --apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, "CLAUDE.md")); err != nil {
		t.Errorf("requested project should receive CLAUDE.md: %v", err)
	}
	if _, err := os.Stat(filepath.Join(other, "CLAUDE.md")); err == nil {
		t.Errorf("current cwd should not be touched by team sync --project")
	}
}

func TestTeamRulesInitProjectWritesRequestedTeamHome(t *testing.T) {
	project := t.TempDir()
	other := t.TempDir()
	chdir(t, other)

	_, _, err := captureOutput(t, func() error {
		return runTeam([]string{"rules", "init", "--project", project})
	})
	if err != nil {
		t.Fatalf("team rules init --project: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, ".amq-squad", "team-rules.md")); err != nil {
		t.Errorf("requested project should receive team-rules.md: %v", err)
	}
	if _, err := os.Stat(filepath.Join(other, ".amq-squad", "team-rules.md")); err == nil {
		t.Errorf("current cwd should not be touched by team rules init --project")
	}
}
