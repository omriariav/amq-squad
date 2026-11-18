package scheduler

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// C5a PR 1, row D3 (roster adoption, internal/scheduler/roster.go
// AdoptUpRequest): a scheduler restart anchors a seat's lapsed-attempt
// exclusion at the start the core staged for its launch; a record the seat
// wrote in its own agent directory with a later start cannot erase the
// exclusion.
func TestC5aASeatWrittenLaunchRecordDoesNotMoveTheAdoptionAnchor(t *testing.T) {
	root := t.TempDir()
	store, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	seatStart := t0
	lapsedAt := seatStart.Add(time.Minute)
	adoptedAt := seatStart.Add(10 * time.Minute)
	tk := task.Task{ID: "t1", State: task.StatePending, Pool: &sonnet, Attempts: []task.Attempt{{
		N: 1, Holder: "seat-a", Pool: &sonnet, GrantedAt: lapsedAt, EndedAt: lapsedAt.Add(time.Minute), Outcome: task.OutcomeExpired,
	}}}
	// the core staged seat-a's launch at seatStart
	if err := os.MkdirAll(launch.StagedDir(root, "seat-a"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := launch.WriteFile(filepath.Join(launch.StagedDir(root, "seat-a"), launch.FileName), launch.Record{Handle: "seat-a", Root: root, Runtime: sonnet.Runtime, Model: sonnet.Model, Effort: sonnet.Effort, StartedAt: seatStart}); err != nil {
		t.Fatal(err)
	}
	// seat-a writes a record in its own agent directory with a start after
	// its lapsed attempt
	planted := filepath.Join(root, "agents", "seat-a", "extensions", launch.LayerName)
	if err := os.MkdirAll(planted, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := launch.WriteFile(filepath.Join(planted, launch.FileName), launch.Record{Handle: "seat-a", Root: root, Runtime: sonnet.Runtime, Model: sonnet.Model, Effort: sonnet.Effort, StartedAt: adoptedAt}); err != nil {
		t.Fatal(err)
	}
	restarted := &Scheduler{Store: store}
	restarted.AdoptUpRequest("seat-a", "request-1", sonnet, adoptedAt)
	if got := restarted.seats["seat-a"].joinedAt; !got.Equal(seatStart) {
		t.Errorf("adopted join anchor = %s, want the staged launch's %s", got, seatStart)
	}
	if got := restarted.idleSeat([]task.Task{tk}, tk, sonnet); got != "" {
		t.Errorf("a record seat-a wrote erased its lapsed-attempt exclusion: %q", got)
	}
}
