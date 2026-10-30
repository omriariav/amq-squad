package scheduler

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// fakeRoster is a launcher that launches a roster: every requested seat
// joins with its request id (as a launch whose proofs passed), except the
// ones named in notUp; a set fail outcome is returned instead.
type fakeRoster struct {
	*fakeLauncher
	s        *Scheduler
	launches [][]SeatRequest
	fail     RosterOutcome
	notUp    map[string]bool
}

func (f *fakeRoster) LaunchRoster(reqs []SeatRequest) RosterOutcome {
	f.launches = append(f.launches, reqs)
	if f.fail.Stage != "" {
		return f.fail
	}
	out := RosterOutcome{Stage: RosterApplied, LaunchID: fmt.Sprintf("L%d", len(f.launches))}
	for _, r := range reqs {
		if f.notUp[r.Handle] {
			out.Seats = append(out.Seats, RosterSeatState{Handle: r.Handle, ReasonCode: "no_proof", Detail: "no accepted proof within 90s"})
			continue
		}
		f.s.Post(Event{Kind: SeatUp, At: f.s.Now(), Handle: r.Handle, Pool: r.Pool, RequestID: r.ID})
		out.Seats = append(out.Seats, RosterSeatState{Handle: r.Handle, Up: true})
	}
	return out
}

func newRosterHarness(t *testing.T, cfg Config, roster ...RosterSeat) (*harness, *fakeRoster) {
	t.Helper()
	cfg.Roster = roster
	h := newHarness(t, cfg)
	fr := &fakeRoster{fakeLauncher: h.l, s: h.s}
	h.s.Launcher = fr
	return h, fr
}

func count(ds []Decision, kind string) int {
	n := 0
	for _, d := range ds {
		if d.Kind == kind {
			n++
		}
	}
	return n
}

var (
	rosterAB = []RosterSeat{{Handle: "s-a", Pool: sonnet}, {Handle: "s-b", Pool: gpt}}
	rosterAA = []RosterSeat{{Handle: "s-a", Pool: sonnet}, {Handle: "s-b", Pool: sonnet}}
)

// t16 row 2: two roster seats of different pools hold leases at once under
// max_seats 2, and no seat is ever requested: the roster launched both in
// one launch at the first tick. Reverted (seats on demand), each task
// requests its own seat and the second launch is the one AMQ refuses.
func TestRosterSeatsOfTwoPoolsHoldLeasesAtOnce(t *testing.T) {
	h, fr := newRosterHarness(t, Config{MaxSeats: 2}, rosterAB...)
	first := h.tick()
	if len(fr.launches) != 1 || len(fr.launches[0]) != 2 || !has(first, "seat_up", "", "s-a") || !has(first, "seat_up", "", "s-b") {
		t.Fatalf("roster launch %+v; decisions %+v", fr.launches, first)
	}
	a, b := h.add("a", &sonnet), h.add("b", &gpt)
	ds := h.tick()
	if !has(ds, "dispatched", a.ID, "s-a") || !has(ds, "dispatched", b.ID, "s-b") {
		t.Fatalf("decisions %+v", ds)
	}
	if h.get(a.ID).Lease == nil || h.get(b.ID).Lease == nil {
		t.Fatal("both leases are not held at once")
	}
	if len(h.l.requests) != 0 || count(ds, "seat_requested") != 0 || len(fr.launches) != 1 {
		t.Fatalf("a seat was requested: %+v; launches %d", h.l.requests, len(fr.launches))
	}
}

// t16 row 3: a seat cap of 1 with two roster seats up is one lease at a
// time: the second task waits seat_cap with an idle seat, and gets that
// seat once the first lease ends. Reverted (the cap counting seats), both
// tasks would be dispatched or none.
func TestRosterSeatCapOfOneMeansOneLeaseAtATime(t *testing.T) {
	h, _ := newRosterHarness(t, Config{MaxSeats: 1, IdleTimeout: time.Hour}, rosterAA...)
	h.tick()
	a, b := h.add("a", &sonnet), h.add("b", &sonnet)
	ds := h.tick()
	if count(ds, "dispatched") != 1 || !has(ds, "dispatched", a.ID, "s-a") {
		t.Fatalf("decisions %+v", ds)
	}
	if got := h.get(b.ID); got.Lease != nil || got.WaitReason != task.WaitSeatCap {
		t.Fatalf("the second task: %+v", got)
	}
	now := h.clk.Now()
	if _, err := h.store.Claim(a.ID, "s-a", nil, time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.Done(a.ID, "s-a", "", "", now); err != nil {
		t.Fatal(err)
	}
	ds = h.tick()
	if h.get(a.ID).Lease != nil || count(ds, "dispatched") != 1 || h.get(b.ID).Lease == nil {
		t.Fatalf("after the first lease ended: %+v", ds)
	}
}

// t16 row 5: a gone roster seat with a waiting task of its pool is a
// seat_replacement_needs_relaunch decision (with AMQ's reason code) and a
// block on the lead, once; no seat is requested and nothing is retried.
// The lead's relaunch then waits for the other seat's lease, closes it,
// relaunches the roster in one launch and returns the task to pending,
// where it is dispatched. Reverted, the task requests a seat each start
// timeout (seat_requested, seat_request_cancelled, again).
func TestGoneRosterSeatBlocksForTheLeadsRelaunch(t *testing.T) {
	h, fr := newRosterHarness(t, Config{MaxSeats: 2, IdleTimeout: time.Hour, SeatStartTimeout: time.Minute}, rosterAB...)
	h.tick()
	busy := h.add("busy", &gpt)
	h.tick()
	h.s.Post(Event{Kind: SeatGone, At: h.clk.Now(), Handle: "s-a"})
	a := h.add("a", &sonnet)
	var all []Decision
	for i := 0; i < 4; i++ {
		all = append(all, h.tick()...)
		h.clk.Add(2 * time.Minute)
	}
	if count(all, ReasonSeatRelaunch) != 1 || count(all, "seat_requested") != 0 || count(all, "seat_request_cancelled") != 0 || len(h.l.requests) != 0 {
		t.Fatalf("decisions %+v; requests %+v", all, h.l.requests)
	}
	for _, d := range all {
		if d.Kind == ReasonSeatRelaunch && (d.TaskID != a.ID || d.Seat != "s-a" || d.Reason != AMQBindingPresent) {
			t.Fatalf("the decision: %+v", d)
		}
	}
	got := h.get(a.ID)
	if got.State != task.StateBlocked || got.BlockReason != ReasonSeatRelaunch || got.BlockRoute != task.RouteLead || !strings.Contains(got.BlockDetail, RelaunchVerb) {
		t.Fatalf("the task: %+v", got)
	}
	// The relaunch waits for s-b's lease.
	h.s.RequestRelaunch("s-a is gone")
	ds := h.tick()
	if !has(ds, "roster_relaunch_requested", "", "") || !has(ds, "dispatch_held", "", "") || len(h.l.closed) != 0 || len(fr.launches) != 1 {
		t.Fatalf("relaunch with a lease held: %+v; closed %v", ds, h.l.closed)
	}
	if _, err := h.store.Release(busy.ID, "lead", "make way for the relaunch", h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	ds = h.tick()
	if !has(ds, "seat_closed", "", "s-b") || len(fr.launches) != 2 || len(fr.launches[1]) != 2 || !has(ds, "roster_launched", "", "") ||
		!has(ds, "task_reset", a.ID, "") || !has(ds, "dispatched", a.ID, "s-a") || !has(ds, "dispatched", busy.ID, "s-b") {
		t.Fatalf("the relaunch: %+v; closed %v; launches %d", ds, h.l.closed, len(fr.launches))
	}
}

// t16 rule 1: a parked roster seat stays up past the idle timeout (an
// on-demand seat closes); an unresponsive one still closes.
func TestParkedRosterSeatIsNotClosedForIdleness(t *testing.T) {
	h, _ := newRosterHarness(t, Config{MaxSeats: 2, IdleTimeout: time.Minute}, rosterAB...)
	h.tick()
	h.clk.Add(time.Hour)
	if ds := h.tick(); count(ds, "seat_closed") != 0 || len(h.l.closed) != 0 {
		t.Fatalf("a parked seat closed: %+v", ds)
	}
	h.s.seats["s-a"].unresponsive = true
	if ds := h.tick(); !has(ds, "seat_closed", "", "s-a") || has(ds, "seat_closed", "", "s-b") {
		t.Fatalf("the unresponsive seat: %+v", ds)
	}
}

// t16 rule 2: the router's concurrency caps count a roster seat only while
// it holds something (a closing seat still counts); a parked one is
// capacity, not an occupant.
func TestRosterOccupancyOmitsParkedSeats(t *testing.T) {
	h, _ := newRosterHarness(t, Config{MaxSeats: 2}, rosterAB...)
	h.tick()
	if occ := h.s.Occupancy(); len(occ) != 0 {
		t.Fatalf("parked seats in the occupancy: %+v", occ)
	}
	h.s.seats["s-b"].closing = "quarantined"
	if occ := h.s.Occupancy(); len(occ) != 1 || occ[0].Handle != "s-b" {
		t.Fatalf("a closing seat: %+v", occ)
	}
}

// t16 rule 3: at a restart with only some roster seats live, the live
// ones are adopted and dispatched to, nothing launches (AMQ would refuse
// it), and a task of a missing seat's pool blocks for the relaunch.
func TestPartialRosterIsAdoptedAndNotLaunched(t *testing.T) {
	h, fr := newRosterHarness(t, Config{MaxSeats: 2}, rosterAB...)
	h.s.AdoptUp("s-a", sonnet, h.clk.Now())
	a, b := h.add("a", &sonnet), h.add("b", &gpt)
	ds := h.tick()
	if len(fr.launches) != 0 || !has(ds, "roster_incomplete", "", "s-b") || !has(ds, "dispatched", a.ID, "s-a") || !has(ds, ReasonSeatRelaunch, b.ID, "s-b") {
		t.Fatalf("decisions %+v; launches %d", ds, len(fr.launches))
	}
	h2, fr2 := newRosterHarness(t, Config{MaxSeats: 2}, rosterAB...)
	h2.s.AdoptUp("s-a", sonnet, h2.clk.Now())
	h2.s.AdoptUp("s-b", gpt, h2.clk.Now())
	if ds := h2.tick(); len(fr2.launches) != 0 || !has(ds, "roster_adopted", "", "") {
		t.Fatalf("a whole roster adopted: %+v; launches %d", ds, len(fr2.launches))
	}
}

// t16 rule 4 (and N3): a roster seat whose exit check is pending keeps its
// worktree; the roster launch waits for the check.
func TestRosterLaunchWaitsForAPendingExitCheck(t *testing.T) {
	h, fr := newRosterHarness(t, Config{MaxSeats: 2}, rosterAB...)
	ex := &deferringExits{}
	h.s.Exits = ex
	h.s.Post(Event{Kind: SeatGone, At: h.clk.Now(), Handle: "s-a"})
	if ds := h.tick(); len(fr.launches) != 0 || !has(ds, "dispatch_held", "", "") {
		t.Fatalf("launched over a pending exit check: %+v", ds)
	}
	ex.open = true
	if ds := h.tick(); len(fr.launches) != 1 || !has(ds, "seat_up", "", "s-a") {
		t.Fatalf("after the check: %+v", ds)
	}
}

// t16 rule 6: seats that did not prove after an applied launch are named
// (seat_not_up) and their pool's task waits for the reservation, then
// blocks for the relaunch once it is cancelled; a launcher that cannot
// launch a roster fails the start.
func TestRosterSeatNotUpAndALauncherWithoutRoster(t *testing.T) {
	h, fr := newRosterHarness(t, Config{MaxSeats: 2, SeatStartTimeout: time.Minute}, rosterAB...)
	fr.notUp = map[string]bool{"s-b": true}
	b := h.add("b", &gpt)
	ds := h.tick()
	if !has(ds, "seat_not_up", "", "s-b") || !has(ds, "seat_up", "", "s-a") || h.get(b.ID).WaitReason != task.WaitSeatStarting {
		t.Fatalf("decisions %+v; b %+v", ds, h.get(b.ID))
	}
	h.clk.Add(2 * time.Minute)
	ds = h.tick()
	if !has(ds, "seat_request_cancelled", "", "") || !has(ds, ReasonSeatRelaunch, b.ID, "s-b") {
		t.Fatalf("after the start timeout: %+v", ds)
	}
	plain := newHarness(t, Config{Roster: rosterAB})
	_, err := plain.s.Tick()
	var failed *RosterLaunchError
	if !errors.As(err, &failed) || failed.Stage != "launch_error" {
		t.Fatalf("a launcher without a roster: %v", err)
	}
}

// t16: a pool no roster seat serves is refused as an on-demand request was
// (no free seat in the seat list), and a pi seat with no model serves any
// pi model.
func TestRosterPoolRules(t *testing.T) {
	h, _ := newRosterHarness(t, Config{MaxSeats: 2}, RosterSeat{Handle: "p1", Pool: task.Pool{Runtime: "pi"}})
	h.tick()
	qwen := task.Pool{Runtime: "pi", Model: "qwen3.6:27b", Effort: "high"}
	a, c := h.add("a", &qwen), h.add("c", &sonnet)
	ds := h.tick()
	if !has(ds, "dispatched", a.ID, "p1") {
		t.Fatalf("a modelless pi seat: %+v", ds)
	}
	if !has(ds, "seat_request_failed", c.ID, "") || !strings.Contains(ds[len(ds)-1].Reason, "no free seat in the squad's seat list") {
		t.Fatalf("a pool with no roster seat: %+v", ds)
	}
	if !PoolServes(task.Pool{Runtime: "pi", Model: "m"}, task.Pool{Runtime: "pi", Model: "m", Effort: "low"}) || PoolServes(sonnet, task.Pool{Runtime: "claude", Model: "sonnet", Effort: "high"}) {
		t.Fatal("PoolServes")
	}
}

// t16: the lead's relaunch to a scheduler without a roster is refused, and
// the state file names the roster and a pending relaunch for status.
func TestRelaunchRefusedWithoutARosterAndStateNamesIt(t *testing.T) {
	h := newHarness(t, Config{})
	h.s.RequestRelaunch("x")
	if ds := h.tick(); !has(ds, "roster_relaunch_refused", "", "") {
		t.Fatalf("decisions %+v", ds)
	}
	r, _ := newRosterHarness(t, Config{MaxSeats: 2, StatePath: t.TempDir() + "/scheduler.json"}, rosterAB...)
	r.tick()
	r.add("busy", &gpt)
	r.tick()
	r.s.RequestRelaunch("x")
	r.tick()
	st, err := ReadState(r.s.Config.StatePath)
	if err != nil || strings.Join(st.Roster, ",") != "s-a,s-b" || !st.RelaunchPending {
		t.Fatalf("state %+v %v", st, err)
	}
}
