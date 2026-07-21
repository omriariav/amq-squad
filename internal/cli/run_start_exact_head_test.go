package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/bootstrapack"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
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
			if len(manifest.Members) != 1 || manifest.Members["cto"].Role != "cto" || manifest.StagedMembers["qa"].Role != "qa" || len(manifest.BootstrapDigests) != 2 || manifest.BootstrapDigests["cto"] == "" || manifest.BootstrapDigests["qa"] == "" || len(manifest.BootstrapBindings) != 2 {
				t.Fatalf("manifest exact-session evidence members=%v digests=%v bindings=%v", manifest.Members, manifest.BootstrapDigests, manifest.BootstrapBindings)
			}

			readiness := calculateRunReadiness(dir, profile, session)
			if !readiness.Ready || readiness.InitialCount != 1 || readiness.StagedCount != 1 {
				t.Fatalf("readiness ready=%t initial=%d staged=%d rows=%+v", readiness.Ready, readiness.InitialCount, readiness.StagedCount, readiness.Rows)
			}
			if readinessRowStatus(readiness, "bootstrap:cto") != "ready" || readinessRowStatus(readiness, "staged_role:qa") != "ready" || readinessRowStatus(readiness, "bootstrap:qa") != "ready" {
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

func TestPreparedDirectLiveLaunchRejectsCodexClaudeNativeAndToolDriftBeforeSideEffects(t *testing.T) {
	for _, tc := range []struct {
		binary string
		name   string
		extra  []string
	}{
		{binary: "codex", name: "native-effort", extra: []string{"--codex-args=-c model_reasoning_effort=low"}},
		{binary: "claude", name: "native-effort", extra: []string{"--claude-args=--effort low"}},
		{binary: "codex", name: "tool-allow", extra: []string{"--tool-allow", "mcp:unaccepted"}},
		{binary: "claude", name: "tool-allow", extra: []string{"--tool-allow", "mcp:unaccepted"}},
		{binary: "codex", name: "tool-block", extra: []string{"--tool-block", "mcp:unaccepted"}},
		{binary: "claude", name: "tool-block", extra: []string{"--tool-block", "mcp:unaccepted"}},
	} {
		t.Run(tc.binary+"/"+tc.name, func(t *testing.T) {
			setupFakeAMQSessionRoots(t)
			dir := prepareRunStartBinaryFixture(t, tc.binary)
			t.Setenv(envTmuxTarget, "new-session")
			t.Setenv("TMUX", "/tmp/fake-tmux,1,0")
			t.Setenv("TMUX_PANE", "%42")
			oldPane := launchCurrentPaneIdentity
			launchCurrentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
				return &tmuxpane.PaneIdentity{Session: "managed", WindowID: "@1", WindowName: "cto", PaneID: "%42"}, nil
			}
			t.Cleanup(func() { launchCurrentPaneIdentity = oldPane })
			oldTerminal := launchStdinIsTerminal
			launchStdinIsTerminal = func() bool { return true }
			t.Cleanup(func() { launchStdinIsTerminal = oldTerminal })
			observerCalls := 0
			oldObserver := launchPlanObserver
			launchPlanObserver = func(launch.Record, []string) { observerCalls++ }
			t.Cleanup(func() { launchPlanObserver = oldObserver })
			execCalls := 0
			oldExec := amqSyscallExec
			amqSyscallExec = func(string, []string, []string) error { execCalls++; return nil }
			t.Cleanup(func() { amqSyscallExec = oldExec })
			args := withoutArg(preparedLeadLaunchArgs(dir, tc.binary), "--dry-run")
			args = append(args[:len(args)-1], append(tc.extra, args[len(args)-1])...)
			agentUpArgs := append([]string{tc.binary}, args[:len(args)-1]...)
			_, _, err := captureOutput(t, func() error { return runAgentUp(agentUpArgs) })
			if err == nil || !strings.Contains(err.Error(), "actual launch record input") {
				t.Fatalf("unaccepted direct %s input error = %v", tc.binary, err)
			}
			if observerCalls != 0 || execCalls != 0 {
				t.Fatalf("unaccepted direct %s input reached observer=%d exec=%d", tc.binary, observerCalls, execCalls)
			}
			env, envErr := resolveAMQEnvForTeamLaunchProfile(dir, team.DefaultProfile, "prepared", "cto")
			if envErr != nil {
				t.Fatal(envErr)
			}
			agentDir := filepath.Join(env.Root, "agents", "cto")
			if _, readErr := launch.Read(agentDir); !os.IsNotExist(readErr) {
				t.Fatalf("unaccepted direct %s input left launch record: %v", tc.binary, readErr)
			}
		})
	}

	t.Run("claude-worker-no-preauthorize", func(t *testing.T) {
		setupFakeAMQSessionRoots(t)
		dir := t.TempDir()
		if _, _, err := captureOutput(t, func() error {
			return runRunStart([]string{
				"--project", dir, "--profile", team.DefaultProfile, "--session", "prepared",
				"--roles", "cto,qa", "--binary", "cto=codex,qa=claude", "--lead", "cto",
				"--launch-shape", runwizard.LaunchShapeWorkingTeamTogether,
				"--goal", "Freeze complete Claude launcher authority", "--visibility", visibilityDetached, "--prepare",
			}, "test")
		}); err != nil {
			t.Fatal(err)
		}
		manifest, err := readPreparedRunManifest(dir, team.DefaultProfile, "prepared")
		if err != nil {
			t.Fatal(err)
		}
		if len(manifest.Members["qa"].LauncherAuthority) == 0 {
			t.Fatal("prepared Claude worker did not freeze built-in launcher authority")
		}
		calls := 0
		oldObserver := launchPlanObserver
		launchPlanObserver = func(launch.Record, []string) { calls++ }
		t.Cleanup(func() { launchPlanObserver = oldObserver })
		_, _, err = captureOutput(t, func() error {
			return runLaunch([]string{
				"--project", dir, "--team-home", dir, "--team-profile", team.DefaultProfile,
				"--role", "qa", "--me", "qa", "--session", "prepared", "--trust", trustModeApproveForMe,
				"--no-preauthorize-inscope", "--dry-run", "claude",
			})
		})
		if err == nil || calls != 0 {
			t.Fatalf("Claude authority opt-out err=%v observer_calls=%d", err, calls)
		}
	})
}

func TestPreparedManifestMutationOrRemovalAfterRecordWriteRollsBackWithoutExec(t *testing.T) {
	for _, action := range []string{"mutate", "remove"} {
		t.Run(action, func(t *testing.T) {
			setupFakeAMQSessionRoots(t)
			dir := prepareRunStartBinaryFixture(t, "codex")
			t.Setenv(envTmuxTarget, "new-session")
			t.Setenv("TMUX", "/tmp/fake-tmux,1,0")
			t.Setenv("TMUX_PANE", "%42")
			oldPane := launchCurrentPaneIdentity
			launchCurrentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
				return &tmuxpane.PaneIdentity{Session: "managed", WindowID: "@1", WindowName: "cto", PaneID: "%42"}, nil
			}
			t.Cleanup(func() { launchCurrentPaneIdentity = oldPane })
			oldTerminal := launchStdinIsTerminal
			launchStdinIsTerminal = func() bool { return true }
			t.Cleanup(func() { launchStdinIsTerminal = oldTerminal })
			oldAfterWrite := preparedLaunchAfterRecordWrite
			preparedLaunchAfterRecordWrite = func(launch.Record) error {
				path := preparedRunPath(dir, team.DefaultProfile, "prepared")
				if action == "remove" {
					return os.Remove(path)
				}
				manifest, err := readPreparedRunManifest(dir, team.DefaultProfile, "prepared")
				if err != nil {
					return err
				}
				manifest.PreparedAt = manifest.PreparedAt.Add(time.Second)
				manifest = nextPreparedRunManifestForTest(t, manifest)
				return writePreparedRunManifest(path, manifest)
			}
			t.Cleanup(func() { preparedLaunchAfterRecordWrite = oldAfterWrite })
			execCalls := 0
			oldExec := amqSyscallExec
			amqSyscallExec = func(string, []string, []string) error { execCalls++; return nil }
			t.Cleanup(func() { amqSyscallExec = oldExec })
			token := reservedPreparedRunTokenForTest(t, dir, team.DefaultProfile, "prepared")
			args := withoutArg(preparedLeadLaunchArgs(dir, "codex"), "--dry-run")
			_, _, err := captureOutput(t, func() error { return runLaunchWithPreparedToken(args, token) })
			if err == nil || !strings.Contains(err.Error(), "accepted prepared launch identity") {
				t.Fatalf("post-write manifest %s error = %v", action, err)
			}
			if execCalls != 0 {
				t.Fatalf("post-write manifest %s executed %d times", action, execCalls)
			}
			env, envErr := resolveAMQEnvForTeamLaunchProfile(dir, team.DefaultProfile, "prepared", "cto")
			if envErr != nil {
				t.Fatal(envErr)
			}
			agentDir := filepath.Join(env.Root, "agents", "cto")
			if _, readErr := launch.Read(agentDir); !os.IsNotExist(readErr) {
				t.Fatalf("post-write manifest %s left launch record: %v", action, readErr)
			}
		})
	}
}

func TestPreparedLaunchRollbackPreservesConcurrentRecordReplacement(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	dir := prepareRunStartBinaryFixture(t, "codex")
	t.Setenv(envTmuxTarget, "new-session")
	t.Setenv("TMUX", "/tmp/fake-tmux,1,0")
	t.Setenv("TMUX_PANE", "%42")
	oldPane := launchCurrentPaneIdentity
	launchCurrentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		return &tmuxpane.PaneIdentity{Session: "managed", WindowID: "@1", WindowName: "cto", PaneID: "%42"}, nil
	}
	t.Cleanup(func() { launchCurrentPaneIdentity = oldPane })
	oldTerminal := launchStdinIsTerminal
	launchStdinIsTerminal = func() bool { return true }
	t.Cleanup(func() { launchStdinIsTerminal = oldTerminal })
	env, err := resolveAMQEnvForTeamLaunchProfile(dir, team.DefaultProfile, "prepared", "cto")
	if err != nil {
		t.Fatal(err)
	}
	agentDir := filepath.Join(env.Root, "agents", "cto")
	oldAfterWrite := preparedLaunchAfterRecordWrite
	preparedLaunchAfterRecordWrite = func(rec launch.Record) error {
		replacement := rec
		replacement.Conversation = "concurrent-replacement"
		replacement.StartedAt = replacement.StartedAt.Add(time.Second)
		if err := launch.Write(agentDir, replacement); err != nil {
			return err
		}
		return os.Remove(preparedRunPath(dir, team.DefaultProfile, "prepared"))
	}
	t.Cleanup(func() { preparedLaunchAfterRecordWrite = oldAfterWrite })
	execCalls := 0
	oldExec := amqSyscallExec
	amqSyscallExec = func(string, []string, []string) error { execCalls++; return nil }
	t.Cleanup(func() { amqSyscallExec = oldExec })
	token := reservedPreparedRunTokenForTest(t, dir, team.DefaultProfile, "prepared")
	_, _, err = captureOutput(t, func() error {
		return runLaunchWithPreparedToken(withoutArg(preparedLeadLaunchArgs(dir, "codex"), "--dry-run"), token)
	})
	if err == nil || execCalls != 0 {
		t.Fatalf("post-write drift err=%v exec_calls=%d", err, execCalls)
	}
	stored, readErr := launch.Read(agentDir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if stored.Conversation != "concurrent-replacement" {
		t.Fatalf("rollback overwrote concurrent replacement: %+v", stored)
	}
}

func TestPreparedManifestLockOrderFreshPreparationAndDirectLaunchDoNotDeadlock(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	dir := t.TempDir()
	writerReady := make(chan struct{})
	releaseWriter := make(chan struct{})
	oldWriterSeam := preparedManifestWriterAcquired
	preparedManifestWriterAcquired = func(string, string, string) error {
		close(writerReady)
		<-releaseWriter
		return nil
	}
	t.Cleanup(func() { preparedManifestWriterAcquired = oldWriterSeam })
	prepareDone := make(chan error, 1)
	go func() {
		prepareDone <- runRunStart([]string{
			"--project", dir, "--profile", team.DefaultProfile, "--session", "prepared",
			"--roles", "cto", "--binary", "cto=codex", "--lead", "cto",
			"--launch-shape", runwizard.LaunchShapeWorkingTeamTogether,
			"--goal", "Prove nested fresh preparation lock order", "--visibility", visibilityDetached, "--prepare",
		}, "test")
	}()
	select {
	case <-writerReady:
	case <-time.After(5 * time.Second):
		t.Fatal("fresh preparation did not reach prepared-manifest admission")
	}
	launchDone := make(chan error, 1)
	go func() {
		launchDone <- runLaunch([]string{
			"--project", dir, "--team-home", dir, "--role", "cto", "--me", "cto",
			"--session", "prepared", "--trust", trustModeApproveForMe, "codex",
		})
	}()
	select {
	case err := <-launchDone:
		if err == nil || !strings.Contains(err.Error(), "prepared.lock") {
			t.Fatalf("direct launch while preparation owns manifest admission error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("direct launch deadlocked behind fresh preparation")
	}
	close(releaseWriter)
	select {
	case err := <-prepareDone:
		if err != nil {
			t.Fatalf("fresh nested preparation failed after release: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("fresh nested preparation deadlocked after release")
	}
}

func TestRunStartPinnedAdmissionsSerializePublicPreparationThroughGoal(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Project: "", Orchestrated: true, Lead: "cto",
		Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: "sess"}},
	})
	base := []string{
		"--project", dir, "--profile", team.DefaultProfile, "--session", "sess",
		"--launch-shape", runwizard.LaunchShapeWorkingTeamTogether,
		"--goal", "Keep one accepted generation", "--visibility", visibilityDetached,
	}
	if _, _, err := captureOutput(t, func() error { return runRunStart(append(append([]string{}, base...), "--prepare"), "test") }); err != nil {
		t.Fatalf("initial prepare: %v", err)
	}
	writerWaiting := make(chan struct{})
	oldBefore := preparedManifestWriterBeforeAdmission
	preparedManifestWriterBeforeAdmission = func(string, string, string) error {
		close(writerWaiting)
		return nil
	}
	t.Cleanup(func() { preparedManifestWriterBeforeAdmission = oldBefore })
	prepareDone := make(chan error, 1)
	oldUp := runStartUpWithVersion
	runStartUpWithVersion = func([]string, string) error {
		go func() {
			prepareDone <- runRunStart(append(append([]string{}, base...), "--prepare"), "test")
		}()
		select {
		case <-writerWaiting:
		case <-time.After(5 * time.Second):
			return fmt.Errorf("public prepare did not reach writer admission")
		}
		select {
		case err := <-prepareDone:
			return fmt.Errorf("public prepare escaped pinned reader admission: %v", err)
		case <-time.After(50 * time.Millisecond):
			return nil
		}
	}
	oldGoal := runStartGoalWithVersion
	oldReady := runStartLeadReadyCheck
	runStartGoalWithVersion = func([]string, string) error { return nil }
	runStartLeadReadyCheck = func(string, string, string, string) (runStartLeadReadiness, error) {
		return runStartLeadReadiness{Ready: true}, nil
	}
	t.Cleanup(func() {
		runStartUpWithVersion = oldUp
		runStartGoalWithVersion = oldGoal
		runStartLeadReadyCheck = oldReady
	})
	if err := runRunStart(append(append([]string{}, base...), "--go"), "test"); err != nil {
		t.Fatalf("pinned go: %v", err)
	}
	select {
	case err := <-prepareDone:
		if err != nil {
			t.Fatalf("serialized public prepare: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("public prepare remained blocked after pinned run returned")
	}
}

func TestPreparedExternalBindingPreservesOnlySameDeliveredGoalAttemptAndRecordIdentity(t *testing.T) {
	contract, err := goalDeliveryContractForBinary("codex")
	if err != nil {
		t.Fatal(err)
	}
	tm := team.Team{Project: "/project", Orchestrated: true, Lead: "cto", Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: "sess"}}}
	plannedRecord := launch.Record{
		CWD: "/project", Binary: "codex", Session: "sess", Handle: "cto", Role: "cto", Root: "/mail/sess",
		TeamProfile: team.DefaultProfile, External: true, AdoptionMode: adoptionModeExternalProjectLead,
		Tmux: &launch.TmuxInfo{PaneID: "%42", Session: "tmux", Target: "external"},
	}
	plannedBinding := func(goal, attempt string) *launch.GoalBinding {
		prompt := contract.prompt(goal, tm, team.DefaultProfile, "sess", "cto", attempt)
		return contract.binding(goal, attempt, prompt, "prepared-run", "accepted")
	}
	deliveredBinding := func(goal, attempt string) *launch.GoalBinding {
		prompt := contract.prompt(goal, tm, team.DefaultProfile, "sess", "cto", attempt)
		binding := contract.binding(goal, attempt, prompt, "goal-control", "delivered")
		binding.DeliveryState = goalBindingDeliveryDelivered
		return binding
	}
	for _, tc := range []struct {
		name             string
		deliveredGoal    string
		deliveredAttempt string
		mutateIdentity   func(*launch.Record)
		wantPreserved    bool
	}{
		{name: "same-goal-same-attempt", deliveredGoal: "ship", deliveredAttempt: "attempt-1", wantPreserved: true},
		{name: "different-goal", deliveredGoal: "old", deliveredAttempt: "attempt-1"},
		{name: "different-attempt", deliveredGoal: "ship", deliveredAttempt: "attempt-old"},
		{name: "different-pane-identity", deliveredGoal: "ship", deliveredAttempt: "attempt-1", mutateIdentity: func(rec *launch.Record) { rec.Tmux.PaneID = "%99" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agentDir := filepath.Join(t.TempDir(), "agents", "cto")
			planned := plannedRecord
			planned.Tmux = &launch.TmuxInfo{PaneID: "%42", Session: "tmux", Target: "external"}
			planned.GoalBinding = plannedBinding("ship", "attempt-1")
			current := planned
			current.Tmux = &launch.TmuxInfo{PaneID: "%42", Session: "tmux", Target: "external"}
			current.GoalBinding = deliveredBinding(tc.deliveredGoal, tc.deliveredAttempt)
			if tc.mutateIdentity != nil {
				tc.mutateIdentity(&current)
			}
			if err := launch.Write(agentDir, current); err != nil {
				t.Fatal(err)
			}
			if _, err := writeExternalLeadLaunchRecord(agentDir, planned, "cto", "sess"); err != nil {
				t.Fatal(err)
			}
			stored, err := launch.Read(agentDir)
			if err != nil {
				t.Fatal(err)
			}
			preserved := stored.GoalBinding != nil && stored.GoalBinding.DeliveryState == goalBindingDeliveryDelivered
			if preserved != tc.wantPreserved {
				t.Fatalf("preserved=%t want=%t binding=%+v", preserved, tc.wantPreserved, stored.GoalBinding)
			}
			if !tc.wantPreserved && !reflect.DeepEqual(stored.GoalBinding, planned.GoalBinding) {
				t.Fatalf("mismatched prior binding did not persist planned state: got=%+v want=%+v", stored.GoalBinding, planned.GoalBinding)
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
			_, manifestDigest, err := readPreparedRunManifestSnapshot(dir, team.DefaultProfile, "sess")
			if err != nil {
				t.Fatal(err)
			}
			wantToken := preparedRunTokenFromSnapshot(manifest, manifestDigest)
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
			recordToken := preparedRunTokenFromRecord(rec)
			if !samePreparedRunGeneration(recordToken, wantToken) {
				t.Fatalf("external record generation=%+v want=%+v", recordToken, wantToken)
			}
			if err := validatePreparedRunPathID("prepared launch attempt", recordToken.LaunchAttempt); err != nil {
				t.Fatalf("external record launch attempt=%q: %v", recordToken.LaunchAttempt, err)
			}
			if reservation, err := readExactPreparedLaunchReservation(dir, team.DefaultProfile, "sess", recordToken); err != nil || reservation.LaunchAttempt != rec.PreparedRunLaunchAttempt {
				t.Fatalf("external record launch attempt is not reservation-bound: reservation=%+v record=%+v err=%v", reservation, rec, err)
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
			workerToken := backend.launches[0].PreparedRunToken
			if !samePreparedRunGeneration(workerToken, wantToken) || workerToken.LaunchAttempt != recordToken.LaunchAttempt {
				t.Fatalf("external worker token=%+v generation=%+v record=%+v", workerToken, wantToken, recordToken)
			}
		})
	}
}

func TestPreparedRunTokenTransportIsInternalAndRestoreReplayIsExact(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	dir := prepareRunStartBinaryFixture(t, "codex")
	manifest, digest, err := readPreparedRunManifestSnapshot(dir, team.DefaultProfile, "prepared")
	if err != nil {
		t.Fatal(err)
	}
	token := preparedRunTokenFromSnapshot(manifest, digest)
	cmd := emitTeamCommand(emitTeamCommandInput{
		CWD: dir, SquadBin: "amq-squad", TeamHome: dir, Workstream: "prepared", Profile: team.DefaultProfile,
		Member: team.Member{Role: "cto", Handle: "cto", Binary: "codex", CWD: dir}, PreparedRunToken: token,
	})
	if !strings.Contains(cmd, internalPreparedRunTokenEnv+"=") || strings.Contains(cmd, "--prepared-run-") {
		t.Fatalf("prepared token transport must be internal-only: %s", cmd)
	}

	var observed launch.Record
	oldObserver := launchPlanObserver
	launchPlanObserver = func(rec launch.Record, _ []string) { observed = rec }
	t.Cleanup(func() { launchPlanObserver = oldObserver })
	if _, _, err := captureOutput(t, func() error {
		return runLaunchWithPreparedToken(preparedLeadLaunchArgs(dir, "codex"), token)
	}); err != nil {
		t.Fatalf("exact token launch: %v", err)
	}
	if got := preparedRunTokenFromRecord(observed); got != token {
		t.Fatalf("launch record token=%+v want=%+v", got, token)
	}
	restoreCommand := emitCommand(observed)
	if !strings.Contains(restoreCommand, internalPreparedRunTokenEnv+"=") || !strings.Contains(restoreCommand, internalPreparedRunRestoreEnv+"=") || strings.Contains(restoreCommand, "--prepared-run-") {
		t.Fatalf("restore must replay internal token exactly: %s", restoreCommand)
	}

	mutatePreparedManifest(t, dir, func(m *preparedRunManifest) { m.Generation = "replacement-generation" })
	changedManifest, changedDigest, err := readPreparedRunManifestSnapshot(dir, team.DefaultProfile, "prepared")
	if err != nil {
		t.Fatal(err)
	}
	if changed := preparedRunTokenFromSnapshot(changedManifest, changedDigest); changed == token {
		t.Fatalf("test mutation did not replace prepared token: %+v", changed)
	}
	if err := validatePreparedRunToken(token, changedManifest, changedDigest); err == nil {
		t.Fatal("changed manifest unexpectedly validates the original token")
	}
	if _, err := preparedContextForLaunchRecord(observed); err == nil {
		t.Fatal("stale launch record unexpectedly revalidated against replacement manifest")
	}
	observerCalls := 0
	var staleObserved launch.Record
	launchPlanObserver = func(rec launch.Record, _ []string) { observerCalls++; staleObserved = rec }
	_, _, err = captureOutput(t, func() error {
		return runLaunchWithPreparedToken(preparedLeadLaunchArgs(dir, "codex"), token)
	})
	if err == nil || !strings.Contains(err.Error(), "prepared run token changed") || observerCalls != 0 {
		t.Fatalf("stale restore err=%v observer_calls=%d requested=%+v observed=%+v team_home=%q cwd=%q", err, observerCalls, token, preparedRunTokenFromRecord(staleObserved), staleObserved.TeamHome, staleObserved.CWD)
	}
}

func TestPreparedDeliveredManagedRestoreReachesExec(t *testing.T) {
	for _, binary := range []string{"codex", "claude"} {
		for _, conversation := range []string{"", "saved-conversation"} {
			t.Run(binary+"/conversation="+conversation, func(t *testing.T) {
				runPreparedDeliveredManagedRestoreCase(t, binary, conversation)
			})
		}
	}
}

func TestPreparedRestoreRawRecordCASRejectsFullFieldReplacement(t *testing.T) {
	agentDir := t.TempDir()
	rec := launch.Record{CWD: "/project", Binary: "codex", Role: "cto", Handle: "cto", Session: "prepared", BootstrapExpectation: &bootstrapack.Expectation{Required: true}}
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	persisted, err := launch.Read(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	digest := preparedRestoreRecordDigest(persisted)
	replaced := persisted
	replaced.BootstrapExpectation = &bootstrapack.Expectation{Required: false, NotRequiredReason: "replacement"}
	if err := launch.Write(agentDir, replaced); err != nil {
		t.Fatal(err)
	}
	if _, err := writeLaunchRecordWithSnapshot(agentDir, persisted, digest, nil); err == nil || !strings.Contains(err.Error(), "persisted launch record changed before CAS") {
		t.Fatalf("full-field replacement CAS error=%v", err)
	}
}

func TestPreparedRestoreDescriptorTransportFailsClosed(t *testing.T) {
	token := preparedRunToken{Generation: strings.Repeat("a", 32), ManifestDigest: "d", GoalNamespace: "default/s", GoalDigest: "goal", LaunchAttempt: strings.Repeat("b", 32)}
	desc := preparedRestoreDescriptor{Token: token, AttemptID: strings.Repeat("c", 32), RecordDigest: "record", SemanticDigest: "semantic"}
	other := token
	other.Generation = strings.Repeat("d", 32)
	for _, tc := range []struct{ name, tokenEnv, descEnv, needle string }{
		{name: "descriptor-without-token", descEnv: encodePreparedRestoreDescriptor(desc), needle: "descriptor/token mismatch"},
		{name: "incomplete-descriptor", tokenEnv: encodePreparedRunToken(token), descEnv: `{"token":{"generation":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}`, needle: "descriptor is incomplete"},
		{name: "mismatched-token", tokenEnv: encodePreparedRunToken(other), descEnv: encodePreparedRestoreDescriptor(desc), needle: "descriptor/token mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(internalPreparedRunTokenEnv, tc.tokenEnv)
			t.Setenv(internalPreparedRunRestoreEnv, tc.descEnv)
			execs := 0
			oldExec := amqSyscallExec
			amqSyscallExec = func(string, []string, []string) error { execs++; return nil }
			t.Cleanup(func() { amqSyscallExec = oldExec })
			if err := runLaunch(nil); err == nil || !strings.Contains(err.Error(), tc.needle) {
				t.Fatalf("error=%v want %q", err, tc.needle)
			}
			if execs != 0 {
				t.Fatalf("invalid descriptor reached exec=%d", execs)
			}
		})
	}
}

func TestPreparedManagedWorkerSavedConversationRestoreReachesExec(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	dir := t.TempDir()
	originalCWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalCWD); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})
	if _, _, err := captureOutput(t, func() error {
		return runRunStart([]string{"--project", dir, "--profile", team.DefaultProfile, "--session", "prepared", "--roles", "cto,qa", "--binary", "cto=codex,qa=claude", "--lead", "cto", "--launch-shape", runwizard.LaunchShapeWorkingTeamTogether, "--goal", "Execute worker restore fixture", "--visibility", "detached", "--prepare"}, "test")
	}); err != nil {
		t.Fatal(err)
	}
	manifest, digest, err := readPreparedRunManifestSnapshot(dir, team.DefaultProfile, "prepared")
	if err != nil {
		t.Fatal(err)
	}
	token := preparedRunTokenFromSnapshot(manifest, digest)
	launchAttempt, err := reservePreparedRunLaunch(dir, team.DefaultProfile, "prepared", token)
	if err != nil {
		t.Fatalf("reserve launch: %v", err)
	}
	token.LaunchAttempt = launchAttempt
	t.Setenv(envTmuxTarget, "new-window")
	t.Setenv("TMUX", "/tmp/fake-tmux,1,0")
	t.Setenv("TMUX_PANE", "%43")
	oldPane := launchCurrentPaneIdentity
	launchCurrentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		return &tmuxpane.PaneIdentity{Session: "managed", WindowID: "@2", WindowName: "qa", PaneID: "%43"}, nil
	}
	t.Cleanup(func() { launchCurrentPaneIdentity = oldPane })
	oldTerminal := launchStdinIsTerminal
	launchStdinIsTerminal = func() bool { return true }
	t.Cleanup(func() { launchStdinIsTerminal = oldTerminal })
	var execEnvs [][]string
	var execArgv [][]string
	oldExec := amqSyscallExec
	amqSyscallExec = func(_ string, argv, env []string) error {
		execArgv = append(execArgv, append([]string(nil), argv...))
		execEnvs = append(execEnvs, append([]string(nil), env...))
		return nil
	}
	t.Cleanup(func() { amqSyscallExec = oldExec })
	if _, _, err := captureOutput(t, func() error {
		return runLaunchWithPreparedToken(withoutArg(preparedLeadLaunchArgs(dir, "codex"), "--dry-run"), token)
	}); err != nil {
		t.Fatalf("lead launch: %v", err)
	}
	args := []string{"--project", dir, "--team-home", dir, "--team-profile", team.DefaultProfile, "--role", "qa", "--me", "qa", "--session", "prepared", "--trust", trustModeApproveForMe, "claude"}
	if _, _, err := captureOutput(t, func() error { return runLaunchWithPreparedToken(args, token) }); err != nil {
		t.Fatalf("worker launch: %v", err)
	}
	if err := consumePreparedRunGoal(dir, team.DefaultProfile, "prepared", token, "cto"); err != nil {
		t.Fatalf("consume prepared goal: %v", err)
	}
	if err := completePreparedRunLaunch(dir, team.DefaultProfile, "prepared", token); err != nil {
		t.Fatalf("complete launch: %v", err)
	}
	env, err := resolveAMQEnvForTeamLaunchProfile(dir, team.DefaultProfile, "prepared", "qa")
	if err != nil {
		t.Fatal(err)
	}
	agentDir := filepath.Join(env.Root, "agents", "qa")
	rec, err := launch.Read(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	rec.AgentPID = 0
	rec.Conversation = "worker-thread"
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	if err := execRestoreRecord(rec); err != nil {
		t.Fatalf("worker restore: %v", err)
	}
	stored, err := launch.Read(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(execArgv) != 3 || stored.GoalBinding != nil || preparedRunTokenFromRecord(stored) != token || generatedBootstrapPrompt(execArgv[2]) != "" {
		t.Fatalf("worker restore evidence exec=%d binding=%+v token=%+v", len(execArgv), stored.GoalBinding, preparedRunTokenFromRecord(stored))
	}
	const restoreExecIndex = 2
	for _, entry := range execEnvs[restoreExecIndex] {
		if strings.HasPrefix(entry, internalPreparedRunTokenEnv+"=") || strings.HasPrefix(entry, internalPreparedRunRestoreEnv+"=") {
			t.Fatalf("worker transport leaked: %q", entry)
		}
	}
	stored.AgentPID = 0
	if err := launch.Write(agentDir, stored); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(t.TempDir(), "amq-squad-helper")
	wrapperBody := "#!/bin/sh\nPREPARED_RESTORE_HELPER_PATH=" + shellQuote(os.Getenv("PATH")) + " GO_WANT_PREPARED_RESTORE_HELPER=1 exec " + shellQuote(os.Args[0]) + " -test.run=TestPreparedRestoreShellHelper -- \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(wrapperBody), 0o755); err != nil {
		t.Fatal(err)
	}
	oldGenerated := generatedSquadCommandOverride
	generatedSquadCommandOverride = wrapper
	t.Cleanup(func() { generatedSquadCommandOverride = oldGenerated })
	capturePath := filepath.Join(t.TempDir(), "exec-captured")
	t.Setenv("PREPARED_RESTORE_CAPTURE", capturePath)
	emitted := emitCommand(stored)
	cmd := exec.Command("sh", "-c", emitted)
	cmd.Env = os.Environ()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("emitted shell restore: %v\n%s", err, out)
	}
	if b, err := os.ReadFile(capturePath); err != nil || string(b) != "exec-stripped" {
		t.Fatalf("emitted helper capture=%q err=%v", b, err)
	}
}

func TestPreparedRestoreShellHelper(t *testing.T) {
	if os.Getenv("GO_WANT_PREPARED_RESTORE_HELPER") != "1" {
		return
	}
	sep := -1
	for i, arg := range os.Args {
		if arg == "--" {
			sep = i
			break
		}
	}
	if sep < 0 || sep+3 > len(os.Args) || os.Args[sep+1] != "agent" || os.Args[sep+2] != "up" {
		os.Exit(2)
	}
	if err := os.Setenv("PATH", os.Getenv("PREPARED_RESTORE_HELPER_PATH")); err != nil {
		os.Exit(2)
	}
	launchCurrentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		return &tmuxpane.PaneIdentity{Session: "managed", WindowID: "@2", WindowName: "qa", PaneID: "%43"}, nil
	}
	launchStdinIsTerminal = func() bool { return true }
	amqSyscallExec = func(_ string, _ []string, env []string) error {
		for _, entry := range env {
			if strings.HasPrefix(entry, internalPreparedRunTokenEnv+"=") || strings.HasPrefix(entry, internalPreparedRunRestoreEnv+"=") {
				return fmt.Errorf("internal transport leaked")
			}
		}
		return os.WriteFile(os.Getenv("PREPARED_RESTORE_CAPTURE"), []byte("exec-stripped"), 0o644)
	}
	if err := runAgentUp(os.Args[sep+3:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
	os.Exit(0)
}

func runPreparedDeliveredManagedRestoreCase(t *testing.T, binary, conversation string) {
	setupFakeAMQSessionRoots(t)
	dir := prepareRunStartBinaryFixture(t, binary)
	originalCWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalCWD); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})
	manifest, digest, err := readPreparedRunManifestSnapshot(dir, team.DefaultProfile, "prepared")
	if err != nil {
		t.Fatal(err)
	}
	token := preparedRunTokenFromSnapshot(manifest, digest)
	launchAttempt, err := reservePreparedRunLaunch(dir, team.DefaultProfile, "prepared", token)
	if err != nil {
		t.Fatalf("reserve launch: %v", err)
	}
	token.LaunchAttempt = launchAttempt
	t.Setenv(envTmuxTarget, "new-session")
	t.Setenv("TMUX", "/tmp/fake-tmux,1,0")
	t.Setenv("TMUX_PANE", "%42")
	oldPane := launchCurrentPaneIdentity
	launchCurrentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		return &tmuxpane.PaneIdentity{Session: "managed", WindowID: "@1", WindowName: "cto", PaneID: "%42"}, nil
	}
	t.Cleanup(func() { launchCurrentPaneIdentity = oldPane })
	oldTerminal := launchStdinIsTerminal
	launchStdinIsTerminal = func() bool { return true }
	t.Cleanup(func() { launchStdinIsTerminal = oldTerminal })
	var execArgv [][]string
	var execEnvs [][]string
	oldExec := amqSyscallExec
	amqSyscallExec = func(_ string, argv, env []string) error {
		execArgv = append(execArgv, append([]string(nil), argv...))
		execEnvs = append(execEnvs, append([]string(nil), env...))
		return nil
	}
	t.Cleanup(func() { amqSyscallExec = oldExec })
	if _, _, err := captureOutput(t, func() error {
		return runLaunchWithPreparedToken(withoutArg(preparedLeadLaunchArgs(dir, binary), "--dry-run"), token)
	}); err != nil {
		t.Fatalf("initial launch: %v", err)
	}
	oldLister, oldSend := statusPaneLister, sendPromptToPane
	statusPaneLister = func() ([]tmuxpane.TmuxPane, error) {
		return []tmuxpane.TmuxPane{{PaneID: "%42", CWD: dir, Command: binary, Title: "amq:prepared:cto"}}, nil
	}
	sendPromptToPane = func(string, string) error { return nil }
	t.Cleanup(func() { statusPaneLister, sendPromptToPane = oldLister, oldSend })
	opts, err := resolveGoalDeliveryOptions(dir, team.DefaultProfile, "prepared", "cto", manifest.GoalText, true, true, true, "goal start", namespaceConflictOverrideOptions{})
	if err != nil {
		t.Fatal(err)
	}
	opts.PreparedRunToken = token
	result, err := executeGoalDelivery(opts)
	if err != nil {
		t.Fatalf("goal delivery: %v", err)
	}
	if result.DeliveryReceipt == nil {
		t.Fatal("missing receipt")
	}
	receipt := result.DeliveryReceipt
	contract, _ := goalDeliveryContractForBinary(binary)
	if claimed, _, err := claimGoalAttempt(dir, team.DefaultProfile, "prepared", receipt.AttemptID, contract.ClaimRoute, time.Now().UTC()); err != nil || !claimed {
		t.Fatalf("claim: %v", err)
	}
	if claimed, existing, err := claimGoalAttempt(dir, team.DefaultProfile, "prepared", receipt.AttemptID, "amq", time.Now().UTC()); err != nil || claimed || existing.Route != contract.ClaimRoute {
		t.Fatalf("duplicate claim was not rejected claimed=%t existing=%+v err=%v", claimed, existing, err)
	}
	if err := completePreparedRunLaunch(dir, team.DefaultProfile, "prepared", token); err != nil {
		t.Fatalf("complete launch: %v", err)
	}
	env, err := resolveAMQEnvForTeamLaunchProfile(dir, team.DefaultProfile, "prepared", "cto")
	if err != nil {
		t.Fatal(err)
	}
	agentDir := filepath.Join(env.Root, "agents", "cto")
	rec, err := launch.Read(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	beforeBinding := *rec.GoalBinding
	rec.AgentPID = 0
	rec.Conversation = conversation
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	if err := execRestoreRecord(rec); err != nil {
		t.Fatalf("delivered restore: %v", err)
	}
	stored, err := launch.Read(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(execArgv) != 2 || !reflect.DeepEqual(stored.GoalBinding, &beforeBinding) || preparedRunTokenFromRecord(stored) != token {
		t.Fatalf("restore evidence exec=%d binding=%+v token=%+v", len(execArgv), stored.GoalBinding, preparedRunTokenFromRecord(stored))
	}
	for _, entry := range execEnvs[1] {
		if strings.HasPrefix(entry, internalPreparedRunTokenEnv+"=") || strings.HasPrefix(entry, internalPreparedRunRestoreEnv+"=") {
			t.Fatalf("internal restore transport leaked: %q", entry)
		}
	}
	restorePrompt := generatedBootstrapPrompt(execArgv[1])
	if conversation == "" {
		if restorePrompt == "" || digestRunArtifactBytes([]byte(restorePrompt)) != manifest.BootstrapDigests["cto"] {
			t.Fatalf("restore bootstrap is not exact accepted prompt")
		}
		resumeAttempt, err := newPreparedRunGeneration()
		if err != nil {
			t.Fatal(err)
		}
		desc := &preparedRestoreDescriptor{Token: token, AttemptID: resumeAttempt, RecordDigest: preparedRestoreRecordDigest(stored), SemanticDigest: preparedRestoreSemanticDigest(stored)}
		if err := runLaunchWithIntent(append([]string{"--no-bootstrap"}, launchArgsFromRecord(stored)...), token, desc); err == nil || !strings.Contains(err.Error(), "requires the accepted bootstrap prompt") {
			t.Fatalf("no-conversation no-bootstrap restore error=%v", err)
		}
	} else if restorePrompt != "" {
		t.Fatalf("saved-conversation restore replayed bootstrap")
	}
	if binary == "codex" && conversation == "" {
		baselineExec := len(execArgv)
		good := stored
		good.AgentPID = 0
		expectRejected := func(name string, candidate launch.Record) {
			t.Helper()
			if err := launch.Write(agentDir, candidate); err != nil {
				t.Fatal(err)
			}
			if err := execRestoreRecord(candidate); err == nil {
				t.Fatalf("%s restore unexpectedly succeeded", name)
			}
			if len(execArgv) != baselineExec {
				t.Fatalf("%s reached exec", name)
			}
			current, err := launch.Read(agentDir)
			if err != nil || !reflect.DeepEqual(current, candidate) {
				t.Fatalf("%s unsafe overwrite current=%+v err=%v", name, current, err)
			}
		}
		for _, tc := range []struct {
			name   string
			mutate func(*launch.GoalBinding)
		}{
			{name: "binding-reserved", mutate: func(b *launch.GoalBinding) { b.DeliveryState = goalBindingDeliveryReserved }},
			{name: "binding-source", mutate: func(b *launch.GoalBinding) { b.Source = "prepared-run" }},
			{name: "binding-detail", mutate: func(b *launch.GoalBinding) { b.Detail = "wrong" }},
			{name: "binding-goal", mutate: func(b *launch.GoalBinding) { b.Goal = "wrong" }},
			{name: "binding-attempt", mutate: func(b *launch.GoalBinding) { b.AttemptID = "wrong" }},
			{name: "binding-command", mutate: func(b *launch.GoalBinding) { b.Command += " --profile wrong" }},
		} {
			candidate := good
			bindingCopy := *good.GoalBinding
			tc.mutate(&bindingCopy)
			candidate.GoalBinding = &bindingCopy
			expectRejected(tc.name, candidate)
		}
		if err := launch.Write(agentDir, good); err != nil {
			t.Fatal(err)
		}
		attemptPath, _ := goalAttemptPath(dir, team.DefaultProfile, "prepared", receipt.AttemptID)
		claimPath := goalAttemptClaimPath(attemptPath)
		receiptPath := filepath.Join(deliveryReceiptDir(dir, team.DefaultProfile, "prepared"), receipt.AttemptID+".json")
		for _, pathCase := range []struct{ name, path string }{{"attempt-missing", attemptPath}, {"claim-missing", claimPath}, {"receipt-missing", receiptPath}} {
			backup := pathCase.path + ".backup"
			if err := os.Rename(pathCase.path, backup); err != nil {
				t.Fatal(err)
			}
			expectRejected(pathCase.name, good)
			if err := os.Rename(backup, pathCase.path); err != nil {
				t.Fatal(err)
			}
		}
		for _, pathCase := range []struct{ name, path string }{{"attempt-corrupt", attemptPath}, {"claim-corrupt", claimPath}, {"receipt-corrupt", receiptPath}} {
			original, err := os.ReadFile(pathCase.path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(pathCase.path, []byte("{"), 0o644); err != nil {
				t.Fatal(err)
			}
			expectRejected(pathCase.name, good)
			if err := os.WriteFile(pathCase.path, original, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		claimBytes, err := os.ReadFile(claimPath)
		if err != nil {
			t.Fatal(err)
		}
		var changedClaim goalAttemptClaim
		if err := json.Unmarshal(claimBytes, &changedClaim); err != nil {
			t.Fatal(err)
		}
		changedClaim.Route = "amq"
		changedClaimBytes, _ := json.MarshalIndent(changedClaim, "", "  ")
		if err := os.WriteFile(claimPath, append(changedClaimBytes, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		expectRejected("claim-wrong-route", good)
		if err := os.WriteFile(claimPath, claimBytes, 0o644); err != nil {
			t.Fatal(err)
		}
		receiptBytes, err := os.ReadFile(receiptPath)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			name   string
			mutate func(*deliveryReceiptData)
		}{
			{name: "receipt-target", mutate: func(r *deliveryReceiptData) { r.Target.Handle = "wrong" }},
			{name: "receipt-method", mutate: func(r *deliveryReceiptData) { r.Method = "wrong" }},
			{name: "receipt-status", mutate: func(r *deliveryReceiptData) { r.Status = "wrong" }},
			{name: "receipt-token", mutate: func(r *deliveryReceiptData) { r.PreparedRunDigest = "wrong" }},
			{name: "receipt-stage", mutate: func(r *deliveryReceiptData) { r.Stages = nil }},
			{name: "receipt-fallback", mutate: func(r *deliveryReceiptData) { r.Fallback = true }},
		} {
			var changed deliveryReceiptData
			if err := json.Unmarshal(receiptBytes, &changed); err != nil {
				t.Fatal(err)
			}
			tc.mutate(&changed)
			payload, _ := json.MarshalIndent(changed, "", "  ")
			if err := os.WriteFile(receiptPath, append(payload, '\n'), 0o644); err != nil {
				t.Fatal(err)
			}
			expectRejected(tc.name, good)
		}
		if err := os.WriteFile(receiptPath, receiptBytes, 0o644); err != nil {
			t.Fatal(err)
		}
		receiptBackup := receiptPath + ".target"
		if err := os.Rename(receiptPath, receiptBackup); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(receiptBackup, receiptPath); err != nil {
			t.Fatal(err)
		}
		expectRejected("receipt-symlink", good)
		if err := os.Remove(receiptPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(receiptBackup, receiptPath); err != nil {
			t.Fatal(err)
		}
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

func TestPreparedExternalStoredMutationAfterRegistrationPreventsWorkerExecution(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*launch.Record)
	}{
		{name: "goal", mutate: func(rec *launch.Record) {
			changed := *rec.GoalBinding
			changed.Goal = "unaccepted concurrent goal"
			rec.GoalBinding = &changed
		}},
		{name: "attempt", mutate: func(rec *launch.Record) {
			changed := *rec.GoalBinding
			changed.AttemptID = "unaccepted-concurrent-attempt"
			rec.GoalBinding = &changed
		}},
		{name: "pane-identity", mutate: func(rec *launch.Record) {
			rec.Tmux.PaneID = "%99"
			rec.Terminal.PaneID = "%99"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
			oldAfterRegister := runStartExternalLeadAfterRegister
			var mutatedRecord launch.Record
			runStartExternalLeadAfterRegister = func(project, profile, session, role string) error {
				agentDir := filepath.Join(squadnamespace.AMQRoot(project, profile, session), "agents", role)
				rec, err := launch.Read(agentDir)
				if err != nil {
					return err
				}
				tc.mutate(&rec)
				mutatedRecord = rec
				return launch.Write(agentDir, rec)
			}
			t.Cleanup(func() { runStartExternalLeadAfterRegister = oldAfterRegister })
			workerExecCalls := 0
			oldWorkerExec := runStartExecuteExternalWorkers
			runStartExecuteExternalWorkers = func(string, teamLaunchOptions) error {
				workerExecCalls++
				return nil
			}
			t.Cleanup(func() { runStartExecuteExternalWorkers = oldWorkerExec })
			_, _, err := captureOutput(t, func() error { return runRunStart(liveArgs, "test") })
			if err == nil || !strings.Contains(err.Error(), "changed before worker spawn") {
				t.Fatalf("stored external mutation error = %v", err)
			}
			if workerExecCalls != 0 || len(backend.launches) != 0 {
				t.Fatalf("stored external mutation reached worker executor=%d backend_launches=%d", workerExecCalls, len(backend.launches))
			}
			stored, readErr := launch.Read(filepath.Join(squadnamespace.AMQRoot(dir, team.DefaultProfile, "sess"), "agents", "cto"))
			if readErr != nil || !reflect.DeepEqual(stored, mutatedRecord) {
				t.Fatalf("concurrent replacement was not preserved: %v", readErr)
			}
		})
	}
}

func TestPreparedExternalTokenFailureRollsBackOwnedRecord(t *testing.T) {
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
	base := []string{"-p", dir, "-s", "sess", "--external-lead", "--goal", "Rollback only this prepared generation", "--launch-shape", runwizard.LaunchShapeWorkingTeamTogether}
	if _, _, err := captureOutput(t, func() error { return runRunStart(append(append([]string{}, base...), "--prepare"), "test") }); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	stubSuccessfulRunStartGoalDelivery(t)
	agentDir := filepath.Join(squadnamespace.AMQRoot(dir, team.DefaultProfile, "sess"), "agents", "cto")
	baselineLaunches := len(backend.launches)
	oldAfterRegister := runStartExternalLeadAfterRegister
	runStartExternalLeadAfterRegister = func(project, profile, session, role string) error {
		manifest, err := readPreparedRunManifest(project, profile, session)
		if err != nil {
			return err
		}
		manifest = nextPreparedRunManifestForTest(t, manifest)
		return writePreparedRunManifest(preparedRunPath(project, profile, session), manifest)
	}
	t.Cleanup(func() { runStartExternalLeadAfterRegister = oldAfterRegister })
	_, _, err := captureOutput(t, func() error { return runRunStart(append(append([]string{}, base...), "--go"), "test") })
	if err == nil {
		t.Fatal("forced external prepared drift unexpectedly launched")
	}
	if len(backend.launches) != baselineLaunches {
		t.Fatalf("forced drift reached worker backend: before=%d after=%d", baselineLaunches, len(backend.launches))
	}
	stored, readErr := launch.Read(agentDir)
	if !os.IsNotExist(readErr) {
		t.Fatalf("run-owned external record was not removed: %v %+v", readErr, stored)
	}
}

func TestPreparedExternalRollbackRestoresPriorRecordByFixedCAS(t *testing.T) {
	agentDir := filepath.Join(t.TempDir(), "agents", "cto")
	previous := launch.Record{Schema: 1, Role: "cto", Handle: "cto", Session: "sess", Conversation: "prior"}
	written := launch.Record{Role: "cto", Handle: "cto", Session: "sess", Conversation: "run-owned"}
	if err := launch.Write(agentDir, written); err != nil {
		t.Fatal(err)
	}
	written, err := launch.Read(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := rollbackPreparedExternalLeadRecord(agentDir, &previous, written)
	if err != nil || !applied {
		t.Fatalf("rollback applied=%t error=%v", applied, err)
	}
	stored, err := launch.Read(agentDir)
	if err != nil || !reflect.DeepEqual(stored, previous) {
		t.Fatalf("prior record restoration err=%v got=%+v want=%+v", err, stored, previous)
	}
}

func TestPreparedExternalPreWriteFailureQuiescesNewWakeAndWritesNoRecord(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Project: "", Orchestrated: true, Lead: "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "sess"},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "sess"},
		},
	})
	useFakeTmuxBackend(t)
	stubCurrentRunStartPane(t, "%42")
	stubRunStartLeadWake(t)
	liveArgs := prepareRunStartTestInvocation(t, []string{"-p", dir, "-s", "sess", "--external-lead", "--go"})
	oldAfterWake := externalLeadAfterWakeStart
	externalLeadAfterWakeStart = func(leadWakeResult) error { return fmt.Errorf("forced pre-write failure") }
	t.Cleanup(func() { externalLeadAfterWakeStart = oldAfterWake })
	oldSignal := externalLeadWakeProcessGroupSignal
	var signals []syscall.Signal
	externalLeadWakeProcessGroupSignal = func(pgid int, signal syscall.Signal) error {
		if pgid != 1234 {
			t.Fatalf("cleanup pgid=%d want=1234", pgid)
		}
		signals = append(signals, signal)
		if signal == 0 {
			return syscall.ESRCH
		}
		return nil
	}
	t.Cleanup(func() { externalLeadWakeProcessGroupSignal = oldSignal })
	_, _, err := captureOutput(t, func() error { return runRunStart(liveArgs, "test") })
	if err == nil || !strings.Contains(err.Error(), "forced pre-write failure") {
		t.Fatalf("pre-write failure=%v", err)
	}
	if !reflect.DeepEqual(signals, []syscall.Signal{syscall.SIGTERM, 0}) {
		t.Fatalf("wake cleanup signals=%v", signals)
	}
	agentDir := filepath.Join(squadnamespace.AMQRoot(dir, team.DefaultProfile, "sess"), "agents", "cto")
	if _, err := launch.Read(agentDir); !os.IsNotExist(err) {
		t.Fatalf("pre-write failure left launch record: %v", err)
	}
}

func TestPreparedExternalRollbackPreservesExistingWake(t *testing.T) {
	oldSignal := externalLeadWakeProcessGroupSignal
	calls := 0
	externalLeadWakeProcessGroupSignal = func(int, syscall.Signal) error { calls++; return nil }
	t.Cleanup(func() { externalLeadWakeProcessGroupSignal = oldSignal })
	if err := rollbackStartedExternalLeadWake(leadWakeResult{PID: 2222, Started: false}); err != nil || calls != 0 {
		t.Fatalf("existing wake cleanup err=%v signal_calls=%d", err, calls)
	}
}

func TestPinnedGoalReservationRejectsChangedPreparedBindingBeforeAttempt(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Project: "", Orchestrated: true, Lead: "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "sess"},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "sess"},
		},
	})
	useFakeTmuxBackend(t)
	stubCurrentRunStartPane(t, "%42")
	stubRunStartLeadWake(t)
	liveArgs := prepareRunStartTestInvocation(t, []string{"-p", dir, "-s", "sess", "--external-lead", "--go"})
	if _, _, err := captureOutput(t, func() error { return runRunStart(liveArgs, "test") }); err != nil {
		t.Fatalf("external lead go: %v", err)
	}
	manifest, digest, err := readPreparedRunManifestSnapshot(dir, team.DefaultProfile, "sess")
	if err != nil {
		t.Fatal(err)
	}
	token := preparedRunTokenFromSnapshot(manifest, digest)
	agentDir := filepath.Join(squadnamespace.AMQRoot(dir, team.DefaultProfile, "sess"), "agents", "cto")
	rec, err := launch.Read(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	changed := *rec.GoalBinding
	changed.DeliveryState = goalBindingDeliveryDelivered
	rec.GoalBinding = &changed
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}

	opts, err := resolveGoalDeliveryOptions(dir, team.DefaultProfile, "sess", "cto", manifest.GoalText, true, true, true, "goal start", namespaceConflictOverrideOptions{})
	if err != nil {
		t.Fatal(err)
	}
	opts.PreparedRunToken = token
	_, err = executeGoalDelivery(opts)
	if err == nil || !strings.Contains(err.Error(), "not the exact pinned prepared binding") {
		t.Fatalf("changed prepared binding error=%v", err)
	}
	entries, readErr := os.ReadDir(goalAttemptDir(dir, team.DefaultProfile, "sess"))
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatal(readErr)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			t.Fatalf("changed prepared binding created attempt/receipt artifact %s", entry.Name())
		}
	}
	stored, err := launch.Read(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stored.GoalBinding, rec.GoalBinding) {
		t.Fatalf("changed prepared binding was mutated: before=%+v after=%+v", rec.GoalBinding, stored.GoalBinding)
	}
}

func TestPinnedGoalProfileAndRecordDriftRejectBeforeNewAttempt(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, string, string, launch.Record, preparedRunManifest)
		want   string
	}{
		{
			name: "profile-member",
			mutate: func(t *testing.T, dir, _ string, _ launch.Record, manifest preparedRunManifest) {
				tm, err := team.ReadProfile(dir, team.DefaultProfile)
				if err != nil {
					t.Fatal(err)
				}
				for i := range tm.Members {
					if tm.Members[i].Role == "cto" {
						if manifest.Members["cto"].ActorMode == team.ActorModeImplementation {
							tm.Members[i].ActorMode = team.ActorModeReview
						} else {
							tm.Members[i].ActorMode = team.ActorModeImplementation
						}
					}
				}
				if err := team.WriteProfile(dir, team.DefaultProfile, tm); err != nil {
					t.Fatal(err)
				}
			},
			want: "current lead member identity differs",
		},
		{
			name: "launch-record-generation",
			mutate: func(t *testing.T, _ string, agentDir string, rec launch.Record, _ preparedRunManifest) {
				rec.PreparedRunDigest = strings.Repeat("0", 64)
				if err := launch.Write(agentDir, rec); err != nil {
					t.Fatal(err)
				}
			},
			want: "launch record prepared run token differs",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := seedTeam(t, team.Team{
				Project: "", Orchestrated: true, Lead: "cto",
				Members: []team.Member{
					{Role: "cto", Binary: "codex", Handle: "cto", Session: "sess"},
					{Role: "qa", Binary: "codex", Handle: "qa", Session: "sess"},
				},
			})
			useFakeTmuxBackend(t)
			stubCurrentRunStartPane(t, "%42")
			stubRunStartLeadWake(t)
			liveArgs := prepareRunStartTestInvocation(t, []string{"-p", dir, "-s", "sess", "--external-lead", "--go"})
			if _, _, err := captureOutput(t, func() error { return runRunStart(liveArgs, "test") }); err != nil {
				t.Fatalf("external lead go: %v", err)
			}
			manifest, digest, err := readPreparedRunManifestSnapshot(dir, team.DefaultProfile, "sess")
			if err != nil {
				t.Fatal(err)
			}
			token := preparedRunTokenFromSnapshot(manifest, digest)
			agentDir := filepath.Join(squadnamespace.AMQRoot(dir, team.DefaultProfile, "sess"), "agents", "cto")
			rec, err := launch.Read(agentDir)
			if err != nil {
				t.Fatal(err)
			}
			opts, err := resolveGoalDeliveryOptions(dir, team.DefaultProfile, "sess", "cto", manifest.GoalText, true, true, true, "goal start", namespaceConflictOverrideOptions{})
			if err != nil {
				t.Fatal(err)
			}
			opts.PreparedRunToken = token
			tc.mutate(t, dir, agentDir, rec, manifest)

			_, err = executeGoalDelivery(opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("prepared identity drift error=%v want=%q", err, tc.want)
			}
			entries, readErr := os.ReadDir(goalAttemptDir(dir, team.DefaultProfile, "sess"))
			if readErr != nil && !os.IsNotExist(readErr) {
				t.Fatal(readErr)
			}
			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), ".json") {
					t.Fatalf("prepared identity drift created attempt/receipt artifact %s", entry.Name())
				}
			}
		})
	}
}

func TestPreparedRunSuccessCarriesOneTokenAcrossRecordBootstrapAndReceipt(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Project: "", Orchestrated: true, Lead: "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "sess"},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "sess"},
		},
	})
	useFakeTmuxBackend(t)
	stubCurrentRunStartPane(t, "%42")
	stubRunStartLeadWake(t)
	liveArgs := prepareRunStartTestInvocation(t, []string{"-p", dir, "-s", "sess", "--external-lead", "--goal", "Deliver one pinned generation", "--go"})
	if _, _, err := captureOutput(t, func() error { return runRunStart(liveArgs, "test") }); err != nil {
		t.Fatalf("external lead go: %v", err)
	}
	manifest, digest, err := readPreparedRunManifestSnapshot(dir, team.DefaultProfile, "sess")
	if err != nil {
		t.Fatal(err)
	}
	generationToken := preparedRunTokenFromSnapshot(manifest, digest)
	agentDir := filepath.Join(squadnamespace.AMQRoot(dir, team.DefaultProfile, "sess"), "agents", "cto")
	rec, err := launch.Read(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	token := preparedRunTokenFromRecord(rec)
	if !samePreparedRunGeneration(token, generationToken) || token.LaunchAttempt == "" || rec.BootstrapExpectation == nil || rec.BootstrapExpectation.Required {
		t.Fatalf("record/bootstrap token evidence rec=%+v generation=%+v token=%+v", rec, generationToken, token)
	}
	receipt := newDeliveryReceipt(dir, team.DefaultProfile, "sess", "cto", "cto", "test", "test")
	applyPreparedRunTokenToReceipt(&receipt, token)
	if receipt.PreparedRunGeneration != token.Generation || receipt.PreparedRunLaunchAttempt != token.LaunchAttempt || receipt.PreparedRunDigest != token.ManifestDigest || receipt.PreparedRunGoalNamespace != token.GoalNamespace || receipt.PreparedRunGoalDigest != token.GoalDigest {
		t.Fatalf("receipt token evidence=%+v want=%+v", receipt, token)
	}
	stored, err := launch.Read(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	if preparedRunTokenFromRecord(stored) != token || stored.BootstrapExpectation == nil {
		t.Fatalf("post-delivery record/bootstrap token changed: %+v", stored)
	}
}

func preparedLeadLaunchArgs(dir, binary string) []string {
	return []string{
		"--project", dir, "--team-home", dir, "--team-profile", team.DefaultProfile,
		"--role", "cto", "--me", "cto", "--session", "prepared",
		"--trust", trustModeApproveForMe, "--dry-run", binary,
	}
}

func withoutArg(args []string, remove string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		if arg != remove {
			out = append(out, arg)
		}
	}
	return out
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
	republishPreparedRunManifestForTest(t, manifest)
}
