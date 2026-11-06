package scheduler

import (
	"errors"
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
	seq  int
	slot string
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

func (p *tamperPreparer) LedgerRecovery(h string) (int, string, bool) {
	r, ok := p.ledgers[h]
	return r.seq, r.slot, ok
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
	for _, want := range []string{"seat seat-a", "quarantined at /root/extensions/amq-squad/quarantine/commits.seat-a.seq150", "(seq 150)"} {
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
	if _, err := h.store.Block(tk.ID, "", ReasonLedgerTampered, "seat seat-a: quarantined at /q (seq 150): x", task.RouteLead, h.clk.Now()); err != nil {
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

func (p *twoSeatPreparer) LedgerRecovery(h string) (int, string, bool) {
	r, ok := p.ledgers[h]
	return r.seq, r.slot, ok
}

func intPtr(n int) *int { return &n }
