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
	From             string   `json:"from"`
	Via              []string `json:"via"`
	Writer           string   `json:"writer"`
	To               string   `json:"to"`
	Who              []string `json:"who"`
	Needs            string   `json:"needs,omitempty"`
	ClearsBlockGroup bool     `json:"clears_block_group"`
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
		{From: StatePending, Via: []string{"Grant", "GrantClean", "ClaimServing"}, Writer: "grant", To: StateInProgress, Who: []string{WhoScheduler, WhoAssertedHandle}, Needs: "no blocker; the seat holds nothing; the seat is leasable"},
		{From: StatePending, Via: []string{"Block", "BlockWithPaths"}, Writer: "block", To: StateBlocked, Who: []string{WhoScheduler, WhoAssertedHandle}},
		{From: StatePending, Via: []string{"Cancel"}, Writer: "Cancel", To: StateCancelled, Who: []string{WhoAssertedHandle}},
		{From: StateInProgress, Via: []string{"Done"}, Writer: "complete", To: StateCompleted, Who: []string{WhoLiveHolder}, Needs: "no declared files; a required verdict must pass", ClearsBlockGroup: true},
		{From: StateInProgress, Via: []string{"Complete", "CompleteBound"}, Writer: "complete", To: StateCompleted, Who: []string{WhoScheduler, WhoHarnessShell}, Needs: "a report; a required verdict must pass", ClearsBlockGroup: true},
		{From: StateInProgress, Via: []string{"Done", "Complete", "CompleteBound"}, Writer: "block", To: StateBlocked, Who: []string{WhoLiveHolder, WhoScheduler, WhoHarnessShell}, Needs: "a required verdict is missing or does not pass"},
		{From: StateInProgress, Via: []string{"RefuseCommitPaths"}, Writer: "block", To: StateBlocked, Who: []string{WhoScheduler, WhoHarnessShell}, Needs: "a report"},
		{From: StateInProgress, Via: []string{"Block", "BlockWithPaths"}, Writer: "block", To: StateBlocked, Who: []string{WhoLiveHolder}},
		{From: StateInProgress, Via: []string{"Fail"}, Writer: "Fail", To: StatePending, Who: []string{WhoLiveHolder}},
		{From: StateInProgress, Via: []string{"Release"}, Writer: "Release", To: StatePending, Who: []string{WhoAssertedHandle}},
		{From: StateInProgress, Via: []string{"ExpireDue"}, Writer: "ExpireDue", To: StatePending, Who: []string{WhoScheduler}, Needs: "no report; the lease is due"},
		{From: StateInProgress, Via: []string{"Cancel"}, Writer: "Cancel", To: StateCancelled, Who: []string{WhoAssertedHandle}},
		{From: StateBlocked, Via: []string{"ResetBy"}, Writer: "ResetBy", To: StatePending, Who: []string{WhoCreator}, Needs: "the route is not owner", ClearsBlockGroup: true},
		{From: StateBlocked, Via: []string{"Reset"}, Writer: "Reset", To: StatePending, Who: []string{WhoScheduler}, ClearsBlockGroup: true},
		{From: StateBlocked, Via: []string{"ResetIfBlocked"}, Writer: "ResetIfBlocked", To: StatePending, Who: []string{WhoScheduler}, Needs: "the block reason and the match", ClearsBlockGroup: true},
		{From: StateBlocked, Via: []string{"WidenGated"}, Writer: "WidenGated", To: StatePending, Who: []string{WhoProvenLead}, Needs: "no report; refused paths; reason widen_requested or growth_gate_required", ClearsBlockGroup: true},
		{From: StateBlocked, Via: []string{"WidenGated"}, Writer: "WidenGated", To: StateInProgress, Who: []string{WhoProvenLead}, Needs: "a report; the lead is not the reporter", ClearsBlockGroup: true},
		{From: StateBlocked, Via: []string{"Escalate"}, Writer: "block", To: StateBlocked, Who: []string{WhoProvenLead}, Needs: "the route becomes owner"},
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
		{Name: "verifier_fail", ReaderPackage: "internal/routing", ReaderFunc: "verifierResult", Outcomes: []string{OutcomeFailed, OutcomeCommitRefused, OutcomeBlocked}},
		{Name: "exit_scan", ReaderPackage: "internal/scheduler", ReaderFunc: "scanAttemptsLog", Outcomes: []string{OutcomeFailed, OutcomeExpired, OutcomeUnclaimed}},
		{Name: "seat_lapsed", ReaderPackage: "internal/scheduler", ReaderFunc: "idleSeatExcept", Outcomes: []string{OutcomeUnclaimed, OutcomeExpired}},
	}
}
