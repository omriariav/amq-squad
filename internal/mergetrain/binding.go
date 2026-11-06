package mergetrain

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// The task-to-PR binding (3-2 fixes 2 to 4, lead rulings). The harness commit
// step (2b-2 milestone B) binds a task to its PR when it opens it, and the
// train re-binds after its own rebase push, both through task.Store.BindPR
// (the store's lock and journal). The train reads the task through that
// binding, keyed by repository and PR number, never through a task id the
// request names; a real run's request names PR numbers only.
//
// The declared-file matcher (Declared) is swapped for milestone B's
// task.MatchPath at milestone B's merge.

// BindTasks fills each PR of a real-run request from the task bound to it:
// its id, declared files and pushed head. No bound task, more than one, or a
// binding with no pr_head marks the PR unbound and the check refuses it
// (task_unbound).
func BindTasks(store task.Store, repository string, req *Request) error {
	for i := range req.PRs {
		if err := bindItem(store, repository, &req.PRs[i]); err != nil {
			return err
		}
	}
	return nil
}

// BindUnnamed is BindTasks for the items that name no task and no declared
// files (brief 3.1.1-2 item 6): a dry run reads their binding as the real
// run does, so a request naming only the PR plans what the real run checks.
// An item that names its task or its files is left as it is.
func BindUnnamed(store task.Store, repository string, req *Request) error {
	for i := range req.PRs {
		if it := &req.PRs[i]; it.Task == "" && len(it.DeclaredFiles) == 0 {
			if err := bindItem(store, repository, it); err != nil {
				return err
			}
		}
	}
	return nil
}

// BindDryRun fills task data for the dry-run request forms. Named items keep
// their requested task id, but read the binding by repository and PR exactly
// as a real run does, so both paths check the same pushed head.
func BindDryRun(store task.Store, repository string, req *Request) error {
	for i := range req.PRs {
		it := &req.PRs[i]
		if it.Task == "" && len(it.DeclaredFiles) == 0 {
			if err := bindItem(store, repository, it); err != nil {
				return err
			}
			continue
		}
		if it.Task == "" || len(it.DeclaredFiles) > 0 {
			continue
		}
		requested := it.Task
		tk, err := store.Get(requested)
		if err != nil {
			return fmt.Errorf("PR #%d task %s: %w", it.Number, requested, err)
		}
		it.DeclaredFiles = tk.DeclaredFiles
		bound, why, err := boundTask(store, repository, it.Number)
		if err != nil {
			return err
		}
		switch {
		case why != "":
			it.Unbound = why
		case bound.ID != requested:
			it.Unbound = fmt.Sprintf("task %s is named, but task %s is bound to %s#%d", requested, bound.ID, repository, it.Number)
		default:
			it.BoundHead = bound.PRHead
		}
	}
	return nil
}

func bindItem(store task.Store, repository string, it *Item) error {
	b, why, err := boundTask(store, repository, it.Number)
	if err != nil {
		return err
	}
	if why != "" {
		it.Unbound = why
		return nil
	}
	it.Task, it.DeclaredFiles, it.BoundHead = b.ID, b.DeclaredFiles, b.PRHead
	return nil
}

// boundTask reads the store for the one task bound to (repository, pr).
func boundTask(store task.Store, repository string, pr int) (task.Task, string, error) {
	all, err := task.Snapshot(store.Dir)
	if err != nil {
		return task.Task{}, "", fmt.Errorf("read task store: %w", err)
	}
	var bound []task.Task
	for _, t := range all {
		if t.PRNumber == pr && strings.EqualFold(t.PRRepository, repository) {
			bound = append(bound, t)
		}
	}
	switch {
	case len(bound) != 1:
		return task.Task{}, fmt.Sprintf("%d tasks are bound to %s#%d", len(bound), repository, pr), nil
	case bound[0].PRHead == "":
		return task.Task{}, fmt.Sprintf("task %s is bound to %s#%d with no pr_head", bound[0].ID, repository, pr), nil
	}
	return bound[0], "", nil
}

// ErrBindingFailed: the train could not re-bind a task after its own rebase;
// the run stops before the merge, and the next run sees pr_head_moved.
var ErrBindingFailed = errors.New("binding_failed")

// ErrBindingReplayPending: the train's binding write crossed the task
// store's commit point and failed after it (*task.CommittedError): the
// binding is durable and the store replays its after-image at its next
// locked access. The run stops, in these words, not as binding_failed (the
// plan reviewer on #903).
var ErrBindingReplayPending = errors.New("the binding is written; the task store's replay is pending; the next run continues from it")

// bindingError is a failed train binding write in words: a CommittedError
// is ErrBindingReplayPending, anything else ErrBindingFailed.
func bindingError(taskID, head string, err error) error {
	var committed *task.CommittedError
	if errors.As(err, &committed) {
		return fmt.Errorf("%w (task %s to %s: %v)", ErrBindingReplayPending, taskID, short(head), err)
	}
	return fmt.Errorf("%w: task %s to %s: %v", ErrBindingFailed, taskID, short(head), err)
}

// ErrHeadSuperseded: bindAdmitted found the head superseded by a lead
// re-bind decision in the journal read under the audit lock, and wrote no
// binding.
var ErrHeadSuperseded = errors.New("head superseded by a lead re-bind decision")

// bindWrite is one binding write: the task's binding of PR moves from the
// bound head From to To. Conditional makes it hold only while the task is
// still bound at From (BindPRFrom, the lead's verb). Readmit is that verb,
// the one re-admission of a superseded head. Readmitted, when set, builds
// the verb's rebind_readmitted entry from whether the head was admitted;
// Undo, when set, builds the entry that undoes it when the write fails
// (the verb's rebind_superseded with the reason rebind_failed). Both are
// built by the caller (rebind.go) and appended by bindAdmitted inside the
// write's critical section.
type bindWrite struct {
	Task, Repository string
	PR               int
	From, To         string
	Conditional      bool
	Readmit          bool
	Readmitted       func(admitted bool) Entry
	Undo             func(err error) Entry
}

// bindAdmitted is the only binding writer in the train and the lead's verb
// (Codex's fourth pass on #903): every binding write of this package goes
// through it, and a source guard fails on any other reference to BindPR or
// BindPRFrom in the package. The harness writes bindings outside it, and
// outside this claim (Codex's fifth pass): the commit step's
// task.Store.BindPR (internal/cli/seat_commit.go:221, the BindPR hook of
// internal/seatcommit/commit.go:1415) and task.Store.CompleteBound
// (internal/task/scope.go:140, from internal/cli/seat_commit.go:123 and
// internal/scheduler/scheduler.go:740). It is one critical section under the journal lock
// (Audit.AuditLockPath, `<audit.jsonl>.lock`, the exclusive flock every
// Append takes, so every writer of rebind_superseded and rebind_readmitted
// takes it too): the fresh read of the journal, headAdmitted on that read,
// for the lead's verb its rebind_readmitted entry (journaled before the
// write, as update_requested before the update), the binding write, and,
// when the write fails, the verb's undo entry (a terminal
// rebind_superseded, so the journal shows the head not admitted).
// The two orders serialize: a supersede appended first is seen by the
// fresh read and the write is refused; one appended after a binding write
// lands in the journal after it, so trainHeads and every later admission
// read it. A Readmit write binds a superseded head and reports that it was
// not admitted (admitted false). Otherwise a superseded head is refused:
// no binding is written, record (when set) gets a reconcile entry naming
// the lead's merge-train rebind verb once the lock is released, and the
// error is ErrHeadSuperseded. A readmission that could not be appended
// writes no binding and returns an error wrapping errReadmittedNotWritten.
// A write that crossed the store's commit point and failed after it
// (*task.CommittedError) is returned as it is, with no undo: the binding
// is durable.
func bindAdmitted(a Audit, b Binder, record func(Entry) error, w bindWrite) (admitted bool, err error) {
	err = a.locked(func() error {
		journal, err := a.Read()
		if err != nil {
			return err
		}
		admitted = headAdmitted(journal, w.Repository, w.PR, w.To)
		if !admitted && !w.Readmit {
			return ErrHeadSuperseded
		}
		if w.Readmitted != nil {
			if aerr := a.appendLocked(w.Readmitted(admitted)); aerr != nil {
				return fmt.Errorf("%w: %v", errReadmittedNotWritten, aerr)
			}
		}
		if w.Conditional {
			err = b.BindPRFrom(w.Task, w.Repository, w.PR, w.From, w.To)
		} else {
			err = b.BindPR(w.Task, w.Repository, w.PR, w.To)
		}
		// a CommittedError crossed the store's commit point: the binding is
		// durable and the store replays its after-image at the next locked
		// access, so it counts as written (Codex's fifth pass on #903): the
		// readmission stands and there is no undo. The undo runs only on an
		// error from before the commit point.
		var committed *task.CommittedError
		if err != nil && w.Undo != nil && !errors.As(err, &committed) {
			if uerr := a.appendLocked(w.Undo(err)); uerr != nil {
				return errors.Join(err, fmt.Errorf("the undo entry was not written: %w", uerr))
			}
		}
		return err
	})
	if errors.Is(err, ErrHeadSuperseded) && record != nil {
		if rerr := record(Entry{Kind: "reconcile", PR: w.PR, Task: w.Task, Head: w.From, NewHead: w.To,
			Detail: fmt.Sprintf("the head %s was superseded by a lead re-bind decision before the binding write; the train does not bind it, only merge-train rebind re-admits it\nbinding not recovered", short(w.To))}); rerr != nil {
			return admitted, errors.Join(err, rerr)
		}
	}
	return admitted, err
}

// errReadmittedNotWritten: the verb's rebind_readmitted entry could not be
// appended, so no binding was written.
var errReadmittedNotWritten = errors.New("the readmission entry was not written")

// Binder writes a task's PR binding. Its BindPR and BindPRFrom are called
// only by bindAdmitted.
type Binder interface {
	BindPR(taskID, repository string, pr int, head string) error
	// BindPRFrom binds only while the task is still bound to from, under
	// the store's lock (the lead's re-bind; task.ErrPRBindingMismatch
	// otherwise).
	BindPRFrom(taskID, repository string, pr int, from, head string) error
	RecordPRMerge(taskID, repository string, pr int, head, commit string) error
}

// StoreBinder binds through task.Store.BindPR.
type StoreBinder struct {
	Store task.Store
	Now   func() time.Time
}

func (b StoreBinder) BindPR(taskID, repository string, pr int, head string) error {
	now := time.Now()
	if b.Now != nil {
		now = b.Now()
	}
	_, err := b.Store.BindPR(taskID, repository, pr, head, now)
	return err
}

func (b StoreBinder) BindPRFrom(taskID, repository string, pr int, from, head string) error {
	now := time.Now()
	if b.Now != nil {
		now = b.Now()
	}
	_, err := b.Store.BindPRFrom(taskID, repository, pr, from, head, now)
	return err
}

func (b StoreBinder) RecordPRMerge(taskID, repository string, pr int, head, commit string) error {
	now := time.Now()
	if b.Now != nil {
		now = b.Now()
	}
	_, err := b.Store.RecordPRMerge(taskID, repository, pr, head, commit, now)
	return err
}
