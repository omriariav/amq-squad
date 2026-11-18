package statecontract

// This file declares the reasons a task's block and wait fields are given,
// from the sites that pass them. An entry's Code is a constant used by name,
// never a second string literal. TestReasonsAreTiedToTheirWriters ties each
// entry to its writer sites; docs/v3-spike/330-s1-2b-reasons/sites.md holds
// the file and line of each site.

import (
	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
	"github.com/omriariav/amq-squad/v3/internal/piruntime"
	"github.com/omriariav/amq-squad/v3/internal/registry"
	"github.com/omriariav/amq-squad/v3/internal/routing"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/seatexec"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// ReasonWriter is one place a listed constant is passed as a reason.
type ReasonWriter struct {
	Function string `json:"function"`
	Door     string `json:"door"`
}

// ReasonEntry is one declared constant.
type ReasonEntry struct {
	Code     string         `json:"code"`
	Constant string         `json:"constant"`
	Mark     string         `json:"mark,omitempty"`
	Writers  []ReasonWriter `json:"writers,omitempty"`
}

// OpenDoor is one place a reason enters that no list holds.
type OpenDoor struct {
	Name      string   `json:"name"`
	Functions []string `json:"functions"`
}

// KeptWord is a launch stage or seat-state word the launch path writes as a
// string literal and that never reaches a task: Guard is the function that
// keeps it from scheduler.(LaunchFailure).Code.
type KeptWord struct {
	Word  string `json:"word"`
	Guard string `json:"guard"`
	Why   string `json:"why"`
}

// CallerDoor is a pass-through helper's parameters (scheduler.firstNonEmpty's
// two) that are launch doors only in Functions, the launch path's callers of
// it; other functions call it with other values. NotCodes are the words one
// of those callers passes it that are no code, each with its function and
// why.
type CallerDoor struct {
	Doors     []string     `json:"doors"`
	Functions []string     `json:"functions"`
	NotCodes  []CallerWord `json:"not_codes"`
}

// CallerWord is a literal a launch caller passes a CallerDoor that is no
// launch code: allowed in Function only.
type CallerWord struct {
	Word     string `json:"word"`
	Function string `json:"function"`
	Why      string `json:"why"`
}

// LaunchFamily is the launch path's codes: what a failed launch writes, which
// reaches a task through Door (the pool's tasks wait on it and, when the
// launch fails the same way again, block on it). Constants are each on the
// block and wait lists; Doors are the declared doors of both lists where the
// launch path writes them, and no string literal reaches them but a Kept word.
// Outside are the launch path's codes that never reach a task: the
// seat-prepare refusals, the pi runtime's refusals and the proof server's
// alarm and log codes (OutsideDoors), each on no block or wait list; pi words
// them with the family.
type LaunchFamily struct {
	Door         string        `json:"door"`
	Doors        []string      `json:"doors"`
	CallerDoors  []CallerDoor  `json:"caller_doors"`
	Constants    []string      `json:"constants"`
	Kept         []KeptWord    `json:"kept"`
	OutsideDoors []string      `json:"outside_doors"`
	Outside      []ReasonEntry `json:"outside"`
}

// ReasonList is one vocabulary: open, with its named doors.
type ReasonList struct {
	Open      bool          `json:"open"`
	OpenDoors []OpenDoor    `json:"open_doors"`
	Doors     []string      `json:"doors"`
	Entries   []ReasonEntry `json:"entries"`
}

// BlockReasons returns the declared block reasons.
func BlockReasons() ReasonList {
	return ReasonList{
		Open: true,
		OpenDoors: []OpenDoor{
			{Name: "the launch failure code: the reason code, else the stage", Functions: []string{"scheduler.(LaunchFailure).Code"}},
			{Name: "AMQ launchapi field Reason, copied by seatlaunch.rosterOutcome", Functions: []string{"seatlaunch.rosterOutcome"}},
			{Name: "AMQ launchapi field ReasonCode, copied by seatlaunch.rosterOutcome", Functions: []string{"seatlaunch.rosterOutcome"}},
			{Name: "the pending action kinds joined with a comma", Functions: []string{"seatlaunch.rosterOutcome"}},
			{Name: "the --reason flag of task block", Functions: []string{"cli.runTaskTransition"}},
			{Name: "decoded field launch.ProofReply.Code", Functions: []string{"seatexec.DialProof"}},
			{Name: "decoded field launch.Exec.ReasonCode", Functions: []string{"launch.Decode"}},
			{Name: "decoded field scheduler.LaunchFailure.Stage", Functions: []string{"scheduler.ReadState"}},
			{Name: "decoded field scheduler.LaunchFailure.ReasonCode", Functions: []string{"scheduler.ReadState"}},
			{Name: "field task.Task.BlockReason read back from disk", Functions: []string{"task.snapshotOnce", "task.readDir", "task.recoverJournal"}},
			{Name: "parameter reasonCode of task.(Store).RefuseCommit, which no code outside tests calls", Functions: []string{"task.(Store).RefuseCommit"}},
		},
		Doors: []string{
			"field launch.ProofReply.Code",
			"field piruntime.ResumeRefusal.Code",
			"field scheduler.CommitResult.ReasonCode",
			"field scheduler.Event.ReasonCode",
			"field scheduler.RosterLaunchError.Stage",
			"field scheduler.RosterOutcome.ReasonCode",
			"field scheduler.RosterOutcome.Stage",
			"field scheduler.Route.Block",
			"field seatcommit.Result.ReasonCode",
			"field seatcommit.Violation.Reason",
			"field seatlaunch.Result.Stage",
			"field seatlaunch.SeatState.ReasonCode",
			"field seatlaunch.SeatState.State",
			"field seatlaunch.proofSeat.refused",
			"local code in scheduler.(*Scheduler).commit",
			"param seatcommit.(Result).refuse#0",
			"param seatexec.asRefusal#0",
			"param seatexec.refusef#0",
			"param seatlaunch.firstNonEmptyStr#1",
			"param task.(Store).Block#2",
			"param task.(Store).Escalate#1",
			"param task.block#1",
			"result registry.OpenPinned#1",
			"result seatcommit.planVerifier#1",
			"result seatlaunch.launchFailure#0",
			"result seatlaunch.launchFailure#1",
		},
		Entries: []ReasonEntry{
			{Code: claudeseat.TokenInsecure, Constant: "claudeseat.TokenInsecure", Writers: []ReasonWriter{{Function: "seatexec.prepare", Door: "param seatexec.refusef#0"}}},
			{Code: registry.PinMismatch, Constant: "registry.PinMismatch", Writers: []ReasonWriter{{Function: "seatcommit.commitAndPush", Door: "param seatcommit.(Result).refuse#0"}, {Function: "registry.OpenPinned", Door: "result registry.OpenPinned#1"}}},
			{Code: registry.PinMissing, Constant: "registry.PinMissing", Writers: []ReasonWriter{{Function: "registry.OpenPinned", Door: "result registry.OpenPinned#1"}}},
			{Code: routing.BlockPinUnresolved, Constant: "routing.BlockPinUnresolved", Writers: []ReasonWriter{{Function: "routing.(*Router).decide", Door: "field scheduler.Route.Block"}}},
			{Code: routing.BlockTreeUnchecked, Constant: "routing.BlockTreeUnchecked", Writers: []ReasonWriter{{Function: "routing.(*Router).decide", Door: "field scheduler.Route.Block"}}},
			{Code: routing.HistoryTampered, Constant: "routing.HistoryTampered", Writers: []ReasonWriter{{Function: "routing.(*Router).holdTampered", Door: "field scheduler.Route.Block"}}},
			{Code: scheduler.GoneProcessExited, Constant: "scheduler.GoneProcessExited", Mark: "traced, guarded at scheduler.(*Scheduler).applyEvents", Writers: []ReasonWriter{{Function: "scheduler.(*Scheduler).checkLiveness", Door: "field scheduler.Event.ReasonCode"}}},
			{Code: scheduler.ReasonCommitFailed, Constant: "scheduler.ReasonCommitFailed", Writers: []ReasonWriter{{Function: "scheduler.(*Scheduler).commit", Door: "local code in scheduler.(*Scheduler).commit"}}},
			{Code: scheduler.ReasonLedgerTampered, Constant: "scheduler.ReasonLedgerTampered", Writers: []ReasonWriter{{Function: "scheduler.(*Scheduler).blockOnTamper", Door: "param task.(Store).Block#2"}}},
			{Code: scheduler.ReasonResourcePresent, Constant: "scheduler.ReasonResourcePresent", Writers: []ReasonWriter{{Function: "seatlaunch.rosterOutcome", Door: "field scheduler.RosterOutcome.ReasonCode"}}},
			{Code: scheduler.ReasonSeatRelaunch, Constant: "scheduler.ReasonSeatRelaunch", Writers: []ReasonWriter{{Function: "scheduler.(*Scheduler).rosterNoIdleSeat", Door: "param task.(Store).Block#2"}}},
			{Code: scheduler.ReasonTaskBranchUnprepared, Constant: "scheduler.ReasonTaskBranchUnprepared", Writers: []ReasonWriter{{Function: "scheduler.(*Scheduler).dispatch", Door: "param task.(Store).Block#2"}}},
			{Code: scheduler.ReasonVerifierInterrupted, Constant: "scheduler.ReasonVerifierInterrupted", Writers: []ReasonWriter{{Function: "scheduler.(*Scheduler).commit", Door: "local code in scheduler.(*Scheduler).commit"}}},
			{Code: seatcommit.ReasonBaseUnrecorded, Constant: "seatcommit.ReasonBaseUnrecorded", Writers: []ReasonWriter{{Function: "seatcommit.run", Door: "param seatcommit.(Result).refuse#0"}}},
			{Code: seatcommit.ReasonGrants, Constant: "seatcommit.ReasonGrants", Writers: []ReasonWriter{{Function: "seatcommit.run", Door: "param seatcommit.(Result).refuse#0"}, {Function: "seatcommit.planVerifier", Door: "result seatcommit.planVerifier#1"}}},
			{Code: seatcommit.ReasonGrowthGate, Constant: "seatcommit.ReasonGrowthGate", Writers: []ReasonWriter{{Function: "cli.applyLeadWiden", Door: "param task.(Store).Escalate#1"}}},
			{Code: seatcommit.ReasonLeaseTaskMismatch, Constant: "seatcommit.ReasonLeaseTaskMismatch", Writers: []ReasonWriter{{Function: "seatcommit.run", Door: "param seatcommit.(Result).refuse#0"}}},
			{Code: seatcommit.ReasonLedgerTampered, Constant: "seatcommit.ReasonLedgerTampered", Writers: []ReasonWriter{{Function: "seatcommit.(Result).tamperedRefusal", Door: "param seatcommit.(Result).refuse#0"}, {Function: "seatcommit.(Result).writeOutcome", Door: "param seatcommit.(Result).refuse#0"}}},
			{Code: seatcommit.ReasonLinkTarget, Constant: "seatcommit.ReasonLinkTarget", Writers: []ReasonWriter{{Function: "seatcommit.check", Door: "field seatcommit.Violation.Reason"}, {Function: "seatcommit.verify", Door: "param seatcommit.(Result).refuse#0"}}},
			{Code: seatcommit.ReasonNested, Constant: "seatcommit.ReasonNested", Writers: []ReasonWriter{{Function: "seatcommit.check", Door: "field seatcommit.Violation.Reason"}}},
			{Code: seatcommit.ReasonNewerWriter, Constant: "seatcommit.ReasonNewerWriter", Writers: []ReasonWriter{{Function: "seatcommit.(Result).ledgerRefusal", Door: "param seatcommit.(Result).refuse#0"}}},
			{Code: seatcommit.ReasonOutside, Constant: "seatcommit.ReasonOutside", Writers: []ReasonWriter{{Function: "seatcommit.check", Door: "field seatcommit.Violation.Reason"}}},
			{Code: seatcommit.ReasonPRBoundElsewhere, Constant: "seatcommit.ReasonPRBoundElsewhere", Writers: []ReasonWriter{{Function: "seatcommit.openPR", Door: "param seatcommit.(Result).refuse#0"}, {Function: "seatcommit.prCheck", Door: "param seatcommit.(Result).refuse#0"}}},
			{Code: seatcommit.ReasonPRPending, Constant: "seatcommit.ReasonPRPending", Mark: "traced, guarded at scheduler.(*Scheduler).commit", Writers: []ReasonWriter{{Function: "seatcommit.openPR", Door: "field seatcommit.Result.ReasonCode"}, {Function: "seatcommit.prCheck", Door: "field seatcommit.Result.ReasonCode"}}},
			{Code: seatcommit.ReasonProtected, Constant: "seatcommit.ReasonProtected", Writers: []ReasonWriter{{Function: "seatcommit.check", Door: "field seatcommit.Violation.Reason"}}},
			{Code: seatcommit.ReasonPushRejected, Constant: "seatcommit.ReasonPushRejected", Writers: []ReasonWriter{{Function: "seatcommit.commitAndPush", Door: "param seatcommit.(Result).refuse#0"}}},
			{Code: seatcommit.ReasonPushTargetUnacknowledged, Constant: "seatcommit.ReasonPushTargetUnacknowledged", Writers: []ReasonWriter{{Function: "seatcommit.run", Door: "param seatcommit.(Result).refuse#0"}}},
			{Code: seatcommit.ReasonRecordedDiffMismatch, Constant: "seatcommit.ReasonRecordedDiffMismatch", Writers: []ReasonWriter{{Function: "seatcommit.commitAndPush", Door: "param seatcommit.(Result).refuse#0"}}},
			{Code: seatcommit.ReasonSchemaMismatch, Constant: "seatcommit.ReasonSchemaMismatch", Writers: []ReasonWriter{{Function: "seatcommit.(Result).ledgerRefusal", Door: "param seatcommit.(Result).refuse#0"}}},
			{Code: seatcommit.ReasonSizeCap, Constant: "seatcommit.ReasonSizeCap", Writers: []ReasonWriter{{Function: "seatcommit.check", Door: "field seatcommit.Violation.Reason"}}},
			{Code: seatcommit.ReasonStepCancelled, Constant: "seatcommit.ReasonStepCancelled", Mark: "traced, guarded at scheduler.(*Scheduler).commit", Writers: []ReasonWriter{{Function: "seatcommit.run", Door: "field seatcommit.Result.ReasonCode"}, {Function: "seatcommit.verify", Door: "field seatcommit.Result.ReasonCode"}}},
			{Code: seatcommit.ReasonVerifierInterrupted, Constant: "seatcommit.ReasonVerifierInterrupted", Writers: []ReasonWriter{{Function: "seatcommit.verify", Door: "field seatcommit.Result.ReasonCode"}}},
			{Code: seatcommit.ReasonWidenSnapshotMismatch, Constant: "seatcommit.ReasonWidenSnapshotMismatch", Writers: []ReasonWriter{{Function: "seatcommit.run", Door: "param seatcommit.(Result).refuse#0"}}},
			{Code: seatcommit.ReasonWidenSnapshotMissing, Constant: "seatcommit.ReasonWidenSnapshotMissing", Writers: []ReasonWriter{{Function: "seatcommit.run", Door: "param seatcommit.(Result).refuse#0"}}},
			{Code: seatcommit.ReasonWorktree, Constant: "seatcommit.ReasonWorktree", Writers: []ReasonWriter{{Function: "seatcommit.Run", Door: "param seatcommit.(Result).refuse#0"}, {Function: "seatcommit.commitAndPush", Door: "param seatcommit.(Result).refuse#0"}, {Function: "seatcommit.run", Door: "param seatcommit.(Result).refuse#0"}}},
			{Code: seatexec.ClaudeHooksMissing, Constant: "seatexec.ClaudeHooksMissing", Writers: []ReasonWriter{{Function: "seatexec.CheckClaudeHooks", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ClaudePermissionMismatch, Constant: "seatexec.ClaudePermissionMismatch", Writers: []ReasonWriter{{Function: "seatexec.CheckClaudePermission", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ClaudeProjectMCP, Constant: "seatexec.ClaudeProjectMCP", Writers: []ReasonWriter{{Function: "seatexec.CheckClaudeProjectMCP", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.IdentityIncomplete, Constant: "seatexec.IdentityIncomplete", Writers: []ReasonWriter{{Function: "seatexec.checkIdentity", Door: "param seatexec.refusef#0"}, {Function: "seatexec.prepare", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.PiNotEmbedded, Constant: "seatexec.PiNotEmbedded", Writers: []ReasonWriter{{Function: "seatexec.piShimProvider", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.SandboxPinsMissing, Constant: "seatexec.SandboxPinsMissing", Writers: []ReasonWriter{{Function: "seatexec.CheckCodexSandboxPins", Door: "param seatexec.refusef#0"}}},
			{Code: task.BlockVerdictFailed, Constant: "task.BlockVerdictFailed", Writers: []ReasonWriter{{Function: "seatcommit.verify", Door: "param seatcommit.(Result).refuse#0"}, {Function: "task.(Store).complete", Door: "param task.block#1"}}},
			{Code: task.BlockVerdictMissing, Constant: "task.BlockVerdictMissing", Writers: []ReasonWriter{{Function: "task.(Store).complete", Door: "param task.block#1"}, {Function: "seatcommit.planVerifier", Door: "result seatcommit.planVerifier#1"}}},
			{Code: task.BlockVerdictTreeMismatch, Constant: "task.BlockVerdictTreeMismatch", Writers: []ReasonWriter{{Function: "seatcommit.verify", Door: "param seatcommit.(Result).refuse#0"}}},
			{Code: task.ReasonGrantsInvalid, Constant: "task.ReasonGrantsInvalid", Writers: []ReasonWriter{{Function: "routing.(*Router).decide", Door: "field scheduler.Route.Block"}, {Function: "scheduler.(*Scheduler).dispatch", Door: "param task.(Store).Block#2"}}},
			{Code: task.RefusalPRBoundElsewhere, Constant: "task.RefusalPRBoundElsewhere", Writers: []ReasonWriter{{Function: "scheduler.(*Scheduler).applyCommit", Door: "field scheduler.CommitResult.ReasonCode"}}},
			{Code: task.ReasonClassUnknown, Constant: "task.ReasonClassUnknown", Writers: []ReasonWriter{{Function: "routing.(*Router).decide", Door: "field scheduler.Route.Block"}}},
			{Code: task.ReasonRetriesExhausted, Constant: "task.ReasonRetriesExhausted", Writers: []ReasonWriter{{Function: "routing.(*Router).decide", Door: "field scheduler.Route.Block"}, {Function: "scheduler.(DefaultRouter).Route", Door: "field scheduler.Route.Block"}}},
			{Code: task.ReasonRePinAfterAttempt, Constant: "task.ReasonRePinAfterAttempt", Mark: "no writer"},
			{Code: task.ReasonWidenRequested, Constant: "task.ReasonWidenRequested", Mark: "no writer"},
			{Code: task.ReasonGrowthGateRequired, Constant: "task.ReasonGrowthGateRequired", Mark: "no writer"},
			{Code: seatcommit.RefusalSeatHeadDetachedUnmerged, Constant: "seatcommit.RefusalSeatHeadDetachedUnmerged", Mark: "no writer"},
			{Code: seatcommit.ReasonEvidenceRefSymbolic, Constant: "seatcommit.ReasonEvidenceRefSymbolic", Mark: "no writer"},
			{Code: seatcommit.ReasonVerifierTimeout, Constant: "seatcommit.ReasonVerifierTimeout", Mark: "no writer"},
			{Code: seatcommit.ReasonVerifierOutput, Constant: "seatcommit.ReasonVerifierOutput", Mark: "no writer"},
			{Code: seatcommit.ReasonVerifierStart, Constant: "seatcommit.ReasonVerifierStart", Mark: "no writer"},
			{Code: seatcommit.ReasonVerifierCancelled, Constant: "seatcommit.ReasonVerifierCancelled", Mark: "no writer"},
			{Code: seatcommit.ReasonCommitFailed, Constant: "seatcommit.ReasonCommitFailed", Mark: "no writer"},
			{Code: scheduler.ReasonSeatIdentityUnproven, Constant: "scheduler.ReasonSeatIdentityUnproven", Mark: "no writer"},
			{Code: routing.ReasonInvalid, Constant: "routing.ReasonInvalid", Mark: "no writer"},
			{Code: scheduler.AMQBindingPresent, Constant: "scheduler.AMQBindingPresent", Mark: "no writer"},
			{Code: schedproc.ReasonSeatHeadDetachedUnmerged, Constant: "schedproc.ReasonSeatHeadDetachedUnmerged", Mark: "no writer"},
			// S2-3: the launch family's constants that were string literals (LaunchReasons)
			{Code: piruntime.ReasonLedgerUnreadable, Constant: "piruntime.ReasonLedgerUnreadable", Writers: []ReasonWriter{{Function: "piruntime.CheckResume", Door: "field piruntime.ResumeRefusal.Code"}}},
			{Code: piruntime.ReasonPiResumeContextLost, Constant: "piruntime.ReasonPiResumeContextLost", Writers: []ReasonWriter{{Function: "piruntime.CheckResume", Door: "field piruntime.ResumeRefusal.Code"}}},
			{Code: piruntime.ReasonPiSessionIDInvalid, Constant: "piruntime.ReasonPiSessionIDInvalid", Writers: []ReasonWriter{{Function: "piruntime.CheckResume", Door: "field piruntime.ResumeRefusal.Code"}}},
			{Code: piruntime.ReasonPiSessionIDMissing, Constant: "piruntime.ReasonPiSessionIDMissing", Writers: []ReasonWriter{{Function: "piruntime.CheckResume", Door: "field piruntime.ResumeRefusal.Code"}}},
			{Code: piruntime.ReasonPiSessionUnreadable, Constant: "piruntime.ReasonPiSessionUnreadable", Writers: []ReasonWriter{{Function: "piruntime.CheckResume", Door: "field piruntime.ResumeRefusal.Code"}}},
			{Code: scheduler.ReasonLaunchError, Constant: "scheduler.ReasonLaunchError", Writers: []ReasonWriter{{Function: "schedproc.(*Launcher).LaunchRoster", Door: "field scheduler.RosterOutcome.Stage"}, {Function: "scheduler.(*Scheduler).launchRoster", Door: "field scheduler.RosterLaunchError.Stage"}, {Function: "seatlaunch.(*Launcher).LaunchRoster", Door: "field scheduler.RosterOutcome.Stage"}, {Function: "seatlaunch.launchFailure", Door: "param seatlaunch.firstNonEmptyStr#1"}, {Function: "seatlaunch.rosterOutcome", Door: "field scheduler.RosterOutcome.Stage"}}},
			{Code: scheduler.ReasonNotStarted, Constant: "scheduler.ReasonNotStarted", Writers: []ReasonWriter{{Function: "seatlaunch.WaitSeats", Door: "field seatlaunch.SeatState.State"}}},
			{Code: seatexec.ReasonArgvMissing, Constant: "seatexec.ReasonArgvMissing", Writers: []ReasonWriter{{Function: "seatexec.prepare", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonCodexApprovalMissing, Constant: "seatexec.ReasonCodexApprovalMissing", Writers: []ReasonWriter{{Function: "seatexec.CheckCodexSandbox", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonCodexSandboxMissing, Constant: "seatexec.ReasonCodexSandboxMissing", Writers: []ReasonWriter{{Function: "seatexec.CheckCodexSandbox", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonDirWritableByOthers, Constant: "seatexec.ReasonDirWritableByOthers", Writers: []ReasonWriter{{Function: "seatexec.(*anchor).check", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonEnvNeedsInvalid, Constant: "seatexec.ReasonEnvNeedsInvalid", Writers: []ReasonWriter{{Function: "seatexec.ValidateNeeds", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonIdentityMismatch, Constant: "seatexec.ReasonIdentityMismatch", Writers: []ReasonWriter{{Function: "seatexec.checkIdentity", Door: "param seatexec.refusef#0"}, {Function: "seatexec.prepare", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonIdentityProofFailed, Constant: "seatexec.ReasonIdentityProofFailed", Writers: []ReasonWriter{{Function: "seatexec.identityProof", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonIdentityProofTimeout, Constant: "seatexec.ReasonIdentityProofTimeout", Writers: []ReasonWriter{{Function: "seatexec.identityProof", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonMailboxMissing, Constant: "seatexec.ReasonMailboxMissing", Writers: []ReasonWriter{{Function: "seatexec.prepare", Door: "param seatexec.asRefusal#0"}}},
			{Code: seatexec.ReasonMkdirFailed, Constant: "seatexec.ReasonMkdirFailed", Writers: []ReasonWriter{{Function: "seatexec.(*anchor).walk", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonPathInvalid, Constant: "seatexec.ReasonPathInvalid", Writers: []ReasonWriter{{Function: "seatexec.(*anchor).open", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonProofExpired, Constant: "seatexec.ReasonProofExpired", Writers: []ReasonWriter{{Function: "seatexec.sendProof", Door: "param seatexec.refusef#0"}, {Function: "seatlaunch.(*proofServer).judgeLocked", Door: "field seatlaunch.proofSeat.refused"}}},
			{Code: seatexec.ReasonProofRefused, Constant: "seatexec.ReasonProofRefused", Writers: []ReasonWriter{{Function: "seatexec.sendProof", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonProofTicketConsumed, Constant: "seatexec.ReasonProofTicketConsumed", Writers: []ReasonWriter{{Function: "seatexec.sendProof", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonProofTicketMissing, Constant: "seatexec.ReasonProofTicketMissing", Writers: []ReasonWriter{{Function: "seatexec.sendProof", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonProofUnacknowledged, Constant: "seatexec.ReasonProofUnacknowledged", Writers: []ReasonWriter{{Function: "seatexec.sendProof", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonProviderMissing, Constant: "seatexec.ReasonProviderMissing", Writers: []ReasonWriter{{Function: "seatexec.providerPath", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonRecordInvalid, Constant: "seatexec.ReasonRecordInvalid", Writers: []ReasonWriter{{Function: "seatexec.piShimProvider", Door: "param seatexec.refusef#0"}, {Function: "seatexec.prepare", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonRecordMissing, Constant: "seatexec.ReasonRecordMissing", Writers: []ReasonWriter{{Function: "seatexec.prepare", Door: "param seatexec.asRefusal#0"}}},
			{Code: seatexec.ReasonRoleMismatch, Constant: "seatexec.ReasonRoleMismatch", Writers: []ReasonWriter{{Function: "seatexec.prepare", Door: "param seatexec.refusef#0"}, {Function: "seatexec.recheck", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonRoleMissing, Constant: "seatexec.ReasonRoleMissing", Writers: []ReasonWriter{{Function: "seatexec.prepare", Door: "param seatexec.asRefusal#0"}}},
			{Code: seatexec.ReasonRootInvalid, Constant: "seatexec.ReasonRootInvalid", Writers: []ReasonWriter{{Function: "seatexec.openRoot", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonRootUnreadable, Constant: "seatexec.ReasonRootUnreadable", Writers: []ReasonWriter{{Function: "seatexec.openRoot", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonRulesMismatch, Constant: "seatexec.ReasonRulesMismatch", Writers: []ReasonWriter{{Function: "seatexec.readRules", Door: "param seatexec.refusef#0"}, {Function: "seatexec.recheck", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonRulesMissing, Constant: "seatexec.ReasonRulesMissing", Writers: []ReasonWriter{{Function: "seatexec.readRules", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonRuntimeMismatch, Constant: "seatexec.ReasonRuntimeMismatch", Writers: []ReasonWriter{{Function: "seatexec.piShimProvider", Door: "param seatexec.refusef#0"}, {Function: "seatexec.providerPath", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonSeatInvalid, Constant: "seatexec.ReasonSeatInvalid", Writers: []ReasonWriter{{Function: "seatexec.prepare", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonStatFailed, Constant: "seatexec.ReasonStatFailed", Writers: []ReasonWriter{{Function: "seatexec.(*anchor).check", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonSymlinkInPath, Constant: "seatexec.ReasonSymlinkInPath", Writers: []ReasonWriter{{Function: "seatexec.(*anchor).open", Door: "param seatexec.refusef#0"}, {Function: "seatexec.(*anchor).readFile", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonWriteFailed, Constant: "seatexec.ReasonWriteFailed", Writers: []ReasonWriter{{Function: "seatexec.(*anchor).writeFileMode", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonWrongOwner, Constant: "seatexec.ReasonWrongOwner", Writers: []ReasonWriter{{Function: "seatexec.(*anchor).check", Door: "param seatexec.refusef#0"}}},
			{Code: seatlaunch.ReasonApplyAmbiguous, Constant: "seatlaunch.ReasonApplyAmbiguous", Writers: []ReasonWriter{{Function: "seatlaunch.launchFailure", Door: "result seatlaunch.launchFailure#1"}}},
			{Code: seatlaunch.ReasonApplyFailed, Constant: "seatlaunch.ReasonApplyFailed", Writers: []ReasonWriter{{Function: "seatlaunch.Launch", Door: "field seatlaunch.Result.Stage"}, {Function: "seatlaunch.rosterOutcome", Door: "field scheduler.RosterOutcome.Stage"}}},
			{Code: seatlaunch.ReasonDecisionsPending, Constant: "seatlaunch.ReasonDecisionsPending", Writers: []ReasonWriter{{Function: "seatlaunch.Launch", Door: "field seatlaunch.Result.Stage"}}},
			{Code: seatlaunch.ReasonIdentityForged, Constant: "seatlaunch.ReasonIdentityForged", Writers: []ReasonWriter{{Function: "seatlaunch.(*proofServer).forged", Door: "field launch.ProofReply.Code"}, {Function: "seatlaunch.(*proofServer).forged", Door: "field seatlaunch.proofSeat.refused"}}},
			{Code: seatlaunch.ReasonNoProof, Constant: "seatlaunch.ReasonNoProof", Writers: []ReasonWriter{{Function: "seatlaunch.WaitSeats", Door: "field seatlaunch.SeatState.ReasonCode"}}},
			{Code: seatlaunch.ReasonNotObserved, Constant: "seatlaunch.ReasonNotObserved", Writers: []ReasonWriter{{Function: "seatlaunch.launchFailure", Door: "result seatlaunch.launchFailure#1"}}},
			{Code: seatlaunch.ReasonPrepareRefused, Constant: "seatlaunch.ReasonPrepareRefused", Writers: []ReasonWriter{{Function: "seatlaunch.Launch", Door: "field seatlaunch.Result.Stage"}}},
			{Code: seatlaunch.ReasonProofLedgerFailed, Constant: "seatlaunch.ReasonProofLedgerFailed", Writers: []ReasonWriter{{Function: "seatlaunch.(*proofServer).judgeLocked", Door: "field seatlaunch.proofSeat.refused"}}},
			{Code: seatlaunch.ReasonProofMalformed, Constant: "seatlaunch.ReasonProofMalformed", Writers: []ReasonWriter{{Function: "seatlaunch.(*proofServer).serve", Door: "field launch.ProofReply.Code"}}},
			{Code: seatlaunch.ReasonProofMarkerFailed, Constant: "seatlaunch.ReasonProofMarkerFailed", Writers: []ReasonWriter{{Function: "seatlaunch.(*proofServer).judgeLocked", Door: "field seatlaunch.proofSeat.refused"}}},
			{Code: seatlaunch.ReasonProofOriginUnknown, Constant: "seatlaunch.ReasonProofOriginUnknown", Writers: []ReasonWriter{{Function: "seatlaunch.(*proofServer).judgeLocked", Door: "field launch.ProofReply.Code"}}},
			{Code: seatlaunch.ReasonProofTokenInvalid, Constant: "seatlaunch.ReasonProofTokenInvalid", Writers: []ReasonWriter{{Function: "seatlaunch.(*proofServer).judgeLocked", Door: "field launch.ProofReply.Code"}}},
			{Code: seatlaunch.ReasonProofUnknown, Constant: "seatlaunch.ReasonProofUnknown", Writers: []ReasonWriter{{Function: "seatlaunch.(*proofServer).judgeLocked", Door: "field launch.ProofReply.Code"}}},
			{Code: seatlaunch.ReasonRefused, Constant: "seatlaunch.ReasonRefused", Writers: []ReasonWriter{{Function: "seatlaunch.seatState", Door: "field seatlaunch.SeatState.State"}}},
			{Code: seatlaunch.ReasonResourcePresent, Constant: "seatlaunch.ReasonResourcePresent", Writers: []ReasonWriter{{Function: "seatlaunch.Launch", Door: "field seatlaunch.Result.Stage"}}},
			{Code: seatlaunch.ReasonSeatNotUp, Constant: "seatlaunch.ReasonSeatNotUp", Writers: []ReasonWriter{{Function: "seatlaunch.launchFailure", Door: "result seatlaunch.launchFailure#0"}}},
		},
	}
}

// WaitReasons returns the declared wait reasons.
func WaitReasons() ReasonList {
	return ReasonList{
		Open: true,
		OpenDoors: []OpenDoor{
			{Name: "the launch failure code: the reason code, else the stage", Functions: []string{"scheduler.(LaunchFailure).Code"}},
			{Name: "AMQ launchapi field Reason, copied by seatlaunch.rosterOutcome", Functions: []string{"seatlaunch.rosterOutcome"}},
			{Name: "AMQ launchapi field ReasonCode, copied by seatlaunch.rosterOutcome", Functions: []string{"seatlaunch.rosterOutcome"}},
			{Name: "the pending action kinds joined with a comma", Functions: []string{"seatlaunch.rosterOutcome"}},
			{Name: "decoded field launch.ProofReply.Code", Functions: []string{"seatexec.DialProof"}},
			{Name: "decoded field launch.Exec.ReasonCode", Functions: []string{"launch.Decode"}},
			{Name: "decoded field scheduler.LaunchFailure.Stage", Functions: []string{"scheduler.ReadState"}},
			{Name: "decoded field scheduler.LaunchFailure.ReasonCode", Functions: []string{"scheduler.ReadState"}},
			{Name: "field task.Task.WaitReason read back from disk", Functions: []string{"task.snapshotOnce", "task.readDir", "task.recoverJournal"}},
			{Name: "the sentence task.DependencyMergeWait formats", Functions: []string{"task.DependencyMergeWait"}},
		},
		Doors: []string{
			"element in routing.waitReason",
			"field launch.ProofReply.Code",
			"field piruntime.ResumeRefusal.Code",
			"field scheduler.Event.ReasonCode",
			"field scheduler.RosterLaunchError.Stage",
			"field scheduler.RosterOutcome.ReasonCode",
			"field scheduler.RosterOutcome.Stage",
			"field scheduler.Route.Wait",
			"field seatlaunch.Result.Stage",
			"field seatlaunch.SeatState.ReasonCode",
			"field seatlaunch.SeatState.State",
			"field seatlaunch.proofSeat.refused",
			"param scheduler.(*Scheduler).wait#1",
			"param scheduler.firstNonEmpty#1",
			"param seatexec.asRefusal#0",
			"param seatexec.refusef#0",
			"param seatlaunch.firstNonEmptyStr#1",
			"result routing.waitReason#0",
			"result seatlaunch.launchFailure#0",
			"result seatlaunch.launchFailure#1",
			"result task.Blocker#0",
		},
		Entries: []ReasonEntry{
			{Code: claudeseat.TokenInsecure, Constant: "claudeseat.TokenInsecure", Writers: []ReasonWriter{{Function: "seatexec.prepare", Door: "param seatexec.refusef#0"}}},
			{Code: routing.SkipConcurrencyCap, Constant: "routing.SkipConcurrencyCap", Writers: []ReasonWriter{{Function: "routing.waitReason", Door: "element in routing.waitReason"}}},
			{Code: routing.SkipCostCap, Constant: "routing.SkipCostCap", Writers: []ReasonWriter{{Function: "routing.waitReason", Door: "element in routing.waitReason"}}},
			{Code: routing.SkipPrivacyClass, Constant: "routing.SkipPrivacyClass", Writers: []ReasonWriter{{Function: "routing.waitReason", Door: "element in routing.waitReason"}}},
			{Code: routing.SkipReviewerFamily, Constant: "routing.SkipReviewerFamily", Writers: []ReasonWriter{{Function: "routing.waitReason", Door: "element in routing.waitReason"}}},
			{Code: scheduler.GoneProcessExited, Constant: "scheduler.GoneProcessExited", Mark: "traced, guarded at scheduler.(*Scheduler).applyEvents", Writers: []ReasonWriter{{Function: "scheduler.(*Scheduler).checkLiveness", Door: "field scheduler.Event.ReasonCode"}}},
			{Code: scheduler.ReasonResourcePresent, Constant: "scheduler.ReasonResourcePresent", Writers: []ReasonWriter{{Function: "seatlaunch.rosterOutcome", Door: "field scheduler.RosterOutcome.ReasonCode"}}},
			{Code: scheduler.WaitNoRosterSeat, Constant: "scheduler.WaitNoRosterSeat", Writers: []ReasonWriter{{Function: "scheduler.(*Scheduler).rosterNoIdleSeat", Door: "param scheduler.(*Scheduler).wait#1"}}},
			{Code: seatexec.ClaudeHooksMissing, Constant: "seatexec.ClaudeHooksMissing", Writers: []ReasonWriter{{Function: "seatexec.CheckClaudeHooks", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ClaudePermissionMismatch, Constant: "seatexec.ClaudePermissionMismatch", Writers: []ReasonWriter{{Function: "seatexec.CheckClaudePermission", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ClaudeProjectMCP, Constant: "seatexec.ClaudeProjectMCP", Writers: []ReasonWriter{{Function: "seatexec.CheckClaudeProjectMCP", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.IdentityIncomplete, Constant: "seatexec.IdentityIncomplete", Writers: []ReasonWriter{{Function: "seatexec.checkIdentity", Door: "param seatexec.refusef#0"}, {Function: "seatexec.prepare", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.PiNotEmbedded, Constant: "seatexec.PiNotEmbedded", Writers: []ReasonWriter{{Function: "seatexec.piShimProvider", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.SandboxPinsMissing, Constant: "seatexec.SandboxPinsMissing", Writers: []ReasonWriter{{Function: "seatexec.CheckCodexSandboxPins", Door: "param seatexec.refusef#0"}}},
			{Code: task.WaitDependencies, Constant: "task.WaitDependencies", Writers: []ReasonWriter{{Function: "task.Blocker", Door: "result task.Blocker#0"}}},
			{Code: task.WaitFileLock, Constant: "task.WaitFileLock", Writers: []ReasonWriter{{Function: "task.Blocker", Door: "result task.Blocker#0"}}},
			{Code: task.WaitNoRoute, Constant: "task.WaitNoRoute", Writers: []ReasonWriter{{Function: "routing.(*Router).Route", Door: "field scheduler.Route.Wait"}, {Function: "scheduler.(DefaultRouter).Route", Door: "field scheduler.Route.Wait"}, {Function: "scheduler.(*Scheduler).dispatch", Door: "param scheduler.firstNonEmpty#1"}, {Function: "routing.waitReason", Door: "result routing.waitReason#0"}}},
			{Code: task.WaitOutcomeLogUnwritable, Constant: "task.WaitOutcomeLogUnwritable", Writers: []ReasonWriter{{Function: "routing.(*Router).Route", Door: "field scheduler.Route.Wait"}, {Function: "routing.(*Router).holdTampered", Door: "field scheduler.Route.Wait"}}},
			{Code: task.WaitSeatCap, Constant: "task.WaitSeatCap", Writers: []ReasonWriter{{Function: "scheduler.(*Scheduler).dispatch", Door: "param scheduler.(*Scheduler).wait#1"}, {Function: "scheduler.(*Scheduler).rosterNoIdleSeat", Door: "param scheduler.(*Scheduler).wait#1"}}},
			{Code: task.WaitSeatStarting, Constant: "task.WaitSeatStarting", Writers: []ReasonWriter{{Function: "scheduler.(*Scheduler).waitForSeat", Door: "param scheduler.(*Scheduler).wait#1"}}},
			{Code: task.WaitUsageLimit, Constant: "task.WaitUsageLimit", Writers: []ReasonWriter{{Function: "scheduler.(*Scheduler).dispatch", Door: "param scheduler.(*Scheduler).wait#1"}, {Function: "scheduler.(*Scheduler).rosterNoIdleSeat", Door: "param scheduler.(*Scheduler).wait#1"}}},
			{Code: task.WaitPrivacyClass, Constant: "task.WaitPrivacyClass", Mark: "no writer"},
			{Code: task.WaitConcurrencyCap, Constant: "task.WaitConcurrencyCap", Mark: "no writer"},
			{Code: task.WaitCostCap, Constant: "task.WaitCostCap", Mark: "no writer"},
			{Code: task.WaitReviewerFamily, Constant: "task.WaitReviewerFamily", Mark: "no writer"},
			// S2-3: the launch family's constants that were string literals (LaunchReasons)
			{Code: piruntime.ReasonLedgerUnreadable, Constant: "piruntime.ReasonLedgerUnreadable", Writers: []ReasonWriter{{Function: "piruntime.CheckResume", Door: "field piruntime.ResumeRefusal.Code"}}},
			{Code: piruntime.ReasonPiResumeContextLost, Constant: "piruntime.ReasonPiResumeContextLost", Writers: []ReasonWriter{{Function: "piruntime.CheckResume", Door: "field piruntime.ResumeRefusal.Code"}}},
			{Code: piruntime.ReasonPiSessionIDInvalid, Constant: "piruntime.ReasonPiSessionIDInvalid", Writers: []ReasonWriter{{Function: "piruntime.CheckResume", Door: "field piruntime.ResumeRefusal.Code"}}},
			{Code: piruntime.ReasonPiSessionIDMissing, Constant: "piruntime.ReasonPiSessionIDMissing", Writers: []ReasonWriter{{Function: "piruntime.CheckResume", Door: "field piruntime.ResumeRefusal.Code"}}},
			{Code: piruntime.ReasonPiSessionUnreadable, Constant: "piruntime.ReasonPiSessionUnreadable", Writers: []ReasonWriter{{Function: "piruntime.CheckResume", Door: "field piruntime.ResumeRefusal.Code"}}},
			{Code: scheduler.ReasonLaunchError, Constant: "scheduler.ReasonLaunchError", Writers: []ReasonWriter{{Function: "schedproc.(*Launcher).LaunchRoster", Door: "field scheduler.RosterOutcome.Stage"}, {Function: "scheduler.(*Scheduler).launchRoster", Door: "field scheduler.RosterLaunchError.Stage"}, {Function: "scheduler.(*Scheduler).launchRoster", Door: "param scheduler.firstNonEmpty#1"}, {Function: "scheduler.(*Scheduler).noteLaunchFailure", Door: "param scheduler.firstNonEmpty#1"}, {Function: "seatlaunch.(*Launcher).LaunchRoster", Door: "field scheduler.RosterOutcome.Stage"}, {Function: "seatlaunch.launchFailure", Door: "param seatlaunch.firstNonEmptyStr#1"}, {Function: "seatlaunch.rosterOutcome", Door: "field scheduler.RosterOutcome.Stage"}}},
			{Code: scheduler.ReasonNotStarted, Constant: "scheduler.ReasonNotStarted", Writers: []ReasonWriter{{Function: "scheduler.(*Scheduler).launchRoster", Door: "param scheduler.firstNonEmpty#1"}, {Function: "seatlaunch.WaitSeats", Door: "field seatlaunch.SeatState.State"}}},
			{Code: seatexec.ReasonArgvMissing, Constant: "seatexec.ReasonArgvMissing", Writers: []ReasonWriter{{Function: "seatexec.prepare", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonCodexApprovalMissing, Constant: "seatexec.ReasonCodexApprovalMissing", Writers: []ReasonWriter{{Function: "seatexec.CheckCodexSandbox", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonCodexSandboxMissing, Constant: "seatexec.ReasonCodexSandboxMissing", Writers: []ReasonWriter{{Function: "seatexec.CheckCodexSandbox", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonDirWritableByOthers, Constant: "seatexec.ReasonDirWritableByOthers", Writers: []ReasonWriter{{Function: "seatexec.(*anchor).check", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonEnvNeedsInvalid, Constant: "seatexec.ReasonEnvNeedsInvalid", Writers: []ReasonWriter{{Function: "seatexec.ValidateNeeds", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonIdentityMismatch, Constant: "seatexec.ReasonIdentityMismatch", Writers: []ReasonWriter{{Function: "seatexec.checkIdentity", Door: "param seatexec.refusef#0"}, {Function: "seatexec.prepare", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonIdentityProofFailed, Constant: "seatexec.ReasonIdentityProofFailed", Writers: []ReasonWriter{{Function: "seatexec.identityProof", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonIdentityProofTimeout, Constant: "seatexec.ReasonIdentityProofTimeout", Writers: []ReasonWriter{{Function: "seatexec.identityProof", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonMailboxMissing, Constant: "seatexec.ReasonMailboxMissing", Writers: []ReasonWriter{{Function: "seatexec.prepare", Door: "param seatexec.asRefusal#0"}}},
			{Code: seatexec.ReasonMkdirFailed, Constant: "seatexec.ReasonMkdirFailed", Writers: []ReasonWriter{{Function: "seatexec.(*anchor).walk", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonPathInvalid, Constant: "seatexec.ReasonPathInvalid", Writers: []ReasonWriter{{Function: "seatexec.(*anchor).open", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonProofExpired, Constant: "seatexec.ReasonProofExpired", Writers: []ReasonWriter{{Function: "seatexec.sendProof", Door: "param seatexec.refusef#0"}, {Function: "seatlaunch.(*proofServer).judgeLocked", Door: "field seatlaunch.proofSeat.refused"}}},
			{Code: seatexec.ReasonProofRefused, Constant: "seatexec.ReasonProofRefused", Writers: []ReasonWriter{{Function: "seatexec.sendProof", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonProofTicketConsumed, Constant: "seatexec.ReasonProofTicketConsumed", Writers: []ReasonWriter{{Function: "seatexec.sendProof", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonProofTicketMissing, Constant: "seatexec.ReasonProofTicketMissing", Writers: []ReasonWriter{{Function: "seatexec.sendProof", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonProofUnacknowledged, Constant: "seatexec.ReasonProofUnacknowledged", Writers: []ReasonWriter{{Function: "seatexec.sendProof", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonProviderMissing, Constant: "seatexec.ReasonProviderMissing", Writers: []ReasonWriter{{Function: "seatexec.providerPath", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonRecordInvalid, Constant: "seatexec.ReasonRecordInvalid", Writers: []ReasonWriter{{Function: "seatexec.piShimProvider", Door: "param seatexec.refusef#0"}, {Function: "seatexec.prepare", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonRecordMissing, Constant: "seatexec.ReasonRecordMissing", Writers: []ReasonWriter{{Function: "seatexec.prepare", Door: "param seatexec.asRefusal#0"}}},
			{Code: seatexec.ReasonRoleMismatch, Constant: "seatexec.ReasonRoleMismatch", Writers: []ReasonWriter{{Function: "seatexec.prepare", Door: "param seatexec.refusef#0"}, {Function: "seatexec.recheck", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonRoleMissing, Constant: "seatexec.ReasonRoleMissing", Writers: []ReasonWriter{{Function: "seatexec.prepare", Door: "param seatexec.asRefusal#0"}}},
			{Code: seatexec.ReasonRootInvalid, Constant: "seatexec.ReasonRootInvalid", Writers: []ReasonWriter{{Function: "seatexec.openRoot", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonRootUnreadable, Constant: "seatexec.ReasonRootUnreadable", Writers: []ReasonWriter{{Function: "seatexec.openRoot", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonRulesMismatch, Constant: "seatexec.ReasonRulesMismatch", Writers: []ReasonWriter{{Function: "seatexec.readRules", Door: "param seatexec.refusef#0"}, {Function: "seatexec.recheck", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonRulesMissing, Constant: "seatexec.ReasonRulesMissing", Writers: []ReasonWriter{{Function: "seatexec.readRules", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonRuntimeMismatch, Constant: "seatexec.ReasonRuntimeMismatch", Writers: []ReasonWriter{{Function: "seatexec.piShimProvider", Door: "param seatexec.refusef#0"}, {Function: "seatexec.providerPath", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonSeatInvalid, Constant: "seatexec.ReasonSeatInvalid", Writers: []ReasonWriter{{Function: "seatexec.prepare", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonStatFailed, Constant: "seatexec.ReasonStatFailed", Writers: []ReasonWriter{{Function: "seatexec.(*anchor).check", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonSymlinkInPath, Constant: "seatexec.ReasonSymlinkInPath", Writers: []ReasonWriter{{Function: "seatexec.(*anchor).open", Door: "param seatexec.refusef#0"}, {Function: "seatexec.(*anchor).readFile", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonWriteFailed, Constant: "seatexec.ReasonWriteFailed", Writers: []ReasonWriter{{Function: "seatexec.(*anchor).writeFileMode", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.ReasonWrongOwner, Constant: "seatexec.ReasonWrongOwner", Writers: []ReasonWriter{{Function: "seatexec.(*anchor).check", Door: "param seatexec.refusef#0"}}},
			{Code: seatlaunch.ReasonApplyAmbiguous, Constant: "seatlaunch.ReasonApplyAmbiguous", Writers: []ReasonWriter{{Function: "seatlaunch.launchFailure", Door: "result seatlaunch.launchFailure#1"}}},
			{Code: seatlaunch.ReasonApplyFailed, Constant: "seatlaunch.ReasonApplyFailed", Writers: []ReasonWriter{{Function: "seatlaunch.Launch", Door: "field seatlaunch.Result.Stage"}, {Function: "seatlaunch.rosterOutcome", Door: "field scheduler.RosterOutcome.Stage"}}},
			{Code: seatlaunch.ReasonDecisionsPending, Constant: "seatlaunch.ReasonDecisionsPending", Writers: []ReasonWriter{{Function: "seatlaunch.Launch", Door: "field seatlaunch.Result.Stage"}}},
			{Code: seatlaunch.ReasonIdentityForged, Constant: "seatlaunch.ReasonIdentityForged", Writers: []ReasonWriter{{Function: "seatlaunch.(*proofServer).forged", Door: "field launch.ProofReply.Code"}, {Function: "seatlaunch.(*proofServer).forged", Door: "field seatlaunch.proofSeat.refused"}}},
			{Code: seatlaunch.ReasonNoProof, Constant: "seatlaunch.ReasonNoProof", Writers: []ReasonWriter{{Function: "seatlaunch.WaitSeats", Door: "field seatlaunch.SeatState.ReasonCode"}}},
			{Code: seatlaunch.ReasonNotObserved, Constant: "seatlaunch.ReasonNotObserved", Writers: []ReasonWriter{{Function: "seatlaunch.launchFailure", Door: "result seatlaunch.launchFailure#1"}}},
			{Code: seatlaunch.ReasonPrepareRefused, Constant: "seatlaunch.ReasonPrepareRefused", Writers: []ReasonWriter{{Function: "seatlaunch.Launch", Door: "field seatlaunch.Result.Stage"}}},
			{Code: seatlaunch.ReasonProofLedgerFailed, Constant: "seatlaunch.ReasonProofLedgerFailed", Writers: []ReasonWriter{{Function: "seatlaunch.(*proofServer).judgeLocked", Door: "field seatlaunch.proofSeat.refused"}}},
			{Code: seatlaunch.ReasonProofMalformed, Constant: "seatlaunch.ReasonProofMalformed", Writers: []ReasonWriter{{Function: "seatlaunch.(*proofServer).serve", Door: "field launch.ProofReply.Code"}}},
			{Code: seatlaunch.ReasonProofMarkerFailed, Constant: "seatlaunch.ReasonProofMarkerFailed", Writers: []ReasonWriter{{Function: "seatlaunch.(*proofServer).judgeLocked", Door: "field seatlaunch.proofSeat.refused"}}},
			{Code: seatlaunch.ReasonProofOriginUnknown, Constant: "seatlaunch.ReasonProofOriginUnknown", Writers: []ReasonWriter{{Function: "seatlaunch.(*proofServer).judgeLocked", Door: "field launch.ProofReply.Code"}}},
			{Code: seatlaunch.ReasonProofTokenInvalid, Constant: "seatlaunch.ReasonProofTokenInvalid", Writers: []ReasonWriter{{Function: "seatlaunch.(*proofServer).judgeLocked", Door: "field launch.ProofReply.Code"}}},
			{Code: seatlaunch.ReasonProofUnknown, Constant: "seatlaunch.ReasonProofUnknown", Writers: []ReasonWriter{{Function: "seatlaunch.(*proofServer).judgeLocked", Door: "field launch.ProofReply.Code"}}},
			{Code: seatlaunch.ReasonRefused, Constant: "seatlaunch.ReasonRefused", Writers: []ReasonWriter{{Function: "seatlaunch.seatState", Door: "field seatlaunch.SeatState.State"}}},
			{Code: seatlaunch.ReasonResourcePresent, Constant: "seatlaunch.ReasonResourcePresent", Writers: []ReasonWriter{{Function: "seatlaunch.Launch", Door: "field seatlaunch.Result.Stage"}}},
			{Code: seatlaunch.ReasonSeatNotUp, Constant: "seatlaunch.ReasonSeatNotUp", Writers: []ReasonWriter{{Function: "seatlaunch.launchFailure", Door: "result seatlaunch.launchFailure#0"}}},
		},
	}
}

// NotReasons returns the constants that are refusal codes and never a
// task's block or wait reason.
func NotReasons() []ReasonEntry {
	return []ReasonEntry{
		{Code: task.RefusalFileLock, Constant: "task.RefusalFileLock"},
		{Code: task.RefusalSelfWiden, Constant: "task.RefusalSelfWiden"},
		{Code: task.RefusalClaimOfferRequired, Constant: "task.RefusalClaimOfferRequired"},
		{Code: task.RefusalClaimOfferExpired, Constant: "task.RefusalClaimOfferExpired"},
		{Code: task.RefusalClaimPoolMismatch, Constant: "task.RefusalClaimPoolMismatch"},
		{Code: task.RefusalClaimPoolUnknown, Constant: "task.RefusalClaimPoolUnknown"},
	}
}

// LaunchReasons returns the launch family (S2-3, 3.3.0): the constants the
// launch path writes, the doors it writes them at, and the four stage and
// state words it writes as literals, each with its guard
// (docs/v3-spike/330-s1-2b-reasons/sites.md 5a and 5c).
func LaunchReasons() LaunchFamily {
	return LaunchFamily{
		Door: "scheduler.(LaunchFailure).Code",
		Doors: []string{
			"field launch.ProofReply.Code",
			"field piruntime.ResumeRefusal.Code",
			"field scheduler.RosterLaunchError.Stage",
			"field scheduler.RosterOutcome.Stage",
			"field seatlaunch.Result.Stage",
			"field seatlaunch.SeatState.ReasonCode",
			"field seatlaunch.SeatState.State",
			"field seatlaunch.proofSeat.refused",
			"param seatexec.asRefusal#0",
			"param seatexec.refusef#0",
			"param seatlaunch.firstNonEmptyStr#1",
			"result seatlaunch.launchFailure#0",
			"result seatlaunch.launchFailure#1",
		},
		CallerDoors: []CallerDoor{
			{
				Doors:     []string{"param scheduler.firstNonEmpty#0", "param scheduler.firstNonEmpty#1"},
				Functions: []string{"scheduler.(*Scheduler).launchRoster", "scheduler.(*Scheduler).noteLaunchFailure", "scheduler.(LaunchFailure).Code"},
				NotCodes:  []CallerWord{{Word: "-", Function: "scheduler.(*Scheduler).launchRoster", Why: "the roster_launched decision's placeholder for a launch with no id"}},
			},
		},
		Constants: []string{
			// listed before S2-3
			"claudeseat.TokenInsecure",
			"scheduler.AMQBindingPresent",
			"scheduler.ReasonResourcePresent",
			"seatexec.ClaudeHooksMissing",
			"seatexec.ClaudePermissionMismatch",
			"seatexec.IdentityIncomplete",
			"seatexec.PiNotEmbedded",
			"seatexec.SandboxPinsMissing",
			// S2-3: the 54 launch codes that were string literals
			"piruntime.ReasonLedgerUnreadable",
			"piruntime.ReasonPiResumeContextLost",
			"piruntime.ReasonPiSessionIDInvalid",
			"piruntime.ReasonPiSessionIDMissing",
			"piruntime.ReasonPiSessionUnreadable",
			"scheduler.ReasonLaunchError",
			"scheduler.ReasonNotStarted",
			"seatexec.ReasonArgvMissing",
			"seatexec.ReasonCodexApprovalMissing",
			"seatexec.ReasonCodexSandboxMissing",
			"seatexec.ReasonDirWritableByOthers",
			"seatexec.ReasonEnvNeedsInvalid",
			"seatexec.ReasonIdentityMismatch",
			"seatexec.ReasonIdentityProofFailed",
			"seatexec.ReasonIdentityProofTimeout",
			"seatexec.ReasonMailboxMissing",
			"seatexec.ReasonMkdirFailed",
			"seatexec.ReasonPathInvalid",
			"seatexec.ReasonProofExpired",
			"seatexec.ReasonProofRefused",
			"seatexec.ReasonProofTicketConsumed",
			"seatexec.ReasonProofTicketMissing",
			"seatexec.ReasonProofUnacknowledged",
			"seatexec.ReasonProviderMissing",
			"seatexec.ReasonRecordInvalid",
			"seatexec.ReasonRecordMissing",
			"seatexec.ReasonRoleMismatch",
			"seatexec.ReasonRoleMissing",
			"seatexec.ReasonRootInvalid",
			"seatexec.ReasonRootUnreadable",
			"seatexec.ReasonRulesMismatch",
			"seatexec.ReasonRulesMissing",
			"seatexec.ReasonRuntimeMismatch",
			"seatexec.ReasonSeatInvalid",
			"seatexec.ReasonStatFailed",
			"seatexec.ReasonSymlinkInPath",
			"seatexec.ReasonWriteFailed",
			"seatexec.ReasonWrongOwner",
			"seatlaunch.ReasonApplyAmbiguous",
			"seatlaunch.ReasonApplyFailed",
			"seatlaunch.ReasonDecisionsPending",
			"seatlaunch.ReasonIdentityForged",
			"seatlaunch.ReasonNoProof",
			"seatlaunch.ReasonNotObserved",
			"seatlaunch.ReasonPrepareRefused",
			"seatlaunch.ReasonProofLedgerFailed",
			"seatlaunch.ReasonProofMalformed",
			"seatlaunch.ReasonProofMarkerFailed",
			"seatlaunch.ReasonProofOriginUnknown",
			"seatlaunch.ReasonProofTokenInvalid",
			"seatlaunch.ReasonProofUnknown",
			"seatlaunch.ReasonRefused",
			"seatlaunch.ReasonResourcePresent",
			"seatlaunch.ReasonSeatNotUp",
			// added after S2-3: 3.3.1 item 5 (D5 (c))
			"seatexec.ClaudeProjectMCP",
		},
		Kept: []KeptWord{
			{Word: "pending", Guard: "seatlaunch.WaitSeats", Why: "seatState starts every seat at pending; before WaitSeats returns, a seat still pending is rewritten to not_started with the reason code no_proof"},
			{Word: "up", Guard: "seatlaunch.(*Launcher).settleOne", Why: "set only with Up true, and a seat that is up settles as up or closing, never as a failure"},
			{Word: "prepared", Guard: "seatlaunch.Launch", Why: "set only in Launch's dry-run arm, and no code outside tests sets Options.DryRun"},
			{Word: "applied", Guard: "seatlaunch.launchFailure", Why: "launchFailure returns the roster's stage only when it is not applied"},
		},
		OutsideDoors: []string{
			"param seatlaunch.(*proofServer).forged#2",
			"param seatlaunch.(*proofServer).reject#2",
		},
		Outside: []ReasonEntry{
			{Code: piruntime.ReasonRejected, Constant: "piruntime.ReasonRejected"},
			{Code: piruntime.RuntimeMismatchReason, Constant: "piruntime.RuntimeMismatchReason"},
			{Code: schedproc.ReasonBaseFetchUnauthenticated, Constant: "schedproc.ReasonBaseFetchUnauthenticated"},
			{Code: schedproc.ReasonBaseTipMoved, Constant: "schedproc.ReasonBaseTipMoved"},
			{Code: schedproc.ReasonSeatBranchUnmerged, Constant: "schedproc.ReasonSeatBranchUnmerged"},
			{Code: schedproc.ReasonSeatBranchUnpushed, Constant: "schedproc.ReasonSeatBranchUnpushed"},
			{Code: schedproc.ReasonSeatGitdirForeign, Constant: "schedproc.ReasonSeatGitdirForeign"},
			{Code: schedproc.ReasonSeatHeadUnreadable, Constant: "schedproc.ReasonSeatHeadUnreadable"},
			{Code: schedproc.ReasonSeatWorktreeCheckFailed, Constant: "schedproc.ReasonSeatWorktreeCheckFailed"},
			{Code: schedproc.ReasonSeatWorktreeDirty, Constant: "schedproc.ReasonSeatWorktreeDirty"},
			{Code: seatlaunch.ReasonProofConnectionsFull, Constant: "seatlaunch.ReasonProofConnectionsFull"},
			{Code: seatlaunch.ReasonProofOrigin, Constant: "seatlaunch.ReasonProofOrigin"},
			{Code: seatlaunch.ReasonProofReplayed, Constant: "seatlaunch.ReasonProofReplayed"},
		},
	}
}
