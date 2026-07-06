package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

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
	want := "amq coop exec codex -- test-prompt"
	if !strings.Contains(stdout, want) {
		t.Fatalf("stdout missing %q in:\n%s", want, stdout)
	}
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

func TestRunLaunchDryRunRequireWakeVersionGate(t *testing.T) {
	// amq 0.34.1+ launches fail at the door when the wake sidecar cannot
	// acquire its lock (#30): coop exec gains --require-wake by default.
	setupFakeAMQWithVersion(t, "0.34.1")
	stdout, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "--trust", "sandboxed", "codex", "test-prompt"})
	})
	if err != nil {
		t.Fatalf("runLaunch: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "amq coop exec --require-wake codex -- test-prompt") {
		t.Fatalf("amq 0.34.1 launch should pass --require-wake:\n%s", stdout)
	}
}

func TestRunLaunchDryRunRequireWakeWithSessionShape(t *testing.T) {
	// Pin the full production argv shape: --session before --require-wake,
	// both before the binary positional (amq rejects misplaced flags).
	setupFakeAMQWithVersion(t, "0.34.1")
	stdout, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "--session", "issue-96", "--trust", "sandboxed", "codex", "test-prompt"})
	})
	if err != nil {
		t.Fatalf("runLaunch: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "amq coop exec --session issue-96 --require-wake codex -- test-prompt") {
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
			"codex", "test-prompt",
		})
	})
	if err != nil {
		t.Fatalf("runLaunch: %v\nstderr:\n%s", err, stderr)
	}
	wantRoot := filepath.Join(dir, ".agent-mail", "review", "issue-96")
	wantCommand := "amq coop exec --root " + wantRoot + " --require-wake codex -- test-prompt"
	privateWantCommand := "amq coop exec --root /private" + wantRoot + " --require-wake codex -- test-prompt"
	if !strings.Contains(stdout, wantCommand) && !strings.Contains(stdout, privateWantCommand) {
		t.Fatalf("named-profile launch should use derived profile root %q, got:\n%s", wantRoot, stdout)
	}
	if strings.Contains(stdout, "--session issue-96") {
		t.Fatalf("named-profile launch must not exec AMQ by legacy --session shorthand:\n%s", stdout)
	}
}

func TestRunLaunchDryRunWakeInjectVersionGate(t *testing.T) {
	setupFakeAMQWithVersion(t, "0.37.0")
	stdout, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{
			"--dry-run", "--no-bootstrap",
			"--wake-inject-via", "/opt/amq-inject",
			"--wake-inject-arg=--pane", "--wake-inject-arg=%42",
			"codex", "test-prompt",
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
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "--wake-inject-via", "/opt/amq-inject", "codex"})
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

func TestRunLaunchDryRunNoRequireWakeOptOut(t *testing.T) {
	setupFakeAMQWithVersion(t, "0.34.1")
	stdout, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "--no-require-wake", "codex", "test-prompt"})
	})
	if err != nil {
		t.Fatalf("runLaunch: %v\nstderr:\n%s", err, stderr)
	}
	if strings.Contains(stdout, "--require-wake") {
		t.Fatalf("--no-require-wake must omit the flag:\n%s", stdout)
	}
}

func TestRunLaunchDryRunOldAMQOmitsRequireWake(t *testing.T) {
	// 0.34.0 predates the flag; passing it would fail every launch.
	setupFakeAMQWithVersion(t, "0.34.0")
	stdout, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "codex", "test-prompt"})
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
	want := "amq coop exec /opt/launch.sh -- --pull --workspace /x test-prompt"
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
	want := "amq coop exec codex -- --dangerously-bypass-approvals-and-sandbox test-prompt"
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
	want := "amq coop exec claude -- --permission-mode auto --model sonnet"
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
	want := "amq coop exec codex -- test-prompt"
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
	want := "amq coop exec codex -- --dangerously-bypass-approvals-and-sandbox --enable goals"
	if !strings.Contains(stdout, want) {
		t.Fatalf("stdout missing %q in:\n%s", want, stdout)
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
	want := "amq coop exec codex -- --enable goals"
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
	want := "amq coop exec codex -- --dangerously-bypass-approvals-and-sandbox resume cto-thread"
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
	want := "amq coop exec codex -- resume cto-thread"
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
	want := "amq coop exec codex -- --dangerously-bypass-approvals-and-sandbox --enable goals resume cto-thread"
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
	want := "amq coop exec claude -- --permission-mode auto --resume 550e8400-e29b-41d4-a716-446655440000"
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
	setupFakeAMQWithVersion(t, "")
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
