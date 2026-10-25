package routing

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/atomicfile"
)

// The head record (Codex pass 5, (a), (e), new 2): one signed file under the
// routing directory, rewritten through atomicfile on every append to either
// log. It says how far each chain must reach (the last outcome seq and tag,
// the last decision seq and tag), which seed file signs them (its device and
// inode), and which breaks are open: the one place problems persist across
// reloads until a clearance acknowledges them. On load the logs must reach
// the head, or every missing seq is a break (truncated:<seq>, no task
// known, so the sweep holds). A head that is missing, malformed or whose tag
// fails in a session that has routed is routing_invalid; a seed whose device
// or inode differs from the head's is the seed refusal.

// HeadPath is the head record of a session root.
func HeadPath(root string) string { return filepath.Join(Dir(root), "head.json") }

type headRecord struct {
	Schema      int       `json:"schema"`
	OutcomeSeq  int       `json:"outcome_seq"`
	OutcomeTag  string    `json:"outcome_tag"`
	DecisionSeq int       `json:"decision_seq"`
	DecisionTag string    `json:"decision_tag"`
	SeedDev     uint64    `json:"seed_dev"`
	SeedIno     uint64    `json:"seed_ino"`
	Open        []Problem `json:"open_problems"`
	At          time.Time `json:"at"`
	Tag         string    `json:"tag,omitempty"`
}

func (h headRecord) sign(key []byte) headRecord {
	h.Tag = ""
	h.Tag = hmacTag(key, "amq-squad routing head v1", h)
	return h
}

func (h headRecord) verify(key []byte) bool {
	return h.Schema == 1 && h.Tag != "" && h.sign(key).Tag == h.Tag
}

// readHead reads the head record; ok false when there is none.
func readHead(root string, key []byte) (headRecord, bool, error) {
	raw, err := os.ReadFile(HeadPath(root))
	if errors.Is(err, fs.ErrNotExist) {
		return headRecord{}, false, nil
	}
	if err != nil {
		return headRecord{}, false, err
	}
	var h headRecord
	if err := json.Unmarshal(raw, &h); err != nil || !h.verify(key) {
		return headRecord{}, true, fmt.Errorf("routing head %s does not decode or its tag fails (%s)", HeadPath(root), HistoryTampered)
	}
	return h, true, nil
}

// checkHead reads the head record and runs the checks of a session that has
// routed: a head that does not verify, a head missing beside an outcome log,
// decision log or head, and a seed file other than the one the head
// recorded (device and inode; seedDev 0 when unknown) are each
// routing_invalid. The router's start and the weekly report run this one
// check (phase 5 re-pass, P2-7).
func checkHead(root string, key []byte, seedDev, seedIno uint64) (headRecord, bool, error) {
	h, found, err := readHead(root, key)
	switch {
	case err != nil:
		return headRecord{}, found, &InvalidError{Err: err}
	case !found && sessionStarted(root):
		return headRecord{}, false, &InvalidError{Err: fmt.Errorf("routing head %s is missing in a session that has routed before", HeadPath(root))}
	case found && seedDev != 0 && (h.SeedDev != seedDev || h.SeedIno != seedIno):
		return headRecord{}, true, &InvalidError{Err: fmt.Errorf("routing seed %s is not the file the head recorded (device and inode differ)", SeedPath(root))}
	}
	return h, found, nil
}

// saveHead rewrites the head record from what the router holds now.
func (r *Router) saveHead() error {
	if r.o.Root == "" {
		return nil
	}
	h := headRecord{Schema: 1, OutcomeSeq: r.head.Seq, OutcomeTag: r.head.Tag, DecisionSeq: r.decHead.Seq, DecisionTag: r.decHead.Tag,
		SeedDev: r.seedDev, SeedIno: r.seedIno, Open: r.openProblems(), At: r.o.Now()}
	if h.Open == nil {
		h.Open = []Problem{}
	}
	raw, err := json.Marshal(h.sign(r.key))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(Dir(r.o.Root), 0o700); err != nil {
		return err
	}
	if err := atomicfile.Write(HeadPath(r.o.Root), raw, 0o600); err != nil {
		return fmt.Errorf("routing head: %w", err)
	}
	r.headRec = h
	return nil
}

// shortfall are the breaks of a chain that does not reach the head: every
// seq after the chain's last good one up to the head's, or the head's own
// seq when the chain ends there with another tag.
func shortfall(log string, got chainHead, wantSeq int, wantTag string) []Problem {
	var out []Problem
	switch {
	case got.Seq < wantSeq:
		for s := got.Seq + 1; s <= wantSeq; s++ {
			out = append(out, Problem{ID: fmt.Sprintf("truncated:%s%d", logPrefix(log), s), Log: log, Seq: s,
				What: fmt.Sprintf("%s row %d, recorded in the head, is missing (the log ends at %d)", log, s, got.Seq)})
		}
	case got.Seq == wantSeq && wantSeq > 0 && got.Tag != wantTag:
		out = append(out, Problem{ID: fmt.Sprintf("truncated:%s%d", logPrefix(log), wantSeq), Log: log, Seq: wantSeq,
			What: fmt.Sprintf("%s row %d is not the row the head recorded", log, wantSeq)})
	}
	return out
}

func logPrefix(log string) string {
	if log == "decision" {
		return "decision:"
	}
	return ""
}
