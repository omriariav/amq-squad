package seatcommit

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/gatetarget"
	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/rules"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// The checks of the harness commit step (plan 4.3, 7.2, amendment 21): every
// changed path of the seat's diff must be inside the task's declared_files,
// outside the grant's protected paths (a managed: entry protects only the
// amq-squad managed block of that file), and the diff must fit the size cap.
// A growth gate the owner accepted for this task lets a change past a
// protected path or the size cap; nothing else does.

// Reason codes of a refusal (task.RefuseCommit's reason code).
const (
	ReasonGlobalConfig   = "global_config_changed"   // the owner's global agent config differs from the preflight copy (amendment 28)
	ReasonProtected      = "protected_path"          // a changed path is protected by the grant
	ReasonOutside        = "outside_declared_files"  // a changed path is outside the task's declared files (S6 W5)
	ReasonSizeCap        = "size_cap_exceeded"       // the diff is over the size cap
	ReasonNested         = "nested_repository"       // a changed path is a gitlink or a nested repository
	ReasonBaseUnrecorded = "base_unrecorded"         // no core-recorded base for the seat's branch
	ReasonWorktree       = "worktree_mismatch"       // the record's worktree or branch is not what the repository shows
	ReasonPushRejected   = "push_rejected"           // the remote refused a fast-forward push (no force push, ever)
	ReasonGrants         = "grants_invalid"          // the grant could not be read; nothing commits
	ReasonCommitFailed   = "commit_failed"           // the step kept failing (repeated deferrals)
	ReasonQuarantineTree = "quarantine_tree_changed" // the worktree changed after a quarantine the owner cleared
	// ReasonClaudeConfig: a Claude seat's config dir no longer has its launch
	// digest at the exit (or the hook's context call found it changed); the
	// seat is quarantined like global_config_changed (milestone B first pass P1).
	ReasonClaudeConfig = "claude_config_changed"
	// ReasonClearUnsigned: a quarantine's clear is not the owner's signed
	// record; the seat stays quarantined (R1).
	ReasonClearUnsigned = ReasonQuarantineUnverified
	// ReasonQuarantineUnverified: a cleared quarantine whose ClearRecord does
	// not verify (the owner's issuer signature, and for a Buzz-signed clear
	// the owner's answer in the gate store). Every consumer of a clear (the
	// commit step, the exit check, the widen dry run) refuses with it,
	// whether the record is bad or the gate store cannot be read.
	ReasonQuarantineUnverified = "quarantine_unverified"
	// ReasonPRBoundElsewhere: an open PR on the seat branch is not this
	// task's harness PR (another task's, another repository's, another base);
	// it is never reused (P2, the reviewer's pre-clearance).
	ReasonPRBoundElsewhere = "pr_bound_elsewhere"
)

// Growth-gate limits named in a GrowTask target.
const (
	LimitProtected = "protected_paths"
	LimitSize      = "size_cap"
)

// Violation is one failed check.
type Violation struct {
	Reason string   `json:"reason"`
	Paths  []string `json:"paths,omitempty"`
	Detail string   `json:"detail,omitempty"`
	// Managed are the paths of a protected_path violation whose managed
	// block changed: a widening onto them crosses the protected-path limit
	// and raises the growth gate (P2).
	Managed []string `json:"managed,omitempty"`
}

// GateChecker answers whether an owner-signed growth gate for an exact
// target is accepted (internal/gateverify's full consumer check). A nil
// checker accepts nothing.
type GateChecker interface {
	Accepted(target string) bool
}

// protectedSet is the grant's protected entries made repo-relative.
type protectedSet struct {
	rel     []string // repo-relative entries (a trailing / covers a tree; * ? [ are globs)
	managed []string // file base names whose managed block is protected
}

// protectedFor resolves the grant's protected entries (grants.Implicit, the
// owner's and squad.yaml's lists) plus the core's own state for one
// repository: entries under ~/ or absolute are kept when they fall inside
// the repository root, so a session root or rules file kept in the
// repository is covered too.
func protectedFor(g grants.Grants, repoRoot, home string, extra ...string) protectedSet {
	var ps protectedSet
	realRoot := evalOr(repoRoot)
	for _, e := range append(g.ProtectedEntries(), extra...) {
		if e = strings.TrimSpace(e); e == "" {
			continue
		}
		if name, ok := strings.CutPrefix(e, grants.ManagedPrefix); ok {
			ps.managed = append(ps.managed, name)
			continue
		}
		tree := strings.HasSuffix(e, "/")
		abs := ""
		switch {
		case strings.HasPrefix(e, "~/"):
			if home == "" {
				continue
			}
			abs = filepath.Join(home, strings.TrimPrefix(e, "~/"))
		case filepath.IsAbs(e):
			abs = filepath.Clean(e)
		default:
			ps.rel = append(ps.rel, filepath.ToSlash(e))
			continue
		}
		rel, ok := inside(realRoot, evalOr(abs))
		if !ok {
			rel, ok = inside(repoRoot, abs)
		}
		if !ok {
			continue
		}
		if rel == "." {
			rel = "**"
		} else if tree {
			rel += "/"
		}
		ps.rel = append(ps.rel, rel)
	}
	return ps
}

// evalOr resolves the symlinks of the longest existing prefix of p.
func evalOr(p string) string {
	if p == "" {
		return p
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	dir, base := filepath.Split(filepath.Clean(p))
	if dir == "" || filepath.Clean(dir) == filepath.Clean(p) {
		return p
	}
	return filepath.Join(evalOr(filepath.Clean(dir)), base)
}

func inside(root, p string) (string, bool) {
	if root == "" {
		return "", false
	}
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// match returns the protected entry covering a repo-relative path. Matching
// folds case (a case-insensitive filesystem makes .Claude/ and .claude/ one
// directory) and is conservative: an entry covers a path when it matches the
// path or any ancestor directory of it.
func (ps protectedSet) match(p string) (string, bool) {
	lp := strings.ToLower(p)
	for _, e := range ps.rel {
		le := strings.ToLower(strings.TrimSuffix(e, "/"))
		if le == "**" {
			return e, true
		}
		glob := strings.ContainsAny(le, "*?[")
		for q := lp; q != "" && q != "."; q = parent(q) {
			if glob {
				if ok, _ := path.Match(le, q); ok {
					return e, true
				}
			} else if q == le {
				return e, true
			}
		}
	}
	return "", false
}

func parent(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return ""
}

// managedName returns the managed: entry name that covers a path's file
// name (case folded), "" when none does.
func (ps protectedSet) managedName(p string) string {
	base := strings.ToLower(path.Base(p))
	for _, m := range ps.managed {
		if strings.ToLower(m) == base {
			return m
		}
	}
	return ""
}

// overlaps reports the widened globs that may match a protected path (the
// widening check; task.FilesOverlap is conservative, case folded here).
func (ps protectedSet) overlaps(globs []string) []string {
	var lowerRel []string
	for _, e := range ps.rel {
		lowerRel = append(lowerRel, strings.ToLower(e))
	}
	var out []string
	for _, g := range globs {
		if task.FilesOverlap([]string{strings.ToLower(g)}, lowerRel) {
			out = append(out, g)
		}
	}
	return out
}

// ManagedShape is every amq-squad managed block of a text, in order, with
// the marker sequence: each begin marker opens a block that runs to the next
// end marker (or the end of the text), and every stray marker is kept in
// sequence. Two texts with the same shape have the same blocks, byte for
// byte, in the same order (the guard's managedBlockKept and the commit
// step's managed-block check share it).
func ManagedShape(t string) []string {
	var shape []string
	for len(t) > 0 {
		b, e := strings.Index(t, rules.BeginMarker), strings.Index(t, rules.EndMarker)
		switch {
		case b < 0 && e < 0:
			return shape
		case b < 0 || (e >= 0 && e < b): // a stray end marker
			shape, t = append(shape, "stray-end"), t[e+len(rules.EndMarker):]
		default:
			rest := t[b:]
			end := strings.Index(rest[len(rules.BeginMarker):], rules.EndMarker)
			if end < 0 {
				return append(shape, "open:"+rest)
			}
			n := len(rules.BeginMarker) + end + len(rules.EndMarker)
			shape, t = append(shape, "block:"+rest[:n]), rest[n:]
		}
	}
	return shape
}

func sameShape(a, b string) bool {
	x, y := ManagedShape(a), ManagedShape(b)
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// ProtectedTarget is the growth-gate target that lets a task's widening onto
// protected paths through: the widened globs, sorted, as a JSON list.
func ProtectedTarget(root, taskID string, globs []string) string {
	g := append([]string(nil), globs...)
	sort.Strings(g)
	raw, _ := json.Marshal(g)
	return gatetarget.GrowTask(root, taskID, LimitProtected, string(raw))
}

// SizeTarget is the growth-gate target that lets a task's diff of lines and
// files past the size cap.
func SizeTarget(root, taskID string, lines, files int) string {
	return gatetarget.GrowTask(root, taskID, LimitSize, fmt.Sprintf(`{"lines":%d,"files":%d}`, lines, files))
}

// gatedProtected returns the globs of the accepted protected-path gates
// recorded on the task's scope changes.
func gatedProtected(t task.Task, root string, gates GateChecker) []string {
	if gates == nil {
		return nil
	}
	prefix := gatetarget.GrowTask(root, t.ID, LimitProtected, "")
	var out []string
	for _, sc := range t.ScopeChanges {
		for _, target := range sc.Gates {
			value, ok := strings.CutPrefix(target, prefix)
			if !ok {
				continue
			}
			var globs []string
			if json.Unmarshal([]byte(value), &globs) != nil || ProtectedTarget(root, t.ID, globs) != target || !gates.Accepted(target) {
				continue
			}
			out = append(out, globs...)
		}
	}
	return out
}

// gatedSize returns the largest accepted size gate (lines, files) recorded
// on the task's scope changes, zero when none.
func gatedSize(t task.Task, root string, gates GateChecker) (lines, files int) {
	if gates == nil {
		return 0, 0
	}
	prefix := gatetarget.GrowTask(root, t.ID, LimitSize, "")
	for _, sc := range t.ScopeChanges {
		for _, target := range sc.Gates {
			value, ok := strings.CutPrefix(target, prefix)
			if !ok {
				continue
			}
			var v struct{ Lines, Files int }
			if json.Unmarshal([]byte(value), &v) != nil || SizeTarget(root, t.ID, v.Lines, v.Files) != target || !gates.Accepted(target) {
				continue
			}
			lines, files = max(lines, v.Lines), max(files, v.Files)
		}
	}
	return lines, files
}

// matchFold is task.MatchPath with both sides case folded.
func matchFold(globs []string, p string) bool {
	low := make([]string, len(globs))
	for i, g := range globs {
		low[i] = strings.ToLower(g)
	}
	return task.MatchPath(low, strings.ToLower(p))
}

// homeDir is os.UserHomeDir, "" on error.
func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

// OutsidePaths is the exact paths a refusal found outside the task's
// declared files (the outside_declared_files violation's Paths, in the
// order the check found them, untruncated; describe() truncates its display
// at eight). The task record keeps them (task.RefusedPaths, 3.0.1 t16 round
// 7), so the lead's widening reads exact paths, never the display text.
func (r Result) OutsidePaths() []string {
	for _, v := range r.Violations {
		if v.Reason == ReasonOutside {
			return append([]string(nil), v.Paths...)
		}
	}
	return nil
}
