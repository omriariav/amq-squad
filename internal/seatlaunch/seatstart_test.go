//go:build unix

package seatlaunch

import (
	"testing"
	"time"

	"github.com/avivsinai/agent-message-queue/launchapi"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
)

// Brief 31-2 PR 2, change 4 (rows 4 and D, the launch's half): a roster
// launch whose request for dev carries its own start (the add key's
// press, which comes before the relaunch's decision stamp) writes that
// press as dev's marks.json start, while cto, whose request carries no
// start of its own (as at the launch at scheduler run), keeps the launch's
// start. The launch budget for dev then
// counts from the key press: dev went live more than the 10 s ceiling
// after the press, though less than 10 s after the relaunch's decision, so
// status (screen.go's launchTiming over marks.json and the proof) shows it
// over the ceiling. Reverted (the launch's start for every seat), dev's
// mark is the squad's start.
func TestTheAddedSeatsMarksStartIsTheKeyPress(t *testing.T) {
	f := newRosterFixture(t, launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "applied", Disposition: "committed",
		Observations: []launchapi.ParticipantObservationV1{{Handle: "cto", Runnable: true, Resource: "tmux:v1:pane:%7"}, {Handle: "dev", Runnable: true, Resource: "tmux:v1:pane:%8"}}}, true)
	squadStart := time.Now().Add(-2 * time.Hour).UTC()
	keyPress := time.Now().Add(-11 * time.Second).UTC()
	decision := time.Now().Add(-2 * time.Second).UTC() // the relaunch's roster_relaunch_requested stamp
	f.l.Options.Start = squadStart
	out := f.l.LaunchRoster([]scheduler.SeatRequest{{ID: "r1", Handle: "cto", Pool: poolA}, {ID: "r2", Handle: "dev", Pool: poolDev, Start: keyPress}})
	if out.Stage != scheduler.RosterApplied {
		t.Fatalf("the launch: %+v", out)
	}
	root := f.q.SessionRoot()
	cto, err := ReadSeatMarks(root, "cto")
	if err != nil || cto.Start == nil || !cto.Start.Equal(squadStart) || cto.LaunchID != out.LaunchID {
		t.Fatalf("cto's marks %+v (%v): want the launch's start %s", cto, err, squadStart)
	}
	dev, err := ReadSeatMarks(root, "dev")
	if err != nil || dev.Start == nil || !dev.Start.Equal(keyPress) || dev.LaunchID != out.LaunchID {
		t.Fatalf("dev's marks %+v (%v): want the key press %s", dev, err, keyPress)
	}
	proof, err := launch.ReadProof(root, "dev")
	if err != nil {
		t.Fatal(err)
	}
	const ceiling = 10 * time.Second // status.LaunchCeiling
	if live := proof.At.Sub(*dev.Start); live <= ceiling || proof.At.Sub(decision) >= ceiling {
		t.Fatalf("dev live %s after the key press, %s after the decision: the budget must count from the press", live, proof.At.Sub(decision))
	}
	// the drain's end (the roster launch's start) is dev's, after the key
	// press and before seat live; cto, with the squad's start, has none
	// (status names the segment from the press to it "drain")
	if dev.DrainEnd == nil || dev.DrainEnd.Before(keyPress) || dev.DrainEnd.After(proof.At) || cto.DrainEnd != nil {
		t.Fatalf("drain ends: dev %v, cto %v", dev.DrainEnd, cto.DrainEnd)
	}
	if f.l.Options.SeatStart != nil {
		t.Fatalf("the launcher's own options kept a request's start: %+v", f.l.Options.SeatStart)
	}
}

// The plan reviewer's settled reading, the launch's half: every relaunch
// stamps every seat it launches with its own start, and drain_end is
// written for each of them; the add key's press is the added seat's start
// for its own relaunch and its added_at from then on. cto added at T1 by a
// relaunch decided at R1: cto starts at T1 with added_at T1, dev at R1;
// both carry a drain end. A relaunch decided at R2 that adds dev at T2 gives cto start R2 (added_at T1 kept) and dev start T2 with
// added_at T2. A plain relaunch at R3 gives both seats R3, added_at T1 and
// T2 kept, and a drain end each. Reverted (added_at not carried, or the
// relaunch's stamp not written), the marks lose the press or keep the
// squad's start.
func TestAnAddedSeatKeepsAddedAtAndTakesEachLaunchsStart(t *testing.T) {
	f := newRosterFixture(t, launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "applied", Disposition: "committed",
		Observations: []launchapi.ParticipantObservationV1{{Handle: "cto", Runnable: true, Resource: "tmux:v1:pane:%7"}, {Handle: "dev", Runnable: true, Resource: "tmux:v1:pane:%8"}}}, true)
	f.l.Options.Start = time.Now().Add(-2 * time.Hour).UTC() // AMQ_SQUAD_START: the start of the launch at scheduler run only
	t1, r1 := time.Now().Add(-time.Hour).UTC(), time.Now().Add(-time.Hour+time.Second).UTC()
	t2, r2 := time.Now().Add(-11*time.Second).UTC(), time.Now().Add(-10*time.Second).UTC()
	r3 := time.Now().Add(-time.Second).UTC()
	root := f.q.SessionRoot()
	type want struct{ start, added time.Time }
	check := func(label string, reqs []scheduler.SeatRequest, w map[string]want) {
		t.Helper()
		out := f.l.LaunchRoster(reqs)
		if out.Stage != scheduler.RosterApplied {
			t.Fatalf("%s: %+v", label, out)
		}
		for h, x := range w {
			m, err := ReadSeatMarks(root, h)
			addedOK := (x.added.IsZero() && m.AddedAt == nil) || (m.AddedAt != nil && m.AddedAt.Equal(x.added))
			if err != nil || m.LaunchID != out.LaunchID || m.Start == nil || !m.Start.Equal(x.start) || !addedOK || m.DrainEnd == nil {
				t.Fatalf("%s: %s's marks %+v (%v); want start %s, added_at %s and a drain end", label, h, m, err, x.start, x.added)
			}
		}
	}
	check("cto's relaunch", []scheduler.SeatRequest{{ID: "r1", Handle: "cto", Pool: poolA, Start: t1, AddedAt: t1}, {ID: "r2", Handle: "dev", Pool: poolDev, Start: r1}},
		map[string]want{"cto": {t1, t1}, "dev": {r1, time.Time{}}})
	check("dev's relaunch", []scheduler.SeatRequest{{ID: "r3", Handle: "cto", Pool: poolA, Start: r2}, {ID: "r4", Handle: "dev", Pool: poolDev, Start: t2, AddedAt: t2}},
		map[string]want{"cto": {r2, t1}, "dev": {t2, t2}})
	check("a plain relaunch", []scheduler.SeatRequest{{ID: "r5", Handle: "cto", Pool: poolA, Start: r3}, {ID: "r6", Handle: "dev", Pool: poolDev, Start: r3}},
		map[string]want{"cto": {r3, t1}, "dev": {r3, t2}})
}
