package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
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

// TestRunStatusSessionRequiresTeam covers the single-session DETAIL path:
// status --session NAME still hard-requires a configured team, because it
// classifies that team's members. The no-selector BOARD path is the one that
// degrades gracefully (see TestRunStatusBoardNoTeamDegradesGracefully).
func TestRunStatusSessionRequiresTeam(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	_, _, err := captureOutput(t, func() error {
		return runStatus([]string{"--session", "issue-96"})
	})
	if err == nil || !strings.Contains(err.Error(), "no team configured") {
		t.Fatalf("want 'no team configured' error, got %v", err)
	}
}

// TestRunStatusBoardNoTeamDegradesGracefully proves the new front-door
// contract: bare `status` (no --session) routes to the board, which must NOT
// hard-error when there is no team / no sessions / amq is unresolvable. With
// PATH stripped of `amq`, base-root resolution fails and the board renders a
// non-fatal guidance line, returning nil.
func TestRunStatusBoardNoTeamDegradesGracefully(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	t.Setenv("PATH", "")
	stdout, _, err := captureOutput(t, func() error {
		return runStatus(nil)
	})
	if err != nil {
		t.Fatalf("board front-door must not hard-error, got %v", err)
	}
	if !strings.Contains(stdout, "amq-squad:") {
		t.Fatalf("expected a guidance notice on stdout, got:\n%s", stdout)
	}
}

func TestRunStatusProjectTargetsSessionOtherDir(t *testing.T) {
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
		return runStatus([]string{"--project", project, "--session", "issue-99", "--json"})
	})
	if err != nil {
		t.Fatalf("status --project --session: %v\nstderr:\n%s", err, stderr)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, stdout)
	if env.Data.TeamHome != project {
		t.Fatalf("status --project team_home = %q, want %s", env.Data.TeamHome, project)
	}
	if env.Data.Workstream != "issue-99" {
		t.Fatalf("status --project workstream = %q, want issue-99", env.Data.Workstream)
	}
}

func TestRunStatusProjectValidation(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	_, _, err := captureOutput(t, func() error {
		return runStatus([]string{"--project", missing})
	})
	if err == nil || !strings.Contains(err.Error(), "--project") {
		t.Fatalf("status --project missing error = %v, want --project error", err)
	}

	_, _, err = captureOutput(t, func() error {
		return runStatus([]string{"--project", ""})
	})
	if err == nil || !strings.Contains(err.Error(), "--project requires a directory") {
		t.Fatalf("status empty --project error = %v, want directory guidance", err)
	}
}

func TestStatusJSONTopologyFlagsSplitSessionForVisibleMode(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-238"},
			{Role: "fullstack", Binary: "codex", Handle: "fullstack", Session: "issue-238"},
		},
		Orchestrated: true,
		Lead:         "cto",
	})
	seedAgentRecord(t, base, "issue-238", "cto", launch.Record{
		Binary: "codex", Handle: "cto", Role: "cto", Session: "issue-238", AgentPID: 1001,
		Tmux: &launch.TmuxInfo{
			Session:  "operator-visible",
			WindowID: "@1",
			PaneID:   "%1",
			Target:   "new-window",
		},
	})
	seedAgentRecord(t, base, "issue-238", "fullstack", launch.Record{
		Binary: "codex", Handle: "fullstack", Role: "fullstack", Session: "issue-238", AgentPID: 1002,
		Tmux: &launch.TmuxInfo{
			Session:  "hidden-workers",
			WindowID: "@2",
			PaneID:   "%2",
			Target:   "new-session",
		},
	})
	swapStatusPaneLister(t, []tmuxpane.TmuxPane{{PaneID: "%1"}, {PaneID: "%2"}}, nil)

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-238",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(map[int]bool{1001: true, 1002: true}, map[int]bool{1001: true, 1002: true}, time.Now()),
	})
	if err != nil {
		t.Fatalf("status --json: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	if env.Data.Topology == nil {
		t.Fatal("topology missing from status JSON")
	}
	topology := env.Data.Topology
	if topology.Mode != "split-session" || !topology.VisibleProblem || topology.ProblemFor != visibilitySiblingTabs {
		t.Fatalf("topology = %+v, want split-session visible problem for sibling-tabs", topology)
	}
	if strings.Join(topology.TmuxSessions, ",") != "hidden-workers,operator-visible" {
		t.Fatalf("tmux sessions = %v, want sorted split sessions", topology.TmuxSessions)
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

func TestExecuteStatusIsolatesForeignProfileLaunchRecord(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := t.TempDir()
	for _, profile := range []string{"product", "release"} {
		if err := team.WriteProfile(dir, profile, team.Team{
			Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "main"}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	seedAgentRecord(t, base, "main", "cto", launch.Record{
		Binary:      "codex",
		Handle:      "cto",
		Role:        "cto",
		Session:     "main",
		AgentPID:    5555,
		TeamProfile: "product",
		Tmux:        &launch.TmuxInfo{Session: "tmux-product", PaneID: "%5"},
	})
	swapStatusPaneLister(t, []tmuxpane.TmuxPane{{PaneID: "%5"}}, nil)

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		Profile:          "release",
		RequestedSession: "main",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(map[int]bool{5555: true}, map[int]bool{5555: true}, time.Now()),
	})
	if err != nil {
		t.Fatalf("status --json: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	got := env.Data.Records[0]
	if got.Status != statusStateMissing || got.RecordState != "missing" || got.Tmux != nil {
		t.Fatalf("foreign-profile launch record should be isolated from release profile, got %+v", got)
	}
	if got.Root == filepath.Join(base, "main") {
		t.Fatalf("release profile should not inspect legacy session root %s", got.Root)
	}
}

func TestExecuteStatusJSONIncludesSpawnMetadata(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "issue-96", SpawnOrigin: "profile", SpawnDepth: 1},
		},
	})
	seedAgentRecord(t, base, "issue-96", "qa", launch.Record{
		Binary: "codex", Handle: "qa", AgentPID: 5555, SpawnOrigin: "cto", SpawnDepth: 1,
	})
	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		Probe:            statusProbe(map[int]bool{5555: true}, map[int]bool{5555: true}, time.Now()),
		JSON:             true,
	})
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	if len(env.Data.Records) != 1 {
		t.Fatalf("records = %d, want 1", len(env.Data.Records))
	}
	got := env.Data.Records[0]
	if got.SpawnOrigin != "cto" || got.SpawnDepth != 1 {
		t.Fatalf("spawn metadata = origin %q depth %d, want cto/1", got.SpawnOrigin, got.SpawnDepth)
	}
	if got.RecordState != "launched" {
		t.Fatalf("record_state = %q, want launched", got.RecordState)
	}
	if !env.Data.Capabilities.AutonomousGuardrails {
		t.Fatalf("status capabilities must advertise autonomous guardrails")
	}
	if env.Data.GoalBinding.Mode != "amq_task_brief" || env.Data.GoalBinding.NativeGoal {
		t.Fatalf("goal binding = %+v", env.Data.GoalBinding)
	}
	if env.Data.GoalBinding.BriefPath == "" || env.Data.GoalBinding.TasksPath == "" {
		t.Fatalf("status goal binding should expose brief/tasks paths: %+v", env.Data.GoalBinding)
	}
}

func TestExecuteStatusJSONReportsNativeGoalForLiveLeadRecord(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "issue-96"},
		},
		Orchestrated: true,
		Lead:         "cto",
	})
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary: "codex", Handle: "cto", Role: "cto", AgentPID: 4242,
		GoalBinding: &launch.GoalBinding{
			Mode:       "native_goal",
			NativeGoal: true,
			Source:     "launch-argv",
			Command:    `/goal --goal "ship"`,
		},
	})
	seedAgentRecord(t, base, "issue-96", "qa", launch.Record{
		Binary: "codex", Handle: "qa", Role: "qa", AgentPID: 3131,
	})
	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		Probe:            statusProbe(map[int]bool{4242: true, 3131: true}, map[int]bool{4242: true, 3131: true}, time.Now()),
		JSON:             true,
	})
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	if env.Data.GoalBinding.Mode != "native_goal" || !env.Data.GoalBinding.NativeGoal || !env.Data.GoalBinding.Verified {
		t.Fatalf("goal binding = %+v", env.Data.GoalBinding)
	}
	if env.Data.GoalBinding.Source != "launch-record" || env.Data.GoalBinding.NativeSource != "launch-argv" {
		t.Fatalf("goal binding source = %+v", env.Data.GoalBinding)
	}
	if !strings.Contains(env.Data.GoalBinding.Command, "/goal --goal") {
		t.Fatalf("goal binding command = %+v", env.Data.GoalBinding)
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

func TestExecuteStatusWakeLockOnlyWakeLive(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	// Seed a wake.lock with a live PID but no launch record. Status must not
	// flatten this into stale; the wake helper is usable enough for resume and
	// AMQ delivery.
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
	if !strings.Contains(out, "wake-live") {
		t.Errorf("wake-only should report wake-live:\n%s", out)
	}
	if strings.Contains(out, "\tstale\t") || strings.Contains(out, " stale ") {
		t.Errorf("verified wake must not render as stale:\n%s", out)
	}

	jsonOut, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(map[int]bool{4321: true}, map[int]bool{4321: true}, time.Now()),
	})
	if err != nil {
		t.Fatalf("status json: %v\n%s", err, jsonOut)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, jsonOut)
	if len(env.Data.Records) != 1 {
		t.Fatalf("records = %+v, want one", env.Data.Records)
	}
	row := env.Data.Records[0]
	if row.Status != statusStateWakeLive || !row.Signals.WakeAlive {
		t.Fatalf("wake-live json row = %+v, want status wake-live with wake_alive", row)
	}
}

func TestExecuteStatusWakeLiveWithRelativeAMQRootFromOtherCWD(t *testing.T) {
	setupFakeAMQRelativeSessionRoots(t)
	project := t.TempDir()
	other := t.TempDir()
	chdir(t, other)
	if err := team.Write(project, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	}); err != nil {
		t.Fatal(err)
	}
	agentDir := filepath.Join(project, ".agent-mail", "issue-96", "agents", "cto")
	writeWakeLock(t, agentDir, wakeLockFile{PID: 4321, Root: ".agent-mail/issue-96", Started: time.Now()})

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       project,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		JSON:             true,
		Probe: duplicateLaunchProbe{
			PIDAlive: func(pid int) bool { return pid == 4321 },
			ProcessMatch: func(pid int, predicate func(args string) bool) bool {
				return pid == 4321 && predicate("amq wake --me cto --root .agent-mail/issue-96")
			},
			Now: time.Now,
		},
	})
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	row := env.Data.Records[0]
	if row.Status != statusStateWakeLive || !row.Signals.WakeAlive {
		t.Fatalf("relative-root status row = %+v, want wake-live", row)
	}
	if row.AgentDir != agentDir {
		t.Fatalf("agent_dir = %q, want %q", row.AgentDir, agentDir)
	}
}

func TestExecuteStatusJSON(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "lead-handle", Session: "issue-96"},
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "issue-96"},
		},
		Orchestrated: true,
		Lead:         "cto",
	})
	seedAgentRecord(t, base, "issue-96", "lead-handle", launch.Record{
		Binary: "codex", Handle: "lead-handle", AgentPID: 100,
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
	if !env.Data.Orchestrated || env.Data.Lead != "cto" || env.Data.LeadHandle != "lead-handle" {
		t.Fatalf("orchestration fields = orchestrated:%v lead:%q lead_handle:%q, want true/cto/lead-handle", env.Data.Orchestrated, env.Data.Lead, env.Data.LeadHandle)
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

func TestExecuteStatusJSONOmitsOrchestrationForFlatTeams(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(nil, nil, time.Now()),
	})
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	for _, absent := range []string{`"orchestrated"`, `"lead"`, `"lead_handle"`} {
		if strings.Contains(out, absent) {
			t.Fatalf("flat status JSON should omit %s:\n%s", absent, out)
		}
	}
}

func TestExecuteStatusJSONIncludesAutonomousPolicy(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
			{Role: "fullstack", Binary: "codex", Handle: "fullstack", Session: "issue-96"},
		},
		Orchestrated: true,
		Lead:         "cto",
		Composition:  team.CompositionAutonomous,
		Autonomous: &team.AutonomousPolicy{
			MaxActiveAgents: 4,
			MaxTotalSpawns:  3,
			AllowedRoles:    []string{"fullstack"},
			BudgetTurns:     20,
			State:           team.AutonomousState{TotalSpawns: 1, BudgetTurnsUsed: 5},
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
	if !env.Data.Autonomous.Enabled || env.Data.Autonomous.Composition != team.CompositionAutonomous {
		t.Fatalf("autonomous status missing/enabled false: %+v", env.Data.Autonomous)
	}
	if env.Data.Autonomous.MaxActiveAgents != 4 || env.Data.Autonomous.TotalSpawns != 1 || env.Data.Autonomous.BudgetTurnsLeft != 15 {
		t.Fatalf("autonomous counters mismatch: %+v", env.Data.Autonomous)
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
