// Package task is amq-squad's native, binary-neutral pull-based task store:
// the lead decomposes a goal into profile/session-scoped tasks, and workers
// (any binary) claim them and self-schedule around dependencies. It is the
// amq-squad-native analog of the amq swarm task list — but with a create path
// (Add), so a Codex or Claude lead can decompose the goal. See
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
	"github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// Status values for the six-state machine.
const (
	StatusPending    = "pending"
	StatusInProgress = "in_progress"
	StatusCompleted  = "completed"
	StatusFailed     = "failed"
	StatusBlocked    = "blocked"
	StatusCancelled  = "cancelled"
)

// AttentionLifecycle is the derived operator-attention lifecycle of a task.
// It is intentionally separate from the persisted execution status: existing
// stores keep their six-state schema while completed/cancelled work projects to
// closed, and replacement-linked cancellation projects to superseded.
type AttentionLifecycle string

const (
	AttentionLifecycleClosed     AttentionLifecycle = "closed"
	AttentionLifecycleSuperseded AttentionLifecycle = "superseded"
)

func AttentionLifecycleFor(t Task) AttentionLifecycle {
	switch t.Status {
	case StatusCompleted:
		return AttentionLifecycleClosed
	case StatusCancelled:
		if strings.TrimSpace(t.ReplacedBy) != "" {
			return AttentionLifecycleSuperseded
		}
		return AttentionLifecycleClosed
	default:
		return AttentionLifecycle(t.Status)
	}
}

func IsAttentionLifecycleTerminal(t Task) bool {
	switch AttentionLifecycleFor(t) {
	case AttentionLifecycleClosed, AttentionLifecycleSuperseded:
		return true
	default:
		return false
	}
}

const DefaultLeaseDuration = 2 * time.Hour

// Task is one unit of work in a workstream's task store.
type Task struct {
	ID                      string               `json:"id"`
	Title                   string               `json:"title"`
	Description             string               `json:"description,omitempty"`
	Status                  string               `json:"status"`
	AssignedTo              string               `json:"assigned_to,omitempty"`
	DependsOn               []string             `json:"depends_on,omitempty"`
	CreatedAt               time.Time            `json:"created_at"`
	UpdatedAt               time.Time            `json:"updated_at"`
	Evidence                string               `json:"evidence,omitempty"`
	FailureReason           string               `json:"failure_reason,omitempty"`
	BlockReason             string               `json:"block_reason,omitempty"`
	ResetReason             string               `json:"reset_reason,omitempty"`
	CancelReason            string               `json:"cancel_reason,omitempty"`
	ReadyAt                 *time.Time           `json:"ready_at,omitempty"`
	Lease                   *Lease               `json:"lease,omitempty"`
	DependencyOverrides     []DependencyOverride `json:"dependency_overrides,omitempty"`
	Replaces                string               `json:"replaces,omitempty"`
	ReplacedBy              string               `json:"replaced_by,omitempty"`
	ReviewOf                string               `json:"review_of,omitempty"`
	ReviewTasks             []string             `json:"review_tasks,omitempty"`
	FinalHead               string               `json:"final_head,omitempty"`
	Outbox                  []OutboxIntent       `json:"outbox,omitempty"`
	Releases                []ReleaseAudit       `json:"releases,omitempty"`
	NotificationSuppression *SuppressionAudit    `json:"completion_notification_suppressed,omitempty"`
	Dispatch                *Dispatch            `json:"dispatch,omitempty"`
}

// Lease is issued on every new claim. Expiry is evidence for reconcile and
// explicit recovery only: it never silently unclaims or reassigns a worker.
// Legacy in_progress task files without this field remain owned and are
// reported as legacy_unleased.
type Lease struct {
	Owner           string     `json:"owner"`
	IssuedAt        time.Time  `json:"issued_at"`
	RenewedAt       time.Time  `json:"renewed_at"`
	ExpiresAt       time.Time  `json:"expires_at"`
	StaleObservedAt *time.Time `json:"stale_observed_at,omitempty"`
}

type DependencyState struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
}

type DependencyOverride struct {
	Actor  string            `json:"actor"`
	Reason string            `json:"reason"`
	At     time.Time         `json:"at"`
	Unmet  []DependencyState `json:"unmet"`
}

const (
	OutboxPending   = "pending"
	OutboxSending   = "sending"
	OutboxUncertain = "delivery_uncertain"
	OutboxDelivered = "delivered"
	OutboxFailed    = "failed"
)

const (
	DeliveryFailedBeforeInvoke = "failed_before_invoke"
	DeliveryUncertain          = "delivery_uncertain"
	DeliveryDelivered          = "delivered"
)

// DeliveryOutcome is the typed command-boundary truth consumed by the task
// lifecycle. It deliberately distinguishes failures proven before AMQ was
// invoked from invoked/no-ID outcomes, which are unsafe to retry blindly.
type DeliveryOutcome struct {
	State     string
	MessageID string
	Error     string
}

type ReceiptLink struct {
	AttemptID string `json:"attempt_id"`
	Path      string `json:"path"`
}

// OutboxIntent is committed with the task transition before any AMQ send.
// A process crash in Sending is deliberately uncertain and never auto-retried.
type OutboxIntent struct {
	ID               string        `json:"id"`
	TaskID           string        `json:"task_id"`
	Type             string        `json:"type"`
	State            string        `json:"state"`
	From             string        `json:"from"`
	To               string        `json:"to"`
	Thread           string        `json:"thread,omitempty"`
	Kind             string        `json:"kind"`
	Subject          string        `json:"subject"`
	Body             string        `json:"body,omitempty"`
	MessageID        string        `json:"message_id,omitempty"`
	ReceiptAttemptID string        `json:"receipt_attempt_id,omitempty"`
	ReceiptPath      string        `json:"receipt_path,omitempty"`
	ReceiptAttempts  []ReceiptLink `json:"receipt_attempts,omitempty"`
	LastError        string        `json:"last_error,omitempty"`
	CreatedAt        time.Time     `json:"created_at"`
	UpdatedAt        time.Time     `json:"updated_at"`
	RetryAudits      []RetryAudit  `json:"retry_audits,omitempty"`
}

type RetryAudit struct {
	Actor                 string    `json:"actor"`
	Reason                string    `json:"reason"`
	At                    time.Time `json:"at"`
	PreviousState         string    `json:"previous_state,omitempty"`
	ConfirmedNotDelivered bool      `json:"confirmed_not_delivered,omitempty"`
	ReceiptAttemptID      string    `json:"receipt_attempt_id,omitempty"`
	ReceiptPath           string    `json:"receipt_path,omitempty"`
}

type ReleaseAudit struct {
	Actor  string    `json:"actor"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

type SuppressionAudit struct {
	Actor  string    `json:"actor"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

// AddInput is the create payload for Add.
type AddInput struct {
	Title       string
	Description string
	DependsOn   []string
	AssignTo    string
	ReviewOf    string
}

// Dispatch records the durable AMQ message linked to a native task.
type Dispatch struct {
	Sender           string    `json:"sender,omitempty"`
	Assignee         string    `json:"assignee,omitempty"`
	Thread           string    `json:"thread,omitempty"`
	Kind             string    `json:"kind,omitempty"`
	Subject          string    `json:"subject,omitempty"`
	MessageID        string    `json:"message_id,omitempty"`
	ReceiptAttemptID string    `json:"receipt_attempt_id,omitempty"`
	ReceiptPath      string    `json:"receipt_path,omitempty"`
	DispatchedAt     time.Time `json:"dispatched_at,omitempty"`
}

// Dir is the default-profile task directory for a workstream.
func Dir(projectDir, session string) string {
	return DirForProfile(projectDir, team.DefaultProfile, session)
}

func DirForProfile(projectDir, profile, session string) string {
	return namespace.TasksPath(projectDir, profile, session)
}

// Add creates a new pending task and returns it. The id is allocated under the
// store lock so concurrent adds never collide.
func Add(projectDir, session string, in AddInput, now time.Time) (Task, error) {
	return AddForProfile(projectDir, team.DefaultProfile, session, in, now)
}

func AddForProfile(projectDir, profile, session string, in AddInput, now time.Time) (Task, error) {
	if strings.TrimSpace(in.Title) == "" {
		return Task{}, fmt.Errorf("task title is required")
	}
	var created Task
	err := withLockForProfile(projectDir, profile, session, func(dir string) error {
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
		readyAt := (*time.Time)(nil)
		if len(dedupeNonEmpty(in.DependsOn)) == 0 {
			ready := now
			readyAt = &ready
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
			ReadyAt:     readyAt,
			ReviewOf:    strings.TrimSpace(in.ReviewOf),
		}
		changed := []Task{created}
		if created.ReviewOf != "" {
			target := byID[created.ReviewOf]
			if target == nil {
				return fmt.Errorf("review-of task %q does not exist", created.ReviewOf)
			}
			target.ReviewTasks = appendUniqueSorted(target.ReviewTasks, created.ID)
			target.UpdatedAt = now
			changed = append(changed, *target)
		}
		return commitTasks(dir, changed, now)
	})
	return created, err
}

// List returns all tasks in the workstream, sorted by id.
func List(projectDir, session string) ([]Task, error) {
	return ListForProfile(projectDir, team.DefaultProfile, session)
}

func ListForProfile(projectDir, profile, session string) ([]Task, error) {
	var tasks []Task
	err := withReadLockForProfile(projectDir, profile, session, func(dir string) error {
		var err error
		tasks, err = readAll(dir)
		return err
	})
	if err != nil {
		return nil, err
	}
	sortTasks(tasks)
	return tasks, nil
}

// Show returns one task by id.
func Show(projectDir, session, id string) (Task, error) {
	return ShowForProfile(projectDir, team.DefaultProfile, session, id)
}

func ShowForProfile(projectDir, profile, session, id string) (Task, error) {
	tasks, err := ListForProfile(projectDir, profile, session)
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
	return ClaimForProfile(projectDir, team.DefaultProfile, session, id, handle, now)
}

func ClaimForProfile(projectDir, profile, session, id, handle string, now time.Time) (Task, error) {
	return ClaimWithOptionsForProfile(projectDir, profile, session, id, ClaimOptions{Actor: handle, LeaseDuration: DefaultLeaseDuration, Now: now})
}

// Done / Fail / Block are the in_progress → terminal transitions.
func Done(projectDir, session, id, actor, evidence string, now time.Time) (Task, error) {
	return DoneForProfile(projectDir, team.DefaultProfile, session, id, actor, evidence, now)
}

func DoneForProfile(projectDir, profile, session, id, actor, evidence string, now time.Time) (Task, error) {
	result, err := DoneAtomicForProfile(projectDir, profile, session, id, DoneOptions{Actor: actor, Evidence: evidence, Notify: true, Now: now})
	return result.Task, err
}

func Fail(projectDir, session, id, actor, reason string, now time.Time) (Task, error) {
	return FailForProfile(projectDir, team.DefaultProfile, session, id, actor, reason, now)
}

func FailForProfile(projectDir, profile, session, id, actor, reason string, now time.Time) (Task, error) {
	return terminalForProfile(projectDir, profile, session, id, actor, StatusFailed, func(t *Task) { t.FailureReason = strings.TrimSpace(reason) }, now)
}

func Block(projectDir, session, id, actor, reason string, now time.Time) (Task, error) {
	return BlockForProfile(projectDir, team.DefaultProfile, session, id, actor, reason, now)
}

func BlockForProfile(projectDir, profile, session, id, actor, reason string, now time.Time) (Task, error) {
	return terminalForProfile(projectDir, profile, session, id, actor, StatusBlocked, func(t *Task) { t.BlockReason = strings.TrimSpace(reason) }, now)
}

// Reset returns a non-pending task to pending so it can be claimed again.
func Reset(projectDir, session, id, actor, reason string, now time.Time) (Task, error) {
	return ResetForProfile(projectDir, team.DefaultProfile, session, id, actor, reason, now)
}

func ResetForProfile(projectDir, profile, session, id, actor, reason string, now time.Time) (Task, error) {
	return mutateForProfile(projectDir, profile, session, id, func(t *Task, _ map[string]*Task) error {
		if t.Status == StatusPending {
			return fmt.Errorf("task %s is already pending; reset requires a non-pending task", id)
		}
		if err := requireAssignee(t, actor, "reset"); err != nil {
			return err
		}
		t.Status = StatusPending
		t.AssignedTo = ""
		t.Lease = nil
		t.Evidence, t.FailureReason, t.BlockReason, t.CancelReason = "", "", "", ""
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
	return LinkDispatchForProfile(projectDir, team.DefaultProfile, session, id, dispatch, now)
}

func LinkDispatchForProfile(projectDir, profile, session, id string, dispatch Dispatch, now time.Time) (Task, error) {
	return mutateForProfile(projectDir, profile, session, id, func(t *Task, _ map[string]*Task) error {
		d := dispatch
		d.Sender = strings.TrimSpace(d.Sender)
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
	return terminalForProfile(projectDir, team.DefaultProfile, session, id, actor, to, set, now)
}

func terminalForProfile(projectDir, profile, session, id, actor, to string, set func(*Task), now time.Time) (Task, error) {
	return mutateForProfile(projectDir, profile, session, id, func(t *Task, _ map[string]*Task) error {
		if t.Status != StatusInProgress {
			return fmt.Errorf("task %s is %s, not in_progress; claim it before marking it %s", id, t.Status, to)
		}
		if err := requireAssignee(t, actor, to); err != nil {
			return err
		}
		t.Status = to
		t.Lease = nil
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
	return mutateForProfile(projectDir, team.DefaultProfile, session, id, fn)
}

func mutateForProfile(projectDir, profile, session, id string, fn func(t *Task, all map[string]*Task) error) (Task, error) {
	id = strings.TrimSpace(id)
	var out Task
	err := withLockForProfile(projectDir, profile, session, func(dir string) error {
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
		return commitTasks(dir, []Task{*t}, t.UpdatedAt)
	})
	return out, err
}

func withLock(projectDir, session string, fn func(dir string) error) error {
	return withLockForProfile(projectDir, team.DefaultProfile, session, fn)
}

func withLockForProfile(projectDir, profile, session string, fn func(dir string) error) error {
	dir := DirForProfile(projectDir, profile, session)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("ensure task dir %s: %w", dir, err)
	}
	return flock.WithLock(filepath.Join(dir, ".lock"), func() error {
		if _, err := recoverCommittedTransaction(dir); err != nil {
			return err
		}
		return fn(dir)
	})
}

func withReadLockForProfile(projectDir, profile, session string, fn func(dir string) error) error {
	dir := DirForProfile(projectDir, profile, session)
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return fn(dir)
		}
		return err
	}
	return flock.WithLock(filepath.Join(dir, ".lock"), func() error {
		if _, err := recoverCommittedTransaction(dir); err != nil {
			return err
		}
		return fn(dir)
	})
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
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") || !strings.HasSuffix(e.Name(), ".json") {
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

func appendUniqueSorted(in []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return in
	}
	for _, existing := range in {
		if existing == value {
			return in
		}
	}
	out := append(append([]string(nil), in...), value)
	sort.Strings(out)
	return out
}
