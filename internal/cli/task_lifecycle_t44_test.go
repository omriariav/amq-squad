package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	taskstore "github.com/omriariav/amq-squad/v2/internal/task"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestT44AckFixtureCannotTriggerCompletionWarning(t *testing.T) {
	project := seedTeam(t, team.Team{Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"}, {Role: "platform-dev", Binary: "codex", Handle: "platform-dev", Session: "s"}}, Orchestrated: true, Lead: "cto"})
	now := time.Date(2026, 7, 17, 8, 0, 0, 0, time.UTC)
	created, err := taskstore.AddForProfile(project, team.DefaultProfile, "s", taskstore.AddInput{Title: "retrospective", AssignTo: "platform-dev"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = taskstore.LinkDispatchForProfile(project, team.DefaultProfile, "s", created.ID, taskstore.Dispatch{Sender: "cto", Assignee: "platform-dev", Thread: "p2p/cto__platform-dev", MessageID: "dispatch-t44"}, now); err != nil {
		t.Fatal(err)
	}
	if _, err = taskstore.ClaimForProfile(project, team.DefaultProfile, "s", created.ID, "platform-dev", now); err != nil {
		t.Fatal(err)
	}
	_, sourceFile, _, _ := runtime.Caller(0)
	fixture, err := os.ReadFile(filepath.Join(filepath.Dir(sourceFile), "testdata", "task_lifecycle", "t44_ack.md"))
	if err != nil {
		t.Fatal(err)
	}
	inbox := filepath.Join(project, ".agent-mail", "s", "agents", "cto", "inbox", "new")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inbox, "2026-07-17T08-27-07.910Z_pid81890_bb3a52f4.md"), fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	warnings, err := statusTaskWarnings(project, team.DefaultProfile, "s")
	if err != nil {
		t.Fatal(err)
	}
	for _, warning := range warnings {
		if warning.Kind == "task_completion_reconcile_ready" || warning.Kind == "task_completion_evidence_mismatch" || warning.Kind == "task_completion_evidence_stale" {
			t.Fatalf("t44 ACK prose triggered completion correlation: %+v", warning)
		}
	}
}
