package routing

import (
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Brief 31-1, row 9 (the router's half): a roster seat's own pool is
// admitted with no registry entry and no table chain. A Codex id outside
// the old codexModelLists and a pi PROVIDER/ID model each get a task: a pin
// to it routes there, and an unpinned task falls to it after its tier's
// chain. Without the roster pools both refuse as before.
func TestRosterPoolsAreAdmittedWithoutRegistryOrChain(t *testing.T) {
	codex := task.Pool{Runtime: "codex", Model: "gpt-9-nova", Effort: "high"}
	pi := task.Pool{Runtime: "pi", Model: "openrouter/some-model", Effort: "medium"}
	f := newFixture(t).apply("schema: 1\nexploration_share: 0\n")
	f.reg.Entries = nil // no registry entry for any pool
	router := func(roster []task.Pool) *Router {
		r, err := New(Options{Table: f.tbl, Registry: f.reg, Grants: f.g, Seed: "fixture", Now: func() time.Time { return t0 },
			Tasks: func() ([]task.Task, error) { return nil, nil }, RosterPools: roster})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	for _, p := range []task.Pool{codex, pi} {
		pinned := newTask("t1", "implement")
		pinned.Pool = &p
		if _, route := router(nil).Decide(pinned, nil); route.Pool != nil {
			t.Fatalf("%s routed with no roster: %+v", p.Key(), route)
		}
		if _, route := router([]task.Pool{p}).Decide(pinned, nil); route.Pool == nil || *route.Pool != p {
			t.Fatalf("a pin to the roster pool %s: %+v", p.Key(), route)
		}
		open := newTask("t2", "implement")
		if _, route := router([]task.Pool{p}).Decide(open, nil); route.Pool == nil || *route.Pool != p {
			t.Fatalf("an unpinned task with the roster pool %s: %+v", p.Key(), route)
		}
	}
}
