package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/rules"
	"github.com/omriariav/amq-squad/v2/internal/runtimecontrol"
	"github.com/omriariav/amq-squad/v2/internal/team"
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
)

func TestRunStartGoRequiresExplicitLaunchShapeForFreshRun(t *testing.T) {
	dir := t.TempDir()
	err := runRunStart([]string{
		"--project", dir, "--session", "fresh", "--roles", "cto",
		"--visibility", "detached", "--go",
	}, "test")
	if err == nil || !strings.Contains(err.Error(), "requires explicit --launch-shape") {
		t.Fatalf("omitted launch shape error = %v", err)
	}
	if team.Exists(dir) {
		t.Fatal("omitted launch shape created a fresh profile")
	}
}

func TestRunStartReadinessJSONIsPureAndUsesEmptyArrays(t *testing.T) {
	dir := t.TempDir()
	if err := team.WriteProfile(dir, team.DefaultProfile, team.Team{
		Project: dir, Orchestrated: true, Lead: "cto", ExecutionMode: executionModeProjectLead,
		Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: "missing", CWD: dir}},
	}); err != nil {
		t.Fatal(err)
	}
	out, errOut, err := captureOutput(t, func() error {
		return runRunStart([]string{"--project", dir, "--session", "missing", "--visibility", "detached", "--readiness-json"}, "test")
	})
	if err == nil || !strings.Contains(err.Error(), "artifact readiness failed") {
		t.Fatalf("missing readiness error = %v", err)
	}
	if strings.TrimSpace(errOut) != "" {
		t.Fatalf("readiness JSON wrote human stderr: %q", errOut)
	}
	trimmed := strings.TrimSpace(out)
	if !strings.HasPrefix(trimmed, "{") || strings.Contains(out, "orchestrated run") {
		t.Fatalf("readiness stdout is not pure JSON:\n%s", out)
	}
	for _, want := range []string{`"initial_roster": []`, `"staged_roster": []`, `"rows": [`} {
		if !strings.Contains(out, want) {
			t.Fatalf("readiness JSON missing non-null array %s:\n%s", want, out)
		}
	}
}

func TestRunStartReadinessJSONExposesAcceptedGoalBinding(t *testing.T) {
	dir := prepareRunStartFixture(t, runwizard.LaunchShapeWorkingTeamTogether)
	manifest, err := readPreparedRunManifest(dir, team.DefaultProfile, "prepared")
	if err != nil {
		t.Fatal(err)
	}
	out, errOut, err := captureOutput(t, func() error {
		return runRunStart([]string{"--project", dir, "--session", "prepared", "--visibility", "detached", "--readiness-json"}, "test")
	})
	if err != nil {
		t.Fatalf("readiness JSON: %v", err)
	}
	if strings.TrimSpace(errOut) != "" || strings.Contains(out, "orchestrated run") || !strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Fatalf("readiness output is not pure JSON: stderr=%q stdout=%s", errOut, out)
	}
	for _, want := range []string{`"goal_source": "` + manifest.GoalSource + `"`, `"goal_digest": "` + manifest.GoalDigest + `"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("readiness JSON missing %s:\n%s", want, out)
		}
	}
	if !strings.Contains(out, `"staged_roster": []`) {
		t.Fatalf("readiness JSON encoded an empty prepared roster as null:\n%s", out)
	}
}

func TestDirectLeadSessionPreparationUsesExactBinaryGoalBinding(t *testing.T) {
	for _, binary := range []string{"codex", "claude"} {
		t.Run(binary, func(t *testing.T) {
			dir := t.TempDir()
			profile := "direct"
			session := "prepared"
			tm := team.Team{
				Project: dir, Orchestrated: true, Lead: "cto", ExecutionMode: executionModeDirectLeadSession,
				Members: []team.Member{{Role: "cto", Handle: "cto", Binary: binary, Session: session, CWD: dir}},
			}
			if err := team.WriteProfile(dir, profile, tm); err != nil {
				t.Fatal(err)
			}
			_, _, err := captureOutput(t, func() error {
				return runRunStart([]string{
					"--project", dir, "--profile", profile, "--session", session,
					"--launch-shape", runwizard.LaunchShapeWorkingTeamTogether,
					"--goal", "Execute the declared direct lead goal", "--visibility", "detached", "--prepare",
				}, "test")
			})
			if err != nil {
				t.Fatalf("direct lead preparation: %v", err)
			}
			manifest, err := readPreparedRunManifest(dir, profile, session)
			if err != nil {
				t.Fatal(err)
			}
			wantMode := map[string]string{"codex": "prompt_goal", "claude": "native_goal"}[binary]
			wantLine := "Goal binding: " + wantMode
			if got := manifest.BootstrapBindings["cto"]; got != wantLine {
				t.Fatalf("manifest binding line = %q, want %q", got, wantLine)
			}
			binding := acceptedGoalBinding{Text: manifest.GoalText, Source: manifest.GoalSource, Namespace: manifest.GoalNamespace, Digest: manifest.GoalDigest}
			prompt, err := preparedBootstrap(dir, profile, session, binding, tm, tm.Members[0], acceptedRunContext{Version: "test", Topology: manifest.Topology})
			if err != nil {
				t.Fatal(err)
			}
			if !bootstrapHasExactLine(prompt, "- "+wantLine) || bootstrapHasExactLine(prompt, "- Goal binding: "+wantMode+"_missing") || bootstrapHasExactLine(prompt, "- Goal binding: amq_task_brief") {
				t.Fatalf("prepared direct lead bootstrap has wrong exact binding line:\n%s", prompt)
			}
			prepared, err := preparedGoalBinding(tm, profile, session, tm.Members[0], binding)
			if err != nil {
				t.Fatal(err)
			}
			if launchRecordHasGoalBinding(launch.Record{Binary: binary, GoalBinding: prepared}) {
				t.Fatal("accepted preparation intent must not become delivered launch evidence")
			}
		})
	}
}

func TestDirectLeadSessionPreparationFailureLeavesEveryArtifactUnchanged(t *testing.T) {
	dir := t.TempDir()
	profile := "direct"
	session := "prepared"
	tm := team.Team{
		Project: dir, Orchestrated: true, Lead: "cto", ExecutionMode: executionModeDirectLeadSession,
		Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: session, CWD: dir}},
	}
	if err := team.WriteProfile(dir, profile, tm); err != nil {
		t.Fatal(err)
	}
	profilePath := team.ProfilePath(dir, profile)
	profileBefore, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	sentinels := map[string]string{
		filepath.Join(dir, "AGENTS.md"): "operator agents sentinel\n",
		filepath.Join(dir, "CLAUDE.md"): "operator claude sentinel\n",
		filepath.Join(squadnamespace.AMQRoot(dir, profile, session), "agents", "cto", "extensions", "io.github.omriariav.amq-squad", "role.md"):      "operator role sentinel\n",
		filepath.Join(squadnamespace.AMQRoot(dir, profile, session), "agents", "cto", "extensions", "io.github.omriariav.amq-squad", "bootstrap.md"): "operator bootstrap sentinel\n",
	}
	for path, body := range sentinels {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	oldBuild := buildPreparedRunManifestForPreparation
	buildPreparedRunManifestForPreparation = func(string, string, string, string, string, acceptedGoalBinding, acceptedRunContext) (preparedRunManifest, error) {
		return preparedRunManifest{}, fmt.Errorf("injected post-write manifest failure")
	}
	t.Cleanup(func() { buildPreparedRunManifestForPreparation = oldBuild })

	_, _, err = captureOutput(t, func() error {
		return runRunStart([]string{
			"--project", dir, "--profile", profile, "--session", session,
			"--launch-shape", runwizard.LaunchShapeWorkingTeamTogether,
			"--goal", "Exercise transactional preparation", "--visibility", "detached", "--prepare",
		}, "test")
	})
	if err == nil || !strings.Contains(err.Error(), "injected post-write manifest failure") {
		t.Fatalf("preparation error = %v", err)
	}
	for _, path := range []string{briefPathForProfile(dir, profile, session), rules.Path(dir), preparedRunPath(dir, profile, session)} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("failed preparation left artifact %s: %v", path, statErr)
		}
	}
	for path, want := range sentinels {
		got, readErr := os.ReadFile(path)
		if readErr != nil || string(got) != want {
			t.Fatalf("failed preparation changed %s: read=%v got=%q", path, readErr, got)
		}
	}
	profileAfter, readErr := os.ReadFile(profilePath)
	if readErr != nil || !reflect.DeepEqual(profileAfter, profileBefore) {
		t.Fatalf("failed preparation changed existing profile: read=%v changed=%t", readErr, !reflect.DeepEqual(profileAfter, profileBefore))
	}
}

func TestDirectLeadSessionPreparationWithoutDeclaredLeadFailsBeforeMutationWithRemedy(t *testing.T) {
	dir := t.TempDir()
	profile := "direct"
	session := "prepared"
	if err := team.WriteProfile(dir, profile, team.Team{
		Project: dir, ExecutionMode: executionModeDirectLeadSession,
		Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: session, CWD: dir}},
	}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(team.ProfilePath(dir, profile))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = captureOutput(t, func() error {
		return runRunStart([]string{
			"--project", dir, "--profile", profile, "--session", session, "--lead", "cto",
			"--launch-shape", runwizard.LaunchShapeWorkingTeamTogether,
			"--goal", "Declare the direct lead", "--visibility", "detached", "--prepare",
		}, "test")
	})
	wantCommand := "amq-squad team lead set cto --project " + shellQuote(dir) + " --profile " + shellQuote(profile)
	if err == nil || !strings.Contains(err.Error(), wantCommand) || !strings.Contains(err.Error(), "--session "+shellQuote(session)) {
		t.Fatalf("lead-less remediation error = %v, want command %q", err, wantCommand)
	}
	after, readErr := os.ReadFile(team.ProfilePath(dir, profile))
	if readErr != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("lead-less failure changed profile: read=%v changed=%t", readErr, !reflect.DeepEqual(after, before))
	}
	for _, path := range []string{briefPathForProfile(dir, profile, session), rules.Path(dir), preparedRunPath(dir, profile, session), filepath.Join(dir, "AGENTS.md"), filepath.Join(dir, "CLAUDE.md")} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("lead-less failure crossed mutation boundary at %s: %v", path, statErr)
		}
	}
}

func TestRunStartGoWithShapeRequiresPreparedManifest(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Orchestrated: true,
		Lead:         "cto",
		Members:      []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: "legacy"}},
	})
	err := runRunStart([]string{
		"--project", dir, "--session", "legacy",
		"--launch-shape", runwizard.LaunchShapeWorkingTeamTogether,
		"--visibility", "detached", "--go",
	}, "test")
	if err == nil || !strings.Contains(err.Error(), "artifact readiness failed") {
		t.Fatalf("missing manifest error = %v", err)
	}
	if _, statErr := os.Stat(preparedRunPath(dir, team.DefaultProfile, "legacy")); !os.IsNotExist(statErr) {
		t.Fatalf("live launch synthesized a prepared manifest: %v", statErr)
	}
}

func TestRunStartGoRejectsMismatchedPreparedLaunchShape(t *testing.T) {
	dir := prepareRunStartFixture(t, runwizard.LaunchShapeWorkingTeamTogether)
	err := runRunStart([]string{
		"--project", dir, "--session", "prepared",
		"--launch-shape", runwizard.LaunchShapeLeadOnlyStaged,
		"--visibility", "detached", "--go",
	}, "test")
	if err == nil || !strings.Contains(err.Error(), "accepted launch shape") || !strings.Contains(err.Error(), "differs from requested") {
		t.Fatalf("mismatched shape error = %v", err)
	}
}

func TestRunStartGoBlocksExistingLegacyProfileWithoutShapeMigration(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Orchestrated: true,
		Lead:         "cto",
		Members:      []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: "legacy"}},
	})
	before, err := os.ReadFile(team.ProfilePath(dir, team.DefaultProfile))
	if err != nil {
		t.Fatal(err)
	}
	err = runRunStart([]string{
		"--project", dir, "--session", "legacy", "--visibility", "detached", "--go",
	}, "test")
	if err == nil || !strings.Contains(err.Error(), "requires explicit --launch-shape") || !strings.Contains(err.Error(), "migrate") {
		t.Fatalf("legacy omission error = %v", err)
	}
	after, readErr := os.ReadFile(team.ProfilePath(dir, team.DefaultProfile))
	if readErr != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("blocked legacy launch mutated profile: read=%v changed=%t", readErr, !reflect.DeepEqual(after, before))
	}
}

func prepareRunStartFixture(t *testing.T, shape string) string {
	t.Helper()
	dir := t.TempDir()
	_, _, err := captureOutput(t, func() error {
		return runRunStart([]string{
			"--project", dir, "--profile", team.DefaultProfile, "--session", "prepared",
			"--roles", "cto", "--binary", "cto=codex", "--lead", "cto",
			"--launch-shape", shape, "--goal", "Execute the accepted readiness fixture",
			"--visibility", "detached", "--prepare",
		}, "test")
	})
	if err != nil {
		t.Fatalf("prepare run fixture: %v", err)
	}
	return dir
}

func TestPreparedRunLiveGoalBindingHydratesAcceptedManifestAndRejectsSuppliedMismatch(t *testing.T) {
	dir := prepareRunStartFixture(t, runwizard.LaunchShapeWorkingTeamTogether)
	manifest, err := readPreparedRunManifest(dir, team.DefaultProfile, "prepared")
	if err != nil {
		t.Fatal(err)
	}
	got, err := preparedRunLiveGoalBinding(dir, team.DefaultProfile, "prepared", "", "", "")
	if err != nil {
		t.Fatalf("omitted live flags must hydrate from accepted manifest: %v", err)
	}
	if got.Text != manifest.GoalText || got.Source != manifest.GoalSource || got.Digest != manifest.GoalDigest || got.Namespace != manifest.GoalNamespace {
		t.Fatalf("hydrated binding=%+v manifest=%+v", got, manifest)
	}

	for _, tc := range []struct {
		name, goal, source, digest, want string
	}{
		{name: "text", goal: "substituted goal", want: "live goal text mismatch"},
		{name: "source", source: "accepted_brief", want: "live goal source mismatch"},
		{name: "digest", digest: "sha256:stale", want: "live goal digest mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := preparedRunLiveGoalBinding(dir, team.DefaultProfile, "prepared", tc.goal, tc.source, tc.digest)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("mismatch error=%v want %q", err, tc.want)
			}
		})
	}
}

func TestRunStartGoGoalMismatchBlocksBeforeManagedSpawnOrDelivery(t *testing.T) {
	dir := prepareRunStartFixture(t, runwizard.LaunchShapeWorkingTeamTogether)
	manifest, err := readPreparedRunManifest(dir, team.DefaultProfile, "prepared")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "text", args: []string{"--goal", "substituted goal"}},
		{name: "source", args: []string{"--goal-source", "accepted_brief"}},
		{name: "digest", args: []string{"--goal-digest", "sha256:stale"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upCalls, goalCalls := 0, 0
			stubRunStartGoalDelivery(t,
				func([]string, string) error { upCalls++; return nil },
				func([]string, string) error { goalCalls++; return nil },
				func(string, string, string, string) (runStartLeadReadiness, error) {
					return runStartLeadReadiness{Ready: true}, nil
				},
				func(time.Duration) {}, time.Now,
			)
			args := []string{
				"--project", dir, "--profile", team.DefaultProfile, "--session", "prepared",
				"--launch-shape", manifest.LaunchShape, "--visibility", "detached", "--go",
			}
			args = append(args, tc.args...)
			_, _, runErr := captureOutput(t, func() error { return runRunStart(args, "test") })
			if runErr == nil || !strings.Contains(runErr.Error(), "accepted live goal binding mismatch") {
				t.Fatalf("mismatch launch error=%v", runErr)
			}
			if upCalls != 0 || goalCalls != 0 {
				t.Fatalf("mismatch crossed zero-spawn boundary: up=%d goal=%d", upCalls, goalCalls)
			}
		})
	}
}

func TestRunStartPreparationProposalIsReadOnlyAndPrecedesPreparationWrites(t *testing.T) {
	dir := t.TempDir()
	args := []string{
		"--project", dir, "--profile", team.DefaultProfile, "--session", "proposal",
		"--roles", "cto", "--binary", "cto=codex", "--lead", "cto",
		"--launch-shape", runwizard.LaunchShapeWorkingTeamTogether,
		"--goal", "Execute the accepted proposal", "--visibility", "detached",
	}
	out, _, err := captureOutput(t, func() error {
		return runRunStart(append(append([]string{}, args...), "--prepare-plan"), "test")
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Read-only preparation proposal for default/proposal",
		"Initial launch: 1 members - cto",
		"planned create " + briefPathForProfile(dir, team.DefaultProfile, "proposal"),
		"planned create " + rules.Path(dir),
		"planned create " + team.ProfilePath(dir, team.DefaultProfile),
		"planned create " + preparedRunPath(dir, team.DefaultProfile, "proposal"),
		"routing=durable-amq gates=operator-contract",
		"Proposal only.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("proposal missing %q:\n%s", want, out)
		}
	}
	for _, path := range []string{team.ProfilePath(dir, team.DefaultProfile), briefPathForProfile(dir, team.DefaultProfile, "proposal"), preparedRunPath(dir, team.DefaultProfile, "proposal"), squadnamespace.AMQRoot(dir, team.DefaultProfile, "proposal")} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("read-only proposal mutated %s: %v", path, statErr)
		}
	}

	writeAuthoredReadinessRole(t, dir, "custom-reviewer", "# custom-reviewer\n\nTODO: describe this role\n")
	blockedArgs := []string{
		"--project", dir, "--profile", team.DefaultProfile, "--session", "blocked",
		"--roles", "cto,custom-reviewer", "--binary", "cto=codex,custom-reviewer=codex", "--lead", "cto",
		"--launch-shape", runwizard.LaunchShapeWorkingTeamTogether,
		"--goal", "Execute the accepted blocked proposal", "--visibility", "detached", "--prepare",
	}
	_, _, err = captureOutput(t, func() error { return runRunStart(blockedArgs, "test") })
	if err == nil || !strings.Contains(err.Error(), "role blocker [generic]") {
		t.Fatalf("predictable generic-role blocker = %v", err)
	}
	for _, path := range []string{team.ProfilePath(dir, team.DefaultProfile), briefPathForProfile(dir, team.DefaultProfile, "blocked"), preparedRunPath(dir, team.DefaultProfile, "blocked"), squadnamespace.AMQRoot(dir, team.DefaultProfile, "blocked")} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("blocked preparation partially wrote %s: %v", path, statErr)
		}
	}
}

func TestFreshProfilePreparationFailureRollsBackEveryAcceptedTarget(t *testing.T) {
	dir := t.TempDir()
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	sentinels := map[string][]byte{
		filepath.Join(dir, rules.AgentsFile): []byte("operator AGENTS sentinel\n"),
		filepath.Join(dir, rules.ClaudeFile): []byte("operator CLAUDE sentinel\n"),
	}
	for path, body := range sentinels {
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	oldBuild := buildPreparedRunManifestForPreparation
	buildPreparedRunManifestForPreparation = func(string, string, string, string, string, acceptedGoalBinding, acceptedRunContext) (preparedRunManifest, error) {
		return preparedRunManifest{}, fmt.Errorf("injected fresh-profile post-proposal failure")
	}
	t.Cleanup(func() { buildPreparedRunManifestForPreparation = oldBuild })

	_, _, err := captureOutput(t, func() error {
		return runRunStart([]string{
			"--project", dir, "--profile", team.DefaultProfile, "--session", "atomic-fresh",
			"--roles", "cto,qa", "--binary", "cto=codex,qa=codex", "--lead", "cto",
			"--tool-profile", "cto=minimal,qa=browser",
			"--launch-shape", runwizard.LaunchShapeWorkingTeamTogether,
			"--goal", "Exercise the complete fresh preparation transaction", "--visibility", "detached", "--prepare",
		}, "test")
	})
	if err == nil || !strings.Contains(err.Error(), "injected fresh-profile post-proposal failure") {
		t.Fatalf("preparation error = %v", err)
	}
	for _, path := range []string{
		team.ProfilePath(dir, team.DefaultProfile),
		rules.Path(dir),
		briefPathForProfile(dir, team.DefaultProfile, "atomic-fresh"),
		preparedRunPath(dir, team.DefaultProfile, "atomic-fresh"),
		filepath.Join(codexHome, generatedCodexProfileName(team.DefaultProfile, "cto")+".config.toml"),
		filepath.Join(codexHome, generatedCodexProfileName(team.DefaultProfile, "qa")+".config.toml"),
	} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("failed fresh preparation left target %s: %v", path, statErr)
		}
	}
	for path, want := range sentinels {
		got, readErr := os.ReadFile(path)
		if readErr != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("failed fresh preparation changed %s: read=%v got=%q", path, readErr, got)
		}
	}
}

func TestFreshProfilePreparationPreflightsManifestAncestorsBeforeRosterWrites(t *testing.T) {
	dir := t.TempDir()
	blockedAncestor := filepath.Join(dir, team.DirName, "prepared")
	if err := os.MkdirAll(filepath.Dir(blockedAncestor), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blockedAncestor, []byte("operator-owned blocker\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := captureOutput(t, func() error {
		return runRunStart([]string{
			"--project", dir, "--profile", team.DefaultProfile, "--session", "ancestor-blocked",
			"--roles", "cto", "--binary", "cto=codex", "--lead", "cto",
			"--launch-shape", runwizard.LaunchShapeWorkingTeamTogether,
			"--goal", "Preflight the prepared manifest ancestry", "--visibility", "detached", "--prepare",
		}, "test")
	})
	if err == nil || !strings.Contains(err.Error(), "preflight prepared manifest ancestor") {
		t.Fatalf("manifest ancestor error = %v", err)
	}
	for _, path := range []string{team.ProfilePath(dir, team.DefaultProfile), rules.Path(dir), briefPathForProfile(dir, team.DefaultProfile, "ancestor-blocked")} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("manifest ancestor failure crossed mutation boundary at %s: %v", path, statErr)
		}
	}
	got, readErr := os.ReadFile(blockedAncestor)
	if readErr != nil || string(got) != "operator-owned blocker\n" {
		t.Fatalf("manifest ancestor changed: read=%v got=%q", readErr, got)
	}
}

func TestFreshRunPreparationAMQFailureIncludesBootstrapRemedy(t *testing.T) {
	dir := t.TempDir()
	layout, err := resolveRunStartLayout(runStartLayoutInput{Visibility: visibilityDetached, VisibilitySet: true})
	if err != nil {
		t.Fatal(err)
	}
	oldObserve := observePreparedRunEnvironment
	observePreparedRunEnvironment = func(string, string) preparedRunEnvironmentObservation {
		return preparedRunEnvironmentObservation{
			BinaryVersion: "test",
			Skill:         doctorCheck{Name: "skill version", Status: doctorOK, Detail: "matching"},
			AMQ:           doctorCheck{Name: "amq version", Status: doctorFail, Detail: "amq env failed: cannot determine root: no .amqrc found and AM_ROOT not set"},
			Terminal:      doctorCheck{Name: "terminal context", Status: doctorOK, Detail: "detached"},
		}
	}
	t.Cleanup(func() { observePreparedRunEnvironment = oldObserve })
	_, err = buildRunPreparationProposal(runPreparationProposalInput{
		Project: dir, Profile: team.DefaultProfile, Session: "fresh", LaunchShape: runwizard.LaunchShapeWorkingTeamTogether,
		Goal: "Bootstrap AMQ explicitly", Team: team.Team{Project: dir, Orchestrated: true, Lead: "cto", Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: "fresh"}}},
		Context: acceptedRunContext{Version: "test", Topology: acceptedTopology(layout, false)},
	})
	wantRoot := filepath.Join(dir, defaultBaseRootName)
	if err == nil || !strings.Contains(err.Error(), "amq init --root "+shellQuote(wantRoot)) || !strings.Contains(err.Error(), "--agents "+shellQuote("cto,user")) || !strings.Contains(err.Error(), "then rerun the proposal") {
		t.Fatalf("fresh AMQ remedy = %v", err)
	}
}

func TestRunStartPreparationPreflightsEveryGeneratedPolicyBeforeAnyWrite(t *testing.T) {
	dir := t.TempDir()
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	foreign := filepath.Join(codexHome, generatedCodexProfileName(team.DefaultProfile, "qa")+".config.toml")
	if err := os.WriteFile(foreign, []byte("# foreign operator policy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	earlier := filepath.Join(codexHome, generatedCodexProfileName(team.DefaultProfile, "cto")+".config.toml")
	_, _, err := captureOutput(t, func() error {
		return runRunStart([]string{
			"--project", dir, "--profile", team.DefaultProfile, "--session", "policy-blocked",
			"--roles", "cto,qa", "--binary", "cto=codex,qa=codex", "--lead", "cto",
			"--tool-profile", "cto=minimal,qa=browser",
			"--launch-shape", runwizard.LaunchShapeWorkingTeamTogether,
			"--goal", "Execute the accepted policy plan", "--visibility", "detached", "--prepare",
		}, "test")
	})
	if err == nil || !strings.Contains(err.Error(), "exists with different content") || !strings.Contains(err.Error(), foreign) {
		t.Fatalf("foreign later policy blocker = %v", err)
	}
	for _, path := range []string{earlier, team.ProfilePath(dir, team.DefaultProfile), briefPathForProfile(dir, team.DefaultProfile, "policy-blocked"), rules.Path(dir), preparedRunPath(dir, team.DefaultProfile, "policy-blocked"), squadnamespace.AMQRoot(dir, team.DefaultProfile, "policy-blocked")} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("policy preflight partially wrote %s: %v", path, statErr)
		}
	}
}

func TestRunStartPreparationMalformedPointerPlanBlocksAllWrites(t *testing.T) {
	dir := t.TempDir()
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	pointer := filepath.Join(dir, rules.AgentsFile)
	if err := os.WriteFile(pointer, []byte("user content\n"+rules.BeginMarker+"\nunterminated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	policy := filepath.Join(codexHome, generatedCodexProfileName(team.DefaultProfile, "cto")+".config.toml")
	backend := useFakeTmuxBackend(t)
	_, _, err := captureOutput(t, func() error {
		return runRunStart([]string{
			"--project", dir, "--profile", team.DefaultProfile, "--session", "pointer-blocked",
			"--roles", "cto", "--binary", "cto=codex", "--lead", "cto", "--tool-profile", "cto=minimal",
			"--launch-shape", runwizard.LaunchShapeWorkingTeamTogether,
			"--goal", "Execute the accepted pointer plan", "--visibility", "detached", "--prepare",
		}, "test")
	})
	if err == nil || !strings.Contains(err.Error(), "pointer plan blocker [malformed markers]") {
		t.Fatalf("malformed pointer blocker = %v", err)
	}
	for _, path := range []string{policy, team.ProfilePath(dir, team.DefaultProfile), briefPathForProfile(dir, team.DefaultProfile, "pointer-blocked"), rules.Path(dir), preparedRunPath(dir, team.DefaultProfile, "pointer-blocked"), squadnamespace.AMQRoot(dir, team.DefaultProfile, "pointer-blocked")} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("malformed pointer plan partially wrote %s: %v", path, statErr)
		}
	}
	if len(backend.launches) != 0 {
		t.Fatalf("malformed pointer plan opened panes: %+v", backend.launches)
	}
}

func TestRunStartPreparationRevalidatesAcceptedPointerPlanBeforeWrites(t *testing.T) {
	dir := t.TempDir()
	pointer := filepath.Join(dir, rules.AgentsFile)
	oldAfterProposal := runPreparationAfterProposal
	runPreparationAfterProposal = func() {
		if err := os.WriteFile(pointer, []byte("concurrent user change\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { runPreparationAfterProposal = oldAfterProposal })
	_, _, err := captureOutput(t, func() error {
		return runRunStart([]string{
			"--project", dir, "--profile", team.DefaultProfile, "--session", "pointer-race",
			"--roles", "cto", "--binary", "cto=codex", "--lead", "cto",
			"--launch-shape", runwizard.LaunchShapeWorkingTeamTogether,
			"--goal", "Execute the accepted pointer race plan", "--visibility", "detached", "--prepare",
		}, "test")
	})
	if err == nil || !strings.Contains(err.Error(), "changed after the accepted proposal") {
		t.Fatalf("pointer revalidation error = %v", err)
	}
	for _, path := range []string{team.ProfilePath(dir, team.DefaultProfile), briefPathForProfile(dir, team.DefaultProfile, "pointer-race"), rules.Path(dir), preparedRunPath(dir, team.DefaultProfile, "pointer-race"), squadnamespace.AMQRoot(dir, team.DefaultProfile, "pointer-race")} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("pointer race partially wrote %s: %v", path, statErr)
		}
	}
}

func TestExistingNamedProfileProposalUsesExactMemberIdentityAndPreservesPolicy(t *testing.T) {
	dir := t.TempDir()
	const profile = "review"
	const session = "existing"
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	tm := team.Team{
		Project: dir, Orchestrated: true, Lead: "cto", ExecutionMode: executionModeProjectLead,
		ControlRoot: dir, TargetProjectRoot: dir,
		Members: []team.Member{{Role: "cto", Handle: "captain", Binary: "claude", Session: session, Model: "sonnet"}},
	}
	if err := team.WriteProfile(dir, profile, tm); err != nil {
		t.Fatal(err)
	}
	if err := applyRunStartToolProfiles(dir, profile, "cto=minimal"); err != nil {
		t.Fatal(err)
	}
	before, err := team.ReadProfile(dir, profile)
	if err != nil {
		t.Fatal(err)
	}
	args := []string{
		"--project", dir, "--profile", profile, "--session", session,
		"--launch-shape", runwizard.LaunchShapeWorkingTeamTogether,
		"--goal", "Execute the accepted existing profile", "--visibility", "detached",
	}
	out, _, err := captureOutput(t, func() error {
		return runRunStart(append(append([]string{}, args...), "--prepare-plan"), "test")
	})
	if err != nil {
		t.Fatal(err)
	}
	rolePath := filepath.Join(squadnamespace.AMQRoot(dir, profile, session), "agents", "captain", "extensions", "io.github.omriariav.amq-squad", "role.md")
	for _, want := range []string{"handle=captain", "binary=claude", "model=sonnet", "tool_policy=minimal", "goal_mode=native_goal", rolePath} {
		if !strings.Contains(out, want) {
			t.Fatalf("existing proposal missing %q:\n%s", want, out)
		}
	}
	_, _, err = captureOutput(t, func() error {
		return runRunStart(append(append([]string{}, args...), "--prepare"), "test")
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := team.ReadProfile(dir, profile)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Members) != 1 || after.Members[0].Handle != before.Members[0].Handle || after.Members[0].Binary != before.Members[0].Binary || after.Members[0].Model != before.Members[0].Model || after.Members[0].ToolProfile != before.Members[0].ToolProfile || after.Members[0].ToolConfig != before.Members[0].ToolConfig || after.Members[0].ToolMCPConfig != before.Members[0].ToolMCPConfig {
		t.Fatalf("existing member identity/policy changed: before=%+v after=%+v", before.Members, after.Members)
	}
}

func TestNamedProfilePreparationPreservesNamespaceBootstrapAndLaunchBinding(t *testing.T) {
	dir := t.TempDir()
	const profile = "review"
	const session = "named"
	const goal = "Execute the accepted named-profile goal"
	_, _, err := captureOutput(t, func() error {
		return runRunStart([]string{
			"--project", dir, "--profile", profile, "--session", session,
			"--roles", "cto", "--binary", "cto=codex", "--lead", "cto",
			"--launch-shape", runwizard.LaunchShapeWorkingTeamTogether,
			"--goal", goal, "--visibility", "detached", "--prepare",
		}, "test")
	})
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := preparedRunPath(dir, profile, session)
	if want := filepath.Join(dir, ".amq-squad", "prepared", profile, session+".json"); manifestPath != want {
		t.Fatalf("prepared path=%q want=%q", manifestPath, want)
	}
	if _, statErr := os.Stat(manifestPath); statErr != nil {
		t.Fatal(statErr)
	}
	result := calculateRunReadiness(dir, profile, session)
	if !result.Ready || result.Namespace != profile+"/"+session {
		t.Fatalf("named readiness = %+v", result)
	}
	bootstrap := readinessRow(result, "bootstrap:cto").Evidence
	for _, want := range []string{squadnamespace.AMQRoot(dir, profile, session), briefPathForProfile(dir, profile, session), "namespace=" + profile + "/" + session} {
		if !strings.Contains(bootstrap, want) {
			t.Fatalf("named bootstrap missing %q: %s", want, bootstrap)
		}
	}
	manifest, err := readPreparedRunManifest(dir, profile, session)
	if err != nil {
		t.Fatal(err)
	}
	tm, err := team.ReadProfile(dir, profile)
	if err != nil {
		t.Fatal(err)
	}
	binding, bindErr := preparedGoalBinding(tm, profile, session, tm.Members[0], acceptedGoalBinding{
		Text: manifest.GoalText, Source: manifest.GoalSource,
		Namespace: manifest.GoalNamespace, Digest: manifest.GoalDigest,
	})
	if bindErr != nil {
		t.Fatal(bindErr)
	}
	if binding == nil || binding.Goal != goal || !strings.Contains(binding.Command, "--profile "+profile) || !strings.Contains(binding.Command, "--session "+session) {
		t.Fatalf("named launch binding = %+v", binding)
	}
}

func TestObservedEnvironmentAndTopologyFailuresBlockReadinessBeforePanes(t *testing.T) {
	dir := prepareRunStartFixture(t, runwizard.LaunchShapeWorkingTeamTogether)
	manifest, err := readPreparedRunManifest(dir, team.DefaultProfile, "prepared")
	if err != nil {
		t.Fatal(err)
	}
	oldObserve := observePreparedRunEnvironment
	t.Cleanup(func() { observePreparedRunEnvironment = oldObserve })
	base := preparedRunEnvironmentObservation{
		BinaryVersion: "test",
		Skill:         doctorCheck{Name: "skill version", Status: doctorOK, Detail: "observed matching skill test"},
		AMQ:           doctorCheck{Name: "amq version", Status: doctorOK, Detail: "observed amq " + doctorMinAMQVersion},
		Terminal:      doctorCheck{Name: "tmux", Status: doctorOK, Detail: "observed tmux"},
		HostContext:   runtimecontrol.DetectHostContext([]string{"TMUX=test"}, false),
		Capabilities:  []string{"amq-routing", "terminal-context", "tmux-topology"},
	}

	t.Run("observed skill mismatch", func(t *testing.T) {
		backend := useFakeTmuxBackend(t)
		observation := base
		observation.Skill = doctorCheck{Name: "skill version", Status: doctorWarn, Detail: "installed skill v2.21.0 differs from current binary v2.22.0"}
		observePreparedRunEnvironment = func(string, string) preparedRunEnvironmentObservation { return observation }
		out, _, runErr := captureOutput(t, func() error {
			return runRunStart([]string{"--project", dir, "--profile", team.DefaultProfile, "--session", "prepared", "--visibility", "detached", "--readiness-json"}, "test")
		})
		if runErr == nil || !strings.Contains(runErr.Error(), "artifact readiness failed") || !strings.Contains(out, `"ready": false`) || !strings.Contains(out, "observed binary/skill") || len(backend.launches) != 0 {
			t.Fatalf("skill mismatch readiness err=%v launches=%v output=%s", runErr, backend.launches, out)
		}
	})

	t.Run("observed AMQ too old and missing routing capability", func(t *testing.T) {
		backend := useFakeTmuxBackend(t)
		observation := base
		observation.AMQ = doctorCheck{Name: "amq version", Status: doctorFail, Detail: "amq 0.41.0 is older than required " + doctorMinAMQVersion}
		observation.Capabilities = []string{"tmux-topology"}
		observePreparedRunEnvironment = func(string, string) preparedRunEnvironmentObservation { return observation }
		out, _, runErr := captureOutput(t, func() error {
			return runRunStart([]string{"--project", dir, "--profile", team.DefaultProfile, "--session", "prepared", "--visibility", "detached", "--readiness-json"}, "test")
		})
		if runErr == nil || !strings.Contains(runErr.Error(), "artifact readiness failed") || !strings.Contains(out, `"ready": false`) || !strings.Contains(out, "observed AMQ") || len(backend.launches) != 0 {
			t.Fatalf("AMQ mismatch readiness err=%v launches=%v output=%s", runErr, backend.launches, out)
		}
	})

	t.Run("requested topology differs", func(t *testing.T) {
		observePreparedRunEnvironment = func(string, string) preparedRunEnvironmentObservation { return base }
		other := manifest.Topology
		other.Visibility, other.Target = visibilityCurrent, "current-window"
		result := calculateRunReadinessWithContext(dir, team.DefaultProfile, "prepared", acceptedRunContext{Version: "test", Topology: other})
		if result.Ready || readinessRowStatus(result, "environment") != "drifted" || !strings.Contains(readinessRow(result, "environment").Evidence, "requested topology differs") {
			t.Fatalf("topology mismatch readiness = %+v", result)
		}
	})

	t.Run("terminal context schema differs", func(t *testing.T) {
		backend := useFakeTmuxBackend(t)
		observation := base
		observation.HostContext.SchemaVersion++
		observePreparedRunEnvironment = func(string, string) preparedRunEnvironmentObservation { return observation }
		out, _, runErr := captureOutput(t, func() error {
			return runRunStart([]string{"--project", dir, "--profile", team.DefaultProfile, "--session", "prepared", "--visibility", "detached", "--readiness-json"}, "test")
		})
		if runErr == nil || !strings.Contains(runErr.Error(), "artifact readiness failed") || !strings.Contains(out, "terminal context schema drift") || len(backend.launches) != 0 {
			t.Fatalf("terminal schema readiness err=%v launches=%v output=%s", runErr, backend.launches, out)
		}
	})
}

func TestApplyRunStartToolProfilesPrevalidatesUnknownRoleBeforeWriting(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Handle: "cto", Binary: "codex", Session: "s"},
			{Role: "qa", Handle: "qa", Binary: "codex", Session: "s"},
		},
	})
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	before, err := team.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = applyRunStartToolProfiles(dir, team.DefaultProfile, "cto=minimal,missing=browser")
	if err == nil || !strings.Contains(err.Error(), "unknown initial role") {
		t.Fatalf("unknown role error = %v", err)
	}
	after, readErr := team.Read(dir)
	if readErr != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("unknown role partially mutated profile: read=%v changed=%t", readErr, !reflect.DeepEqual(after, before))
	}
	entries, readDirErr := os.ReadDir(codexHome)
	if readDirErr != nil || len(entries) != 0 {
		t.Fatalf("unknown role wrote generated policies: entries=%v err=%v", entries, readDirErr)
	}
}

func TestApplyRunStartToolProfilesLaterConflictPublishesNothing(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Handle: "cto", Binary: "codex", Session: "s"},
			{Role: "qa", Handle: "qa", Binary: "codex", Session: "s"},
		},
	})
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	conflict := filepath.Join(codexHome, generatedCodexProfileName(team.DefaultProfile, "qa")+".config.toml")
	if err := os.WriteFile(conflict, []byte("# foreign policy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := team.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = applyRunStartToolProfiles(dir, team.DefaultProfile, "cto=minimal,qa=browser")
	if err == nil || !strings.Contains(err.Error(), "exists with different content") {
		t.Fatalf("later conflict error = %v", err)
	}
	ctoPolicy := filepath.Join(codexHome, generatedCodexProfileName(team.DefaultProfile, "cto")+".config.toml")
	if _, statErr := os.Stat(ctoPolicy); !os.IsNotExist(statErr) {
		t.Fatalf("earlier target was published before later conflict: %v", statErr)
	}
	after, readErr := team.Read(dir)
	if readErr != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("later conflict partially mutated profile: read=%v changed=%t", readErr, !reflect.DeepEqual(after, before))
	}
}

func TestRunReadinessMachineStatuses(t *testing.T) {
	tests := []struct {
		name       string
		artifact   string
		wantStatus string
		mutate     func(*testing.T, string, *preparedRunManifest)
	}{
		{name: "ready", artifact: "brief", wantStatus: "ready"},
		{name: "missing", artifact: "role:custom-role", wantStatus: "missing", mutate: func(t *testing.T, dir string, manifest *preparedRunManifest) {
			addReadinessCustomRole(t, dir, manifest, "custom-role", "# Custom role\n\nOwn a bounded implementation slice.\n")
			if err := os.Remove(team.CustomRolePath(dir, "custom-role")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "stub", artifact: "brief", wantStatus: "stub", mutate: func(t *testing.T, dir string, manifest *preparedRunManifest) {
			path := briefPathForProfile(dir, team.DefaultProfile, "prepared")
			if err := os.WriteFile(path, []byte(briefStubFirstLine+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "generic", artifact: "role:custom-role", wantStatus: "generic", mutate: func(t *testing.T, dir string, manifest *preparedRunManifest) {
			addReadinessCustomRole(t, dir, manifest, "custom-role", "# Custom role\n\nNo catalog description is configured for this custom role. Follow team rules.\n")
		}},
		{name: "stale", artifact: "team_rules", wantStatus: "stale", mutate: func(t *testing.T, dir string, _ *preparedRunManifest) {
			path := rules.Path(dir)
			if err := os.WriteFile(path, []byte("# stale rules from another workstream\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "drifted", artifact: "profile", wantStatus: "drifted", mutate: func(t *testing.T, dir string, _ *preparedRunManifest) {
			tm, err := team.Read(dir)
			if err != nil {
				t.Fatal(err)
			}
			tm.Members[0].Model = "changed-after-acceptance"
			if err := team.Write(dir, tm); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := prepareRunStartFixture(t, runwizard.LaunchShapeWorkingTeamTogether)
			manifest, err := readPreparedRunManifest(dir, team.DefaultProfile, "prepared")
			if err != nil {
				t.Fatal(err)
			}
			if tt.mutate != nil {
				tt.mutate(t, dir, &manifest)
				if err := writePreparedRunManifest(preparedRunPath(dir, team.DefaultProfile, "prepared"), manifest); err != nil {
					t.Fatal(err)
				}
			}
			result := calculateRunReadiness(dir, team.DefaultProfile, "prepared")
			if got := readinessRowStatus(result, tt.artifact); got != tt.wantStatus {
				t.Fatalf("%s status = %q, want %q\nrows=%+v", tt.artifact, got, tt.wantStatus, result.Rows)
			}
			if tt.wantStatus != "ready" && result.Ready {
				t.Fatalf("non-ready %s row did not fail readiness", tt.wantStatus)
			}
		})
	}
}

func TestRunReadinessFiveAuthoredThreeIntendedOneProfileFailsExactRoster(t *testing.T) {
	dir := prepareRunStartFixture(t, runwizard.LaunchShapeWorkingTeamTogether)
	manifest, err := readPreparedRunManifest(dir, team.DefaultProfile, "prepared")
	if err != nil {
		t.Fatal(err)
	}
	for _, roleID := range []string{"platform-dev", "runtime-dev", "protocol-reviewer", "operator-reviewer"} {
		writeAuthoredReadinessRole(t, dir, roleID, "# "+roleID+"\n\nOwn the explicit "+roleID+" task, mutation boundary, routing, and done criteria.\n")
		digest, _, err := roleContractDigest(dir, roleID)
		if err != nil {
			t.Fatal(err)
		}
		manifest.RoleDigests[roleID] = digest
	}
	manifest.InitialRoster = []string{"cto", "platform-dev", "runtime-dev"}
	manifest.StagedRoster = []string{"protocol-reviewer", "operator-reviewer"}
	if err := writePreparedRunManifest(preparedRunPath(dir, team.DefaultProfile, "prepared"), manifest); err != nil {
		t.Fatal(err)
	}

	result := calculateRunReadiness(dir, team.DefaultProfile, "prepared")
	if result.Ready || result.InitialCount != 3 || result.StagedCount != 2 {
		t.Fatalf("mismatched readiness = ready:%t initial:%d staged:%d", result.Ready, result.InitialCount, result.StagedCount)
	}
	profile := readinessRow(result, "profile")
	if profile.Status != "drifted" || !strings.Contains(profile.Evidence, "initial roster mismatch") || !strings.Contains(profile.Evidence, "accepted 3") || !strings.Contains(profile.Evidence, "profile 1") {
		t.Fatalf("profile mismatch row = %+v", profile)
	}
	for _, roleID := range []string{"platform-dev", "runtime-dev"} {
		if row := readinessRow(result, "bootstrap:"+roleID); row.Status != "missing" || !strings.Contains(row.Evidence, "absent from the profile") {
			t.Fatalf("missing accepted bootstrap %s = %+v", roleID, row)
		}
	}
	for _, roleID := range []string{"protocol-reviewer", "operator-reviewer"} {
		if row := readinessRow(result, "staged_role:"+roleID); row.Status != "ready" || !strings.Contains(row.Evidence, "absent from exact session launch") {
			t.Fatalf("staged absence %s = %+v", roleID, row)
		}
		if got := readinessRowStatus(result, "bootstrap:"+roleID); got != "" {
			t.Fatalf("staged-only role %s received bootstrap row %q", roleID, got)
		}
	}
}

func TestPreparedBootstrapEvidenceInitialOnlyExactContract(t *testing.T) {
	dir := prepareRunStartFixture(t, runwizard.LaunchShapeWorkingTeamTogether)
	manifest, err := readPreparedRunManifest(dir, team.DefaultProfile, "prepared")
	if err != nil {
		t.Fatal(err)
	}
	addReadinessCustomRole(t, dir, &manifest, "operator-reviewer", "# Operator reviewer\n\nReview durable operator gates and never mutate implementation files.\n")
	if err := writePreparedRunManifest(preparedRunPath(dir, team.DefaultProfile, "prepared"), manifest); err != nil {
		t.Fatal(err)
	}
	result := calculateRunReadiness(dir, team.DefaultProfile, "prepared")
	row := readinessRow(result, "bootstrap:cto")
	if row.Status != "ready" {
		t.Fatalf("lead bootstrap row = %+v", row)
	}
	for _, want := range []string{
		"namespace=default/prepared",
		"role=cto",
		"lead=cto",
		"brief=" + briefPathForProfile(dir, team.DefaultProfile, "prepared"),
		"rules=" + rules.Path(dir),
		"role_path=",
		"goal_mode=prompt_goal",
		"goal_digest=" + manifest.GoalDigest,
		"routing=durable-amq",
		"gates=operator-contract",
	} {
		if !strings.Contains(row.Evidence, want) {
			t.Fatalf("bootstrap evidence missing %q: %s", want, row.Evidence)
		}
	}
	if got := readinessRowStatus(result, "bootstrap:operator-reviewer"); got != "" {
		t.Fatalf("staged-only role received bootstrap row %q", got)
	}
}

func TestPreparedRunGoalBindingCodexClaudeParityInActualLaunchInput(t *testing.T) {
	for _, binary := range []string{"codex", "claude"} {
		t.Run(binary, func(t *testing.T) {
			dir := prepareRunStartBinaryFixture(t, binary)
			manifest, err := readPreparedRunManifest(dir, team.DefaultProfile, "prepared")
			if err != nil {
				t.Fatal(err)
			}
			tm, err := team.ReadProfile(dir, team.DefaultProfile)
			if err != nil {
				t.Fatal(err)
			}
			binding, err := preparedGoalBinding(tm, team.DefaultProfile, "prepared", tm.Members[0], acceptedGoalBinding{
				Text: manifest.GoalText, Source: manifest.GoalSource,
				Namespace: manifest.GoalNamespace, Digest: manifest.GoalDigest,
			})
			if err != nil {
				t.Fatal(err)
			}
			if binding == nil || binding.Source != "prepared-run" || binding.Goal != "Execute the accepted readiness fixture" || binding.DeliveryState != goalBindingDeliveryPrepared || launchRecordHasGoalBinding(launch.Record{Binary: binary, GoalBinding: binding}) {
				t.Fatalf("prepared %s binding = %+v", binary, binding)
			}
			wantMode := map[string]string{"codex": "prompt_goal", "claude": "native_goal"}[binary]
			if binding.Mode != wantMode {
				t.Fatalf("prepared %s mode = %q, want %q", binary, binding.Mode, wantMode)
			}

			var observed launch.Record
			var argv []string
			oldObserver := launchPlanObserver
			launchPlanObserver = func(rec launch.Record, args []string) {
				observed = rec
				argv = append([]string(nil), args...)
			}
			t.Cleanup(func() { launchPlanObserver = oldObserver })
			_, _, err = captureOutput(t, func() error {
				return runLaunch([]string{
					"--project", dir,
					"--team-home", dir,
					"--team-profile", team.DefaultProfile,
					"--role", "cto",
					"--me", "cto",
					"--session", "prepared",
					"--trust", "sandboxed",
					"--dry-run",
					binary,
				})
			})
			if err != nil {
				t.Fatalf("dry-run launch: %v", err)
			}
			if observed.GoalBinding == nil || observed.GoalBinding.Source != "prepared-run" || observed.GoalBinding.DeliveryState != goalBindingDeliveryPrepared || observed.GoalBinding.Goal != manifest.GoalText || launchRecordHasGoalBinding(observed) {
				t.Fatalf("actual launch did not preserve accepted planned/unverified binding: %+v", observed.GoalBinding)
			}
			joined := strings.Join(argv, "\n")
			if !strings.Contains(joined, "Goal binding: "+wantMode) || strings.Contains(joined, "Goal binding: "+wantMode+"_missing") {
				t.Fatalf("%s actual-launch bootstrap did not carry the accepted binding:\n%s", binary, joined)
			}
		})
	}
}

func TestDirectLaunchNeverTrustsDriftedPreparedGoalBinding(t *testing.T) {
	dir := prepareRunStartBinaryFixture(t, "codex")
	path := briefPathForProfile(dir, team.DefaultProfile, "prepared")
	if err := os.WriteFile(path, []byte("# changed after accepted preparation\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var observed launch.Record
	oldObserver := launchPlanObserver
	launchPlanObserver = func(rec launch.Record, _ []string) { observed = rec }
	t.Cleanup(func() { launchPlanObserver = oldObserver })
	_, _, err := captureOutput(t, func() error {
		return runLaunch([]string{
			"--project", dir, "--team-home", dir, "--team-profile", team.DefaultProfile,
			"--role", "cto", "--me", "cto", "--session", "prepared", "--trust", "sandboxed", "--dry-run", "codex",
		})
	})
	if err == nil || !strings.Contains(err.Error(), "prepared launch readiness drift [brief/drifted]") {
		t.Fatalf("drifted prepared launch error = %v", err)
	}
	if observed.GoalBinding != nil {
		t.Fatalf("drifted prepared launch reached launch observer: %+v", observed.GoalBinding)
	}
}

func TestRunStartPreparedCompositionLaunchSuccessBothShapes(t *testing.T) {
	for _, tt := range []struct {
		name          string
		shape         string
		roles         string
		binary        string
		staged        string
		wantInitial   []string
		wantStaged    []string
		wantUpMembers int
	}{
		{
			name: "working team together", shape: runwizard.LaunchShapeWorkingTeamTogether,
			roles: "cto,qa", binary: "cto=codex,qa=claude",
			wantInitial: []string{"cto", "qa"}, wantUpMembers: 2,
		},
		{
			name: "lead only staged", shape: runwizard.LaunchShapeLeadOnlyStaged,
			roles: "cto", binary: "cto=codex", staged: "qa",
			wantInitial: []string{"cto"}, wantStaged: []string{"qa"}, wantUpMembers: 1,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			prepareArgs := []string{
				"--project", dir, "--profile", team.DefaultProfile, "--session", "prepared",
				"--roles", tt.roles, "--binary", tt.binary, "--lead", "cto",
				"--launch-shape", tt.shape, "--goal", "Execute the accepted composition",
				"--visibility", "detached", "--prepare",
			}
			if tt.staged != "" {
				prepareArgs = append(prepareArgs, "--staged-roles", tt.staged)
			}
			if _, _, err := captureOutput(t, func() error { return runRunStart(prepareArgs, "test") }); err != nil {
				t.Fatalf("prepare: %v", err)
			}
			manifest, err := readPreparedRunManifest(dir, team.DefaultProfile, "prepared")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(manifest.InitialRoster, tt.wantInitial) || !reflect.DeepEqual(manifest.StagedRoster, tt.wantStaged) {
				t.Fatalf("prepared rosters initial=%v staged=%v", manifest.InitialRoster, manifest.StagedRoster)
			}
			profile, err := team.Read(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(profile.Members) != tt.wantUpMembers {
				t.Fatalf("profile members=%d want=%d", len(profile.Members), tt.wantUpMembers)
			}

			var upCalls, goalCalls [][]string
			stubRunStartGoalDelivery(t,
				func(args []string, _ string) error {
					upCalls = append(upCalls, append([]string(nil), args...))
					return nil
				},
				func(args []string, _ string) error {
					goalCalls = append(goalCalls, append([]string(nil), args...))
					return nil
				},
				func(string, string, string, string) (runStartLeadReadiness, error) {
					return runStartLeadReadiness{Ready: true, Detail: "verified prepared lead"}, nil
				},
				func(time.Duration) {}, time.Now,
			)
			_, _, err = captureOutput(t, func() error {
				return runRunStart([]string{
					"--project", dir, "--profile", team.DefaultProfile, "--session", "prepared",
					"--launch-shape", tt.shape, "--goal", "Execute the accepted composition",
					"--visibility", "detached", "--go",
				}, "test")
			})
			if err != nil {
				t.Fatalf("live launch: %v", err)
			}
			if len(upCalls) != 1 || len(goalCalls) != 1 {
				t.Fatalf("launch calls up=%v goal=%v", upCalls, goalCalls)
			}
			if strings.Contains(strings.Join(upCalls[0], " "), "--roles") {
				t.Fatalf("live launch replayed roster mutation: %v", upCalls[0])
			}
		})
	}
}

func TestFreshWizardPreparationAndLaunchRejectionsOpenNoPanes(t *testing.T) {
	for _, tt := range []struct {
		name           string
		input          string
		wantPrepared   bool
		wantProfile    bool
		wantPromptText string
	}{
		{name: "preparation rejected", input: "\n", wantPromptText: "Prepare coordination artifacts now?"},
		{name: "launch rejected", input: "y\n\n", wantPrepared: true, wantProfile: true, wantPromptText: "Launch now?"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			backend := useFakeTmuxBackend(t)
			oldProjectExecute := runStartWizardProjectExecute
			oldPrepareConfirm := runStartWizardPrepareConfirm
			oldLaunchConfirm := runStartWizardConfirm
			runStartWizardProjectExecute = runRunStart
			runStartWizardPrepareConfirm = promptRunStartWizardPrepare
			runStartWizardConfirm = promptRunStartWizardLaunch
			t.Cleanup(func() {
				runStartWizardProjectExecute = oldProjectExecute
				runStartWizardPrepareConfirm = oldPrepareConfirm
				runStartWizardConfirm = oldLaunchConfirm
			})
			spec := runwizard.Spec{
				Scope: "project", Backend: runwizard.BackendRunStart,
				Project: dir, Profile: team.DefaultProfile, ProfileBranch: runwizard.ProfileBranchNew, Session: "wizard",
				Roles: "cto", Binary: "cto=codex", ToolProfile: "cto=full",
				Lead: "cto", LeadMode: "builder", LaunchShape: runwizard.LaunchShapeWorkingTeamTogether,
				Visibility: "detached", OperatorMode: team.OperatorInteractionSeparateTerminal, LauncherPane: launcherPaneKeep,
				Goal: "Execute the explicitly accepted wizard goal",
			}
			var prompts strings.Builder
			if err := finishRunStartWizard(spec, "test", strings.NewReader(tt.input), &prompts); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(prompts.String(), tt.wantPromptText) {
				t.Fatalf("prompt missing %q:\n%s", tt.wantPromptText, prompts.String())
			}
			if len(backend.launches) != 0 {
				t.Fatalf("rejected gate opened panes: %+v", backend.launches)
			}
			if team.Exists(dir) != tt.wantProfile {
				t.Fatalf("profile exists=%t want=%t", team.Exists(dir), tt.wantProfile)
			}
			_, manifestErr := os.Stat(preparedRunPath(dir, team.DefaultProfile, "wizard"))
			if tt.wantPrepared {
				if manifestErr != nil {
					t.Fatalf("approved preparation did not write manifest: %v", manifestErr)
				}
				if result := calculateRunReadiness(dir, team.DefaultProfile, "wizard"); !result.Ready {
					t.Fatalf("approved preparation not ready: %+v", result.Rows)
				}
			} else if !os.IsNotExist(manifestErr) {
				t.Fatalf("rejected preparation wrote manifest: %v", manifestErr)
			}
		})
	}
}

func addReadinessCustomRole(t *testing.T, dir string, manifest *preparedRunManifest, roleID, body string) {
	t.Helper()
	writeAuthoredReadinessRole(t, dir, roleID, body)
	digest, _, err := roleContractDigest(dir, roleID)
	if err != nil {
		t.Fatal(err)
	}
	manifest.StagedRoster = append(manifest.StagedRoster, roleID)
	manifest.RoleDigests[roleID] = digest
}

func prepareRunStartBinaryFixture(t *testing.T, binary string) string {
	t.Helper()
	dir := t.TempDir()
	_, _, err := captureOutput(t, func() error {
		return runRunStart([]string{
			"--project", dir, "--profile", team.DefaultProfile, "--session", "prepared",
			"--roles", "cto", "--binary", "cto=" + binary, "--lead", "cto",
			"--launch-shape", runwizard.LaunchShapeWorkingTeamTogether,
			"--goal", "Execute the accepted readiness fixture",
			"--visibility", "detached", "--prepare",
		}, "test")
	})
	if err != nil {
		t.Fatalf("prepare %s fixture: %v", binary, err)
	}
	return dir
}

func writeAuthoredReadinessRole(t *testing.T, dir, roleID, body string) {
	t.Helper()
	path := team.CustomRolePath(dir, roleID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readinessRowStatus(result runReadinessResult, artifact string) string {
	return readinessRow(result, artifact).Status
}

func readinessRow(result runReadinessResult, artifact string) runReadinessRow {
	for _, row := range result.Rows {
		if row.Artifact == artifact {
			return row
		}
	}
	return runReadinessRow{}
}
