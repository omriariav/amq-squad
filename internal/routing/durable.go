package routing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Durability before dispatch (Codex passes 1 to 3, P1-4). Before any route
// the router checks the outcome log against what it last read or wrote
// (size, modification time, file identity) and reads it in full again when
// it changed, or at least every FullCheckInterval. A full read verifies each
// row's tag (rows are HMAC-signed under the session seed): a row that fails
// is treated as absent and reported (history_tampered), so a hand edit or a
// same-size rewrite is detected, not trusted. Then the tail is checked
// before anything is appended (the last line must end with a newline and
// parse as a row), so a flush never covers a torn tail. Then the row of
// every ended attempt that has none is written (a row written again takes
// its policy from the kept decision, never a new one), and every pending
// decision. Any failure stops every route with outcome_log_unwritable.
//
// Same-UID limit (recorded): a writer with the owner's uid can read the seed
// and forge tags, or reset a file's mtime; the tags catch edits that do not
// know the seed and every accident.

// FullCheckInterval bounds how long a same-size, same-mtime change of the
// outcome log can go unread.
const FullCheckInterval = time.Minute

// logStamp is the outcome log's identity as last read or written.
type logStamp struct {
	info os.FileInfo // nil: the log did not exist
}

func stampOf(path string) (logStamp, error) {
	st, err := os.Stat(path)
	if os.IsNotExist(err) {
		return logStamp{}, nil
	}
	if err != nil {
		return logStamp{}, err
	}
	if !st.Mode().IsRegular() {
		return logStamp{}, fmt.Errorf("outcome log %s is not a regular file", path)
	}
	return logStamp{info: st}, nil
}

func (s logStamp) same(o logStamp) bool {
	if s.info == nil || o.info == nil {
		return s.info == nil && o.info == nil
	}
	return os.SameFile(s.info, o.info) && s.info.Size() == o.info.Size() && s.info.ModTime().Equal(o.info.ModTime())
}

func (s logStamp) size() int64 {
	if s.info == nil {
		return 0
	}
	return s.info.Size()
}

// durable runs the checks; nil means routing may dispatch. holds are the
// breaks this pass held tasks for (Flush reports them; Route goes on, since
// only the held tasks wait).
func (r *Router) durable(list []task.Task, now time.Time) (holds []Problem, err error) {
	if r.o.Root == "" {
		if list != nil {
			return nil, r.flush(list, now)
		}
		return nil, nil
	}
	if err := r.verifyLog(now); err != nil {
		return nil, err
	}
	// The tail before any append: a flush must not cover it.
	if err := r.checkTail(); err != nil {
		return nil, err
	}
	// A new break in the history holds the tasks it may have cost a row
	// before anything is re-logged for them (tamper.go).
	if holds, err = r.holdForProblems(list, now); err != nil {
		return nil, err
	}
	if list != nil {
		if err := r.flush(list, now); err != nil {
			return holds, fmt.Errorf("outcome log: %w", err)
		}
	}
	if err := r.writePendingDecisions(); err != nil {
		return holds, err
	}
	return holds, r.checkTail()
}

// checkTail refuses a torn tail, named with its byte length (Codex pass 5,
// (c)): the routing stop.
func (r *Router) checkTail() error {
	path := OutcomePath(r.o.Root)
	st, err := stampOf(path)
	if err != nil {
		return err
	}
	if st.size() > 0 && !tailComplete(path) {
		return fmt.Errorf("outcome log %s: the last line is torn (%d bytes after the last newline) or is no row; not appending past it", path, tornBytes(path))
	}
	return nil
}

// tornBytes is the length of the unterminated last line, 0 when the log ends
// with a newline.
func tornBytes(path string) int {
	_, _, _, tail, err := verifyChain(path, nil)
	if err != nil {
		return 0
	}
	return tail
}

// verifyLog reads the log again when it changed under the router, or when
// the last full read is older than FullCheckInterval.
func (r *Router) verifyLog(now time.Time) error {
	st, err := stampOf(OutcomePath(r.o.Root))
	if err != nil {
		return err
	}
	if st.same(r.logStat) && now.Sub(r.checkedAt) < FullCheckInterval && !now.Before(r.checkedAt) {
		return nil
	}
	return r.reload()
}

// reload rebuilds what the router knows from the outcome log by walking its
// hash chain (chain.go): only good rows count; every break is a problem
// (history_tampered). The chain must reach the head record (head.go): every
// missing seq is truncated:<seq>. A row the router had at its last read and
// the log no longer has is a problem that names its task. Breaks found by an
// earlier read, the decision log's, and the head's open ones stay listed
// until a clearance acknowledges them.
func (r *Router) reload() error {
	path := OutcomePath(r.o.Root)
	good, problems, head, tail, err := verifyChain(path, r.key)
	if err != nil {
		return fmt.Errorf("outcome log: %w", err)
	}
	before := r.logged
	r.logged, r.hist, r.smolDone, r.spent = map[string]bool{}, map[string]record{}, map[string]string{}, map[string]float64{}
	for _, row := range good {
		r.note(row)
	}
	if r.loaded {
		for k := range before {
			if !r.logged[k] {
				task, n := taskOfKey(k), 0
				fmt.Sscanf(k[len(task)+1:], "%d", &n)
				problems = append(problems, Problem{ID: "lost:" + k, Log: "outcome", Task: task, Attempt: n, What: "row " + k + " was in the log and is gone"})
			}
		}
	}
	problems = append(problems, shortfall("outcome", head, r.headRec.OutcomeSeq, r.headRec.OutcomeTag)...)
	if head.Seq < r.headRec.OutcomeSeq {
		head = chainHead{Seq: r.headRec.OutcomeSeq, Tag: r.headRec.OutcomeTag} // new rows follow the head, not the cut
	}
	r.problems = mergeProblems(r.problems, problems)
	// A seq the head already reports as truncated is not also a gap once rows
	// follow the cut: one break per missing seq.
	trunc := map[string]bool{}
	for _, p := range r.problems {
		if strings.HasPrefix(p.ID, "truncated:") {
			trunc[strings.TrimPrefix(p.ID, "truncated:")] = true
		}
	}
	kept := r.problems[:0]
	for _, p := range r.problems {
		if strings.HasPrefix(p.ID, "gap:") && trunc[strings.TrimPrefix(p.ID, "gap:")] {
			continue
		}
		kept = append(kept, p)
	}
	r.problems = kept
	r.head, r.loaded, r.tornTail = head, true, tail
	st, err := stampOf(path)
	if err != nil {
		return err
	}
	r.logStat, r.checkedAt = st, r.o.Now()
	return nil
}

// mergeProblems adds the found breaks to the known ones, each id once.
func mergeProblems(have, found []Problem) []Problem {
	seen := map[string]bool{}
	var out []Problem
	for _, p := range append(append([]Problem(nil), have...), found...) {
		if !seen[p.ID] {
			seen[p.ID] = true
			out = append(out, p)
		}
	}
	return out
}

// writePendingDecisions writes the queued decisions in order, signed and
// chained, and moves the head; the first failure leaves it and the later
// ones queued.
func (r *Router) writePendingDecisions() error {
	for len(r.pendingDec) > 0 {
		d := *r.pendingDec[0]
		d.Seq, d.Prev = r.decHead.Seq+1, r.decHead.Tag
		d = d.sign(r.key)
		if err := AppendJSONL(DecisionPath(r.o.Root), d); err != nil {
			return fmt.Errorf("decision log: %w", err)
		}
		r.decHead = chainHead{Seq: d.Seq, Tag: d.Tag}
		r.pendingDec = r.pendingDec[1:]
		if err := r.saveHead(); err != nil {
			return err
		}
	}
	return nil
}

// tailComplete reports whether the log ends with a newline and its last line
// parses as an outcome row. A complete row whose tag fails is not a torn
// tail: it is treated as absent and reported (history_tampered) by the full
// read, and the next row may follow it.
// It reads backwards in 64 KiB chunks.
func tailComplete(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() == 0 {
		return false
	}
	end := st.Size()
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, end-1); err != nil || last[0] != '\n' {
		return false
	}
	end-- // drop the final newline
	var line []byte
	for off := end; off > 0; {
		n := min(int64(64<<10), off)
		off -= n
		chunk := make([]byte, n)
		if _, err := f.ReadAt(chunk, off); err != nil && err != io.EOF {
			return false
		}
		line = append(chunk, line...)
		if i := bytes.LastIndexByte(line, '\n'); i >= 0 {
			line = line[i+1:]
			break
		}
	}
	var row Row
	return json.Unmarshal(line, &row) == nil && row.Schema == RowSchema && row.Task != ""
}
