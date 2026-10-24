package routing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Durability before dispatch (Codex pass 1 and 2, P1-4). Before any route,
// the router re-verifies the outcome log against what it last read or
// wrote (its size, and that the last row parses), re-reads it when it
// changed (deleted, truncated, or a torn tail), writes the row of every
// ended attempt that has none, and writes every pending decision. Any
// failure stops every route with wait_reason outcome_log_unwritable, so a
// decision never rests on spend or history that is not on disk.

// durable runs the checks; nil means routing may dispatch.
func (r *Router) durable(list []task.Task, now time.Time) error {
	if r.o.Root == "" {
		if list != nil {
			return r.flush(list, now)
		}
		return nil
	}
	if err := r.verifyLog(); err != nil {
		return err
	}
	if list != nil {
		if err := r.flush(list, now); err != nil {
			return fmt.Errorf("outcome log: %w", err)
		}
	}
	if err := r.writePendingDecisions(); err != nil {
		return err
	}
	if size := fileSize(OutcomePath(r.o.Root)); size > 0 && !tailParses(OutcomePath(r.o.Root)) {
		return fmt.Errorf("outcome log %s: the last row does not parse", OutcomePath(r.o.Root))
	}
	return nil
}

// verifyLog compares the log with what the router last saw and re-reads it
// when it differs, so a deleted or truncated log has its missing rows
// written again by the next flush (the attempts are still in the store).
func (r *Router) verifyLog() error {
	path := OutcomePath(r.o.Root)
	size := fileSize(path)
	if size < 0 {
		return fmt.Errorf("outcome log %s: cannot stat", path)
	}
	if size == r.logSize && (size == 0 || tailParses(path)) {
		return nil
	}
	return r.reload()
}

// reload rebuilds what the router knows from the log on disk.
func (r *Router) reload() error {
	rows, _, err := ReadRows(OutcomePath(r.o.Root))
	if err != nil {
		return fmt.Errorf("outcome log: %w", err)
	}
	r.logged, r.hist, r.smolDone, r.spent = map[string]bool{}, map[string]record{}, map[string]string{}, map[string]float64{}
	for _, row := range rows {
		r.note(row)
	}
	r.logSize = max(fileSize(OutcomePath(r.o.Root)), 0)
	return nil
}

// writePendingDecisions writes the queued decisions in order; the first
// failure leaves it and the later ones queued.
func (r *Router) writePendingDecisions() error {
	for len(r.pendingDec) > 0 {
		if err := AppendJSONL(DecisionPath(r.o.Root), r.pendingDec[0]); err != nil {
			return fmt.Errorf("decision log: %w", err)
		}
		r.pendingDec = r.pendingDec[1:]
	}
	return nil
}

// fileSize is the file's size, 0 when it does not exist, -1 on another error.
func fileSize(path string) int64 {
	st, err := os.Stat(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil || !st.Mode().IsRegular() {
		return -1
	}
	return st.Size()
}

// tailParses reports whether the log ends with a newline and its last line
// is an outcome row, reading backwards in 64 KiB chunks.
func tailParses(path string) bool {
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
