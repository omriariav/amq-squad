package mergetrain

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// The lead's recorded re-bind (brief 3.2.0-3a item 6): #894's recovery is not
// the general rule of reconcileUpdates, since the code that moved its branch
// wrote no update_requested entry. It is a lead decision through the
// scheduler's decision socket, proved from the lead's pane like task widen,
// and the train verifies the proof itself before it binds: exactly one force
// push from the bound head to the named head, the named head's first parent
// a base tip the train rebased onto (a journaled request's base, a real
// run's batch base that listed the PR at the bound head, or the base's
// current tip), and the patch ids equal. Any failing part is a refusal and
// nothing is written.

// RebindInput is one lead re-bind decision as the scheduler applies it.
type RebindInput struct {
	Forge  Forge
	Audit  Audit
	Tasks  task.Store
	Binder Binder
	PR     int
	From   string // the bound head
	To     string // the head the train's unrecorded update produced
	Lead   string
	Now    func() time.Time
}

// RebindResult is the proof the binding was written on.
type RebindResult struct {
	Run        string `json:"run"`
	Task       string `json:"task"`
	Repository string `json:"repository"`
	Base       string `json:"base"`
	PR         int    `json:"pr"`
	From       string `json:"from"`
	To         string `json:"to"`
	Parent     string `json:"parent"`
	ParentIs   string `json:"parent_is"`
	PatchID    string `json:"patch_id"`
	Push       string `json:"force_push"`
}

// Refusal codes of a re-bind decision.
const (
	RefusedRebindInvalid  = "rebind_invalid"
	RefusedRebindUnbound  = "rebind_unbound"
	RefusedRebindHead     = "rebind_head_mismatch"
	RefusedRebindUnproved = "rebind_unproved"
	RefusedRebindFailed   = "rebind_failed"
)

// RebindRefusal is a re-bind the train refused, with its code and the part
// that failed in words.
type RebindRefusal struct{ Code, Detail string }

func (e *RebindRefusal) Error() string { return "rebind refused (" + e.Code + "): " + e.Detail }

var rebindSHARe = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Rebind verifies the proof and writes the binding: the task bound to the
// PR at From (exactly one), the PR's head read as To, the proof
// (proveUpdate), then the rebase and rebind audit entries and the binding
// write. The audit entries carry a run id of their own.
func Rebind(ctx context.Context, in RebindInput) (RebindResult, error) {
	if in.PR < 1 || !rebindSHARe.MatchString(in.From) || !rebindSHARe.MatchString(in.To) || in.From == in.To {
		return RebindResult{}, &RebindRefusal{Code: RefusedRebindInvalid, Detail: "a re-bind names a PR number and two different full lowercase head SHAs (--from the bound head, --to the head the update produced)"}
	}
	if in.Forge == nil || in.Binder == nil {
		return RebindResult{}, &RebindRefusal{Code: RefusedRebindFailed, Detail: "no forge or no task store to read and write the binding"}
	}
	all, err := task.Snapshot(in.Tasks.Dir)
	if err != nil {
		return RebindResult{}, &RebindRefusal{Code: RefusedRebindFailed, Detail: "read task store: " + err.Error()}
	}
	var bound []task.Task
	for _, t := range all {
		if t.PRNumber == in.PR && t.PRHead == in.From {
			bound = append(bound, t)
		}
	}
	if len(bound) != 1 {
		return RebindResult{}, &RebindRefusal{Code: RefusedRebindUnbound, Detail: fmt.Sprintf("%d tasks are bound to PR #%d at head %s; a re-bind moves exactly one task's binding from the head it holds", len(bound), in.PR, short(in.From))}
	}
	tk := bound[0]
	repository := tk.PRRepository
	cur, err := in.Forge.PR(ctx, repository, in.PR)
	if err != nil {
		return RebindResult{}, &RebindRefusal{Code: RefusedRebindFailed, Detail: fmt.Sprintf("read PR #%d: %v", in.PR, err)}
	}
	if cur.HeadSHA != in.To {
		return RebindResult{}, &RebindRefusal{Code: RefusedRebindHead, Detail: fmt.Sprintf("PR #%d's head is %s, not the named head %s; nothing is bound", in.PR, short(cur.HeadSHA), short(in.To))}
	}
	entries, err := in.Audit.Read()
	if err != nil {
		return RebindResult{}, &RebindRefusal{Code: RefusedRebindFailed, Detail: "read audit log: " + err.Error()}
	}
	tip, err := in.Forge.BranchHead(ctx, repository, cur.BaseRef)
	if err != nil {
		return RebindResult{}, &RebindRefusal{Code: RefusedRebindFailed, Detail: fmt.Sprintf("read %s head: %v", cur.BaseRef, err)}
	}
	bases := journaledBases(entries, repository, in.PR, in.From)
	bases = append(bases, baseCandidate{tip, "the base's current tip"})
	proof, why, err := proveUpdate(ctx, in.Forge, repository, cur, in.From, in.To, bases, "", "")
	if err != nil {
		return RebindResult{}, &RebindRefusal{Code: RefusedRebindFailed, Detail: err.Error()}
	}
	if why != "" {
		return RebindResult{}, &RebindRefusal{Code: RefusedRebindUnproved, Detail: why}
	}
	now := time.Now
	if in.Now != nil {
		now = in.Now
	}
	res := RebindResult{Run: rebindRunID(now()), Task: tk.ID, Repository: repository, Base: cur.BaseRef, PR: in.PR, From: in.From, To: in.To,
		Parent: proof.Parent, ParentIs: proof.ParentIs, PatchID: proof.OldID, Push: proof.Push.Before + " -> " + proof.Push.After + " by " + orDash(proof.Push.Actor)}
	detail := fmt.Sprintf("recovered by the lead %s's rebind decision: the train's update moved the branch and was not recorded; first parent %s is %s; one force push %s", orDash(in.Lead), short(proof.Parent), proof.ParentIs, res.Push)
	write := func(e Entry) error {
		e.Run, e.Repository, e.Base = res.Run, repository, cur.BaseRef
		return in.Audit.Append(e)
	}
	// audit first (the rebase, then the rebind), then the binding write
	if err := write(Entry{Kind: "rebase", PR: in.PR, Task: tk.ID, Head: in.From, NewHead: in.To, BaseSHA: proof.Parent, Detail: detail}); err != nil {
		return RebindResult{}, &RebindRefusal{Code: RefusedRebindFailed, Detail: "audit log: " + err.Error()}
	}
	if err := write(Entry{Kind: "rebind", PR: in.PR, Task: tk.ID, Head: in.From, NewHead: in.To, Detail: "patch-id: " + proof.OldID + "\nfirst parent: " + proof.Parent + "\n" + detail}); err != nil {
		return RebindResult{}, &RebindRefusal{Code: RefusedRebindFailed, Detail: "audit log: " + err.Error()}
	}
	// The one admission check before the write (the plan reviewer on
	// #903): this verb is the re-admission, so a superseded head is bound
	// here and recorded as re-admitted after the superseded attempt.
	admitted := headAdmitted(entries, repository, in.PR, in.To)
	// The write holds only while the binding is still at From, checked under
	// the store's lock (Codex on #903): a train run that advanced the binding
	// since the snapshot above keeps its head and its merge fact. The two
	// entries above stay journaled; reconcileRebinds applies a rebind entry
	// only to a task still bound at its Head, so they bind nothing later.
	if err := in.Binder.BindPRFrom(tk.ID, repository, in.PR, in.From, in.To); err != nil {
		if errors.Is(err, task.ErrPRBindingMismatch) {
			// The terminal entry (the plan reviewer on #903): the rebase
			// entry above would otherwise keep the named head in trainHeads,
			// and a later run would take a push of exactly that head for the
			// train's own; headAdmitted refuses a head whose latest terminal
			// entry is this one, on every path that trusts or binds it.
			why := fmt.Sprintf("the binding moved while the re-bind was proved: %v; nothing is bound", err)
			if aerr := write(Entry{Kind: "rebind_superseded", PR: in.PR, Task: tk.ID, Head: in.From, NewHead: in.To, Detail: why}); aerr != nil {
				return RebindResult{}, &RebindRefusal{Code: RefusedRebindFailed, Detail: why + "; and the audit log refused the superseded entry: " + aerr.Error()}
			}
			return RebindResult{}, &RebindRefusal{Code: RefusedRebindHead, Detail: why + " (the proof's entries and the superseded entry are journaled under run " + res.Run + ")"}
		}
		return RebindResult{}, &RebindRefusal{Code: "binding_failed", Detail: fmt.Sprintf("task %s to %s: %v", tk.ID, short(in.To), err)}
	}
	// The re-admission (the plan reviewer on #903): a head a superseded
	// re-bind had taken out of trainHeads comes back only through this
	// verb's applied binding, recorded as the terminal rebind_readmitted
	// entry under this run; headAdmitted reads the latest terminal entry
	// of a head. The binding is written, so a failed append is reported in
	// the result's words, not as a refusal.
	readmission := "the lead's re-bind bound this head; it is the train's own"
	if !admitted {
		readmission += " again, after a superseded attempt"
	}
	if err := write(Entry{Kind: "rebind_readmitted", PR: in.PR, Task: tk.ID, Head: in.From, NewHead: in.To, Detail: readmission}); err != nil {
		res.Push += " (the readmission entry was not written: " + err.Error() + ")"
	}
	return res, nil
}

// journaledBases are the base tips the train pinned when it worked on the PR
// at the bound head: every real-run update_requested for that head, and
// every real-run batch whose pairs list the PR at that head (the old code
// journaled no request; its batch entry names the base it rebased onto, as
// run ab184d07's does for #894 at 87fb71e9).
func journaledBases(entries []Entry, repository string, pr int, head string) []baseCandidate {
	var out []baseCandidate
	seen := map[string]bool{}
	add := func(sha, which string) {
		if sha != "" && !seen[sha] {
			seen[sha] = true
			out = append(out, baseCandidate{sha, which})
		}
	}
	for _, e := range entries {
		if e.DryRun || e.Repository != repository {
			continue
		}
		switch {
		case e.Kind == "update_requested" && e.PR == pr && e.Head == head:
			add(e.BaseSHA, "the base tip journaled by run "+e.Run)
		case e.Kind == "batch":
			for _, p := range e.Pairs {
				if p.Number == pr && p.Head == head {
					add(e.BaseSHA, "the base tip of run "+e.Run+"'s batch")
				}
			}
		}
	}
	return out
}

func rebindRunID(now time.Time) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "mt-rebind-" + now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b[:])
}

// RebindPlain is a re-bind refusal's plain line by its code, for the verb.
func RebindPlain(code string) string {
	switch code {
	case RefusedRebindInvalid:
		return "the re-bind does not name a PR and two full head SHAs"
	case RefusedRebindUnbound:
		return "no one task is bound to the PR at the head named as the bound head"
	case RefusedRebindHead:
		return "the PR's head, or the task's binding, is not where the re-bind names it"
	case RefusedRebindUnproved:
		return "the named head is not proved to be the train's update of the bound head; nothing is bound"
	case "binding_failed":
		return "the proof held but the binding could not be written"
	}
	return "the re-bind was refused"
}
