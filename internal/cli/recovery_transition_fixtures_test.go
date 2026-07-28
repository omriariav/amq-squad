package cli

import (
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
)

// ONE COMPLETE NATIVE FIXTURE, shared, because the alternative produced two defect classes in one day.
//
// dev-2's cross-gate found five native fixtures carrying an INCOMPLETE exact binding and two carrying a
// GoalMode that production can never emit. Both are the same root failure: each fixture was written by
// hand next to the row that needed it, so each one encoded whatever the author was thinking about at that
// moment, and a row meant to prove ONE defect could refuse for an unrelated missing field instead.
//
// THE SINGLE-DEFECT PROPERTY IS THE POINT. A falsifier proves what it names only if everything EXCEPT the
// named defect is valid. Hand-built fixtures cannot maintain that as the constructor's requirements grow:
// every new required field silently converts some existing rows into rows that refuse for the new reason.
// With one complete fixture, a row states its defect as a single explicit override of a known-good value,
// and adding a required field updates every row at once.
//
// USE: take the complete input, then break EXACTLY ONE thing.
//
//	in := completeNativeTransitionInput(project, profile, session, attemptID)
//	in.Supervisor = ""  // the single defect this row is about

// fixtureNativeGoalMode is the mode a blocked native goal ACTUALLY carries.
//
// PINNED TO PRODUCTION, and stated as a citation rather than a constant import because THERE IS NO
// EXPORTED CONSTANT: goal_supervision.go:334 compares b.Goal.Mode against the bare literal
// "native_goal_blocked" inside the goal_attempt eligibility reason. Two fixtures previously used "native",
// which no production path emits -- so they pinned behaviour for a state the system cannot reach, and a
// test asserting an unreachable state actively defends whatever the real state does instead.
//
// This is a latent drift risk I am NOT fixing here (the speed directive scopes me to the four blockers):
// production owns this value as a literal, so nothing mechanically ties this fixture to it. Flagged in the
// delivery as a follow-up rather than silently absorbed.
const fixtureNativeGoalMode = "native_goal_blocked"

// The launch generation the claim is written against.
//
// THE VALUES DELIBERATELY MATCH the u6 assessment fixture's launch generation
// (goal_supervision_u6_falsifiers_test.go:62-64) rather than introducing a second vocabulary. A record
// built from these constants and revalidated against that assessment must AGREE; had I invented
// "launch-record-digest-1" here while the assessment said "launch-digest-1", every such row would refuse
// on launch drift and I would have manufactured a fixture disagreement that looks like a real finding.
const (
	fixtureLaunchID              = "launch-1"
	fixtureLaunchDigest          = "launch-digest-1"
	fixtureLaunchModTime   int64 = 1700000000
	fixturePauseGeneration       = "pause-gen-1"
	fixtureFingerprint           = "fingerprint-1"
	fixtureSupervisor            = "supervisor-1"
)

// nativeTransitionBase is the Base a native reservation legitimately carries.
//
// It exists because the flat launch generation is now REQUIRED for native, and eleven call sites shared
// the identical three-field Base literal. Adding the fields inline at each site would have made the next
// required field an eleven-site edit again -- and the fixture-drift defects dev-2 found are exactly what
// happens when N hand-maintained copies of one fixture fall out of step.
func nativeTransitionBase(project, profile, session string) resumeGoalTransitionRecord {
	return resumeGoalTransitionRecord{
		Project: project, Profile: profile, Session: session,
		// LEAD IDENTITY. Required by the shared contract as of HOLD 2 blocker 3: they were compared by
		// revalidation but required by nothing, so a blank record and a blank assessment agreed BY MUTUAL
		// ABSENCE. Adding them here rather than at eleven call sites is the whole reason this helper exists.
		Role: "cto", Handle: "cto",
		LaunchID:            fixtureLaunchID,
		LaunchRecordDigest:  fixtureLaunchDigest,
		LaunchRecordModTime: fixtureLaunchModTime,
	}
}

// redeliverTransitionBase is the Base a redeliver reservation legitimately carries.
//
// It exists because HOLD 2 blocker 2 required the redeliver contract to be DEFINED: the record must carry the
// fields its own identity is derived from (OriginalAttemptID + OriginalBindingDigest) so publication can
// RECOMPUTE that identity instead of comparing it to itself, plus the NewAttemptID the constructor never set.
//
// The identity sources are taken as parameters and must be the SAME values passed as the input's
// AttemptID/BindingDigest -- the contract recomputes resumeGoalTransitionID from the record's copies, so a
// fixture whose Base disagreed with its input would refuse on the identity check rather than on whatever the
// row is about.
func redeliverTransitionBase(project, profile, session, originalAttemptID, bindingDigest string) resumeGoalTransitionRecord {
	return resumeGoalTransitionRecord{
		Project: project, Profile: profile, Session: session,
		Role: "cto", Handle: "cto",
		OriginalAttemptID:     originalAttemptID,
		OriginalBindingDigest: bindingDigest,
		// The NEW attempt being created -- deliberately DIFFERENT from the original, because two redelivery
		// validators require exactly that (validateResumeGoalTransitionPlan and the explicit-CAS branch of
		// validateResumeGoalTransitionForDelivery: "transition reuses the original attempt id").
		NewAttemptID: "attempt-new",
	}
}

// completeNativeTransitionInput returns an input the constructor MUST accept.
//
// The NamespaceID is derived with squadnamespace.ID from the same profile/session pair, because the
// constructor requires that exact equality -- a hand-written namespace string would make every row using
// this fixture refuse on the namespace check instead of the thing it is testing.
// It reuses validNativeBinding (goal_supervision_u6_falsifiers_test.go:874) for the nested block rather
// than defining a second all-valid binding. A duplicate would be a second owner of one fixture, and the
// two would disagree the first time either was extended -- which is the same one-fact-two-owners shape
// that produces double delivery in production, demoted to test scaffolding.
func completeNativeTransitionInput(project, profile, session, attemptID string) recoveryTransitionInput {
	namespaceID := squadnamespace.ID(profile, session)
	base := nativeTransitionBase(project, profile, session)
	base.Role, base.Handle = "lead", "cto"
	return recoveryTransitionInput{
		Base:                base,
		Kind:                recoveryTransitionKindNativeGoalResume,
		NamespaceID:         namespaceID,
		AttemptID:           attemptID,
		PauseGeneration:     fixturePauseGeneration,
		PreclaimFingerprint: fixtureFingerprint,
		Supervisor:          fixtureSupervisor,
		NativeBinding:       validNativeBinding(namespaceID, attemptID),
	}
}
