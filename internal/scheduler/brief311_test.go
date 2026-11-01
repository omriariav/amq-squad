package scheduler

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Brief 31-1, the lead's condition (1) on the quarantine's deletion: a task
// store that still carries a quarantine record written before 3.1 (the live
// scribe's claude_hooks_silent) no longer stops that seat: the scheduler
// dispatches to it and the store leases it, with nothing to clear.
func TestAnOldQuarantineRecordNoLongerStopsTheSeat(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour})
	dir := filepath.Join(h.store.Dir, "quarantine")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := `{"handle":"seat-a","reason":"claude_hooks_silent","detail":"no hook report","at":"2026-10-31T19:24:37Z"}`
	if err := os.WriteFile(filepath.Join(dir, "seat-a.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	h.add("a", &sonnet)
	h.up("seat-a", sonnet)
	d := h.tick()
	if !has(d, "dispatched", "t1", "seat-a") {
		t.Fatalf("decisions %+v", d)
	}
	if got := h.get("t1"); got.Lease == nil || got.Lease.Holder != "seat-a" {
		t.Fatalf("t1 %+v", got)
	}
	if _, err := h.store.Claim("t1", "seat-a", nil, time.Hour, h.clk.Now()); err != nil {
		t.Fatalf("the seat cannot claim: %v", err)
	}
}
