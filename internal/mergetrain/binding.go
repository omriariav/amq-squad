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

// Binder writes a task's PR binding.
type Binder interface {
	BindPR(taskID, repository string, pr int, head string) error
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
