package scheduler

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Tests of the 2b-2 milestone B fix commit 1 (the first pass on e2a59176).

// exitRecorder is an ExitChecker that quarantines the seats in bad.
type exitRecorder struct {
	calls []string
	bad   map[string]bool
}

func (e *exitRecorder) CheckExit(handle, taskID, why string) ExitResult {
	e.calls = append(e.calls, handle+"/"+taskID+"/"+why)
	if e.bad[handle] {
		return ExitResult{Quarantine: true, ReasonCode: "global_config_changed", Detail: "settings.json changed"}
	}
	return ExitResult{OK: true}
}

func called(calls []string, want string) bool {
	for _, c := range calls {
		if c == want {
			return true
		}
	}
	return false
}

// R3 (scheduler.go:233 and :307): the exit check ran only on a done report,
// so a seat that changed the owner's config and then failed, went away or
// ran past its lease skipped the quarantine. It runs on every terminal path:
// a failed attempt, a gone seat, an expired lease.
func TestExitCheckRunsOnEveryTerminalPath(t *testing.T) {
	h := newHarness(t, Config{LeaseDuration: 10 * time.Minute, IdleTimeout: time.Hour})
	ex := &exitRecorder{bad: map[string]bool{"seat-a": true}}
	h.s.Exits = ex
	h.add("fails", &sonnet)
	h.up("seat-a", sonnet)
	h.tick()
	h.store.Claim("t1", "seat-a", nil, 0, h.clk.Now())
	if _, err := h.store.Fail("t1", "seat-a", "tests red", h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	d := h.tick()
	if !called(ex.calls, "seat-a/t1/failed") || !has(d, "seat_quarantined", "t1", "seat-a") {
		t.Fatalf("failed exit: calls %v, decisions %+v", ex.calls, d)
	}
	if qs, _ := task.Quarantined(h.store.Dir); len(qs) != 1 || qs[0].Handle != "seat-a" {
		t.Fatalf("not quarantined in the store: %+v", qs)
	}
	// A gone seat.
	h.up("seat-b", sonnet)
	h.tick()
	h.s.Post(Event{Kind: SeatGone, At: h.clk.Now(), Handle: "seat-b"})
	h.tick()
	if !called(ex.calls, "seat-b/t1/gone") && !called(ex.calls, "seat-b//gone") {
		t.Fatalf("gone exit not checked: %v", ex.calls)
	}
	// An expired lease (seat-c claims, then runs silently past its lease).
	h.add("expires", &sonnet)
	h.up("seat-c", sonnet)
	h.tick()
	held := ""
	for _, id := range []string{"t1", "t2"} {
		if got := h.get(id); got.Lease != nil && got.Lease.Holder == "seat-c" {
			held = id
		}
	}
	if held == "" {
		t.Fatal("seat-c got no lease")
	}
	h.store.Claim(held, "seat-c", nil, 0, h.clk.Now())
	h.clk.Add(11 * time.Minute)
	h.tick() // the expired claimed seat closes (confirmed by the fake launcher)
	if called(ex.calls, "seat-c/"+held+"/"+task.OutcomeExpired) {
		t.Fatalf("the exit check ran before the close was confirmed: %v", ex.calls)
	}
	h.tick()
	if !called(ex.calls, "seat-c/"+held+"/"+task.OutcomeExpired) {
		t.Fatalf("expired exit not checked: %v", ex.calls)
	}
}

// R3 (scheduler.go:224): only unclaimed expirations were marked, so a
// claimed seat running silently past expiry got another task and CleanSeat
// reset its live worktree. An expired claimed attempt closes the seat
// through the launcher; it is never prepared (cleaned) or dispatched to
// again, and a launcher whose close does not confirm the termination keeps
// the seat counted until its SeatGone.
func TestExpiredClaimedSeatIsClosedBeforeAnyCleanupOrDispatch(t *testing.T) {
	for _, async := range []bool{false, true} {
		h := newHarness(t, Config{LeaseDuration: 10 * time.Minute, IdleTimeout: time.Hour})
		l := &asyncLauncher{fakeLauncher: h.l, async: async}
		h.s.Launcher = l
		p := &preparer{}
		h.s.Preparer = p
		h.add("long", &sonnet)
		h.up("seat-a", sonnet)
		h.tick()
		h.store.Claim("t1", "seat-a", nil, 0, h.clk.Now())
		h.clk.Add(11 * time.Minute)
		h.add("next", &sonnet)
		d := h.tick()
		if !has(d, "seat_closing", "t1", "seat-a") || has(d, "dispatched", "", "seat-a") {
			t.Fatalf("async=%v: the expired seat: %+v", async, d)
		}
		if len(h.l.closed) != 1 || h.l.closed[0] != "seat-a" {
			t.Fatalf("async=%v: not closed through the launcher: %v", async, h.l.closed)
		}
		prepared := len(p.calls)
		d = h.tick()
		if has(d, "dispatched", "", "seat-a") || len(p.calls) != prepared {
			t.Fatalf("async=%v: the closing seat was prepared or dispatched to: %+v %v", async, d, p.calls)
		}
		if async {
			if h.s.seats["seat-a"] == nil || !h.s.seats["seat-a"].awaitGone {
				t.Fatal("an unconfirmed close forgot the seat before its seat_gone")
			}
			h.s.Post(Event{Kind: SeatGone, At: h.clk.Now(), Handle: "seat-a"})
			h.tick()
		}
		if h.s.seats["seat-a"] != nil {
			t.Fatalf("async=%v: the seat is still counted after its confirmed close", async)
		}
	}
}

type asyncLauncher struct {
	*fakeLauncher
	async bool
}

func (a *asyncLauncher) CloseUnconfirmed(string) bool { return a.async }

// P2 (scheduler.go:387 and :393): completion and the PR binding were two
// writes, so a failed binding left a completed, unbound task. They are one
// store transaction: a PR number another task holds refuses for the lead
// (pr_bound_elsewhere) and the task is not completed.
func TestCompletionAndPRBindingAreOneTransaction(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour})
	h.store.Add(task.AddInput{Title: "holder", Class: "implement", DeclaredFiles: []string{"z/"}}, t0)
	h.store.Claim("t1", "seat-z", nil, time.Hour, t0)
	h.store.Done("t1", "seat-z", "ok", "", t0)
	if _, err := h.store.CompleteBound("t1", "o/r", 42, strings.Repeat("a", 40), t0); err != nil {
		t.Fatal(err)
	}
	h.s.Committer = &scripted{results: []CommitResult{{OK: true, PRRepository: "o/r", PRNumber: 42, PRHead: strings.Repeat("b", 40)}}}
	h.store.Add(task.AddInput{Title: "second", Class: "implement", Pool: &sonnet, DeclaredFiles: []string{"a/"}}, t0)
	h.up("seat-a", sonnet)
	h.tick()
	h.store.Claim("t2", "seat-a", nil, 0, t0)
	h.store.Done("t2", "seat-a", "ok", "", t0)
	d := h.tick()
	got := h.get("t2")
	if got.State == task.StateCompleted || got.PRNumber != 0 || got.BlockReason != task.RefusalPRBoundElsewhere || !has(d, "commit_refused", "t2", "seat-a") {
		t.Fatalf("a PR bound to t1 completed t2: %+v (%+v)", got, d)
	}
}

// P1-9 (scheduler.go:326): a seat_gone for a handle this incarnation does
// not know (after a restart) was ignored. Every seat_gone runs the exit
// check.
func TestEverySeatGoneRunsTheExitCheck(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour})
	ex := &exitRecorder{bad: map[string]bool{"stranger": true}}
	h.s.Exits = ex
	h.s.Post(Event{Kind: SeatGone, At: h.clk.Now(), Handle: "stranger"})
	d := h.tick()
	if !called(ex.calls, "stranger//gone") || !has(d, "seat_quarantined", "", "stranger") {
		t.Fatalf("an unknown seat's gone: calls %v, decisions %+v", ex.calls, d)
	}
}

// deferringExits defers every check until open is set.
type deferringExits struct {
	open  bool
	calls int
}

func (e *deferringExits) CheckExit(handle, taskID, why string) ExitResult {
	e.calls++
	if !e.open {
		return ExitResult{Deferred: true, Detail: "the preflight copy is unreadable"}
	}
	return ExitResult{OK: true}
}

// N3 (exits.go:93): a deferred exit check was requeued without reserving the
// seat, so CleanSeat and a dispatch went ahead before the quarantine
// decision. While a seat's check is queued or deferred the seat gets no
// preparation and no lease.
func TestDeferredExitCheckKeepsTheSeatReserved(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour})
	ex := &deferringExits{}
	h.s.Exits = ex
	p := &preparer{}
	h.s.Preparer = p
	h.add("fails", &sonnet)
	h.up("seat-a", sonnet)
	h.tick()
	h.store.Claim("t1", "seat-a", nil, 0, h.clk.Now())
	h.store.Fail("t1", "seat-a", "red", h.clk.Now())
	h.add("next", &sonnet)
	prepared := len(p.calls)
	for i := 0; i < 2; i++ {
		if d := h.tick(); has(d, "dispatched", "", "seat-a") || len(p.calls) != prepared {
			t.Fatalf("a seat with a deferred exit check was prepared or dispatched to: %+v %v", d, p.calls)
		}
	}
	ex.open = true
	if d := h.tick(); !has(d, "dispatched", "", "seat-a") {
		t.Fatalf("after the check decided: %+v", d)
	}
}

// holdLauncher records reservations and releases and can refuse closes.
type holdLauncher struct {
	*fakeLauncher
	reserved, released []string
	closeFails         int
}

func (h *holdLauncher) Reserve(handle string)  { h.reserved = append(h.reserved, handle) }
func (h *holdLauncher) SeatGone(handle string) { h.released = append(h.released, handle) }
func (h *holdLauncher) CloseSeat(handle, reason string) error {
	if h.closeFails > 0 {
		h.closeFails--
		return errors.New("not confirmed yet")
	}
	return h.fakeLauncher.CloseSeat(handle, reason)
}

// N2 (process.go:166, slots.go:65): adopted live seats counted in the seat
// map but not in the launcher's slots, and dispatch ran before the close, so
// a restart could reserve a handle whose old process still ran. Adoption
// holds the slot, and no dispatch happens until every adopted seat is
// closed and confirmed gone.
func TestRestartHoldsAdoptedSlotsAndClosesThemBeforeAnyDispatch(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour})
	l := &holdLauncher{fakeLauncher: h.l, closeFails: 1}
	h.s.Launcher = l
	h.s.AdoptClosing("old-1", sonnet, h.clk.Now())
	if len(l.reserved) != 1 || l.reserved[0] != "old-1" {
		t.Fatalf("the adopted slot was not held: %v", l.reserved)
	}
	h.add("work", &sonnet)
	h.up("seat-a", sonnet)
	if d := h.tick(); !has(d, "dispatch_held", "", "") || has(d, "dispatched", "", "") || len(h.l.requests) != 0 {
		t.Fatalf("a dispatch before the adopted seat closed: %+v", d)
	}
	if d := h.tick(); h.s.seats["old-1"] != nil || !has(d, "dispatched", "t1", "seat-a") {
		t.Fatalf("after the confirmed close: %+v", d)
	}
}

// N3 (scheduler.go:340, :356; exits.go:87): seat_gone deleted the seat and
// released its slot even while its exit check deferred, so a replacement
// overwrote the staged evidence, and the deferred queue lived in memory. The
// gone seat's slot stays held until the check is decided, and the queue is
// persisted to the exits ledger and rebuilt by a restarted scheduler (which
// holds the slot again).
func TestDeferredGoneCheckHoldsTheSlotAndSurvivesARestart(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "exits.json")
	h := newHarness(t, Config{IdleTimeout: time.Hour, ExitsPath: ledger})
	l := &holdLauncher{fakeLauncher: h.l}
	h.s.Launcher = l
	ex := &deferringExits{}
	h.s.Exits = ex
	h.up("seat-a", sonnet)
	h.tick()
	h.s.Post(Event{Kind: SeatGone, At: h.clk.Now(), Handle: "seat-a"})
	h.tick()
	if len(l.released) != 0 {
		t.Fatalf("the slot was released while the check deferred: %v", l.released)
	}
	// A restart: a new scheduler over the same ledger.
	s2 := &Scheduler{Store: h.store, Now: h.clk.Now, Launcher: l, Exits: ex, Config: Config{IdleTimeout: time.Hour, ExitsPath: ledger}, Incarnation: "t2"}
	if _, err := s2.Tick(); err != nil {
		t.Fatal(err)
	}
	if len(l.reserved) == 0 || l.reserved[len(l.reserved)-1] != "seat-a" || !s2.exitPending("seat-a") {
		t.Fatalf("the restart lost the pending gone check: reserved %v", l.reserved)
	}
	ex.open = true
	if _, err := s2.Tick(); err != nil {
		t.Fatal(err)
	}
	if len(l.released) != 1 || l.released[0] != "seat-a" || s2.exitPending("seat-a") {
		t.Fatalf("after the check decided: released %v", l.released)
	}
}
