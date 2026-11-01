package schedproc

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/scheduler"
)

// The decisions log (decisions.jsonl under Dir) read back (brief 31-1's
// second PR): status shows its tail and reads the launch budget's
// first-message mark from it, and `amq-squad up` tails it for the roster's
// outcome. Every read is lock-free: the log is append-only, one JSON line
// per decision, and a torn last line is skipped.

// DecisionsName is the decisions log under Dir.
const DecisionsName = "decisions.jsonl"

// DecisionsPath is the root's decisions log.
func DecisionsPath(root string) string { return filepath.Join(Dir(root), DecisionsName) }

// DecisionLine is one line of the decisions log.
type DecisionLine struct {
	At time.Time `json:"at"`
	scheduler.Decision
}

// decisionsTailMax bounds a tail read (the last MiB of the log).
const decisionsTailMax = 1 << 20

// ReadDecisionsTail returns the well-formed lines of the log's last MiB,
// oldest first (none when the log is absent).
func ReadDecisionsTail(root string) ([]DecisionLine, error) {
	f, err := os.Open(DecisionsPath(root))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	off := max(st.Size()-decisionsTailMax, 0)
	lines, _, err := readDecisionsAt(f, off)
	return lines, err
}

// ReadDecisionsFrom returns the whole lines written at or after offset and
// the offset after the last whole line (a torn last line is read again
// next time).
func ReadDecisionsFrom(root string, offset int64) ([]DecisionLine, int64, error) {
	f, err := os.Open(DecisionsPath(root))
	if os.IsNotExist(err) {
		return nil, offset, nil
	}
	if err != nil {
		return nil, offset, err
	}
	defer f.Close()
	return readDecisionsAt(f, offset)
}

// DecisionsSize is the log's size now (0 when absent): where a reader that
// wants only what comes next starts.
func DecisionsSize(root string) int64 {
	st, err := os.Stat(DecisionsPath(root))
	if err != nil {
		return 0
	}
	return st.Size()
}

func readDecisionsAt(f *os.File, off int64) ([]DecisionLine, int64, error) {
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return nil, off, err
	}
	raw, err := io.ReadAll(f)
	if err != nil {
		return nil, off, err
	}
	end := bytes.LastIndexByte(raw, '\n') + 1 // whole lines only
	var out []DecisionLine
	for _, ln := range bytes.Split(raw[:end], []byte("\n")) {
		var d DecisionLine
		if len(bytes.TrimSpace(ln)) == 0 || json.Unmarshal(ln, &d) != nil || d.Kind == "" {
			continue // a partial first line of a tail read, or a torn line
		}
		out = append(out, d)
	}
	return out, off + int64(end), nil
}

// AppendDecision appends one decision line to the root's log (0600 in the
// 0700 scheduler directory): a decision taken outside the scheduler's
// process, such as the session it found absent and created, or what `up`
// stopped. One O_APPEND write per line.
func AppendDecision(root string, at time.Time, d scheduler.Decision) error {
	if err := os.MkdirAll(Dir(root), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(DecisionsPath(root), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	logLine(f, at, d)
	return f.Close()
}
