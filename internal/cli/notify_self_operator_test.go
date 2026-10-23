package cli

import (
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/attention"
)

func attentionState(items []operatorAttention, kind string, cleared bool) bool {
	for _, i := range items {
		if i.EventType == kind && i.Cleared == cleared {
			return true
		}
	}
	return false
}

func TestSelfOperatorEventSelectClearReopen(t *testing.T) {
	now := time.Now()
	key := "default/s\x00self_approved\x00gate/x"
	active := operatorAttention{EventType: "self_approved", Key: key, LatestID: "a1"}
	sel, _, st := selectNotifications([]operatorAttention{active}, notifyStateFile{Schema: 2, Items: map[string]notifyStateRecord{}}, time.Hour, now)
	if len(sel) != 1 || !st.Items[key].Active {
		t.Fatal("initial event not selected")
	}
	_, _, st = selectNotifications([]operatorAttention{{EventType: "self_approved", Key: key, LatestID: "a1", Cleared: true}}, st, time.Hour, now.Add(time.Minute))
	if st.Items[key].Active {
		t.Fatal("clear remained active")
	}
	sel, _, st = selectNotifications([]operatorAttention{active}, st, time.Hour, now.Add(2*time.Minute))
	if len(sel) != 1 || !st.Items[key].Active {
		t.Fatal("same-fingerprint reopen not selected")
	}
}

func TestScopedSelfOperatorTombstonesModeFlipAndRemoval(t *testing.T) {
	self := attention.SelfApprovedKey("default", "s", "gate/x")
	human := attention.HumanOnlyGateKey("default", "s", "gate/x")
	other := attention.SelfApprovedKey("other", "s", "gate/x")
	prior := notifyStateFile{Schema: 2, Items: map[string]notifyStateRecord{self: {Active: true}, human: {Active: true}, other: {Active: true}}}
	items := scopedSelfOperatorTombstones(nil, prior, "default", "s")
	if !attentionState(items, "self_approved", true) || !attentionState(items, "human_only_gate", true) || len(items) != 2 {
		t.Fatalf("scope tombstones=%+v", items)
	}
	_, _, next := selectNotifications(items, prior, time.Hour, time.Now())
	if next.Items[self].Active || next.Items[human].Active || !next.Items[other].Active {
		t.Fatalf("scope clear=%+v", next.Items)
	}
}
