package scheduler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Tests of the 2b-2 milestone B fix commit 4 (the fourth pass on 13c1fe76).

// asyncHoldLauncher is a holdLauncher whose nil close does not confirm the
// seat's termination (the external stand-in).
type asyncHoldLauncher struct{ *holdLauncher }

func (a *asyncHoldLauncher) CloseUnconfirmed(string) bool { return true }

// N2 (exits.go:292-299): every nil close of an adopted seat counted as
// confirmed, so an External seat of the previous incarnation was forgotten,
// and dispatch went ahead, while it still ran. A nil close confirms only for
// a launcher that confirms synchronously; an AsyncCloser's adopted seat stays
// closing, and dispatch held, until its seat_gone.
func TestAdoptedSeatOfAnAsyncCloserStaysClosingUntilSeatGone(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour})
	l := &asyncHoldLauncher{&holdLauncher{fakeLauncher: h.l}}
	h.s.Launcher = l
	h.s.AdoptClosing("old-1", sonnet, h.clk.Now())
	h.add("work", &sonnet)
	h.up("seat-a", sonnet)
	for i := 0; i < 2; i++ {
		if d := h.tick(); !has(d, "dispatch_held", "", "") || has(d, "dispatched", "", "") || h.s.seats["old-1"] == nil || h.s.Ready() {
			t.Fatalf("tick %d: an unconfirmed close of an adopted seat let dispatch go ahead: %+v", i+1, d)
		}
	}
	h.s.Post(Event{Kind: SeatGone, At: h.clk.Now(), Handle: "old-1"})
	if d := h.tick(); !has(d, "dispatched", "t1", "seat-a") || !h.s.Ready() {
		t.Fatalf("after the adopted seat's seat_gone: %+v", d)
	}
}

func readExitsLedger(t *testing.T, path string) exitsLedger {
	t.Helper()
	var l exitsLedger
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &l); err != nil {
		t.Fatal(err)
	}
	return l
}

func ledgerHas(l exitsLedger, handle string) bool {
	for _, it := range l.Queue {
		if it.Handle == handle {
			return true
		}
	}
	return false
}

// N3 (a) (exits.go:112, :120, :150): AdoptClosing's queueExit wrote the
// exits ledger before loadExits read it, so the previous incarnation's
// unrelated deferred checks were lost. Adoption loads the ledger first: both
// the adopted seat's check and the earlier deferred one are persisted, and
// the earlier gone seat's slot is held again.
func TestAdoptionLoadsTheExitsLedgerFirst(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "exits.json")
	raw, _ := json.Marshal(exitsLedger{Schema: 1, Queue: []exitItem{{Handle: "gone-1", Why: "gone", Gone: true, At: t0}}})
	if err := os.WriteFile(ledger, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, Config{IdleTimeout: time.Hour, ExitsPath: ledger})
	l := &holdLauncher{fakeLauncher: h.l}
	h.s.Launcher, h.s.Exits = l, &deferringExits{}
	h.s.AdoptClosing("old-1", sonnet, h.clk.Now())
	got := readExitsLedger(t, ledger)
	if !ledgerHas(got, "gone-1") || !ledgerHas(got, "old-1") || !h.s.exitPending("gone-1") {
		t.Fatalf("the ledger after adoption: %+v", got)
	}
	if strings.Join(l.reserved, ",") != "gone-1,old-1" {
		t.Fatalf("reserved %v", l.reserved)
	}
}

// N3 (b) (exits.go:139-145, :159-165): a failed write or read of the exits
// ledger was ignored, so the checks it should hold were silently lost across
// a restart. A ledger that cannot be read or written stops dispatch
// (exits_ledger_unwritable) until it can.
func TestUnwritableExitsLedgerStopsDispatch(t *testing.T) {
	// Unreadable: the ledger does not parse.
	dir := t.TempDir()
	ledger := filepath.Join(dir, "exits.json")
	os.WriteFile(ledger, []byte("{"), 0o600)
	h := newHarness(t, Config{IdleTimeout: time.Hour, ExitsPath: ledger})
	h.s.Exits = &exitRecorder{}
	h.add("work", &sonnet)
	h.up("seat-a", sonnet)
	if d := h.tick(); !has(d, "exits_ledger_unwritable", "", "") || has(d, "dispatched", "", "") {
		t.Fatalf("an unreadable ledger: %+v", d)
	}
	os.WriteFile(ledger, []byte(`{"schema":1,"queue":[]}`), 0o600)
	if d := h.tick(); !has(d, "dispatched", "t1", "seat-a") {
		t.Fatalf("after the ledger was repaired: %+v", d)
	}
	// Unwritable: its directory refuses the write.
	ro := filepath.Join(t.TempDir(), "ro")
	os.MkdirAll(ro, 0o700)
	h2 := newHarness(t, Config{IdleTimeout: time.Hour, ExitsPath: filepath.Join(ro, "exits.json")})
	h2.s.Exits = &exitRecorder{}
	h2.add("work", &sonnet)
	h2.up("seat-a", sonnet)
	os.Chmod(ro, 0o500)
	t.Cleanup(func() { os.Chmod(ro, 0o700) })
	if d := h2.tick(); !has(d, "exits_ledger_unwritable", "", "") || has(d, "dispatched", "", "") {
		t.Fatalf("an unwritable ledger: %+v", d)
	}
	os.Chmod(ro, 0o700)
	if d := h2.tick(); !has(d, "dispatched", "t1", "seat-a") {
		t.Fatalf("after the ledger became writable: %+v", d)
	}
}

// partialExits defers the checks of one why until open.
type partialExits struct {
	deferWhy string
	open     bool
}

func (e *partialExits) CheckExit(handle, taskID, why string) ExitResult {
	if why == e.deferWhy && !e.open {
		return ExitResult{Deferred: true, Detail: "not yet"}
	}
	return ExitResult{OK: true}
}

// ledgerLauncher records, at each release, whether the exits ledger still
// held an item for the released handle.
type ledgerLauncher struct {
	*holdLauncher
	ledger      string
	t           *testing.T
	heldAtStart []bool
}

func (l *ledgerLauncher) SeatGone(handle string) {
	l.heldAtStart = append(l.heldAtStart, ledgerHas(readExitsLedger(l.t, l.ledger), handle))
	l.holdLauncher.SeatGone(handle)
}

// N3 (c) (exits.go:221-254): runExits released a handle's slot when one of
// its items was decided while a later item for the same handle deferred, and
// released before the decided item left the persisted ledger. The slot is
// released only once every queued item for the handle is decided, and only
// after the ledger write that removes them.
func TestGoneSlotIsReleasedOnlyWhenEveryItemIsDecidedAndPersisted(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "exits.json")
	h := newHarness(t, Config{IdleTimeout: time.Hour, ExitsPath: ledger})
	l := &ledgerLauncher{holdLauncher: &holdLauncher{fakeLauncher: h.l}, ledger: ledger, t: t}
	ex := &partialExits{deferWhy: "failed"}
	h.s.Launcher, h.s.Exits = l, ex
	h.up("seat-a", sonnet)
	h.tick()
	h.s.Post(Event{Kind: SeatGone, At: h.clk.Now(), Handle: "seat-a"})
	h.s.queueExit(exitItem{Handle: "seat-a", TaskID: "t9", Why: "failed", At: h.clk.Now()})
	h.tick()
	if len(l.released) != 0 || !h.s.exitPending("seat-a") {
		t.Fatalf("the slot was released while another check for the seat deferred: %v", l.released)
	}
	ex.open = true
	h.tick()
	if len(l.released) != 1 || l.released[0] != "seat-a" || len(l.heldAtStart) != 1 || l.heldAtStart[0] {
		t.Fatalf("the release: %v (ledger still held the seat at the release: %v)", l.released, l.heldAtStart)
	}
}
