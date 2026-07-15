package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	activitystore "github.com/omriariav/amq-squad/v2/internal/activity"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func writeMonitorActivity(t *testing.T, project, profile, session, dirHandle string, file activitystore.File) {
	t.Helper()
	root := filepath.Join(project, ".agent-mail")
	if profile != team.DefaultProfile {
		root = filepath.Join(root, profile)
	}
	agentDir := filepath.Join(root, session, "agents", dirHandle)
	if err := activitystore.Write(agentDir, file); err != nil {
		t.Fatalf("write activity: %v", err)
	}
}

func TestMonitorExactCodingActivityUsesExtendedThreshold(t *testing.T) {
	dir := seedTeam(t, team.Team{Members: []team.Member{{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "s"}}})
	withMonitorOperatorState(t, 0, 0)
	withMonitorStatusRows(t, []statusRecord{{Handle: "fullstack", Status: statusStateLive, Tmux: &tmuxRuntimeJSON{PaneID: "%5", PaneAlive: true}}})
	withMonitorPaneBusy(t, false, true)
	taskID := seedInProgressTask(t, dir, "s", "fullstack", time.Now().Add(-2*time.Hour))
	writeMonitorActivity(t, dir, team.DefaultProfile, "s", "fullstack", activitystore.File{
		Handle: "fullstack", TaskID: taskID, Phase: "coding", WrittenAt: time.Now().Add(-20 * time.Minute),
	})

	out, err := runMonitorOnce(t)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, monitorEventIdleActiveTask) {
		t.Fatalf("exact coding activity inside its extended threshold must suppress escalation:\n%s", out)
	}
}

func TestMonitorActivityClaimsFailClosed(t *testing.T) {
	tests := []struct {
		name       string
		file       func(taskID string) activitystore.File
		session    string
		wantReason string
	}{
		{name: "task mismatch", session: "s", file: func(string) activitystore.File {
			return activitystore.File{Handle: "fullstack", TaskID: "t999", Phase: "coding", WrittenAt: time.Now()}
		}, wantReason: "canonical task"},
		{name: "actor mismatch", session: "s", file: func(taskID string) activitystore.File {
			return activitystore.File{Handle: "other", TaskID: taskID, Phase: "coding", WrittenAt: time.Now()}
		}, wantReason: "exact assignee"},
		{name: "future skew", session: "s", file: func(taskID string) activitystore.File {
			return activitystore.File{Handle: "fullstack", TaskID: taskID, Phase: "testing", WrittenAt: time.Now().Add(time.Hour)}
		}, wantReason: "future-skewed"},
		{name: "arbitrary phase", session: "s", file: func(taskID string) activitystore.File {
			return activitystore.File{Handle: "fullstack", TaskID: taskID, Phase: "totally-busy", WrittenAt: time.Now()}
		}, wantReason: "bounded phase catalog"},
		{name: "session mismatch", session: "other", file: func(taskID string) activitystore.File {
			return activitystore.File{Handle: "fullstack", TaskID: taskID, Phase: "coding", WrittenAt: time.Now()}
		}, wantReason: "heartbeat absent"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := seedTeam(t, team.Team{Members: []team.Member{{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "s"}}})
			withMonitorOperatorState(t, 0, 0)
			withMonitorStatusRows(t, []statusRecord{{Handle: "fullstack", Status: statusStateLive, Tmux: &tmuxRuntimeJSON{PaneID: "%5", PaneAlive: true}}})
			withMonitorPaneBusy(t, false, true)
			taskID := seedInProgressTask(t, dir, "s", "fullstack", time.Now().Add(-2*time.Hour))
			writeMonitorActivity(t, dir, team.DefaultProfile, tc.session, "fullstack", tc.file(taskID))

			out, err := runMonitorOnce(t)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{monitorEventIdleActiveTask, `"activity_valid":false`, tc.wantReason, `"base_threshold_seconds":900`} {
				if !strings.Contains(out, want) {
					t.Fatalf("non-authoritative activity must not suppress and must expose %q:\n%s", want, out)
				}
			}
		})
	}
}

func TestMonitorMalformedActivityDoesNotSuppress(t *testing.T) {
	dir := seedTeam(t, team.Team{Members: []team.Member{{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "s"}}})
	withMonitorOperatorState(t, 0, 0)
	withMonitorStatusRows(t, []statusRecord{{Handle: "fullstack", Status: statusStateLive, Tmux: &tmuxRuntimeJSON{PaneID: "%5", PaneAlive: true}}})
	withMonitorPaneBusy(t, false, true)
	seedInProgressTask(t, dir, "s", "fullstack", time.Now().Add(-2*time.Hour))
	agentDir := filepath.Join(dir, ".agent-mail", "s", "agents", "fullstack")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, activitystore.Filename), []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := runMonitorOnce(t)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{monitorEventIdleActiveTask, `"activity_valid":false`, "heartbeat malformed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("malformed heartbeat must be non-authoritative and visible as evidence %q:\n%s", want, out)
		}
	}
}

func TestMonitorIdleEventExposesStaleExactTestingEvidence(t *testing.T) {
	dir := seedTeam(t, team.Team{Members: []team.Member{{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "s"}}})
	withMonitorOperatorState(t, 0, 0)
	withMonitorStatusRows(t, []statusRecord{{Handle: "fullstack", Status: statusStateLive, Tmux: &tmuxRuntimeJSON{PaneID: "%5", PaneAlive: true}}})
	withMonitorPaneBusy(t, false, true)
	taskID := seedInProgressTask(t, dir, "s", "fullstack", time.Now().Add(-2*time.Hour))
	writeMonitorActivity(t, dir, team.DefaultProfile, "s", "fullstack", activitystore.File{
		Handle: "fullstack", TaskID: taskID, Phase: "testing", WrittenAt: time.Now().Add(-61 * time.Minute),
	})

	out, err := runMonitorOnce(t)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"activity_source":"heartbeat-file"`, `"activity_phase":"testing"`, `"activity_valid":true`, `"effective_threshold_seconds":3600`} {
		if !strings.Contains(out, want) {
			t.Fatalf("idle event missing exact stale activity evidence %q:\n%s", want, out)
		}
	}
}

func TestMonitorJSONFinalSnapshotsCoverBoundsAndErrors(t *testing.T) {
	t.Run("max ticks", func(t *testing.T) {
		seedTeam(t, team.Team{Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"}}})
		withMonitorOperatorState(t, 0, 0)
		stdout, _, err := captureOutput(t, func() error {
			return runMonitor([]string{"--session", "s", "--interval", "1ms", "--timeout", "0", "--max-ticks", "2", "--json"})
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{`"schema_version":1`, `"kind":"monitor_final"`, `"exit_reason":"max_ticks"`, `"ticks":2`, `"max_ticks":2`} {
			if !strings.Contains(stdout, want) {
				t.Fatalf("bounded final snapshot missing %q:\n%s", want, stdout)
			}
		}
	})

	t.Run("source error", func(t *testing.T) {
		dir := seedTeam(t, team.Team{Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"}}})
		withMonitorOperatorState(t, 0, 0)
		if err := os.WriteFile(filepath.Join(dir, ".amq-squad", "evidence"), []byte("broken"), 0o600); err != nil {
			t.Fatal(err)
		}
		stdout, _, err := captureOutput(t, func() error { return runMonitor([]string{"--session", "s", "--once", "--json"}) })
		if err == nil {
			t.Fatal("broken source must return an error")
		}
		for _, want := range []string{`"kind":"monitor_error"`, `"kind":"monitor_final"`, `"exit_reason":"source_error"`, `"ticks":0`} {
			if !strings.Contains(stdout, want) {
				t.Fatalf("error final snapshot missing %q:\n%s", want, stdout)
			}
		}
	})
}

func TestMonitorRejectsUnboundedLoop(t *testing.T) {
	seedTeam(t, team.Team{Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"}}})
	_, _, err := captureOutput(t, func() error {
		return runMonitor([]string{"--session", "s", "--timeout", "0", "--max-ticks", "0"})
	})
	if err == nil || !strings.Contains(err.Error(), "must be bounded") {
		t.Fatalf("unbounded monitor should fail before polling, got %v", err)
	}
}
