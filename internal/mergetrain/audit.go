package mergetrain

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/flock"
)

// DigestDomain is the S1 merge formula's domain (spike/gateverify
// TargetDigest, class merge). The S2 gate s2-merge-01 digest is pinned by a
// test, so the train's digest is the one S2 measured.
const DigestDomain = "amq-squad-gate-target-v1"

// Pair is one (PR, head SHA) of an ordered batch.
type Pair struct {
	Number int    `json:"number"`
	Head   string `json:"head"`
}

// BatchDigest is sha256 over the length-prefixed domain, "merge", the
// repository, the base SHA, the pair count and each ordered (PR, head) pair
// (plan 7.3, R34): a reorder, a new head or a new base changes it. Nobody
// signs it; the train writes it to the audit log.
func BatchDigest(repository, baseSHA string, pairs []Pair) string {
	var b []byte
	put := func(v string) {
		b = binary.BigEndian.AppendUint32(b, uint32(len(v)))
		b = append(b, v...)
	}
	put(DigestDomain)
	put("merge")
	put(repository)
	put(baseSHA)
	put(strconv.Itoa(len(pairs)))
	for _, p := range pairs {
		put(strconv.Itoa(p.Number))
		put(p.Head)
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// AuditSchema names the audit line format.
const AuditSchema = "amq-squad-merge-audit/1"

// Entry is one audit log line. Kind is one of: protection (the branch
// protection check), batch (the digest of the ordered set about to land,
// written again after a rebase that changes a head), refusal, rebase, merge,
// stop (manual mode or an error ended the run), plan (a dry run's result).
type Entry struct {
	Schema     string   `json:"schema"`
	At         string   `json:"at"`
	Run        string   `json:"run"`
	Kind       string   `json:"kind"`
	Repository string   `json:"repository"`
	Base       string   `json:"base"`
	BaseSHA    string   `json:"base_sha,omitempty"`
	Pairs      []Pair   `json:"pairs,omitempty"`
	Digest     string   `json:"digest,omitempty"`
	PR         int      `json:"pr,omitempty"`
	Task       string   `json:"task,omitempty"`
	Head       string   `json:"head,omitempty"`
	NewHead    string   `json:"new_head,omitempty"`
	Merged     string   `json:"merged,omitempty"`
	Rules      []string `json:"rules,omitempty"`
	Detail     string   `json:"detail,omitempty"`
	GateID     string   `json:"gate_id,omitempty"`
	DryRun     bool     `json:"dry_run,omitempty"`
}

// Audit is the append-only audit log of one session, <session root>/
// extensions/amq-squad/mergetrain/audit.jsonl (0600, flock, fsynced). guard
// keeps extensions/amq-squad/ a protected path, so a seat's tool call cannot
// write it; same-UID writes are the accepted host threat (plan 7.3).
type Audit struct{ Path string }

// AuditFor is the audit log under an AMQ session root.
func AuditFor(root string) Audit {
	return Audit{Path: filepath.Join(root, "extensions", "amq-squad", "mergetrain", "audit.jsonl")}
}

// Append writes one entry and fsyncs it.
func (a Audit) Append(e Entry) error {
	if a.Path == "" {
		return errors.New("merge train: no audit log path")
	}
	if e.Schema == "" {
		e.Schema = AuditSchema
	}
	if e.At == "" {
		e.At = time.Now().UTC().Format(time.RFC3339Nano)
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.Path), 0o700); err != nil {
		return err
	}
	return flock.WithLock(a.Path+".lock", func() error {
		f, err := os.OpenFile(a.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		if _, err = f.Write(append(line, '\n')); err == nil {
			err = f.Sync()
		}
		return errors.Join(err, f.Close())
	})
}

// Read returns every well-formed entry; a torn line is skipped.
func (a Audit) Read() ([]Entry, error) {
	f, err := os.Open(a.Path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Schema == AuditSchema {
			out = append(out, e)
		}
	}
	if err := sc.Err(); err != nil {
		return out, fmt.Errorf("read audit log: %w", err)
	}
	return out, nil
}

// trainHeads are the heads the train's own rebases produced for a PR: a
// force push that lands on one of them is the train's, not a seat's.
func (a Audit) trainHeads(repository string, pr int) map[string]bool {
	entries, _ := a.Read()
	out := map[string]bool{}
	for _, e := range entries {
		if e.Kind == "rebase" && e.Repository == repository && e.PR == pr && e.NewHead != "" {
			out[e.NewHead] = true
		}
	}
	return out
}
