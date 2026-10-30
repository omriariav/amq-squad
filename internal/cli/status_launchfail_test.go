package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
	"github.com/omriariav/amq-squad/v3/internal/status"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// t17 row 1, status: the scheduler's last launch failure of a pool shows
// on the failed seat's row (its reason in place of the starting or
// no-signal reason), on the waiting task's row (the scheduler wrote the
// reason as its wait reason in place of seat_starting) and in the
// attention queue with the stage and the detail. Reverted (status reads no
// launch failure), the seat row says no_signal and nothing names why.
func TestStatusShowsTheLastLaunchFailureReason(t *testing.T) {
	root := t.TempDir()
	chdir(t, root)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	prevNow, prevAlive := statusNow, statusPIDAlive
	statusNow, statusPIDAlive = func() time.Time { return now }, func(int) bool { return false }
	t.Cleanup(func() { statusNow, statusPIDAlive = prevNow, prevAlive })
	if err := os.MkdirAll(filepath.Join(root, "agents", "rev-1"), 0o700); err != nil {
		t.Fatal(err)
	}
	store, _ := task.Open(root)
	pool := task.Pool{Runtime: "codex", Model: "gpt-5.4", Effort: "medium"}
	tk, err := store.Add(task.AddInput{Title: "review the parser", Class: "implement", Pool: &pool}, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetWait(tk.ID, scheduler.AMQBindingPresent, now); err != nil {
		t.Fatal(err)
	}
	st := scheduler.State{UpdatedAt: now, MaxSeats: 2, Seats: []scheduler.SeatView{}, Refused: []scheduler.RefusedSeat{},
		LaunchFailures: []scheduler.LaunchFailure{{Pool: pool, Handle: "rev-1", RequestID: "seat-req-x-2", Stage: "apply_failed",
			ReasonCode: scheduler.AMQBindingPresent, Detail: "launch: the session binding is present", At: now.Add(-time.Minute), Count: 1}}}
	raw, _ := json.Marshal(st)
	mustWrite(t, scheduler.StatePathFor(root), string(raw))
	t.Setenv("AM_ROOT", root)
	text, _, err := captureOutput(t, func() error { return runStatusWithVersion([]string{"--json"}, "3.0.1-test") })
	if err != nil {
		t.Fatal(err)
	}
	var doc status.Status
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatalf("%v:\n%s", err, text)
	}
	var seat *status.Seat
	for i := range doc.Seats {
		if doc.Seats[i].Handle == "rev-1" {
			seat = &doc.Seats[i]
		}
	}
	if seat == nil || seat.ReasonCode != scheduler.AMQBindingPresent {
		t.Fatalf("the seat's row: %+v", doc.Seats)
	}
	if len(doc.Tasks) != 1 || doc.Tasks[0].ReasonCode != scheduler.AMQBindingPresent {
		t.Fatalf("the task's row: %+v", doc.Tasks)
	}
	found := false
	for _, a := range doc.Attention {
		if a.Kind == scheduler.KindSeatLaunchFailed && a.Seat == "rev-1" && strings.Contains(a.Subject, "stage apply_failed") &&
			strings.Contains(a.Subject, scheduler.AMQBindingPresent) && strings.Contains(a.Subject, "the session binding is present") {
			found = true
		}
	}
	if !found {
		t.Fatalf("attention %+v", doc.Attention)
	}
	table, _, err := captureOutput(t, func() error { return runStatusWithVersion(nil, "3.0.1-test") })
	if err != nil {
		t.Fatal(err)
	}
	ok := false
	sc := bufio.NewScanner(strings.NewReader(table))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) >= 6 && f[2] == "seat" && f[3] == "rev-1" && f[5] == scheduler.AMQBindingPresent {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("the table's seat row:\n%s", table)
	}
}

// t17, end to end through the verb: a roster launch AMQ refuses at Apply
// ends `scheduler run` with the refusal, its decisions log carries a
// seat_launch_failed line with the stage, the reason code and the detail,
// and the state it left makes status name the reason. The verb also wires
// the per-tick liveness check from the launch ledger of its session root.
// Reverted (no state written on the failure, or no Liveness wired), the
// status names nothing and a dead seat is never seen.
func TestSchedulerRunRefusalLeavesTheReasonForStatus(t *testing.T) {
	_, root, seatsFile := schedulerFixture(t)
	f := &refusingRoster{out: scheduler.RosterOutcome{Stage: "apply_failed", ReasonCode: scheduler.AMQBindingPresent, Detail: "launch: the session binding is present"}}
	schedulerAMQLauncher = func(sl *schedproc.Slots, _ string, _ seatlaunch.Options) scheduler.SeatLauncher { f.sl = sl; return f }
	var started *scheduler.Scheduler
	schedulerStarted = func(s *scheduler.Scheduler) { started = s }
	_, _, err := captureOutput(t, func() error {
		return runSchedulerRun(context.Background(), []string{"--seats", seatsFile, "--lead", "lead", "--tick", "20ms"}, os.Stdout)
	})
	if ExitCode(err) != ExitSchedulerRefused {
		t.Fatalf("the refusal: %v (exit %d)", err, ExitCode(err))
	}
	if lv, ok := started.Liveness.(*schedproc.Liveness); !ok || lv.Root != root {
		t.Fatalf("the liveness check: %#v", started.Liveness)
	}
	raw, err := os.ReadFile(filepath.Join(schedproc.Dir(root), "decisions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	logged := false
	for _, line := range strings.Split(string(raw), "\n") {
		var d scheduler.Decision
		if json.Unmarshal([]byte(line), &d) == nil && d.Kind == scheduler.KindSeatLaunchFailed && d.Seat == "impl-1" && d.Stage == "apply_failed" &&
			d.ReasonCode == scheduler.AMQBindingPresent && strings.Contains(d.Reason, "the session binding is present") {
			logged = true
		}
	}
	if !logged {
		t.Fatalf("decisions.jsonl:\n%s", raw)
	}
	doc := buildStatusContract(root, "", "3.0.1-test", time.Now())
	for _, a := range doc.Attention {
		if a.Kind == scheduler.KindSeatLaunchFailed && a.Seat == "impl-1" && strings.Contains(a.Subject, scheduler.AMQBindingPresent) {
			return
		}
	}
	t.Fatalf("status after the refusal: %+v", doc.Attention)
}
