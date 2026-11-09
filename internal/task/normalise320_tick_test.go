// These scheduler rows live in internal/task so this task's declared file set
// stays disjoint from internal/scheduler; do not move them without revisiting that lock.
package task_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

var tick320T = time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)

func legacyTickTask(t *testing.T, taskAttempt int) (task.Store, task.Task) {
	t.Helper()
	store, err := task.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	grantedAt := tick320T.Add(-100 * time.Minute)
	tk, err := store.Add(task.AddInput{Title: "legacy tick", Class: "implement"}, grantedAt)
	if err != nil {
		t.Fatal(err)
	}
	tk, err = store.Claim(tk.ID, "dev", nil, task.DefaultLeaseDuration, grantedAt)
	if err != nil {
		t.Fatal(err)
	}
	tk.Attempt = taskAttempt
	tk.Budget = 0
	tk.Lease.GrantedAt = grantedAt
	tk.Lease.ExpiresAt = tick320T.Add(5 * time.Minute)
	tk.Lease.Attempt = 0
	tk.Lease.BudgetEndsAt = time.Time{}
	raw, err := json.MarshalIndent(tk, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.Dir, tk.ID+".json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return store, tk
}

func decisionCount(ds []scheduler.Decision, kind string) int {
	n := 0
	for _, d := range ds {
		if d.Kind == kind {
			n++
		}
	}
	return n
}

func TestSchedulerGivesAnOldLegacyAttemptOneLease(t *testing.T) {
	store, tk := legacyTickTask(t, 1)
	now := tick320T
	s := &scheduler.Scheduler{Store: store, Now: func() time.Time { return now }, Config: scheduler.Config{LeaseDuration: task.DefaultLeaseDuration}}
	first, err := s.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if decisionCount(first, "lease_normalised") != 1 || decisionCount(first, "task_budget_warning") != 1 {
		t.Fatalf("first tick decisions = %+v", first)
	}
	for _, forbidden := range []string{"task_budget_spent", "lease_expired", "seat_closing"} {
		if decisionCount(first, forbidden) != 0 {
			t.Fatalf("first tick logged %s: %+v", forbidden, first)
		}
	}
	got, err := store.Get(tk.ID)
	if err != nil || got.State != task.StateInProgress {
		t.Fatalf("task after first tick = %+v, err %v", got, err)
	}
	now = now.Add(time.Minute)
	second, err := s.Tick()
	if err != nil || decisionCount(second, "lease_normalised") != 0 {
		t.Fatalf("second tick decisions = %+v, err %v", second, err)
	}
}

func TestAttemptZeroNormalizationWritesAndLogsOnce(t *testing.T) {
	store, tk := legacyTickTask(t, 0)
	now := tick320T
	s := &scheduler.Scheduler{Store: store, Now: func() time.Time { return now }, Config: scheduler.Config{LeaseDuration: task.DefaultLeaseDuration}}
	first, err := s.Tick()
	if err != nil || decisionCount(first, "lease_normalised") != 1 {
		t.Fatalf("first tick decisions = %+v, err %v", first, err)
	}
	path := filepath.Join(store.Dir, tk.ID+".json")
	wantTask, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	wantGeneration, err := os.ReadFile(filepath.Join(store.Dir, ".generation"))
	if err != nil {
		t.Fatal(err)
	}
	for _, elapsed := range []time.Duration{time.Minute, 2 * time.Minute} {
		now = tick320T.Add(elapsed)
		got, err := s.Tick()
		if err != nil || decisionCount(got, "lease_normalised") != 0 {
			t.Fatalf("tick at %s decisions = %+v, err %v", elapsed, got, err)
		}
	}
	gotTask, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	gotGeneration, err := os.ReadFile(filepath.Join(store.Dir, ".generation"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotTask) != string(wantTask) || string(gotGeneration) != string(wantGeneration) {
		t.Fatalf("attempt-0 task was rewritten: generation %q -> %q", wantGeneration, gotGeneration)
	}
	got, err := store.Get(tk.ID)
	if err != nil || got.Attempt != 0 || got.Lease.Attempt != 0 {
		t.Fatalf("attempt number was invented: task = %+v, err %v", got, err)
	}
}
