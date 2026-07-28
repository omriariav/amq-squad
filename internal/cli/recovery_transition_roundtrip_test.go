package cli

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	// ALIAS IS NOT DIRECTORY. There is no internal/runwizard: runwizard is the alias
	// resume_goal.go:21 gives to internal/wizard. I made this exact mistake with squadnamespace
	// earlier, wrote the lesson into recovery_transition.go, and then repeated it here. go vet
	// caught it; go build could not, because this is a _test.go file.
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
)

// PR5 requirement 3, written BEFORE the wiring on purpose.
//
// PR5 routes resume_goal.go's existing redelivery reservation through the new shared constructor.
// That touches another PR's writer, and this test is the thing that makes the touch safe: it
// proves a redeliver reservation built the NEW way is indistinguishable, to redelivery's OWN
// readers, from one built the old way.
//
// Tests-before-the-change is deliberate. Written afterwards, this test would be shaped by
// whatever the wiring happened to produce -- which is how a regression test comes to certify the
// bug it was meant to catch. Written first, the wiring has to satisfy it.
func TestRedeliverReservationRoundTripsThroughItsExistingReaders(t *testing.T) {
	project := t.TempDir()
	const profile = "squad"
	const session = "v2-25-0"
	const attemptID = "attempt-abc"
	const bindingDigest = "digest-def"

	// The record the OLD code built as a struct literal, populated with EVERY field
	// validateResumeGoalTransitionPlan requires. A minimal record would decode fine and fail
	// validation, which is exactly the gap the JSON-only draft had.
	const goalText = "ship it"
	base := resumeGoalTransitionRecord{
		SchemaVersion:         resumeGoalTransitionSchemaVersion,
		Project:               project,
		Profile:               profile,
		Session:               session,
		Role:                  "cto",
		Handle:                "cto",
		MemberSession:         session,
		MemberCWD:             project,
		MemberBinary:          "codex",
		GoalDigest:            digestBytes([]byte(goalText)),
		OriginalAttemptID:     attemptID,
		OriginalBindingDigest: bindingDigest,
		OriginalAttemptDigest: "attempt-digest",
		OriginalClaimDigest:   "claim-digest",
		NewAttemptID:          "attempt-xyz",
		LaunchID:              "launch-1",
		LaunchStartedAt:       time.Now().UTC(),
		TeamRecordDigest:      "team-digest",
		TeamRecordModTime:     1,
		LaunchRecordDigest:    "launch-digest",
		LaunchRecordModTime:   1,
		CreatedAt:             time.Now().UTC(),
	}

	record, path, err := newRecoveryTransitionRecord(recoveryTransitionInput{
		Base:          base,
		Kind:          recoveryTransitionKindRedeliver,
		AttemptID:     attemptID,
		BindingDigest: bindingDigest,
	})
	if err != nil {
		t.Fatalf("constructor refused a well-formed redeliver input: %v", err)
	}

	// PROPERTY 1a: the constructor and redelivery's derivation agree -- ONE DERIVATION OWNER.
	// This is what the same-helper assertion proves, and no more. My earlier claim that it also
	// proved formula stability was BACKWARDS: if the constructor and the helper drifted together,
	// this comparison would still pass.
	wantID := resumeGoalTransitionID(attemptID, bindingDigest)
	if record.TransitionID != wantID {
		t.Errorf("redeliver identity changed: got %q, want the canonical %q.\n"+
			"Redelivery's readers compute this id independently; a different one makes every "+
			"existing reservation unfindable.", record.TransitionID, wantID)
	}

	// PROPERTY 1b: the GOLDEN COMPATIBILITY VECTOR -- a fixed literal, computed once out of band
	// and never at runtime. THIS is what catches sha/input-order/separator drift, and that drift
	// is the failure that would strand every persisted legacy record: reservations already on disk
	// become unfindable while every same-helper assertion keeps passing.
	//
	// sha256("attempt-abc" + NUL + "digest-def"), matching resumeGoalTransitionID's formula.
	// If this literal is wrong, the test fails on its first run -- which is the point. A wrong
	// golden vector announces itself; a missing one hides a migration break.
	const goldenID = "663df61cf81c30cb48238b02b058d9225af2a7d7cb867edec2128db813f46af3"
	if wantID != goldenID {
		t.Errorf("the legacy id derivation CHANGED: %q != golden %q.\n"+
			"Every reservation already on disk was written under the golden value and is now "+
			"unfindable. This is a migration, not a refactor.", wantID, goldenID)
	}

	// PROPERTY 2: the PATH is the one redelivery's readers compute. Same reasoning -- derived
	// through resumeGoalTransitionPath, not assembled here, so a change in either place fails.
	wantPath, pathErr := resumeGoalTransitionPath(project, profile, session, wantID)
	if pathErr != nil {
		t.Fatalf("canonical redeliver path: %v", pathErr)
	}
	if path != wantPath {
		t.Errorf("redeliver path changed: got %q, want %q.\nA reservation written elsewhere is "+
			"invisible to the readers that look for it -- present on disk and undiscoverable.", path, wantPath)
	}

	// PROPERTY 3: it must NOT acquire PR5's supervision fields. The per-kind contract says this
	// path cannot honestly have a pause generation, and a record carrying one would be
	// indistinguishable from a supervision claim to anything keyed on that field.
	if strings.TrimSpace(record.PauseGeneration) != "" {
		t.Errorf("redeliver record acquired a pause generation (%q); this path holds no assessment, "+
			"so any value here was recomputed or invented", record.PauseGeneration)
	}
	if record.RecoveryKind != string(recoveryTransitionKindRedeliver) {
		t.Errorf("redeliver record kind = %q, want %q -- the kind must be explicit in the body even "+
			"though the filename is the legacy one", record.RecoveryKind, recoveryTransitionKindRedeliver)
	}

	// PROPERTY 4: it passes redelivery's ACTUAL production validator.
	//
	// My first draft marshalled and unmarshalled JSON and called that a round trip. dev-2 was
	// right that it proves nothing: Go IGNORES unknown fields, so a constructor-built record can
	// decode cleanly while violating delivery validation. Checking against the real validator
	// found that immediately -- validateResumeGoalTransitionPlan requires NewAttemptID, LaunchID,
	// LaunchStartedAt, CreatedAt, MemberCWD, MemberBinary, both record digests and both mod-times,
	// and requires NewAttemptID != OriginalAttemptID. My minimal record set NONE of them, so the
	// JSON test would have certified a record production REFUSES.
	//
	// The validator is called directly rather than reimplemented, so it cannot drift from the one
	// redelivery uses.
	plan := runwizard.ResumeGoalPlan{
		TransitionID:      record.TransitionID,
		LeadRole:          base.Role,
		LeadHandle:        base.Handle,
		Goal:              goalText,
		OriginalAttemptID: attemptID,
		BindingDigest:     bindingDigest,
		AttemptDigest:     base.OriginalAttemptDigest,
		ClaimDigest:       base.OriginalClaimDigest,
	}
	if err := validateResumeGoalTransitionPlan(record, project, profile, session, plan); err != nil {
		t.Errorf("a constructor-built redeliver record must satisfy redelivery's OWN validator: %v.\n"+
			"If this fails, the wiring changed what redelivery records, not merely how it builds them.", err)
	}

	// PROPERTY 5: the scan recognises it, with the kind the FILENAME implies. The legacy filename
	// carries no kind, so recognition must supply redeliver -- and if it did not, a redelivery
	// reservation would be invisible to supervision, which is the forbidden case.
	parsed, recognition := recognizeRecoveryTransitionName(filepath.Base(path))
	if recognition != recoveryNameRecognized {
		t.Fatalf("the scan does not recognise a canonical redeliver reservation (%v)", recognition)
	}
	if !parsed.Legacy || parsed.Kind != recoveryTransitionKindRedeliver {
		t.Errorf("recognition = %+v; a legacy-named reservation must be classified redeliver, or "+
			"supervision resume would not see a live redelivery claim", parsed)
	}
}

// The negative half of the per-kind contract, in the same file because it is the same invariant
// seen from the other side: a redeliver input carrying a pause generation is REFUSED.
//
// Without this, "redeliver must not carry a pause generation" is a property of the inputs I chose
// to test rather than of the constructor.
func TestRedeliverInputCarryingAPauseGenerationIsRefused(t *testing.T) {
	_, _, err := newRecoveryTransitionRecord(recoveryTransitionInput{
		Base:            resumeGoalTransitionRecord{Project: t.TempDir(), Profile: "squad", Session: "v2-25-0"},
		Kind:            recoveryTransitionKindRedeliver,
		AttemptID:       "attempt-abc",
		BindingDigest:   "digest-def",
		PauseGeneration: "pause-gen-from-somewhere",
	})
	if err == nil {
		t.Fatal("a redeliver input carrying a pause generation must be refused: this path holds no " +
			"assessment, so the value was recomputed or invented")
	}
	if !strings.Contains(err.Error(), "must NOT carry a pause generation") {
		t.Errorf("refusal must name the reason: %v", err)
	}
}
