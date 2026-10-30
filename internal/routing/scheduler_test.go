package routing

import (
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

type recordingLauncher struct {
	requests []scheduler.SeatRequest
	closeErr error // a close the launcher cannot do yet: the seat stays closing
}

func (l *recordingLauncher) RequestSeat(r scheduler.SeatRequest) error {
	l.requests = append(l.requests, r)
	return nil
}
func (l *recordingLauncher) CancelSeatRequest(scheduler.SeatRequest) error { return nil }
func (l *recordingLauncher) CloseSeat(string, string) error                { return l.closeErr }

// The Router drops into the scheduler in place of DefaultRouter (lead ruling
// 15): a routed task gets a seat request for the chosen pool, a local-only
// task with no local entry waits with privacy_class in the store.
func TestRouterInsideTheScheduler(t *testing.T) {
	root := t.TempDir()
	store, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	now := t0
	if _, err := store.Add(task.AddInput{Title: "parse", Class: "implement", DeclaredFiles: []string{"cmd/main.go"}}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Add(task.AddInput{Title: "rotate keys", Class: "implement", DeclaredFiles: []string{"internal/secrets/vault.go"}}, now); err != nil {
		t.Fatal(err)
	}
	f := newFixture(t).apply("schema: 1\nexploration_share: 0\nprivacy_paths:\n  - glob: internal/secrets/\n    class: local\n")
	r, err := New(Options{Table: f.tbl, Registry: f.reg, Grants: f.g, Root: root, Now: func() time.Time { return now },
		Tasks: func() ([]task.Task, error) { return task.Snapshot(store.Dir) }})
	if err != nil {
		t.Fatal(err)
	}
	l := &recordingLauncher{}
	s, err := scheduler.New(scheduler.Options{Store: store, Now: func() time.Time { return now }, Launcher: l, Router: r,
		Config: scheduler.Config{MaxSeats: 4}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Tick(); err != nil {
		t.Fatal(err)
	}
	if len(l.requests) != 1 || l.requests[0].TaskID != "t1" || l.requests[0].Pool.Key() != "claude:sonnet:medium" {
		t.Fatalf("seat requests = %+v", l.requests)
	}
	t2, err := store.Get("t2")
	if err != nil {
		t.Fatal(err)
	}
	if t2.State != task.StatePending || t2.WaitReason != task.WaitPrivacyClass || t2.Pool != nil {
		t.Fatalf("t2 = %s %q %v", t2.State, t2.WaitReason, t2.Pool)
	}
}
