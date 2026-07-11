package attention

import (
	"errors"
	"testing"
	"time"
)

func TestDedupPerSinkEscalationClearAndFailure(t *testing.T) {
	now := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	e := Event{Key: "p/s\x00gate\x00gate/x", Fingerprint: "m1", Escalation: "initial"}
	sel, st, _ := Select([]Event{e}, State{}, "desktop", time.Hour, now, false)
	if len(sel) != 1 {
		t.Fatal(len(sel))
	}
	st = Commit(st, e.Key, "desktop", e, now, nil)
	sel, st, _ = Select([]Event{e}, st, "desktop", time.Hour, now.Add(time.Minute), false)
	if len(sel) != 0 {
		t.Fatal("duplicate")
	}
	e.Escalation = "reminder"
	sel, st, _ = Select([]Event{e}, st, "desktop", time.Hour, now.Add(31*time.Minute), false)
	if len(sel) != 1 {
		t.Fatal("escalation suppressed")
	}
	st = Commit(st, e.Key, "hook", e, now, errors.New("fail"))
	if st.Items[e.Key].Deliveries["hook"].FailureCount != 1 {
		t.Fatal("failure")
	}
	_, st, _ = Select(nil, st, "desktop", time.Hour, now, false)
	if st.Items[e.Key].Active {
		t.Fatal("not cleared")
	}
	sel, _, _ = Select([]Event{e}, st, "desktop", time.Hour, now, false)
	if len(sel) != 1 {
		t.Fatal("reactivation suppressed")
	}
}
func TestLocalFingerprintChanges(t *testing.T) {
	a := LocalInputFingerprint("%1", "approval", true, "Allow rm?")
	b := LocalInputFingerprint("%1", "approval", true, "Allow push?")
	if a == b {
		t.Fatal("same")
	}
}

func TestLocalInputUnknownPreservesAndConfirmedClearResets(t *testing.T) {
	now := time.Now()
	key := LocalInputKey("default", "s", "qa")
	e := Event{EventType: "local_input_blocked", Key: key, Fingerprint: "p1"}
	sel, st, _ := Select([]Event{e}, State{}, "desktop", time.Hour, now, false)
	st = Commit(st, key, "desktop", e, now, nil)
	if len(sel) != 1 {
		t.Fatal()
	}
	_, st, _ = Select(nil, st, "desktop", time.Hour, now.Add(time.Minute), false)
	if !st.Items[key].Active {
		t.Fatal("unknown absence cleared local blocker")
	}
	_, st, _ = Select([]Event{{EventType: "local_input_blocked", Key: key, Cleared: true}}, st, "desktop", time.Hour, now.Add(2*time.Minute), false)
	if st.Items[key].Active {
		t.Fatal("confirmed clear retained active")
	}
	sel, _, _ = Select([]Event{e}, st, "desktop", time.Hour, now.Add(3*time.Minute), false)
	if len(sel) != 1 {
		t.Fatal("reappearance suppressed")
	}
}
