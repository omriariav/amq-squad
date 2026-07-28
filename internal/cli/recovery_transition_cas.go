package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// PR5 / #498 CAS layer. Three durable publications -- RESERVE, BIND, CONSUME -- each going
// through publishGoalJSON, the one CAS owner (goal_attempt.go:152). There is no new store and no
// new primitive here; this file only decides WHAT is published and WHAT a lost race means.
//
// THE PUBLICATION PRIMITIVE IS link(2), NOT O_EXCL. publishGoalJSON writes a same-directory
// candidate, fsyncs it, and link(2)s it into the canonical path: losers see ErrExist and can never
// observe a partial file. I had assumed O_EXCL and written an enforcement test against that
// assumption; it would have detected nothing here. Recorded so the next reader does not repeat it.
//
// THE THREE PUBLICATIONS DO NOT SHARE LOST-RACE SEMANTICS, and that asymmetry is the whole reason
// this is not one generic helper. Read from the existing redelivery code before writing this:
//
//	RESERVE  !published -> REFUSE. Another actor holds the claim. This is claim-once.
//	BIND     !published -> RE-READ AND VALIDATE, not refuse. A binding may legitimately already
//	         exist from this same actor's earlier attempt; what matters is that the existing one
//	         AGREES about the launch generation. Refusing here would break the idempotent retry
//	         that ensureResumeGoalTransitionBinding relies on (resume_goal.go:954-967).
//	CONSUME  !published -> REFUSE. A concurrent consumption means someone else completed the
//	         delivery, and two consumptions of one claim is the double-delivery signature.
//
// A single "publish or fail" helper across all three would look like a simplification and would
// silently convert bind's legitimate idempotence into a hard failure on every retry. The
// uniformity is the bug, not the duplication.

// recoveryReservation pairs the record with the EXACT path it was published to.
//
// The path comes from newRecoveryTransitionRecord and is never recomputed here. A reserver that
// derived its own path would be a second path owner, and a claim written where no scanner looks
// exists on disk while blocking nothing -- the r6 failure at the container level.
type recoveryReservation struct {
	Record resumeGoalTransitionRecord
	Path   string
}

// reserveRecoveryTransition publishes the reservation. First writer wins; a loser REFUSES.
func reserveRecoveryTransition(record resumeGoalTransitionRecord, path string) (recoveryReservation, error) {
	// PUBLICATION CONTRACT, before marshal. Every constructor guard is bypassable by a caller that builds a
	// record itself and calls this directly, so the last step must re-decide rather than trust.
	if err := validateRecoveryTransitionPublication(record, path); err != nil {
		return recoveryReservation{}, err
	}
	payload, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return recoveryReservation{}, fmt.Errorf("marshal recovery transition reservation: %w", err)
	}
	published, err := publishGoalJSON(path, append(payload, '\n'))
	if err != nil {
		return recoveryReservation{}, fmt.Errorf("publish recovery transition reservation: %w", err)
	}
	if !published {
		// NOT an overwrite and NOT a retry. Another actor reserved this pause first, and the
		// contract prefers a refusal with a recovery step over a second audited resume.
		return recoveryReservation{}, fmt.Errorf(
			"recovery transition %s already exists at %s: another actor holds this pause's claim, so a second delivery is refused",
			record.TransitionID, path)
	}
	return recoveryReservation{Record: record, Path: path}, nil
}

// bindRecoveryTransitionGeneration pins the launch generation the claim was made against, so a
// generation change between reservation and delivery is DETECTED rather than assumed away.
//
// This mirrors ensureResumeGoalTransitionBinding deliberately, including its lost-race handling.
func bindRecoveryTransitionGeneration(res recoveryReservation, launchDigest string, launchModTime int64) error {
	boundPath := resumeGoalTransitionBoundPath(res.Path)
	bound := resumeGoalTransitionBound{
		SchemaVersion:       resumeGoalTransitionSchemaVersion,
		TransitionID:        res.Record.TransitionID,
		NewAttemptID:        res.Record.NewAttemptID,
		LaunchRecordDigest:  launchDigest,
		LaunchRecordModTime: launchModTime,
		BoundAt:             time.Now().UTC(),
	}
	payload, err := json.MarshalIndent(bound, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal recovery transition binding: %w", err)
	}
	published, err := publishGoalJSON(boundPath, append(payload, '\n'))
	if err != nil {
		return fmt.Errorf("publish recovery transition binding: %w", err)
	}
	if published {
		return nil
	}
	// A binding already exists. That is NOT a failure by itself -- see the asymmetry note above.
	// What must hold is that it AGREES; a binding recorded against a different launch generation
	// means the runtime moved under us and the claim no longer describes the world it was made in.
	existingBytes, readErr := os.ReadFile(boundPath)
	if readErr != nil {
		// Cannot read the thing that beat us: unknown, therefore refused. An unreadable binding is
		// not an absent one.
		return fmt.Errorf("read concurrent recovery transition binding: %w", readErr)
	}
	var existing resumeGoalTransitionBound
	if err := json.Unmarshal(existingBytes, &existing); err != nil {
		return fmt.Errorf("parse concurrent recovery transition binding: %w", err)
	}
	if err := validateResumeGoalTransitionBound(existing, res.Record, launchDigest, launchModTime); err != nil {
		return fmt.Errorf("launch generation changed after this claim was reserved: %w", err)
	}
	return nil
}

// consumeRecoveryTransition records that the delivery completed. A concurrent consumption REFUSES.
//
// Called ONLY after a successful delivery. On a failed delivery the reservation is deliberately
// left UNCONSUMED, so the pause reads as indeterminate and the next scan refuses rather than
// risking a second audited resume.
// It takes the identity EXPLICITLY rather than a recoveryReservation, and the reason is worth
// stating: the legacy consume path holds only an id and a path, never a record. A
// reservation-shaped parameter would have forced that caller to fabricate a populated
// resumeGoalTransitionRecord literal purely to satisfy the signature -- precisely the literal the
// AST pin forbids, manufactured to feed the pin's own neighbour. A signature that makes a correct
// caller write forbidden code is the wrong signature; I wrote it that way first and the wiring
// caught it.
func consumeRecoveryTransition(transitionID, newAttemptID, path string) error {
	consumed := resumeGoalTransitionConsumed{
		SchemaVersion: resumeGoalTransitionSchemaVersion,
		TransitionID:  transitionID,
		NewAttemptID:  newAttemptID,
		ConsumedAt:    time.Now().UTC(),
	}
	payload, err := json.MarshalIndent(consumed, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal recovery transition consumption: %w", err)
	}
	published, err := publishGoalJSON(resumeGoalTransitionConsumedPath(path), append(payload, '\n'))
	if err != nil {
		return fmt.Errorf("publish recovery transition consumption: %w", err)
	}
	if !published {
		return fmt.Errorf(
			"recovery transition %s was concurrently consumed: two consumptions of one claim is the double-delivery signature, so this is reported rather than ignored",
			transitionID)
	}
	return nil
}
