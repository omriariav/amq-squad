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

// Codex P2 2 on 31-2 PR 2, the launch's half: a seat keeps its own start
// across relaunches. cto was added at T1: its first launch writes T1 and a
// drain end. A later roster launch whose requests carry T1 for cto (kept
// by the scheduler, TestAnAddedSeatsStartIsKeptUntilItLeavesTheRoster) and
// T2 for dev, added since, writes cto's marks with T1 again and no drain
// end (that launch's wait is not cto's drain), and dev's with T2 and its
// drain end. Reverted (the scheduler dropping cto's start), cto's marks
// fall back to the squad's start.
func TestAnAddedSeatKeepsItsStartAcrossRelaunches(t *testing.T) {
	f := newRosterFixture(t, launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "applied", Disposition: "committed",
		Observations: []launchapi.ParticipantObservationV1{{Handle: "cto", Runnable: true, Resource: "tmux:v1:pane:%7"}, {Handle: "dev", Runnable: true, Resource: "tmux:v1:pane:%8"}}}, true)
	f.l.Options.Start = time.Now().Add(-2 * time.Hour).UTC()
	t1 := time.Now().Add(-time.Hour).UTC()
	t2 := time.Now().Add(-11 * time.Second).UTC()
	if out := f.l.LaunchRoster([]scheduler.SeatRequest{{ID: "r1", Handle: "cto", Pool: poolA, Start: t1}, {ID: "r2", Handle: "dev", Pool: poolDev}}); out.Stage != scheduler.RosterApplied {
		t.Fatalf("the first launch: %+v", out)
	}
	root := f.q.SessionRoot()
	if cto, _ := ReadSeatMarks(root, "cto"); cto.Start == nil || !cto.Start.Equal(t1) || cto.DrainEnd == nil {
		t.Fatalf("cto after its own relaunch: %+v", cto)
	}
	out := f.l.LaunchRoster([]scheduler.SeatRequest{{ID: "r3", Handle: "cto", Pool: poolA, Start: t1}, {ID: "r4", Handle: "dev", Pool: poolDev, Start: t2}})
	if out.Stage != scheduler.RosterApplied {
		t.Fatalf("the second launch: %+v", out)
	}
	cto, _ := ReadSeatMarks(root, "cto")
	dev, _ := ReadSeatMarks(root, "dev")
	if cto.Start == nil || !cto.Start.Equal(t1) || cto.DrainEnd != nil || cto.LaunchID != out.LaunchID {
		t.Fatalf("cto after a later relaunch: %+v (want T1 %s, no drain end)", cto, t1)
	}
	if dev.Start == nil || !dev.Start.Equal(t2) || dev.DrainEnd == nil || dev.LaunchID != out.LaunchID {
		t.Fatalf("dev, added since: %+v (want T2 %s and a drain end)", dev, t2)
	}
}
