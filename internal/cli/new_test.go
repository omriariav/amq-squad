package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/catalog"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestRunNewRequiresSubcommand(t *testing.T) {
	_, stderr, err := captureOutput(t, func() error {
		return runNew(nil)
	})
	if err == nil {
		t.Fatal("new without a subcommand should fail")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
	if !strings.Contains(stderr, "amq-squad new") {
		t.Errorf("missing new help on stderr:\n%s", stderr)
	}
}

func TestRunNewTeamDelegatesToTeamInit(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	_, stderr, err := captureOutput(t, func() error {
		return runNew([]string{
			"team",
			"--roles", "cto,fullstack",
			"--binary", "cto=codex",
			"--session", "issue-96",
		})
	})
	if err != nil {
		t.Fatalf("new team: %v\nstderr:\n%s", err, stderr)
	}

	cfg, err := team.Read(dir)
	if err != nil {
		t.Fatalf("read team config: %v", err)
	}
	if len(cfg.Members) != 2 {
		t.Fatalf("members = %d, want 2", len(cfg.Members))
	}
	for _, m := range cfg.Members {
		if m.Session != "issue-96" {
			t.Errorf("member %s session = %q, want issue-96", m.Role, m.Session)
		}
	}
	if _, err := os.Stat(team.ProfilePath(dir, team.DefaultProfile)); err != nil {
		t.Fatalf("team profile not written: %v", err)
	}
}

func TestRunNewTeamProjectWritesTargetDir(t *testing.T) {
	project := t.TempDir()
	other := t.TempDir()
	chdir(t, other)

	_, stderr, err := captureOutput(t, func() error {
		return runNew([]string{
			"team",
			"--project", project,
			"--roles", "cto",
			"--binary", "cto=codex",
			"--session", "issue-98",
		})
	})
	if err != nil {
		t.Fatalf("new team --project: %v\nstderr:\n%s", err, stderr)
	}

	if _, err := team.Read(project); err != nil {
		t.Fatalf("target project team config not written: %v", err)
	}
	if _, err := os.Stat(team.ProfilePath(other, team.DefaultProfile)); !os.IsNotExist(err) {
		t.Fatalf("new team --project should not write in caller cwd; stat err = %v", err)
	}
}

func TestRunNewTeamSyncWritesPointerStubs(t *testing.T) {
	project := t.TempDir()
	other := t.TempDir()
	chdir(t, other)

	_, stderr, err := captureOutput(t, func() error {
		return runNew([]string{
			"team",
			"--project", project,
			"--sync",
			"--roles", "cto",
			"--binary", "cto=codex",
			"--session", "issue-99",
		})
	})
	if err != nil {
		t.Fatalf("new team --sync --project: %v\nstderr:\n%s", err, stderr)
	}

	for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
		body, err := os.ReadFile(filepath.Join(project, name))
		if err != nil {
			t.Fatalf("%s should be written in target project: %v", name, err)
		}
		for _, want := range []string{
			"amq-squad:managed:begin",
			"Team norms",
			".amq-squad/team-rules.md",
		} {
			if !strings.Contains(string(body), want) {
				t.Fatalf("%s missing %q in:\n%s", name, want, body)
			}
		}
		if _, err := os.Stat(filepath.Join(other, name)); !os.IsNotExist(err) {
			t.Fatalf("new team --sync --project should not write %s in caller cwd; stat err = %v", name, err)
		}
	}
}

func TestRunNewTeamDryRunSyncPrintsPlanWithoutWriting(t *testing.T) {
	project := t.TempDir()
	other := t.TempDir()
	chdir(t, other)

	stdout, stderr, err := captureOutput(t, func() error {
		return runNew([]string{
			"team",
			"--project", project,
			"--sync",
			"--dry-run",
			"--roles", "cto,qa",
			"--session", "issue-99",
		})
	})
	if err != nil {
		t.Fatalf("new team --sync --dry-run --project: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		"# amq-squad team init --dry-run",
		"# writes: none",
		"cto",
		"qa",
		"# sync preview",
		"amq-squad team sync --apply --project",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, stdout)
		}
	}
	if _, err := team.Read(project); !os.IsNotExist(err) {
		t.Fatalf("new team --dry-run should not write target team config; err = %v", err)
	}
	for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
		if _, err := os.Stat(filepath.Join(project, name)); !os.IsNotExist(err) {
			t.Fatalf("new team --sync --dry-run should not write %s; stat err = %v", name, err)
		}
	}
	if _, err := os.Stat(team.ProfilePath(other, team.DefaultProfile)); !os.IsNotExist(err) {
		t.Fatalf("new team --dry-run should not write in caller cwd; stat err = %v", err)
	}
}

func TestRunNewTeamDryRunSyncJSONIncludesSyncCommandWithoutWriting(t *testing.T) {
	project := t.TempDir()
	other := t.TempDir()
	chdir(t, other)
	wantProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := captureOutput(t, func() error {
		return runNew([]string{
			"team",
			"--project", project,
			"--sync",
			"--dry-run",
			"--json",
			"--roles", "cto,qa",
			"--session", "issue-99",
		})
	})
	if err != nil {
		t.Fatalf("new team --sync --dry-run --json --project: %v\nstderr:\n%s", err, stderr)
	}
	env := decodeJSONEnvelope[teamProfilePlan](t, stdout)
	if env.Kind != "team_profile_plan" {
		t.Errorf("kind = %q, want team_profile_plan", env.Kind)
	}
	if env.Data.TeamHome != wantProject {
		t.Errorf("team_home = %q, want %q", env.Data.TeamHome, wantProject)
	}
	if !strings.Contains(env.Data.SyncCommand, "amq-squad team sync --apply --project") {
		t.Fatalf("sync_command missing project-scoped sync: %q", env.Data.SyncCommand)
	}
	if strings.Contains(stdout, "# sync preview") {
		t.Fatalf("JSON output leaked human sync preview:\n%s", stdout)
	}
	if env.Data.Members != 2 || len(env.Data.Plan) != 2 {
		t.Fatalf("members/plan = %d/%d, want 2/2", env.Data.Members, len(env.Data.Plan))
	}
	if _, err := team.Read(project); !os.IsNotExist(err) {
		t.Fatalf("new team --dry-run --json should not write target team config; err = %v", err)
	}
	for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
		if _, err := os.Stat(filepath.Join(project, name)); !os.IsNotExist(err) {
			t.Fatalf("new team --sync --dry-run --json should not write %s; stat err = %v", name, err)
		}
	}
	if _, err := os.Stat(team.ProfilePath(other, team.DefaultProfile)); !os.IsNotExist(err) {
		t.Fatalf("new team --dry-run --json should not write in caller cwd; stat err = %v", err)
	}
}

func TestRunNewTeamSyncNamedProfilePassesProfileToSync(t *testing.T) {
	project := t.TempDir()
	memberDir := t.TempDir()
	chdir(t, project)

	_, stderr, err := captureOutput(t, func() error {
		return runNew([]string{
			"team",
			"--profile", "review",
			"--sync",
			"--allow-outside",
			"--roles", "cto",
			"--cwd", "cto=" + memberDir,
			"--session", "review-main",
		})
	})
	if err != nil {
		t.Fatalf("new team --profile review --sync: %v\nstderr:\n%s", err, stderr)
	}
	if _, err := os.Stat(filepath.Join(memberDir, "AGENTS.md")); err != nil {
		t.Fatalf("named profile sync should write member cwd AGENTS.md: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, "AGENTS.md")); err == nil {
		t.Fatal("named profile sync should be scoped to member cwd, not team-home")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat team-home AGENTS.md: %v", err)
	}
}

func TestRunNewProfileDelegatesToNamedTeamInit(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	_, stderr, err := captureOutput(t, func() error {
		return runNew([]string{
			"profile", "review",
			"--roles", "cto,qa",
			"--binary", "qa=codex",
			"--session", "review-main",
		})
	})
	if err != nil {
		t.Fatalf("new profile review: %v\nstderr:\n%s", err, stderr)
	}

	cfg, err := team.ReadProfile(dir, "review")
	if err != nil {
		t.Fatalf("read named team config: %v", err)
	}
	if len(cfg.Members) != 2 {
		t.Fatalf("members = %d, want 2", len(cfg.Members))
	}
	if cfg.Members[1].Role != "qa" || cfg.Members[1].Binary != "codex" {
		t.Fatalf("qa binary override not persisted: %+v", cfg.Members)
	}
	if _, err := os.Stat(team.ProfilePath(dir, team.DefaultProfile)); !os.IsNotExist(err) {
		t.Fatalf("new profile should not write the default profile; stat err = %v", err)
	}
}

func TestRunNewProfileForwardsEffortOverride(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	_, stderr, err := captureOutput(t, func() error {
		return runNew([]string{
			"profile", "review",
			"--roles", "qa",
			"--binary", "qa=claude",
			"--effort", "qa=max",
			"--session", "review-main",
		})
	})
	if err != nil {
		t.Fatalf("new profile review --effort: %v\nstderr:\n%s", err, stderr)
	}

	cfg, err := team.ReadProfile(dir, "review")
	if err != nil {
		t.Fatalf("read named team config: %v", err)
	}
	if len(cfg.Members) != 1 {
		t.Fatalf("members = %d, want 1", len(cfg.Members))
	}
	if got := strings.Join(cfg.Members[0].ClaudeArgs, " "); got != "--effort max" {
		t.Fatalf("qa claude_args = %q, want %q", got, "--effort max")
	}
}

func TestRunNewTeamRejectsProfileSessionCollisionBeforeWrite(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	_, _, err := captureOutput(t, func() error {
		return runNew([]string{"team", "--profile", "review", "--roles", "cto", "--session", "review"})
	})
	if err == nil ||
		!strings.Contains(err.Error(), "team init refused") ||
		!strings.Contains(err.Error(), "colliding AMQ roots") ||
		!strings.Contains(err.Error(), "--profile codex-review --session review") {
		t.Fatalf("expected profile/session collision error, got %v", err)
	}
	if _, statErr := os.Stat(team.ProfilePath(dir, "review")); !os.IsNotExist(statErr) {
		t.Fatalf("refused new team must not write named profile; stat err = %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".agent-mail")); !os.IsNotExist(statErr) {
		t.Fatalf("refused new team must not write .agent-mail; stat err = %v", statErr)
	}
}

func TestRunNewProfileRejectsProfileSessionCollisionBeforeWrite(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	_, _, err := captureOutput(t, func() error {
		return runNew([]string{"profile", "review", "--roles", "cto", "--session", "review"})
	})
	if err == nil ||
		!strings.Contains(err.Error(), "team init refused") ||
		!strings.Contains(err.Error(), "colliding AMQ roots") ||
		!strings.Contains(err.Error(), "--profile codex-review --session review") {
		t.Fatalf("expected profile/session collision error, got %v", err)
	}
	if _, statErr := os.Stat(team.ProfilePath(dir, "review")); !os.IsNotExist(statErr) {
		t.Fatalf("refused new profile must not write named profile; stat err = %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".agent-mail")); !os.IsNotExist(statErr) {
		t.Fatalf("refused new profile must not write .agent-mail; stat err = %v", statErr)
	}
}

func TestRunNewProfileWarnsWhenSharedRulesDescribeDifferentRoster(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	// First team writes team-rules.md describing the cto roster.
	if _, stderr, err := captureOutput(t, func() error {
		return runNew([]string{"team", "--roles", "cto", "--session", "issue-96"})
	}); err != nil {
		t.Fatalf("new team: %v\nstderr:\n%s", err, stderr)
	}

	// A second profile with a DIFFERENT roster reuses the shared (no-clobber)
	// team-rules.md, whose Role Scope still names cto, not pm -> warn (#155).
	_, stderr, err := captureOutput(t, func() error {
		return runNew([]string{"profile", "review", "--roles", "pm", "--session", "review-main"})
	})
	if err != nil {
		t.Fatalf("new profile review: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{"left unchanged", "does not describe this profile's members", "bootstrap"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stale-rules warning missing %q in stderr:\n%s", want, stderr)
		}
	}
}

func TestRunNewProfileNoWarnWhenSharedRulesMatchRoster(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	if _, stderr, err := captureOutput(t, func() error {
		return runNew([]string{"team", "--roles", "cto", "--session", "issue-96"})
	}); err != nil {
		t.Fatalf("new team: %v\nstderr:\n%s", err, stderr)
	}

	// A second profile with the SAME roster (cto) reuses the shared file, which
	// already describes cto -> no roster-drift warning.
	_, stderr, err := captureOutput(t, func() error {
		return runNew([]string{"profile", "review", "--roles", "cto", "--session", "review-main"})
	})
	if err != nil {
		t.Fatalf("new profile review: %v\nstderr:\n%s", err, stderr)
	}
	if strings.Contains(stderr, "left unchanged") {
		t.Fatalf("matching roster must not warn; stderr:\n%s", stderr)
	}
}

func TestRunNewProfileProjectBeforeNameDryRunJSON(t *testing.T) {
	project := t.TempDir()
	other := t.TempDir()
	chdir(t, other)
	wantProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := captureOutput(t, func() error {
		return runNew([]string{
			"profile",
			"--project", project,
			"--dry-run",
			"--json",
			"review",
			"--roles", "2,9",
			"--session", "review-main",
		})
	})
	if err != nil {
		t.Fatalf("new profile --project --dry-run --json: %v\nstderr:\n%s", err, stderr)
	}
	env := decodeJSONEnvelope[teamProfilePlan](t, stdout)
	if env.Data.Profile != "review" {
		t.Fatalf("profile = %q, want review", env.Data.Profile)
	}
	if env.Data.TeamHome != wantProject {
		t.Fatalf("team_home = %q, want %q", env.Data.TeamHome, wantProject)
	}
	if env.Data.Members != 2 {
		t.Fatalf("members = %d, want 2", env.Data.Members)
	}
	if _, err := os.Stat(team.ProfilePath(project, "review")); !os.IsNotExist(err) {
		t.Fatalf("new profile dry-run should not write named profile; stat err = %v", err)
	}
}

func TestRunNewProfileRejectsDefaultAndExplicitProfileFlag(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "default",
			args: []string{"profile", "default", "--roles", "cto"},
			want: "use 'new team'",
		},
		{
			name: "explicit profile flag",
			args: []string{"profile", "review", "--profile", "other", "--roles", "cto"},
			want: "do not pass --profile",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := captureOutput(t, func() error {
				return runNew(tc.args)
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("runNew(%v) error = %v, want %q", tc.args, err, tc.want)
			}
		})
	}
}

func TestRunNewTeamAllowOutsideRequiresSync(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return runNew([]string{"team", "--allow-outside", "--roles", "cto"})
	})
	if err == nil || !strings.Contains(err.Error(), "--allow-outside only applies with --sync") {
		t.Fatalf("new team --allow-outside error = %v, want sync guidance", err)
	}
}

func TestRunNewTeamRoleSelectionShortcuts(t *testing.T) {
	t.Run("market numbers", func(t *testing.T) {
		dir := t.TempDir()
		chdir(t, dir)

		_, stderr, err := captureOutput(t, func() error {
			return runNew([]string{"team", "--roles", "2,9", "--session", "issue-100"})
		})
		if err != nil {
			t.Fatalf("new team --roles 2,9: %v\nstderr:\n%s", err, stderr)
		}

		cfg, err := team.Read(dir)
		if err != nil {
			t.Fatalf("read team config: %v", err)
		}
		got := []string{}
		for _, m := range cfg.Members {
			got = append(got, m.Role)
		}
		want := []string{"cto", "qa"}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("roles = %v, want %v", got, want)
		}
	})

	t.Run("all", func(t *testing.T) {
		dir := t.TempDir()
		chdir(t, dir)

		_, stderr, err := captureOutput(t, func() error {
			return runNew([]string{"team", "--roles", "all", "--session", "issue-101"})
		})
		if err != nil {
			t.Fatalf("new team --roles all: %v\nstderr:\n%s", err, stderr)
		}

		cfg, err := team.Read(dir)
		if err != nil {
			t.Fatalf("read team config: %v", err)
		}
		if len(cfg.Members) != len(catalog.IDs()) {
			t.Fatalf("members = %d, want %d", len(cfg.Members), len(catalog.IDs()))
		}
	})
}

func TestRunNewSessionDelegatesToUpDryRun(t *testing.T) {
	seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto"},
			{Role: "fullstack", Binary: "claude", Handle: "fullstack"},
		},
	})

	stdout, stderr, err := captureOutput(t, func() error {
		return runNew([]string{"session", "--dry-run", "--no-bootstrap", "issue-97"})
	})
	if err != nil {
		t.Fatalf("new session --dry-run: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		"# workstream: issue-97",
		"agent up codex",
		"agent up claude",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("new session dry-run missing %q in:\n%s", want, stdout)
		}
	}
}

func TestRunNewSessionSeedFromDryRunPrintsCandidateBrief(t *testing.T) {
	swapSeedClock(t)
	dir := t.TempDir()
	chdir(t, dir)
	seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	})
	source := filepath.Join(dir, "brief.md")
	if err := os.WriteFile(source, []byte("# Issue 98\n\nSeeded body.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := captureOutput(t, func() error {
		return runNew([]string{"session", "--dry-run", "--seed-from", "file:" + source, "issue-98"})
	})
	if err != nil {
		t.Fatalf("new session --dry-run --seed-from: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		"source: file:" + source,
		"generated_at: 2026-05-17T12:00:00Z",
		"# Issue 98",
		"Seeded body.",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("seed dry-run output missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "agent up") {
		t.Fatalf("seed dry-run should print the candidate brief, not a launch plan:\n%s", stdout)
	}
	if _, err := os.Stat(briefPath(dir, "issue-98")); !os.IsNotExist(err) {
		t.Fatalf("seed dry-run should not write the brief; stat err = %v", err)
	}
}

func TestRunNewSessionProjectDelegatesFromOtherCWD(t *testing.T) {
	project := t.TempDir()
	other := t.TempDir()
	if err := team.Write(project, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	chdir(t, other)

	stdout, stderr, err := captureOutput(t, func() error {
		return runNew([]string{"session", "--project", project, "--dry-run", "--no-bootstrap", "issue-99"})
	})
	if err != nil {
		t.Fatalf("new session --project --dry-run: %v\nstderr:\n%s", err, stderr)
	}
	wantProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# team-home: " + wantProject,
		"# workstream: issue-99",
		"agent up codex",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("new session --project missing %q in:\n%s", want, stdout)
		}
	}
}

func TestRunNewProjectFlagRequiresExistingDirectory(t *testing.T) {
	missing := t.TempDir() + "/missing"
	_, _, err := captureOutput(t, func() error {
		return runNew([]string{"session", "--project", missing, "--dry-run"})
	})
	if err == nil || !strings.Contains(err.Error(), "--project") {
		t.Fatalf("new session --project missing dir error = %v, want --project error", err)
	}
}

func TestRunNewProjectFlagRequiresNonEmptyDirectory(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return runNew([]string{"session", "--project", "", "--dry-run"})
	})
	if err == nil || !strings.Contains(err.Error(), "--project requires a directory") {
		t.Fatalf("new session empty --project error = %v, want --project error", err)
	}
}

func TestRunDispatchesNew(t *testing.T) {
	stdout, stderr, err := captureOutput(t, func() error {
		return Run([]string{"new", "--help"}, "test")
	})
	if err != nil {
		t.Fatalf("Run new --help: %v", err)
	}
	if stdout != "" {
		t.Errorf("new --help should not print stdout, got:\n%s", stdout)
	}
	if !strings.Contains(stderr, "amq-squad new") {
		t.Errorf("new --help missing usage:\n%s", stderr)
	}
	if strings.Contains(stderr, "new team --project ~/Code/app --profile") {
		t.Errorf("new --help should steer named profiles to 'new profile', got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "amq-squad new profile review --project ~/Code/app --roles cto") {
		t.Errorf("new --help missing named-profile example:\n%s", stderr)
	}
}
