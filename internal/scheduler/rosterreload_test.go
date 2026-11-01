package scheduler

import (
	"errors"
	"path/filepath"
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

// The plan reviewer before the merge (the key press): an added seat is an
// existing seat from its next relaunch on, and each launch's start is that
// launch's. s-c added at T1, then s-d at T2: the relaunch s-c's press
// caused carries T1 for s-c; the one s-d's press caused carries T2 for s-d
// and none for s-c; a plain relaunch after it carries none for either (each
// seat takes that launch's start; seatlaunch keeps the presses as added_at,
// TestAnAddedSeatKeepsAddedAtAndTakesEachLaunchsStart). Reverted (the press
// kept as a start), the later relaunches carry T1 and T2 again.
func TestTheKeyPressIsTheStartOfTheLaunchItCausedOnly(t *testing.T) {
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
	if got := starts(fr.launches[1]); !got["s-c"].Equal(t1) || !got["s-a"].IsZero() || !got["s-b"].IsZero() {
		t.Fatalf("s-c's own relaunch: %v", got)
	}
	if got := starts(fr.launches[2]); !got["s-c"].IsZero() || !got["s-d"].Equal(t2) {
		t.Fatalf("s-d's own relaunch: %v", got)
	}
	for h, at := range starts(fr.launches[3]) {
		if !at.IsZero() {
			t.Fatalf("a plain relaunch carried a press for %s: %s", h, at)
		}
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

// Brief 31-2 PR 2, the lease cap (the lead's gate on 5d20b5ff; the plan
// reviewer's ruling before the merge): a squad of two roster seats under a
// cap of 2 adds a third through a reload whose recomputed cap is 3; after
// the relaunch the third seat gets a lease while the other two hold
// theirs. Under a recomputed cap of 2 the reload (schedproc trims it, in
// squad.yaml order) launches the first two seats only: s-c is not
// launched, roster_over_cap names it with the cure, the state status reads
// lists it as unlaunched, and its task gets no lease. Reverted (the cap
// fixed at the start), the third task waits under the cap of 3 too.
func TestAReloadRecomputesTheLeaseCap(t *testing.T) {
	for _, tc := range []struct {
		name       string
		cap        int
		roster     []RosterSeat
		unlaunched []string
		leased     int
	}{
		{"cap 3", 3, append(slices.Clone(rosterAB), RosterSeat{Handle: "s-c", Pool: opus}), nil, 3},
		{"cap 2", 2, slices.Clone(rosterAB), []string{"s-c"}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "scheduler.json")
			h, fr := newRosterHarness(t, Config{MaxSeats: 2, IdleTimeout: time.Hour, SeatStartTimeout: time.Minute, StatePath: path}, rosterAB...)
			h.tick()
			h.s.RequestRelaunchReload("add s-c", t0, &RosterReload{Roster: tc.roster, MaxSeats: tc.cap, Unlaunched: tc.unlaunched})
			ds := h.tick()
			launched := asRoster(fr.launches[len(fr.launches)-1])
			if len(fr.launches) != 2 || rosterHandles(launched) != rosterHandles(tc.roster) || h.s.Config.MaxSeats != tc.cap {
				t.Fatalf("the relaunch: launched %s, cap %d", rosterHandles(launched), h.s.Config.MaxSeats)
			}
			over := has(ds, RosterOverCap, "", "s-c")
			if over != (tc.unlaunched != nil) {
				t.Fatalf("roster_over_cap logged %v: %+v", over, ds)
			}
			for _, d := range ds {
				if d.Kind == RosterOverCap && (!strings.Contains(d.Reason, "max_seats 2") || !strings.Contains(d.Reason, "widen the grant or remove the seat")) {
					t.Fatalf("the decision: %+v", d)
				}
			}
			if st, err := ReadState(path); err != nil || strings.Join(st.Unlaunched, ",") != strings.Join(tc.unlaunched, ",") {
				t.Fatalf("the state's unlaunched seats: %v (%v)", st.Unlaunched, err)
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
			if leased != tc.leased || (tc.unlaunched != nil && third.Lease != nil) || (tc.unlaunched == nil && (third.Lease == nil || third.Lease.Holder != "s-c")) {
				t.Fatalf("leased %d, want %d; the third task %+v", leased, tc.leased, third)
			}
		})
	}
}

// Codex P2 3 on 31-2 PR 2: the state the tick writes agrees with the swap.
// scheduler.json read right after the relaunch's tick names the new roster
// (s-c added, s-b removed) and the new lease cap, beside the seats just
// launched. Reverted (the tick's config captured before the swap), it names
// the old roster and cap until the next tick.
func TestTheStateWrittenInTheRelaunchsTickNamesTheNewRoster(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scheduler.json")
	h, fr := newRosterHarness(t, Config{MaxSeats: 2, IdleTimeout: time.Hour, SeatStartTimeout: time.Minute, StatePath: path}, rosterAB...)
	h.tick()
	h.s.RequestRelaunchReload("swap s-b for s-c", t0, &RosterReload{Roster: []RosterSeat{{Handle: "s-a", Pool: sonnet}, {Handle: "s-c", Pool: opus}}, MaxSeats: 3})
	h.tick()
	if len(fr.launches) != 2 {
		t.Fatalf("launches %d", len(fr.launches))
	}
	st, err := ReadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(st.Roster, ",") != "s-a,s-c" || st.MaxSeats != 3 {
		t.Fatalf("scheduler.json after the relaunch's tick: roster %v, max_seats %d", st.Roster, st.MaxSeats)
	}
}
