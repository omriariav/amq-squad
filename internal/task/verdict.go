package task

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/omriariav/amq-squad/v3/internal/sealchain"
)

// The sealed verdict (v3-5-verifier rules 2, 3 and 11). The harness records
// the tree its verifier runs on (RecordSnapshot), then the verdict
// (RecordVerdict): the verdict must name that tree (verdict_tree_mismatch
// otherwise) and the digest of the command that ran, and the store seals it
// with the session seed (sealchain.Tag over the verdict, its task, attempt
// and holder). Completion verifies the seal and the tree; a verdict written
// into a task file without them is no verdict (verdict_missing).
//
// Limit, as for every sealed record (the routing chain's sentence): a
// same-UID process that reads the session seed can forge a verdict; the seal
// detects every verdict written without it (a hand edit, a copied verdict, a
// stripped tag).

// verdictLabel and snapshotLabel are the seals' domain labels.
const (
	verdictLabel  = "amq-squad verdict v1"
	snapshotLabel = "amq-squad verifier snapshot v1"
)

// VerdictDetailCap bounds a verdict's detail: the block detail the lead reads
// (rule 6: the redacted output tail, at most 4 KiB).
const VerdictDetailCap = 4 << 10

// ErrVerdictTreeMismatch is a verdict whose tree is not the lease's
// snapshot, or a snapshot that would move under a recorded verdict.
var ErrVerdictTreeMismatch = errors.New(BlockVerdictTreeMismatch)

// ReasonGrantsInvalid is the dispatch refusal of a gated class with no
// verifier command (rule 4: grants_invalid for the verifiers key).
const ReasonGrantsInvalid = "grants_invalid"

// VerifierUnconfiguredError refuses the dispatch of a task whose class is in
// the squad's verifier gate while the owner grant names no command for it:
// such a task never reaches a seat.
type VerifierUnconfiguredError struct{ Class string }

func (e *VerifierUnconfiguredError) Error() string {
	return fmt.Sprintf("%s: verifiers: class %s is in the squad's verifier gate and the owner grant names no verifier command for it (verifiers.%s); a task of class %s is not dispatched", ReasonGrantsInvalid, e.Class, e.Class, e.Class)
}

// CommandDigest is the digest of a verifier command as the grant names it
// (the probe's: its predicate version and prompt).
func CommandDigest(command string) string {
	sum := sha256.Sum256([]byte(command))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// verdictBody is the signed bytes of a verdict.
type verdictBody struct {
	Task          string    `json:"task"`
	Attempt       int       `json:"attempt"`
	Holder        string    `json:"holder"`
	Verifier      string    `json:"verifier"`
	Result        string    `json:"result"`
	Source        string    `json:"source"`
	Detail        string    `json:"detail"`
	Tree          string    `json:"tree"`
	CommandDigest string    `json:"command_digest"`
	Prev          string    `json:"prev_command_digest"`
	At            time.Time `json:"at"`
}

func sealBody(t Task, v Verdict) verdictBody {
	return verdictBody{Task: t.ID, Attempt: t.Attempt, Holder: t.Lease.Holder, Verifier: v.Verifier, Result: v.Result, Source: v.Source,
		Detail: v.Detail, Tree: v.Tree, CommandDigest: v.CommandDigest, Prev: v.PrevCommandDigest, At: v.At.UTC()}
}

// snapshotBody is the signed bytes of a snapshot.
type snapshotBody struct {
	Task    string `json:"task"`
	Attempt int    `json:"attempt"`
	Holder  string `json:"holder"`
	Tree    string `json:"tree"`
	Command string `json:"command_digest"`
}

func snapshotTag(key []byte, t Task, tree, command string) string {
	return sealchain.Tag(key, snapshotLabel, snapshotBody{Task: t.ID, Attempt: t.Attempt, Holder: t.Lease.Holder, Tree: tree, Command: command})
}

// sessionKey reads the store's session seed without writing one.
func (s Store) sessionKey() ([]byte, string) {
	root, err := s.sessionRoot()
	if err != nil {
		return nil, err.Error()
	}
	key, ok, err := sealchain.ReadKey(root)
	switch {
	case err != nil:
		return nil, "the session seed that seals verdicts: " + err.Error()
	case !ok:
		return nil, "the session seed that seals verdicts is gone"
	}
	return key.Key, ""
}

// SnapshotSealed reports whether the lease's snapshot is the harness's: its
// seal verifies under the session seed. A sealed snapshot with no sealed
// verdict on it is a verifier run that started and never recorded (the
// harness stopped mid-run): the commit step never runs the verifier on that
// tree again by itself (verifier_interrupted until the lead's reset).
func (s Store) SnapshotSealed(t Task) bool {
	if t.Lease == nil || t.Lease.Snapshot == "" || t.Lease.SnapshotTag == "" {
		return false
	}
	key, why := s.sessionKey()
	return why == "" && hmac.Equal([]byte(snapshotTag(key, t, t.Lease.Snapshot, t.Lease.SnapshotCommand)), []byte(t.Lease.SnapshotTag))
}

// SealedVerdict is the lease's verdict when its seal verifies and it names
// the lease's sealed snapshot; otherwise nil and why. Only a sealed verdict
// is reused or completes a task: an unsealed one is no verdict.
func (s Store) SealedVerdict(t Task) (*Verdict, string) {
	if t.Lease == nil || t.Lease.Verdict == nil {
		return nil, "no verdict is recorded"
	}
	if why := s.verdictInvalid(t); why != "" {
		return nil, why
	}
	v := *t.Lease.Verdict
	return &v, ""
}

// sessionRoot is the AMQ session root of a store opened with Open (its Dir
// is DirFor(root)); a store elsewhere has no seed to seal with.
func (s Store) sessionRoot() (string, error) {
	dir := filepath.Clean(s.Dir)
	root := filepath.Dir(filepath.Dir(filepath.Dir(dir)))
	if DirFor(root) != dir {
		return "", fmt.Errorf("the task store %s is not under a session root; a verdict is sealed with the session seed", s.Dir)
	}
	return root, nil
}

// RecordSnapshot records, sealed, the tree the harness's verifier is about
// to run on in the task's current attempt: the commit step's snapshot tree
// (just before the verifier starts), the probe's checkout. It is the
// harness's call, like RecordVerdict. A sealed verdict on another tree is
// never moved under (verdict_tree_mismatch); an unsealed verdict is dropped.
func (s Store) RecordSnapshot(id, tree, commandDigest string, now time.Time) (Task, error) {
	tree = strings.TrimSpace(tree)
	root, err := s.sessionRoot()
	if err != nil {
		return Task{}, err
	}
	return s.mutate(id, now, func(t *Task, _ *tasks) error {
		switch {
		case tree == "":
			return fmt.Errorf("a snapshot names the tree the verifier runs on")
		case t.State != StateInProgress || t.Lease == nil:
			return fmt.Errorf("task %s is %s with no attempt to record a snapshot on", t.ID, t.State)
		case !strings.HasPrefix(commandDigest, "sha256:"):
			return fmt.Errorf("a snapshot names the digest of the command about to run")
		}
		if t.Lease.Verdict != nil {
			if why := s.verdictInvalid(*t); why != "" {
				t.Lease.Verdict = nil // no verdict: never moved under, never reused
			} else if t.Lease.Verdict.Tree != tree {
				return fmt.Errorf("task %s attempt %d: %w: its verdict ran on %s, the snapshot is %s", t.ID, t.Attempt, ErrVerdictTreeMismatch, t.Lease.Verdict.Tree, tree)
			}
		}
		key, err := sealchain.SessionKey(root)
		if err != nil {
			return fmt.Errorf("seal the snapshot: %w", err)
		}
		t.Lease.Snapshot, t.Lease.SnapshotCommand, t.Lease.SnapshotTag = tree, commandDigest, snapshotTag(key.Key, *t, tree, commandDigest)
		return nil
	})
}

// RecordVerdict records the harness's verdict on the current attempt of a
// task whose class has a mechanical verifier: the commit step's test run or
// the probe's predicate. It is the harness's call, never the seat's (no task
// verb exposes it; the scheduler process and the commit step it runs call
// it); one verdict per attempt, of the class's own verifier, on the tree the
// lease's snapshot names (verdict_tree_mismatch otherwise), with the
// command's digest. The store seals it with the session seed. On the same
// tree a sealed verdict of another command (the grant's command changed
// between two runs of one attempt) is superseded, its digest kept in the new
// verdict's signed bytes; an unsealed verdict is no verdict and is replaced.
func (s Store) RecordVerdict(id string, v Verdict, now time.Time) (Task, error) {
	root, err := s.sessionRoot()
	if err != nil {
		return Task{}, err
	}
	return s.mutate(id, now, func(t *Task, _ *tasks) error {
		want := ClassVerifier(t.Class)
		switch {
		case !MechanicalVerifier(want):
			return fmt.Errorf("task %s: class %s has no mechanical verifier", t.ID, t.Class)
		case t.State != StateInProgress || t.Lease == nil:
			return fmt.Errorf("task %s is %s with no attempt to record a verdict on", t.ID, t.State)
		case v.Verifier != want:
			return fmt.Errorf("task %s: verdict of verifier %q, class %s is verified by %s", t.ID, v.Verifier, t.Class, want)
		case v.Result != VerdictPass && v.Result != VerdictFail:
			return fmt.Errorf("verdict result %q: want pass or fail", v.Result)
		case strings.TrimSpace(v.Source) == "":
			return fmt.Errorf("a verdict names the harness step that ran the verifier")
		case t.Lease.Snapshot == "" || !s.SnapshotSealed(*t):
			return fmt.Errorf("task %s attempt %d: %w: no sealed snapshot is recorded for the verdict's tree %q", t.ID, t.Attempt, ErrVerdictTreeMismatch, v.Tree)
		case v.Tree != t.Lease.Snapshot:
			return fmt.Errorf("task %s attempt %d: %w: the verdict ran on %q, the lease's snapshot is %s", t.ID, t.Attempt, ErrVerdictTreeMismatch, v.Tree, t.Lease.Snapshot)
		case !strings.HasPrefix(v.CommandDigest, "sha256:") || v.CommandDigest != t.Lease.SnapshotCommand:
			return fmt.Errorf("task %s attempt %d: the verdict's command digest %q is not the one its snapshot announced (%q)", t.ID, t.Attempt, v.CommandDigest, t.Lease.SnapshotCommand)
		}
		v.PrevCommandDigest = ""
		if old := t.Lease.Verdict; old != nil && s.verdictInvalid(*t) == "" {
			if old.CommandDigest == v.CommandDigest {
				return fmt.Errorf("task %s attempt %d already has a %s verdict of this command on this tree", t.ID, t.Attempt, old.Result)
			}
			v.PrevCommandDigest = old.CommandDigest // superseded: the grant's command changed
		}
		key, err := sealchain.SessionKey(root)
		if err != nil {
			return fmt.Errorf("seal the verdict: %w", err)
		}
		v.Source, v.Detail, v.At = strings.TrimSpace(v.Source), capDetail(strings.TrimSpace(v.Detail)), now.UTC()
		v.Tag = sealchain.Tag(key.Key, verdictLabel, sealBody(*t, v))
		t.Lease.Verdict = &v
		return nil
	})
}

// verdictInvalid is why the lease's verdict is not the harness's ("" when it
// is): its seal verifies under the session seed and it names the lease's
// snapshot.
func (s Store) verdictInvalid(t Task) string {
	v := t.Lease.Verdict
	key, why := s.sessionKey()
	switch {
	case why != "":
		return why
	case v.Tag == "":
		return "the recorded verdict carries no harness seal"
	case !hmac.Equal([]byte(sealchain.Tag(key, verdictLabel, sealBody(t, *v))), []byte(v.Tag)):
		return "the recorded verdict's seal fails"
	case v.Tree == "" || v.Tree != t.Lease.Snapshot:
		return fmt.Sprintf("the recorded verdict ran on %q, not the lease's snapshot %q", v.Tree, t.Lease.Snapshot)
	case !s.SnapshotSealed(t):
		return "the lease's snapshot carries no harness seal"
	}
	return ""
}

// capDetail keeps the last VerdictDetailCap bytes of a detail (the tail of
// a verifier's output), cut at a rune boundary.
func capDetail(s string) string {
	if len(s) <= VerdictDetailCap {
		return s
	}
	s = s[len(s)-VerdictDetailCap:]
	for len(s) > 0 && !utf8.RuneStart(s[0]) {
		s = s[1:]
	}
	return s
}

// dispatchable refuses the dispatch of a task whose class is in the
// verifier gate with no configured command (rule 4). The source's error is
// not a refusal here: the done-report gate fails closed on it.
func (s Store) dispatchable(t Task) error {
	if !MechanicalVerifier(ClassVerifier(t.Class)) || s.Verifiers == nil {
		return nil
	}
	cmd, required, err := s.Verifiers(t)
	if err == nil && required && cmd == "" {
		return &VerifierUnconfiguredError{Class: t.Class}
	}
	return nil
}
