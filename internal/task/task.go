// Package task is amq-squad's native, binary-neutral pull-based task store:
// the lead decomposes a goal into tasks under .amq-squad/tasks/<session>/, and
// workers (any binary) claim them and self-schedule around dependencies. It is
// the amq-squad-native analog of the amq swarm task list — but with a create
// path (Add), so a Codex or Claude lead can decompose the goal. See
// docs/task-store-design.md.
package task

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/flock"
)

// Status values for the five-state machine.
const (
	StatusPending    = "pending"
	StatusInProgress = "in_progress"
	StatusCompleted  = "completed"
	StatusFailed     = "failed"
	StatusBlocked    = "blocked"
)

// Task is one unit of work in a workstream's task store.
type Task struct {
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	Description   string    `json:"description,omitempty"`
	Status        string    `json:"status"`
	AssignedTo    string    `json:"assigned_to,omitempty"`
	DependsOn     []string  `json:"depends_on,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	Evidence      string    `json:"evidence,omitempty"`
	FailureReason string    `json:"failure_reason,omitempty"`
	BlockReason   string    `json:"block_reason,omitempty"`
	ResetReason   string    `json:"reset_reason,omitempty"`
	Dispatch      *Dispatch `json:"dispatch,omitempty"`
}

// AddInput is the create payload for Add.
type AddInput struct {
	Title       string
	Description string
	DependsOn   []string
	AssignTo    string
}

// Dispatch records the durable AMQ message linked to a native task.
type Dispatch struct {
	Assignee     string    `json:"assignee,omitempty"`
	Thread       string    `json:"thread,omitempty"`
	Kind         string    `json:"kind,omitempty"`
	Subject      string    `json:"subject,omitempty"`
	MessageID    string    `json:"message_id,omitempty"`
	DispatchedAt time.Time `json:"dispatched_at,omitempty"`
}

// Dir is the task directory for a workstream: .amq-squad/tasks/<session>.
func Dir(projectDir, session string) string {
	return filepath.Join(projectDir, ".amq-squad", "tasks", session)
}

// Add creates a new pending task and returns it. The id is allocated under the
// store lock so concurrent adds never collide.
func Add(projectDir, session string, in AddInput, now time.Time) (Task, error) {
	if strings.TrimSpace(in.Title) == "" {
		return Task{}, fmt.Errorf("task title is required")
	}
	var created Task
	err := withLock(projectDir, session, func(dir string) error {
		tasks, err := readAll(dir)
		if err != nil {
			return err
		}
		// Validate dependency ids exist now (a typo'd dep would otherwise gate
		// the task forever). Because a dep must reference an already-created
		// task and ids increase monotonically (allocID = max+1), every edge
		// points from a higher id to a lower one — the dependency graph is a
		// DAG by construction, so cycles and self-dependencies are impossible
		// here (a self-dep references the not-yet-allocated id and fails this
		// existence check).
		byID := indexByID(tasks)
		for _, dep := range in.DependsOn {
			if _, ok := byID[dep]; !ok {
				return fmt.Errorf("depends-on task %q does not exist", dep)
			}
		}
		created = Task{
			ID:          allocID(tasks),
			Title:       strings.TrimSpace(in.Title),
			Description: strings.TrimSpace(in.Description),
			Status:      StatusPending,
			AssignedTo:  strings.TrimSpace(in.AssignTo),
			DependsOn:   dedupeNonEmpty(in.DependsOn),
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		return writeTask(dir, created)
	})
	return created, err
}

// List returns all tasks in the workstream, sorted by id.
func List(projectDir, session string) ([]Task, error) {
	tasks, err := readAll(Dir(projectDir, session))
	if err != nil {
		return nil, err
	}
	sortTasks(tasks)
	return tasks, nil
}

// Show returns one task by id.
func Show(projectDir, session, id string) (Task, error) {
	tasks, err := readAll(Dir(projectDir, session))
	if err != nil {
		return Task{}, err
	}
	for _, t := range tasks {
		if t.ID == strings.TrimSpace(id) {
			return t, nil
		}
	}
	return Task{}, fmt.Errorf("task %q not found in workstream %q", strings.TrimSpace(id), session)
}

// Claim moves a pending task to in_progress for handle, but only when every
// dependency is completed (dependency gating).
func Claim(projectDir, session, id, handle string, now time.Time) (Task, error) {
	if strings.TrimSpace(handle) == "" {
		return Task{}, fmt.Errorf("--me handle is required to claim a task")
	}
	return mutate(projectDir, session, id, func(t *Task, all map[string]*Task) error {
		if t.Status != StatusPending {
			return fmt.Errorf("task %s is %s, not pending; only pending tasks can be claimed", id, t.Status)
		}
		for _, dep := range t.DependsOn {
			d := all[dep]
			if d == nil {
				return fmt.Errorf("task %s depends on %s, which does not exist", id, dep)
			}
			if d.Status != StatusCompleted {
				return fmt.Errorf("task %s is blocked on %s (%s); complete it first", id, dep, d.Status)
			}
		}
		t.Status = StatusInProgress
		t.AssignedTo = strings.TrimSpace(handle)
		t.Evidence, t.FailureReason, t.BlockReason, t.ResetReason = "", "", "", ""
		t.UpdatedAt = now
		return nil
	})
}

// Done / Fail / Block are the in_progress → terminal transitions.
func Done(projectDir, session, id, actor, evidence string, now time.Time) (Task, error) {
	return terminal(projectDir, session, id, actor, StatusCompleted, func(t *Task) { t.Evidence = strings.TrimSpace(evidence) }, now)
}

func Fail(projectDir, session, id, actor, reason string, now time.Time) (Task, error) {
	return terminal(projectDir, session, id, actor, StatusFailed, func(t *Task) { t.FailureReason = strings.TrimSpace(reason) }, now)
}

func Block(projectDir, session, id, actor, reason string, now time.Time) (Task, error) {
	return terminal(projectDir, session, id, actor, StatusBlocked, func(t *Task) { t.BlockReason = strings.TrimSpace(reason) }, now)
}

// Reset returns a non-pending task to pending so it can be claimed again.
func Reset(projectDir, session, id, actor, reason string, now time.Time) (Task, error) {
	return mutate(projectDir, session, id, func(t *Task, _ map[string]*Task) error {
		if t.Status == StatusPending {
			return fmt.Errorf("task %s is already pending; reset requires a non-pending task", id)
		}
		if err := requireAssignee(t, actor, "reset"); err != nil {
			return err
		}
		t.Status = StatusPending
		t.AssignedTo = ""
		t.Evidence, t.FailureReason, t.BlockReason = "", "", ""
		if trimmed := strings.TrimSpace(reason); trimmed != "" {
			t.ResetReason = trimmed
		} else {
			t.ResetReason = ""
		}
		t.UpdatedAt = now
		return nil
	})
}

// LinkDispatch records durable AMQ metadata on a task.
func LinkDispatch(projectDir, session, id string, dispatch Dispatch, now time.Time) (Task, error) {
	return mutate(projectDir, session, id, func(t *Task, _ map[string]*Task) error {
		d := dispatch
		d.Assignee = strings.TrimSpace(d.Assignee)
		d.Thread = strings.TrimSpace(d.Thread)
		d.Kind = strings.TrimSpace(d.Kind)
		d.Subject = strings.TrimSpace(d.Subject)
		d.MessageID = strings.TrimSpace(d.MessageID)
		if d.DispatchedAt.IsZero() {
			d.DispatchedAt = now
		}
		t.Dispatch = &d
		t.UpdatedAt = now
		return nil
	})
}

// terminal applies an in_progress → completed/failed/blocked transition.
func terminal(projectDir, session, id, actor, to string, set func(*Task), now time.Time) (Task, error) {
	return mutate(projectDir, session, id, func(t *Task, _ map[string]*Task) error {
		if t.Status != StatusInProgress {
			return fmt.Errorf("task %s is %s, not in_progress; claim it before marking it %s", id, t.Status, to)
		}
		if err := requireAssignee(t, actor, to); err != nil {
			return err
		}
		t.Status = to
		set(t)
		t.UpdatedAt = now
		return nil
	})
}

func requireAssignee(t *Task, actor, verb string) error {
	actor = strings.TrimSpace(actor)
	if t.AssignedTo == "" {
		return nil
	}
	if actor == "" {
		return fmt.Errorf("--me handle is required to %s task %s assigned to %s", verb, t.ID, t.AssignedTo)
	}
	if actor != t.AssignedTo {
		return fmt.Errorf("task %s is assigned to %s; %s cannot mark it %s", t.ID, t.AssignedTo, actor, verb)
	}
	return nil
}

// --- internals ---

// mutate locks the store, loads all tasks, applies fn to the target task (with
// the full set available for dependency checks), and persists just that task.
func mutate(projectDir, session, id string, fn func(t *Task, all map[string]*Task) error) (Task, error) {
	id = strings.TrimSpace(id)
	var out Task
	err := withLock(projectDir, session, func(dir string) error {
		tasks, err := readAll(dir)
		if err != nil {
			return err
		}
		all := indexByID(tasks)
		t := all[id]
		if t == nil {
			return fmt.Errorf("task %q not found in workstream %q", id, session)
		}
		if err := fn(t, all); err != nil {
			return err
		}
		out = *t
		return writeTask(dir, *t)
	})
	return out, err
}

func withLock(projectDir, session string, fn func(dir string) error) error {
	dir := Dir(projectDir, session)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("ensure task dir %s: %w", dir, err)
	}
	return flock.WithLock(filepath.Join(dir, ".lock"), func() error { return fn(dir) })
}

func readAll(dir string) ([]Task, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read task dir: %w", err)
	}
	var tasks []Task
	for _, e := range entries {
		// Only finished task files. A crash leaves a "<id>.json.tmp" behind,
		// which ends in .tmp (not .json) and is correctly skipped here; the
		// real file only appears after the atomic rename, never partial.
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		var t Task
		if err := json.Unmarshal(b, &t); err != nil {
			return nil, fmt.Errorf("parse %s: %w", e.Name(), err)
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

func writeTask(dir string, t Task) error {
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal task: %w", err)
	}
	path := filepath.Join(dir, t.ID+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// indexByID maps id → *Task pointing into the input slice. The pointers are
// valid only for the lifetime of that slice (i.e. within the calling
// mutate/Add scope); callers must not retain them past the lock callback.
func indexByID(tasks []Task) map[string]*Task {
	m := make(map[string]*Task, len(tasks))
	for i := range tasks {
		m[tasks[i].ID] = &tasks[i]
	}
	return m
}

// allocID returns t<N+1> where N is the max numeric suffix among t<N> ids.
func allocID(tasks []Task) string {
	max := 0
	for _, t := range tasks {
		if n, ok := parseTaskNum(t.ID); ok && n > max {
			max = n
		}
	}
	return "t" + strconv.Itoa(max+1)
}

func parseTaskNum(id string) (int, bool) {
	if !strings.HasPrefix(id, "t") {
		return 0, false
	}
	n, err := strconv.Atoi(id[1:])
	if err != nil {
		return 0, false
	}
	return n, true
}

func dedupeNonEmpty(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func sortTasks(tasks []Task) {
	sort.Slice(tasks, func(i, j int) bool {
		ni, oki := parseTaskNum(tasks[i].ID)
		nj, okj := parseTaskNum(tasks[j].ID)
		if oki && okj {
			return ni < nj
		}
		return tasks[i].ID < tasks[j].ID
	})
}
