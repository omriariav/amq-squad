package schedproc

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

func TestTamperHoldBesideCancelAndCleanHasARecoveryDecision(t *testing.T) {
	t.Run("cancelled attempt refuses at clean", func(t *testing.T) {
		r := newTamperedSeatRig(t)
		installRealCancelPath(t, r)
		if err := os.WriteFile(commitLedgerPath(r), r.original, 0o600); err != nil {
			t.Fatal(err)
		}
		r.ticks(3)
		got, err := r.store.Get(r.tk.ID)
		if err != nil || got.Lease == nil {
			t.Fatalf("task was not dispatched: %+v, %v", got, err)
		}
		if _, err := r.store.Claim(got.ID, got.Lease.Holder, nil, time.Hour, r.now); err != nil {
			t.Fatal(err)
		}
		if _, err := r.store.Cancel(got.ID, "lead", "stop", r.now); err != nil {
			t.Fatal(err)
		}
		cleaner := &tamperBeforeAttemptClean{CleanSeats: r.s.Preparer.(*CleanSeats), before: func() error {
			return os.WriteFile(commitLedgerPath(r), r.tampered, 0o600)
		}}
		r.s.Preparer = cleaner
		five := r.ticks(5)
		assertDecisionCounts(t, five, map[string]int{"seat_closing": 1, "seat_closed": 1, "seat_clean_deferred": 1, "exit_refused": 1})
		if cleaner.calls != 1 {
			t.Fatalf("real clean calls = %d, want the one clean that discovered the tamper", cleaner.calls)
		}
		if _, held := r.s.HeldAlarm(r.handle); held {
			t.Fatal("a clean refusal unexpectedly became a tamper-alarm hold")
		}
		assertQuarantineSlots(t, r, 1)
		reply := recoverTamperedRig(r)
		if !reply.OK {
			t.Fatalf("slot recovery = %+v", reply)
		}
		next := r.ticks(1)
		assertDecisionCounts(t, next, map[string]int{"seat_cleaned": 1})
	})

	t.Run("blocked task is cancelled", func(t *testing.T) {
		r := newTamperedSeatRig(t)
		installRealCancelPath(t, r)
		r.ticks(5)
		if _, err := r.store.Cancel(r.tk.ID, "lead", "stop the blocked task", r.now); err != nil {
			t.Fatal(err)
		}
		five := r.ticks(5)
		assertDecisionCounts(t, five, map[string]int{})
		if _, held := r.s.HeldAlarm(r.handle); !held {
			t.Fatal("cancelling the blocked task dropped its tamper hold")
		}
		assertQuarantineSlots(t, r, 1)
		reply := recoverTamperedRig(r)
		if !reply.OK {
			t.Fatalf("slot recovery = %+v", reply)
		}
		next := r.ticks(1)
		assertDecisionCounts(t, next, map[string]int{"ledger_hold_released": 1})
	})

}

type tamperBeforeAttemptClean struct {
	*CleanSeats
	before func() error
	calls  int
}

func TestCancelReservationProcessHelper(t *testing.T) {
	root := os.Getenv("AMQ_TEST_CANCEL_MARKER_ROOT")
	if root == "" {
		return
	}
	store, err := task.Open(root)
	if err != nil {
		os.Exit(2)
	}
	taskID := os.Getenv("AMQ_TEST_CANCEL_MARKER_TASK")
	seat := os.Getenv("AMQ_TEST_CANCEL_MARKER_SEAT")
	_, nonce, _, err := store.ReserveCancel(seat, taskID, 1, time.Now())
	if err != nil {
		os.Exit(3)
	}
	if err := os.WriteFile(os.Getenv("AMQ_TEST_CANCEL_MARKER_READY"), []byte("ready\n"), 0o600); err != nil {
		os.Exit(4)
	}
	stop := os.Getenv("AMQ_TEST_CANCEL_MARKER_STOP")
	if stop == "" {
		return // simulate a crashed scheduler: leave the marker behind
	}
	defer store.ReleaseSeat(seat, nonce)
	for {
		if _, err := os.Stat(stop); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// P12(c) at the reachable process boundary. A separate live scheduler
// process owns the cancel reservation while the lead's no-scheduler clean
// reaches ReserveSeatWithDecision and is refused. If that scheduler has
// crashed, the same caller takes the marker over and completes the clean.
func TestLeadCleanMeetsCancelReservationFromAnotherProcess(t *testing.T) {
	for _, tc := range []struct {
		name string
		live bool
	}{
		{name: "live scheduler refuses the lead clean", live: true},
		{name: "crashed scheduler marker is taken over", live: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSocketCleanFixture(t)
			ready := filepath.Join(t.TempDir(), "ready")
			stop := ""
			if tc.live {
				stop = filepath.Join(t.TempDir(), "stop")
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestCancelReservationProcessHelper$")
			var child bytes.Buffer
			cmd.Stdout, cmd.Stderr = &child, &child
			cmd.Env = append(os.Environ(),
				"AMQ_TEST_CANCEL_MARKER_ROOT="+f.root,
				"AMQ_TEST_CANCEL_MARKER_SEAT="+f.handle,
				"AMQ_TEST_CANCEL_MARKER_TASK="+f.task.ID,
				"AMQ_TEST_CANCEL_MARKER_READY="+ready,
				"AMQ_TEST_CANCEL_MARKER_STOP="+stop)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				if _, err := os.Stat(ready); err == nil {
					break
				}
				if time.Now().After(deadline) {
					_ = cmd.Process.Kill()
					t.Fatalf("reservation helper was not ready: %s", child.String())
				}
				time.Sleep(10 * time.Millisecond)
			}
			if tc.live {
				t.Cleanup(func() {
					_ = os.WriteFile(stop, []byte("stop\n"), 0o600)
					_ = cmd.Wait()
				})
			} else if err := cmd.Wait(); err != nil {
				t.Fatalf("reservation helper: %v\n%s", err, child.String())
			}

			marker := filepath.Join(f.store.Dir, "reserved", f.handle)
			var record map[string]any
			if err := json.Unmarshal(mustRead(t, marker), &record); err != nil || record["cancel"] != f.task.ID+"#1" {
				t.Fatalf("cancel marker = %+v, %v", record, err)
			}
			var decisions bytes.Buffer
			cleaner := &CleanSeats{Root: f.root, Git: "git"}
			cleaner.SetLog(&decisions, func() time.Time { return time.Now().UTC() })
			reply := manualCleanDecision(f.store, cleanDecisionLiveness{gone: true}, cleaner)("lead", CleanRequest{Seat: f.handle})
			if tc.live {
				if reply.OK || reply.Code != "seat_clean_failed" || !strings.Contains(reply.Detail, "being cleaned by pid") {
					t.Fatalf("lead clean beside live cancel marker = %+v", reply)
				}
				if _, err := os.Stat(marker); err != nil {
					t.Fatalf("live cancel marker disappeared: %v", err)
				}
				return
			}
			if !reply.OK || !strings.Contains(decisions.String(), `"kind":"cleaner_marker_taken_over"`) {
				t.Fatalf("lead clean after crashed scheduler = %+v decisions=%s", reply, decisions.String())
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("taken-over marker remains: %v", err)
			}
		})
	}
}

func (c *tamperBeforeAttemptClean) CleanAttempt(handle, taskID string, attempt int, outcome string) error {
	c.calls++
	before := c.before
	c.before = nil
	if before != nil {
		if err := before(); err != nil {
			return err
		}
	}
	return c.CleanSeats.CleanAttempt(handle, taskID, attempt, outcome)
}

func installRealCancelPath(t *testing.T, r *tamperedSeatRig) {
	t.Helper()
	r.c.ExitStep = func(handle, taskID, why string) seatcommit.Result {
		return seatcommit.ExitCheck(seatcommit.Options{Root: r.root, Handle: handle, Task: mustTask(t, r, taskID), Now: func() time.Time { return r.now }}, why)
	}
	r.s.Liveness = cleanDecisionLiveness{gone: true}
}

func mustTask(t *testing.T, r *tamperedSeatRig, id string) task.Task {
	t.Helper()
	got, err := r.store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func commitLedgerPath(r *tamperedSeatRig) string {
	return filepath.Join(seatcommit.LedgerDir(r.root), r.handle+".json")
}

func recoverTamperedRig(r *tamperedSeatRig) DecideReply {
	return recoverTamperedRigSeq(r, "1")
}

func recoverTamperedRigSeq(r *tamperedSeatRig, seq string) DecideReply {
	decide := recoverDecision(r.s, r.root, Recovery{Root: r.root, Target: r.target, Git: "git", Now: func() time.Time { return r.now }}.Apply, func() time.Time { return r.now })
	return decide("lead", RecoverRequest{Seat: r.handle, Seq: seq, Slot: 1, Reason: "recorded cancel and clean interaction"})
}

func assertQuarantineSlots(t *testing.T, r *tamperedSeatRig, want int) {
	t.Helper()
	entries, err := os.ReadDir(seatcommit.QuarantineDir(r.root))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != want {
		t.Fatalf("quarantine slots = %d, want %d: %v", len(entries), want, entries)
	}
}

func assertDecisionCounts(t *testing.T, ds []scheduler.Decision, want map[string]int) {
	t.Helper()
	got := map[string]int{}
	for _, d := range ds {
		got[d.Kind]++
	}
	for kind, n := range want {
		if got[kind] != n {
			t.Fatalf("decision %s count = %d, want %d; all=%v", kind, got[kind], n, got)
		}
	}
	for kind, n := range got {
		if _, ok := want[kind]; !ok && n != 0 {
			t.Fatalf("unexpected decision %s count = %d; all=%v", kind, n, got)
		}
	}
}
