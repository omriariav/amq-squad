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
	"github.com/omriariav/amq-squad/v2/internal/procinfo"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

const realAMQCoopWakeDoorbell = ": AMQ doorbell run amq drain --include-body then act on it"

// TestRealAMQWakeCompatibility is the required macOS floor/current contract,
// with latest rerun as a forward-compatibility canary. Unlike the Linux queue
// compatibility test, every case here uses a disposable real tmux PTY and
// fails (rather than skips) when tmux or native wake injection is unavailable
// after the lane opts in. AMQ v0.48.0's historical Linux-only
// /proc/sys/dev/tty/legacy_tiocsti=0 degradation cannot be exercised by this
// macOS lane; upstream AMQ unit tests own that non-input-notifier path.
func TestRealAMQWakeCompatibility(t *testing.T) {
	amq := strings.TrimSpace(os.Getenv("AMQ_SQUAD_REAL_AMQ"))
	if amq == "" {
		t.Skip("set AMQ_SQUAD_REAL_AMQ to run disposable real-AMQ wake checks")
	}
	isolateRealAMQManagedContext(t)
	requireExecutable(t, amq, "AMQ_SQUAD_REAL_AMQ")
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatalf("required real-wake lane needs tmux: %v", err)
	}
	version := strings.TrimSpace(realWakeCommand(t, t.TempDir(), nil, amq, "version"))
	if !semverMeetsStableFloor(version, doctorMinAMQVersion) {
		t.Fatalf("real AMQ %q is below supported floor %s", version, doctorMinAMQVersion)
	}
	if expected := strings.TrimSpace(os.Getenv("AMQ_SQUAD_REAL_AMQ_VERSION")); expected != "" && expected != "latest" && strings.TrimPrefix(version, "v") != strings.TrimPrefix(expected, "v") {
		t.Fatalf("real AMQ version = %q, expected requested %q", version, expected)
	}
	t.Logf("real wake compatibility: amq=%s version=%s tmux=%s", amq, version, tmux)
	t.Run("exact inject-via wake retirement", func(t *testing.T) {
		realAMQExactInjectViaWakeRetirement(t, amq)
	})

	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	squad := filepath.Join(t.TempDir(), "amq-squad")
	realWakeCommand(t, repo, nil, "go", "build", "-o", squad, "./cmd/amq-squad")
	nativeRecorder := buildRealWakeRecorder(t, repo)

	t.Run("native wake auto and explicit raw", func(t *testing.T) {
		for _, mode := range []string{"auto", "raw"} {
			mode := mode
			t.Run(mode, func(t *testing.T) {
				h := newRealWakeHarness(t, tmux, amq)
				h.init("sender", "codex")
				h.start([]string{amq, "coop", "exec", "--root", h.root, "--me", "codex", "--require-wake", "--wake-inject-mode", mode, h.recorder})
				h.send("sender", "codex", "native-"+mode, "native wake canary")
				line := h.oneSubmittedLine()
				assertRealAMQCoopWakePayload(t, line)
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
		assertRealAMQCoopWakePayload(t, line)
	})

	t.Run("busy managed agent has one terminal-input owner", func(t *testing.T) {
		t.Run("pre-fix control captures duplicate full prompts", func(t *testing.T) {
			lines := realAMQBusyWakeOwnershipCase(t, tmux, amq, squad, nativeRecorder, true)
			t.Logf("pre-fix duplicate reproduction (one durable inbox message): %#v", lines)
		})
		t.Run("verified native wake suppresses notifier fallback", func(t *testing.T) {
			lines := realAMQBusyWakeOwnershipCase(t, tmux, amq, squad, nativeRecorder, false)
			t.Logf("fixed single-owner capture through retry horizon: %#v", lines)
		})
	})

	t.Run("managed stop resume and cleanup", func(t *testing.T) {
		h := newRealWakeHarness(t, tmux, amq)
		leadRecorder := filepath.Join(h.project, "cto-recorder")
		qaRecorder := filepath.Join(h.project, "qa-recorder")
		installNativeRealWakeRecorder(t, nativeRecorder, leadRecorder)
		installNativeRealWakeRecorder(t, nativeRecorder, qaRecorder)
		writeRealWakeTeamBinaries(t, h.project, h.session, leadRecorder, qaRecorder)
		h.init("cto", "qa")
		leadSession := h.tmuxSession + "-lead"
		leadArgs := []string{"agent", "up", leadRecorder, "--project", h.project, "--role", "cto", "--session", h.session, "--team-workstream", "--me", "cto", "--no-bootstrap", "--no-default-args", "--wake-inject-mode", "raw"}
		h.startAuxiliary(leadSession, filepath.Join(h.project, "cto-submitted.txt"), filepath.Join(h.project, "cto-ready"), append([]string{squad}, leadArgs...))
		args := []string{"agent", "up", qaRecorder, "--project", h.project, "--role", "qa", "--session", h.session, "--team-workstream", "--me", "qa", "--no-bootstrap", "--no-default-args", "--wake-inject-mode", "raw"}
		h.start(append([]string{squad}, args...))
		agentDir := filepath.Join(h.root, "agents", "qa")
		initial, err := launch.Read(agentDir)
		if err != nil {
			t.Fatalf("read initial managed launch record: %v", err)
		}
		initialWake, err := readWakeLock(agentDir)
		if err != nil {
			t.Fatalf("read initial managed wake lock: %v", err)
		}
		if initial.AgentPID <= 0 || initialWake.PID <= 0 || initial.Tmux == nil || initial.Tmux.Session != h.tmuxSession {
			t.Fatalf("initial managed launch record lacks pid/wake/tmux evidence: %+v", initial)
		}
		initialPaneID := strings.TrimSpace(realWakeCommand(t, h.project, h.env(), tmux, "display-message", "-p", "-t", h.tmuxSession, "#{pane_id}"))
		if initialPaneID == "" || initial.Tmux.PaneID != initialPaneID {
			t.Fatalf("initial managed launch pane/session = %q/%q, want %q/%q", initial.Tmux.PaneID, initial.Tmux.Session, initialPaneID, h.tmuxSession)
		}
		assertRealWakeProcessIdentity(t, initial)
		initialSession := h.tmuxSession
		stopOut := realWakeCommand(t, h.project, h.env(), squad,
			"down", "--project", h.project, "--profile", team.DefaultProfile, "--session", h.session, "--role", "qa")
		t.Logf("initial managed stop: %s", strings.TrimSpace(stopOut))
		h.killSessionIfPresent(initialSession)
		assertRealWakeStopped(t, h, agentDir, "qa", initial.AgentPID, initialWake.PID, initialSession, false)
		lead, err := launch.Read(filepath.Join(h.root, "agents", "cto"))
		if err != nil {
			t.Fatalf("read managed lead launch record: %v", err)
		}
		if lead.Tmux == nil || lead.Tmux.Session != leadSession {
			t.Fatalf("managed lead launch record lacks expected tmux session: %+v", lead)
		}
		assertRealWakeProcessIdentity(t, lead)
		resumedSession := initialSession + "-resume"
		h.tmuxSession = resumedSession

		if err := os.Remove(h.ready); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if err := os.Remove(h.capture); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		// gh#758/t11 slice B: resume --exec now defaults to the launchapi
		// launch path, same as start/up (gh#755/gh#757) -- which requires a
		// real, adapter-known provider ("codex"/"claude") to compile a
		// launch intent. This fixture's recorder binaries stand in for a
		// real agent generically and are not adapter-known providers, so
		// this resume must opt out to the legacy direct-tmux path, exactly
		// like other recorder-based fixtures already opt `start`/`up` out
		// via --launch-via legacy where needed.
		resumeOut := realWakeCommand(t, h.project, h.env(), squad,
			"resume", "--project", h.project, "--profile", team.DefaultProfile, "--session", h.session,
			"--role", "qa", "--exec", "--launch-via", "legacy", "--target", "new-session", "--terminal-session", resumedSession)
		t.Logf("managed resume: %s", strings.TrimSpace(resumeOut))
		waitForRealWakeFile(t, h.ready, "resumed recorder readiness")
		resumed, err := launch.Read(agentDir)
		if err != nil {
			t.Fatalf("read resumed managed launch record: %v", err)
		}
		resumedWake, err := readWakeLock(agentDir)
		if err != nil {
			t.Fatalf("read resumed managed wake lock: %v", err)
		}
		if resumed.AgentPID <= 0 || resumedWake.PID <= 0 || resumed.AgentPID == initial.AgentPID || resumedWake.PID == initialWake.PID || !resumed.StartedAt.After(initial.StartedAt) || resumed.Tmux == nil || resumed.Tmux.Session != resumedSession {
			t.Fatalf("resumed launch did not record fresh pid/wake/time/tmux evidence: initial=%+v resumed=%+v", initial, resumed)
		}
		resumedPaneID := strings.TrimSpace(realWakeCommand(t, h.project, h.env(), tmux, "display-message", "-p", "-t", resumedSession, "#{pane_id}"))
		if resumedPaneID == "" || resumed.Tmux.PaneID != resumedPaneID {
			t.Fatalf("resumed managed launch pane/session = %q/%q, want %q/%q", resumed.Tmux.PaneID, resumed.Tmux.Session, resumedPaneID, resumedSession)
		}
		assertRealWakeProcessIdentity(t, resumed)
		h.send("cto", "qa", "managed-resume", "managed resume wake canary")
		line := h.oneSubmittedLine()
		assertRealAMQCoopWakePayload(t, line)

		realWakeCommand(t, h.project, h.env(), squad,
			"down", "--project", h.project, "--profile", team.DefaultProfile, "--session", h.session, "--role", "cto")
		stopOut = realWakeCommand(t, h.project, h.env(), squad,
			"down", "--project", h.project, "--profile", team.DefaultProfile, "--session", h.session, "--role", "qa", "--close-panes")
		t.Logf("final managed stop: %s", strings.TrimSpace(stopOut))
		staleLockPreserved := assertRealWakeStopped(
			t, h, agentDir, "qa", resumed.AgentPID, resumedWake.PID, resumedSession, true,
		)
		if staleLockPreserved && !strings.Contains(stopOut, "wake quiescence=fs_stale_lock_preserved") {
			t.Fatalf("final stop preserved the exact stale wake lock without reporting its verified quiescence status:\n%s", stopOut)
		}
	})

	t.Run("dispatch force durable plus prompt fallback", func(t *testing.T) {
		h := newRealWakeHarness(t, tmux, amq)
		writeRealWakeTeamBinaries(t, h.project, h.session, nativeRecorder, nativeRecorder)
		h.init("cto", "qa")
		leadSession := h.tmuxSession + "-lead"
		leadArgs := []string{"agent", "up", nativeRecorder, "--project", h.project, "--role", "cto", "--session", h.session, "--team-workstream", "--me", "cto", "--no-bootstrap", "--no-default-args", "--wake-inject-mode", "raw"}
		h.startAuxiliary(leadSession, filepath.Join(h.project, "cto-submitted.txt"), filepath.Join(h.project, "cto-ready"), append([]string{squad}, leadArgs...))
		h.start([]string{nativeRecorder}) // Deliberately no AMQ wake sidecar.
		paneID := strings.TrimSpace(realWakeCommand(t, h.project, h.env(), tmux, "display-message", "-p", "-t", h.tmuxSession, "#{pane_id}"))
		panePIDText := strings.TrimSpace(realWakeCommand(t, h.project, h.env(), tmux, "display-message", "-p", "-t", paneID, "#{pane_pid}"))
		panePID, err := strconv.Atoi(panePIDText)
		if err != nil {
			t.Fatalf("parse pane pid %q: %v", panePIDText, err)
		}
		tmuxInfo := &launch.TmuxInfo{Session: h.tmuxSession, PaneID: paneID, Target: "new-session"}
		if err := launch.Write(filepath.Join(h.root, "agents", "qa"), launch.Record{
			CWD: h.project, Binary: nativeRecorder, Role: "qa", Handle: "qa", Session: h.session,
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
			Data mutationResult `json:"data"`
		}
		if err := json.Unmarshal([]byte(out), &envelope); err != nil {
			t.Fatalf("parse real dispatch JSON: %v\n%s", err, out)
		}
		statusAccepted := envelope.Data.Status == "queued_and_nudged" || envelope.Data.Status == "queued_nudge_submit_unconfirmed"
		if !statusAccepted || envelope.Data.MessageID == "" || envelope.Data.Root == "" {
			t.Fatalf("real dispatch transport result = %+v", envelope.Data)
		}
		if envelope.Data.TaskID != "" {
			t.Fatalf("taskless real dispatch unexpectedly created task %q", envelope.Data.TaskID)
		}
		if strings.Contains(out, `"delivery_receipt"`) {
			t.Fatalf("real dispatch exposed deleted delivery receipt: %s", out)
		}
		line := h.oneSubmittedLine()
		assertMarkerFreeWake(t, line)
		wantRoot, err := filepath.EvalSymlinks(h.root)
		if err != nil {
			t.Fatalf("canonicalize real dispatch fixture root %q: %v", h.root, err)
		}
		if want := dispatchNudgePrompt(wantRoot); line != want {
			t.Fatalf("submitted fallback prompt = %q, want rooted %q", line, want)
		}
		drained := realWakeCommand(t, h.project, h.env(), amq, "drain", "--root", h.root, "--me", "qa", "--include-body")
		if !strings.Contains(drained, body) {
			t.Fatalf("durable body did not drain intact:\n%s", drained)
		}
	})
}

func realAMQBusyWakeOwnershipCase(t *testing.T, tmux, amq, squad, recorder string, emulateLegacyNotifier bool) []string {
	t.Helper()
	h := newRealWakeHarness(t, tmux, amq)
	writeRealWakeTeamBinaries(t, h.project, h.session, recorder, recorder)
	h.init("cto", "qa")
	args := []string{
		"agent", "up", recorder, "--project", h.project, "--role", "qa", "--session", h.session,
		"--team-workstream", "--me", "qa", "--no-bootstrap", "--no-default-args", "--wake-inject-mode", "raw",
	}
	h.startBusy(append([]string{squad}, args...))
	t.Setenv("TMUX_TMPDIR", h.tmuxTmpDir)
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")

	agentDir := filepath.Join(h.root, "agents", "qa")
	rec, err := launch.Read(agentDir)
	if err != nil {
		t.Fatalf("read busy managed launch record: %v", err)
	}
	if rec.Tmux == nil || strings.TrimSpace(rec.Tmux.PaneID) == "" {
		t.Fatalf("busy managed launch lacks real pane evidence: %+v", rec)
	}
	if !verifiedSessionNotifierWakeLive(agentDir, h.root, team.DefaultProfile, h.session, "qa", rec) {
		wake, wakeErr := readWakeLock(agentDir)
		t.Fatalf("busy managed target lacks positively verified native wake: launch=%+v wake=%+v wake_err=%v", rec, wake, wakeErr)
	}

	h.send("cto", "qa", "busy-single-owner", "one durable message while the agent is busy")
	pending, err := snapshotSessionInboxMessages(h.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("busy target inbox/new contains %d messages, want exactly one: %+v", len(pending), pending)
	}
	var messagePath string
	for path := range pending {
		messagePath = path
	}
	messageID, err := readSessionNotifierMessageID(messagePath)
	if err != nil {
		t.Fatalf("read real AMQ message identity: %v", err)
	}

	// Keep the agent process blocked before its first stdin read. Wait until the
	// native AMQ owner has queued its full raw doorbell in the real tmux PTY,
	// then exercise either the historical unconditional notifier path or the
	// fixed verified-owner gate against that same unread durable message.
	waitForRealWakeCondition(t, "native AMQ doorbell queued while recorder is busy", func() bool {
		cmd := exec.Command(tmux, "capture-pane", "-p", "-t", rec.Tmux.PaneID, "-S", "-80")
		cmd.Env = h.env()
		out, captureErr := cmd.Output()
		return captureErr == nil && strings.Contains(strings.ReplaceAll(string(out), "\r", ""), realAMQCoopWakeDoorbell)
	})
	wakeLive := verifiedSessionNotifierWakeLive
	if emulateLegacyNotifier {
		wakeLive = sessionNotifierWakeAbsent
	}
	stopNotifier := make(chan os.Signal, 1)
	notifierDone := make(chan error, 1)
	sendDone := make(chan error, 1)
	go func() {
		notifierDone <- executeSessionNotifier(sessionNotifierExecution{
			ProjectDir: h.project, Profile: team.DefaultProfile, Session: h.session, Root: h.root,
			Token: "busy-owner-" + strconv.FormatBool(emulateLegacyNotifier),
			TTL:   time.Minute, Heartbeat: time.Hour, Stop: stopNotifier, Now: time.Now,
			WakeLive: wakeLive,
			SendKeys: func(paneID, prompt string) error {
				err := tmuxpane.SendPromptToPane(paneID, prompt)
				sendDone <- err
				return err
			},
		})
	}()
	notifierPath := sessionNotifierRuntimePath(h.project, team.DefaultProfile, h.session)
	deadline := time.Now().Add(8 * time.Second)
	for {
		persisted, readErr := readSessionNotifierRecord(notifierPath)
		if readErr == nil && sessionNotifierAttempted(persisted.AttemptedMessageIDs, "qa", messageID) {
			break
		}
		select {
		case notifierErr := <-notifierDone:
			t.Fatalf("real session notifier exited before reserving %s: %v", messageID, notifierErr)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("real session notifier did not durably reserve %s: read_err=%v", messageID, readErr)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if emulateLegacyNotifier {
		select {
		case notifyErr := <-sendDone:
			t.Logf("pre-fix notifier result while target busy: err=%v", notifyErr)
		case <-time.After(8 * time.Second):
			t.Fatal("pre-fix real session notifier did not attempt the competing prompt")
		}
	} else {
		select {
		case notifyErr := <-sendDone:
			t.Fatalf("verified native wake still allowed notifier input: %v", notifyErr)
		default:
		}
	}

	if !emulateLegacyNotifier {
		// AMQ's external injection timeout is five seconds. Keeping the target
		// unread beyond that first retry horizon proves the fixed notifier does
		// not introduce a second full prompt while durable work remains queued.
		time.Sleep(6 * time.Second)
		select {
		case notifyErr := <-sendDone:
			t.Fatalf("verified native wake allowed delayed notifier input: %v", notifyErr)
		default:
		}
	}
	stopNotifier <- os.Interrupt
	if notifierErr := <-notifierDone; notifierErr != nil {
		t.Fatalf("stop real session notifier: %v", notifierErr)
	}
	h.releaseBusy()
	deadline = time.Now().Add(8 * time.Second)
	for {
		b, readErr := os.ReadFile(h.capture)
		if readErr == nil {
			if emulateLegacyNotifier && strings.Contains(string(b), realAMQCoopWakeDoorbell) && strings.Contains(string(b), dispatchNudgePrompt(h.root)) {
				break
			}
			if !emulateLegacyNotifier && len(nonemptyLines(string(b))) >= 1 {
				break
			}
		}
		if time.Now().After(deadline) {
			cmd := exec.Command(tmux, "capture-pane", "-p", "-t", rec.Tmux.PaneID, "-S", "-120")
			cmd.Env = h.env()
			pane, captureErr := cmd.CombinedOutput()
			t.Fatalf("timed out waiting for busy wake prompt cohort; read_err=%v capture=%q pane_err=%v\npane:\n%s", readErr, string(b), captureErr, string(pane))
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Give already-queued terminal input a bounded interval to reach the
	// recorder before asserting the complete prompt cohort.
	time.Sleep(300 * time.Millisecond)
	b, err := os.ReadFile(h.capture)
	if err != nil {
		t.Fatal(err)
	}
	lines := nonemptyLines(string(b))
	for _, line := range lines {
		assertMarkerFreeWake(t, line)
	}
	wantFallback := dispatchNudgePrompt(h.root)
	rawCount := strings.Count(string(b), realAMQCoopWakeDoorbell)
	fallbackCount := strings.Count(string(b), wantFallback)
	if emulateLegacyNotifier {
		// A busy real PTY may deliver the two complete inputs as separate scanner
		// lines or coalesce them before the recorder's first read. Count exact
		// full prompt occurrences in the byte stream, then reject any residual
		// non-whitespace content so both forms prove the same two-owner defect.
		residual := strings.Replace(string(b), realAMQCoopWakeDoorbell, "", 1)
		residual = strings.Replace(residual, wantFallback, "", 1)
		if rawCount != 1 || fallbackCount != 1 || strings.TrimSpace(residual) != "" {
			t.Fatalf("pre-fix control capture=%#v, want one AMQ doorbell plus one notifier fallback", lines)
		}
	} else if len(lines) != 1 || rawCount != 1 || fallbackCount != 0 {
		t.Fatalf("fixed busy capture=%#v, want exactly one AMQ doorbell and no notifier fallback", lines)
	}
	pending, err = snapshotSessionInboxMessages(h.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("busy target inbox changed before drain: got %d messages, want the original one", len(pending))
	}
	return lines
}

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
