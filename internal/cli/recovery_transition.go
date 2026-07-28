package cli

// PR5 / #498 ledger extension. Additive to resume_goal.go's existing recovery-transition
// machinery: ONE store, ONE CAS owner, ONE parser. goal_attempt.go stays read-only and there is
// no second claim store and no adapter, per the ruling that a parallel store is the
// two-deciders defect at the exact spot whose failure mode is double delivery of an audited
// /goal resume.
//
// READ THIS FIRST IF THE PREFIXES LOOK REDUNDANT. There are two derivations on purpose:
//   - LEGACY: ".resume-redelivery-<sha256(attemptID \x00 bindingDigest)>.json", the identity
//     resume_goal.go has always written. It stays READABLE for recovery and loses its writer
//     path; a hit at that derivation is an AUTHORITATIVE existing claim.
//   - CURRENT: ".recovery-<kind>-<sha256(namespaceID \x00 pauseGeneration \x00 attemptID)>.json",
//     the ruled stable key. PauseGeneration is CONSUMED from the assessment, never recomputed
//     here -- PR4 owns that derivation and a second owner reading a different snapshot is the
//     failure this milestone kept producing.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
)

// recoveryTransitionKind distinguishes what a reservation is FOR. It lives in BOTH the filename
// prefix and the record body, and the parser requires them to agree.
//
// Why both: the prefix makes the kind-agnostic pause scan cheap and the directory human-
// readable, while the body makes it durable evidence rather than a naming convention. Requiring
// AGREEMENT is what stops a renamed file from silently changing what a reservation means --
// disagreement is ambiguous evidence, and ambiguous evidence refuses.
type recoveryTransitionKind string

const (
	recoveryTransitionKindRedeliver        recoveryTransitionKind = "redeliver"
	recoveryTransitionKindNativeGoalResume recoveryTransitionKind = "native-goal-resume"
)

// legacyRecoveryTransitionPrefix is the pre-PR5 filename prefix. Readable, never written.
const legacyRecoveryTransitionPrefix = ".resume-redelivery-"

// currentRecoveryTransitionPrefix is the kind-carrying prefix for the ruled stable key.
const currentRecoveryTransitionPrefix = ".recovery-"

// supervisionClaimKey builds the ruled durable identity: NamespaceID + PauseGeneration +
// AttemptID.
//
// Every component is CONSUMED, none derived here. PauseGeneration in particular arrives from
// GoalSupervisionAssessment.Binding.PauseGeneration, which PR4 computes from captured LaunchID +
// Goal.BindingDigest + Goal.AttemptID + Goal.Mode. Recomputing it in this file would make PR5 a
// second derivation owner, and the defect that produces is not a field-sensitivity issue -- it
// is one identity with two owners that can disagree.
//
// PreclaimFingerprint is deliberately NOT part of this key. It rotates when claim evidence
// changes, and writing the claim IS a change to claim evidence, so a fingerprint-keyed claim
// would rotate its own identity at the moment of being recorded and become unmatchable. It is
// stored as staleness evidence instead.
func supervisionClaimKey(namespaceID, pauseGeneration, attemptID string) (string, error) {
	namespaceID = strings.TrimSpace(namespaceID)
	pauseGeneration = strings.TrimSpace(pauseGeneration)
	attemptID = strings.TrimSpace(attemptID)
	// Each component is required. A blank one would silently collapse distinct pauses onto one
	// key, which is the double-delivery failure wearing a hash.
	if namespaceID == "" || pauseGeneration == "" || attemptID == "" {
		return "", fmt.Errorf("recovery claim key requires namespace, pause generation and attempt id (got %q/%q/%q)",
			namespaceID, pauseGeneration, attemptID)
	}
	sum := sha256.Sum256([]byte(namespaceID + "\x00" + pauseGeneration + "\x00" + attemptID))
	return hex.EncodeToString(sum[:]), nil
}

// currentRecoveryTransitionName renders the kind-carrying filename for a claim key.
func currentRecoveryTransitionName(kind recoveryTransitionKind, claimKey string) string {
	return currentRecoveryTransitionPrefix + string(kind) + "-" + claimKey + ".json"
}

// parsedRecoveryTransitionName is what the ONE parser returns.
type parsedRecoveryTransitionName struct {
	Kind     recoveryTransitionKind
	ClaimKey string
	Legacy   bool
}

// parseRecoveryTransitionName is the SINGLE parser for both derivations.
//
// It rejects rather than guesses. Unknown, missing, malformed or mismatched kind refuses,
// because a reservation whose purpose cannot be determined is exactly the ambiguous evidence
// that must not be treated as absent. The caller then compares Kind against the record body's
// kind; this function cannot do that comparison itself because it only sees the name.
func parseRecoveryTransitionName(name string) (parsedRecoveryTransitionName, bool) {
	switch {
	case strings.HasSuffix(name, ".consumed.json"), strings.HasSuffix(name, ".bound.json"):
		// Companion artifacts, not reservations. Named explicitly so a future companion
		// suffix cannot be silently misread as a reservation.
		return parsedRecoveryTransitionName{}, false
	case !strings.HasSuffix(name, ".json"):
		return parsedRecoveryTransitionName{}, false
	}

	if rest, ok := trimPrefixExact(name, legacyRecoveryTransitionPrefix); ok {
		key := strings.TrimSuffix(rest, ".json")
		if !isRecoveryClaimKey(key) {
			return parsedRecoveryTransitionName{}, false
		}
		// Legacy names carry NO kind. They are redelivery reservations by construction, which
		// is recorded here rather than inferred at each call site.
		return parsedRecoveryTransitionName{Kind: recoveryTransitionKindRedeliver, ClaimKey: key, Legacy: true}, true
	}

	rest, ok := trimPrefixExact(name, currentRecoveryTransitionPrefix)
	if !ok {
		return parsedRecoveryTransitionName{}, false
	}
	body := strings.TrimSuffix(rest, ".json")
	// Split from the RIGHT: the claim key is a fixed-length hex tail, and kinds contain
	// hyphens ("native-goal-resume"), so splitting from the left would mangle them.
	cut := strings.LastIndex(body, "-")
	if cut <= 0 {
		return parsedRecoveryTransitionName{}, false
	}
	kind := recoveryTransitionKind(body[:cut])
	key := body[cut+1:]
	if !isRecoveryClaimKey(key) || !knownRecoveryTransitionKind(kind) {
		return parsedRecoveryTransitionName{}, false
	}
	return parsedRecoveryTransitionName{Kind: kind, ClaimKey: key}, true
}

// knownRecoveryTransitionKind refuses unknown kinds rather than passing them through.
//
// An unrecognised kind is not a new kind to be tolerated: it means this binary does not
// understand a reservation that exists, and proceeding would ignore a claim it cannot read.
func knownRecoveryTransitionKind(kind recoveryTransitionKind) bool {
	switch kind {
	case recoveryTransitionKindRedeliver, recoveryTransitionKindNativeGoalResume:
		return true
	default:
		return false
	}
}

// isRecoveryClaimKey validates the sha256 hex shape both derivations use.
func isRecoveryClaimKey(key string) bool {
	if len(key) != 64 {
		return false
	}
	_, err := hex.DecodeString(key)
	return err == nil
}

// trimPrefixExact is strings.TrimPrefix plus a report of whether it applied, so a caller cannot
// mistake "no prefix" for "empty remainder".
func trimPrefixExact(s, prefix string) (string, bool) {
	if !strings.HasPrefix(s, prefix) {
		return "", false
	}
	return strings.TrimPrefix(s, prefix), true
}

// recoveryNameRecognition is the TRI-STATE the scan needs, per dev-2's binding correction.
//
// A boolean cannot express this directory's reality. goalAttemptDir holds ordinary
// <attempt>.json and <attempt>.claim.json files alongside recovery transitions, so:
//   - NotATransition: definitely not ours. IGNORED. Blocking here would let normal attempt files
//     wedge all recovery permanently, which is amendment 1 overcorrected into an outage.
//   - Recognized: parses completely. INSPECTED.
//   - Malformed: TRANSITION-LIKE -- it carries a recovery prefix -- but its structure, kind or key
//     cannot be read. BLOCKS, and deliberately WITHOUT requiring a key match: if corruption
//     destroyed the key, demanding the key be present proves nothing about ownership while
//     quietly permitting delivery. That was my fail-open, fast-rejected.
type recoveryNameRecognition int

const (
	recoveryNameNotATransition recoveryNameRecognition = iota
	recoveryNameRecognized
	recoveryNameMalformed
)

// recognizeRecoveryTransitionName is the one recognition point for both derivations.
//
// It distinguishes "not a transition" from "a transition I cannot read" by PREFIX first: only a
// name carrying a recovery prefix can be malformed-transition-like. Everything else is somebody
// else's file and is ignored.
func recognizeRecoveryTransitionName(name string) (parsedRecoveryTransitionName, recoveryNameRecognition) {
	switch {
	case strings.HasSuffix(name, ".consumed.json"), strings.HasSuffix(name, ".bound.json"):
		// Companions are handled by their own path; not reservations.
		return parsedRecoveryTransitionName{}, recoveryNameNotATransition
	case !strings.HasPrefix(name, legacyRecoveryTransitionPrefix) && !strings.HasPrefix(name, currentRecoveryTransitionPrefix):
		// No recovery prefix: an ordinary attempt/claim file or an unrelated one.
		return parsedRecoveryTransitionName{}, recoveryNameNotATransition
	case !strings.HasSuffix(name, ".json"):
		// Prefixed but not a JSON artifact: transition-like and unreadable.
		return parsedRecoveryTransitionName{}, recoveryNameMalformed
	}
	parsed, ok := parseRecoveryTransitionName(name)
	if !ok {
		return parsedRecoveryTransitionName{}, recoveryNameMalformed
	}
	return parsed, recoveryNameRecognized
}

// newRecoveryTransitionRecord is the SINGLE constructor AND the single path deriver for every
// recovery-transition reservation. It returns the record and the filename that must hold it, so
// no caller can pair a validated record with a PATH of its own choosing.
//
// It returns the CANONICAL FULL PATH, not a basename (dev-2's correction). A basename leaves the
// DIRECTORY to the caller, so a correct hash for namespace A could be written into namespace B's
// directory and each scanner would miss it -- the claim would exist and be invisible to both.
// That is the same gap as r6's session layer: I closed the name and left the container. There is
// deliberately no filename-only API, so there is nothing to redirect.
//
// dev-2's original correction was about the id:
// otherwise the redelivery site could keep passing its own id and write a current-looking record
// under the legacy key.
//
// PER-KIND VALIDATION, and the reason is POSSESSION rather than preference. A future kind should
// copy the DISPATCH, not one of its branches:
//
//	redeliver           has no assessment in hand, so it has NO PauseGeneration. Requiring one
//	                    would force either local recomputation (forbidden -- PR4 owns that
//	                    derivation) or a faked value, which is worse than the literal this
//	                    constructor replaces. It requires what that path genuinely holds:
//	                    AttemptID and BindingDigest.
//	native-goal-resume  consumes a full assessment and requires the RATIFIED U7 SET:
//	                    NamespaceID, assessment-captured PauseGeneration, AttemptID,
//	                    PreclaimFingerprint, an explicit Supervisor, a persisted SchemaVersion, a
//	                    derived NewAttemptID, and the exact-binding block (PaneID, Goal
//	                    Mode/AttemptID/BindingDigest/CommandDigest, Blocker ID/ResolutionDigest,
//	                    Policy Mode/Revision). None recomputed; all consumed from the one
//	                    observation that authorized the claim.
//
// THE RECORD IS THE RECOVERY CONTRACT. It must be sufficient to REVALIDATE THE EXACT AUTHORIZED
// OBSERVATION, not merely to derive a filename -- recovery cannot revalidate a field the record never
// persisted, which is why the set is exhaustive rather than convenient.
//
// THIS COMMENT WAS ITSELF A DEFECT TWICE, in opposite directions, which is why it now states the rule
// and the enforcement point together. It first claimed the whole ruled set was required when only four
// fields were; it was corrected to list what was missing; then Supervisor became required in code while
// this text still listed it as missing. A comment that tracks intent rather than behaviour is wrong at
// every point where the two differ, and it drifts in BOTH directions -- overclaiming, then underclaiming
// after the code caught up.
//
// BOTH DIRECTIONS ARE ENFORCED. A redeliver write carrying a PauseGeneration is REFUSED too: it
// cannot honestly have one, so its presence means someone recomputed or invented it, which is the
// failure the dispatch exists to prevent. A contract that only checks for missing fields lets the
// other half through.
//
// READ vs WRITE asymmetry (ruled): on READ, absent PR5 fields mean legacy/redeliver, identified
// by the legacy key and blocking per the consumed-blocks ruling. On WRITE, absence of a required
// field refuses -- a defaulted identity field is indistinguishable on disk from a legacy record,
// so a lenient writer silently enrols new records into the legacy population.
// THE CONSTRUCTOR'S POSTURE TOWARD SIDE-DOOR (Base-carried) VALUES, stated once because THREE separate
// defects of this exact shape were found in it in one day: a smuggled NewAttemptID (U7b), a smuggled
// Supervisor, and a smuggled PauseGeneration. Every one checked the explicit INPUT and trusted
// `record := in.Base`, which copies whatever a caller cared to set.
//
// TWO POSTURES, and the distinction is what makes the exception principled rather than an oversight:
//
//  1. A field DERIVED FROM A VALIDATED INPUT (PauseGeneration, PreclaimFingerprint, NewAttemptID,
//     Supervisor) REFUSES when a Base value DISAGREES. The overwrite would be safe -- the correct value
//     wins -- which is exactly why it must not be silent: a caller that set Base differently believes
//     something false about the record it is creating, and silent correction defers that discovery
//     somewhere less observable. An AGREEING Base value is ACCEPTED, so the guard cannot degenerate into
//     "refuse any Base value" and reject a caller who populated Base consistently.
//
//  2. TransitionID is the ONE EXCEPTION and stays OVERWRITE, because no legitimate caller-supplied value
//     exists for it: it is fully derived per kind, so a Base value is meaningless rather than
//     contradictory. That posture is already pinned by TestConstructorOverwritesASmuggledTransitionID,
//     which proves a smuggled id cannot survive. Refusing there would add a failure mode with nothing to
//     protect.
//
// A field a caller MUST NOT SUPPLY AT ALL for this kind (redeliver's PauseGeneration and Supervisor)
// refuses ANY nonempty value on EITHER route, compared RAW: `omitempty` does not omit a whitespace-only
// string, so a trimmed guard would persist a present-but-meaningless value.
//
// Redeliver's PreclaimFingerprint is deliberately NOT guarded -- it is optional evidence on that path
// rather than a derived identity, so a caller-supplied value is legitimate there.
func newRecoveryTransitionRecord(in recoveryTransitionInput) (resumeGoalTransitionRecord, string, error) {
	kind := recoveryTransitionKind(strings.TrimSpace(string(in.Kind)))
	// SCHEMA VERSION FOR EVERY KIND. Native reservations previously persisted ZERO -- nothing on that path
	// set it -- while four readers validate nonzero (resume_goal.go:351, :403, :1131, :1210). Dormant only
	// because today's readers are redelivery-path: any reader that starts validating a native reservation
	// would reject every one as malformed. ZERO from Base is ABSENT, not disagreement, because that is what
	// every legacy caller passes; a disagreeing NONZERO value refuses.
	if v := in.Base.SchemaVersion; v != 0 && v != resumeGoalTransitionSchemaVersion {
		return resumeGoalTransitionRecord{}, "", fmt.Errorf(
			"recovery transition schema version %d disagrees with the current %d: a record written under a version its readers do not accept is undiscoverable evidence",
			v, resumeGoalTransitionSchemaVersion)
	}
	if !knownRecoveryTransitionKind(kind) {
		return resumeGoalTransitionRecord{}, "", fmt.Errorf("recovery transition requires a known kind, got %q", in.Kind)
	}
	attemptID := strings.TrimSpace(in.AttemptID)
	if attemptID == "" {
		return resumeGoalTransitionRecord{}, "", fmt.Errorf("recovery transition (%s) requires an attempt id", kind)
	}
	pause := strings.TrimSpace(in.PauseGeneration)
	fingerprint := strings.TrimSpace(in.PreclaimFingerprint)

	record := in.Base
	record.RecoveryKind = string(kind)
	record.SchemaVersion = resumeGoalTransitionSchemaVersion

	// THE ONE COLLAPSED SIDE DOOR, LOCKED. Nesting the U7 block turned fifteen potential Base-carried
	// side doors into one, and cto's condition was that the one must be guarded NOW or we have merely
	// concentrated the risk. It refuses on BOTH kinds and EVEN WHEN THE VALUES AGREE -- deliberately
	// stricter than the flat posture rule above, and dev-2's reason is why: for a flat derived field an
	// agreeing Base value means a consistent caller, but there is NO legitimate Base source for this block
	// at all, so agreement proves nothing about intent. "Refuse any presence" is the correct posture for a
	// field the constructor alone may populate.
	if record.NativeBinding != nil {
		return resumeGoalTransitionRecord{}, "", fmt.Errorf(
			"recovery transition (%s) must not carry a Base-supplied native binding: this block is built by the constructor from validated inputs, so a Base value bypassed every check that makes it trustworthy",
			kind)
	}
	record.NativeBinding = nil

	switch kind {
	case recoveryTransitionKindRedeliver:
		// This path holds no assessment. Presence of a pause generation here is evidence that
		// someone derived one locally, so it refuses rather than storing it.
		// BOTH RAW ROUTES, and this guard is where the Supervisor bug came from: I used it as the
		// template, so it propagated its own defect into the new field. `record := in.Base` above copies
		// Base.PauseGeneration, and `pause` derives from in.PauseGeneration only -- so a value arriving
		// through Base was returned and PERSISTED on a redeliver record, making pause-generation presence
		// an unreliable native-origin signal exactly as it did for the supervisor.
		//
		// RAW, not trimmed, for the reason dev-2 gave on the supervisor: `omitempty` does not omit a
		// whitespace-only string, so a trimmed guard would serialise a present-but-meaningless value.
		// Redeliver has no assessment, so it has no exact binding to persist.
		if in.NativeBinding != nil {
			return resumeGoalTransitionRecord{}, "", fmt.Errorf("redeliver transitions must NOT carry a native binding: that block is the assessment's exact binding and this path holds no assessment")
		}
		if in.PauseGeneration != "" || record.PauseGeneration != "" {
			route, carried := "input", in.PauseGeneration
			if in.PauseGeneration == "" {
				route, carried = "Base record", record.PauseGeneration
			}
			return resumeGoalTransitionRecord{}, "", fmt.Errorf("redeliver transitions must NOT carry a pause generation: this path holds no assessment, so a value here was recomputed or invented (arrived via %s as %q)", route, carried)
		}
		if strings.TrimSpace(in.BindingDigest) == "" {
			return resumeGoalTransitionRecord{}, "", fmt.Errorf("redeliver transition requires the binding digest its identity is derived from")
		}
		// Same asymmetry as the pause generation above: this path holds no assessment and no supervisor,
		// so a supervisor here was invented. Refusing is what keeps "supervisor present" a reliable
		// signal that a record came from the supervised path.
		//
		// BOTH ROUTES, and my first version only guarded ONE. `record := in.Base` above copies
		// Base.Supervisor, so a value arriving through Base was returned and PERSISTED while
		// in.Supervisor was empty -- defeating the asymmetry through the door I left open. I had applied
		// the Base-route guard correctly for NewAttemptID in the native branch twenty lines below and
		// did not apply it here, which is the same-pattern-half-applied failure.
		//
		// RAW COMPARISON, NOT TRIMMED, and dev-2's reason is sharper than my own: a whitespace-only value
		// survives `omitempty` because the raw string is nonempty, so it would serialise into the record
		// as a present-but-meaningless supervisor. Trimming before the check would let exactly that
		// through. Trimming is right for the NATIVE branch, which stores a validated identity; here the
		// question is only whether ANYTHING is present.
		if in.Supervisor != "" || record.Supervisor != "" {
			route := "input"
			carried := in.Supervisor
			if in.Supervisor == "" {
				route, carried = "Base record", record.Supervisor
			}
			return resumeGoalTransitionRecord{}, "", fmt.Errorf("redeliver transitions must NOT carry a supervisor: this path holds no assessment and no supervising actor, so a value here was invented (arrived via %s as %q)", route, carried)
		}
		// The canonical redeliver identity is unchanged, so redelivery's own readers are
		// untouched -- PR5 does not refactor that read side. It is now emitted HERE, which is
		// what makes "no write bypasses the one constructor" true in meaning.
		record.TransitionID = resumeGoalTransitionID(attemptID, in.BindingDigest)
		record.PreclaimFingerprint = fingerprint // optional evidence on this path
		// THE SHARED DURABLE CONTRACT, on what this branch just built. Everything above is
		// CONSTRUCTOR-ONLY work -- route disagreement and Base smuggling, which a finished record cannot
		// testify about. Everything observable IN the record is decided by the one validator, so the
		// publisher cannot hold a different opinion about what a valid redeliver record is.
		//
		// This closes dev-2's blocker 2 at its root: this branch used to return a record with a BLANK
		// NewAttemptID, which publication then unconditionally refused. The constructor accepted what the
		// publisher rejected, and my own anti-vacuity test asserted the accepted case and could never pass.
		if err := validateRecoveryTransitionRecordContract(record); err != nil {
			return resumeGoalTransitionRecord{}, "", err
		}
		// Derived through resumeGoalTransitionPath so redelivery's EXISTING readers are
		// untouched: the path they already compute is the path this constructor emits.
		path, err := resumeGoalTransitionPath(in.Base.Project, in.Base.Profile, in.Base.Session, record.TransitionID)
		if err != nil {
			return resumeGoalTransitionRecord{}, "", fmt.Errorf("derive redeliver transition path: %w", err)
		}
		return record, path, nil

	case recoveryTransitionKindNativeGoalResume:
		// THE SCOPE-TRIPLE REQUIREMENT LIVED HERE AND IS DELETED. It is a completed-record content check, so
		// the shared contract owns it (recovery_transition_contract.go), and keeping a copy here was the
		// ruled shape half-applied: I added the one decider and did not remove what it replaced. A second
		// list is not redundancy, it is co-drift waiting to happen -- deleting a row from the shared contract
		// would have left these constructor tests green while publication silently regressed.
		//
		// NamespaceID must equal the canonical id for this profile/session, or a correct hash
		// could be written under a namespace it does not belong to -- present on disk and
		// invisible to the scanner that would look for it.
		//
		// squadnamespace.ID is at internal/namespace/namespace.go:46 (verified by reading it, not
		// by trusting the citation). My earlier search failed because I assumed the package
		// DIRECTORY matched the import alias: the alias is squadnamespace, the path is
		// internal/namespace.
		if want := squadnamespace.ID(in.Base.Profile, in.Base.Session); strings.TrimSpace(in.NamespaceID) != want {
			return resumeGoalTransitionRecord{}, "", fmt.Errorf("supervision resume namespace id %q does not match the canonical id %q for profile/session: a claim written under a foreign namespace is invisible to the scanner that would find it", in.NamespaceID, want)
		}
		if pause == "" {
			return resumeGoalTransitionRecord{}, "", fmt.Errorf("supervision resume requires the assessment-captured pause generation; extend the assessment-to-plan binding rather than omitting it")
		}
		if fingerprint == "" {
			return resumeGoalTransitionRecord{}, "", fmt.Errorf("supervision resume requires the preclaim fingerprint as staleness evidence")
		}
		// THE FLAT LAUNCH GENERATION requirement lived here and is DELETED for the same reason as the scope
		// triple: it is completed-record content, so the shared contract is its sole owner. Note what does
		// NOT move with it -- these fields arrive via Base, and Base is their LEGITIMATE route because they
		// have no explicit input counterpart and no derived value to disagree with. That is a provenance
		// fact, but it is a fact about what the constructor must NOT refuse, so it needs no check at all.
		//
		// SUPERVISOR IS REQUIRED AND VALIDATED HERE TOO, not only at the gate. The gate protects the
		// delivering path; this protects the RECORD, and any future caller of the constructor that
		// skips the gate would otherwise write an unattributable claim. A durable identity field whose
		// only guard lives in one caller is guarded by convention rather than by construction.
		supervisor := strings.TrimSpace(in.Supervisor)
		if supervisor == "" {
			return resumeGoalTransitionRecord{}, "", fmt.Errorf("supervision resume requires the supervisor identity: a claim nobody is recorded as authorising cannot be audited after the fact")
		}
		if supervisor == supervisorIdentityPlaceholder {
			return resumeGoalTransitionRecord{}, "", fmt.Errorf("supervision resume supervisor is still the literal placeholder %s: recording it would durably attribute the resume to nobody while looking like an answer", supervisorIdentityPlaceholder)
		}
		// AND THE BASE ROUTE HERE TOO, by the same rule I applied to NewAttemptID twenty lines below.
		// Assigning the validated value would OVERWRITE a disagreeing Base.Supervisor silently: the
		// persisted attribution would be correct, so nothing breaks -- which is exactly why it is worth
		// refusing. A caller passing a different supervisor through Base believes something false about
		// what this record will say, and silently correcting them hides a caller bug that will surface
		// somewhere less observable. Same reasoning as the attempt id: a record whose identity fields
		// disagree is ambiguous evidence, not a tie to break.
		if supplied := strings.TrimSpace(record.Supervisor); supplied != "" && supplied != supervisor {
			return resumeGoalTransitionRecord{}, "", fmt.Errorf(
				"supervision resume supervisor %q disagrees with the Base-supplied %q: the record would attribute the resume to one actor while the caller believes another",
				supervisor, supplied)
		}
		record.Supervisor = supervisor
		key, err := supervisionClaimKey(in.NamespaceID, pause, attemptID)
		if err != nil {
			return resumeGoalTransitionRecord{}, "", err
		}
		// DERIVED here, never accepted from the caller. dev-2's correction: a caller-supplied id
		// would let a legacy or one-byte-wrong value through, writing a current-looking record
		// under the wrong key -- unmatched on read, therefore absent, therefore a second
		// delivery.
		record.TransitionID = key
		// Same rule as Supervisor and NewAttemptID: a DISAGREEING Base value refuses rather than being
		// silently corrected. See the posture note above the constructor for why TransitionID is the one
		// deliberate exception.
		if supplied := strings.TrimSpace(record.PauseGeneration); supplied != "" && supplied != pause {
			return resumeGoalTransitionRecord{}, "", fmt.Errorf(
				"supervision resume pause generation %q disagrees with the Base-supplied %q: two sources for one derived identity is the one-identity-two-owners shape whose symptom is double delivery",
				pause, supplied)
		}
		record.PauseGeneration = pause
		if supplied := strings.TrimSpace(record.PreclaimFingerprint); supplied != "" && supplied != fingerprint {
			return resumeGoalTransitionRecord{}, "", fmt.Errorf(
				"supervision resume preclaim fingerprint %q disagrees with the Base-supplied %q: the record would carry staleness evidence the caller did not intend",
				fingerprint, supplied)
		}
		record.PreclaimFingerprint = fingerprint

		// THE U7 EXACT BINDING. Required, every field validated, and assembled as a FRESH VALUE field by
		// field rather than by assigning the caller's pointer: a pointer assignment would leave the record
		// ALIASING memory the caller still holds, so a later mutation by the caller would silently change a
		// record we validated. Validation of aliased data expires the moment the caller touches it again.
		if in.NativeBinding == nil {
			return resumeGoalTransitionRecord{}, "", fmt.Errorf("supervision resume requires the native exact binding: the record IS the recovery contract, and recovery cannot revalidate fields the record never persists")
		}
		nb := *in.NativeBinding
		// THE NINE-INTERIOR REQUIRED LIST AND THE PolicyRevision CHECK LIVED HERE AND ARE DELETED. They were
		// the third and largest duplicate of the shared contract, and the most dangerous one to keep: the
		// all-zero-interiors record dev-2 found was refused by THIS list while publication accepted it, so
		// two lists meant the constructor path and the publication path disagreed about the same record.
		//
		// WHAT SURVIVES ABOVE IS THE nil CHECK ONLY, and it survives as a ROUTE/MECHANICAL fact rather than a
		// content requirement: the dereference on the next line needs it, and "the caller supplied no block"
		// is knowledge the finished record cannot carry (a nil block and a caller who passed nothing are
		// indistinguishable once the record exists). Per the ruling, it keeps the nil fact and NOT the
		// interior list.
		//
		// THE INTENTIONAL DUAL, with its invariant ENFORCED rather than assumed. GoalAttemptID is the
		// authorized assessment identity; the flat NewAttemptID is the ruled lifecycle identity used
		// through bound/consumed. Two fields carrying one value is only legitimate because this equality
		// is checked -- that is exactly what distinguishes it from the five duplicate fields dev-2
		// rejected from my draft.
		if strings.TrimSpace(nb.GoalAttemptID) != attemptID {
			return resumeGoalTransitionRecord{}, "", fmt.Errorf(
				"native binding goal attempt id %q disagrees with the reservation attempt %q: the authorized identity and the lifecycle identity must be the same attempt",
				nb.GoalAttemptID, attemptID)
		}
		if strings.TrimSpace(nb.NamespaceID) != strings.TrimSpace(in.NamespaceID) {
			return resumeGoalTransitionRecord{}, "", fmt.Errorf(
				"native binding namespace id %q disagrees with the claim namespace %q: a record filed under one namespace claiming another is invisible to the scanner that would find it",
				nb.NamespaceID, in.NamespaceID)
		}
		record.NativeBinding = &resumeGoalNativeBinding{
			NamespaceID:             strings.TrimSpace(nb.NamespaceID),
			PaneID:                  strings.TrimSpace(nb.PaneID),
			GoalMode:                strings.TrimSpace(nb.GoalMode),
			GoalAttemptID:           strings.TrimSpace(nb.GoalAttemptID),
			GoalBindingDigest:       strings.TrimSpace(nb.GoalBindingDigest),
			GoalCommandDigest:       strings.TrimSpace(nb.GoalCommandDigest),
			BlockerID:               strings.TrimSpace(nb.BlockerID),
			BlockerResolutionDigest: strings.TrimSpace(nb.BlockerResolutionDigest),
			PolicyMode:              strings.TrimSpace(nb.PolicyMode),
			PolicyRevision:          nb.PolicyRevision,
		}
		// NEWATTEMPTID, ruled for native reservations (#498, U7 slice, cto 2026-07-28T09:25): the
		// persisted record must carry an attempt identity a reader can MATCH on. Without it the
		// cross-kind mutex's "proves a different pause" escape keys on fields native records never
		// set, so it can never open -- an escape unreachable by construction, which is worse than no
		// escape because the surrounding code reads as conditional while behaving unconditionally.
		//
		// DERIVED from the validated attemptID, never accepted from Base, for the same reason
		// TransitionID is: a caller-supplied value could disagree with the key this record is filed
		// under, and a record whose identity fields disagree is ambiguous evidence rather than a tie
		// to break.
		//
		// THE FIELD IS SEMANTICALLY OVERLOADED ACROSS KINDS, AND THAT IS A HAZARD RATHER THAN A
		// DESIGN. For REDELIVER, NewAttemptID is the NEW attempt being created, and two validators
		// require it to DIFFER from OriginalAttemptID (validateResumeGoalTransitionPlan, and the
		// explicit-CAS branch of validateResumeGoalTransitionForDelivery: "transition reuses the
		// original attempt id"). For NATIVE it is the SAME attempt being resumed, so that invariant is
		// INVERTED. Both validators are redelivery-scoped today, so nothing breaks now -- but Unit B
		// is deliberately building KIND-AGNOSTIC readers, and the first kind-agnostic validator that
		// applies the differs-invariant to a native record will reject every native reservation. Any
		// such reader MUST scope that check to recoveryTransitionKindRedeliver.
		if supplied := strings.TrimSpace(record.NewAttemptID); supplied != "" && supplied != attemptID {
			return resumeGoalTransitionRecord{}, "", fmt.Errorf(
				"supervision resume new attempt id %q disagrees with the attempt being resumed %q: the record would be filed under one attempt and claim another",
				supplied, attemptID)
		}
		record.NewAttemptID = attemptID
		// THE SHARED DURABLE CONTRACT, on what this branch just built -- the same function publication calls.
		// The per-field requirements above that duplicate it are now the CONSTRUCTOR-ONLY half: they refuse a
		// disagreeing INPUT ROUTE (Base versus explicit) and name the input that was wrong, which is a better
		// error for a caller than a refusal about the finished record. The contract is what guarantees the
		// record is valid regardless of how it was assembled.
		if err := validateRecoveryTransitionRecordContract(record); err != nil {
			return resumeGoalTransitionRecord{}, "", err
		}
		// The DIRECTORY is derived from the same validated triple, inside the constructor. A
		// caller joining a basename could place a namespace-A claim in namespace-B's directory,
		// where neither scanner would find it.
		return record, filepath.Join(goalAttemptDir(in.Base.Project, in.Base.Profile, in.Base.Session), currentRecoveryTransitionName(kind, key)), nil
	}
	return resumeGoalTransitionRecord{}, "", fmt.Errorf("unhandled recovery transition kind %q", kind)
}

// recoveryTransitionInput is what a caller supplies. It carries NO TransitionID field on purpose:
// the identity is DERIVED per kind, so there is nothing for a caller to get wrong or to smuggle.
//
// Base carries what the existing redelivery writer already computes, which is what makes routing
// that writer through here additive rather than a rewrite of what it records.
type recoveryTransitionInput struct {
	Base                resumeGoalTransitionRecord
	Kind                recoveryTransitionKind
	NamespaceID         string
	AttemptID           string
	BindingDigest       string
	PauseGeneration     string
	PreclaimFingerprint string
	Supervisor          string

	// NativeBinding is the U7 exact-binding block, supplied EXPLICITLY and never through Base.
	// dev-2 owns the type; this is the only route the constructor accepts it by, because
	// Base.NativeBinding is a refused side door on BOTH kinds -- see the posture note above the
	// constructor. Non-nil exactly for native goal-resume; must be nil for redeliver.
	NativeBinding *resumeGoalNativeBinding
}
