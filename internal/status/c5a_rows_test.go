package status

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/state"
)

// C5a PR 1 (the launch screen, internal/status/screen.go seatLaunch): a
// seat's exec_refused outcome comes from seat-exec's exec record under the
// staged directory; a refusal the seat wrote in its own agent directory is
// not reported.
func TestC5aASeatWrittenRefusalIsNotTheLaunchOutcome(t *testing.T) {
	root := t.TempDir()
	staged := launch.Record{Handle: "cto", Root: root, LaunchID: "L1", Apply: &launch.Apply{Outcome: "applied"}}
	refused := staged
	refused.Exec = &launch.Exec{Outcome: "refused", ReasonCode: "identity_proof_failed"}
	planted := filepath.Join(root, "agents", "cto", "extensions", launch.LayerName)
	if err := os.MkdirAll(planted, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := launch.WriteFile(filepath.Join(planted, launch.FileName), refused); err != nil {
		t.Fatal(err)
	}
	if sl := seatLaunch(root, "cto", staged); sl.Outcome != "applied" {
		t.Errorf("a refusal cto wrote in its own agent directory: %s %s", sl.Outcome, sl.ReasonCode)
	}
	execDir := filepath.Join(launch.StagedDir(root, "cto"), "exec")
	if err := os.MkdirAll(execDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := launch.WriteFile(filepath.Join(execDir, launch.FileName), refused); err != nil {
		t.Fatal(err)
	}
	if sl := seatLaunch(root, "cto", staged); sl.Outcome != "exec_refused" || sl.ReasonCode != "identity_proof_failed" {
		t.Errorf("seat-exec's refusal: %s %s, want exec_refused identity_proof_failed", sl.Outcome, sl.ReasonCode)
	}
}

// C5a PR 1 (Codex pass 1, finding 3): an earlier launch's exec record, left
// under the staged directory by a restage whose seat-exec has not run, does
// not join the seat: its role, runtime and model are the staged launch's.
func TestC5aAnEarlierLaunchsExecRecordDoesNotJoinTheSeat(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "agents", "cto"), 0o700); err != nil {
		t.Fatal(err)
	}
	staged := launch.StagedDir(root, "cto")
	if err := os.MkdirAll(filepath.Join(staged, "exec"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := launch.WriteFile(filepath.Join(staged, launch.FileName), launch.Record{Handle: "cto", Root: root, LaunchID: "L2", Role: "reviewer", Runtime: "claude", Model: "opus", StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := launch.WriteFile(filepath.Join(staged, "exec", launch.FileName), launch.Record{Handle: "cto", Root: root, LaunchID: "L1", Role: "implementer", Runtime: "codex", Model: "gpt-x", AgentPID: os.Getpid(), StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	o := CollectOptions{Root: root, Now: time.Now(), PIDAlive: func(int) bool { return true }}
	seat, _ := collectSeat(o, "cto", state.Message{}, func(string, ...any) {})
	if seat.Role != "reviewer" || seat.Runtime != "claude" || seat.Model != "opus" || seat.Live {
		t.Errorf("seat %+v: an earlier launch's exec record joined it", seat)
	}
}
