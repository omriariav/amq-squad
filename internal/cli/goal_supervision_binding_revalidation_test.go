package cli

import (
	"strings"
	"testing"
	"time"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// #498 U7: FALSIFIERS FOR DELIVERY-TIME REVALIDATION.
//
// Each row makes the ASSESSMENT disagree with the reserved claim in exactly ONE field. That direction is
// deliberate: mutating the record would test that we notice a corrupted file, which is a different property.
// The one that matters is a claim reserved for one observation being delivered against another -- and the
// U5 gates cannot catch it, because a pane can be perfectly live, managed and idle while being the wrong
// pane for THIS claim.
func revalidationFixture(t *testing.T) (resumeGoalTransitionRecord, GoalSupervisionAssessment) {
	t.Helper()
	const profile = "squad"
	const session = "v2-25-0"
	const attemptID = "attempt-abc"
	project := t.TempDir()
	ns := squadnamespace.ID(profile, session)
	now := time.Now().UTC()

	assessment := GoalSupervisionAssessment{
		Fresh: true, Eligible: true, ObservedAt: now, FreshUntil: now.Add(time.Hour),
		Policy:      team.GoalSupervisionPolicyStatus{Mode: team.GoalSupervisionSafeAuto, Revision: 3, Source: "test"},
		Fingerprint: "fingerprint-1",
		Blocker:     GoalSupervisionBlockerEvidence{ID: "blocker-1", Known: true, Resolved: true, ResolutionDigest: "resolution-digest-1"},
		Binding: GoalSupervisionBinding{
			Project: project, Profile: profile, Session: session, NamespaceID: ns,
			LeadRole: "cto", LeadHandle: "cto",
			PauseGeneration: "pause-gen-1",
			// THE LAUNCH GENERATION the assessment observed. Present on both sides of the comparison, or the
			// launch rows below would drift a field the record never carried and refuse for absence instead
			// of for disagreement.
			LaunchID:           "launch-1",
			LaunchRecordDigest: "launch-record-digest-1",
			// Nonzero because eligibility requires it: a zero here would make the fixture describe a state
			// no eligible assessment can reach.
			LaunchRecordModTime: 1700000000,
			Pane:                GoalSupervisionPaneIdentity{PaneID: "%7", Managed: true},
			Goal: GoalSupervisionGoalIdentity{
				// THE REAL MODE. This fixture said "native", which no production path emits: eligibility
				// (goal_supervision.go:334) requires exactly "native_goal_blocked", so the old value pinned
				// behaviour for a state the system cannot produce -- and a test asserting an unreachable
				// state actively defends whatever the reachable one does instead.
				Mode: fixtureNativeGoalMode, AttemptID: attemptID,
				BindingDigest: "binding-digest-1", CommandDigest: "command-digest-1",
			},
		},
	}
	record := resumeGoalTransitionRecord{
		SchemaVersion: resumeGoalTransitionSchemaVersion,
		Project:       project, Profile: profile, Session: session,
		// The flat identities the claim also bound. Absent before, which meant the newly-compared rows
		// would have refused on the ANTI-VACUITY control rather than on their own drift.
		Role: "cto", Handle: "cto",
		LaunchID:            "launch-1",
		LaunchRecordDigest:  "launch-record-digest-1",
		LaunchRecordModTime: 1700000000,
		RecoveryKind:        string(recoveryTransitionKindNativeGoalResume),
		PauseGeneration:     "pause-gen-1",
		PreclaimFingerprint: "fingerprint-1",
		Supervisor:          "cto",
		NewAttemptID:        attemptID,
		NativeBinding: &resumeGoalNativeBinding{
			NamespaceID: ns, PaneID: "%7", GoalMode: fixtureNativeGoalMode, GoalAttemptID: attemptID,
			GoalBindingDigest: "binding-digest-1", GoalCommandDigest: "command-digest-1",
			BlockerID: "blocker-1", BlockerResolutionDigest: "resolution-digest-1",
			PolicyMode: team.GoalSupervisionSafeAuto, PolicyRevision: 3,
		},
	}
	return record, assessment
}

// THE ANTI-VACUITY CONTROL. Without it every refusal row below would pass against an implementation that
// refused everything, including the matching case production always presents.
func TestRevalidationPassesWhenTheClaimStillMatchesTheAssessment(t *testing.T) {
	record, assessment := revalidationFixture(t)
	if err := revalidateNativeBindingAgainstAssessment(record, assessment); err != nil {
		t.Fatalf("a claim that still matches its assessment must revalidate: %v", err)
	}
}

// ONE ROW PER BOUND FIELD. Exhaustive on purpose: a revalidation that checks a convenient subset reports the
// binding as verified while leaving the unchecked fields free to disagree.
func TestRevalidationRefusesEveryDisagreeingBoundField(t *testing.T) {
	for _, row := range []struct {
		name    string
		drift   func(*GoalSupervisionAssessment)
		wantErr string
	}{
		{"namespace id", func(a *GoalSupervisionAssessment) { a.Binding.NamespaceID = "squad/other" }, "namespace id"},
		{"pane id", func(a *GoalSupervisionAssessment) { a.Binding.Pane.PaneID = "%999" }, "pane id"},
		{"goal mode", func(a *GoalSupervisionAssessment) { a.Binding.Goal.Mode = "prompt" }, "goal mode"},
		{"goal attempt id", func(a *GoalSupervisionAssessment) { a.Binding.Goal.AttemptID = "attempt-other" }, "goal attempt id"},
		{"goal binding digest", func(a *GoalSupervisionAssessment) { a.Binding.Goal.BindingDigest = "drifted" }, "goal binding digest"},
		{"goal command digest", func(a *GoalSupervisionAssessment) { a.Binding.Goal.CommandDigest = "drifted" }, "goal command digest"},
		{"blocker id", func(a *GoalSupervisionAssessment) { a.Blocker.ID = "blocker-other" }, "blocker id"},
		{"blocker resolution digest", func(a *GoalSupervisionAssessment) { a.Blocker.ResolutionDigest = "drifted" }, "blocker resolution digest"},
		{"policy mode", func(a *GoalSupervisionAssessment) { a.Policy.Mode = "manual" }, "policy mode"},
		// A REVISION BUMP is the operator changing policy after the claim was reserved.
		{"policy revision bumped", func(a *GoalSupervisionAssessment) { a.Policy.Revision = 4 }, "policy revision"},
		// A LOWER revision is just as much a disagreement. Equality is the rule, not monotonicity: treating
		// "newer is fine" as acceptable would let a policy change silently re-authorize an old claim.
		{"policy revision lowered", func(a *GoalSupervisionAssessment) { a.Policy.Revision = 2 }, "policy revision"},
		{"pause generation", func(a *GoalSupervisionAssessment) { a.Binding.PauseGeneration = "pause-other" }, "pause generation"},

		// THE FLAT SET dev-2's cross-gate found uncompared. One row per field, each drifted ALONE, because a
		// row that drifted two fields would stay green if either check were deleted -- and the whole reason
		// these rows exist is that the checks were absent while the function claimed to be exhaustive.
		{"project", func(a *GoalSupervisionAssessment) { a.Binding.Project = "/other/project" }, "project"},
		{"profile", func(a *GoalSupervisionAssessment) { a.Binding.Profile = "other-profile" }, "profile"},
		{"session", func(a *GoalSupervisionAssessment) { a.Binding.Session = "v9-99-9" }, "session"},
		{"lead role", func(a *GoalSupervisionAssessment) { a.Binding.LeadRole = "worker" }, "lead role"},
		// Role and handle drift SEPARATELY on purpose: the fixture deliberately sets both to "cto", so a
		// single row changing one value could be satisfied by a check that compared the other. This pair only
		// distinguishes the two checks because each row leaves the other field matching.
		{"lead handle", func(a *GoalSupervisionAssessment) { a.Binding.LeadHandle = "cto-2" }, "lead handle"},
		// A DIFFERENT LAUNCH ID is a relaunch: the pane can be live, managed and idle while belonging to a
		// process this claim never authorized. This is the drift the U5 gates cannot see.
		{"launch id", func(a *GoalSupervisionAssessment) { a.Binding.LaunchID = "launch-2" }, "launch id"},
		{"launch record digest", func(a *GoalSupervisionAssessment) { a.Binding.LaunchRecordDigest = "drifted" },
			"launch record digest"},
		// THE ABA CASE: modtime moves while the digest is identical, so only the modtime row can catch it.
		// This is the same asymmetry M-U5b proved for the U5 generation gate, applied to the persisted claim.
		{"launch record mod time", func(a *GoalSupervisionAssessment) { a.Binding.LaunchRecordModTime = 1800000000 },
			"launch record mod time"},
		// AND THE OTHER DIRECTION, because equality is the rule rather than monotonicity: a modtime that went
		// BACKWARDS is a rewritten launch record too, and "newer is fine" would accept it.
		{"launch record mod time lowered", func(a *GoalSupervisionAssessment) { a.Binding.LaunchRecordModTime = 1600000000 },
			"launch record mod time"},
		{"preclaim fingerprint", func(a *GoalSupervisionAssessment) { a.Fingerprint = "fingerprint-rotated" },
			"preclaim fingerprint"},
	} {
		record, assessment := revalidationFixture(t)
		row.drift(&assessment)
		err := revalidateNativeBindingAgainstAssessment(record, assessment)
		if err == nil {
			t.Errorf("%s: a disagreeing bound field must REFUSE delivery", row.name)
			continue
		}
		if !strings.Contains(err.Error(), row.wantErr) {
			t.Errorf("%s: refusal must name the field, got: %v", row.name, err)
		}
	}
}

// THE RECORD'S OWN INTERNAL INVARIANTS, re-checked at delivery rather than trusted from construction.
// Construction-time enforcement says nothing about a record read back from disk.
func TestRevalidationRefusesAnInternallyInconsistentClaim(t *testing.T) {
	for _, row := range []struct {
		name    string
		corrupt func(*resumeGoalTransitionRecord)
		wantErr string
	}{
		// THE KIND MUST BE NATIVE. A redeliver record reaching this function would be compared field by field
		// against a native assessment, and wherever both sides were blank it would report agreement -- a pass
		// manufactured by mutual absence. The binding block is left INTACT in this row so the refusal can only
		// come from the kind check: nulling it too would be caught by the row below and prove nothing.
		{"redeliver kind", func(r *resumeGoalTransitionRecord) {
			r.RecoveryKind = string(recoveryTransitionKindRedeliver)
		}, "kind"},
		{"blank kind", func(r *resumeGoalTransitionRecord) { r.RecoveryKind = "" }, "kind"},
		{"absent binding block", func(r *resumeGoalTransitionRecord) { r.NativeBinding = nil }, "carries no exact binding"},
		{"missing supervisor", func(r *resumeGoalTransitionRecord) { r.Supervisor = "" }, "carries no supervisor"},
		// The intentional dual disagreeing WITH ITSELF: two attempt identities in one record that differ is
		// ambiguous evidence about which attempt was authorized.
		{"dual attempt identities disagree", func(r *resumeGoalTransitionRecord) { r.NewAttemptID = "attempt-other" },
			"disagrees with its own bound goal attempt"},
		{"stale schema version", func(r *resumeGoalTransitionRecord) { r.SchemaVersion = 0 }, "schema version"},
	} {
		record, assessment := revalidationFixture(t)
		row.corrupt(&record)
		err := revalidateNativeBindingAgainstAssessment(record, assessment)
		if err == nil {
			t.Errorf("%s: must REFUSE", row.name)
			continue
		}
		if !strings.Contains(err.Error(), row.wantErr) {
			t.Errorf("%s: refusal must name the cause, got: %v", row.name, err)
		}
	}
}
