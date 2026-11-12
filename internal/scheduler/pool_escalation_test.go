package scheduler_test

import (
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/registry"
	"github.com/omriariav/amq-squad/v3/internal/routing"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

type escalationLauncher struct{}

func (escalationLauncher) RequestSeat(scheduler.SeatRequest) error       { return nil }
func (escalationLauncher) CancelSeatRequest(scheduler.SeatRequest) error { return nil }
func (escalationLauncher) CloseSeat(string, string) error                { return nil }

func TestGrantClaimFailEscalateGrantRecordsTheRealRoutersPool(t *testing.T) {
	root := t.TempDir()
	store, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	store.Verifiers = func(t task.Task) (string, bool, error) { return "run-script", t.Class == "script", nil }
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	tk, err := store.Add(task.AddInput{Title: "retry", Class: "script"}, now)
	if err != nil {
		t.Fatal(err)
	}
	haiku := task.Pool{Runtime: "claude", Model: "haiku", Effort: "low"}
	sonnet := task.Pool{Runtime: "claude", Model: "sonnet", Effort: "medium"}
	entry := func(p task.Pool) registry.Entry {
		return registry.Entry{Runtime: p.Runtime, Provider: "anthropic", Model: p.Model, Effort: p.Effort,
			Verified: true, State: registry.StateReady, PrivacyClass: grants.PrivacyVendor, ConcurrencyPool: p.Key(), ConcurrencyCap: 1}
	}
	table := routing.Default()
	table.ExplorationShare = 0
	g := grants.Defaults()
	g.Verifiers = map[string]string{"script": "run-script"}
	router, err := routing.New(routing.Options{
		Table: table, Registry: registry.Registry{Schema: 1, Entries: []registry.Entry{entry(haiku), entry(sonnet)}},
		Grants: g, Seed: "pool-escalation", Now: func() time.Time { return now },
		Tasks: func() ([]task.Task, error) { return task.Snapshot(store.Dir) },
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := scheduler.New(scheduler.Options{Store: store, Now: func() time.Time { return now }, Launcher: escalationLauncher{}, Router: router,
		Config: scheduler.Config{MaxSeats: 2, LeaseDuration: time.Minute}, Incarnation: "pooltest"})
	if err != nil {
		t.Fatal(err)
	}
	s.AdoptUp("seat-haiku", haiku, now)
	s.AdoptUp("seat-sonnet", sonnet, now)
	if _, err := s.Tick(); err != nil {
		t.Fatal(err)
	}
	first, err := store.Get(tk.ID)
	if err != nil || first.Pool == nil || *first.Pool != haiku || first.Lease == nil || first.Lease.Holder != "seat-haiku" {
		t.Fatalf("first route = %+v, %v", first, err)
	}
	now = now.Add(time.Second)
	if _, err := store.ClaimOffered(tk.ID, "seat-haiku", &haiku, time.Minute, now); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if _, err := store.Fail(tk.ID, "seat-haiku", "work failed", now); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if _, err := s.Tick(); err != nil {
		t.Fatal(err)
	}
	second, err := store.Get(tk.ID)
	if err != nil || second.Pool == nil || *second.Pool != sonnet || second.Lease == nil || second.Lease.Holder != "seat-sonnet" {
		t.Fatalf("escalated route = %+v, %v", second, err)
	}
}
