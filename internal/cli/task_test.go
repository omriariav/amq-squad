package cli

import (
	"os"
	"strings"
	"testing"
	"time"
)

func withFixedTaskNow(t *testing.T) {
	t.Helper()
	prev := taskNow
	taskNow = func() time.Time { return time.Date(2026, 6, 13, 16, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { taskNow = prev })
}

func TestTaskAddListClaimDoneFlow(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	withFixedTaskNow(t)

	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"add", "--title", "wire limiter", "--session", "s"})
	}); err != nil {
		t.Fatalf("task add: %v", err)
	}
	// list shows the task.
	out, _, err := captureOutput(t, func() error {
		return runTask([]string{"list", "--session", "s"})
	})
	if err != nil {
		t.Fatalf("task list: %v", err)
	}
	if !strings.Contains(out, "t1") || !strings.Contains(out, "wire limiter") || !strings.Contains(out, "pending") {
		t.Fatalf("list missing the task:\n%s", out)
	}
	// claim → done.
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"claim", "t1", "--me", "worker", "--session", "s"})
	}); err != nil {
		t.Fatalf("task claim: %v", err)
	}
	out, _, err = captureOutput(t, func() error {
		return runTask([]string{"done", "t1", "--me", "worker", "--evidence", "PR#1", "--session", "s"})
	})
	if err != nil {
		t.Fatalf("task done: %v", err)
	}
	if !strings.Contains(out, "t1 is now completed") {
		t.Errorf("done output unexpected:\n%s", out)
	}
}

func TestTaskRequiresSession(t *testing.T) {
	chdir(t, t.TempDir())
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"add", "--title", "x"})
	}); err == nil || !strings.Contains(err.Error(), "--session is required") {
		t.Fatalf("want --session required, got %v", err)
	}
}

func TestTaskListJSONEnvelope(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	withFixedTaskNow(t)
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"add", "--title", "x", "--session", "s"})
	}); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := captureOutput(t, func() error {
		return runTask([]string{"list", "--json", "--session", "s"})
	})
	if err != nil {
		t.Fatalf("task list --json: %v", err)
	}
	if !strings.Contains(stdout, "\"kind\": \"tasks\"") && !strings.Contains(stdout, "\"kind\":\"tasks\"") {
		t.Errorf("expected a tasks envelope, got:\n%s", stdout)
	}
}

func TestTaskMutationJSONEnvelopes(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	withFixedTaskNow(t)
	stdout, _, err := captureOutput(t, func() error {
		return runTask([]string{"add", "--title", "x", "--assign", "worker", "--session", "s", "--json"})
	})
	if err != nil {
		t.Fatalf("task add --json: %v", err)
	}
	added := decodeJSONEnvelope[mutationResult](t, stdout)
	if added.Kind != "task_add" || added.Data.ID != "t1" || added.Data.Status != "created" || added.Data.Session != "s" {
		t.Fatalf("bad task_add envelope: %+v", added)
	}
	if strings.Contains(stdout, "added t1") {
		t.Fatalf("--json must not include human output:\n%s", stdout)
	}

	stdout, _, err = captureOutput(t, func() error {
		return runTask([]string{"claim", "t1", "--me", "worker", "--session", "s", "--json"})
	})
	if err != nil {
		t.Fatalf("task claim --json: %v", err)
	}
	claimed := decodeJSONEnvelope[mutationResult](t, stdout)
	if claimed.Kind != "task_claim" || claimed.Data.Status != "in_progress" || claimed.Data.Role != "worker" {
		t.Fatalf("bad task_claim envelope: %+v", claimed)
	}
}

func TestTaskRejectsUnsafeSession(t *testing.T) {
	chdir(t, t.TempDir())
	for _, bad := range []string{"../escape", "a/b", "..", "UP"} {
		if _, _, err := captureOutput(t, func() error {
			return runTask([]string{"add", "--title", "x", "--session", bad})
		}); err == nil || !strings.Contains(err.Error(), "invalid --session") {
			t.Errorf("session %q: want invalid --session error, got %v", bad, err)
		}
	}
}

func TestTaskTransitionRejectsInapplicableFlag(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	withFixedTaskNow(t)
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"add", "--title", "x", "--session", "s"})
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"claim", "t1", "--me", "w", "--session", "s"})
	}); err != nil {
		t.Fatal(err)
	}
	// `fail --evidence` must be a clear error, not a silent drop (--evidence
	// belongs to done, not fail).
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"fail", "t1", "--me", "w", "--evidence", "E", "--session", "s"})
	}); err == nil || !strings.Contains(err.Error(), "evidence") {
		t.Fatalf("fail --evidence should be rejected, got %v", err)
	}
}

func TestTaskShowAndReset(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	withFixedTaskNow(t)
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"add", "--title", "x", "--desc", "details", "--session", "s"})
	}); err != nil {
		t.Fatal(err)
	}
	out, _, err := captureOutput(t, func() error {
		return runTask([]string{"show", "t1", "--session", "s"})
	})
	if err != nil {
		t.Fatalf("task show: %v", err)
	}
	for _, want := range []string{"ID: t1", "Title: x", "Description: details"} {
		if !strings.Contains(out, want) {
			t.Fatalf("show output missing %q:\n%s", want, out)
		}
	}
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"claim", "t1", "--me", "worker", "--session", "s"})
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"block", "t1", "--me", "worker", "--reason", "waiting", "--session", "s"})
	}); err != nil {
		t.Fatal(err)
	}
	out, _, err = captureOutput(t, func() error {
		return runTask([]string{"reset", "t1", "--me", "worker", "--reason", "retry", "--session", "s"})
	})
	if err != nil {
		t.Fatalf("task reset: %v", err)
	}
	if !strings.Contains(out, "t1 is now pending") {
		t.Fatalf("reset output unexpected:\n%s", out)
	}
}

func TestTaskAssigneeOnlyTransitions(t *testing.T) {
	chdir(t, t.TempDir())
	withFixedTaskNow(t)
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"add", "--title", "x", "--session", "s"})
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"claim", "t1", "--me", "worker", "--session", "s"})
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"done", "t1", "--session", "s"})
	}); err == nil || !strings.Contains(err.Error(), "--me handle is required") {
		t.Fatalf("done without assignee should be rejected, got %v", err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"done", "t1", "--me", "other", "--session", "s"})
	}); err == nil || !strings.Contains(err.Error(), "assigned to worker") {
		t.Fatalf("done by non-assignee should be rejected, got %v", err)
	}
}

func TestTaskShowJSONEnvelope(t *testing.T) {
	chdir(t, t.TempDir())
	withFixedTaskNow(t)
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"add", "--title", "x", "--session", "s"})
	}); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := captureOutput(t, func() error {
		return runTask([]string{"show", "t1", "--json", "--session", "s"})
	})
	if err != nil {
		t.Fatalf("task show --json: %v", err)
	}
	if !strings.Contains(stdout, "\"kind\": \"task\"") || !strings.Contains(stdout, "\"id\": \"t1\"") {
		t.Fatalf("task show json unexpected:\n%s", stdout)
	}
}

func TestTaskRejectsExtraPositional(t *testing.T) {
	chdir(t, t.TempDir())
	// An extra positional after the flags (Go's parser stops at a leading
	// positional, so the meaningful case is a stray arg after --session).
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"list", "--session", "s", "extra"})
	}); err == nil || !strings.Contains(err.Error(), "unexpected argument") {
		t.Fatalf("extra positional should be rejected, got %v", err)
	}
}

func TestTaskUnknownSubcommandErrors(t *testing.T) {
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"bogus", "--session", "s"})
	}); err == nil || !strings.Contains(err.Error(), "unknown 'task' subcommand") {
		t.Fatalf("want unknown-subcommand error, got %v", err)
	}
}

func TestTaskClaimRequiresID(t *testing.T) {
	chdir(t, t.TempDir())
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"claim", "--session", "s", "--me", "w"})
	}); err == nil || !strings.Contains(err.Error(), "requires a task id") {
		t.Fatalf("want task-id-required error, got %v", err)
	}
}

// Ensure the task store path is project-scoped and discoverable on disk.
func TestTaskStoreOnDisk(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	withFixedTaskNow(t)
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"add", "--title", "x", "--session", "issue-9"})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir + "/.amq-squad/tasks/issue-9/t1.json"); err != nil {
		t.Fatalf("task file not written to the expected path: %v", err)
	}
}
