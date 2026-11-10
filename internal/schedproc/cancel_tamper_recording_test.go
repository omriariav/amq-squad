package schedproc

import (
	"os"
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
		if err := os.WriteFile(commitLedgerPath(r), r.tampered, 0o600); err != nil {
			t.Fatal(err)
		}
		five := r.ticks(5)
		assertDecisionCounts(t, five, map[string]int{"seat_closing": 1, "seat_closed": 1, "exit_refused": 1})
		if _, held := r.s.HeldAlarm(r.handle); held {
			t.Fatal("an exit refusal unexpectedly became a tamper-alarm hold")
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

	t.Run("lead clean during the cancel reservation", func(t *testing.T) {
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
		clean := manualCleanDecision(r.store, cleanDecisionLiveness{gone: true}, r.s.Preparer)
		launcher := &cleanDuringCancelClose{base: r.l, clean: func() DecideReply {
			return clean("lead", CleanRequest{Seat: r.handle})
		}, after: func() error { return os.WriteFile(commitLedgerPath(r), r.tampered, 0o600) }}
		r.s.Launcher = launcher
		five := r.ticks(5)
		assertDecisionCounts(t, five, map[string]int{"seat_closing": 1, "seat_closed": 1, "exit_refused": 1})
		if launcher.calls != 1 || launcher.reply.OK || launcher.reply.Code != "seat_clean_failed" || !strings.Contains(launcher.reply.Detail, "being cleaned") {
			t.Fatalf("lead clean during reservation = calls %d reply %+v", launcher.calls, launcher.reply)
		}
		if _, held := r.s.HeldAlarm(r.handle); held {
			t.Fatal("the cancel exit refusal unexpectedly became a tamper-alarm hold")
		}
		assertQuarantineSlots(t, r, 1)
		entries, err := os.ReadDir(seatcommit.QuarantineDir(r.root))
		if err != nil {
			t.Fatal(err)
		}
		name := strings.TrimPrefix(entries[0].Name(), "commits."+r.handle+".seq")
		seq, _, _ := strings.Cut(name, ".slot")
		if seq == "" || seq == entries[0].Name() {
			t.Fatalf("quarantine slot name = %q", entries[0].Name())
		}
		recovered := recoverTamperedRigSeq(r, seq)
		if !recovered.OK {
			t.Fatalf("slot recovery = %+v", recovered)
		}
		next := r.ticks(1)
		assertDecisionCounts(t, next, map[string]int{"seat_cleaned": 1})
	})
}

type cleanDuringCancelClose struct {
	base  scheduler.SeatLauncher
	clean func() DecideReply
	after func() error
	reply DecideReply
	calls int
}

func (c *cleanDuringCancelClose) RequestSeat(req scheduler.SeatRequest) error {
	return c.base.RequestSeat(req)
}

func (c *cleanDuringCancelClose) CancelSeatRequest(req scheduler.SeatRequest) error {
	return c.base.CancelSeatRequest(req)
}

func (c *cleanDuringCancelClose) CloseSeat(handle, reason string) error {
	c.calls++
	c.reply = c.clean()
	if c.after != nil {
		if err := c.after(); err != nil {
			return err
		}
	}
	return c.base.CloseSeat(handle, reason)
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
