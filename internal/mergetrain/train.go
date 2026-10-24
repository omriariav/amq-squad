// Package mergetrain is the deterministic merge train (plan 7.2, 7.3, R34):
// the lead decides an ordered set of PRs inside the standing per-repository
// merge grant, and this Go loop lands it under the owner's identity. Per PR
// the core checks the grant's six limits (repository, base branch, CI green,
// size cap, no force push, no protected-path change) and the task scope (the
// PR's diff against the task's declared_files). A refusal goes to the lead and
// raises an owner-signed gate (the growth class, gatetarget.MergeBeyond); an
// approval opens exactly that head past exactly those rules. The batch digest
// over the ordered (PR, head SHA) pairs plus the base SHA is written to the
// audit log and signed by nobody; a rebase that changes a head recomputes it
// and re-runs the check. Before a real run the server's branch protection must
// require CI and refuse direct push, else the train refuses to run. GitHub's
// merge queue is not available on user-owned repositories (S2: 422), so this
// loop is the queue.
package mergetrain

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gatetarget"
	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Item is one PR of the lead's ordered set. DeclaredFiles is the task's file
// set; when empty and Task names a task, the CLI fills it from the task store.
type Item struct {
	Number        int      `json:"number"`
	Task          string   `json:"task,omitempty"`
	DeclaredFiles []string `json:"declared_files,omitempty"`
}

// RequestSchema is the lead's input schema version.
const RequestSchema = 1

// Request is the lead's ordered PR set for one repository and base branch.
// RequiredChecks adds CI check names to the server's required set; a dry run
// of a repository whose protection names none uses it to read CI at all.
type Request struct {
	Schema         int      `json:"schema"`
	Repository     string   `json:"repository"`
	Base           string   `json:"base"`
	PRs            []Item   `json:"prs"`
	RequiredChecks []string `json:"required_checks,omitempty"`
}

var repoRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// Validate checks the request shape: owner/name, a base, 1..50 unique PRs and
// declared files that are clean repository-relative globs.
func (r *Request) Validate() error {
	if r.Schema != RequestSchema {
		return fmt.Errorf("merge train request schema %d, want %d", r.Schema, RequestSchema)
	}
	if !repoRe.MatchString(r.Repository) || strings.TrimSpace(r.Base) == "" || strings.ContainsAny(r.Base, " \t\r\n") {
		return errors.New("merge train request needs repository owner/name and a base branch")
	}
	if len(r.PRs) == 0 || len(r.PRs) > 50 {
		return fmt.Errorf("merge train request needs 1..50 PRs, got %d", len(r.PRs))
	}
	seen := map[int]bool{}
	for i := range r.PRs {
		it := &r.PRs[i]
		if it.Number < 1 || seen[it.Number] {
			return fmt.Errorf("PR %d is not a unique positive number", it.Number)
		}
		seen[it.Number] = true
		files, err := task.CleanDeclaredFiles(it.DeclaredFiles)
		if err != nil {
			return fmt.Errorf("PR #%d: %w", it.Number, err)
		}
		it.DeclaredFiles = files
	}
	return nil
}

// Escalator raises the owner-signed gate a refusal escalates to (3-1's gate
// store writer: the record first, then the question).
type Escalator interface {
	Raise(spec gatestore.Spec) (gatestore.Record, error)
}

// Gates reports an accepted owner answer for an action and target (the full
// owner-signature check, gateverify.Verifier.AcceptedFor).
type Gates interface {
	Accepted(action, target string) (gateID string, ok bool)
}

// Step is what manual mode shows before each merge.
type Step struct {
	Index  int
	Number int
	Head   string
	Digest string
}

// Train lands one request. The zero Manual is auto mode, the default.
type Train struct {
	Forge    Forge
	Grants   grants.Resolved
	Audit    Audit
	Escalate Escalator // nil: the refusal is recorded with gate_error
	Gates    Gates     // nil: no gate opens a refused PR
	Lead     string    // the handle that raises gates
	DryRun   bool      // plan, order, conflicts, file-set checks; no merge, no gate
	Manual   bool      // keep the per-merge stop (plan 7.2)
	Confirm  func(Step) bool
	// InSeat is set when the process is a seat (AMQ_SQUAD_SEAT): a real run
	// refuses, since no seat process holds a merge path (R34).
	InSeat bool
	CIWait time.Duration // how long a rebased head may wait for CI (default 20 min)
	Poll   time.Duration // CI poll interval (default 15 s)
	Now    func() time.Time
	Sleep  func(time.Duration)
}

// PlanPR is one PR's line of the plan and of the run.
type PlanPR struct {
	Number    int       `json:"number"`
	Task      string    `json:"task,omitempty"`
	Head      string    `json:"head"`
	Draft     bool      `json:"draft"`
	Changed   int       `json:"changed_lines"`
	Files     []string  `json:"files"`
	Refusals  []Refusal `json:"refusals,omitempty"`
	GateID    string    `json:"gate_id,omitempty"`
	GateError string    `json:"gate_error,omitempty"`
	Lifted    bool      `json:"lifted,omitempty"` // an accepted owner gate opened it
	After     []int     `json:"after,omitempty"`  // earlier PRs whose files overlap: rebase after them
	// RebaseForCI: no required check ran at this head and its base is behind;
	// the real run rebases it (CI then runs) and re-checks.
	RebaseForCI bool     `json:"rebase_for_ci,omitempty"`
	Overlap     []string `json:"overlap,omitempty"`
	Status      string   `json:"status"` // planned | refused | merged | stopped
	Merged      string   `json:"merged,omitempty"`
	base        string   // the PR's merge base, for RebaseForCI
}

// ciAbsentOnly: the only refusal is CI with no run at the head.
func ciAbsentOnly(rs []Refusal) bool {
	if len(rs) != 1 || rs[0].Rule != RuleCIGreen {
		return false
	}
	_, list, ok := strings.Cut(rs[0].Detail, "not green: ")
	return ok && !slices.ContainsFunc(strings.Split(list, ", "), func(b string) bool { return !strings.HasSuffix(b, "=absent") })
}

// Result is a run's outcome (the dry run's plan, or what landed).
type Result struct {
	Run           string     `json:"run"`
	Repository    string     `json:"repository"`
	Base          string     `json:"base"`
	BaseSHA       string     `json:"base_sha"`
	DryRun        bool       `json:"dry_run"`
	Manual        bool       `json:"manual"`
	Protection    Protection `json:"protection"`
	ProtectionMet bool       `json:"protection_met"`
	CIChecks      []string   `json:"ci_checks"`
	Pairs         []Pair     `json:"pairs"`
	Digest        string     `json:"digest"`
	Digests       []string   `json:"digests,omitempty"` // every digest written, in order
	PRs           []PlanPR   `json:"prs"`
	Stopped       string     `json:"stopped,omitempty"`
	WallMS        int64      `json:"wall_ms"`
}

// Refused lists the refused PR numbers.
func (r Result) Refused() []int {
	var out []int
	for _, p := range r.PRs {
		if p.Status == "refused" {
			out = append(out, p.Number)
		}
	}
	return out
}

var (
	// ErrProtection: the server does not require CI or does not refuse direct
	// push, so the train refuses to run (7.3).
	ErrProtection = errors.New("branch protection does not require CI and refuse direct push")
	// ErrSeat: a seat process asked for a real run.
	ErrSeat = errors.New("a seat process holds no merge path (R34); the train runs under the owner's identity outside the seat")
)

func (t *Train) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

// Run checks, orders, records and (unless DryRun) lands the request.
func (t *Train) Run(ctx context.Context, req Request) (res Result, err error) {
	start := t.now()
	if err := req.Validate(); err != nil {
		return Result{}, err
	}
	if t.InSeat && !t.DryRun {
		return Result{}, ErrSeat
	}
	res = Result{Run: runID(start), Repository: req.Repository, Base: req.Base, DryRun: t.DryRun, Manual: t.Manual}
	defer func() { res.WallMS = t.now().Sub(start).Milliseconds() }()
	entry := func(e Entry) error {
		e.Run, e.Repository, e.Base, e.DryRun = res.Run, req.Repository, req.Base, t.DryRun
		return t.Audit.Append(e)
	}

	prot, err := t.Forge.Protection(ctx, req.Repository, req.Base)
	if err != nil {
		return res, fmt.Errorf("read branch protection: %w", err)
	}
	res.Protection, res.ProtectionMet = prot, prot.Met()
	detail := "ok"
	if !prot.Met() {
		detail = fmt.Sprintf("unmet: required checks %v, direct push refused %v", prot.RequiredChecks, prot.DirectPushRefused)
	}
	if err := entry(Entry{Kind: "protection", Detail: detail, Rules: prot.Sources}); err != nil {
		return res, err
	}
	if !prot.Met() && !t.DryRun {
		_ = entry(Entry{Kind: "stop", Detail: "branch protection: " + detail})
		res.Stopped = "branch protection: " + detail
		return res, ErrProtection
	}
	res.CIChecks = union(prot.RequiredChecks, req.RequiredChecks)
	if res.BaseSHA, err = t.Forge.BranchHead(ctx, req.Repository, req.Base); err != nil {
		return res, fmt.Errorf("read %s head: %w", req.Base, err)
	}

	// Plan: check every PR at its current head, then order and digest.
	for _, it := range req.PRs {
		p, err := t.checkPR(ctx, req, it, res.CIChecks)
		if err != nil {
			return res, err
		}
		if ciAbsentOnly(p.Refusals) && p.base != res.BaseSHA {
			p.Refusals, p.RebaseForCI = nil, true
		}
		if len(p.Refusals) > 0 {
			t.refuse(req, &p, entry)
		}
		res.PRs = append(res.PRs, p)
	}
	order(res.PRs)
	t.digest(&res, entry)
	if t.DryRun {
		return res, entry(Entry{Kind: "plan", BaseSHA: res.BaseSHA, Pairs: res.Pairs, Digest: res.Digest,
			Detail: fmt.Sprintf("%d planned, %d refused %v", len(res.Pairs), len(res.Refused()), res.Refused())})
	}

	merged := map[string]int{} // path -> the PR that landed it in this run
	for i := range res.PRs {
		p := &res.PRs[i]
		if p.Status != "planned" {
			continue
		}
		if t.Manual && (t.Confirm == nil || !t.Confirm(Step{Index: i, Number: p.Number, Head: p.Head, Digest: res.Digest})) {
			res.Stopped = fmt.Sprintf("manual stop before #%d", p.Number)
			return res, entry(Entry{Kind: "stop", PR: p.Number, Head: p.Head, Detail: res.Stopped})
		}
		if err := t.land(ctx, req, &res, p, merged, entry); err != nil {
			res.Stopped = fmt.Sprintf("#%d: %v", p.Number, err)
			_ = entry(Entry{Kind: "stop", PR: p.Number, Head: p.Head, Detail: res.Stopped})
			return res, err
		}
	}
	return res, nil
}

// checkPR reads one PR at its head and runs the rules check.
func (t *Train) checkPR(ctx context.Context, req Request, it Item, ci []string) (PlanPR, error) {
	pr, err := t.Forge.PR(ctx, req.Repository, it.Number)
	if err != nil {
		return PlanPR{}, fmt.Errorf("read PR #%d: %w", it.Number, err)
	}
	runs, err := t.Forge.Checks(ctx, req.Repository, pr.HeadSHA)
	if err != nil {
		return PlanPR{}, fmt.Errorf("read checks of #%d at %s: %w", it.Number, short(pr.HeadSHA), err)
	}
	p := PlanPR{Number: it.Number, Task: it.Task, Head: pr.HeadSHA, Draft: pr.Draft, Changed: Changed(pr.Files), Files: paths(pr.Files), Status: "planned", base: pr.BaseSHA}
	p.Refusals = check(checkInput{repository: req.Repository, base: req.Base, item: it, pr: pr, runs: runs, grant: t.Grants,
		required: ci, trainHeads: t.Audit.trainHeads(req.Repository, it.Number)})
	return p, nil
}

// refuse records a refusal and escalates it: an accepted owner gate for this
// exact head and these rules lifts it; otherwise the gate is raised (not in a
// dry run, which raises nothing) and the PR merges nothing.
func (t *Train) refuse(req Request, p *PlanPR, entry func(Entry) error) {
	rs := rules(p.Refusals)
	target := gatetarget.MergeBeyond(req.Repository, p.Number, p.Head, rs)
	if t.Gates != nil {
		if id, ok := t.Gates.Accepted("spawn", target); ok {
			p.Lifted, p.GateID, p.Status = true, id, "planned"
			_ = entry(Entry{Kind: "refusal", PR: p.Number, Task: p.Task, Head: p.Head, Rules: rs, GateID: id, Detail: "lifted by the owner's accepted gate"})
			return
		}
	}
	p.Status = "refused"
	switch {
	case t.DryRun:
		p.GateError = "dry run: would raise " + target
	case t.Escalate == nil:
		p.GateError = "no gate writer attached"
	default:
		id := "merge-" + strings.TrimPrefix(gatetarget.Digest("spawn", target), "sha256:")[:16]
		rec, err := t.Escalate.Raise(gatestore.Spec{GateID: id, Action: "spawn", Target: target, RaisedBy: t.Lead})
		switch {
		case err == nil, errors.Is(err, fs.ErrExist):
			p.GateID = id
			if rec.GateID != "" {
				p.GateID = rec.GateID
			}
		default:
			p.GateID, p.GateError = id, err.Error()
		}
	}
	detail := describe(p.Refusals)
	if p.GateError != "" {
		detail += "; " + p.GateError
	}
	_ = entry(Entry{Kind: "refusal", PR: p.Number, Task: p.Task, Head: p.Head, Rules: rs, GateID: p.GateID, Detail: detail})
}

// order marks, for each PR, the earlier PRs of the set whose file sets
// overlap it: the predicted conflicts (refused PRs included, since the lead
// may still land them through a gate). The run lands them in the lead's
// order and rebases the later one when the earlier one landed.
func order(prs []PlanPR) {
	for j := range prs {
		for i := 0; i < j; i++ {
			var shared []string
			for _, f := range prs[j].Files {
				if slices.Contains(prs[i].Files, f) && !slices.Contains(prs[j].Overlap, f) {
					shared = append(shared, f)
				}
			}
			if len(shared) > 0 {
				prs[j].After = append(prs[j].After, prs[i].Number)
				prs[j].Overlap = append(prs[j].Overlap, shared...)
			}
		}
	}
}

// digest recomputes the batch digest over the planned PRs from the current
// base and writes it to the audit log.
func (t *Train) digest(res *Result, entry func(Entry) error) {
	res.Pairs = nil
	for _, p := range res.PRs {
		if p.Status == "planned" {
			res.Pairs = append(res.Pairs, Pair{Number: p.Number, Head: p.Head})
		}
	}
	res.Digest = BatchDigest(res.Repository, res.BaseSHA, res.Pairs)
	res.Digests = append(res.Digests, res.Digest)
	_ = entry(Entry{Kind: "batch", BaseSHA: res.BaseSHA, Pairs: res.Pairs, Digest: res.Digest})
}

// land re-checks one PR at its current head, rebases it when it overlaps a PR
// this run landed or the server reports a conflict, re-checks after a rebase
// that changed the head, and merges it with the head pinned.
func (t *Train) land(ctx context.Context, req Request, res *Result, p *PlanPR, merged map[string]int, entry func(Entry) error) error {
	it := item(req, p.Number)
	cur, err := t.Forge.PR(ctx, req.Repository, p.Number)
	if err != nil {
		return err
	}
	rebase := cur.Mergeable == "CONFLICTING" || p.RebaseForCI
	for _, f := range paths(cur.Files) {
		if _, ok := merged[f]; ok {
			rebase = true
		}
	}
	if rebase {
		if err := t.Forge.Rebase(ctx, req.Repository, p.Number); err != nil {
			return fmt.Errorf("rebase: %w", err)
		}
		if cur, err = t.Forge.PR(ctx, req.Repository, p.Number); err != nil {
			return err
		}
		if cur.HeadSHA != p.Head {
			if err := entry(Entry{Kind: "rebase", PR: p.Number, Head: p.Head, NewHead: cur.HeadSHA, BaseSHA: res.BaseSHA}); err != nil {
				return err
			}
			t.waitCI(ctx, req.Repository, cur.HeadSHA, res.CIChecks)
		}
	}
	fresh, err := t.checkPR(ctx, req, it, res.CIChecks)
	if err != nil {
		return err
	}
	fresh.After, fresh.Overlap = p.After, p.Overlap
	moved := fresh.Head != p.Head
	*p = fresh
	if moved {
		t.digest(res, entry) // a new head (the train's rebase or a seat's push): new digest
	}
	if len(p.Refusals) > 0 {
		if t.refuse(req, p, entry); p.Status == "refused" {
			t.digest(res, entry)
			return nil
		}
	}
	if p.Draft {
		if err := t.Forge.Ready(ctx, req.Repository, p.Number); err != nil {
			return fmt.Errorf("mark ready: %w", err)
		}
	}
	sha, err := t.Forge.Merge(ctx, req.Repository, p.Number, p.Head)
	if err != nil {
		return fmt.Errorf("merge: %w", err)
	}
	p.Status, p.Merged = "merged", sha
	for _, f := range p.Files {
		merged[f] = p.Number
	}
	res.BaseSHA = sha
	return entry(Entry{Kind: "merge", PR: p.Number, Task: p.Task, Head: p.Head, Merged: sha, Digest: res.Digest})
}

// waitCI polls the required checks at a new head until none is pending or
// the wait ends; the re-check after it decides.
func (t *Train) waitCI(ctx context.Context, repository, sha string, required []string) {
	wait, poll := t.CIWait, t.Poll
	if wait == 0 {
		wait = 20 * time.Minute
	}
	if poll == 0 {
		poll = 15 * time.Second
	}
	sleep := t.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	deadline := t.now().Add(wait)
	for t.now().Before(deadline) && ctx.Err() == nil {
		runs, err := t.Forge.Checks(ctx, repository, sha)
		if err == nil && !pending(required, runs) {
			return
		}
		sleep(poll)
	}
}

func pending(required []string, runs []CheckRun) bool {
	for _, name := range required {
		done := false
		for _, r := range runs {
			if r.Name == name && (r.Status == "" || strings.EqualFold(r.Status, "completed")) {
				done = true
			}
		}
		if !done {
			return true
		}
	}
	return false
}

func item(req Request, n int) Item {
	for _, it := range req.PRs {
		if it.Number == n {
			return it
		}
	}
	return Item{Number: n}
}

func describe(rs []Refusal) string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Rule+": "+r.Detail)
	}
	return strings.Join(out, "; ")
}

func union(a, b []string) []string {
	out := slices.Clone(a)
	for _, s := range b {
		if s = strings.TrimSpace(s); s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func runID(now time.Time) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "mt-" + now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b[:])
}
