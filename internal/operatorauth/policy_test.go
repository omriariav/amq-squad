package operatorauth

import "testing"

func TestImmutableExclusionsAndBindings(t *testing.T) {
	for _, kind := range []string{GateRelease, GateTag, GatePublish, GateExternalSend, GateDestructiveFilesystem, "unknown"} {
		if _, err := ValidateAllowlist([]string{kind}); err == nil {
			t.Fatalf("allowlist accepted human-only %q", kind)
		}
	}
	if err := Evaluate(GateMerge, "protected_branch_push", []string{GateMerge}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ kind, action string }{{GateSpawn, "github_release"}, {GateMerge, "default_branch_push"}, {GateFeatureBranchPush, "protected_branch_push"}} {
		if err := Evaluate(tc.kind, tc.action, []string{tc.kind}); err == nil {
			t.Fatalf("accepted gate/action mismatch %+v", tc)
		}
	}
}

func TestHumanPrecedence(t *testing.T) {
	facts := []MessageFact{{ID: "s", From: "cto", Bound: true, Decision: "approved", After: true}, {ID: "h", From: "user", Bound: true, Decision: "denied", After: true}}
	got := ResolvePrecedence(facts, "user", "cto")
	if got.Source != "human" || got.Decision != "denied" {
		t.Fatalf("precedence = %+v", got)
	}
	facts[1] = MessageFact{ID: "h", From: "user", After: true}
	if got := ResolvePrecedence(facts, "user", "cto"); !got.Barrier {
		t.Fatalf("human intervention did not create barrier: %+v", got)
	}
}

func TestHumanPrecedenceIsOrderIndependent(t *testing.T) {
	facts := []MessageFact{{ID: "s2", From: "cto", Bound: true, Decision: "approved", After: true}, {ID: "h1", From: "user", After: true}, {ID: "h3", From: "user", Bound: true, Decision: "approved", After: true}, {ID: "h2", From: "user", Bound: true, Decision: "denied", After: true}}
	for _, ordered := range [][]MessageFact{facts, {facts[3], facts[1], facts[0], facts[2]}} {
		got := ResolvePrecedence(ordered, "user", "cto")
		if got.Decision != "denied" || got.MessageID != "h2" {
			t.Fatalf("order-dependent precedence: %+v", got)
		}
	}
}

func TestHumanPrecedenceCompleteOrderingAndSameTimestamp(t *testing.T) {
	facts := []MessageFact{
		{ID: "s9", From: "cto", Bound: true, Decision: "approved", After: true, Order: 99},
		{ID: "h2", From: "user", Bound: false, After: true, Order: 20},
		{ID: "h3", From: "user", Bound: true, Decision: "approved", After: true, Order: 10},
		{ID: "h1", From: "user", Bound: true, Decision: "denied", After: true, Order: 1},
	}
	if got := ResolvePrecedence(facts, "user", "cto"); got.Decision != "denied" || got.MessageID != "h1" {
		t.Fatalf("denial did not outrank all: %+v", got)
	}
	if got := ResolvePrecedence(facts[:3], "user", "cto"); got.Decision != "approved" || got.MessageID != "h3" {
		t.Fatalf("approval did not outrank barrier/self: %+v", got)
	}
	if got := ResolvePrecedence(facts[:2], "user", "cto"); !got.Barrier || got.MessageID != "h2" {
		t.Fatalf("barrier did not outrank self: %+v", got)
	}
	same := []MessageFact{{ID: "a", From: "user", Bound: true, Decision: "denied", After: true, Order: 7}, {ID: "b", From: "user", Bound: true, Decision: "denied", After: true, Order: 7}}
	if got := ResolvePrecedence(same, "user", "cto"); got.MessageID != "b" {
		t.Fatalf("same timestamp ID tiebreak = %+v", got)
	}
}
