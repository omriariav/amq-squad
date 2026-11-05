package scheduler_test

import (
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/routing"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

func TestMaxAttemptExpiriesBlockForLead(t *testing.T) {
	now := time.Date(2026, 11, 5, 6, 0, 0, 0, time.UTC)
	store, err := task.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pool := task.Pool{Runtime: "claude", Model: "sonnet", Effort: "medium"}
	tk, err := store.Add(task.AddInput{Title: "hangs", Class: "script", Pool: &pool}, now)
	if err != nil {
		t.Fatal(err)
	}
	root, home, repo := t.TempDir(), t.TempDir(), t.TempDir()
	realRouter, err := routing.Open(routing.StartOptions{Home: home, RepoRoot: repo, Root: root,
		Tasks: func() ([]task.Task, error) { return task.Snapshot(store.Dir) }, Now: func() time.Time { return now }, RosterPools: []task.Pool{pool}})
	if err != nil || realRouter == nil {
		t.Fatal(err)
	}
	s := &scheduler.Scheduler{Store: store, Now: func() time.Time { return now }, Router: realRouter,
		Config: scheduler.Config{LeaseDuration: time.Minute, IdleTimeout: time.Hour}, Incarnation: "expiry-router"}
	for n := 1; n <= routing.DefaultMaxAttempts; n++ {
		holder := "seat-" + string(rune('a'+n-1))
		if _, err := store.Grant(tk.ID, holder, &pool, time.Minute, now); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Claim(tk.ID, holder, nil, time.Minute, now); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Minute)
		if _, err := s.Tick(); err != nil {
			t.Fatal(err)
		}
		got, err := store.Get(tk.ID)
		if err != nil || len(got.Attempts) != n || got.Attempts[n-1].Outcome != task.OutcomeExpired {
			t.Fatalf("attempt %d expiry = %+v, %v", n, got, err)
		}
	}
	got, err := store.Get(tk.ID)
	if err != nil || got.State != task.StateBlocked || got.BlockReason != "retries_exhausted" || got.BlockRoute != task.RouteLead || got.BlockDetail != "3 failed attempts" {
		t.Fatalf("MaxAttempts expiries did not block through routing.Open for the lead: %+v, %v", got, err)
	}
}
