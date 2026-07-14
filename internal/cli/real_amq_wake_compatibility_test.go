package cli

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/amqexec"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// TestRealAMQWakeCompatibility is the required macOS floor/latest contract for
// native terminal injection. Unlike the Linux queue compatibility test, every
// case here uses a disposable real tmux PTY and fails (rather than skips) when
// tmux or native wake injection is unavailable after the lane opts in.
func TestRealAMQWakeCompatibility(t *testing.T) {
	amq := strings.TrimSpace(os.Getenv("AMQ_SQUAD_REAL_AMQ"))
	if amq == "" {
		t.Skip("set AMQ_SQUAD_REAL_AMQ to run disposable real-AMQ wake checks")
	}
	requireExecutable(t, amq, "AMQ_SQUAD_REAL_AMQ")
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatalf("required real-wake lane needs tmux: %v", err)
	}
	version := strings.TrimSpace(realWakeCommand(t, t.TempDir(), nil, amq, "version"))
	if expected := strings.TrimSpace(os.Getenv("AMQ_SQUAD_REAL_AMQ_VERSION")); expected != "" && expected != "latest" && strings.TrimPrefix(version, "v") != strings.TrimPrefix(expected, "v") {
		t.Fatalf("real AMQ version = %q, expected requested %q", version, expected)
	}
	t.Logf("real wake compatibility: amq=%s version=%s tmux=%s", amq, version, tmux)

	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	squad := filepath.Join(t.TempDir(), "amq-squad")
	realWakeCommand(t, repo, nil, "go", "build", "-o", squad, "./cmd/amq-squad")

	t.Run("native wake auto and explicit raw", func(t *testing.T) {
		for _, mode := range []string{"auto", "raw"} {
			mode := mode
			t.Run(mode, func(t *testing.T) {
				h := newRealWakeHarness(t, tmux, amq)
				h.init("sender", "codex")
				h.start([]string{amq, "coop", "exec", "--root", h.root, "--me", "codex", "--require-wake", "--wake-inject-mode", mode, h.recorder})
				h.send("sender", "codex", "native-"+mode, "native wake canary")
				line := h.oneSubmittedLine()
				assertMarkerFreeWake(t, line)
				if !strings.Contains(line, "native-"+mode) {
					t.Fatalf("submitted wake = %q, want subject native-%s", line, mode)
				}
			})
		}
	})

	t.Run("managed codex non-identifying handle emits raw and wakes", func(t *testing.T) {
		h := newRealWakeHarness(t, tmux, amq)
		writeRealWakeTeam(t, h.project, h.session)
		h.init("cto", "qa")
		args := []string{"agent", "up", h.recorder, "--project", h.project, "--role", "qa", "--session", h.session, "--team-workstream", "--me", "qa", "--no-bootstrap", "--no-default-args"}
		preview := realWakeCommand(t, h.project, h.env(), squad, append(args, "--dry-run")...)
		if !strings.Contains(preview, "amq coop exec") || !strings.Contains(preview, "--wake-inject-mode raw") || !strings.Contains(preview, "--me qa") {
			t.Fatalf("managed launch did not emit explicit raw coop exec for qa:\n%s", preview)
		}
		h.start(append([]string{squad}, args...))
		rec, err := launch.Read(filepath.Join(h.root, "agents", "qa"))
		if err != nil {
			t.Fatalf("read managed launch record: %v", err)
		}
		if rec.Handle != "qa" || rec.WakeInjectMode != "raw" || rec.Tmux == nil || rec.Tmux.PaneID == "" {
			t.Fatalf("managed launch record lacks qa/raw/real-pane evidence: %+v", rec)
		}
		h.send("cto", "qa", "managed-raw", "managed wake canary")
		line := h.oneSubmittedLine()
		assertMarkerFreeWake(t, line)
		if !strings.Contains(line, "managed-raw") {
			t.Fatalf("submitted managed wake = %q", line)
		}
	})

	t.Run("dispatch force durable plus prompt fallback", func(t *testing.T) {
		h := newRealWakeHarness(t, tmux, amq)
		writeRealWakeTeam(t, h.project, h.session)
		h.init("cto", "qa")
		h.start([]string{h.recorder}) // Deliberately no AMQ wake sidecar.
		paneID := strings.TrimSpace(realWakeCommand(t, h.project, h.env(), tmux, "display-message", "-p", "-t", h.tmuxSession, "#{pane_id}"))
		panePIDText := strings.TrimSpace(realWakeCommand(t, h.project, h.env(), tmux, "display-message", "-p", "-t", paneID, "#{pane_pid}"))
		panePID, err := strconv.Atoi(panePIDText)
		if err != nil {
			t.Fatalf("parse pane pid %q: %v", panePIDText, err)
		}
		tmuxInfo := &launch.TmuxInfo{Session: h.tmuxSession, PaneID: paneID, Target: "new-session"}
		if err := launch.Write(filepath.Join(h.root, "agents", "qa"), launch.Record{
			CWD: h.project, Binary: "codex", Role: "qa", Handle: "qa", Session: h.session,
			SharedWorkstream: true, Root: h.root, BaseRoot: filepath.Dir(h.root), AMQVersion: version,
			WakeInjectMode: "raw", AgentPID: panePID, StartedAt: time.Now().UTC(), Tmux: tmuxInfo,
			Terminal: launch.TerminalInfoFromTmux(tmuxInfo), TeamProfile: team.DefaultProfile, TeamHome: h.project,
		}); err != nil {
			t.Fatalf("write no-wake launch record: %v", err)
		}

		body := "durable dispatch body line one\nline two: $() `literal`"
		out := realWakeCommand(t, h.project, h.env(), squad,
			"dispatch", "--project", h.project, "--session", h.session, "--role", "qa", "--from", "cto",
			"--subject", "real fallback", "--body", body, "--force", "--json")
		var envelope struct {
			Data struct {
				DeliveryReceipt struct {
					Method   string `json:"method"`
					Fallback bool   `json:"fallback"`
				} `json:"delivery_receipt"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(out), &envelope); err != nil {
			t.Fatalf("parse real dispatch JSON: %v\n%s", err, out)
		}
		if envelope.Data.DeliveryReceipt.Method != "durable_amq_plus_prompt_fallback" || !envelope.Data.DeliveryReceipt.Fallback {
			t.Fatalf("real dispatch receipt = %+v", envelope.Data.DeliveryReceipt)
		}
		line := h.oneSubmittedLine()
		assertMarkerFreeWake(t, line)
		if line != dispatchNudgePrompt {
			t.Fatalf("submitted fallback prompt = %q, want fixed %q", line, dispatchNudgePrompt)
		}
		drained := realWakeCommand(t, h.project, h.env(), amq, "drain", "--root", h.root, "--me", "qa", "--include-body")
		if !strings.Contains(drained, body) {
			t.Fatalf("durable body did not drain intact:\n%s", drained)
		}
	})
}

type realWakeHarness struct {
	t           *testing.T
	tmux        string
	amq         string
	project     string
	root        string
	session     string
	tmuxSession string
	tmuxTmpDir  string
	recorder    string
	capture     string
	ready       string
}

func newRealWakeHarness(t *testing.T, tmux, amq string) *realWakeHarness {
	t.Helper()
	project := t.TempDir()
	session := "wake-compat"
	suffix := randomRealWakeSuffix(t)
	h := &realWakeHarness{
		t: t, tmux: tmux, amq: amq, project: project, session: session,
		root:        filepath.Join(project, ".agent-mail", session),
		tmuxSession: "amq-wake-" + suffix,
		tmuxTmpDir:  filepath.Join("/tmp", "amq-wake-tmux-"+suffix),
		recorder:    filepath.Join(project, "codex"), capture: filepath.Join(project, "submitted.txt"), ready: filepath.Join(project, "ready"),
	}
	if err := os.MkdirAll(h.tmuxTmpDir, 0o700); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n: > \"$WAKE_CAPTURE\"\nprintf ready > \"$WAKE_READY\"\nwhile IFS= read -r line; do printf '%s\\n' \"$line\" >> \"$WAKE_CAPTURE\"; done\n"
	if err := os.WriteFile(h.recorder, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd := exec.Command(tmux, "kill-session", "-t", h.tmuxSession)
		cmd.Env = h.env()
		_ = cmd.Run()
		_ = os.RemoveAll(h.tmuxTmpDir)
	})
	return h
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

func (h *realWakeHarness) start(argv []string) {
	h.t.Helper()
	command := shellCommand("env", append([]string{"WAKE_CAPTURE=" + h.capture, "WAKE_READY=" + h.ready, "PATH=" + filepath.Dir(h.amq) + string(os.PathListSeparator) + os.Getenv("PATH"), "AMQ_NO_UPDATE_CHECK=1"}, argv...)...)
	realWakeCommand(h.t, h.project, h.env(), h.tmux, "new-session", "-d", "-s", h.tmuxSession, "-c", h.project, command)
	waitForRealWakeFile(h.t, h.ready, "recorder readiness")
}

func (h *realWakeHarness) send(from, to, subject, body string) {
	h.t.Helper()
	realWakeCommand(h.t, h.project, h.env(), h.amq, "send", "--root", h.root, "--me", from, "--to", to, "--subject", subject, "--body", body, "--kind", "todo")
}

func (h *realWakeHarness) oneSubmittedLine() string {
	h.t.Helper()
	waitForRealWakeFile(h.t, h.capture, "submitted terminal input")
	deadline := time.Now().Add(8 * time.Second)
	for {
		b, err := os.ReadFile(h.capture)
		if err == nil {
			lines := nonemptyLines(string(b))
			if len(lines) == 1 {
				return lines[0]
			}
			if len(lines) > 1 {
				h.t.Fatalf("wake submitted %d lines, want exactly one: %q", len(lines), string(b))
			}
		}
		if time.Now().After(deadline) {
			pane := realWakeCommand(h.t, h.project, h.env(), h.tmux, "capture-pane", "-p", "-t", h.tmuxSession, "-S", "-80")
			h.t.Fatalf("timed out waiting for one submitted line (capture err=%v)\npane:\n%s", err, pane)
		}
		time.Sleep(50 * time.Millisecond)
	}
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

func nonemptyLines(raw string) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r", ""), "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func assertMarkerFreeWake(t *testing.T, got string) {
	t.Helper()
	for _, marker := range []string{"\x1b[200~", "\x1b[201~", "[200~", "[201~"} {
		if strings.Contains(got, marker) {
			t.Fatalf("submitted terminal input contains bracketed-paste marker %q: %q", marker, got)
		}
	}
}

func randomRealWakeSuffix(t *testing.T) string {
	t.Helper()
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatalf("create unique tmux session suffix: %v", err)
	}
	return fmt.Sprintf("%x", raw[:])
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

func writeRealWakeTeam(t *testing.T, project, session string) {
	t.Helper()
	if err := team.WriteProfile(project, team.DefaultProfile, team.Team{
		Project: project, Orchestrated: true, Lead: "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: session},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: session},
		},
	}); err != nil {
		t.Fatal(err)
	}
}

func requireExecutable(t *testing.T, path, label string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s %q: %v", label, path, err)
	}
	if info.IsDir() || info.Mode()&0o111 == 0 {
		t.Fatalf("%s %q is not executable", label, path)
	}
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
