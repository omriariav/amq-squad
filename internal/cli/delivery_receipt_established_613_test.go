package cli

import (
	"strings"
	"testing"
	"time"
)

// baseReceipt613 is a minimal receipt in the shape the field artifacts show:
// the always-frozen fields populated, the derived fields ABSENT. That is the
// state a pane_send actually persists (verified against 6 of 6 receipts in the
// v2-26-0 session under .amq-squad/receipts/), and it is the precondition for
// the bug this file pins.
func baseReceipt613() deliveryReceiptData {
	return deliveryReceiptData{
		SchemaVersion: 2,
		AttemptID:     "20260801T180656.781302000Z-pane_send-amq-dev-1-amq-dev-1-p60908-0000000000000001",
		Kind:          "pane_send",
		Sender:        "cto",
		CreatedAt:     time.Date(2026, 8, 1, 18, 6, 56, 0, time.UTC),
		// Every always-frozen field is populated here on purpose: a
		// "mutate it to empty" case against an already-empty base would be
		// empty-to-empty and pass vacuously, which is the failure mode the
		// guard-on-the-guard exists to prevent in the first place.
		Target:                   deliveryReceiptTarget{ProjectDir: "/p", Profile: "squad-v2-26-0", Session: "v2-26-0", Role: "amq-dev-1", Handle: "amq-dev-1"},
		Root:                     "/p/.agent-mail/squad-v2-26-0/v2-26-0",
		Path:                     "/p/.amq-squad/receipts/squad-v2-26-0/v2-26-0/a.json",
		PreparedRunGeneration:    "gen-1",
		PreparedRunLaunchAttempt: "attempt-1",
		PreparedRunDigest:        "sha256:digest",
		PreparedRunGoalNamespace: "squad-v2-26-0/v2-26-0",
		PreparedRunGoalDigest:    "sha256:goal",
	}
}

// TestEstablishThenFreezeAcceptsDerivation is #613's primary regression: a
// receipt persisted WITHOUT the derived fields, read back WITH them, must merge
// cleanly. Before the fix each of these produced
//
//	receipt_corrupt: immutable <field> changed for attempt <id>
//
// on a pane_send that had already delivered successfully — the false error that
// trains operators to ignore errors.
func TestEstablishThenFreezeAcceptsDerivation(t *testing.T) {
	for _, tc := range []struct {
		field   string
		derived func(deliveryReceiptData) deliveryReceiptData
	}{
		{"recipient", func(r deliveryReceiptData) deliveryReceiptData { r.Recipient = "amq-dev-1"; return r }},
		{"recipients", func(r deliveryReceiptData) deliveryReceiptData { r.Recipients = []string{"amq-dev-1"}; return r }},
		{"thread", func(r deliveryReceiptData) deliveryReceiptData { r.Thread = "p2p/amq-dev-1__cto"; return r }},
	} {
		t.Run(tc.field+"/absent-then-derived", func(t *testing.T) {
			if err := validateReceiptMergeIdentity(baseReceipt613(), tc.derived(baseReceipt613())); err != nil {
				t.Fatalf("establishing %s must not be reported as mutation: %v", tc.field, err)
			}
		})
		// Symmetric: the derived side may equally be the CURRENT one, because
		// which side carries the derivation depends on read/write ordering.
		t.Run(tc.field+"/derived-then-absent", func(t *testing.T) {
			if err := validateReceiptMergeIdentity(tc.derived(baseReceipt613()), baseReceipt613()); err != nil {
				t.Fatalf("establishing %s in the other direction must not be reported as mutation: %v", tc.field, err)
			}
		})
	}
}

// TestEstablishThenFreezeStillRefusesGenuineMutation is the other half, and the
// half that makes the fix a narrowing rather than a deletion. Relaxing a guard
// is not removing it: once a derived field is ESTABLISHED, changing it to a
// different value is still corruption and must still refuse with the same
// receipt_corrupt string — consumers parse it.
func TestEstablishThenFreezeStillRefusesGenuineMutation(t *testing.T) {
	for _, tc := range []struct {
		field    string
		current  func(deliveryReceiptData) deliveryReceiptData
		incoming func(deliveryReceiptData) deliveryReceiptData
	}{
		{
			"recipient",
			func(r deliveryReceiptData) deliveryReceiptData { r.Recipient = "amq-dev-1"; return r },
			func(r deliveryReceiptData) deliveryReceiptData { r.Recipient = "amq-dev-2"; return r },
		},
		{
			"recipients",
			func(r deliveryReceiptData) deliveryReceiptData { r.Recipients = []string{"amq-dev-1"}; return r },
			func(r deliveryReceiptData) deliveryReceiptData { r.Recipients = []string{"amq-dev-2"}; return r },
		},
		{
			"thread",
			func(r deliveryReceiptData) deliveryReceiptData { r.Thread = "p2p/amq-dev-1__cto"; return r },
			func(r deliveryReceiptData) deliveryReceiptData { r.Thread = "p2p/amq-dev-2__cto"; return r },
		},
	} {
		t.Run(tc.field, func(t *testing.T) {
			err := validateReceiptMergeIdentity(tc.current(baseReceipt613()), tc.incoming(baseReceipt613()))
			if err == nil {
				t.Fatalf("changing an established %s is corruption and must refuse", tc.field)
			}
			// Assert the exact contract, not merely that something failed: the
			// message names the field and keeps the receipt_corrupt prefix.
			if !strings.Contains(err.Error(), "receipt_corrupt: immutable "+tc.field+" changed") {
				t.Fatalf("refusal must name the field and keep the parseable prefix, got: %v", err)
			}
		})
	}
}

// TestAlwaysFrozenFieldsRefuseEvenFromEmpty is the GUARD ON THE GUARD.
//
// The fix introduces an establish-then-freeze class. The risk it creates is that
// a field which must NEVER be empty quietly drifts into that class during a
// later refactor, at which point corruption would merge silently. This asserts
// the always-frozen set still refuses on any change INCLUDING from-empty, so the
// two classes cannot be confused without a test failing.
//
// The table is COMPLETE, not representative: it names every field in the
// always-frozen set. A representative subset would leave the unlisted fields
// free to drift into establish-then-freeze unobserved, which is precisely the
// drift this test claims to prevent. If a field is added to the always-frozen
// set in the checks table, it must be added here too.
func TestAlwaysFrozenFieldsRefuseEvenFromEmpty(t *testing.T) {
	for _, tc := range []struct {
		field  string
		mutate func(deliveryReceiptData) deliveryReceiptData
	}{
		{"schema_version", func(r deliveryReceiptData) deliveryReceiptData { r.SchemaVersion = 0; return r }},
		{"attempt_id", func(r deliveryReceiptData) deliveryReceiptData { r.AttemptID = ""; return r }},
		{"kind", func(r deliveryReceiptData) deliveryReceiptData { r.Kind = ""; return r }},
		{"sender", func(r deliveryReceiptData) deliveryReceiptData { r.Sender = ""; return r }},
		{"created_at", func(r deliveryReceiptData) deliveryReceiptData { r.CreatedAt = time.Time{}; return r }},
		{"target", func(r deliveryReceiptData) deliveryReceiptData { r.Target = deliveryReceiptTarget{}; return r }},
		{"root", func(r deliveryReceiptData) deliveryReceiptData { r.Root = ""; return r }},
		{"path", func(r deliveryReceiptData) deliveryReceiptData { r.Path = ""; return r }},
		{"prepared_run_generation", func(r deliveryReceiptData) deliveryReceiptData { r.PreparedRunGeneration = ""; return r }},
		{"prepared_run_launch_attempt", func(r deliveryReceiptData) deliveryReceiptData { r.PreparedRunLaunchAttempt = ""; return r }},
		{"prepared_run_digest", func(r deliveryReceiptData) deliveryReceiptData { r.PreparedRunDigest = ""; return r }},
		{"prepared_run_goal_namespace", func(r deliveryReceiptData) deliveryReceiptData { r.PreparedRunGoalNamespace = ""; return r }},
		{"prepared_run_goal_digest", func(r deliveryReceiptData) deliveryReceiptData { r.PreparedRunGoalDigest = ""; return r }},
	} {
		t.Run(tc.field+"/to-empty", func(t *testing.T) {
			if err := validateReceiptMergeIdentity(baseReceipt613(), tc.mutate(baseReceipt613())); err == nil {
				t.Fatalf("%s is always-frozen: even a change to empty must refuse, or it has silently"+
					" joined the establish-then-freeze class", tc.field)
			}
		})
	}
}

// TestMergePreservesEstablishedFields is amq-dev-2's F1, turned into a
// regression, and it tests the EFFECT rather than the predicate.
//
// The validator accepting establishment in both directions is only half a
// contract. If the merge then writes the established value back to empty,
// acceptance is LOSSY: the check says "this is establishment, not mutation" and
// the merge un-establishes it. Establish-then-freeze has to mean frozen.
//
// This gap was unreachable before the validator was narrowed — strict equality
// refused populated-vs-empty outright — so narrowing the check without widening
// the carry-forward is what opened it. The two must move together, and this test
// is what holds them together.
//
// Note deliberately what the other tests in this file CANNOT do: they call
// validateReceiptMergeIdentity directly, so they prove refusal and acceptance
// but never observe the merged receipt. A predicate test cannot catch a lossy
// merge. That is why this one drives mergeDeliveryReceipt.
func TestMergePreservesEstablishedFields(t *testing.T) {
	established := func(r deliveryReceiptData) deliveryReceiptData {
		r.Recipient = "amq-dev-1"
		r.Recipients = []string{"amq-dev-1"}
		r.Thread = "p2p/amq-dev-1__cto"
		return r
	}

	t.Run("incoming-empty-keeps-established", func(t *testing.T) {
		merged, err := mergeDeliveryReceipt(established(baseReceipt613()), baseReceipt613())
		if err != nil {
			t.Fatalf("merging an empty incoming over established values must not fail: %v", err)
		}
		if merged.Recipient != "amq-dev-1" {
			t.Errorf("recipient was un-established by the merge: got %q", merged.Recipient)
		}
		if len(merged.Recipients) != 1 || merged.Recipients[0] != "amq-dev-1" {
			t.Errorf("recipients was un-established by the merge: got %v", merged.Recipients)
		}
		if merged.Thread != "p2p/amq-dev-1__cto" {
			t.Errorf("thread was un-established by the merge: got %q", merged.Thread)
		}
	})

	t.Run("incoming-established-over-empty-current", func(t *testing.T) {
		merged, err := mergeDeliveryReceipt(baseReceipt613(), established(baseReceipt613()))
		if err != nil {
			t.Fatalf("establishing values over an empty current must not fail: %v", err)
		}
		if merged.Recipient != "amq-dev-1" || merged.Thread != "p2p/amq-dev-1__cto" ||
			len(merged.Recipients) != 1 {
			t.Errorf("establishment in this direction must land: recipient=%q recipients=%v thread=%q",
				merged.Recipient, merged.Recipients, merged.Thread)
		}
	})
}
