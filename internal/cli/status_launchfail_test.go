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
	_, root, proj := schedulerFixture(t)
	f := &refusingRoster{out: scheduler.RosterOutcome{Stage: "apply_failed", ReasonCode: scheduler.AMQBindingPresent, Detail: "launch: the session binding is present"}}
	schedulerAMQLauncher = func(sl *schedproc.Slots, _ string, _ seatlaunch.Options) scheduler.SeatLauncher { f.sl = sl; return f }
	var started *scheduler.Scheduler
	schedulerStarted = func(s *scheduler.Scheduler) { started = s }
	_, _, err := captureOutput(t, func() error {
		return runSchedulerRun(context.Background(), []string{"--project", proj, "--tick", "20ms"}, os.Stdout)
	})
	if ExitCode(err) != ExitSchedulerRefused {
		t.Fatalf("the refusal: %v (exit %d)", err, ExitCode(err))
	}
	if lv, ok := started.Liveness.(*schedproc.Liveness); !ok || lv.Root != root || lv.ReadBound != schedproc.ProofReadBound(20*time.Millisecond, 1) {
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

// Codex finding 1 on t17, end to end through the verb and status: the real
// seat launcher, its AMQ answering prepare with output that is not JSON
// and a credential key on stderr's first line with its quoted value on the
// next. The scheduler ends with the failure; the value is in none of the
// error, the decisions log, the state file, status --json or the status
// table, which all name the failure. Reverted (AMQ's stderr wrapped before
// its redaction), every one of them carries the value.
func TestInvalidJSONThroughTheVerbLeavesNoCredentialInStatus(t *testing.T) {
	_, root, proj := schedulerFixture(t)
	value := "t17" + "statusvalue" + "0095"
	stderr := "api" + "_key:\n  \"" + value + "\""
	var real *seatlaunch.Launcher
	schedulerAMQLauncher = func(sl *schedproc.Slots, r string, opts seatlaunch.Options) scheduler.SeatLauncher {
		opts.RunAMQ = func(context.Context, string, []string, []byte, []string, string) ([]byte, []byte, int, error) {
			return []byte("amq: not json"), []byte(stderr), 2, nil
		}
		opts.LookPath = func(name string) (string, error) { return "/usr/local/bin/" + name, nil }
		opts.Home, opts.Env = t.TempDir(), []string{"PATH=/usr/bin"}
		real = &seatlaunch.Launcher{Plan: sl.Plan, PlanRoster: sl.PlanRoster, Options: opts, Root: r,
			PaneGone: func(string) (bool, bool) { return true, true }, PanesFor: func(string) (int, bool) { return 0, true }}
		return real
	}
	schedulerStarted = func(s *scheduler.Scheduler) { real.Post = s.Post }
	_, _, err := captureOutput(t, func() error {
		return runSchedulerRun(context.Background(), []string{"--project", proj, "--tick", "20ms"}, os.Stdout)
	})
	if err == nil || !strings.Contains(err.Error(), "without a PrepareResult") || strings.Contains(err.Error(), value) {
		t.Fatalf("the verb's error: %v", err)
	}
	for _, p := range []string{filepath.Join(schedproc.Dir(root), "decisions.jsonl"), scheduler.StatePathFor(root)} {
		raw, err := os.ReadFile(p)
		if err != nil || !strings.Contains(string(raw), "without a PrepareResult") || strings.Contains(string(raw), value) {
			t.Fatalf("%s (%v):\n%s", p, err, raw)
		}
	}
	doc := buildStatusContract(root, "", "3.0.1-test", time.Now())
	js, _ := json.Marshal(doc)
	var table strings.Builder
	if err := writeStatusContractTable(&table, doc); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(js), "without a PrepareResult") || strings.Contains(string(js), value) || strings.Contains(table.String(), value) || !strings.Contains(table.String(), "seat_launch_failed") {
		t.Fatalf("status:\n%s\n%s", js, table.String())
	}
}
