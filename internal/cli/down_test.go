package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/internal/launch"
	"github.com/omriariav/amq-squad/internal/team"
)

type recordingTerminator struct {
	mu     sync.Mutex
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

func TestRunDownRejectsRoleAndAll(t *testing.T) {
	seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"}},
	})
	_, _, err := captureOutput(t, func() error {
		return runDown([]string{"--role", "cto", "--all", "--force"})
	})
	if err == nil {
		t.Fatal("--role and --all together should be a usage error")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
}

func TestRunDownRequiresSelector(t *testing.T) {
	seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"}},
	})
	_, _, err := captureOutput(t, func() error {
		return runDown([]string{"--force"})
	})
	if err == nil {
		t.Fatal("missing selector should be a usage error")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
}

func TestRunDownGracefulReturnsUnavailable(t *testing.T) {
	seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"}},
	})
	_, _, err := captureOutput(t, func() error {
		return runDown([]string{"--role", "cto"})
	})
	if err == nil || !strings.Contains(err.Error(), "graceful down is unavailable") {
		t.Fatalf("graceful path should fail with unavailable: got %v", err)
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

func TestExecuteDownForceSendsTermToVerifiedPID(t *testing.T) {
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

	term := &recordingTerminator{}
	out, err := runDownExec(t, downExecution{
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
	for _, want := range []string{"# workstream: issue-96", "cto", "fullstack", "force-sent", "SIGTERM sent to pid 1111", "SIGTERM sent to pid 2222"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q in:\n%s", want, out)
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
	for _, want := range []string{"force-sent", "failed", "SIGTERM sent to pid 100", "terminate pid 200: operation not permitted"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q in:\n%s", want, out)
		}
	}
}

func TestExecuteDownResolvesDefaultWorkstream(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		// Legacy-style per-role session so defaultTeamWorkstreamName falls
		// through to defaultWorkstreamName(projectDir).
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
