package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

type recordingTerminator struct {
	mu     sync.Mutex
	name   string // signal label this fake reports via SignalName; defaults to SIGTERM
	calls  []int
	failOn map[int]error
}

func (r *recordingTerminator) Terminate(pid int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, pid)
	if err, ok := r.failOn[pid]; ok {
		return err
	}
	return nil
}

func (r *recordingTerminator) SignalName() string {
	if r.name == "" {
		return "SIGTERM"
	}
	return r.name
}

func TestRetireWakeWithAMQUsesExactPersistedInjectorIdentity(t *testing.T) {
	previous := runExactWakeRetire
	t.Cleanup(func() { runExactWakeRetire = previous })
	root := filepath.Join(t.TempDir(), ".agent-mail", "s")
	var got amqCommandRequest
	runExactWakeRetire = func(req amqCommandRequest) ([]byte, error) {
		got = req
		return []byte(fmt.Sprintf(`{"status":"retired","agent":"qa","root":%q,"pid":4242,"reason":"exact target retired"}`, root)), nil
	}
	rec := launch.Record{CWD: t.TempDir(), AMQVersion: doctorMinAMQVersion, WakePID: 4242, WakeInjectVia: "/usr/bin/tmux", WakeInjectArgs: []string{"load-buffer", "-"}}
	result, err := retireWakeWithAMQ(rec, root, "qa")
	if err != nil || result.PID != 4242 {
		t.Fatalf("retire result=%+v err=%v", result, err)
	}
	want := []string{"wake", "retire", "--root", root, "--me", "qa", "--inject-via", "/usr/bin/tmux", "--inject-arg", "load-buffer", "--inject-arg", "-", "--json"}
	if !reflect.DeepEqual(got.Arg, want) {
		t.Fatalf("wake retire args=%q want=%q", got.Arg, want)
	}
}

func stubOwnerlessWakeRecovery(t *testing.T, root, handle string, pid int) {
	t.Helper()
	previous := runExactWakeRecoverOwner
	t.Cleanup(func() { runExactWakeRecoverOwner = previous })
	runExactWakeRecoverOwner = func(req amqCommandRequest) ([]byte, error) {
		want := []string{"wake", "recover-owner", "--root", root, "--me", handle, "--json"}
		if !reflect.DeepEqual(req.Arg, want) {
			t.Fatalf("wake recover-owner args=%q want=%q", req.Arg, want)
		}
		return []byte(fmt.Sprintf(
			`{"status":"refused","agent":%q,"root":%q,"pid":%d,"reason":%q,"next_action":%q}`,
			handle,
			root,
			pid,
			ownerlessWakeRecoveryReason,
			ownerlessWakeRecoveryAction,
		)), errors.New("exit status 1")
	}
}

func TestReapRawWakeRetirementFallbackIsReported(t *testing.T) {
	agentDir := t.TempDir()
	root := filepath.Dir(agentDir)
	writeWakeLock(t, agentDir, wakeLockFile{PID: 4242, Root: root})
	term := &recordingTerminator{}
	result := reapStaleArtifacts(agentDir, "qa", root, false, launch.Record{AMQVersion: doctorMinAMQVersion}, term, downFakeProbe(map[int]bool{4242: true}, map[int]bool{4242: true}))
	if result.WakeRetirement != "raw_signal_fallback" || !strings.Contains(result.summary(), "raw or has no persisted inject-via identity") || len(term.calls) != 1 || term.calls[0] != 4242 {
		t.Fatalf("raw retirement result=%+v calls=%v", result, term.calls)
	}
}

func TestReapExactWakeRetirementRecognizesSelfCleanup(t *testing.T) {
	previous := runExactWakeRetire
	t.Cleanup(func() { runExactWakeRetire = previous })
	root := t.TempDir()
	agentDir := filepath.Join(root, "agents", "qa")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeWakeLock(t, agentDir, wakeLockFile{PID: 4242, Root: root})
	stubOwnerlessWakeRecovery(t, root, "qa", 4242)
	runExactWakeRetire = func(amqCommandRequest) ([]byte, error) {
		if err := os.Remove(wakeLockPath(agentDir)); err != nil {
			t.Fatal(err)
		}
		return []byte(fmt.Sprintf(`{"status":"refused","agent":"qa","root":%q,"pid":4242,"reason":"wake self-cleaned"}`, root)), errors.New("exit status 1")
	}
	result := reapStaleArtifacts(agentDir, "qa", root, false, launch.Record{AMQVersion: doctorMinAMQVersion, WakePID: 4242, WakeInjectVia: "/usr/bin/tmux"}, &recordingTerminator{}, downFakeProbe(map[int]bool{4242: false}, nil))
	if result.WakeRetirement != nativeWakeRetireSelfCleaned || result.failed() || !result.LockRemoved || result.WakeKilled != 4242 {
		t.Fatalf("self-cleaned retirement result=%+v", result)
	}
}

func TestReapExactWakeRetirementRefusalNeverFallsBackToSignal(t *testing.T) {
	previous := runExactWakeRetire
	t.Cleanup(func() { runExactWakeRetire = previous })
	agentDir := t.TempDir()
	root := filepath.Dir(agentDir)
	writeWakeLock(t, agentDir, wakeLockFile{PID: 4242, Root: root})
	stubOwnerlessWakeRecovery(t, root, "qa", 4242)
	runExactWakeRetire = func(amqCommandRequest) ([]byte, error) {
		return []byte(fmt.Sprintf(`{"status":"refused","agent":"qa","root":%q,"pid":4242,"reason":"target mismatch"}`, root)), errors.New("exit status 1")
	}
	term := &recordingTerminator{}
	result := reapStaleArtifacts(agentDir, "qa", root, false, launch.Record{AMQVersion: doctorMinAMQVersion, WakePID: 4242, WakeInjectVia: "/usr/bin/tmux"}, term, downFakeProbe(map[int]bool{4242: true}, map[int]bool{4242: true}))
	if result.WakeRetirement != "amq_exact_refused" || !result.failed() || len(term.calls) != 0 {
		t.Fatalf("exact refusal result=%+v fallback calls=%v", result, term.calls)
	}
}

func TestReapExactWakeRetirementSuccessNeverFallsBackToSignal(t *testing.T) {
	previous := runExactWakeRetire
	t.Cleanup(func() { runExactWakeRetire = previous })
	agentDir := t.TempDir()
	root := filepath.Dir(agentDir)
	writeWakeLock(t, agentDir, wakeLockFile{PID: 4242, Root: root})
	stubOwnerlessWakeRecovery(t, root, "qa", 4242)
	runExactWakeRetire = func(amqCommandRequest) ([]byte, error) {
		// Deliberately leave the lock in place to prove native success can never
		// fall through to the legacy signal path.
		return []byte(fmt.Sprintf(`{"status":"retired","agent":"qa","root":%q,"pid":4242,"reason":"exact target retired"}`, root)), nil
	}
	term := &recordingTerminator{}
	result := reapStaleArtifacts(agentDir, "qa", root, false, launch.Record{AMQVersion: doctorMinAMQVersion, WakePID: 4242, WakeInjectVia: "/usr/bin/tmux"}, term, downFakeProbe(map[int]bool{4242: true}, map[int]bool{4242: true}))
	if result.WakeRetirement != "amq_exact_lock_remaining" || !result.failed() || len(term.calls) != 0 {
		t.Fatalf("exact success result=%+v fallback calls=%v", result, term.calls)
	}
}

func TestReapExactWakeRetirementRejectsMismatchedPIDWithoutFallback(t *testing.T) {
	previous := runExactWakeRetire
	t.Cleanup(func() { runExactWakeRetire = previous })
	agentDir := t.TempDir()
	root := filepath.Dir(agentDir)
	writeWakeLock(t, agentDir, wakeLockFile{PID: 4242, Root: root})
	stubOwnerlessWakeRecovery(t, root, "qa", 4242)
	runExactWakeRetire = func(amqCommandRequest) ([]byte, error) {
		return []byte(fmt.Sprintf(`{"status":"retired","agent":"qa","root":%q,"pid":5252,"reason":"exact target retired"}`, root)), nil
	}
	term := &recordingTerminator{}
	result := reapStaleArtifacts(agentDir, "qa", root, false, launch.Record{AMQVersion: doctorMinAMQVersion, WakePID: 4242, WakeInjectVia: "/usr/bin/tmux"}, term, downFakeProbe(map[int]bool{4242: true, 5252: true}, map[int]bool{4242: true, 5252: true}))
	if result.WakeRetirement != "amq_exact_refused" || !result.failed() || !strings.Contains(result.RetirementDetail, "mismatched pid=5252") || len(term.calls) != 0 {
		t.Fatalf("mismatched pid result=%+v fallback calls=%v", result, term.calls)
	}
}

func TestReapOwnerBoundWakeUsesRecoverOwnerWithoutOwnerlessRetire(t *testing.T) {
	previousRecover := runExactWakeRecoverOwner
	previousRetire := runExactWakeRetire
	t.Cleanup(func() {
		runExactWakeRecoverOwner = previousRecover
		runExactWakeRetire = previousRetire
	})
	root := t.TempDir()
	agentDir := filepath.Join(root, "agents", "qa")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeWakeLock(t, agentDir, wakeLockFile{PID: 4242, Root: root})
	recoverCalls := 0
	runExactWakeRecoverOwner = func(req amqCommandRequest) ([]byte, error) {
		recoverCalls++
		if recoverCalls == 1 {
			return []byte(fmt.Sprintf(
				`{"status":"refused","agent":"qa","root":%q,"pid":4242,"owner_pid":5151,"owner_session":99,"reason":"handle qa is owned by live process","next_action":"exit owner"}`,
				root,
			)), errors.New("exit status 1")
		}
		if err := os.Remove(wakeLockPath(agentDir)); err != nil {
			t.Fatal(err)
		}
		return []byte(fmt.Sprintf(
			`{"status":"recovered","agent":"qa","root":%q,"pid":4242,"owner_pid":5151,"owner_session":99,"reason":"exact owner claim released"}`,
			root,
		)), nil
	}
	runExactWakeRetire = func(amqCommandRequest) ([]byte, error) {
		t.Fatal("owner-bound recovery must not call ownerless wake retire")
		return nil, nil
	}
	previousTimeout := wakeOwnerRecoveryTimeout
	previousPoll := wakeOwnerRecoveryPoll
	wakeOwnerRecoveryTimeout = time.Second
	wakeOwnerRecoveryPoll = time.Millisecond
	t.Cleanup(func() {
		wakeOwnerRecoveryTimeout = previousTimeout
		wakeOwnerRecoveryPoll = previousPoll
	})
	result := reapStaleArtifacts(
		agentDir,
		"qa",
		root,
		false,
		launch.Record{CWD: t.TempDir(), AgentPID: 5151, WakePID: 4242, WakeInjectVia: "/usr/bin/tmux"},
		&recordingTerminator{},
		downFakeProbe(map[int]bool{4242: false}, nil),
	)
	if result.failed() || result.WakeRetirement != "amq_owner_recovered" || !result.LockRemoved || recoverCalls != 2 {
		t.Fatalf("owner-bound recovery result=%+v calls=%d", result, recoverCalls)
	}
}

func TestReapOwnerRecoveryRejectsMismatchedOwnerWithoutFallback(t *testing.T) {
	previousRecover := runExactWakeRecoverOwner
	previousRetire := runExactWakeRetire
	t.Cleanup(func() {
		runExactWakeRecoverOwner = previousRecover
		runExactWakeRetire = previousRetire
	})
	agentDir := t.TempDir()
	root := filepath.Dir(agentDir)
	writeWakeLock(t, agentDir, wakeLockFile{PID: 4242, Root: root})
	runExactWakeRecoverOwner = func(amqCommandRequest) ([]byte, error) {
		return []byte(fmt.Sprintf(
			`{"status":"refused","agent":"qa","root":%q,"pid":4242,"owner_pid":6161,"reason":"live owner"}`,
			root,
		)), errors.New("exit status 1")
	}
	runExactWakeRetire = func(amqCommandRequest) ([]byte, error) {
		t.Fatal("mismatched owner must not fall back to ownerless retire")
		return nil, nil
	}
	term := &recordingTerminator{}
	result := reapStaleArtifacts(
		agentDir,
		"qa",
		root,
		false,
		launch.Record{AgentPID: 5151, WakePID: 4242, WakeInjectVia: "/usr/bin/tmux"},
		term,
		downFakeProbe(map[int]bool{4242: true}, map[int]bool{4242: true}),
	)
	if !result.failed() || result.WakeRetirement != "amq_owner_recovery_refused" ||
		!strings.Contains(result.RetirementDetail, "owner pid=6161, want persisted agent pid=5151") ||
		len(term.calls) != 0 {
		t.Fatalf("mismatched owner result=%+v fallback calls=%v", result, term.calls)
	}
}

func TestReapCorruptOwnerBoundWakeWithNoPersistedWakePIDStillFails(t *testing.T) {
	previousRecover := runExactWakeRecoverOwner
	previousRetire := runExactWakeRetire
	t.Cleanup(func() {
		runExactWakeRecoverOwner = previousRecover
		runExactWakeRetire = previousRetire
	})
	agentDir := t.TempDir()
	root := filepath.Dir(agentDir)
	if err := os.WriteFile(wakeLockPath(agentDir), []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	runExactWakeRecoverOwner = func(amqCommandRequest) ([]byte, error) {
		return []byte(fmt.Sprintf(
			`{"status":"refused","agent":"qa","root":%q,"reason":"wake lock is corrupt","next_action":"use wake repair"}`,
			root,
		)), errors.New("exit status 1")
	}
	runExactWakeRetire = func(amqCommandRequest) ([]byte, error) {
		t.Fatal("corrupt owner-bound recovery must not fall back to ownerless retire")
		return nil, nil
	}

	result := reapStaleArtifacts(
		agentDir,
		"qa",
		root,
		false,
		launch.Record{AgentPID: 5151, WakePID: 0, WakeInjectVia: "/usr/bin/tmux"},
		&recordingTerminator{},
		downFakeProbe(nil, nil),
	)
	if result.WakeSignalFailed != 0 || result.WakeRetirement != "amq_owner_recovery_refused" ||
		!result.failed() || result.any() || !strings.Contains(result.RetirementDetail, "status refused") {
		t.Fatalf("pid-zero corrupt owner recovery result=%+v", result)
	}
}

func TestReapResultOwnerRecoveryStatusesFailWithoutPID(t *testing.T) {
	for _, status := range []string{"amq_owner_recovery_refused", "amq_owner_recovery_lock_remaining"} {
		t.Run(status, func(t *testing.T) {
			result := reapResult{WakeRetirement: status}
			if !result.failed() {
				t.Fatalf("status %q with pid zero must fail", status)
			}
		})
	}
}

// TestReapPresenceReassertsOfflineAfterWakeDies covers the amq >=0.52.2 lost
// update: the dying wake's own teardown does a locked read-modify-write of
// presence.json that preserves whatever Status it read (active, from before
// our flip) and refreshes LastSeen. If that write lands after our initial
// flip but before the wake process actually exits, our flip is clobbered by
// a fresh "active" file that would recreate the zombie-heartbeat class
// (#38/#44). reapStaleArtifacts must wait for the wake PID to die and
// re-assert offline once it does.
func TestReapPresenceReassertsOfflineAfterWakeDies(t *testing.T) {
	agentDir := t.TempDir()
	root := filepath.Dir(agentDir)
	writePresence(t, agentDir, presenceFile{
		Schema: 1, Handle: "qa", Status: "active",
		LastSeen: time.Now().Add(-5 * time.Second),
	})

	callCount := 0
	rewritten := false
	probe := duplicateLaunchProbe{
		PIDAlive: func(pid int) bool {
			if pid != 4242 {
				return false
			}
			callCount++
			if callCount <= 2 {
				return true
			}
			if !rewritten {
				// The dying wake's own teardown persist: it read Status
				// "active" before our flip below landed, and now writes
				// that stale value back with a freshly-bumped LastSeen.
				writePresence(t, agentDir, presenceFile{
					Schema: 1, Handle: "qa", Status: "active",
					LastSeen: time.Now(),
				})
				rewritten = true
			}
			return false
		},
		Now: time.Now,
	}

	term := &recordingTerminator{}
	rec := launch.Record{AMQVersion: doctorMinAMQVersion, WakePID: 4242}
	result := reapStaleArtifacts(agentDir, "qa", root, false, rec, term, probe)

	if !result.PresenceFlip {
		t.Fatalf("expected PresenceFlip=true, got %+v", result)
	}
	if !rewritten {
		t.Fatalf("test setup did not exercise the lost-update rewrite; callCount=%d", callCount)
	}
	if len(term.calls) != 0 {
		t.Fatalf("no wake lock present; terminator should not have been called: %v", term.calls)
	}
	pres, err := readPresenceForEntry(agentDir)
	if err != nil {
		t.Fatalf("read presence: %v", err)
	}
	if !strings.EqualFold(pres.Status, "offline") {
		t.Fatalf("presence should be re-asserted offline after the wake's stale rewrite; got %q", pres.Status)
	}
}

// TestReapPresenceFlipSkipsReassertWhenWakeAlreadyDead covers the no-race
// case: the wake PID is already dead by the time we check, so the bounded
// wait resolves immediately and the single up-front flip stands unchanged.
func TestReapPresenceFlipSkipsReassertWhenWakeAlreadyDead(t *testing.T) {
	agentDir := t.TempDir()
	root := filepath.Dir(agentDir)
	writePresence(t, agentDir, presenceFile{
		Schema: 1, Handle: "qa", Status: "active",
		LastSeen: time.Now().Add(-5 * time.Second),
	})

	pidAliveCalls := 0
	probe := duplicateLaunchProbe{
		PIDAlive: func(int) bool {
			pidAliveCalls++
			return false
		},
		Now: time.Now,
	}

	term := &recordingTerminator{}
	rec := launch.Record{AMQVersion: doctorMinAMQVersion, WakePID: 4242}
	result := reapStaleArtifacts(agentDir, "qa", root, false, rec, term, probe)

	if !result.PresenceFlip {
		t.Fatalf("expected PresenceFlip=true, got %+v", result)
	}
	if pidAliveCalls == 0 {
		t.Fatalf("expected the bounded wait to check wake liveness at least once")
	}
	pres, err := readPresenceForEntry(agentDir)
	if err != nil {
		t.Fatalf("read presence: %v", err)
	}
	if !strings.EqualFold(pres.Status, "offline") {
		t.Fatalf("presence should be offline; got %q", pres.Status)
	}
}

// downFakeProbe implements duplicateLaunchProbe with explicit per-PID liveness and
// binary-match decisions so tests never shell out to ps or send real signals.
func downFakeProbe(alive map[int]bool, match map[int]bool) duplicateLaunchProbe {
	return duplicateLaunchProbe{
		PIDAlive: func(pid int) bool { return alive[pid] },
		ProcessMatch: func(pid int, _ func(args string) bool) bool {
			return match[pid]
		},
		Now: time.Now,
	}
}

// downRealMatcherProbe returns a probe whose ProcessMatch actually invokes
// the predicate against synthetic ps args, so reapStaleArtifacts is
// exercised through wakeProcessMatcher rather than through a canned
// pass/fail map. The pidArgs map maps each live PID to the string `ps`
// would return for it.
func downRealMatcherProbe(alive map[int]bool, pidArgs map[int]string) duplicateLaunchProbe {
	return duplicateLaunchProbe{
		PIDAlive: func(pid int) bool { return alive[pid] },
		ProcessMatch: func(pid int, predicate func(args string) bool) bool {
			args, ok := pidArgs[pid]
			if !ok {
				return false
			}
			return predicate(args)
		},
		Now: time.Now,
	}
}

// seedAgentRecord writes a launch.json under the AMQ fake-root layout for a
// given workstream and handle. Returns the agent dir so tests can assert on
// resolution side effects.
func seedAgentRecord(t *testing.T, base, workstream, handle string, rec launch.Record) string {
	t.Helper()
	agentDir := filepath.Join(base, workstream, "agents", handle)
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	return agentDir
}

func runDownExec(t *testing.T, d downExecution) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	d.Out = &buf
	err := executeDown(d)
	return buf.String(), err
}

func TestExecuteDownDryRunUsesCanonicalRecordWithoutMutation(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	project := canonicalFilesystemPath(t.TempDir())
	const (
		profile = "review"
		session = "dry-run"
		handle  = "worker"
		pid     = 5150
	)
	worktree := filepath.Join(project, ".amq-squad", "worktrees", handle)
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := team.WriteProfile(project, profile, team.Team{
		Project:    project,
		Workstream: session,
		Members: []team.Member{{
			Role: handle, Handle: handle, Binary: "codex", Session: session, CWD: worktree,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	canonicalRoot := filepath.Join(project, ".agent-mail", profile, session)
	agentDir := filepath.Join(canonicalRoot, "agents", handle)
	if err := launch.Write(agentDir, launch.Record{
		TeamHome: project, TeamProfile: profile, Session: session,
		Role: handle, Handle: handle, Binary: "codex", CWD: worktree,
		Root: canonicalRoot, BaseRoot: filepath.Dir(canonicalRoot),
		AgentPID: pid, StartedAt: time.Now().Add(-time.Minute).UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	term := &recordingTerminator{}
	out, err := runDownExec(t, downExecution{
		Verb: "down", ProjectDir: project, Profile: profile,
		RequestedSession: session, ExplicitProject: true, ExplicitProfile: true, ExplicitSession: true,
		Role: handle, Terminator: term,
		Probe:  downFakeProbe(map[int]bool{pid: true}, map[int]bool{pid: true}),
		DryRun: true, JSON: true,
	})
	if err != nil {
		t.Fatalf("executeDown dry-run: %v\n%s", err, out)
	}
	if len(term.calls) != 0 {
		t.Fatalf("dry-run signaled pids %v", term.calls)
	}
	env := decodeJSONEnvelope[downEnvelopeData](t, out)
	if env.Data.Root != canonicalRoot {
		t.Errorf("dry-run root = %q, want canonical root %q", env.Data.Root, canonicalRoot)
	}
	if len(env.Data.Reports) != 1 || env.Data.Reports[0].Agent.Outcome != downStatusPlanned {
		t.Fatalf("dry-run reports = %+v, want one would-stop result", env.Data.Reports)
	}
	rec, err := launch.Read(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	if rec.StoppedAt != nil {
		t.Fatalf("dry-run marked launch record stopped at %v", rec.StoppedAt)
	}
}

func TestExecuteDownStoppedRecordCannotBootstrapImplicitProject(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	project := t.TempDir()
	chdir(t, project)
	const (
		profile = "review"
		session = "stop-context"
	)
	if err := team.WriteProfile(project, profile, team.Team{
		Project: project,
		Members: []team.Member{{Role: "worker", Binary: "codex", Handle: "worker", Session: session}},
	}); err != nil {
		t.Fatal(err)
	}
	baseRoot := filepath.Join(project, ".agent-mail", profile)
	root := filepath.Join(baseRoot, session)
	startedAt := time.Now().Add(-time.Hour).UTC()
	agentDir := filepath.Join(root, "agents", "worker")
	if err := launch.Write(agentDir, launch.Record{
		TeamHome: project, TeamProfile: profile, Session: session, Role: "worker", Handle: "worker",
		Root: root, BaseRoot: baseRoot, CWD: project, Binary: "codex", AgentPID: 5151, StartedAt: startedAt,
	}); err != nil {
		t.Fatal(err)
	}
	out, err := runDownExec(t, downExecution{
		ProjectDir: project, Profile: profile, RequestedSession: session,
		ExplicitProject: true, ExplicitProfile: true, ExplicitSession: true,
		Role: "worker", Terminator: &recordingTerminator{},
		Probe: downFakeProbe(map[int]bool{5151: true}, map[int]bool{5151: true}),
	})
	if err != nil {
		t.Fatal(err)
	}
	stopped, err := launch.Read(agentDir)
	if err != nil || stopped.StoppedAt == nil || stopped.StoppedAt.IsZero() {
		t.Fatalf("stopped launch record = %+v, err=%v\n%s", stopped, err, out)
	}

	isolateCanonicalContextTest(t, project)
	outside := t.TempDir()
	resumeChdir(t, outside)
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_BASE_ROOT", baseRoot)
	t.Setenv("AM_SESSION", session)
	t.Setenv("AM_ME", "worker")
	contextScanLaunchEntries = func(string) ([]launch.Entry, error) {
		return []launch.Entry{{Record: stopped, AgentDir: agentDir, Source: launch.FileName}}, nil
	}
	// Even if the stopped PID still looks alive while SIGTERM is taking effect,
	// the durable stop marker must immediately remove the record from context.
	contextPIDAlive = func(int) bool { return true }
	contextProcessMatch = func(int, func(string) bool) bool { return true }
	contextProcessStartTime = func(int) (time.Time, bool) { return startedAt.Add(-time.Second), true }
	ctx, err := resolveCanonicalContext(contextResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !sameResolvedDir(ctx.ProjectDir, outside) || sameResolvedDir(ctx.ProjectDir, project) {
		t.Fatalf("stopped record bootstrapped TeamHome as project: %#v", ctx)
	}
}

func TestRunStopRejectsRoleAndAll(t *testing.T) {
	seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"}},
	})
	_, _, err := captureOutput(t, func() error {
		return runDown([]string{"--role", "cto", "--all"})
	})
	if err == nil {
		t.Fatal("--role and --all together should be a usage error")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
}

func TestRunStopProjectTargetsOtherDir(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	project := t.TempDir()
	other := t.TempDir()
	if err := team.Write(project, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-99"}},
	}); err != nil {
		t.Fatal(err)
	}
	chdir(t, other)

	stdout, stderr, err := captureOutput(t, func() error {
		return runDown([]string{"--project", project, "--all", "--session", "issue-99"})
	})
	if err != nil {
		t.Fatalf("stop --project: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{"# amq-squad down", "# workstream: issue-99", "no launch record"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stop --project output missing %q in:\n%s", want, stdout)
		}
	}
}

func TestExecuteDownRejectsUnknownRole(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"}},
	})
	_, err := runDownExec(t, downExecution{
		ProjectDir: dir,
		Role:       "ghost",
		Terminator: &recordingTerminator{},
		Probe:      downFakeProbe(nil, nil),
	})
	if err == nil || !strings.Contains(err.Error(), `unknown role "ghost"`) {
		t.Fatalf("want unknown role error, got %v", err)
	}
}

// TestExecuteDownSendsTermToVerifiedPID inverts the old force-required test:
// down with NO --force now SIGTERMs every live, binary-matched agent, and the
// summary surfaces the resumable hint because on-disk state is preserved.
func TestExecuteDownSendsTermToVerifiedPID(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "issue-96"},
		},
	})
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:   "codex",
		Handle:   "cto",
		AgentPID: 1111,
		Root:     filepath.Join(base, "issue-96"),
	})
	seedAgentRecord(t, base, "issue-96", "fullstack", launch.Record{
		Binary:   "claude",
		Handle:   "fullstack",
		AgentPID: 2222,
		Root:     filepath.Join(base, "issue-96"),
	})

	term := &recordingTerminator{name: "SIGTERM"}
	out, err := runDownExec(t, downExecution{
		Verb:             "down",
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		All:              true,
		Terminator:       term,
		Probe:            downFakeProbe(map[int]bool{1111: true, 2222: true}, map[int]bool{1111: true, 2222: true}),
	})
	if err != nil {
		t.Fatalf("down: %v\noutput:\n%s", err, out)
	}
	term.mu.Lock()
	got := append([]int(nil), term.calls...)
	term.mu.Unlock()
	if len(got) != 2 || got[0] != 1111 || got[1] != 2222 {
		t.Fatalf("terminator calls = %v, want [1111 2222]", got)
	}
	for _, want := range []string{
		"# amq-squad down", "# workstream: issue-96", "cto", "fullstack",
		"stopped", "SIGTERM sent to pid 1111", "SIGTERM sent to pid 2222",
		"bring it back with 'amq-squad resume'",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q in:\n%s", want, out)
		}
	}
	// State on disk must be PRESERVED (recoverable via resume): the launch
	// record stays readable after down.
	for _, handle := range []string{"cto", "fullstack"} {
		agentDir := filepath.Join(base, "issue-96", "agents", handle)
		if _, readErr := launch.Read(agentDir); readErr != nil {
			t.Errorf("launch record for %q must be preserved after down: %v", handle, readErr)
		}
	}
}

func TestExecuteDownNotLiveForDeadPID(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary: "codex", Handle: "cto", AgentPID: 1234,
	})
	term := &recordingTerminator{}
	out, err := runDownExec(t, downExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		Role:             "cto",
		Terminator:       term,
		Probe:            downFakeProbe(map[int]bool{1234: false}, map[int]bool{1234: true}),
	})
	if err != nil {
		t.Fatalf("down: %v", err)
	}
	if len(term.calls) != 0 {
		t.Fatalf("terminator must not be called for dead pid; got %v", term.calls)
	}
	if !strings.Contains(out, "not-live") || !strings.Contains(out, "pid 1234 is not alive") {
		t.Errorf("output missing not-live detail:\n%s", out)
	}
}

func TestExecuteDownMaybeLiveForNoPIDWithFreshPresence(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	previousSpawn := sessionNotifierSpawn
	t.Cleanup(func() { sessionNotifierSpawn = previousSpawn })
	spawned := 0
	sessionNotifierSpawn = func(_, _, _, _, _ string) (notificationWatcherProcess, error) {
		spawned++
		return nil, errors.New("unexpected notifier spawn")
	}
	// Codex seats can finish launch without ever recording a pid; a fresh
	// heartbeat means the agent may well still be running.
	agentDir := seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary: "codex", Handle: "cto", AgentPID: 0,
	})
	writePresence(t, agentDir, presenceFile{Schema: 1, Handle: "cto", Status: "active", LastSeen: time.Now().Add(-5 * time.Second)})
	term := &recordingTerminator{}
	out, err := runDownExec(t, downExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		Role:             "cto",
		Terminator:       term,
		Probe:            downFakeProbe(nil, nil),
	})
	if len(term.calls) != 0 {
		t.Fatalf("terminator must not be called when no pid was captured; got %v", term.calls)
	}
	if spawned != 0 {
		t.Fatalf("never-expected session notifier spawned %d time(s) during incomplete down", spawned)
	}
	if _, ok := err.(*PartialError); !ok {
		t.Fatalf("maybe-live member must not read as clean success; want *PartialError, got %T: %v", err, err)
	}
	for _, want := range []string{"maybe-live", "no pid captured", "WARN:", "# AM_ROOT:"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestRenderDownReportsMaybeLiveWithFailureStaysPartial(t *testing.T) {
	var buf bytes.Buffer
	reports := []downReport{
		{Role: "cto", Root: "/tmp/root", Status: downStatusFailed, Detail: "terminate pid 5: boom"},
		{Role: "qa", Root: "/tmp/root", Status: downStatusMaybeLive, Detail: "no pid captured at launch — may still be live"},
	}
	// failed + maybe-live with zero sent must still be partial (exit 3), not a
	// plain error that hides the unconfirmed-stop members.
	err := renderDownReports(&buf, "stop", "issue-96", reports)
	pe, ok := err.(*PartialError)
	if !ok {
		t.Fatalf("want *PartialError, got %T: %v", err, err)
	}
	if !strings.Contains(pe.Message, "may still be live") {
		t.Errorf("partial message should mention maybe-live members: %q", pe.Message)
	}
}

func TestRestoreDownSessionServicesAttemptsBothAfterPartialStop(t *testing.T) {
	watcherErr := errors.New("watcher restart failed")
	notifierErr := errors.New("notifier restart failed")
	var calls []string
	err := restoreDownSessionServices(true, true, func() error {
		calls = append(calls, "watcher")
		return watcherErr
	}, func() error {
		calls = append(calls, "notifier")
		return notifierErr
	})
	if !slices.Equal(calls, []string{"watcher", "notifier"}) {
		t.Fatalf("restore calls = %v, want both services", calls)
	}
	if !errors.Is(err, watcherErr) || !errors.Is(err, notifierErr) {
		t.Fatalf("restore error = %v, want both causes", err)
	}
}

func TestExecuteDownNotLiveForNoPIDStalePresence(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	agentDir := seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary: "codex", Handle: "cto", AgentPID: 0,
	})
	// Presence exists but is stale, so there is no reason to suspect liveness.
	writePresence(t, agentDir, presenceFile{Schema: 1, Handle: "cto", Status: "active", LastSeen: time.Now().Add(-1 * time.Hour)})
	term := &recordingTerminator{}
	out, err := runDownExec(t, downExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		Role:             "cto",
		Terminator:       term,
		Probe:            downFakeProbe(nil, nil),
	})
	if err != nil {
		t.Fatalf("down: %v", err)
	}
	if !strings.Contains(out, "not-live") || !strings.Contains(out, "no pid captured") {
		t.Errorf("expected not-live with no-pid detail:\n%s", out)
	}
}

func TestExecuteDownNotLiveForMissingRecord(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	term := &recordingTerminator{}
	out, err := runDownExec(t, downExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		Role:             "cto",
		Terminator:       term,
		Probe:            downFakeProbe(nil, nil),
	})
	if err != nil {
		t.Fatalf("down: %v", err)
	}
	if len(term.calls) != 0 {
		t.Fatalf("terminator must not be called when no launch record; got %v", term.calls)
	}
	if !strings.Contains(out, "no launch record") {
		t.Errorf("output missing 'no launch record':\n%s", out)
	}
}

func TestExecuteDownNotLiveForBinaryMismatch(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary: "codex", Handle: "cto", AgentPID: 9000,
	})
	term := &recordingTerminator{}
	out, err := runDownExec(t, downExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		Role:             "cto",
		Terminator:       term,
		Probe:            downFakeProbe(map[int]bool{9000: true}, map[int]bool{9000: false}),
	})
	if err != nil {
		t.Fatalf("down: %v", err)
	}
	if len(term.calls) != 0 {
		t.Fatalf("terminator must not be called on binary mismatch; got %v", term.calls)
	}
	if !strings.Contains(out, "PID reuse") {
		t.Errorf("output missing PID-reuse detail:\n%s", out)
	}
}

func TestExecuteDownPartialFailureReturnsAggregateError(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "issue-96"},
		},
	})
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{Binary: "codex", Handle: "cto", AgentPID: 100})
	seedAgentRecord(t, base, "issue-96", "fullstack", launch.Record{Binary: "claude", Handle: "fullstack", AgentPID: 200})

	term := &recordingTerminator{
		failOn: map[int]error{200: errors.New("operation not permitted")},
	}
	out, err := runDownExec(t, downExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		All:              true,
		Terminator:       term,
		Probe:            downFakeProbe(map[int]bool{100: true, 200: true}, map[int]bool{100: true, 200: true}),
	})
	if err == nil || !strings.Contains(err.Error(), "1 of 2") {
		t.Fatalf("partial failure should aggregate: got %v", err)
	}
	for _, want := range []string{"stopped", "failed", "SIGTERM sent to pid 100", "terminate pid 200: operation not permitted"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q in:\n%s", want, out)
		}
	}
}

func TestExecuteDownResolvesDefaultWorkstream(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		// Legacy-style per-role session so member-session inference yields
		// nothing and (with no pin) resolution falls through to the
		// defaultWorkstreamName(projectDir) basename.
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "cto"}},
	})
	workstream := defaultWorkstreamName(dir)
	seedAgentRecord(t, base, workstream, "cto", launch.Record{Binary: "codex", Handle: "cto", AgentPID: 4242})

	term := &recordingTerminator{}
	out, err := runDownExec(t, downExecution{
		ProjectDir: dir,
		All:        true,
		Terminator: term,
		Probe:      downFakeProbe(map[int]bool{4242: true}, map[int]bool{4242: true}),
	})
	if err != nil {
		t.Fatalf("down: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "# workstream: "+workstream) {
		t.Errorf("default workstream not used:\n%s", out)
	}
	if len(term.calls) != 1 || term.calls[0] != 4242 {
		t.Fatalf("terminator calls = %v, want [4242]", term.calls)
	}
}

// TestExecuteDownReapsOrphanWakeOnDeadAgent covers #44: agent PID dead,
// wake sidecar still alive + heartbeating presence. down --force must
// SIGTERM the wake, drop the lock, and flip presence offline so the next
// `up` does not collide.
func TestExecuteDownReapsOrphanWakeOnDeadAgent(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "qa", Binary: "claude", Handle: "qa", Session: "issue-96"}},
	})
	agentDir := seedAgentRecord(t, base, "issue-96", "qa", launch.Record{
		Binary: "claude", Handle: "qa", AgentPID: 1111,
	})
	writeWakeLock(t, agentDir, wakeLockFile{PID: 3476, Root: filepath.Join(base, "issue-96")})
	writePresence(t, agentDir, presenceFile{
		Schema: 1, Handle: "qa", Status: "active",
		LastSeen: time.Now().Add(-5 * time.Second),
	})

	term := &recordingTerminator{}
	out, err := runDownExec(t, downExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		Role:             "qa",
		Terminator:       term,
		// Agent pid dead, wake pid alive; both pass process-match.
		Probe: downFakeProbe(
			map[int]bool{1111: false, 3476: true},
			map[int]bool{1111: false, 3476: true},
		),
	})
	if err != nil {
		t.Fatalf("cleaned reap should be exit 0: got %v\n%s", err, out)
	}
	if len(term.calls) != 1 || term.calls[0] != 3476 {
		t.Fatalf("expected SIGTERM to orphan wake pid 3476; got %v", term.calls)
	}
	if _, statErr := os.Stat(filepath.Join(agentDir, ".wake.lock")); !os.IsNotExist(statErr) {
		t.Errorf("stale .wake.lock should be removed; stat err = %v", statErr)
	}
	pres, err := readPresenceForEntry(agentDir)
	if err != nil {
		t.Fatalf("read presence: %v", err)
	}
	if !strings.EqualFold(pres.Status, "offline") {
		t.Errorf("presence should be flipped to offline; got %q", pres.Status)
	}
	for _, want := range []string{"cleaned", "recorded pid 1111 is not alive", "SIGTERM sent to wake pid 3476", "removed stale .wake.lock", "flipped presence to offline"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestExecuteDownReapsZombiePresenceWhenWakeDead covers the case where the
// agent and wake are both dead but presence is still "active" within the
// freshness window (zombie heartbeat from a long-running orphan that died
// recently). down should flip it offline so preflight cannot block.
func TestExecuteDownReapsZombiePresenceWhenWakeDead(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cpo", Binary: "codex", Handle: "cpo", Session: "issue-96"}},
	})
	agentDir := seedAgentRecord(t, base, "issue-96", "cpo", launch.Record{
		Binary: "codex", Handle: "cpo", AgentPID: 6139,
	})
	writePresence(t, agentDir, presenceFile{
		Schema: 1, Handle: "cpo", Status: "active",
		LastSeen: time.Now().Add(-30 * time.Second),
	})

	term := &recordingTerminator{}
	out, err := runDownExec(t, downExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		Role:             "cpo",
		Terminator:       term,
		Probe:            downFakeProbe(map[int]bool{6139: false}, map[int]bool{6139: false}),
	})
	if err != nil {
		t.Fatalf("zombie-presence reap should be exit 0: got %v\n%s", err, out)
	}
	if len(term.calls) != 0 {
		t.Fatalf("no terminator calls expected when no live PID; got %v", term.calls)
	}
	pres, err := readPresenceForEntry(agentDir)
	if err != nil {
		t.Fatalf("read presence: %v", err)
	}
	if !strings.EqualFold(pres.Status, "offline") {
		t.Errorf("presence should be flipped to offline; got %q", pres.Status)
	}
	if !strings.Contains(out, "cleaned") || !strings.Contains(out, "flipped presence to offline") {
		t.Errorf("output missing cleaned+flip detail:\n%s", out)
	}
}

// TestExecuteDownReapDoesNotTouchForeignPresence guards against clobbering
// a presence file written by a different handle that happens to live under
// this agent dir (defense in depth: shouldn't happen, but if it does we
// must not silently flip another agent's heartbeat).
func TestExecuteDownReapDoesNotTouchForeignPresence(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "qa", Binary: "claude", Handle: "qa", Session: "issue-96"}},
	})
	agentDir := seedAgentRecord(t, base, "issue-96", "qa", launch.Record{
		Binary: "claude", Handle: "qa", AgentPID: 5555,
	})
	writePresence(t, agentDir, presenceFile{
		Schema: 1, Handle: "someone-else", Status: "active",
		LastSeen: time.Now().Add(-5 * time.Second),
	})

	term := &recordingTerminator{}
	_, err := runDownExec(t, downExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		Role:             "qa",
		Terminator:       term,
		Probe:            downFakeProbe(map[int]bool{5555: false}, map[int]bool{5555: false}),
	})
	if err != nil {
		t.Fatalf("down: %v", err)
	}
	pres, err := readPresenceForEntry(agentDir)
	if err != nil {
		t.Fatalf("read presence: %v", err)
	}
	if pres.Handle != "someone-else" || !strings.EqualFold(pres.Status, "active") {
		t.Errorf("foreign presence must not be modified; got handle=%q status=%q", pres.Handle, pres.Status)
	}
}

// TestExecuteDownReapsLockOnPIDReuse covers a stale .wake.lock whose PID
// has been recycled by an unrelated process. The lock must be removed but
// the reused-PID process must not be SIGTERMed.
func TestExecuteDownReapsLockOnPIDReuse(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	agentDir := seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary: "codex", Handle: "cto", AgentPID: 7000,
	})
	writeWakeLock(t, agentDir, wakeLockFile{PID: 8888, Root: filepath.Join(base, "issue-96")})

	term := &recordingTerminator{}
	_, err := runDownExec(t, downExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		Role:             "cto",
		Terminator:       term,
		// Agent dead; wake PID alive but process-match returns false (unrelated process).
		Probe: downFakeProbe(
			map[int]bool{7000: false, 8888: true},
			map[int]bool{7000: false, 8888: false},
		),
	})
	if err != nil {
		t.Fatalf("lock cleanup should be exit 0: got %v", err)
	}
	if len(term.calls) != 0 {
		t.Fatalf("reused PID must not receive SIGTERM; got %v", term.calls)
	}
	if _, statErr := os.Stat(filepath.Join(agentDir, ".wake.lock")); !os.IsNotExist(statErr) {
		t.Errorf("stale lock should still be removed even on PID reuse; stat err = %v", statErr)
	}
}

// TestExecuteDownAllScopedToConfiguredMembers proves --all does not sweep
// every launch record on disk; only configured team members are targeted.
func TestExecuteDownAllScopedToConfiguredMembers(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{Binary: "codex", Handle: "cto", AgentPID: 11})
	seedAgentRecord(t, base, "issue-96", "stranger", launch.Record{Binary: "claude", Handle: "stranger", AgentPID: 22})

	term := &recordingTerminator{}
	_, err := runDownExec(t, downExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		All:              true,
		Terminator:       term,
		Probe:            downFakeProbe(map[int]bool{11: true, 22: true}, map[int]bool{11: true, 22: true}),
	})
	if err != nil {
		t.Fatalf("down: %v", err)
	}
	if len(term.calls) != 1 || term.calls[0] != 11 {
		t.Fatalf("--all targeted unconfigured handles: calls = %v, want [11]", term.calls)
	}
}

// TestExecuteDownReapKeepsLockOnSignalFailure covers the case where a
// matching live wake exists but Terminate returns an error (e.g. EPERM on
// a wake owned by another uid). The lock must NOT be removed and presence
// must NOT flip — otherwise the next `up` would see clean state and
// duplicate-launch on top of a still-running wake.
func TestExecuteDownReapKeepsLockOnSignalFailure(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "qa", Binary: "claude", Handle: "qa", Session: "issue-96"}},
	})
	agentDir := seedAgentRecord(t, base, "issue-96", "qa", launch.Record{
		Binary: "claude", Handle: "qa", AgentPID: 1111,
	})
	writeWakeLock(t, agentDir, wakeLockFile{PID: 3476, Root: filepath.Join(base, "issue-96")})
	writePresence(t, agentDir, presenceFile{
		Schema: 1, Handle: "qa", Status: "active",
		LastSeen: time.Now().Add(-5 * time.Second),
	})

	term := &recordingTerminator{failOn: map[int]error{3476: errors.New("operation not permitted")}}
	out, err := runDownExec(t, downExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		Role:             "qa",
		Terminator:       term,
		// Agent dead, wake alive + matching.
		Probe: downFakeProbe(
			map[int]bool{1111: false, 3476: true},
			map[int]bool{1111: false, 3476: true},
		),
	})
	if err == nil {
		t.Fatalf("signal failure should not return nil:\n%s", out)
	}
	if _, statErr := os.Stat(filepath.Join(agentDir, ".wake.lock")); statErr != nil {
		t.Errorf(".wake.lock must be preserved when SIGTERM fails: %v", statErr)
	}
	pres, err := readPresenceForEntry(agentDir)
	if err != nil {
		t.Fatalf("read presence: %v", err)
	}
	if !strings.EqualFold(pres.Status, "active") {
		t.Errorf("presence must not flip to offline when wake signal failed; got %q", pres.Status)
	}
	for _, want := range []string{"failed", "failed to signal wake pid 3476", "operation not permitted"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestExecuteDownReapRejectsForeignRootWake covers PID reuse where the
// live PID belongs to a wake for a different workstream root. The lock
// must be removed (stale for this dir) and the foreign wake must NOT be
// signaled. Uses the real wakeProcessMatcher predicate via synthetic ps
// args so a future refactor that calls the wrong matcher is caught here.
func TestExecuteDownReapRejectsForeignRootWake(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	agentDir := seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary: "codex", Handle: "cto", AgentPID: 7000,
	})
	ourRoot := filepath.Join(base, "issue-96")
	// Lock claims our root; the live PID actually belongs to a wake for a
	// foreign root with the same handle. wakeProcessMatcher must reject on
	// root mismatch; reap must classify the lock as stale and not signal.
	writeWakeLock(t, agentDir, wakeLockFile{PID: 9000, Root: ourRoot})

	term := &recordingTerminator{}
	_, err := runDownExec(t, downExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		Role:             "cto",
		Terminator:       term,
		Probe: downRealMatcherProbe(
			map[int]bool{7000: false, 9000: true},
			map[int]string{
				// Same --me cto, but --root points to a foreign workstream.
				9000: "amq wake --me cto --root /tmp/foreign-root/other-session",
			},
		),
	})
	if err != nil {
		t.Fatalf("foreign-root reap should be exit 0: %v", err)
	}
	if len(term.calls) != 0 {
		t.Fatalf("foreign-root wake must not be signaled; got %v", term.calls)
	}
	if _, statErr := os.Stat(filepath.Join(agentDir, ".wake.lock")); !os.IsNotExist(statErr) {
		t.Errorf("foreign-root lock should be removed as stale; stat err = %v", statErr)
	}
}

// TestExecuteDownAgentKilledButWakeReapFailsIsFailed covers the contract
// surfaced in Codex pass 2: when the agent SIGTERM succeeds but the
// matching live wake cannot be signaled, the per-member status must be
// downStatusFailed so renderDownReports counts it as a failure. Without
// this, the summary would read "1 force-sent" and exit 0 even though the
// wake is still running and the lock is intentionally preserved.
func TestExecuteDownAgentKilledButWakeReapFailsIsFailed(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "qa", Binary: "claude", Handle: "qa", Session: "issue-96"}},
	})
	agentDir := seedAgentRecord(t, base, "issue-96", "qa", launch.Record{
		Binary: "claude", Handle: "qa", AgentPID: 1111,
	})
	ourRoot := filepath.Join(base, "issue-96")
	writeWakeLock(t, agentDir, wakeLockFile{PID: 3476, Root: ourRoot})
	writePresence(t, agentDir, presenceFile{
		Schema: 1, Handle: "qa", Status: "active",
		LastSeen: time.Now().Add(-5 * time.Second),
	})

	term := &recordingTerminator{
		// Agent SIGTERM succeeds; wake SIGTERM fails (EPERM-shaped).
		failOn: map[int]error{3476: errors.New("operation not permitted")},
	}
	out, err := runDownExec(t, downExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		Role:             "qa",
		Terminator:       term,
		Probe: downRealMatcherProbe(
			map[int]bool{1111: true, 3476: true},
			map[int]string{
				1111: "claude",
				3476: "amq wake --me qa --root " + ourRoot,
			},
		),
	})
	if err == nil {
		t.Fatalf("wake-reap failure on force-sent path must produce non-zero exit:\n%s", out)
	}
	if _, ok := err.(*PartialError); ok {
		// Per current contract this returns a plain error (1 of 1 failed,
		// no other sent). Either is acceptable as "not clean success", but
		// guard against a future regression to nil.
		t.Logf("note: returned PartialError (also acceptable): %v", err)
	}
	for _, want := range []string{"failed", "SIGTERM sent to pid 1111", "failed to signal wake pid 3476", "operation not permitted"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// The wake-lock and presence must remain so the next preflight still sees
	// the live writer.
	if _, statErr := os.Stat(filepath.Join(agentDir, ".wake.lock")); statErr != nil {
		t.Errorf(".wake.lock must be preserved after wake signal failure: %v", statErr)
	}
	pres, err := readPresenceForEntry(agentDir)
	if err != nil {
		t.Fatalf("read presence: %v", err)
	}
	if !strings.EqualFold(pres.Status, "active") {
		t.Errorf("presence must not flip when wake signal failed; got %q", pres.Status)
	}
}

// TestRenderDownReportsCleanedAndFailedStaysPartial covers the reporting
// contract for the new cleaned status mixed with a failed teardown: the
// combined exit must be partial so the operator sees both that work was
// done AND that something needs attention.
func TestRenderDownReportsCleanedAndFailedStaysPartial(t *testing.T) {
	var buf bytes.Buffer
	reports := []downReport{
		{Role: "cto", Root: "/tmp/root", Status: downStatusCleaned, Detail: "recorded pid 1 is not alive; flipped presence to offline"},
		{Role: "qa", Root: "/tmp/root", Status: downStatusFailed, Detail: "terminate pid 5: boom"},
	}
	err := renderDownReports(&buf, "stop", "issue-96", reports)
	pe, ok := err.(*PartialError)
	if !ok {
		t.Fatalf("cleaned+failed must be *PartialError, got %T: %v", err, err)
	}
	// summary line literal: 0 stopped, 1 cleaned, ...
	if !strings.Contains(buf.String(), "0 stopped, 1 cleaned") {
		t.Errorf("summary line missing cleaned counter:\n%s", buf.String())
	}
	if !strings.Contains(pe.Message, "1 of 2") {
		t.Errorf("partial message missing %q: %q", "1 of 2", pe.Message)
	}
}
