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
// written again after a rebase that changes a head), refusal, update_requested
// (the train is about to ask GitHub to update a branch: the PR, the bound
// head and the base tip, journaled before the mutation, brief 3.2.0-3a item
// 0), update_failed (GitHub definitely refused the mutation, so the request
// counts for nothing), update_ambiguous (the mutation failed on this side
// without a definite refusal, so the request stays live), rebase (the head
// the update produced, read back), rebind, merge_intent,
// merge, reconcile, stop (manual mode or an error ended the run), plan (a
// dry run's result).
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

// Append writes one entry and fsyncs it. Under the append lock the tail is
// checked first, the rule of the outcome log (2b-4 AppendJSONL): a torn last
// line (no newline) is terminated before the entry, so the entry stays one
// whole line and Read skips only the torn one (5 re-pass, P2-6).
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
		f, err := os.OpenFile(a.Path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		out := append(line, '\n')
		st, err := f.Stat()
		if err == nil && st.Size() > 0 {
			last := make([]byte, 1)
			if _, err = f.ReadAt(last, st.Size()-1); err == nil && last[0] != '\n' {
				out = append([]byte{'\n'}, out...)
			}
		}
		if err == nil {
			if _, err = f.Write(out); err == nil {
				err = f.Sync()
			}
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
// force push that lands on one of them is the train's, not a seat's. A
// head is taken out again by a terminal rebind_superseded entry (the lead's
// re-bind whose conditional binding write refused, rebind.go) and brought
// back only by a terminal rebind_readmitted entry, which only the lead's
// merge-train rebind verb writes (its applied binding, over the decision
// socket with the lead proof; no train code path writes it): the latest
// terminal entry of a head decides, latest in the journal's order (the
// append sequence), never by the entries' timestamps, and a rebase entry
// never decides once a terminal entry names the head, whatever the order
// of the rebase entries (the plan reviewer on #903).
func (a Audit) trainHeads(repository string, pr int) map[string]bool {
	entries, _ := a.Read()
	out := map[string]bool{}
	for _, e := range entries {
		if e.Kind == "rebase" && e.Repository == repository && e.PR == pr && e.NewHead != "" {
			out[e.NewHead] = true
		}
	}
	for h, kind := range terminalKinds(entries, repository, pr) {
		if kind == "rebind_superseded" {
			delete(out, h)
		}
	}
	return out
}

// terminalKinds is each head's latest terminal entry kind (rebind_superseded
// or rebind_readmitted) for a PR, by the journal's order.
func terminalKinds(entries []Entry, repository string, pr int) map[string]string {
	terminal := map[string]string{} // head -> the latest terminal kind, in journal order
	for _, e := range entries {
		if e.Repository != repository || e.PR != pr || e.NewHead == "" {
			continue
		}
		switch e.Kind {
		case "rebind_superseded", "rebind_readmitted":
			terminal[e.NewHead] = e.Kind
		}
	}
	return terminal
}

// supersededHead reports a head whose latest terminal entry is
// rebind_superseded: the train never binds or recovers it; only the lead's
// merge-train rebind verb re-admits it.
func (a Audit) supersededHead(repository string, pr int, head string) bool {
	entries, _ := a.Read()
	return terminalKinds(entries, repository, pr)[head] == "rebind_superseded"
}

// lastUpdateRequest is the latest journaled update_requested of a real run
// for a PR at a bound head: the train asked GitHub to update that branch
// from that head, and the entry carries the base tip it pinned. A request
// GitHub definitely refused is followed by an update_failed entry for the
// same head and counts for nothing: GitHub never took it, so a later move
// of the branch is not the train's. A request whose mutation failed
// without a definite refusal (a timeout, a 5xx, a dropped connection: the
// plan reviewer on #903) is followed by update_ambiguous and stays live,
// since GitHub may have completed it; ambiguous carries that entry's
// detail.
func lastUpdateRequest(entries []Entry, repository string, pr int, head string) (req Entry, ambiguous string, found bool) {
	for _, e := range entries {
		if e.DryRun || e.Repository != repository || e.PR != pr || e.Head != head {
			continue
		}
		switch e.Kind {
		case "update_requested":
			req, ambiguous, found = e, "", true
		case "update_failed":
			req, ambiguous, found = Entry{}, "", false
		case "update_ambiguous":
			if found {
				ambiguous = e.Detail
			}
		}
	}
	return req, ambiguous, found
}
