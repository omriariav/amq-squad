package schedproc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/ledger"
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
	// Root is the AMQ session root whose per-handle hook ledger supplies the
	// second leg of Codex activity admission.
	Root string
	// LogDecision records launch-time repairs such as a stale index refresh.
	LogDecision func(scheduler.Decision)

	mu    sync.Mutex
	inUse map[string]string // handle -> request id
	byReq map[string]string // request id -> handle
	// codexActivityAdmission is replaced at every seat launch. Absence is
	// conservative: a Codex seat gets the manual renewal instruction until a
	// launch has admitted trusted hooks or its own ledger proves activity.
	codexActivityAdmission map[string]codexActivityAdmission
}

type codexActivityAdmission struct {
	trusted  bool
	launched time.Time
}

// AdmitCodexActivity records the launch-time exact-hash trust result and the
// boundary after which only this handle's activity ledger can supply trust.
func (s *Slots) AdmitCodexActivity(handle string, trusted bool, launched time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.codexActivityAdmission == nil {
		s.codexActivityAdmission = map[string]codexActivityAdmission{}
	}
	s.codexActivityAdmission[handle] = codexActivityAdmission{trusted: trusted, launched: launched}
}

// NeedsManualRenew reports whether dispatch must tell this Codex seat to
// renew by hand. Non-Codex seats use their established activity producers.
func (s *Slots) NeedsManualRenew(handle string) bool {
	s.mu.Lock()
	runtime := ""
	for _, seat := range s.Squad.Seats {
		if seat.Handle == handle {
			runtime = seat.Runtime
			break
		}
	}
	admission, admitted := s.codexActivityAdmission[handle]
	root := s.Root
	s.mu.Unlock()
	if runtime != "codex" {
		return false
	}
	if admitted && admission.trusted {
		return false
	}
	if !admitted || admission.launched.IsZero() || root == "" {
		return true
	}
	active, err := ledger.HasActivitySince(root, handle, admission.launched)
	return err != nil || !active
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
		s.inUse, s.byReq = map[string]string{}, map[string]string{}
	}
	for _, seat := range s.Squad.Seats {
		if !poolMatches(req.Pool, seat) || s.inUse[seat.Handle] != "" {
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
	g := seatcommit.BaseGit{Bin: firstNonEmpty(s.Git, "git")}
	var common, gd, head string
	var err error
	if statErr == nil {
		// An existing worktree's repository is its own, through its own git
		// dir only (seatGitDir); its HEAD must name a commit: the move is a
		// two-tree read-tree from it, never a reset over files it cannot
		// account for.
		projectCommon, _ := commonDir(root) // "" when the project root is no checkout
		if gd, err = seatGitDir(seat.Worktree, projectCommon); err != nil {
			return seat, fmt.Errorf("seat %s: %v", seat.Handle, err)
		}
		common = commonOf(gd)
		if head, err = g.Run(seat.Worktree, []string{"GIT_DIR=" + gd}, "rev-parse", "--verify", "-q", "HEAD^{commit}"); err != nil || head == "" {
			return seat, fmt.Errorf("%s: seat %s: the worktree %s has a HEAD that names no commit; the launch never resets over it: check out a commit or remove the worktree, then launch again", ReasonSeatHeadUnreadable, seat.Handle, seat.Worktree)
		}
	} else if common, err = commonDir(root); err != nil {
		return seat, fmt.Errorf("seat %s base: %v", seat.Handle, err)
	}
	tip, err := s.baseTip(g, common, seat)
	if err != nil {
		return seat, err
	}
	seat.Base = tip
	// The resting branch as it stands: a local commit the tip does not hold
	// refuses (ReasonSeatBranchUnpushed), and every update-ref of the branch
	// passes this value as the expected old one, so a concurrent move
	// refuses rather than being overwritten.
	cur, err := g.Run(common, []string{"GIT_DIR=" + common}, "rev-parse", "--verify", "-q", "refs/heads/"+seat.Branch+"^{commit}")
	if err != nil {
		cur = ""
	}
	if cur != "" && cur != tip {
		ahead, err := g.Run(common, []string{"GIT_DIR=" + common}, "rev-list", "--abbrev-commit", "--max-count=11", tip+".."+cur)
		if err != nil {
			return seat, fmt.Errorf("seat %s branch: %v", seat.Handle, err)
		}
		if ahead != "" {
			return seat, fmt.Errorf("%s: seat %s: the local branch %s holds commits the %s tip %s does not (%s); the harness never discards a local commit: push, merge or delete it, then launch again", ReasonSeatBranchUnpushed, seat.Handle, seat.Branch, firstNonEmpty(s.BaseBranch, "main"), short(tip), strings.Join(strings.Fields(ahead), ", "))
		}
	}
	// HEAD's commit is either the resting branch's value checked above or
	// one the tip holds: a detached HEAD at a local commit would lose its
	// files in the move and its last reference at the HEAD switch.
	if statErr == nil && head != tip && head != cur {
		ahead, err := g.Run(common, []string{"GIT_DIR=" + common}, "rev-list", "--abbrev-commit", "--max-count=11", tip+".."+head)
		if err != nil {
			return seat, fmt.Errorf("seat %s worktree: %v", seat.Handle, err)
		}
		if ahead != "" {
			return seat, fmt.Errorf("%s: seat %s: the worktree's HEAD is at %s, with commits the %s tip %s does not hold (%s); the harness never discards a local commit: push, merge or branch it, then launch again", ReasonSeatHeadDetachedUnmerged, seat.Handle, short(head), firstNonEmpty(s.BaseBranch, "main"), short(tip), strings.Join(strings.Fields(ahead), ", "))
		}
	}
	if statErr == nil {
		refreshed, err := cleanWorktree(g, gd, seat)
		if err != nil {
			return seat, err
		}
		if refreshed && s.LogDecision != nil {
			s.LogDecision(scheduler.Decision{Kind: "seat_index_refreshed", Seat: seat.Handle, Reason: fmt.Sprintf("seat %s's index was stale; refreshed", seat.Handle)})
		}
		if err := moveWorktree(g, gd, common, seat, head, tip, cur); err != nil {
			return seat, fmt.Errorf("seat %s worktree to the base tip %s: %v", seat.Handle, short(tip), err)
		}
		return seat, nil
	}
	// A local reset of the resting branch, never a push.
	if _, err := g.Run(root, []string{"GIT_DIR=" + common}, "update-ref", "-m", "amq-squad launch base "+seat.Handle, "refs/heads/"+seat.Branch, tip, cur); err != nil {
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

// ReasonBaseTipMoved refuses a launch whose fetched base tip is not the
// object id ls-remote reported for the base branch a moment before.
const ReasonBaseTipMoved = "base_tip_moved"

// ReasonSeatHeadUnreadable refuses a seat's launch when its existing
// worktree's HEAD names no commit (unborn or unreadable): the move to the tip
// is never a reset over files nothing accounts for.
const ReasonSeatHeadUnreadable = "seat_head_unreadable"

// ReasonSeatHeadDetachedUnmerged refuses a seat's launch when its worktree's
// HEAD is at a commit the base tip does not hold and that is not the resting
// branch's checked value (a detached HEAD's local commit): the harness never
// discards a local commit.
const ReasonSeatHeadDetachedUnmerged = "seat_head_detached_unmerged"

// ReasonSeatWorktreeDirty refuses a roster plan of a seat whose worktree
// has local changes, tracked or untracked (3.2.0-2b-1 item 2): the plan's
// own check, after an index refresh, with the seat named; never read-tree's
// text, which appears below the rule only when read-tree fails anyway.
const ReasonSeatWorktreeDirty = "seat_worktree_dirty"

// ReasonSeatWorktreeCheckFailed refuses a roster plan whose cleanliness
// check could not run (git's diff-index or ls-files failed, or the refresh
// failed on a tree the two then called clean).
const ReasonSeatWorktreeCheckFailed = "seat_worktree_check_failed"

// ReasonSeatBranchUnpushed refuses a seat's launch when its local resting
// branch holds commits the base tip does not: the harness never discards a
// local commit; the lead decides.
const ReasonSeatBranchUnpushed = "seat_branch_unpushed"

// ReasonBaseFetchUnauthenticated refuses a launch whose anonymous fetch of
// the base tip failed: an HTTPS push URL with no gh pinned (brief 31-1), a
// private repository most likely.
const ReasonBaseFetchUnauthenticated = "base_fetch_unauthenticated"

// baseTip is the base branch's tip: fetched from the push URL the launch
// recorded from the grant (into a shadow git directory, so the shared
// repository's config, remotes and url rewrites are never read), or the
// local base branch when the grant names no push URL. A remote seat branch
// ahead of the tip and unmerged refuses with ReasonSeatBranchUnmerged.
func (s *Slots) baseTip(g seatcommit.BaseGit, common string, seat seatlaunch.Seat) (string, error) {
	base := firstNonEmpty(s.BaseBranch, "main")
	url := s.Target.PushURL
	if url == "" {
		tip, err := g.Run(common, []string{"GIT_DIR=" + common}, "rev-parse", "--verify", "refs/heads/"+base+"^{commit}")
		if err != nil {
			return "", fmt.Errorf("seat %s base: the local base branch %s: %v", seat.Handle, base, err)
		}
		return tip, nil
	}
	// An HTTPS push URL fetches with the push's authentication: the pinned
	// gh as the only credential helper (seatcommit.FetchCredentials), or,
	// with no gh pinned, anonymously (brief 31-1), where a failure is
	// base_fetch_unauthenticated.
	cred, credEnv, anonymous, why := seatcommit.FetchCredentials(s.Target, os.Environ())
	if why != "" {
		return "", fmt.Errorf("seat %s base: %s", seat.Handle, why)
	}
	fetchFailed := func(err error) error {
		if anonymous {
			return fmt.Errorf("%s: seat %s: the anonymous fetch of %s failed (%v); pin gh (registry pins) for a private HTTPS repository, or use SSH; the gh pin is read from the registry as before, so an owner who already pinned gh sees no change", ReasonBaseFetchUnauthenticated, seat.Handle, url, err)
		}
		return fmt.Errorf("seat %s base: %v", seat.Handle, err)
	}
	sh, err := seatcommit.NewBaseShadow(g, common)
	if err != nil {
		return "", fmt.Errorf("seat %s base: %v", seat.Handle, err)
	}
	defer os.RemoveAll(sh.Dir)
	fg := seatcommit.BaseGit{Bin: g.Bin, Pre: cred} // ls-remote and fetch only
	fenv := append(append([]string(nil), sh.Env...), credEnv...)
	out, err := fg.Run(sh.Dir, fenv, "ls-remote", "--", url, "refs/heads/"+base, "refs/heads/"+seat.Branch)
	if err != nil {
		return "", fetchFailed(err)
	}
	refs := map[string]string{} // ref -> the object id ls-remote reported
	for _, line := range strings.Split(out, "\n") {
		if oid, ref, ok := strings.Cut(line, "\t"); ok {
			refs[ref] = oid
		}
	}
	if refs["refs/heads/"+base] == "" {
		return "", fmt.Errorf("seat %s base: the push URL has no branch %s", seat.Handle, base)
	}
	specs := []string{"+refs/heads/" + base + ":refs/base"}
	if refs["refs/heads/"+seat.Branch] != "" {
		specs = append(specs, "+refs/heads/"+seat.Branch+":refs/seat")
	}
	if _, err := fg.Run(sh.Dir, fenv, append([]string{"fetch", "-q", "--no-tags", "--no-write-fetch-head", "--", url}, specs...)...); err != nil {
		return "", fetchFailed(err)
	}
	tip, err := g.Run(sh.Dir, sh.Env, "rev-parse", "--verify", "refs/base^{commit}")
	if err != nil {
		return "", fmt.Errorf("seat %s base: %v", seat.Handle, err)
	}
	if want := refs["refs/heads/"+base]; tip != want {
		return "", fmt.Errorf("%s: seat %s: the push URL's %s was %s at ls-remote and %s at the fetch; launch again", ReasonBaseTipMoved, seat.Handle, base, short(want), short(tip))
	}
	if len(specs) == 2 {
		ahead, err := g.Run(sh.Dir, sh.Env, "rev-list", "--abbrev-commit", "--max-count=11", "refs/base..refs/seat")
		if err != nil {
			return "", fmt.Errorf("seat %s base: %v", seat.Handle, err)
		}
		if ahead != "" {
			return "", fmt.Errorf("%s: seat %s: the remote branch %s holds commits the %s tip %s does not (%s); the harness never force-pushes it: merge, close or delete it, then launch again", ReasonSeatBranchUnmerged, seat.Handle, seat.Branch, base, short(tip), strings.Join(strings.Fields(ahead), ", "))
		}
	}
	return tip, nil
}

// GrantBase is the base tip a task granted to the seat starts from (3.0.2
// t23, the reviewer's ruling: the base refreshes at the grant): the launch's
// fetch (baseTip) again, from the push URL the launch recorded, in the
// seat worktree's repository, whose object store keeps the fetched commit.
func (s *Slots) GrantBase(handle string) (string, error) {
	for _, seat := range s.Squad.Seats {
		if seat.Handle != handle {
			continue
		}
		if seat.Branch == "" {
			seat.Branch = "seat/" + seat.Handle
		}
		common, err := commonDir(seat.Worktree)
		if err != nil {
			return "", fmt.Errorf("seat %s base: %v", handle, err)
		}
		return s.baseTip(seatcommit.BaseGit{Bin: firstNonEmpty(s.Git, "git")}, common, seat)
	}
	return "", fmt.Errorf("seat %s is not in the squad's seat list", handle)
}

// TaskStart is the doorbell's Start (3.0.2 t23): the grant's base fetch
// (GrantBase), then the task's branch cut from that tip and recorded as the
// task's base (seatcommit.StartTask). Any error, a failed fetch included,
// rings nothing and blocks the task for the lead (task_branch_unprepared).
// A seat with no commit ledger (no recorded base, no git worktree) is left
// as it is, as CleanSeats leaves it: its commit step refuses base_unrecorded.
func (s *Slots) TaskStart(root string, now func() time.Time) func(handle string, t task.Task) error {
	return func(handle string, t task.Task) error {
		if _, err := seatcommit.ReadLedger(root, handle); os.IsNotExist(err) {
			return nil
		}
		tip, err := s.GrantBase(handle)
		if err != nil {
			return err
		}
		return seatcommit.StartTask(root, handle, s.Git, nil, t, tip, now())
	}
}

// moveWorktree puts an existing seat worktree (git dir gd, HEAD at head) on
// the tip: a two-tree read-tree from head in the shadow (a local change the
// move would overwrite refuses, nothing is lost; CleanSeat keeps any other
// leftover as evidence), then the resting branch moves from cur to the tip
// and HEAD names it.
func moveWorktree(g seatcommit.BaseGit, gd, common string, seat seatlaunch.Seat, head, tip, cur string) error {
	if head != tip {
		sh, err := seatcommit.NewBaseShadow(g, common)
		if err != nil {
			return err
		}
		defer os.RemoveAll(sh.Dir)
		if _, err := g.Run(seat.Worktree, append(sh.Env, "GIT_WORK_TREE="+seat.Worktree, "GIT_INDEX_FILE="+filepath.Join(gd, "index")), "read-tree", "-m", "-u", head, tip); err != nil {
			return err
		}
	}
	if _, err := g.Run(seat.Worktree, []string{"GIT_DIR=" + common}, "update-ref", "-m", "amq-squad launch base "+seat.Handle, "refs/heads/"+seat.Branch, tip, cur); err != nil {
		return err
	}
	_, err := g.Run(seat.Worktree, []string{"GIT_DIR=" + gd}, "symbolic-ref", "HEAD", "refs/heads/"+seat.Branch)
	return err
}

// baseGit, baseShadow and newBaseShadow moved to seatcommit (BaseGit,
// BaseShadow, NewBaseShadow) so the recovery's carry check uses the same
// shadow and credentials as this fetch (the plan reviewer on #904).

// cleanWorktree is the roster plan's own dirty-worktree check (3.2.0-2b-1
// item 2, the instances of 2026-11-05: a stale index refused three plans
// with "Entry 'MIGRATION.md' not uptodate"): the index is refreshed first
// through the hardened git (stat data only, never content), then the
// tracked diff against HEAD and the untracked, non-ignored files are read.
// A tree clean after the refresh proceeds; it reports whether the refresh
// changed the index (the caller logs that once). A tree still dirty refuses
// with ReasonSeatWorktreeDirty and the seat named, before any read-tree.
// update-index's nonzero exit is read only after the two listings: it
// exits nonzero for a file that needs an update, which the listings then
// name as dirty; a failed refresh on a clean tree is the check failing.
func cleanWorktree(g seatcommit.BaseGit, gd string, seat seatlaunch.Seat) (bool, error) {
	index := filepath.Join(gd, "index")
	before, _ := os.ReadFile(index)
	env := []string{"GIT_DIR=" + gd, "GIT_WORK_TREE=" + seat.Worktree, "GIT_INDEX_FILE=" + index}
	_, refreshErr := g.Run(seat.Worktree, env, "update-index", "--refresh")
	tracked, trackedErr := g.Run(seat.Worktree, env, "diff-index", "--name-only", "HEAD", "--")
	untracked, untrackedErr := g.Run(seat.Worktree, env, "ls-files", "--others", "--exclude-standard")
	if trackedErr != nil || untrackedErr != nil {
		return false, fmt.Errorf("%s: seat %s: the worktree cleanliness check failed", ReasonSeatWorktreeCheckFailed, seat.Handle)
	}
	if tracked != "" || untracked != "" {
		return false, fmt.Errorf("%s: seat %s: the worktree has local changes; archive or remove them before launch", ReasonSeatWorktreeDirty, seat.Handle)
	}
	if refreshErr != nil {
		return false, fmt.Errorf("%s: seat %s: the index refresh failed", ReasonSeatWorktreeCheckFailed, seat.Handle)
	}
	after, _ := os.ReadFile(index)
	return !bytes.Equal(before, after), nil
}

// commonDir is a checkout's common git directory, read from its .git.
func commonDir(root string) (string, error) {
	gd, err := gitDirOf(root)
	if err != nil {
		return "", err
	}
	return commonOf(gd), nil
}

// commonOf is a git dir's common directory: the one its commondir file
// names, else the git dir itself.
func commonOf(gd string) string {
	c, err := os.ReadFile(filepath.Join(gd, "commondir"))
	if err != nil {
		return gd
	}
	if c := strings.TrimSpace(string(c)); !filepath.IsAbs(c) {
		return filepath.Clean(filepath.Join(gd, c))
	}
	return filepath.Clean(strings.TrimSpace(string(c)))
}

// ReasonSeatGitdirForeign refuses a seat's launch when its worktree's .git
// leads to a git dir that is not its own: a symbolic link, or a gitdir file
// naming anything but <common>/worktrees/<name> of the project repository
// whose gitdir file points back at this worktree.
const ReasonSeatGitdirForeign = "seat_gitdir_foreign"

// seatGitDir is an existing seat worktree's own git dir, symlinks evaluated:
// a .git directory (a plain clone) as is; a .git file only when it names a
// directory directly under <common>/worktrees, common being the project
// repository's when the project root is a checkout, whose gitdir file names
// this worktree's .git. Anything else refuses with ReasonSeatGitdirForeign:
// a redirected .git would move another checkout's HEAD and refs.
func seatGitDir(wt, projectCommon string) (string, error) {
	dotgit := filepath.Join(wt, ".git")
	foreign := func(why string, args ...any) (string, error) {
		return "", fmt.Errorf("%s: the worktree %s: %s", ReasonSeatGitdirForeign, wt, fmt.Sprintf(why, args...))
	}
	st, err := os.Lstat(dotgit)
	switch {
	case err != nil:
		return "", fmt.Errorf("%s is not a git checkout: %v", wt, err)
	case st.Mode()&os.ModeSymlink != 0:
		return foreign("its .git is a symbolic link")
	case st.IsDir():
		return dotgit, nil
	}
	named, err := worktreeGitDir(wt)
	if err != nil {
		return foreign("%v", err)
	}
	gd, err := filepath.EvalSymlinks(named)
	if err != nil {
		return foreign("its .git names %s: %v", named, err)
	}
	common, err := filepath.EvalSymlinks(commonOf(gd))
	if err != nil || filepath.Dir(gd) != filepath.Join(common, "worktrees") {
		return foreign("its .git names %s, not a directory under a repository's worktrees/", gd)
	}
	if projectCommon != "" {
		if pc, err := filepath.EvalSymlinks(projectCommon); err != nil || pc != common {
			return foreign("its .git names %s, a worktree of %s, not of the project repository %s", gd, common, projectCommon)
		}
	}
	back, err := os.ReadFile(filepath.Join(gd, "gitdir"))
	if err != nil {
		return foreign("%s has no gitdir file: %v", gd, err)
	}
	b := strings.TrimSpace(string(back))
	if !filepath.IsAbs(b) {
		b = filepath.Join(gd, b)
	}
	rb, err1 := filepath.EvalSymlinks(b)
	rd, err2 := filepath.EvalSymlinks(dotgit)
	if err1 != nil || err2 != nil || rb != rd {
		return foreign("%s belongs to %s, not to this worktree", gd, b)
	}
	return gd, nil
}

// gitDirOf is a checkout's git directory: its .git directory (a plain
// clone), or the one a linked worktree's .git file names.
func gitDirOf(wt string) (string, error) {
	dotgit := filepath.Join(wt, ".git")
	st, err := os.Lstat(dotgit)
	if err != nil {
		return "", fmt.Errorf("%s is not a git checkout: %v", wt, err)
	}
	if st.IsDir() {
		return dotgit, nil
	}
	return worktreeGitDir(wt)
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
		l.Slots.inUse, l.Slots.byReq = map[string]string{}, map[string]string{}
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
