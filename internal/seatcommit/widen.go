package seatcommit

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// The lead widening path (plan 4.3): the lead may add paths to a task's
// declared_files as a recorded decision, inside the grant's limits. A
// widening onto a protected path, or one that leaves the refused diff over
// the size cap, crosses a grant limit: the core refuses it, and the owner-
// signed growth gate for that exact crossing (GrowTask target) is the only
// way through. Once the owner's gate is accepted, the same widening is
// allowed and the gate target is recorded on the scope change, where the
// commit step re-verifies it.

// ReasonGrowthGate is the reason code of a widening that crosses a grant
// limit; the task goes to the owner (block_route owner).
const ReasonGrowthGate = task.ReasonGrowthGateRequired

// WidenOptions are the inputs of the widening check.
type WidenOptions struct {
	Root      string
	Task      task.Task
	Added     []string
	Grants    grants.Resolved
	GrantsErr error
	RepoRoot  string // the task's repository root
	Home      string
	Gates     GateChecker
	// ProtectedAdded is the subset of Added whose exact source paths resolved
	// to a protected path or managed file name. The gate binds these resolved
	// paths, never their symlink aliases.
	ProtectedAdded []string
	// Commit, when set, runs the commit step as a dry run with the widened
	// set on the seat's diff (a task with a done report): the size cap and
	// the protected paths are checked on the real diff, not only the globs.
	Commit *Options
}

// Crossing is one grant limit a widening crosses.
type Crossing struct {
	Limit    string   `json:"limit"` // protected_paths | size_cap
	Target   string   `json:"target"`
	Globs    []string `json:"globs,omitempty"`
	Detail   string   `json:"detail"`
	Accepted bool     `json:"accepted"`
}

// WidenDecision is the core's answer to a widening.
type WidenDecision struct {
	Allowed    bool       `json:"allowed"`
	ReasonCode string     `json:"reason_code,omitempty"`
	Detail     string     `json:"detail,omitempty"`
	Crossings  []Crossing `json:"crossings"`
	// Gates are the accepted growth-gate targets to record on the scope
	// change.
	Gates  []string `json:"gates,omitempty"`
	Notes  []string `json:"notes,omitempty"`
	DryRun *Result  `json:"dry_run,omitempty"`
}

// coveredBy reports whether a protected-path crossing already names every
// one of the paths.
func coveredBy(cs []Crossing, paths []string) bool {
	for _, c := range cs {
		if c.Limit != LimitProtected {
			continue
		}
		all := true
		for _, p := range paths {
			if !matchFold(c.Globs, p) {
				all = false
			}
		}
		if all {
			return true
		}
	}
	return false
}

// CheckWiden decides a proposed widening against the grant.
func CheckWiden(o WidenOptions) WidenDecision {
	d := WidenDecision{Crossings: []Crossing{}}
	if o.GrantsErr != nil {
		d.ReasonCode, d.Detail = ReasonGrants, "the grant could not be read: "+o.GrantsErr.Error()
		return d
	}
	added, err := task.CleanDeclaredFiles(o.Added)
	if err != nil || len(added) == 0 {
		d.ReasonCode, d.Detail = "widen_invalid", fmt.Sprintf("the widened paths are not a valid declared set: %v", err)
		return d
	}
	if o.Home == "" {
		o.Home = homeDir()
	}
	accepted := func(target string) bool {
		return !o.Grants.Insecure && o.Gates != nil && o.Gates.Accepted(target)
	}
	ps := protectedFor(o.Grants.Grants, o.RepoRoot, o.Home, filepath.Join(o.Root, "extensions", "amq-squad")+"/", "~/.amq-squad/")
	cross := ps.overlaps(added)
	if len(o.ProtectedAdded) > 0 {
		protected, err := task.CleanDeclaredFiles(o.ProtectedAdded)
		if err != nil {
			d.ReasonCode, d.Detail = "widen_invalid", "the resolved protected paths are invalid: "+err.Error()
			return d
		}
		addedSet, seen := make(map[string]bool, len(added)), make(map[string]bool, len(cross))
		for _, g := range added {
			addedSet[g] = true
		}
		for _, g := range cross {
			seen[g] = true
		}
		for _, g := range protected {
			if !addedSet[g] {
				d.ReasonCode, d.Detail = "widen_invalid", fmt.Sprintf("resolved protected path %q is not in the widened paths", g)
				return d
			}
			if !seen[g] {
				cross, seen[g] = append(cross, g), true
			}
		}
		sort.Strings(cross)
	}
	if len(cross) > 0 {
		t := ProtectedTarget(o.Root, o.Task.ID, cross)
		d.Crossings = append(d.Crossings, Crossing{Limit: LimitProtected, Target: t, Globs: cross, Accepted: accepted(t),
			Detail: "the widened paths " + strings.Join(cross, ", ") + " cover protected paths of the grant"})
	}
	if o.Commit != nil {
		proposed, err := task.CleanDeclaredFiles(append(append([]string(nil), o.Task.DeclaredFiles...), added...))
		if err != nil {
			d.ReasonCode, d.Detail = "widen_invalid", err.Error()
			return d
		}
		co := *o.Commit
		dryTask := o.Task
		if dryTask.Report == nil && co.Handle != "" {
			dryTask.Report = &task.Report{By: co.Handle}
		}
		co.Task, co.DryRun, co.Declared, co.Root = dryTask, true, proposed, o.Root
		co.Grants, co.GrantsErr, co.Home, co.Gates = o.Grants, nil, o.Home, o.Gates
		res := Run(co)
		d.DryRun = &res
		if res.Deferred {
			d.ReasonCode, d.Detail = "widen_check_failed", "the dry run of the commit step failed: "+res.Detail
			return d
		}
		// Only a clean dry run, or one refused for the diff policy the
		// violations below turn into crossings, can allow a widening; every
		// other refusal (an unverified clear, a missing base, a tampered
		// ledger, ...) refuses it with its own code.
		if !res.OK && !diffPolicyRefusal(res) {
			d.ReasonCode, d.Detail = firstNonEmpty(res.ReasonCode, "widen_check_failed"), res.Detail
			return d
		}
		for _, v := range res.Violations {
			switch v.Reason {
			case ReasonSizeCap:
				t := SizeTarget(o.Root, o.Task.ID, res.Lines, res.Files)
				d.Crossings = append(d.Crossings, Crossing{Limit: LimitSize, Target: t, Accepted: accepted(t), Detail: v.Detail})
			case ReasonOutside:
				d.Notes = append(d.Notes, "still outside the widened set: "+strings.Join(v.Paths, ", "))
			case ReasonProtected:
				// A managed-block change is a protected-path growth crossing
				// (P2 of the milestone B first pass): the gate is raised for
				// those exact files, never a widening that succeeds while the
				// commit stays refused.
				if len(v.Managed) > 0 && !coveredBy(d.Crossings, v.Managed) {
					t := ProtectedTarget(o.Root, o.Task.ID, v.Managed)
					d.Crossings = append(d.Crossings, Crossing{Limit: LimitProtected, Target: t, Globs: v.Managed, Accepted: accepted(t),
						Detail: "the seat's diff changes the managed block of " + strings.Join(v.Managed, ", ") + ", a protected path of the grant"})
				}
				if len(v.Paths) > len(v.Managed) {
					d.Notes = append(d.Notes, "the commit step will still refuse: "+describe([]Violation{v}))
				}
			default:
				d.Notes = append(d.Notes, "the commit step will still refuse: "+describe([]Violation{v}))
			}
		}
	}
	var open []string
	for _, c := range d.Crossings {
		if c.Accepted {
			d.Gates = append(d.Gates, c.Target)
		} else {
			open = append(open, c.Detail)
		}
	}
	if len(open) > 0 {
		d.Gates = nil
		d.ReasonCode = ReasonGrowthGate
		d.Detail = "the widening crosses a grant limit and needs the owner's growth gate: " + strings.Join(open, "; ")
		if o.Grants.Insecure {
			d.Detail += " (the owner grants file is insecure: no gate opens, " + grants.ReasonInsecure + ")"
		}
		return d
	}
	d.Allowed = true
	return d
}

// diffPolicyRefusal reports whether a dry run was refused by the diff policy:
// it carries violations and its code is the first violation's (what Run sets).
// Only those refusals are turned into crossings; any other refusal, one that
// carries violations under another code included, refuses the widening.
func diffPolicyRefusal(res Result) bool {
	return len(res.Violations) > 0 && res.ReasonCode == res.Violations[0].Reason
}
