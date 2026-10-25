package task

import (
	"fmt"
	"strings"
	"time"
)

// The lead widening path of the harness commit step (plan 4.3, 7.2,
// amendment 21): a recorded lead decision adds paths to declared_files
// inside the grant. The grant checks (protected paths, size cap) are the
// caller's (internal/seatcommit); the store keeps the file-lock exclusion
// and refuses a widening by the seat that holds or reported the task.

// RefusalFileLock is the reason code of a widening that would overlap another
// active task's declared files.
const RefusalFileLock = "file_lock_conflict"

// RefusalSelfWiden is the reason code of a widening asked for by the seat
// that holds or reported the task: the seat never widens its own set.
const RefusalSelfWiden = "self_widen"

// WidenRefusal is a widening the store refuses; the reason code goes to the
// lead, who may wait for the conflicting tasks or reassign.
type WidenRefusal struct {
	TaskID     string
	ReasonCode string
	Conflicts  []string
}

func (e *WidenRefusal) Error() string {
	if e.ReasonCode == RefusalSelfWiden {
		return fmt.Sprintf("widen %s refused (%s): the seat that holds or reported the task never widens its own set", e.TaskID, e.ReasonCode)
	}
	return fmt.Sprintf("widen %s refused (%s): overlaps the declared files of %s", e.TaskID, e.ReasonCode, strings.Join(e.Conflicts, ", "))
}

// Widen records a lead decision that adds paths to declared_files. A task
// blocked by a commit refusal is re-armed: its lease goes back to the
// reporting seat, claimed, so the commit step runs again.
func (s Store) Widen(id, actor, reason string, paths []string, dur time.Duration, now time.Time) (Task, error) {
	return s.WidenGated(id, actor, reason, paths, nil, dur, now)
}

// WidenGated is Widen with the growth-gate targets that accepted owner gates
// opened for it, recorded on the scope change for the commit step.
func (s Store) WidenGated(id, actor, reason string, paths, gates []string, dur time.Duration, now time.Time) (Task, error) {
	added, err := CleanDeclaredFiles(paths)
	if err != nil {
		return Task{}, err
	}
	actor = strings.TrimSpace(actor)
	return s.mutate(id, now, func(t *Task, all *tasks) error {
		if Terminal(t.State) || len(added) == 0 || strings.TrimSpace(reason) == "" || actor == "" {
			return fmt.Errorf("widen needs a non-terminal task, paths, an actor and a reason")
		}
		if (t.Lease != nil && t.Lease.Holder == actor) || (t.Report != nil && t.Report.By == actor) {
			return &WidenRefusal{TaskID: t.ID, ReasonCode: RefusalSelfWiden}
		}
		proposed, err := CleanDeclaredFiles(append(append([]string(nil), t.DeclaredFiles...), added...))
		if err != nil {
			return err
		}
		// The widened set must keep the exclusion the grant gave: under the
		// store lock, no other in-progress or blocked task may hold an
		// overlapping set (amendment 21 path; the lead gets a reason code).
		if t.State == StateInProgress || t.State == StateBlocked {
			var conflicts []string
			for _, o := range all.list {
				if o.ID != t.ID && (o.State == StateInProgress || o.State == StateBlocked) && FilesOverlap(proposed, o.DeclaredFiles) {
					conflicts = append(conflicts, o.ID)
				}
			}
			if len(conflicts) > 0 {
				return &WidenRefusal{TaskID: t.ID, ReasonCode: RefusalFileLock, Conflicts: conflicts}
			}
		}
		t.DeclaredFiles = proposed
		sc := ScopeChange{Actor: actor, At: now, Reason: strings.TrimSpace(reason), Added: added}
		if g := dedupe(gates); len(g) > 0 {
			sc.Gates = g
		}
		t.ScopeChanges = append(t.ScopeChanges, sc)
		if t.State == StateBlocked && t.Report != nil {
			if dur <= 0 {
				dur = DefaultLeaseDuration
			}
			at := now
			t.State, t.BlockReason, t.BlockRoute, t.BlockDetail, t.Attempt = StateInProgress, "", "", "", t.Attempt+1
			t.Lease = &Lease{Holder: t.Report.By, GrantedAt: now, ExpiresAt: now.Add(dur), ClaimedAt: &at, RenewedAt: &at}
		}
		return nil
	})
}

// Rearm returns a task blocked on its done report to the commit step: the
// lease goes back to the reporting seat, claimed (the owner's clear of a
// global-config quarantine; a widening re-arms through Widen).
func (s Store) Rearm(id string, dur time.Duration, now time.Time) (Task, error) {
	return s.mutate(id, now, func(t *Task, _ *tasks) error {
		if t.State != StateBlocked || t.Report == nil {
			return fmt.Errorf("task %s is %s with no done report; nothing to re-arm", t.ID, t.State)
		}
		if dur <= 0 {
			dur = DefaultLeaseDuration
		}
		at := now
		t.State, t.BlockReason, t.BlockRoute, t.BlockDetail, t.Attempt = StateInProgress, "", "", "", t.Attempt+1
		t.Lease = &Lease{Holder: t.Report.By, GrantedAt: now, ExpiresAt: now.Add(dur), ClaimedAt: &at, RenewedAt: &at}
		return nil
	})
}

// RefusalPRBoundElsewhere is the reason code of a PR binding whose number
// another task holds: a PR is never bound to two tasks.
const RefusalPRBoundElsewhere = "pr_bound_elsewhere"

// ErrPRBoundElsewhere is returned by BindPR and CompleteBound for a PR
// number another task holds.
var ErrPRBoundElsewhere = fmt.Errorf("%s", RefusalPRBoundElsewhere)

func prFree(t *Task, all *tasks, repository string, number int) error {
	if number == 0 {
		return nil
	}
	for _, o := range all.list {
		if o.ID != t.ID && o.PRNumber == number && strings.EqualFold(o.PRRepository, repository) {
			return fmt.Errorf("%w: %s PR #%d is bound to task %s", ErrPRBoundElsewhere, repository, number, o.ID)
		}
	}
	return nil
}

// CompleteBound is the commit step's acceptance and its PR binding in one
// transaction (P2 of the milestone B first pass): the task completes and,
// when the step has a PR (number > 0), is bound to it through BindPR's
// rule (3-2's pr_repository, pr_number and pr_head, the same validation).
// A PR another task holds refuses (pr_bound_elsewhere) and nothing is
// written. A push without a PR completes the task unbound.
func (s Store) CompleteBound(id, repository string, number int, head string, now time.Time) (Task, error) {
	return s.mutate(id, now, func(t *Task, all *tasks) error {
		if t.State != StateInProgress || t.Report == nil {
			return fmt.Errorf("task %s has no done report awaiting the commit step", t.ID)
		}
		if number < 0 {
			return fmt.Errorf("a PR binding needs a PR number")
		}
		if number > 0 {
			if err := bindPR(t, all, repository, number, head); err != nil {
				return err
			}
		}
		s.complete(t, now)
		return nil
	})
}

// WithSeatFree runs fn under the store's lock while the seat holds nothing
// and is not quarantined: CleanSeat's reservation of the seat (R3 of the
// milestone B first pass), so no grant or claim lands between its check and
// its reset. fn gets the store's tasks and must not call the store.
func (s Store) WithSeatFree(handle string, fn func(list []Task) error) error {
	return s.withLock(true, func() error {
		list, err := readDir(s.Dir)
		if err != nil {
			return err
		}
		if held := HeldBy(handle, list); held != nil {
			return fmt.Errorf("seat %s holds task %s; its worktree is cleaned only between leases", handle, held.ID)
		}
		if err := quarantined(s.Dir, handle); err != nil {
			return err
		}
		return fn(list)
	})
}

// Escalate routes a blocked task to the owner: a widening the lead asked for
// crosses a grant limit, so the decision is the owner-signed growth gate
// (plan 4.3). The report stays; the seat stays reserved.
func (s Store) Escalate(id, reasonCode, detail string, now time.Time) (Task, error) {
	return s.mutate(id, now, func(t *Task, _ *tasks) error {
		if t.State != StateBlocked {
			return fmt.Errorf("task %s is %s; only a blocked task escalates to the owner", t.ID, t.State)
		}
		return block(t, reasonCode, detail, RouteOwner)
	})
}
