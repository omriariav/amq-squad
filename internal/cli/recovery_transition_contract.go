package cli

import (
	"fmt"
	"strings"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
)

// #498 U7 / HOLD 2: THE ONE DURABLE-RECORD CONTRACT.
//
// WHY THIS FILE EXISTS. I wrote the publication validator as its OWN list of checks, and dev-2 found by
// source trace that it accepted records the constructor would refuse -- exactly:
//
//	a current-schema native record with scope, supervisor, pause generation, an arbitrary 64-hex
//	TransitionID, and NativeBinding = &resumeGoalNativeBinding{} with all ten interiors ZERO,
//	published at the canonical path derived from that arbitrary id.
//
// Publication accepted it. Nothing durable about that record is usable: no scanner derives that id, so the
// claim is present on disk and reads as ABSENT for the real pause, which is the double delivery claim-once
// exists to prevent -- reached through the writer I added to prevent it.
//
// THE RULED SHAPE IS ONE DECIDER, TWO CALL SITES, not a second hand-maintained list inside publication. Two
// lists of record requirements must co-evolve forever, and their drift IS the constructor-versus-publisher
// disagreement this milestone has been closing all day. With one function, a field added to the contract is
// automatically required at publication and the co-drift is structurally impossible.
//
// THE BOUNDARY (dev-2's refinement, ruled binding) is what makes the split principled rather than arbitrary:
//
//	SHARED (here)      every invariant OBSERVABLE IN THE COMPLETED RECORD: per-kind required fields, and
//	                   derived-identity RECOMPUTATION from the record's own durable source fields.
//	CONSTRUCTOR ONLY   what a finished record cannot testify about: which INPUT ROUTE a value arrived by,
//	                   Base smuggling, and fresh-copy/non-aliasing. A record on disk cannot tell you whether
//	                   its supervisor came through the explicit input or through Base.
//	PUBLICATION        this validator, then exact path/name agreement, which is about the record's LOCATION
//	                   rather than its content.
//
// IDENTITY IS RECOMPUTED, NOT COMPARED TO ITSELF. This is the sharpest part of dev-2's finding and the reason
// path-equality alone was not closure: my canonical path derivation took record.TransitionID as INPUT, so
// checking the path against it proved only that the record agreed with itself. A self-referential invariant
// binds nothing -- the same defect class as U10's runtime-resolved baseline_head, which cto rejected for
// binding the run to whatever tree it found. The identity must be derived from the fields that GENERATE it.
func validateRecoveryTransitionRecordContract(record resumeGoalTransitionRecord) error {
	kind := recoveryTransitionKind(strings.TrimSpace(record.RecoveryKind))
	if kind == "" {
		return fmt.Errorf("recovery transition record carries no recovery kind, so it is neither a valid redeliver nor a valid native reservation")
	}
	if !knownRecoveryTransitionKind(kind) {
		return fmt.Errorf("recovery transition record has unknown recovery kind %q", kind)
	}
	if record.SchemaVersion != resumeGoalTransitionSchemaVersion {
		return fmt.Errorf(
			"recovery transition record schema version %d is not the current %d: its own readers validate this, so a record written under a version they reject is undiscoverable evidence",
			record.SchemaVersion, resumeGoalTransitionSchemaVersion)
	}
	// THE SCOPE TRIPLE MUST ALREADY BE IN CANONICAL FORM -- stored trimmed, not merely trimmable.
	//
	// dev-2's static counterexample: a Project of "<tmpdir> " (trailing space) is nonblank under a TrimSpace
	// check, so the contract accepted it; the constructor then derived its path from the RAW value while
	// canonicalRecoveryTransitionPath re-derived from the TRIMMED one. Publication refused the constructor's
	// own output. goalAttemptDir trims session and normalizes profile but joins project verbatim, so project
	// was the live vector -- all three are checked because a normalization difference is not something to
	// re-audit per field every time goalAttemptDir changes.
	//
	// RULED FIX: canonicalize AT THE DATA rather than aligning two consumers of ambiguous data. One canonical
	// form in the record, everything derives from it. Refusing is chosen over silently rewriting the value
	// because a caller holding "<tmpdir> " believes something false about where its claim will live, and
	// silent correction defers that discovery to somewhere less observable -- the same posture rule the
	// constructor applies to disagreeing Base values.
	for _, f := range [][2]string{{"project", record.Project}, {"profile", record.Profile}, {"session", record.Session}} {
		if f[1] != strings.TrimSpace(f[1]) {
			return fmt.Errorf(
				"recovery transition (%s) %s %q is not stored in canonical form: the record must hold the exact value every path derivation uses, or the writer and the publisher compute different locations for the same claim",
				kind, f[0], f[1])
		}
	}
	// THE SCOPE TRIPLE, both kinds. Without it there is no canonical location and no namespace identity, and
	// goalAttemptDir would build a RELATIVE path from blanks -- a claim published relative to whatever
	// directory the process happens to be in, unfindable by every scanner including the one that wrote it.
	if err := requireRecoveryFields(kind, [][2]string{
		{"project", record.Project},
		{"profile", record.Profile},
		{"session", record.Session},
		// NewAttemptID is required for BOTH kinds, and the reason differs per kind rather than being one
		// rule: for redeliver it is the new attempt being created, for native it is the attempt being
		// resumed. Both need a reader to be able to MATCH on it -- the cross-kind mutex's
		// proves-a-different-pause escape keys on it, and a blank one makes that escape unreachable.
		{"new attempt id", record.NewAttemptID},
	}); err != nil {
		return err
	}
	// NONBLANK IS NOT READER-ACCEPTED (codex finding 4). The requirement above proves the field is present;
	// it says nothing about whether the value can ever be USED. "../new" is nonblank, and for redeliver it is
	// also different from the original attempt, so it satisfied every writer-side check and published --
	// while the delivery path refuses it at resume_goal.go:1176 via goalAttemptPath. The result is durable
	// evidence NOTHING CAN CONSUME: the claim blocks at the claim-once gate permanently, and no code path
	// deletes reservations.
	//
	// VALIDATED THROUGH THE READER'S OWN OWNER, never re-implemented. goalAttemptPath (goal_attempt.go:105)
	// holds the shape rule -- empty, ".", "..", non-basename, or containing a separator -- and re-stating
	// those characters here would be a SECOND OPINION about attempt-id shape, which is the exact defect this
	// file exists to make structurally impossible. Calling the reader's function is what makes writer and
	// reader agree by construction instead of by maintenance.
	//
	// IN THE SHARED PREAMBLE, so it covers BOTH KINDS. The native branch derives its claim key by HASHING
	// NewAttemptID (validateNativeRecoveryRecordContract below), and a hash accepts any string, so native
	// carried the identical hole under a different derivation. Placing this in the redeliver branch alone --
	// where the finding was reported -- would have closed one of two doors.
	//
	// It also closes the READ-BACK TAMPER PATH: this contract runs against the record re-read from disk
	// (goal_supervision_resume.go:416), where catching a tampered value is the entire purpose of the call.
	//
	// MY OWN COMMENT CONVICTED ME. The redeliver branch below cites "the explicit-CAS branch of
	// validateResumeGoalTransitionForDelivery at :1173-1174" as the reason for its equality check. I was
	// reading that region and mirrored the refusal at :1173 while not mirroring the one THREE LINES LATER at
	// :1176. Reading the adjacent line and not this one is how a writer/reader disagreement survives review.
	if _, err := goalAttemptPath(record.Project, record.Profile, record.Session, record.NewAttemptID); err != nil {
		return fmt.Errorf(
			"recovery transition (%s) new attempt id %q is not a value its own delivery reader accepts (%w): the delivery path refuses it, so this record would be durable evidence nothing can consume and the pause would block forever",
			kind, record.NewAttemptID, err)
	}

	switch kind {
	case recoveryTransitionKindNativeGoalResume:
		return validateNativeRecoveryRecordContract(record)
	case recoveryTransitionKindRedeliver:
		return validateRedeliverRecoveryRecordContract(record)
	}
	// Unreachable via knownRecoveryTransitionKind above. Stated as a refusal rather than a fallthrough so a
	// future kind that adds a constructor branch and forgets a contract branch FAILS here instead of
	// publishing an unvalidated record.
	return fmt.Errorf("recovery transition record kind %q has no durable contract", kind)
}

// validateNativeRecoveryRecordContract is the RATIFIED U7 SET, checked against the finished record.
func validateNativeRecoveryRecordContract(record resumeGoalTransitionRecord) error {
	kind := recoveryTransitionKindNativeGoalResume
	if err := requireRecoveryFields(kind, [][2]string{
		// ROLE AND HANDLE. dev-2's blocker 3: the ratified contract requires these, the production caller
		// supplies them, and revalidation compares them -- but nothing REQUIRED them, so a blank record
		// revalidated against a blank assessment passed BY MUTUAL ABSENCE. That was the third
		// pass-by-mutual-absence of the day. A comparison is not a requirement.
		{"lead role", record.Role},
		{"lead handle", record.Handle},
		// WHO AUTHORISED IT. Accountability that exists only in a live process cannot be audited after it.
		{"supervisor", record.Supervisor},
		{"pause generation", record.PauseGeneration},
		{"preclaim fingerprint", record.PreclaimFingerprint},
		// THE FLAT LAUNCH GENERATION. Recovery cannot revalidate a generation the record never persisted,
		// and a relaunch is the one drift the U5 liveness gates cannot see: the pane can be live, managed
		// and idle while belonging to a process this claim never authorized.
		{"launch id", record.LaunchID},
		{"launch record digest", record.LaunchRecordDigest},
	}); err != nil {
		return err
	}
	if strings.TrimSpace(record.Supervisor) == supervisorIdentityPlaceholder {
		return fmt.Errorf(
			"recovery transition (%s) supervisor is still the literal placeholder %s: recording it would durably attribute the resume to nobody while LOOKING like an answer, which is worse than a blank",
			kind, supervisorIdentityPlaceholder)
	}
	// <= 0 rather than == 0: a negative modtime is not a plausible observation either, and accepting one
	// would persist a generation no stat call could reproduce.
	if record.LaunchRecordModTime <= 0 {
		return fmt.Errorf(
			"recovery transition (%s) requires a positive launch record mod time, got %d: zero is indistinguishable from an unset field, so drift against it can never be detected",
			kind, record.LaunchRecordModTime)
	}
	if record.NativeBinding == nil {
		return fmt.Errorf("recovery transition (%s) requires its exact binding: the record IS the recovery contract, and delivery cannot revalidate fields the record never persists", kind)
	}
	nb := record.NativeBinding
	// EVERY INTERIOR, because dev-2's counterexample was an all-zero block that passed a nil check. A
	// pointer-presence sentinel tests that someone allocated a struct, not that the binding says anything.
	if err := requireRecoveryFields(kind, [][2]string{
		{"native binding namespace id", nb.NamespaceID},
		{"native binding pane id", nb.PaneID},
		{"native binding goal mode", nb.GoalMode},
		{"native binding goal attempt id", nb.GoalAttemptID},
		{"native binding goal binding digest", nb.GoalBindingDigest},
		{"native binding goal command digest", nb.GoalCommandDigest},
		// Blocker fields are REQUIRED, not optional: eligibility (goal_supervision.go:341-342) requires
		// Known + nonblank ID + Resolved + nonblank ResolutionDigest, so an authorized native resume ALWAYS
		// carries both. Accepting empty would let a gate-skipping caller write a record the executor could
		// never have authorized.
		{"native binding blocker id", nb.BlockerID},
		{"native binding blocker resolution digest", nb.BlockerResolutionDigest},
		{"native binding policy mode", nb.PolicyMode},
	}); err != nil {
		return err
	}
	if nb.PolicyRevision <= 0 {
		return fmt.Errorf("recovery transition (%s) requires a positive native binding policy revision, got %d: revision zero is indistinguishable from an unset field", kind, nb.PolicyRevision)
	}
	// THE NAMESPACE MUST BE THE CANONICAL ONE FOR THIS RECORD'S OWN PROFILE/SESSION. Checked here rather
	// than only at construction because it is fully observable in the record: a correct hash filed under a
	// foreign namespace is invisible to the scanner that would look for it.
	if want := squadnamespace.ID(record.Profile, record.Session); strings.TrimSpace(nb.NamespaceID) != want {
		return fmt.Errorf(
			"recovery transition (%s) native binding namespace id %q is not the canonical id %q for its own profile/session: a claim filed under a foreign namespace is invisible to the scanner that would find it",
			kind, nb.NamespaceID, want)
	}
	// THE INTENTIONAL DUAL'S INVARIANT, re-checked on the finished record. Two fields carrying one value is
	// legitimate ONLY while something enforces that they do; construction-time enforcement says nothing
	// about a record read back from disk or hand-built by a direct caller.
	if strings.TrimSpace(nb.GoalAttemptID) != strings.TrimSpace(record.NewAttemptID) {
		return fmt.Errorf(
			"recovery transition (%s) native binding goal attempt id %q disagrees with the record's lifecycle attempt %q: a record whose two attempt identities differ is ambiguous evidence about which attempt was authorized",
			kind, nb.GoalAttemptID, record.NewAttemptID)
	}
	// THE DERIVED IDENTITY, RECOMPUTED FROM THE RECORD'S OWN SOURCE FIELDS. This is dev-2's central finding:
	// an arbitrary 64-hex id that merely AGREES with its filename and path is not an identity, because no
	// pause scanner derives it. The claim key is what a scanner computes from namespace + pause generation +
	// attempt, so that is what the record must carry.
	wantID, err := supervisionClaimKey(nb.NamespaceID, record.PauseGeneration, record.NewAttemptID)
	if err != nil {
		return fmt.Errorf("recovery transition (%s) cannot derive its own claim key: %w", kind, err)
	}
	if strings.TrimSpace(record.TransitionID) != wantID {
		return fmt.Errorf(
			"recovery transition (%s) transition id %q is not the claim key %q derived from its own namespace/pause/attempt: an id no scanner computes is unmatchable on read, therefore treated as ABSENT, therefore a second delivery",
			kind, record.TransitionID, wantID)
	}
	return nil
}

// validateRedeliverRecoveryRecordContract is the complete redeliver contract dev-2's blocker 2 required be
// DEFINED rather than assumed.
//
// It was previously undefined, and the cost was concrete: the constructor accepted an input carrying only a
// scope triple and returned a record with a blank NewAttemptID, which publication then unconditionally
// refused. The constructor and the publisher disagreed about what a valid redeliver record is, and my own
// anti-vacuity test asserted the accepted case and could never pass.
func validateRedeliverRecoveryRecordContract(record resumeGoalTransitionRecord) error {
	kind := recoveryTransitionKindRedeliver
	if err := requireRecoveryFields(kind, [][2]string{
		// THE IDENTITY SOURCE FIELDS. Required because the identity is recomputed from them below: a record
		// that does not carry what generates its own id cannot have that id verified, and an unverifiable
		// id is exactly the arbitrary-64-hex hole in the native branch wearing different field names.
		{"original attempt id", record.OriginalAttemptID},
		{"original binding digest", record.OriginalBindingDigest},
	}); err != nil {
		return err
	}
	// THE NEW ATTEMPT MUST DIFFER FROM THE ORIGINAL. Both peer-owned readers already refuse equality
	// (validateResumeGoalTransitionPlan at resume_goal.go:458-459, and the explicit-CAS branch of
	// validateResumeGoalTransitionForDelivery at :1173-1174), so without this the writer could make DURABLE a
	// record its own readers reject -- the writer/reader disagreement class, with the ledger left holding
	// evidence nothing can consume.
	//
	// SCOPED TO REDELIVER ONLY, deliberately. For native, NewAttemptID EQUALS the resumed attempt by ruling:
	// the same field means "the new attempt being created" here and "the attempt being resumed" there. That
	// overload is a known hazard rather than a design, which is exactly why the invariant has to live in the
	// per-kind contract instead of the shared preamble -- a kind-agnostic version of this check would reject
	// every native reservation.
	if strings.TrimSpace(record.NewAttemptID) == strings.TrimSpace(record.OriginalAttemptID) {
		return fmt.Errorf(
			"recovery transition (%s) new attempt id %q must differ from the original attempt id it replaces: its own readers refuse a transition that reuses the original attempt, so this record would be durable evidence nothing can consume",
			kind, record.NewAttemptID)
	}
	// THE ABSENCES ARE PART OF THE CONTRACT, and they are compared RAW rather than trimmed: `omitempty` does
	// not omit a whitespace-only string, so a trimmed guard would let a present-but-meaningless value
	// serialise. This path holds no assessment, so a value in either field was recomputed or invented, and
	// refusing is what keeps their PRESENCE a reliable signal that a record came from the supervised path.
	if record.NativeBinding != nil {
		return fmt.Errorf("recovery transition (%s) must not carry a native exact binding: that block is the assessment's binding and this path holds no assessment", kind)
	}
	if record.PauseGeneration != "" {
		return fmt.Errorf("recovery transition (%s) must not carry a pause generation (got %q): this path holds no assessment, so the value was recomputed or invented", kind, record.PauseGeneration)
	}
	if record.Supervisor != "" {
		return fmt.Errorf("recovery transition (%s) must not carry a supervisor (got %q): this path holds no assessment and no supervising actor, so the value was invented", kind, record.Supervisor)
	}
	// RECOMPUTED from the record's own durable source fields, exactly as the native branch recomputes its
	// claim key. resumeGoalTransitionID is the derivation redelivery's EXISTING readers use, so this proves
	// the record is filed under the id those readers will look for.
	wantID := resumeGoalTransitionID(record.OriginalAttemptID, record.OriginalBindingDigest)
	if strings.TrimSpace(record.TransitionID) != wantID {
		return fmt.Errorf(
			"recovery transition (%s) transition id %q is not the id %q derived from its own original attempt and binding digest: redelivery's readers compute that value, so a record under any other id is invisible to them",
			kind, record.TransitionID, wantID)
	}
	return nil
}

// requireRecoveryFields refuses the FIRST blank field and names it.
//
// A table rather than a run of ifs so that adding a required field is one line and cannot silently skip the
// refusal, and so every message has the same shape for an operator reading a refusal they have not seen.
func requireRecoveryFields(kind recoveryTransitionKind, fields [][2]string) error {
	for _, f := range fields {
		if strings.TrimSpace(f[1]) == "" {
			return fmt.Errorf("recovery transition (%s) requires a %s", kind, f[0])
		}
	}
	return nil
}
