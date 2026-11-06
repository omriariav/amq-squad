package scheduler

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// The rows of 3.2.0-0c-B's scheduler half (tamper.go): a tamper alarm is
// a typed block raised once and the seat is held; a non-tamper prepare
// failure is logged once, then counted; the hold survives a restart; the
// lead's recovery releases the seat and returns the blocked task.

// tamperPreparer refuses one seat as tampered and prepares every other;
// it answers the recovery reader from what the test puts in its ledger
// stand-in (recover).
type tamperPreparer struct {
	seat    string
	calls   int
	err     error
	ledgers map[string]recoveryRecord
}

type recoveryRecord struct {
	seq   int
	slot  string
	alarm string
}

func (p *tamperPreparer) PrepareSeat(h string) error {
	if h == p.seat {
		p.calls++
		return p.err
	}
	return nil
}

// recover is the lead's recovery landing in the seat's sealed ledger (the
// reader's source of truth); the seat's prepare passes from then on.
func (p *tamperPreparer) recover(h string, seq int, slot string) {
	if p.ledgers == nil {
		p.ledgers = map[string]recoveryRecord{}
	}
	p.ledgers[h] = recoveryRecord{seq: seq, slot: slot}
	if h == p.seat {
		p.err = nil
	}
}

func (p *tamperPreparer) LedgerRecovery(h string) (RecoveryMark, bool) {
	r, ok := p.ledgers[h]
	return RecoveryMark{Seq: r.seq, Slot: r.slot, Alarm: r.alarm}, ok
}

var _ RecoveryReader = (*tamperPreparer)(nil)

func slotOf(seat, seq string) string {
	return filepath.Join("/root/extensions/amq-squad/quarantine", "commits."+seat+".seq"+seq)
}

func tamperErr(seat, seq string) *LedgerTamperedError {
	path := slotOf(seat, seq)
	return &LedgerTamperedError{Handle: seat, Seq: seq, Quarantine: path,
		Err: errors.New("the commit ledger of seat " + seat + ": commit ledger " + seat + ": ledger_tampered: the tag of commits." + seat + " link " + seq + " fails; the refused document is quarantined at " + path)}
}

func countOf(ds []Decision, kind, seat string) int {
	n := 0
	for _, d := range ds {
		if d.Kind == kind && (seat == "" || d.Seat == seat) {
			n++
		}
	}
	return n
}

// B1: a tampered ledger at prepare produces exactly one seat_prepare_failed
// line and one typed block over N ticks (N large); the block names the
// seat, the seq and the quarantine path; the seat is prepared once and
// never again, and a second task of the pool is never dispatched to it.
// Reverted (the prepare error logged and the task left pending, the 3.1.2
// path), the line repeats every tick and no block is raised.
func TestTamperAlarmBlocksOnceAndHoldsTheSeat(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour})
	p := &tamperPreparer{seat: "seat-a", err: tamperErr("seat-a", "150")}
	h.s.Preparer = p
	tk := h.add("a", &sonnet)
	h.up("seat-a", sonnet)
	var all []Decision
	for i := 0; i < 300; i++ {
		all = append(all, h.tick()...)
		h.clk.Add(2 * time.Second)
	}
	if n := countOf(all, "seat_prepare_failed", "seat-a"); n != 1 {
		t.Fatalf("seat_prepare_failed logged %d times over 300 ticks, want 1", n)
	}
	if n := countOf(all, "blocked", "seat-a"); n != 1 || p.calls != 1 {
		t.Fatalf("blocked %d times, the seat prepared %d times; want 1 and 1", n, p.calls)
	}
	var line Decision
	for _, d := range all {
		if d.Kind == "seat_prepare_failed" {
			line = d
		}
	}
	if line.ReasonCode != ReasonLedgerTampered || line.TaskID != tk.ID {
		t.Fatalf("the line carries no reason code: %+v", line)
	}
	got := h.get(tk.ID)
	if got.State != task.StateBlocked || got.BlockReason != ReasonLedgerTampered || got.BlockRoute != task.RouteLead {
		t.Fatalf("the task is not blocked ledger_tampered for the lead: %+v", got)
	}
	alarm := h.s.tampered["seat-a"].Alarm
	for _, want := range []string{"seat seat-a", "quarantined at /root/extensions/amq-squad/quarantine/commits.seat-a.seq150", "(seq 150; alarm " + alarm + ")"} {
		if !strings.Contains(got.BlockDetail, want) || !strings.Contains(line.Reason, want) {
			t.Fatalf("the block detail or the line does not name %q:\n%s\n%s", want, got.BlockDetail, line.Reason)
		}
	}
	// a second task of the pool never reaches the held seat
	b := h.add("b", &sonnet)
	for i := 0; i < 50; i++ {
		all = h.tick()
		h.clk.Add(2 * time.Second)
		if has(all, "dispatched", b.ID, "seat-a") || has(all, "seat_prepare_failed", b.ID, "") {
			t.Fatalf("the held seat was tried for %s: %+v", b.ID, all)
		}
	}
	if p.calls != 1 {
		t.Fatalf("the held seat was prepared %d times", p.calls)
	}
	if got := h.get(b.ID); got.State != task.StatePending {
		t.Fatalf("task b is %s, want pending (waiting for another seat)", got.State)
	}
}

// B1, the restart: the hold is written to the state file and a new
// incarnation restores it (RestoreLedgerTampers), so the alarm is not
// raised again after a restart. Reverted (the state not restored), the new
// incarnation prepares the seat and logs the line again.
func TestTamperHoldSurvivesARestart(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour, StatePath: filepath.Join(t.TempDir(), "scheduler.json")})
	p := &tamperPreparer{seat: "seat-a", err: tamperErr("seat-a", "3")}
	h.s.Preparer = p
	h.add("a", &sonnet)
	h.up("seat-a", sonnet)
	h.tick()
	st, err := ReadState(h.s.Config.StatePath)
	if err != nil || len(st.LedgerTampered) != 1 || st.LedgerTampered[0].Handle != "seat-a" || st.LedgerTampered[0].Seq != "3" || len(st.LedgerTampered[0].Tasks) != 1 {
		t.Fatalf("the state file does not hold the alarm: %v %+v", err, st.LedgerTampered)
	}
	// a new incarnation on the same store and state
	next := &Scheduler{Store: h.store, Now: h.clk.Now, Launcher: h.l, Doorbell: h.bell, Config: h.s.Config, Incarnation: "u", Preparer: p}
	if n := next.RestoreLedgerTampers(st); n != 1 {
		t.Fatalf("restored %d alarms, want 1", n)
	}
	b := h.add("b", &sonnet)
	next.Post(Event{Kind: SeatUp, At: h.clk.Now(), Handle: "seat-a", Pool: sonnet, RequestID: "test-seat-a"})
	next.requests = append(next.requests, request{req: SeatRequest{ID: "test-seat-a", Pool: sonnet}, at: h.clk.Now()})
	for i := 0; i < 20; i++ {
		ds, err := next.Tick()
		if err != nil {
			t.Fatal(err)
		}
		if has(ds, "seat_prepare_failed", "", "") || has(ds, "dispatched", b.ID, "seat-a") {
			t.Fatalf("the restarted scheduler tried the held seat: %+v", ds)
		}
	}
	if p.calls != 1 {
		t.Fatalf("the seat was prepared %d times across the restart, want 1", p.calls)
	}
}

// B2, the scheduler half: the lead's recovery in the seat's sealed ledger
// (the preparer's RecoveryReader; the apply step writes the chain, the
// tick reads it) releases the seat at the next tick: the task blocked
// ledger_tampered returns to pending (task_reset), the hold is released
// (ledger_hold_released), the seat is prepared again and, the ledger now
// passing, the task is dispatched to it. A recovery record that covers
// neither the hold's slot nor its seq (an older recovery) releases
// nothing. Reverted (the ledger not read), the seat stays held and the
// task blocked.
func TestRecoveryReleasesTheSeatAndReturnsTheTask(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour})
	p := &tamperPreparer{seat: "seat-a", err: tamperErr("seat-a", "150")}
	h.s.Preparer = p
	tk := h.add("a", &sonnet)
	h.up("seat-a", sonnet)
	h.tick()
	if got := h.get(tk.ID); got.State != task.StateBlocked {
		t.Fatalf("not blocked: %+v", got)
	}
	// a clear is not the recovery: ticks alone, and the lead's own reset of
	// the task, leave the seat held
	if _, err := h.store.Reset(tk.ID, nil, h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		ds := h.tick()
		h.clk.Add(2 * time.Second)
		if has(ds, "dispatched", tk.ID, "seat-a") || has(ds, "seat_prepare_failed", "", "") {
			t.Fatalf("the held seat was tried without a recovery: %+v", ds)
		}
	}
	if got := h.get(tk.ID); got.State != task.StatePending {
		t.Fatalf("the reset task is %s", got.State)
	}
	// block it again through the alarm's record (the recovery returns the
	// tasks it holds), then recover
	h.s.tampered["seat-a"].Tasks = []string{tk.ID}
	if _, err := h.store.Block(tk.ID, "", ReasonLedgerTampered, h.s.tampered["seat-a"].Detail, task.RouteLead, h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	// an older recovery (seq 3, another slot) does not cover a seq-150 hold
	p.recover("seat-a", 3, slotOf("seat-a", "3"))
	p.err = tamperErr("seat-a", "150")
	if ds := h.tick(); has(ds, "ledger_hold_released", "", "seat-a") || has(ds, "task_reset", tk.ID, "") {
		t.Fatalf("an older recovery released the hold: %+v", ds)
	}
	p.recover("seat-a", 150, slotOf("seat-a", "150"))
	ds := h.tick()
	if !has(ds, "ledger_hold_released", "", "seat-a") || !has(ds, "task_reset", tk.ID, "seat-a") || !has(ds, "dispatched", tk.ID, "seat-a") {
		t.Fatalf("the recovery did not return the task and dispatch it: %+v", ds)
	}
	if p.calls != 2 {
		t.Fatalf("the seat was prepared %d times, want 2 (once before the alarm, once after the recovery)", p.calls)
	}
	if got := h.get(tk.ID); got.State != task.StateInProgress || got.Lease == nil || got.Lease.Holder != "seat-a" {
		t.Fatalf("not dispatched to the recovered seat: %+v", got)
	}
}

// B4: a non-tamper prepare failure over N ticks logs one seat_prepare_failed
// line and one seat_prepare_suppressed count line; the task stays pending
// and the seat is tried every tick; a change of reason, of task (its
// record's last change, or another task) or a prepare that passes resets
// the count, and the next failure is logged again. Reverted (every failure
// logged), N lines.
func TestPrepareFailureIsLoggedOnceThenCounted(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour})
	p := &tamperPreparer{seat: "seat-a", err: errors.New("the commit ledger of seat seat-a: dirty")}
	h.s.Preparer = p
	tk := h.add("a", &sonnet)
	h.up("seat-a", sonnet)
	var all []Decision
	for i := 0; i < 200; i++ {
		all = append(all, h.tick()...)
		h.clk.Add(2 * time.Second)
	}
	if f, s := countOf(all, "seat_prepare_failed", "seat-a"), countOf(all, "seat_prepare_suppressed", "seat-a"); f != 1 || s != 1 {
		t.Fatalf("%d failed lines and %d suppressed lines over 200 ticks, want 1 and 1", f, s)
	}
	if p.calls != 200 {
		t.Fatalf("the seat was tried %d times, want every tick", p.calls)
	}
	if got := h.get(tk.ID); got.State != task.StatePending {
		t.Fatalf("the task is %s, want pending", got.State)
	}
	// the reason changes: logged again
	p.err = errors.New("the commit ledger of seat seat-a: worse")
	ds := h.tick()
	if !has(ds, "seat_prepare_failed", tk.ID, "seat-a") {
		t.Fatalf("a new reason was not logged: %+v", ds)
	}
	if ds = h.tick(); !has(ds, "seat_prepare_suppressed", tk.ID, "seat-a") {
		t.Fatalf("the repeat of the new reason was not counted: %+v", ds)
	}
	// the task changes (its record's last change): logged again
	h.clk.Add(time.Second)
	if _, err := h.store.Set(tk.ID, intPtr(2), nil, h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if ds = h.tick(); !has(ds, "seat_prepare_failed", tk.ID, "seat-a") {
		t.Fatalf("the changed task was not logged: %+v", ds)
	}
	// the prepare passes once (the task dispatched, then released by the
	// lead), then the same seat fails for the same task again: logged again
	p.err = nil
	if ds = h.tick(); !has(ds, "dispatched", tk.ID, "seat-a") {
		t.Fatalf("the passing prepare did not dispatch: %+v", ds)
	}
	if _, err := h.store.Release(tk.ID, "seat-a", "back to the pool", h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	p.err = errors.New("the commit ledger of seat seat-a: worse")
	if ds = h.tick(); !has(ds, "seat_prepare_failed", tk.ID, "seat-a") {
		t.Fatalf("the failure after a passing prepare was not logged: %+v", ds)
	}
	if ds = h.tick(); !has(ds, "seat_prepare_suppressed", tk.ID, "seat-a") {
		t.Fatalf("its repeat was not counted: %+v", ds)
	}
}

// Codex's P2 on #904: two pending tasks on one failing seat alternate at
// the dispatch; each is logged once and counted once (four lines over 200
// ticks), never two lines a tick. Reverted (the key per seat only), 400.
func TestTwoTasksOnOneFailingSeatAreEachLoggedOnce(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour})
	p := &tamperPreparer{seat: "seat-a", err: errors.New("the commit ledger of seat seat-a: dirty")}
	h.s.Preparer = p
	a := h.add("a", &sonnet)
	b := h.add("b", &sonnet)
	h.up("seat-a", sonnet)
	var all []Decision
	for i := 0; i < 200; i++ {
		all = append(all, h.tick()...)
		h.clk.Add(2 * time.Second)
	}
	for _, id := range []string{a.ID, b.ID} {
		f, c := 0, 0
		for _, d := range all {
			if d.TaskID == id && d.Kind == "seat_prepare_failed" {
				f++
			}
			if d.TaskID == id && d.Kind == "seat_prepare_suppressed" {
				c++
			}
		}
		if f != 1 || c != 1 {
			t.Fatalf("task %s: %d failed lines and %d count lines over 200 ticks, want 1 and 1", id, f, c)
		}
	}
}

// Codex's P2 on #904, the interrupted completion: the chain is written and
// the process ends before the state write and before anything else (a
// crash, a lost decision line); the next incarnation restores the hold
// from the state file and its first tick releases it from the ledger's
// recovery record: the task returns to pending and is dispatched; the
// state file no longer holds the seat. Reverted (no reconciliation), the
// hold can never be released.
func TestRestartedHoldIsReleasedFromTheLedgersRecovery(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour, StatePath: filepath.Join(t.TempDir(), "scheduler.json")})
	p := &tamperPreparer{seat: "seat-a", err: tamperErr("seat-a", "150")}
	h.s.Preparer = p
	tk := h.add("a", &sonnet)
	h.up("seat-a", sonnet)
	h.tick()
	st, err := ReadState(h.s.Config.StatePath)
	if err != nil || len(st.LedgerTampered) != 1 {
		t.Fatalf("the hold is not in the state file: %v %+v", err, st)
	}
	// the chain is written (the ledger records the recovery); the process
	// ends here
	p.recover("seat-a", 150, slotOf("seat-a", "150"))
	next := &Scheduler{Store: h.store, Now: h.clk.Now, Launcher: h.l, Doorbell: h.bell, Config: h.s.Config, Incarnation: "u", Preparer: p}
	next.RestoreLedgerTampers(st)
	next.requests = append(next.requests, request{req: SeatRequest{ID: "test-seat-a", Pool: sonnet}, at: h.clk.Now()})
	next.Post(Event{Kind: SeatUp, At: h.clk.Now(), Handle: "seat-a", Pool: sonnet, RequestID: "test-seat-a"})
	ds, err := next.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if !has(ds, "ledger_hold_released", "", "seat-a") || !has(ds, "task_reset", tk.ID, "seat-a") || !has(ds, "dispatched", tk.ID, "seat-a") {
		t.Fatalf("the restarted scheduler did not release the hold from the ledger: %+v", ds)
	}
	if st, err = ReadState(h.s.Config.StatePath); err != nil || len(st.LedgerTampered) != 0 {
		t.Fatalf("the state file still holds the seat: %v %+v", err, st.LedgerTampered)
	}
}

// Codex's P2 on #904: two seats held, one recovered; the other stays held
// in the state file and across a restart, never prepared, while the
// recovered one is granted. Reverted (every hold released on any
// recovery), the unrecovered seat is prepared.
func TestTwoHeldSeatsOneRecovered(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour, StatePath: filepath.Join(t.TempDir(), "scheduler.json")})
	p := &twoSeatPreparer{errs: map[string]error{"seat-a": tamperErr("seat-a", "5"), "seat-b": tamperErr("seat-b", "9")}, calls: map[string]int{}}
	h.s.Preparer = p
	a := h.add("a", &sonnet)
	b := h.add("b", &sonnet)
	h.up("seat-a", sonnet)
	h.up("seat-b", sonnet)
	for i := 0; i < 3; i++ {
		h.tick()
	}
	if h.get(a.ID).State != task.StateBlocked || h.get(b.ID).State != task.StateBlocked || p.calls["seat-a"] != 1 || p.calls["seat-b"] != 1 {
		t.Fatalf("both seats were not held once each: %+v %s %s", p.calls, h.get(a.ID).State, h.get(b.ID).State)
	}
	p.ledgers = map[string]recoveryRecord{"seat-a": {seq: 5, slot: slotOf("seat-a", "5")}}
	delete(p.errs, "seat-a")
	ds := h.tick()
	if !has(ds, "ledger_hold_released", "", "seat-a") || has(ds, "ledger_hold_released", "", "seat-b") || !has(ds, "dispatched", "", "seat-a") {
		t.Fatalf("seat-a released alone: %+v", ds)
	}
	st, err := ReadState(h.s.Config.StatePath)
	if err != nil || len(st.LedgerTampered) != 1 || st.LedgerTampered[0].Handle != "seat-b" {
		t.Fatalf("the state file after one recovery: %v %+v", err, st.LedgerTampered)
	}
	next := &Scheduler{Store: h.store, Now: h.clk.Now, Launcher: h.l, Doorbell: h.bell, Config: h.s.Config, Incarnation: "u", Preparer: p}
	next.RestoreLedgerTampers(st)
	for _, seat := range []string{"seat-a", "seat-b"} {
		next.requests = append(next.requests, request{req: SeatRequest{ID: "test-" + seat, Pool: sonnet}, at: h.clk.Now()})
		next.Post(Event{Kind: SeatUp, At: h.clk.Now(), Handle: seat, Pool: sonnet, RequestID: "test-" + seat})
	}
	c := h.add("c", &sonnet)
	for i := 0; i < 10; i++ {
		if ds, err := next.Tick(); err != nil {
			t.Fatal(err)
		} else if has(ds, "dispatched", "", "seat-b") || has(ds, "seat_prepare_failed", "", "seat-b") {
			t.Fatalf("the held seat-b was tried after the restart: %+v", ds)
		}
	}
	if p.calls["seat-b"] != 1 {
		t.Fatalf("seat-b was prepared %d times across the restart, want 1", p.calls["seat-b"])
	}
	if got := h.get(c.ID); got.State != task.StatePending && got.Lease != nil && got.Lease.Holder == "seat-b" {
		t.Fatalf("task c went to the held seat: %+v", got)
	}
}

// twoSeatPreparer fails the seats in errs and reads recoveries from ledgers.
type twoSeatPreparer struct {
	errs    map[string]error
	calls   map[string]int
	ledgers map[string]recoveryRecord
}

func (p *twoSeatPreparer) PrepareSeat(h string) error {
	p.calls[h]++
	return p.errs[h]
}

func (p *twoSeatPreparer) LedgerRecovery(h string) (RecoveryMark, bool) {
	r, ok := p.ledgers[h]
	return RecoveryMark{Seq: r.seq, Slot: r.slot, Alarm: r.alarm}, ok
}

func intPtr(n int) *int { return &n }

// The plan reviewer on c9826c31: the input where covers decides. A seat
// whose ledger verifies and carries an older recovery record (seq 3, its
// slot) while the hold is from a later alarm (seq 150): the record does
// not cover the hold, the seat stays held over many ticks and the task
// blocked; the record of the hold's own slot releases it. Reverted (covers
// always true), the older record releases the hold.
func TestAnOlderRecoveryRecordDoesNotReleaseAHold(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour})
	p := &tamperPreparer{seat: "seat-a", err: tamperErr("seat-a", "150")}
	h.s.Preparer = p
	tk := h.add("a", &sonnet)
	h.up("seat-a", sonnet)
	h.tick()
	p.ledgers = map[string]recoveryRecord{"seat-a": {seq: 3, slot: slotOf("seat-a", "3")}}
	for i := 0; i < 20; i++ {
		ds := h.tick()
		h.clk.Add(2 * time.Second)
		if has(ds, "ledger_hold_released", "", "seat-a") || has(ds, "task_reset", tk.ID, "") || has(ds, "dispatched", tk.ID, "seat-a") {
			t.Fatalf("an older record released the hold: %+v", ds)
		}
	}
	if got := h.get(tk.ID); got.State != task.StateBlocked || p.calls != 1 {
		t.Fatalf("the hold did not stand: %s, prepared %d times", got.State, p.calls)
	}
	p.recover("seat-a", 150, slotOf("seat-a", "150"))
	if ds := h.tick(); !has(ds, "ledger_hold_released", "", "seat-a") || !has(ds, "dispatched", tk.ID, "seat-a") {
		t.Fatalf("the hold's own record did not release it: %+v", ds)
	}
}

// Codex on c9826c31, with the plan reviewer's addition: a task-store read
// error other than not-found keeps the hold (deleting it would leave the
// task blocked with nothing to reconcile), is logged once per task, seat
// and reason and then counted (one ledger_release_failed and one
// ledger_release_suppressed over many ticks of a persistent error), and
// the release completes when the error clears. A task the store no longer
// has is skipped and the hold released. Reverted (any error skips the
// task), the first erroring tick deletes the hold with the task blocked.
func TestAStoreReadErrorKeepsTheHoldAndLogsOnce(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour})
	p := &tamperPreparer{seat: "seat-a", err: tamperErr("seat-a", "150")}
	h.s.Preparer = p
	tk := h.add("a", &sonnet)
	h.up("seat-a", sonnet)
	h.tick()
	p.recover("seat-a", 150, slotOf("seat-a", "150"))
	prev := storeGet
	t.Cleanup(func() { storeGet = prev })
	storeGet = func(task.Store, string) (task.Task, error) {
		return task.Task{}, errors.New("the store's disk is away")
	}
	var all []Decision
	for i := 0; i < 30; i++ {
		all = append(all, h.tick()...)
		h.clk.Add(2 * time.Second)
	}
	if countOf(all, "ledger_release_failed", "seat-a") != 1 || countOf(all, "ledger_release_suppressed", "seat-a") != 1 || countOf(all, "ledger_hold_released", "seat-a") != 0 {
		t.Fatalf("over 30 ticks of a read error: %+v", all)
	}
	if !h.s.tamperedSeat("seat-a") || h.get(tk.ID).State != task.StateBlocked {
		t.Fatal("the read error dropped the hold or returned the task")
	}
	storeGet = prev
	if ds := h.tick(); !has(ds, "ledger_hold_released", "", "seat-a") || !has(ds, "task_reset", tk.ID, "seat-a") || !has(ds, "dispatched", tk.ID, "seat-a") {
		t.Fatalf("the release after the error cleared: %+v", ds)
	}
	// a task the store no longer has: skipped, the hold released
	h.s.tampered = map[string]*LedgerTamper{"seat-a": {Handle: "seat-a", Seq: "150", Quarantine: slotOf("seat-a", "150"), Tasks: []string{"t-gone"}}}
	if ds := h.tick(); !has(ds, "ledger_hold_released", "", "seat-a") || has(ds, "ledger_release_failed", "", "") {
		t.Fatalf("a gone task: %+v", ds)
	}
}

// Codex on c9826c31: the reset fails once; the hold stays and the task
// stays blocked; the retry on the next tick completes the release.
// Reverted (the hold deleted before the resets), the retry finds no hold
// and the task stays blocked for good.
func TestAFailedResetKeepsTheHoldForTheRetry(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour})
	p := &tamperPreparer{seat: "seat-a", err: tamperErr("seat-a", "150")}
	h.s.Preparer = p
	tk := h.add("a", &sonnet)
	h.up("seat-a", sonnet)
	h.tick()
	p.recover("seat-a", 150, slotOf("seat-a", "150"))
	prev := storeReset
	t.Cleanup(func() { storeReset = prev })
	fails := 1
	storeReset = func(st task.Store, id string, match func(task.Task) bool, now time.Time) (task.Task, error) {
		if fails > 0 {
			fails--
			return task.Task{}, errors.New("the store's journal is full")
		}
		return prev(st, id, match, now)
	}
	ds := h.tick()
	if !has(ds, "ledger_release_failed", tk.ID, "seat-a") || has(ds, "ledger_hold_released", "", "seat-a") || has(ds, "task_reset", tk.ID, "") {
		t.Fatalf("the failed reset: %+v", ds)
	}
	if !h.s.tamperedSeat("seat-a") || h.get(tk.ID).State != task.StateBlocked {
		t.Fatal("the failed reset dropped the hold or returned the task")
	}
	if ds = h.tick(); !has(ds, "task_reset", tk.ID, "seat-a") || !has(ds, "ledger_hold_released", "", "seat-a") || !has(ds, "dispatched", tk.ID, "seat-a") {
		t.Fatalf("the retry: %+v", ds)
	}
}

// The plan reviewer on af1a6124, the lowered-seq replay: the ledger keeps
// a recovery record (seq 3, its slot); the head's seq is lowered to 1 and
// the head put back, the ledger verifies with the seq-3 record, which is
// past the hold's seq but names another slot: no release over many ticks,
// nor after a restart; the record of the hold's own slot releases it.
// Reverted (a seq at or past the hold's covers it again), the old record
// releases the hold on the first tick.
func TestALoweredSeqReplayDoesNotReleaseTheHold(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour, StatePath: filepath.Join(t.TempDir(), "scheduler.json")})
	p := &tamperPreparer{seat: "seat-a", err: tamperErr("seat-a", "1")}
	h.s.Preparer = p
	tk := h.add("a", &sonnet)
	h.up("seat-a", sonnet)
	h.tick()
	p.ledgers = map[string]recoveryRecord{"seat-a": {seq: 3, slot: slotOf("seat-a", "3")}}
	for i := 0; i < 20; i++ {
		ds := h.tick()
		h.clk.Add(2 * time.Second)
		if has(ds, "ledger_hold_released", "", "seat-a") || has(ds, "task_reset", tk.ID, "") {
			t.Fatalf("the replayed seq-3 record released the seq-1 hold: %+v", ds)
		}
	}
	st, err := ReadState(h.s.Config.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	next := &Scheduler{Store: h.store, Now: h.clk.Now, Launcher: h.l, Doorbell: h.bell, Config: h.s.Config, Incarnation: "u", Preparer: p}
	next.RestoreLedgerTampers(st)
	for i := 0; i < 5; i++ {
		if ds, err := next.Tick(); err != nil || has(ds, "ledger_hold_released", "", "seat-a") {
			t.Fatalf("the restarted scheduler released the hold on the replay: %v %+v", err, ds)
		}
	}
	p.ledgers["seat-a"] = recoveryRecord{seq: 1, slot: slotOf("seat-a", "1")}
	if ds, err := next.Tick(); err != nil || !has(ds, "ledger_hold_released", "", "seat-a") || !has(ds, "task_reset", tk.ID, "seat-a") {
		t.Fatalf("the hold's own slot did not release it: %v %+v", err, ds)
	}
}

// The plan reviewer on af1a6124: a recovery for a different slot of the
// same seq (commits.<h>.seq150.2, a later quarantine of another document)
// does not release a hold on commits.<h>.seq150, though its seq equals the
// hold's. Reverted (a seq at or past the hold's covers it again), it
// releases the hold.
func TestARecoveryForAnotherSlotDoesNotReleaseTheHold(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour})
	p := &tamperPreparer{seat: "seat-a", err: tamperErr("seat-a", "150")}
	h.s.Preparer = p
	tk := h.add("a", &sonnet)
	h.up("seat-a", sonnet)
	h.tick()
	p.ledgers = map[string]recoveryRecord{"seat-a": {seq: 150, slot: slotOf("seat-a", "150") + ".2"}}
	for i := 0; i < 10; i++ {
		ds := h.tick()
		h.clk.Add(2 * time.Second)
		if has(ds, "ledger_hold_released", "", "seat-a") || has(ds, "task_reset", tk.ID, "") {
			t.Fatalf("a recovery for another slot released the hold: %+v", ds)
		}
	}
	if !h.s.tamperedSeat("seat-a") || h.get(tk.ID).State != task.StateBlocked {
		t.Fatal("the hold did not stand")
	}
}

// Codex's delta pass on c267382d (1), the plan reviewer's ruling: a hold
// with no slot (the refusal could not quarantine the document) is never
// released by a record that names a slot or a seq, nor by a decision by
// alarm for another alarm; it keeps its alarm id across a restart (the
// state file); the decision by alarm naming its own alarm releases it.
// Reverted (no release by alarm), the hold strands.
func TestAHoldWithNoSlotIsReleasedOnlyByItsAlarm(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour, StatePath: filepath.Join(t.TempDir(), "scheduler.json")})
	e := tamperErr("seat-a", "150")
	e.Quarantine = ""
	p := &tamperPreparer{seat: "seat-a", err: e}
	h.s.Preparer = p
	tk := h.add("a", &sonnet)
	h.up("seat-a", sonnet)
	h.tick()
	alarm := h.s.tampered["seat-a"].Alarm
	if len(alarm) != 8 || !strings.Contains(h.get(tk.ID).BlockDetail, "; alarm "+alarm+")") {
		t.Fatalf("the hold's alarm %q is not in its block: %q", alarm, h.get(tk.ID).BlockDetail)
	}
	for _, rec := range []recoveryRecord{
		{seq: 150, slot: slotOf("seat-a", "150") + ".1"}, // a slot and the hold's seq
		{alarm: "0badcafe"}, // another alarm
	} {
		p.ledgers = map[string]recoveryRecord{"seat-a": rec}
		for i := 0; i < 5; i++ {
			ds := h.tick()
			h.clk.Add(2 * time.Second)
			if has(ds, "ledger_hold_released", "", "seat-a") || has(ds, "task_reset", tk.ID, "") {
				t.Fatalf("%+v released a hold with no slot: %+v", rec, ds)
			}
		}
	}
	st, err := ReadState(h.s.Config.StatePath)
	if err != nil || len(st.LedgerTampered) != 1 || st.LedgerTampered[0].Alarm != alarm {
		t.Fatalf("the state file does not keep the alarm: %v %+v", err, st.LedgerTampered)
	}
	next := &Scheduler{Store: h.store, Now: h.clk.Now, Launcher: h.l, Doorbell: h.bell, Config: h.s.Config, Incarnation: "u", Preparer: p}
	next.RestoreLedgerTampers(st)
	if got, ok := next.HeldAlarm("seat-a"); !ok || got != alarm {
		t.Fatalf("the restarted hold's alarm: %q %v, want %q", got, ok, alarm)
	}
	p.ledgers["seat-a"] = recoveryRecord{alarm: alarm}
	p.err = nil // the ledger verifies again
	next.requests = append(next.requests, request{req: SeatRequest{ID: "test-seat-a", Pool: sonnet}, at: h.clk.Now()})
	next.Post(Event{Kind: SeatUp, At: h.clk.Now(), Handle: "seat-a", Pool: sonnet, RequestID: "test-seat-a"})
	ds, err := next.Tick()
	if err != nil || !has(ds, "ledger_hold_released", "", "seat-a") || !has(ds, "task_reset", tk.ID, "seat-a") || !has(ds, "dispatched", tk.ID, "seat-a") {
		t.Fatalf("the decision by its alarm did not release the hold: %v %+v", err, ds)
	}
	if _, ok := next.HeldAlarm("seat-a"); ok {
		t.Fatal("the released seat is still published as held")
	}
}

// Codex's delta pass on c267382d (2): a hold on slot X (seq 150); the head
// lowered to seq 1 gives a later slot Y at a lower seq; the recovery from
// Y (its record names Y) does not release X's hold, which no verified
// recovery of X can answer either (the chain is behind X's seq); X's hold
// is released only by the decision naming its alarm. Reverted (no release
// by alarm), the hold strands.
func TestALowerSeqSlotLeavesTheHoldToItsAlarm(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour})
	p := &tamperPreparer{seat: "seat-a", err: tamperErr("seat-a", "150")}
	h.s.Preparer = p
	tk := h.add("a", &sonnet)
	h.up("seat-a", sonnet)
	h.tick()
	alarm := h.s.tampered["seat-a"].Alarm
	p.ledgers = map[string]recoveryRecord{"seat-a": {seq: 1, slot: slotOf("seat-a", "1")}}
	for i := 0; i < 10; i++ {
		ds := h.tick()
		h.clk.Add(2 * time.Second)
		if has(ds, "ledger_hold_released", "", "seat-a") {
			t.Fatalf("the recovery of the lower-seq slot released X's hold: %+v", ds)
		}
	}
	p.recover("seat-a", 0, "")
	p.ledgers["seat-a"] = recoveryRecord{alarm: alarm}
	ds := h.tick()
	if !has(ds, "ledger_hold_released", "", "seat-a") || !has(ds, "task_reset", tk.ID, "seat-a") || !has(ds, "dispatched", tk.ID, "seat-a") {
		t.Fatalf("X's alarm did not release its hold: %+v", ds)
	}
	for _, d := range ds {
		if d.Kind == "ledger_hold_released" && !strings.Contains(d.Reason, "alarm "+alarm+" is released") {
			t.Fatalf("the release does not name the alarm: %q", d.Reason)
		}
	}
}

// Codex's delta pass on c267382d (3): a decision by alarm for an earlier
// hold that lands late (the ledger records alarm A) never releases a newer
// hold on the same seat (alarm B), over many ticks; B's own decision does.
// Reverted (any decision by alarm releases), the late decision releases B.
func TestALateDecisionForAnEarlierAlarmDoesNotReleaseANewerHold(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour})
	p := &tamperPreparer{seat: "seat-a", err: tamperErr("seat-a", "150")}
	h.s.Preparer = p
	tk := h.add("a", &sonnet)
	h.up("seat-a", sonnet)
	h.tick()
	b := h.s.tampered["seat-a"].Alarm
	a := "a0000001"
	if a == b {
		a = "a0000002"
	}
	p.ledgers = map[string]recoveryRecord{"seat-a": {alarm: a}}
	for i := 0; i < 20; i++ {
		ds := h.tick()
		h.clk.Add(2 * time.Second)
		if has(ds, "ledger_hold_released", "", "seat-a") || has(ds, "task_reset", tk.ID, "") {
			t.Fatalf("an earlier alarm's decision released the newer hold: %+v", ds)
		}
	}
	p.recover("seat-a", 0, "")
	p.ledgers["seat-a"] = recoveryRecord{alarm: b}
	if ds := h.tick(); !has(ds, "ledger_hold_released", "", "seat-a") {
		t.Fatalf("the newer hold's own decision did not release it: %+v", ds)
	}
}

// Codex on b250b951, temp names defeat the suppression: a persistent
// failure whose text names a fresh temp directory each tick (a disk full
// under CleanSeat's shadow) is one failure: one seat_prepare_failed and one
// seat_prepare_suppressed over 30 ticks, the logged line keeping the full
// text; a failure that says something else is logged again. Reverted (the
// raw text compared), every tick logs a new first failure.
func TestAFailureNamingAFreshTempFileIsLoggedOnce(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour})
	p := &tamperPreparer{seat: "seat-a"}
	h.s.Preparer = p
	h.add("a", &sonnet)
	h.up("seat-a", sonnet)
	var all []Decision
	for i := 0; i < 30; i++ {
		p.err = fmt.Errorf("the commit ledger of seat seat-a: write /var/folders/zz/T/amq-squad-shadow-%d/index.lock: no space left on device (%d bytes)", 1000+i*7919, 4096+i)
		all = append(all, h.tick()...)
		h.clk.Add(2 * time.Second)
	}
	if countOf(all, "seat_prepare_failed", "seat-a") != 1 || countOf(all, "seat_prepare_suppressed", "seat-a") != 1 {
		t.Fatalf("over 30 ticks of one failure with a fresh temp name: %+v", all)
	}
	for _, d := range all {
		if d.Kind == "seat_prepare_failed" && !strings.Contains(d.Reason, "amq-squad-shadow-1000/index.lock: no space left on device") {
			t.Fatalf("the logged line lost the full text: %q", d.Reason)
		}
	}
	p.err = errors.New("the commit ledger of seat seat-a: write /var/folders/zz/T/amq-squad-shadow-5/index.lock: permission denied")
	if ds := h.tick(); !has(ds, "seat_prepare_failed", "", "seat-a") {
		t.Fatalf("a changed reason was not logged: %+v", ds)
	}
}

// Codex on b250b951, the recovery clears an unrelated block: between the
// release's read of the task (blocked ledger_tampered) and its reset,
// another decision resets it and blocks it again for another reason; the
// reset is one conditional store mutation (ResetIfBlocked), so the new
// block stays, no task_reset is logged and the hold is released (the
// tamper block is gone). Reverted (an unconditional Reset), the new block
// is cleared and the task dispatched.
func TestARecoveryNeverClearsABlockRaisedAfterItsRead(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour})
	p := &tamperPreparer{seat: "seat-a", err: tamperErr("seat-a", "150")}
	h.s.Preparer = p
	tk := h.add("a", &sonnet)
	h.up("seat-a", sonnet)
	h.tick()
	p.recover("seat-a", 150, slotOf("seat-a", "150"))
	prev := storeGet
	t.Cleanup(func() { storeGet = prev })
	storeGet = func(st task.Store, id string) (task.Task, error) {
		got, err := prev(st, id)
		// another decision between the read and the reset
		if _, rerr := st.Reset(id, nil, h.clk.Now()); rerr != nil {
			t.Fatal(rerr)
		}
		if _, berr := st.Block(id, "lead", "lead_hold", "held by the lead for another reason", task.RouteLead, h.clk.Now()); berr != nil {
			t.Fatal(berr)
		}
		return got, err
	}
	ds := h.tick()
	storeGet = prev
	if has(ds, "task_reset", tk.ID, "") || has(ds, "dispatched", tk.ID, "") || has(ds, "ledger_release_failed", "", "") {
		t.Fatalf("the release touched the new block: %+v", ds)
	}
	if got := h.get(tk.ID); got.State != task.StateBlocked || got.BlockReason != "lead_hold" {
		t.Fatalf("the new block was cleared: %s %s", got.State, got.BlockReason)
	}
	if !has(ds, "ledger_hold_released", "", "seat-a") || h.s.tamperedSeat("seat-a") {
		t.Fatalf("the hold was not released: %+v", ds)
	}
}

// The plan reviewer on af1a6124: the conditional reset matches the hold's
// own block. Between the release's read and its reset, the task is reset
// and blocked ledger_tampered again for another slot (another alarm's
// detail): that block stays, no task_reset, no dispatch, and the hold
// whose slot the ledger records is released. Reverted (the reset matches
// the reason only), the other slot's block is cleared.
func TestARecoveryKeepsATamperBlockOfAnotherSlot(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour})
	p := &tamperPreparer{seat: "seat-a", err: tamperErr("seat-a", "150")}
	h.s.Preparer = p
	tk := h.add("a", &sonnet)
	h.up("seat-a", sonnet)
	h.tick()
	p.recover("seat-a", 150, slotOf("seat-a", "150"))
	again := tamperDetail("seat-a", tamperErr("seat-a", "151"), "0badcafe")
	prev := storeGet
	t.Cleanup(func() { storeGet = prev })
	storeGet = func(st task.Store, id string) (task.Task, error) {
		got, err := prev(st, id)
		if _, rerr := st.Reset(id, nil, h.clk.Now()); rerr != nil {
			t.Fatal(rerr)
		}
		if _, berr := st.Block(id, "", ReasonLedgerTampered, again, task.RouteLead, h.clk.Now()); berr != nil {
			t.Fatal(berr)
		}
		return got, err
	}
	ds := h.tick()
	storeGet = prev
	if has(ds, "task_reset", tk.ID, "") || has(ds, "dispatched", tk.ID, "") || has(ds, "ledger_release_failed", "", "") {
		t.Fatalf("the release touched the other slot's block: %+v", ds)
	}
	if got := h.get(tk.ID); got.State != task.StateBlocked || got.BlockReason != ReasonLedgerTampered || got.BlockDetail != again {
		t.Fatalf("the other slot's block was cleared: %s %s %q", got.State, got.BlockReason, got.BlockDetail)
	}
	if !has(ds, "ledger_hold_released", "", "seat-a") {
		t.Fatalf("the hold was not released: %+v", ds)
	}
}

// The plan reviewer on the masked key: two failures whose texts differ
// only in a temp file's name are one failure (one seat_prepare_failed),
// and a failure that differs in words is another (a second
// seat_prepare_failed): over three ticks, two lines. Reverted (the raw
// text as the key), the temp name alone logs a second line, three in all.
func TestReasonsDifferingOnlyInATempNameLogOnce(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour})
	p := &tamperPreparer{seat: "seat-a"}
	h.s.Preparer = p
	h.add("a", &sonnet)
	h.up("seat-a", sonnet)
	var all []Decision
	for _, reason := range []string{
		"the commit ledger of seat seat-a: write /tmp/amq-squad-shadow-4127/index: no space left on device",
		"the commit ledger of seat seat-a: write /tmp/amq-squad-shadow-90355/index: no space left on device",
		"the commit ledger of seat seat-a: write /tmp/amq-squad-shadow-90355/index: permission denied",
	} {
		p.err = errors.New(reason)
		all = append(all, h.tick()...)
		h.clk.Add(2 * time.Second)
	}
	if n := countOf(all, "seat_prepare_failed", "seat-a"); n != 2 {
		t.Fatalf("%d seat_prepare_failed lines, want 2 (the temp name alone is one failure, the words another): %+v", n, all)
	}
	if reasonIdentity("write /tmp/amq-squad-shadow-4127/index: no space") != reasonIdentity("write /tmp/amq-squad-shadow-90355/index: no space") ||
		reasonIdentity("write /tmp/x/index: no space") == reasonIdentity("write /tmp/x/index: permission denied") {
		t.Fatal("reasonIdentity does not mask the temp name or merges different words")
	}
}

// The plan reviewer on 5d2a576a, the race the socket check cannot close:
// the decision by alarm for A passes the socket's check (A holds the
// seat), and before its record is sealed A's hold is released by its slot
// and a new alarm B holds the seat (the next prepare refused again). The
// record then sealed names A, so B stays held over many ticks and its task
// stays blocked; B's own decision releases it. Reverted (a decision by
// alarm matched on the seat, not the alarm), A's late record releases B.
func TestADecisionForAReleasedAlarmDoesNotReleaseTheNextHold(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour})
	p := &tamperPreparer{seat: "seat-a", err: tamperErr("seat-a", "150")}
	h.s.Preparer = p
	t1 := h.add("a", &sonnet)
	h.up("seat-a", sonnet)
	h.tick()
	a, ok := h.s.HeldAlarm("seat-a")
	if !ok || a != h.s.tampered["seat-a"].Alarm {
		t.Fatalf("the socket's view of A: %q %v", a, ok)
	}
	// the verb for A is accepted here (A holds the seat); its record is not
	// sealed yet. Meanwhile the lead cancels t1, t2 waits for the seat, the
	// ledger records the recovery of A's slot, and the seat's next prepare
	// is refused again: one tick releases A and raises B.
	if _, err := h.store.Cancel(t1.ID, "lead", "replaced", h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	t2 := h.add("b", &sonnet)
	p.ledgers = map[string]recoveryRecord{"seat-a": {seq: 150, slot: slotOf("seat-a", "150")}}
	p.err = tamperErr("seat-a", "151")
	ds := h.tick()
	b, held := h.s.HeldAlarm("seat-a")
	if !has(ds, "ledger_hold_released", "", "seat-a") || !held || b == a || h.get(t2.ID).State != task.StateBlocked {
		t.Fatalf("A released by its slot and B raised: held %v alarm %q (A %q): %+v", held, b, a, ds)
	}
	// the decision for A is sealed now: the record names A
	p.ledgers["seat-a"] = recoveryRecord{alarm: a}
	for i := 0; i < 20; i++ {
		ds := h.tick()
		h.clk.Add(2 * time.Second)
		if has(ds, "ledger_hold_released", "", "seat-a") || has(ds, "task_reset", t2.ID, "") {
			t.Fatalf("A's late record released B: %+v", ds)
		}
	}
	if got, _ := h.s.HeldAlarm("seat-a"); got != b || h.get(t2.ID).State != task.StateBlocked {
		t.Fatalf("B did not stand: %q, t2 %s", got, h.get(t2.ID).State)
	}
	p.err = nil
	p.ledgers["seat-a"] = recoveryRecord{alarm: b}
	if ds := h.tick(); !has(ds, "ledger_hold_released", "", "seat-a") || !has(ds, "task_reset", t2.ID, "seat-a") {
		t.Fatalf("B's own decision did not release it: %+v", ds)
	}
}
