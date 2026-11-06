package seatcommit

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/atomicfile"
	"github.com/omriariav/amq-squad/v3/internal/flock"
	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/registry"
	"github.com/omriariav/amq-squad/v3/internal/sealchain"
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
	Head     string         `json:"head,omitempty"` // a 3.0.1 seat branch's last harness commit (3.0.2: each commit is on its task's branch, Commits[].Branch)
	Commits  []LedgerCommit `json:"commits"`
	At       time.Time      `json:"at"`
	// Quarantine, Cleared and Seeded are the records of the quarantine that
	// brief 31-1 deleted (a config-drift block, the owner's signed clears, an
	// entry seeded by a clear). Nothing writes or honors them any more; they
	// stay in the struct only because the seal is computed over it, so a
	// ledger sealed before 3.1 still verifies, and the evidence ref of a
	// legacy quarantine is still checked and expired like any other.
	Quarantine *LedgerQuarantine  `json:"quarantine,omitempty"`
	Cleared    []LedgerQuarantine `json:"cleared,omitempty"`
	Seeded     bool               `json:"seeded,omitempty"`
	// Discarded are the trees CleanSeat took off the worktree before a new
	// lease (a refused, failed or expired attempt's leftovers), kept as
	// evidence.
	Discarded []LedgerDiscard `json:"discarded,omitempty"`
	// EvidenceSeq numbers the evidence refs (refs/amq-squad/evidence/<task>/<n>)
	// that anchor the kept trees in the seat repository (P2-5).
	EvidenceSeq int `json:"evidence_seq,omitempty"`
	// Target is the launch's record of the owner's grant (R2 of the
	// milestone B first pass, D1 and D2 of the second): the merge grant's
	// repository, base branch, push URL and local ack, and the pinned gh with
	// its digest. The commit step reads them from here, never from the seat's
	// repository (whose remote.origin.pushurl the seat can rewrite), and
	// refuses when they differ from the grant at commit time.
	Target
	// Rest is the commit the seat's worktree rests on between leases: the
	// base branch's tip CleanSeat read at the last lease boundary (one branch
	// per task, 3.0.2 t23). "" before the first boundary (Head, then Base).
	Rest string `json:"rest,omitempty"`
	// Lease is the current lease's start (StartTask): the task, its own
	// branch, the base tip recorded at its dispatch and the commit the
	// worktree was put on. CleanSeat clears it at the next lease boundary.
	Lease *LedgerLease `json:"lease,omitempty"`
	// Recovered is the lead's recovery this ledger version starts from
	// (3.2.0-0c-B, RecoverLedger): the quarantined seq and slot, the
	// digests of the refused copies and the entries carried. Only the first
	// link of a recovered chain carries it; the next write keeps it (the
	// chain's record of where it continued from).
	Recovered *LedgerRecovery `json:"recovered,omitempty"`
	// AlarmRecoveries are the lead's decisions by alarm, one record per
	// alarm id, appended and never replaced (the plan reviewer's ruling on
	// Codex's pass on 254f2c2c: a single field let an older alarm's late
	// decision replace a newer one's, stranding the newer hold). The
	// scheduler releases a hold when any record names its alarm. A chain
	// continuation starts a new body, which carries only its own decision's
	// record. Every later write keeps the list, as it keeps Recovered.
	AlarmRecoveries []LedgerRecovery `json:"alarm_recoveries,omitempty"`
	// Seal is the ledger's link in its chain (the ruling of the phase 5
	// re-pass): an HMAC tag keyed by the session seed over the ledger
	// without its seal, its seq and the previous version's tag; the signed
	// head under the routing directory names the link the ledger must be at.
	Seal *sealchain.Link `json:"seal,omitempty"`
}

// LedgerRecovery is the record of a lead's recovery in the new chain's
// first link (3.2.0-0c-B rule 3): the quarantined seq N the chain continues
// after, the slot's path, the sha256 of the quarantined body and head (the
// sidecar's digests, checked against the copies), who decided and why, and
// the entries carried as "task:commit".
type LedgerRecovery struct {
	Seq        int       `json:"quarantine_seq"`
	Slot       string    `json:"quarantine_path"`
	BodySHA256 string    `json:"body_sha256,omitempty"`
	HeadSHA256 string    `json:"head_sha256,omitempty"`
	By         string    `json:"by"`
	Reason     string    `json:"reason,omitempty"`
	At         time.Time `json:"at"`
	Carried    []string  `json:"carried,omitempty"`
	// Verified: the document verified when the lead decided (a transient
	// alarm, or a document put back); the record was sealed into the next
	// link of the same body, nothing restarted and nothing carried.
	Verified bool `json:"verified,omitempty"`
	// Link is the seq of the link that sealed this decision (the chain's
	// next link: the quarantined seq's, or the live chain's when Verified),
	// so a retry reports the link written, never one rebuilt from Seq (Codex
	// on b250b951). 0 in a record written before the field.
	Link int `json:"link,omitempty"`
	// Alarm is the scheduler's alarm id a decision by alarm released
	// (RecoverOptions.Alarm): set, the record names no slot and no seq; the
	// scheduler releases exactly that alarm's hold from it (the plan
	// reviewer's ruling on Codex's delta pass on c267382d).
	Alarm string `json:"alarm,omitempty"`
}

// ReasonLedgerTampered blocks a step whose commits ledger fails its tag or
// its chain: routed to the owner, never a silent pass.
const ReasonLedgerTampered = "ledger_tampered"

const ledgerLabel = "amq-squad commits ledger v1"

func ledgerHeadName(handle string) string { return "commits." + handle }

// Target is the part of the owner's grant the launch records for a seat's
// commit step, with the gh pin the launch verified (evidence).
type Target struct {
	Repository   string `json:"repository,omitempty"`
	BaseBranch   string `json:"base_branch,omitempty"`
	PushURL      string `json:"push_url,omitempty"`
	LocalPushAck bool   `json:"local_push_ack,omitempty"`
	// GhPath is the harness's copy of the pinned gh in the core's tools
	// directory (ToolsDir), GhPinPath the registry path it was copied from
	// (evidence): the push's credential helper and the PR calls run the copy.
	GhPath    string `json:"gh_path,omitempty"`
	GhSHA256  string `json:"gh_sha256,omitempty"`
	GhPinPath string `json:"gh_pin_path,omitempty"`
}

// ToolsDir is the core-written 0700 directory of the harness's copies of
// pinned tools: <root>/extensions/amq-squad/tools.
func ToolsDir(root string) string {
	return filepath.Join(root, "extensions", "amq-squad", "tools")
}

// grantPart is the target without the gh evidence: what must equal the
// grant at commit time.
func (t Target) grantPart() Target {
	t.GhPath, t.GhSHA256, t.GhPinPath = "", "", ""
	return t
}

// pushKindOf is grants.PushKind (a test seam: the httptest row serves its
// https stand-in on http://127.0.0.1).
var pushKindOf = grants.PushKind

// ReasonPushTargetUnacknowledged: a local push target without the owner's
// local_push_ack, or a recorded target that differs from the grant's at
// commit time (D2).
const ReasonPushTargetUnacknowledged = "push_target_unacknowledged"

// CheckTarget is the commit step's check of a target (D2): the push URL is an
// absolute URL of the admitted schemes (grants.PushKind), and a local one
// carries the owner's ack. An empty push URL is no push (push_url_missing).
func CheckTarget(t Target) error {
	if t.PushURL == "" {
		return nil
	}
	switch pushKindOf(t.PushURL) {
	case "":
		return fmt.Errorf("%s: the push URL %q is not an absolute URL of an admitted scheme", ReasonPushTargetUnacknowledged, t.PushURL)
	case grants.PushLocal:
		if !t.LocalPushAck {
			return fmt.Errorf("%s: the local push target %q has no server-side ruleset and the grant carries no local_push_ack", ReasonPushTargetUnacknowledged, t.PushURL)
		}
	case grants.PushHTTPS:
		if t.GhPath == "" || t.GhSHA256 == "" {
			return fmt.Errorf("%s: an https push needs the pinned gh as its credential helper (registry pins: gh)", registry.PinMissing)
		}
	}
	return nil
}

// LaunchTarget is the launch's reading of the grant with the gh pin (the
// registry's pins, re-hashed now): recorded for the push and the PR calls
// when pinned. It never checks the target (brief 31-1: CheckTarget runs in
// the commit step, where the push happens); its error is only a gh pin
// that is present and does not verify, and the target it returns then
// carries no gh. The verified gh is copied, from its hashed
// descriptor, into toolsDir (ToolsDir of the session root, core-written,
// 0700) and the copy re-hashed; the target names the copy (the fourth
// pass: git execs the helper by pathname, never the registry path).
func LaunchTarget(g grants.Resolved, home, toolsDir string) (Target, error) {
	t := TargetFor(g)
	reg, err := registry.Read(registry.Path(home))
	if err == nil {
		if pin, ok := reg.PinFor("gh"); ok {
			tool, code, detail := registry.OpenPinned("gh", pin.Path, pin.SHA256)
			if tool == nil {
				return t, fmt.Errorf("%s: %s", code, detail)
			}
			defer tool.Close()
			if toolsDir == "" {
				return t, fmt.Errorf("%s: the launch has no tools directory for the pinned gh", registry.PinMissing)
			}
			c, err := tool.Install(toolsDir, "gh")
			if err != nil {
				return t, fmt.Errorf("%s: the pinned gh could not be copied into %s: %v", registry.PinMismatch, toolsDir, err)
			}
			c.Close()
			t.GhPath, t.GhSHA256, t.GhPinPath = c.Path, c.Digest, pin.Path
		}
	}
	return t, nil
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
	t.Repository, t.PushURL, t.LocalPushAck = m.Repository, m.PushURL, m.LocalPushAck
	t.BaseBranch = firstNonEmpty(m.BaseBranch, t.BaseBranch)
	return t
}

// LedgerDiscard is one worktree diff CleanSeat discarded.
type LedgerDiscard struct {
	// TaskID and Reason are the seat's last ended attempt: its task and why
	// it ended (for a refusal, commit_refused and the refusal's reason code),
	// so the refused work stays inspectable.
	TaskID     string    `json:"task_id,omitempty"`
	Attempt    int       `json:"attempt,omitempty"`
	Reason     string    `json:"reason"`
	Parent     string    `json:"parent"`
	Tree       string    `json:"tree"`
	DiffDigest string    `json:"diff_digest"`
	At         time.Time `json:"at"`
	EvidenceRef
}

// EvidenceRef is the ref that anchors a kept tree in the seat repository's
// object store (P2-5 of the phase 5 re-pass): git gc keeps an object only
// while a ref reaches it, so a tree named in JSON alone is pruned after the
// grace period. The ref stays until RetainUntil, the explicit retention
// expiry; a quarantine's ref stays while it is not cleared.
type EvidenceRef struct {
	Ref string `json:"ref,omitempty"`
	// RefTree is the tree the ref was set to; the sealed ledger records it,
	// and every commit step and exit check verifies the ref still resolves
	// to it (a deleted or moved ref is ledger_tampered).
	RefTree     string     `json:"ref_tree,omitempty"`
	RetainUntil *time.Time `json:"retain_until,omitempty"`
}

// LedgerQuarantine is the record shape of a legacy quarantine (deleted by
// brief 31-1; kept so a sealed ledger written before 3.1 still verifies).
type LedgerQuarantine struct {
	Task       string               `json:"task"`
	Reason     string               `json:"reason,omitempty"`
	Parent     string               `json:"parent,omitempty"`
	Tree       string               `json:"tree,omitempty"`
	DiffDigest string               `json:"diff_digest,omitempty"`
	Snapshot   string               `json:"snapshot_error,omitempty"`
	Config     []legacyConfigChange `json:"config_changes"`
	Launch     string               `json:"launch,omitempty"`
	At         time.Time            `json:"at"`
	ClearedAt  *time.Time           `json:"cleared_at,omitempty"`
	ClearedBy  string               `json:"cleared_by,omitempty"`
	EvidenceRef
	Clear *legacyClearRecord `json:"clear,omitempty"`
}

// legacyConfigChange is the shape of a legacy quarantine's config diff entry.
type legacyConfigChange struct {
	File, Preflight, Detail string
}

// legacyClearRecord is the shape of a legacy owner's signed clear.
type legacyClearRecord struct {
	Schema        int       `json:"schema"`
	Seat          string    `json:"seat"`
	Task          string    `json:"task"`
	Tree          string    `json:"tree,omitempty"`
	DiffDigest    string    `json:"diff_digest,omitempty"`
	QuarantinedAt time.Time `json:"quarantined_at"`
	ClearedAt     time.Time `json:"cleared_at"`
	KeyID         string    `json:"key_id"`
	GateID        string    `json:"gate_id,omitempty"`
	EventID       string    `json:"event_id,omitempty"`
	ConfigDigest  string    `json:"config_digest,omitempty"`
	Launch        string    `json:"launch,omitempty"`
	Condition     string    `json:"condition,omitempty"`
	Signature     string    `json:"signature,omitempty"`
}

// LedgerLease is one lease's start on the seat (3.0.2 t23): the task's
// branch, the base branch's tip when the task was dispatched (the parent of
// the task's first commit), and the commit the worktree started on (the
// base, or the task's own earlier commit on a later attempt).
type LedgerLease struct {
	Task   string    `json:"task"`
	Branch string    `json:"branch"`
	Base   string    `json:"base"`
	Start  string    `json:"start"`
	At     time.Time `json:"at"`
}

// LedgerCommit is one harness commit.
type LedgerCommit struct {
	Task   string    `json:"task"`
	Commit string    `json:"commit"`
	Parent string    `json:"parent"`
	Tree   string    `json:"tree"`
	At     time.Time `json:"at"`
	Pushed bool      `json:"pushed"`
	// Branch is the task's own branch the commit was pushed to (one branch
	// per task, 3.0.2 t23); Base is the base tip recorded at the task's
	// dispatch. Both are "" on a 3.0.1 commit of the seat branch.
	Branch string `json:"branch,omitempty"`
	Base   string `json:"base,omitempty"`
	// The diff the step checked before it committed (P2-6 of the phase 5
	// re-pass): a retry after a deferred push or PR reuses it, never a diff
	// recomputed against the new ledger parent (the commit itself, so empty).
	DeclaredDigest string   `json:"declared_digest,omitempty"`
	DiffDigest     string   `json:"diff_digest,omitempty"`
	Lines          int      `json:"lines,omitempty"`
	Changes        []Change `json:"changes,omitempty"`
	// Note marks an entry the lead's recovery carried from a quarantined
	// chain ("carried from quarantine seq N", 3.2.0-0c-B); "" on an entry
	// the commit step wrote.
	Note string `json:"note,omitempty"`
}

// LedgerDir is the ledger directory under an AMQ session root.
func LedgerDir(root string) string { return filepath.Join(root, "extensions", "amq-squad", "commits") }

func ledgerPath(root, handle string) string { return filepath.Join(LedgerDir(root), handle+".json") }

// Parent is the commit the seat's worktree diff is taken against: the
// current lease's start, else the resting commit.
func (l Ledger) Parent() string {
	if l.Lease != nil && l.Lease.Start != "" {
		return l.Lease.Start
	}
	return l.rest()
}

// rest is the commit the worktree rests on between leases: the base tip of
// the last lease boundary; a 3.0.1 ledger's last harness commit (Head) or
// launch base until its first boundary.
func (l Ledger) rest() string { return firstNonEmpty(l.Rest, l.Head, l.Base) }

// TaskBranch is a task's own branch on a seat (3.0.2 t23):
// seat/<handle>-<task id>, beside the resting branch seat/<handle> (a ref
// cannot be both seat/<handle> and a directory of task branches). It is a
// label only: nothing parses it back into a handle and a task (a handle may
// itself carry a hyphen); the ledger's per-task record (Lease, Commits) is
// the only source of which task a branch carries.
func TaskBranch(handle, taskID string) string { return "seat/" + handle + "-" + taskID }

// knownBranch reports whether a ref is the task branch of the lease the
// ledger records (Lease.Task, Lease.Branch): the lease record is the only
// source of whether, and on which task, the worktree is; never the name.
func (l Ledger) knownBranch(ref string) bool {
	return l.Lease != nil && l.Lease.Branch != "" && ref == "refs/heads/"+l.Lease.Branch
}

// taskCommitAt is the index of the task's last commit on its branch, -1
// when none.
func (l Ledger) taskCommitAt(taskID, branch string) int {
	for i := len(l.Commits) - 1; i >= 0; i-- {
		if c := l.Commits[i]; c.Task == taskID && c.Branch == branch {
			return i
		}
	}
	return -1
}

// recordedAt is the index of the ledger's commit of this exact diff (parent
// and tree), -1 when none: a re-run after a deferred push or PR finds it,
// and CleanSeat knows a committed worktree from a discarded one.
func (l Ledger) recordedAt(parent, tree string) int {
	for i := len(l.Commits) - 1; i >= 0; i-- {
		if c := l.Commits[i]; c.Parent == parent && c.Tree == tree {
			return i
		}
	}
	return -1
}

// taskStart is the branch and parent of a task's commit: the lease's start
// when StartTask moved the worktree onto the task's branch; without one, the
// task's branch from the resting commit (the seat worked there). why is set
// when the lease the ledger records is another task's.
func (l Ledger) taskStart(handle, taskID string) (branch, parent, base, why string) {
	branch = TaskBranch(handle, taskID)
	if ls := l.Lease; ls != nil {
		if ls.Task != taskID {
			return branch, "", "", fmt.Sprintf("the seat's worktree started task %s's lease, not task %s's", ls.Task, taskID)
		}
		return ls.Branch, ls.Start, ls.Base, ""
	}
	parent = l.rest()
	return branch, parent, parent, ""
}

// ReadLedger loads a seat's ledger; os.ErrNotExist when there is none. A
// refusal (sealchain.ErrTampered) first copies the refused document and its
// head into the quarantine (QuarantineDir, 3.2.0-0c-A), and the error names
// the copy: the evidence of a tamper alarm survives the next writer.
func ReadLedger(root, handle string) (Ledger, error) { return ReadLedgerAs(root, handle, "read") }

// ReadLedgerAs is ReadLedger with the caller's verb on the quarantine's
// sidecar (the scheduler's prepare, each writer).
func ReadLedgerAs(root, handle, verb string) (Ledger, error) {
	l, raw, err := readLedger(root, handle)
	if errors.Is(err, sealchain.ErrTampered) {
		if afterLedgerRefusal != nil {
			afterLedgerRefusal(root, handle)
		}
		err = quarantine(root, handle, verb, err, raw)
	}
	return l, err
}

// afterLedgerRefusal is a test seam between a refusing read and its
// quarantine (row A10: the copy is of the bytes the check refused, never a
// re-read of a file a concurrent writer may have replaced since).
var afterLedgerRefusal func(root, handle string)

// readLedger is the read and the check: the file, its schema and handle,
// the seal under the session seed and the signed head (sealchain.Check).
func readLedger(root, handle string) (Ledger, ledgerRaw, error) {
	var l Ledger
	var raw ledgerRaw
	// The source reads never follow a link (Codex on #893): a ledger or a
	// head that is a symlink (to the seed, to anything) is a refusal in its
	// own right, nothing is read or copied through it, and the sidecar
	// names it.
	headPath := sealchain.HeadPath(root, ledgerHeadName(handle))
	snapHead := func() error {
		var err error
		raw.head, raw.hasHead, err = readNoFollow(headPath)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			raw.head, raw.hasHead, raw.headNote = nil, false, err.Error()+" (not copied)"
			return fmt.Errorf("commit ledger %s: %w: the head %v", handle, sealchain.ErrTampered, err)
		}
		return nil
	}
	body, present, err := readNoFollow(ledgerPath(root, handle))
	raw.body, raw.hasBody = body, err == nil && present
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		raw.bodyNote = err.Error() + " (not copied)"
		_ = snapHead()
		return l, raw, fmt.Errorf("commit ledger %s: %w: %v", handle, sealchain.ErrTampered, err)
	}
	if !present {
		// only a ledger with no sealed head is absent; one whose head
		// says it existed was removed (ledger_tampered, never "no
		// ledger", "no base" or "unused")
		if herr := snapHead(); herr != nil {
			return l, raw, herr
		}
		if key, ok, kerr := sealchain.ReadKey(root); kerr != nil {
			return l, raw, fmt.Errorf("commit ledger %s: %w: the session seed: %v", handle, sealchain.ErrTampered, kerr)
		} else if ok {
			if aerr := sealchain.Absent(root, ledgerHeadName(handle), key); aerr != nil {
				return l, raw, fmt.Errorf("commit ledger %s: %w", handle, aerr)
			}
		}
		return l, raw, os.ErrNotExist
	}
	// A sealed record that does not decode, or carries another schema or
	// handle, is ledger_tampered for the owner, never a deferral (the fix
	// round's item 25).
	if err := json.Unmarshal(body, &l); err != nil {
		_ = snapHead()
		return l, raw, sealchain.Tampered("commit ledger "+handle+" does not decode", err)
	}
	if l.Schema != LedgerSchema || l.Handle != handle {
		_ = snapHead()
		return l, raw, sealchain.Tampered("commit ledger "+handle, fmt.Errorf("schema %d for %q", l.Schema, l.Handle))
	}
	// The seal: a hand edit, an older copy, a stripped seal or a deleted
	// head is ledger_tampered (sealchain.ErrTampered).
	key, ok, err := sealchain.ReadKey(root)
	if err != nil {
		_ = snapHead()
		return l, raw, fmt.Errorf("commit ledger %s: %w: the session seed: %v", handle, sealchain.ErrTampered, err)
	}
	if !ok {
		_ = snapHead()
		return l, raw, fmt.Errorf("commit ledger %s: %w: the session seed that seals it is gone", handle, sealchain.ErrTampered)
	}
	sealed := l
	sealed.Seal = nil
	if herr := snapHead(); herr != nil {
		return l, raw, herr
	}
	if err := sealchain.Check(root, ledgerHeadName(handle), key, ledgerLabel, l.Seal, sealed); err != nil {
		return l, raw, fmt.Errorf("commit ledger %s: %w", handle, err)
	}
	return l, raw, nil
}

// writeLedger seals and saves a seat's ledger: the one point every write
// passes, under the ledger lock (each caller holds it). Before it writes it
// reads and verifies the document on disk, the check ReadLedgerAs runs
// (3.2.0-0c-A): a document that fails is refused, nothing is moved, the
// refused body and head are quarantined, and the error names the copy. The
// caller's own read is not enough: it happened before the caller's git
// work, and the incident's refused body was lost to exactly such a re-seal.
// verb is the writer's name on the quarantine's sidecar.
func writeLedger(root string, l Ledger, verb string) error {
	l.Schema = LedgerSchema
	l.Seal = nil
	if _, err := ReadLedgerAs(root, l.Handle, verb); err != nil && !os.IsNotExist(err) {
		return err
	}
	key, err := sealchain.SessionKey(root)
	if err != nil {
		return fmt.Errorf("commit ledger %s: the session seed: %w", l.Handle, err)
	}
	dir := LedgerDir(root)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	_, err = sealchain.Write(root, ledgerHeadName(l.Handle), key, ledgerLabel, l, func(link *sealchain.Link) error {
		sealed := l
		sealed.Seal = link
		raw, err := json.MarshalIndent(sealed, "", "  ")
		if err != nil {
			return err
		}
		// One durable writer (2b-5): temp in the directory, fsync, rename, fsync.
		return atomicfile.Write(ledgerPath(root, l.Handle), append(raw, '\n'), 0o600)
	}, time.Now())
	return err
}

// ErrLedgerBusy is a seat's commit ledger lock held by another step (a
// running commit step): a tick-path caller defers, never waits (v3-5-verifier
// P1-1 of the second pass).
var ErrLedgerBusy = errors.New("the seat's commit ledger is busy (a commit step holds it)")

// withLedgerTryLock is withLedgerLock without waiting: ErrLedgerBusy when it
// is held. Off unix (no advisory locks) it proceeds, as the store does.
func withLedgerTryLock(root, handle string, fn func() error) error {
	if err := os.MkdirAll(LedgerDir(root), 0o700); err != nil {
		return err
	}
	lock, ok, err := flock.TryExclusive(filepath.Join(LedgerDir(root), handle+".lock"), true)
	switch {
	case errors.Is(err, flock.ErrUnsupported):
	case err != nil:
		return err
	case !ok:
		return ErrLedgerBusy
	default:
		defer lock.Close()
	}
	return fn()
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
	return recordBase(root, handle, worktree, branch, base, nil, now, "seat commit --base")
}

// recordBase is RecordBase and RecordWorktreeBase; verb names the writer on
// a quarantine's sidecar.
func recordBase(root, handle, worktree, branch, base string, target *Target, now time.Time, verb string) (Ledger, error) {
	if handle == "" || worktree == "" || branch == "" || len(strings.TrimSpace(base)) < 40 {
		return Ledger{}, fmt.Errorf("record base: handle, worktree, branch and a full commit id are required")
	}
	var out Ledger
	err := withLedgerLock(root, handle, func() error {
		l, err := ReadLedgerAs(root, handle, verb)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && l.Seeded && l.Worktree == "" {
			// a legacy seed (a clear of a task-store quarantine, before 3.1)
			// is continued, not replaced: the base lands in it
			l.Worktree, l.Branch, l.Base, l.Seeded = worktree, branch, strings.TrimSpace(base), false
			if target != nil {
				l.Target = *target
			}
			out = l
			return writeLedger(root, l, verb)
		}
		if err != nil || l.Worktree != worktree || l.Branch != branch {
			l = Ledger{Handle: handle, Worktree: worktree, Branch: branch, Base: strings.TrimSpace(base), Commits: []LedgerCommit{}, At: now.UTC()}
		} else if target == nil || l.Target == *target {
			out = l
			return nil
		}
		if target != nil {
			// Each launch records the owner's grant as it stands (R2).
			l.Target = *target
		}
		out = l
		return writeLedger(root, l, verb)
	})
	return out, err
}

// RecordTarget records the owner's grant for an existing ledger (seat
// commit --base, the lead's base decision, records it with the base).
func RecordTarget(root, handle string, target Target) (Ledger, error) {
	const verb = "seat commit --base"
	var out Ledger
	err := withLedgerLock(root, handle, func() error {
		l, err := ReadLedgerAs(root, handle, verb)
		if err != nil {
			return err
		}
		l.Target = target
		out = l
		return writeLedger(root, l, verb)
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
	return recordBase(root, handle, worktree, branch, head, &target, now, "launch record base")
}
