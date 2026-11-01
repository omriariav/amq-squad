package status

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Brief 3.1.1-2 item 2: a root holding an earlier session's records (day 4:
// lead, qa, scribe and senior-dev beside the live cto, dev and reviewer).
// seats[] is the running roster, the scheduler state's roster with its
// lead; every other record under the root is a stale_records entry with
// its session and launch id, and no seat row. Reverted, seats[] listed all
// seven handles.
func TestStatusSeatsAreTheRunningRoster(t *testing.T) {
	now := time.Date(2026, 11, 1, 19, 0, 0, 0, time.UTC)
	root := filepath.Join(t.TempDir(), "root")
	seat := func(handle, session, launchID string) {
		for _, d := range []string{filepath.Join(root, "agents", handle), launch.StagedDir(root, handle)} {
			if err := os.MkdirAll(d, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		rec := launch.Record{Handle: handle, Root: root, Session: session, Runtime: "codex", LaunchID: launchID}
		if err := launch.WriteFile(filepath.Join(launch.StagedDir(root, handle), launch.FileName), rec); err != nil {
			t.Fatal(err)
		}
	}
	for _, h := range []string{"lead", "qa", "scribe", "senior-dev"} {
		seat(h, "v30", "old-"+h)
	}
	for _, h := range []string{"cto", "dev", "reviewer"} {
		seat(h, "v31", "c811afa6-"+h)
	}
	write(t, scheduler.StatePathFor(root), `{"lead":"cto","max_seats":2,"live_seats":2,"seats":[],"roster":["dev","reviewer"]}`)
	s := Build(Collect(CollectOptions{Root: root, Session: "v31", Now: now, PIDAlive: func(int) bool { return false }}))
	var seats []string
	for _, st := range s.Seats {
		seats = append(seats, st.Handle)
	}
	if !slices.Equal(seats, []string{"cto", "dev", "reviewer"}) {
		t.Fatalf("seats %v, want the running roster with its lead", seats)
	}
	want := []StaleRecord{{"lead", "v30", "old-lead"}, {"qa", "v30", "old-qa"}, {"scribe", "v30", "old-scribe"}, {"senior-dev", "v30", "old-senior-dev"}}
	if !slices.Equal(s.StaleRecords, want) {
		t.Fatalf("stale_records %+v, want %+v", s.StaleRecords, want)
	}
	if s.Counts.All != 3 {
		t.Fatalf("a stale record is counted: %+v", s.Counts)
	}
	// No roster known (seats on demand, no scheduler state): every record
	// is a seat row, as before, and stale_records is [].
	_ = os.Remove(scheduler.StatePathFor(root))
	s = Build(Collect(CollectOptions{Root: root, Session: "v31", Now: now, PIDAlive: func(int) bool { return false }}))
	if len(s.Seats) != 7 || s.StaleRecords == nil || len(s.StaleRecords) != 0 {
		t.Fatalf("without a roster: %d seats, stale_records %v", len(s.Seats), s.StaleRecords)
	}
}

// Brief 3.1.1-2 item 1, row 4 (status's half): a task waiting
// no_roster_seat names the pool no roster seat serves (wait_pool, from the
// scheduler state's route waits) and its attention item says so. A task
// whose wait moved on names none.
func TestNoRosterSeatWaitNamesThePool(t *testing.T) {
	now := time.Date(2026, 11, 1, 19, 0, 0, 0, time.UTC)
	opus := task.Pool{Runtime: "claude", Model: "opus", Effort: "high"}
	in := Input{Now: now, Root: "/r", Tasks: []task.Task{
		{Schema: 1, ID: "t27", Title: "the size cap", Class: "implement", State: task.StatePending, WaitReason: scheduler.WaitNoRosterSeat, CreatedAt: now, UpdatedAt: now},
		{Schema: 1, ID: "t28", Title: "moved on", Class: "implement", State: task.StatePending, WaitReason: task.WaitSeatCap, CreatedAt: now, UpdatedAt: now},
	}, Scheduler: scheduler.State{RouteWaits: []scheduler.RouteWait{
		{TaskID: "t27", Reason: scheduler.WaitNoRosterSeat, Pool: opus},
		{TaskID: "t28", Reason: scheduler.WaitNoRosterSeat, Pool: opus},
	}}}
	s := Build(in)
	pools := map[string]string{}
	for _, r := range s.Tasks {
		pools[r.ID] = r.WaitPool
	}
	if pools["t27"] != "claude:opus:high" || pools["t28"] != "" {
		t.Fatalf("wait_pool %v", pools)
	}
	found := false
	for _, a := range s.Attention {
		if a.TaskID == "t27" {
			found = a.Subject == "t27 waits: no seat serves claude:opus:high: the size cap" && a.Severity == "medium"
		}
	}
	if !found {
		t.Fatalf("attention %+v", s.Attention)
	}
}
