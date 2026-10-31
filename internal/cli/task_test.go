package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

func withFixedTaskNow(t *testing.T) time.Time {
	t.Helper()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	prev := taskNow
	taskNow = func() time.Time { return now }
	t.Cleanup(func() { taskNow = prev })
	return now
}

func taskCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	out, _, err := captureOutput(t, func() error { return runTask(args) })
	return out, err
}

func TestTaskCLIFlowAtTheSessionRoot(t *testing.T) {
	root := t.TempDir()
	chdir(t, root)
	withFixedTaskNow(t)
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_ME", "")
	if _, err := taskCLI(t, "add", "--title", "Fix the parser", "--class", "implement", "--files", "internal/parse/", "--pool", "claude:sonnet:medium", "--me", "lead"); err != nil {
		t.Fatal(err)
	}
	if _, err := taskCLI(t, "add", "--title", "Review it", "--class", "review", "--depends-on", "t1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "extensions", "amq-squad", "tasks", "t1.json")); err != nil {
		t.Fatalf("the store is not under the session root: %v", err)
	}
	out, err := taskCLI(t, "claim", "--me", "impl-1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	claimed := decodeJSONEnvelope[taskEnvelope](t, out).Data.Task
	if claimed.ID != "t1" || claimed.Lease == nil || claimed.Lease.Holder != "impl-1" || claimed.Lease.ClaimedAt == nil {
		t.Fatalf("claim = %+v", claimed)
	}
	if _, err := taskCLI(t, "claim", "--me", "impl-2"); err == nil {
		t.Fatal("impl-2 pulled t2 before its dependency completed")
	}
	if _, err := taskCLI(t, "done", "t1", "--me", "impl-1", "--evidence", "go test ok"); err != nil {
		t.Fatal(err)
	}
	shown, err := taskCLI(t, "show", "t1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeJSONEnvelope[taskEnvelope](t, shown).Data.Task; got.State != task.StateInProgress || got.Report == nil {
		t.Fatalf("a task with declared files must wait for the commit step: %+v", got)
	}
	list, err := taskCLI(t, "list")
	if err != nil || !strings.Contains(list, "t1") || !strings.Contains(list, "claude:sonnet:medium") {
		t.Fatalf("list (%v):\n%s", err, list)
	}
	raw, err := taskCLI(t, "list", "--json", "--state", "pending")
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Kind string           `json:"kind"`
		Data taskListEnvelope `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &env); err != nil || env.Kind != "tasks" || len(env.Data.Tasks) != 1 || env.Data.Tasks[0].ID != "t2" {
		t.Fatalf("list --json --state pending = %s (%v)", raw, err)
	}
}

func TestTaskCLIRefusals(t *testing.T) {
	root := t.TempDir()
	chdir(t, root)
	withFixedTaskNow(t)
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_ME", "")
	if _, err := taskCLI(t, "add", "--title", "x", "--class", "script"); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"add", "--title", "no class"}, "class"},
		{[]string{"add", "--title", "x", "--class", "deploy"}, "closed list"},
		{[]string{"claim", "t1", "--me", "user"}, "non-runnable mailbox participant"},
		{[]string{"done", "t1"}, "requires --me"},
		{[]string{"done", "t1", "--me", "h", "--reason", "x"}, "does not take --reason"},
		{[]string{"fail", "t1", "--me", "h"}, "requires --reason"},
		{[]string{"done", "--me", "h"}, "requires a task id"},
		{[]string{"set", "t1", "--me", "lead"}, "needs --priority or --pool"},
		{[]string{"claim", "t1", "--me", "h", "--pool", "nocolons"}, "runtime:model:effort"},
		{[]string{"bogus"}, "bogus"},
	}
	for _, c := range cases {
		if _, err := taskCLI(t, c.args...); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("task %v: err %v, want %q", c.args, err, c.want)
		}
	}
	t.Setenv("AM_ROOT", "")
	if _, err := taskCLI(t, "list"); err == nil || !strings.Contains(err.Error(), "no store root") {
		t.Errorf("list without a root: %v", err)
	}
}

func TestTaskCLIReconcileListsExpiredLeases(t *testing.T) {
	root := t.TempDir()
	chdir(t, root)
	now := withFixedTaskNow(t)
	t.Setenv("AM_ROOT", root)
	s, _ := task.Open(root)
	if _, err := s.Add(task.AddInput{Title: "a", Class: "script"}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Grant("t1", "ghost", nil, time.Minute, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	out, err := taskCLI(t, "reconcile")
	if err != nil || !strings.Contains(out, "1 expired leases") || !strings.Contains(out, "t1 held by ghost") {
		t.Fatalf("reconcile (%v):\n%s", err, out)
	}
}

// 3.0.2: the re-pin refusal after the first claim names the lead's way out
// (task reset --pool).
func TestTaskCLISetRepinRefusalNamesReset(t *testing.T) {
	root := t.TempDir()
	chdir(t, root)
	withFixedTaskNow(t)
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_ME", "")
	if _, err := taskCLI(t, "add", "--title", "x", "--class", "script", "--pool", "claude:haiku:low", "--me", "lead"); err != nil {
		t.Fatal(err)
	}
	if _, err := taskCLI(t, "claim", "t1", "--me", "seat"); err != nil {
		t.Fatal(err)
	}
	_, err := taskCLI(t, "set", "t1", "--pool", "claude:sonnet:medium", "--me", "lead")
	if err == nil || !strings.HasSuffix(err.Error(), "use task reset --pool") {
		t.Fatalf("re-pin after the first claim: %v", err)
	}
}
