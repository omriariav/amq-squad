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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/launch"
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
	Remote    string // default origin
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
	// Quarantine is set on a global_config_changed refusal: the caller
	// quarantines the seat (no new lease) and closes it.
	Quarantine bool `json:"quarantine,omitempty"`
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
// (task.Complete, task.RefuseCommit).
func Run(o Options) Result {
	res := Result{Task: o.Task.ID, Seat: o.Handle, Changes: []Change{}}
	// A dry run also takes a task blocked by a refusal (the widening check).
	awaiting := o.Task.State == task.StateInProgress || (o.DryRun && o.Task.State == task.StateBlocked)
	if o.Task.Report == nil || o.Task.Report.By != o.Handle || !awaiting {
		return res.refuse(ReasonWorktree, task.RouteLead, fmt.Sprintf("task %s has no done report by %s awaiting the commit step", o.Task.ID, o.Handle))
	}
	if o.DryRun {
		return run(o, res)
	}
	var out Result
	err := withLedgerLock(o.Root, o.Handle, func() error {
		out = run(o, res)
		return nil
	})
	if err != nil {
		return res.defer_("commit ledger lock: " + err.Error())
	}
	return out
}

func run(o Options, res Result) Result {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Env == nil {
		o.Env = os.Environ()
	}
	if o.Remote == "" {
		o.Remote = "origin"
	}
	if o.Identity.Name == "" || o.Identity.Email == "" {
		o.Identity = DefaultIdentity
	}
	if o.Home == "" {
		o.Home = homeDir()
	}
	if o.Git == "" {
		p, err := exec.LookPath("git")
		if err != nil {
			return res.defer_("git is not on PATH: " + err.Error())
		}
		o.Git = p
	}
	// Amendment 28: the owner's global agent config against the preflight
	// copy, at every seat exit, before anything is committed. A change blocks
	// before the commit step and quarantines the seat.
	if !o.DryRun {
		changes, err := seatexec.PreflightDiff(o.Home)
		if err != nil {
			return res.defer_("preflight diff: " + err.Error())
		}
		if len(changes) > 0 {
			return quarantine(o, res, changes)
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
	g := git{bin: o.Git, dir: rec.Worktree, env: harnessEnv(o.Env)}
	repoRoot, why := checkWorktree(g, rec, o.Task.ProjectPath)
	if why != "" {
		return res.refuse(ReasonWorktree, task.RouteLead, why)
	}
	parent := ledger.Parent()
	res.Parent = parent
	tree, err := snapshot(g, parent)
	if err != nil {
		return res.defer_("snapshot of the worktree: " + err.Error())
	}
	res.Tree = tree
	if q := ledger.Quarantine; q != nil && !o.DryRun {
		switch {
		case q.ClearedAt == nil:
			res.Quarantine = true
			return res.refuse(ReasonGlobalConfig, task.RouteOwner, fmt.Sprintf("seat %s is quarantined since %s (task %s, the owner's global agent config changed); nothing of its work is committed until the owner clears it (amq-squad seat clear --seat %s)", o.Handle, q.At.Format(time.RFC3339), q.Task, o.Handle))
		case q.Task == o.Task.ID && q.Tree != "" && q.Tree != tree:
			return res.refuse(ReasonQuarantineTree, task.RouteOwner, fmt.Sprintf("the seat's tree changed after the quarantine: the owner cleared %s, the worktree is now %s", q.Tree, tree))
		}
		ledger.Quarantine = nil
		if err := writeLedger(o.Root, ledger); err != nil {
			return res.defer_("commit ledger: " + err.Error())
		}
		res.Notes = append(res.Notes, "the owner cleared the quarantine of task "+q.Task)
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
	repo, base := repositoryOf(g, o.Remote), firstNonEmpty(o.Grants.DefaultBranch, "main")
	res.CapLines, res.CapFiles = o.Grants.SizeCap(repo, base)
	res.Violations = check(o, g, rec, repoRoot, parent, tree, entries, declared, &res)
	if len(res.Violations) > 0 {
		v := res.Violations[0]
		return res.refuse(v.Reason, task.RouteLead, describe(res.Violations))
	}
	if o.DryRun {
		res.OK = true
		return res
	}
	return commitAndPush(o, g, ledger, rec, res)
}

// quarantine is the global_config_changed block: before the commit step,
// the seat's tree is snapshotted (never committed or pushed) and kept on the
// ledger with its diff digest and the config diff as the block's evidence;
// the caller quarantines the seat (task.Store.Quarantine: no new lease) and
// closes it. Only an existing ledger holds the evidence; without one the
// refusal still stands and names why no tree was kept.
func quarantine(o Options, res Result, changes []seatexec.ConfigChange) Result {
	res.Config, res.Quarantine = changes, true
	var files []string
	for _, c := range changes {
		files = append(files, c.File+" ("+c.Detail+")")
	}
	detail := "the owner's global agent config changed during the seat's run: " + strings.Join(files, "; ") + "; the seat is quarantined and its diff kept uncommitted until the owner clears it (amq-squad seat clear --seat " + o.Handle + ")"
	q := LedgerQuarantine{Task: o.Task.ID, Config: changes, At: o.Now().UTC()}
	l, lerr := ReadLedger(o.Root, o.Handle)
	if lerr == nil && l.Quarantine != nil && l.Quarantine.ClearedAt == nil {
		return res.refuse(ReasonGlobalConfig, task.RouteOwner, detail) // the first evidence stays
	}
	if rec, err := stagedRecord(o.Root, o.Handle); err != nil {
		q.Snapshot = err.Error()
	} else if lerr != nil {
		q.Snapshot = "no commit ledger: " + lerr.Error()
	} else {
		g := git{bin: o.Git, dir: rec.Worktree, env: harnessEnv(o.Env)}
		q.Parent = l.Parent()
		if tree, err := snapshot(g, q.Parent); err != nil {
			q.Snapshot = err.Error()
		} else if raw, err := g.run(nil, nil, "diff-tree", "-r", "-z", "--no-renames", "--no-abbrev", "--no-ext-diff", "--no-textconv", "--raw", q.Parent, tree); err != nil {
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
	return res.refuse(ReasonGlobalConfig, task.RouteOwner, detail)
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

// checkWorktree confirms the worktree is a checkout of the task's
// repository with the record's branch checked out. It returns the
// repository root (the task's project path, else the worktree's top level).
func checkWorktree(g git, rec launch.Record, project string) (string, string) {
	top, err := g.line("rev-parse", "--show-toplevel")
	if err != nil {
		return "", "the seat worktree " + rec.Worktree + " is not a git checkout: " + err.Error()
	}
	if evalOr(top) != evalOr(rec.Worktree) {
		return "", fmt.Sprintf("the seat worktree %s is inside the checkout %s, not its root", rec.Worktree, top)
	}
	head, err := g.line("symbolic-ref", "-q", "HEAD")
	if err != nil || head != "refs/heads/"+rec.Branch {
		return "", fmt.Sprintf("the seat worktree has %q checked out, not the seat branch %s", head, rec.Branch)
	}
	if project == "" {
		return top, ""
	}
	common, err := g.line("rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err.Error()
	}
	pg := git{bin: g.bin, dir: project, env: g.env}
	projCommon, err := pg.line("rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil || evalOr(projCommon) != evalOr(common) {
		return "", fmt.Sprintf("the seat worktree %s is not a checkout of the task's repository %s", rec.Worktree, project)
	}
	return project, ""
}

// snapshot builds the tree of the worktree as it is now, in a temporary
// index started from parent (the seat's real index is not trusted or
// touched): tracked changes, deletions and untracked files that .gitignore
// does not exclude. git itself refuses a path beyond a symbolic link.
func snapshot(g git, parent string) (string, error) {
	f, err := os.CreateTemp("", "amq-squad-seat-index-*")
	if err != nil {
		return "", err
	}
	idx := f.Name()
	f.Close()
	os.Remove(idx)
	defer os.Remove(idx)
	env := []string{"GIT_INDEX_FILE=" + idx}
	if _, err := g.run(nil, env, "read-tree", parent); err != nil {
		return "", err
	}
	if _, err := g.run(nil, env, "add", "-A", "--", "."); err != nil {
		return "", err
	}
	out, err := g.run(nil, env, "write-tree")
	return strings.TrimSpace(string(out)), err
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
	var protected, outside, nested []string
	for _, e := range entries {
		p := e.Path
		if e.NewMode == "160000" || e.OldMode == "160000" {
			nested = append(nested, p)
		}
		if entry, hit := ps.match(p); hit && !matchFold(gated, p) {
			protected = append(protected, p+" ("+entry+")")
		} else if name := ps.managedName(p); name != "" && !matchFold(gated, p) && managedChanged(g, parent, tree, p) {
			protected = append(protected, p+" (the managed block of "+name+")")
		}
		if !task.MatchPath(declared, p) {
			outside = append(outside, p)
		}
	}
	var out []Violation
	if len(protected) > 0 {
		out = append(out, Violation{Reason: ReasonProtected, Paths: protected})
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
// pushes it as a fast-forward. A re-run after a crash finds the recorded
// commit and only pushes.
func commitAndPush(o Options, g git, l Ledger, rec launch.Record, res Result) Result {
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
	if _, err := g.run(nil, nil, "update-ref", "-m", "amq-squad seat commit "+o.Task.ID, "refs/heads/"+rec.Branch, res.Commit); err != nil {
		return res.defer_(err.Error())
	}
	// The seat's index follows the new branch tip; the files are untouched.
	if _, err := g.run(nil, nil, "read-tree", res.Commit); err != nil {
		return res.defer_(err.Error())
	}
	if l.Commits[last].Pushed {
		res.Pushed, res.OK = true, true
		return res
	}
	if _, err := g.line("remote", "get-url", o.Remote); err != nil {
		res.Notes = append(res.Notes, "no remote "+o.Remote+": committed, not pushed")
		res.OK = true
		return res
	}
	out, err := g.run(nil, nil, "push", "--porcelain", "--no-verify", o.Remote, res.Commit+":refs/heads/"+rec.Branch)
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
	res.Pushed, res.OK = true, true
	return res
}

// Message is the harness commit message: the task id and title, then
// trailers naming the seat and the digests of the declared set and the diff.
func Message(t task.Task, seat, declaredDigest, diffDigest string) string {
	title := strings.Join(strings.Fields(t.Title), " ")
	return fmt.Sprintf("%s: %s\n\nCommitted by the amq-squad harness for seat %s: the seat changed the tree, the Go core checked it against the task's declared files and the grant, then committed (plan 4.3, amendment 21).\n\nAmq-Squad-Task: %s\nAmq-Squad-Seat: %s\nAmq-Squad-Declared-Files: %s\nAmq-Squad-Diff: %s\n",
		t.ID, title, seat, t.ID, seat, declaredDigest, diffDigest)
}

// repositoryOf is the remote's owner/name ("" when it has none).
func repositoryOf(g git, remote string) string {
	url, err := g.line("remote", "get-url", remote)
	if err != nil {
		return ""
	}
	url = strings.TrimSuffix(strings.TrimSuffix(url, "/"), ".git")
	if i := strings.LastIndexAny(url, ":/"); i >= 0 {
		if j := strings.LastIndexAny(url[:i], ":/"); j >= 0 {
			return url[j+1:]
		}
	}
	return url
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
