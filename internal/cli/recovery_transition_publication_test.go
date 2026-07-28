package cli

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
)

// #498 U7 / BLOCKER 1 FALSIFIERS: the publication contract.
//
// THE VALIDATOR SHIPPED WITHOUT THESE, and that is the finding worth recording. I wrote
// validateRecoveryTransitionPublication, wired it in, and reported it as delivered -- a guard whose own
// correctness rested on nothing but my reading of it. dev-2 then found, by reading it, that every check
// examined filepath.Base and a canonical filename in an ARBITRARY DIRECTORY passed. An unverified guard is
// not a weaker version of a verified one; it is a claim, and this milestone's whole retro is about claims
// outrunning artifacts.
//
// EVERY ROW DRIVES reserveRecoveryTransition, NOT THE VALIDATOR DIRECTLY. That is deliberate and it is the
// M-U5h lesson applied before the fact: a mutation that DELETES THE VALIDATOR CALL from the publisher
// leaves the validator itself perfectly intact and perfectly unit-tested, so rows that call the validator
// directly all stay green while the production path validates nothing. Only rows that go through the
// publisher can kill that mutation. The wiring is part of the contract.

// ANTI-VACUITY FIRST, and for both kinds. Every refusal row below is worthless if the publisher refuses
// everything -- a validator that always errored would satisfy all four falsifiers and break production.
//
// This row also pins the property that matters most in practice: THE PUBLISHER ACCEPTS THE ONE
// CONSTRUCTOR'S OWN OUTPUT. Publication re-decides rather than trusting, so it necessarily holds a second
// opinion about where a claim belongs -- and if that opinion ever diverges from the constructor's, every
// legitimate write fails closed. Pinning agreement is what makes the duplication safe.
func TestPublicationAcceptsTheConstructorsOwnOutputForBothKinds(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"
	const attemptID = "attempt-abc"

	t.Run("native", func(t *testing.T) {
		project := t.TempDir()
		record, path, err := newRecoveryTransitionRecord(
			completeNativeTransitionInput(project, profile, session, attemptID))
		if err != nil {
			t.Fatalf("the complete native fixture must be accepted by the constructor: %v", err)
		}
		if _, err := reserveRecoveryTransition(record, path); err != nil {
			t.Fatalf("the publisher REFUSED the constructor's own output: %v\nThe two derive the canonical "+
				"path independently; a disagreement between them fails every legitimate native resume "+
				"closed, so this is a production outage rather than a test failure.", err)
		}
	})

	t.Run("redeliver", func(t *testing.T) {
		project := t.TempDir()
		// BUILT FROM THE COMPLETE REDELIVER BASE. The first version of this row passed a Base carrying only
		// the scope triple, and dev-2 proved statically that it could never pass: the constructor returned a
		// blank NewAttemptID and publication unconditionally refused it. The row asserted the ACCEPTED case
		// against an input the publisher rejects -- an anti-vacuity control that was itself vacuous.
		record, path, err := newRecoveryTransitionRecord(recoveryTransitionInput{
			Base:          redeliverTransitionBase(project, profile, session, attemptID, "binding-digest-1"),
			Kind:          recoveryTransitionKindRedeliver,
			AttemptID:     attemptID,
			BindingDigest: "binding-digest-1",
		})
		if err != nil {
			t.Fatalf("a complete redeliver input must be accepted by the constructor: %v", err)
		}
		if _, err := reserveRecoveryTransition(record, path); err != nil {
			t.Fatalf("the publisher REFUSED the constructor's own redeliver output: %v\nRedeliver's "+
				"canonical name is the LEGACY prefix, so a publisher that assumed the current prefix for "+
				"every kind would fail exactly here -- and redelivery is the shipped path.", err)
		}
	})
}

// dev-2's FINDING, as a falsifier: a correct basename in the wrong directory.
//
// THE FALSIFYING INPUT IS THE CONSTRUCTOR'S OWN RECORD AND ITS OWN FILENAME, relocated. Nothing about the
// record or the name is wrong; only the container is. That is what makes it the right input -- a
// hand-damaged record would be refused by some earlier shape check and would prove nothing about the
// directory. The record here is byte-for-byte one the publisher accepts at its canonical path, as the
// anti-vacuity row above proves.
//
// WHY IT MATTERS: a namespace-A claim published into namespace-B's directory is invisible to BOTH
// scanners. It exists on disk and blocks nothing. Absent means "no prior claim", and "no prior claim"
// means a SECOND DELIVERY -- the exact failure claim-once exists to prevent, reached by a write that
// passed every name-shaped check.
func TestPublicationRefusesACanonicalNameInTheWrongDirectory(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"
	const attemptID = "attempt-abc"

	project := t.TempDir()
	record, canonicalPath, err := newRecoveryTransitionRecord(
		completeNativeTransitionInput(project, profile, session, attemptID))
	if err != nil {
		t.Fatalf("the complete native fixture must be accepted: %v", err)
	}

	// Same basename, foreign directory. Using a second TempDir rather than a subdirectory of the first,
	// so this cannot be mistaken for a test about path depth: it is a different namespace container.
	foreign := filepath.Join(t.TempDir(), filepath.Base(canonicalPath))
	if foreign == canonicalPath {
		t.Fatal("fixture is void: the foreign path equals the canonical one")
	}

	_, err = reserveRecoveryTransition(record, foreign)
	if err == nil {
		t.Fatalf("publishing a CANONICALLY NAMED record into a foreign directory was ACCEPTED (%q).\n"+
			"Every check that reads only filepath.Base passes here, which is precisely why this row "+
			"exists: the claim would sit on disk where no scanner looks, read as ABSENT, and permit a "+
			"second delivery.", foreign)
	}
	// The refusal must name the CANONICAL LOCATION, not merely report a bad path. An operator holding
	// this error needs to know where the claim should have gone; "invalid path" sends them to read code.
	if !strings.Contains(err.Error(), canonicalPath) {
		t.Errorf("the refusal must name the canonical location %q so an operator can see the "+
			"disagreement; got: %v", canonicalPath, err)
	}
}

// A MINIMAL RECORD MUST NOT BECOME DURABLE EVIDENCE, even at a perfectly canonical path.
//
// This is the shape the unvalidated publisher accepted: a record carrying just enough to be filed. It is
// placed at its OWN canonical path deliberately, so the path checks all pass and the row isolates the
// per-kind shape contract. A native claim without its exact binding cannot be revalidated at delivery --
// so it can never be safely acted on, and writing it creates a claim that blocks recovery without being
// able to authorize it.
func TestPublicationRefusesAMinimalNativeRecordAtItsCanonicalPath(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"
	const attemptID = "attempt-abc"

	project := t.TempDir()
	namespaceID := squadnamespace.ID(profile, session)
	key, err := supervisionClaimKey(namespaceID, fixturePauseGeneration, attemptID)
	if err != nil {
		t.Fatalf("canonical claim key: %v", err)
	}

	minimal := resumeGoalTransitionRecord{
		SchemaVersion: resumeGoalTransitionSchemaVersion,
		Project:       project, Profile: profile, Session: session,
		RecoveryKind: string(recoveryTransitionKindNativeGoalResume),
		TransitionID: key,
	}
	path := filepath.Join(goalAttemptDir(project, profile, session),
		currentRecoveryTransitionName(recoveryTransitionKindNativeGoalResume, key))

	if _, err := reserveRecoveryTransition(minimal, path); err == nil {
		t.Fatal("a MINIMAL native record at its canonical path was published.\nIt carries no exact " +
			"binding, no supervisor and no pause generation, so delivery could never revalidate it -- " +
			"the claim would block recovery while being unable to authorize it.")
	}
}

// A BLANK KIND must refuse. It is neither a valid redeliver nor a valid native record, and the scanner's
// tri-state parser cannot classify what it finds.
//
// The record is otherwise COMPLETE and sits at a canonical native path, so the only defect is the missing
// kind -- the single-defect property. A record that was also missing other fields would refuse for
// whichever check ran first and would prove nothing about the kind requirement.
func TestPublicationRefusesABlankKindRecord(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"
	const attemptID = "attempt-abc"

	project := t.TempDir()
	record, path, err := newRecoveryTransitionRecord(
		completeNativeTransitionInput(project, profile, session, attemptID))
	if err != nil {
		t.Fatalf("the complete native fixture must be accepted: %v", err)
	}

	// The ONE defect, applied to an otherwise valid record at its own canonical path.
	record.RecoveryKind = ""

	if _, err := reserveRecoveryTransition(record, path); err == nil {
		t.Fatal("a record with NO recovery kind was published.\nOn read, a blank kind is " +
			"indistinguishable from a legacy record, so a lenient writer silently enrols new native " +
			"claims into the legacy population where native validation never runs.")
	}
}

// PUBLICATION ACCEPTS ONLY THE WRITER VOCABULARY THE CONSTRUCTOR EMITS.
//
// A redeliver record under the CURRENT kind-carrying prefix parses cleanly, carries the right kind, and
// agrees with its own claim key -- it passes every name-shaped check. No writer produces it, and no
// redelivery reader looks for it: those readers compute the LEGACY name. So this is a claim that would be
// invisible to the very population it belongs to, published under a name that looks more modern than the
// correct one.
//
// This row is what makes the contract "the one canonical path" rather than "any recognized path". Without
// it, a publisher could independently permit a second naming vocabulary per kind and every name check
// would still pass.
func TestPublicationRefusesARedeliverRecordUnderTheNativeNamingVocabulary(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"
	const attemptID = "attempt-abc"

	project := t.TempDir()
	record, canonicalPath, err := newRecoveryTransitionRecord(recoveryTransitionInput{
		Base:          redeliverTransitionBase(project, profile, session, attemptID, "binding-digest-1"),
		Kind:          recoveryTransitionKindRedeliver,
		AttemptID:     attemptID,
		BindingDigest: "binding-digest-1",
	})
	if err != nil {
		t.Fatalf("a complete redeliver input must be accepted: %v", err)
	}

	// Same directory, same claim key, same kind in the name -- only the VOCABULARY differs.
	alternate := filepath.Join(filepath.Dir(canonicalPath),
		currentRecoveryTransitionName(recoveryTransitionKindRedeliver, record.TransitionID))
	if alternate == canonicalPath {
		t.Fatal("fixture is void: the alternate name equals the canonical one, so redeliver's canonical " +
			"vocabulary is no longer the legacy prefix and this row must be rewritten")
	}

	if _, err := reserveRecoveryTransition(record, alternate); err == nil {
		t.Fatalf("a redeliver record was published under the current-prefix name %q.\nIt parses, its kind "+
			"agrees and its claim key agrees -- but redelivery's readers compute the LEGACY name, so this "+
			"claim is invisible to the population it belongs to.", filepath.Base(alternate))
	}
}

// THE SHARED CONTRACT, EXERCISED THROUGH THE PUBLISHER, one field at a time.
//
// dev-2's HOLD-2 blocker 1 was that publication owned a SUBSET of the constructor's requirements, so it
// accepted records the constructor would refuse -- most sharply a NativeBinding whose ten interiors were all
// ZERO, which satisfied a nil check and testified to nothing. These rows are the falsifiers for the shared
// contract that replaced that subset, and they run through reserveRecoveryTransition so they also pin the
// WIRING: a mutation deleting the contract call from publication must kill every row here.
//
// EACH ROW BLANKS EXACTLY ONE FIELD ON AN OTHERWISE VALID RECORD, at its own canonical path. Blanking on the
// RECORD rather than the input is deliberate: the input route is the constructor's concern, and what
// publication must refuse is a finished record handed to it directly by any caller.
func TestPublicationRefusesEveryMissingNativeContractField(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"
	const attemptID = "attempt-abc"

	for _, row := range []struct {
		name    string
		blank   func(*resumeGoalTransitionRecord)
		wantErr string
	}{
		// THE FLAT SET. Role and Handle are here because blocker 3 found them compared by revalidation and
		// required by nothing -- a blank record and a blank assessment agreed by mutual absence.
		{"lead role", func(r *resumeGoalTransitionRecord) { r.Role = "" }, "lead role"},
		{"lead handle", func(r *resumeGoalTransitionRecord) { r.Handle = "" }, "lead handle"},
		{"supervisor", func(r *resumeGoalTransitionRecord) { r.Supervisor = "" }, "supervisor"},
		{"pause generation", func(r *resumeGoalTransitionRecord) { r.PauseGeneration = "" }, "pause generation"},
		{"preclaim fingerprint", func(r *resumeGoalTransitionRecord) { r.PreclaimFingerprint = "" }, "preclaim fingerprint"},
		{"launch id", func(r *resumeGoalTransitionRecord) { r.LaunchID = "" }, "launch id"},
		{"launch record digest", func(r *resumeGoalTransitionRecord) { r.LaunchRecordDigest = "" }, "launch record digest"},
		{"launch record mod time", func(r *resumeGoalTransitionRecord) { r.LaunchRecordModTime = 0 }, "launch record mod time"},
		{"new attempt id", func(r *resumeGoalTransitionRecord) { r.NewAttemptID = "" }, "new attempt id"},
		// THE PLACEHOLDER, which is sharper than a blank: it LOOKS like an answer while attributing the
		// resume to nobody.
		{"placeholder supervisor", func(r *resumeGoalTransitionRecord) { r.Supervisor = supervisorIdentityPlaceholder },
			"placeholder"},

		// THE TEN INTERIORS -- dev-2's exact counterexample, decomposed. A pointer-presence check passes for
		// every one of these.
		{"binding namespace id", func(r *resumeGoalTransitionRecord) { r.NativeBinding.NamespaceID = "" },
			"native binding namespace id"},
		{"binding pane id", func(r *resumeGoalTransitionRecord) { r.NativeBinding.PaneID = "" },
			"native binding pane id"},
		{"binding goal mode", func(r *resumeGoalTransitionRecord) { r.NativeBinding.GoalMode = "" },
			"native binding goal mode"},
		{"binding goal attempt id", func(r *resumeGoalTransitionRecord) { r.NativeBinding.GoalAttemptID = "" },
			"native binding goal attempt id"},
		{"binding goal binding digest", func(r *resumeGoalTransitionRecord) { r.NativeBinding.GoalBindingDigest = "" },
			"native binding goal binding digest"},
		{"binding goal command digest", func(r *resumeGoalTransitionRecord) { r.NativeBinding.GoalCommandDigest = "" },
			"native binding goal command digest"},
		{"binding blocker id", func(r *resumeGoalTransitionRecord) { r.NativeBinding.BlockerID = "" },
			"native binding blocker id"},
		{"binding blocker resolution digest", func(r *resumeGoalTransitionRecord) { r.NativeBinding.BlockerResolutionDigest = "" },
			"native binding blocker resolution digest"},
		{"binding policy mode", func(r *resumeGoalTransitionRecord) { r.NativeBinding.PolicyMode = "" },
			"native binding policy mode"},
		{"binding policy revision", func(r *resumeGoalTransitionRecord) { r.NativeBinding.PolicyRevision = 0 },
			"positive native binding policy revision"},
		// THE WHOLE BLOCK, kept as its own row: the nil case and the all-zero case are different defects and
		// a check for one does not catch the other.
		{"binding block absent", func(r *resumeGoalTransitionRecord) { r.NativeBinding = nil },
			"requires its exact binding"},
		// THE DUAL'S INVARIANT: two attempt identities in one record that differ is ambiguous evidence about
		// which attempt was authorized. Set on the binding side so NewAttemptID stays nonblank and the row
		// cannot be satisfied by the blank check above.
		{"dual attempt identities disagree",
			func(r *resumeGoalTransitionRecord) { r.NativeBinding.GoalAttemptID = "attempt-somebody-elses" },
			"disagrees with the record's lifecycle attempt"},
	} {
		project := t.TempDir()
		record, path, err := newRecoveryTransitionRecord(
			completeNativeTransitionInput(project, profile, session, attemptID))
		if err != nil {
			t.Fatalf("%s: the complete native fixture must be accepted: %v", row.name, err)
		}
		row.blank(&record)
		_, err = reserveRecoveryTransition(record, path)
		if err == nil {
			t.Errorf("%s: publication accepted a record missing a required field. Publication is the last "+
				"step before the record becomes durable evidence, so anything it accepts is what recovery "+
				"has to work with.", row.name)
			continue
		}
		if !strings.Contains(err.Error(), row.wantErr) {
			t.Errorf("%s: refusal must name the field (want %q), got: %v", row.name, row.wantErr, err)
		}
	}
}

// THE DERIVED IDENTITY, for BOTH kinds. This is the row that closes the self-reference.
//
// The record here is COMPLETELY VALID in shape, and its path is the canonical path FOR THE ID IT CARRIES --
// so the filename agrees with the body, the body agrees with the filename, and the full-path equality check
// passes. Only recomputation catches it: the id is not the one that the record's own source fields generate,
// so no scanner will ever compute it, and the claim is present on disk while reading as ABSENT for the real
// pause. That is the double delivery, reached through a record that agrees with itself in every direction.
//
// My previous round's path-equality check took record.TransitionID as INPUT, which is why it could not see
// this: an invariant that derives its expectation from the thing it is checking binds nothing. Same defect
// class as U10's runtime-resolved baseline_head.
func TestPublicationRefusesARecordWhoseDerivedIdentityIsWrong(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"
	const attemptID = "attempt-abc"

	t.Run("native", func(t *testing.T) {
		project := t.TempDir()
		record, _, err := newRecoveryTransitionRecord(
			completeNativeTransitionInput(project, profile, session, attemptID))
		if err != nil {
			t.Fatalf("the complete native fixture must be accepted: %v", err)
		}
		// A WELL-FORMED CLAIM KEY FOR A DIFFERENT PAUSE -- not garbage. Garbage would be caught by any shape
		// check and would prove only that malformed input fails; this is exactly as valid as the right answer
		// and differs only in being wrong.
		foreign, keyErr := supervisionClaimKey(
			squadnamespace.ID(profile, session), "a-different-pause-generation", attemptID)
		if keyErr != nil {
			t.Fatalf("foreign claim key: %v", keyErr)
		}
		if foreign == record.TransitionID {
			t.Fatal("fixture is void: the foreign claim key equals the canonical one")
		}
		record.TransitionID = foreign
		// The path is rebuilt FOR THE FOREIGN ID, so body, filename and full path are mutually consistent.
		foreignPath := filepath.Join(goalAttemptDir(project, profile, session),
			currentRecoveryTransitionName(recoveryTransitionKindNativeGoalResume, foreign))

		_, err = reserveRecoveryTransition(record, foreignPath)
		if err == nil {
			t.Fatal("publication accepted a native record whose transition id is not the claim key derived " +
				"from its own namespace/pause/attempt. Body and filename agree, the path is canonical for " +
				"that id, and no pause scanner will ever compute it.")
		}
		if !strings.Contains(err.Error(), "is not the claim key") {
			t.Errorf("the refusal must name the derived-identity disagreement, got: %v", err)
		}
	})

	t.Run("redeliver", func(t *testing.T) {
		project := t.TempDir()
		record, _, err := newRecoveryTransitionRecord(recoveryTransitionInput{
			Base:          redeliverTransitionBase(project, profile, session, attemptID, "binding-digest-1"),
			Kind:          recoveryTransitionKindRedeliver,
			AttemptID:     attemptID,
			BindingDigest: "binding-digest-1",
		})
		if err != nil {
			t.Fatalf("a complete redeliver input must be accepted: %v", err)
		}
		// The id redelivery's readers would compute for a DIFFERENT attempt.
		foreign := resumeGoalTransitionID("attempt-somebody-elses", "binding-digest-1")
		if foreign == record.TransitionID {
			t.Fatal("fixture is void: the foreign id equals the canonical one")
		}
		record.TransitionID = foreign
		foreignPath, pathErr := resumeGoalTransitionPath(project, profile, session, foreign)
		if pathErr != nil {
			t.Fatalf("foreign redeliver path: %v", pathErr)
		}

		_, err = reserveRecoveryTransition(record, foreignPath)
		if err == nil {
			t.Fatal("publication accepted a redeliver record whose transition id is not derived from its " +
				"own original attempt and binding digest -- invisible to the readers that compute it.")
		}
		if !strings.Contains(err.Error(), "is not the id") {
			t.Errorf("the refusal must name the derived-identity disagreement, got: %v", err)
		}
	})
}

// THE REDELIVER CONTRACT'S REQUIRED FIELDS AND REQUIRED ABSENCES.
//
// Blocker 2 was that this contract had never been DEFINED: the constructor accepted a scope-triple-only Base
// and returned a blank NewAttemptID that publication then unconditionally refused. Both halves are pinned
// here -- the fields it must carry, and the fields whose PRESENCE means someone recomputed or invented a
// value this path cannot honestly hold.
func TestPublicationRefusesAnIncompleteRedeliverRecord(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"
	const attemptID = "attempt-abc"

	for _, row := range []struct {
		name    string
		break1  func(*resumeGoalTransitionRecord)
		wantErr string
	}{
		{"missing new attempt id", func(r *resumeGoalTransitionRecord) { r.NewAttemptID = "" }, "new attempt id"},
		{"missing original attempt id", func(r *resumeGoalTransitionRecord) { r.OriginalAttemptID = "" },
			"original attempt id"},
		{"missing original binding digest", func(r *resumeGoalTransitionRecord) { r.OriginalBindingDigest = "" },
			"original binding digest"},
		// THE INEQUALITY. Both peer-owned readers already refuse a transition that reuses the original
		// attempt, so accepting it here would make durable a record nothing can consume. Set on the
		// NewAttemptID side so the identity recomputation (which keys on OriginalAttemptID and
		// OriginalBindingDigest) is untouched and this row can only fail on the inequality.
		{"new attempt reuses the original", func(r *resumeGoalTransitionRecord) { r.NewAttemptID = r.OriginalAttemptID },
			"must differ from the original attempt id"},
		// THE REQUIRED ABSENCES, compared RAW: omitempty does not omit a whitespace-only string, so a trimmed
		// guard would let a present-but-meaningless value serialise.
		{"carries a pause generation", func(r *resumeGoalTransitionRecord) { r.PauseGeneration = "pause-gen-1" },
			"must not carry a pause generation"},
		{"carries a supervisor", func(r *resumeGoalTransitionRecord) { r.Supervisor = "cto" },
			"must not carry a supervisor"},
		{"carries a whitespace-only supervisor", func(r *resumeGoalTransitionRecord) { r.Supervisor = "   " },
			"must not carry a supervisor"},
	} {
		project := t.TempDir()
		record, path, err := newRecoveryTransitionRecord(recoveryTransitionInput{
			Base:          redeliverTransitionBase(project, profile, session, attemptID, "binding-digest-1"),
			Kind:          recoveryTransitionKindRedeliver,
			AttemptID:     attemptID,
			BindingDigest: "binding-digest-1",
		})
		if err != nil {
			t.Fatalf("%s: a complete redeliver input must be accepted: %v", row.name, err)
		}
		row.break1(&record)
		_, err = reserveRecoveryTransition(record, path)
		if err == nil {
			t.Errorf("%s: publication accepted an invalid redeliver record", row.name)
			continue
		}
		if !strings.Contains(err.Error(), row.wantErr) {
			t.Errorf("%s: refusal must name the cause (want %q), got: %v", row.name, row.wantErr, err)
		}
	}
}

// NON-CANONICAL SCOPE IS REFUSED AT CONSTRUCTION, so no constructor-accepted pair can be publisher-refused.
//
// THE PROPERTY THIS PROTECTS: every record/path pair the constructor returns must be accepted by the
// publisher. dev-2 broke it statically with one character -- a Project of "<tmpdir> " passed the nonblank
// check, the constructor derived its path from the RAW value, and canonicalRecoveryTransitionPath re-derived
// from the TRIMMED one, so publication refused the constructor's own output. goalAttemptDir trims session and
// normalizes profile but joins project verbatim, which is why project was the live vector.
//
// The fix is at the DATA (the record must already hold the canonical value) rather than aligning two
// consumers of an ambiguous one, so this row asserts REFUSAL AT CONSTRUCTION -- the drifting pair cannot be
// built at all. The second half proves the canonical form still works end to end, or this would be a test
// that "fixed" the edge by rejecting the whole feature.
func TestNonCanonicalScopeIsRefusedAtConstructionSoPathsCannotDrift(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"
	const attemptID = "attempt-abc"

	t.Run("trailing space in project refuses", func(t *testing.T) {
		project := t.TempDir()
		in := completeNativeTransitionInput(project+" ", profile, session, attemptID)
		_, _, err := newRecoveryTransitionRecord(in)
		if err == nil {
			t.Fatal("a Project carrying a trailing space was ACCEPTED. It is nonblank, so every TrimSpace " +
				"check passes -- and the constructor derives its path from the raw value while publication " +
				"derives from the trimmed one, so the record and its own publisher disagree about where the " +
				"claim belongs.")
		}
		if !strings.Contains(err.Error(), "canonical form") {
			t.Errorf("the refusal must name the canonicalization requirement, got: %v", err)
		}
	})

	// ANTI-VACUITY: the same input with a canonical project must construct AND publish. Without this, the row
	// above would be satisfied by a contract that refused every project.
	t.Run("canonical project constructs and publishes", func(t *testing.T) {
		project := t.TempDir()
		record, path, err := newRecoveryTransitionRecord(
			completeNativeTransitionInput(project, profile, session, attemptID))
		if err != nil {
			t.Fatalf("the canonical form must be accepted at construction: %v", err)
		}
		if _, err := reserveRecoveryTransition(record, path); err != nil {
			t.Fatalf("the publisher must accept the constructor's own output: %v", err)
		}
	})
}

// GAP A: a correct basename in a SUBDIRECTORY of its own canonical directory must refuse.
//
// I found this gap by designing the mutation set rather than by review: a weakening of `path != canonical` to
// `!strings.HasPrefix(path, goalAttemptDir(...))` SURVIVES every other row here, because my wrong-directory row
// places the file in a separate TempDir, which fails a prefix test too. So a prefix relaxation would look
// tested while actually accepting a claim nested one level deeper inside the right namespace — invisible to the
// scanner for exactly the same reason a foreign directory is.
//
// This row is the one that kills that relaxation: the path is INSIDE the canonical directory, so a prefix check
// passes it and only full equality refuses it.
func TestPublicationRefusesACanonicalNameInASubdirectoryOfItsOwnDirectory(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"
	const attemptID = "attempt-abc"

	project := t.TempDir()
	record, canonicalPath, err := newRecoveryTransitionRecord(
		completeNativeTransitionInput(project, profile, session, attemptID))
	if err != nil {
		t.Fatalf("the complete native fixture must be accepted: %v", err)
	}

	nested := filepath.Join(filepath.Dir(canonicalPath), "nested", filepath.Base(canonicalPath))
	if !strings.HasPrefix(nested, filepath.Dir(canonicalPath)) {
		t.Fatal("fixture is void: the nested path is not inside the canonical directory, so it would also " +
			"fail a prefix check and this row would not distinguish equality from prefix")
	}

	if _, err := reserveRecoveryTransition(record, nested); err == nil {
		t.Fatalf("publication accepted a canonically-named record in %q, a SUBDIRECTORY of its own canonical "+
			"directory. A directory-prefix check passes this; only exact path equality refuses it, and the "+
			"scanner reads the canonical directory only -- so this claim would exist and block nothing.", nested)
	}
}

// GAP B: the executor must REFUSE, with ZERO pane input, when the PERSISTED claim disagrees with the assessment.
//
// THIS ROW IS ONLY HONEST BECAUSE OF THE RULED READ-BACK. Before it, the executor revalidated the record it had
// just built from the assessment against that same assessment, so the comparison was a tautology and no input
// could make it fail. Writing this test then would have required contriving a disagreement production cannot
// produce — the unreachable-state fixture class. With the read-back, the loader is the seam, and a drifted
// persisted record is a state the world genuinely reaches (a bad marshal, a partial write, a concurrent edit).
//
// It also kills the call-site-deletion mutation legitimately: deleting the revalidation call from the executor
// makes this row deliver, and the zero-input assertion catches it.
func TestExecutorRefusesADriftedPersistedClaimWithoutDelivering(t *testing.T) {
	assessment, action, payload, _, now := u6DeliveringFixture(t)

	u6StubSeams(t,
		func(GoalSupervisionAssessment, string, string) error { return nil },
		func(GoalSupervisionAssessment) error { return nil },
	)
	loadPayload := func(GoalSupervisionAssessment) (string, string, error) {
		return payload, digestGoalSupervisionString(payload), nil
	}

	delivered := 0
	// THE ONE DEFECT: the persisted claim comes back with a single drifted field. Read the real file first and
	// mutate ONE field of it, rather than fabricating a record — fabricating would risk failing for a missing
	// field instead of for the drift, which is the single-defect rule applied to the seam itself.
	drifting := func(path string) (resumeGoalTransitionRecord, error) {
		record, err := readReservedRecoveryTransition(path)
		if err != nil {
			return resumeGoalTransitionRecord{}, err
		}
		if record.NativeBinding == nil {
			t.Fatal("the persisted native claim carries no exact binding, so this row cannot drift one field of it")
		}
		record.NativeBinding.PaneID = "%999"
		return record, nil
	}

	outcome, err := executeSupervisionResume(assessment, action, "supervisor-1",
		func() time.Time { return now }, loadPayload,
		passingGenerationReader(assessment), passingPaneReader(assessment),
		drifting,
		func(string) error { delivered++; return nil })

	if err == nil {
		t.Fatal("the executor DELIVERED against a persisted claim that disagrees with the assessment. The " +
			"claim on disk is the recovery contract; if it does not describe the assessment being acted on, " +
			"the delivery is authorized by something other than what was reserved.")
	}
	if delivered != 0 || outcome.Delivered {
		t.Errorf("delivered=%d Delivered=%t; a claim/assessment disagreement must refuse BEFORE pane input -- "+
			"a gate that refuses after delivering has already caused the harm it exists to prevent",
			delivered, outcome.Delivered)
	}
	if !strings.Contains(err.Error(), "pane id") {
		t.Errorf("the refusal must name the disagreeing field, got: %v", err)
	}
}

// dev-2's TWO COUNTEREXAMPLES to the first read-back, as executor-driven rows.
//
// The PaneID row above kills deletion of the revalidation call, but it did NOT kill either of these, and the
// reason is worth stating: revalidateNativeBindingAgainstAssessment deliberately excludes TransitionID because
// "publication already proves name+path agree" -- true at its old call site, FALSE once I added a read-back
// after publication. I moved a validator across a boundary without re-deriving what its exclusions rest on.
//
// Both rows read the REAL persisted record and change exactly ONE field, so neither can pass or fail for a
// missing field instead of the drift it names.
func TestExecutorRefusesAPersistedClaimWithADriftedDurableIdentity(t *testing.T) {
	for _, row := range []struct {
		name    string
		drift   func(*resumeGoalTransitionRecord)
		wantErr string
	}{
		{
			// A well-shaped 64-hex id that is simply not this claim's. The body no longer matches its own
			// filename or its derived claim key, so no scanner would ever find it -- the absent-means-no-claim
			// path to a second delivery, reached through a record that passed every binding comparison.
			name:    "transition id",
			drift:   func(r *resumeGoalTransitionRecord) { r.TransitionID = strings.Repeat("d", 64) },
			wantErr: "durable-record contract",
		},
		{
			// A DIFFERENT NONBLANK supervisor. The validator checks presence only, so this passed and delivery
			// would proceed under a claim attributing authorization to someone else. A wrong attribution is
			// worse than a missing one because it looks like an answer.
			name:    "supervisor identity",
			drift:   func(r *resumeGoalTransitionRecord) { r.Supervisor = "somebody-else" },
			wantErr: "authorized by",
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			assessment, action, payload, _, now := u6DeliveringFixture(t)
			u6StubSeams(t,
				func(GoalSupervisionAssessment, string, string) error { return nil },
				func(GoalSupervisionAssessment) error { return nil },
			)
			loadPayload := func(GoalSupervisionAssessment) (string, string, error) {
				return payload, digestGoalSupervisionString(payload), nil
			}

			delivered := 0
			drifting := func(path string) (resumeGoalTransitionRecord, error) {
				record, err := readReservedRecoveryTransition(path)
				if err != nil {
					return resumeGoalTransitionRecord{}, err
				}
				row.drift(&record)
				return record, nil
			}

			outcome, err := executeSupervisionResume(assessment, action, "supervisor-1",
				func() time.Time { return now }, loadPayload,
				passingGenerationReader(assessment), passingPaneReader(assessment),
				drifting,
				func(string) error { delivered++; return nil })

			if err == nil {
				t.Fatalf("the executor DELIVERED against a persisted claim whose %s had drifted. The claim on "+
					"disk is the durable evidence; if its identity or its recorded authorizer is not what was "+
					"reserved, the delivery rests on something other than the reservation.", row.name)
			}
			if delivered != 0 || outcome.Delivered {
				t.Errorf("%s: delivered=%d Delivered=%t; this must refuse BEFORE pane input",
					row.name, delivered, outcome.Delivered)
			}
			if !strings.Contains(err.Error(), row.wantErr) {
				t.Errorf("%s: refusal must name the cause (want %q), got: %v", row.name, row.wantErr, err)
			}
		})
	}
}

// AND THE NIL-LOADER POSTURE: a missing read-back loader REFUSES rather than skipping.
//
// The M-U5g rule, applied to the new seam. Without this row, deleting the nil check turns a missing check into
// a passing one, and every test above stays green because they all supply a loader.
func TestExecutorRefusesANilReservedClaimLoaderRatherThanSkipping(t *testing.T) {
	assessment, action, payload, _, now := u6DeliveringFixture(t)
	u6StubSeams(t,
		func(GoalSupervisionAssessment, string, string) error { return nil },
		func(GoalSupervisionAssessment) error { return nil },
	)
	loadPayload := func(GoalSupervisionAssessment) (string, string, error) {
		return payload, digestGoalSupervisionString(payload), nil
	}

	delivered := 0
	_, err := executeSupervisionResume(assessment, action, "supervisor-1",
		func() time.Time { return now }, loadPayload,
		passingGenerationReader(assessment), passingPaneReader(assessment),
		nil,
		func(string) error { delivered++; return nil })

	if err == nil {
		t.Fatal("a nil reserved-claim loader was treated as a SKIP. A missing verification that becomes a " +
			"passing verification is the authorization-seam defect: production would deliver having proven " +
			"nothing about what it persisted.")
	}
	if delivered != 0 {
		t.Errorf("delivered %d time(s) with no loader supplied", delivered)
	}
}

// A STALE SCHEMA VERSION must refuse at publication, even at a perfectly canonical path.
//
// The record is otherwise complete and correctly located; only the version is wrong. Its own readers
// validate nonzero-and-current (resume_goal.go:351, :403, :1131, :1210), so a record written under a
// version they reject is undiscoverable evidence: present on disk, unreadable by the code that looks for
// it, and therefore ABSENT -- which permits a second delivery.
//
// This row exists because the audit table would otherwise read NONE for this cell. The check was in the
// validator from the start and nothing proved it fired.
func TestPublicationRefusesAStaleSchemaVersion(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"
	const attemptID = "attempt-abc"

	project := t.TempDir()
	record, path, err := newRecoveryTransitionRecord(
		completeNativeTransitionInput(project, profile, session, attemptID))
	if err != nil {
		t.Fatalf("the complete native fixture must be accepted: %v", err)
	}

	// The ONE defect. Zero specifically, because that is what a record written by a caller that never set
	// the field carries -- the reachable failure rather than an invented one.
	record.SchemaVersion = 0

	if _, err := reserveRecoveryTransition(record, path); err == nil {
		t.Fatal("a record with schema version 0 was published at its canonical path.\nIts own readers " +
			"validate nonzero, so they would reject it -- the claim would exist while reading as absent.")
	}
}

// A RECORD WITH NO SCOPE has no canonical location, so there is nothing to verify a path against.
//
// This is the fixture the old lost-race test used, and it is worth pinning as a refusal rather than
// merely fixing that test: goalAttemptDir builds a RELATIVE path from blanks, so a claim published from
// such a record lands relative to whatever directory the process happens to be in -- unfindable by every
// scanner including the one that wrote it.
func TestPublicationRefusesARecordWithNoScopeTriple(t *testing.T) {
	dir := t.TempDir()
	record := resumeGoalTransitionRecord{
		SchemaVersion: resumeGoalTransitionSchemaVersion,
		RecoveryKind:  string(recoveryTransitionKindRedeliver),
		TransitionID:  strings.Repeat("c", 64),
		NewAttemptID:  "attempt-new",
	}
	path := filepath.Join(dir, legacyRecoveryTransitionPrefix+record.TransitionID+".json")

	if _, err := reserveRecoveryTransition(record, path); err == nil {
		t.Fatal("a record with NO project/profile/session was published.\nIt has no canonical location, " +
			"so no path can be verified against it and the claim cannot be found by the scanner that " +
			"would look for it.")
	}
}
