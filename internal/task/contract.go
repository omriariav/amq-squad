package task

import (
	"reflect"
	"strings"
)

// StateContract is one state and whether it is terminal.
type StateContract struct {
	Name     string `json:"name"`
	Terminal bool   `json:"terminal"`
}

// MoveContract is one task state-machine move.
type MoveContract struct {
	From   string   `json:"from"`
	Via    []string `json:"via"`
	Writer string   `json:"writer"`
	To     string   `json:"to"`
	Who    []string `json:"who"`
	// Needs is the whole non-state, non-actor condition from each Via entry to this move, or empty when that condition does not fit one sentence of at most 300 characters.
	Needs            string `json:"needs,omitempty"`
	ClearsBlockGroup bool   `json:"clears_block_group"`
}

// ConditionalOutcome records an outcome that belongs to a reader's set only
// when the named condition holds.
type ConditionalOutcome struct {
	Outcome   string `json:"outcome"`
	Condition string `json:"condition"`
}

// OutcomeSetContract declares one reader's failed-like outcome grouping.
type OutcomeSetContract struct {
	Name          string               `json:"name"`
	ReaderPackage string               `json:"reader_package"`
	ReaderFunc    string               `json:"reader_func"`
	Outcomes      []string             `json:"outcomes"`
	Conditional   []ConditionalOutcome `json:"conditional,omitempty"`
}

// Actor checks used by task moves.
const (
	WhoLiveHolder     = "live_holder"
	WhoCreator        = "creator"
	WhoProvenLead     = "proven_lead"
	WhoScheduler      = "scheduler"
	WhoHarnessShell   = "harness_shell"
	WhoAssertedHandle = "asserted_handle"
)

// States returns the task states in their declaration order.
func States() []StateContract {
	names := []string{StatePending, StateInProgress, StateBlocked, StateCompleted, StateCancelled}
	out := make([]StateContract, len(names))
	for i, name := range names {
		out[i] = StateContract{Name: name, Terminal: Terminal(name)}
	}
	return out
}

// Moves returns the task state-machine moves observed in the store.
func Moves() []MoveContract {
	return []MoveContract{
		{Via: []string{"Add", "AddWithFileWaits"}, Writer: "AddWithFileWaits", To: StatePending, Who: []string{WhoAssertedHandle}},
		{From: StatePending, Via: []string{"Grant", "GrantClean", "Claim", "ClaimServing"}, Writer: "grant", To: StateInProgress, Who: []string{WhoScheduler, WhoAssertedHandle}},
		{From: StatePending, Via: []string{"Block", "BlockWithPaths"}, Writer: "block", To: StateBlocked, Who: []string{WhoScheduler, WhoAssertedHandle}},
		{From: StatePending, Via: []string{"Cancel"}, Writer: "Cancel", To: StateCancelled, Who: []string{WhoAssertedHandle}},
		{From: StateInProgress, Via: []string{"Done"}, Writer: "complete", To: StateCompleted, Who: []string{WhoLiveHolder}, Needs: "Done has no prior report and no declared files; when a verdict is required or a sealed verdict is present, that verdict passes", ClearsBlockGroup: true},
		{From: StateInProgress, Via: []string{"Complete", "CompleteBound"}, Writer: "complete", To: StateCompleted, Who: []string{WhoScheduler, WhoHarnessShell}, Needs: "a report exists; CompleteBound also has a nonnegative PR number and, when positive, a valid free binding; when a verdict is required or a sealed verdict is present, that verdict passes", ClearsBlockGroup: true},
		{From: StateInProgress, Via: []string{"Done", "Complete", "CompleteBound"}, Writer: "block", To: StateBlocked, Who: []string{WhoLiveHolder, WhoScheduler, WhoHarnessShell}, Needs: "Done has no prior report and no declared files; Complete and CompleteBound have a report; CompleteBound also has a nonnegative PR number and, when positive, a valid free binding; a verdict is required but absent, or a sealed verdict is present and does not pass"},
		{From: StateInProgress, Via: []string{"RefuseCommit", "RefuseCommitPaths"}, Writer: "block", To: StateBlocked, Who: []string{WhoScheduler, WhoHarnessShell}, Needs: "paths are exact, a report exists, the route is empty, lead, or owner, and the reason code is non-empty"},
		{From: StateInProgress, Via: []string{"Block", "BlockWithPaths"}, Writer: "block", To: StateBlocked, Who: []string{WhoLiveHolder}},
		{From: StateInProgress, Via: []string{"Fail"}, Writer: "Fail", To: StatePending, Who: []string{WhoLiveHolder}},
		{From: StateInProgress, Via: []string{"Release"}, Writer: "Release", To: StatePending, Who: []string{WhoAssertedHandle}},
		{From: StateInProgress, Via: []string{"ExpireDue"}, Writer: "ExpireDue", To: StatePending, Who: []string{WhoScheduler}, Needs: "no report; the lease is due"},
		{From: StateInProgress, Via: []string{"Cancel"}, Writer: "Cancel", To: StateCancelled, Who: []string{WhoAssertedHandle}},
		{From: StateBlocked, Via: []string{"ResetBy"}, Writer: "ResetBy", To: StatePending, Who: []string{WhoCreator, WhoAssertedHandle}, Needs: "the caller is non-empty, the route is not owner, and the caller matches the creator when recorded; when no creator is recorded, any non-empty caller is accepted", ClearsBlockGroup: true},
		{From: StateBlocked, Via: []string{"Reset"}, Writer: "Reset", To: StatePending, Who: []string{WhoScheduler}, ClearsBlockGroup: true},
		{From: StateBlocked, Via: []string{"ResetIfBlocked"}, Writer: "ResetIfBlocked", To: StatePending, Who: []string{WhoScheduler}, Needs: "the block reason matches, and the optional match function is absent or accepts the task", ClearsBlockGroup: true},
		{From: StateBlocked, Via: []string{"Widen", "WidenGated"}, Writer: "WidenGated", To: StatePending, Who: []string{WhoProvenLead}, Needs: "added paths and their union are valid and non-empty, reason and actor are non-empty, actor is not holder or reporter, no active file conflicts, the task has no report and has refused paths, and the block reason is widen_requested or growth_gate_required", ClearsBlockGroup: true},
		{From: StateBlocked, Via: []string{"Widen", "WidenGated"}, Writer: "WidenGated", To: StateInProgress, Who: []string{WhoProvenLead}, Needs: "added paths and their union are valid and non-empty, reason and actor are non-empty, actor is not holder or reporter, no active file conflicts, and a report exists", ClearsBlockGroup: true},
		{From: StateBlocked, Via: []string{"Escalate"}, Writer: "block", To: StateBlocked, Who: []string{WhoProvenLead}, Needs: "the reason code is non-empty"},
		{From: StateBlocked, Via: []string{"Cancel"}, Writer: "Cancel", To: StateCancelled, Who: []string{WhoAssertedHandle}},
	}
}

// Outcomes returns attempt outcomes in their declaration order.
func Outcomes() []string {
	return []string{OutcomeCompleted, OutcomeUnclaimed, OutcomeExpired, OutcomeFailed, OutcomeReleased, OutcomeBlocked, OutcomeCancelled, OutcomeCommitRefused}
}

// Routes returns block routes in their declaration order.
func Routes() []string { return []string{RouteLead, RouteOwner} }

// BlockGroup returns the JSON names of the fields that form a task's block
// group. The tags are reflected from Task so tag changes cannot drift.
func BlockGroup() []string {
	typ := reflect.TypeOf(Task{})
	names := []string{"BlockReason", "BlockRoute", "BlockDetail", "BlockAttempt", "RefusedPaths"}
	out := make([]string, 0, len(names))
	for _, name := range names {
		field, ok := typ.FieldByName(name)
		if !ok {
			panic("task block-group field missing: " + name)
		}
		out = append(out, strings.Split(field.Tag.Get("json"), ",")[0])
	}
	return out
}

// OutcomeSets returns the six failed-like groupings used by readers.
func OutcomeSets() []OutcomeSetContract {
	classifiedWorkFailure := "the attempt detail is classified true in RefusalWorkFailure"
	blockedWorkFailure := "the attempt detail is classified true in RefusalWorkFailure, or is unclassified (blocked defaults to a work failure)"
	return []OutcomeSetContract{
		{Name: "retry_budget", ReaderPackage: "internal/scheduler", ReaderFunc: "DefaultRouter.Route", Outcomes: []string{OutcomeFailed, OutcomeExpired}},
		{Name: "router_retry_effect", ReaderPackage: "internal/routing", ReaderFunc: "effect", Outcomes: []string{OutcomeFailed, OutcomeExpired, OutcomeCommitRefused, OutcomeBlocked}, Conditional: []ConditionalOutcome{{Outcome: OutcomeCommitRefused, Condition: classifiedWorkFailure}, {Outcome: OutcomeBlocked, Condition: blockedWorkFailure}}},
		{Name: "weekly_report_failed", ReaderPackage: "internal/routing", ReaderFunc: "failedOutcome", Outcomes: []string{OutcomeFailed, OutcomeExpired, OutcomeCommitRefused, OutcomeBlocked}},
		{Name: "verifier_fail_without_verdict", ReaderPackage: "internal/routing", ReaderFunc: "verifierResult", Outcomes: []string{OutcomeFailed, OutcomeCommitRefused, OutcomeBlocked}, Conditional: []ConditionalOutcome{{Outcome: OutcomeFailed, Condition: "the verifier is mechanical and the attempt has no verdict"}, {Outcome: OutcomeCommitRefused, Condition: "the verifier is mechanical and the attempt has no verdict"}, {Outcome: OutcomeBlocked, Condition: "the verifier is mechanical, the attempt has no verdict, and the detail is not verdict_missing"}}},
		{Name: "exit_scan", ReaderPackage: "internal/scheduler", ReaderFunc: "scanAttemptsLog", Outcomes: []string{OutcomeFailed, OutcomeExpired, OutcomeUnclaimed}},
		{Name: "seat_lapsed", ReaderPackage: "internal/scheduler", ReaderFunc: "idleSeatExcept", Outcomes: []string{OutcomeUnclaimed, OutcomeExpired}},
	}
}
