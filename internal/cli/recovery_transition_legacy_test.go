package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// COMPATIBILITY PROOF (1) of the two dev-2 ruled must both exist and neither substitute for the
// other:
//
//	(1) HERE: a legacy fixture built THROUGH resumeGoalTransitionID, proving the CURRENT code
//	    recognises and blocks the reservations the old code wrote. This is about the reader.
//	(2) pr5-roundtrip: a fixed hard-coded golden AttemptID+BindingDigest -> exact id/path vector,
//	    proving the ALGORITHM has not moved. This is about persisted records on disk.
//
// Why (1) cannot be a hard-coded name: the fixture must be what the writer WOULD produce, so if
// the legacy derivation is ever changed this fixture changes with it and the recognition test
// keeps passing -- correctly, because recognition tracks the writer. Why (2) cannot be built by a
// helper: a helper that drifts with the implementation proves nothing about records already
// written. Each covers exactly the failure the other cannot see, which is why one is not
// redundant with the other.

// legacyReservationFor builds the filename the PRE-PR5 writer would have produced, through the
// production helper rather than by string assembly. String assembly here would be a second
// derivation owner inside the test -- the very defect PR5 exists to remove.
func legacyReservationFor(t *testing.T, dir, attemptID, bindingDigest string) (string, string) {
	t.Helper()
	id := resumeGoalTransitionID(attemptID, bindingDigest)
	name := legacyRecoveryTransitionPrefix + id + ".json"
	path := filepath.Join(dir, name)
	// A legacy body carries NO recovery_kind: that field did not exist when these were written.
	// Its absence is the whole point of the read/write asymmetry -- absent means legacy on READ,
	// and refuses on WRITE.
	if err := os.WriteFile(path, []byte(`{"transition_id":"`+id+`"}`), 0o644); err != nil {
		t.Fatalf("write legacy fixture: %v", err)
	}
	return name, id
}

func TestLegacyReservationsAreRecognisedAndBlockTheirOwnPause(t *testing.T) {
	dir := t.TempDir()
	const attemptID = "attempt-abc"
	const bindingDigest = "digest-def"

	name, id := legacyReservationFor(t, dir, attemptID, bindingDigest)

	// RECOGNITION. A legacy name carries no kind, so recognition must SUPPLY redeliver. If it
	// returned NotATransition instead, a live redelivery claim would be invisible to supervision
	// resume -- one delivered resume authorising another, the forbidden case.
	parsed, recognition := recognizeRecoveryTransitionName(name)
	if recognition != recoveryNameRecognized {
		t.Fatalf("a legacy reservation must be RECOGNISED, got %v for %q", recognition, name)
	}
	if !parsed.Legacy {
		t.Error("a legacy-named reservation must be classified Legacy, or it will be matched against the current key and missed")
	}
	if parsed.Kind != recoveryTransitionKindRedeliver {
		t.Errorf("kind = %q, want redeliver: legacy names carry no kind, so recognition supplies it", parsed.Kind)
	}
	if parsed.ClaimKey != id {
		t.Errorf("claim key = %q, want %q", parsed.ClaimKey, id)
	}

	// BLOCKING, for its OWN pause. Reserved-and-not-consumed is indeterminate, and indeterminate
	// refuses.
	scan, err := scanRecoveryTransitionsForPause(dir, strings.Repeat("f", 64), id)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	blocker := scan.blocking()
	if blocker == nil {
		t.Fatal("a legacy reservation for THIS pause must block: the old writer's claims are " +
			"authoritative, and PR5 removes their writer path, not their standing")
	}
	if strings.TrimSpace(blocker.recovery()) == "" {
		t.Error("the blocker must carry a runnable next step")
	}
}

// The other half, and the one that turns the property above from "blocks" into "blocks the RIGHT
// pause". Without it, blocking would be satisfied by a scan that blocked on any legacy file
// anywhere -- which would wedge every future pause behind any historical record in the directory.
//
// THE FALSIFYING INPUT: a legacy reservation for a DIFFERENT attempt, in the SAME directory. It
// is byte-for-byte the same KIND of artifact; only the identity differs.
func TestALegacyReservationForADifferentAttemptDoesNotBlockThisPause(t *testing.T) {
	dir := t.TempDir()

	_, otherID := legacyReservationFor(t, dir, "attempt-SOMEONE-ELSE", "digest-other")
	ourID := resumeGoalTransitionID("attempt-abc", "digest-def")
	if otherID == ourID {
		t.Fatal("fixture is void: both attempts hash to the same legacy id")
	}

	scan, err := scanRecoveryTransitionsForPause(dir, strings.Repeat("f", 64), ourID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if blocker := scan.blocking(); blocker != nil {
		t.Errorf("a legacy reservation for a DIFFERENT attempt must NOT block this pause (%s).\n"+
			"Directory-wide legacy blocking would wedge every future pause behind any historical "+
			"record, which is an outage dressed as caution.", blocker.describe())
	}
}

// CONSUMED STILL BLOCKS, on the legacy derivation too. This is the ruled semantics and it is
// counter-intuitive enough to be worth pinning: a COMPLETED redelivery means a delivery may have
// reached the pane, so it is terminal ownership evidence about this pause -- never permission for
// a second automatic action.
//
// My first scan treated proven-consumed as ignorable. It contradicted two ratified texts and would
// have made a completed redelivery INVISIBLE to supervision resume.
func TestAConsumedLegacyReservationStillBlocks(t *testing.T) {
	dir := t.TempDir()
	name, id := legacyReservationFor(t, dir, "attempt-abc", "digest-def")

	consumed := resumeGoalTransitionConsumedPath(filepath.Join(dir, name))
	if err := os.WriteFile(consumed, []byte(`{"consumed":true}`), 0o644); err != nil {
		t.Fatalf("write consumption record: %v", err)
	}

	scan, err := scanRecoveryTransitionsForPause(dir, strings.Repeat("f", 64), id)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	blocker := scan.blocking()
	if blocker == nil {
		t.Fatal("a CONSUMED reservation must still block: a delivery may have reached the pane, so " +
			"a second automatic delivery is refused. There is no non-blocking lifecycle state for " +
			"a matching key.")
	}
	if !strings.Contains(blocker.Reason, "CONSUMED") {
		t.Errorf("the blocker must say the claim was consumed, so the operator knows a delivery may "+
			"already have landed rather than thinking it stalled; got %q", blocker.Reason)
	}
}
