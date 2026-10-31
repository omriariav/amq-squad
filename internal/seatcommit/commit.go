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
	"context"
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
	"github.com/omriariav/amq-squad/v3/internal/sealchain"
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
	// PRBoundTo names the task the store binds a repository's PR number to
	// ("" when none): the only provenance of a PR (R7).
	PRBoundTo func(repository string, number int) string
	// BindPR binds a PR the step opened to the task at once (the store's
	// BindPR, 3-2's binding), so a re-run finds it bound.
	BindPR func(repository string, number int, head string) error
	// Clear verifies the owner's signed clear of a quarantine (the owner's
	// trust store); nil verifies nothing, so no clear counts.
	Clear ClearVerifier
	// Ctx cancels the step (the task was cancelled while the step ran, P2-1
	// of the second pass): a running verifier's group is killed, no verdict
	// is recorded and nothing is pushed. nil never cancels.
	Ctx context.Context
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
	// PRRepository, PRNumber and PRHead are the task binding the commit
	// step writes (3-2's pr_repository, pr_number, pr_head: the grant's
	// repository, the PR it opened, 0 when none, and the head it pushed).
	PRRepository string `json:"pr_repository,omitempty"`
	PRNumber     int    `json:"pr_number,omitempty"`
	PRHead       string `json:"pr_head,omitempty"`
	// Verifier is the class verifier's run on the snapshot tree
	// (v3-5-verifier), nil when the class has none configured.
	Verifier *VerifierOutcome `json:"verifier,omitempty"`
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

// ReasonStepCancelled is the deferral of a step whose task was cancelled
// while it ran (its result is discarded).
const ReasonStepCancelled = "commit_cancelled"

func (o Options) cancelled() bool { return o.Ctx != nil && o.Ctx.Err() != nil }

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
	if code != "" && clearedOnThisLaunch(o, code) {
		code, changes = "", nil
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
	// The exit check runs on the scheduler's tick: it never waits for the
	// ledger a running commit step holds, it defers (P1-1 of the second pass).
	err := withLedgerTryLock(o.Root, o.Handle, func() error {
		if terr := verifyEvidence(o); errors.Is(terr, sealchain.ErrTampered) {
			out = res.tamperedRefusal(terr)
			return nil
		}
		if l, lerr := ReadLedger(o.Root, o.Handle); lerr == nil && l.Quarantine != nil && l.Quarantine.ClearedAt != nil && !l.Quarantine.Clear.Verified(o.Handle, l.Quarantine, o.Clear) {
			res.Quarantine = true
			out = res.refuse(ReasonQuarantineUnverified, task.RouteOwner, unverifiedDetail(o.Handle))
			return nil
		}
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
		if terr := verifyEvidence(o); errors.Is(terr, sealchain.ErrTampered) {
			return res.tamperedRefusal(terr)
		}
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
	if errors.Is(err, sealchain.ErrTampered) {
		// The ledger's tag or chain failed: a block for the owner, never a
		// pass and never a retry into the same file (ruling).
		return res.tamperedRefusal(err)
	}
	if err != nil {
		return res.defer_(err.Error())
	}
	if ledger.Seeded && ledger.Base == "" {
		return res.refuse(ReasonBaseUnrecorded, task.RouteLead, "no core-recorded base for seat "+o.Handle+" (its ledger entry is the seed of a clear); the launch records it (seat commit --base SHA records one as a lead decision)")
	}
	if ledger.Worktree != rec.Worktree || ledger.Branch != rec.Branch {
		return res.refuse(ReasonWorktree, task.RouteLead, fmt.Sprintf("the commit ledger records %s on %s, the launch record %s on %s", ledger.Worktree, ledger.Branch, rec.Worktree, rec.Branch))
	}
	// One branch per task (3.0.2 t23): the task's own branch and the commit
	// its worktree started on, never the seat's previous task.
	branch, parent, base, why := ledger.taskStart(o.Handle, o.Task.ID)
	if why != "" {
		return res.refuse(ReasonWorktree, task.RouteLead, why)
	}
	res.Branch = branch
	env := harnessEnv(o.Env)
	rp, repoRoot, why := checkWorktree(rec, o.Task.ProjectPath, rec.Branch, branch)
	if why != "" {
		return res.refuse(ReasonWorktree, task.RouteLead, why)
	}
	sh, err := newShadow(o.Git, env, rp)
	if err != nil {
		return res.defer_("the harness git dir: " + err.Error())
	}
	defer sh.close()
	g := sh.g
	res.Parent = parent
	tree, err := snapshot(sh, parent)
	if err != nil {
		return res.defer_("snapshot of the worktree: " + err.Error())
	}
	// A re-run after the step returned the worktree to rest (3.0.2 t23): the
	// worktree is the resting commit and the task's commit is on its branch;
	// the checks, the verifier and the push run on that commit's diff.
	if i := ledger.taskCommitAt(o.Task.ID, branch); i >= 0 && ledger.Lease == nil {
		if pt, err := g.line("rev-parse", parent+"^{tree}"); err == nil && pt == tree {
			parent, tree = ledger.Commits[i].Parent, ledger.Commits[i].Tree
			res.Parent = parent
		}
	}
	res.Tree = tree
	// lift is a verified clear the step lifts: written to the ledger only once
	// the binding check passed, so a refused or deferred step leaves the
	// ledger unchanged (Codex pass on 2477f10f, finding 3).
	var lift *LedgerQuarantine
	if q := ledger.Quarantine; q != nil {
		switch {
		case q.ClearedAt == nil && o.DryRun:
			// a dry run of a quarantined seat has nothing to verify
		case q.ClearedAt == nil:
			res.Quarantine = true
			return res.refuse(firstNonEmpty(q.Reason, ReasonGlobalConfig), task.RouteOwner, fmt.Sprintf("seat %s is quarantined since %s (task %s); nothing of its work is committed until the owner clears it (amq-squad seat clear --seat %s)", o.Handle, q.At.Format(time.RFC3339), q.Task, o.Handle))
		case !q.Clear.Verified(o.Handle, q, o.Clear):
			// every mode of the commit step, a dry run included, verifies
			// the clear and refuses on any failure, an unreadable gate store too
			res.Quarantine = true
			return res.refuse(ReasonQuarantineUnverified, task.RouteOwner, unverifiedDetail(o.Handle))
		case o.DryRun:
		case q.Task == o.Task.ID && q.Tree != "" && q.Tree != tree:
			return res.refuse(ReasonQuarantineTree, task.RouteOwner, fmt.Sprintf("the seat's tree changed after the quarantine: the owner cleared %s, the worktree is now %s", q.Tree, tree))
		}
		if !o.DryRun && q.ClearedAt != nil {
			lift = q
		}
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
	// The declared set's digest is over its canonical JSON (an array of
	// strings), so ["a","b"] and ["a\nb"] never share one (P2-7).
	res.DeclaredDigest = DeclaredDigest(declared)
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
	// 3.0.2 t24: the binding check runs before the commit and the push. A
	// task branch that carries an open PR this task does not hold refuses
	// here, with nothing committed or pushed.
	if r, stop := prCheck(o, sh, ledger, res); stop {
		return r
	}
	if lift != nil {
		ledger.Cleared = append(ledger.Cleared, *lift)
		ledger.Quarantine = nil
		if err := writeLedger(o.Root, ledger); err != nil {
			return res.defer_("commit ledger: " + err.Error())
		}
		res.Notes = append(res.Notes, "the owner's signed clear lifted the quarantine of task "+lift.Task)
	}
	// v3-5-verifier rule 7: the class's verifier on the snapshot tree, the
	// owner-config diff again, the verdict; only a pass reaches the push.
	res, done := verify(o, sh, res)
	if done {
		return res
	}
	if o.cancelled() {
		return res.defer_(ReasonStepCancelled + ": the task was cancelled while the step ran; nothing is pushed")
	}
	out := commitAndPush(o, sh, ledger, base, res)
	if out.OK {
		out = settle(o, sh, rec, out)
	}
	return out
}

// settle returns the worktree to a clean base after the done report is
// committed (3.0.2 t23): the base branch's tip, on the seat's resting
// branch, recorded as the ledger's Rest, the lease's start cleared. A
// failure is a note: the work is committed, and the next lease boundary's
// CleanSeat returns the worktree.
func settle(o Options, sh *shadow, rec launch.Record, res Result) Result {
	l, err := ReadLedger(o.Root, o.Handle)
	if err != nil {
		res.Notes = append(res.Notes, "the worktree stays on the task's tree until the next lease boundary: "+err.Error())
		return res
	}
	repo := seatRepo(o.Git, harnessEnv(o.Env), sh.rp)
	rest, note := restTarget(rec, l)
	if note != "" {
		res.Notes = append(res.Notes, note)
	}
	if err := toRest(o.Git, harnessEnv(o.Env), sh, repo, rec.Branch, res.Tree, rest); err != nil {
		res.Notes = append(res.Notes, "the worktree stays on the task's tree until the next lease boundary: "+err.Error())
		return res
	}
	l.Rest, l.Lease = rest, nil
	if err := writeLedger(o.Root, l); err != nil {
		res.Notes = append(res.Notes, "commit ledger: "+err.Error())
	}
	return res
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
	if rec, err := stagedRecord(o.Root, o.Handle); err == nil {
		q.Launch = rec.LaunchID
	}
	l, lerr := ReadLedger(o.Root, o.Handle)
	if lerr == nil && l.Quarantine != nil && l.Quarantine.ClearedAt == nil {
		return res.refuse(code, task.RouteOwner, detail) // the first evidence stays
	}
	if rec, err := stagedRecord(o.Root, o.Handle); err != nil {
		q.Snapshot = err.Error()
	} else if lerr != nil {
		q.Snapshot = "no commit ledger: " + lerr.Error()
	} else if l.Seeded && l.Base == "" {
		q.Snapshot = "seed entry: no base recorded yet, no diff yet"
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
			if lerr == nil {
				repo := seatRepo(o.Git, harnessEnv(o.Env), rp)
				expireEvidence(repo, &l, o.Now())
				if ref, err := anchorEvidence(repo, &l, o.Task.ID, tree, o.Now()); err != nil {
					detail += "; the kept tree could not be anchored: " + err.Error()
				} else {
					q.EvidenceRef = ref
				}
			}
		}
	}
	if lerr == nil {
		if l.Quarantine != nil && l.Quarantine.Clear != nil {
			l.Cleared = append(l.Cleared, *l.Quarantine)
		}
		l.Quarantine = &q
		if err := writeLedger(o.Root, l); err != nil {
			detail += "; the evidence could not be written: " + err.Error()
		}
	} else {
		detail += "; no tree kept: " + q.Snapshot
	}
	return res.refuse(code, task.RouteOwner, detail)
}

// CleanSeat returns a seat's worktree to a clean base before the seat gets
// a new lease (2b-2 milestone B, the R19 live finding: after a refused task
// was reset, the seat's next lease inherited the refused diff and every
// later commit of that seat was refused for it, a loop). A diff left on the
// worktree against the ledger parent that the ledger did not commit is the
// leftover of an attempt that did not commit (refused and reset, failed,
// expired): it is snapshotted and recorded on the ledger as evidence (tree
// and diff digest). Then the worktree is checked out at the base branch's
// tip (3.0.2 t23: every task starts from the base, never from the seat's
// previous task) in the harness's shadow (no filter or hook of the seat's
// repository runs), untracked, non-ignored files are removed, HEAD returns
// from the last task's branch to the seat's resting branch and that branch
// is moved to the tip, recorded as the ledger's Rest. It does nothing for a
// clean worktree already resting on the tip. Seat commits on the branch are
// dropped too: the ledger is the core's record, never the seat's history.
// The whole reset runs under the task store's lock with the seat free (R3):
// no claim can land between the check and the reset.
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
	// CleanSeat runs on the tick (the seat's preparation before a lease): a
	// busy ledger is ErrLedgerBusy, never a wait.
	err = withLedgerTryLock(root, handle, func() error {
		// Only between leases, never mid-task (condition 2 of the ruling):
		// the store lock covers the state read with the seat's unclean marker
		// and, in a second take, the marker's removal with the seat
		// re-checked; the git work runs between them, outside the lock (no
		// lock reachable from the tick is held across a subprocess). While
		// the marker exists no grant or claim leases the seat; only a
		// completed clean removes it, and only its own (the nonce): a clean
		// that fails or dies leaves the seat unclean for the next CleanSeat.
		list, nonce, err := store.ReserveSeat(handle, now)
		if err != nil {
			return err
		}
		var derr error
		discarded, derr = cleanSeat(root, handle, gitBin, env, clear, list, now)
		if derr != nil {
			return derr
		}
		return store.ReleaseSeat(handle, nonce)
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
	// The worktree is on the resting branch, or on the task branch of the
	// lease the ledger records: the lease record decides, never the name.
	rp, err := worktreePaths(rec.Worktree)
	if err != nil {
		return false, fmt.Errorf("the seat worktree %s is not a git checkout root: %v", rec.Worktree, err)
	}
	onTask := rp.HeadRef != "refs/heads/"+rec.Branch
	if onTask && !l.knownBranch(rp.HeadRef) {
		return false, fmt.Errorf("the seat worktree has %q checked out, not the seat branch %s or the task branch of the lease its commit ledger records", rp.HeadRef, rec.Branch)
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
	// The next lease starts at the base (3.0.2 t23, amendment 2b): the tip
	// the launch recorded on the launch record, never the ledger's first base.
	rest, _ := restTarget(rec, l)
	head, _ := repo.line("rev-parse", "--verify", "-q", "refs/heads/"+rec.Branch)
	if l.Lease == nil && !onTask && tree == parentTree && head == parent && parent == rest {
		return false, nil
	}
	// A diff the ledger committed (the lease's task) is not a discard; any
	// other diff is the leftover of an attempt that did not commit.
	discarded := tree != parentTree && l.recordedAt(parent, tree) < 0
	if discarded {
		raw, _ := sh.g.run(nil, nil, "diff-tree", "-r", "-z", "--no-renames", "--no-abbrev", "--no-ext-diff", "--no-textconv", "--raw", parent, tree)
		taskID, last := lastAttempt(list, handle)
		expireEvidence(repo, &l, now)
		ref, err := anchorEvidence(repo, &l, taskID, tree, now)
		if err != nil {
			return false, fmt.Errorf("anchor the discarded tree: %w", err)
		}
		l.Discarded = append(l.Discarded, LedgerDiscard{TaskID: taskID, Reason: last, Parent: parent, Tree: tree, DiffDigest: digest(raw), At: now.UTC(), EvidenceRef: ref})
		if err := writeLedger(root, l); err != nil {
			return false, err
		}
	}
	if beforeReset != nil {
		beforeReset()
	}
	if err := toRest(gitBin, henv, sh, repo, rec.Branch, tree, rest); err != nil {
		return false, err
	}
	if l.Rest != rest || l.Lease != nil {
		l.Rest, l.Lease = rest, nil
		if err := writeLedger(root, l); err != nil {
			return false, err
		}
	}
	return discarded, nil
}

// toRest checks the worktree out at the resting commit: an index of the
// worktree as it is (tree, its snapshot), reset to rest with the files
// updated in the harness's shadow, then the untracked, non-ignored files
// removed and the seat's own index at rest. Then HEAD returns from the task
// branch (the caller checked the ledger records it) to the seat's resting
// branch, which moves to rest. The caller records Rest and clears the
// lease's start.
func toRest(gitBin string, henv []string, sh *shadow, repo git, restBranch, tree, rest string) error {
	idx := sh.tempIndex()
	os.Remove(idx)
	defer os.Remove(idx)
	ig := sh.index(idx)
	if _, err := ig.run(nil, nil, "read-tree", tree); err != nil {
		return err
	}
	if _, err := ig.run(nil, nil, "read-tree", "-u", "--reset", rest); err != nil {
		return err
	}
	if _, err := ig.run(nil, nil, "clean", "-fdq"); err != nil {
		return err
	}
	if _, err := sh.index(sh.rp.Index).run(nil, nil, "read-tree", rest); err != nil {
		return err
	}
	if _, err := repo.run(nil, nil, "update-ref", "-m", "amq-squad rest seat", "refs/heads/"+restBranch, rest); err != nil {
		return err
	}
	if sh.rp.HeadRef != "refs/heads/"+restBranch {
		return setHead(gitBin, henv, sh.rp, restBranch)
	}
	return nil
}

// restTarget is the commit a seat's worktree rests on between leases
// (amendment 2b, one base): the base tip the launch fetched and recorded on
// the launch record (launch.Record.Base, 3.0.2 t22); each grant then cuts
// the task's branch from a fresh fetch (StartTask's base). A record without
// one (a launch before 3.0.2) keeps the ledger's resting commit, with the
// note saying so; never a local branch of the shared repository, which can
// be ahead of or behind the remote.
func restTarget(rec launch.Record, l Ledger) (rest, note string) {
	if b := strings.TrimSpace(rec.Base); b != "" {
		return b, ""
	}
	rest = l.rest()
	return rest, "launch_base_missing: the seat's launch record carries no base (a launch before 3.0.2); the worktree rests on the ledger's resting commit " + rest + ", never a local branch; the next launch records the base"
}

// setHead points the worktree's HEAD at a branch (git's symbolic-ref in the
// worktree's own git dir, with the harness's hardened git).
func setHead(gitBin string, env []string, rp repoPaths, branch string) error {
	g := git{bin: gitBin, dir: rp.GitDir, env: env}.with("GIT_DIR=" + rp.GitDir)
	_, err := g.run(nil, nil, "symbolic-ref", "HEAD", "refs/heads/"+branch)
	return err
}

// StartTask puts a seat's worktree on its task's own branch when the lease
// is granted (3.0.2 t23: the scheduler's doorbell runs it right after the
// grant, before the seat is rung): seat/<handle>-<task id> cut from base,
// the remote base tip the doorbell fetched at this grant, recorded as the
// task's base on the ledger (base "" keeps the resting commit); or, for a
// task with an earlier commit on its branch (a later attempt), at that
// commit with the branch's own base, so the attempt's commit follows the
// branch and its open PR. The lease's start is recorded on the ledger
// first; the commit step diffs against it. A seat with no commit ledger is
// left as it is (its commit step refuses base_unrecorded).
func StartTask(root, handle, gitBin string, env []string, t task.Task, base string, now time.Time) error {
	if gitBin == "" {
		var err error
		if gitBin, err = exec.LookPath("git"); err != nil {
			return err
		}
	}
	if env == nil {
		env = os.Environ()
	}
	return withLedgerTryLock(root, handle, func() error {
		l, err := ReadLedger(root, handle)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if l.Lease != nil && l.Lease.Task != t.ID {
			return fmt.Errorf("seat %s still carries task %s's lease start; a lease boundary (CleanSeat) clears it", handle, l.Lease.Task)
		}
		rec, err := stagedRecord(root, handle)
		if err != nil {
			return err
		}
		branch := TaskBranch(handle, t.ID)
		rp, _, why := checkWorktree(rec, "", rec.Branch, branch)
		if why != "" {
			return fmt.Errorf("%s", why)
		}
		henv := harnessEnv(env)
		repo := seatRepo(gitBin, henv, rp)
		rest := l.rest()
		start := firstNonEmpty(strings.TrimSpace(base), rest)
		base = start
		if i := l.taskCommitAt(t.ID, branch); i >= 0 {
			c := l.Commits[i]
			start, base = c.Commit, firstNonEmpty(c.Base, c.Parent)
		}
		// The lease's start is recorded first, so the branch the worktree is
		// moved to below is always one the ledger records (CleanSeat trusts
		// the record, never the name).
		if l.Lease == nil {
			l.Lease = &LedgerLease{Task: t.ID, Branch: branch, Base: base, Start: start, At: now.UTC()}
			if err := writeLedger(root, l); err != nil {
				return err
			}
		}
		start = l.Lease.Start
		if start != rest {
			if _, err := repo.line("rev-parse", "--verify", "-q", start+"^{commit}"); err != nil {
				return fmt.Errorf("task %s's earlier commit %s is not in the seat repository: %v", t.ID, start, err)
			}
			sh, err := newShadow(gitBin, henv, rp)
			if err != nil {
				return err
			}
			defer sh.close()
			idx := sh.tempIndex()
			os.Remove(idx)
			defer os.Remove(idx)
			ig := sh.index(idx)
			if _, err := ig.run(nil, nil, "read-tree", rest); err != nil {
				return err
			}
			if _, err := ig.run(nil, nil, "read-tree", "-u", "--reset", start); err != nil {
				return err
			}
			if _, err := sh.index(rp.Index).run(nil, nil, "read-tree", start); err != nil {
				return err
			}
		}
		if _, err := repo.run(nil, nil, "update-ref", "-m", "amq-squad start task "+t.ID, "refs/heads/"+branch, start); err != nil {
			return err
		}
		return setHead(gitBin, henv, rp, branch)
	})
}

// EvidenceRetention is how long a kept tree's evidence ref stays after it is
// recorded (a quarantine's stays while it is not cleared).
const EvidenceRetention = 30 * 24 * time.Hour

// evidenceRefName is refs/amq-squad/evidence/<task>/<n>; a task id
// character a ref name cannot carry becomes "_".
func evidenceRefName(seat, taskID string, n int) string {
	// The seat is part of the name (the fix round's item 22): two seats on
	// one repository never share a ref, whatever their counters say.
	return "refs/amq-squad/evidence/" + refPart(seat) + "/" + refPart(taskID) + "/" + strconv.Itoa(n)
}

// commitRefName is refs/amq-squad/commits/<seat>/<task>: the anchor of a
// task's last harness commit in the seat repository (3.0.2 t23).
func commitRefName(seat, taskID string) string {
	return "refs/amq-squad/commits/" + refPart(seat) + "/" + refPart(taskID)
}

// refPart is a ref name component: a character a ref name cannot carry
// becomes "_".
func refPart(v string) string {
	v = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, v)
	if v == "" {
		return "none"
	}
	return v
}

// anchorEvidence points a new evidence ref at a kept tree in the seat
// repository (the object store the shadow wrote it to), so git gc keeps it
// until the retention expiry (P2-5).
func anchorEvidence(repo git, l *Ledger, taskID, tree string, now time.Time) (EvidenceRef, error) {
	l.EvidenceSeq++
	ref := evidenceRefName(l.Handle, taskID, l.EvidenceSeq)
	if _, err := repo.run(nil, nil, "update-ref", "-m", "amq-squad evidence "+taskID, ref, tree); err != nil {
		return EvidenceRef{}, err
	}
	until := now.UTC().Add(EvidenceRetention)
	return EvidenceRef{Ref: ref, RefTree: tree, RetainUntil: &until}, nil
}

// verifyEvidence checks every evidence ref the sealed ledger records: it
// must still resolve to the tree recorded with it. A ref that is gone or
// points elsewhere (a same-UID seat can edit its repository's refs) is
// ledger_tampered (the fix round's item 22).
func verifyEvidence(o Options) error {
	if o.Git == "" {
		return nil // no git binary: the step defers on its own
	}
	l, err := ReadLedger(o.Root, o.Handle)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var refs []EvidenceRef
	for _, d := range l.Discarded {
		refs = append(refs, d.EvidenceRef)
	}
	if q := l.Quarantine; q != nil {
		refs = append(refs, q.EvidenceRef)
	}
	var live []EvidenceRef
	for _, e := range refs {
		if e.Ref != "" {
			live = append(live, e)
		}
	}
	if len(live) == 0 {
		return nil
	}
	rec, err := stagedRecord(o.Root, o.Handle)
	if err != nil {
		return err
	}
	rp, err := worktreePaths(rec.Worktree)
	if err != nil {
		return err
	}
	repo := seatRepo(o.Git, harnessEnv(o.Env), rp)
	for _, e := range live {
		got, err := repo.line("rev-parse", "--verify", "-q", e.Ref+"^{tree}")
		if err != nil || got != e.RefTree {
			return fmt.Errorf("%w: the evidence ref %s no longer resolves to the kept tree %s (now %q)", sealchain.ErrTampered, e.Ref, e.RefTree, got)
		}
	}
	return nil
}

// tamperedRefusal is the owner's block for a sealed ledger or an evidence
// ref that fails its check.
func (r Result) tamperedRefusal(err error) Result {
	return r.refuse(ReasonLedgerTampered, task.RouteOwner, err.Error()+"; nothing is committed or pushed until the owner inspects the commits ledger and the evidence refs")
}

// expireEvidence deletes the evidence refs whose retention has expired (the
// ledger keeps the record, without the ref); an uncleared quarantine's ref
// never expires.
func expireEvidence(repo git, l *Ledger, now time.Time) {
	drop := func(e *EvidenceRef) {
		if e.Ref == "" || e.RetainUntil == nil || now.Before(*e.RetainUntil) {
			return
		}
		if _, err := repo.run(nil, nil, "update-ref", "-d", e.Ref); err == nil {
			e.Ref = ""
		}
	}
	for i := range l.Discarded {
		drop(&l.Discarded[i].EvidenceRef)
	}
	if q := l.Quarantine; q != nil && q.ClearedAt != nil {
		drop(&q.EvidenceRef)
	}
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
// config), that the worktree is a checkout root with the branch the ledger
// expects checked out (the seat's resting branch, or the task's branch of
// the lease), and of the task's repository when one is named. It returns
// the worktree's git paths and the repository root (the task's project
// path, else the worktree).
func checkWorktree(rec launch.Record, project string, branches ...string) (repoPaths, string, string) {
	rp, err := worktreePaths(rec.Worktree)
	if err != nil {
		return rp, "", "the seat worktree " + rec.Worktree + " is not a git checkout root: " + err.Error()
	}
	ok := false
	for _, b := range branches {
		ok = ok || rp.HeadRef == "refs/heads/"+b
	}
	if !ok {
		return rp, "", fmt.Sprintf("the seat worktree has %q checked out, not the branch %s", rp.HeadRef, strings.Join(branches, " or "))
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
		entry, hit := ps.match(p)
		name := ps.managedName(p)
		if (hit || name != "") && !regularInSnapshot(e) {
			// A protected or managed path must stay a regular file: a symlink
			// (whose target text could match the block while the effective
			// file is a broken link), a submodule entry or a mode change there
			// is protected_path, gated or not (P1-1).
			protected = append(protected, p+" (not a regular file in the snapshot: mode "+e.OldMode+" -> "+e.NewMode+")")
			if name != "" {
				managed = append(managed, p)
			}
		} else if hit && !matchFold(gated, p) {
			protected = append(protected, p+" ("+entry+")")
		} else if name != "" && !matchFold(gated, p) && managedChanged(g, parent, tree, p) {
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

// regularInSnapshot reports whether a changed entry stays a regular file:
// its new mode is a regular file's (100644 or 100755) and, when it existed
// before, its mode did not change; a deletion (000000) is left to the
// content checks.
func regularInSnapshot(e rawEntry) bool {
	regular := func(m string) bool { return m == "100644" || m == "100755" }
	switch {
	case e.NewMode == "000000":
		return e.OldMode == "000000" || regular(e.OldMode)
	case !regular(e.NewMode):
		return false
	case e.OldMode != "000000" && e.OldMode != e.NewMode:
		return false
	}
	return true
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

// commitAndPush writes the harness commit (parent: the commit the task's
// worktree started on; the seat's own commits, if any, are not in its
// history), records it in the ledger with the task's branch, anchors it in
// the seat repository, moves the task branch to it when the worktree is on
// it, and pushes it to the task's branch as a fast-forward to the URL the
// launch recorded from the grant. A re-run after a crash finds the recorded
// commit and only pushes.
func commitAndPush(o Options, sh *shadow, l Ledger, base string, res Result) Result {
	g := sh.g
	parentTree, err := g.line("rev-parse", res.Parent+"^{tree}")
	if err != nil {
		return res.defer_(err.Error())
	}
	last, earlier := l.recordedAt(res.Parent, res.Tree), l.taskCommitAt(o.Task.ID, res.Branch)
	switch {
	case last >= 0 && l.Commits[last].Task == o.Task.ID && l.Commits[last].Branch == res.Branch:
		res.Commit = l.Commits[last].Commit // this task's commit exists; finish its push
		res.Notes = append(res.Notes, "the harness commit "+res.Commit+" was already recorded")
	case earlier >= 0 && l.Commits[earlier].Commit == res.Parent && res.Tree == parentTree:
		// A later attempt with no edits on the task's recorded commit
		// (StartTask put the worktree there): nothing new to commit, but
		// "nothing to commit" is OK only once that commit is pushed and bound;
		// it goes on to the push and the PR as if it had just been made
		// (Codex pass on 2477f10f, finding 1).
		last = earlier
		c := l.Commits[last]
		res.Commit, res.Parent, res.Tree = c.Commit, c.Parent, c.Tree
		res.DeclaredDigest, res.DiffDigest, res.Lines = c.DeclaredDigest, c.DiffDigest, c.Lines
		res.Changes, res.Files = append([]Change{}, c.Changes...), len(c.Changes)
		res.Notes = append(res.Notes, "no new edits on the task's recorded commit "+c.Commit+": it is pushed and bound")
	case earlier >= 0 && l.Commits[earlier].Commit != res.Parent && res.Tree != parentTree:
		// A later attempt appends to the task's branch only from its last
		// commit (StartTask puts the worktree there); a diff from anywhere
		// else would not follow the branch or its PR.
		return res.refuse(ReasonWorktree, task.RouteLead, fmt.Sprintf("task %s's branch %s carries its earlier commit %s, and the lease did not start the worktree there (the seat worked from %s); nothing is committed or pushed",
			o.Task.ID, res.Branch, l.Commits[earlier].Commit, res.Parent))
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
		l.Commits = append(l.Commits, LedgerCommit{Task: o.Task.ID, Commit: res.Commit, Parent: res.Parent, Tree: res.Tree, At: o.Now().UTC(),
			DeclaredDigest: res.DeclaredDigest, DiffDigest: res.DiffDigest, Lines: res.Lines, Changes: res.Changes, Branch: res.Branch, Base: base})
		last = len(l.Commits) - 1
		if err := writeLedger(o.Root, l); err != nil {
			return res.defer_("commit ledger: " + err.Error())
		}
	}
	repo := seatRepo(o.Git, harnessEnv(o.Env), sh.rp)
	// The anchor keeps the commit in the seat repository after the task
	// branch's local ref goes at the next lease boundary (a later attempt
	// starts from it).
	if _, err := repo.run(nil, nil, "update-ref", "-m", "amq-squad seat commit "+o.Task.ID, commitRefName(o.Handle, o.Task.ID), res.Commit); err != nil {
		return res.defer_(err.Error())
	}
	if sh.rp.HeadRef == "refs/heads/"+res.Branch {
		if _, err := repo.run(nil, nil, "update-ref", "-m", "amq-squad seat commit "+o.Task.ID, "refs/heads/"+res.Branch, res.Commit); err != nil {
			return res.defer_(err.Error())
		}
		// The seat's index follows the new branch tip; the files are untouched.
		if _, err := sh.index(sh.rp.Index).run(nil, nil, "read-tree", res.Commit); err != nil {
			return res.defer_(err.Error())
		}
	}
	if l.Commits[last].Pushed {
		res.Pushed, res.OK, res.PRHead = true, true, res.Commit
		return openPR(o, sh, l, res)
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
	out, err := g.run(nil, pushEnv, append(pushArgs, "push", "--porcelain", "--no-verify", l.PushURL, res.Commit+":refs/heads/"+res.Branch)...)
	if err != nil {
		text := string(out) + err.Error()
		if strings.Contains(text, "[rejected]") || strings.Contains(text, "non-fast-forward") || strings.Contains(text, "fetch first") {
			return res.refuse(ReasonPushRejected, task.RouteLead, "the remote refused a fast-forward push of "+res.Branch+" (no force push): "+firstLine(text))
		}
		return res.defer_("push: " + firstLine(text))
	}
	l.Commits[last].Pushed = true
	if err := writeLedger(o.Root, l); err != nil {
		return res.defer_("commit ledger: " + err.Error())
	}
	res.Pushed, res.OK, res.PRHead = true, true, res.Commit
	return openPR(o, sh, l, res)
}

// ReasonPRPending defers a pushed step whose PR binding is not made yet
// (--open-pr): the task completes only with its binding.
const ReasonPRPending = "pr_pending"

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

// ghRunner is the commit step's gh: every invocation re-verifies the pin
// and runs the hashed descriptor (/dev/fd/3 on Linux) or the verified path
// (darwin), outside the seat's worktree, with --repo from the launch's
// record of the grant. why is set when the pin does not verify.
func ghRunner(o Options, l Ledger, dir string) (run func(args ...string) (string, error), why string) {
	if o.Gh == "" {
		tool, why := pinnedGh(l)
		if why != "" {
			return nil, why
		}
		tool.Close()
	}
	return func(args ...string) (string, error) {
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
		cmd.Dir = dir
		cmd.Env = append(ghEnv(o.Env), "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1")
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}, ""
}

// branchPR reads the open PRs of the task's branch, every one of them: a
// push updates every open PR on its head, so one disqualifying PR refuses
// even beside this task's own. Provenance is the task binding in the store
// only (R7): an open PR is this task's only when the store binds its number
// to this task, it is from this repository and onto the grant's base; any
// other open PR is named in why (the commit step never reuses it); no PR
// body is ever read. err is a gh failure.
func branchPR(o Options, run func(args ...string) (string, error), l Ledger, branch string) (pr *ghPR, why string, err error) {
	out, err := run("pr", "list", "--head", branch, "--state", "open", "--json", "number,state,headRefOid,baseRefName,isCrossRepository")
	if err != nil {
		return nil, "", errors.New("gh pr list failed: " + firstLine(out))
	}
	var prs []ghPR
	if err := json.Unmarshal([]byte(out), &prs); err != nil {
		return nil, "", errors.New("gh pr list gave no PR list: " + firstLine(out))
	}
	base := l.base(o.Grants)
	var mine *ghPR
	for i, p := range prs {
		b := ""
		if o.PRBoundTo != nil {
			b = o.PRBoundTo(l.Repository, p.Number)
		}
		switch {
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
			return &prs[i], fmt.Sprintf("the open PR #%d on %s %s", p.Number, branch, why), nil
		}
		mine = &prs[i]
	}
	return mine, "", nil
}

// prCheck is the binding check before the commit and the push (3.0.2 t24,
// the v31 finding: t20's commit was pushed onto seat/dev, which carried PR
// #815 bound to t12, and only then refused). With --open-pr, the task's
// branch is read through gh before anything is written: an open PR this
// task does not hold refuses pr_bound_elsewhere, and a gh failure defers;
// either way nothing is committed or pushed and the seat's diff waits.
func prCheck(o Options, sh *shadow, l Ledger, res Result) (Result, bool) {
	if !o.OpenPR || l.Repository == "" {
		return res, false // openPR names a missing repository after the push
	}
	pending := func(why string) (Result, bool) {
		r := res.defer_(ReasonPRPending + ": " + why + " (the binding check runs before the commit: nothing is committed or pushed; retried next tick)")
		r.ReasonCode = ReasonPRPending
		return r, true
	}
	run, why := ghRunner(o, l, sh.dir)
	if why != "" {
		return pending(why)
	}
	_, why, err := branchPR(o, run, l, res.Branch)
	if err != nil {
		return pending(err.Error())
	}
	if why != "" {
		return res.refuse(ReasonPRBoundElsewhere, task.RouteLead, why+"; the commit step never reuses it: nothing is committed or pushed"), true
	}
	return res, false
}

// openPR finds or opens the draft PR of the pushed task branch with the
// pinned gh (D2), as the harness, when o.OpenPR is set, and returns its
// number in the result (the binding pr_number/pr_head that 3-2's merge
// train reads). The open PR this task holds (branchPR) is reused and bound
// again at the pushed head (a later attempt appends to its branch and PR);
// any other open PR refuses with pr_bound_elsewhere. A PR the step opens is
// bound to the task at once (BindPR), so a re-run after a crash finds it
// bound. A head still behind the push is a deferral (the forge has not
// caught up).
func openPR(o Options, sh *shadow, l Ledger, res Result) Result {
	if !o.OpenPR {
		return res
	}
	// With --open-pr the completion waits for the binding (P2-3 of the phase
	// 5 re-pass): a pushed step whose PR could not be opened, found or read
	// is deferred as pr_pending (the push is recorded; the next tick retries
	// the PR only), never a success that completes the task unbound.
	pending := func(why string) Result {
		r := res.defer_(ReasonPRPending + ": " + why + " (pushed " + res.Commit + "; the PR is retried next tick)")
		r.ReasonCode = ReasonPRPending
		return r
	}
	if l.Repository == "" {
		return pending("the launch recorded no repository from the grant (one merge grant names it)")
	}
	run, why := ghRunner(o, l, sh.dir)
	if why != "" {
		return pending(why)
	}
	bind := func(n int) error {
		if o.BindPR == nil {
			return nil
		}
		return o.BindPR(l.Repository, n, res.Commit)
	}
	p, why, err := branchPR(o, run, l, res.Branch)
	switch {
	case err != nil:
		return pending(err.Error())
	case why != "":
		return res.refuse(ReasonPRBoundElsewhere, task.RouteLead, why+"; the commit step never reuses it (pushed "+res.Commit+", no PR bound)")
	case p != nil && p.HeadRefOid != res.Commit:
		return res.defer_(fmt.Sprintf("the open PR #%d is at %s, not yet the pushed head %s", p.Number, p.HeadRefOid, res.Commit))
	case p != nil:
		if err := bind(p.Number); err != nil {
			return res.refuse(ReasonPRBoundElsewhere, task.RouteLead, fmt.Sprintf("the open PR #%d could not be bound again to %s at %s: %v", p.Number, o.Task.ID, res.Commit, err))
		}
		res.PRRepository, res.PRNumber = l.Repository, p.Number
		return res
	}
	out, err := run("pr", "create", "--draft", "--head", res.Branch, "--base", l.base(o.Grants), "--title", o.Task.ID+": "+strings.Join(strings.Fields(o.Task.Title), " "),
		"--body", Message(o.Task, o.Handle, res.DeclaredDigest, res.DiffDigest))
	if err != nil {
		return pending("gh pr create failed: " + firstLine(out))
	}
	if i := strings.LastIndex(out, "/pull/"); i >= 0 {
		out = strings.Fields(out[i+len("/pull/"):] + " ")[0]
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil || n <= 0 {
		return pending("gh gave no PR number: " + firstLine(out))
	}
	if o.PRBoundTo != nil {
		if t := o.PRBoundTo(l.Repository, n); t != "" && t != o.Task.ID {
			return res.refuse(ReasonPRBoundElsewhere, task.RouteLead, fmt.Sprintf("gh answered PR #%d, which the store binds to task %s", n, t))
		}
	}
	if err := bind(n); err != nil {
		return res.refuse(ReasonPRBoundElsewhere, task.RouteLead, fmt.Sprintf("the new PR #%d could not be bound to %s: %v", n, o.Task.ID, err))
	}
	res.PRRepository, res.PRNumber = l.Repository, n
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

// DeclaredDigest is the digest of a declared file set: sha256 over its
// canonical JSON array (encoding/json, in the order given; the store keeps
// the set sorted and deduplicated).
func DeclaredDigest(declared []string) string {
	if declared == nil {
		declared = []string{}
	}
	raw, _ := json.Marshal(declared)
	return digest(raw)
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

func unverifiedDetail(handle string) string {
	return fmt.Sprintf("seat %s's quarantine carries a clear that is not the owner's verified record (the owner's signature, and for a Buzz-signed clear the owner's answer in the gate store); it stays quarantined", handle)
}

// clearedOnThisLaunch says whether the owner already cleared this condition
// on the seat's current launch (a verified ClearRecord, pending or lifted, that
// names this launch and this condition): the same silence or drift is not
// raised again, and a new lease is a new launch, so detection runs fresh.
func clearedOnThisLaunch(o Options, code string) bool {
	rec, err := stagedRecord(o.Root, o.Handle)
	if err != nil || rec.LaunchID == "" {
		return false
	}
	l, err := ReadLedger(o.Root, o.Handle)
	if err != nil {
		return false
	}
	qs := l.Cleared
	if l.Quarantine != nil {
		qs = append(append([]LedgerQuarantine{}, qs...), *l.Quarantine)
	}
	for i := range qs {
		q := &qs[i]
		if q.ClearedAt != nil && q.Clear != nil && q.Clear.Condition == code && q.Clear.Launch == rec.LaunchID && q.Clear.Verified(o.Handle, q, o.Clear) {
			return true
		}
	}
	return false
}
