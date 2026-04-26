package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunLaunchDryRunPrependsCodexDefaultArgsWithPrompt(t *testing.T) {
	setupFakeAMQ(t)

	stdout, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "codex", "test-prompt"})
	})
	if err != nil {
		t.Fatalf("runLaunch: %v\nstderr:\n%s", err, stderr)
	}
	want := "amq coop exec codex -- --dangerously-bypass-approvals-and-sandbox test-prompt"
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
	runErr := fn()
	if err := stdoutW.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stderrW.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = oldStdout
	os.Stderr = oldStderr

	stdout, err := io.ReadAll(stdoutR)
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := io.ReadAll(stderrR)
	if err != nil {
		t.Fatal(err)
	}
	return string(stdout), string(stderr), runErr
}
