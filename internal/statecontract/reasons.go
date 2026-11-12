package statecontract

// This file declares the reasons a task's block and wait fields are given,
// from the sites that pass them. An entry's Code is a constant used by name,
// never a second string literal. TestReasonsAreTiedToTheirWriters ties each
// entry to its writer sites; docs/v3-spike/330-s1-2b-reasons/sites.md holds
// the file and line of each site.

import (
	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
	"github.com/omriariav/amq-squad/v3/internal/registry"
	"github.com/omriariav/amq-squad/v3/internal/routing"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/seatexec"
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
			"field scheduler.CommitResult.ReasonCode",
			"field scheduler.Event.ReasonCode",
			"field scheduler.RosterOutcome.ReasonCode",
			"field scheduler.Route.Block",
			"field seatcommit.Result.ReasonCode",
			"field seatcommit.Violation.Reason",
			"local code in scheduler.(*Scheduler).commit",
			"param seatcommit.(Result).refuse#0",
			"param seatexec.refusef#0",
			"param task.(Store).Block#2",
			"param task.(Store).Escalate#1",
			"param task.block#1",
			"result registry.OpenPinned#1",
			"result seatcommit.planVerifier#1",
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
			{Code: seatexec.IdentityIncomplete, Constant: "seatexec.IdentityIncomplete", Writers: []ReasonWriter{{Function: "seatexec.checkIdentity", Door: "param seatexec.refusef#0"}, {Function: "seatexec.prepare", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.PiNotEmbedded, Constant: "seatexec.PiNotEmbedded", Writers: []ReasonWriter{{Function: "seatexec.piShimProvider", Door: "param seatexec.refusef#0"}}},
			{Code: seatexec.SandboxPinsMissing, Constant: "seatexec.SandboxPinsMissing", Writers: []ReasonWriter{{Function: "seatexec.CheckCodexSandboxPins", Door: "param seatexec.refusef#0"}}},
			{Code: task.BlockVerdictFailed, Constant: "task.BlockVerdictFailed", Writers: []ReasonWriter{{Function: "seatcommit.verify", Door: "param seatcommit.(Result).refuse#0"}, {Function: "task.(Store).complete", Door: "param task.block#1"}}},
			{Code: task.BlockVerdictMissing, Constant: "task.BlockVerdictMissing", Writers: []ReasonWriter{{Function: "task.(Store).complete", Door: "param task.block#1"}, {Function: "seatcommit.planVerifier", Door: "result seatcommit.planVerifier#1"}}},
			{Code: task.BlockVerdictTreeMismatch, Constant: "task.BlockVerdictTreeMismatch", Writers: []ReasonWriter{{Function: "seatcommit.verify", Door: "param seatcommit.(Result).refuse#0"}}},
			{Code: task.ReasonGrantsInvalid, Constant: "task.ReasonGrantsInvalid", Writers: []ReasonWriter{{Function: "routing.(*Router).decide", Door: "field scheduler.Route.Block"}, {Function: "scheduler.(*Scheduler).dispatch", Door: "param task.(Store).Block#2"}}},
			{Code: task.RefusalPRBoundElsewhere, Constant: "task.RefusalPRBoundElsewhere", Writers: []ReasonWriter{{Function: "scheduler.(*Scheduler).applyCommit", Door: "field scheduler.CommitResult.ReasonCode"}}},
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
			"field scheduler.Event.ReasonCode",
			"field scheduler.RosterOutcome.ReasonCode",
			"field scheduler.Route.Wait",
			"param scheduler.(*Scheduler).wait#1",
			"param scheduler.firstNonEmpty#1",
			"param seatexec.refusef#0",
			"result routing.waitReason#0",
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
