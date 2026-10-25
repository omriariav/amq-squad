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

// verdictLabel is the seal's domain label.
const verdictLabel = "amq-squad verdict v1"

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
	At            time.Time `json:"at"`
}

func sealBody(t Task, v Verdict) verdictBody {
	return verdictBody{Task: t.ID, Attempt: t.Attempt, Holder: t.Lease.Holder, Verifier: v.Verifier, Result: v.Result, Source: v.Source,
		Detail: v.Detail, Tree: v.Tree, CommandDigest: v.CommandDigest, At: v.At.UTC()}
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

// RecordSnapshot records the tree the harness's verifier runs on in the
// task's current attempt: the commit step's snapshot tree, the probe's
// checkout. It is the harness's call, like RecordVerdict. A recorded verdict
// on another tree is never moved under: that is verdict_tree_mismatch.
func (s Store) RecordSnapshot(id, tree string, now time.Time) (Task, error) {
	tree = strings.TrimSpace(tree)
	return s.mutate(id, now, func(t *Task, _ *tasks) error {
		switch {
		case tree == "":
			return fmt.Errorf("a snapshot names the tree the verifier runs on")
		case t.State != StateInProgress || t.Lease == nil:
			return fmt.Errorf("task %s is %s with no attempt to record a snapshot on", t.ID, t.State)
		case t.Lease.Verdict != nil && t.Lease.Verdict.Tree != tree:
			return fmt.Errorf("task %s attempt %d: %w: its verdict ran on %s, the snapshot is %s", t.ID, t.Attempt, ErrVerdictTreeMismatch, t.Lease.Verdict.Tree, tree)
		}
		t.Lease.Snapshot = tree
		return nil
	})
}

// RecordVerdict records the harness's verdict on the current attempt of a
// task whose class has a mechanical verifier: the commit step's test run or
// the probe's predicate. It is the harness's call, never the seat's (no task
// verb exposes it; the scheduler process and the commit step it runs call
// it); one verdict per attempt, of the class's own verifier, on the tree the
// lease's snapshot names (verdict_tree_mismatch otherwise), with the
// command's digest. The store seals it with the session seed.
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
		case t.Lease.Verdict != nil:
			return fmt.Errorf("task %s attempt %d already has a %s verdict", t.ID, t.Attempt, t.Lease.Verdict.Result)
		case v.Verifier != want:
			return fmt.Errorf("task %s: verdict of verifier %q, class %s is verified by %s", t.ID, v.Verifier, t.Class, want)
		case v.Result != VerdictPass && v.Result != VerdictFail:
			return fmt.Errorf("verdict result %q: want pass or fail", v.Result)
		case strings.TrimSpace(v.Source) == "":
			return fmt.Errorf("a verdict names the harness step that ran the verifier")
		case t.Lease.Snapshot == "":
			return fmt.Errorf("task %s attempt %d: %w: no snapshot is recorded for the verdict's tree %q", t.ID, t.Attempt, ErrVerdictTreeMismatch, v.Tree)
		case v.Tree != t.Lease.Snapshot:
			return fmt.Errorf("task %s attempt %d: %w: the verdict ran on %q, the lease's snapshot is %s", t.ID, t.Attempt, ErrVerdictTreeMismatch, v.Tree, t.Lease.Snapshot)
		case !strings.HasPrefix(v.CommandDigest, "sha256:"):
			return fmt.Errorf("a verdict carries the digest of the command that ran")
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
	root, err := s.sessionRoot()
	if err != nil {
		return err.Error()
	}
	key, ok, err := sealchain.ReadKey(root)
	switch {
	case err != nil:
		return "the session seed that seals verdicts: " + err.Error()
	case !ok:
		return "the session seed that seals verdicts is gone"
	case v.Tag == "":
		return "the recorded verdict carries no harness seal"
	case !hmac.Equal([]byte(sealchain.Tag(key.Key, verdictLabel, sealBody(t, *v))), []byte(v.Tag)):
		return "the recorded verdict's seal fails"
	case v.Tree == "" || v.Tree != t.Lease.Snapshot:
		return fmt.Sprintf("the recorded verdict ran on %q, not the lease's snapshot %q", v.Tree, t.Lease.Snapshot)
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
