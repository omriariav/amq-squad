package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
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

func TestBoardRowAgesOutStaleRecords(t *testing.T) {
	now := boardNow
	old := now.Add(-96 * time.Hour) // older than boardStaleRecordAge (72h)
	recent := now.Add(-10 * time.Minute)

	t.Run("ghosts do not drag a live session to degraded", func(t *testing.T) {
		sess := state.Session{
			Name: "ws",
			Agents: []state.Agent{
				{Liveness: state.LivenessAlive, LastSeen: recent},
				{Liveness: state.LivenessStale, LastSeen: old},
				{Liveness: state.LivenessDead, LastSeen: old},
			},
		}
		row := boardRowFor(t.TempDir(), sess, now)
		if row.State != boardStateRunning {
			t.Fatalf("state = %q, want running (old ghosts aged out)", row.State)
		}
		if row.AgentsAlive != 1 || row.AgentsTotal != 1 || row.AgentsStale != 2 {
			t.Fatalf("got alive=%d total=%d stale=%d; want 1/1/2", row.AgentsAlive, row.AgentsTotal, row.AgentsStale)
		}
		if cell := boardAgentsCell(row); !strings.Contains(cell, "1/1 alive") || !strings.Contains(cell, "(+2 stale)") {
			t.Fatalf("cell = %q, want '1/1 alive (+2 stale)'", cell)
		}
	})

	t.Run("only old ghosts read stopped, not degraded", func(t *testing.T) {
		sess := state.Session{
			Name: "ws",
			Agents: []state.Agent{
				{Liveness: state.LivenessStale, LastSeen: old},
				{Liveness: state.LivenessDead, LastSeen: old},
			},
		}
		row := boardRowFor(t.TempDir(), sess, now)
		if row.State != boardStateStopped {
			t.Fatalf("state = %q, want stopped", row.State)
		}
		if row.AgentsTotal != 0 || row.AgentsStale != 2 {
			t.Fatalf("got total=%d stale=%d; want 0/2", row.AgentsTotal, row.AgentsStale)
		}
		if cell := boardAgentsCell(row); cell != "stopped (2 stale)" {
			t.Fatalf("cell = %q, want 'stopped (2 stale)'", cell)
		}
	})

	t.Run("a recently-down member still reads degraded", func(t *testing.T) {
		sess := state.Session{
			Name: "ws",
			Agents: []state.Agent{
				{Liveness: state.LivenessAlive, LastSeen: recent},
				{Liveness: state.LivenessStale, LastSeen: recent}, // not yet cold
			},
		}
		row := boardRowFor(t.TempDir(), sess, now)
		if row.State != boardStateDegraded {
			t.Fatalf("state = %q, want degraded (recent down member counts)", row.State)
		}
		if row.AgentsTotal != 2 || row.AgentsStale != 0 {
			t.Fatalf("got total=%d stale=%d; want 2/0", row.AgentsTotal, row.AgentsStale)
		}
	})

	t.Run("an undated leftover record is kept, not aged out", func(t *testing.T) {
		sess := state.Session{
			Name:   "ws",
			Agents: []state.Agent{{Liveness: state.LivenessMissing}}, // zero LastSeen
		}
		row := boardRowFor(t.TempDir(), sess, now)
		if row.AgentsTotal != 1 || row.AgentsStale != 0 {
			t.Fatalf("undated record: total=%d stale=%d; want 1/0", row.AgentsTotal, row.AgentsStale)
		}
	})
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
	// The old "# AM_BASE_ROOT" debug header is GONE from the default render: it
	// read like stray debug output on the front-door command.
	if strings.Contains(out, "AM_BASE_ROOT") {
		t.Errorf("board should not lead with the AM_BASE_ROOT debug header:\n%s", out)
	}
	// A tempdir base root is non-default, so the summary folds it in compactly
	// instead of a leading debug header.
	if !strings.Contains(out, "root: "+base) {
		t.Errorf("non-default root should be folded into the summary line:\n%s", out)
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
	// Agent health is "N/M alive" for the live session.
	if !strings.Contains(out, "1/1 alive") {
		t.Errorf("board missing health count:\n%s", out)
	}
	// A stopped squad reads "stopped" or "M agents", NEVER "0/N alive". (A
	// degraded session legitimately shows "0/1 alive (1 at-risk)"; the guard is
	// scoped to the stopped row only.)
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "stopped-ws") && strings.Contains(line, "alive") {
			t.Errorf("stopped squad should not render '0/N alive':\n%s", line)
		}
	}
}

func TestStatusBoardWakeLiveSessionIsDegradedNotStopped(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	agentDir := filepath.Join(base, "wake-ws", "agents", "cto")
	seedAgentRecord(t, base, "wake-ws", "cto", launch.Record{
		Binary: "codex", Handle: "cto", Role: "cto", Session: "wake-ws", Root: filepath.Join(base, "wake-ws"),
	})
	writeWakeLock(t, agentDir, wakeLockFile{PID: 4444, Root: filepath.Join(base, "wake-ws"), Started: boardNow.Add(-time.Minute)})

	probe := boardProbe(map[int]bool{4444: true}, map[int]bool{4444: true})
	out, err := runBoardExec(t, base, proj, probe, false)
	if err != nil {
		t.Fatalf("board: %v\n%s", err, out)
	}
	for _, want := range []string{"wake-ws", "degraded", "0/1 alive (1 wake-live)", "1 wake-live"} {
		if !strings.Contains(out, want) {
			t.Errorf("wake-live board missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "wake-ws  stopped") {
		t.Errorf("wake-live session must not render stopped:\n%s", out)
	}

	jsonOut, err := runBoardExec(t, base, proj, probe, true)
	if err != nil {
		t.Fatalf("board json: %v\n%s", err, jsonOut)
	}
	env := decodeJSONEnvelope[sessionsEnvelopeData](t, jsonOut)
	if len(env.Data.Sessions) != 1 {
		t.Fatalf("sessions = %+v, want one wake-live session", env.Data.Sessions)
	}
	row := env.Data.Sessions[0]
	if row.State != boardStateDegraded || row.WakeLive != 1 || row.AgentsAlive != 0 {
		t.Fatalf("wake-live json row = %+v, want degraded wake_live=1 agents_alive=0", row)
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

func TestStatusBoardJSONCarriesProfileActionsAndOrchestration(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	proj := t.TempDir()
	if err := team.Write(proj, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "lead-handle", Session: "running-ws"},
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "running-ws"},
		},
		Orchestrated: true,
		Lead:         "cto",
	}); err != nil {
		t.Fatal(err)
	}
	seedAgentRecord(t, base, "running-ws", "lead-handle", launch.Record{
		Binary: "codex", Handle: "lead-handle", Role: "cto", Session: "running-ws", AgentPID: 1111,
		TeamProfile: team.DefaultProfile,
		Tmux:        &launch.TmuxInfo{Session: "tmux-running-ws", PaneID: "%118"},
	})
	seedBoardPresence(t, base, "running-ws", "lead-handle", "active", boardNow.Add(-30*time.Second))
	swapStatusPaneLister(t, []tmuxpane.TmuxPane{{PaneID: "%118"}}, nil)

	probe := boardProbe(map[int]bool{1111: true}, map[int]bool{1111: true})
	boardOut, err := runBoardExec(t, base, proj, probe, true)
	if err != nil {
		t.Fatalf("board --json: %v\n%s", err, boardOut)
	}
	boardEnv := decodeJSONEnvelope[sessionsEnvelopeData](t, boardOut)
	if len(boardEnv.Data.Sessions) != 1 {
		t.Fatalf("sessions = %+v, want one", boardEnv.Data.Sessions)
	}
	row := boardEnv.Data.Sessions[0]
	if row.Profile != team.DefaultProfile {
		t.Fatalf("board profile = %q, want default", row.Profile)
	}
	if row.Namespace.ID != "default/running-ws" {
		t.Fatalf("board namespace = %+v, want default/running-ws", row.Namespace)
	}
	if !row.Orchestrated || row.Lead != "cto" || row.LeadHandle != "lead-handle" {
		t.Fatalf("board orchestration = orchestrated:%v lead:%q lead_handle:%q, want true/cto/lead-handle", row.Orchestrated, row.Lead, row.LeadHandle)
	}
	if row.Execution == nil || row.Execution.Mode != executionModeProjectLead || row.Execution.MutableActor != "cto" || !row.Execution.ImplementationAllowed {
		t.Fatalf("board execution = %+v, want project_lead led by cto", row.Execution)
	}
	if row.OperatorDelivery == nil || !row.OperatorDelivery.PollRequired || row.OperatorDelivery.WakeSupported || !row.OperatorDelivery.DurableAMQ {
		t.Fatalf("board operator_delivery = %+v, want poll-required durable AMQ without wake", row.OperatorDelivery)
	}
	if len(row.Actions) == 0 {
		t.Fatalf("board actions empty: %+v", row)
	}

	statusOut, err := runStatusExec(t, statusExecution{
		ProjectDir:       proj,
		RequestedSession: "running-ws",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(map[int]bool{1111: true}, map[int]bool{1111: true}, boardNow),
	})
	if err != nil {
		t.Fatalf("status --json: %v\n%s", err, statusOut)
	}
	statusEnv := decodeJSONEnvelope[statusEnvelopeData](t, statusOut)
	if statusEnv.Data.Namespace.ID != "default/running-ws" {
		t.Fatalf("status namespace = %+v, want default/running-ws", statusEnv.Data.Namespace)
	}
	for _, record := range statusEnv.Data.Records {
		if record.Namespace.ID != "default/running-ws" {
			t.Fatalf("status record namespace = %+v, want default/running-ws", record.Namespace)
		}
	}
	if !reflect.DeepEqual(row.Actions, statusEnv.Data.Actions) {
		t.Fatalf("board actions differ from single-session actions\nboard:  %+v\nstatus: %+v", row.Actions, statusEnv.Data.Actions)
	}
	if row.Actions[len(row.Actions)-1].Kind != "attach_control" {
		t.Fatalf("board actions should include attach_control for live tmux session: %+v", row.Actions)
	}
}

func TestStatusBoardJSONReleaseReadinessBlockedByVisibleLeadInvariant(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	proj := t.TempDir()
	if err := team.Write(proj, team.Team{
		Members: []team.Member{
			{Role: "release-lead", Binary: "codex", Handle: "release-lead", Session: "v2-11-0"},
			{Role: "developer", Binary: "claude", Handle: "developer", Session: "v2-11-0"},
		},
		Orchestrated:  true,
		Lead:          "release-lead",
		ExecutionMode: executionModeProjectTeam,
		LeadExecution: &team.LeadExecution{
			Posture:             team.LeadExecutionVisibleTeam,
			IndependentReview:   &team.IndependentReview{Status: team.IndependentReviewComplete, Reviewer: "developer"},
			FinalRecommendation: "ready except visibility",
		},
	}); err != nil {
		t.Fatal(err)
	}
	seedAgentRecord(t, base, "v2-11-0", "release-lead", launch.Record{
		Binary: "codex", Handle: "release-lead", Role: "release-lead", Session: "v2-11-0", AgentPID: 2222,
		TeamProfile:  team.DefaultProfile,
		AdoptionMode: "managed_session", LauncherPaneID: "%launcher",
		Tmux: &launch.TmuxInfo{Session: "detached", PaneID: "%222", Target: "new-session"},
	})
	seedBoardPresence(t, base, "v2-11-0", "release-lead", "active", boardNow.Add(-30*time.Second))
	swapStatusPaneLister(t, []tmuxpane.TmuxPane{{PaneID: "%222"}}, nil)

	out, err := runBoardExec(t, base, proj, boardProbe(map[int]bool{2222: true}, map[int]bool{2222: true}), true)
	if err != nil {
		t.Fatalf("board --json: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[sessionsEnvelopeData](t, out)
	if len(env.Data.Sessions) != 1 || env.Data.Sessions[0].Execution == nil {
		t.Fatalf("board sessions = %+v, want one row with execution", env.Data.Sessions)
	}
	exec := env.Data.Sessions[0].Execution
	if !exec.InvariantsEvaluated || exec.InvariantOK {
		t.Fatalf("board execution invariants = evaluated:%v ok:%v errors:%v, want evaluated false invariant", exec.InvariantsEvaluated, exec.InvariantOK, exec.InvariantErrors)
	}
	if exec.ReleaseReadiness.Ready {
		t.Fatalf("board release readiness = %+v, want blocked by visible lead invariant", exec.ReleaseReadiness)
	}
	gate := readinessGatesByCode(exec.ReleaseReadiness.Gates)["visible_lead_invariants_ok"]
	if gate.Passed || !strings.Contains(gate.Evidence, "no_visible_lead") {
		t.Fatalf("visible lead gate = %+v, want failed no_visible_lead evidence", gate)
	}
}

func TestStatusBoardJSONOmitsOrchestrationForFlatTeams(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	proj := t.TempDir()
	if err := team.Write(proj, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "flat-ws"}},
	}); err != nil {
		t.Fatal(err)
	}
	seedAgentRecord(t, base, "flat-ws", "cto", launch.Record{
		Binary: "codex", Handle: "cto", Role: "cto", Session: "flat-ws", AgentPID: 1111,
	})
	seedBoardPresence(t, base, "flat-ws", "cto", "active", boardNow.Add(-30*time.Second))

	out, err := runBoardExec(t, base, proj, boardProbe(map[int]bool{1111: true}, map[int]bool{1111: true}), true)
	if err != nil {
		t.Fatalf("board --json: %v\n%s", err, out)
	}
	for _, absent := range []string{`"orchestrated"`, `"lead"`, `"lead_handle"`} {
		if strings.Contains(out, absent) {
			t.Fatalf("flat board JSON should omit %s:\n%s", absent, out)
		}
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

// TestClassifyBriefStubVsNoneVsReal proves the three-way brief classification:
// a missing file is briefNone, an untouched generated stub is briefStub (NOT
// parroted as real), and an operator-authored brief is briefReal with its first
// meaningful line as the one-liner.
func TestClassifyBriefStubVsNoneVsReal(t *testing.T) {
	proj := t.TempDir()
	briefDir := filepath.Join(proj, ".amq-squad", briefsDirName)
	if err := os.MkdirAll(briefDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// no-brief: nothing on disk for this session.
	if line, kind := classifyBrief(proj, "ghost-ws"); kind != briefNone || line != "" {
		t.Errorf("missing brief = (%q,%v), want (\"\",briefNone)", line, kind)
	}
	// stub: the exact generated stub body for the session.
	if err := os.WriteFile(filepath.Join(briefDir, "stub-ws.md"),
		[]byte(briefStubContent("stub-ws")), 0o644); err != nil {
		t.Fatal(err)
	}
	if line, kind := classifyBrief(proj, "stub-ws"); kind != briefStub || line != "" {
		t.Errorf("stub brief = (%q,%v), want (\"\",briefStub)", line, kind)
	}
	// real: operator-authored content.
	if err := os.WriteFile(filepath.Join(briefDir, "real-ws.md"),
		[]byte("# real-ws\n\nShip the polished status board.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if line, kind := classifyBrief(proj, "real-ws"); kind != briefReal || line != "Ship the polished status board." {
		t.Errorf("real brief = (%q,%v), want (\"Ship the polished status board.\",briefReal)", line, kind)
	}
}

// TestBoardBriefCellLabels proves the BRIEF column renders the distinct,
// honest labels for stub vs no-brief vs real (truncated) briefs.
func TestBoardBriefCellLabels(t *testing.T) {
	cases := []struct {
		name string
		row  sessionBoardRow
		want string
	}{
		{"stub", sessionBoardRow{briefKind: briefStub}, "(stub brief)"},
		{"none", sessionBoardRow{briefKind: briefNone}, "(no brief)"},
		{"real", sessionBoardRow{briefKind: briefReal, Brief: "Ship the board."}, "Ship the board."},
		{"real-empty", sessionBoardRow{briefKind: briefReal, Brief: "   "}, "(no brief)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := boardBriefCell(c.row); got != c.want {
				t.Errorf("boardBriefCell(%s) = %q, want %q", c.name, got, c.want)
			}
		})
	}
}

// TestBoardRendersStubAndNoBriefLabels proves end-to-end that a session backed
// by a generated stub shows "(stub brief)" and a session with no brief shows
// "(no brief)" — never the stub placeholder prose passed off as a real brief.
func TestBoardRendersStubAndNoBriefLabels(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	seedAgentRecord(t, base, "stub-ws", "cto", launch.Record{
		Binary: "codex", Handle: "cto", Role: "cto", Session: "stub-ws", AgentPID: 1111,
	})
	seedBoardPresence(t, base, "stub-ws", "cto", "active", boardNow.Add(-10*time.Second))
	seedAgentRecord(t, base, "bare-ws", "qa", launch.Record{
		Binary: "claude", Handle: "qa", Role: "qa", Session: "bare-ws", AgentPID: 2222,
	})
	seedBoardPresence(t, base, "bare-ws", "qa", "active", boardNow.Add(-10*time.Second))

	briefDir := filepath.Join(proj, ".amq-squad", briefsDirName)
	if err := os.MkdirAll(briefDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// stub-ws gets the untouched generated stub; bare-ws gets no brief file.
	if err := os.WriteFile(filepath.Join(briefDir, "stub-ws.md"),
		[]byte(briefStubContent("stub-ws")), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runBoardExec(t, base, proj,
		boardProbe(map[int]bool{1111: true, 2222: true}, map[int]bool{1111: true, 2222: true}), false)
	if err != nil {
		t.Fatalf("board: %v\n%s", err, out)
	}
	if !strings.Contains(out, "(stub brief)") {
		t.Errorf("board should label the generated stub honestly:\n%s", out)
	}
	if !strings.Contains(out, "(no brief)") {
		t.Errorf("board should show (no brief) for a session with no brief file:\n%s", out)
	}
	// The stub placeholder prose must NOT leak into the board as a fake brief.
	if strings.Contains(out, "Use this brief to capture") {
		t.Errorf("board leaked stub placeholder prose as a real brief:\n%s", out)
	}
}

// TestBoardAgentsCellStateAware proves the AGENTS column word follows the
// rolled-up state: running shows "N/N alive", degraded adds the at-risk note,
// and a stopped squad reads "stopped"/"M agents" — never "0/N alive".
func TestBoardAgentsCellStateAware(t *testing.T) {
	cases := []struct {
		name string
		row  sessionBoardRow
		want string
	}{
		{"running", sessionBoardRow{State: boardStateRunning, AgentsAlive: 3, AgentsTotal: 3}, "3/3 alive"},
		{"degraded-atrisk", sessionBoardRow{State: boardStateDegraded, AgentsAlive: 2, AgentsTotal: 3, AtRisk: 1}, "2/3 alive (1 at-risk)"},
		{"stopped-with-agents", sessionBoardRow{State: boardStateStopped, AgentsAlive: 0, AgentsTotal: 3}, "3 agents"},
		{"stopped-one-agent", sessionBoardRow{State: boardStateStopped, AgentsAlive: 0, AgentsTotal: 1}, "1 agent"},
		{"stopped-no-agents", sessionBoardRow{State: boardStateStopped, AgentsAlive: 0, AgentsTotal: 0}, "stopped"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := boardAgentsCell(c.row)
			if got != c.want {
				t.Errorf("boardAgentsCell(%s) = %q, want %q", c.name, got, c.want)
			}
			if c.row.State == boardStateStopped && strings.Contains(got, "alive") {
				t.Errorf("stopped squad %s should not mention 'alive': %q", c.name, got)
			}
		})
	}
}

// TestSortBoardRowsAttentionFirst proves the attention-first order: degraded
// above running above stopped, and within a state most-recent-activity first —
// so a running/degraded squad sorts above a stopped one even when the stopped
// one is more recent on the clock.
func TestSortBoardRowsAttentionFirst(t *testing.T) {
	rows := []sessionBoardRow{
		{Name: "stopped-recent", State: boardStateStopped, LastActivity: boardNow.Add(-1 * time.Minute)},
		{Name: "running-old", State: boardStateRunning, LastActivity: boardNow.Add(-2 * time.Hour)},
		{Name: "degraded-old", State: boardStateDegraded, LastActivity: boardNow.Add(-3 * time.Hour)},
		{Name: "running-new", State: boardStateRunning, LastActivity: boardNow.Add(-10 * time.Minute)},
		{Name: "stopped-old", State: boardStateStopped, LastActivity: boardNow.Add(-5 * time.Hour)},
	}
	sortBoardRows(rows)
	gotOrder := make([]string, len(rows))
	for i, r := range rows {
		gotOrder[i] = r.Name
	}
	want := []string{"degraded-old", "running-new", "running-old", "stopped-recent", "stopped-old"}
	for i := range want {
		if gotOrder[i] != want[i] {
			t.Errorf("sort order = %v, want %v", gotOrder, want)
			break
		}
	}
	// Explicit guard on the headline requirement: a stopped squad — even the
	// most recent one — must NOT outrank a live/degraded squad.
	degradedIdx, stoppedRecentIdx := indexOf(gotOrder, "degraded-old"), indexOf(gotOrder, "stopped-recent")
	runningOldIdx := indexOf(gotOrder, "running-old")
	if degradedIdx > stoppedRecentIdx || runningOldIdx > stoppedRecentIdx {
		t.Errorf("live/degraded squads must sort above stopped: %v", gotOrder)
	}
}

func indexOf(ss []string, target string) int {
	for i, s := range ss {
		if s == target {
			return i
		}
	}
	return -1
}

// TestBoardRendersAttentionFirstOrder proves the rendered table places the live
// session above the stopped one even though the stopped session was seeded with
// a more recent last-activity than the running one.
func TestBoardRendersAttentionFirstOrder(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	// running-ws: alive via PID, but its last-activity is OLDER than the
	// stopped one's recorded last-seen.
	seedAgentRecord(t, base, "running-ws", "cto", launch.Record{
		Binary: "codex", Handle: "cto", Role: "cto", Session: "running-ws", AgentPID: 1111,
	})
	seedBoardPresence(t, base, "running-ws", "cto", "active", boardNow.Add(-3*time.Hour))
	// stopped-ws: dead PID and a STALE presence (>90s old, so no live mailbox),
	// yet its recorded last-seen is MORE RECENT than running-ws's. Genuinely
	// stopped, but newer on the clock — the attention-first sort must still put
	// the live session above it.
	seedAgentRecord(t, base, "stopped-ws", "qa", launch.Record{
		Binary: "claude", Handle: "qa", Role: "qa", Session: "stopped-ws", AgentPID: 2222,
	})
	seedBoardPresence(t, base, "stopped-ws", "qa", "offline", boardNow.Add(-2*time.Minute))

	out, err := runBoardExec(t, base, proj,
		boardProbe(map[int]bool{1111: true, 2222: false}, map[int]bool{1111: true}), false)
	if err != nil {
		t.Fatalf("board: %v\n%s", err, out)
	}
	if ri, si := strings.Index(out, "running-ws"), strings.Index(out, "stopped-ws"); ri == -1 || si == -1 || ri > si {
		t.Errorf("running session must render above the stopped one (running@%d, stopped@%d):\n%s", ri, si, out)
	}
	// Guard the premise: stopped-ws must genuinely roll up to "stopped" (not
	// degraded) for this to be a real running-vs-stopped ordering check.
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "stopped-ws") && !strings.Contains(line, "stopped") {
			t.Errorf("stopped-ws should roll up to stopped:\n%s", line)
		}
	}
}

// TestBoardSummaryLineCounts proves the top summary line reports the correct
// sessions / running / degraded / at-risk counts and always shows at-risk even
// when it is zero.
func TestBoardSummaryLineCounts(t *testing.T) {
	base := seedMultiSessionBoard(t)
	proj := t.TempDir()
	probe := boardProbe(
		map[int]bool{1111: true, 2222: false, 3333: false},
		map[int]bool{1111: true},
	)
	out, err := runBoardExec(t, base, proj, probe, false)
	if err != nil {
		t.Fatalf("board: %v\n%s", err, out)
	}
	// 3 sessions: 1 running, 1 stopped, 1 degraded (the at-risk one). The
	// degraded session has 1 dead-mailbox-live agent -> at-risk total 1.
	want := "amq-squad · 3 sessions · 1 running · 1 degraded · 1 at-risk"
	if !strings.Contains(out, want) {
		t.Errorf("summary line missing/incorrect:\nwant substring: %q\ngot:\n%s", want, out)
	}
}

// TestBoardSummaryLineShowsZeroAtRisk proves the at-risk count is shown even
// when zero (an honest 0 rather than a hidden field), and that a single session
// reads "1 session" not "1 sessions".
func TestBoardSummaryLineShowsZeroAtRisk(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	seedAgentRecord(t, base, "solo-ws", "cto", launch.Record{
		Binary: "codex", Handle: "cto", Role: "cto", Session: "solo-ws", AgentPID: 1111,
	})
	seedBoardPresence(t, base, "solo-ws", "cto", "active", boardNow.Add(-10*time.Second))
	out, err := runBoardExec(t, base, proj,
		boardProbe(map[int]bool{1111: true}, map[int]bool{1111: true}), false)
	if err != nil {
		t.Fatalf("board: %v\n%s", err, out)
	}
	if !strings.Contains(out, "1 session ·") {
		t.Errorf("single session should read '1 session':\n%s", out)
	}
	if !strings.Contains(out, "0 at-risk") {
		t.Errorf("at-risk count should be shown even when zero:\n%s", out)
	}
}

// TestBoardSummaryFoldsNonDefaultRoot proves a non-default base root is folded
// compactly into the summary line, while the default <project>/.agent-mail root
// is NOT called out (no leading debug header either way).
func TestBoardSummaryFoldsNonDefaultRoot(t *testing.T) {
	rows := []sessionBoardRow{{Name: "a", State: boardStateRunning, AgentsAlive: 1, AgentsTotal: 1}}
	// Non-default root -> folded into the summary.
	if got := boardSummaryLine("/tmp/custom-root", rows); !strings.Contains(got, "root: /tmp/custom-root") {
		t.Errorf("non-default root should be folded into summary, got %q", got)
	}
	// Default <project>/.agent-mail root -> not called out.
	if got := boardSummaryLine("/Users/me/proj/.agent-mail", rows); strings.Contains(got, "root:") {
		t.Errorf("default root should not be called out in summary, got %q", got)
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
