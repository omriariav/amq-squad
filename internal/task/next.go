package task

import (
	"sort"
	"strings"
	"time"
)

// The roles a next entry names (S3-1): the squad's lead, the task's lease
// holder, and the owner, who answers through Buzz.
const (
	NextLead   = "lead"
	NextHolder = "holder"
	NextOwner  = "owner"
)

// NextEntry is one thing that can be done to a task next: the command, who
// runs it, and, when the task record cannot show every condition the move
// needs, those conditions (it may still refuse).
type NextEntry struct {
	Verb      string `json:"verb"`
	Who       string `json:"who"`
	MayRefuse string `json:"may_refuse,omitempty"`
}

// NextCaller is whom next is for: the roles to list (none: every role) and
// the caller's handle ("" when unknown), which the creator check reads.
type NextCaller struct {
	Roles  []string
	Handle string
}

// nextMethod is a Via method a lead, holder or owner runs from the CLI: its
// command, and the roles its asserted_handle Who stands for. A method absent
// here (Grant, Claim, ClaimServing, Complete, CompleteBound, RefuseCommit,
// RefuseCommitPaths, ExpireDue, Reset, ResetIfBlocked, Add,
// AddWithFileWaits) has no next entry: the scheduler, the harness or a
// seat's claim of its offer runs it, or it creates the task.
type nextMethod struct {
	verb     string
	asserted []string
}

var nextMethods = map[string]nextMethod{
	"GrantClean":     {verb: "dispatch --task", asserted: []string{NextLead}},
	"Block":          {verb: "task block", asserted: []string{NextLead}},
	"BlockWithPaths": {verb: "task block", asserted: []string{NextLead}},
	"Cancel":         {verb: "task cancel", asserted: []string{NextLead}},
	"Done":           {verb: "task done"},
	"Fail":           {verb: "task fail"},
	"Release":        {verb: "task release", asserted: []string{NextHolder, NextLead}},
	"ResetBy":        {verb: "task reset", asserted: []string{NextLead}},
	"Widen":          {verb: "task widen"},
	"WidenGated":     {verb: "task widen"},
	"Escalate":       {verb: "task widen"},
}

// nextRoles is the roles a row's Who stands for, for a method of nextMethods.
func nextRoles(method nextMethod, who string) []string {
	switch who {
	case WhoLiveHolder:
		return []string{NextHolder}
	case WhoProvenLead, WhoCreator:
		return []string{NextLead}
	case WhoAssertedHandle:
		return method.asserted
	}
	return nil // scheduler, harness_shell
}

// The conditions a move needs that the task record cannot show.
const (
	nextMayRefuseDispatch = "dispatch checks more than the task record shows: its dependencies completed with their PRs merged at the bound head, no active task holding its files, and the named seat free, cleaned, allowed to run the task's class, and given the owner grant's verifier command for a class in the verifier gate"
	nextMayRefuseWiden    = "the owner's grant must be readable and allow the added paths, which must be refused paths of the task, valid, and not overlap another active task's declared files; a widening past a grant limit needs the owner's growth gate (the command escalates the task to the owner); a re-armed commit must pass the commit step's check again"
	nextMayRefuseGate     = "the owner's growth gate must be accepted first"
	nextMayRefuseSelf     = "the widening handle must not hold or have reported the task"
)

// The commit step's two refusal codes task widen accepts besides the growth
// gate (the CLI's validateBlockedWiden, internal/cli/task_widen.go):
// seatcommit's ReasonOutside and ReasonProtected are these constants, as its
// ReasonGrowthGate is ReasonGrowthGateRequired (seatcommit imports this
// package, so next reads them here).
const (
	WidenReasonOutside   = "outside_declared_files"
	WidenReasonProtected = "protected_path"
)

// nextWidenable is the CLI's widen precondition, read from the record: a
// commit refusal that kept its report for a protected path or the growth
// gate; a refusal for files outside the declared set with refused paths; or
// a seat's typed request (no report, its attempt bound, refused paths,
// widen_requested or growth_gate_required).
func nextWidenable(t Task) bool {
	switch {
	case t.Report != nil && (t.BlockReason == WidenReasonProtected || t.BlockReason == ReasonGrowthGateRequired):
		return true
	case t.BlockReason == WidenReasonOutside:
		return len(t.RefusedPaths) > 0
	}
	return t.Report == nil && t.BlockAttempt > 0 && len(t.RefusedPaths) > 0 &&
		(t.BlockReason == ReasonWidenRequested || t.BlockReason == ReasonGrowthGateRequired)
}

// Next is what can be done to the task next, by the caller's roles, derived
// from the declared moves (Moves) and the task record alone: the moves out of
// the task's state, through each Via method a lead, holder or owner runs,
// as each role the row's Who stands for. A condition the record shows (the
// live lease and its holder, the done report, the block route, the creator,
// a typed widen request) is evaluated, and a move it refuses is not listed;
// any other condition is the entry's may_refuse. A task blocked for the owner
// also lists the owner's answer through Buzz. Never nil.
func Next(t Task, now time.Time, caller NextCaller) []NextEntry {
	wanted := func(role string) bool {
		if len(caller.Roles) == 0 {
			return true
		}
		for _, r := range caller.Roles {
			if r == role {
				return true
			}
		}
		return false
	}
	out := []NextEntry{}
	seen := map[string]int{}
	add := func(e NextEntry) {
		if !wanted(e.Who) {
			return
		}
		key := e.Verb + "\x00" + e.Who
		if i, ok := seen[key]; ok {
			if out[i].MayRefuse == "" || e.MayRefuse == "" {
				out[i].MayRefuse = ""
			}
			return
		}
		seen[key] = len(out)
		out = append(out, e)
	}
	for _, move := range Moves() {
		if move.From != t.State || move.From == "" {
			continue
		}
		for _, via := range move.Via {
			method, ok := nextMethods[via]
			if !ok {
				continue
			}
			for _, who := range move.Who {
				for _, role := range nextRoles(method, who) {
					if e, ok := nextEntry(t, now, caller, move, via, method, role); ok {
						add(e)
					}
				}
			}
		}
	}
	if t.State == StateBlocked && t.BlockRoute == RouteOwner {
		add(NextEntry{Verb: "answer through Buzz", Who: NextOwner})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Who != out[j].Who {
			return nextRoleOrder(out[i].Who) < nextRoleOrder(out[j].Who)
		}
		return out[i].Verb < out[j].Verb
	})
	return out
}

func nextRoleOrder(role string) int {
	switch role {
	case NextHolder:
		return 0
	case NextLead:
		return 1
	}
	return 2
}

// nextEntry evaluates one (row, method, role) against the record.
func nextEntry(t Task, now time.Time, caller NextCaller, move MoveContract, via string, method nextMethod, role string) (NextEntry, bool) {
	e := NextEntry{Verb: method.verb, Who: role}
	liveLease := t.Lease != nil && now.Before(t.Lease.ExpiresAt)
	holderIsCaller := caller.Handle == "" || (t.Lease != nil && t.Lease.Holder == caller.Handle)
	switch via {
	case "Done":
		// the holder's done report: once filed, Done refuses
		return e, liveLease && holderIsCaller && t.Report == nil
	case "Fail", "Block", "BlockWithPaths":
		if move.From == StateInProgress {
			return e, liveLease && holderIsCaller
		}
		return e, true // a pending task: the lead blocks it
	case "Release":
		if role == NextHolder {
			return e, t.Lease != nil && holderIsCaller
		}
		return e, t.Lease != nil
	case "Cancel":
		return e, true
	case "GrantClean":
		e.MayRefuse = nextMayRefuseDispatch
		return e, true
	case "ResetBy":
		// the record shows the route and the creator
		if t.BlockRoute == RouteOwner {
			return e, false
		}
		if t.CreatedBy != "" {
			if caller.Handle != "" && caller.Handle != t.CreatedBy {
				return e, false
			}
			if caller.Handle == "" {
				e.MayRefuse = "only its creator " + t.CreatedBy + " resets it"
			}
		}
		return e, true
	case "Widen", "WidenGated", "Escalate":
		// one command, task widen: the lead's widen re-arms a refused commit
		// (to in progress), returns a typed request (to pending), or, past a
		// grant limit, escalates to the owner (blocked); it runs only on a
		// widenable block, and never for the handle that holds or reported
		// the task (self_widen)
		if !nextWidenable(t) {
			return e, false
		}
		if caller.Handle != "" && ((t.Lease != nil && t.Lease.Holder == caller.Handle) || (t.Report != nil && t.Report.By == caller.Handle)) {
			return e, false
		}
		reasons := []string{nextMayRefuseWiden}
		if caller.Handle == "" {
			reasons = append(reasons, nextMayRefuseSelf)
		}
		if t.BlockRoute == RouteOwner {
			reasons = append(reasons, nextMayRefuseGate)
		}
		e.MayRefuse = strings.Join(reasons, "; ")
		return e, true
	}
	return e, false
}
