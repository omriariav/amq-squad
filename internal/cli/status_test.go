package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/activity"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/state"
	taskstore "github.com/omriariav/amq-squad/v2/internal/task"
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

func actionsByKind(actions []runtimeActionJSON) map[string]runtimeActionJSON {
	out := map[string]runtimeActionJSON{}
	for _, action := range actions {
		out[action.Kind] = action
	}
	return out
}

func assertNoLegacyNamespaceConflictReason(t *testing.T, actions []runtimeActionJSON) {
	t.Helper()
	for _, action := range actions {
		if strings.Contains(action.Reason, "legacy/default session root") {
			t.Fatalf("action %s unexpectedly carries namespace-conflict reason: %+v", action.Kind, action)
		}
	}
}

func findStatusRecord(records []statusRecord, handle string) *statusRecord {
	for i := range records {
		if records[i].Handle == handle {
			return &records[i]
		}
	}
	return nil
}

func readinessGatesByCode(gates []releaseReadinessGateData) map[string]releaseReadinessGateData {
	out := map[string]releaseReadinessGateData{}
	for _, gate := range gates {
		out[gate.ID] = gate
	}
	return out
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

func TestExecuteStatusJSONNamedProfileDisablesActionsOnLegacySessionRootConflict(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	dir := t.TempDir()
	seedProfile(t, dir, "release", team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "main"},
		},
		Orchestrated: true,
		Lead:         "cto",
	})
	legacyAgentDir := filepath.Join(dir, ".agent-mail", "main", "agents", "cto")
	if err := os.MkdirAll(legacyAgentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyAgentDir, "inbox"), []byte("legacy durable state\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		Profile:          "release",
		RequestedSession: "main",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(nil, nil, time.Now()),
	})
	if err != nil {
		t.Fatalf("status --json: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	if env.Data.NamespaceConflict == nil || env.Data.NamespaceConflict.Kind != "legacy_session_root" {
		t.Fatalf("namespace conflict missing: %+v", env.Data.NamespaceConflict)
	}
	for _, action := range env.Data.Actions {
		switch action.Kind {
		case "resume_current_window", "resume_new_session", "stop", "stop_close_panes", "attach_control":
			if action.Available || !strings.Contains(action.Reason, "legacy/default session root") {
				t.Fatalf("action %s should be unavailable with conflict reason: %+v", action.Kind, action)
			}
		case "status", "task_list":
			if !action.Available {
				t.Fatalf("read-only action %s should remain available: %+v", action.Kind, action)
			}
		}
	}
	if len(env.Data.Records) != 1 {
		t.Fatalf("records = %d", len(env.Data.Records))
	}
	for _, action := range env.Data.Records[0].Actions {
		if (action.Kind == "resume" || action.Kind == "focus" || action.Kind == "send") &&
			(action.Available || !strings.Contains(action.Reason, "legacy/default session root")) {
			t.Fatalf("member action %s should be unavailable with conflict reason: %+v", action.Kind, action)
		}
	}
}

func TestExecuteStatusJSONNamedProfileAllowsPresenceOnlyLegacyRoot(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	dir := t.TempDir()
	seedProfile(t, dir, "release", team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "main"},
		},
		Orchestrated: true,
		Lead:         "cto",
	})
	legacyAgentDir := filepath.Join(dir, ".agent-mail", "main", "agents", "cto")
	if err := os.MkdirAll(legacyAgentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyAgentDir, "presence.json"), []byte(`{"status":"active"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		Profile:          "release",
		RequestedSession: "main",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(nil, nil, time.Now()),
	})
	if err != nil {
		t.Fatalf("status --json: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	if env.Data.NamespaceConflict != nil {
		t.Fatalf("presence-only legacy root should not block named profile actions: %+v", env.Data.NamespaceConflict)
	}
}

func TestRunStatusDefaultProfileWarnsWhenNamedProfileHasSameSession(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	dir := t.TempDir()
	chdir(t, dir)
	seedProfile(t, dir, team.DefaultProfile, team.Team{
		Workstream: "main",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "main"},
		},
	})
	seedProfile(t, dir, "release", team.Team{
		Workstream: "main",
		Members: []team.Member{
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "main"},
		},
	})
	namedRoot := filepath.Join(dir, ".agent-mail", "release", "main")
	if err := os.MkdirAll(filepath.Join(namedRoot, "agents", "qa"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(namedRoot, "agents", "qa", "inbox.md"), []byte("named durable state\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := captureOutput(t, func() error {
		return runStatus([]string{"--session", "main"})
	})
	if err != nil {
		t.Fatalf("status --session main: %v\n%s", err, stdout)
	}
	for _, want := range []string{"warning: showing default-profile data", "--profile release", "--session main"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("status warning missing %q:\n%s", want, stdout)
		}
	}

	jsonOut, _, err := captureOutput(t, func() error {
		return runStatus([]string{"--session", "main", "--json"})
	})
	if err != nil {
		t.Fatalf("status --json: %v\n%s", err, jsonOut)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, jsonOut)
	if len(env.Data.Warnings) != 1 {
		t.Fatalf("warnings = %+v, want one shadow warning", env.Data.Warnings)
	}
	got := env.Data.Warnings[0]
	if got.Kind != "default_profile_shadowed" || got.Session != "main" ||
		!strings.Contains(got.Detail, "showing default-profile data") ||
		!strings.Contains(got.SuggestedCommand, "--profile release") ||
		len(got.Conflicts) != 1 || got.Conflicts[0].Profile != "release" {
		t.Fatalf("bad status warning: %+v", got)
	}
}

func TestExecuteStatusJSONSameProfileSessionAllowsNestedNamedNamespaceOnly(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	dir := t.TempDir()
	seedProfile(t, dir, "review", team.Team{
		Workstream: "review",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "review"},
		},
		Orchestrated: true,
		Lead:         "cto",
	})
	namedAgentDir := filepath.Join(dir, ".agent-mail", "review", "review", "agents", "cto")
	if err := os.MkdirAll(namedAgentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(namedAgentDir, "inbox"), []byte("named durable state\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		Profile:          "review",
		RequestedSession: "review",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(nil, nil, time.Now()),
	})
	if err != nil {
		t.Fatalf("status --json: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	if env.Data.NamespaceConflict != nil {
		t.Fatalf("nested named namespace should not be treated as legacy conflict: %+v", env.Data.NamespaceConflict)
	}
	assertNoLegacyNamespaceConflictReason(t, env.Data.Actions)
	if len(env.Data.Records) != 1 {
		t.Fatalf("records = %d, want 1", len(env.Data.Records))
	}
	assertNoLegacyNamespaceConflictReason(t, env.Data.Records[0].Actions)
}

func TestStatusWarnsDispatchedPendingTask(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "issue-96"},
		},
		Orchestrated: true,
		Lead:         "cto",
	})
	now := time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC)
	created, err := taskstore.AddForProfile(dir, team.DefaultProfile, "issue-96", taskstore.AddInput{
		Title:    "pending dispatch",
		AssignTo: "qa",
	}, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := taskstore.LinkDispatchForProfile(dir, team.DefaultProfile, "issue-96", created.ID, taskstore.Dispatch{
		Sender:    "cto",
		Assignee:  "qa",
		Thread:    "p2p/cto__qa",
		Kind:      "todo",
		Subject:   "Validate",
		MessageID: "msg-pending",
	}, now.Add(-30*time.Minute)); err != nil {
		t.Fatal(err)
	}

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(nil, nil, now),
	})
	if err != nil {
		t.Fatalf("status --json: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	w := findStatusWarning(env.Data.Warnings, "task_dispatched_pending")
	if w == nil ||
		!strings.Contains(w.Detail, "task t1 is still pending after dispatch to qa") ||
		!strings.Contains(w.Detail, "message msg-pending") ||
		!strings.Contains(w.SuggestedCommand, "task claim t1 --me qa") {
		t.Fatalf("missing dispatched-pending task warning: %+v", env.Data.Warnings)
	}
}

func TestStatusWarnsWorkerCompletionReportForInProgressTask(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "issue-96"},
		},
		Orchestrated: true,
		Lead:         "cto",
	})
	now := time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC)
	created, err := taskstore.AddForProfile(dir, team.DefaultProfile, "issue-96", taskstore.AddInput{
		Title:    "review result",
		AssignTo: "qa",
	}, now.Add(-2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := taskstore.LinkDispatchForProfile(dir, team.DefaultProfile, "issue-96", created.ID, taskstore.Dispatch{
		Sender:    "cto",
		Assignee:  "qa",
		Kind:      "todo",
		Subject:   "Validate",
		MessageID: "msg-dispatch",
	}, now.Add(-90*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := taskstore.ClaimForProfile(dir, team.DefaultProfile, "issue-96", created.ID, "qa", now.Add(-80*time.Minute)); err != nil {
		t.Fatal(err)
	}
	ctoDir := filepath.Join(dir, ".agent-mail", "issue-96", "agents", "cto")
	seedThreadMessage(t, ctoDir, "new", "msg-done", "qa", []string{"cto"},
		"p2p/qa__cto", "DONE t1", string(state.KindStatus), now.Add(-5*time.Minute))

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(nil, nil, now),
	})
	if err != nil {
		t.Fatalf("status --json: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	w := findStatusWarning(env.Data.Warnings, "task_report_pending_completion")
	if w == nil ||
		!strings.Contains(w.Detail, "task t1 is still in_progress after qa reported completion") ||
		!strings.Contains(w.Detail, "message msg-done") ||
		!strings.Contains(w.SuggestedCommand, "task done t1 --me qa") ||
		!strings.Contains(w.SuggestedCommand, "accepted msg-done") {
		t.Fatalf("missing completion-report task warning: %+v", env.Data.Warnings)
	}
}

func TestStatusWarnsAgedOperatorGate(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	project, base, _ := seedNotifyProject(t, team.DefaultOperator())
	seedNotifyLaunch(t, project, base, "s", "cto")
	seedNotifyMessage(t, base, "s", team.DefaultOperatorHandle, "new", notifyMsg{
		ID:      "gate-1",
		From:    "cto",
		To:      team.DefaultOperatorHandle,
		Thread:  "gate/release",
		Subject: "APPROVAL: release",
		Kind:    string(state.KindQuestion),
		Created: notifyNow.Add(-125 * time.Minute),
	})

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       project,
		RequestedSession: "s",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(nil, nil, notifyNow),
	})
	if err != nil {
		t.Fatalf("status --json: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	w := findStatusWarning(env.Data.Warnings, "operator_gate_strong_warning")
	if w == nil ||
		!strings.Contains(w.Detail, "gate/release") ||
		!strings.Contains(w.Detail, "strong-warning") ||
		!strings.Contains(w.Detail, "poll-required/no-wake") ||
		!strings.Contains(w.SuggestedCommand, "amq-squad thread") ||
		!strings.Contains(w.SuggestedCommand, "gate/release") {
		t.Fatalf("missing aged operator-gate warning: %+v", env.Data.Warnings)
	}
}

func TestStatusJSONIncludesHeartbeatActivity(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "issue-96"},
		},
	})
	now := time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC)
	agentDir := seedAgentRecord(t, base, "issue-96", "qa", launch.Record{
		Binary:  "codex",
		Handle:  "qa",
		Role:    "qa",
		Session: "issue-96",
		CWD:     dir,
	})
	if err := activity.Write(agentDir, activity.File{
		Handle:    "qa",
		TaskID:    "t11",
		Phase:     "testing",
		Detail:    "make ci",
		WrittenAt: now.Add(-30 * time.Second),
	}); err != nil {
		t.Fatalf("seed activity: %v", err)
	}

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(nil, nil, now),
	})
	if err != nil {
		t.Fatalf("status --json: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	rec := findStatusRecord(env.Data.Records, "qa")
	if rec == nil || rec.Activity == nil {
		t.Fatalf("qa activity missing: %+v", env.Data.Records)
	}
	if rec.Activity.Source != activity.SourceHeartbeat || rec.Activity.Quality != activity.StateFresh ||
		rec.Activity.TaskID != "t11" || rec.Activity.Phase != "testing" || rec.Activity.Detail != "make ci" {
		t.Fatalf("status activity = %+v", rec.Activity)
	}
}

func TestStatusJSONToleratesMalformedActivity(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "issue-96"},
		},
	})
	now := time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC)
	agentDir := seedAgentRecord(t, base, "issue-96", "qa", launch.Record{
		Binary:  "codex",
		Handle:  "qa",
		Role:    "qa",
		Session: "issue-96",
		CWD:     dir,
	})
	if err := os.WriteFile(filepath.Join(agentDir, activity.Filename), []byte("{not-json"), 0o600); err != nil {
		t.Fatalf("seed malformed activity: %v", err)
	}

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(nil, nil, now),
	})
	if err != nil {
		t.Fatalf("status --json should tolerate malformed activity: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	rec := findStatusRecord(env.Data.Records, "qa")
	if rec == nil || rec.Activity == nil {
		t.Fatalf("qa malformed activity should degrade to unknown: %+v", env.Data.Records)
	}
	if rec.Activity.Quality != activity.StateUnknown || rec.Activity.Source != activity.SourceUnknown ||
		!strings.Contains(rec.Activity.Detail, "unreadable") {
		t.Fatalf("malformed activity = %+v", rec.Activity)
	}
}

func TestStatusJSONUsesTaskStoreActivityFallback(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "issue-96"},
		},
	})
	now := time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC)
	created, err := taskstore.AddForProfile(dir, team.DefaultProfile, "issue-96", taskstore.AddInput{
		Title:    "review fix",
		AssignTo: "qa",
	}, now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := taskstore.ClaimForProfile(dir, team.DefaultProfile, "issue-96", created.ID, "qa", now.Add(-30*time.Second)); err != nil {
		t.Fatal(err)
	}

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(nil, nil, now),
	})
	if err != nil {
		t.Fatalf("status --json: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	rec := findStatusRecord(env.Data.Records, "qa")
	if rec == nil || rec.Activity == nil {
		t.Fatalf("qa task-store activity missing: %+v", env.Data.Records)
	}
	if rec.Activity.Source != activity.SourceTaskStore || rec.Activity.Quality != activity.StateUnknown ||
		rec.Activity.TaskID != "t1" || rec.Activity.Phase != "task_in_progress" ||
		rec.Activity.Detail != "review fix" {
		t.Fatalf("task-store activity = %+v", rec.Activity)
	}
}

func findStatusWarning(warnings []statusWarning, kind string) *statusWarning {
	for i := range warnings {
		if warnings[i].Kind == kind {
			return &warnings[i]
		}
	}
	return nil
}

func swapStatusLocalInputDetector(t *testing.T, fn func(string) (tmuxpane.LocalInputBlocker, bool)) {
	t.Helper()
	prev := statusLocalInputDetector
	statusLocalInputDetector = fn
	t.Cleanup(func() { statusLocalInputDetector = prev })
}

func TestStatusJSONSurfacesManagedChildLocalInputBlocker(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-320"},
			{Role: "qa", Binary: "claude", Handle: "qa", Session: "issue-320"},
		},
		Orchestrated: true,
		Lead:         "cto",
	})
	seedAgentRecord(t, base, "issue-320", "cto", launch.Record{
		CWD:      dir,
		Binary:   "codex",
		Handle:   "cto",
		Role:     "cto",
		Session:  "issue-320",
		AgentPID: 1001,
		Tmux:     &launch.TmuxInfo{Session: "squad", WindowID: "@1", PaneID: "%1", Target: "new-window"},
	})
	seedAgentRecord(t, base, "issue-320", "qa", launch.Record{
		CWD:      dir,
		Binary:   "claude",
		Handle:   "qa",
		Role:     "qa",
		Session:  "issue-320",
		AgentPID: 1002,
		Tmux:     &launch.TmuxInfo{Session: "squad", WindowID: "@2", PaneID: "%2", Target: "new-window"},
	})
	swapStatusPaneLister(t, []tmuxpane.TmuxPane{{PaneID: "%1"}, {PaneID: "%2"}}, nil)
	var calls []string
	swapStatusLocalInputDetector(t, func(paneID string) (tmuxpane.LocalInputBlocker, bool) {
		calls = append(calls, paneID)
		if paneID != "%2" {
			return tmuxpane.LocalInputBlocker{}, false
		}
		return tmuxpane.LocalInputBlocker{
			Kind:        "approval_prompt",
			Summary:     "Permission rule Bash(rm -rf *) requires confirmation",
			Destructive: true,
			Recovery:    "operator decision required, or ask the worker to use a non-destructive alternative before proceeding",
		}, true
	})

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-320",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(map[int]bool{1001: true, 1002: true}, map[int]bool{1001: true, 1002: true}, time.Now()),
	})
	if err != nil {
		t.Fatalf("status --json: %v\n%s", err, out)
	}
	if strings.Join(calls, ",") != "%2" {
		t.Fatalf("local input detector calls = %v, want only managed child pane %%2", calls)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	qa := findStatusRecord(env.Data.Records, "qa")
	if qa == nil || qa.LocalInput == nil {
		t.Fatalf("qa local_input missing: %+v", env.Data.Records)
	}
	if qa.LocalInput.Status != "blocked" || qa.LocalInput.Kind != "approval_prompt" ||
		qa.LocalInput.PaneID != "%2" || !qa.LocalInput.Destructive ||
		qa.LocalInput.Source != "tmux-pane-tail" {
		t.Fatalf("qa local_input = %+v", qa.LocalInput)
	}
	if cto := findStatusRecord(env.Data.Records, "cto"); cto == nil || cto.LocalInput != nil {
		t.Fatalf("lead row must not get local_input: %+v", cto)
	}
	warning := findStatusWarning(env.Data.Warnings, "local_input_blocked")
	if warning == nil {
		t.Fatalf("local_input_blocked warning missing: %+v", env.Data.Warnings)
	}
	for _, want := range []string{"role qa", "pane %2", "local prompt waits", "AMQ may stay silent", "non-destructive alternative"} {
		if !strings.Contains(warning.Detail, want) {
			t.Fatalf("warning detail missing %q: %q", want, warning.Detail)
		}
	}
	if !strings.Contains(warning.SuggestedCommand, "amq-squad focus") || !strings.Contains(warning.SuggestedCommand, "--role qa") {
		t.Fatalf("warning suggested command should focus the blocked child: %+v", warning)
	}
	for _, forbidden := range []string{"auto-approve", "--force"} {
		if strings.Contains(strings.ToLower(qa.LocalInput.Recovery), forbidden) ||
			strings.Contains(strings.ToLower(warning.Detail), forbidden) ||
			strings.Contains(strings.ToLower(warning.SuggestedCommand), forbidden) {
			t.Fatalf("local input recovery surfaces unsafe suggestion %q: local=%q warning=%+v", forbidden, qa.LocalInput.Recovery, warning)
		}
	}
}

func TestStatusLocalInputSkipsExternalUnmanagedAndDeadPanes(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-320"},
			{Role: "qa", Binary: "claude", Handle: "qa", Session: "issue-320"},
			{Role: "docs", Binary: "codex", Handle: "docs", Session: "issue-320"},
		},
		Orchestrated: true,
		Lead:         "cto",
	})
	seedAgentRecord(t, base, "issue-320", "cto", launch.Record{
		CWD:      dir,
		Binary:   "codex",
		Handle:   "cto",
		Role:     "cto",
		Session:  "issue-320",
		External: true,
		Tmux:     &launch.TmuxInfo{Session: "operator", WindowID: "@1", PaneID: "%1", Target: "external"},
	})
	seedAgentRecord(t, base, "issue-320", "qa", launch.Record{
		CWD:      dir,
		Binary:   "claude",
		Handle:   "qa",
		Role:     "qa",
		Session:  "issue-320",
		AgentPID: 1002,
		Tmux:     &launch.TmuxInfo{Session: "adopted", WindowID: "@2", PaneID: "%2"},
	})
	seedAgentRecord(t, base, "issue-320", "docs", launch.Record{
		CWD:      dir,
		Binary:   "codex",
		Handle:   "docs",
		Role:     "docs",
		Session:  "issue-320",
		AgentPID: 1003,
		Tmux:     &launch.TmuxInfo{Session: "squad", WindowID: "@3", PaneID: "%3", Target: "new-window"},
	})
	swapStatusPaneLister(t, []tmuxpane.TmuxPane{{PaneID: "%1"}, {PaneID: "%2"}}, nil)
	var calls []string
	swapStatusLocalInputDetector(t, func(paneID string) (tmuxpane.LocalInputBlocker, bool) {
		calls = append(calls, paneID)
		return tmuxpane.LocalInputBlocker{
			Kind:     "approval_prompt",
			Summary:  "Do you want to allow this command?",
			Recovery: "operator decision required",
		}, true
	})

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-320",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(map[int]bool{1002: true, 1003: false}, map[int]bool{1002: true, 1003: false}, time.Now()),
	})
	if err != nil {
		t.Fatalf("status --json: %v\n%s", err, out)
	}
	if len(calls) != 0 {
		t.Fatalf("detector should skip external lead, unmanaged/adopted pane, and dead pane; calls=%v", calls)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	for _, row := range env.Data.Records {
		if row.LocalInput != nil {
			t.Fatalf("no row should carry local_input: %+v", row)
		}
	}
	if warning := findStatusWarning(env.Data.Warnings, "local_input_blocked"); warning != nil {
		t.Fatalf("no local_input_blocked warning expected: %+v", warning)
	}
}

func TestExecuteStatusJSONSameProfileSessionKeepsLegacyAgentDurableConflict(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	dir := t.TempDir()
	seedProfile(t, dir, "review", team.Team{
		Workstream: "review",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "review"},
		},
		Orchestrated: true,
		Lead:         "cto",
	})
	namedAgentDir := filepath.Join(dir, ".agent-mail", "review", "review", "agents", "cto")
	if err := os.MkdirAll(namedAgentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(namedAgentDir, "presence.json"), []byte(`{"status":"active"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	legacyAgentDir := filepath.Join(dir, ".agent-mail", "review", "agents", "cto")
	if err := os.MkdirAll(legacyAgentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyAgentDir, "inbox"), []byte("legacy durable state\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		Profile:          "review",
		RequestedSession: "review",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(nil, nil, time.Now()),
	})
	if err != nil {
		t.Fatalf("status --json: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	if env.Data.NamespaceConflict == nil || env.Data.NamespaceConflict.Kind != "legacy_session_root" {
		t.Fatalf("legacy durable state should still conflict: %+v", env.Data.NamespaceConflict)
	}
}

func TestExecuteStatusJSONSameProfileSessionKeepsLegacyAgentsHandleDurableConflict(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	dir := t.TempDir()
	seedProfile(t, dir, "review", team.Team{
		Workstream: "review",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "review"},
		},
		Orchestrated: true,
		Lead:         "cto",
	})
	namedAgentDir := filepath.Join(dir, ".agent-mail", "review", "review", "agents", "cto")
	if err := os.MkdirAll(namedAgentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(namedAgentDir, "presence.json"), []byte(`{"status":"active"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	legacyAgentDir := filepath.Join(dir, ".agent-mail", "review", "agents", "agents")
	if err := os.MkdirAll(legacyAgentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyAgentDir, "inbox"), []byte("legacy durable state\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		Profile:          "review",
		RequestedSession: "review",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(nil, nil, time.Now()),
	})
	if err != nil {
		t.Fatalf("status --json: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	if env.Data.NamespaceConflict == nil || env.Data.NamespaceConflict.Kind != "legacy_session_root" {
		t.Fatalf("legacy agent handle named agents should still conflict: %+v", env.Data.NamespaceConflict)
	}
}

func TestExecuteStatusJSONSameProfileSessionAllowsSiblingNamedNamespaces(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	dir := t.TempDir()
	seedProfile(t, dir, "review", team.Team{
		Workstream: "review",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "review"},
		},
		Orchestrated: true,
		Lead:         "cto",
	})
	for _, session := range []string{"review", "other"} {
		namedAgentDir := filepath.Join(dir, ".agent-mail", "review", session, "agents", "cto")
		if err := os.MkdirAll(namedAgentDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(namedAgentDir, "inbox"), []byte("named durable state\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		Profile:          "review",
		RequestedSession: "review",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(nil, nil, time.Now()),
	})
	if err != nil {
		t.Fatalf("status --json: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	if env.Data.NamespaceConflict != nil {
		t.Fatalf("sibling named namespaces should not be treated as legacy conflict: %+v", env.Data.NamespaceConflict)
	}
	assertNoLegacyNamespaceConflictReason(t, env.Data.Actions)
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

func TestExecuteStatusJSONReportsBlockedNativeGoalForLiveLeadRecord(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-274"},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "issue-274"},
		},
		Orchestrated: true,
		Lead:         "cto",
	})
	seedAgentRecord(t, base, "issue-274", "cto", launch.Record{
		Binary: "codex", Handle: "cto", Role: "cto", AgentPID: 4242,
		GoalBinding: &launch.GoalBinding{
			Mode:       "native_goal_blocked",
			NativeGoal: true,
			Source:     "goal-runtime",
			Command:    `/goal --goal "ship"`,
			Detail:     "Goal blocked (/goal resume)",
		},
	})
	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-274",
		ExplicitSession:  true,
		Probe:            statusProbe(map[int]bool{4242: true}, map[int]bool{4242: true}, time.Now()),
		JSON:             true,
	})
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	if env.Data.GoalBinding.Mode != "native_goal_blocked" || !env.Data.GoalBinding.NativeGoal || !env.Data.GoalBinding.Verified {
		t.Fatalf("goal binding = %+v, want blocked native goal", env.Data.GoalBinding)
	}
	if env.Data.Operator.Poll == nil || env.Data.Operator.Poll.OpenBlockers != 1 {
		t.Fatalf("operator poll = %+v, want open_blockers=1", env.Data.Operator.Poll)
	}
	if env.Data.Execution.GoalBinding != "native_goal_blocked" {
		t.Fatalf("execution goal binding = %q, want native_goal_blocked", env.Data.Execution.GoalBinding)
	}
}

func TestExecuteStatusJSONReportsMissingNativeGoalForLiveProjectLead(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-247"},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "issue-247"},
		},
		Orchestrated: true,
		Lead:         "cto",
	})
	seedAgentRecord(t, base, "issue-247", "cto", launch.Record{
		Binary: "codex", Handle: "cto", Role: "cto", AgentPID: 4242,
	})
	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-247",
		ExplicitSession:  true,
		Probe:            statusProbe(map[int]bool{4242: true}, map[int]bool{4242: true}, time.Now()),
		JSON:             true,
		RuntimeVersion:   "2.10.0",
	})
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	if env.Data.GoalBinding.Mode != "native_goal_missing" || env.Data.GoalBinding.NativeGoal || env.Data.GoalBinding.Verified {
		t.Fatalf("goal binding = %+v, want missing native goal", env.Data.GoalBinding)
	}
	if env.Data.GoalBinding.NativeSource != "missing" || !strings.Contains(env.Data.GoalBinding.Detail, "without launch-record evidence") {
		t.Fatalf("goal binding detail = %+v", env.Data.GoalBinding)
	}
	if env.Data.Execution.GoalBinding != "native_goal_missing" || !env.Data.Execution.ImplementationAllowed {
		t.Fatalf("execution goal binding = %+v", env.Data.Execution)
	}
}

func TestExecuteStatusJSONIncludesExecutionMode(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-247"},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "issue-247"},
		},
		Orchestrated:      true,
		Lead:              "cto",
		ExecutionMode:     executionModeProjectTeam,
		ControlRoot:       "/tmp/control",
		TargetProjectRoot: "/tmp/project",
		TargetContract:    "2.10.0",
	})
	seedAgentRecord(t, base, "issue-247", "cto", launch.Record{
		Binary: "codex", Handle: "cto", Role: "cto", AgentPID: 4242,
		Tmux: &launch.TmuxInfo{
			Session:  "tmux-issue-247",
			WindowID: "@1",
			PaneID:   "%1",
			Target:   "new-window",
		},
	})
	swapStatusPaneLister(t, []tmuxpane.TmuxPane{{PaneID: "%1"}}, nil)

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-247",
		ExplicitSession:  true,
		Probe:            statusProbe(map[int]bool{4242: true}, map[int]bool{4242: true}, time.Now()),
		JSON:             true,
		RuntimeVersion:   "2.10.0",
	})
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	exec := env.Data.Execution
	if exec.Mode != executionModeProjectTeam || exec.MutableActor != "cto" || !exec.ImplementationAllowed {
		t.Fatalf("execution = %+v, want project_team led by cto", exec)
	}
	if strings.Join(exec.VisibleTeamMembers, ",") != "cto,qa" {
		t.Fatalf("visible members = %v, want cto,qa", exec.VisibleTeamMembers)
	}
	if exec.ControlRoot != "/tmp/control" || exec.TargetProjectRoot != "/tmp/project" {
		t.Fatalf("execution roots = %q/%q", exec.ControlRoot, exec.TargetProjectRoot)
	}
	if !exec.VersionCompatibility.Compatible || exec.VersionCompatibility.RunningVersion != "2.10.0" {
		t.Fatalf("version compatibility = %+v", exec.VersionCompatibility)
	}
}

func TestExecuteStatusJSONBlocksReleaseReadyForSoloWithoutReason(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "release-lead", Binary: "codex", Handle: "release-lead", Session: "v2-11-0"},
		},
		Orchestrated:  true,
		Lead:          "release-lead",
		ExecutionMode: executionModeProjectLead,
		LeadExecution: &team.LeadExecution{
			Posture:             team.LeadExecutionSolo,
			IndependentReview:   &team.IndependentReview{Status: team.IndependentReviewWaived, Reason: "reviewer unavailable for fixture"},
			FinalRecommendation: "ready except solo justification",
		},
	})
	seedAgentRecord(t, base, "v2-11-0", "release-lead", launch.Record{
		Binary: "codex", Handle: "release-lead", Role: "release-lead", AgentPID: 7201,
		AdoptionMode: "managed_window", LauncherPaneID: "%launcher",
		Tmux: &launch.TmuxInfo{Session: "squad", WindowID: "@1", PaneID: "%1", Target: "new-window"},
	})
	swapStatusPaneLister(t, []tmuxpane.TmuxPane{{PaneID: "%1"}}, nil)
	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "v2-11-0",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(map[int]bool{7201: true}, map[int]bool{7201: true}, time.Now()),
		RuntimeVersion:   "2.11.0",
	})
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	if env.Data.Execution.LeadExecution.Posture != team.LeadExecutionSolo || !env.Data.Execution.LeadExecution.Declared {
		t.Fatalf("lead execution = %+v, want declared solo", env.Data.Execution.LeadExecution)
	}
	ready := env.Data.Execution.ReleaseReadiness
	if ready.Ready || ready.State != "blocked" {
		t.Fatalf("release readiness = %+v, want blocked", ready)
	}
	gates := readinessGatesByCode(ready.Gates)
	gate := gates["solo_justification_for_non_trivial_goal"]
	if !gate.Required || gate.Passed || !strings.Contains(gate.Detail, "solo") {
		t.Fatalf("solo gate = %+v, want required unsatisfied gate", gate)
	}
}

func TestExecuteStatusJSONReleaseReadyWithVisibleTeamAndReview(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "release-lead", Binary: "codex", Handle: "release-lead", Session: "v2-11-0"},
			{Role: "developer", Binary: "claude", Handle: "developer", Session: "v2-11-0"},
		},
		Orchestrated:  true,
		Lead:          "release-lead",
		ExecutionMode: executionModeProjectTeam,
		LeadExecution: &team.LeadExecution{
			Posture:             team.LeadExecutionVisibleTeam,
			DecisionTime:        "2026-06-28T23:00:00Z",
			Reason:              "release lead owns code and developer reviews each slice",
			ChildBudget:         1,
			PlannedDelegations:  []string{"developer review"},
			ReviewPlan:          "developer reviews release gates",
			IndependentReview:   &team.IndependentReview{Status: team.IndependentReviewComplete, Reviewer: "developer", ThreadID: "p2p/developer__release-lead"},
			FinalRecommendation: "ready after validation",
		},
	})
	seedAgentRecord(t, base, "v2-11-0", "release-lead", launch.Record{
		Binary: "codex", Handle: "release-lead", Role: "release-lead", AgentPID: 7301,
		AdoptionMode: "managed_window", LauncherPaneID: "%launcher",
		Tmux: &launch.TmuxInfo{Session: "squad", WindowID: "@1", PaneID: "%1", Target: "new-window"},
	})
	swapStatusPaneLister(t, []tmuxpane.TmuxPane{{PaneID: "%1"}}, nil)
	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "v2-11-0",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(map[int]bool{7301: true}, map[int]bool{7301: true}, time.Now()),
		RuntimeVersion:   "2.11.0",
	})
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	exec := env.Data.Execution
	if exec.LeadExecution.Posture != team.LeadExecutionVisibleTeam || exec.LeadExecution.IndependentReview.Status != team.IndependentReviewComplete {
		t.Fatalf("lead execution = %+v, want visible team with complete review", exec.LeadExecution)
	}
	if !exec.ReleaseReadiness.Ready || exec.ReleaseReadiness.State != "ready" {
		t.Fatalf("release readiness = %+v, want ready", exec.ReleaseReadiness)
	}
	for _, gate := range exec.ReleaseReadiness.Gates {
		if gate.Required && !gate.Passed {
			t.Fatalf("gate %+v should be satisfied in ready state", gate)
		}
	}
}

func TestExecuteStatusJSONBlocksReleaseReadyForReviewCompleteWithoutEvidence(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "release-lead", Binary: "codex", Handle: "release-lead", Session: "v2-11-0"},
			{Role: "developer", Binary: "claude", Handle: "developer", Session: "v2-11-0"},
		},
		Orchestrated:  true,
		Lead:          "release-lead",
		ExecutionMode: executionModeProjectTeam,
		LeadExecution: &team.LeadExecution{
			Posture:             team.LeadExecutionVisibleTeam,
			IndependentReview:   &team.IndependentReview{Status: team.IndependentReviewComplete},
			FinalRecommendation: "ready except review evidence",
		},
	})
	seedAgentRecord(t, base, "v2-11-0", "release-lead", launch.Record{
		Binary: "codex", Handle: "release-lead", Role: "release-lead", AgentPID: 7401,
		AdoptionMode: "managed_window", LauncherPaneID: "%launcher",
		Tmux: &launch.TmuxInfo{Session: "squad", WindowID: "@1", PaneID: "%1", Target: "new-window"},
	})
	swapStatusPaneLister(t, []tmuxpane.TmuxPane{{PaneID: "%1"}}, nil)
	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "v2-11-0",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(map[int]bool{7401: true}, map[int]bool{7401: true}, time.Now()),
		RuntimeVersion:   "2.11.0",
	})
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	ready := env.Data.Execution.ReleaseReadiness
	if ready.Ready {
		t.Fatalf("release readiness = %+v, want blocked without review evidence", ready)
	}
	gate := readinessGatesByCode(ready.Gates)["independent_review_evidence_or_waiver"]
	if gate.Passed || !strings.Contains(gate.Detail, "evidence") {
		t.Fatalf("review gate = %+v, want unsatisfied evidence gate", gate)
	}
}

func TestExecuteStatusJSONReleaseReadyAllowsTrivialSoloWithoutReason(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "release-lead", Binary: "codex", Handle: "release-lead", Session: "tiny"},
		},
		Orchestrated:  true,
		Lead:          "release-lead",
		ExecutionMode: executionModeProjectLead,
		LeadExecution: &team.LeadExecution{
			Posture:             team.LeadExecutionSolo,
			GoalSignificance:    team.GoalSignificanceTrivial,
			IndependentReview:   &team.IndependentReview{Status: team.IndependentReviewWaived, Reason: "trivial docs-only change"},
			FinalRecommendation: "ready",
		},
	})
	seedAgentRecord(t, base, "tiny", "release-lead", launch.Record{
		Binary: "codex", Handle: "release-lead", Role: "release-lead", AgentPID: 7501,
		AdoptionMode: "managed_window", LauncherPaneID: "%launcher",
		Tmux: &launch.TmuxInfo{Session: "squad", WindowID: "@1", PaneID: "%1", Target: "new-window"},
	})
	swapStatusPaneLister(t, []tmuxpane.TmuxPane{{PaneID: "%1"}}, nil)
	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "tiny",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(map[int]bool{7501: true}, map[int]bool{7501: true}, time.Now()),
		RuntimeVersion:   "2.11.0",
	})
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	ready := env.Data.Execution.ReleaseReadiness
	if !ready.Ready {
		t.Fatalf("release readiness = %+v, want ready for explicitly trivial solo goal", ready)
	}
	if _, ok := readinessGatesByCode(ready.Gates)["solo_justification_for_non_trivial_goal"]; ok {
		t.Fatalf("trivial solo goal should not emit non-trivial solo justification gate: %+v", ready.Gates)
	}
}

func TestExecuteStatusJSONReleaseReadyBlockedByVisibleLeadInvariant(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
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
	})
	seedAgentRecord(t, base, "v2-11-0", "release-lead", launch.Record{
		Binary: "codex", Handle: "release-lead", Role: "release-lead", AgentPID: 7601,
		AdoptionMode: "managed_session", LauncherPaneID: "%launcher",
		Tmux: &launch.TmuxInfo{Session: "detached", WindowID: "@1", PaneID: "%1", Target: "new-session"},
	})
	swapStatusPaneLister(t, []tmuxpane.TmuxPane{{PaneID: "%1"}}, nil)
	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "v2-11-0",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(map[int]bool{7601: true}, map[int]bool{7601: true}, time.Now()),
		RuntimeVersion:   "2.11.0",
	})
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	ready := env.Data.Execution.ReleaseReadiness
	if ready.Ready {
		t.Fatalf("release readiness = %+v, want blocked by visibility invariant", ready)
	}
	gate := readinessGatesByCode(ready.Gates)["visible_lead_invariants_ok"]
	if gate.Passed || !strings.Contains(gate.Evidence, "no_visible_lead") {
		t.Fatalf("visible lead gate = %+v, want failed no_visible_lead evidence", gate)
	}
}

func TestExecutionContractForTeamReleaseReadinessNotEvaluatedWithoutRuntimeInvariants(t *testing.T) {
	tm := team.Team{
		Project:       "/project",
		Orchestrated:  true,
		Lead:          "release-lead",
		ExecutionMode: executionModeProjectTeam,
		Members: []team.Member{
			{Role: "release-lead", Binary: "codex", Handle: "release-lead", Session: "v2-11-0"},
			{Role: "developer", Binary: "claude", Handle: "developer", Session: "v2-11-0"},
		},
		LeadExecution: &team.LeadExecution{
			Posture:             team.LeadExecutionVisibleTeam,
			IndependentReview:   &team.IndependentReview{Status: team.IndependentReviewComplete, Reviewer: "developer"},
			FinalRecommendation: "ready except runtime invariants",
		},
	}
	exec := executionContractForTeam(tm, team.DefaultProfile, "v2-11-0", "native_goal", "", "2.11.0")
	if exec.InvariantsEvaluated {
		t.Fatalf("static execution contract should not mark invariants evaluated: %+v", exec)
	}
	if exec.ReleaseReadiness.Ready || exec.ReleaseReadiness.State != "not_evaluated" {
		t.Fatalf("static release readiness = %+v, want not_evaluated", exec.ReleaseReadiness)
	}
	gate := readinessGatesByCode(exec.ReleaseReadiness.Gates)["visible_lead_invariants_ok"]
	if gate.Passed || gate.Evidence != "not_evaluated" {
		t.Fatalf("static visible lead gate = %+v, want not_evaluated failure", gate)
	}
}

func TestExecuteStatusJSONPolicyDisablesDirectChildControlActions(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "release-lead", Binary: "codex", Handle: "release-lead", Session: "v2-11-0"},
			{Role: "developer", Binary: "claude", Handle: "developer", Session: "v2-11-0"},
		},
		Orchestrated:  true,
		Lead:          "release-lead",
		ExecutionMode: executionModeProjectTeam,
	})
	seedAgentRecord(t, base, "v2-11-0", "release-lead", launch.Record{
		Binary: "codex", Handle: "release-lead", Role: "release-lead", AgentPID: 7001,
		Tmux: &launch.TmuxInfo{Session: "squad", WindowID: "@1", PaneID: "%1", Target: "new-window"},
	})
	seedAgentRecord(t, base, "v2-11-0", "developer", launch.Record{
		Binary: "claude", Handle: "developer", Role: "developer", AgentPID: 7002,
		Tmux: &launch.TmuxInfo{Session: "squad", WindowID: "@2", PaneID: "%2", Target: "new-window"},
	})
	swapStatusPaneLister(t, []tmuxpane.TmuxPane{{PaneID: "%1"}, {PaneID: "%2"}}, nil)

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "v2-11-0",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(map[int]bool{7001: true, 7002: true}, map[int]bool{7001: true, 7002: true}, time.Now()),
		RuntimeVersion:   "2.11.0",
	})
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	records := map[string]statusRecord{}
	for _, r := range env.Data.Records {
		records[r.Role] = r
	}
	leadActions := actionsByKind(records["release-lead"].Actions)
	childActions := actionsByKind(records["developer"].Actions)
	for _, kind := range []string{"send", "goal_deliver", "dispatch"} {
		if !leadActions[kind].Available {
			t.Fatalf("lead %s action should remain available: %+v", kind, leadActions[kind])
		}
		if childActions[kind].Available || !strings.Contains(childActions[kind].Reason, "visible lead") {
			t.Fatalf("child %s action should be unavailable via execution policy: %+v", kind, childActions[kind])
		}
	}
	for _, kind := range []string{"focus", "status", "task_list"} {
		if !childActions[kind].Available {
			t.Fatalf("child read/focus action %s should remain available: %+v", kind, childActions[kind])
		}
	}
}

func TestExecuteStatusJSONMarksOperatorVisibleLead(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "release-lead", Binary: "codex", Handle: "release-lead", Session: "v2-11-0"},
			{Role: "developer", Binary: "claude", Handle: "developer", Session: "v2-11-0"},
		},
		Orchestrated:  true,
		Lead:          "release-lead",
		ExecutionMode: executionModeProjectTeam,
	})
	seedAgentRecord(t, base, "v2-11-0", "release-lead", launch.Record{
		Binary: "codex", Handle: "release-lead", Role: "release-lead", AgentPID: 7101,
		AdoptionMode: "managed_window", LauncherPaneID: "%launcher",
		Tmux: &launch.TmuxInfo{Session: "squad", WindowID: "@1", PaneID: "%1", Target: "new-window"},
	})
	seedAgentRecord(t, base, "v2-11-0", "developer", launch.Record{
		Binary: "claude", Handle: "developer", Role: "developer", AgentPID: 7102,
		Tmux: &launch.TmuxInfo{Session: "squad", WindowID: "@2", PaneID: "%2", Target: "new-window"},
	})
	swapStatusPaneLister(t, []tmuxpane.TmuxPane{{PaneID: "%1"}, {PaneID: "%2"}}, nil)

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "v2-11-0",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(map[int]bool{7101: true, 7102: true}, map[int]bool{7101: true, 7102: true}, time.Now()),
		RuntimeVersion:   "2.11.0",
	})
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	if !env.Data.Execution.InvariantOK || len(env.Data.Execution.InvariantErrors) != 0 {
		t.Fatalf("execution invariants = ok:%v errors:%v, want clean", env.Data.Execution.InvariantOK, env.Data.Execution.InvariantErrors)
	}
	records := map[string]statusRecord{}
	for _, r := range env.Data.Records {
		records[r.Role] = r
	}
	lead := records["release-lead"]
	if !lead.OperatorVisible || lead.RoleBoundary != "lead" || lead.AdoptionMode != "managed_window" || lead.VisibilityProblem != "" {
		t.Fatalf("lead visibility fields = %+v, want visible managed lead", lead)
	}
	child := records["developer"]
	if child.OperatorVisible || child.RoleBoundary != "child" || child.AdoptionMode != "managed_window" {
		t.Fatalf("child visibility fields = %+v, want non-visible child", child)
	}
}

func TestExecuteStatusJSONFlagsDetachedVisibleLeadInvariant(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members:       []team.Member{{Role: "release-lead", Binary: "codex", Handle: "release-lead", Session: "v2-11-0"}},
		Orchestrated:  true,
		Lead:          "release-lead",
		ExecutionMode: executionModeProjectLead,
	})
	seedAgentRecord(t, base, "v2-11-0", "release-lead", launch.Record{
		Binary: "codex", Handle: "release-lead", Role: "release-lead", AgentPID: 7201,
		AdoptionMode: "managed_session", LauncherPaneID: "%launcher",
		Tmux: &launch.TmuxInfo{Session: "detached-squad", WindowID: "@1", PaneID: "%1", Target: "new-session"},
	})
	swapStatusPaneLister(t, []tmuxpane.TmuxPane{{PaneID: "%1"}}, nil)

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "v2-11-0",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(map[int]bool{7201: true}, map[int]bool{7201: true}, time.Now()),
		RuntimeVersion:   "2.11.0",
	})
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	if env.Data.Execution.InvariantOK || len(env.Data.Execution.InvariantErrors) != 1 {
		t.Fatalf("execution invariants = ok:%v errors:%v, want one detached-lead error", env.Data.Execution.InvariantOK, env.Data.Execution.InvariantErrors)
	}
	if env.Data.Execution.InvariantErrors[0].Code != "no_visible_lead" || !strings.Contains(env.Data.Execution.InvariantErrors[0].Message, "detached") {
		t.Fatalf("invariant error = %+v, want detached no_visible_lead detail", env.Data.Execution.InvariantErrors[0])
	}
	lead := env.Data.Records[0]
	if lead.OperatorVisible || lead.RoleBoundary != "lead" || lead.AdoptionMode != "managed_session" {
		t.Fatalf("lead visibility fields = %+v, want detached non-visible lead", lead)
	}
	if lead.VisibilityProblem != "detached_session" {
		t.Fatalf("visibility problem = %q, want detached_session", lead.VisibilityProblem)
	}
	repair := actionsByKind(lead.VisibilityRepairActions)
	if repair["attach_control"].Command != "tmux -CC attach -t detached-squad" {
		t.Fatalf("repair actions = %+v, want attach_control for detached session", repair)
	}
	if !repair["resume_current_window"].Available {
		t.Fatalf("repair actions = %+v, want resume_current_window available", repair)
	}
}

func TestExecuteStatusJSONFlagsCurrentPaneCollapsedLead(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members:       []team.Member{{Role: "release-lead", Binary: "codex", Handle: "release-lead", Session: "v2-11-0"}},
		Orchestrated:  true,
		Lead:          "release-lead",
		ExecutionMode: executionModeProjectLead,
	})
	seedAgentRecord(t, base, "v2-11-0", "release-lead", launch.Record{
		Binary: "codex", Handle: "release-lead", Role: "release-lead", AgentPID: 7301,
		AdoptionMode: "bare_agent_up", LauncherPaneID: "%1",
		Tmux: &launch.TmuxInfo{Session: "root", WindowID: "@1", PaneID: "%1"},
	})
	swapStatusPaneLister(t, []tmuxpane.TmuxPane{{PaneID: "%1"}}, nil)

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "v2-11-0",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(map[int]bool{7301: true}, map[int]bool{7301: true}, time.Now()),
		RuntimeVersion:   "2.11.0",
	})
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	lead := env.Data.Records[0]
	if lead.OperatorVisible || !lead.CurrentPaneConflict || lead.VisibilityProblem != "current_pane_collapse" {
		t.Fatalf("lead visibility fields = %+v, want collapsed non-visible lead", lead)
	}
	if env.Data.Execution.InvariantOK || env.Data.Execution.InvariantErrors[0].Code != "lead_pane_collapsed" {
		t.Fatalf("execution invariants = %+v, want lead_pane_collapsed", env.Data.Execution)
	}
}

func TestExecuteStatusJSONFailClosedWhenPaneOriginUnprovable(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members:       []team.Member{{Role: "release-lead", Binary: "codex", Handle: "release-lead", Session: "v2-11-0"}},
		Orchestrated:  true,
		Lead:          "release-lead",
		ExecutionMode: executionModeProjectLead,
	})
	seedAgentRecord(t, base, "v2-11-0", "release-lead", launch.Record{
		Binary: "codex", Handle: "release-lead", Role: "release-lead", AgentPID: 7351,
		Tmux: &launch.TmuxInfo{Session: "squad", WindowID: "@1", PaneID: "%1", Target: "new-window"},
	})
	swapStatusPaneLister(t, []tmuxpane.TmuxPane{{PaneID: "%1"}}, nil)

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "v2-11-0",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(map[int]bool{7351: true}, map[int]bool{7351: true}, time.Now()),
		RuntimeVersion:   "2.11.0",
	})
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	lead := env.Data.Records[0]
	if lead.OperatorVisible || lead.VisibilityProblem != "pane_origin_unprovable" {
		t.Fatalf("lead visibility fields = %+v, want fail-closed pane_origin_unprovable", lead)
	}
	if env.Data.Execution.InvariantOK || env.Data.Execution.InvariantErrors[0].Code != "no_visible_lead" {
		t.Fatalf("execution invariants = %+v, want no_visible_lead", env.Data.Execution)
	}
}

func TestExecuteStatusJSONAllowsExternalLeadInCurrentPane(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members:       []team.Member{{Role: "release-lead", Binary: "codex", Handle: "release-lead", Session: "v2-11-0"}},
		Orchestrated:  true,
		Lead:          "release-lead",
		ExecutionMode: executionModeProjectLead,
	})
	seedAgentRecord(t, base, "v2-11-0", "release-lead", launch.Record{
		Binary: "codex", Handle: "release-lead", Role: "release-lead", AgentPID: 7401,
		External: true, AdoptionMode: adoptionModeExternalProjectLead, LauncherPaneID: "%1",
		Tmux: &launch.TmuxInfo{Session: "root", WindowID: "@1", PaneID: "%1", Target: "external"},
	})
	swapStatusPaneLister(t, []tmuxpane.TmuxPane{{PaneID: "%1"}}, nil)

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "v2-11-0",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(map[int]bool{7401: true}, map[int]bool{7401: true}, time.Now()),
		RuntimeVersion:   "2.11.0",
	})
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	lead := env.Data.Records[0]
	if !lead.OperatorVisible || lead.CurrentPaneConflict || lead.AdoptionMode != adoptionModeExternalProjectLead || lead.VisibilityProblem != "" {
		t.Fatalf("lead visibility fields = %+v, want visible external lead without conflict", lead)
	}
	if !env.Data.Execution.InvariantOK {
		t.Fatalf("execution invariants = %+v, want clean", env.Data.Execution)
	}
}

func TestExecuteStatusJSONRechecksExternalLeadPaneForVisibility(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members:       []team.Member{{Role: "release-lead", Binary: "codex", Handle: "release-lead", Session: "v2-11-0"}},
		Orchestrated:  true,
		Lead:          "release-lead",
		ExecutionMode: executionModeProjectLead,
	})
	seedAgentRecord(t, base, "v2-11-0", "release-lead", launch.Record{
		Binary: "codex", Handle: "release-lead", Role: "release-lead",
		External: true, AdoptionMode: adoptionModeExternalProjectLead, LauncherPaneID: "%1",
		Tmux: &launch.TmuxInfo{Session: "root", WindowID: "@1", PaneID: "%1", Target: "external"},
	})
	prevLister := statusPaneLister
	prevInspector := statusPaneInspector
	statusPaneLister = func() ([]tmuxpane.TmuxPane, error) { return nil, nil }
	inspectCalls := 0
	statusPaneInspector = func(id string) (tmuxpane.TmuxPane, bool) {
		inspectCalls++
		if id == "%1" && inspectCalls >= 2 {
			return tmuxpane.TmuxPane{PaneID: "%1", WindowID: "@1", Session: "root"}, true
		}
		return tmuxpane.TmuxPane{}, false
	}
	t.Cleanup(func() { statusPaneLister = prevLister; statusPaneInspector = prevInspector })

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "v2-11-0",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(nil, nil, time.Now()),
		RuntimeVersion:   "2.11.0",
	})
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	lead := env.Data.Records[0]
	if lead.Status != statusStateLive || lead.Tmux == nil || !lead.Tmux.PaneAlive {
		t.Fatalf("lead status = %+v, want live external pane after visibility recheck", lead)
	}
	if !lead.OperatorVisible || lead.VisibilityProblem != "" {
		t.Fatalf("lead visibility fields = %+v, want visible external lead", lead)
	}
	if !env.Data.Execution.InvariantOK {
		t.Fatalf("execution invariants = %+v, want clean", env.Data.Execution)
	}
}

func TestExecuteStatusJSONFlagsGenericExternalProjectLeadBoundary(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members:       []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
		Orchestrated:  true,
		Lead:          "cto",
		ExecutionMode: executionModeProjectLead,
	})
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary: "codex", Handle: "cto", Role: "cto", AgentPID: 7777,
		External: true, AdoptionMode: adoptionModeExternal,
		Tmux: &launch.TmuxInfo{Session: "root", WindowID: "@1", PaneID: "%1", Target: "external"},
	})
	swapStatusPaneLister(t, []tmuxpane.TmuxPane{{PaneID: "%1"}}, nil)

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(map[int]bool{7777: true}, map[int]bool{7777: true}, time.Now()),
		RuntimeVersion:   "2.15.0",
	})
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	lead := env.Data.Records[0]
	if lead.OperatorVisible || lead.VisibilityProblem != "role_boundary_violation" {
		t.Fatalf("lead visibility = %+v, want boundary violation", lead)
	}
	if env.Data.Execution.InvariantOK || len(env.Data.Execution.InvariantErrors) != 1 {
		t.Fatalf("execution invariants = %+v, want one boundary error", env.Data.Execution)
	}
	inv := env.Data.Execution.InvariantErrors[0]
	if inv.Code != "lead_role_boundary_violation" || inv.Remedy == nil || !strings.Contains(inv.Remedy.Command, "resume") {
		t.Fatalf("invariant = %+v, want lead boundary repair", inv)
	}
}

// #256 follow-up: a direct_lead_session lead is now classified role_boundary=lead
// so its visibility is judged, and an externally registered direct lead in the
// current pane is operator-visible without raising a visible-lead invariant
// (direct_lead_session stays out of the invariant-required set).
func TestExecuteStatusJSONMarksDirectLeadSessionVisibleWhenExternal(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members:       []team.Member{{Role: "release-lead", Binary: "codex", Handle: "release-lead", Session: "v2-11-0"}},
		ExecutionMode: executionModeDirectLeadSession,
	})
	seedAgentRecord(t, base, "v2-11-0", "release-lead", launch.Record{
		Binary: "codex", Handle: "release-lead", Role: "release-lead", AgentPID: 7501,
		External: true, AdoptionMode: "external", LauncherPaneID: "%1",
		Tmux: &launch.TmuxInfo{Session: "root", WindowID: "@1", PaneID: "%1", Target: "external"},
	})
	swapStatusPaneLister(t, []tmuxpane.TmuxPane{{PaneID: "%1"}}, nil)

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "v2-11-0",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(map[int]bool{7501: true}, map[int]bool{7501: true}, time.Now()),
		RuntimeVersion:   "2.11.0",
	})
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	lead := env.Data.Records[0]
	if lead.RoleBoundary != "lead" || !lead.OperatorVisible || lead.AdoptionMode != "external" || lead.VisibilityProblem != "" {
		t.Fatalf("direct-lead-session external lead = %+v, want visible lead", lead)
	}
	if !env.Data.Execution.InvariantOK || len(env.Data.Execution.InvariantErrors) != 0 {
		t.Fatalf("direct_lead_session must not raise visible-lead invariants: %+v", env.Data.Execution)
	}
}

// #256 follow-up: a CONFIGURED direct lead (orchestration lead running in direct
// mode) that landed in the launcher pane (bare agent up, no managed split) is
// classified role_boundary=lead but stays fail-closed: operator_visible=false
// with a current_pane_collapse problem, and still no invariant
// (direct_lead_session is the explicit exception mode). A registered/configured
// direct lead is what gets marked; a bare flat member is left as member.
func TestExecuteStatusJSONDirectLeadSessionBarePaneNotVisible(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members:       []team.Member{{Role: "release-lead", Binary: "codex", Handle: "release-lead", Session: "v2-11-0"}},
		Orchestrated:  true,
		Lead:          "release-lead",
		ExecutionMode: executionModeDirectLeadSession,
	})
	seedAgentRecord(t, base, "v2-11-0", "release-lead", launch.Record{
		Binary: "codex", Handle: "release-lead", Role: "release-lead", AgentPID: 7601,
		AdoptionMode: "bare_agent_up", LauncherPaneID: "%9",
		Tmux: &launch.TmuxInfo{Session: "root", WindowID: "@9", PaneID: "%9", Target: ""},
	})
	swapStatusPaneLister(t, []tmuxpane.TmuxPane{{PaneID: "%9"}}, nil)

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "v2-11-0",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(map[int]bool{7601: true}, map[int]bool{7601: true}, time.Now()),
		RuntimeVersion:   "2.11.0",
	})
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	lead := env.Data.Records[0]
	if lead.RoleBoundary != "lead" || lead.OperatorVisible || !lead.CurrentPaneConflict || lead.VisibilityProblem != "current_pane_collapse" {
		t.Fatalf("direct-lead-session bare pane = %+v, want fail-closed collapsed lead", lead)
	}
	if !env.Data.Execution.InvariantOK || len(env.Data.Execution.InvariantErrors) != 0 {
		t.Fatalf("direct_lead_session must not raise visible-lead invariants even when not visible: %+v", env.Data.Execution)
	}
}

// #256 follow-up regression: project_team with no resolvable lead (multi-member,
// empty Lead) must fail the visible-lead invariant rather than silently marking
// every member non-lead.
func TestExecuteStatusJSONFlagsEmptyLeadMultiMemberProjectTeam(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "release-lead", Binary: "codex", Handle: "release-lead", Session: "v2-11-0"},
			{Role: "developer", Binary: "claude", Handle: "developer", Session: "v2-11-0"},
		},
		ExecutionMode: executionModeProjectTeam,
	})
	seedAgentRecord(t, base, "v2-11-0", "release-lead", launch.Record{
		Binary: "codex", Handle: "release-lead", Role: "release-lead", AgentPID: 7701,
		AdoptionMode: "managed_window", LauncherPaneID: "%launcher",
		Tmux: &launch.TmuxInfo{Session: "squad", WindowID: "@1", PaneID: "%1", Target: "new-window"},
	})
	seedAgentRecord(t, base, "v2-11-0", "developer", launch.Record{
		Binary: "claude", Handle: "developer", Role: "developer", AgentPID: 7702,
		AdoptionMode: "managed_window", LauncherPaneID: "%launcher",
		Tmux: &launch.TmuxInfo{Session: "squad", WindowID: "@2", PaneID: "%2", Target: "new-window"},
	})
	swapStatusPaneLister(t, []tmuxpane.TmuxPane{{PaneID: "%1"}, {PaneID: "%2"}}, nil)

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "v2-11-0",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(map[int]bool{7701: true, 7702: true}, map[int]bool{7701: true, 7702: true}, time.Now()),
		RuntimeVersion:   "2.11.0",
	})
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	if env.Data.Execution.InvariantOK || len(env.Data.Execution.InvariantErrors) == 0 || env.Data.Execution.InvariantErrors[0].Code != "no_visible_lead" {
		t.Fatalf("empty-lead multi-member project_team invariants = %+v, want no_visible_lead failure", env.Data.Execution)
	}
	for _, r := range env.Data.Records {
		if r.OperatorVisible || r.RoleBoundary == "lead" {
			t.Fatalf("no member should be a visible lead without a configured lead: %+v", r)
		}
	}
}

// #256 follow-up regression: global_orchestrator members are control-plane only,
// so each record is role_boundary=orchestrator and never operator_visible.
func TestExecuteStatusJSONGlobalOrchestratorRoleBoundary(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members:       []team.Member{{Role: "release-lead", Binary: "codex", Handle: "release-lead", Session: "v2-11-0"}},
		ExecutionMode: executionModeGlobalOrchestrator,
	})
	seedAgentRecord(t, base, "v2-11-0", "release-lead", launch.Record{
		Binary: "codex", Handle: "release-lead", Role: "release-lead", AgentPID: 7801,
		AdoptionMode: "managed_window", LauncherPaneID: "%launcher",
		Tmux: &launch.TmuxInfo{Session: "root", WindowID: "@1", PaneID: "%1", Target: "new-window"},
	})
	swapStatusPaneLister(t, []tmuxpane.TmuxPane{{PaneID: "%1"}}, nil)

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "v2-11-0",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(map[int]bool{7801: true}, map[int]bool{7801: true}, time.Now()),
		RuntimeVersion:   "2.11.0",
	})
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	rec := env.Data.Records[0]
	if rec.RoleBoundary != "orchestrator" || rec.OperatorVisible {
		t.Fatalf("global_orchestrator record = %+v, want role_boundary=orchestrator and not operator-visible", rec)
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

// TestStatusJSONProvesExternalOrchestratorBinding proves the #287 verification
// surface: for a registered external orchestrator, status --json exposes the
// goal_binding plus a per-record identity (external=true) and wake signal, so a
// client can confirm the wakeable orchestrator identity without inferring it.
func TestStatusJSONProvesExternalOrchestratorBinding(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members:       []team.Member{{Role: goalOrchestratorRole, Binary: "codex", Handle: "global-orch", Session: "issue-96"}},
		Orchestrated:  true,
		Lead:          goalOrchestratorRole,
		ExecutionMode: executionModeProjectLead,
	})
	agentDir := seedAgentRecord(t, base, "issue-96", "global-orch", launch.Record{
		CWD:      dir,
		Binary:   "codex",
		Handle:   "global-orch",
		Role:     goalOrchestratorRole,
		Session:  "issue-96",
		External: true,
		WakePID:  4321,
		Tmux:     &launch.TmuxInfo{Session: "global", WindowID: "@1", PaneID: "%99", Target: "external"},
		GoalBinding: &launch.GoalBinding{
			Mode:       "native_goal",
			NativeGoal: true,
			Source:     "goal-control",
			Command:    `/goal --goal "ship safely"`,
		},
	})
	writeWakeLock(t, agentDir, wakeLockFile{PID: 4321, Root: filepath.Join(base, "issue-96"), Started: time.Now()})

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
	if env.Data.GoalBinding.Mode != "native_goal" || !env.Data.GoalBinding.Verified {
		t.Fatalf("goal_binding = %+v, want verified native_goal", env.Data.GoalBinding)
	}
	if env.Data.Lead != goalOrchestratorRole || !env.Data.Orchestrated {
		t.Fatalf("lead/orchestrated = %q/%v", env.Data.Lead, env.Data.Orchestrated)
	}
	var row *statusRecord
	for i := range env.Data.Records {
		if env.Data.Records[i].Role == goalOrchestratorRole {
			row = &env.Data.Records[i]
			break
		}
	}
	if row == nil {
		t.Fatalf("no orchestrator record in %+v", env.Data.Records)
	}
	if !row.External {
		t.Fatalf("orchestrator record must expose external=true: %+v", row)
	}
	if row.Status != statusStateWakeLive || !row.Signals.WakeAlive || row.Signals.WakePID != 4321 {
		t.Fatalf("orchestrator record wake/identity = %+v", row)
	}
}

// TestStatusJSONSurfacesPreauthorizedActions proves #296 auditability: the
// in-scope allowlist granted at launch is visible in status --json, and never
// includes main push / tag / release.
func TestStatusJSONSurfacesPreauthorizedActions(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members:      []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}, {Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "issue-96"}},
		Orchestrated: true,
		Lead:         "cto",
	})
	seedAgentRecord(t, base, "issue-96", "fullstack", launch.Record{
		CWD:                  dir,
		Binary:               "claude",
		Handle:               "fullstack",
		Role:                 "fullstack",
		Session:              "issue-96",
		AgentPID:             4242,
		Tmux:                 &launch.TmuxInfo{PaneID: "%7"},
		PreauthorizedActions: claudeInScopePreauthAllowlist("issue-96"),
	})

	jsonOut, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(map[int]bool{4242: true}, map[int]bool{4242: true}, time.Now()),
	})
	if err != nil {
		t.Fatalf("status json: %v\n%s", err, jsonOut)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, jsonOut)
	var row *statusRecord
	for i := range env.Data.Records {
		if env.Data.Records[i].Role == "fullstack" {
			row = &env.Data.Records[i]
			break
		}
	}
	if row == nil {
		t.Fatalf("no fullstack record in %+v", env.Data.Records)
	}
	joined := strings.Join(row.PreauthorizedActions, "\n")
	if len(row.PreauthorizedActions) == 0 || !strings.Contains(joined, "gh pr create") {
		t.Fatalf("status must surface preauthorized_actions: %+v", row.PreauthorizedActions)
	}
	for _, forbidden := range []string{"origin main", "git tag", "gh release"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("preauthorized_actions must never include %q: %+v", forbidden, row.PreauthorizedActions)
		}
	}
}

func orchestratorStatusRecord(rows []statusRecord) *statusRecord {
	for i := range rows {
		if rows[i].Role == goalOrchestratorRole {
			return &rows[i]
		}
	}
	return nil
}

// TestStatusJSONOrchestratorWakeDrivenUserPollOnly proves #288: a registered
// orchestrator whose wake sidecar carries a drain inject-cmd is reported as
// wake-driven/auto-drain (no periodic amq drain loop), while the virtual
// operator handle stays poll-only. wake_auto_drain composes with #283's
// WakeInjectCmd.
func TestStatusJSONOrchestratorWakeDrivenUserPollOnly(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members:       []team.Member{{Role: goalOrchestratorRole, Binary: "codex", Handle: "global-orch", Session: "issue-96"}},
		Orchestrated:  true,
		Lead:          goalOrchestratorRole,
		ExecutionMode: executionModeProjectLead,
	})
	agentDir := seedAgentRecord(t, base, "issue-96", "global-orch", launch.Record{
		CWD:           dir,
		Binary:        "codex",
		Handle:        "global-orch",
		Role:          goalOrchestratorRole,
		Session:       "issue-96",
		External:      true,
		WakePID:       4321,
		WakeInjectCmd: wakeDrainInject(),
		Tmux:          &launch.TmuxInfo{Session: "global", WindowID: "@1", PaneID: "%99", Target: "external"},
	})
	writeWakeLock(t, agentDir, wakeLockFile{PID: 4321, Root: filepath.Join(base, "issue-96"), Started: time.Now()})

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
	row := orchestratorStatusRecord(env.Data.Records)
	if row == nil {
		t.Fatalf("no orchestrator record in %+v", env.Data.Records)
	}
	// Orchestrator handle: wake-driven auto-drain, reactively live (no polling).
	if !row.WakeAutoDrain {
		t.Fatalf("orchestrator must report wake_auto_drain=true: %+v", row)
	}
	if row.Status != statusStateWakeLive || !row.Signals.WakeAlive {
		t.Fatalf("orchestrator must be reactively wake-live: %+v", row)
	}
	// Virtual operator handle: stays poll-only, not wakeable (out of scope).
	if !env.Data.OperatorDelivery.PollRequired || env.Data.OperatorDelivery.WakeSupported {
		t.Fatalf("operator handle must remain poll-only: %+v", env.Data.OperatorDelivery)
	}
}

// TestStatusJSONWakeAutoDrainDegradedWhenSidecarDead proves QA's honest-failure
// requirement: wake_auto_drain is a CONFIGURATION signal, so when the sidecar
// process is dead the orchestrator surfaces as degraded (not wake-live) rather
// than silently appearing healthy.
func TestStatusJSONWakeAutoDrainDegradedWhenSidecarDead(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members:       []team.Member{{Role: goalOrchestratorRole, Binary: "codex", Handle: "global-orch", Session: "issue-96"}},
		Orchestrated:  true,
		Lead:          goalOrchestratorRole,
		ExecutionMode: executionModeProjectLead,
	})
	agentDir := seedAgentRecord(t, base, "issue-96", "global-orch", launch.Record{
		CWD:           dir,
		Binary:        "codex",
		Handle:        "global-orch",
		Role:          goalOrchestratorRole,
		Session:       "issue-96",
		External:      true,
		WakePID:       4321,
		WakeInjectCmd: wakeDrainInject(),
		Tmux:          &launch.TmuxInfo{Session: "global", WindowID: "@1", PaneID: "%99", Target: "external"},
	})
	writeWakeLock(t, agentDir, wakeLockFile{PID: 4321, Root: filepath.Join(base, "issue-96"), Started: time.Now()})

	// Probe reports the wake PID as DEAD.
	jsonOut, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(map[int]bool{4321: false}, map[int]bool{4321: false}, time.Now()),
	})
	if err != nil {
		t.Fatalf("status json: %v\n%s", err, jsonOut)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, jsonOut)
	row := orchestratorStatusRecord(env.Data.Records)
	if row == nil {
		t.Fatalf("no orchestrator record in %+v", env.Data.Records)
	}
	if !row.WakeAutoDrain {
		t.Fatalf("wake_auto_drain (configuration) should stay true even with a dead sidecar: %+v", row)
	}
	if row.Status == statusStateWakeLive || row.Signals.WakeAlive {
		t.Fatalf("dead sidecar must surface as degraded, not wake-live: %+v", row)
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
