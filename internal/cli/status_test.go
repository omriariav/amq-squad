package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/activity"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/state"
	taskstore "github.com/omriariav/amq-squad/v3/internal/task"
	"github.com/omriariav/amq-squad/v3/internal/team"
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

func seedStatusExternalEvidenceMessage(t *testing.T, agentDir string) {
	t.Helper()
	dir := filepath.Join(agentDir, "inbox", "cur")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `---json
{
  "schema": 1,
  "id": "ext1",
  "from": "kanban",
  "to": ["cto"],
  "thread": "task/KAN-42",
  "subject": "external kanban evidence",
  "created": "2026-06-22T11:55:00Z",
  "kind": "status",
  "labels": ["orchestrator", "orchestrator:kanban", "task-state:blocked", "blocking"],
  "context": {"orchestrator":{"task":{"id":"KAN-42"}}}
}
---
body
`
	if err := os.WriteFile(filepath.Join(dir, "ext1.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func seedStatusSymphonyMessage(t *testing.T, agentDir, id, event string, created time.Time) {
	t.Helper()
	dir := filepath.Join(agentDir, "inbox", "cur")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `---json
{
  "schema": 1,
  "id": "` + id + `",
  "from": "cto",
  "to": ["cto"],
  "thread": "task/amq-squad",
  "subject": "[symphony] ` + event + `: amq-squad",
  "created": "` + created.UTC().Format(time.RFC3339Nano) + `",
  "kind": "status",
  "labels": ["orchestrator", "orchestrator:symphony"],
  "context": {"orchestrator":{"event":"` + event + `","name":"symphony","task":{"id":"amq-squad"},"transport":"hook","version":1}}
}
---
body
`
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
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

func TestExecuteStatusJSONIncludesExternalEvidence(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	project := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	agentDir := seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary: "codex", Role: "cto", Handle: "cto", Session: "issue-96", CWD: project, AgentPID: 11,
	})
	seedStatusExternalEvidenceMessage(t, agentDir)

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       project,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		Profile:          team.DefaultProfile,
		Probe:            statusProbe(map[int]bool{}, map[int]bool{}, notifyNow),
		JSON:             true,
	})
	if err != nil {
		t.Fatalf("executeStatus: %v", err)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	if len(env.Data.ExternalEvidence) != 1 {
		t.Fatalf("external_evidence = %+v, want one row", env.Data.ExternalEvidence)
	}
	row := env.Data.ExternalEvidence[0]
	if row.Source != "amq-kanban" || row.SourceLabel != "orchestrator:kanban" || row.ExternalTaskID != "KAN-42" || row.State != "blocked" || row.Mutable {
		t.Fatalf("external evidence row = %+v", row)
	}
	if len(env.Data.Records) != 1 || env.Data.Records[0].Handle != "cto" {
		t.Fatalf("native status records changed: %+v", env.Data.Records)
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

func setupFakeAMQSessionRootMkdirOnSession(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dir, ".agent-mail")
	script := `#!/bin/sh
if [ "$1" != "env" ]; then
  echo "unexpected amq command: $*" >&2
  exit 1
fi
session=""
root_arg=""
me=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --session)
      shift
      session="$1"
      ;;
    --root)
      shift
      root_arg="$1"
      ;;
    --me)
      shift
      me="$1"
      ;;
  esac
  shift
done
root="$AMQ_FAKE_BASE"
if [ "$root_arg" != "" ]; then
  root="$root_arg"
elif [ "$session" != "" ]; then
  root="$root/$session"
  mkdir -p "$root/agents/${me:-cto}/inbox/new"
fi
printf '{"root":"%s","base_root":"%s","me":"%s"}\n' "$root" "$AMQ_FAKE_BASE" "${me:-cto}"
`
	if err := os.WriteFile(filepath.Join(binDir, "amq"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AMQ_FAKE_BASE", base)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return base
}

func TestStatusIgnoresUnstructuredWorkerCompletionReport(t *testing.T) {
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
		Thread:    "p2p/cto__qa",
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
		"p2p/cto__qa", "DONE t1", string(state.KindStatus), now.Add(-5*time.Minute))

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
	if w := findStatusWarning(env.Data.Warnings, "task_completion_reconcile_ready"); w != nil {
		t.Fatalf("unstructured DONE prose triggered completion warning: %+v", w)
	}
}

func TestStatusDoesNotTreatAckStartCompleteProseAsCompletion(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
			{Role: "platform-dev", Binary: "codex", Handle: "platform-dev", Session: "issue-96"},
		},
		Orchestrated: true,
		Lead:         "cto",
	})
	now := time.Date(2026, 7, 15, 19, 4, 54, 0, time.UTC)
	created, err := taskstore.AddForProfile(dir, team.DefaultProfile, "issue-96", taskstore.AddInput{Title: "wizard slice", AssignTo: "platform-dev"}, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := taskstore.LinkDispatchForProfile(dir, team.DefaultProfile, "issue-96", created.ID, taskstore.Dispatch{
		Sender: "cto", Assignee: "platform-dev", Thread: "p2p/cto__platform-dev", Kind: "todo", Subject: "TASK t2", MessageID: "dispatch-t2",
	}, now.Add(-50*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := taskstore.ClaimForProfile(dir, team.DefaultProfile, "issue-96", created.ID, "platform-dev", now.Add(-40*time.Minute)); err != nil {
		t.Fatal(err)
	}
	ctoDir := filepath.Join(dir, ".agent-mail", "issue-96", "agents", "cto")
	seedThreadMessage(t, ctoDir, "new", "2026-07-15T21-21-59.878Z_pid32072_1f1baa43", "platform-dev", []string{"cto"},
		"p2p/cto__platform-dev", "ACK #477 final hardening matrix", string(state.KindStatus), now,
		"ACK #477 core acceptance/hardening: complete the negative evidence/apply matrix for t1 and completion reconciliation.")

	out, err := runStatusExec(t, statusExecution{
		ProjectDir: dir, RequestedSession: "issue-96", ExplicitSession: true, JSON: true,
		Probe: statusProbe(nil, nil, now.Add(time.Minute)),
	})
	if err != nil {
		t.Fatalf("status --json: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	for _, warning := range env.Data.Warnings {
		if warning.Kind == "task_report_pending_completion" || strings.Contains(warning.SuggestedCommand, "task done") || strings.Contains(warning.Detail, "task done") {
			t.Fatalf("ACK/start prose must not be completion evidence or blind-close guidance: %+v", warning)
		}
	}
}

func TestStatusTaskWarningsSuppressClosedAndSupersededTasks(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC)
	link := func(tk taskstore.Task) {
		t.Helper()
		if _, err := taskstore.LinkDispatchForProfile(dir, team.DefaultProfile, "s", tk.ID, taskstore.Dispatch{Sender: "cto", Assignee: "qa", Thread: "p2p/cto__qa", MessageID: "dispatch-" + tk.ID}, now); err != nil {
			t.Fatal(err)
		}
	}

	completed, _ := taskstore.AddForProfile(dir, team.DefaultProfile, "s", taskstore.AddInput{Title: "completed", AssignTo: "qa"}, now)
	link(completed)
	_, _ = taskstore.ClaimForProfile(dir, team.DefaultProfile, "s", completed.ID, "qa", now)
	if _, err := taskstore.DoneForProfile(dir, team.DefaultProfile, "s", completed.ID, "qa", "done", now); err != nil {
		t.Fatal(err)
	}
	cancelled, _ := taskstore.AddForProfile(dir, team.DefaultProfile, "s", taskstore.AddInput{Title: "cancelled", AssignTo: "qa"}, now)
	link(cancelled)
	if _, err := taskstore.CancelForProfile(dir, team.DefaultProfile, "s", cancelled.ID, "qa", "obsolete", "", now); err != nil {
		t.Fatal(err)
	}
	superseded, _ := taskstore.AddForProfile(dir, team.DefaultProfile, "s", taskstore.AddInput{Title: "superseded", AssignTo: "qa"}, now)
	link(superseded)
	replacement, _ := taskstore.AddForProfile(dir, team.DefaultProfile, "s", taskstore.AddInput{Title: "replacement"}, now)
	if _, err := taskstore.CancelForProfile(dir, team.DefaultProfile, "s", superseded.ID, "qa", "replaced", replacement.ID, now); err != nil {
		t.Fatal(err)
	}

	warnings, err := statusTaskWarnings(dir, team.DefaultProfile, "s")
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("terminal attention lifecycle emitted status warnings: %+v", warnings)
	}
}

func TestStatusWarnsAgedOperatorGate(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	project, base, _ := seedNotifyProject(t, team.DefaultOperator())
	project = canonicalFilesystemPath(project)
	base = canonicalFilesystemPath(base)
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
		!strings.Contains(w.SuggestedCommand, "amq-squad operator status") ||
		!strings.Contains(w.SuggestedCommand, "--session s") {
		t.Fatalf("missing aged operator-gate warning: %+v", env.Data.Warnings)
	}
}

func TestStatusWarnsAgedOperatorGateNamedProfileExactRoot(t *testing.T) {
	project := t.TempDir()
	profile, session := "release", "s"
	op := team.DefaultOperator()
	cfg := team.Team{
		Project: project, Workstream: session, Operator: &op,
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: session}},
	}
	if err := team.WriteProfile(project, profile, cfg); err != nil {
		t.Fatal(err)
	}
	root := squadnamespace.AMQRoot(project, profile, session)
	ctoDir := filepath.Join(root, "agents", "cto")
	if err := launch.Write(ctoDir, launch.Record{
		CWD: project, Binary: "codex", Handle: "cto", Role: "cto", Session: session,
		Root: root, BaseRoot: root, AgentPID: 42, StartedAt: notifyNow, TeamProfile: profile,
	}); err != nil {
		t.Fatal(err)
	}
	seedNotifyMessageToDir(t, filepath.Join(root, "agents", team.DefaultOperatorHandle), "new", notifyMsg{
		ID: "gate-named", From: "cto", To: team.DefaultOperatorHandle, Thread: "gate/release",
		Subject: "APPROVAL: release", Kind: string(state.KindQuestion), Created: notifyNow.Add(-125 * time.Minute),
	})

	warnings := statusAgedOperatorGateWarnings(project, profile, session, notifyNow)
	w := findStatusWarning(warnings, "operator_gate_strong_warning")
	if w == nil || !strings.Contains(w.Detail, "gate/release") || !strings.Contains(w.SuggestedCommand, "--profile release") {
		t.Fatalf("missing named-profile aged operator-gate warning: %+v", warnings)
	}
}

func TestStatusAgedOperatorGateWarningsIgnoreMissingOrNonDirectoryRoot(t *testing.T) {
	for _, tc := range []struct {
		name     string
		makeRoot bool
	}{
		{name: "missing"},
		{name: "non-directory", makeRoot: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := t.TempDir()
			root := squadnamespace.AMQRoot(project, team.DefaultProfile, "s")
			if tc.makeRoot {
				if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(root, []byte("not a session directory"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if warnings := statusAgedOperatorGateWarnings(project, team.DefaultProfile, "s", notifyNow); len(warnings) != 0 {
				t.Fatalf("warnings for %s root: %+v", tc.name, warnings)
			}
		})
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

func TestStatusJSONUsesSymphonyActivityFallback(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	})
	now := time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC)
	agentDir := seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Handle:  "cto",
		Role:    "cto",
		Session: "issue-96",
		CWD:     dir,
	})
	seedStatusSymphonyMessage(t, agentDir, "symphony-after-run", "after_run", now.Add(-30*time.Second))

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
	rec := findStatusRecord(env.Data.Records, "cto")
	if rec == nil || rec.Activity == nil {
		t.Fatalf("cto symphony activity missing: %+v", env.Data.Records)
	}
	if rec.Activity.Source != activity.SourceSymphony || rec.Activity.Quality != activity.StateFresh ||
		rec.Activity.TaskID != "amq-squad" || rec.Activity.Phase != "symphony_after_run" ||
		rec.Activity.Detail != "[symphony] after_run: amq-squad" {
		t.Fatalf("symphony activity = %+v", rec.Activity)
	}
}

func TestStatusJSONSymphonyBeforeRunCanBeFresh(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	})
	now := time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC)
	agentDir := seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Handle:  "cto",
		Role:    "cto",
		Session: "issue-96",
		CWD:     dir,
	})
	seedStatusSymphonyMessage(t, agentDir, "symphony-before-run", "before_run", now.Add(-10*time.Second))

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
	rec := findStatusRecord(env.Data.Records, "cto")
	if rec == nil || rec.Activity == nil {
		t.Fatalf("cto symphony activity missing: %+v", env.Data.Records)
	}
	if rec.Activity.Source != activity.SourceSymphony || rec.Activity.Quality != activity.StateFresh ||
		rec.Activity.Stale || rec.Activity.Phase != "symphony_before_run" {
		t.Fatalf("before_run symphony activity = %+v", rec.Activity)
	}
}

func TestStatusJSONHeartbeatWinsOverSymphony(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	})
	now := time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC)
	agentDir := seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Handle:  "cto",
		Role:    "cto",
		Session: "issue-96",
		CWD:     dir,
	})
	seedStatusSymphonyMessage(t, agentDir, "symphony-after-run", "after_run", now.Add(-10*time.Second))
	if err := activity.Write(agentDir, activity.File{
		Handle:    "cto",
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
	rec := findStatusRecord(env.Data.Records, "cto")
	if rec == nil || rec.Activity == nil {
		t.Fatalf("cto activity missing: %+v", env.Data.Records)
	}
	if rec.Activity.Source != activity.SourceHeartbeat || rec.Activity.TaskID != "t11" ||
		rec.Activity.Phase != "testing" {
		t.Fatalf("heartbeat should win over symphony activity, got %+v", rec.Activity)
	}
}

func TestStatusJSONSymphonyAfterCreateNeverFresh(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	})
	now := time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC)
	agentDir := seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Handle:  "cto",
		Role:    "cto",
		Session: "issue-96",
		CWD:     dir,
	})
	seedStatusSymphonyMessage(t, agentDir, "symphony-after-create", "after_create", now.Add(-10*time.Second))

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
	rec := findStatusRecord(env.Data.Records, "cto")
	if rec == nil || rec.Activity == nil {
		t.Fatalf("cto symphony activity missing: %+v", env.Data.Records)
	}
	if rec.Activity.Source != activity.SourceSymphony || rec.Activity.Quality != activity.StateUnknown ||
		!rec.Activity.Stale || rec.Activity.Phase != "symphony_after_create" {
		t.Fatalf("after_create symphony activity = %+v", rec.Activity)
	}
}

func TestStatusJSONSymphonyBeforeRemoveNeverFresh(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	})
	now := time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC)
	agentDir := seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Handle:  "cto",
		Role:    "cto",
		Session: "issue-96",
		CWD:     dir,
	})
	seedStatusSymphonyMessage(t, agentDir, "symphony-before-remove", "before_remove", now.Add(-10*time.Second))

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
	rec := findStatusRecord(env.Data.Records, "cto")
	if rec == nil || rec.Activity == nil {
		t.Fatalf("cto symphony activity missing: %+v", env.Data.Records)
	}
	if rec.Activity.Source != activity.SourceSymphony || rec.Activity.Quality != activity.StateUnknown ||
		!rec.Activity.Stale || rec.Activity.Phase != "symphony_before_remove" {
		t.Fatalf("before_remove symphony activity = %+v", rec.Activity)
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

func TestStatusUsesPlainTaskStateAndOmitsGoalSupervision(t *testing.T) {
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
	if w := findStatusWarning(env.Data.Warnings, "task_dispatched_pending"); w != nil {
		t.Fatalf("simple status interpreted legacy dispatch metadata: %+v", w)
	}
	if strings.Contains(out, `"goal_supervision"`) {
		t.Fatalf("simple status projected goal-supervision machinery: %s", out)
	}
	if strings.Contains(out, `"open_gates"`) {
		t.Fatalf("scoped status emitted an unobserved hardcoded gate count: %s", out)
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
}

func TestSelectStatusLaunchRecordRequiresExactHandleIdentity(t *testing.T) {
	tm := team.Team{Project: "/repo"}
	member := team.Member{Role: "dev", Handle: "dev", Binary: "codex"}
	entries := []launch.Entry{{
		AgentDir: "/repo/.agent-mail/work/agents/outsider",
		Record: launch.Record{
			TeamHome: "/repo", TeamProfile: team.DefaultProfile, Session: "work",
			Role: "dev", Handle: "outsider", Binary: "codex",
			Root: "/repo/.agent-mail/work",
		},
	}}
	selection := selectStatusLaunchRecord(tm, team.DefaultProfile, member, "work", duplicateLaunchProbe{}, entries)
	if selection.Found || len(selection.DuplicatePaths) != 0 {
		t.Fatalf("wrong-handle same-role record was selected: %+v", selection)
	}
	warnings := statusUnmanagedLaunchRecordWarningsFromEntries(team.Team{Project: "/repo", Members: []team.Member{member}}, team.DefaultProfile, "work", entries)
	if len(warnings) != 1 || warnings[0].Kind != "unmanaged_launch_record" ||
		!strings.Contains(warnings[0].Detail, "outsider") || !strings.Contains(warnings[0].Detail, launch.ExistingPath(entries[0].AgentDir)) {
		t.Fatalf("wrong-handle record was not surfaced as unmanaged: %+v", warnings)
	}
}

// TestRunStatusSessionRequiresTeam covers the single-session DETAIL path:
// status --session NAME still hard-requires a configured team, because it
// classifies that team's members. The no-selector BOARD path is the one that
// degrades gracefully (see TestRunStatusBoardNoTeamDegradesGracefully).
func TestRunStatusSessionRequiresTeam(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	_, _, err := captureOutput(t, func() error {
		return runStatusWithVersion([]string{"--session", "issue-96"}, "dev")
	})
	if err == nil || !strings.Contains(err.Error(), "no team configured") {
		t.Fatalf("want 'no team configured' error, got %v", err)
	}
}

func TestRunStatusProjectValidation(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	_, _, err := captureOutput(t, func() error {
		return runStatusWithVersion([]string{"--project", missing}, "dev")
	})
	if err == nil || !strings.Contains(err.Error(), "--project") {
		t.Fatalf("status --project missing error = %v, want --project error", err)
	}

	_, _, err = captureOutput(t, func() error {
		return runStatusWithVersion([]string{"--project", ""}, "dev")
	})
	if err == nil || !strings.Contains(err.Error(), "--project requires a directory") {
		t.Fatalf("status empty --project error = %v, want directory guidance", err)
	}
}

func TestRunStatusExplicitNamedProfileDoesNotCreateLegacySessionInbox(t *testing.T) {
	dir := t.TempDir()
	base := setupFakeAMQSessionRootMkdirOnSession(t)
	chdir(t, dir)
	seedProfile(t, dir, "review", team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "review"},
		},
		Orchestrated: true,
		Lead:         "cto",
	})
	namedAgentDir := filepath.Join(base, "review", "review", "agents", "cto")
	if err := os.MkdirAll(namedAgentDir, 0o755); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := captureOutput(t, func() error {
		return runStatusWithVersion([]string{"--profile", "review", "--session", "review", "--json"}, "dev")
	})
	if err != nil {
		t.Fatalf("status explicit named profile: %v\n%s", err, stdout)
	}
	legacyInbox := filepath.Join(base, "review", "agents", "cto", "inbox", "new")
	if _, err := os.Stat(legacyInbox); !os.IsNotExist(err) {
		t.Fatalf("status must not resolve through legacy --session root or mkdir %s; stat err = %v", legacyInbox, err)
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
		return runStatusWithVersion([]string{"--session", "main"}, "dev")
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
		return runStatusWithVersion([]string{"--session", "main", "--json"}, "dev")
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
