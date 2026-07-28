package cli

import (
	"errors"
	"strings"
	"time"
)

// PR5 / #498: the claim-once native /goal resume. This file owns the CLAIM MACHINERY and no
// store: every durable write goes through the one recovery-transition CAS owner in
// resume_goal.go, per the ruling that a parallel store is the two-deciders defect at the exact
// spot whose failure mode is DOUBLE DELIVERY of an audited resume.
//
// Each eligibility clause below quotes #498 verbatim beside its check, so review is
// contract-to-code rather than re-deriving the design. Where the check is "consult the
// assessment", that is deliberate and ruled: assessment.Eligible is AUTHORITATIVE and this
// executor does not maintain a second eligibility policy. Two places computing one policy is
// how #573's preview and admission came to disagree.

// supervisionResumeRefusal explains a refusal in the operator's terms, always with a recovery
// command. A refusal without a next action is a dead end the operator cannot clear.
type supervisionResumeRefusal struct {
	Clause   string // the #498 contract clause that was not satisfied
	Detail   string
	Recovery string
}

func (r supervisionResumeRefusal) Error() string {
	msg := "native goal resume refused: " + r.Detail
	if r.Clause != "" {
		msg += " [#498: " + r.Clause + "]"
	}
	if r.Recovery != "" {
		msg += "\nRecovery: " + r.Recovery
	}
	return msg
}

// supervisionResumeStep is the ruled sequence, named so the audit trail and the tests refer to
// the same steps rather than to line numbers.
type supervisionResumeStep string

const (
	stepFinalAssessment supervisionResumeStep = "final-assessment"
	stepExclusionReads  supervisionResumeStep = "exclusion-reads"
	stepReserve         supervisionResumeStep = "reserve"
	stepBind            supervisionResumeStep = "bind"
	stepCrossKindRescan supervisionResumeStep = "cross-kind-rescan"
	stepDeliver         supervisionResumeStep = "deliver"
	stepConsume         supervisionResumeStep = "consume"
)

// supervisionResumePayloadLoader obtains the EXACT native resume payload bytes, read-only.
//
// THE SEAM IS FOR TESTABILITY; THE BINDING IS FOR SECURITY. I had this backwards and dev-2
// corrected it (#498 P4), so it is stated plainly here: injection is what MAKES substitution
// possible -- a test substitutes this loader by design, and that is the point of it being a seam.
// It is not a barrier against substitution and claiming otherwise credits the seam with work it
// does not do.
//
// The actual security property: NO loader output authorizes a delivery -- not self-consistent
// output, not output a caller vouches for, not output from the production reader -- unless BOTH
// equalities hold against assessment.Binding.Goal.CommandDigest, which PR4 captured from the launch
// record independently of anything this loader returns. The durable binding digest is the
// third party, and it is the only thing standing between a substituted payload and a pane.
//
// A nil loader REFUSES, same posture as a nil clock: it is a wiring fault, not a condition to
// default around, and defaulting would let a caller silently opt out of payload authorization.
type supervisionResumePayloadLoader func(GoalSupervisionAssessment) (payload string, digest string, err error)

// supervisionResumeOutcome is the STRUCTURED post-success report (#498 U6 finding 3).
//
// It exists because I first made post-consume failures SILENT. U6 says failure never authorizes replay;
// it does not say failure evidence may disappear, and discarding an audit-directive error makes a failed
// AUDIT RECORD invisible -- the opposite of what an audit path is for. I over-corrected from "must not
// retry" to "must be quiet".
//
// Delivered is the irreversible fact. The two post-success actions report SEPARATELY, each carrying its
// own error text, so a caller can see that delivery succeeded while its bookkeeping did not -- which is a
// real and reportable state, not a contradiction.
type supervisionResumeOutcome struct {
	// Delivered becomes true IMMEDIATELY after known-successful pane input, INDEPENDENTLY of consume.
	//
	// The previous wording here said "and a completed consume" -- which is exactly the bug dev-2 caught in
	// the code, left behind in the doc comment after I fixed the code. Delivery is the irreversible fact;
	// consume is bookkeeping that can fail after it, and Consumed reports that separately. A field comment
	// that describes the old semantics is worse than none, because it is the first thing a reader trusts.
	Delivered bool `json:"delivered"`

	// Consumed reports the claim-lifecycle completion separately from Delivered, because they can
	// genuinely disagree: delivery is irreversible and consume can fail after it.
	// DeliveryOutcomeUnknown is set ONLY on the pane-input error path, where the bytes may or may not have
	// landed. THAT SENTENCE WAS FALSE UNTIL codex finding 5: the single write site set it for EVERY error out
	// of the delivery boundary, including a failed durable directive that provably never reached the pane. The
	// field documented the contract correctly and the code did not implement it, which is the inverse of
	// comment-outlives-code and just as expensive. It is now enforced by a typed branch on
	// supervisionDefiniteNonDeliveryError, with unknown as the fail-closed default.
	// It cannot be inferred from Delivered: Delivered must stay FALSE when success is not known,
	// so the unknown case and an ordinary pre-delivery refusal are indistinguishable without this flag --
	// and they are the two states U6 most needs separated, because one leaves a live reservation and an
	// operator decision, and the other leaves nothing at all.
	DeliveryOutcomeUnknown bool `json:"delivery_outcome_unknown"`

	Consumed     bool   `json:"consumed"`
	ConsumeError string `json:"consume_error,omitempty"`

	DeliveredDirectivePublished bool   `json:"delivered_directive_published"`
	DeliveredDirectiveError     string `json:"delivered_directive_error,omitempty"`

	StatusPolled    bool   `json:"status_polled"`
	StatusPollError string `json:"status_poll_error,omitempty"`
}

// warnings renders the post-success failures for stderr. Empty when everything succeeded.
func (o supervisionResumeOutcome) warnings() []string {
	var out []string
	if o.ConsumeError != "" {
		out = append(out, "the resume WAS delivered but consuming the claim FAILED: "+o.ConsumeError+
			" (the pause is now INDETERMINATE; automatic replay stays blocked at the reserved claim, and it "+
			"must be inspected before any manual resume)")
	}
	if o.DeliveredDirectiveError != "" {
		out = append(out, "delivered audit directive FAILED after a completed delivery: "+o.DeliveredDirectiveError+
			" (the resume DID happen and the claim is consumed; re-invocation is refused at the consumed claim)")
	}
	if o.StatusPollError != "" {
		out = append(out, "post-delivery status poll FAILED: "+o.StatusPollError+
			" (observability only; the resume is unaffected)")
	}
	return out
}

// supervisionResumeClock is the INJECTED time source. It is a seam, not a convenience: with a bare
// time.Now() inside the check, the expiry falsifier would have to sleep, and a test that sleeps to
// cross a deadline is timing-dependent and therefore flaky in exactly the direction that makes
// people delete it. Injected, the falsifier is deterministic -- advance one tick past FreshUntil.
type supervisionResumeClock func() time.Time

// executeSupervisionResume runs the confirmed sequence. The ordering is the contract, not a
// preference, and each deviation has a named failure mode:
//
//		action/identity -> final assessment -> wall clock -> policy -> exclusion reads -> RESERVE
//		-> bind -> CROSS-KIND RESCAN -> deliver -> consume
//
//	  - reserve BEFORE deliver, so a crash between them reads as INDETERMINATE rather than
//	    undelivered. #498: "no earlier resume claim for this pause generation has been delivered or
//	    remains indeterminate". Reserving after delivery would make a crash look fresh and permit a
//	    second audited resume.
//	  - no reassessment after the reserve, because this actor's own claim write changes claim
//	    evidence and therefore the fingerprint; a post-claim recheck would fail every time and read
//	    as a bug in the assessment surface rather than as the expected self-write.
//	  - the cross-kind rescan is NOT a reassessment. It reads the ledger only -- never the
//	    fingerprint, never eligibility -- so the no-post-claim-reassessment rule is untouched.
//
// This doc block was ORPHANED for a while: inserting supervisionResumeClock between it and the
// function left it documenting the type instead. Third instance of that defect in this milestone
// (r9's formatArgs comment, r10's paneLookupDefinitelyGone contract), and always the same cause --
// a new declaration inserted between a comment and what it describes. Worth a habit, not just a
// fix: after inserting a declaration, check what the comment ABOVE the insertion point now
// documents.
//
// executeSupervisionResume takes the CANONICAL ACTION and the SUPERVISOR IDENTITY as explicit
// inputs, not as things it derives.
//
// U1: the action is compared against the assessment BEFORE any reserve. Passing the action in is
// what makes that comparison mean anything -- if the executor re-derived the resume command from the
// assessment it would agree with itself by construction, which is the shape of a check that cannot
// fail. The action arriving from outside is what lets it disagree, and disagreement is what a stale
// or substituted invocation looks like.
//
// The supervisor identity is likewise supplied, never inferred from the lead. Two different actors,
// two different accountabilities: inferring the supervisor from LeadRole would record the wrong one
// in the durable claim and there would be no way to tell afterwards.
func executeSupervisionResume(
	assessment GoalSupervisionAssessment,
	action GoalSupervisionAction,
	supervisor string,
	now supervisionResumeClock,
	loadPayload supervisionResumePayloadLoader,
	readGeneration supervisionGenerationReader,
	readPane supervisionPaneReader,
	// #498 U7 ruled option (d): the reserved claim is READ BACK FROM DISK and revalidated, so the comparison
	// has two independent sides instead of comparing the assessment to itself. Injected rather than called
	// directly for the same reason the U5 readers are: a test must be able to hand this path a drifted record,
	// and a production call hard-wired here would make that impossible without touching the filesystem.
	loadReservedClaim supervisionReservedClaimLoader,
	deliver func(string) error,
) (supervisionResumeOutcome, error) {
	// ALL PRE-MUTATION GATES, through the ONE shared evaluator (#498 F5).
	//
	// These checks used to be inline here. They now live in evaluateSupervisionPreMutationGates so the
	// executor and --dry-run consume the SAME gate list, in the same order, producing the same
	// verdicts. That is not tidying: the dry-run exists to PREDICT this function, and a second copy of
	// the gates would drift exactly where agreement is the entire point -- a dry-run reporting
	// "would deliver" where this path refuses.
	//
	// Every gate is READ-ONLY, so nothing below this line has written anything yet. That is what makes
	// U1's "zero ledger writes on mismatch" structural rather than a promise: the first write is the
	// reserve, and it is after this block.
	eval := evaluateSupervisionPreMutationGates(assessment, action, supervisor, now, loadPayload,
		scanRecoveryTransitionsForPause)
	if !eval.allPassed() {
		// The failed gate carries its own clause, detail and recovery, so the refusal an operator sees
		// is identical whether they hit it here or in a dry-run.
		return supervisionResumeOutcome{}, eval.refusal()
	}
	payload := eval.Payload
	// THE PRE-RESERVE BASELINE, captured before any write. The delivery-time drift gate compares
	// against THIS, not against anything re-derived after the reserve -- see AuthorizedDigest's
	// definition for why recomputation would turn the comparison into a tautology.
	authorizedDigest := eval.AuthorizedDigest
	key, dir, legacyKey := eval.ClaimKey, eval.Dir, eval.LegacyKey

	// STEP 3. RESERVE through the single CAS owner. The record AND its path both come from the
	// one constructor -- this executor derives neither, so there is no second path owner and
	// nothing for the AST pin to catch. publishGoalJSON's link(2) is the first-writer primitive;
	// a lost race is a refusal, never an overwrite. (It is NOT O_EXCL; I had assumed that and was
	// wrong, and an enforcement test written against the assumption detected nothing.)
	record, path, err := newRecoveryTransitionRecord(recoveryTransitionInput{
		Base: resumeGoalTransitionRecord{
			Project: assessment.Binding.Project,
			Profile: assessment.Binding.Profile,
			Session: assessment.Binding.Session,
			Role:    assessment.Binding.LeadRole,
			Handle:  assessment.Binding.LeadHandle,
			// THE FLAT LAUNCH GENERATION, consumed from the same assessment. dev-2's cross-gate found these
			// three unpopulated: the record persisted the nested block and left the launch generation it was
			// reserved against absent, so delivery-time revalidation had nothing to compare. A record that
			// cannot answer "which launch was this claim made against" is not a recovery contract for the
			// one drift that actually ends a session -- the runtime being relaunched under the claim.
			//
			// The U5 gate reads the CURRENT generation and compares it against the assessment; this persists
			// what the assessment SAW, so a process that crashes and restarts can still tell the two apart.
			// Without it, recovery revalidates the assessment against itself.
			LaunchID:            assessment.Binding.LaunchID,
			LaunchRecordDigest:  assessment.Binding.LaunchRecordDigest,
			LaunchRecordModTime: assessment.Binding.LaunchRecordModTime,
			// CREATEDAT, decided rather than left ambiguous (dev-2's blocker 4 found native persisting the
			// ZERO time because no native caller set it). Ruling asked for acceptable-by-design or a caller
			// fix; this is the caller fix, because the zero value is not merely cosmetic here: an operator
			// investigating an unexpected resume reads this record to establish WHEN the claim was made, and
			// a zero timestamp is indistinguishable from a corrupted one while looking like data.
			//
			// time.Now() rather than the injected clock deliberately: the resume clock is the FRESHNESS
			// decider, and freshness comparisons are gate logic. Using it here would make an audit timestamp
			// a function of a decision input, so a test that manipulates the clock to drive a gate would
			// silently rewrite the record's provenance too.
			CreatedAt: time.Now().UTC(),
		},
		Kind:                recoveryTransitionKindNativeGoalResume,
		NamespaceID:         assessment.Binding.NamespaceID,
		AttemptID:           assessment.Binding.Goal.AttemptID,
		PauseGeneration:     assessment.Binding.PauseGeneration,
		PreclaimFingerprint: assessment.Fingerprint,
		Supervisor:          supervisor,
		// THE U7 EXACT BINDING, supplied through the EXPLICIT input and never through Base. Base.NativeBinding
		// is a side door the constructor refuses on both kinds, so populating it here would be refused --
		// correctly, and the refusal is what makes this route the only one.
		//
		// Every value is CONSUMED from the assessment rather than recomputed. The assessment is the single
		// observation this claim is made against; recomputing any of these here would bind the record to a
		// DIFFERENT moment than the one that authorized it, which is the one-identity-two-owners shape whose
		// symptom is double delivery.
		NativeBinding: &resumeGoalNativeBinding{
			NamespaceID:             assessment.Binding.NamespaceID,
			PaneID:                  assessment.Binding.Pane.PaneID,
			GoalMode:                assessment.Binding.Goal.Mode,
			GoalAttemptID:           assessment.Binding.Goal.AttemptID,
			GoalBindingDigest:       assessment.Binding.Goal.BindingDigest,
			GoalCommandDigest:       assessment.Binding.Goal.CommandDigest,
			BlockerID:               assessment.Blocker.ID,
			BlockerResolutionDigest: assessment.Blocker.ResolutionDigest,
			PolicyMode:              assessment.Policy.Mode,
			PolicyRevision:          assessment.Policy.Revision,
		},
	})
	if err != nil {
		return supervisionResumeOutcome{}, supervisionResumeRefusal{
			Clause:   "claim-once",
			Detail:   "cannot construct the resume claim: " + err.Error(),
			Recovery: assessment.Actions.Inspect.Command,
		}
	}
	reservation, err := reserveRecoveryTransition(record, path)
	if err != nil {
		return supervisionResumeOutcome{}, supervisionResumeRefusal{
			Clause:   "claim-once",
			Detail:   "could not reserve the resume claim: " + err.Error(),
			Recovery: "another actor holds or held the claim for this pause; inspect the ledger",
		}
	}

	// STEP 4. BIND the launch generation, so a generation change after reservation is DETECTED
	// rather than assumed stable -- the same protection redelivery already takes.
	//
	// The generation is CONSUMED from the assessment, not re-read here. PR4 captured
	// LaunchRecordDigest/LaunchRecordModTime as part of the observation this claim is being made
	// against; re-reading them would bind the claim to a DIFFERENT moment than the one that
	// authorised it, and would quietly make this file a second observer of a generation PR4 owns.
	if err := bindRecoveryTransitionGeneration(reservation,
		assessment.Binding.LaunchRecordDigest, assessment.Binding.LaunchRecordModTime); err != nil {
		return supervisionResumeOutcome{}, supervisionResumeRefusal{
			Clause:   "the same launch generation, pane identity, native goal command/binding, and attempt identity remain current",
			Detail:   "launch generation could not be bound after reservation: " + err.Error(),
			Recovery: "the reservation is left unconsumed; re-run the sweep after the launch generation settles",
		}
	}

	// STEP 5, the cross-kind rescan. Step 2 plus the link(2) publication arbitrate only actors
	// racing to the SAME path. Two actors of different kinds -- or old-vs-new derivation -- race to
	// DIFFERENT paths: both pass step 2, both win their own link, both deliver. Kind is in the
	// filename, so the filesystem CANNOT arbitrate across kinds; this read is the cross-kind mutex.
	//
	// The primitive is named correctly here now. It said O_EXCL twice, FIFTY LINES BELOW the step-3
	// comment in this same file that corrects it to link(2) and explicitly narrates having been
	// wrong about exactly that. Not a comment that aged out of date -- a contradiction authored in
	// one sitting, in one file, where the correction and the uncorrected claim sat together.
	//
	// Both racers refusing is the CORRECT outcome. The contract prefers
	// indeterminate-with-recovery over double delivery, and the window is small.
	after, err := scanRecoveryTransitionsForPause(dir, key, legacyKey)
	if err != nil {
		return supervisionResumeOutcome{}, supervisionResumeRefusal{
			Clause:   "claim-once",
			Detail:   "cannot re-scan the ledger after reserving: " + err.Error(),
			Recovery: "the reservation is left unconsumed and must be inspected before any manual resume",
		}
	}
	// Excluded by EXACT PATH EQUALITY and nothing looser: our own reservation is the one thing in
	// the ledger that must not block us, and any weaker match (same key, same kind, prefix) could
	// exclude a competitor too.
	if competitor := after.competingWith(reservation.Path); competitor != nil {
		return supervisionResumeOutcome{}, supervisionResumeRefusal{
			Clause:   "no earlier resume claim ... delivered or remains indeterminate",
			Detail:   "another recovery transition for this pause appeared concurrently: " + competitor.describe(),
			Recovery: competitor.recovery(),
		}
	}

	// STEP 6. ONE audited delivery of the DIGEST-VERIFIED NATIVE PAYLOAD.
	//
	// Neither action.Command nor assessment.Actions.Resume.Command is ever delivered: both are the
	// operator-facing supervisor invocation, and typing either into the agent's pane would
	// recursively invoke the supervisor inside the thing being supervised.
	//
	// The payload comes from the INJECTED loader, never threaded in as caller data, so there is no
	// substitutable intermediary between authorization and send.
	//
	// (This comment previously named resolveAuthorizedResumePayload, a delivery-time-only helper that
	// A2 removed. Fourth comment-outlives-code instance this milestone -- the deletion is not done
	// until every mention of the deleted thing is reclassified as history or corrected.)
	// U5's FOUR DELIVERY-TIME CLAUSES ARE NOW ALL PRESENT. The payload-drift gate is inline here
	// because it consumes the AuthorizedDigest baseline established BEFORE reserve, and separating the
	// comparison from its baseline is how that gate became a tautology once already. The other three --
	// delivery-time wall clock, launch-generation digest+modtime drift, and exact live managed-idle
	// pane revalidation with a nonzero PID -- are in evaluateSupervisionDeliveryTimeGates, called
	// immediately below, so all four run between the rescan and the input.
	//
	// This does NOT replace the pre-reserve check; it catches drift that happened AFTER it. The current record is re-read and its
	// GoalBinding.Command digest re-verified, so bytes that were authorized at reserve time but have
	// since changed are refused here with the reservation left INDETERMINATE -- same posture as
	// generation drift, and deliberately not a silent re-authorization of whatever the record now
	// says.
	currentPayload, currentDigest, rereadErr := loadPayload(assessment)
	if rereadErr != nil {
		return supervisionResumeOutcome{}, supervisionResumeRefusal{
			Clause:   "the same native goal command/binding remains current",
			Detail:   "cannot re-read the native resume payload immediately before delivery: " + rereadErr.Error(),
			Recovery: "the reservation is left unconsumed and INDETERMINATE; inspect the goal binding before any manual resume",
		}
	}
	if currentDigest != authorizedDigest || currentPayload != payload ||
		digestGoalSupervisionString(currentPayload) != strings.TrimSpace(assessment.Binding.Goal.CommandDigest) {
		return supervisionResumeOutcome{}, supervisionResumeRefusal{
			Clause:   "the same native goal command/binding remains current",
			Detail:   "the native goal binding changed between reservation and delivery; the authorized payload is no longer current",
			Recovery: "the reservation is left unconsumed and INDETERMINATE; drift is a refusal, never a re-authorization",
		}
	}
	// U7 REVALIDATION, before the U5 world-checks. Order is deliberate: this asks whether the RESERVED
	// CLAIM still describes the assessment we are acting on, and there is no point proving the pane is live
	// and idle if the claim was written for a different pane, attempt, or policy revision. Cheap comparisons
	// of already-loaded values, and they gate the expensive tmux reads that follow.
	// READ THE CLAIM BACK FROM DISK FIRST. Revalidating reservation.Record here was a TAUTOLOGY: that record was
	// built field-by-field from `assessment` twenty lines above, so comparing it to `assessment` compared the
	// assessment to itself and could never fail. The comparison only means something when the two sides come
	// from different places, so the left side now comes off the filesystem through the real unmarshal.
	//
	// NIL LOADER REFUSES, never skips -- the M-U5g rule. A missing check that silently becomes a passing check
	// is the authorization-seam defect, and every test that supplies a loader would stay green while production
	// validated nothing.
	if loadReservedClaim == nil {
		return supervisionResumeOutcome{}, supervisionResumeRefusal{
			Clause:   "the same launch generation, pane identity, native goal command/binding, and attempt identity remain current",
			Detail:   "no reserved-claim loader was supplied, so the persisted claim cannot be revalidated before delivery",
			Recovery: "this is a wiring fault, not an operator condition; the caller must supply the read-back loader",
		}
	}
	persisted, readErr := loadReservedClaim(reservation.Path)
	if readErr != nil {
		// UNREADABLE IS NOT UNCHANGED. We just wrote this file; if it cannot be read back, the write did not
		// land as intended or something moved it, and either way we cannot prove what the ledger now holds.
		return supervisionResumeOutcome{}, supervisionResumeRefusal{
			Clause:   "the same launch generation, pane identity, native goal command/binding, and attempt identity remain current",
			Detail:   "the reserved claim could not be read back from its canonical path: " + readErr.Error(),
			Recovery: "the reservation may exist but is unverifiable; inspect the ledger path before any manual resume",
		}
	}
	// RE-ESTABLISH THE DURABLE CONTRACT AFTER DISK I/O, through the ONE existing validator.
	//
	// dev-2's blocker: revalidateNativeBindingAgainstAssessment deliberately EXCLUDES TransitionID, and its own
	// comment gives the reason -- "publication already proves name+path agree". That reason was sound at the old
	// call site and I INVALIDATED IT by adding a step after publication: the read-back exists precisely to catch
	// bad serialization and concurrent mutation, so a proof established before the write is stale here. A loader
	// returning the real record with only TransitionID changed to another well-shaped 64-hex value passed every
	// comparison and reached pane input, under a body that no longer matches its own filename or derived key.
	//
	// I moved a validator across a boundary and did not re-derive the assumptions its exclusions rest on. The
	// fix is NOT a second field list here -- it is to run the same publication contract again, now against the
	// PERSISTED body and the path it actually occupies, which re-proves schema, kind, required fields, the
	// recomputed derived identity, and filename/full-path agreement on the bytes that are really on disk.
	if err := validateRecoveryTransitionPublication(persisted, reservation.Path); err != nil {
		return supervisionResumeOutcome{}, supervisionResumeRefusal{
			Clause:   "the same launch generation, pane identity, native goal command/binding, and attempt identity remain current",
			Detail:   "the PERSISTED claim does not satisfy the durable-record contract at its own path: " + err.Error(),
			Recovery: "the reservation exists but its persisted body is not valid evidence; inspect the claim on disk before any manual resume",
		}
	}
	// SUPERVISOR BY EQUALITY, not by presence. The validator checks only nonblank, so a persisted claim naming a
	// DIFFERENT nonblank actor passed -- delivery would proceed under a claim attributing the authorization to
	// someone other than the caller. Presence-where-identity-was-required is the predicate-weaker-than-property
	// class, and an audit field is exactly where it matters most: a wrong attribution looks like an answer.
	if strings.TrimSpace(persisted.Supervisor) != strings.TrimSpace(supervisor) {
		return supervisionResumeOutcome{}, supervisionResumeRefusal{
			Clause: "the same launch generation, pane identity, native goal command/binding, and attempt identity remain current",
			Detail: "the PERSISTED claim records supervisor " + persisted.Supervisor +
				" but this resume was authorized by " + supervisor,
			Recovery: "the durable claim attributes the resume to a different actor; do not deliver until the ledger is inspected",
		}
	}
	if err := revalidateNativeBindingAgainstAssessment(persisted, assessment); err != nil {
		return supervisionResumeOutcome{}, supervisionResumeRefusal{
			Clause:   "the same launch generation, pane identity, native goal command/binding, and attempt identity remain current",
			Detail:   "the PERSISTED claim does not match the assessment at delivery time: " + err.Error(),
			Recovery: "the reservation is left unconsumed and INDETERMINATE; inspect the claim on disk and the assessment before any manual resume",
		}
	}

	// THE REMAINING THREE U5 GATES, after the payload re-read and before the delivery boundary. They run
	// LAST on purpose: everything above establishes that the CLAIM is sound, and these establish that
	// the WORLD still matches the claim. A refusal here leaves the reservation unconsumed and
	// INDETERMINATE, identical to payload drift, because the ordering guarantee is the same -- nothing
	// has been typed yet, so refusing costs a re-run while proceeding could cost a wrong delivery.
	//
	// THIS IS NOT THE U4.2 CHECK, and the comment here used to claim it was ("immediately before input").
	// It is not: the delivery boundary publishes a durable directive after this point and before pane input
	// (codex finding 3). The authoritative immediately-before-input re-check lives INSIDE the boundary, and
	// this call is now the cheap pre-directive filter that keeps an already-doomed delivery from writing an
	// audit record. Both exist, and each is killable on its own: remove this one and a drifted world still
	// publishes a directive; remove the one in the boundary and drift between directive and input is invisible.
	if refusal := evaluateSupervisionDeliveryTimeGates(assessment, now, readGeneration, readPane); refusal != nil {
		return supervisionResumeOutcome{}, *refusal
	}

	if err := deliver(currentPayload); err != nil {
		// PROVEN NON-DELIVERY IS NOT UNKNOWN (codex finding 5). The boundary marks the refusals that happen
		// strictly before sendPromptToPane -- blank pane id, missing publisher, missing gate, failed durable
		// directive, and the post-directive gate refusal -- with a typed error, because for those no bytes
		// exist to be ambiguous about. Reporting them as unknown told the operator to go inspect a pane about
		// a question that has no ambiguity in it, and it stranded the pause behind an "operator must decide"
		// state with nothing to decide.
		//
		// POLARITY: UNKNOWN IS THE DEFAULT AND A DEFINITE VERDICT REQUIRES PROOF. This branch downgrades to
		// definite ONLY on the typed marker; everything else, including an error from inside sendPromptToPane,
		// keeps DeliveryOutcomeUnknown. Inverting it -- default definite, mark unknown -- reads as the same
		// code and is a fail-open at the one seam where being wrong costs a second audited resume.
		//
		// THE RESERVATION IS NOT TOUCHED EITHER WAY, ruled. Proven non-delivery does NOT release, clear or
		// delete the claim: nothing in this system deletes reservations, and a self-deleting claim would
		// manufacture the orphan-companion state of finding 2. Only the REPORT changes.
		var definite *supervisionDefiniteNonDeliveryError
		if errors.As(err, &definite) {
			if definite.Refusal != nil {
				// THE GATE'S OWN VERDICT, not a paraphrase: its clause and recovery are what the operator needs,
				// and re-composing them here would be a second opinion about why the world failed the check. The
				// proven fact is PREPENDED so the operator learns it before the gate's detail. A copy is taken so
				// the boundary's refusal value is never mutated.
				refusal := *definite.Refusal
				refusal.Detail = "NO PANE INPUT OCCURRED -- the durable directive was published, the world was then re-checked immediately before input, and it refused: " + refusal.Detail
				return supervisionResumeOutcome{}, refusal
			}
			return supervisionResumeOutcome{}, supervisionResumeRefusal{
				Clause:   "claim-once",
				Detail:   "the claim was recorded and delivery was refused BEFORE any pane input: " + err.Error(),
				Recovery: "no delivery occurred, so the pane needs no inspection; the reservation remains and must be consumed or cleared deliberately before another automatic attempt",
			}
		}
		// TERMINAL INDETERMINATE, reported through the OUTCOME rather than inferred from it. Returning the
		// zero outcome here made this case indistinguishable from an ordinary pre-delivery refusal, so the
		// command reported "refused" for exactly U6's unknown-pane-input state -- the one state that leaves
		// a live reservation and an operator decision behind.
		//
		// The reservation stays UNCONSUMED on purpose: the input may or may not have landed, so the pause is
		// indeterminate and the claim-once gate refuses the next attempt rather than risking a second
		// audited resume.
		return supervisionResumeOutcome{DeliveryOutcomeUnknown: true}, supervisionResumeRefusal{
			Clause:   "claim-once",
			Detail:   "delivery failed after the claim was recorded; this pause is now INDETERMINATE: " + err.Error(),
			Recovery: "inspect the pane, then clear or consume the reservation deliberately; automatic resume will not retry",
		}
	}

	// DELIVERED IS TRUE FROM HERE, before consume is attempted.
	//
	// I set this AFTER consume succeeded, so a known-successful delivery followed by a consume FAILURE
	// returned the ZERO outcome -- Delivered=false about a delivery that demonstrably happened. That is the
	// same return-value-versus-reality disagreement my own policy row guards in the opposite direction,
	// and dev-2 caught me enforcing it one way while violating it the other.
	//
	// The irreversible fact is established by the delivery, not by the bookkeeping that follows it. An
	// outcome that denies a completed delivery is the most dangerous possible report here, because a
	// caller reading Delivered=false may reasonably try again.
	outcome := supervisionResumeOutcome{Delivered: true}

	// STEP 7. CONSUME. Reached ONLY on a KNOWN-SUCCESSFUL delivery; an unknown result returned above
	// without consuming, which is what leaves the pause indeterminate rather than recorded as done.
	if err := consumeRecoveryTransition(reservation.Record.TransitionID, reservation.Record.NewAttemptID, reservation.Path); err != nil {
		// The claim stays RESERVED-and-unconsumed, so the pause reads indeterminate and automatic replay
		// is still blocked at the gate. But the delivery HAPPENED, so the outcome must say so while the
		// error reports the bookkeeping failure. Returning both is the only honest shape: a bare error
		// would erase the delivery, and a bare outcome would hide the failure.
		outcome.ConsumeError = err.Error()
		return outcome, err
	}
	outcome.Consumed = true

	// STEP 8. POST-SUCCESS BOOKKEEPING AND OBSERVABILITY, reported rather than swallowed.
	//
	// WHY NOT RETURN AN ORDINARY ERROR HERE -- and my first rationale was WRONG, so the correct one is
	// recorded instead. I wrote that returning an error "would deliver a second audited resume". It would
	// not: CLAIM-ONCE ALREADY PREVENTS THAT. The reservation is consumed, and a consumed claim BLOCKS, so
	// any automatic re-entry refuses at the claim-once gate before any input. The ledger is the replay
	// protection, not this function's return value, and my comment credited the wrong mechanism -- the
	// same error as crediting an injected seam with a durable binding's security.
	//
	// The sound reason is CONFLATION: every caller convention treats a returned error as "the action
	// failed". Here the action SUCCEEDED and only its bookkeeping did not, so an ordinary error would make
	// a completed, irreversible delivery indistinguishable from one that never happened.
	//
	// So failures are REPORTED in the outcome instead of returned, and the exit code stays success. That
	// satisfies both halves of U6's sentence: failure never authorizes replay (the ledger stays consumed),
	// and failure evidence never disappears (structured fields plus caller-rendered warnings).
	// ORDER IS PART OF THE CONTRACT: consume (above) -> DELIVERED directive -> status poll. The directive
	// may only claim delivery after the consume that makes it true, and the poll observes the world after
	// both.
	if publishDeliveredDirective != nil {
		if err := publishDeliveredDirective(assessment, supervisor, currentPayload); err != nil {
			outcome.DeliveredDirectiveError = err.Error()
		} else {
			outcome.DeliveredDirectivePublished = true
		}
	}
	if pollSupervisionStatusOnce != nil {
		if err := pollSupervisionStatusOnce(assessment); err != nil {
			outcome.StatusPollError = err.Error()
		} else {
			outcome.StatusPolled = true
		}
	}
	return outcome, nil
}
