package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
)

func TestPreparedRunMixedSessionRosterIsExactAcrossDefaultAndNamedProfiles(t *testing.T) {
	for _, profile := range []string{team.DefaultProfile, "named"} {
		t.Run(profile, func(t *testing.T) {
			dir := t.TempDir()
			session := "focus"
			tm := team.Team{
				Project: dir, Orchestrated: true, Lead: "cto", ExecutionMode: executionModeProjectLead,
				Members: []team.Member{
					{Role: "cto", Handle: "cto", Binary: "codex", Session: session, CWD: dir},
					{Role: "qa", Handle: "qa", Binary: "claude", Session: "other", CWD: dir},
				},
			}
			if err := team.WriteProfile(dir, profile, tm); err != nil {
				t.Fatal(err)
			}

			baseArgs := []string{
				"--project", dir, "--profile", profile, "--session", session,
				"--launch-shape", runwizard.LaunchShapeWorkingTeamTogether,
				"--goal", "Launch only the exact focus roster", "--visibility", visibilityDetached,
			}
			_, _, err := captureOutput(t, func() error {
				return runRunStart(append(append([]string(nil), baseArgs...), "--prepare-plan"), "test")
			})
			if err == nil || !strings.Contains(err.Error(), "add it to --staged-roles") {
				t.Fatalf("other-session member without explicit staging error = %v", err)
			}
			if _, statErr := os.Stat(preparedRunPath(dir, profile, session)); !os.IsNotExist(statErr) {
				t.Fatalf("rejected proposal wrote a manifest: %v", statErr)
			}

			acceptedArgs := append(append([]string(nil), baseArgs...), "--staged-roles", "qa")
			proposalOut, _, err := captureOutput(t, func() error {
				return runRunStart(append(append([]string(nil), acceptedArgs...), "--prepare-plan"), "test")
			})
			if err != nil {
				t.Fatalf("accepted proposal: %v", err)
			}
			for _, want := range []string{"Initial launch: 1 members - cto", "Staged for later: 1 roles - qa", "bootstrap:cto"} {
				if !strings.Contains(proposalOut, want) {
					t.Fatalf("proposal missing %q:\n%s", want, proposalOut)
				}
			}
			if strings.Contains(proposalOut, "bootstrap:qa") {
				t.Fatalf("other-session staged member received an initial bootstrap row:\n%s", proposalOut)
			}

			if _, _, err := captureOutput(t, func() error {
				return runRunStart(append(append([]string(nil), acceptedArgs...), "--prepare"), "test")
			}); err != nil {
				t.Fatalf("prepare: %v", err)
			}
			manifest, err := readPreparedRunManifest(dir, profile, session)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(manifest.InitialRoster, []string{"cto"}) || !reflect.DeepEqual(manifest.StagedRoster, []string{"qa"}) {
				t.Fatalf("manifest rosters initial=%v staged=%v", manifest.InitialRoster, manifest.StagedRoster)
			}
			if len(manifest.Members) != 1 || manifest.Members["cto"].Role != "cto" || len(manifest.BootstrapDigests) != 1 || manifest.BootstrapDigests["cto"] == "" || len(manifest.BootstrapBindings) != 1 {
				t.Fatalf("manifest exact-session evidence members=%v digests=%v bindings=%v", manifest.Members, manifest.BootstrapDigests, manifest.BootstrapBindings)
			}

			readiness := calculateRunReadiness(dir, profile, session)
			if !readiness.Ready || readiness.InitialCount != 1 || readiness.StagedCount != 1 {
				t.Fatalf("readiness ready=%t initial=%d staged=%d rows=%+v", readiness.Ready, readiness.InitialCount, readiness.StagedCount, readiness.Rows)
			}
			if readinessRowStatus(readiness, "bootstrap:cto") != "ready" || readinessRowStatus(readiness, "staged_role:qa") != "ready" || readinessRowStatus(readiness, "bootstrap:qa") != "" {
				t.Fatalf("readiness bootstrap/staged rows=%+v", readiness.Rows)
			}

			setupFakeAMQSessionRoots(t)
			chdir(t, dir)
			backend := useFakeTmuxBackend(t)
			opts := teamLaunchOptions{
				Terminal: "tmux", Target: "new-session", Layout: "tiled", Workstream: session,
				Profile: profile, Trust: string(trustModeSandboxed), SquadBin: "amq-squad", DryRun: true,
			}
			if _, _, err := captureOutput(t, func() error { return executeTeamLaunch(opts, true, true) }); err != nil {
				t.Fatalf("backend dry-run plan: %v", err)
			}
			opts.DryRun = false
			if _, _, err := captureOutput(t, func() error { return executeTeamLaunch(opts, true, true) }); err != nil {
				t.Fatalf("backend live launch: %v", err)
			}
			if len(backend.dryRuns) != 1 || len(backend.launches) != 1 || len(backend.teams) != 2 {
				t.Fatalf("backend captures dry=%d live=%d teams=%d", len(backend.dryRuns), len(backend.launches), len(backend.teams))
			}
			for _, launched := range backend.teams {
				if roles := teamMemberRoles(launched.Members); !reflect.DeepEqual(roles, []string{"cto"}) {
					t.Fatalf("backend launched roles %v, want exact session roster [cto]", roles)
				}
			}
		})
	}
}

func TestPreparedManagedLeadExactBootstrapEvidenceCodexClaude(t *testing.T) {
	for _, binary := range []string{"codex", "claude"} {
		t.Run(binary, func(t *testing.T) {
			setupFakeAMQSessionRoots(t)
			dir := prepareRunStartBinaryFixture(t, binary)
			manifest, err := readPreparedRunManifest(dir, team.DefaultProfile, "prepared")
			if err != nil {
				t.Fatal(err)
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
				return runLaunch(preparedLeadLaunchArgs(dir, binary))
			})
			if err != nil {
				t.Fatalf("managed dry-run: %v", err)
			}
			prompt := generatedBootstrapPrompt(argv)
			if prompt == "" || digestRunArtifactBytes([]byte(prompt)) != manifest.BootstrapDigests["cto"] {
				t.Fatalf("actual bootstrap digest=%q accepted=%q", digestRunArtifactBytes([]byte(prompt)), manifest.BootstrapDigests["cto"])
			}
			wantMode := map[string]string{"codex": "prompt_goal", "claude": "native_goal"}[binary]
			if observed.GoalBinding == nil || observed.GoalBinding.Mode != wantMode || observed.GoalBinding.Source != "prepared-run" || observed.GoalBinding.DeliveryState != goalBindingDeliveryPrepared || observed.GoalBinding.Goal != manifest.GoalText {
				t.Fatalf("managed planned binding = %+v", observed.GoalBinding)
			}
			if launchRecordHasGoalBinding(observed) {
				t.Fatalf("planned binding was fabricated as delivered: %+v", observed.GoalBinding)
			}
			if !bootstrapHasExactLine(prompt, "- Goal binding: "+wantMode) {
				t.Fatalf("actual bootstrap missing healthy binding mode:\n%s", prompt)
			}
		})
	}
}

func TestPreparedMemberIdentityUsesEffectiveModelAndEffortLayers(t *testing.T) {
	for _, tc := range []struct {
		name       string
		binaryArgs map[string][]string
		memberArgs []string
		defaultCfg string
		wantModel  string
		wantEffort string
	}{
		{
			name: "team-native", binaryArgs: map[string][]string{"codex": {"--model", "team-model", "-c", "model_reasoning_effort=low"}},
			wantModel: "team-model", wantEffort: "low",
		},
		{
			name: "member-native-wins", binaryArgs: map[string][]string{"codex": {"--model", "team-model", "-c", "model_reasoning_effort=low"}},
			memberArgs: []string{"--model", "member-model", "-c", "model_reasoning_effort=high"},
			wantModel:  "member-model", wantEffort: "high",
		},
		{name: "configured-default", defaultCfg: `{"models":{"codex":"configured-model"}}`, wantModel: "configured-model", wantEffort: effortAutomatic},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.defaultCfg != "" {
				configDir, home := t.TempDir(), t.TempDir()
				writeFile(t, filepath.Join(configDir, "amq-squad", "config.json"), tc.defaultCfg)
				withModelLookupRoots(t, configDir, home, map[string]string{})
			}
			dir := t.TempDir()
			tm := team.Team{
				Project: dir, Orchestrated: true, Lead: "cto", ExecutionMode: executionModeProjectLead,
				BinaryArgs: tc.binaryArgs,
				Members:    []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: "prepared", CWD: dir, CodexArgs: tc.memberArgs}},
			}
			if err := team.WriteProfile(dir, team.DefaultProfile, tm); err != nil {
				t.Fatal(err)
			}
			if _, _, err := captureOutput(t, func() error {
				return runRunStart([]string{
					"--project", dir, "--profile", team.DefaultProfile, "--session", "prepared",
					"--launch-shape", runwizard.LaunchShapeWorkingTeamTogether,
					"--goal", "Preserve effective model and effort", "--visibility", visibilityDetached, "--prepare",
				}, "test")
			}); err != nil {
				t.Fatalf("prepare: %v", err)
			}
			manifest, err := readPreparedRunManifest(dir, team.DefaultProfile, "prepared")
			if err != nil {
				t.Fatal(err)
			}
			identity := manifest.Members["cto"]
			if identity.Model != tc.wantModel || identity.Effort != tc.wantEffort {
				t.Fatalf("effective identity model=%q effort=%q want model=%q effort=%q", identity.Model, identity.Effort, tc.wantModel, tc.wantEffort)
			}
			context := acceptedRunContext{Version: "test", Topology: manifest.Topology}
			if err := validatePreparedLaunchBootstrapInputs(dir, team.DefaultProfile, "prepared", context, "", "", "", ""); err != nil {
				t.Fatalf("unchanged effective launch identity rejected: %v", err)
			}
			if tc.name == "configured-default" {
				if err := validatePreparedLaunchBootstrapInputs(dir, team.DefaultProfile, "prepared", context, "cto=unaccepted-model", "", "", ""); err == nil || !strings.Contains(err.Error(), "identity drift") {
					t.Fatalf("unaccepted model override error = %v", err)
				}
				if err := validatePreparedLaunchBootstrapInputs(dir, team.DefaultProfile, "prepared", context, "", "cto=high", "", ""); err == nil || !strings.Contains(err.Error(), "identity drift") {
					t.Fatalf("unaccepted effort override error = %v", err)
				}
			}
		})
	}
}

func TestPreparedManagedLaunchDriftFailsBeforeObserver(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*testing.T, string)
		extra  []string
	}{
		{name: "brief", mutate: func(t *testing.T, dir string) {
			if err := os.WriteFile(briefPathForProfile(dir, team.DefaultProfile, "prepared"), []byte("# drifted brief\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "profile", mutate: func(t *testing.T, dir string) {
			tm, err := team.ReadProfile(dir, team.DefaultProfile)
			if err != nil {
				t.Fatal(err)
			}
			tm.Members[0].Handle = "changed"
			if err := team.WriteProfile(dir, team.DefaultProfile, tm); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "binding", mutate: func(t *testing.T, dir string) {
			mutatePreparedManifest(t, dir, func(m *preparedRunManifest) { m.BootstrapBindings["cto"] = "Goal binding: prompt_goal_missing" })
		}},
		{name: "digest", mutate: func(t *testing.T, dir string) {
			mutatePreparedManifest(t, dir, func(m *preparedRunManifest) { m.BootstrapDigests["cto"] = strings.Repeat("0", 64) })
		}},
		{name: "no-bootstrap", extra: []string{"--no-bootstrap"}},
		{name: "handle", extra: []string{"--me", "wrong"}},
		{name: "model", extra: []string{"--model", "drifted-model"}},
		{name: "tool-policy", extra: []string{"--tool-profile", "full", "--tool-config", "drifted-policy"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setupFakeAMQSessionRoots(t)
			dir := prepareRunStartBinaryFixture(t, "codex")
			if tc.mutate != nil {
				tc.mutate(t, dir)
			}
			calls := 0
			oldObserver := launchPlanObserver
			launchPlanObserver = func(launch.Record, []string) { calls++ }
			t.Cleanup(func() { launchPlanObserver = oldObserver })
			args := preparedLeadLaunchArgs(dir, "codex")
			if len(tc.extra) > 0 {
				args = append(args[:len(args)-1], append(tc.extra, args[len(args)-1])...)
			}
			_, _, err := captureOutput(t, func() error { return runLaunch(args) })
			if err == nil {
				t.Fatal("drifted prepared launch unexpectedly succeeded")
			}
			if calls != 0 {
				t.Fatalf("drift reached launch observer %d times: %v", calls, err)
			}
		})
	}
}

func TestDirectLaunchWithoutPreparedManifestPreservesLegacyBehavior(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	dir := t.TempDir()
	var observed launch.Record
	calls := 0
	oldObserver := launchPlanObserver
	launchPlanObserver = func(rec launch.Record, _ []string) { observed, calls = rec, calls+1 }
	t.Cleanup(func() { launchPlanObserver = oldObserver })
	_, _, err := captureOutput(t, func() error {
		return runLaunch([]string{
			"--project", dir, "--team-home", dir, "--role", "cto", "--me", "cto",
			"--session", "legacy", "--trust", "sandboxed", "--dry-run", "codex",
		})
	})
	if err != nil || calls != 1 || observed.GoalBinding != nil {
		t.Fatalf("legacy no-manifest launch err=%v calls=%d binding=%+v", err, calls, observed.GoalBinding)
	}
}

func TestPreparedBootstrapRevalidationNeverDowngradesToLegacyRouting(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	dir := prepareRunStartBinaryFixture(t, "codex")
	var observed launch.Record
	var argv []string
	oldObserver := launchPlanObserver
	launchPlanObserver = func(rec launch.Record, args []string) {
		observed = rec
		argv = append([]string(nil), args...)
	}
	t.Cleanup(func() { launchPlanObserver = oldObserver })
	if _, _, err := captureOutput(t, func() error { return runLaunch(preparedLeadLaunchArgs(dir, "codex")) }); err != nil {
		t.Fatalf("capture accepted prepared launch: %v", err)
	}
	expected, err := preparedContextForLaunchRecord(observed)
	if err != nil || expected == nil {
		t.Fatalf("capture prepared context: context=%+v err=%v", expected, err)
	}
	if err := os.Remove(preparedRunPath(dir, team.DefaultProfile, "prepared")); err != nil {
		t.Fatal(err)
	}
	err = revalidatePreparedBootstrapPromptForLaunch(observed, generatedBootstrapPrompt(argv), expected)
	if err == nil || !strings.Contains(err.Error(), "disappeared before bootstrap validation") {
		t.Fatalf("prepared manifest disappearance error = %v", err)
	}
}

func TestPreparedExternalLeadRecordEvidenceCodexClaude(t *testing.T) {
	for _, binary := range []string{"codex", "claude"} {
		t.Run(binary, func(t *testing.T) {
			effectiveModel := binary + "-native-model"
			dir := seedTeam(t, team.Team{
				Project: "", Orchestrated: true, Lead: "cto",
				BinaryArgs: map[string][]string{binary: {"--model", effectiveModel}},
				Members: []team.Member{
					{Role: "cto", Binary: binary, Handle: "cto", Session: "sess"},
					{Role: "qa", Binary: "codex", Handle: "qa", Session: "sess"},
				},
			})
			backend := useFakeTmuxBackend(t)
			stubCurrentRunStartPane(t, "%42")
			stubRunStartLeadWake(t)
			liveArgs := prepareRunStartTestInvocation(t, []string{"-p", dir, "-s", "sess", "--external-lead", "--go"})
			if _, _, err := captureOutput(t, func() error { return runRunStart(liveArgs, "test") }); err != nil {
				t.Fatalf("external lead go: %v", err)
			}
			manifest, err := readPreparedRunManifest(dir, team.DefaultProfile, "sess")
			if err != nil {
				t.Fatal(err)
			}
			recordDir := filepath.Join(squadnamespace.AMQRoot(dir, team.DefaultProfile, "sess"), "agents", "cto")
			rec, err := launch.Read(recordDir)
			if err != nil {
				t.Fatalf("external record: %v", err)
			}
			agentDir := filepath.Join(rec.Root, "agents", rec.Handle)
			wantMode := map[string]string{"codex": "prompt_goal", "claude": "native_goal"}[binary]
			if !rec.External || rec.Role != manifest.Members["cto"].Role || rec.Handle != manifest.Members["cto"].Handle || rec.Binary != manifest.Members["cto"].Binary || rec.Model != effectiveModel || manifest.Members["cto"].Model != effectiveModel || rec.GoalBinding == nil || rec.GoalBinding.Mode != wantMode || rec.GoalBinding.Source != "prepared-run" || rec.GoalBinding.DeliveryState != goalBindingDeliveryPrepared || rec.GoalBinding.Goal != manifest.GoalText {
				t.Fatalf("external accepted record identity/binding = %+v", rec)
			}
			if launchRecordHasGoalBinding(rec) {
				t.Fatalf("external planned binding was fabricated as delivered: %+v", rec.GoalBinding)
			}
			tm, err := team.ReadProfile(dir, team.DefaultProfile)
			if err != nil {
				t.Fatal(err)
			}
			active, _ := filterMembersBySession(tm.Members, "sess")
			tm.Members = active
			binding := acceptedGoalBinding{
				Text: manifest.GoalText, Source: manifest.GoalSource,
				Namespace: manifest.GoalNamespace, Digest: manifest.GoalDigest,
			}
			acceptedPrompt, err := preparedBootstrap(dir, team.DefaultProfile, "sess", binding, tm, tm.Members[0], acceptedRunContext{Version: "test", Topology: manifest.Topology})
			if err != nil {
				t.Fatal(err)
			}
			prompt, err := buildBootstrapPrompt(bootstrapContextFor(rec, agentDir, dir))
			if err != nil {
				t.Fatal(err)
			}
			if got := digestRunArtifactBytes([]byte(prompt)); got != manifest.BootstrapDigests["cto"] {
				t.Fatalf("external bootstrap digest=%q accepted=%q; %s", got, manifest.BootstrapDigests["cto"], firstBootstrapPromptDifference(acceptedPrompt, prompt))
			}
			if err := validatePreparedBootstrapPromptForLaunch(rec, prompt); err != nil {
				t.Fatalf("external exact bootstrap validation: %v", err)
			}
			roles := teamMemberRoles(backend.teams[len(backend.teams)-1].Members)
			if len(backend.launches) != 1 || !reflect.DeepEqual(roles, []string{"qa"}) {
				t.Fatalf("external worker launch roles=%v launches=%d", roles, len(backend.launches))
			}
		})
	}
}

func TestPreparedExternalLeadMismatchWritesNoRecordOrPane(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Project: "", Orchestrated: true, Lead: "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "sess"},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "sess"},
		},
	})
	backend := useFakeTmuxBackend(t)
	stubCurrentRunStartPane(t, "%42")
	stubRunStartLeadWake(t)
	liveArgs := prepareRunStartTestInvocation(t, []string{"-p", dir, "-s", "sess", "--external-lead", "--go"})
	mutatePreparedManifest(t, dir, func(m *preparedRunManifest) { m.BootstrapDigests["cto"] = strings.Repeat("0", 64) })
	_, _, err := captureOutput(t, func() error { return runRunStart(liveArgs, "test") })
	if err == nil {
		t.Fatal("tampered external bootstrap unexpectedly launched")
	}
	if len(backend.launches) != 0 {
		t.Fatalf("tampered external bootstrap opened panes: %+v", backend.launches)
	}
	agentDir := filepath.Join(squadnamespace.AMQRoot(dir, team.DefaultProfile, "sess"), "agents", "cto")
	if _, readErr := launch.Read(agentDir); !os.IsNotExist(readErr) {
		t.Fatalf("tampered external bootstrap wrote a record: %v", readErr)
	}
}

func preparedLeadLaunchArgs(dir, binary string) []string {
	return []string{
		"--project", dir, "--team-home", dir, "--team-profile", team.DefaultProfile,
		"--role", "cto", "--me", "cto", "--session", "prepared",
		"--trust", "sandboxed", "--dry-run", binary,
	}
}

func generatedBootstrapPrompt(argv []string) string {
	for i := len(argv) - 1; i >= 0; i-- {
		if strings.Contains(argv[i], "Goal binding:") {
			return argv[i]
		}
	}
	return ""
}

func mutatePreparedManifest(t *testing.T, dir string, mutate func(*preparedRunManifest)) {
	t.Helper()
	manifest, err := readPreparedRunManifest(dir, team.DefaultProfile, "prepared")
	if err != nil {
		// External fixtures use their own session name.
		manifest, err = readPreparedRunManifest(dir, team.DefaultProfile, "sess")
	}
	if err != nil {
		t.Fatal(err)
	}
	mutate(&manifest)
	if err := writePreparedRunManifest(preparedRunPath(dir, manifest.Profile, manifest.Session), manifest); err != nil {
		t.Fatal(err)
	}
}
