package schedproc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Slots are the seats the scheduler may start: the squad's typed seat list
// (the `launch --seats` file; squad.yaml compiles to it). A request for a
// pool takes the first free seat whose runtime matches, and for Claude and
// Codex its model and effort (a pi seat's model, when the list names one).
// The seat gets its own worktree on its own branch (created from the base
// branch when missing, git hooks off); a seat whose worktree is the
// repository root is never started, because the commit step moves its
// branch.
type Slots struct {
	Squad      seatlaunch.Squad // normalized; Wrapper and AMQPath set
	BaseBranch string           // default main
	Git        string           // default git on PATH
	Now        func() time.Time

	mu     sync.Mutex
	inUse  map[string]string // handle -> request id
	byReq  map[string]string // request id -> handle
	closed map[string]bool   // handles quarantined or otherwise not to be reused
}

func (s *Slots) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func poolMatches(p task.Pool, seat seatlaunch.Seat) bool {
	if p.Runtime != seat.Runtime {
		return false
	}
	if seat.Runtime == "pi" {
		return seat.Model == "" || seat.Model == p.Model
	}
	return seat.Model == p.Model && seat.Effort == p.Effort
}

// Plan reserves a free matching seat for the request and returns the
// one-seat squad to launch (the seatlaunch.Launcher's SeatPlanner).
func (s *Slots) Plan(req scheduler.SeatRequest) (seatlaunch.Squad, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inUse == nil {
		s.inUse, s.byReq, s.closed = map[string]string{}, map[string]string{}, map[string]bool{}
	}
	for _, seat := range s.Squad.Seats {
		if !poolMatches(req.Pool, seat) || s.inUse[seat.Handle] != "" || s.closed[seat.Handle] {
			continue
		}
		seat, err := s.ensureWorktree(seat)
		if err != nil {
			return seatlaunch.Squad{}, err
		}
		seat.RequestID = req.ID
		q := s.Squad
		q.Seats = []seatlaunch.Seat{seat}
		s.inUse[seat.Handle], s.byReq[req.ID] = req.ID, seat.Handle
		return q, nil
	}
	return seatlaunch.Squad{}, fmt.Errorf("no free seat in the squad's seat list for pool %s", req.Pool.Key())
}

// Release frees a seat (closed, or its request cancelled).
func (s *Slots) Release(handle string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id := s.inUse[handle]; id != "" {
		delete(s.byReq, id)
	}
	delete(s.inUse, handle)
}

// ReleaseRequest frees the seat reserved for a request id.
func (s *Slots) ReleaseRequest(id string) {
	s.mu.Lock()
	h := s.byReq[id]
	s.mu.Unlock()
	if h != "" {
		s.Release(h)
	}
}

func (s *Slots) ensureWorktree(seat seatlaunch.Seat) (seatlaunch.Seat, error) {
	root := filepath.Clean(s.Squad.ProjectRoot)
	if seat.Worktree == "" || filepath.Clean(seat.Worktree) == root {
		return seat, fmt.Errorf("seat %s has no worktree of its own (the repository root is never a seat worktree: the commit step moves the seat branch)", seat.Handle)
	}
	if seat.Branch == "" {
		seat.Branch = "seat/" + seat.Handle
	}
	if _, err := os.Stat(seat.Worktree); err == nil {
		return seat, nil
	}
	base := firstNonEmpty(s.BaseBranch, "main")
	args := []string{"-c", "core.hooksPath=" + os.DevNull, "-C", root, "worktree", "add"}
	if s.gitOK("-C", root, "rev-parse", "--verify", "-q", "refs/heads/"+seat.Branch) {
		args = append(args, seat.Worktree, seat.Branch)
	} else {
		args = append(args, "-b", seat.Branch, seat.Worktree, base)
	}
	if out, err := exec.Command(firstNonEmpty(s.Git, "git"), args...).CombinedOutput(); err != nil {
		return seat, fmt.Errorf("seat %s worktree: %v: %s", seat.Handle, err, strings.TrimSpace(string(out)))
	}
	return seat, nil
}

func (s *Slots) gitOK(args ...string) bool {
	return exec.Command(firstNonEmpty(s.Git, "git"), args...).Run() == nil
}

// Launcher sends each request to the launcher of its runtime (the AMQ launch
// API for Claude and Codex; the external runner for pi on AMQ v0.107.0) and
// frees the seat's slot when its request is cancelled or the seat closed.
type Launcher struct {
	Slots     *Slots
	ByRuntime map[string]scheduler.SeatLauncher

	mu     sync.Mutex
	reqRun map[string]string // request id -> runtime
	seatRT map[string]string // handle -> runtime
}

func (l *Launcher) RequestSeat(req scheduler.SeatRequest) error {
	inner := l.ByRuntime[req.Pool.Runtime]
	if inner == nil {
		return fmt.Errorf("no launcher for runtime %s", req.Pool.Runtime)
	}
	l.mu.Lock()
	if l.reqRun == nil {
		l.reqRun, l.seatRT = map[string]string{}, map[string]string{}
	}
	l.reqRun[req.ID] = req.Pool.Runtime
	l.mu.Unlock()
	err := inner.RequestSeat(req)
	if err != nil {
		l.Slots.ReleaseRequest(req.ID)
	}
	return err
}

func (l *Launcher) CancelSeatRequest(req scheduler.SeatRequest) error {
	l.mu.Lock()
	rt := l.reqRun[req.ID]
	l.mu.Unlock()
	inner := l.ByRuntime[firstNonEmpty(rt, req.Pool.Runtime)]
	if inner == nil {
		return nil
	}
	err := inner.CancelSeatRequest(req)
	if err == nil {
		l.Slots.ReleaseRequest(req.ID)
	}
	return err
}

func (l *Launcher) CloseSeat(handle, reason string) error {
	runtime := ""
	for _, s := range l.Slots.Squad.Seats {
		if s.Handle == handle {
			runtime = s.Runtime
		}
	}
	inner := l.ByRuntime[runtime]
	if inner == nil {
		return nil
	}
	err := inner.CloseSeat(handle, reason)
	if err == nil {
		l.Slots.Release(handle)
		if reason == "quarantined" {
			l.Slots.mu.Lock()
			if l.Slots.closed == nil {
				l.Slots.closed = map[string]bool{}
			}
			l.Slots.closed[handle] = true
			l.Slots.mu.Unlock()
		}
	}
	return err
}

var _ scheduler.SeatLauncher = (*Launcher)(nil)

// External is the stand-in launcher for seats AMQ v0.107.0 cannot launch
// (pi: the pinned library has no pi adapter, seatlaunch.PiAdapterInLibrary).
// The core does its part as for any seat: the worktree and branch, the
// staged launch record and role.md under the session root, the recorded
// commit base. Then it writes a request file under
// <root>/extensions/amq-squad/scheduler/requests/ for an external runner,
// which starts the provider with the seat identity and posts seat_up
// (`amq-squad scheduler post`). A cancel or a close writes a close file the
// runner obeys; both are confirmed at once (the stand-in's limit, recorded).
type External struct {
	Slots *Slots
	Root  string
}

// Request is the file an external runner reads.
type Request struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"task_id"`
	Pool      task.Pool `json:"pool"`
	Handle    string    `json:"handle"`
	Runtime   string    `json:"runtime"`
	Worktree  string    `json:"worktree"`
	Branch    string    `json:"branch"`
	RolePath  string    `json:"role_path"`
	Root      string    `json:"root"`
	BaseRoot  string    `json:"base_root"`
	Session   string    `json:"session"`
	CreatedAt time.Time `json:"created_at"`
}

// RequestsDir is where External writes requests.
func RequestsDir(root string) string { return filepath.Join(Dir(root), "requests") }

func (e External) RequestSeat(req scheduler.SeatRequest) error {
	q, err := e.Slots.Plan(req)
	if err != nil {
		return err
	}
	seat := q.Seats[0]
	role := []byte(seat.Role)
	sum := sha256.Sum256(role)
	rulesSHA, err := seatlaunch.FileDigest(q.RulesPath)
	if err != nil {
		e.Slots.ReleaseRequest(req.ID)
		return err
	}
	rec := launch.Record{Handle: seat.Handle, Root: q.SessionRoot(), BaseRoot: q.BaseRoot, Session: q.Session, Runtime: seat.Runtime,
		Model: seat.Model, Effort: seat.Effort, RoleSHA256: "sha256:" + hex.EncodeToString(sum[:]), RulesPath: q.RulesPath, RulesSHA256: rulesSHA,
		Branch: seat.Branch, Worktree: seat.Worktree, AMQPath: q.AMQPath, EnvNeeds: seat.EnvNeeds, RequestID: req.ID, Role: seat.RoleName,
		CWD: seat.Worktree, Binary: seat.Runtime, TeamHome: q.TeamHome, StartedAt: e.Slots.now().UTC(), Launcher: "external"}
	if err := seatlaunch.Stage(seatlaunch.Compiled{Records: []launch.Record{rec}, Roles: [][]byte{role}}); err != nil {
		e.Slots.ReleaseRequest(req.ID)
		return err
	}
	if _, err := seatcommit.RecordWorktreeBase(e.Slots.Git, rec.Root, seat.Handle, seat.Worktree, seat.Branch, e.Slots.now()); err != nil {
		e.Slots.ReleaseRequest(req.ID)
		return fmt.Errorf("seat %s: record the commit base: %w", seat.Handle, err)
	}
	r := Request{ID: req.ID, TaskID: req.TaskID, Pool: req.Pool, Handle: seat.Handle, Runtime: seat.Runtime, Worktree: seat.Worktree, Branch: seat.Branch,
		RolePath: filepath.Join(launch.StagedDir(rec.Root, seat.Handle), launch.RoleFileName), Root: rec.Root, BaseRoot: q.BaseRoot, Session: q.Session, CreatedAt: e.Slots.now().UTC()}
	return writeJSON(filepath.Join(RequestsDir(e.Root), req.ID+".json"), r)
}

func (e External) CancelSeatRequest(req scheduler.SeatRequest) error {
	os.Remove(filepath.Join(RequestsDir(e.Root), req.ID+".json"))
	return writeJSON(filepath.Join(RequestsDir(e.Root), req.ID+".cancel"), map[string]string{"id": req.ID})
}

func (e External) CloseSeat(handle, reason string) error {
	return writeJSON(filepath.Join(RequestsDir(e.Root), "close-"+handle+".json"), map[string]string{"handle": handle, "reason": reason})
}

var _ scheduler.SeatLauncher = External{}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(v, "", "  ")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
