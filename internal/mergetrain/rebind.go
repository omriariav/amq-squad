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
	// The write goes through the one admission (bindAdmitted): one critical
	// section under the journal lock (Audit.AuditLockPath) on a fresh read
	// of the journal. This verb is the re-admission, so a superseded head
	// is bound here, and whether it had been superseded words the
	// readmission. The plan reviewer's ruling on the order inside the
	// section: the terminal rebind_readmitted entry first (the journal
	// before the mutation, as update_requested before the update), then the
	// conditional write, which holds only while the binding is still at
	// From, checked under the store's lock (Codex on #903): a train run
	// that advanced the binding since the snapshot above keeps its head and
	// its merge fact. A write that fails there (the binding moved, or a
	// store error before its commit point) is undone in the same section by
	// a terminal rebind_superseded entry with the reason rebind_failed, so
	// the journal shows the head not admitted, but only on a definite
	// outcome: the store's read under its lock succeeds and shows the
	// binding not at To; a read that fails or shows To leaves the
	// readmission standing and the verb stops with the outcome not known; a write past the commit
	// point (task.CommittedError) is durable and counts as written. A
	// binding already at the target (a train's recovery bound the same head
	// first) is the store's read-only success. A crash between the readmission and the
	// write leaves the head admitted and unbound: the lead asked for it,
	// nothing binds it but bindAdmitted, and the verb run again binds it.
	// The rebase and rebind entries above stay journaled either way;
	// reconcileRebinds applies a rebind entry only to a task still bound at
	// its Head, through bindAdmitted.
	readmitted := func(admitted bool) Entry {
		readmission := "the lead's re-bind bound this head; it is the train's own"
		if !admitted {
			readmission += " again, after a superseded attempt"
		}
		return Entry{Kind: "rebind_readmitted", Run: res.Run, Repository: repository, Base: cur.BaseRef, PR: in.PR, Task: tk.ID, Head: in.From, NewHead: in.To, Detail: readmission}
	}
	undo := func(err error) Entry {
		return Entry{Kind: "rebind_superseded", Run: res.Run, Repository: repository, Base: cur.BaseRef, PR: in.PR, Task: tk.ID, Head: in.From, NewHead: in.To, Detail: "rebind_failed: " + bindFailure(err)}
	}
	_, err = bindAdmitted(in.Audit, in.Binder, nil, bindWrite{Task: tk.ID, Repository: repository, PR: in.PR, From: in.From, To: in.To, Conditional: true, Readmit: true, Readmitted: readmitted, Undo: undo})
	var committed *task.CommittedError
	switch {
	case err == nil:
	case errors.As(err, &committed):
		// the binding crossed the store's commit point and is durable; the
		// store replays its after-image at its next locked access
		res.Push += " (the binding is written; the task store's replay is pending: " + err.Error() + ")"
	case errors.Is(err, errReadmittedNotWritten):
		return RebindResult{}, &RebindRefusal{Code: RefusedRebindFailed, Detail: err.Error() + "; nothing is bound"}
	case errors.Is(err, ErrBindingOutcomeUnknown):
		return RebindResult{}, &RebindRefusal{Code: RefusedRebindFailed, Detail: err.Error() + " (the readmission is journaled under run " + res.Run + ")"}
	case errors.Is(err, task.ErrPRBindingMismatch):
		return RebindResult{}, &RebindRefusal{Code: RefusedRebindHead, Detail: bindFailure(err) + " (the proof's entries, the readmission and the superseded entry are journaled under run " + res.Run + ")"}
	default:
		return RebindResult{}, &RebindRefusal{Code: "binding_failed", Detail: fmt.Sprintf("task %s to %s: %s", tk.ID, short(in.To), bindFailure(err))}
	}
	return res, nil
}

// bindFailure is a failed re-bind write in words, for the refusal and for
// the undo entry's reason.
func bindFailure(err error) string {
	if errors.Is(err, task.ErrPRBindingMismatch) {
		return fmt.Sprintf("the binding moved while the re-bind was proved: %v; nothing is bound", err)
	}
	return fmt.Sprintf("the binding could not be written: %v; nothing is bound", err)
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
