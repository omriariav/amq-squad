package schedproc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Slots are the seats the scheduler may start: the squad's typed seat list
// (the `launch --seats` file; squad.yaml compiles to it). A request for a
// pool takes the first free seat whose runtime matches, and for Claude and
// Codex its model and effort (a pi seat's model, when the list names one).
// The seat gets its own worktree on its own branch, both put on the base
// branch's tip at every launch (git hooks off); a seat whose worktree is the
// repository root is never started, because the commit step moves its
// branch.
type Slots struct {
	Squad      seatlaunch.Squad // normalized; Wrapper and AMQPath set
	BaseBranch string           // default main
	Git        string           // default git on PATH
	// Target is the launch's record of the owner's grant for each seat's
	// commit step: repository, base branch and push URL (R2).
	Target seatcommit.Target

	mu     sync.Mutex
	inUse  map[string]string // handle -> request id
	byReq  map[string]string // request id -> handle
	closed map[string]bool   // handles quarantined or otherwise not to be reused
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
	if _, err := s.checkWorktree(seat); err != nil {
		return seat, err
	}
	if seat.Branch == "" {
		seat.Branch = "seat/" + seat.Handle
	}
	_, statErr := os.Stat(seat.Worktree)
	if _, err := os.Lstat(filepath.Join(seat.Worktree, ".git")); statErr == nil && err != nil {
		return seat, nil // not a checkout: no base; its commit step refuses base_unrecorded
	}
	// The launch-time base (3.0.2 t22): at every launch the seat's worktree
	// and its resting branch start from the base branch's tip, never from
	// the commit ledger's first base or the branch's previous head.
	g := baseGit{bin: firstNonEmpty(s.Git, "git")}
	repo := root // an existing worktree's repository is its own
	if statErr == nil {
		repo = seat.Worktree
	}
	common, err := commonDir(repo)
	if err != nil {
		return seat, fmt.Errorf("seat %s base: %v", seat.Handle, err)
	}
	tip, err := s.baseTip(g, common, seat)
	if err != nil {
		return seat, err
	}
	seat.Base = tip
	if statErr == nil {
		if err := g.moveWorktree(common, seat, tip); err != nil {
			return seat, fmt.Errorf("seat %s worktree to the base tip %s: %v", seat.Handle, short(tip), err)
		}
		return seat, nil
	}
	// A local reset of the resting branch, never a push.
	if _, err := g.run(root, []string{"GIT_DIR=" + common}, "update-ref", "-m", "amq-squad launch base "+seat.Handle, "refs/heads/"+seat.Branch, tip); err != nil {
		return seat, fmt.Errorf("seat %s branch: %v", seat.Handle, err)
	}
	// The hardened git (R2): no hook, filter or attribute of the shared
	// repository config runs, and no credential reaches git.
	if err := seatcommit.AddWorktree(s.Git, root, seat.Worktree, seat.Branch, tip, nil); err != nil {
		return seat, fmt.Errorf("seat %s worktree: %v", seat.Handle, err)
	}
	return seat, nil
}

// ReasonSeatBranchUnmerged refuses a seat's launch when the remote copy of
// its resting branch holds commits the base tip does not: the harness never
// force-pushes it; the lead decides.
const ReasonSeatBranchUnmerged = "seat_branch_unmerged"

// baseTip is the base branch's tip: fetched from the push URL the launch
// recorded from the grant (into a shadow git directory, so the shared
// repository's config, remotes and url rewrites are never read), or the
// local base branch when the grant names no push URL. A remote seat branch
// ahead of the tip and unmerged refuses with ReasonSeatBranchUnmerged.
func (s *Slots) baseTip(g baseGit, common string, seat seatlaunch.Seat) (string, error) {
	base := firstNonEmpty(s.BaseBranch, "main")
	url := s.Target.PushURL
	if url == "" {
		tip, err := g.run(common, []string{"GIT_DIR=" + common}, "rev-parse", "--verify", "refs/heads/"+base+"^{commit}")
		if err != nil {
			return "", fmt.Errorf("seat %s base: the local base branch %s: %v", seat.Handle, base, err)
		}
		return tip, nil
	}
	// An HTTPS push URL fetches with the push's authentication: the pinned
	// gh as the only credential helper (seatcommit.FetchCredentials).
	cred, credEnv, why := seatcommit.FetchCredentials(s.Target, os.Environ())
	if why != "" {
		return "", fmt.Errorf("seat %s base: %s", seat.Handle, why)
	}
	sh, err := newBaseShadow(g, common)
	if err != nil {
		return "", fmt.Errorf("seat %s base: %v", seat.Handle, err)
	}
	defer os.RemoveAll(sh.dir)
	fg := baseGit{bin: g.bin, pre: cred} // ls-remote and fetch only
	fenv := append(append([]string(nil), sh.env...), credEnv...)
	out, err := fg.run(sh.dir, fenv, "ls-remote", "--", url, "refs/heads/"+base, "refs/heads/"+seat.Branch)
	if err != nil {
		return "", fmt.Errorf("seat %s base: %v", seat.Handle, err)
	}
	refs := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if _, ref, ok := strings.Cut(line, "\t"); ok {
			refs[ref] = true
		}
	}
	if !refs["refs/heads/"+base] {
		return "", fmt.Errorf("seat %s base: the push URL has no branch %s", seat.Handle, base)
	}
	specs := []string{"+refs/heads/" + base + ":refs/base"}
	if refs["refs/heads/"+seat.Branch] {
		specs = append(specs, "+refs/heads/"+seat.Branch+":refs/seat")
	}
	if _, err := fg.run(sh.dir, fenv, append([]string{"fetch", "-q", "--no-tags", "--no-write-fetch-head", "--", url}, specs...)...); err != nil {
		return "", fmt.Errorf("seat %s base: %v", seat.Handle, err)
	}
	tip, err := g.run(sh.dir, sh.env, "rev-parse", "--verify", "refs/base^{commit}")
	if err != nil {
		return "", fmt.Errorf("seat %s base: %v", seat.Handle, err)
	}
	if len(specs) == 2 {
		ahead, err := g.run(sh.dir, sh.env, "rev-list", "--abbrev-commit", "--max-count=11", "refs/base..refs/seat")
		if err != nil {
			return "", fmt.Errorf("seat %s base: %v", seat.Handle, err)
		}
		if ahead != "" {
			return "", fmt.Errorf("%s: seat %s: the remote branch %s holds commits the %s tip %s does not (%s); the harness never force-pushes it: merge, close or delete it, then launch again", ReasonSeatBranchUnmerged, seat.Handle, seat.Branch, base, short(tip), strings.Join(strings.Fields(ahead), ", "))
		}
	}
	return tip, nil
}

// moveWorktree puts an existing seat worktree on the tip: a two-tree
// read-tree from its HEAD in the shadow (a local change the move would
// overwrite refuses, nothing is lost; CleanSeat keeps any other leftover as
// evidence), then the resting branch moves to the tip and HEAD names it.
func (g baseGit) moveWorktree(common string, seat seatlaunch.Seat, tip string) error {
	gd, err := worktreeGitDir(seat.Worktree)
	if err != nil {
		return err
	}
	wenv := []string{"GIT_DIR=" + gd}
	old, err := g.run(seat.Worktree, wenv, "rev-parse", "--verify", "-q", "HEAD^{commit}")
	if err != nil {
		old = ""
	}
	if old != tip {
		sh, err := newBaseShadow(g, common)
		if err != nil {
			return err
		}
		defer os.RemoveAll(sh.dir)
		args := []string{"read-tree", "-u", "--reset", tip}
		if old != "" {
			args = []string{"read-tree", "-m", "-u", old, tip}
		}
		if _, err := g.run(seat.Worktree, append(sh.env, "GIT_WORK_TREE="+seat.Worktree, "GIT_INDEX_FILE="+filepath.Join(gd, "index")), args...); err != nil {
			return err
		}
	}
	if _, err := g.run(seat.Worktree, []string{"GIT_DIR=" + common}, "update-ref", "-m", "amq-squad launch base "+seat.Handle, "refs/heads/"+seat.Branch, tip); err != nil {
		return err
	}
	_, err = g.run(seat.Worktree, wenv, "symbolic-ref", "HEAD", "refs/heads/"+seat.Branch)
	return err
}

// baseGit is the launch-time base's git with the harness's R2 settings
// (seatcommit's): no global or system config, hooks, attribute files,
// fsmonitor or replace objects; the push's environment.
type baseGit struct {
	bin string
	pre []string // the fetch's credential settings (FetchCredentials)
}

func (g baseGit) run(dir string, extra []string, args ...string) (string, error) {
	argv := append(append([]string{"-c", "core.hooksPath=" + os.DevNull, "-c", "core.attributesFile=" + os.DevNull, "-c", "core.fsmonitor=false", "-c", "protocol.ext.allow=never"}, g.pre...), args...)
	cmd := exec.Command(g.bin, argv...)
	cmd.Dir = dir
	// The push's environment (seatcommit.HarnessEnv: its keptGitVars list,
	// GIT_SSH, GIT_SSH_COMMAND, GIT_SSH_VARIANT and GIT_ASKPASS, in git.go).
	cmd.Env = append(seatcommit.HarnessEnv(os.Environ()), extra...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

// baseShadow is a git directory of the harness's own (git's default config)
// on the repository's object store.
type baseShadow struct {
	dir string
	env []string
}

func newBaseShadow(g baseGit, common string) (baseShadow, error) {
	dir, err := os.MkdirTemp("", "amq-squad-base-*")
	if err != nil {
		return baseShadow{}, err
	}
	if _, err := g.run(dir, nil, "init", "-q", "--template=", dir); err != nil {
		os.RemoveAll(dir)
		return baseShadow{}, err
	}
	return baseShadow{dir: dir, env: []string{"GIT_DIR=" + filepath.Join(dir, ".git"), "GIT_OBJECT_DIRECTORY=" + filepath.Join(common, "objects")}}, nil
}

// commonDir is a checkout's common git directory, read from its .git.
func commonDir(root string) (string, error) {
	dotgit := filepath.Join(root, ".git")
	st, err := os.Lstat(dotgit)
	if err != nil {
		return "", fmt.Errorf("%s is not a git checkout: %v", root, err)
	}
	if st.IsDir() {
		return dotgit, nil
	}
	gd, err := worktreeGitDir(root)
	if err != nil {
		return "", err
	}
	c, err := os.ReadFile(filepath.Join(gd, "commondir"))
	if err != nil {
		return gd, nil
	}
	if c := strings.TrimSpace(string(c)); !filepath.IsAbs(c) {
		return filepath.Clean(filepath.Join(gd, c)), nil
	}
	return filepath.Clean(strings.TrimSpace(string(c))), nil
}

// worktreeGitDir is a linked worktree's git directory from its .git file.
func worktreeGitDir(wt string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(wt, ".git"))
	if err != nil {
		return "", fmt.Errorf("%s is not a linked worktree: %v", wt, err)
	}
	gd, ok := strings.CutPrefix(strings.TrimSpace(string(raw)), "gitdir: ")
	if !ok || gd == "" {
		return "", fmt.Errorf("%s/.git is not a gitdir file", wt)
	}
	if !filepath.IsAbs(gd) {
		gd = filepath.Join(wt, gd)
	}
	return filepath.Clean(gd), nil
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// Launcher sends each request to the launcher of its runtime (the AMQ launch
// API for Claude, Codex and, since AMQ v0.116, pi) and
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
	if err == nil && !l.CloseUnconfirmed(handle) {
		// A confirmed close frees the slot now; an unconfirmed one (a
		// launcher that is a scheduler.AsyncCloser and says so) only at
		// the seat's seat_gone (P1-6).
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

// CloseUnconfirmed asks the seat's own launcher (a scheduler.AsyncCloser)
// whether its nil close does not confirm the termination; the scheduler then
// waits for the seat's seat_gone (R3). The AMQ launcher confirms.
func (l *Launcher) CloseUnconfirmed(handle string) bool {
	for _, s := range l.Slots.Squad.Seats {
		if s.Handle == handle {
			a, ok := l.ByRuntime[s.Runtime].(scheduler.AsyncCloser)
			return ok && a.CloseUnconfirmed(handle)
		}
	}
	return false
}

// Reserve holds a seat's slot (N2: a seat a previous incarnation left
// running; N3: a gone seat whose exit check is pending), so no replacement
// launches under its handle and worktree.
func (l *Launcher) Reserve(handle string) {
	l.Slots.mu.Lock()
	defer l.Slots.mu.Unlock()
	if l.Slots.inUse == nil {
		l.Slots.inUse, l.Slots.byReq, l.Slots.closed = map[string]string{}, map[string]string{}, map[string]bool{}
	}
	if l.Slots.inUse[handle] == "" {
		l.Slots.inUse[handle] = "reserved"
	}
}

// SeatGone frees the seat's slot once its termination is reported (P1-6):
// a replacement never launches under the same handle and worktree before
// the old provider stops.
func (l *Launcher) SeatGone(handle string) { l.Slots.Release(handle) }

var (
	_ scheduler.SeatLauncher     = (*Launcher)(nil)
	_ scheduler.AsyncCloser      = (*Launcher)(nil)
	_ scheduler.SeatGoneObserver = (*Launcher)(nil)
	_ scheduler.SeatReserver     = (*Launcher)(nil)
)

func cleanPath(p string) string { return filepath.Clean(p) }

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
