package task

import (
	"testing"
	"time"
)

var fixedNow = time.Date(2026, 6, 13, 16, 0, 0, 0, time.UTC)

func TestAddAllocatesSequentialIDs(t *testing.T) {
	dir := t.TempDir()
	a, err := Add(dir, "s", AddInput{Title: "first"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Add(dir, "s", AddInput{Title: "second"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != "t1" || b.ID != "t2" {
		t.Fatalf("ids = %s,%s want t1,t2", a.ID, b.ID)
	}
	if a.Status != StatusPending {
		t.Errorf("new task status = %q, want pending", a.Status)
	}
	list, err := List(dir, "s")
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %d (%v)", len(list), err)
	}
}

func TestAddRequiresTitleAndValidatesDeps(t *testing.T) {
	dir := t.TempDir()
	if _, err := Add(dir, "s", AddInput{Title: "  "}, fixedNow); err == nil {
		t.Error("empty title should be rejected")
	}
	if _, err := Add(dir, "s", AddInput{Title: "x", DependsOn: []string{"t99"}}, fixedNow); err == nil {
		t.Error("dependency on a non-existent task should be rejected")
	}
}

func TestClaimGatesOnDependencies(t *testing.T) {
	dir := t.TempDir()
	dep, _ := Add(dir, "s", AddInput{Title: "dep"}, fixedNow)
	gated, _ := Add(dir, "s", AddInput{Title: "gated", DependsOn: []string{dep.ID}}, fixedNow)

	// Cannot claim while the dependency is not completed.
	if _, err := Claim(dir, "s", gated.ID, "worker", fixedNow); err == nil {
		t.Fatal("claim should be gated until the dependency completes")
	}
	// Complete the dependency, then the gated task is claimable.
	if _, err := Claim(dir, "s", dep.ID, "worker", fixedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := Done(dir, "s", dep.ID, "worker", "", fixedNow); err != nil {
		t.Fatal(err)
	}
	got, err := Claim(dir, "s", gated.ID, "worker2", fixedNow)
	if err != nil {
		t.Fatalf("claim after dep completed: %v", err)
	}
	if got.Status != StatusInProgress || got.AssignedTo != "worker2" {
		t.Fatalf("claimed = %+v, want in_progress/worker2", got)
	}
}

func TestStateMachineTransitions(t *testing.T) {
	dir := t.TempDir()
	tk, _ := Add(dir, "s", AddInput{Title: "x"}, fixedNow)

	// done requires in_progress.
	if _, err := Done(dir, "s", tk.ID, "w", "ev", fixedNow); err == nil {
		t.Error("done on a pending task should be rejected")
	}
	if _, err := Claim(dir, "s", tk.ID, "w", fixedNow); err != nil {
		t.Fatal(err)
	}
	// claim twice rejected (no longer pending).
	if _, err := Claim(dir, "s", tk.ID, "w2", fixedNow); err == nil {
		t.Error("re-claiming an in_progress task should be rejected")
	}
	done, err := Done(dir, "s", tk.ID, "w", "shipped", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != StatusCompleted || done.Evidence != "shipped" {
		t.Fatalf("done = %+v", done)
	}
	// terminal: cannot fail a completed task.
	if _, err := Fail(dir, "s", tk.ID, "w", "no", fixedNow); err == nil {
		t.Error("fail on a completed task should be rejected")
	}
}

func TestFailAndBlockCarryReasons(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		title string
		fn    func(id string) (Task, error)
		want  string
		field func(Task) string
	}{
		{"a", func(id string) (Task, error) { return Fail(dir, "s", id, "w", "boom", fixedNow) }, StatusFailed, func(t Task) string { return t.FailureReason }},
		{"b", func(id string) (Task, error) { return Block(dir, "s", id, "w", "waiting", fixedNow) }, StatusBlocked, func(t Task) string { return t.BlockReason }},
	} {
		tk, _ := Add(dir, "s", AddInput{Title: tc.title}, fixedNow)
		if _, err := Claim(dir, "s", tk.ID, "w", fixedNow); err != nil {
			t.Fatal(err)
		}
		got, err := tc.fn(tk.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != tc.want || tc.field(got) == "" {
			t.Fatalf("%s -> %+v, want %s with reason", tc.title, got, tc.want)
		}
	}
}

func TestMutateUnknownIDErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := Claim(dir, "s", "t404", "w", fixedNow); err == nil {
		t.Error("claiming an unknown id should error")
	}
}
