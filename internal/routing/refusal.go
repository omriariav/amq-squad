package routing

import (
	"github.com/omriariav/amq-squad/v3/internal/registry"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// RefusalWorkFailure classifies every reason code the commit step can end
// an attempt with (brief 3.1.1-2 item 1): true when the code says the work
// failed (its verifier failed it), false when the grant, the declared set,
// a binding or the seat branch's data refused the diff. Only a work failure
// moves the retry one tier up and counts toward MaxAttempts; any other
// refusal retries on the same tier, after the lead's reset, widen or a
// grant change (the commit step blocks the task; nothing re-dispatches it
// alone). One table: a row fails when a code seatcommit can emit is not in
// it, so a new code is never a failure by default.
var RefusalWorkFailure = map[string]bool{
	// The policy refusals: the grant or the declared set refused the diff.
	seatcommit.ReasonSizeCap:    false,
	seatcommit.ReasonOutside:    false,
	seatcommit.ReasonProtected:  false,
	seatcommit.ReasonGrowthGate: false,
	seatcommit.ReasonNested:     false,
	// The binding deferrals and refusals.
	seatcommit.ReasonPRPending:        false,
	seatcommit.ReasonPRBoundElsewhere: false,
	// The seat branch's data guards and the harness's own state.
	seatcommit.ReasonBaseUnrecorded:           false,
	seatcommit.ReasonWorktree:                 false,
	seatcommit.ReasonRecordedDiffMismatch:     false,
	seatcommit.ReasonLeaseTaskMismatch:        false,
	seatcommit.ReasonLedgerTampered:           false,
	seatcommit.ReasonPushTargetUnacknowledged: false,
	seatcommit.ReasonEvidenceRefSymbolic:      false,
	task.BlockVerdictTreeMismatch:             false,
	// The grant, the remote, the step and the verifier's own harness.
	seatcommit.ReasonGrants:              false,
	registry.PinMissing:                  false,
	registry.PinMismatch:                 false,
	seatcommit.ReasonPushRejected:        false,
	seatcommit.ReasonCommitFailed:        false,
	seatcommit.ReasonStepCancelled:       false,
	seatcommit.ReasonVerifierStart:       false,
	seatcommit.ReasonVerifierInterrupted: false,
	seatcommit.ReasonVerifierCancelled:   false,
	// A widening's own refusals (seatcommit.Widen): never an attempt's
	// outcome, classified so the table names every code seatcommit emits.
	"widen_invalid":      false,
	"widen_check_failed": false,
	// The work failed: its verifier said fail, ran past its bound, or the
	// done report carried no verdict of a gated class.
	task.BlockVerdictFailed:          true,
	task.BlockVerdictMissing:         true,
	seatcommit.ReasonVerifierTimeout: true,
	seatcommit.ReasonVerifierOutput:  true,
}

// failure reports whether an attempt counts as a failed attempt of the
// work: a retry moves one tier up and it counts toward MaxAttempts. A
// failed or expired attempt is one. A commit refusal or a block counts only
// when RefusalWorkFailure says its code is a work failure; a commit refusal
// with a code the table does not name never counts, and a block with a code
// it does not name (a seat's own block) still does. Unclaimed and released
// attempts are seat or lead events and retry on the same tier.
func failure(a task.Attempt) bool {
	switch a.Outcome {
	case task.OutcomeFailed, task.OutcomeExpired:
		return true
	case task.OutcomeCommitRefused, task.OutcomeBlocked:
		if work, ok := RefusalWorkFailure[a.Detail]; ok {
			return work
		}
		return a.Outcome == task.OutcomeBlocked
	}
	return false
}
