// Package task is the 3.0 task store (plan 4.5, R17): one JSON file per task
// under <AM_ROOT>/extensions/amq-squad/tasks/, five states, dependency
// gating, file locks over declared_files, leases and pools. Every mutation
// holds the store lock and commits through the journal in transaction.go.
// docs/task-store-design.md is the schema contract.
package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/flock"
)

// Schema is the task file schema version.
const Schema = 1

// DefaultLeaseDuration is the proposed lease length (brief 2b-1 ruling 3;
// measured in docs/v3-spike/2b1-execution.md).
const DefaultLeaseDuration = 10 * time.Minute

// ErrNotFound is returned for an unknown task id.
var ErrNotFound = errors.New("task not found")

// ErrNoLease is returned when the handle holds no matching lease.
var ErrNoLease = errors.New("no lease held")

// Task is one task file (schema 1).
type Task struct {
	Schema        int           `json:"schema"`
	ID            string        `json:"id"`
	Title         string        `json:"title"`
	Description   string        `json:"description,omitempty"`
	Class         string        `json:"class"`
	State         string        `json:"state"`
	Priority      int           `json:"priority"`
	DependsOn     []string      `json:"depends_on"`
	DeclaredFiles []string      `json:"declared_files"`
	ProjectPath   string        `json:"project_path,omitempty"`
	Pool          *Pool         `json:"pool"`
	Lease         *Lease        `json:"lease"`
	Attempt       int           `json:"attempt"`
	Attempts      []Attempt     `json:"attempts,omitempty"`
	WaitReason    string        `json:"wait_reason,omitempty"`
	BlockReason   string        `json:"block_reason,omitempty"`
	BlockRoute    string        `json:"block_route,omitempty"`
	BlockDetail   string        `json:"block_detail,omitempty"`
	Report        *Report       `json:"report"`
	Evidence      string        `json:"evidence,omitempty"`
	CancelReason  string        `json:"cancel_reason,omitempty"`
	ScopeChanges  []ScopeChange `json:"scope_changes,omitempty"`
	// The PR binding (3-2 fix 4, milestone B's field names): the harness
	// commit step, and the merge train after its own rebase, write it through
	// BindPR; it survives every mutation like any other field.
	PRRepository string     `json:"pr_repository,omitempty"`
	PRNumber     int        `json:"pr_number,omitempty"`
	PRHead       string     `json:"pr_head,omitempty"`
	CreatedBy    string     `json:"created_by,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	ClosedAt     *time.Time `json:"closed_at"`
}

// Lease is the current holder's lease. An unclaimed lease (ClaimedAt nil)
// is an assignment the seat has not acknowledged; it is never renewed.
type Lease struct {
	Holder    string     `json:"holder"`
	GrantedAt time.Time  `json:"granted_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	ClaimedAt *time.Time `json:"claimed_at"`
	RenewedAt *time.Time `json:"renewed_at"`
	Renewals  int        `json:"renewals"`
	// Verdict is the harness's verdict on this attempt (RecordVerdict): the
	// only way a class with a mechanical verifier completes.
	Verdict *Verdict `json:"verdict,omitempty"`
}

// Attempt is one ended lease.
type Attempt struct {
	N         int       `json:"n"`
	Holder    string    `json:"holder"`
	Pool      *Pool     `json:"pool"`
	GrantedAt time.Time `json:"granted_at"`
	EndedAt   time.Time `json:"ended_at"`
	Outcome   string    `json:"outcome"`
	Detail    string    `json:"detail,omitempty"`
	Verdict   *Verdict  `json:"verdict,omitempty"` // the harness's verdict, when one was recorded
}

// Report is the holder's done report, kept until the commit step decides.
type Report struct {
	By        string    `json:"by"`
	At        time.Time `json:"at"`
	Evidence  string    `json:"evidence,omitempty"`
	FinalHead string    `json:"final_head,omitempty"`
}

// ScopeChange is a recorded lead decision that widened declared_files.
type ScopeChange struct {
	Actor  string    `json:"actor"`
	At     time.Time `json:"at"`
	Reason string    `json:"reason"`
	Added  []string  `json:"added"`
	// Gates are the growth-gate targets (internal/gatetarget) an accepted
	// owner gate opened for this widening; the commit step re-verifies each
	// before it lets a change past a grant limit (2b-2 milestone B).
	Gates []string `json:"gates,omitempty"`
}

// Terminal reports whether the state is completed or cancelled.
func Terminal(state string) bool { return state == StateCompleted || state == StateCancelled }

// Store is one squad's task store.
type Store struct{ Dir string }

// DirFor is the store directory under an AMQ session root.
func DirFor(root string) string {
	return filepath.Join(root, "extensions", "amq-squad", "tasks")
}

// Open returns the store of an AMQ session root, which must be a clean
// absolute path. It creates nothing; the first mutation creates the dir.
func Open(root string) (Store, error) {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return Store{}, fmt.Errorf("task store root %q must be a clean absolute path", root)
	}
	return Store{Dir: DirFor(root)}, nil
}

// AddInput is the create payload of Add.
type AddInput struct {
	Title         string
	Description   string
	Class         string
	DependsOn     []string
	DeclaredFiles []string
	Pool          *Pool
	Priority      int
	ProjectPath   string
	CreatedBy     string
}

// Add creates a pending task.
func (s Store) Add(in AddInput, now time.Time) (Task, error) {
	in.Title, in.Class = strings.TrimSpace(in.Title), strings.TrimSpace(in.Class)
	if in.Title == "" {
		return Task{}, fmt.Errorf("task title is required")
	}
	if !ValidClass(in.Class) {
		return Task{}, fmt.Errorf("task class %q is not in the closed list %s", in.Class, strings.Join(Classes, ", "))
	}
	files, err := CleanDeclaredFiles(in.DeclaredFiles)
	if err != nil {
		return Task{}, err
	}
	var created Task
	err = s.update(now, func(all *tasks) error {
		deps := dedupe(in.DependsOn)
		for _, d := range deps {
			if all.get(d) == nil {
				return fmt.Errorf("depends-on task %q does not exist", d)
			}
		}
		created = Task{
			Schema: Schema, ID: all.nextID(), Title: in.Title, Description: strings.TrimSpace(in.Description),
			Class: in.Class, State: StatePending, Priority: in.Priority, DependsOn: deps, DeclaredFiles: files,
			ProjectPath: strings.TrimSpace(in.ProjectPath), Pool: in.Pool, CreatedBy: strings.TrimSpace(in.CreatedBy),
			CreatedAt: now, UpdatedAt: now,
		}
		all.list = append(all.list, created)
		all.touch(created.ID)
		return nil
	})
	return created, err
}

// List returns every task in id order, under the lock (a committed journal
// is replayed first).
func (s Store) List() ([]Task, error) {
	var out []Task
	err := s.withLock(false, func() error {
		var err error
		out, err = readDir(s.Dir)
		return err
	})
	return out, err
}

// Get returns one task.
func (s Store) Get(id string) (Task, error) {
	list, err := s.List()
	if err != nil {
		return Task{}, err
	}
	for _, t := range list {
		if t.ID == strings.TrimSpace(id) {
			return t, nil
		}
	}
	return Task{}, fmt.Errorf("%w: %q", ErrNotFound, id)
}

// Snapshot reads the store without the lock and without writing: the task
// files, overlaid with the after-images of a committed journal that is still
// present. Task files are only replaced by rename, so a reader never sees a
// partial file. Used by the hook verbs and status.
//
// A reader can interleave with a writer: it may read one task file before a
// transaction and the next after it, with the journal already removed, a
// state that never existed. Every apply bumps the store generation before it
// removes the journal, so Snapshot reads the generation before and after its
// scan and retries when it changed (snapshotRetries times, then ErrBusy).
func Snapshot(dir string) ([]Task, error) {
	for i := 0; i < snapshotRetries; i++ {
		before := readGeneration(dir)
		list, err := snapshotOnce(dir)
		if err != nil {
			return nil, err
		}
		if readGeneration(dir) == before {
			return list, nil
		}
	}
	return nil, fmt.Errorf("%w: the store changed during every snapshot read", ErrBusy)
}

const snapshotRetries = 20

const generationName = ".generation"

// readGeneration is the store generation, "" before the first transaction.
func readGeneration(dir string) string {
	raw, _ := os.ReadFile(filepath.Join(dir, generationName))
	return strings.TrimSpace(string(raw))
}

// bumpGeneration replaces the generation file by rename (the reader sees the
// old or the new value, never a partial one). Called under the store lock.
func bumpGeneration(dir string) error {
	n, _ := strconv.ParseUint(readGeneration(dir), 10, 64)
	tmp := filepath.Join(dir, generationName+".tmp")
	if err := os.WriteFile(tmp, []byte(strconv.FormatUint(n+1, 10)+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, generationName))
}

// snapshotFileHook is a test seam called after each task file a scan reads.
var snapshotFileHook func(name string)

func snapshotOnce(dir string) ([]Task, error) {
	list, err := readDir(dir)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, journalName))
	if os.IsNotExist(err) {
		return list, nil
	}
	if err != nil {
		return nil, err
	}
	var j journal
	if err := json.Unmarshal(raw, &j); err != nil {
		return list, nil // a torn journal never crossed the commit point
	}
	byID := map[string]int{}
	for i, t := range list {
		byID[t.ID] = i
	}
	for _, t := range j.Tasks {
		if i, ok := byID[t.ID]; ok {
			list[i] = t
		} else {
			list = append(list, t)
		}
	}
	sortTasks(list)
	return list, nil
}

// tasks is the in-lock working set of one transaction.
type tasks struct {
	list    []Task
	changed map[string]bool
	dir     string // the store directory (seat quarantine lookups under the lock)
}

func (a *tasks) get(id string) *Task {
	for i := range a.list {
		if a.list[i].ID == id {
			return &a.list[i]
		}
	}
	return nil
}

func (a *tasks) touch(id string) { a.changed[id] = true }

func (a *tasks) nextID() string {
	max := 0
	for _, t := range a.list {
		if n, ok := taskNum(t.ID); ok && n > max {
			max = n
		}
	}
	return "t" + strconv.Itoa(max+1)
}

// ErrBusy is returned by a no-wait mutation when another writer holds the
// store lock.
var ErrBusy = errors.New("task store busy")

// update runs fn on the locked working set and commits the touched tasks.
func (s Store) update(now time.Time, fn func(all *tasks) error) error {
	return s.updateWith(s.withLock, now, fn)
}

// updateNoWait is update with a try-lock: it returns ErrBusy instead of
// waiting for another writer (the hook path must never block).
func (s Store) updateNoWait(now time.Time, fn func(all *tasks) error) error {
	return s.updateWith(s.withTryLock, now, fn)
}

func (s Store) withTryLock(_ bool, fn func() error) error {
	lock, acquired, err := flock.TryExclusive(filepath.Join(s.Dir, ".lock"), true)
	switch {
	case errors.Is(err, flock.ErrUnsupported):
		// No advisory locks on this platform; the blocking path is a no-op too.
	case err != nil:
		return err
	case !acquired:
		return ErrBusy
	default:
		defer lock.Close()
	}
	if err := recoverJournal(s.Dir); err != nil {
		return err
	}
	return fn()
}

func (s Store) updateWith(lock func(bool, func() error) error, now time.Time, fn func(all *tasks) error) error {
	return lock(true, func() error {
		list, err := readDir(s.Dir)
		if err != nil {
			return err
		}
		all := &tasks{list: list, changed: map[string]bool{}, dir: s.Dir}
		if err := fn(all); err != nil {
			return err
		}
		var changed []Task
		for _, t := range all.list {
			if all.changed[t.ID] {
				t.UpdatedAt = now
				changed = append(changed, t)
			}
		}
		return commit(s.Dir, changed, now)
	})
}

// mutate runs fn on one task of the locked working set.
func (s Store) mutate(id string, now time.Time, fn func(t *Task, all *tasks) error) (Task, error) {
	var out Task
	err := s.update(now, func(all *tasks) error {
		t := all.get(strings.TrimSpace(id))
		if t == nil {
			return fmt.Errorf("%w: %q", ErrNotFound, id)
		}
		if err := fn(t, all); err != nil {
			return err
		}
		all.touch(t.ID)
		out = *t
		return nil
	})
	out.UpdatedAt = now
	return out, err
}

func (s Store) withLock(create bool, fn func() error) error {
	if _, err := os.Stat(s.Dir); os.IsNotExist(err) {
		if !create {
			return fn() // an absent store reads as empty
		}
		if err := os.MkdirAll(s.Dir, 0o700); err != nil {
			return fmt.Errorf("create task store %s: %w", s.Dir, err)
		}
	}
	return flock.WithLock(filepath.Join(s.Dir, ".lock"), func() error {
		if err := recoverJournal(s.Dir); err != nil {
			return err
		}
		return fn()
	})
}

func readDir(dir string) ([]Task, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read task store: %w", err)
	}
	var out []Task
	for _, e := range entries {
		// <id>.json only: dotfiles (lock, journal) and *.tmp are skipped.
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		var t Task
		if err := json.Unmarshal(raw, &t); err != nil {
			return nil, fmt.Errorf("parse %s: %w", e.Name(), err)
		}
		out = append(out, t)
		if snapshotFileHook != nil {
			snapshotFileHook(e.Name())
		}
	}
	sortTasks(out)
	return out, nil
}

// ValidID reports whether id is a canonical t<N> leaf (N > 0, no padding).
func ValidID(id string) error {
	if n, ok := taskNum(id); !ok || n <= 0 || "t"+strconv.Itoa(n) != id {
		return fmt.Errorf("invalid task id %q: expected canonical t<N>", id)
	}
	return nil
}

func taskNum(id string) (int, bool) {
	if !strings.HasPrefix(id, "t") {
		return 0, false
	}
	n, err := strconv.Atoi(id[1:])
	return n, err == nil
}

// sortTasks orders by numeric id.
func sortTasks(list []Task) {
	sort.SliceStable(list, func(i, j int) bool {
		ni, oki := taskNum(list[i].ID)
		nj, okj := taskNum(list[j].ID)
		if oki && okj {
			return ni < nj
		}
		return list[i].ID < list[j].ID
	})
}

// DispatchOrder sorts tasks as the scheduler dispatches them: higher
// priority first, then lower id.
func DispatchOrder(list []Task) {
	sortTasks(list)
	sort.SliceStable(list, func(i, j int) bool { return list[i].Priority > list[j].Priority })
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
