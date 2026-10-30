package scheduler

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/sealchain"
)

// Phase 5 re-pass of 2b-2 B, P2-4: the exits ledger is written by the one
// durable writer (atomicfile: an exclusive temp file, fsync, rename, the
// directory fsync), not a shared <path>.tmp: a leftover at that name (a
// crashed writer's) never stops the write.
func TestExitsLedgerIsWrittenByTheDurableWriter(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "exits.json")
	if err := os.Mkdir(ledger+".tmp", 0o700); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, Config{IdleTimeout: time.Hour, ExitsPath: ledger})
	h.s.Exits = &deferringExits{}
	h.s.AdoptClosing("old-1", sonnet, h.clk.Now())
	if h.s.exitsErr != nil {
		t.Fatalf("the exits ledger write failed: %v", h.s.exitsErr)
	}
	if got := readExitsLedger(t, ledger); !ledgerHas(got, "old-1") {
		t.Fatalf("the ledger after the write: %+v", got)
	}
}

// Phase 5 re-pass of 2b-2 B, the ruling: the exits queue is sealed under
// the session seed with a signed head; a same-UID edit of the queue, or its
// removal, is ledger_tampered: dispatch stops and the block is logged for
// the owner.
func TestSealedExitsQueueDetectsASameUIDEdit(t *testing.T) {
	root := t.TempDir()
	ledger := filepath.Join(t.TempDir(), "exits.json")
	cfg := Config{IdleTimeout: time.Hour, ExitsPath: ledger, SealRoot: root}
	h := newHarness(t, cfg)
	h.s.Exits = &deferringExits{}
	h.s.AdoptClosing("old-1", sonnet, h.clk.Now())
	if h.s.exitsErr != nil {
		t.Fatal(h.s.exitsErr)
	}
	reload := func() *harness {
		g := newHarness(t, cfg)
		g.s.Exits = &deferringExits{}
		g.add("work", &sonnet)
		g.up("seat-a", sonnet)
		return g
	}
	if g := reload(); has(g.tick(), "ledger_tampered", "", "") {
		t.Fatalf("an untouched sealed queue was refused: %v", g.s.exitsErr)
	}
	raw, _ := os.ReadFile(ledger)
	// The edit: the queue emptied by hand (a deferred gone-only check lost).
	edited := []byte(`{"schema":1,"queue":[],"seal":` + string(sealOf(t, raw)) + `}`)
	if err := os.WriteFile(ledger, edited, 0o600); err != nil {
		t.Fatal(err)
	}
	g := reload()
	if d := g.tick(); !has(d, "ledger_tampered", "", "") || has(d, "dispatched", "", "") || !errors.Is(g.s.exitsErr, sealchain.ErrTampered) {
		t.Fatalf("a hand-edited exits queue: %+v (%v)", d, g.s.exitsErr)
	}
	// The removal: the head says the queue existed.
	os.Remove(ledger)
	g = reload()
	if d := g.tick(); !has(d, "ledger_tampered", "", "") || has(d, "dispatched", "", "") {
		t.Fatalf("a removed exits queue: %+v (%v)", d, g.s.exitsErr)
	}
}

// sealOf is the raw seal object of a sealed exits ledger.
func sealOf(t *testing.T, raw []byte) []byte {
	t.Helper()
	var l struct {
		Seal any `json:"seal"`
	}
	if err := json.Unmarshal(raw, &l); err != nil || l.Seal == nil {
		t.Fatalf("no seal in the exits ledger: %v", err)
	}
	b, _ := json.Marshal(l.Seal)
	return b
}

// The fix round's item 25: a sealed exits queue that does not decode, or
// carries another schema, is ledger_tampered (dispatch stops, logged for the
// owner), never a plain parse error.
func TestUndecodableSealedExitsQueueIsTampered(t *testing.T) {
	for _, body := range []string{"{", `{"schema":2,"queue":[]}`} {
		root := t.TempDir()
		ledger := filepath.Join(t.TempDir(), "exits.json")
		cfg := Config{IdleTimeout: time.Hour, ExitsPath: ledger, SealRoot: root}
		h := newHarness(t, cfg)
		h.s.Exits = &deferringExits{}
		h.s.AdoptClosing("old-1", sonnet, h.clk.Now())
		if err := os.WriteFile(ledger, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		g := newHarness(t, cfg)
		g.s.Exits = &deferringExits{}
		g.add("work", &sonnet)
		g.up("seat-a", sonnet)
		if d := g.tick(); !has(d, "ledger_tampered", "", "") || has(d, "dispatched", "", "") || !errors.Is(g.s.exitsErr, sealchain.ErrTampered) {
			t.Fatalf("%q: %+v (%v)", body, d, g.s.exitsErr)
		}
	}
}
