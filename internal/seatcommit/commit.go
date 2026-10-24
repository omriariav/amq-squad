// Package seatcommit is the harness-side commit step of 3.0 (plan 4.3, 7.2,
// amendments 21 and 28): `amq-squad seat commit --seat H`, which the
// scheduler runs on a seat's done report. The seat only changes its
// worktree; the Go core takes the tree, checks every changed path against
// the task's declared_files, the grant's protected paths and the size cap,
// runs the global-config diff of the seat exit, and only then commits under
// the harness identity and pushes the seat branch (never a force push). A
// refusal names its reason code and goes to the lead through the task store
// (task.RefuseCommit). The model is never the enforcement point.
package seatcommit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/redact"
	"github.com/omriariav/amq-squad/v3/internal/registry"
	"github.com/omriariav/amq-squad/v3/internal/seatexec"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Identity is the author and committer of every harness commit.
type Identity struct{ Name, Email string }

// DefaultIdentity is the harness identity when the caller names none.
var DefaultIdentity = Identity{Name: "amq-squad harness", Email: "harness@amq-squad.invalid"}

// Options are the inputs of one commit step.
type Options struct {
	Root   string    // the AMQ session root (task store, ledger, staged launch records)
	Handle string    // the seat
	Task   task.Task // the task whose done report the seat filed
	Grants grants.Resolved
	// GrantsErr is the grant load error; any error refuses (grants_invalid).
	GrantsErr error
	Home      string // the owner's home (preflight diff, ~/ protected entries)
	Git       string // git executable; default from PATH
	Identity  Identity
	Gates     GateChecker // accepted growth gates (nil: none)
	Env       []string    // default os.Environ()
	Now       func() time.Time
	// DryRun computes the diff and the checks only: no preflight diff, no
	// commit, push or ledger write (the widening check).
	DryRun bool
	// Declared replaces the task's declared_files (a proposed widening, dry
	// run only).
	Declared []string
	// OpenPR opens (or finds) the seat branch's draft PR with gh after the
	// push; Gh is the gh executable (default from PATH).
	OpenPR bool
	Gh     string
	// PRBoundTo names the task the store binds a PR number to ("" when
	// none): the only provenance of a PR (R7).
	PRBoundTo func(number int) string
	// BindPR binds a PR the step opened to the task at once (the store's
	// BindPR), so a re-run finds it bound.
	BindPR func(number int, head string) error
	// Clear verifies the owner's signed clear of a quarantine (the owner's
	// trust store); nil verifies nothing, so no clear counts.
	Clear ClearVerifier
}

// Change is one changed path of the seat's diff.
type Change struct {
	Path    string `json:"path"`
	Added   int    `json:"added"`
	Deleted int    `json:"deleted"`
	Binary  bool   `json:"binary,omitempty"`
}

// Result is the step's answer. OK, a refusal (ReasonCode, Route) or
// Deferred (an infrastructure failure: nothing was decided).
type Result struct {
	OK             bool                    `json:"ok"`
	Deferred       bool                    `json:"deferred,omitempty"`
	ReasonCode     string                  `json:"reason_code,omitempty"`
	Route          string                  `json:"route,omitempty"`
	Detail         string                  `json:"detail,omitempty"`
	Task           string                  `json:"task"`
	Seat           string                  `json:"seat"`
	Branch         string                  `json:"branch,omitempty"`
	Parent         string                  `json:"parent,omitempty"`
	Tree           string                  `json:"tree,omitempty"`
	Commit         string                  `json:"commit,omitempty"`
	Pushed         bool                    `json:"pushed"`
	Changes        []Change                `json:"changes"`
	Lines          int                     `json:"lines"`
	Files          int                     `json:"files"`
	CapLines       int                     `json:"cap_lines"`
	CapFiles       int                     `json:"cap_files"`
	DeclaredDigest string                  `json:"declared_digest,omitempty"`
	DiffDigest     string                  `json:"diff_digest,omitempty"`
	Violations     []Violation             `json:"violations,omitempty"`
	Config         []seatexec.ConfigChange `json:"config_changes,omitempty"`
	Notes          []string                `json:"notes,omitempty"`
	// Quarantine is set on a global_config_changed or claude_config_changed
	// refusal: the caller quarantines the seat (no new lease) and closes it.
	Quarantine bool `json:"quarantine,omitempty"`
	// PRNumber and PRHead are the task binding the commit step writes (the
	// PR it opened, 0 when none, and the head it pushed).
	PRNumber int    `json:"pr_number,omitempty"`
	PRHead   string `json:"pr_head,omitempty"`
}

func (r Result) refuse(code, route, detail string) Result {
	r.OK, r.ReasonCode, r.Route, r.Detail = false, code, route, detail
	if r.Route == "" {
		r.Route = task.RouteLead
	}
	return r
}

func (r Result) defer_(detail string) Result {
	r.OK, r.Deferred, r.Detail = false, true, detail
	return r
}

// Run is the commit step for one done report. It holds the seat's ledger
// lock for the whole step, so the scheduler's run and a lead's manual run
// never interleave. It writes no task state: the caller records the result
// (task.CompleteBound, task.RefuseCommit).
func Run(o Options) Result {
	res := Result{Task: o.Task.ID, Seat: o.Handle, Changes: []Change{}}
	// A dry run also takes a task blocked by a refusal (the widening check).
	awaiting := o.Task.State == task.StateInProgress || (o.DryRun && o.Task.State == task.StateBlocked)
	if o.Task.Report == nil || o.Task.Report.By != o.Handle || !awaiting {
		return res.refuse(ReasonWorktree, task.RouteLead, fmt.Sprintf("task %s has no done report by %s awaiting the commit step", o.Task.ID, o.Handle))
	}
	if o.DryRun {
		return run(o.withDefaults(), res)
	}
	var out Result
	err := withLedgerLock(o.Root, o.Handle, func() error {
		out = run(o.withDefaults(), res)
		return nil
	})
	if err != nil {
		return res.defer_("commit ledger lock: " + err.Error())
	}
	return out
}

func (o Options) withDefaults() Options {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Env == nil {
		o.Env = os.Environ()
	}
	if o.Identity.Name == "" || o.Identity.Email == "" {
		o.Identity = DefaultIdentity
	}
	if o.Home == "" {
		o.Home = homeDir()
	}
	if o.Git == "" {
		if p, err := exec.LookPath("git"); err == nil {
			o.Git = p
		}
	}
	return o
}

// exitChanges is amendment 28's check at a seat exit: the owner's global
// agent config against the preflight copy, a Claude seat's config dir
// against its launch digest, and a quarantine the store already holds for
// the seat (the hook's context call found the dir changed during the run).
// It returns the reason code and the changes; "" when nothing changed.
func exitChanges(o Options) (string, []seatexec.ConfigChange, error) {
	changes, err := seatexec.PreflightDiff(o.Home)
	if err != nil {
		return "", nil, err
	}
	code := ""
	if len(changes) > 0 {
		code = ReasonGlobalConfig
	}
	if rec, err := stagedRecord(o.Root, o.Handle); err == nil && rec.Runtime == "claude" {
		dir := claudeseat.Dir(rec.Root, rec.Handle, rec.LaunchID)
		if drift, detail := claudeseat.ConfigDrift(rec.Root, rec.Handle, rec.LaunchID, rec.ClaudeConfig); drift {
			changes = append(changes, seatexec.ConfigChange{File: dir, Preflight: "the launch digest " + rec.ClaudeConfig, Detail: detail})
			code = firstNonEmpty(code, ReasonClaudeConfig)
		}
		// P1-10: a Claude seat that ran (an accepted proof in the core's
		// launch ledger) with no hook report since its launch had its hook
		// removed.
		if proof, err := launch.ReadProof(rec.Root, rec.Handle); err == nil && rec.ClaudeConfig != "" && proof.PID > 0 {
			if n, err := claudeseat.ReportsSince(rec.Root, rec.Handle, proof.At); err != nil || n == 0 {
				changes = append(changes, seatexec.ConfigChange{File: dir, Detail: fmt.Sprintf("no hook context report since the launch at %s: a removed hook is the drift signal", proof.At.UTC().Format(time.RFC3339))})
				code = firstNonEmpty(code, claudeseat.HooksSilent)
			}
		}
	}
	if code == "" {
		qs, err := task.Quarantined(task.DirFor(o.Root))
		if err != nil {
			return "", nil, err
		}
		for _, q := range qs {
			if q.Handle == o.Handle {
				changes = append(changes, seatexec.ConfigChange{File: "task store quarantine", Detail: q.Reason + ": " + q.Detail})
				code = firstNonEmpty(q.Reason, ReasonGlobalConfig)
			}
		}
	}
	return code, changes, nil
}

// ExitCheck runs the exit check at a seat exit that is not a done report
// (failed, gone, expired: R3 of the milestone B first pass): on a change the
// seat's tree is snapshotted onto the ledger as evidence and the result
// quarantines the seat, exactly as at a done report. OK means nothing
// changed. why names the exit (failed, gone, expired) in the detail.
func ExitCheck(o Options, why string) Result {
	res := Result{Task: o.Task.ID, Seat: o.Handle, Changes: []Change{}}
	o = o.withDefaults()
	var out Result
	err := withLedgerLock(o.Root, o.Handle, func() error {
		code, changes, err := exitChanges(o)
		switch {
		case err != nil:
			out = res.defer_("exit check: " + err.Error())
		case code != "":
			out = quarantine(o, res, code, changes, "at the seat's exit ("+why+")")
		default:
			res.OK = true
			out = res
		}
		return nil
	})
	if err != nil {
		return res.defer_("commit ledger lock: " + err.Error())
	}
	return out
}

func run(o Options, res Result) Result {
	if o.Git == "" {
		return res.defer_("git is not on PATH")
	}
	// Amendment 28 and R3: the exit check at every seat exit, before anything
	// is committed. A change blocks before the commit step and quarantines
	// the seat.
	if !o.DryRun {
		code, changes, err := exitChanges(o)
		if err != nil {
			return res.defer_("exit check: " + err.Error())
		}
		if code != "" {
			return quarantine(o, res, code, changes, "")
		}
	}
	if o.GrantsErr != nil {
		return res.refuse(ReasonGrants, task.RouteLead, "the grant could not be read: "+o.GrantsErr.Error())
	}
	rec, err := stagedRecord(o.Root, o.Handle)
	if err != nil {
		return res.refuse(ReasonWorktree, task.RouteLead, err.Error())
	}
	res.Branch = rec.Branch
	ledger, err := ReadLedger(o.Root, o.Handle)
	if os.IsNotExist(err) {
		return res.refuse(ReasonBaseUnrecorded, task.RouteLead, "no core-recorded base for seat "+o.Handle+"; the launch records it (seat commit --base SHA records one as a lead decision)")
	}
	if err != nil {
		return res.defer_(err.Error())
	}
	if ledger.Worktree != rec.Worktree || ledger.Branch != rec.Branch {
		return res.refuse(ReasonWorktree, task.RouteLead, fmt.Sprintf("the commit ledger records %s on %s, the launch record %s on %s", ledger.Worktree, ledger.Branch, rec.Worktree, rec.Branch))
	}
	env := harnessEnv(o.Env)
	rp, repoRoot, why := checkWorktree(rec, o.Task.ProjectPath)
	if why != "" {
		return res.refuse(ReasonWorktree, task.RouteLead, why)
	}
	sh, err := newShadow(o.Git, env, rp)
	if err != nil {
		return res.defer_("the harness git dir: " + err.Error())
	}
	defer sh.close()
	g := sh.g
	parent := ledger.Parent()
	res.Parent = parent
	tree, err := snapshot(sh, parent)
	if err != nil {
		return res.defer_("snapshot of the worktree: " + err.Error())
	}
	res.Tree = tree
	if q := ledger.Quarantine; q != nil && !o.DryRun {
		switch {
		case q.ClearedAt == nil:
			res.Quarantine = true
			return res.refuse(firstNonEmpty(q.Reason, ReasonGlobalConfig), task.RouteOwner, fmt.Sprintf("seat %s is quarantined since %s (task %s); nothing of its work is committed until the owner clears it (amq-squad seat clear --seat %s)", o.Handle, q.At.Format(time.RFC3339), q.Task, o.Handle))
		case !q.Clear.Verified(o.Handle, q, o.Clear):
			res.Quarantine = true
			return res.refuse(ReasonClearUnsigned, task.RouteOwner, fmt.Sprintf("seat %s's quarantine carries a clear that is not the owner's signed record (a trusted key in the owner's trust store); it stays quarantined", o.Handle))
		case q.Task == o.Task.ID && q.Tree != "" && q.Tree != tree:
			return res.refuse(ReasonQuarantineTree, task.RouteOwner, fmt.Sprintf("the seat's tree changed after the quarantine: the owner cleared %s, the worktree is now %s", q.Tree, tree))
		}
		ledger.Quarantine = nil
		if err := writeLedger(o.Root, ledger); err != nil {
			return res.defer_("commit ledger: " + err.Error())
		}
		res.Notes = append(res.Notes, "the owner's signed clear lifted the quarantine of task "+q.Task)
	}
	raw, err := g.run(nil, nil, "diff-tree", "-r", "-z", "--no-renames", "--no-abbrev", "--no-ext-diff", "--no-textconv", "--raw", parent, tree)
	if err != nil {
		return res.defer_(err.Error())
	}
	entries, err := parseRaw(raw)
	if err != nil {
		return res.defer_(err.Error())
	}
	ns, err := g.run(nil, nil, "diff-tree", "-r", "-z", "--no-renames", "--no-ext-diff", "--no-textconv", "--numstat", parent, tree)
	if err != nil {
		return res.defer_(err.Error())
	}
	stats, err := parseNumstat(ns)
	if err != nil {
		return res.defer_(err.Error())
	}
	declared := o.Task.DeclaredFiles
	if o.DryRun && o.Declared != nil {
		declared = o.Declared
	}
	res.DeclaredDigest = digest([]byte(strings.Join(declared, "\n")))
	res.DiffDigest = digest(raw)
	for _, s := range stats {
		res.Changes = append(res.Changes, Change{Path: s.Path, Added: s.Added, Deleted: s.Deleted, Binary: s.Binary})
		res.Lines += s.Added + s.Deleted
	}
	res.Files = len(res.Changes)
	// The repository and base branch the launch recorded from the grant,
	// never the seat's remote (R2).
	res.CapLines, res.CapFiles = o.Grants.SizeCap(ledger.Repository, ledger.base(o.Grants))
	res.Violations = check(o, g, rec, repoRoot, parent, tree, entries, declared, &res)
	if len(res.Violations) > 0 {
		v := res.Violations[0]
		return res.refuse(v.Reason, task.RouteLead, describe(res.Violations))
	}
	if o.DryRun {
		res.OK = true
		return res
	}
	// D2: the target the launch recorded must still be the grant's.
	if want := TargetFor(o.Grants); ledger.Target.grantPart() != want.grantPart() {
		return res.refuse(ReasonPushTargetUnacknowledged, task.RouteOwner, fmt.Sprintf("the push target the launch recorded (%s, %s, %q, ack %v) is not the grant's now (%s, %s, %q, ack %v); nothing is committed or pushed",
			ledger.Repository, ledger.BaseBranch, ledger.PushURL, ledger.LocalPushAck, want.Repository, want.BaseBranch, want.PushURL, want.LocalPushAck))
	}
	if err := CheckTarget(ledger.Target); err != nil {
		return res.refuse(ReasonPushTargetUnacknowledged, task.RouteOwner, err.Error())
	}
	return commitAndPush(o, sh, ledger, rec, res)
}

// base is the branch the seat's work goes to: the launch's record of the
// grant, else the grant's default branch, else main.
func (l Ledger) base(g grants.Resolved) string {
	return firstNonEmpty(l.BaseBranch, g.DefaultBranch, "main")
}

// quarantine is the config-change block: before the commit step, the seat's
// tree is snapshotted (never committed or pushed) and kept on the ledger
// with its diff digest and the config diff as the block's evidence; the
// caller quarantines the seat (task.Store.Quarantine: no new lease) and
// closes it. Only an existing ledger holds the evidence; without one the
// refusal still stands and names why no tree was kept.
func quarantine(o Options, res Result, code string, changes []seatexec.ConfigChange, when string) Result {
	res.Config, res.Quarantine = changes, true
	var files []string
	for _, c := range changes {
		files = append(files, c.File+" ("+c.Detail+")")
	}
	what := "the owner's global agent config changed during the seat's run"
	if code == ReasonClaudeConfig {
		what = "the seat's Claude config dir changed during its run"
	}
	if when != "" {
		what += " " + when
	}
	detail := what + ": " + strings.Join(files, "; ") + "; the seat is quarantined and its diff kept uncommitted until the owner clears it (amq-squad seat clear --seat " + o.Handle + ")"
	q := LedgerQuarantine{Task: o.Task.ID, Reason: code, Config: changes, At: o.Now().UTC()}
	l, lerr := ReadLedger(o.Root, o.Handle)
	if lerr == nil && l.Quarantine != nil && l.Quarantine.ClearedAt == nil {
		return res.refuse(code, task.RouteOwner, detail) // the first evidence stays
	}
	if rec, err := stagedRecord(o.Root, o.Handle); err != nil {
		q.Snapshot = err.Error()
	} else if lerr != nil {
		q.Snapshot = "no commit ledger: " + lerr.Error()
	} else if rp, err := worktreePaths(rec.Worktree); err != nil {
		q.Snapshot = err.Error()
	} else if sh, err := newShadow(o.Git, harnessEnv(o.Env), rp); err != nil {
		q.Snapshot = err.Error()
	} else {
		defer sh.close()
		q.Parent = l.Parent()
		if tree, err := snapshot(sh, q.Parent); err != nil {
			q.Snapshot = err.Error()
		} else if raw, err := sh.g.run(nil, nil, "diff-tree", "-r", "-z", "--no-renames", "--no-abbrev", "--no-ext-diff", "--no-textconv", "--raw", q.Parent, tree); err != nil {
			q.Snapshot = err.Error()
		} else {
			q.Tree, q.DiffDigest = tree, digest(raw)
			res.Parent, res.Tree, res.DiffDigest = q.Parent, tree, q.DiffDigest
		}
	}
	if lerr == nil {
		l.Quarantine = &q
		if err := writeLedger(o.Root, l); err != nil {
			detail += "; the evidence could not be written: " + err.Error()
		}
	} else {
		detail += "; no tree kept: " + q.Snapshot
	}
	return res.refuse(code, task.RouteOwner, detail)
}

// CleanSeat returns a seat's worktree to its ledger parent before the seat
// gets a new lease (2b-2 milestone B, the R19 live finding: after a refused
// task was reset, the seat's next lease inherited the refused diff and every
// later commit of that seat was refused for it, a loop). A diff left on the
// worktree is the leftover of an attempt that did not commit (refused and
// reset, failed, expired): it is snapshotted and recorded on the ledger as
// evidence (tree and diff digest), then the worktree is checked out at the
// parent in the harness's shadow (no filter or hook of the seat's
// repository runs), untracked, non-ignored files are removed and the seat
// branch is moved to the parent. It does nothing for a clean worktree. Seat
// commits on the branch are dropped too: the parent is the core's record,
// never the seat's history. The whole reset runs under the task store's
// lock with the seat free (R3): no claim can land between the check and the
// reset.
func CleanSeat(root, handle, gitBin string, env []string, clear ClearVerifier, now time.Time) (discarded bool, err error) {
	if gitBin == "" {
		if gitBin, err = exec.LookPath("git"); err != nil {
			return false, err
		}
	}
	if env == nil {
		env = os.Environ()
	}
	store, err := task.Open(root)
	if err != nil {
		return false, err
	}
	err = withLedgerLock(root, handle, func() error {
		// Only between leases, never mid-task (condition 2 of the ruling),
		// with the store locked for the whole reset.
		return store.WithSeatFree(handle, func(list []task.Task) error {
			var derr error
			discarded, derr = cleanSeat(root, handle, gitBin, env, clear, list, now)
			return derr
		})
	})
	return discarded, err
}

// beforeReset is a test seam: it runs inside CleanSeat between the check
// and the reset.
var beforeReset func()

func cleanSeat(root, handle, gitBin string, env []string, clear ClearVerifier, list []task.Task, now time.Time) (bool, error) {
	rec, err := stagedRecord(root, handle)
	if err != nil {
		return false, err
	}
	l, err := ReadLedger(root, handle)
	if err != nil {
		return false, err
	}
	if q := l.Quarantine; q != nil && (q.ClearedAt == nil || !q.Clear.Verified(handle, q, clear)) {
		return false, fmt.Errorf("seat %s is quarantined; its tree is evidence and is not cleaned", handle)
	}
	rp, _, why := checkWorktree(rec, "")
	if why != "" {
		return false, fmt.Errorf("%s", why)
	}
	henv := harnessEnv(env)
	sh, err := newShadow(gitBin, henv, rp)
	if err != nil {
		return false, err
	}
	defer sh.close()
	parent := l.Parent()
	tree, err := snapshot(sh, parent)
	if err != nil {
		return false, err
	}
	parentTree, err := sh.g.line("rev-parse", parent+"^{tree}")
	if err != nil {
		return false, err
	}
	repo := seatRepo(gitBin, henv, rp)
	head, _ := repo.line("rev-parse", "--verify", "-q", "refs/heads/"+rec.Branch)
	if tree == parentTree && head == parent {
		return false, nil
	}
	raw, _ := sh.g.run(nil, nil, "diff-tree", "-r", "-z", "--no-renames", "--no-abbrev", "--no-ext-diff", "--no-textconv", "--raw", parent, tree)
	taskID, last := lastAttempt(list, handle)
	l.Discarded = append(l.Discarded, LedgerDiscard{TaskID: taskID, Reason: last, Parent: parent, Tree: tree, DiffDigest: digest(raw), At: now.UTC()})
	if err := writeLedger(root, l); err != nil {
		return false, err
	}
	if beforeReset != nil {
		beforeReset()
	}
	// The checkout: an index of the worktree as it is (the snapshot), reset
	// to the parent with the files updated, then the untracked files
	// removed; then the seat's own index and branch follow the parent.
	idx := sh.tempIndex()
	os.Remove(idx)
	defer os.Remove(idx)
	ig := sh.index(idx)
	if _, err := ig.run(nil, nil, "read-tree", tree); err != nil {
		return false, err
	}
	if _, err := ig.run(nil, nil, "read-tree", "-u", "--reset", parent); err != nil {
		return false, err
	}
	if _, err := ig.run(nil, nil, "clean", "-fdq"); err != nil {
		return false, err
	}
	if _, err := sh.index(rp.Index).run(nil, nil, "read-tree", parent); err != nil {
		return false, err
	}
	if _, err := repo.run(nil, nil, "update-ref", "-m", "amq-squad clean seat "+handle, "refs/heads/"+rec.Branch, parent); err != nil {
		return false, err
	}
	return true, nil
}

// lastAttempt is the seat's most recently ended attempt: its task and why it
// ended ("commit_refused: outside_declared_files", "failed: tests red", ...).
func lastAttempt(list []task.Task, handle string) (string, string) {
	var at time.Time
	id, why := "", "no ended attempt of this seat in the store"
	for _, t := range list {
		for _, a := range t.Attempts {
			if a.Holder == handle && a.EndedAt.After(at) {
				at, id, why = a.EndedAt, t.ID, a.Outcome
				if a.Detail != "" {
					why += ": " + a.Detail
				}
			}
		}
	}
	return id, why
}

// StagedSeat is the seat's staged launch record (worktree and branch).
func StagedSeat(root, handle string) (launch.Record, error) { return stagedRecord(root, handle) }

// stagedRecord reads the launch record the core staged under the session
// root (launch.StagedDir, inside the guard's protected extension dir), not
// the copy seat-exec publishes in the seat's mailbox.
func stagedRecord(root, handle string) (launch.Record, error) {
	raw, err := os.ReadFile(filepath.Join(launch.StagedDir(root, handle), launch.FileName))
	if err != nil {
		return launch.Record{}, fmt.Errorf("no staged launch record for seat %s: %v", handle, err)
	}
	rec, err := launch.Decode(raw)
	if err != nil {
		return rec, fmt.Errorf("staged launch record for seat %s: %v", handle, err)
	}
	if !filepath.IsAbs(rec.Worktree) || rec.Branch == "" || rec.Handle != handle {
		return rec, fmt.Errorf("the staged launch record for seat %s names no absolute worktree and branch", handle)
	}
	return rec, nil
}

// checkWorktree confirms, from git's own files (never the seat repository's
// config), that the worktree is a checkout root with the record's branch
// checked out, and of the task's repository when one is named. It returns
// the worktree's git paths and the repository root (the task's project
// path, else the worktree).
func checkWorktree(rec launch.Record, project string) (repoPaths, string, string) {
	rp, err := worktreePaths(rec.Worktree)
	if err != nil {
		return rp, "", "the seat worktree " + rec.Worktree + " is not a git checkout root: " + err.Error()
	}
	if rp.HeadRef != "refs/heads/"+rec.Branch {
		return rp, "", fmt.Sprintf("the seat worktree has %q checked out, not the seat branch %s", rp.HeadRef, rec.Branch)
	}
	if project == "" {
		return rp, rec.Worktree, ""
	}
	pp, err := worktreePaths(project)
	if err != nil || evalOr(pp.Common) != evalOr(rp.Common) {
		return rp, "", fmt.Sprintf("the seat worktree %s is not a checkout of the task's repository %s", rec.Worktree, project)
	}
	return rp, project, ""
}

// check runs every path check and returns the violations, most severe
// first: protected paths, outside the declared set, nested repositories,
// then the size cap.
func check(o Options, g git, rec launch.Record, repoRoot, parent, tree string, entries []rawEntry, declared []string, res *Result) []Violation {
	extra := []string{filepath.Join(o.Root, "extensions", "amq-squad") + "/", "~/.amq-squad/"}
	if rec.RulesPath != "" {
		extra = append(extra, rec.RulesPath)
	}
	ps := protectedFor(o.Grants.Grants, repoRoot, o.Home, extra...)
	gated := gatedProtected(o.Task, o.Root, o.Gates)
	var protected, managed, outside, nested []string
	for _, e := range entries {
		p := e.Path
		if e.NewMode == "160000" || e.OldMode == "160000" {
			nested = append(nested, p)
		}
		if entry, hit := ps.match(p); hit && !matchFold(gated, p) {
			protected = append(protected, p+" ("+entry+")")
		} else if name := ps.managedName(p); name != "" && !matchFold(gated, p) && managedChanged(g, parent, tree, p) {
			protected = append(protected, p+" (the managed block of "+name+")")
			managed = append(managed, p)
		}
		if !task.MatchPath(declared, p) {
			outside = append(outside, p)
		}
	}
	var out []Violation
	if len(protected) > 0 {
		out = append(out, Violation{Reason: ReasonProtected, Paths: protected, Managed: managed})
	}
	if len(outside) > 0 {
		out = append(out, Violation{Reason: ReasonOutside, Paths: outside, Detail: "declared: " + strings.Join(declared, ", ")})
	}
	if len(nested) > 0 {
		out = append(out, Violation{Reason: ReasonNested, Paths: nested})
	}
	capLines, capFiles := res.CapLines, res.CapFiles
	if gl, gf := gatedSize(o.Task, o.Root, o.Gates); gl > 0 || gf > 0 {
		capLines, capFiles = max(capLines, gl), max(capFiles, gf)
		res.Notes = append(res.Notes, fmt.Sprintf("size cap raised by an accepted growth gate to %d lines, %d files", capLines, capFiles))
	}
	// One reason code for both halves of the cap; the detail names which.
	var over []string
	if res.Lines > capLines {
		over = append(over, fmt.Sprintf("size_cap_lines: %d changed lines over the cap of %d", res.Lines, capLines))
	}
	if res.Files > capFiles {
		over = append(over, fmt.Sprintf("size_cap_files: %d changed files over the cap of %d", res.Files, capFiles))
	}
	if len(over) > 0 {
		out = append(out, Violation{Reason: ReasonSizeCap, Detail: strings.Join(over, "; ")})
	}
	return out
}

// managedChanged reports whether the amq-squad managed block of a file
// differs between the parent and the new tree (a missing side reads as
// empty, so adding or removing a block is a change).
func managedChanged(g git, parent, tree, p string) bool {
	read := func(rev string) string {
		out, err := g.run(nil, nil, "cat-file", "blob", rev+":"+p)
		if err != nil {
			return ""
		}
		return string(out)
	}
	return !sameShape(read(parent), read(tree))
}

func describe(vs []Violation) string {
	var parts []string
	for _, v := range vs {
		s := v.Reason
		if len(v.Paths) > 0 {
			paths := v.Paths
			more := ""
			if len(paths) > 8 {
				paths, more = paths[:8], fmt.Sprintf(" and %d more", len(v.Paths)-8)
			}
			s += ": " + strings.Join(paths, ", ") + more
		}
		if v.Detail != "" {
			s += " (" + v.Detail + ")"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, "; ")
}

// commitAndPush writes the harness commit (parent: the ledger's last harness
// commit or the base; the seat's own commits, if any, are not in its
// history), moves the seat branch to it, records it in the ledger, and
// pushes it as a fast-forward to the URL the launch recorded from the grant.
// A re-run after a crash finds the recorded commit and only pushes.
func commitAndPush(o Options, sh *shadow, l Ledger, rec launch.Record, res Result) Result {
	g := sh.g
	last := len(l.Commits) - 1
	parentTree, err := g.line("rev-parse", res.Parent+"^{tree}")
	if err != nil {
		return res.defer_(err.Error())
	}
	switch {
	case res.Tree == parentTree && last >= 0 && l.Commits[last].Commit == res.Parent && l.Commits[last].Task == o.Task.ID:
		res.Commit = res.Parent // this task's commit exists; finish its push
		res.Notes = append(res.Notes, "the harness commit "+res.Parent+" was already recorded")
	case res.Tree == parentTree:
		res.Notes = append(res.Notes, "empty diff: nothing to commit")
		res.OK = true
		return res
	default:
		now := o.Now().UTC().Format(time.RFC3339)
		ident := []string{"GIT_AUTHOR_NAME=" + o.Identity.Name, "GIT_AUTHOR_EMAIL=" + o.Identity.Email, "GIT_AUTHOR_DATE=" + now,
			"GIT_COMMITTER_NAME=" + o.Identity.Name, "GIT_COMMITTER_EMAIL=" + o.Identity.Email, "GIT_COMMITTER_DATE=" + now}
		out, err := g.run([]byte(Message(o.Task, o.Handle, res.DeclaredDigest, res.DiffDigest)), ident, "commit-tree", res.Tree, "-p", res.Parent, "-F", "-")
		if err != nil {
			return res.defer_(err.Error())
		}
		res.Commit = strings.TrimSpace(string(out))
		l.Head = res.Commit
		l.Commits = append(l.Commits, LedgerCommit{Task: o.Task.ID, Commit: res.Commit, Parent: res.Parent, Tree: res.Tree, At: o.Now().UTC()})
		last = len(l.Commits) - 1
		if err := writeLedger(o.Root, l); err != nil {
			return res.defer_("commit ledger: " + err.Error())
		}
	}
	repo := seatRepo(o.Git, harnessEnv(o.Env), sh.rp)
	if _, err := repo.run(nil, nil, "update-ref", "-m", "amq-squad seat commit "+o.Task.ID, "refs/heads/"+rec.Branch, res.Commit); err != nil {
		return res.defer_(err.Error())
	}
	// The seat's index follows the new branch tip; the files are untouched.
	if _, err := sh.index(sh.rp.Index).run(nil, nil, "read-tree", res.Commit); err != nil {
		return res.defer_(err.Error())
	}
	if l.Commits[last].Pushed {
		res.Pushed, res.OK, res.PRHead = true, true, res.Commit
		return openPR(o, sh, l, rec, res)
	}
	if l.PushURL == "" {
		res.Notes = append(res.Notes, PushURLMissing+": the launch recorded no push URL from the grant (merge.push_url): committed, not pushed")
		res.OK = true
		return res
	}
	pushArgs, pushEnv, why := pushCredentials(o, l)
	if why != "" {
		return res.refuse(registry.PinMismatch, task.RouteOwner, why)
	}
	out, err := g.run(nil, pushEnv, append(pushArgs, "push", "--porcelain", "--no-verify", l.PushURL, res.Commit+":refs/heads/"+rec.Branch)...)
	if err != nil {
		text := string(out) + err.Error()
		if strings.Contains(text, "[rejected]") || strings.Contains(text, "non-fast-forward") || strings.Contains(text, "fetch first") {
			return res.refuse(ReasonPushRejected, task.RouteLead, "the remote refused a fast-forward push of "+rec.Branch+" (no force push): "+firstLine(text))
		}
		return res.defer_("push: " + firstLine(text))
	}
	l.Commits[last].Pushed = true
	if err := writeLedger(o.Root, l); err != nil {
		return res.defer_("commit ledger: " + err.Error())
	}
	res.Pushed, res.OK, res.PRHead = true, true, res.Commit
	return openPR(o, sh, l, rec, res)
}

// PushURLMissing is the note (and status attention) of a commit step whose
// launch recorded no push URL (D1): it commits and does not push.
const PushURLMissing = "push_url_missing"

// pushCredentials is the push's authentication (D2), passed by the harness
// itself, nothing read from a config file: an https://github.com push uses
// the pinned gh (re-hashed now against the digest the launch recorded) as
// the only credential helper, with gh's own token variables the one
// exception to the credential strip, on this call only; an SSH push uses the
// agent the harness environment carries; a local push needs nothing.
func pushCredentials(o Options, l Ledger) (args, env []string, why string) {
	if pushKindOf(l.PushURL) != grants.PushHTTPS {
		return nil, nil, ""
	}
	// The pinned gh is re-hashed through its descriptor immediately before
	// the push that runs it as the helper (git execs the verified path).
	tool, why := pinnedGh(l)
	if why != "" {
		return nil, nil, why
	}
	gh := tool.Path
	tool.Close()
	args = []string{"-c", "credential.helper=", "-c", "credential.helper=!" + shellQuote(gh) + " auth git-credential"}
	for _, kv := range o.Env {
		if name, _, _ := strings.Cut(kv, "="); name == "GH_TOKEN" || name == "GITHUB_TOKEN" {
			env = append(env, kv)
		}
	}
	return args, env, ""
}

// pinnedGh is the gh the launch pinned, opened through the anchored walk
// and re-hashed through its descriptor (the caller closes it).
func pinnedGh(l Ledger) (*registry.VerifiedTool, string) {
	if l.GhPath == "" || l.GhSHA256 == "" {
		return nil, registry.PinMissing + ": the launch recorded no pinned gh (registry pins: gh with path and sha256)"
	}
	tool, code, detail := registry.OpenPinned("gh", l.GhPath, l.GhSHA256)
	if tool == nil {
		return nil, code + ": " + detail
	}
	return tool, ""
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// ghPR is one open PR as `gh pr list --json` reports it.
type ghPR struct {
	Number            int    `json:"number"`
	State             string `json:"state"`
	HeadRefOid        string `json:"headRefOid"`
	BaseRefName       string `json:"baseRefName"`
	IsCrossRepository bool   `json:"isCrossRepository"`
}

// openPR finds or opens the draft PR of the pushed seat branch with the
// pinned gh (D2), as the harness, when o.OpenPR is set, and returns its
// number in the result (the binding pr_number/pr_head that 3-2's merge
// train reads). gh runs outside the seat's worktree with --repo from the
// launch's record of the grant. Provenance is the task binding in the store
// only (R7): an open PR on the branch is reused only when the store binds
// its number to this task, it is from this repository, onto the grant's
// base, and at the pushed head; any other open PR refuses with
// pr_bound_elsewhere; no PR body is ever read. A PR the step opens is bound
// to the task at once (BindPR), so a re-run after a crash finds it bound. A
// head still behind the push is a deferral (the forge has not caught up).
func openPR(o Options, sh *shadow, l Ledger, rec launch.Record, res Result) Result {
	if !o.OpenPR {
		return res
	}
	if l.Repository == "" {
		res.Notes = append(res.Notes, "no PR: the launch recorded no repository from the grant (one merge grant names it)")
		return res
	}
	if o.Gh == "" {
		if tool, why := pinnedGh(l); why != "" {
			res.Notes = append(res.Notes, "no PR: "+why)
			return res
		} else {
			tool.Close()
		}
	}
	// Every gh invocation re-verifies the pin and runs the hashed
	// descriptor (/dev/fd/3 on Linux) or the verified path (darwin).
	run := func(args ...string) (string, error) {
		args = append(args, "--repo", l.Repository)
		var cmd *exec.Cmd
		if o.Gh != "" {
			cmd = exec.Command(o.Gh, args...) // a test seam
		} else {
			tool, why := pinnedGh(l)
			if why != "" {
				return why, errors.New(why)
			}
			defer tool.Close()
			cmd = tool.Command(args...)
		}
		cmd.Dir = sh.dir
		cmd.Env = append(ghEnv(o.Env), "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1")
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	bound := func(n int) string {
		if o.PRBoundTo == nil {
			return ""
		}
		return o.PRBoundTo(n)
	}
	base := l.base(o.Grants)
	out, err := run("pr", "list", "--head", rec.Branch, "--state", "open", "--json", "number,state,headRefOid,baseRefName,isCrossRepository")
	if err != nil {
		res.Notes = append(res.Notes, "gh pr list failed: "+firstLine(out))
		return res
	}
	var prs []ghPR
	if err := json.Unmarshal([]byte(out), &prs); err != nil {
		res.Notes = append(res.Notes, "gh pr list gave no PR list: "+firstLine(out))
		return res
	}
	for _, p := range prs {
		why := ""
		switch b := bound(p.Number); {
		case !strings.EqualFold(p.State, "OPEN"):
			continue
		case b == "":
			why = "is bound to no task in the store"
		case b != o.Task.ID:
			why = "is bound to task " + b + " in the store"
		case p.IsCrossRepository:
			why = "is from another repository"
		case p.BaseRefName != base:
			why = "targets " + p.BaseRefName + ", not the grant's base " + base
		}
		if why != "" {
			return res.refuse(ReasonPRBoundElsewhere, task.RouteLead, fmt.Sprintf("the open PR #%d on %s %s; the commit step never reuses it (pushed %s, no PR bound)", p.Number, rec.Branch, why, res.Commit))
		}
		if p.HeadRefOid != res.Commit {
			return res.defer_(fmt.Sprintf("the open PR #%d is at %s, not yet the pushed head %s", p.Number, p.HeadRefOid, res.Commit))
		}
		res.PRNumber = p.Number
		return res
	}
	out, err = run("pr", "create", "--draft", "--head", rec.Branch, "--base", base, "--title", o.Task.ID+": "+strings.Join(strings.Fields(o.Task.Title), " "),
		"--body", Message(o.Task, o.Handle, res.DeclaredDigest, res.DiffDigest))
	if err != nil {
		res.Notes = append(res.Notes, "gh pr create failed: "+firstLine(out))
		return res
	}
	if i := strings.LastIndex(out, "/pull/"); i >= 0 {
		out = strings.Fields(out[i+len("/pull/"):] + " ")[0]
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil || n <= 0 {
		res.Notes = append(res.Notes, "gh gave no PR number: "+firstLine(out))
		return res
	}
	if t := bound(n); t != "" && t != o.Task.ID {
		return res.refuse(ReasonPRBoundElsewhere, task.RouteLead, fmt.Sprintf("gh answered PR #%d, which the store binds to task %s", n, t))
	}
	if o.BindPR != nil {
		if err := o.BindPR(n, res.Commit); err != nil {
			return res.refuse(ReasonPRBoundElsewhere, task.RouteLead, fmt.Sprintf("the new PR #%d could not be bound to %s: %v", n, o.Task.ID, err))
		}
	}
	res.PRNumber = n
	return res
}

// ghEnv is gh's environment: the harness strip (no credential, no GIT_*),
// and no GH_* or GITHUB_* variable either (D2: gh authenticates from the
// owner's own gh login under HOME; the token variables are kept on the push
// call only).
func ghEnv(parent []string) []string {
	var out []string
	for _, kv := range redact.StripEnv(parent) {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "GIT_") || strings.HasPrefix(name, "GH_") || strings.HasPrefix(name, "GITHUB_") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, hardenedVars...)
}

// Message is the harness commit message: the task id and title, then
// trailers naming the seat and the digests of the declared set and the diff.
func Message(t task.Task, seat, declaredDigest, diffDigest string) string {
	title := strings.Join(strings.Fields(t.Title), " ")
	return fmt.Sprintf("%s: %s\n\nCommitted by the amq-squad harness for seat %s: the seat changed the tree, the Go core checked it against the task's declared files and the grant, then committed (plan 4.3, amendment 21).\n\nAmq-Squad-Task: %s\nAmq-Squad-Seat: %s\nAmq-Squad-Declared-Files: %s\nAmq-Squad-Diff: %s\n",
		t.ID, title, seat, t.ID, seat, declaredDigest, diffDigest)
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func firstLine(s string) string {
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		if l := strings.TrimSpace(line); l != "" && !strings.HasPrefix(l, "To ") {
			return l
		}
	}
	return strings.TrimSpace(s)
}
