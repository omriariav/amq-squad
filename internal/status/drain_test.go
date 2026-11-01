package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
)

// Brief 31-2 PR 2, the plan reviewer's line 1: a seat a relaunch launched
// from the add key carries its own start (the key press) and the drain's
// end in its marks; its launch budget names the segment between them
// "drain", ahead of prepare, mint, apply and seat_exec. On a busy squad the
// drain is the slowest, and the over-ceiling warning says so rather than
// blame seat_exec or prepare. A seat without a drain end keeps the four
// segments. Reverted (no drain segment), the 30 s wait is prepare's. The
// press is also shown as qa-2's added_at, and dev has none.
func TestARelaunchedSeatsBudgetNamesTheDrain(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(base, "root")
	squadStart := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	keyPress := squadStart.Add(time.Hour)
	if err := os.MkdirAll(schedproc.Dir(root), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := schedproc.WriteLaunchRecord(root, schedproc.LaunchRecord{Lead: "cto", Root: root, PID: 77, StartedAt: squadStart, Start: &squadStart}); err != nil {
		t.Fatal(err)
	}
	ms := time.Millisecond
	// 30 s of drain, then 200 ms to the staged record, then the launch
	budgetSeat(t, root, "qa-2", "codex", "gpt-5.6-sol", keyPress, 30200*ms, 602*ms, 538*ms, 144*ms, nil)
	drainEnd := keyPress.Add(30 * time.Second)
	marks, _ := json.Marshal(seatlaunch.SeatMarks{LaunchID: "launch-1", Start: &keyPress, DrainEnd: &drainEnd, AddedAt: &keyPress})
	if err := os.WriteFile(filepath.Join(launch.StagedDir(root, "qa-2"), seatlaunch.MarksFileName), marks, 0o600); err != nil {
		t.Fatal(err)
	}
	budgetSeat(t, root, "dev", "codex", "gpt-5.6-terra", squadStart, 3201*ms, 602*ms, 538*ms, 144*ms, nil)
	st := Build(Collect(CollectOptions{Root: root, Now: keyPress.Add(time.Minute), PIDAlive: func(int) bool { return false }}))
	s := seatByHandle(t, st, "qa-2")
	tm := s.Launch.Timing
	if names := segNames(tm.Segments); names != "drain,prepare,mint,apply,seat_exec" || deref64(tm.Segments[0].MS) != 30000 || deref64(tm.Segments[1].MS) != 200 {
		t.Fatalf("qa-2 segments %s %+v", names, tm.Segments)
	}
	if tm.SlowestSegment != "drain" || !tm.OverCeiling || deref64(tm.ToSeatLiveMS) != 31484 {
		t.Fatalf("qa-2 timing %+v", tm)
	}
	if w := hasWarning(s, WarnLaunchBudget); !strings.Contains(w, "slowest segment drain 30.000 s") {
		t.Fatalf("qa-2's warning %q", w)
	}
	if names := segNames(seatByHandle(t, st, "dev").Launch.Timing.Segments); names != "prepare,mint,apply,seat_exec" {
		t.Fatalf("dev segments %s", names)
	}
	// the press that added qa-2 is shown as its added_at (the plan reviewer
	// before the merge); dev, in the squad from its start, has none
	if s.Launch.AddedAt == nil || !s.Launch.AddedAt.Equal(keyPress) || seatByHandle(t, st, "dev").Launch.AddedAt != nil {
		t.Fatalf("added_at: qa-2 %v, dev %v", s.Launch.AddedAt, seatByHandle(t, st, "dev").Launch.AddedAt)
	}
}
