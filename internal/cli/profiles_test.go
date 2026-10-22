package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/team"
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
		return runTeamInit([]string{"--profile", "review", "--roles", "cto", "--binary", "cto=codex", "--session", "review-main", "--trust", "sandboxed"})
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
		return runTeamInit([]string{"--profile", "review", "--roles", "cto", "--binary", "cto=codex", "--session", "review-main"})
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

// TestResumeFooterCarriesProfile is deleted (gh#758/t11): its whole premise
// -- resume's plan-only output carries a resume-specific footer suggesting a
// profile-aware follow-up command -- no longer holds. The plan-only path now
// renders plan's own generic PrepareResultV1 output verbatim (no per-command
// follow-up suggestion at all, matching plan.go, which has never had one),
// per the approved "thin alias to plan" design (task/t11).

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
		"amq-squad goal --project " + shellQuote(resolveDir(dir)) + " --profile release --session main --goal <goal> --override-namespace-conflict --reason <why>",
		"amq send --root " + shellQuote(filepath.Join(resolveDir(dir), ".agent-mail", "release", "main")),
		"amq-squad down --project " + shellQuote(resolveDir(dir)) + " --profile default --session main --all",
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
		"explicit default-profile AMQ send",
		"amq send --root " + shellQuote(filepath.Join(resolveDir(dir), ".agent-mail", "main")),
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("dispatch error missing %q:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), "--profile default --session main --role <role> --subject <subject> --body <body> --override-namespace-conflict") {
		t.Fatalf("shadow recovery must not print inert override flags:\n%v", err)
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
