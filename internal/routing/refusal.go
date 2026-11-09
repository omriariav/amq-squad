package routing

import (
	"github.com/omriariav/amq-squad/v3/internal/registry"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// RefusalWorkFailure classifies every reason code the commit step can end
// an attempt with, and the block reasons that ask for a decision (brief
// 3.1.1-2 item 1). The three retry classes are: a work failure escalates and
// counts; a policy refusal does neither; an expired lease counts without
// escalating. This table distinguishes work failures from policy refusals;
// attemptOutcomeEffect gives expiry its own row. One table: a row fails when
// a code seatcommit can emit is not in it, so a new code is never a failure
// by default.
var RefusalWorkFailure = map[string]bool{
	// The policy refusals: the grant or the declared set refused the diff.
	seatcommit.ReasonSizeCap:    false,
	seatcommit.ReasonOutside:    false,
	seatcommit.ReasonProtected:  false,
	seatcommit.ReasonGrowthGate: false,
	seatcommit.ReasonNested:     false,
	seatcommit.ReasonLinkTarget: false, // a changed symlink's tree destination is refused (#851)
	// The binding deferrals and refusals.
	seatcommit.ReasonPRPending:        false,
	seatcommit.ReasonPRBoundElsewhere: false,
	// The seat branch's data guards and the harness's own state.
	seatcommit.ReasonBaseUnrecorded:       false,
	seatcommit.ReasonWorktree:             false,
	seatcommit.ReasonRecordedDiffMismatch: false,
	seatcommit.ReasonLeaseTaskMismatch:    false,
	seatcommit.ReasonLedgerTampered:       false,
	// The ledger's writer version or schema is an install or writer bug,
	// never the seat's work: no pool escalation and no attempt counted.
	seatcommit.ReasonNewerWriter:              false,
	seatcommit.ReasonSchemaMismatch:           false,
	seatcommit.ReasonPushTargetUnacknowledged: false,
	seatcommit.ReasonEvidenceRefSymbolic:      false,
	task.BlockVerdictTreeMismatch:             false,
	// The typed widen's size dry run found no sealed snapshot of the stopped
	// attempt, or one of another attempt (#853): the harness's data.
	seatcommit.ReasonWidenSnapshotMissing:  false,
	seatcommit.ReasonWidenSnapshotMismatch: false,
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
	// Blocks that ask for a decision rather than report a failure: a seat's
	// typed widen request (#844; the lead widens and the task returns to
	// pending) and the harness's unprepared task branch.
	task.ReasonWidenRequested:            false,
	scheduler.ReasonTaskBranchUnprepared: false,
	// The work failed: its verifier said fail, ran past its bound, or the
	// done report carried no verdict of a gated class.
	task.BlockVerdictFailed:          true,
	task.BlockVerdictMissing:         true,
	seatcommit.ReasonVerifierTimeout: true,
	seatcommit.ReasonVerifierOutput:  true,
}

type retryEffect struct{ escalate, count bool }

// attemptOutcomeEffect classifies outcomes whose retry effect does not depend
// on a refusal detail. Expired is deliberately counted without escalation.
var attemptOutcomeEffect = map[string]retryEffect{
	task.OutcomeFailed:  {escalate: true, count: true},
	task.OutcomeExpired: {count: true},
}

func effect(a task.Attempt) retryEffect {
	if got, ok := attemptOutcomeEffect[a.Outcome]; ok {
		return got
	}
	switch a.Outcome {
	case task.OutcomeCommitRefused, task.OutcomeBlocked:
		if work, ok := RefusalWorkFailure[a.Detail]; ok {
			return retryEffect{escalate: work, count: work}
		}
		work := a.Outcome == task.OutcomeBlocked
		return retryEffect{escalate: work, count: work}
	}
	return retryEffect{}
}

func failure(a task.Attempt) bool   { return effect(a).count }
func escalates(a task.Attempt) bool { return effect(a).escalate }
