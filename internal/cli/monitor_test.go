package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	taskstore "github.com/omriariav/amq-squad/v2/internal/task"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func withMonitorOperatorState(t *testing.T, openGates, unread int) {
	t.Helper()
	prev := monitorOperatorState
	monitorOperatorState = func(projectDir, profile, session string) (int, int, error) {
		return openGates, unread, nil
	}
	t.Cleanup(func() { monitorOperatorState = prev })
}

func seedMergeEvidence(t *testing.T, dir, session, issue string) {
	t.Helper()
	evDir := filepath.Join(dir, ".amq-squad", "evidence")
	if err := os.MkdirAll(evDir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := session + "-" + issue + "-merge-evidence.json"
	if err := os.WriteFile(filepath.Join(evDir, name), []byte(`{"head_sha":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMonitorOnceEmitsEvents(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"}},
	})
	seedMergeEvidence(t, dir, "s", "282")
	withMonitorOperatorState(t, 1, 0)

	stdout, _, err := captureOutput(t, func() error {
		return runMonitor([]string{"--session", "s", "--once", "--json"})
	})
	if err != nil {
		t.Fatalf("monitor --once: %v", err)
	}
	line := strings.TrimSpace(stdout)
	for _, want := range []string{`"events_found":true`, monitorEventMergeReady, `"issue":"282"`, monitorEventOpenGate} {
		if !strings.Contains(line, want) {
			t.Fatalf("monitor tick missing %q:\n%s", want, line)
		}
	}
	if strings.Contains(line, monitorEventInbox) {
		t.Fatalf("no unread inbox seeded; inbox event must not fire:\n%s", line)
	}
}

func TestMonitorHandledIssueSuppressesMergeReady(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"}},
	})
	seedMergeEvidence(t, dir, "s", "282")
	withMonitorOperatorState(t, 0, 0)

	stdout, _, err := captureOutput(t, func() error {
		return runMonitor([]string{"--session", "s", "--once", "--json", "--handled-issue", "282"})
	})
	if err != nil {
		t.Fatalf("monitor --handled-issue: %v", err)
	}
	line := strings.TrimSpace(stdout)
	if strings.Contains(line, monitorEventMergeReady) {
		t.Fatalf("handled issue 282 must not fire merge_gate_ready:\n%s", line)
	}
	if !strings.Contains(line, `"events_found":false`) {
		t.Fatalf("with the only signal handled, tick must be idle:\n%s", line)
	}
}

func TestMonitorEmitsBlockedTaskEvent(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"}},
	})
	withMonitorOperatorState(t, 0, 0)
	now := time.Now()
	tk, err := taskstore.AddForProfile(dir, team.DefaultProfile, "s", taskstore.AddInput{Title: "do x"}, now)
	if err != nil {
		t.Fatalf("add task: %v", err)
	}
	if _, err := taskstore.ClaimForProfile(dir, team.DefaultProfile, "s", tk.ID, "cto", now); err != nil {
		t.Fatalf("claim task: %v", err)
	}
	if _, err := taskstore.BlockForProfile(dir, team.DefaultProfile, "s", tk.ID, "cto", "waiting on operator", now); err != nil {
		t.Fatalf("block task: %v", err)
	}

	stdout, _, err := captureOutput(t, func() error {
		return runMonitor([]string{"--session", "s", "--once", "--json"})
	})
	if err != nil {
		t.Fatalf("monitor: %v", err)
	}
	if !strings.Contains(stdout, monitorEventBlockedTask) || !strings.Contains(stdout, "waiting on operator") {
		t.Fatalf("expected blocked task event:\n%s", stdout)
	}
}

func TestMonitorEmitsFailedTaskEvent(t *testing.T) {
	dir := seedTeam(t, team.Team{Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"}}})
	withMonitorOperatorState(t, 0, 0)
	now := time.Now()
	tk, err := taskstore.AddForProfile(dir, team.DefaultProfile, "s", taskstore.AddInput{Title: "do x"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := taskstore.ClaimForProfile(dir, team.DefaultProfile, "s", tk.ID, "cto", now); err != nil {
		t.Fatal(err)
	}
	if _, err := taskstore.FailForProfile(dir, team.DefaultProfile, "s", tk.ID, "cto", "boom", now); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := captureOutput(t, func() error { return runMonitor([]string{"--session", "s", "--once", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, monitorEventBlockedTask) || !strings.Contains(stdout, "boom") || !strings.Contains(stdout, "failed") {
		t.Fatalf("expected failed task attention event:\n%s", stdout)
	}
}

func TestMonitorSuppressesClosedAndSupersededTasks(t *testing.T) {
	dir := seedTeam(t, team.Team{Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"}}})
	withMonitorOperatorState(t, 0, 0)
	now := time.Now()

	completed, _ := taskstore.AddForProfile(dir, team.DefaultProfile, "s", taskstore.AddInput{Title: "completed"}, now)
	_, _ = taskstore.ClaimForProfile(dir, team.DefaultProfile, "s", completed.ID, "cto", now)
	if _, err := taskstore.DoneForProfile(dir, team.DefaultProfile, "s", completed.ID, "cto", "done", now); err != nil {
		t.Fatal(err)
	}
	cancelled, _ := taskstore.AddForProfile(dir, team.DefaultProfile, "s", taskstore.AddInput{Title: "cancelled"}, now)
	if _, err := taskstore.CancelForProfile(dir, team.DefaultProfile, "s", cancelled.ID, "cto", "obsolete", "", now); err != nil {
		t.Fatal(err)
	}
	superseded, _ := taskstore.AddForProfile(dir, team.DefaultProfile, "s", taskstore.AddInput{Title: "superseded"}, now)
	replacement, _ := taskstore.AddForProfile(dir, team.DefaultProfile, "s", taskstore.AddInput{Title: "replacement"}, now)
	if _, err := taskstore.CancelForProfile(dir, team.DefaultProfile, "s", superseded.ID, "cto", "replaced", replacement.ID, now); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := captureOutput(t, func() error { return runMonitor([]string{"--session", "s", "--once", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, `"events_found":false`) || strings.Contains(stdout, monitorEventBlockedTask) {
		t.Fatalf("closed/superseded tasks should not wake monitor:\n%s", stdout)
	}
}

func TestMonitorIdleLoopIsBoundedByMaxTicks(t *testing.T) {
	seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"}},
	})
	withMonitorOperatorState(t, 0, 0) // no gates, no inbox; no evidence; no tasks → all idle

	stdout, _, err := captureOutput(t, func() error {
		return runMonitor([]string{"--session", "s", "--interval", "1ms", "--max-ticks", "3", "--json"})
	})
	if err != nil {
		t.Fatalf("bounded idle loop must exit cleanly: %v", err)
	}
	ticks := 0
	for _, l := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if strings.Contains(l, `"kind":"monitor_tick"`) {
			ticks++
			if !strings.Contains(l, `"events_found":false`) {
				t.Fatalf("idle tick should report no events:\n%s", l)
			}
		}
	}
	if ticks != 3 {
		t.Fatalf("expected exactly 3 bounded idle ticks, got %d:\n%s", ticks, stdout)
	}
}

func TestMonitorFailsClosedOnOperatorReadError(t *testing.T) {
	seedTeam(t, team.Team{Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"}}})
	prev := monitorOperatorState
	monitorOperatorState = func(projectDir, profile, session string) (int, int, error) {
		return 0, 0, fmt.Errorf("amq scan failed")
	}
	t.Cleanup(func() { monitorOperatorState = prev })

	stdout, _, err := captureOutput(t, func() error {
		return runMonitor([]string{"--session", "s", "--once", "--json"})
	})
	if err == nil {
		t.Fatal("a broken operator-state read must fail closed (non-zero), not report idle")
	}
	if !strings.Contains(stdout, `"kind":"monitor_error"`) || strings.Contains(stdout, `"kind":"monitor_tick"`) {
		t.Fatalf("expected an error plus final snapshot, not an idle monitor tick:\n%s", stdout)
	}
}

func TestMonitorFailsClosedOnEvidenceReadError(t *testing.T) {
	dir := seedTeam(t, team.Team{Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"}}})
	withMonitorOperatorState(t, 0, 0)
	// Make .amq-squad/evidence a FILE so os.ReadDir fails with a non-not-exist
	// error: a broken evidence source must fail closed, not look empty/idle.
	amqDir := filepath.Join(dir, ".amq-squad")
	if err := os.MkdirAll(amqDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(amqDir, "evidence"), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := captureOutput(t, func() error {
		return runMonitor([]string{"--session", "s", "--max-ticks", "2", "--interval", "1ms", "--json"})
	})
	if err == nil {
		t.Fatal("a broken evidence dir read must fail closed (non-zero), not idle out the loop")
	}
}

func TestMonitorRequiresSession(t *testing.T) {
	seedTeam(t, team.Team{Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"}}})
	_, _, err := captureOutput(t, func() error { return runMonitor([]string{"--once"}) })
	if err == nil || !strings.Contains(err.Error(), "--session") {
		t.Fatalf("monitor without --session must error, got %v", err)
	}
}
