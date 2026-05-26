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
	"github.com/omriariav/amq-squad/internal/team"
)

func statusProbe(alive map[int]bool, match map[int]bool, now time.Time) duplicateLaunchProbe {
	return duplicateLaunchProbe{
		PIDAlive:     func(pid int) bool { return alive[pid] },
		ProcessMatch: func(pid int, _ func(args string) bool) bool { return match[pid] },
		Now:          func() time.Time { return now },
	}
}

func runStatusExec(t *testing.T, s statusExecution) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	s.Out = &buf
	err := executeStatus(s)
	return buf.String(), err
}

func TestRunStatusRequiresTeam(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	_, _, err := captureOutput(t, func() error {
		return runStatus(nil)
	})
	if err == nil || !strings.Contains(err.Error(), "no team configured") {
		t.Fatalf("want 'no team configured' error, got %v", err)
	}
}

func TestExecuteStatusLiveAgent(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	})
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary: "codex", Handle: "cto", AgentPID: 5555,
	})
	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		Probe:            statusProbe(map[int]bool{5555: true}, map[int]bool{5555: true}, time.Now()),
	})
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	for _, want := range []string{"cto", "live", "agent pid 5555 alive"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q in:\n%s", want, out)
		}
	}
}

func TestExecuteStatusStaleWhenPIDDead(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary: "codex", Handle: "cto", AgentPID: 7777,
	})
	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		Probe:            statusProbe(map[int]bool{7777: false}, map[int]bool{7777: true}, time.Now()),
	})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "stale") || !strings.Contains(out, "pid 7777 not alive") {
		t.Errorf("expected stale + dead-pid detail:\n%s", out)
	}
}

func TestExecuteStatusStaleOnBinaryMismatch(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary: "codex", Handle: "cto", AgentPID: 8888,
	})
	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		Probe:            statusProbe(map[int]bool{8888: true}, map[int]bool{8888: false}, time.Now()),
	})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "stale") || !strings.Contains(out, "binary mismatch") {
		t.Errorf("expected stale + binary-mismatch detail:\n%s", out)
	}
}

func TestExecuteStatusMissingWhenNoSignals(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		Probe:            statusProbe(nil, nil, time.Now()),
	})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "missing") || !strings.Contains(out, "no live signals") {
		t.Errorf("expected missing + no-signals detail:\n%s", out)
	}
}

func writeStatusPresence(t *testing.T, base, workstream, handle string, pres presenceFile) {
	t.Helper()
	agentDir := filepath.Join(base, workstream, "agents", handle)
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, marshalErr := json.Marshal(pres)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if err := os.WriteFile(presencePath(agentDir), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteStatusLiveOnFreshActivePresence(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	now := time.Now()
	writeStatusPresence(t, base, "issue-96", "cto", presenceFile{
		Handle:   "cto",
		Status:   "active",
		LastSeen: now.Add(-10 * time.Second),
	})
	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(nil, nil, now),
	})
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	if env.Kind != "status" {
		t.Errorf("envelope kind = %q, want status", env.Kind)
	}
	rows := env.Data.Records
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].Status != statusStateLive {
		t.Errorf("status = %q, want live", rows[0].Status)
	}
	if rows[0].Signals.Presence != "active" || rows[0].Signals.LastSeen.IsZero() {
		t.Errorf("presence signals not exposed: %+v", rows[0].Signals)
	}
}

func TestExecuteStatusSurfacesRootAndPresenceOnlyDetail(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	now := time.Now()
	writeStatusPresence(t, base, "issue-96", "cto", presenceFile{
		Handle:   "cto",
		Status:   "active",
		LastSeen: now.Add(-10 * time.Second),
	})
	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		JSON:             false,
		Probe:            statusProbe(nil, nil, now),
	})
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	// AM_ROOT must be discoverable, and a presence-only live member must say so
	// (no verified pid) so it reconciles with down's "no pid to signal".
	for _, want := range []string{"# AM_ROOT:", "no verified pid"} {
		if !strings.Contains(out, want) {
			t.Errorf("status output missing %q:\n%s", want, out)
		}
	}
}

func TestExecuteStatusStalePresenceCollapsesToMissing(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	now := time.Now()
	writeStatusPresence(t, base, "issue-96", "cto", presenceFile{
		Handle:   "cto",
		Status:   "active",
		LastSeen: now.Add(-1 * time.Hour),
	})
	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		Probe:            statusProbe(nil, nil, now),
	})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "missing") {
		t.Errorf("stale-only presence must collapse to missing, got:\n%s", out)
	}
}

func TestExecuteStatusInactivePresenceCollapsesToMissing(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	now := time.Now()
	writeStatusPresence(t, base, "issue-96", "cto", presenceFile{
		Handle:   "cto",
		Status:   "idle",
		LastSeen: now.Add(-10 * time.Second),
	})
	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		Probe:            statusProbe(nil, nil, now),
	})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "missing") {
		t.Errorf("inactive presence must collapse to missing, got:\n%s", out)
	}
}

func TestExecuteStatusPresenceHandleMismatchIsStaleNotLive(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	now := time.Now()
	writeStatusPresence(t, base, "issue-96", "cto", presenceFile{
		Handle:   "someone-else",
		Status:   "active",
		LastSeen: now.Add(-10 * time.Second),
	})
	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		Probe:            statusProbe(nil, nil, now),
	})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if strings.Contains(out, "\tlive\t") || strings.Contains(out, " live ") {
		t.Errorf("handle-mismatched presence must not be live:\n%s", out)
	}
	if !strings.Contains(out, "stale") {
		t.Errorf("handle-mismatched fresh presence should surface as stale:\n%s", out)
	}
}

func TestExecuteStatusWakeLockOnlyStale(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	// Seed a wake.lock with a live PID but no launch record. Status must
	// classify this as stale (live signal present but no verified agent),
	// never as live.
	agentDir := filepath.Join(base, "issue-96", "agents", "cto")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lock := wakeLockFile{PID: 4321, Root: filepath.Join(base, "issue-96"), Started: time.Now()}
	data, err := json.Marshal(lock)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wakeLockPath(agentDir), data, 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		Probe:            statusProbe(map[int]bool{4321: true}, map[int]bool{4321: true}, time.Now()),
	})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "stale") {
		t.Errorf("wake-only must be stale, never live:\n%s", out)
	}
}

func TestExecuteStatusJSON(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "issue-96"},
		},
	})
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary: "codex", Handle: "cto", AgentPID: 100,
	})

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(map[int]bool{100: true}, map[int]bool{100: true}, time.Now()),
	})
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	if env.Kind != "status" {
		t.Errorf("envelope kind = %q, want status", env.Kind)
	}
	rows := env.Data.Records
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %v", len(rows), rows)
	}
	var ctoRow, fullstackRow statusRecord
	for _, r := range rows {
		switch r.Role {
		case "cto":
			ctoRow = r
		case "fullstack":
			fullstackRow = r
		}
	}
	if ctoRow.Status != statusStateLive {
		t.Errorf("cto status = %q, want live", ctoRow.Status)
	}
	if ctoRow.Signals.AgentPID != 100 || !ctoRow.Signals.AgentAlive || !ctoRow.Signals.BinaryMatch {
		t.Errorf("cto signals incomplete: %+v", ctoRow.Signals)
	}
	if fullstackRow.Status != statusStateMissing {
		t.Errorf("fullstack status = %q, want missing", fullstackRow.Status)
	}
}

func TestExecuteStatusIgnoresUnconfiguredHandles(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	// Stranger agent dir exists on disk; status must not surface it as a row.
	seedAgentRecord(t, base, "issue-96", "stranger", launch.Record{
		Binary: "claude", Handle: "stranger", AgentPID: 12345,
	})

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(map[int]bool{12345: true}, map[int]bool{12345: true}, time.Now()),
	})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if strings.Contains(out, "stranger") {
		t.Errorf("status leaked unconfigured handle:\n%s", out)
	}
}

func TestExecuteStatusDefaultWorkstreamFallthrough(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "cto"}},
	})
	workstream := defaultWorkstreamName(dir)
	seedAgentRecord(t, base, workstream, "cto", launch.Record{
		Binary: "codex", Handle: "cto", AgentPID: 9,
	})
	out, err := runStatusExec(t, statusExecution{
		ProjectDir: dir,
		Probe:      statusProbe(map[int]bool{9: true}, map[int]bool{9: true}, time.Now()),
	})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, workstream) {
		t.Errorf("default workstream %q not in output:\n%s", workstream, out)
	}
}
