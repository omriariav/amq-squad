package seatcommit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/atomicfile"
	"github.com/omriariav/amq-squad/v3/internal/flock"
	"github.com/omriariav/amq-squad/v3/internal/seatexec"
)

// The commit ledger is the core's record of a seat branch: the base commit
// the core saw when it launched the seat, and every harness commit since.
// It lives under <root>/extensions/amq-squad/commits/ (the guard's
// protected extension dir), never in the repository: everything under .git
// is writable by a same-UID seat (a branch ref, a base ref, a forged
// "harness" commit), so the diff base the commit step checks against is
// read only from here.

// LedgerSchema is the ledger file schema.
const LedgerSchema = 1

// Ledger is one seat's commit ledger.
type Ledger struct {
	Schema   int            `json:"schema"`
	Handle   string         `json:"handle"`
	Worktree string         `json:"worktree"`
	Branch   string         `json:"branch"`
	Base     string         `json:"base"`           // the worktree HEAD at the seat's first launch
	Head     string         `json:"head,omitempty"` // the last harness commit, "" before the first
	Commits  []LedgerCommit `json:"commits"`
	At       time.Time      `json:"at"`
	// Quarantine is the evidence of a global_config_changed block: the
	// seat's tree as it stood (never committed or pushed), its diff digest
	// and the config diff. Nothing of the seat's work is committed until the
	// owner clears it, and then only that same tree.
	Quarantine *LedgerQuarantine `json:"quarantine,omitempty"`
}

// LedgerQuarantine is one quarantine of a seat's branch.
type LedgerQuarantine struct {
	Task       string                  `json:"task"`
	Parent     string                  `json:"parent,omitempty"`
	Tree       string                  `json:"tree,omitempty"`
	DiffDigest string                  `json:"diff_digest,omitempty"`
	Snapshot   string                  `json:"snapshot_error,omitempty"` // why no tree was kept
	Config     []seatexec.ConfigChange `json:"config_changes"`
	At         time.Time               `json:"at"`
	ClearedAt  *time.Time              `json:"cleared_at,omitempty"`
	ClearedBy  string                  `json:"cleared_by,omitempty"`
}

// ClearQuarantine records the owner's clear on the seat's ledger; the next
// commit step for that task commits the kept tree only.
func ClearQuarantine(root, handle, by string, now time.Time) (Ledger, error) {
	var out Ledger
	err := withLedgerLock(root, handle, func() error {
		l, err := ReadLedger(root, handle)
		if err != nil {
			return err
		}
		if l.Quarantine == nil || l.Quarantine.ClearedAt != nil {
			out = l
			return nil
		}
		at := now.UTC()
		l.Quarantine.ClearedAt, l.Quarantine.ClearedBy = &at, by
		out = l
		return writeLedger(root, l)
	})
	return out, err
}

// LedgerCommit is one harness commit.
type LedgerCommit struct {
	Task   string    `json:"task"`
	Commit string    `json:"commit"`
	Parent string    `json:"parent"`
	Tree   string    `json:"tree"`
	At     time.Time `json:"at"`
	Pushed bool      `json:"pushed"`
}

// LedgerDir is the ledger directory under an AMQ session root.
func LedgerDir(root string) string { return filepath.Join(root, "extensions", "amq-squad", "commits") }

func ledgerPath(root, handle string) string { return filepath.Join(LedgerDir(root), handle+".json") }

// Parent is the commit the seat's next diff is taken against.
func (l Ledger) Parent() string {
	if l.Head != "" {
		return l.Head
	}
	return l.Base
}

// ReadLedger loads a seat's ledger; os.ErrNotExist when there is none.
func ReadLedger(root, handle string) (Ledger, error) {
	var l Ledger
	raw, err := os.ReadFile(ledgerPath(root, handle))
	if err != nil {
		return l, err
	}
	if err := json.Unmarshal(raw, &l); err != nil {
		return l, fmt.Errorf("commit ledger %s: %w", handle, err)
	}
	if l.Schema != LedgerSchema || l.Handle != handle {
		return l, fmt.Errorf("commit ledger %s: schema %d for %q", handle, l.Schema, l.Handle)
	}
	return l, nil
}

func writeLedger(root string, l Ledger) error {
	l.Schema = LedgerSchema
	raw, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	dir := LedgerDir(root)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// One durable writer (2b-5): temp in the directory, fsync, rename, fsync.
	return atomicfile.Write(ledgerPath(root, l.Handle), append(raw, '\n'), 0o600)
}

// withLedgerLock serializes the commit step for one seat (the scheduler's
// run and a lead's manual `seat commit`).
func withLedgerLock(root, handle string, fn func() error) error {
	if err := os.MkdirAll(LedgerDir(root), 0o700); err != nil {
		return err
	}
	return flock.WithLock(filepath.Join(LedgerDir(root), handle+".lock"), fn)
}

// RecordBase writes a seat's ledger with its base commit when there is none
// for this worktree and branch (the launch calls it before the seat runs, so
// the base is the core's observation, not the seat's). An existing ledger for
// the same worktree and branch is kept: a relaunch continues from the last
// harness commit. It returns the ledger in effect.
func RecordBase(root, handle, worktree, branch, base string, now time.Time) (Ledger, error) {
	if handle == "" || worktree == "" || branch == "" || len(strings.TrimSpace(base)) < 40 {
		return Ledger{}, fmt.Errorf("record base: handle, worktree, branch and a full commit id are required")
	}
	var out Ledger
	err := withLedgerLock(root, handle, func() error {
		l, err := ReadLedger(root, handle)
		if err == nil && l.Worktree == worktree && l.Branch == branch {
			out = l
			return nil
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		out = Ledger{Handle: handle, Worktree: worktree, Branch: branch, Base: strings.TrimSpace(base), Commits: []LedgerCommit{}, At: now.UTC()}
		return writeLedger(root, out)
	})
	return out, err
}

// RecordWorktreeBase reads the worktree's HEAD commit and records it as the
// seat's base (RecordBase). A worktree that is not a git checkout records
// nothing and returns an error the caller may ignore: the commit step then
// refuses with base_unrecorded.
func RecordWorktreeBase(gitBin, root, handle, worktree, branch string, now time.Time) (Ledger, error) {
	if gitBin == "" {
		gitBin = "git"
	}
	g := git{bin: gitBin, dir: worktree, env: harnessEnv(os.Environ())}
	head, err := g.line("rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return Ledger{}, err
	}
	return RecordBase(root, handle, worktree, branch, head, now)
}
