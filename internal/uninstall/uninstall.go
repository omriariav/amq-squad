// Package uninstall is `amq-squad uninstall` (phase 5, plan 16.4): a dry run
// by default that names every path it would remove, grouped, and the
// commands it will not run; --write removes, the owner files only with
// --owner-files as well, and the sealed ledgers and evidence only with
// --purge. It refuses while anything runs (uninstall_busy), never writes the
// owner's global agent config (R16: it prints the commands and the entries
// for the owner), removes secrets first (overwritten once with zeros, then
// unlinked; best effort on APFS), and removes everything through the
// fsanchor descriptor walk from anchored roots: a symlink in place of a
// directory it expects is uninstall_refused_symlink with nothing removed.
// It is idempotent, and a partial failure leaves a report of what remains.
package uninstall

import (
	"fmt"
	"time"
)

// Error codes.
const (
	CodeBusy    = "uninstall_busy"
	CodeSymlink = "uninstall_refused_symlink"
	CodePartial = "uninstall_partial"
)

// Error is a coded refusal or failure.
type Error struct{ Code, Detail string }

func (e *Error) Error() string { return e.Code + ": " + e.Detail }

// Options are one run's inputs. Without Write nothing is changed.
type Options struct {
	Root       string // the AMQ session root, absolute and clean
	Home       string // the owner's home (~/.amq-squad, ~/.codex)
	CodexHome  string // $CODEX_HOME, "" for ~/.codex
	Git        string // the git binary for --purge ("" is git on PATH)
	Write      bool
	OwnerFiles bool // with Write: also the owner files under ~/.amq-squad
	Purge      bool // with Write: also the sealed ledgers, evidence refs, clear records and gate store
	Now        func() time.Time
}

// Group names, in the order they print and (but for the secrets) run.
const (
	GroupSession  = "session"
	GroupSeatCfg  = "seatcfg"
	GroupTools    = "tools"
	GroupWorktree = "worktrees"
	GroupOwner    = "owner"
	GroupSealed   = "sealed"
	GroupEvidence = "evidence"
	GroupGate     = "gate"
)

// Entry is one path (or evidence ref) in a group.
type Entry struct {
	Path   string
	Note   string
	Secret bool
}

// Group is one printed group.
type Group struct {
	ID      string
	Title   string
	Status  string // what happens to its entries under the flags given
	Entries []Entry
}

// Result is what a run did or would do. Removed and Remaining are paths.
type Result struct {
	Root        string
	Written     bool
	NothingToDo bool
	Groups      []Group
	ByHand      []string
	Left        []string // entries of ~/.amq-squad this verb does not own
	NotRemoved  []string // worktrees listed but not removed, with the reason
	Notes       []string // what a run did differently from the plan (a hard-linked secret)
	Removed     []string
	Remaining   []string
	Failures    []string
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// Run builds the plan and, with Write, executes it. A refusal (busy, symlink)
// returns the Result so far with the error and removes nothing. A writing run
// holds the scheduler's own lock for its whole length (it only probed it
// before), so a scheduler cannot start and a second uninstall is refused
// while it runs; the busy check runs again with the lock held.
func Run(o Options) (Result, error) {
	if o.Root == "" || o.Home == "" {
		return Result{}, fmt.Errorf("uninstall: the session root and the home are required")
	}
	res := Result{Root: o.Root, Written: o.Write}
	p, err := discover(o)
	if err != nil {
		return res, err
	}
	res.fill(p, o)
	var held *schedLock
	if o.Write && !res.NothingToDo {
		var why string
		if held, why = takeSchedulerLock(o); why != "" {
			return res, &Error{Code: CodeBusy, Detail: why}
		}
		defer held.release()
		// with the lock held, plan again: nothing changed since is trusted
		if p, err = discover(o); err != nil {
			return res, err
		}
		res = Result{Root: o.Root, Written: o.Write}
		res.fill(p, o)
	}
	if live := busy(o, held != nil); len(live) > 0 {
		return res, &Error{Code: CodeBusy, Detail: joinList(live)}
	}
	if !o.Write || res.NothingToDo {
		return res, nil
	}
	return res, execute(p, o, &res)
}

func joinList(list []string) string {
	out := ""
	for i, s := range list {
		if i > 0 {
			out += "; "
		}
		out += s
	}
	return out
}
