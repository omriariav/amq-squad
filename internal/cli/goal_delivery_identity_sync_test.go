package cli

import (
	"strings"
	"testing"
)

// #498: THE DELIVERY-IDENTITY SYNC GUARD.
//
// `validateTransitionGoalDeliveryBeforeSend` receives the attempt to send twice -- as the explicit
// `attemptID` parameter and inside `opts.AttemptID` -- and the consumed-recovery mutex downstream
// derives claim-once identity from the latter. They are equal on every path today, so the guard is
// unreachable in production. It exists because nothing ENFORCED that equality, and a future desync
// would silently make a claim-once decision about an attempt nobody is delivering.
//
// TESTING AN UNREACHABLE GUARD is exactly as valuable as testing a reachable one here, and for a
// reason worth stating: the guard's whole purpose is to fire on a state that does not yet exist. If it
// were only verified by production paths it would be verified by never running, and a guard that has
// never fired even once is indistinguishable from a guard that cannot fire.
//
// The guard is testable with almost no fixture precisely BECAUSE it is placed first: it returns before
// `withCurrentGoalIdentityWriterLocks`, so a desynced call never touches team state, locks, or the
// filesystem. That is a property of the placement, not luck, and moving the guard below the locks
// would break this test for reasons unrelated to the invariant -- which is the signal we would want.
func TestDesyncedDeliveryAttemptIdentityIsRefusedAsAWiringFault(t *testing.T) {
	opts := goalDeliveryOptions{
		Project: t.TempDir(),
		Profile: "squad",
		Session: "v2-25-0",
		Role:    "cto",
		// The identity the consumed-recovery mutex would derive from.
		AttemptID: "attempt-the-mutex-would-judge",
	}

	// The identity actually being sent. Different, which is the whole falsifying input.
	_, _, err := validateTransitionGoalDeliveryBeforeSend(opts, goalDeliveryReservation{}, "prompt",
		"attempt-actually-being-sent")

	if err == nil {
		t.Fatal("a desynced delivery identity must be REFUSED; proceeding would let the claim-once " +
			"mutex judge one attempt while another is delivered")
	}
	if !strings.Contains(err.Error(), "WIRING FAULT") {
		t.Errorf("refusal must be labelled a WIRING FAULT rather than a delivery failure, got: %v\n"+
			"An operator can do nothing about a desync between two internal identities, so a message "+
			"that reads like an operational failure invites a retry that would desync identically.", err)
	}
	// BOTH identities must appear, so a reader can see WHICH pair disagreed. A refusal that merely
	// says "desynced" would send someone hunting through two call chains.
	if !strings.Contains(err.Error(), "attempt-the-mutex-would-judge") ||
		!strings.Contains(err.Error(), "attempt-actually-being-sent") {
		t.Errorf("refusal must name both disagreeing identities, got: %v", err)
	}
}

// THE ANTI-VACUITY CONTROL. Without it, the row above would pass even if the function refused
// EVERYTHING -- including the synced case that production actually takes.
//
// This asserts only that the SYNCED call does not produce the wiring-fault refusal. It deliberately
// does NOT assert success: with an empty project directory the call fails later for entirely unrelated
// reasons (no team profile, no locks, a zero reservation), and demanding success would mean building a
// full delivery fixture to prove a negative about one guard.
func TestSyncedDeliveryAttemptIdentityDoesNotTripTheWiringGuard(t *testing.T) {
	const attemptID = "attempt-abc"
	opts := goalDeliveryOptions{
		Project:   t.TempDir(),
		Profile:   "squad",
		Session:   "v2-25-0",
		Role:      "cto",
		AttemptID: attemptID,
	}

	_, _, err := validateTransitionGoalDeliveryBeforeSend(opts, goalDeliveryReservation{}, "prompt", attemptID)

	// It will fail -- just not for THIS reason.
	if err != nil && strings.Contains(err.Error(), "WIRING FAULT") {
		t.Errorf("a SYNCED identity pair must not trip the wiring guard, got: %v.\nIf it does, the "+
			"guard refuses the case production actually takes and the row above proves nothing.", err)
	}
}

// WHITESPACE IS NOT A DESYNC. The guard compares trimmed values, because a stray space in one of two
// internally-generated identifiers is not the class of divergence this exists to catch -- and refusing
// it would turn a cosmetic difference into a blocked delivery.
func TestWhitespaceDifferencesAreNotTreatedAsADesync(t *testing.T) {
	opts := goalDeliveryOptions{
		Project:   t.TempDir(),
		Profile:   "squad",
		Session:   "v2-25-0",
		Role:      "cto",
		AttemptID: "  attempt-abc  ",
	}

	_, _, err := validateTransitionGoalDeliveryBeforeSend(opts, goalDeliveryReservation{}, "prompt", "attempt-abc")

	if err != nil && strings.Contains(err.Error(), "WIRING FAULT") {
		t.Errorf("padding-only difference must not read as a desync, got: %v", err)
	}
}
