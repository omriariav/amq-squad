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
	"strconv"
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
	// BoundHead and Unbound come from the task-to-PR binding (BindTasks),
	// never from the request.
	BoundHead string `json:"-"`
	Unbound   string `json:"-"`
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
	// ReleaseGates and Root, when set, run the tag and release drift check at
	// every run (Drift): an ungated tag or release becomes an owner alert.
	// The check reads the gate store of every session under the AMQ root
	// (Root's parent); VerifyGate is the owner-signed acceptance a gate must
	// pass to clear one (nil: none clears).
	ReleaseGates *gatestore.Store
	VerifyGate   GateVerifier
	// Binder re-binds a task to the head the train's own rebase pushed (lead
	// ruling, P1-4): the binding stays the one source of truth for pr_head.
	// Tasks is the store the binding is read back from at merge time. A bound
	// PR with no Binder or Tasks, or a failed write, stops the run before the
	// merge (binding_failed).
	Binder Binder
	Tasks  *task.Store
	Root   string
	CIWait time.Duration // how long a rebased head may wait for CI (default 20 min)
	Poll   time.Duration // CI poll interval (default 15 s)
	// UpdateWait bounds the read-back after updatePullRequestBranch: GitHub
	// updates the branch asynchronously, and the train binds the head it
	// reads back, not the mutation's answer (default DefaultUpdateWait).
	UpdateWait time.Duration
	Now        func() time.Time
	Sleep      func(time.Duration)
}

// DefaultUpdateWait is how long the train waits for GitHub to move a branch
// it asked to update before it refuses the PR as branch_update_unconfirmed
// (brief 3.2.0-3a item 1: 120 s, with a backoff between reads).
const DefaultUpdateWait = 120 * time.Second

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
	// RebaseForCI rebases a behind head, waits for CI and re-checks it. The
	// reason is either absent CI or strict branch protection.
	RebaseForCI  bool        `json:"rebase_for_ci,omitempty"`
	RebaseRule   string      `json:"rebase_rule,omitempty"`
	RebaseReason string      `json:"rebase_reason,omitempty"`
	PatchProof   *PatchProof `json:"patch_proof,omitempty"`
	Overlap      []string    `json:"overlap,omitempty"`
	Status       string      `json:"status"` // planned | refused | merged | stopped
	Merged       string      `json:"merged,omitempty"`
	title        string      // the PR's title as the forge read it, for the gate question (brief 3.1.1-1)
	mergeable    string      // server mergeability at the checked head
}

// PatchProof is the stable patch identity checked before a rebased task is
// bound to the server's new head. A dry run sets Required but cannot fill IDs.
type PatchProof struct {
	Required bool   `json:"required"`
	OldHead  string `json:"old_head,omitempty"`
	NewHead  string `json:"new_head,omitempty"`
	OldID    string `json:"old_patch_id,omitempty"`
	NewID    string `json:"new_patch_id,omitempty"`
	Match    bool   `json:"match,omitempty"`
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
	Run           string          `json:"run"`
	Repository    string          `json:"repository"`
	Base          string          `json:"base"`
	BaseSHA       string          `json:"base_sha"`
	DryRun        bool            `json:"dry_run"`
	Manual        bool            `json:"manual"`
	Protection    Protection      `json:"protection"`
	ProtectionMet bool            `json:"protection_met"`
	CIChecks      []RequiredCheck `json:"ci_checks"`
	Pairs         []Pair          `json:"pairs"`
	Digest        string          `json:"digest"`
	Digests       []string        `json:"digests,omitempty"` // every digest written, in order
	Alerts        []Alert         `json:"alerts,omitempty"`  // ungated tags and releases (owner alerts)
	PRs           []PlanPR        `json:"prs"`
	Stopped       string          `json:"stopped,omitempty"`
	WallMS        int64           `json:"wall_ms"`
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
	// ErrPushTargetLocal: the grant's push_url is a local path or file://;
	// no server-side ruleset stands behind it, so a real run is refused
	// whatever local_push_ack says (dry runs proceed).
	ErrPushTargetLocal = errors.New("push_target_local")
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
	var auditErr error // the first failed append; no merge runs after it (P2-11)
	entry := func(e Entry) error {
		e.Run, e.Repository, e.Base, e.DryRun = res.Run, req.Repository, req.Base, t.DryRun
		err := t.Audit.Append(e)
		if err != nil && auditErr == nil {
			auditErr = fmt.Errorf("audit log: %w", err)
		}
		return err
	}

	prot, err := t.Forge.Protection(ctx, req.Repository, req.Base)
	if err != nil {
		return res, fmt.Errorf("read branch protection: %w", err)
	}
	res.Protection, res.ProtectionMet = prot, prot.Met()
	detail := "ok"
	if !prot.Met() {
		detail = fmt.Sprintf("unmet: required checks %v, direct push refused %v (%s)", prot.Checks, prot.DirectPushRefused, strings.Join(prot.Notes, "; "))
	}
	if err := entry(Entry{Kind: "protection", Detail: detail, Rules: prot.Sources}); err != nil {
		return res, err
	}
	if l, ok := t.Forge.(ReleaseLister); ok && t.ReleaseGates != nil && t.Root != "" {
		alerts, resolved, derr := DriftResolved(ctx, l, t.VerifyGate, t.Root, req.Repository, t.now())
		res.Alerts = alerts
		for _, a := range alerts {
			_ = entry(Entry{Kind: "alert", Detail: a.Detail, GateID: a.ID})
		}
		for _, a := range resolved {
			// the alert's entry stays in the log; its clearing is recorded
			_ = entry(Entry{Kind: "alert_cleared", Detail: a.Detail + "; a verified owner gate now covers it", GateID: a.ID})
		}
		if errors.Is(derr, ErrBaselineInvalid) {
			res.Stopped = derr.Error()
			_ = entry(Entry{Kind: "stop", Detail: res.Stopped})
			return res, derr
		}
		if derr != nil {
			_ = entry(Entry{Kind: "alert", Detail: "tag and release drift check failed: " + derr.Error()})
		}
	}
	if m, ok := t.Grants.MergeFor(req.Repository, req.Base); ok && grants.PushTargetLocal(m.PushURL) {
		if !t.DryRun {
			res.Stopped = fmt.Sprintf("push_target_local: the merge grant pushes to %s, which has no server-side ruleset behind it", m.PushURL)
			_ = entry(Entry{Kind: "stop", Detail: res.Stopped})
			return res, fmt.Errorf("%w: %s", ErrPushTargetLocal, m.PushURL)
		}
		res.Protection.Notes = append(res.Protection.Notes, "push_target_local: a real run against "+m.PushURL+" is refused")
	}
	if !prot.Met() && !t.DryRun {
		_ = entry(Entry{Kind: "stop", Detail: "branch protection: " + detail})
		res.Stopped = "branch protection: " + detail
		return res, ErrProtection
	}
	res.CIChecks = slices.Clone(prot.Checks)
	for _, name := range req.RequiredChecks {
		if c := (RequiredCheck{Context: strings.TrimSpace(name)}); c.Context != "" && !slices.Contains(res.CIChecks, c) {
			res.CIChecks = append(res.CIChecks, c)
		}
	}
	if res.BaseSHA, err = t.Forge.BranchHead(ctx, req.Repository, req.Base); err != nil {
		return res, fmt.Errorf("read %s head: %w", req.Base, err)
	}

	// A merge the server made whose response was lost is reconciled from the
	// journal before anything else (5 re-pass, P2-5); a PR the train already
	// merged is reported merged, never re-checked and refused.
	landed := map[int]string{}
	if !t.DryRun {
		if err = t.reconcileRebinds(ctx, &req, entry); err != nil {
			return res, err
		}
		if err = t.reconcileUpdates(ctx, &req, res.BaseSHA, res.CIChecks, entry); err != nil {
			return res, err
		}
		if landed, err = t.reconcileMerges(ctx, req, entry); err != nil {
			return res, err
		}
	}

	// Plan: check every PR at its current head, then order and digest.
	for _, it := range req.PRs {
		if sha, ok := landed[it.Number]; ok {
			res.PRs = append(res.PRs, PlanPR{Number: it.Number, Task: it.Task, Status: "merged", Merged: sha})
			continue
		}
		p, err := t.checkPR(ctx, req, it, res.BaseSHA, res.CIChecks)
		if err != nil {
			return res, err
		}
		behind := false
		if prot.Strict || ciAbsentOnly(p.Refusals) {
			behind, err = t.Forge.Behind(ctx, req.Repository, res.BaseSHA, p.Head)
			if err != nil {
				return res, fmt.Errorf("compare #%d with pinned base: %w", p.Number, err)
			}
		}
		t.applyStrict(&p, prot.Strict && behind, strictUnsafe(p))
		if !prot.Strict && ciAbsentOnly(p.Refusals) && behind {
			p.Refusals, p.RebaseForCI = nil, true
			p.RebaseReason = "required CI is absent on a head behind " + req.Base
		}
		if len(p.Refusals) > 0 {
			t.refuse(req, &p, entry)
		}
		res.PRs = append(res.PRs, p)
	}
	order(res.PRs)
	for i := range res.PRs {
		p := &res.PRs[i]
		it := item(req, p.Number)
		if it.BoundHead != "" && (p.RebaseForCI || len(p.After) > 0 || p.mergeable == "CONFLICTING") {
			p.PatchProof = &PatchProof{Required: true, OldHead: p.Head}
		}
	}
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
		if auditErr != nil {
			res.Stopped = "audit log failed before #" + strconv.Itoa(p.Number)
			return res, auditErr
		}
		err := t.land(ctx, req, &res, p, i, merged, entry, func() error { return auditErr })
		if errors.Is(err, errManualStop) {
			res.Stopped = fmt.Sprintf("manual stop before #%d", p.Number)
			return res, entry(Entry{Kind: "stop", PR: p.Number, Head: p.Head, Detail: res.Stopped})
		}
		if err != nil {
			res.Stopped = fmt.Sprintf("#%d: %v", p.Number, err)
			_ = entry(Entry{Kind: "stop", PR: p.Number, Head: p.Head, Detail: res.Stopped})
			return res, err
		}
	}
	return res, nil
}

// errManualStop: manual mode's confirmation was not given.
var errManualStop = errors.New("manual stop")

// checkPR reads one PR at one head and runs the rules check. The diff comes
// from the compare endpoint for the pinned (base, head) pair, and the head is
// read again after the checks: a head that moved in between is read again,
// so the files, the CI and the head checked are one snapshot (P1-3).
func (t *Train) checkPR(ctx context.Context, req Request, it Item, base string, ci []RequiredCheck) (PlanPR, error) {
	var pr PRInfo
	var runs []CheckRun
	for attempt := 0; ; attempt++ {
		var err error
		if pr, err = t.Forge.PR(ctx, req.Repository, it.Number); err != nil {
			return PlanPR{}, fmt.Errorf("read PR #%d: %w", it.Number, err)
		}
		if pr.Files, err = t.Forge.Diff(ctx, req.Repository, base, pr.HeadSHA); err != nil {
			return PlanPR{}, fmt.Errorf("diff of #%d at %s: %w", it.Number, short(pr.HeadSHA), err)
		}
		if runs, err = t.Forge.Checks(ctx, req.Repository, pr.HeadSHA); err != nil {
			return PlanPR{}, fmt.Errorf("read checks of #%d at %s: %w", it.Number, short(pr.HeadSHA), err)
		}
		again, err := t.Forge.PR(ctx, req.Repository, it.Number)
		if err != nil {
			return PlanPR{}, fmt.Errorf("re-read PR #%d: %w", it.Number, err)
		}
		if again.HeadSHA == pr.HeadSHA {
			break
		}
		if attempt == 2 {
			return PlanPR{}, fmt.Errorf("PR #%d head kept moving while it was checked (%s -> %s)", it.Number, short(pr.HeadSHA), short(again.HeadSHA))
		}
	}
	p := PlanPR{Number: it.Number, Task: it.Task, Head: pr.HeadSHA, Draft: pr.Draft, Changed: Changed(pr.Files), Files: paths(pr.Files), Status: "planned", title: pr.Title, mergeable: pr.Mergeable}
	p.Refusals = check(checkInput{repository: req.Repository, base: req.Base, item: it, pr: pr, runs: runs, grant: t.Grants,
		required: ci, trainHeads: t.Audit.trainHeads(req.Repository, it.Number)})
	return p, nil
}

func strictUnsafe(p PlanPR) string {
	switch {
	case p.mergeable == "CONFLICTING":
		return "the PR conflicts with the current base"
	case slices.ContainsFunc(p.Refusals, func(r Refusal) bool { return r.Rule == RulePRHeadMoved }):
		return "the PR head moved off its task binding"
	default:
		return ""
	}
}

func (t *Train) applyStrict(p *PlanPR, behind bool, unsafe string) {
	if !behind {
		return
	}
	if unsafe != "" {
		p.Refusals = append(p.Refusals, Refusal{Rule: RuleStrictUpToDate,
			Detail: "the branch is behind and the train cannot bring it up to date: " + unsafe})
		return
	}
	p.Refusals = slices.DeleteFunc(p.Refusals, func(r Refusal) bool { return r.Rule == RuleCIGreen })
	p.RebaseForCI = true
	p.RebaseRule = RuleStrictUpToDate
	p.RebaseReason = "GitHub requires the branch to be up to date before merging"
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
		// the question's plain lines read the PR's title and the base the
		// train merges into, both as read here (brief 3.1.1-1)
		rec, err := t.Escalate.Raise(gatestore.Spec{GateID: id, Action: "spawn", Target: target, RaisedBy: t.Lead, Facts: gatestore.SafeFacts(p.title, req.Base)})
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
func (t *Train) land(ctx context.Context, req Request, res *Result, p *PlanPR, index int, merged map[string]int, entry func(Entry) error, audited func() error) error {
	it := item(req, p.Number)
	cur, err := t.Forge.PR(ctx, req.Repository, p.Number)
	if err != nil {
		return err
	}
	if res.Protection.Strict {
		behind, err := t.Forge.Behind(ctx, req.Repository, res.BaseSHA, cur.HeadSHA)
		if err != nil {
			return fmt.Errorf("the train stopped while comparing PR #%d with the current base\nrule: branch freshness\ncompare error: %w", p.Number, err)
		}
		unsafe := ""
		switch {
		case cur.Mergeable == "CONFLICTING":
			unsafe = "the PR conflicts with the current base"
		case it.BoundHead != "" && cur.HeadSHA != it.BoundHead:
			unsafe = "the PR head moved off its task binding"
		}
		t.applyStrict(p, behind, unsafe)
		if len(p.Refusals) > 0 {
			if t.refuse(req, p, entry); p.Status == "refused" {
				t.digest(res, entry)
				return nil
			}
		}
	}
	rebase := cur.Mergeable == "CONFLICTING" || p.RebaseForCI
	for _, f := range p.Files {
		if _, ok := merged[f]; ok {
			rebase = true
		}
	}
	bound := it.BoundHead != ""
	if bound && (t.Binder == nil || t.Tasks == nil) {
		return fmt.Errorf("%w: no task store to read and write the binding of #%d", ErrBindingFailed, p.Number)
	}
	if rebase {
		beforeBase := res.BaseSHA
		oldPatch := ""
		if bound {
			oldPatch, err = t.Forge.PatchID(ctx, req.Repository, beforeBase, p.Head)
			if errors.Is(err, ErrIncompleteDiff) {
				p.PatchProof = &PatchProof{Required: true, OldHead: p.Head}
				return t.refuseUpdate(res, p, entry, RuleRebasePatch, "the update could not be proved: GitHub returned an incomplete diff")
			}
			if err != nil {
				return fmt.Errorf("prove the reviewed patch before updating #%d: %w", p.Number, err)
			}
		}
		// The request is journaled before the mutation (brief 3.2.0-3a item
		// 0): a run that stops between the mutation and the rebase entry
		// leaves the branch moved with no record of the train's hand in it,
		// and the next run recovers from this entry (reconcileUpdates).
		if err := entry(Entry{Kind: "update_requested", PR: p.Number, Task: it.Task, Head: p.Head, BaseSHA: beforeBase,
			Detail: "updatePullRequestBranch (REBASE) with the head pinned"}); err != nil {
			return err
		}
		if _, err := t.Forge.Rebase(ctx, req.Repository, p.Number, p.Head); err != nil {
			// A definite refusal (GitHub answered 4xx, or a GraphQL error
			// that is a refusal: the head moved, a conflict, forbidden)
			// retires the request (update_failed): GitHub never took it,
			// and without this entry the next run could take a seat's own
			// push for the update's result (Codex on #903). A failure on
			// this side without a definite refusal (a timeout, a 5xx, a
			// dropped connection) is ambiguous: GitHub may have completed
			// the update, so the request stays live (update_ambiguous) and
			// the train reads the head back as if the mutation had answered
			// (the plan reviewer on #903).
			if definiteRefusal(err) {
				if aerr := entry(Entry{Kind: "update_failed", PR: p.Number, Task: it.Task, Head: p.Head, BaseSHA: beforeBase, Detail: "updatePullRequestBranch refused: " + err.Error()}); aerr != nil {
					return aerr
				}
				return fmt.Errorf("rebase: %w", err)
			}
			if aerr := entry(Entry{Kind: "update_ambiguous", PR: p.Number, Task: it.Task, Head: p.Head, BaseSHA: beforeBase, Detail: "updatePullRequestBranch failed without a definite refusal, the request stays live: " + err.Error()}); aerr != nil {
				return aerr
			}
		}
		// GitHub performs the update asynchronously and answers with the head
		// as it was (run ab184d07), so the mutation's answer is never bound:
		// the head is read back until it differs from the pinned one or the
		// bounded wait ends (item 1). An update GitHub rejected asynchronously
		// (a conflict) leaves the head unmoved: a refusal, nothing bound.
		cur, moved, err := t.readUpdatedHead(ctx, req.Repository, p.Number, p.Head)
		if err != nil {
			return fmt.Errorf("read #%d after its update: %w", p.Number, err)
		}
		if !moved {
			if bound {
				p.PatchProof = &PatchProof{Required: true, OldHead: p.Head}
			}
			return t.refuseUpdate(res, p, entry, RuleUpdateUnconfirmed, fmt.Sprintf("GitHub did not update the branch of PR #%d within %s (the head did not move); nothing is bound and nothing is merged", p.Number, t.updateWait()))
		}
		pushed := cur.HeadSHA
		// The rebase entry names the head read back (item 2): trainHeads then
		// names it and the force-push rule stays quiet on the train's own
		// update. For a bound PR it is written only once the proof below has
		// passed (the plan reviewer on #903): an entry written before the
		// proof would name an unproven head, and the next run's force-push
		// rule would take that head for the train's own push.
		rebaseEntry := Entry{Kind: "rebase", PR: p.Number, Head: p.Head, NewHead: pushed, BaseSHA: res.BaseSHA}
		if !bound {
			// no task is bound, so there is no proof to wait for: the entry
			// names the head read back, as before
			if err := entry(rebaseEntry); err != nil {
				return err
			}
		}
		if bound {
			afterBase, err := t.Forge.BranchHead(ctx, req.Repository, req.Base)
			if err != nil {
				return fmt.Errorf("read %s head after updating #%d: %w", req.Base, p.Number, err)
			}
			res.BaseSHA = afterBase
			// The proof (item 1): the head read back sits on the base tip the
			// train rebased onto (its first parent; the commit step makes one
			// commit per task), and its patch-id against that base equals the
			// bound head's against the old base. A head on anything else is a
			// push after the update, never the update's result.
			parent, err := t.Forge.FirstParent(ctx, req.Repository, pushed)
			if err != nil {
				return fmt.Errorf("read the first parent of #%d's updated head %s: %w", p.Number, short(pushed), err)
			}
			if parent != afterBase && parent != beforeBase {
				p.PatchProof = &PatchProof{Required: true, OldHead: p.Head, NewHead: pushed, OldID: oldPatch}
				return t.refuseUpdate(res, p, entry, RulePRHeadMoved, fmt.Sprintf("the head read back after the update, %s, is not the update's result: its first parent %s is not the base tip %s the train rebased onto (a push after the update); nothing is bound; re-bind through the commit step", short(pushed), short(parent), short(afterBase)))
			}
			newPatch, err := t.Forge.PatchID(ctx, req.Repository, parent, pushed)
			p.PatchProof = &PatchProof{Required: true, OldHead: p.Head, NewHead: pushed, OldID: oldPatch, NewID: newPatch, Match: err == nil && oldPatch == newPatch}
			if errors.Is(err, ErrIncompleteDiff) {
				p.Head = pushed
				return t.refuseUpdate(res, p, entry, RuleRebasePatch, "the update could not be proved: GitHub returned an incomplete diff")
			}
			if err != nil {
				return fmt.Errorf("prove the updated patch of #%d: %w", p.Number, err)
			}
			if oldPatch != newPatch {
				p.Head = pushed
				return t.refuseUpdate(res, p, entry, RuleRebasePatch, "its change moved during the update")
			}
			// the one admission check before any binding write (the plan
			// reviewer on #903): a head a lead re-bind superseded is never
			// bound by the train, only merge-train rebind re-admits it
			journal, err := t.Audit.Read()
			if err != nil {
				return err
			}
			if !headAdmitted(journal, req.Repository, p.Number, pushed) {
				p.PatchProof = &PatchProof{Required: true, OldHead: p.Head, NewHead: pushed, OldID: oldPatch, NewID: newPatch, Match: true}
				return t.refuseUpdate(res, p, entry, RulePRHeadMoved, fmt.Sprintf("the head read back after the update, %s, was superseded by a lead re-bind decision; the train does not bind it, only merge-train rebind re-admits it; nothing is bound", short(pushed)))
			}
			// the proof passed: audit first (the rebase entry, then the
			// rebind), then the binding write, all before the check
			if err := entry(rebaseEntry); err != nil {
				return err
			}
			if err := entry(Entry{Kind: "rebind", PR: p.Number, Task: it.Task, Head: it.BoundHead, NewHead: pushed, Detail: "patch-id: " + oldPatch + "\nfirst parent: " + parent}); err != nil {
				return err
			}
			// the write through the one admission under the audit lock: a
			// head superseded since the check above is refused there
			if _, err := bindAdmitted(t.Audit, t.Binder, entry, bindWrite{Task: it.Task, Repository: req.Repository, PR: p.Number, From: it.BoundHead, To: pushed}); err != nil {
				if errors.Is(err, ErrHeadSuperseded) {
					return t.refuseUpdate(res, p, entry, RulePRHeadMoved, fmt.Sprintf("the head read back after the update, %s, was superseded by a lead re-bind decision before the binding write; the train does not bind it, only merge-train rebind re-admits it; nothing is bound", short(pushed)))
				}
				return fmt.Errorf("%w: task %s to %s: %v", ErrBindingFailed, it.Task, short(pushed), err)
			}
		}
		t.waitCI(ctx, req.Repository, pushed, res.CIChecks)
	}
	if bound {
		// the check reads the binding as the task store has it now, never the
		// request's cached copy
		b, why, err := boundTask(*t.Tasks, req.Repository, p.Number)
		if err != nil {
			return err
		}
		it.Unbound, it.BoundHead, it.DeclaredFiles = why, b.PRHead, b.DeclaredFiles
	}
	fresh, err := t.checkPR(ctx, req, it, res.BaseSHA, res.CIChecks)
	if err != nil {
		return err
	}
	fresh.After, fresh.Overlap = p.After, p.Overlap
	fresh.PatchProof = p.PatchProof
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
	// Manual mode asks after the final rebase, check and digest, for exactly
	// the head and digest that will merge (P2-9); the merge pins that head.
	if t.Manual && (t.Confirm == nil || !t.Confirm(Step{Index: index, Number: p.Number, Head: p.Head, Digest: res.Digest})) {
		return errManualStop
	}
	if err := audited(); err != nil {
		return err
	}
	if p.Draft {
		if err := t.Forge.Ready(ctx, req.Repository, p.Number); err != nil {
			return fmt.Errorf("mark ready: %w", err)
		}
	}
	// The intent is journaled before the PUT (5 re-pass, P2-5): a merge the
	// server makes whose response is lost is found again, here or at the
	// next run, never taken for a refusal.
	if err := entry(Entry{Kind: "merge_intent", PR: p.Number, Task: p.Task, Head: p.Head, Digest: res.Digest}); err != nil {
		return err
	}
	detail := ""
	sha, err := t.Forge.Merge(ctx, req.Repository, p.Number, p.Head)
	if err != nil {
		got, ok := t.mergedAt(ctx, req.Repository, p.Number, p.Head)
		if !ok {
			return fmt.Errorf("merge: %w", err)
		}
		sha, detail = got, "reconciled: the server merged the pinned head; its response was lost ("+err.Error()+")"
	}
	p.Status, p.Merged = "merged", sha
	if bound {
		if err := t.recordTaskMerge(it.Task, req.Repository, p.Number, p.Head, sha); err != nil {
			return err
		}
	}
	for _, f := range p.Files {
		merged[f] = p.Number
	}
	res.BaseSHA = sha
	return entry(Entry{Kind: "merge", PR: p.Number, Task: p.Task, Head: p.Head, Merged: sha, Digest: res.Digest, Detail: detail})
}

// refuseUpdate refuses a PR on its own update (the proof before the binding,
// or the update itself): the refusal in words under rule, recorded with the
// proof's heads and patch ids, no gate raised, nothing bound.
func (t *Train) refuseUpdate(res *Result, p *PlanPR, entry func(Entry) error, rule, detail string) error {
	p.Refusals = append(p.Refusals, Refusal{Rule: rule, Detail: detail})
	p.Status = "refused"
	e := Entry{Kind: "refusal", PR: p.Number, Task: p.Task, Head: p.Head, Rules: []string{rule}, Detail: detail}
	if proof := p.PatchProof; proof != nil {
		e.Head, e.NewHead = proof.OldHead, proof.NewHead
		if proof.OldID != "" || proof.NewID != "" {
			e.Detail += "\nold patch-id: " + proof.OldID + "\nnew patch-id: " + proof.NewID
		}
	}
	if err := entry(e); err != nil {
		return err
	}
	t.digest(res, entry)
	return nil
}

// reconcileRebinds finishes a proof-backed binding write when a previous run
// stopped after journaling it. It emits no reconcile entry: the journaled head
// and the server head must already be the same.
func (t *Train) reconcileRebinds(ctx context.Context, req *Request, entry func(Entry) error) error {
	if t.Binder == nil || t.Tasks == nil {
		return nil
	}
	entries, err := t.Audit.Read()
	if err != nil {
		return err
	}
	for i := range req.PRs {
		it := &req.PRs[i]
		if it.BoundHead == "" {
			continue
		}
		var pending Entry
		for _, e := range entries {
			if e.Kind == "rebind" && e.Repository == req.Repository && e.PR == it.Number && e.Task == it.Task && e.Head == it.BoundHead && strings.HasPrefix(e.Detail, "patch-id: ") {
				pending = e
			}
		}
		if pending.NewHead == "" {
			continue
		}
		cur, err := t.Forge.PR(ctx, req.Repository, it.Number)
		if err != nil {
			return err
		}
		if cur.HeadSHA != pending.NewHead {
			continue
		}
		// a head a lead re-bind superseded is never replayed (Codex's third
		// pass on #903: the refused re-bind journals its rebind entry before
		// the superseded one); only merge-train rebind re-admits it
		if !headAdmitted(entries, req.Repository, it.Number, pending.NewHead) {
			if err := entry(Entry{Kind: "reconcile", PR: it.Number, Task: it.Task, Head: it.BoundHead, NewHead: pending.NewHead,
				Detail: fmt.Sprintf("the journaled rebind to %s was superseded by a lead re-bind decision; the train does not replay it, only merge-train rebind re-admits it\nbinding not recovered", short(pending.NewHead))}); err != nil {
				return err
			}
			continue
		}
		if _, err := bindAdmitted(t.Audit, t.Binder, entry, bindWrite{Task: it.Task, Repository: req.Repository, PR: it.Number, From: it.BoundHead, To: pending.NewHead}); err != nil {
			if errors.Is(err, ErrHeadSuperseded) {
				continue // superseded since the snapshot: the reconcile entry is written, the binding stays
			}
			return fmt.Errorf("%w: task %s to %s: %v", ErrBindingFailed, it.Task, short(pending.NewHead), err)
		}
		it.BoundHead = pending.NewHead
	}
	return nil
}

// mergedAt reads the PR back: its merge commit when the server merged it at
// exactly the pinned head.
func (t *Train) mergedAt(ctx context.Context, repository string, pr int, head string) (string, bool) {
	cur, err := t.Forge.PR(ctx, repository, pr)
	if err != nil || cur.State != "MERGED" || cur.HeadSHA != head || cur.MergeCommit == "" {
		return "", false
	}
	return cur.MergeCommit, true
}

// reconcileMerges reads the journal at the start of a real run (5 re-pass,
// P2-5): a merge intent with no merge entry after it is checked against the
// server, and a PR the server merged at the intent's head gets its merge
// entry now (the verify verb reads it). It returns every PR of the request
// whose last journaled merge step is a merge, with its merge commit.
func (t *Train) reconcileMerges(ctx context.Context, req Request, entry func(Entry) error) (map[int]string, error) {
	entries, err := t.Audit.Read()
	if err != nil {
		return nil, err
	}
	last := map[int]Entry{} // the last merge_intent or merge of each PR
	for _, e := range entries {
		if !e.DryRun && e.Repository == req.Repository && (e.Kind == "merge_intent" || e.Kind == "merge") {
			last[e.PR] = e
		}
	}
	out := map[int]string{}
	for _, it := range req.PRs {
		e, ok := last[it.Number]
		switch {
		case !ok:
			if it.BoundHead == "" && it.Unbound == "" {
				continue // not a real-run item populated by BindTasks
			}
			cur, err := t.Forge.PR(ctx, req.Repository, it.Number)
			if err != nil {
				return nil, fmt.Errorf("read PR #%d while reconciling: %w", it.Number, err)
			}
			if cur.State != "MERGED" || cur.MergeCommit == "" {
				continue
			}
			if it.BoundHead != "" {
				recorded, err := t.reconcileTaskMerge(it, req.Repository, cur.HeadSHA, cur.MergeCommit, "server-reported merged head", entry)
				if err != nil {
					return nil, err
				}
				if !recorded {
					continue
				}
			}
			if err := entry(Entry{Kind: "merge", PR: it.Number, Task: it.Task, Head: cur.HeadSHA, Merged: cur.MergeCommit,
				Detail: "reconciled: the server already reported the PR merged"}); err != nil {
				return nil, err
			}
			out[it.Number] = cur.MergeCommit
		case e.Kind == "merge":
			if it.BoundHead != "" {
				recorded, err := t.reconcileTaskMerge(it, req.Repository, e.Head, e.Merged, "journaled merge", entry)
				if err != nil {
					return nil, err
				}
				if !recorded {
					continue
				}
			}
			out[it.Number] = e.Merged
		default:
			if it.BoundHead != "" {
				matches, err := t.reconcileBinding(it, req.Repository, e.Head, "journaled merge intent", entry)
				if err != nil {
					return nil, err
				}
				if !matches {
					continue
				}
			}
			sha, merged := t.mergedAt(ctx, req.Repository, it.Number, e.Head)
			if !merged {
				continue // not merged: the PR is checked again and a new intent journaled
			}
			if it.BoundHead != "" {
				recorded, err := t.reconcileTaskMerge(it, req.Repository, e.Head, sha, "journaled merge intent", entry)
				if err != nil {
					return nil, err
				}
				if !recorded {
					continue
				}
			}
			if err := entry(Entry{Kind: "merge", PR: it.Number, Task: e.Task, Head: e.Head, Merged: sha, Digest: e.Digest,
				Detail: "reconciled at the next run: the server merged the journaled head (intent of run " + e.Run + ")"}); err != nil {
				return nil, err
			}
			out[it.Number] = sha
		}
	}
	return out, nil
}

// reconcileTaskMerge records a replay only while the task still has the
// journaled binding. A stale replay is audited and skipped, not an abort.
func (t *Train) reconcileTaskMerge(it Item, repository, head, commit, source string, entry func(Entry) error) (bool, error) {
	matches, err := t.reconcileBinding(it, repository, head, source, entry)
	if err != nil || !matches {
		return false, err
	}
	if err := t.recordTaskMerge(it.Task, repository, it.Number, head, commit); err != nil {
		var mismatch *MergeBindingMismatch
		if errors.As(err, &mismatch) {
			return false, t.auditReconcileMismatch(it, head, mismatch.BoundHead, source, entry)
		}
		return false, err
	}
	return true, nil
}

func (t *Train) reconcileBinding(it Item, repository, head, source string, entry func(Entry) error) (bool, error) {
	boundRepo, boundPR, boundHead := repository, it.Number, it.BoundHead
	if t.Tasks != nil {
		bound, err := t.Tasks.Get(it.Task)
		if err != nil {
			return false, err
		}
		boundRepo, boundPR, boundHead = bound.PRRepository, bound.PRNumber, bound.PRHead
	}
	if strings.EqualFold(boundRepo, repository) && boundPR == it.Number && boundHead == head {
		return true, nil
	}
	return false, t.auditReconcileMismatch(it, head, boundHead, source, entry)
}

func (t *Train) auditReconcileMismatch(it Item, head, boundHead, source string, entry func(Entry) error) error {
	headLabel := "journaled head"
	if strings.HasPrefix(source, "server-") {
		headLabel = "server head"
	}
	detail := fmt.Sprintf("the %s does not match task %s's bound head\n%s: %s\nbound head: %s\nmerge fact not recorded", source, it.Task, headLabel, head, boundHead)
	return entry(Entry{Kind: "reconcile", PR: it.Number, Task: it.Task, Head: head, NewHead: boundHead, Detail: detail})
}

// MergeBindingMismatch carries the full heads needed for words-first CLI
// evidence while still matching ErrBindingFailed and its store cause.
type MergeBindingMismatch struct {
	Task, MergeHead, BoundHead string
	Cause                      error
}

func (e *MergeBindingMismatch) Error() string {
	return fmt.Sprintf("%s: merged head %s does not match task %s's bound head %s", ErrBindingFailed, e.MergeHead, e.Task, e.BoundHead)
}
func (e *MergeBindingMismatch) Unwrap() error { return e.Cause }
func (e *MergeBindingMismatch) Is(target error) bool {
	return target == ErrBindingFailed
}

func (t *Train) recordTaskMerge(taskID, repository string, pr int, head, commit string) error {
	if t.Binder == nil {
		return fmt.Errorf("%w: no task store to record the merge of #%d", ErrBindingFailed, pr)
	}
	if err := t.Binder.RecordPRMerge(taskID, repository, pr, head, commit); err != nil {
		if errors.Is(err, task.ErrPRBindingMismatch) {
			boundHead := ""
			if t.Tasks != nil {
				if bound, getErr := t.Tasks.Get(taskID); getErr == nil {
					boundHead = bound.PRHead
				}
			}
			return &MergeBindingMismatch{Task: taskID, MergeHead: head, BoundHead: boundHead, Cause: err}
		}
		return fmt.Errorf("%w: record task %s merge %s: %v", ErrBindingFailed, taskID, short(commit), err)
	}
	return nil
}

// waitCI polls the required checks at a new head until none is pending or
// the wait ends; the re-check after it decides.
func (t *Train) waitCI(ctx context.Context, repository, sha string, required []RequiredCheck) {
	wait, poll := t.CIWait, t.Poll
	if wait == 0 {
		wait = DefaultCIWait
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

func pending(required []RequiredCheck, runs []CheckRun) bool {
	for _, req := range required {
		done := false
		for _, r := range runs {
			if r.Name == req.Context && (req.AppID == 0 || r.AppID == req.AppID) && (r.Status == "" || strings.EqualFold(r.Status, "completed")) {
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

func runID(now time.Time) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "mt-" + now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b[:])
}
