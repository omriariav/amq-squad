package mergetrain

import (
	"fmt"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/grants"
)

// The six grant limits (plan 7.2), the task scope (as at the commit step) and
// the PR state. Each refusal names one rule.
const (
	RuleRepository    = "repository"
	RuleBaseBranch    = "base_branch"
	RuleCIGreen       = "ci_green"
	RuleSizeCap       = "size_cap"
	RuleForcePush     = "force_push"
	RuleProtectedPath = "protected_path"
	RuleTaskScope     = "task_scope"
	RulePRState       = "pr_state"
	RuleTaskUnbound   = "task_unbound"
	RulePRHeadMoved   = "pr_head_moved"
)

// Refusal is one failed rule for one PR.
type Refusal struct {
	Rule   string `json:"rule"`
	Detail string `json:"detail"`
}

// checkInput is everything one PR's check reads.
type checkInput struct {
	repository, base string
	item             Item
	pr               PRInfo
	runs             []CheckRun
	grant            grants.Resolved
	required         []RequiredCheck // the CI checks that must be green, with the app the rule pins
	trainHeads       map[string]bool // heads the train's own rebases produced
}

// Changed is the PR's changed-line count (additions plus deletions), the
// number the size cap reads.
func Changed(files []FileChange) int {
	n := 0
	for _, f := range files {
		n += f.Additions + f.Deletions
	}
	return n
}

// paths are every path a PR touches, a rename's source included.
func paths(files []FileChange) []string {
	var out []string
	for _, f := range files {
		out = append(out, f.Path)
		if f.PreviousPath != "" && f.PreviousPath != f.Path {
			out = append(out, f.PreviousPath)
		}
	}
	return out
}

// check is the Go core's rules check on one PR at one head. The model is never
// the enforcement point: an empty result is the only pass.
func check(in checkInput) []Refusal {
	var out []Refusal
	add := func(rule, format string, args ...any) {
		out = append(out, Refusal{Rule: rule, Detail: fmt.Sprintf(format, args...)})
	}
	if in.pr.State != "OPEN" {
		add(RulePRState, "PR #%d is %s", in.pr.Number, in.pr.State)
	}
	merge, ok := in.grant.MergeFor(in.repository, in.base)
	switch {
	case !strings.EqualFold(in.pr.Repository, in.repository):
		add(RuleRepository, "PR #%d is on %s, the train runs on %s", in.pr.Number, in.pr.Repository, in.repository)
	case !ok && !slices.ContainsFunc(in.grant.Merge, func(m grants.Merge) bool { return strings.EqualFold(m.Repository, in.repository) }):
		add(RuleRepository, "no merge grant for %s", in.repository)
	}
	switch {
	case in.pr.BaseRef != in.base:
		add(RuleBaseBranch, "PR #%d targets %s, the train lands on %s", in.pr.Number, in.pr.BaseRef, in.base)
	case !ok && slices.ContainsFunc(in.grant.Merge, func(m grants.Merge) bool { return strings.EqualFold(m.Repository, in.repository) }):
		add(RuleBaseBranch, "the merge grant for %s does not cover base %s", in.repository, in.base)
	}
	if ok && merge.CIRequired {
		if why, _ := ciRed(in.required, in.runs); why != "" {
			add(RuleCIGreen, "head %s: %s", short(in.pr.HeadSHA), why)
		}
	}
	if n := Changed(in.pr.Files); ok && n > merge.SizeCapLines {
		add(RuleSizeCap, "%d changed lines, cap %d", n, merge.SizeCapLines)
	}
	if n := len(in.pr.Files); ok && n > merge.SizeCapFiles {
		add(RuleSizeCap, "%d changed files, cap %d", n, merge.SizeCapFiles)
	}
	if len(in.pr.Files) >= MaxCompareFiles {
		add(RuleSizeCap, "the diff reaches the compare endpoint's %d-file limit and cannot be verified whole", MaxCompareFiles)
	}
	for _, fp := range in.pr.ForcePushes {
		if !in.trainHeads[fp.After] {
			add(RuleForcePush, "head was force-pushed %s -> %s by %s", short(fp.Before), short(fp.After), orDash(fp.Actor))
		}
	}
	entries := in.grant.ProtectedEntries()
	for _, p := range paths(in.pr.Files) {
		if e, hit := protectedHit(p, entries); hit {
			add(RuleProtectedPath, "%s is protected (%s)", p, e)
		}
	}
	switch {
	case in.item.Unbound != "":
		add(RuleTaskUnbound, "no task is bound to PR #%d by the harness commit step (%s)", in.pr.Number, in.item.Unbound)
	case in.item.BoundHead != "" && in.pr.HeadSHA != in.item.BoundHead:
		// a push after the binding re-binds through the commit step first;
		// the train's own rebase re-binds right after its push (Binder)
		add(RulePRHeadMoved, "PR #%d head %s is not the pr_head %s bound to task %s; re-bind through the commit step", in.pr.Number, short(in.pr.HeadSHA), short(in.item.BoundHead), in.item.Task)
	}
	if len(in.item.DeclaredFiles) == 0 && in.item.Unbound == "" {
		add(RuleTaskScope, "task %s declares no files", orDash(in.item.Task))
	}
	for _, p := range paths(in.pr.Files) {
		if len(in.item.DeclaredFiles) > 0 && !Declared(p, in.item.DeclaredFiles) {
			add(RuleTaskScope, "%s is outside task %s's declared files %v", p, orDash(in.item.Task), in.item.DeclaredFiles)
		}
	}
	return out
}

// ciRed says why the required checks are not all green at the head ("" when
// they are). A required check counts only from the app its rule pins, and
// only that app's latest result for the name (the highest check-run id); a
// rule that pins no app needs every result for the name to be green, so a
// failing app cannot hide behind another app's success. A required check
// with no result is red: CI absent is not green. absent reports that every
// red check simply has no result at this head.
func ciRed(required []RequiredCheck, runs []CheckRun) (why string, absent bool) {
	if len(required) == 0 {
		return "no required CI check is known (the server protection names none)", false
	}
	stateOf := func(r CheckRun) string {
		if r.Status != "" && !strings.EqualFold(r.Status, "completed") {
			return strings.ToLower(r.Status)
		}
		return strings.ToLower(r.Conclusion)
	}
	var bad []string
	for _, req := range required {
		label := req.Context
		if req.AppID != 0 {
			label += "@app" + strconv.Itoa(req.AppID)
		}
		state := "absent"
		if req.AppID != 0 {
			var latest *CheckRun
			for i, r := range runs {
				if r.Name == req.Context && r.AppID == req.AppID && (latest == nil || r.ID > latest.ID) {
					latest = &runs[i]
				}
			}
			if latest != nil {
				state = stateOf(*latest)
			}
		} else {
			for _, r := range runs {
				if r.Name != req.Context {
					continue
				}
				if s := stateOf(r); state == "absent" || state == "success" {
					state = s
				}
			}
		}
		if state != "success" {
			bad = append(bad, label+"="+state)
		}
	}
	if len(bad) == 0 {
		return "", false
	}
	absent = !slices.ContainsFunc(bad, func(b string) bool { return !strings.HasSuffix(b, "=absent") })
	return "required checks not green: " + strings.Join(bad, ", "), absent
}

// rules returns the sorted, distinct rule codes of a refusal list.
func rules(rs []Refusal) []string {
	var out []string
	for _, r := range rs {
		if !slices.Contains(out, r.Rule) {
			out = append(out, r.Rule)
		}
	}
	sort.Strings(out)
	return out
}

// protectedHit matches a repository path against the grant's protected
// entries. Entries outside the repository (~/..., /...) cannot be in a PR
// diff. A managed: entry protects the whole file here, stricter than guard:
// the train sees a diff, not the managed block's text. A trailing / or an
// existing directory entry covers a tree; * ? [ are globs within a segment.
func protectedHit(p string, entries []string) (string, bool) {
	p = strings.ToLower(p) // case-folded: on a case-insensitive checkout Squad.yaml is squad.yaml
	for _, e := range entries {
		raw := e
		e = strings.ToLower(strings.TrimPrefix(e, grants.ManagedPrefix))
		if strings.HasPrefix(e, "~/") || strings.HasPrefix(e, "/") || e == "" {
			continue
		}
		e = strings.TrimPrefix(e, "./")
		if strings.ContainsAny(e, "*?[") {
			if ok, _ := path.Match(strings.TrimSuffix(e, "/"), p); ok {
				return raw, true
			}
			continue
		}
		dir := strings.TrimSuffix(e, "/")
		if p == dir || strings.HasPrefix(p, dir+"/") {
			return raw, true
		}
	}
	return "", false
}

// Declared reports whether a path is inside a task's declared file set
// (internal/task globs: path.Match per segment, a trailing / or /** covers a
// tree, ** as a segment matches any number of segments).
func Declared(p string, globs []string) bool {
	segs := strings.Split(p, "/")
	for _, g := range globs {
		tree := false
		switch {
		case strings.HasSuffix(g, "/**"):
			g, tree = strings.TrimSuffix(g, "/**"), true
		case strings.HasSuffix(g, "/"):
			g, tree = strings.TrimSuffix(g, "/"), true
		}
		if g == "**" || matchSegs(strings.Split(g, "/"), segs, tree) {
			return true
		}
	}
	return false
}

func matchSegs(g, p []string, tree bool) bool {
	if len(g) == 0 {
		return len(p) == 0 || tree
	}
	if g[0] == "**" {
		for i := 0; i <= len(p); i++ {
			if matchSegs(g[1:], p[i:], tree) {
				return true
			}
		}
		return false
	}
	if len(p) == 0 {
		return false
	}
	if ok, _ := path.Match(g[0], p[0]); !ok {
		return false
	}
	return matchSegs(g[1:], p[1:], tree)
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return orDash(sha)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
