package scheduler

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

var opus = task.Pool{Runtime: "claude", Model: "opus", Effort: "high"}

func rosterHandles(r []RosterSeat) string {
	var hs []string
	for _, rs := range r {
		hs = append(hs, rs.Handle+"="+rs.Pool.Key())
	}
	return strings.Join(hs, ",")
}

// Brief 31-2 PR 2, changes 2, 3 and 4 (rows A, C and 4, the scheduler's
// half). A relaunch carrying a reload and the key press:
//   - swaps nothing while a seat still holds work: the roster, and the
//     reload's other halves (Swap), stay as they were, and no seat closes;
//     s-b, removed from squad.yaml, keeps its lease and stays live (row C);
//   - at the drain's end, after every seat closed (s-b at its own close
//     once it finished, with the relaunch's reason, never a kill), swaps
//     once, then launches the new roster: s-c, the added seat, carries the
//     key press as its start; s-a, whose line changed (sonnet to opus),
//     launches on its new pool with no start of its own; s-b is not
//     launched again (rows A, C, 4);
//
// Reverted to a swap at the request (change 2), Swap runs while s-b holds
// its lease; without the start (change 4), s-c's request carries none.
func TestAReloadSwapsAtTheDrainsEndAndOnlyTheAddedSeatStartsFromTheKeyPress(t *testing.T) {
	h, fr := newRosterHarness(t, Config{MaxSeats: 2, IdleTimeout: time.Hour, SeatStartTimeout: time.Minute}, rosterAB...)
	h.tick()
	busy := h.add("busy", &gpt)
	h.tick()
	if got := h.get(busy.ID); got.Lease == nil || got.Lease.Holder != "s-b" {
		t.Fatalf("busy: %+v", got)
	}
	next := []RosterSeat{{Handle: "s-a", Pool: opus}, {Handle: "s-c", Pool: sonnet}}
	swaps := 0
	var closedAtSwap []string
	reload := &RosterReload{Roster: next, Swap: func() error {
		swaps++
		closedAtSwap = slices.Clone(h.l.closed)
		return nil
	}}
	keyPress := t0.Add(-3 * time.Second)
	h.s.RequestRelaunchReload("add s-c", keyPress, reload)
	ds := h.tick()
	if !has(ds, "roster_relaunch_requested", "", "") || swaps != 0 || len(h.l.closed) != 0 || rosterHandles(h.s.Config.Roster) != rosterHandles(rosterAB) || len(fr.launches) != 1 {
		t.Fatalf("while s-b holds its lease: swaps %d, closed %v, roster %s, launches %d; %+v", swaps, h.l.closed, rosterHandles(h.s.Config.Roster), len(fr.launches), ds)
	}
	if got := h.get(busy.ID); got.Lease == nil || got.Lease.Holder != "s-b" {
		t.Fatalf("the removed seat lost its lease to the reload: %+v", got)
	}
	if _, err := h.store.Release(busy.ID, "lead", "finished", h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	ds = h.tick()
	if swaps != 1 || !slices.Contains(closedAtSwap, "s-a") || !slices.Contains(closedAtSwap, "s-b") {
		t.Fatalf("the swap: %d swaps; closed at the swap %v", swaps, closedAtSwap)
	}
	for _, d := range ds {
		if d.Kind == "seat_closed" && d.Reason != closeRelaunch {
			t.Fatalf("a seat closed for another reason than the relaunch: %+v", d)
		}
	}
	if !has(ds, "seat_closed", "", "s-b") || !has(ds, "roster_reloaded", "", "s-c") || !has(ds, "roster_launched", "", "") || rosterHandles(h.s.Config.Roster) != rosterHandles(next) {
		t.Fatalf("after the drain: roster %s; %+v", rosterHandles(h.s.Config.Roster), ds)
	}
	if len(fr.launches) != 2 {
		t.Fatalf("launches %d", len(fr.launches))
	}
	got := fr.launches[1]
	if len(got) != 2 || got[0].Handle != "s-a" || got[0].Pool != opus || !got[0].Start.IsZero() || got[1].Handle != "s-c" || !got[1].Start.Equal(keyPress) {
		t.Fatalf("the relaunch's requests: %+v", got)
	}
}

// Codex P2 2 on 31-2 PR 2: each added seat keeps its key press on the
// scheduler until it leaves the roster. s-c added at T1, then s-d at T2:
// the second relaunch carries T1 for s-c and T2 for s-d, and so does a
// plain relaunch after it (seatlaunch writes each as the seat's marks.json
// start, TestAnAddedSeatKeepsItsStartAcrossRelaunches); a reload that
// removes s-c drops its start, s-d keeps T2. Reverted (the starts cleared
// after each launch), the later relaunches carry none.
func TestAnAddedSeatsStartIsKeptUntilItLeavesTheRoster(t *testing.T) {
	h, fr := newRosterHarness(t, Config{MaxSeats: 4, IdleTimeout: time.Hour, SeatStartTimeout: time.Minute}, rosterAB...)
	h.tick()
	t1, t2 := t0.Add(-time.Hour), t0.Add(-time.Minute)
	withC := append(slices.Clone(rosterAB), RosterSeat{Handle: "s-c", Pool: opus})
	h.s.RequestRelaunchReload("add s-c", t1, &RosterReload{Roster: withC})
	h.tick()
	withD := append(slices.Clone(withC), RosterSeat{Handle: "s-d", Pool: opus})
	h.s.RequestRelaunchReload("add s-d", t2, &RosterReload{Roster: withD})
	h.tick()
	h.s.RequestRelaunch("again")
	h.tick()
	starts := func(reqs []SeatRequest) map[string]time.Time {
		out := map[string]time.Time{}
		for _, r := range reqs {
			out[r.Handle] = r.Start
		}
		return out
	}
	if len(fr.launches) != 4 {
		t.Fatalf("launches %d", len(fr.launches))
	}
	for i := 2; i <= 3; i++ {
		got := starts(fr.launches[i])
		if !got["s-c"].Equal(t1) || !got["s-d"].Equal(t2) || !got["s-a"].IsZero() || !got["s-b"].IsZero() {
			t.Fatalf("launch %d starts %v", i, got)
		}
	}
	h.s.RequestRelaunchReload("drop s-c", t0, &RosterReload{Roster: append(slices.Clone(rosterAB), RosterSeat{Handle: "s-d", Pool: opus})})
	h.tick()
	got := starts(fr.launches[4])
	if _, in := got["s-c"]; in || !got["s-d"].Equal(t2) || len(h.s.seatStart) != 1 {
		t.Fatalf("after s-c left: %v; kept %v", got, h.s.seatStart)
	}
}

// Brief 31-2 PR 2, change 2 (row B, the scheduler's half): a swap that
// fails leaves the roster as it was, and the relaunch launches the running
// roster unchanged, with no start of the key press; the failure is a
// decision. Reverted (the roster written before Swap, or regardless of its
// error), the roster is the reload's after the failure.
func TestAFailedSwapRelaunchesTheRunningRosterUnchanged(t *testing.T) {
	h, fr := newRosterHarness(t, Config{MaxSeats: 2, IdleTimeout: time.Hour, SeatStartTimeout: time.Minute}, rosterAB...)
	h.tick()
	h.s.RequestRelaunchReload("add s-c", t0, &RosterReload{Roster: []RosterSeat{{Handle: "s-a", Pool: sonnet}, {Handle: "s-b", Pool: gpt}, {Handle: "s-c", Pool: opus}},
		Swap: func() error { return errors.New("injected between the writes") }})
	ds := h.tick()
	if !has(ds, "roster_reload_failed", "", "") || has(ds, "roster_reloaded", "", "") || rosterHandles(h.s.Config.Roster) != rosterHandles(rosterAB) {
		t.Fatalf("after a failed swap: roster %s; %+v", rosterHandles(h.s.Config.Roster), ds)
	}
	if len(fr.launches) != 2 || rosterHandles(asRoster(fr.launches[1])) != rosterHandles(rosterAB) || !fr.launches[1][0].Start.IsZero() || !fr.launches[1][1].Start.IsZero() {
		t.Fatalf("the relaunch after a failed swap: %+v", fr.launches)
	}
}

func asRoster(reqs []SeatRequest) []RosterSeat {
	var out []RosterSeat
	for _, r := range reqs {
		out = append(out, RosterSeat{Handle: r.Handle, Pool: r.Pool})
	}
	return out
}

// Brief 31-2 PR 2, the lease cap (the lead's gate on 5d20b5ff): a squad of
// two roster seats under a cap of 2 adds a third through a reload whose
// recomputed cap is 3; after the relaunch the third seat gets a lease while
// the other two hold theirs. Under a recomputed cap of 2 the third seat
// still launches (never a refusal of the add), roster_over_cap is logged
// for the screen, and its task waits seat_cap while the two others hold
// leases. Reverted (the cap fixed at the start), the third task waits under
// the cap of 3 too.
func TestAReloadRecomputesTheLeaseCap(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cap    int
		leased int
		over   bool
	}{{"cap 3", 3, 3, false}, {"cap 2", 2, 2, true}} {
		t.Run(tc.name, func(t *testing.T) {
			h, fr := newRosterHarness(t, Config{MaxSeats: 2, IdleTimeout: time.Hour, SeatStartTimeout: time.Minute}, rosterAB...)
			h.tick()
			next := append(slices.Clone(rosterAB), RosterSeat{Handle: "s-c", Pool: opus})
			h.s.RequestRelaunchReload("add s-c", t0, &RosterReload{Roster: next, MaxSeats: tc.cap})
			ds := h.tick()
			if len(fr.launches) != 2 || len(fr.launches[1]) != 3 || fr.launches[1][2].Handle != "s-c" || h.s.Config.MaxSeats != tc.cap {
				t.Fatalf("the relaunch: launches %+v, cap %d", fr.launches, h.s.Config.MaxSeats)
			}
			if has(ds, RosterOverCap, "", "s-c") != tc.over {
				t.Fatalf("roster_over_cap logged %v, want %v: %+v", !tc.over, tc.over, ds)
			}
			var ids []string
			for _, p := range []*task.Pool{&sonnet, &gpt, &opus} {
				ids = append(ids, h.add("t-"+p.Model, p).ID)
			}
			h.tick()
			h.tick()
			leased := 0
			for _, id := range ids {
				if h.get(id).Lease != nil {
					leased++
				}
			}
			third := h.get(ids[2])
			if leased != tc.leased || (tc.over && (third.Lease != nil || third.WaitReason != task.WaitSeatCap)) || (!tc.over && (third.Lease == nil || third.Lease.Holder != "s-c")) {
				t.Fatalf("leased %d, want %d; the third task %+v", leased, tc.leased, third)
			}
		})
	}
}
