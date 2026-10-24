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
	"strings"
)

// Both logs are hash chains (Codex passes 4 and 5): every line carries its
// sequence number (seq, from 1) and the previous line's tag (prev) inside the
// signed bytes, and the head record (head.go) says how far each chain must
// reach, so deleting, reordering, duplicating or truncating signed lines
// breaks a chain. The walkers read in file order. A line that does not
// decode, has another schema, or whose tag fails is a break (the tag is
// checked before any field is trusted); a seq ahead of the chain is a gap,
// one break per missing seq, attributed to no task (the missing line's task
// is unknown, never the successor's), and the row re-anchors the chain; a
// seq at or behind it is a duplicate or a reorder and the line is not used.
// An unterminated last line is the torn tail: measured and reported, and the
// routing stop (checkTail).

// Problem is one break in the history. Task, Attempt and Seq are set where
// known.
type Problem struct {
	ID      string `json:"id"` // stable: the same break gives the same id on every read
	Log     string `json:"log"`
	Task    string `json:"task,omitempty"`
	Attempt int    `json:"attempt,omitempty"`
	Seq     int    `json:"seq,omitempty"`
	What    string `json:"what"`
}

// String names the break for the lead: id, what, and the task, attempt and
// seq where known.
func (p Problem) String() string {
	var where []string
	if p.Task != "" {
		where = append(where, "task "+p.Task)
	}
	if p.Attempt > 0 {
		where = append(where, fmt.Sprintf("attempt %d", p.Attempt))
	}
	if p.Seq > 0 {
		where = append(where, fmt.Sprintf("%s seq %d", p.Log, p.Seq))
	}
	if len(where) == 0 {
		where = append(where, "task unknown")
	}
	return p.ID + " (" + p.What + "; " + strings.Join(where, ", ") + ")"
}

func lineID(prefix string, line []byte) string {
	h := sha256.Sum256(line)
	return prefix + ":" + hex.EncodeToString(h[:8])
}

// chainHead is where the next line joins a chain.
type chainHead struct {
	Seq int    // the last good line's seq
	Tag string // the last good line's tag
}

// chainLink is one decoded, tag-verified line's place in its chain.
type chainLink struct {
	Task    string
	Attempt int
	Seq     int
	Prev    string
	Tag     string
}

// walk runs the chain rule over decoded lines: decode returns the link and
// ok, or the task (when readable) of a line that is a break.
func walk(path, log string, decode func([]byte) (chainLink, bool, string)) (good [][]byte, links []chainLink, problems []Problem, head chainHead, tail int, err error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil, nil, head, 0, nil
	}
	if err != nil {
		return nil, nil, nil, head, 0, err
	}
	defer f.Close()
	rd := bufio.NewReaderSize(f, 64<<10)
	for {
		line, rerr := rd.ReadBytes('\n')
		if rerr == io.EOF {
			tail = len(line) // an unterminated last line: the torn tail
			break
		}
		if rerr != nil {
			return nil, nil, nil, head, 0, fmt.Errorf("read %s: %w", path, rerr)
		}
		line = bytes.TrimRight(line, "\n")
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		l, ok, task := decode(line)
		if !ok {
			problems = append(problems, Problem{ID: lineID(log+"_line", line), Log: log, Task: task,
				What: "a " + log + " line that does not decode, has another schema, or whose tag fails"})
			continue
		}
		switch {
		case l.Seq == head.Seq+1 && l.Prev == head.Tag:
		case l.Seq > head.Seq+1:
			for s := head.Seq + 1; s < l.Seq; s++ {
				problems = append(problems, Problem{ID: fmt.Sprintf("gap:%s%d", logPrefix(log), s), Log: log, Seq: s,
					What: fmt.Sprintf("%s row %d is missing before row %d", log, s, l.Seq)})
			}
		case l.Seq == head.Seq+1:
			problems = append(problems, Problem{ID: lineID(log+"_prev", line), Log: log, Seq: l.Seq - 1,
				What: fmt.Sprintf("%s row %d does not follow the row before it: row %d was replaced", log, l.Seq, l.Seq-1)})
		default:
			problems = append(problems, Problem{ID: lineID(log+"_order", line), Log: log, Task: l.Task, Attempt: l.Attempt, Seq: l.Seq,
				What: fmt.Sprintf("%s row %d is a duplicate or out of order (chain at %d)", log, l.Seq, head.Seq)})
			continue
		}
		good, links = append(good, line), append(links, l)
		head = chainHead{Seq: l.Seq, Tag: l.Tag}
	}
	return good, links, problems, head, tail, nil
}

// verifyChain walks the outcome log.
func verifyChain(path string, key []byte) (good []Row, problems []Problem, head chainHead, tail int, err error) {
	lines, _, problems, head, tail, err := walk(path, "outcome", func(line []byte) (chainLink, bool, string) {
		var row Row
		if json.Unmarshal(line, &row) != nil || row.Schema != RowSchema || row.Task == "" || !row.Verify(key) {
			return chainLink{}, false, row.Task
		}
		return chainLink{Task: row.Task, Attempt: row.Attempt, Seq: row.Seq, Prev: row.Prev, Tag: row.Tag}, true, ""
	})
	for _, line := range lines {
		var row Row
		json.Unmarshal(line, &row)
		good = append(good, row)
	}
	return good, problems, head, tail, err
}

// decisionLine is one decoded decision-log line: a decision or a tamper record.
type decisionLine struct {
	decision *Decision
	tamper   *tamperRecord
}

// verifyDecisions walks the decision log.
func verifyDecisions(path string, key []byte) (good []decisionLine, problems []Problem, head chainHead, tail int, err error) {
	lines, _, problems, head, tail, err := walk(path, "decision", func(line []byte) (chainLink, bool, string) {
		var probe struct {
			Kind string `json:"kind"`
			Task string `json:"task"`
		}
		_ = json.Unmarshal(line, &probe)
		if probe.Kind != "" {
			var rec tamperRecord
			if json.Unmarshal(line, &rec) != nil || !rec.verify(key) {
				return chainLink{}, false, ""
			}
			return chainLink{Task: rec.Task, Seq: rec.Seq, Prev: rec.Prev, Tag: rec.Tag}, true, ""
		}
		var d Decision
		if json.Unmarshal(line, &d) != nil || d.Task == "" || d.Attempt <= 0 || !d.verify(key) {
			return chainLink{}, false, probe.Task
		}
		return chainLink{Task: d.Task, Attempt: d.Attempt, Seq: d.Seq, Prev: d.Prev, Tag: d.Tag}, true, ""
	})
	for _, line := range lines {
		var probe struct {
			Kind string `json:"kind"`
		}
		_ = json.Unmarshal(line, &probe)
		if probe.Kind != "" {
			var rec tamperRecord
			json.Unmarshal(line, &rec)
			good = append(good, decisionLine{tamper: &rec})
			continue
		}
		var d Decision
		json.Unmarshal(line, &d)
		good = append(good, decisionLine{decision: &d})
	}
	return good, problems, head, tail, err
}
