package seatcommit

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/atomicfile"
	"github.com/omriariav/amq-squad/v3/internal/flock"
	"github.com/omriariav/amq-squad/v3/internal/grants"
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
	// Discarded are the trees CleanSeat took off the worktree before a new
	// lease (a refused, failed or expired attempt's leftovers), kept as
	// evidence.
	Discarded []LedgerDiscard `json:"discarded,omitempty"`
	// Repository, BaseBranch and PushURL are the launch's record of the
	// owner's grant (R2 of the milestone B first pass): the merge grant's
	// repository and base branch and the URL the harness pushes the seat
	// branch to. The commit step reads them from here, never from the seat's
	// repository (whose remote.origin.pushurl the seat can rewrite).
	Repository string `json:"repository,omitempty"`
	BaseBranch string `json:"base_branch,omitempty"`
	PushURL    string `json:"push_url,omitempty"`
}

// Target is the part of the owner's grant the launch records for a seat's
// commit step.
type Target struct {
	Repository string `json:"repository,omitempty"`
	BaseBranch string `json:"base_branch,omitempty"`
	PushURL    string `json:"push_url,omitempty"`
}

// Target is the ledger's recorded grant.
func (l Ledger) Target() Target {
	return Target{Repository: l.Repository, BaseBranch: l.BaseBranch, PushURL: l.PushURL}
}

// TargetFor is the launch's reading of the owner's grant: the one merge
// grant of the squad's repository (squad.yaml's merge_repositories narrows
// a fleet's list to it) with its base branch and push URL; with no single
// merge grant, the grant's default branch and no push (the commit step then
// commits and does not push).
func TargetFor(g grants.Resolved) Target {
	t := Target{BaseBranch: firstNonEmpty(g.DefaultBranch, "main")}
	if g.Insecure || len(g.Merge) != 1 {
		return t
	}
	m := g.Merge[0]
	t.Repository, t.PushURL = m.Repository, m.PushURL
	t.BaseBranch = firstNonEmpty(m.BaseBranch, t.BaseBranch)
	return t
}

// LedgerDiscard is one worktree diff CleanSeat discarded.
type LedgerDiscard struct {
	// TaskID and Reason are the seat's last ended attempt: its task and why
	// it ended (for a refusal, commit_refused and the refusal's reason code),
	// so the refused work stays inspectable.
	TaskID     string    `json:"task_id,omitempty"`
	Reason     string    `json:"reason"`
	Parent     string    `json:"parent"`
	Tree       string    `json:"tree"`
	DiffDigest string    `json:"diff_digest"`
	At         time.Time `json:"at"`
}

// LedgerQuarantine is one quarantine of a seat's branch.
type LedgerQuarantine struct {
	Task       string                  `json:"task"`
	Reason     string                  `json:"reason,omitempty"` // global_config_changed | claude_config_changed
	Parent     string                  `json:"parent,omitempty"`
	Tree       string                  `json:"tree,omitempty"`
	DiffDigest string                  `json:"diff_digest,omitempty"`
	Snapshot   string                  `json:"snapshot_error,omitempty"` // why no tree was kept
	Config     []seatexec.ConfigChange `json:"config_changes"`
	At         time.Time               `json:"at"`
	ClearedAt  *time.Time              `json:"cleared_at,omitempty"`
	ClearedBy  string                  `json:"cleared_by,omitempty"`
	// Clear is the owner's signed clear record (R1 of the milestone B first
	// pass): a clear counts only when its signature verifies against a
	// trusted key of the owner's trust store.
	Clear *ClearRecord `json:"clear,omitempty"`
}

// ClearSigner signs the owner's clear (the core's Ed25519 issuer key,
// operatorauth.LoadEd25519Signer); ClearVerifier verifies it (the owner's
// trust store, operatorauth.AuthorizationTrustStore).
type (
	ClearSigner interface {
		KeyID() string
		Sign([]byte) ([]byte, error)
	}
	ClearVerifier interface {
		Verify(keyID string, payload, signature []byte) error
	}
)

// ClearSchema versions ClearRecord.
const ClearSchema = 1

// ClearRecord is the owner's signed clear of one quarantine: it names the
// seat, the task, the kept tree and diff digest and the quarantine's time,
// so a signature cannot be moved to another quarantine.
type ClearRecord struct {
	Schema        int       `json:"schema"`
	Seat          string    `json:"seat"`
	Task          string    `json:"task"`
	Tree          string    `json:"tree,omitempty"`
	DiffDigest    string    `json:"diff_digest,omitempty"`
	QuarantinedAt time.Time `json:"quarantined_at"`
	ClearedAt     time.Time `json:"cleared_at"`
	KeyID         string    `json:"key_id"`
	Signature     string    `json:"signature,omitempty"` // base64 Ed25519 over Payload()
}

// Payload is the signed bytes: the record without its signature.
func (c ClearRecord) Payload() []byte {
	c.Signature = ""
	raw, _ := json.Marshal(c)
	return append([]byte("amq-squad seat clear\n"), raw...)
}

// Verified reports whether the clear is the owner's signed record for this
// seat's quarantine q.
func (c *ClearRecord) Verified(seat string, q *LedgerQuarantine, v ClearVerifier) bool {
	if c == nil || v == nil || q == nil || q.ClearedAt == nil || c.Schema != ClearSchema || c.Seat != seat || c.Task != q.Task ||
		c.Tree != q.Tree || c.DiffDigest != q.DiffDigest || !c.QuarantinedAt.Equal(q.At) || !c.ClearedAt.Equal(*q.ClearedAt) {
		return false
	}
	sig, err := base64.StdEncoding.DecodeString(c.Signature)
	return err == nil && v.Verify(c.KeyID, c.Payload(), sig) == nil
}

// ClearQuarantine records the owner's signed clear on the seat's ledger;
// the next commit step for that task commits the kept tree only. Without a
// signer nothing is cleared.
func ClearQuarantine(root, handle string, signer ClearSigner, now time.Time) (Ledger, error) {
	var out Ledger
	if signer == nil {
		return out, fmt.Errorf("seat clear needs the owner's signing key")
	}
	err := withLedgerLock(root, handle, func() error {
		l, err := ReadLedger(root, handle)
		if err != nil {
			return err
		}
		if l.Quarantine == nil || l.Quarantine.ClearedAt != nil {
			out = l
			return nil
		}
		q := l.Quarantine
		at := now.UTC()
		c := ClearRecord{Schema: ClearSchema, Seat: handle, Task: q.Task, Tree: q.Tree, DiffDigest: q.DiffDigest, QuarantinedAt: q.At, ClearedAt: at, KeyID: signer.KeyID()}
		sig, err := signer.Sign(c.Payload())
		if err != nil {
			return fmt.Errorf("sign the clear: %w", err)
		}
		c.Signature = base64.StdEncoding.EncodeToString(sig)
		q.ClearedAt, q.ClearedBy, q.Clear = &at, "owner ("+c.KeyID+")", &c
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
	return recordBase(root, handle, worktree, branch, base, nil, now)
}

func recordBase(root, handle, worktree, branch, base string, target *Target, now time.Time) (Ledger, error) {
	if handle == "" || worktree == "" || branch == "" || len(strings.TrimSpace(base)) < 40 {
		return Ledger{}, fmt.Errorf("record base: handle, worktree, branch and a full commit id are required")
	}
	var out Ledger
	err := withLedgerLock(root, handle, func() error {
		l, err := ReadLedger(root, handle)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err != nil || l.Worktree != worktree || l.Branch != branch {
			l = Ledger{Handle: handle, Worktree: worktree, Branch: branch, Base: strings.TrimSpace(base), Commits: []LedgerCommit{}, At: now.UTC()}
		} else if target == nil || l.Target() == *target {
			out = l
			return nil
		}
		if target != nil {
			// Each launch records the owner's grant as it stands (R2).
			l.Repository, l.BaseBranch, l.PushURL = target.Repository, target.BaseBranch, target.PushURL
		}
		out = l
		return writeLedger(root, l)
	})
	return out, err
}

// RecordTarget records the owner's grant for an existing ledger (seat
// commit --base, the lead's base decision, records it with the base).
func RecordTarget(root, handle string, target Target) (Ledger, error) {
	var out Ledger
	err := withLedgerLock(root, handle, func() error {
		l, err := ReadLedger(root, handle)
		if err != nil {
			return err
		}
		l.Repository, l.BaseBranch, l.PushURL = target.Repository, target.BaseBranch, target.PushURL
		out = l
		return writeLedger(root, l)
	})
	return out, err
}

// RecordWorktreeBase reads the commit the worktree's branch names and
// records it as the seat's base (RecordBase), with the launch's record of
// the owner's grant (target). The worktree's HEAD is read from git's files
// and resolved in the repository with the hardened git (R2). A worktree that
// is not a git checkout records nothing and returns an error the caller may
// ignore: the commit step then refuses with base_unrecorded.
func RecordWorktreeBase(gitBin, root, handle, worktree, branch string, target Target, now time.Time) (Ledger, error) {
	if gitBin == "" {
		gitBin = "git"
	}
	rp, err := worktreePaths(worktree)
	if err != nil {
		return Ledger{}, err
	}
	if rp.HeadRef == "" {
		return Ledger{}, fmt.Errorf("the worktree %s has a detached HEAD", worktree)
	}
	head, err := seatRepo(gitBin, harnessEnv(os.Environ()), rp).line("rev-parse", "--verify", rp.HeadRef+"^{commit}")
	if err != nil {
		return Ledger{}, err
	}
	return recordBase(root, handle, worktree, branch, head, &target, now)
}
