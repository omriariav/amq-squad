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

func TestStructuredDispatchContractBindsActorsAndArtifactOwnership(t *testing.T) {
	dir := t.TempDir()
	contract := AddInput{
		Title: "implement", Intent: IntentImplement, Artifact: "internal/task",
		ExpectedBaseSHA: "0835361c6869da35067b1c2542c98579876595fa",
		Implementer:     "dev", Reviewer: "reviewer",
	}
	tk, err := Add(dir, "s", contract, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if tk.Intent != IntentImplement || tk.Artifact != "internal/task" || tk.Implementer != "dev" || tk.Reviewer != "reviewer" {
		t.Fatalf("structured task lost contract: %+v", tk)
	}
	if _, err := Claim(dir, "s", tk.ID, "reviewer", fixedNow); err == nil {
		t.Fatal("reviewer claimed implementation mutation authority")
	}
	if _, err := Claim(dir, "s", tk.ID, "dev", fixedNow); err != nil {
		t.Fatalf("declared implementer claim: %v", err)
	}
	if _, err := Add(dir, "s", contract, fixedNow); err == nil {
		t.Fatal("competing implementation for one artifact was accepted implicitly")
	}
	contract.Title = "parallel experiment"
	contract.ParallelWorkExplicit = true
	if _, err := Add(dir, "s", contract, fixedNow); err != nil {
		t.Fatalf("explicit parallel implementation: %v", err)
	}
}

func TestStructuredReviewClaimIsReviewerOnly(t *testing.T) {
	dir := t.TempDir()
	tk, err := Add(dir, "s", AddInput{
		Title: "review", Intent: IntentReview, Artifact: "internal/task",
		ExpectedBaseSHA: "0835361c6869da35067b1c2542c98579876595fa",
		Implementer:     "dev", Reviewer: "reviewer",
	}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Claim(dir, "s", tk.ID, "dev", fixedNow); err == nil {
		t.Fatal("implementer claimed independent review")
	}
	if _, err := Claim(dir, "s", tk.ID, "reviewer", fixedNow); err != nil {
		t.Fatalf("declared reviewer claim: %v", err)
	}
}

func TestStructuredTaskRejectsAssigneeAuthorityMismatchBeforePersistence(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct{ intent, assign string }{
		{IntentImplement, "reviewer"}, {IntentReview, "dev"}, {IntentAudit, "dev"}, {IntentLifecycle, "reviewer"},
	} {
		_, err := Add(dir, "s", AddInput{
			Title: tc.intent, Intent: tc.intent, Artifact: "release/v2.22.0",
			ExpectedBaseSHA: "0835361c6869da35067b1c2542c98579876595fa",
			Implementer:     "dev", Reviewer: "reviewer", AssignTo: tc.assign,
		}, fixedNow)
		if err == nil {
			t.Fatalf("%s mismatched assignee was persisted", tc.intent)
		}
	}
	listed, err := List(dir, "s")
	if err != nil || len(listed) != 0 {
		t.Fatalf("rejected contracts changed store: tasks=%+v err=%v", listed, err)
	}
}

func TestStructuredLifecycleClaimUsesImplementerAuthority(t *testing.T) {
	dir := t.TempDir()
	tk, err := Add(dir, "s", AddInput{
		Title: "prepare tag", Intent: IntentLifecycle, Artifact: "refs/tags/v2.22.0",
		ExpectedBaseSHA: "0835361c6869da35067b1c2542c98579876595fa",
		Implementer:     "release-lead", Reviewer: "operator-reviewer", AssignTo: "release-lead",
	}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Claim(dir, "s", tk.ID, "operator-reviewer", fixedNow); err == nil {
		t.Fatal("reviewer claimed lifecycle execution authority")
	}
	if _, err := Claim(dir, "s", tk.ID, "release-lead", fixedNow); err != nil {
		t.Fatalf("declared lifecycle implementer claim: %v", err)
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

func TestAttentionLifecycleProjection(t *testing.T) {
	for _, tc := range []struct {
		name     string
		task     Task
		want     AttentionLifecycle
		terminal bool
	}{
		{name: "pending", task: Task{Status: StatusPending}, want: AttentionLifecycle(StatusPending)},
		{name: "in progress", task: Task{Status: StatusInProgress}, want: AttentionLifecycle(StatusInProgress)},
		{name: "failed remains attention bearing", task: Task{Status: StatusFailed}, want: AttentionLifecycle(StatusFailed)},
		{name: "blocked remains attention bearing", task: Task{Status: StatusBlocked}, want: AttentionLifecycle(StatusBlocked)},
		{name: "completed", task: Task{Status: StatusCompleted}, want: AttentionLifecycleClosed, terminal: true},
		{name: "cancelled", task: Task{Status: StatusCancelled}, want: AttentionLifecycleClosed, terminal: true},
		{name: "replacement linked", task: Task{Status: StatusCancelled, ReplacedBy: "t2"}, want: AttentionLifecycleSuperseded, terminal: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := AttentionLifecycleFor(tc.task); got != tc.want {
				t.Fatalf("AttentionLifecycleFor = %q, want %q", got, tc.want)
			}
			if got := IsAttentionLifecycleTerminal(tc.task); got != tc.terminal {
				t.Fatalf("IsAttentionLifecycleTerminal = %t, want %t", got, tc.terminal)
			}
		})
	}
}
