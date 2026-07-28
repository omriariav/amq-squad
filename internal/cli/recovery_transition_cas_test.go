package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// These three proofs exist because MUTATION TESTING FOUND THEM MISSING, not because I reasoned my
// way to them. W4, W6 and W2 all SURVIVED the first mutation run: the corresponding properties
// were asserted in prose, in file headers and in commit messages, and nowhere in a test.
//
// W4 is the one worth remembering. Bind's re-read-and-validate on a lost race is the finding I
// called the most consequential of the milestone; I wrote three paragraphs on why collapsing the
// three publications into one uniform helper would break it -- and making bind refuse on
// !published failed nothing. I documented the property and never pinned it. A comment is not a
// test, and being pleased with an insight is not evidence for it.

// W4: bind is the ONE publication of the three where a lost race is NOT a failure.
//
// BOTH DIRECTIONS, deliberately: a bind that refused everything would satisfy a
// refuses-on-change test alone, and a bind that accepted everything would satisfy an
// idempotent-retry test alone. Only the pair pins the actual contract.
func TestBindIsIdempotentForTheSameGenerationAndRefusesADifferentOne(t *testing.T) {
	const digest = "launch-digest-1"
	const modTime int64 = 1700000000

	newReservation := func(t *testing.T) recoveryReservation {
		t.Helper()
		dir := t.TempDir()
		return recoveryReservation{
			Record: resumeGoalTransitionRecord{
				SchemaVersion: resumeGoalTransitionSchemaVersion,
				TransitionID:  strings.Repeat("a", 64),
				NewAttemptID:  "attempt-new",
			},
			Path: filepath.Join(dir, ".resume-redelivery-"+strings.Repeat("a", 64)+".json"),
		}
	}

	t.Run("same generation binds twice", func(t *testing.T) {
		res := newReservation(t)
		if err := bindRecoveryTransitionGeneration(res, digest, modTime); err != nil {
			t.Fatalf("first bind must succeed: %v", err)
		}
		// THE LEGITIMATE RETRY. A prior attempt by this same actor already published the binding;
		// the second call loses the CAS race and must still succeed, because the existing binding
		// AGREES. Refusing here would break every resumed delivery that had already bound.
		if err := bindRecoveryTransitionGeneration(res, digest, modTime); err != nil {
			t.Errorf("re-binding the SAME launch generation must SUCCEED -- a lost race here is a "+
				"legitimate retry, not a failure. This is the one publication of the three where "+
				"!published is not an error: %v", err)
		}
	})

	t.Run("different generation refuses", func(t *testing.T) {
		res := newReservation(t)
		if err := bindRecoveryTransitionGeneration(res, digest, modTime); err != nil {
			t.Fatalf("first bind must succeed: %v", err)
		}
		// The runtime moved under us: the claim no longer describes the world it was made in.
		err := bindRecoveryTransitionGeneration(res, "launch-digest-CHANGED", modTime)
		if err == nil {
			t.Fatal("binding against a DIFFERENT launch generation must REFUSE: the reservation was " +
				"made against a generation that no longer exists, so proceeding would deliver into a " +
				"runtime the claim never authorised")
		}
		if !strings.Contains(err.Error(), "generation") {
			t.Errorf("the refusal must name the generation change: %v", err)
		}
	})
}

// W6: the omitempty tags are a COMPATIBILITY REQUIREMENT, not formatting.
//
// I claimed this in the struct comment and in the commit message. Dropping the tags failed
// nothing, because the round-trip proof goes through validateResumeGoalTransitionPlan, which never
// looks at serialised bytes. This asserts the on-disk SHAPE, which is the thing the claim is about.
//
// Both directions again: absent when unset, present when set. Without the second half, a record
// that dropped the fields entirely would pass.
func TestLegacyShapedRecordsSerialiseWithoutThePR5Keys(t *testing.T) {
	const kindKey = `"recovery_kind"`
	const pauseKey = `"pause_generation"`
	const fingerprintKey = `"preclaim_fingerprint"`

	legacy := resumeGoalTransitionRecord{
		SchemaVersion: resumeGoalTransitionSchemaVersion,
		TransitionID:  strings.Repeat("b", 64),
		CreatedAt:     time.Now().UTC(),
	}
	encoded, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{kindKey, pauseKey, fingerprintKey} {
		if strings.Contains(string(encoded), key) {
			t.Errorf("a legacy-shaped record must not emit %s.\nA record on disk written before PR5 "+
				"carries no such key; emitting it as an empty string makes an ABSENT value "+
				"indistinguishable from a deliberately-blank one, which is exactly the read/write "+
				"asymmetry this design depends on.\ngot: %s", key, encoded)
		}
	}

	// The companion direction: when the fields ARE set they must serialise, or omitempty would be
	// "correct" by never writing them at all.
	populated := legacy
	populated.RecoveryKind = string(recoveryTransitionKindNativeGoalResume)
	populated.PauseGeneration = "pause-gen-1"
	populated.PreclaimFingerprint = "fingerprint-1"
	encoded, err = json.Marshal(populated)
	if err != nil {
		t.Fatalf("marshal populated: %v", err)
	}
	for _, key := range []string{kindKey, pauseKey, fingerprintKey} {
		if !strings.Contains(string(encoded), key) {
			t.Errorf("a populated record MUST emit %s; without this the absence test above would be "+
				"satisfied by never writing the field at all\ngot: %s", key, encoded)
		}
	}
}

// W2, THE REAL ONE: a caller must not be able to SMUGGLE a transition id.
//
// I got this wrong twice and dev-2 caught both. First I claimed the behavioural mutation was
// INERT -- "no caller sets that field, so the branch never fires". Then I claimed the property was
// purely structural and pinned it with a reflection check on recoveryTransitionInput's DIRECT
// field names.
//
// BOTH CLAIMS WERE FALSE, for the same reason. recoveryTransitionInput.Base is a
// resumeGoalTransitionRecord, and that type HAS a TransitionID field. So the smuggling channel
// exists TODAY as Base.TransitionID; the direct-field pin sees only "Base" and never descends, so
// it passed while the hole it named was open. The old test's own NAME asserted something untrue.
//
// And the mutation was never inert: it is inert only for the production callers I happened to
// look at. A test is a caller, and future production callers are exactly what a pin is for. "No
// input can express this" was a conclusion I reached without trying to construct one.
//
// So the property gets its BEHAVIOURAL proof below, with the input dev-2 named: a valid, foreign,
// 64-char id in Base.TransitionID. The constructor must overwrite it with the derived identity.
func TestConstructorOverwritesASmuggledTransitionID(t *testing.T) {
	const attemptID = "attempt-abc"
	const bindingDigest = "digest-def"
	// A WELL-FORMED id belonging to someone else -- not garbage. Garbage would be rejected by any
	// shape check and would prove only that malformed input fails; this one is exactly as valid as
	// the right answer and differs only in being wrong.
	foreign := strings.Repeat("9", 64)
	canonical := resumeGoalTransitionID(attemptID, bindingDigest)
	if foreign == canonical {
		t.Fatal("fixture is void: the foreign id equals the canonical one")
	}

	project := t.TempDir()
	smuggling := redeliverTransitionBase(project, "squad", "v2-25-0", attemptID, bindingDigest)
	// THE SMUGGLING CHANNEL, applied to an otherwise complete Base so the supplied id is the row's only
	// defect. With an incomplete Base this row would now refuse on the redeliver contract and prove nothing
	// about smuggling.
	smuggling.TransitionID = foreign
	record, path, err := newRecoveryTransitionRecord(recoveryTransitionInput{
		Base:          smuggling,
		Kind:          recoveryTransitionKindRedeliver,
		AttemptID:     attemptID,
		BindingDigest: bindingDigest,
	})
	if err != nil {
		t.Fatalf("constructor refused a well-formed input: %v", err)
	}

	if record.TransitionID == foreign {
		t.Errorf("the constructor HONOURED a caller-supplied transition id (%q).\nIdentity is derived "+
			"per kind precisely so there is nothing to smuggle: a supplied id could be a legacy value "+
			"or one byte wrong, writing a current-looking record under the wrong key -- unmatched on "+
			"read, therefore treated as absent, therefore a SECOND DELIVERY.", foreign)
	}
	if record.TransitionID != canonical {
		t.Errorf("TransitionID = %q, want the derived %q", record.TransitionID, canonical)
	}
	// The PATH must follow the derived id too. Overwriting the id while deriving the path from the
	// smuggled one would put a correct record where no scanner looks.
	wantPath, pathErr := resumeGoalTransitionPath(project, "squad", "v2-25-0", canonical)
	if pathErr != nil {
		t.Fatalf("canonical path: %v", pathErr)
	}
	if path != wantPath {
		t.Errorf("path = %q, want %q -- the path must follow the DERIVED identity, not the supplied one",
			path, wantPath)
	}
}

// The structural pin survives as an ADDITIONAL API guard, with an honest name. It proves only that
// no DIRECT TransitionID field is added to the input -- a second, easier smuggling channel. It
// does NOT prove the nested one is closed; that is the behavioural test above. Kept because the
// two catch different edits, stated because the previous version claimed the stronger thing.
func TestRecoveryTransitionInputGainsNoDirectTransitionIDField(t *testing.T) {
	typ := reflect.TypeOf(recoveryTransitionInput{})
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if strings.Contains(strings.ToLower(name), "transitionid") {
			t.Errorf("recoveryTransitionInput.%s adds a DIRECT channel for a caller-supplied "+
				"transition id. The nested Base.TransitionID route is covered behaviourally by "+
				"TestConstructorOverwritesASmuggledTransitionID; this guards the easier one.", name)
		}
	}
	if typ.NumField() == 0 {
		t.Fatal("recoveryTransitionInput has no fields; this pin is enforcing nothing")
	}
	// Anti-vacuity with teeth: Base must still be present and must still be the record type, or
	// the nested channel this test explicitly does NOT cover has moved somewhere unexamined.
	base, ok := typ.FieldByName("Base")
	if !ok || base.Type != reflect.TypeOf(resumeGoalTransitionRecord{}) {
		t.Fatal("recoveryTransitionInput.Base is missing or retyped; the nested-id analysis above no " +
			"longer describes this struct and must be redone")
	}
}

// Kept honest: the reservation and consumption publications DO refuse on a lost race, which is the
// other half of the three-way asymmetry. Without this, "bind is the exception" is a claim about
// bind alone rather than a contrast that could be wrong.
func TestReserveAndConsumeBothRefuseALostRace(t *testing.T) {
	// THE FIXTURE NOW COMES FROM THE CONSTRUCTOR, and that change is the point rather than a tidy-up.
	//
	// It used to be a hand-built record carrying only SchemaVersion/TransitionID/NewAttemptID at a
	// hand-joined path. The publication contract correctly refuses that -- it has no kind and no scope --
	// so this test broke the moment publication started validating, and it broke for a reason that had
	// NOTHING to do with lost races. That is the fixture failure mode dev-2 keeps finding: a row whose
	// setup encodes only what its author was thinking about, which silently becomes a row about something
	// else as soon as the contract grows.
	//
	// Built from the one constructor, the fixture cannot drift out of validity without the constructor
	// itself changing, and this row stays a row about CAS semantics.
	const profile = "squad"
	const session = "v2-25-0"
	project := t.TempDir()
	record, path, err := newRecoveryTransitionRecord(recoveryTransitionInput{
		Base:          redeliverTransitionBase(project, profile, session, "attempt-abc", "binding-digest-1"),
		Kind:          recoveryTransitionKindRedeliver,
		AttemptID:     "attempt-abc",
		BindingDigest: "binding-digest-1",
	})
	if err != nil {
		t.Fatalf("a complete redeliver input must be accepted: %v", err)
	}

	if _, err := reserveRecoveryTransition(record, path); err != nil {
		t.Fatalf("first reserve must succeed: %v", err)
	}
	if _, err := reserveRecoveryTransition(record, path); err == nil {
		t.Error("a second reserve must REFUSE: another actor holds this pause's claim, and " +
			"overwriting it is the double-delivery path")
	}

	if err := consumeRecoveryTransition(record.TransitionID, record.NewAttemptID, path); err != nil {
		t.Fatalf("first consume must succeed: %v", err)
	}
	if err := consumeRecoveryTransition(record.TransitionID, record.NewAttemptID, path); err == nil {
		t.Error("a second consume must REFUSE: two consumptions of one claim is the double-delivery " +
			"signature and must be reported, not absorbed")
	}
	// Prove the consumption actually landed on disk rather than the calls merely returning nil.
	if _, err := os.Stat(resumeGoalTransitionConsumedPath(path)); err != nil {
		t.Errorf("consumption record must exist at the companion path: %v", err)
	}
}
