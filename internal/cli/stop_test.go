package cli

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/internal/launch"
	"github.com/omriariav/amq-squad/internal/team"
)

// TestNewSignalTerminatorMapsForceToSIGKILL pins the flag wiring: stop with no
// --force sends SIGTERM, --force escalates to an unignorable SIGKILL.
func TestNewSignalTerminatorMapsForceToSIGKILL(t *testing.T) {
	soft := newSignalTerminator(false)
	if soft.sig != syscall.SIGTERM {
		t.Errorf("default terminator sig = %v, want SIGTERM", soft.sig)
	}
	if soft.SignalName() != "SIGTERM" {
		t.Errorf("default SignalName = %q, want SIGTERM", soft.SignalName())
	}
	hard := newSignalTerminator(true)
	if hard.sig != syscall.SIGKILL {
		t.Errorf("--force terminator sig = %v, want SIGKILL", hard.sig)
	}
	if hard.SignalName() != "SIGKILL" {
		t.Errorf("--force SignalName = %q, want SIGKILL", hard.SignalName())
	}
}

// TestExecuteStopForceSendsSIGKILL proves that with --force the per-member
// report reflects an actual SIGKILL escalation and that the verified live PID
// is signaled.
func TestExecuteStopForceSendsSIGKILL(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary: "codex", Handle: "cto", AgentPID: 1212,
		Root: filepath.Join(base, "issue-96"),
	})

	// --force maps to a SIGKILL-labeled terminator; mirror that in the fake.
	term := &recordingTerminator{name: "SIGKILL"}
	out, err := runDownExec(t, downExecution{
		Verb:             "stop",
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		Role:             "cto",
		Terminator:       term,
		Probe:            downFakeProbe(map[int]bool{1212: true}, map[int]bool{1212: true}),
	})
	if err != nil {
		t.Fatalf("stop --force: %v\n%s", err, out)
	}
	if len(term.calls) != 1 || term.calls[0] != 1212 {
		t.Fatalf("terminator calls = %v, want [1212]", term.calls)
	}
	for _, want := range []string{"stopped", "SIGKILL sent to pid 1212"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q in:\n%s", want, out)
		}
	}
}

// TestExecuteStopDoesNotSignalReusedPID proves the guards still hold under the
// no-force default: a recorded PID that is alive but does NOT match the
// expected binary (PID reuse) is never signaled.
func TestExecuteStopDoesNotSignalReusedPID(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary: "codex", Handle: "cto", AgentPID: 4040,
	})

	term := &recordingTerminator{}
	out, err := runDownExec(t, downExecution{
		Verb:             "stop",
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		Role:             "cto",
		Terminator:       term,
		// PID alive but binary does not match -> reuse, must not be signaled.
		Probe: downFakeProbe(map[int]bool{4040: true}, map[int]bool{4040: false}),
	})
	if err != nil {
		t.Fatalf("stop: %v\n%s", err, out)
	}
	if len(term.calls) != 0 {
		t.Fatalf("reused/foreign PID must not be signaled; got %v", term.calls)
	}
	if !strings.Contains(out, "PID reuse") {
		t.Errorf("output missing PID-reuse detail:\n%s", out)
	}
}

// TestRunDownAliasBehavesLikeStopAndPrintsHint drives the deprecated alias
// end-to-end: it prints the one-line stderr hint, renders the same report as
// stop (resumable hint included), and preserves on-disk state. The recorded
// PID is dead so the real signalTerminator is never invoked — the test never
// signals a real process.
func TestRunDownAliasBehavesLikeStopAndPrintsHint(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	// Dead PID + stale presence: terminateMember resolves to not-live without
	// ever calling Terminate, so the real terminator in runDown is safe here.
	agentDir := seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary: "codex", Handle: "cto", AgentPID: 0,
	})
	writePresence(t, agentDir, presenceFile{
		Schema: 1, Handle: "cto", Status: "active",
		LastSeen: time.Now().Add(-2 * time.Hour),
	})

	stdout, stderr, err := captureOutput(t, func() error {
		return runDown([]string{"--role", "cto", "--session", "issue-96"})
	})
	if err != nil {
		t.Fatalf("down alias: %v\nstdout:\n%s", err, stdout)
	}
	if !strings.Contains(stderr, "down is now 'stop'") {
		t.Errorf("deprecation hint missing from stderr:\n%s", stderr)
	}
	if !strings.Contains(stdout, "# amq-squad down") {
		t.Errorf("alias should still render under its own verb header:\n%s", stdout)
	}
	if !strings.Contains(stdout, "not-live") {
		t.Errorf("dead-pid member should read not-live:\n%s", stdout)
	}
	// On-disk state preserved (recoverable via resume).
	if _, readErr := launch.Read(agentDir); readErr != nil {
		t.Errorf("launch record must be preserved: %v", readErr)
	}
}

// TestRunStopPrimaryHasNoDeprecationHint proves the primary verb does not emit
// the alias hint. Uses a dead-pid member so the real terminator never fires.
func TestRunStopPrimaryHasNoDeprecationHint(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	agentDir := seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary: "codex", Handle: "cto", AgentPID: 0,
	})
	writePresence(t, agentDir, presenceFile{
		Schema: 1, Handle: "cto", Status: "active",
		LastSeen: time.Now().Add(-2 * time.Hour),
	})

	stdout, stderr, err := captureOutput(t, func() error {
		return runStop([]string{"--role", "cto", "--session", "issue-96"})
	})
	if err != nil {
		t.Fatalf("stop: %v\nstdout:\n%s", err, stdout)
	}
	if strings.Contains(stderr, "down is now 'stop'") {
		t.Errorf("primary stop must not print the deprecation hint:\n%s", stderr)
	}
	if !strings.Contains(stdout, "# amq-squad stop") {
		t.Errorf("stop should render under the stop verb header:\n%s", stdout)
	}
}

// TestRunStopRequiresSelector confirms the selector requirement applies to the
// primary verb too (no --force is needed any more, but a selector still is).
func TestRunStopRequiresSelector(t *testing.T) {
	seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"}},
	})
	_, _, err := captureOutput(t, func() error {
		return runStop([]string{})
	})
	if err == nil {
		t.Fatal("missing selector should be a usage error")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
}
