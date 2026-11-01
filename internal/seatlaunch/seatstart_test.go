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
// press as dev's marks.json start, while cto, a seat that was already in
// the roster, keeps the launch's start. The launch budget for dev then
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

// The plan reviewer before the merge (the key press), the launch's half:
// the press is the start of the launch it caused only, and stays on the
// seat as added_at. cto added at T1: its own launch writes start T1,
// added_at T1 and a drain end. The launch dev's press (T2) caused writes
// dev's start T2, added_at T2 and a drain end, and cto's start as that
// launch's (Options.Start), added_at T1 kept, no drain end. A plain launch
// after it gives both seats that launch's start, added_at T1 and T2 kept,
// no drain end. Reverted (added_at not carried), cto's and dev's added_at
// are gone after the next launch.
func TestAnAddedSeatKeepsAddedAtAndTakesEachLaunchsStart(t *testing.T) {
	f := newRosterFixture(t, launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "applied", Disposition: "committed",
		Observations: []launchapi.ParticipantObservationV1{{Handle: "cto", Runnable: true, Resource: "tmux:v1:pane:%7"}, {Handle: "dev", Runnable: true, Resource: "tmux:v1:pane:%8"}}}, true)
	launchStart := time.Now().Add(-2 * time.Hour).UTC()
	f.l.Options.Start = launchStart
	t1 := time.Now().Add(-time.Hour).UTC()
	t2 := time.Now().Add(-11 * time.Second).UTC()
	root := f.q.SessionRoot()
	type want struct {
		start, added time.Time
		drain        bool
	}
	check := func(label string, reqs []scheduler.SeatRequest, w map[string]want) {
		t.Helper()
		out := f.l.LaunchRoster(reqs)
		if out.Stage != scheduler.RosterApplied {
			t.Fatalf("%s: %+v", label, out)
		}
		for h, x := range w {
			m, err := ReadSeatMarks(root, h)
			addedOK := (x.added.IsZero() && m.AddedAt == nil) || (m.AddedAt != nil && m.AddedAt.Equal(x.added))
			if err != nil || m.LaunchID != out.LaunchID || m.Start == nil || !m.Start.Equal(x.start) || !addedOK || (m.DrainEnd != nil) != x.drain {
				t.Fatalf("%s: %s's marks %+v (%v); want start %s, added_at %s, drain end %v", label, h, m, err, x.start, x.added, x.drain)
			}
		}
	}
	check("cto's own launch", []scheduler.SeatRequest{{ID: "r1", Handle: "cto", Pool: poolA, Start: t1}, {ID: "r2", Handle: "dev", Pool: poolDev}},
		map[string]want{"cto": {t1, t1, true}, "dev": {launchStart, time.Time{}, false}})
	check("dev's own launch", []scheduler.SeatRequest{{ID: "r3", Handle: "cto", Pool: poolA}, {ID: "r4", Handle: "dev", Pool: poolDev, Start: t2}},
		map[string]want{"cto": {launchStart, t1, false}, "dev": {t2, t2, true}})
	check("a plain launch", []scheduler.SeatRequest{{ID: "r5", Handle: "cto", Pool: poolA}, {ID: "r6", Handle: "dev", Pool: poolDev}},
		map[string]want{"cto": {launchStart, t1, false}, "dev": {launchStart, t2, false}})
}
