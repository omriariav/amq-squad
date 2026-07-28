package cli

import (
	"fmt"
	"strings"
)

// #498 U7: DELIVERY-TIME REVALIDATION OF EVERY BOUND FIELD.
//
// This is the reason the exact binding is persisted at all. dev-2's argument for full-depth U7 was that
// "recovery/delivery cannot revalidate fields the record never persists" -- so persisting them without
// revalidating them would satisfy the letter of the schema and none of its purpose. The record is the
// recovery contract; this is where the contract is enforced.
//
// WHAT THIS CHECKS THAT THE U5 GATES DO NOT. The U5 delivery-time gates ask whether the WORLD still matches
// the assessment: is the launch generation unchanged, is the pane still live, managed and idle, is the
// assessment still fresh. This asks a different question: does the RESERVED CLAIM still describe the
// assessment we are about to act on. Those come apart precisely in the case that matters -- a reservation
// written for one pause, and a delivery driven by a different assessment that happens to pass every liveness
// check. The pane can be perfectly alive and still be the wrong pane for THIS claim.
//
// EVERY FIELD THE NATIVE PATH PERSISTS IS COMPARED -- stated that precisely because the previous version of
// this comment said "every persisted field" while the code compared the nested block plus three flat fields,
// and dev-2 was right to call that the convenient-subset failure the sentence disclaims. A revalidation that
// checks a subset tells an operator the binding was verified while leaving the unchecked fields free to
// disagree, which is the shape of the payload-drift gate before it compared the digest AND the modtime.
//
// WHAT IS DELIBERATELY NOT COMPARED, so the absence is a recorded finding rather than an oversight:
// MemberSession/MemberCWD/MemberBinary, GoalDigest, the four Original* fields, and the Team record
// digest/modtime are REDELIVERY-PATH fields that a native reservation never populates. Comparing them would
// compare two blanks and report agreement -- a pass manufactured by mutual absence, which is worse than no
// check because it reads as coverage. TransitionID is not compared here either: it is the key the record is
// FILED under, and publication already proves it matches both the filename and the canonical path, so a
// disagreement cannot reach this function.
//
// LaunchStartedAt IS a genuine gap: eligibility requires it nonzero, so a native assessment always has one,
// but the executor does not persist it and this function therefore cannot check it. Named here rather than
// quietly omitted; it is out of scope for the four ruled blockers and belongs in a follow-up.
func revalidateNativeBindingAgainstAssessment(record resumeGoalTransitionRecord, assessment GoalSupervisionAssessment) error {
	// THE KIND FIRST, because everything below interprets the record AS a native claim.
	//
	// A record read back from disk can be any kind. Revalidating a redeliver record against a native
	// assessment would compare a nil-or-absent binding field by field and report agreement wherever both
	// sides happened to be blank -- a pass produced by two absences rather than by a match. Nothing
	// downstream re-asks this question, so it has to be asked here.
	if kind := recoveryTransitionKind(strings.TrimSpace(record.RecoveryKind)); kind != recoveryTransitionKindNativeGoalResume {
		return fmt.Errorf(
			"reserved claim kind %q is not %q: only a native goal-resume reservation carries the exact binding this revalidation is about, so a claim of any other kind cannot authorize a native delivery",
			record.RecoveryKind, recoveryTransitionKindNativeGoalResume)
	}
	if record.NativeBinding == nil {
		return fmt.Errorf("reserved native claim carries no exact binding: it cannot be revalidated, so it cannot authorize delivery")
	}
	bound := record.NativeBinding

	// Compared by EXACT EQUALITY after trimming, never by substring or prefix. Each row names the field so a
	// refusal sends an operator to the disagreeing value rather than to "the binding changed".
	for _, row := range []struct {
		field  string
		bound  string
		actual string
		why    string
	}{
		{"namespace id", bound.NamespaceID, assessment.Binding.NamespaceID,
			"a claim revalidated against a foreign namespace would authorize delivery into a session it was not written for"},
		{"pane id", bound.PaneID, assessment.Binding.Pane.PaneID,
			"the U5 gates prove the pane is live; this proves it is the pane THIS claim bound"},
		{"goal mode", bound.GoalMode, assessment.Binding.Goal.Mode,
			"a mode change means the delivery contract itself differs from the one that was authorized"},
		{"goal attempt id", bound.GoalAttemptID, assessment.Binding.Goal.AttemptID,
			"resuming a different attempt than the claim was written for is the double-delivery shape wearing a valid reservation"},
		{"goal binding digest", bound.GoalBindingDigest, assessment.Binding.Goal.BindingDigest,
			"the binding is what the goal IS; a different digest is a different goal"},
		{"goal command digest", bound.GoalCommandDigest, assessment.Binding.Goal.CommandDigest,
			"this is the digest the delivered payload is checked against, so a drifted one would authorize different bytes"},
		{"blocker id", bound.BlockerID, assessment.Blocker.ID,
			"the resume is authorized as the resolution of ONE blocker; a different blocker is a different authorization"},
		{"blocker resolution digest", bound.BlockerResolutionDigest, assessment.Blocker.ResolutionDigest,
			"the resolution evidence is what made the resume eligible, so it must be the same evidence at delivery"},
		{"policy mode", bound.PolicyMode, assessment.Policy.Mode,
			"a policy that changed between reservation and delivery may no longer permit automatic action at all"},
	} {
		if strings.TrimSpace(row.bound) != strings.TrimSpace(row.actual) {
			return fmt.Errorf(
				"reserved claim %s %q disagrees with the assessment's %q at delivery time: %s",
				row.field, row.bound, row.actual, row.why)
		}
	}

	// POLICY REVISION IS COMPARED SEPARATELY because it is an int, and a revision BUMP is the signal that an
	// operator changed the policy after this claim was reserved. Equality is the requirement rather than
	// monotonicity: a lower revision is just as much a disagreement, and treating "newer is fine" as
	// acceptable would let a policy change silently re-authorize a claim made under the old one.
	if bound.PolicyRevision != assessment.Policy.Revision {
		return fmt.Errorf(
			"reserved claim policy revision %d disagrees with the assessment's %d at delivery time: the operator changed policy after this claim was reserved, so the authorization it rests on is no longer the current one",
			bound.PolicyRevision, assessment.Policy.Revision)
	}

	// THE FLAT IDENTITIES THE CLAIM ALSO BOUND. Skipping these because they live outside the nested block
	// would be exactly the convenient-subset failure this function exists to avoid -- the field's location in
	// the struct has nothing to do with whether it needs revalidating.
	if strings.TrimSpace(record.Supervisor) == "" {
		return fmt.Errorf("reserved claim carries no supervisor: delivery cannot proceed on a claim nobody is recorded as authorizing")
	}
	// THE FLAT SET, COMPLETED. dev-2's cross-gate found this function comparing the nested block in full
	// while checking only three flat fields, and named it the convenient-subset failure the function's own
	// header comment claims to avoid -- an accurate charge: I wrote "every persisted field is compared"
	// above a loop over one struct. Coverage had followed my attention (I was building the nested block),
	// not the record.
	//
	// EACH ROW IS HERE FOR A DISTINCT FAILURE, not for symmetry with the nested loop:
	for _, row := range []struct {
		field  string
		bound  string
		actual string
		why    string
	}{
		{"project", record.Project, assessment.Binding.Project,
			"a claim revalidated against a different project would authorize delivery into another repository entirely"},
		{"profile", record.Profile, assessment.Binding.Profile,
			"profile selects the namespace directory, so a disagreement means the claim and the assessment do not even share a ledger"},
		{"session", record.Session, assessment.Binding.Session,
			"the session is the workstream the resume belongs to; delivering into a different one resumes work nobody authorized here"},
		{"lead role", record.Role, assessment.Binding.LeadRole,
			"the claim records which lead's pause this is, and a different role is a different actor's work"},
		{"lead handle", record.Handle, assessment.Binding.LeadHandle,
			"role alone is ambiguous across handles, so the handle is what makes the bound identity exact"},
		{"launch id", record.LaunchID, assessment.Binding.LaunchID,
			"a different launch id means the runtime was relaunched: the pane can be perfectly live and belong to a process this claim never authorized"},
		{"launch record digest", record.LaunchRecordDigest, assessment.Binding.LaunchRecordDigest,
			"the launch record is the generation evidence, and a changed digest is a changed generation"},
		{"preclaim fingerprint", record.PreclaimFingerprint, assessment.Fingerprint,
			"the fingerprint is the staleness evidence the claim was reserved under; a rotated one means the claim evidence changed after the reservation"},
	} {
		if strings.TrimSpace(row.bound) != strings.TrimSpace(row.actual) {
			return fmt.Errorf(
				"reserved claim %s %q disagrees with the assessment's %q at delivery time: %s",
				row.field, row.bound, row.actual, row.why)
		}
	}
	// COMPARED AS AN INT, for the same reason PolicyRevision is: a numeric field trimmed into a string
	// comparison would make 0 and "" the same answer, and zero is exactly the absent value that must not
	// silently agree with anything. Equality rather than "newer is fine" -- a modtime that moved in either
	// direction is a launch record that was rewritten, which is precisely the ABA case the digest alone
	// misses when a relaunch reproduces identical bytes.
	if record.LaunchRecordModTime != assessment.Binding.LaunchRecordModTime {
		return fmt.Errorf(
			"reserved claim launch record mod time %d disagrees with the assessment's %d at delivery time: the launch record was rewritten after this claim was reserved, so the generation it was authorized against no longer exists",
			record.LaunchRecordModTime, assessment.Binding.LaunchRecordModTime)
	}
	if strings.TrimSpace(record.PauseGeneration) != strings.TrimSpace(assessment.Binding.PauseGeneration) {
		return fmt.Errorf(
			"reserved claim pause generation %q disagrees with the assessment's %q at delivery time: the pause this claim was written for is not the pause being resumed",
			record.PauseGeneration, assessment.Binding.PauseGeneration)
	}
	// The intentional dual's invariant, re-checked at DELIVERY and not only at construction: the two fields
	// may carry one value only while something enforces that they do, and construction-time enforcement says
	// nothing about a record read back from disk.
	if strings.TrimSpace(record.NewAttemptID) != strings.TrimSpace(bound.GoalAttemptID) {
		return fmt.Errorf(
			"reserved claim lifecycle attempt %q disagrees with its own bound goal attempt %q: a record whose two attempt identities differ is ambiguous evidence about which attempt was authorized",
			record.NewAttemptID, bound.GoalAttemptID)
	}
	if record.SchemaVersion != resumeGoalTransitionSchemaVersion {
		return fmt.Errorf(
			"reserved claim schema version %d is not the current %d: a record its own readers do not accept cannot be revalidated",
			record.SchemaVersion, resumeGoalTransitionSchemaVersion)
	}
	return nil
}
