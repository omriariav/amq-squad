package routing

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// The outcome log is a hash chain (Codex pass 4, N1a): every row carries its
// sequence number (seq, from 1) and the previous row's tag (prev) inside the
// signed bytes, so deleting, reordering or duplicating signed lines breaks
// the chain. verifyChain walks it in file order. A line that does not
// decode, has another schema, or whose tag fails is a problem (N1c: the tag
// is checked before any field is trusted); a seq ahead of the chain is a
// gap (rows were removed) and the row re-anchors the chain; a seq at or
// behind it is a duplicate or a reorder and the row is not used. Every
// problem is history_tampered. An unterminated last line is the torn tail,
// which the tail check owns, and is not a problem here.

// Problem is one break in the outcome history.
type Problem struct {
	ID   string // stable: the same break gives the same id on every read
	Task string // the task the broken line names, when it decodes; "" when unknown
	What string
}

func lineID(prefix string, line []byte) string {
	h := sha256.Sum256(line)
	return prefix + ":" + hex.EncodeToString(h[:8])
}

// chainHead is where the next row joins the chain.
type chainHead struct {
	Seq int    // the last good row's seq
	Tag string // the last good row's tag
}

// verifyChain reads the log at path and returns its good rows, its problems
// and the chain head.
func verifyChain(path string, key []byte) (good []Row, problems []Problem, head chainHead, err error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil, head, nil
	}
	if err != nil {
		return nil, nil, head, err
	}
	defer f.Close()
	rd := bufio.NewReaderSize(f, 64<<10)
	for {
		line, rerr := rd.ReadBytes('\n')
		if rerr == io.EOF {
			break // an unterminated last line is the torn tail (checkTail)
		}
		if rerr != nil {
			return nil, nil, head, fmt.Errorf("read %s: %w", path, rerr)
		}
		line = bytes.TrimRight(line, "\n")
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var row Row
		if json.Unmarshal(line, &row) != nil || row.Schema != RowSchema || row.Task == "" || !row.Verify(key) {
			p := Problem{ID: lineID("line", line), What: "a line that does not decode, has another schema, or whose tag fails"}
			if row.Task != "" {
				p.Task = row.Task
			}
			problems = append(problems, p)
			continue
		}
		switch {
		case row.Seq == head.Seq+1 && row.Prev == head.Tag:
		case row.Seq > head.Seq+1:
			// rows removed before this one: report the gap, re-anchor here
			problems = append(problems, Problem{ID: fmt.Sprintf("gap:%d-%d", head.Seq+1, row.Seq-1), Task: row.Task,
				What: fmt.Sprintf("rows %d to %d are missing before row %d", head.Seq+1, row.Seq-1, row.Seq)})
		case row.Seq == head.Seq+1:
			// the right place but another predecessor: the row before it was
			// replaced or removed; report, re-anchor here
			problems = append(problems, Problem{ID: lineID("prev", line), Task: row.Task,
				What: fmt.Sprintf("row %d does not follow the row before it", row.Seq)})
		default:
			problems = append(problems, Problem{ID: lineID("order", line), Task: row.Task,
				What: fmt.Sprintf("row %d is a duplicate or out of order (chain at %d)", row.Seq, head.Seq)})
			continue
		}
		good = append(good, row)
		head = chainHead{Seq: row.Seq, Tag: row.Tag}
	}
	return good, problems, head, nil
}
