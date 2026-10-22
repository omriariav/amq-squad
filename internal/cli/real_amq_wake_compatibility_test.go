package cli

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/amqexec"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

const realAMQCoopWakeDoorbell = ": AMQ doorbell run amq drain --include-body then act on it"

func buildRealWakeRecorder(t *testing.T, dir string) string {
	t.Helper()
	buildDir := t.TempDir()
	source := filepath.Join(buildDir, "main.go")
	binary := filepath.Join(buildDir, "wake-recorder")
	program := `package main

import (
	"bufio"
	"fmt"
	"os"
	"time"
)

func main() {
	capture := os.Getenv("WAKE_CAPTURE")
	ready := os.Getenv("WAKE_READY")
	f, err := os.OpenFile(capture, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()
	// The lifecycle resume path starts from an interactive shell. Disable its
	// inherited bracketed-paste terminal mode so this native line recorder has
	// the same plain-input contract as the injection-only shell recorder.
	fmt.Fprint(os.Stdout, "\x1b[?2004l")
	if err := os.WriteFile(ready, []byte("ready"), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if release := os.Getenv("WAKE_RELEASE"); release != "" {
		for {
			if _, err := os.Stat(release); err == nil {
				break
			} else if !os.IsNotExist(err) {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		if _, err := fmt.Fprintln(f, scanner.Text()); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := f.Sync(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
`
	if err := os.WriteFile(source, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	realWakeCommand(t, dir, nil, "go", "build", "-o", binary, source)
	return binary
}

func installNativeRealWakeRecorder(t *testing.T, source, target string) {
	t.Helper()
	b, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, b, 0o755); err != nil {
		t.Fatal(err)
	}
}

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
	h.initialTmux = h.tmuxSession
	if err := os.MkdirAll(h.tmuxTmpDir, 0o700); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n: > \"$WAKE_CAPTURE\"\nprintf ready > \"$WAKE_READY\"\nwhile IFS= read -r line; do printf '%s\\n' \"$line\" >> \"$WAKE_CAPTURE\"; done\n"
	if err := os.WriteFile(h.recorder, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, session := range []string{h.initialTmux, h.tmuxSession} {
			cmd := exec.Command(tmux, "kill-session", "-t", session)
			cmd.Env = h.env()
			_ = cmd.Run()
		}
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
	h.startSession(h.tmuxSession, h.capture, h.ready, argv)
}

func (h *realWakeHarness) startBusy(argv []string) {
	h.t.Helper()
	h.release = filepath.Join(h.project, "release-input")
	h.start(argv)
}

func (h *realWakeHarness) releaseBusy() {
	h.t.Helper()
	if strings.TrimSpace(h.release) == "" {
		h.t.Fatal("busy recorder release path is empty")
	}
	if err := os.WriteFile(h.release, []byte("release"), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

func (h *realWakeHarness) startAuxiliary(session, capture, ready string, argv []string) {
	h.t.Helper()
	h.t.Cleanup(func() {
		cmd := exec.Command(h.tmux, "kill-session", "-t", session)
		cmd.Env = h.env()
		_ = cmd.Run()
	})
	h.startSession(session, capture, ready, argv)
}

func (h *realWakeHarness) startSession(session, capture, ready string, argv []string) {
	h.t.Helper()
	envArgs := []string{"WAKE_CAPTURE=" + capture, "WAKE_READY=" + ready, "PATH=" + filepath.Dir(h.amq) + string(os.PathListSeparator) + os.Getenv("PATH"), "AMQ_NO_UPDATE_CHECK=1"}
	if strings.TrimSpace(h.release) != "" {
		envArgs = append(envArgs, "WAKE_RELEASE="+h.release)
	}
	command := shellCommand("env", append(envArgs, argv...)...)
	realWakeCommand(h.t, h.project, h.env(), h.tmux, "new-session", "-d", "-s", session, "-c", h.project, command)
	deadline := time.Now().Add(8 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cmd := exec.Command(h.tmux, "capture-pane", "-p", "-t", session, "-S", "-120")
			cmd.Env = h.env()
			pane, captureErr := cmd.CombinedOutput()
			h.t.Fatalf("timed out waiting for recorder readiness at %s; capture_err=%v\npane:\n%s", ready, captureErr, string(pane))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (h *realWakeHarness) killSessionIfPresent(session string) {
	h.t.Helper()
	listed := realWakeCommand(h.t, h.project, h.env(), h.tmux, "list-sessions", "-F", "#{session_id} #{session_name}")
	for _, line := range nonemptyLines(listed) {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == session {
			realWakeCommand(h.t, h.project, h.env(), h.tmux, "kill-session", "-t", fields[0])
			return
		}
	}
}

func (h *realWakeHarness) sessionExists(session string) bool {
	h.t.Helper()
	cmd := exec.Command(h.tmux, "list-sessions", "-F", "#{session_name}")
	cmd.Env = h.env()
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	for _, name := range nonemptyLines(string(out)) {
		if name == session {
			return true
		}
	}
	return false
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

func assertRealWakeStopped(
	t *testing.T,
	h *realWakeHarness,
	agentDir, handle string,
	agentPID, wakePID int,
	tmuxSession string,
	allowExactStaleLock bool,
) bool {
	t.Helper()
	waitForRealWakePIDExit(t, "agent", agentPID)
	waitForRealWakePIDExit(t, "wake", wakePID)
	waitForRealWakeCondition(t, "tmux session cleanup", func() bool {
		return !h.sessionExists(tmuxSession)
	})
	staleLockPreserved := false
	if _, err := os.Stat(filepath.Join(agentDir, ".wake.lock")); err == nil {
		if !allowExactStaleLock {
			t.Fatal("wake lock survived stop")
		}
		lock, readErr := readWakeLock(agentDir)
		if readErr != nil || lock.PID != wakePID || lock.Agent != handle || !rootsMatch(lock.Root, h.root) {
			t.Fatalf(
				"wake lock survived stop without exact dead-pid identity: lock=%+v read_err=%v want pid=%d agent=%s root=%s",
				lock,
				readErr,
				wakePID,
				handle,
				h.root,
			)
		}
		staleLockPreserved = true
	} else if !os.IsNotExist(err) {
		t.Fatalf("inspect wake lock after stop: %v", err)
	}
	presence, err := readPresenceForEntry(agentDir)
	if err != nil || presence.Status != "offline" {
		t.Fatalf("presence after stop = %+v / %v, want offline", presence, err)
	}
	return staleLockPreserved
}

func assertRealWakeProcessIdentity(t *testing.T, rec launch.Record) {
	t.Helper()
	args, ok := procinfo.Args(rec.AgentPID)
	fields := strings.Fields(args)
	if !ok || len(fields) == 0 {
		t.Fatalf("real procinfo could not read recorded agent pid %d args", rec.AgentPID)
	}
	if got, want := filepath.Base(fields[0]), filepath.Base(rec.Binary); got != want {
		t.Fatalf("recorded agent pid %d first process token basename=%q, want recorded binary basename=%q; args=%q", rec.AgentPID, got, want, args)
	}
	matcher := agentProcessMatcher(rec.Binary)
	if !matcher(args) || !procinfo.Match(rec.AgentPID, matcher) {
		t.Fatalf("recorded agent pid %d did not satisfy the production binary matcher for %q; args=%q", rec.AgentPID, rec.Binary, args)
	}
}

func waitForRealWakePIDExit(t *testing.T, label string, pid int) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for realWakePIDAlive(pid) {
		if time.Now().After(deadline) {
			out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "pid=,ppid=,state=,command=").CombinedOutput()
			t.Fatalf("timed out waiting for %s pid %d exit; ps err=%v output=%q", label, pid, err, strings.TrimSpace(string(out)))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func realWakePIDAlive(pid int) bool {
	return procinfo.Alive(pid)
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

func assertMarkerFreeWake(t *testing.T, got string) {
	t.Helper()
	for _, marker := range []string{"\x1b[200~", "\x1b[201~", "[200~", "[201~"} {
		if strings.Contains(got, marker) {
			t.Fatalf("submitted terminal input contains bracketed-paste marker %q: %q", marker, got)
		}
	}
}

func assertRealAMQCoopWakePayload(t *testing.T, got string) {
	t.Helper()
	assertMarkerFreeWake(t, got)
	if got != realAMQCoopWakeDoorbell {
		t.Fatalf("submitted coop wake = %q, want fixed doorbell %q", got, realAMQCoopWakeDoorbell)
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
	writeRealWakeTeamBinaries(t, project, session, "codex", "codex")
}

func writeRealWakeTeamBinaries(t *testing.T, project, session, leadBinary, qaBinary string) {
	t.Helper()
	if err := team.WriteProfile(project, team.DefaultProfile, team.Team{
		Project: project, Orchestrated: true, Lead: "cto",
		// gh#758/t11 slice B: resume --exec now drives simple_start's
		// shared machinery (buildSimpleStartPlan), which enforces
		// worktree isolation for every launch path -- team_resume.go's
		// old --exec never checked this at all. This fixture's two
		// members share one project cwd deliberately (real-AMQ wake
		// compatibility, not worktree isolation, is under test here).
		SharedCwdException: "real AMQ wake compatibility fixture",
		Members: []team.Member{
			{Role: "cto", Binary: leadBinary, Handle: "cto", Session: session},
			{Role: "qa", Binary: qaBinary, Handle: "qa", Session: session},
		},
	}); err != nil {
		t.Fatal(err)
	}
	// gh#758/t11 slice B: buildSimpleStartPlan reads team-rules.md
	// unconditionally; team_resume.go's old --exec path never did.
	rulesPath := filepath.Join(project, ".amq-squad", "team-rules.md")
	if err := os.MkdirAll(filepath.Dir(rulesPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rulesPath, []byte("test rules\n"), 0o644); err != nil {
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
