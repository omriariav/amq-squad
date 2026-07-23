package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestRunWakeBindingExecPersistsExactLockBeforeTargetExec(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".agent-mail", "prepared")
	agentDir := filepath.Join(root, "agents", "dev")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := launch.Record{Root: root, Handle: "dev", AgentPID: os.Getpid(), PreparedRunGeneration: "g", PreparedRunDigest: "d", PreparedRunLaunchAttempt: "a"}
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(wakeLockFile{PID: 202, Root: root, Started: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wakeLockPath(agentDir), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	wantBinding, _, err := readWakeRecordBinding(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	previousProbe, previousExec := launchWakeBindingProbe, amqSyscallExec
	launchWakeBindingProbe = duplicateLaunchProbe{PIDAlive: func(pid int) bool { return pid == 202 }, ProcessMatch: func(int, func(string) bool) bool { return true }, Now: time.Now}
	var execArgv []string
	amqSyscallExec = func(_ string, argv, _ []string) error { execArgv = append([]string(nil), argv...); return nil }
	t.Cleanup(func() { launchWakeBindingProbe, amqSyscallExec = previousProbe, previousExec })
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_ME", "dev")
	if err := runWakeBindingExec(root, "dev", []string{"go", "version"}); err != nil {
		t.Fatal(err)
	}
	stored, err := launch.Read(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	if stored.WakePID != wantBinding.PID || stored.WakeRecordID != wantBinding.RecordID || stored.WakeRecordDigest != wantBinding.RecordDigest || !reflect.DeepEqual(execArgv, []string{"go", "version"}) {
		t.Fatalf("stored wake binding=%+v exec=%v", stored, execArgv)
	}
}

func TestNativeGoalBindingFromArgsDetectsGoalPrompt(t *testing.T) {
	got := nativeGoalBindingFromArgs([]string{"--enable", "goals", `/goal --goal "ship"`})
	if got == nil || !got.NativeGoal || got.Mode != "native_goal" || got.Source != "launch-argv" {
		t.Fatalf("native goal binding = %+v", got)
	}
	if got.Command != `/goal --goal "ship"` {
		t.Fatalf("command = %q", got.Command)
	}
	if none := nativeGoalBindingFromArgs([]string{"--enable", "goals", "plain prompt"}); none != nil {
		t.Fatalf("plain prompt should not create native binding: %+v", none)
	}
}

func TestGoalBindingFromArgsUsesStructuredPromptForCodex(t *testing.T) {
	tm := team.Team{Project: "/tmp/project", Lead: "cto", ExecutionMode: executionModeProjectLead}
	goal := "ship line one\nship line two"
	prompt := codexGoalControlPrompt(goal, tm, team.DefaultProfile, "issue-460", "cto", "attempt-460")
	got := goalBindingFromArgs("codex", []string{"--enable", "goals", prompt})
	if got == nil || got.NativeGoal || got.Mode != "prompt_goal" || got.Source != "launch-argv" {
		t.Fatalf("Codex prompt goal binding = %+v", got)
	}
	if got.Goal != goal || got.AttemptID != "attempt-460" || got.Command != prompt {
		t.Fatalf("Codex prompt identity changed: %+v", got)
	}
	if strings.Contains(prompt, `ship line one\nship line two`) || !strings.Contains(prompt, "ship line one\nship line two") {
		t.Fatalf("Codex prompt must carry actual goal newlines: %q", prompt)
	}
	if legacy := goalBindingFromArgs("codex", []string{`/goal --goal "ship"`}); legacy != nil {
		t.Fatalf("Codex must not trust a legacy native /goal binding: %+v", legacy)
	}
}

func TestLaunchRecordGoalBindingDeliveryStateUpgradeCompatibility(t *testing.T) {
	tm := team.Team{Project: "/tmp/project", Lead: "cto", ExecutionMode: executionModeProjectLead}
	for _, binary := range []string{"codex", "claude"} {
		t.Run(binary, func(t *testing.T) {
			contract, err := goalDeliveryContractForBinary(binary)
			if err != nil {
				t.Fatal(err)
			}
			const attempt = "attempt-460"
			prompt := contract.prompt("ship", tm, team.DefaultProfile, "issue-460", "cto", attempt)
			binding := func(source, state, detail string) launch.GoalBinding {
				got := *contract.binding("ship", attempt, prompt, source, detail)
				got.DeliveryState = state
				return got
			}
			legacyDeliveredDetail := contract.Label + " delivered as a first-class claim-once control action"
			tests := []struct {
				name    string
				binding launch.GoalBinding
				want    bool
			}{
				{name: "new delivered", binding: binding("goal-control", goalBindingDeliveryDelivered, "delivered"), want: true},
				{name: "new reserved", binding: binding("goal-control", goalBindingDeliveryReserved, "reserved")},
				{name: "new prepared", binding: binding("prepared-run", goalBindingDeliveryPrepared, "accepted")},
				{name: "new unknown state", binding: binding("goal-control", "future", legacyDeliveredDetail)},
				{name: "legacy launch argv", binding: binding("launch-argv", "", "process input"), want: true},
				{name: "legacy delivered goal control", binding: binding("goal-control", "", legacyDeliveredDetail), want: true},
				{name: "legacy reserved goal control", binding: binding("goal-control", "", contract.Label+" reserved as a claim-once control action")},
				{name: "legacy prepared run", binding: binding("prepared-run", "", legacyDeliveredDetail)},
				{name: "legacy unknown source", binding: binding("goal-runtime", "", legacyDeliveredDetail)},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					rec := launch.Record{Binary: binary, GoalBinding: &tt.binding}
					if got := launchRecordHasGoalBinding(rec); got != tt.want {
						t.Fatalf("launchRecordHasGoalBinding(%+v) = %t, want %t", tt.binding, got, tt.want)
					}
				})
			}
		})
	}
}

func TestRunLaunchDryRunSandboxedCodexOmitsBypassDefault(t *testing.T) {
	setupFakeAMQ(t)

	stdout, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "--trust", "sandboxed", "codex", "test-prompt"})
	})
	if err != nil {
		t.Fatalf("runLaunch: %v\nstderr:\n%s", err, stderr)
	}
	if strings.Contains(stdout, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("sandboxed codex must not include bypass arg by default:\n%s", stdout)
	}
	want := "amq coop exec --require-wake --wake-inject-mode raw codex -- test-prompt"
	if !strings.Contains(stdout, want) {
		t.Fatalf("stdout missing %q in:\n%s", want, stdout)
	}
}

func TestRunLaunchBootstrapBoundaryIntegrated(t *testing.T) {
	for _, tc := range []struct{ name, raw, reason string }{
		{"delimiter-tail", "--profile p -- existing-prompt", "after --"},
		{"bare-positional", "--profile p existing-prompt", "positional token"},
		{"unknown-ambiguous", "--mystery value", "ambiguous"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupFakeAMQ(t)
			var observed launch.Record
			var argv []string
			old := launchPlanObserver
			t.Cleanup(func() { launchPlanObserver = old })
			launchPlanObserver = func(rec launch.Record, args []string) { observed = rec; argv = args }
			stdout, _, err := captureOutput(t, func() error {
				return runLaunch([]string{"--dry-run", "--trust", "sandboxed", "--codex-args=" + tc.raw, "codex"})
			})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(stdout, "You are a fresh amq-squad agent.") {
				t.Fatalf("unsafe argv received generated bootstrap:\n%s", stdout)
			}
			if observed.BootstrapExpectation == nil || observed.BootstrapExpectation.Required || !strings.Contains(observed.BootstrapExpectation.NotRequiredReason, tc.reason) {
				t.Fatalf("expectation=%#v argv=%#v", observed.BootstrapExpectation, argv)
			}
		})
	}

	t.Run("terminal-delimiter", func(t *testing.T) {
		setupFakeAMQ(t)
		var argv []string
		old := launchPlanObserver
		t.Cleanup(func() { launchPlanObserver = old })
		launchPlanObserver = func(_ launch.Record, args []string) { argv = args }
		stdout, _, err := captureOutput(t, func() error {
			return runLaunch([]string{"--dry-run", "--trust", "sandboxed", "--codex-args=--profile p --", "codex"})
		})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stdout, "You are a fresh amq-squad agent.") || len(argv) == 0 || argv[len(argv)-1] == "--" || !strings.Contains(argv[len(argv)-1], "You are a fresh amq-squad agent.") {
			t.Fatalf("bootstrap not final positional: stdout=%s argv=%#v", stdout, argv)
		}
		delimiters := 0
		for _, arg := range argv {
			if arg == "--" {
				delimiters++
			}
		}
		if delimiters != 1 {
			t.Fatalf("expected one end-of-options delimiter: %#v", argv)
		}
	})
}

func TestRunLaunchDryRunApproveForMeCodexPreset(t *testing.T) {
	setupFakeAMQ(t)

	stdout, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "--trust", "approve-for-me", "codex", "test-prompt"})
	})
	if err != nil {
		t.Fatalf("runLaunch: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		"--sandbox workspace-write",
		"--ask-for-approval on-request",
		"-c 'approvals_reviewer=\"auto_review\"'",
		"test-prompt",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q in:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("approve-for-me must not imply trusted bypass:\n%s", stdout)
	}
}

func TestRunLaunchPreauthorizesInScopeClaudeWorker(t *testing.T) {
	seedTeam(t, team.Team{
		Members:      []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "v2-14-0"}, {Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "v2-14-0"}},
		Orchestrated: true,
		Lead:         "cto",
	})
	setupFakeAMQ(t)

	stdout, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "--role", "fullstack", "--session", "v2-14-0", "claude", "test-prompt"})
	})
	if err != nil {
		t.Fatalf("runLaunch: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{"--allowedTools", "gh pr create"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("eligible claude worker missing %q in:\n%s", want, stdout)
		}
	}
	// Narrowed slice (#296): PR creation only — push/main/tags/releases never pre-authorized.
	for _, forbidden := range []string{"git push", "origin main", "git tag", "gh release", "--tags", "--follow-tags"} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("pre-auth must never include %q:\n%s", forbidden, stdout)
		}
	}
}

func TestRunLaunchPreauthDoesNotSuppressBootstrap(t *testing.T) {
	seedTeam(t, team.Team{
		Members:      []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "v2-14-0"}, {Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "v2-14-0"}},
		Orchestrated: true,
		Lead:         "cto",
	})
	setupFakeAMQ(t)

	stdout, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--role", "fullstack", "--session", "v2-14-0", "claude"})
	})
	if err != nil {
		t.Fatalf("runLaunch: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		"--allowedTools",
		"Bash(gh pr create:*)",
		"You are a fresh amq-squad agent.",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("eligible claude worker dry-run missing %q in:\n%s", want, stdout)
		}
	}
}

func TestRunLaunchPreauthOptOutAndScope(t *testing.T) {
	seed := func(t *testing.T) {
		seedTeam(t, team.Team{
			Members:      []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "v2-14-0"}, {Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "v2-14-0"}},
			Orchestrated: true,
			Lead:         "cto",
		})
		setupFakeAMQ(t)
	}
	run := func(t *testing.T, args ...string) string {
		stdout, stderr, err := captureOutput(t, func() error { return runLaunch(args) })
		if err != nil {
			t.Fatalf("runLaunch: %v\nstderr:\n%s", err, stderr)
		}
		return stdout
	}

	t.Run("opt-out flag disables pre-auth", func(t *testing.T) {
		seed(t)
		out := run(t, "--dry-run", "--no-bootstrap", "--no-preauthorize-inscope", "--role", "fullstack", "--session", "v2-14-0", "claude", "p")
		if strings.Contains(out, "--allowedTools") {
			t.Fatalf("--no-preauthorize-inscope must suppress pre-auth:\n%s", out)
		}
	})
	t.Run("lead role not pre-authorized", func(t *testing.T) {
		seed(t)
		out := run(t, "--dry-run", "--no-bootstrap", "--role", "cto", "--session", "v2-14-0", "claude", "p")
		if strings.Contains(out, "--allowedTools") {
			t.Fatalf("lead role must not be pre-authorized:\n%s", out)
		}
	})
	t.Run("codex worker unchanged", func(t *testing.T) {
		seed(t)
		out := run(t, "--dry-run", "--no-bootstrap", "--role", "fullstack", "--session", "v2-14-0", "codex", "p")
		if strings.Contains(out, "--allowedTools") {
			t.Fatalf("codex worker is out of scope and must be unchanged:\n%s", out)
		}
	})
}

func TestRunLaunchBuiltInPreauthOptOutPreservesMemberPermissionAllowlist(t *testing.T) {
	seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "v2-14-0"},
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "v2-14-0", PermissionAllowlist: []string{"Bash(rm -rf /tmp/fullstack-review/*:*)"}},
		},
		Orchestrated: true,
		Lead:         "cto",
	})
	setupFakeAMQ(t)

	stdout, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "--no-preauthorize-inscope", "--role", "fullstack", "--session", "v2-14-0", "claude", "p"})
	})
	if err != nil {
		t.Fatalf("runLaunch: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "fullstack-review") || strings.Contains(stdout, "gh pr create") {
		t.Fatalf("explicit allowlist should survive built-in opt-out without PR grant:\n%s", stdout)
	}
}

func TestConfiguredAllowlistOptOutFullRecordReplay(t *testing.T) {
	teamHome := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-401"},
			{Role: "qa", Binary: "claude", Handle: "qa", Session: "issue-401", PermissionAllowlist: []string{"Read(/tmp/qa-review/**)"}},
		},
		Orchestrated: true,
		Lead:         "cto",
	})
	setupFakeAMQ(t)

	recordDir := t.TempDir()
	initial := launch.Record{
		Binary:                "claude",
		Argv:                  []string{"--allowedTools=Read(/tmp/qa-review/**)"},
		Session:               "issue-401",
		Handle:                "qa",
		Role:                  "qa",
		Root:                  filepath.Join(teamHome, ".agent-mail", "issue-401"),
		TeamHome:              teamHome,
		NoPreauthorizeInScope: true,
		PreauthorizedActions:  []string{"Read(/tmp/qa-review/**)"},
	}
	if err := launch.Write(recordDir, initial); err != nil {
		t.Fatal(err)
	}
	stored, err := launch.Read(recordDir)
	if err != nil {
		t.Fatal(err)
	}

	var replayed launch.Record
	oldObserver := launchPlanObserver
	t.Cleanup(func() { launchPlanObserver = oldObserver })
	launchPlanObserver = func(rec launch.Record, _ []string) { replayed = rec }
	args := append([]string{"--dry-run", "--no-bootstrap"}, launchArgsFromRecord(stored)...)
	_, _, err = captureOutput(t, func() error { return runLaunch(args) })
	if err != nil {
		t.Fatalf("replay runLaunch: %v", err)
	}
	if !replayed.NoPreauthorizeInScope {
		t.Fatal("replayed launch record lost no-preauthorize-inscope")
	}
	if !reflect.DeepEqual(replayed.PreauthorizedActions, []string{"Read(/tmp/qa-review/**)"}) {
		t.Fatalf("replayed current grant = %v", replayed.PreauthorizedActions)
	}
	if strings.Contains(strings.Join(replayed.Argv, " "), "gh pr create") {
		t.Fatalf("replayed opt-out restored built-in grant: %v", replayed.Argv)
	}
}

func TestRunLaunchRecordsExplicitAllowedToolsSeparatelyFromMergedGrant(t *testing.T) {
	teamHome := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-401"},
			{Role: "qa", Binary: "claude", Handle: "qa", Session: "issue-401", PermissionAllowlist: []string{"Read(/tmp/policy/**)"}},
		},
		Orchestrated: true,
		Lead:         "cto",
	})
	setupFakeAMQ(t)
	var observed launch.Record
	oldObserver := launchPlanObserver
	t.Cleanup(func() { launchPlanObserver = oldObserver })
	launchPlanObserver = func(rec launch.Record, _ []string) { observed = rec }
	_, _, err := captureOutput(t, func() error {
		return runLaunch([]string{
			"--dry-run", "--no-bootstrap", "--team-home", teamHome,
			"--role", "qa", "--session", "issue-401", "claude", "--",
			"--allowedTools=Edit(/tmp/explicit/**)",
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(observed.ExplicitAllowedTools, []string{"Edit(/tmp/explicit/**)"}) {
		t.Fatalf("explicit_allowed_tools = %v", observed.ExplicitAllowedTools)
	}
	for _, want := range []string{"Edit(/tmp/explicit/**)", "Read(/tmp/policy/**)", "Bash(gh pr create:*)"} {
		if !containsString(observed.PreauthorizedActions, want) {
			t.Fatalf("effective record audit missing %q: %v", want, observed.PreauthorizedActions)
		}
	}
	if containsString(observed.LauncherPreauthorizedActions, "Edit(/tmp/explicit/**)") ||
		!containsString(observed.LauncherPreauthorizedActions, "Read(/tmp/policy/**)") ||
		!containsString(observed.LauncherPreauthorizedActions, "Bash(gh pr create:*)") {
		t.Fatalf("launcher provenance = %v", observed.LauncherPreauthorizedActions)
	}
}

func TestExplicitGrantEqualToLauncherPolicySurvivesPolicyRemoval(t *testing.T) {
	const configured = "Read(/tmp/equal-policy/**)"
	for _, optOut := range []bool{false, true} {
		name := "with built-in"
		if optOut {
			name = "with built-in opt-out"
		}
		t.Run(name, func(t *testing.T) {
			teamHome := t.TempDir()
			writeReplayAllowlistTeam(t, teamHome, []string{configured}, true)
			setupFakeAMQ(t)

			launcherPolicy := []string{configured}
			if !optOut {
				launcherPolicy = []string{"Bash(gh pr create:*)", configured}
			}
			initialArgs := []string{
				"--dry-run", "--no-bootstrap", "--team-home", teamHome,
				"--role", "qa", "--session", "issue-401",
			}
			if optOut {
				initialArgs = append(initialArgs, "--no-preauthorize-inscope")
			}
			initialArgs = append(initialArgs, "claude", "--", claudePreauthChildArgs(launcherPolicy)[0])

			var initial launch.Record
			oldObserver := launchPlanObserver
			t.Cleanup(func() { launchPlanObserver = oldObserver })
			launchPlanObserver = func(rec launch.Record, _ []string) { initial = rec }
			if _, _, err := captureOutput(t, func() error { return runLaunch(initialArgs) }); err != nil {
				t.Fatalf("initial launch: %v", err)
			}
			if !reflect.DeepEqual(initial.ExplicitAllowedTools, launcherPolicy) ||
				!reflect.DeepEqual(initial.LauncherPreauthorizedActions, launcherPolicy) {
				t.Fatalf("equal-valued provenance collapsed: explicit=%v launcher=%v want=%v", initial.ExplicitAllowedTools, initial.LauncherPreauthorizedActions, launcherPolicy)
			}

			// Remove the configured member policy, then replay the exact record.
			writeReplayAllowlistTeam(t, teamHome, nil, true)
			var replayed launch.Record
			launchPlanObserver = func(rec launch.Record, _ []string) { replayed = rec }
			replayArgs := append([]string{"--dry-run", "--no-bootstrap"}, launchArgsFromRecord(initial)...)
			if _, _, err := captureOutput(t, func() error { return runLaunch(replayArgs) }); err != nil {
				t.Fatalf("replay after policy removal: %v", err)
			}
			if !reflect.DeepEqual(replayed.ExplicitAllowedTools, launcherPolicy) {
				t.Fatalf("equal-valued explicit grant was lost: got %v want %v", replayed.ExplicitAllowedTools, launcherPolicy)
			}
			wantLauncher := []string(nil)
			if !optOut {
				wantLauncher = []string{"Bash(gh pr create:*)"}
			}
			if !reflect.DeepEqual(replayed.LauncherPreauthorizedActions, wantLauncher) {
				t.Fatalf("launcher policy was not revoked structurally: got %v want %v", replayed.LauncherPreauthorizedActions, wantLauncher)
			}
			if !containsString(replayed.PreauthorizedActions, configured) {
				t.Fatalf("surviving explicit value absent from effective audit: %v", replayed.PreauthorizedActions)
			}
			if strings.Count(strings.Join(replayed.Argv, " "), "--allowedTools") != 1 {
				t.Fatalf("replay emitted duplicate allowed-tools flags: %v", replayed.Argv)
			}
		})
	}
}

func TestReplayRecomposesLauncherGrantFromCurrentPolicy(t *testing.T) {
	const (
		oldGrant = "Read(/tmp/old-policy/**)"
		newGrant = "Read(/tmp/new-policy/**)"
		explicit = "Edit(/tmp/explicit/**)"
	)
	tests := []struct {
		name        string
		configure   func(t *testing.T, dir string)
		want        []string
		wantBuiltin bool
	}{
		{
			name: "A to B",
			configure: func(t *testing.T, dir string) {
				writeReplayAllowlistTeam(t, dir, []string{newGrant}, true)
			},
			want: []string{explicit, newGrant}, wantBuiltin: true,
		},
		{
			name: "A to empty",
			configure: func(t *testing.T, dir string) {
				writeReplayAllowlistTeam(t, dir, nil, true)
			},
			want: []string{explicit}, wantBuiltin: true,
		},
		{
			name: "role removed",
			configure: func(t *testing.T, dir string) {
				writeReplayAllowlistTeam(t, dir, nil, false)
			},
			want: []string{explicit},
		},
		{
			name: "profile removed",
			configure: func(t *testing.T, dir string) {
				if err := os.MkdirAll(filepath.Join(dir, team.DirName), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			want: []string{explicit},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			teamHome := t.TempDir()
			tc.configure(t, teamHome)
			setupFakeAMQ(t)
			prior := []string{explicit, "Bash(gh pr create:*)", oldGrant}
			rec := launch.Record{
				Binary:                       "claude",
				Argv:                         claudePreauthChildArgs(prior),
				Session:                      "issue-401",
				Handle:                       "qa",
				Role:                         "qa",
				Root:                         filepath.Join(teamHome, ".agent-mail", "issue-401"),
				TeamHome:                     teamHome,
				PreauthorizedActions:         prior,
				LauncherPreauthorizedActions: []string{"Bash(gh pr create:*)", oldGrant},
				ExplicitAllowedTools:         []string{explicit},
			}

			var replayed launch.Record
			oldObserver := launchPlanObserver
			t.Cleanup(func() { launchPlanObserver = oldObserver })
			launchPlanObserver = func(got launch.Record, _ []string) { replayed = got }
			args := append([]string{"--dry-run", "--no-bootstrap"}, launchArgsFromRecord(rec)...)
			_, _, err := captureOutput(t, func() error { return runLaunch(args) })
			if err != nil {
				t.Fatalf("replay runLaunch: %v", err)
			}
			joined := strings.Join(replayed.Argv, " ")
			if strings.Contains(joined, oldGrant) {
				t.Fatalf("revoked grant survived replay: argv=%v audit=%v", replayed.Argv, replayed.PreauthorizedActions)
			}
			for _, want := range tc.want {
				if !strings.Contains(joined, want) {
					t.Fatalf("current/explicit grant %q missing: argv=%v audit=%v", want, replayed.Argv, replayed.PreauthorizedActions)
				}
			}
			hasBuiltin := strings.Contains(joined, "Bash(gh pr create:*)")
			if hasBuiltin != tc.wantBuiltin {
				t.Fatalf("built-in presence = %v, want %v: argv=%v", hasBuiltin, tc.wantBuiltin, replayed.Argv)
			}
			if strings.Count(joined, "--allowedTools") != 1 {
				t.Fatalf("replay emitted duplicate allowed-tools flags: %v", replayed.Argv)
			}
			if tc.wantBuiltin && !reflect.DeepEqual(childArgsAllowedTools(replayed.Argv), replayed.PreauthorizedActions) {
				t.Fatalf("new record audit is not the current effective grant: argv=%v audit=%v", childArgsAllowedTools(replayed.Argv), replayed.PreauthorizedActions)
			}
		})
	}
}

func writeReplayAllowlistTeam(t *testing.T, dir string, allow []string, includeQA bool) {
	t.Helper()
	members := []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-401"}}
	if includeQA {
		members = append(members, team.Member{Role: "qa", Binary: "claude", Handle: "qa", Session: "issue-401", PermissionAllowlist: allow})
	}
	if err := team.Write(dir, team.Team{Members: members, Orchestrated: true, Lead: "cto"}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateManagedTmuxLaunchRejectsNonTTY(t *testing.T) {
	t.Setenv(envTmuxTarget, "current-window")
	t.Setenv("TMUX", "/tmp/tmux-1/default,1,0")
	t.Setenv("TMUX_PANE", "%9")
	old := launchStdinIsTerminal
	launchStdinIsTerminal = func() bool { return false }
	t.Cleanup(func() { launchStdinIsTerminal = old })
	err := validateManagedTmuxLaunch(launch.Record{
		Tmux: &launch.TmuxInfo{PaneID: "%9", Target: "current-window"},
	})
	if err == nil || !strings.Contains(err.Error(), "real terminal") {
		t.Fatalf("managed non-tty launch error = %v, want real terminal refusal", err)
	}
}

func TestAMQSupportsRequireWake(t *testing.T) {
	for version, want := range map[string]bool{
		"":         false, // very old amq: env reports no version
		"garbage":  false, // unparseable: never pass an unverified flag
		"0.33.9":   false,
		"0.34.0":   false, // --require-wake landed in 0.34.1
		"0.35":     false, // two-part versions don't parse; pinned so a parser change is visible
		"0.34.1":   true,
		"v0.34.1":  true,
		"0.35.0":   true,
		"1.0.0":    true,
		" 0.34.1 ": true,
	} {
		if got := amqSupportsRequireWake(version); got != want {
			t.Errorf("amqSupportsRequireWake(%q) = %v, want %v", version, got, want)
		}
	}
}

func TestAMQSupportsWakeInject(t *testing.T) {
	for version, want := range map[string]bool{
		"":         false,
		"garbage":  false,
		"0.36.9":   false,
		"0.37.0":   true,
		"v0.37.0":  true,
		"0.38.0":   true,
		" 0.37.0 ": true,
	} {
		if got := amqSupportsWakeInject(version); got != want {
			t.Errorf("amqSupportsWakeInject(%q) = %v, want %v", version, got, want)
		}
	}
}

func TestAMQSupportsWakeInjectMode(t *testing.T) {
	for version, want := range map[string]bool{
		"": false, "garbage": false, "0.41.9": false,
		"0.42.0-rc1": false, "0.42.0": true, "v0.42.0": true,
		"0.42.1-rc1": true, "1.0.0": true,
	} {
		if got := amqSupportsWakeInjectMode(version); got != want {
			t.Errorf("amqSupportsWakeInjectMode(%q) = %v, want %v", version, got, want)
		}
	}
}

func TestAMQSupportsNoGitignore(t *testing.T) {
	for version, want := range map[string]bool{
		"":         false,
		"garbage":  false,
		"0.39.9":   false,
		"0.40.0":   true,
		"v0.40.0":  true,
		"0.41.0":   true,
		" 0.40.0 ": true,
	} {
		if got := amqSupportsNoGitignore(version); got != want {
			t.Errorf("amqSupportsNoGitignore(%q) = %v, want %v", version, got, want)
		}
	}
}

func TestAMQSupportsBaselineExisting(t *testing.T) {
	for version, want := range map[string]bool{
		"": false, "garbage": false, "0.45.9": false,
		"0.46.0-rc1": false, "0.46.0": true, "v0.46.0": true,
		"0.46.1-rc1": true, "1.0.0": true,
	} {
		if got := amqSupportsBaselineExisting(version); got != want {
			t.Errorf("amqSupportsBaselineExisting(%q) = %v, want %v", version, got, want)
		}
	}
}

func TestRunLaunchDryRunRequireWakeVersionGate(t *testing.T) {
	// amq 0.34.1+ launches fail at the door when the wake sidecar cannot
	// acquire its lock (#30): coop exec gains --require-wake by default.
	setupFakeAMQWithVersion(t, "0.34.1")
	stdout, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "--trust", "sandboxed", "custom-agent", "test-prompt"})
	})
	if err != nil {
		t.Fatalf("runLaunch: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "amq coop exec --require-wake custom-agent -- test-prompt") {
		t.Fatalf("amq 0.34.1 launch should pass --require-wake:\n%s", stdout)
	}
}

func TestRunLaunchDryRunRequireWakeWithSessionShape(t *testing.T) {
	// Pin the full production argv shape: --session before --require-wake,
	// both before the binary positional (amq rejects misplaced flags).
	setupFakeAMQWithVersion(t, "0.34.1")
	stdout, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "--session", "issue-96", "--trust", "sandboxed", "custom-agent", "test-prompt"})
	})
	if err != nil {
		t.Fatalf("runLaunch: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "amq coop exec --session issue-96 --require-wake custom-agent -- test-prompt") {
		t.Fatalf("session + require-wake argv shape drifted:\n%s", stdout)
	}
}

func TestRunLaunchNamedProfileDerivesProfileRoot(t *testing.T) {
	setupFakeAMQWithVersion(t, "0.34.1")
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})

	stdout, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{
			"--dry-run", "--no-bootstrap",
			"--team-profile", "review",
			"--session", "issue-96",
			"--trust", "sandboxed",
			"custom-agent", "test-prompt",
		})
	})
	if err != nil {
		t.Fatalf("runLaunch: %v\nstderr:\n%s", err, stderr)
	}
	wantRoot := filepath.Join(dir, ".agent-mail", "review", "issue-96")
	wantCommand := "amq coop exec --root " + wantRoot + " --me custom-agent --require-wake env -- -u AM_SESSION custom-agent test-prompt"
	privateWantCommand := "amq coop exec --root /private" + wantRoot + " --me custom-agent --require-wake env -- -u AM_SESSION custom-agent test-prompt"
	if !strings.Contains(stdout, wantCommand) && !strings.Contains(stdout, privateWantCommand) {
		t.Fatalf("named-profile launch should use derived profile root %q, got:\n%s", wantRoot, stdout)
	}
	if strings.Contains(stdout, "--session issue-96") {
		t.Fatalf("named-profile launch must not exec AMQ by legacy --session shorthand:\n%s", stdout)
	}
}

func TestExactRootChildCommandUnsetsSessionBeforeRealTarget(t *testing.T) {
	target, args := exactRootChildCommand("/opt/agent", []string{"--model", "test"})
	if target != "env" {
		t.Fatalf("target = %q, want env", target)
	}
	want := []string{"-u", "AM_SESSION", "/opt/agent", "--model", "test"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}

func TestRunLaunchDefaultProfileKeepsSessionfulChildShape(t *testing.T) {
	setupFakeAMQWithVersion(t, "0.43.1")
	stdout, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "--session", "issue-96", "custom-agent"})
	})
	if err != nil {
		t.Fatalf("runLaunch: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "amq coop exec --session issue-96 --require-wake custom-agent") {
		t.Fatalf("default-profile launch lost sessionful shape:\n%s", stdout)
	}
	if strings.Contains(stdout, "-u AM_SESSION") {
		t.Fatalf("default-profile launch must retain AM_SESSION:\n%s", stdout)
	}
}

func TestRunLaunchNamedProfileResumeAndDynamicPathsUseExactRootChildShim(t *testing.T) {
	setupFakeAMQWithVersion(t, "0.43.1")
	project := t.TempDir()
	chdir(t, project)
	project, _ = os.Getwd()
	const (
		profile = "review"
		session = "issue-481"
		handle  = "runtime-dev"
	)
	root := filepath.Join(project, ".agent-mail", profile, session)
	rec := launch.Record{
		CWD: project, Binary: "custom-agent", Role: handle, Handle: handle,
		Session: session, Root: root, BaseRoot: root, TeamProfile: profile,
		TeamHome: project, SharedWorkstream: true, Trust: trustModeSandboxed,
	}

	tests := map[string]func() error{
		"resume": func() error {
			args := append([]string{"--dry-run", "--no-bootstrap"}, launchArgsFromRecord(rec)...)
			return runLaunch(args)
		},
		"dynamic member": func() error {
			return runAgentUp([]string{
				"custom-agent", "--dry-run", "--no-bootstrap", "--role", handle, "--me", handle,
				"--session", session, "--root", root, "--team-profile", profile, "--team-home", project,
			})
		},
	}
	for name, run := range tests {
		t.Run(name, func(t *testing.T) {
			stdout, stderr, err := captureOutput(t, run)
			if err != nil {
				t.Fatalf("%s: %v\nstderr:\n%s", name, err, stderr)
			}
			for _, want := range []string{
				"amq coop exec --root " + shellQuote(root), "--me " + handle,
				"env -- -u AM_SESSION custom-agent",
			} {
				if !strings.Contains(stdout, want) {
					t.Errorf("%s output missing %q:\n%s", name, want, stdout)
				}
			}
			if strings.Contains(stdout, "--session "+session) {
				t.Errorf("%s must keep named profile exact-root/sessionless:\n%s", name, stdout)
			}
		})
	}
}

func TestRunLaunchDryRunWakeInjectVersionGate(t *testing.T) {
	setupFakeAMQWithVersion(t, "0.37.0")
	stdout, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{
			"--dry-run", "--no-bootstrap",
			"--wake-inject-via", "/opt/amq-inject",
			"--wake-inject-arg=--pane", "--wake-inject-arg=%42",
			"custom-agent", "test-prompt",
		})
	})
	if err != nil {
		t.Fatalf("runLaunch: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		"--require-wake",
		"--wake-inject-via /opt/amq-inject",
		"--wake-inject-arg=--pane",
		"--wake-inject-arg=%42",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q in:\n%s", want, stdout)
		}
	}
}

func TestRunLaunchDryRunWakeInjectRejectsOldAMQ(t *testing.T) {
	setupFakeAMQWithVersion(t, "0.36.0")
	_, _, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "--wake-inject-via", "/opt/amq-inject", "custom-agent"})
	})
	if err == nil || !strings.Contains(err.Error(), "requires amq 0.37.0 or newer") {
		t.Fatalf("wake-inject old amq error = %v", err)
	}
}

func TestRunLaunchWakeInjectValidatesShape(t *testing.T) {
	setupFakeAMQWithVersion(t, "0.37.0")
	if _, _, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "--wake-inject-arg=x", "codex"})
	}); err == nil || !strings.Contains(err.Error(), "requires --wake-inject-via") {
		t.Fatalf("missing via error = %v", err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "--wake-inject-via", "relative-inject", "codex"})
	}); err == nil || !strings.Contains(err.Error(), "must be an absolute path") {
		t.Fatalf("relative via error = %v", err)
	}
}

func TestRunLaunchDryRunWakeInjectModeNone(t *testing.T) {
	setupFakeAMQWithVersion(t, "0.42.0")
	stdout, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "--wake-inject-mode", "none", "codex"})
	})
	if err != nil {
		t.Fatalf("wake inject none: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "--wake-inject-mode none") {
		t.Fatalf("zero-input wake mode not forwarded:\n%s", stdout)
	}
	for _, args := range [][]string{
		{"--dry-run", "--no-bootstrap", "--wake-inject-mode", "none", "--wake-inject-via", "/opt/inject", "codex"},
		{"--dry-run", "--no-bootstrap", "--wake-inject-mode", "bogus", "codex"},
	} {
		if _, _, err := captureOutput(t, func() error { return runLaunch(args) }); err == nil {
			t.Fatalf("invalid wake inject combination accepted: %v", args)
		}
	}
}

func TestRunLaunchDefaultsManagedBinaryWakeModeToRaw(t *testing.T) {
	for _, tc := range []struct {
		name     string
		binary   string
		launcher string
	}{
		{name: "codex", binary: "codex"},
		{name: "claude-custom-launcher", binary: "claude", launcher: "/opt/custom-launcher"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupFakeAMQWithVersion(t, "0.42.0")
			var observed launch.Record
			oldObserver := launchPlanObserver
			launchPlanObserver = func(rec launch.Record, _ []string) { observed = rec }
			t.Cleanup(func() { launchPlanObserver = oldObserver })
			args := []string{"--dry-run", "--no-bootstrap"}
			if tc.launcher != "" {
				args = append(args, "--launcher", tc.launcher)
			}
			args = append(args, tc.binary)
			stdout, stderr, err := captureOutput(t, func() error { return runLaunch(args) })
			if err != nil {
				t.Fatalf("runLaunch: %v\n%s", err, stderr)
			}
			if !strings.Contains(stdout, "--wake-inject-mode raw") || observed.WakeInjectMode != "raw" || observed.Binary != tc.binary {
				t.Fatalf("managed wake mode not explicit raw: stdout=%s record=%+v", stdout, observed)
			}
		})
	}
}

func TestRunLaunchWritesManagedRawWakeModeRecord(t *testing.T) {
	setupFakeAMQWithVersion(t, "0.42.0")
	t.Setenv(envTmuxTarget, "")
	previousExec := amqSyscallExec
	amqSyscallExec = func(string, []string, []string) error { return nil }
	t.Cleanup(func() { amqSyscallExec = previousExec })

	if _, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--no-bootstrap", "--role", "cto", "--me", "cto", "codex"})
	}); err != nil {
		t.Fatalf("runLaunch: %v\n%s", err, stderr)
	}
	rec, err := launch.Read(filepath.Join(os.Getenv("AMQ_FAKE_ROOT"), "agents", "cto"))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Binary != "codex" || rec.WakeInjectMode != "raw" {
		t.Fatalf("managed launch record = %+v", rec)
	}
}

func TestRunLaunchUnknownBinaryRetainsAutoWakeMode(t *testing.T) {
	setupFakeAMQWithVersion(t, "0.42.0")
	stdout, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "--wake-inject-mode", "auto", "custom-agent"})
	})
	if err != nil {
		t.Fatalf("runLaunch: %v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "--wake-inject-mode auto") {
		t.Fatalf("custom binary auto behavior changed:\n%s", stdout)
	}
}

func TestRunLaunchWakeInjectModeRequiresAMQ042(t *testing.T) {
	setupFakeAMQWithVersion(t, "0.41.9")
	if _, _, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "--wake-inject-mode", "none", "codex"})
	}); err == nil || !strings.Contains(err.Error(), "requires amq 0.42.0 or newer") {
		t.Fatalf("wake inject mode floor error = %v", err)
	}
}

func TestEnsureLauncherExecutableAcceptsSymlinkedExecutable(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "injector")
	if err := os.WriteFile(target, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "injector-link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported in this environment: %v", err)
	}
	if err := ensureLauncherExecutable(link); err != nil {
		t.Fatalf("symlinked executable should validate: %v", err)
	}
}

func TestLaunchArgsFromRecordReplaysNoRequireWake(t *testing.T) {
	// The opt-out answers an environment constraint (wake cannot acquire its
	// lock), so resume/replay must reproduce it, not silently re-enable the
	// gate. Compare with NoDefaultArgs, the precedent it follows.
	rec := launch.Record{Binary: "codex", Handle: "cto", Session: "issue-96", NoRequireWake: true}
	args := launchArgsFromRecord(rec)
	found := false
	for _, a := range args {
		if a == "--no-require-wake" {
			found = true
		}
	}
	if !found {
		t.Fatalf("replay args missing --no-require-wake: %v", args)
	}
}

func TestLaunchArgsFromRecordReplaysNoPreauthorizeInScope(t *testing.T) {
	rec := launch.Record{
		Binary:                "claude",
		Handle:                "qa",
		Role:                  "qa",
		Session:               "issue-401",
		NoPreauthorizeInScope: true,
		PreauthorizedActions:  []string{"Read(/tmp/qa-review/**)"},
		Argv:                  []string{"--allowedTools=Read(/tmp/qa-review/**)"},
	}
	args := launchArgsFromRecord(rec)
	if !containsArg(args, "--no-preauthorize-inscope") {
		t.Fatalf("replay args missing --no-preauthorize-inscope: %v", args)
	}
	if strings.Contains(strings.Join(args, " "), "qa-review") {
		t.Fatalf("replay args retained launcher-owned grant instead of recomposing current policy: %v", args)
	}
	if cmd := emitCommand(rec); !strings.Contains(cmd, "--no-preauthorize-inscope") {
		t.Fatalf("replay command missing opt-out: %s", cmd)
	}
}

func TestRunLaunchDryRunNoRequireWakeOptOut(t *testing.T) {
	setupFakeAMQWithVersion(t, "0.34.1")
	stdout, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "--no-require-wake", "custom-agent", "test-prompt"})
	})
	if err != nil {
		t.Fatalf("runLaunch: %v\nstderr:\n%s", err, stderr)
	}
	if strings.Contains(stdout, "--require-wake") {
		t.Fatalf("--no-require-wake must omit the flag:\n%s", stdout)
	}
}

func TestRunLaunchDryRunNoGitignore(t *testing.T) {
	setupFakeAMQWithVersion(t, "0.40.0")
	stdout, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "--no-gitignore", "custom-agent", "test-prompt"})
	})
	if err != nil {
		t.Fatalf("runLaunch: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "amq coop exec --require-wake --no-gitignore custom-agent --") ||
		!strings.Contains(stdout, "test-prompt") {
		t.Fatalf("no-gitignore launch should pass through to coop exec:\n%s", stdout)
	}
}

func TestRunLaunchNoGitignoreRejectsOldAMQ(t *testing.T) {
	setupFakeAMQWithVersion(t, "0.39.1")
	_, _, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "--no-gitignore", "custom-agent"})
	})
	if err == nil || !strings.Contains(err.Error(), "requires amq 0.40.0 or newer") {
		t.Fatalf("no-gitignore old amq error = %v", err)
	}
}

func TestRunLaunchDryRunOldAMQOmitsRequireWake(t *testing.T) {
	// 0.34.0 predates the flag; passing it would fail every launch.
	setupFakeAMQWithVersion(t, "0.34.0")
	stdout, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "custom-agent", "test-prompt"})
	})
	if err != nil {
		t.Fatalf("runLaunch: %v\nstderr:\n%s", err, stderr)
	}
	if strings.Contains(stdout, "--require-wake") {
		t.Fatalf("amq 0.34.0 must not receive --require-wake:\n%s", stdout)
	}
}

func TestRunLaunchDryRunCustomLauncherWrapsBinary(t *testing.T) {
	setupFakeAMQ(t)

	stdout, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{
			"--dry-run", "--no-bootstrap", "--no-default-args",
			"--launcher", "/opt/launch.sh",
			"--launcher-args=--pull --workspace /x",
			"claude", "test-prompt",
		})
	})
	if err != nil {
		t.Fatalf("runLaunch: %v\nstderr:\n%s", err, stderr)
	}
	// The launcher is exec'd in place of the binary; launcher args precede the
	// agent's child args so the wrapper can forward the trailing ones to claude.
	want := "amq coop exec --require-wake --wake-inject-mode raw /opt/launch.sh -- --pull --workspace /x test-prompt"
	if !strings.Contains(stdout, want) {
		t.Fatalf("stdout missing %q in:\n%s", want, stdout)
	}
}

func TestEnsureLauncherExecutable(t *testing.T) {
	dir := t.TempDir()

	missing := filepath.Join(dir, "nope.sh")
	if err := ensureLauncherExecutable(missing); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("missing launcher: want 'not found' error, got %v", err)
	}

	notExec := filepath.Join(dir, "plain.sh")
	if err := os.WriteFile(notExec, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureLauncherExecutable(notExec); err == nil || !strings.Contains(err.Error(), "not executable") {
		t.Errorf("non-executable launcher: want 'not executable' error, got %v", err)
	}

	if err := ensureLauncherExecutable(dir); err == nil || !strings.Contains(err.Error(), "directory") {
		t.Errorf("directory launcher: want 'directory' error, got %v", err)
	}

	okExec := filepath.Join(dir, "good.sh")
	if err := os.WriteFile(okExec, []byte("#!/bin/sh\nexec claude \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ensureLauncherExecutable(okExec); err != nil {
		t.Errorf("executable launcher: want nil, got %v", err)
	}
}

func TestRunLaunchDryRunTrustedCodexPrependsBypass(t *testing.T) {
	setupFakeAMQ(t)

	stdout, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "--trust", "trusted", "codex", "test-prompt"})
	})
	if err != nil {
		t.Fatalf("runLaunch: %v\nstderr:\n%s", err, stderr)
	}
	want := "amq coop exec --require-wake --wake-inject-mode raw codex -- --dangerously-bypass-approvals-and-sandbox test-prompt"
	if !strings.Contains(stdout, want) {
		t.Fatalf("stdout missing %q in:\n%s", want, stdout)
	}
}

func TestRunLaunchTrustedRejectsNoDefaultArgs(t *testing.T) {
	setupFakeAMQ(t)
	_, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "--trust", "trusted", "--no-default-args", "codex"})
	})
	if err == nil {
		t.Fatalf("expected --trust trusted with --no-default-args to fail\nstderr:\n%s", stderr)
	}
	if !strings.Contains(err.Error(), "--no-default-args") {
		t.Fatalf("error should mention --no-default-args, got %v", err)
	}
}

func TestRunLaunchSandboxedRejectsBypassInCodexArgs(t *testing.T) {
	setupFakeAMQ(t)
	_, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "--codex-args=--dangerously-bypass-approvals-and-sandbox", "codex"})
	})
	if err == nil {
		t.Fatalf("expected sandboxed codex with bypass in --codex-args to fail\nstderr:\n%s", stderr)
	}
	if !strings.Contains(err.Error(), "trusted") {
		t.Fatalf("error should suggest --trust trusted, got %v", err)
	}
}

func TestRunLaunchModelInsertsNativeFlag(t *testing.T) {
	setupFakeAMQ(t)

	stdout, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "--model", "gpt-5", "codex"})
	})
	if err != nil {
		t.Fatalf("runLaunch: %v\nstderr:\n%s", err, stderr)
	}
	want := "--model gpt-5"
	if !strings.Contains(stdout, want) {
		t.Fatalf("stdout missing %q in:\n%s", want, stdout)
	}
}

func TestRunLaunchModelClaudePlacement(t *testing.T) {
	setupFakeAMQ(t)

	stdout, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "--model", "sonnet", "claude"})
	})
	if err != nil {
		t.Fatalf("runLaunch: %v\nstderr:\n%s", err, stderr)
	}
	want := "amq coop exec --require-wake --wake-inject-mode raw claude -- --permission-mode auto --model sonnet"
	if !strings.Contains(stdout, want) {
		t.Fatalf("stdout missing %q in:\n%s", want, stdout)
	}
}

func TestRunLaunchDryRunNoDefaultArgsOptOut(t *testing.T) {
	setupFakeAMQ(t)

	stdout, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "--no-default-args", "codex", "test-prompt"})
	})
	if err != nil {
		t.Fatalf("runLaunch: %v\nstderr:\n%s", err, stderr)
	}
	if strings.Contains(stdout, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("stdout should not include codex default args:\n%s", stdout)
	}
	want := "amq coop exec --require-wake --wake-inject-mode raw codex -- test-prompt"
	if !strings.Contains(stdout, want) {
		t.Fatalf("stdout missing %q in:\n%s", want, stdout)
	}
}

func TestRunLaunchDryRunAddsBinaryArgs(t *testing.T) {
	setupFakeAMQ(t)

	stdout, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "--trust", "trusted", "--codex-args=--enable goals", "codex"})
	})
	if err != nil {
		t.Fatalf("runLaunch: %v\nstderr:\n%s", err, stderr)
	}
	want := "amq coop exec --require-wake --wake-inject-mode raw codex -- --dangerously-bypass-approvals-and-sandbox --enable goals"
	if !strings.Contains(stdout, want) {
		t.Fatalf("stdout missing %q in:\n%s", want, stdout)
	}
}

func TestRunLaunchDryRunSymphonyForCodex(t *testing.T) {
	setupFakeAMQ(t)
	stdout, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "--trust", "sandboxed", "--symphony", "--me", "cto", "codex"})
	})
	if err != nil {
		t.Fatalf("runLaunch: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "amq coop exec") || !strings.Contains(stdout, "codex") {
		t.Fatalf("dry-run stdout missing coop exec command:\n%s", stdout)
	}
	for _, want := range []string{"would patch existing", "WORKFLOW.md", "AMQ Symphony hooks", "handle cto"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("dry-run stderr missing %q:\n%s", want, stderr)
		}
	}
}

func TestDefaultRunSymphonyInitDisablesChildUpdateCheck(t *testing.T) {
	t.Setenv("AMQ_NO_UPDATE_CHECK", "0")
	setupFakeAMQScript(t, `#!/bin/sh
if [ "$AMQ_NO_UPDATE_CHECK" != "1" ]; then
  echo "update check was not disabled" >&2
  exit 91
fi
exit 0
`)

	err := defaultRunSymphonyInit(symphonyInitConfig{
		Workflow: filepath.Join(t.TempDir(), "WORKFLOW.md"),
		Root:     filepath.Join(t.TempDir(), ".agent-mail", "issue-419"),
		Me:       "cto",
	})
	if err != nil {
		t.Fatalf("defaultRunSymphonyInit: %v", err)
	}
}

func TestExecAMQCoopDisablesUpdateCheckWithoutLeakingIdentity(t *testing.T) {
	t.Setenv("AMQ_NO_UPDATE_CHECK", "0")
	t.Setenv("AM_ROOT", "/stale/root")
	t.Setenv("AM_BASE_ROOT", "/stale/base")
	t.Setenv("AM_SESSION", "")
	t.Setenv("AM_ME", "stale")
	t.Setenv("AMQ_GLOBAL_ROOT", "/stale/global")
	previous := amqSyscallExec
	var gotPath string
	var gotArgv, gotEnv []string
	amqSyscallExec = func(path string, argv, env []string) error {
		gotPath = path
		gotArgv = append([]string(nil), argv...)
		gotEnv = append([]string(nil), env...)
		return nil
	}
	t.Cleanup(func() { amqSyscallExec = previous })

	if err := execAMQCoop("/opt/bin/amq", []string{"coop", "exec", "codex"}); err != nil {
		t.Fatalf("execAMQCoop: %v", err)
	}
	if gotPath != "/opt/bin/amq" || !reflect.DeepEqual(gotArgv, []string{"amq", "coop", "exec", "codex"}) {
		t.Fatalf("exec = path %q argv %#v", gotPath, gotArgv)
	}
	if !envHas(gotEnv, "AMQ_NO_UPDATE_CHECK", "1") {
		t.Fatalf("exec environment missing suppression: %#v", gotEnv)
	}
	for _, key := range []string{"AM_ROOT", "AM_BASE_ROOT", "AM_SESSION", "AM_ME", "AMQ_GLOBAL_ROOT"} {
		if envHasPrefix(gotEnv, key, "") {
			t.Fatalf("exec environment leaked %s: %#v", key, gotEnv)
		}
	}
	if got := os.Getenv("AMQ_NO_UPDATE_CHECK"); got != "0" {
		t.Fatalf("parent AMQ_NO_UPDATE_CHECK = %q, want unchanged 0", got)
	}
}

func TestRunLaunchSymphonyRejectsNonCodex(t *testing.T) {
	setupFakeAMQ(t)
	_, _, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--symphony", "claude"})
	})
	if err == nil || !strings.Contains(err.Error(), "--symphony is only supported for Codex agents; got claude") {
		t.Fatalf("expected non-Codex symphony error, got %v", err)
	}
}

func TestRunLaunchDryRunNoDefaultArgsKeepsExplicitBinaryArgs(t *testing.T) {
	setupFakeAMQ(t)

	stdout, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "--no-default-args", "--codex-args=--enable goals", "codex"})
	})
	if err != nil {
		t.Fatalf("runLaunch: %v\nstderr:\n%s", err, stderr)
	}
	if strings.Contains(stdout, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("stdout should not include codex default args:\n%s", stdout)
	}
	want := "amq coop exec --require-wake --wake-inject-mode raw codex -- --enable goals"
	if !strings.Contains(stdout, want) {
		t.Fatalf("stdout missing %q in:\n%s", want, stdout)
	}
}

func TestRunLaunchDryRunConversationCodexResume(t *testing.T) {
	setupFakeAMQ(t)

	stdout, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--trust", "trusted", "--conversation", "cto-thread", "codex"})
	})
	if err != nil {
		t.Fatalf("runLaunch: %v\nstderr:\n%s", err, stderr)
	}
	want := "amq coop exec --require-wake --wake-inject-mode raw codex -- --dangerously-bypass-approvals-and-sandbox resume cto-thread"
	if !strings.Contains(stdout, want) {
		t.Fatalf("stdout missing %q in:\n%s", want, stdout)
	}
}

func TestRunLaunchDryRunConversationCodexResumeSandboxed(t *testing.T) {
	setupFakeAMQ(t)

	stdout, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--trust", "sandboxed", "--conversation", "cto-thread", "codex"})
	})
	if err != nil {
		t.Fatalf("runLaunch: %v\nstderr:\n%s", err, stderr)
	}
	if strings.Contains(stdout, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("sandboxed conversation restore must not include bypass:\n%s", stdout)
	}
	want := "amq coop exec --require-wake --wake-inject-mode raw codex -- resume cto-thread"
	if !strings.Contains(stdout, want) {
		t.Fatalf("stdout missing %q in:\n%s", want, stdout)
	}
}

func TestRunLaunchDryRunConversationAllowsBinaryArgs(t *testing.T) {
	setupFakeAMQ(t)

	stdout, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--trust", "trusted", "--conversation", "cto-thread", "--codex-args=--enable goals", "codex"})
	})
	if err != nil {
		t.Fatalf("runLaunch: %v\nstderr:\n%s", err, stderr)
	}
	want := "amq coop exec --require-wake --wake-inject-mode raw codex -- --dangerously-bypass-approvals-and-sandbox --enable goals resume cto-thread"
	if !strings.Contains(stdout, want) {
		t.Fatalf("stdout missing %q in:\n%s", want, stdout)
	}
}

func TestRunLaunchDryRunConversationClaudeResume(t *testing.T) {
	setupFakeAMQ(t)

	stdout, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--conversation-id", "550e8400-e29b-41d4-a716-446655440000", "claude"})
	})
	if err != nil {
		t.Fatalf("runLaunch: %v\nstderr:\n%s", err, stderr)
	}
	want := "amq coop exec --require-wake --wake-inject-mode raw claude -- --permission-mode auto --resume 550e8400-e29b-41d4-a716-446655440000"
	if !strings.Contains(stdout, want) {
		t.Fatalf("stdout missing %q in:\n%s", want, stdout)
	}
}

func TestRunLaunchDryRunQuotesConversationWithSpaces(t *testing.T) {
	setupFakeAMQ(t)

	stdout, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "--conversation", "cto thread", "codex"})
	})
	if err != nil {
		t.Fatalf("runLaunch: %v\nstderr:\n%s", err, stderr)
	}
	want := "resume 'cto thread'"
	if !strings.Contains(stdout, want) {
		t.Fatalf("stdout missing %q in:\n%s", want, stdout)
	}
}

func TestRunLaunchConversationRejectsPromptArgs(t *testing.T) {
	setupFakeAMQ(t)

	_, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "--conversation", "cto-thread", "codex", "hello prompt"})
	})
	if err == nil {
		t.Fatal("conversation with prompt args should fail")
	}
	if !strings.Contains(err.Error(), "extra codex args") {
		t.Fatalf("error should mention extra codex args, got %v\nstderr:\n%s", err, stderr)
	}
}

func TestRunLaunchConversationRejectsPassthroughArgs(t *testing.T) {
	setupFakeAMQ(t)

	_, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "--conversation", "claude-thread", "claude", "--", "--model", "sonnet"})
	})
	if err == nil {
		t.Fatal("conversation with passthrough args should fail")
	}
	if !strings.Contains(err.Error(), "extra claude args") {
		t.Fatalf("error should mention extra claude args, got %v\nstderr:\n%s", err, stderr)
	}
}

func TestApplyConversationRestoreArgsIsIdempotent(t *testing.T) {
	// Trusted Codex: defaults include bypass, so argv with bypass + resume
	// should round-trip cleanly via the WithDefaults form.
	trustedDefaults := defaultChildArgsForBinaryWithTrust("codex", trustModeTrusted)
	got, err := applyConversationRestoreArgsWithDefaults("codex", []string{"--dangerously-bypass-approvals-and-sandbox", "resume", "abc"}, "abc", trustedDefaults)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "--dangerously-bypass-approvals-and-sandbox resume abc" {
		t.Fatalf("codex args = %v", got)
	}

	// Sandboxed Codex: defaults are empty. argv with just resume should round-trip.
	got, err = applyConversationRestoreArgs("codex", []string{"resume", "abc"}, "abc")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "resume abc" {
		t.Fatalf("sandboxed codex args = %v", got)
	}

	got, err = applyConversationRestoreArgs("claude", []string{"--permission-mode", "auto", "--resume", "abc"}, "abc")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "--permission-mode auto --resume abc" {
		t.Fatalf("claude args = %v", got)
	}
}

func TestApplyConversationRestoreArgsAllowsConfiguredDefaults(t *testing.T) {
	defaults := []string{"--dangerously-bypass-approvals-and-sandbox", "--enable", "goals"}
	got, err := applyConversationRestoreArgsWithDefaults("codex", defaults, "abc", defaults)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "--dangerously-bypass-approvals-and-sandbox --enable goals resume abc" {
		t.Fatalf("codex args = %v", got)
	}
}

func TestApplyConversationRestoreArgsRejectsConflicts(t *testing.T) {
	if _, err := applyConversationRestoreArgs("codex", []string{"resume", "other"}, "abc"); err == nil {
		t.Fatal("codex conflicting resume should fail")
	}
	if _, err := applyConversationRestoreArgs("claude", []string{"--continue"}, "abc"); err == nil {
		t.Fatal("claude continue plus conversation should fail")
	}
	if _, err := applyConversationRestoreArgs("node", nil, "abc"); err == nil {
		t.Fatal("unsupported binary should fail")
	}
	if _, err := applyConversationRestoreArgs("codex", []string{"--dangerously-bypass-approvals-and-sandbox", "prompt"}, "abc"); err == nil {
		t.Fatal("codex extra args plus conversation should fail")
	}
	if _, err := applyConversationRestoreArgs("claude", []string{"--permission-mode", "auto", "--model", "sonnet"}, "abc"); err == nil {
		t.Fatal("claude extra args plus conversation should fail")
	}
	if _, err := applyConversationRestoreArgs("codex", []string{"--dangerously-bypass-approvals-and-sandbox", "resume", "abc", "--model", "gpt-5"}, "abc"); err == nil {
		t.Fatal("codex native resume plus extra args should fail")
	}
	if _, err := applyConversationRestoreArgs("claude", []string{"--permission-mode", "auto", "--resume", "abc", "--model", "sonnet"}, "abc"); err == nil {
		t.Fatal("claude native resume plus extra args should fail")
	}
}

func setupFakeAMQ(t *testing.T) {
	t.Helper()
	setupFakeAMQWithVersion(t, "0.42.1")
}

// setupFakeAMQWithVersion installs a fake amq whose `env --json` reports the
// given amq_version (empty omits the field, matching very old amq builds).
func setupFakeAMQWithVersion(t *testing.T, version string) {
	t.Helper()
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, ".agent-mail")
	script := `#!/bin/sh
if [ "$1" = "env" ]; then
  if [ -n "$AMQ_FAKE_VERSION" ]; then
    printf '{"root":"%s","amq_version":"%s"}\n' "$AMQ_FAKE_ROOT" "$AMQ_FAKE_VERSION"
  else
    printf '{"root":"%s"}\n' "$AMQ_FAKE_ROOT"
  fi
  exit 0
fi
echo "unexpected amq command: $*" >&2
exit 1
`
	if err := os.WriteFile(filepath.Join(binDir, "amq"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AMQ_FAKE_ROOT", root)
	t.Setenv("AMQ_FAKE_VERSION", version)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func captureOutput(t *testing.T, fn func() error) (string, string, error) {
	t.Helper()
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	os.Stdout = stdoutW
	os.Stderr = stderrW
	type readResult struct {
		data []byte
		err  error
	}
	stdoutCh := make(chan readResult, 1)
	stderrCh := make(chan readResult, 1)
	go func() {
		data, err := io.ReadAll(stdoutR)
		stdoutCh <- readResult{data: data, err: err}
	}()
	go func() {
		data, err := io.ReadAll(stderrR)
		stderrCh <- readResult{data: data, err: err}
	}()
	runErr := fn()
	if err := stdoutW.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stderrW.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = oldStdout
	os.Stderr = oldStderr

	stdout := <-stdoutCh
	if stdout.err != nil {
		t.Fatal(stdout.err)
	}
	stderr := <-stderrCh
	if stderr.err != nil {
		t.Fatal(stderr.err)
	}
	return string(stdout.data), string(stderr.data), runErr
}

// TestRunLaunchDryRunSessionAndRootDropsRoot covers the third call site of
// the session+root mutual-exclusion fix: the coopArgs builder in
// runLaunch must not pass --root to `amq coop exec` when --session is
// already set, matching the boundary policy in resolveAMQEnvInDir. Without
// this, even after restore.go stops emitting both, a caller who passes
// both flags to `agent up` directly would still trip the same rejection
// when launch.go re-builds the coop exec invocation.
func TestRunLaunchDryRunSessionAndRootDropsRoot(t *testing.T) {
	setupFakeAMQ(t)

	stdout, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{
			"--dry-run", "--no-bootstrap",
			"--session", "stream1",
			"--root", "/p/.agent-mail",
			"codex",
		})
	})
	if err != nil {
		t.Fatalf("runLaunch: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "--session stream1") {
		t.Fatalf("coop exec must keep --session stream1:\n%s", stdout)
	}
	if strings.Contains(stdout, "--root") {
		t.Fatalf("coop exec must not emit --root alongside --session (amq rejects the combo):\n%s", stdout)
	}
}
