package cli

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func TestTmuxHarnessExecRequiresCommandAndShellRejectsArgs(t *testing.T) {
	if err := runTmuxHarness([]string{"exec"}); err == nil || !strings.Contains(err.Error(), "requires COMMAND") {
		t.Fatalf("missing command error = %v", err)
	}
	if err := runTmuxHarness([]string{"shell", "extra"}); err == nil || !strings.Contains(err.Error(), "does not take") {
		t.Fatalf("shell argument error = %v", err)
	}
}

func TestTmuxHarnessExecPreservesCommandStdout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tmux requires a Unix platform")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skipf("tmux unavailable: %v", err)
	}
	t.Setenv("TMUX", "/live/socket,1,0")
	t.Setenv("TMUX_PANE", "%999")
	stdout, stderr, err := captureOutput(t, func() error {
		return runTmuxHarness([]string{"exec", "--cwd", t.TempDir(), "--", "/bin/sh", "-c", "printf exact-command-output"})
	})
	if err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "operation not permitted") || strings.Contains(msg, "permission denied") || os.Getenv("CODEX_SANDBOX") != "" {
			t.Skipf("tmux socket access unavailable: %v", err)
		}
		t.Fatal(err)
	}
	if stdout != "exact-command-output" {
		t.Fatalf("stdout = %q, want exact command output only", stdout)
	}
	for _, want := range []string{"tmux harness socket:", "session=$", "window=@", "pane=%", "cleanup: automatic"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr)
		}
	}
}
