package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestSilentRunShellPayloadSuppressesOutputAndForcesZeroExit is the fast,
// tmux-independent regression guard for #525: every payload amq-squad hands
// to `tmux run-shell -b` must be silent and zero-exit BY CONSTRUCTION,
// regardless of what the wrapped command prints or exits with.
func TestSilentRunShellPayloadSuppressesOutputAndForcesZeroExit(t *testing.T) {
	controlRoot := t.TempDir()
	noisyFailingCmd := "printf 'stdout noise\\n'; printf 'stderr noise\\n' 1>&2; exit 7"
	payload := silentRunShellPayload(controlRoot, noisyFailingCmd)

	out, err := exec.Command("/bin/sh", "-c", payload).CombinedOutput()
	if err != nil {
		t.Fatalf("wrapped payload must always exit 0, got err=%v output=%q", err, out)
	}
	if len(out) != 0 {
		t.Fatalf("wrapped payload must produce no combined output, got %q", out)
	}
	logBody, readErr := os.ReadFile(runShellLogPath(controlRoot))
	if readErr != nil {
		t.Fatalf("run-shell log: %v", readErr)
	}
	if !strings.Contains(string(logBody), "stdout noise") || !strings.Contains(string(logBody), "stderr noise") {
		t.Fatalf("run-shell log must still capture the suppressed output for diagnostics, got %q", logBody)
	}
}

func TestSilentRunShellPayloadAppendsAcrossInvocationsNeverTruncates(t *testing.T) {
	controlRoot := t.TempDir()
	for i := 0; i < 2; i++ {
		payload := silentRunShellPayload(controlRoot, fmt.Sprintf("echo call-%d", i))
		if out, err := exec.Command("/bin/sh", "-c", payload).CombinedOutput(); err != nil || len(out) != 0 {
			t.Fatalf("call %d: err=%v out=%q", i, err, out)
		}
	}
	body, err := os.ReadFile(runShellLogPath(controlRoot))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "call-0") || !strings.Contains(string(body), "call-1") {
		t.Fatalf("log must accumulate across invocations (append, never truncate), got %q", body)
	}
}

func TestRunShellLogPathIsUnderControlRootAmqSquadDir(t *testing.T) {
	if got, want := runShellLogPath("/proj"), filepath.Join("/proj", ".amq-squad", "run-shell.log"); got != want {
		t.Fatalf("runShellLogPath = %q, want %q", got, want)
	}
}

// TestRunShellScheduledPayloadsAgainstBogusPaneNeverEnterViewMode is the
// field-realistic regression guard (#525): it schedules the EXACT payload
// forms produced by claudeRenameHelperStart and layoutFinalizationScheduler
// against a bogus pane in a real (but detached, headless) tmux session, then
// uses the issue's own detection method — `tmux display -p '#{pane_mode}'`
// — to prove neither ever pushes the session's pane into view-mode. Pane
// mode is server-side state, independent of any attached client, so no PTY
// is required to observe the bug or its fix.
func TestRunShellScheduledPayloadsAgainstBogusPaneNeverEnterViewMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tmux requires a Unix platform")
	}
	if testing.Short() {
		t.Skip("real tmux run-shell coverage")
	}
	tmuxBin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux is unavailable")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is unavailable")
	}

	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve repository source path")
	}
	repo := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	buildDir := t.TempDir()
	binary := filepath.Join(buildDir, "amq-squad")
	build := exec.Command(goBin, "build", "-o", binary, "./cmd/amq-squad")
	build.Dir = repo
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build amq-squad: %v\n%s", err, out)
	}

	socket := fmt.Sprintf("t9-525-%d", os.Getpid())
	tmuxTmp := shortTestTempDir(t, "t9-525-tmux-")
	var tmuxEnv []string
	for _, item := range os.Environ() {
		if strings.HasPrefix(item, "TMUX=") || strings.HasPrefix(item, "TMUX_PANE=") || strings.HasPrefix(item, "TMUX_TMPDIR=") {
			continue
		}
		tmuxEnv = append(tmuxEnv, item)
	}
	tmuxEnv = append(tmuxEnv, "TMUX_TMPDIR="+tmuxTmp)
	dir := t.TempDir()
	tmuxRun := func(args ...string) ([]byte, error) {
		cmd := exec.Command(tmuxBin, append([]string{"-L", socket}, args...)...)
		cmd.Dir = dir
		cmd.Env = tmuxEnv
		return cmd.CombinedOutput()
	}
	defer func() { _, _ = tmuxRun("kill-server") }()

	created, err := tmuxRun("new-session", "-d", "-x", "80", "-y", "24", "-P", "-F", "#{pane_id}", "-s", "work")
	msg := strings.ToLower(string(created))
	if err != nil {
		msg += " " + strings.ToLower(err.Error())
	}
	if strings.Contains(msg, "operation not permitted") || strings.Contains(msg, "permission denied") {
		t.Skipf("tmux socket access unavailable: %v\n%s", err, created)
	}
	if err != nil {
		if os.Getenv("CODEX_SANDBOX") != "" {
			t.Skipf("tmux socket access unavailable: %v\n%s", err, created)
		}
		t.Fatalf("create tmux session: %v\n%s", err, created)
	}
	paneID := strings.TrimSpace(string(created))
	if paneID == "" {
		t.Fatalf("tmux pane id = %q", created)
	}

	controlRoot := t.TempDir()

	assertNeverEntersViewMode := func(label string) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for {
			mode, _ := tmuxRun("display-message", "-p", "-t", paneID, "#{pane_mode}")
			if strings.TrimSpace(string(mode)) == "" {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: pane entered %q (view-mode overlay leaked, #525)", label, strings.TrimSpace(string(mode)))
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	// 1. The rename helper's exact scheduled form, targeting a pane that
	// does not exist (the #525 field trigger: agent churn racing the delay).
	renamePayload := silentRunShellPayload(controlRoot, shellCommand(binary,
		claudeRenameHelperCommand, "--pane", "%99999", "--name", "ghost", "--delay", "0s"))
	if out, err := tmuxRun("run-shell", "-b", renamePayload); err != nil {
		t.Fatalf("schedule rename payload: %v\n%s", err, out)
	}
	time.Sleep(300 * time.Millisecond)
	assertNeverEntersViewMode("claude rename against a bogus pane")

	// 2. Layout finalization's exact scheduled form, targeting a bogus lead
	// pane/window so every step calls fail() and every tmux sub-command in
	// the script errors.
	layoutScript := layoutFinalizationScript(layoutFinalizationPlan{
		// A ParentPID that is not actually running lets the bounded parent
		// wait loop exit immediately instead of ticking for real time.
		ParentPID: 999999, LauncherPaneID: "%99998", LeadPaneID: "%99999", LeadWindowID: "@99999",
		WarningPath: filepath.Join(controlRoot, "layout.warning"),
	})
	layoutPayload := silentRunShellPayload(controlRoot, layoutScript)
	if out, err := tmuxRun("run-shell", "-b", layoutPayload); err != nil {
		t.Fatalf("schedule layout finalization payload: %v\n%s", err, out)
	}
	time.Sleep(300 * time.Millisecond)
	assertNeverEntersViewMode("layout finalization against a bogus lead pane")

	if _, err := os.Stat(runShellLogPath(controlRoot)); err != nil {
		t.Fatalf("run-shell log must exist so suppressed diagnostics stay observable: %v", err)
	}
}
