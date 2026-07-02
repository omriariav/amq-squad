package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	taskstore "github.com/omriariav/amq-squad/v2/internal/task"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func withMonitorStatusRows(t *testing.T, rows []statusRecord) {
	t.Helper()
	prev := monitorStatusRows
	monitorStatusRows = func(projectDir, profile, session string) ([]statusRecord, error) {
		return rows, nil
	}
	t.Cleanup(func() { monitorStatusRows = prev })
}

func withMonitorPaneBusy(t *testing.T, busy, known bool) {
	t.Helper()
	prev := monitorPaneBusy
	monitorPaneBusy = func(paneID string) (bool, bool) { return busy, known }
	t.Cleanup(func() { monitorPaneBusy = prev })
}

func seedInProgressTask(t *testing.T, dir, session, owner string, updatedAt time.Time) string {
	t.Helper()
	tk, err := taskstore.AddForProfile(dir, team.DefaultProfile, session, taskstore.AddInput{Title: "ship it"}, updatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := taskstore.ClaimForProfile(dir, team.DefaultProfile, session, tk.ID, owner, updatedAt); err != nil {
		t.Fatal(err)
	}
	return tk.ID
}

func seedUnreadMonitorInboxMessage(t *testing.T, dir, session, owner string) {
	t.Helper()
	inbox := filepath.Join(dir, ".agent-mail", session, "agents", owner, "inbox", "new")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatal(err)
	}
	msg := `---json
{
  "schema": 1,
  "id": "undrained-1",
  "from": "cto",
  "to": ["fullstack"],
  "thread": "p2p/cto__fullstack",
  "subject": "Task: drain me",
  "created": "2026-07-01T10:17:00Z",
  "priority": "normal",
  "kind": "todo"
}
---
durable task body that should have been drained
`
	if err := os.WriteFile(filepath.Join(inbox, "undrained-1.md"), []byte(msg), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runMonitorOnce(t *testing.T) (string, error) {
	t.Helper()
	stdout, _, err := captureOutput(t, func() error {
		return runMonitor([]string{"--session", "s", "--once", "--json"})
	})
	return stdout, err
}

func TestMonitorIdleFlagsNotLiveOwnerWithActiveTask(t *testing.T) {
	dir := seedTeam(t, team.Team{Members: []team.Member{{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "s"}}})
	withMonitorOperatorState(t, 0, 0)
	withMonitorStatusRows(t, []statusRecord{{Handle: "fullstack", Status: statusStateMissing}})
	seedInProgressTask(t, dir, "s", "fullstack", time.Now()) // age irrelevant for not-live

	out, err := runMonitorOnce(t)
	if err != nil {
		t.Fatalf("monitor: %v", err)
	}
	if !strings.Contains(out, monitorEventIdleActiveTask) || !strings.Contains(out, "owner:fullstack") {
		t.Fatalf("not-live owner with active task must be flagged:\n%s", out)
	}
}

func TestMonitorIdleSuppressedForBusyOwner(t *testing.T) {
	dir := seedTeam(t, team.Team{Members: []team.Member{{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "s"}}})
	withMonitorOperatorState(t, 0, 0)
	withMonitorStatusRows(t, []statusRecord{{Handle: "fullstack", Status: statusStateLive, Tmux: &tmuxRuntimeJSON{PaneID: "%5", PaneAlive: true}}})
	withMonitorPaneBusy(t, true, true) // busy/mid-turn
	seedInProgressTask(t, dir, "s", "fullstack", time.Now().Add(-2*time.Hour))

	out, err := runMonitorOnce(t)
	if err != nil {
		t.Fatalf("monitor: %v", err)
	}
	if strings.Contains(out, monitorEventIdleActiveTask) {
		t.Fatalf("a busy/mid-turn owner must never be flagged idle:\n%s", out)
	}
	if !strings.Contains(out, `"events_found":false`) {
		t.Fatalf("expected idle tick (busy owner suppressed):\n%s", out)
	}
}

func TestMonitorIdleFlagsLiveStaleOwner(t *testing.T) {
	dir := seedTeam(t, team.Team{Members: []team.Member{{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "s"}}})
	withMonitorOperatorState(t, 0, 0)
	withMonitorStatusRows(t, []statusRecord{{Handle: "fullstack", Status: statusStateLive, Tmux: &tmuxRuntimeJSON{PaneID: "%5", PaneAlive: true}}})
	withMonitorPaneBusy(t, false, true) // live, not busy
	seedInProgressTask(t, dir, "s", "fullstack", time.Now().Add(-1*time.Hour))

	out, err := runMonitorOnce(t)
	if err != nil {
		t.Fatalf("monitor: %v", err)
	}
	if !strings.Contains(out, monitorEventIdleActiveTask) {
		t.Fatalf("live-but-idle owner with a stale active task must be flagged:\n%s", out)
	}
	// Finding #2: the event must carry busy/liveness evidence distinguishing
	// confirmed-not-busy from unknown.
	for _, want := range []string{`"idle_evidence"`, `"busy_known":true`, `"busy":false`, `"owner_status":"live"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("idle event missing busy evidence %q:\n%s", want, out)
		}
	}
}

func TestMonitorIdleFlagsUndrainedInboxWithLiveStaleOwner(t *testing.T) {
	dir := seedTeam(t, team.Team{Members: []team.Member{{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "s"}}})
	withMonitorOperatorState(t, 0, 0)
	withMonitorStatusRows(t, []statusRecord{{Handle: "fullstack", Status: statusStateLive, Tmux: &tmuxRuntimeJSON{PaneID: "%5", PaneAlive: true}}})
	withMonitorPaneBusy(t, false, true) // live, not busy
	seedInProgressTask(t, dir, "s", "fullstack", time.Now().Add(-1*time.Hour))
	seedUnreadMonitorInboxMessage(t, dir, "s", "fullstack")

	out, err := runMonitorOnce(t)
	if err != nil {
		t.Fatalf("monitor: %v", err)
	}
	for _, want := range []string{
		monitorEventIdleActiveTask,
		`"unread_inbox":1`,
		"owner has 1 unread inbox message",
		"owner:fullstack",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("undrained inbox + idle active task event missing %q:\n%s", want, out)
		}
	}
}

func TestMonitorIdleSuppressedWhenBusyUnknown(t *testing.T) {
	dir := seedTeam(t, team.Team{Members: []team.Member{{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "s"}}})
	withMonitorOperatorState(t, 0, 0)
	withMonitorStatusRows(t, []statusRecord{{Handle: "fullstack", Status: statusStateLive, Tmux: &tmuxRuntimeJSON{PaneID: "%5", PaneAlive: true}}})
	withMonitorPaneBusy(t, false, false) // busy state UNKNOWN (e.g. tmux access denied)
	seedInProgressTask(t, dir, "s", "fullstack", time.Now().Add(-2*time.Hour))

	out, err := runMonitorOnce(t)
	if err != nil {
		t.Fatalf("monitor: %v", err)
	}
	if strings.Contains(out, monitorEventIdleActiveTask) {
		t.Fatalf("a live owner whose busy state is UNKNOWN must not be classified idle:\n%s", out)
	}
	if !strings.Contains(out, `"events_found":false`) {
		t.Fatalf("expected idle tick when busy is unresolved:\n%s", out)
	}
}

func TestMonitorIdleSuppressedWhenLiveOwnerHasNoPane(t *testing.T) {
	dir := seedTeam(t, team.Team{Members: []team.Member{{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "s"}}})
	withMonitorOperatorState(t, 0, 0)
	// Live by record but no live pane → busy cannot be probed → unresolved → no flag.
	withMonitorStatusRows(t, []statusRecord{{Handle: "fullstack", Status: statusStateWakeLive}})
	seedInProgressTask(t, dir, "s", "fullstack", time.Now().Add(-2*time.Hour))

	out, err := runMonitorOnce(t)
	if err != nil {
		t.Fatalf("monitor: %v", err)
	}
	if strings.Contains(out, monitorEventIdleActiveTask) {
		t.Fatalf("a live owner with no inspectable pane must not be classified idle:\n%s", out)
	}
}

func TestMonitorIdleSuppressedForFreshTask(t *testing.T) {
	dir := seedTeam(t, team.Team{Members: []team.Member{{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "s"}}})
	withMonitorOperatorState(t, 0, 0)
	withMonitorStatusRows(t, []statusRecord{{Handle: "fullstack", Status: statusStateLive, Tmux: &tmuxRuntimeJSON{PaneID: "%5", PaneAlive: true}}})
	withMonitorPaneBusy(t, false, true)
	seedInProgressTask(t, dir, "s", "fullstack", time.Now()) // fresh: within --stale-after

	out, err := runMonitorOnce(t)
	if err != nil {
		t.Fatalf("monitor: %v", err)
	}
	if strings.Contains(out, monitorEventIdleActiveTask) {
		t.Fatalf("a fresh (within --stale-after) in_progress task must not be flagged:\n%s", out)
	}
}

func TestMonitorIdleSuppressedWhenOtherOperatorEvent(t *testing.T) {
	dir := seedTeam(t, team.Team{Members: []team.Member{{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "s"}}})
	// An open operator gate (and unread inbox) means the operator is already
	// being pulled back; idle must not also fire even though the owner is not-live.
	withMonitorOperatorState(t, 1, 0)
	withMonitorStatusRows(t, []statusRecord{{Handle: "fullstack", Status: statusStateMissing}})
	seedInProgressTask(t, dir, "s", "fullstack", time.Now().Add(-2*time.Hour))

	out, err := runMonitorOnce(t)
	if err != nil {
		t.Fatalf("monitor: %v", err)
	}
	if strings.Contains(out, monitorEventIdleActiveTask) {
		t.Fatalf("idle must be suppressed when another operator-needed event fired:\n%s", out)
	}
	if !strings.Contains(out, monitorEventOpenGate) {
		t.Fatalf("the open gate event should still fire:\n%s", out)
	}
}

func TestMonitorIdleFailsClosedOnStatusReadError(t *testing.T) {
	dir := seedTeam(t, team.Team{Members: []team.Member{{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "s"}}})
	withMonitorOperatorState(t, 0, 0)
	prev := monitorStatusRows
	monitorStatusRows = func(projectDir, profile, session string) ([]statusRecord, error) {
		return nil, errMonitorStatusTest
	}
	t.Cleanup(func() { monitorStatusRows = prev })
	seedInProgressTask(t, dir, "s", "fullstack", time.Now())

	_, err := runMonitorOnce(t)
	if err == nil {
		t.Fatal("a status/liveness read failure during idle assessment must fail closed")
	}
}

var errMonitorStatusTest = errMonitorStatus("status read failed")

type errMonitorStatus string

func (e errMonitorStatus) Error() string { return string(e) }
