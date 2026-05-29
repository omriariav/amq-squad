package console

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/internal/launch"
	"github.com/omriariav/amq-squad/internal/state"
)

// runNow is the deterministic clock anchoring the runner tests.
var runNow = time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)

// runProbe builds a state.Probe from explicit maps with a fixed clock, mirroring
// the internal/state and status-board fake-probe pattern. No subprocess ever
// runs.
func runProbe(alive, match map[int]bool) state.Probe {
	return state.Probe{
		PIDAlive:     func(pid int) bool { return alive[pid] },
		ProcessMatch: func(pid int, _ func(args string) bool) bool { return match[pid] },
		Now:          func() time.Time { return runNow },
	}
}

// seedAgentRecord writes a launch.json under base/<session>/agents/<handle>.
func seedAgentRecord(t *testing.T, base, session, handle string, rec launch.Record) string {
	t.Helper()
	agentDir := filepath.Join(base, session, "agents", handle)
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	return agentDir
}

// seedPresence writes presence.json into an agent dir so liveness rolls up.
func seedPresence(t *testing.T, agentDir, handle, status string, lastSeen time.Time) {
	t.Helper()
	pres := struct {
		Schema   int       `json:"schema"`
		Handle   string    `json:"handle"`
		Status   string    `json:"status"`
		LastSeen time.Time `json:"last_seen"`
	}{Schema: 1, Handle: handle, Status: status, LastSeen: lastSeen}
	data, err := json.Marshal(pres)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "presence.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// seedMultiSessionConsole seeds two sessions into a fresh base root so the
// --once board has real content to render, and returns the base root.
func seedMultiSessionConsole(t *testing.T) string {
	t.Helper()
	base := t.TempDir()

	dir := seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary: "codex", Handle: "cto", Role: "cto", Session: "issue-96", AgentPID: 1111,
	})
	seedPresence(t, dir, "cto", "active", runNow.Add(-30*time.Second))

	seedAgentRecord(t, base, "drive-fix", "qa", launch.Record{
		Binary: "claude", Handle: "qa", Role: "qa", Session: "drive-fix", AgentPID: 2222,
	})
	// No fresh presence: qa's recorded PID is dead -> not alive.

	return base
}

// TestRunOnceRendersStaticBoard proves --once renders the static board (with the
// snapshot-wide triage rollup line) to the supplied writer and starts NO tea
// program. It seeds a real multi-session fixture and uses an injected probe.
func TestRunOnceRendersStaticBoard(t *testing.T) {
	base := seedMultiSessionConsole(t)
	proj := t.TempDir()
	var buf bytes.Buffer

	err := Run(Config{
		ProjectDir: proj,
		BaseRoot:   base,
		Probe:      runProbe(map[int]bool{1111: true, 2222: false}, map[int]bool{1111: true}),
		Once:       true,
		Out:        &buf,
	})
	if err != nil {
		t.Fatalf("Run --once: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"mission control",
		"2 sessions",
		"needs-you", // the triage rollup line is present
		"at-risk",
		"blocked",
		"issue-96",
		"drive-fix",
		"cto",
		"qa",
	} {
		if !contains(out, want) {
			t.Errorf("--once board missing %q:\n%s", want, out)
		}
	}
}

// TestRunOnceNoTeamNoticeToStdout proves a degraded NoTeamNotice on the --once
// path writes guidance to Out and exits 0 (never starts a program).
func TestRunOnceNoTeamNoticeToStdout(t *testing.T) {
	var buf bytes.Buffer
	err := Run(Config{
		Once:         true,
		Out:          &buf,
		NoTeamNotice: "amq-squad: no team configured for profile \"default\".",
	})
	if err != nil {
		t.Fatalf("Run --once with notice: %v", err)
	}
	if !contains(buf.String(), "no team configured") {
		t.Errorf("notice should be written to Out:\n%s", buf.String())
	}
}

// TestRunOnceInitialFilterScopesBoard proves a preset InitialFilter (e.g. from
// --session) narrows the --once board to the matching session.
func TestRunOnceInitialFilterScopesBoard(t *testing.T) {
	base := seedMultiSessionConsole(t)
	proj := t.TempDir()
	var buf bytes.Buffer
	err := Run(Config{
		ProjectDir:    proj,
		BaseRoot:      base,
		Probe:         runProbe(map[int]bool{1111: true, 2222: false}, map[int]bool{1111: true}),
		Once:          true,
		Out:           &buf,
		InitialFilter: "session:issue-96",
	})
	if err != nil {
		t.Fatalf("Run --once with filter: %v", err)
	}
	out := buf.String()
	if !contains(out, "issue-96") {
		t.Errorf("filtered board should include issue-96:\n%s", out)
	}
	if contains(out, "drive-fix") {
		t.Errorf("session:issue-96 filter should exclude drive-fix:\n%s", out)
	}
}

// TestRunInteractiveTTYFailureIsReported proves the interactive path reports a
// clear error (rather than silently doing nothing or crashing) when /dev/tty
// cannot be opened, via the injected ttyOpener seam — no real program runs.
func TestRunInteractiveTTYFailureIsReported(t *testing.T) {
	base := seedMultiSessionConsole(t)
	proj := t.TempDir()
	err := Run(Config{
		ProjectDir: proj,
		BaseRoot:   base,
		Probe:      runProbe(map[int]bool{1111: true, 2222: false}, map[int]bool{1111: true}),
		Once:       false,
		ttyOpener:  func() (*os.File, error) { return nil, os.ErrNotExist },
	})
	if err == nil {
		t.Fatal("interactive path with no /dev/tty should report an error")
	}
	if !contains(err.Error(), "--once") {
		t.Errorf("the TTY-failure error should point at --once, got: %v", err)
	}
}

func contains(haystack, needle string) bool {
	return bytes.Contains([]byte(haystack), []byte(needle))
}
