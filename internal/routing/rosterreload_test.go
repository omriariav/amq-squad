package routing

import (
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Brief 31-2 PR 2, change 5 (row A's router half): the roster pools are
// read per call. A task pinned to a seat newly added to squad.yaml is not
// routed before the relaunch's swap and is routed to that seat's own pool
// after it, on the same router. Reverted (the pools fixed at open), the
// pin stays unrouted after the swap.
func TestARosterSeatAddedByAReloadIsAdmittedAfterTheSwapAndNotBefore(t *testing.T) {
	old := task.Pool{Runtime: "claude", Model: "sonnet", Effort: "medium"}
	added := task.Pool{Runtime: "codex", Model: "gpt-9-nova", Effort: "high"}
	f := newFixture(t).apply("schema: 1\nexploration_share: 0\n")
	f.reg.Entries = nil
	r, err := New(Options{Table: f.tbl, Registry: f.reg, Grants: f.g, Seed: "fixture", Now: func() time.Time { return t0 },
		Tasks: func() ([]task.Task, error) { return nil, nil }, RosterPools: []task.Pool{old}})
	if err != nil {
		t.Fatal(err)
	}
	pinned := newTask("t1", "implement")
	pinned.Pool = &added
	if _, route := r.Decide(pinned, nil); route.Pool != nil {
		t.Fatalf("the new seat's pool routed before the reload: %+v", route)
	}
	r.SetRosterPools([]task.Pool{old, added})
	if _, route := r.Decide(pinned, nil); route.Pool == nil || *route.Pool != added {
		t.Fatalf("the new seat's pool after the reload: %+v", route)
	}
	if got := r.RosterPools(); len(got) != 2 || got[1] != added {
		t.Fatalf("roster pools %+v", got)
	}
	// the setter and a route race cleanly (go test -race): every read is
	// under the router's lock
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); r.SetRosterPools([]task.Pool{old, added}) }()
		go func() { defer wg.Done(); r.Decide(pinned, nil) }()
	}
	wg.Wait()
}
