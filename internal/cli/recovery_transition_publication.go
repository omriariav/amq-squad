package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// #498 U7 / BLOCKER 1: THE PUBLICATION CONTRACT.
//
// This validator was ASSIGNED, listed as owed in my own checkpoint, and then reported as delivered without
// existing. Recording that here because the absence had a shape: reserveRecoveryTransition marshalled and
// published whatever record and path it was handed, so every guard the constructor enforces could be
// bypassed by any caller that built a record itself and called the publisher directly. A perfect constructor
// feeding an unvalidating publisher is the one-decider principle broken at the last step -- the decider is
// only the decider if nothing downstream accepts un-decided input.
//
// WHY VALIDATE AT PUBLICATION AND NOT ONLY AT CONSTRUCTION: they are different threats. Construction
// validates what a caller ASKS FOR; publication validates what is about to become DURABLE EVIDENCE. A record
// can be perfectly constructed and then paired with a path it does not belong at, and the filesystem will
// happily hold it there -- present on disk and invisible to the scanner that computes the canonical name.
// supervisionReservedClaimLoader reads a RESERVED claim back from disk at its canonical path.
//
// #498 U7, ruled option (d) after I reported that the delivery-time revalidation was a TAUTOLOGY at its only
// call site: the executor built the record's exact binding field-by-field from the assessment and then
// revalidated that record against the same assessment, so every comparison reduced to assessment.X ==
// assessment.X and could not fail.
//
// THE FIX IS A GENUINELY SECOND OBSERVATION: disk versus memory. The record goes out through marshal and comes
// back through unmarshal, and THAT is what gets compared to the in-memory assessment. What it really catches,
// stated precisely so the comment cannot outlive the behaviour:
//   - serialization defects in the new schema -- a field that marshals under the wrong key, or drops entirely
//     (every U7 field now has its persist->read->compare loop exercised on the live path);
//   - a write that landed incomplete, or at a path other than the one the reader derives;
//   - concurrent mutation of the record between reserve and delivery.
//
// What it does NOT do is re-observe the WORLD -- that is the U5 delivery-time gates' job (generation, pane,
// freshness), and duplicating it here under U7's name would be the two-deciders shape again.
type supervisionReservedClaimLoader func(path string) (resumeGoalTransitionRecord, error)

// readReservedRecoveryTransition is the production loader: the real file, the real unmarshal.
//
// It deliberately does NOT validate or repair what it reads. Its whole value is being a faithful mirror of
// what is on disk, because the caller's comparison is the check. A loader that "fixed up" a drifted field
// would hide exactly the defect this exists to surface.
func readReservedRecoveryTransition(path string) (resumeGoalTransitionRecord, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return resumeGoalTransitionRecord{}, fmt.Errorf("read reserved recovery transition %s: %w", path, err)
	}
	var record resumeGoalTransitionRecord
	if err := json.Unmarshal(payload, &record); err != nil {
		return resumeGoalTransitionRecord{}, fmt.Errorf("parse reserved recovery transition %s: %w", path, err)
	}
	return record, nil
}

// canonicalRecoveryTransitionPath derives the ONE full path the constructor emits for this record's
// kind and identity, from the record's OWN scope triple.
//
// It deliberately CALLS the same derivations the constructor calls -- resumeGoalTransitionPath for
// redeliver, goalAttemptDir + currentRecoveryTransitionName for native -- rather than re-implementing
// the join. Re-deriving it here would make publication a SECOND path owner, and two owners of one
// identity that can disagree is the defect whose symptom is double delivery; a validator that disagreed
// with the constructor would refuse every legitimate write instead.
//
// The kinds do NOT share a naming vocabulary, and that asymmetry is load-bearing rather than historical:
// redeliver's canonical name is the LEGACY prefix, because redelivery's existing readers already compute
// it and PR5 does not refactor that read side. Native uses the kind-carrying current prefix. So a
// redeliver record under a current-prefix name is not "also fine" -- it is a name no writer emits and no
// redelivery reader looks for.
func canonicalRecoveryTransitionPath(record resumeGoalTransitionRecord, kind recoveryTransitionKind) (string, error) {
	project := strings.TrimSpace(record.Project)
	profile := strings.TrimSpace(record.Profile)
	session := strings.TrimSpace(record.Session)
	// A record with no scope has no canonical location, so there is nothing to compare a path against.
	// Refusing is not pedantry: goalAttemptDir would happily build a RELATIVE path from blanks, and a
	// claim published relative to whatever directory the process happens to be in is unfindable by every
	// scanner including the one that wrote it.
	if project == "" || profile == "" || session == "" {
		return "", fmt.Errorf(
			"recovery transition publication refused: the record carries no exact project/profile/session triple (%q/%q/%q), so it has no canonical location to be verified against",
			record.Project, record.Profile, record.Session)
	}
	transitionID := strings.TrimSpace(record.TransitionID)
	switch kind {
	case recoveryTransitionKindRedeliver:
		// This validates the 64-hex identity shape itself, so a malformed id cannot reach a join.
		return resumeGoalTransitionPath(project, profile, session, transitionID)
	case recoveryTransitionKindNativeGoalResume:
		if !isRecoveryClaimKey(transitionID) {
			return "", fmt.Errorf(
				"recovery transition publication refused: transition id %q is not a claim-key-shaped identity, so no canonical native name derives from it",
				transitionID)
		}
		return filepath.Join(goalAttemptDir(project, profile, session), currentRecoveryTransitionName(kind, transitionID)), nil
	}
	// Unreachable through the validator, which checks knownRecoveryTransitionKind first. Stated as a
	// refusal rather than a fallthrough so a future kind that adds a constructor branch and forgets a
	// path derivation FAILS here instead of silently publishing wherever it was told to.
	return "", fmt.Errorf("recovery transition publication refused: no canonical path derivation for kind %q", kind)
}

func validateRecoveryTransitionPublication(record resumeGoalTransitionRecord, path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("recovery transition publication requires a path")
	}
	// THE ONE DURABLE-RECORD CONTRACT, not a second list of publication's own.
	//
	// This single call replaces the schema/kind/id/per-kind-shape checks this function used to own. dev-2's
	// finding was that those checks were a SUBSET of the constructor's, so publication accepted records the
	// constructor would refuse -- most sharply a NativeBinding with all ten interiors zero, which satisfied a
	// nil check and said nothing. The fix is not a longer list here; two lists of record requirements must
	// co-evolve forever and their drift is the constructor-versus-publisher disagreement itself. One decider,
	// two call sites: a field added to the contract is now required at publication automatically.
	//
	// IT ALSO RECOMPUTES THE DERIVED IDENTITY, which is what makes the path check below meaningful. Before,
	// canonicalRecoveryTransitionPath took record.TransitionID as INPUT, so comparing the path against it
	// proved only that the record agreed with itself -- an arbitrary 64-hex id passed. The contract now
	// derives the id from the fields that generate it, so by the time the path is compared, the identity is
	// established rather than assumed.
	if err := validateRecoveryTransitionRecordContract(record); err != nil {
		return fmt.Errorf("recovery transition publication refused: %w", err)
	}
	kind := recoveryTransitionKind(strings.TrimSpace(record.RecoveryKind))

	// THE FILENAME MUST BE THE ONE THE READERS COMPUTE.
	//
	// THESE THREE NAME CHECKS ARE DIAGNOSTICS, NOT THE CLOSURE, and saying so is the correction: the
	// version dev-2 gated presented them as the load-bearing check while they read only the basename.
	// The full-path equality below SUBSUMES all three -- if the path equals the constructor's output then
	// its name is canonical by construction, so nothing can fail here and pass there. They are kept
	// because they run FIRST and name the specific disagreement (wrong key, wrong kind, legacy-vs-current),
	// which a single "not the canonical path" refusal cannot tell an operator apart. They are reachable,
	// not decorative: each one fires on a real input before equality is ever evaluated.
	parsed, recognition := recognizeRecoveryTransitionName(filepath.Base(path))
	if recognition != recoveryNameRecognized {
		return fmt.Errorf(
			"recovery transition publication refused: %q is not a recognized recovery-transition filename, so the scanner that looks for this claim would never see it",
			filepath.Base(path))
	}
	if parsed.ClaimKey != strings.TrimSpace(record.TransitionID) {
		return fmt.Errorf(
			"recovery transition publication refused: filename claim key %q disagrees with the record transition id %q -- the same filename/body disagreement the scanner treats as ambiguous evidence, manufactured at write time instead of by corruption",
			parsed.ClaimKey, record.TransitionID)
	}
	// Legacy names carry no kind in the filename, so only current-derivation names are compared -- the same
	// asymmetry reservationBlocker applies on the read side.
	if !parsed.Legacy && parsed.Kind != kind {
		return fmt.Errorf(
			"recovery transition publication refused: filename kind %q disagrees with record kind %q",
			parsed.Kind, kind)
	}

	// THE FULL PATH, NOT THE BASENAME. dev-2 found this hole in the version above: every check to this
	// point reads filepath.Base, so a PERFECTLY CANONICAL FILENAME IN AN ARBITRARY DIRECTORY passed.
	//
	// This is the r6 failure class the constructor's own comment names -- "I closed the name and left the
	// container" -- and I reproduced it one file later, which is defect heredity at the level of a habit
	// rather than a copied guard: I was thinking about names because the checks I was writing were about
	// names. A namespace-A claim sitting in namespace-B's directory is invisible to BOTH scanners, so it
	// blocks nothing while existing, and absent means "no prior claim", which means a second delivery.
	//
	// EQUALITY AGAINST THE ONE CONSTRUCTOR'S OUTPUT, per kind, derived from the record's OWN validated
	// scope -- not a directory-prefix test. A prefix test would accept a claim one level deep in the right
	// tree, and it would also let publication permit kind/path combinations the constructor never emits
	// (a redeliver record under a current-prefix name parses and agrees, but no writer produces it).
	// Accepting only the writer's own vocabulary is what makes this a re-decision rather than a
	// second, laxer opinion about where claims may live.
	canonical, canonicalErr := canonicalRecoveryTransitionPath(record, kind)
	if canonicalErr != nil {
		return canonicalErr
	}
	if path != canonical {
		return fmt.Errorf(
			"recovery transition publication refused: path %q is not the canonical location %q for this %s record -- a claim written where no scanner looks exists on disk while blocking nothing, which reads as ABSENT and therefore permits a second delivery",
			path, canonical, kind)
	}

	// THE ONE CHECK THAT IS GENUINELY PUBLICATION'S OWN AND NOT THE RECORD CONTRACT'S: a native reservation
	// must not be published under a LEGACY filename. It survives here rather than moving into the shared
	// contract because it is a property of the record's LOCATION, not of the record -- the same record is
	// perfectly valid at its current-prefix path. The kind must be derivable from the name the scanner parses.
	//
	// It is also strictly subsumed by the path equality above (the canonical native path is current-prefix by
	// construction, so a legacy path cannot equal it). Kept as a named diagnostic for the same reason as the
	// three name checks: "not the canonical path" does not tell an operator that the problem is the naming
	// GENERATION rather than the directory.
	if kind == recoveryTransitionKindNativeGoalResume && parsed.Legacy {
		return fmt.Errorf("recovery transition publication refused: a native reservation must not be published under a legacy filename; the kind must be derivable from the name the scanner parses")
	}
	return nil
}
