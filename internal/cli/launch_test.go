package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunLaunchDryRunSandboxedCodexOmitsBypassDefault(t *testing.T) {
	setupFakeAMQ(t)

	stdout, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "codex", "test-prompt"})
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
	want := "amq coop exec codex -- --model gpt-5"
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
		return runLaunch([]string{"--dry-run", "--conversation", "cto-thread", "codex"})
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
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, ".agent-mail")
	script := `#!/bin/sh
if [ "$1" = "env" ]; then
  printf '{"root":"%s"}\n' "$AMQ_FAKE_ROOT"
  exit 0
fi
echo "unexpected amq command: $*" >&2
exit 1
`
	if err := os.WriteFile(filepath.Join(binDir, "amq"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AMQ_FAKE_ROOT", root)
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
