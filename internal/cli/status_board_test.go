package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/internal/launch"
	"github.com/omriariav/amq-squad/internal/state"
)

// boardNow is the deterministic clock anchoring the board tests so relative
// last-activity rendering and presence freshness are stable.
var boardNow = time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)

// boardProbe builds a state.Probe whose PID liveness and process-match
// decisions come from explicit maps and whose clock is fixed. Mirrors the
// internal/state fakeProbe pattern so the board test never execs a subprocess.
func boardProbe(alive, match map[int]bool) state.Probe {
	return state.Probe{
		PIDAlive:     func(pid int) bool { return alive[pid] },
		ProcessMatch: func(pid int, _ func(args string) bool) bool { return match[pid] },
		Now:          func() time.Time { return boardNow },
	}
}

func TestBoardLastActivity(t *testing.T) {
	now := boardNow
	cases := []struct {
		name string
		t    time.Time
		want string
	}{
		{"never", time.Time{}, "never"},
		{"sub-minute", now.Add(-30 * time.Second), "just now"},
		{"future-skew", now.Add(2 * time.Minute), "just now"},
		{"minutes", now.Add(-5 * time.Minute), "5m ago"},
		{"hours", now.Add(-3 * time.Hour), "3h ago"},
		{"days", now.Add(-2 * 24 * time.Hour), "2d ago"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := boardLastActivity(c.t, now); got != c.want {
				t.Errorf("boardLastActivity(%v) = %q, want %q", c.t, got, c.want)
			}
		})
	}
}

// seedBoardPresence writes presence.json under base/<session>/agents/<handle>
// (the layout state.Build scans), so the board's liveness rollup sees it.
func seedBoardPresence(t *testing.T, base, session, handle, status string, lastSeen time.Time) {
	t.Helper()
	agentDir := filepath.Join(base, session, "agents", handle)
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pres := presenceFile{Schema: 1, Handle: handle, Status: status, LastSeen: lastSeen}
	data, err := json.Marshal(pres)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(presencePath(agentDir), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// runBoardExec drives the board against a captured buffer with the BaseRoot
// seeded directly (no resolver subprocess) and the injected probe + clock.
func runBoardExec(t *testing.T, base, projectDir string, probe state.Probe, jsonOut bool) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	err := executeStatusBoard(statusBoardExecution{
		ProjectDir: projectDir,
		BaseRoot:   base,
		Probe:      probe,
		Now:        func() time.Time { return boardNow },
		Out:        &buf,
		JSON:       jsonOut,
	})
	return buf.String(), err
}

// seedMultiSessionBoard seeds three sessions into a fresh base root:
//   - running:  one alive agent.
//   - stopped:  one agent whose recorded PID is dead and no fresh mailbox.
//   - at-risk:  one agent whose PID is dead but whose mailbox is fresh-active
//     (dead-mailbox-live / zombie heartbeat) -> degraded session.
//
// Returns the base root.
func seedMultiSessionBoard(t *testing.T) string {
	t.Helper()
	base := t.TempDir()

	seedAgentRecord(t, base, "running-ws", "cto", launch.Record{
		Binary: "codex", Handle: "cto", Role: "cto", Session: "running-ws", AgentPID: 1111,
	})
	seedBoardPresence(t, base, "running-ws", "cto", "active", boardNow.Add(-30*time.Second))

	seedAgentRecord(t, base, "stopped-ws", "qa", launch.Record{
		Binary: "claude", Handle: "qa", Role: "qa", Session: "stopped-ws", AgentPID: 2222,
	})
	// No fresh presence: a recorded-but-dead PID with no live mailbox is stale,
	// so the session rolls up to stopped (0 alive).

	seedAgentRecord(t, base, "atrisk-ws", "fs", launch.Record{
		Binary: "claude", Handle: "fs", Role: "fs", Session: "atrisk-ws", AgentPID: 3333,
	})
	seedBoardPresence(t, base, "atrisk-ws", "fs", "active", boardNow.Add(-5*time.Second))

	return base
}

func TestStatusBoardMultiSession(t *testing.T) {
	base := seedMultiSessionBoard(t)
	proj := t.TempDir()
	// running PID alive+match; stopped/atrisk PIDs dead.
	probe := boardProbe(
		map[int]bool{1111: true, 2222: false, 3333: false},
		map[int]bool{1111: true},
	)
	out, err := runBoardExec(t, base, proj, probe, false)
	if err != nil {
		t.Fatalf("board: %v\n%s", err, out)
	}
	// Header names the base root so the operator sees what was scanned.
	if !strings.Contains(out, "AM_BASE_ROOT") {
		t.Errorf("board missing AM_BASE_ROOT header:\n%s", out)
	}
	// Column header is TEXT-led.
	for _, want := range []string{"SESSION", "STATE", "AGENTS", "BRIEF", "LAST-ACTIVITY"} {
		if !strings.Contains(out, want) {
			t.Errorf("board missing column %q:\n%s", want, out)
		}
	}
	// Three sessions, each on its own line with the rolled-up state.
	for _, want := range []string{
		"running-ws", "running",
		"stopped-ws", "stopped",
		"atrisk-ws", "degraded",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("board missing %q:\n%s", want, out)
		}
	}
	// The at-risk session surfaces its dead-mailbox-live agent explicitly.
	if !strings.Contains(out, "at-risk") {
		t.Errorf("board missing at-risk note for dead-mailbox-live session:\n%s", out)
	}
	// Agent health is "N/M alive".
	if !strings.Contains(out, "1/1 alive") {
		t.Errorf("board missing health count:\n%s", out)
	}
}

func TestStatusBoardSessionsJSONEnvelope(t *testing.T) {
	base := seedMultiSessionBoard(t)
	proj := t.TempDir()
	probe := boardProbe(
		map[int]bool{1111: true, 2222: false, 3333: false},
		map[int]bool{1111: true},
	)
	out, err := runBoardExec(t, base, proj, probe, true)
	if err != nil {
		t.Fatalf("board --json: %v\n%s", err, out)
	}
	// The new envelope kind is "sessions"; schema_version is UNCHANGED.
	env := decodeJSONEnvelope[sessionsEnvelopeData](t, out)
	if env.Kind != "sessions" {
		t.Errorf("kind = %q, want sessions", env.Kind)
	}
	if env.SchemaVersion != JSONSchemaVersion {
		t.Errorf("schema_version = %d, want %d (must NOT bump for the new kind)", env.SchemaVersion, JSONSchemaVersion)
	}
	if env.Data.BaseRoot != base {
		t.Errorf("base_root = %q, want %q", env.Data.BaseRoot, base)
	}
	if len(env.Data.Sessions) != 3 {
		t.Fatalf("want 3 sessions, got %d: %+v", len(env.Data.Sessions), env.Data.Sessions)
	}
	byName := map[string]sessionBoardRow{}
	for _, s := range env.Data.Sessions {
		byName[s.Name] = s
	}
	if got := byName["running-ws"].State; got != boardStateRunning {
		t.Errorf("running-ws state = %q, want running", got)
	}
	if got := byName["stopped-ws"].State; got != boardStateStopped {
		t.Errorf("stopped-ws state = %q, want stopped", got)
	}
	atrisk := byName["atrisk-ws"]
	if atrisk.State != boardStateDegraded {
		t.Errorf("atrisk-ws state = %q, want degraded", atrisk.State)
	}
	if atrisk.AtRisk != 1 {
		t.Errorf("atrisk-ws at_risk = %d, want 1", atrisk.AtRisk)
	}
	if running := byName["running-ws"]; running.AgentsAlive != 1 || running.AgentsTotal != 1 {
		t.Errorf("running-ws alive/total = %d/%d, want 1/1", running.AgentsAlive, running.AgentsTotal)
	}
}

// TestStatusBoardJSONHasNoHumanComments guards the JSON contract: the sessions
// envelope must not leak the human "# AM_BASE_ROOT" header onto stdout.
func TestStatusBoardJSONHasNoHumanComments(t *testing.T) {
	base := seedMultiSessionBoard(t)
	proj := t.TempDir()
	out, err := runBoardExec(t, base, proj, boardProbe(map[int]bool{1111: true}, map[int]bool{1111: true}), true)
	if err != nil {
		t.Fatalf("board --json: %v", err)
	}
	if strings.Contains(out, "\n#") || strings.HasPrefix(out, "#") {
		t.Errorf("board --json leaked human comment lines on stdout:\n%s", out)
	}
}

// TestStatusBoardEmptyDiscoveryDegradesGracefully proves an empty base root
// (no sessions) renders a non-fatal guidance state, not an error.
func TestStatusBoardEmptyDiscoveryDegradesGracefully(t *testing.T) {
	base := t.TempDir() // empty: no sessions seeded
	proj := t.TempDir()
	out, err := runBoardExec(t, base, proj, boardProbe(nil, nil), false)
	if err != nil {
		t.Fatalf("empty discovery must not error, got %v", err)
	}
	if !strings.Contains(out, "no sessions found") {
		t.Errorf("expected 'no sessions found' guidance, got:\n%s", out)
	}
}

// TestStatusBoardUnresolvedBaseRootDegradesGracefully proves that when the AMQ
// base root cannot be resolved (amq missing), the board renders a guidance
// notice naming what was looked for and returns nil — never crashing the
// front-door invocation.
func TestStatusBoardUnresolvedBaseRootDegradesGracefully(t *testing.T) {
	proj := t.TempDir()
	var buf bytes.Buffer
	err := executeStatusBoard(statusBoardExecution{
		ProjectDir: proj,
		ResolveBaseRoot: func(string) (string, error) {
			return "", os.ErrNotExist
		},
		Out: &buf,
	})
	if err != nil {
		t.Fatalf("unresolved base root must not error, got %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "amq env") || !strings.Contains(out, "PATH") {
		t.Errorf("guidance should name the `amq env` probe and PATH, got:\n%s", out)
	}
}

// TestStatusBoardUnresolvedJSONStillEmitsEnvelope proves the degraded path also
// honors --json: it returns a well-formed sessions envelope with an empty list
// and a notice, at the UNCHANGED schema_version.
func TestStatusBoardUnresolvedJSONStillEmitsEnvelope(t *testing.T) {
	proj := t.TempDir()
	var buf bytes.Buffer
	err := executeStatusBoard(statusBoardExecution{
		ProjectDir:      proj,
		ResolveBaseRoot: func(string) (string, error) { return "", os.ErrNotExist },
		Out:             &buf,
		JSON:            true,
	})
	if err != nil {
		t.Fatalf("unresolved base root --json must not error, got %v", err)
	}
	env := decodeJSONEnvelope[sessionsEnvelopeData](t, buf.String())
	if env.Kind != "sessions" {
		t.Errorf("kind = %q, want sessions", env.Kind)
	}
	if env.SchemaVersion != JSONSchemaVersion {
		t.Errorf("schema_version = %d, want %d", env.SchemaVersion, JSONSchemaVersion)
	}
	if len(env.Data.Sessions) != 0 {
		t.Errorf("want 0 sessions on degraded path, got %d", len(env.Data.Sessions))
	}
	if env.Data.Notice == "" {
		t.Error("degraded envelope should carry a notice")
	}
}

// TestStatusBoardBriefOneLiner proves the board reads the first meaningful line
// of the workstream brief and skips the heading title.
func TestStatusBoardBriefOneLiner(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary: "codex", Handle: "cto", Role: "cto", Session: "issue-96", AgentPID: 1111,
	})
	briefDir := filepath.Join(proj, ".amq-squad", briefsDirName)
	if err := os.MkdirAll(briefDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "# issue-96\n\nFix the restore session-root collision.\n"
	if err := os.WriteFile(filepath.Join(briefDir, "issue-96.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runBoardExec(t, base, proj, boardProbe(map[int]bool{1111: true}, map[int]bool{1111: true}), false)
	if err != nil {
		t.Fatalf("board: %v", err)
	}
	if !strings.Contains(out, "Fix the restore session-root collision.") {
		t.Errorf("board missing brief one-liner:\n%s", out)
	}
	if strings.Contains(out, "# issue-96") {
		t.Errorf("board should skip the brief heading title:\n%s", out)
	}
}

func TestRelativeAge(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "just now"},
		{5 * time.Minute, "5m"},
		{3 * time.Hour, "3h"},
		{50 * time.Hour, "2d"},
	}
	for _, c := range cases {
		if got := relativeAge(c.d); got != c.want {
			t.Errorf("relativeAge(%s) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestBoardLastActivityFreshnessHonesty(t *testing.T) {
	// Zero time -> never.
	if got := boardLastActivity(time.Time{}, boardNow); got != "never" {
		t.Errorf("zero last-activity = %q, want never", got)
	}
	// Future timestamp (clock skew) -> clamped, not negative.
	if got := boardLastActivity(boardNow.Add(time.Hour), boardNow); got != "just now" {
		t.Errorf("future last-activity = %q, want just now", got)
	}
	// Normal past -> relative age + ago.
	if got := boardLastActivity(boardNow.Add(-2*time.Hour), boardNow); got != "2h ago" {
		t.Errorf("past last-activity = %q, want '2h ago'", got)
	}
}
