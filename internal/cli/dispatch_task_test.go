package cli

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	taskstore "github.com/omriariav/amq-squad/v3/internal/task"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// dispatchTestStore is the task store under the test team's AMQ session
// root (the seams resolve the root to <dir>/.agent-mail/<session>).
func dispatchTestStore(t *testing.T, dir, session string) taskstore.Store {
	t.Helper()
	s, err := taskstore.Open(filepath.Join(dir, ".agent-mail", session))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDispatchCreateTaskLeasesBeforeTheDoorbell(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	store := dispatchTestStore(t, dir, "issue-96")
	_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-abc to qa\n")
	previousRun := runAMQCommand
	runAMQCommand = func(req amqCommandRequest) ([]byte, error) {
		got, err := store.Get("t1")
		if err != nil {
			t.Fatalf("task must exist before the AMQ send: %v", err)
		}
		if got.State != taskstore.StateInProgress || got.Lease == nil || got.Lease.Holder != "qa" || got.Lease.ClaimedAt != nil {
			t.Fatalf("the doorbell ran before the unclaimed lease to qa: %+v", got)
		}
		return previousRun(req)
	}
	t.Cleanup(func() { runAMQCommand = previousRun })

	stdout, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "Validate", "--body", "run",
			"--create-task", "--task-class", "review", "--task-files", "docs/", "--task-pool", "claude:sonnet:medium", "--json"})
	})
	if err != nil {
		t.Fatalf("dispatch --create-task: %v", err)
	}
	result := decodeJSONEnvelope[mutationResult](t, stdout).Data
	if result.TaskID != "t1" || result.MessageID != "msg-abc" || result.Assignee != "qa" {
		t.Fatalf("dispatch result = %+v", result)
	}
	got, err := store.Get("t1")
	if err != nil || got.Class != "review" || strings.Join(got.DeclaredFiles, ",") != "docs/" || got.Pool == nil || got.Pool.Key() != "claude:sonnet:medium" || got.CreatedBy != "cto" {
		t.Fatalf("created task = %+v err=%v", got, err)
	}
}

func TestDispatchCreateTaskRequiresAClass(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent x to qa\n")
	_, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "x", "--body", "y", "--create-task"})
	})
	if err == nil || !strings.Contains(err.Error(), "--task-class") || len(*calls) != 0 {
		t.Fatalf("err=%v calls=%d", err, len(*calls))
	}
}

// A failed doorbell leaves the lease: the lease, not the mail, decides. A
// second dispatch to the same seat accepts its existing lease.
func TestDispatchSendFailureKeepsTheLease(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	calls := withDispatchAMQCommandErrorSeam(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "", nil)
	attempt := 0
	runAMQCommand = func(req amqCommandRequest) ([]byte, error) {
		*calls = append(*calls, req)
		attempt++
		if attempt == 1 {
			return nil, errors.New("transport unavailable")
		}
		return []byte("Sent msg-second to qa\n"), nil
	}
	_, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "plain task", "--body", "do the work", "--create-task", "--task-class", "script"})
	})
	if err == nil || !strings.Contains(err.Error(), "the lease stays") {
		t.Fatalf("first dispatch error = %v", err)
	}
	store := dispatchTestStore(t, dir, "issue-96")
	got, err := store.Get("t1")
	if err != nil || got.State != taskstore.StateInProgress || got.Lease == nil || got.Lease.Holder != "qa" {
		t.Fatalf("after a failed send: %+v %v", got, err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "plain task", "--body", "do the work", "--task", "t1"})
	}); err != nil {
		t.Fatalf("second dispatch of the leased task: %v", err)
	}
	again, _ := store.Get("t1")
	if again.Attempt != 1 || again.Lease.Holder != "qa" {
		t.Fatalf("the second dispatch changed the lease: %+v", again)
	}
}

func TestDispatchTaskRefusesBeforeSend(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		name  string
		setup func(t *testing.T, s taskstore.Store) string
		want  string
	}{
		{"leased to another seat", func(t *testing.T, s taskstore.Store) string {
			mustTask(t, s, "a")
			if _, err := s.Grant("t1", "cto", nil, time.Hour, now); err != nil {
				t.Fatal(err)
			}
			return "t1"
		}, "task t1 is leased to cto"},
		{"dependency gated", func(t *testing.T, s taskstore.Store) string {
			mustTask(t, s, "a")
			if _, err := s.Add(taskstore.AddInput{Title: "b", Class: "script", DependsOn: []string{"t1"}}, now); err != nil {
				t.Fatal(err)
			}
			return "t2"
		}, "dependencies"},
		{"blocked", func(t *testing.T, s taskstore.Store) string {
			mustTask(t, s, "a")
			if _, err := s.Block("t1", "cto", "needs_decision", "", "", now); err != nil {
				t.Fatal(err)
			}
			return "t1"
		}, "task t1 is blocked"},
		{"cancelled", func(t *testing.T, s taskstore.Store) string {
			mustTask(t, s, "a")
			if _, err := s.Cancel("t1", "cto", "superseded", now); err != nil {
				t.Fatal(err)
			}
			return "t1"
		}, "task t1 is cancelled"},
		{"completed", func(t *testing.T, s taskstore.Store) string {
			mustTask(t, s, "a")
			if _, err := s.Claim("t1", "qa", nil, time.Hour, now); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Done("t1", "qa", "ok", "", now); err != nil {
				t.Fatal(err)
			}
			return "t1"
		}, "task t1 is completed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			chdir(t, dir)
			writeDispatchTeam(t, dir)
			id := tc.setup(t, dispatchTestStore(t, dir, "issue-96"))
			calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg to qa\n")
			_, _, err := captureOutput(t, func() error {
				return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "Validate", "--body", "run", "--task", id})
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if len(*calls) != 0 {
				t.Fatalf("a refused dispatch sent AMQ: %d calls", len(*calls))
			}
		})
	}
}

func mustTask(t *testing.T, s taskstore.Store, title string) taskstore.Task {
	t.Helper()
	got, err := s.Add(taskstore.AddInput{Title: title, Class: "script"}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestDispatchRejectsLegacyLeadershipEpochBeforeArtifacts(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-epoch to qa\n")
	_, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--leadership-epoch", "1", "--subject", "legacy", "--body", "run", "--create-task", "--task-class", "script"})
	})
	if err == nil || !strings.Contains(err.Error(), "--leadership-epoch is unavailable in simple task mode") {
		t.Fatalf("legacy leadership flag err=%v", err)
	}
	if tasks, err := dispatchTestStore(t, dir, "issue-96").List(); err != nil || len(tasks) != 0 {
		t.Fatalf("legacy flag refusal persisted tasks: %+v err=%v", tasks, err)
	}
	if len(*calls) != 0 {
		t.Fatalf("legacy flag refusal invoked AMQ: %d", len(*calls))
	}
}

func TestDispatchRefusesImplementForAReviewActorBeforeSideEffects(t *testing.T) {
	for _, path := range []string{"create-task", "existing-task"} {
		t.Run(path, func(t *testing.T) {
			dir := t.TempDir()
			chdir(t, dir)
			writeDispatchActorContractTeam(t, dir)
			calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent forbidden to reviewer\n")
			store := dispatchTestStore(t, dir, "actor-contract")
			args := []string{"--session", "actor-contract", "--role", "reviewer", "--subject", "Forbidden", "--body", "do not run"}
			var before taskstore.Task
			if path == "create-task" {
				args = append(args, "--create-task", "--task-class", "implement")
			} else {
				created, err := store.Add(taskstore.AddInput{Title: "existing forbidden", Class: "implement"}, taskNow())
				if err != nil {
					t.Fatal(err)
				}
				before = created
				args = append(args, "--task", created.ID)
			}
			_, _, err := captureOutput(t, func() error { return runDispatch(args) })
			if err == nil || !strings.Contains(err.Error(), "EffectiveActorMode=review") || !strings.Contains(err.Error(), "current_actor_implementation_allowed=false") {
				t.Fatalf("review actor %s refusal = %v", path, err)
			}
			if len(*calls) != 0 {
				t.Fatalf("review actor refusal sent AMQ: %v", *calls)
			}
			tasks, err := store.List()
			if err != nil {
				t.Fatal(err)
			}
			if path == "create-task" && len(tasks) != 0 {
				t.Fatalf("create-task refusal persisted a task: %+v", tasks)
			}
			if path == "existing-task" && !reflect.DeepEqual(tasks[0], before) {
				t.Fatalf("existing-task refusal mutated the task: before=%+v after=%+v", before, tasks[0])
			}
			if _, statErr := os.Stat(filepath.Join(dir, team.DirName, "receipts", "actor-contract")); !os.IsNotExist(statErr) {
				t.Fatalf("review actor refusal persisted a receipt artifact: %v", statErr)
			}
		})
	}
}
