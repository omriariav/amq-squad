package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/amqexec"
)

const realAMQCoopWakeDoorbell = ": AMQ doorbell run amq drain --include-body then act on it"

type realWakeHarness struct {
	t           *testing.T
	tmux        string
	amq         string
	project     string
	root        string
	session     string
	tmuxSession string
	initialTmux string
	tmuxTmpDir  string
	recorder    string
	capture     string
	ready       string
	release     string
}

func (h *realWakeHarness) env() []string {
	path := filepath.Dir(h.amq) + string(os.PathListSeparator) + os.Getenv("PATH")
	env := withoutRealWakeEnv(envWithoutAMQIdentity(os.Environ()), "TMUX", "TMUX_PANE", "TMUX_TMPDIR")
	return amqexec.NoUpdateCheckEnv(append(env, "PATH="+path, "TMUX_TMPDIR="+h.tmuxTmpDir, "WAKE_CAPTURE="+h.capture, "WAKE_READY="+h.ready))
}

func (h *realWakeHarness) init(handles ...string) {
	h.t.Helper()
	realWakeCommand(h.t, h.project, h.env(), h.amq, "init", "--root", h.root, "--agents", strings.Join(handles, ","))
}

func waitForRealWakeFile(t *testing.T, path, label string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s at %s", label, path)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func waitForRealWakeCondition(t *testing.T, label string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", label)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func nonemptyLines(raw string) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r", ""), "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func withoutRealWakeEnv(env []string, keys ...string) []string {
	drop := make(map[string]bool, len(keys))
	for _, key := range keys {
		drop[key] = true
	}
	out := make([]string, 0, len(env))
	for _, item := range env {
		key, _, _ := strings.Cut(item, "=")
		if !drop[key] {
			out = append(out, item)
		}
	}
	return out
}

func realWakeCommand(t *testing.T, dir string, env []string, binary string, args ...string) string {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	if env == nil {
		env = amqexec.NoUpdateCheckEnv(envWithoutAMQIdentity(os.Environ()))
	}
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s %s: %v\nstdout:\n%s\nstderr:\n%s", binary, strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return stdout.String()
}
