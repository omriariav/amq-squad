// Package ledger reads the evidence 3.0 keeps per session: the hook ledger
// (2a-3, one JSONL file per seat under <root>/extensions/amq-squad/ledger/)
// and the outcome log (2b-4, beside it). It replaces the v2 command evidence
// store (internal/commandevidence/evidence.go, retired by 3-2) and carries its
// two semantics: redaction (the secret-name rule and the secret-argument
// rule) and the task link (the lines a task's lease holders wrote inside
// their lease windows). It never writes.
package ledger

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// MaxRead bounds one ledger read.
const MaxRead = 8 << 20

// Path is a seat's hook ledger.
func Path(root, handle string) string {
	return filepath.Join(root, "extensions", "amq-squad", "ledger", handle+".jsonl")
}

// OutcomePath is the session's outcome log (2b-4: under the AMQ root per
// session, beside the hook ledger; the file name is 2b-4's to confirm).
func OutcomePath(root string) string {
	return filepath.Join(root, "extensions", "amq-squad", "outcomes.jsonl")
}

// Tail returns the last n complete JSON lines of a file, reading at most
// MaxRead bytes from its end; truncated reports a cut head.
func Tail(path string, n int) (lines [][]byte, truncated bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	off := max(st.Size()-MaxRead, 0)
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return nil, false, err
	}
	raw, err := io.ReadAll(f)
	if err != nil {
		return nil, false, err
	}
	for i, ln := range bytes.Split(raw, []byte("\n")) {
		if (i == 0 && off > 0) || len(bytes.TrimSpace(ln)) == 0 || !json.Valid(ln) {
			continue // the partial first line of a tail read, blank or torn lines
		}
		lines = append(lines, ln)
	}
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, off > 0, nil
}

// SecretName is the v2 redaction rule for names (evidenceSecretName).
func SecretName(name string) bool {
	upper := strings.ToUpper(name)
	for _, marker := range []string{"TOKEN", "SECRET", "PASSWORD", "PASSWD", "CREDENTIAL", "PRIVATE_KEY", "API_KEY"} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}

var (
	secretAssign = []string{"token=", "secret=", "password=", "passwd=", "api-key=", "api_key=", "private-key="}
	secretFlags  = map[string]bool{"--token": true, "--secret": true, "--password": true, "--api-key": true, "--private-key": true}
)

// scrubArgs is the v2 secret-argument rule (obviousSecretArg) over a command
// line: a word carrying token=, password=, ... and the word after --token,
// --password, ... are replaced.
func scrubArgs(s string) string {
	words := strings.Fields(s)
	changed := false
	for i, w := range words {
		lw := strings.ToLower(w)
		hit := i > 0 && secretFlags[strings.ToLower(words[i-1])]
		for _, p := range secretAssign {
			hit = hit || strings.Contains(lw, p)
		}
		if hit {
			words[i], changed = "[redacted]", true
		}
	}
	if !changed {
		return s
	}
	return strings.Join(words, " ")
}

// Redact replaces the value of every key that names a secret anywhere in a
// line, payload included (token counts, numbers, stay), and applies the
// secret-argument rule to every string.
func Redact(raw []byte) json.RawMessage {
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&v) != nil {
		return raw
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if enc.Encode(redact(v)) != nil {
		return raw
	}
	return bytes.TrimRight(buf.Bytes(), "\n")
}

func redact(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if _, isNumber := val.(json.Number); SecretName(k) && !isNumber {
				x[k] = "[redacted]"
			} else {
				x[k] = redact(val)
			}
		}
	case []any:
		for i := range x {
			x[i] = redact(x[i])
		}
	case string:
		return scrubArgs(x)
	}
	return v
}

// TaskLine is one redacted hook ledger line linked to a task: written by a
// holder of the task's lease inside that lease's window.
type TaskLine struct {
	Holder  string          `json:"holder"`
	Attempt int             `json:"attempt"`
	Line    json.RawMessage `json:"line"`
}

// ForTask is the task link (v2 evidenceTaskLink, rebuilt on 3.0's stores):
// for every ended attempt and the live lease, the holder's ledger lines whose
// time falls in the lease window.
func ForTask(root string, t task.Task, now time.Time) ([]TaskLine, error) {
	type window struct {
		holder   string
		n        int
		from, to time.Time
	}
	var ws []window
	for _, a := range t.Attempts {
		ws = append(ws, window{a.Holder, a.N, a.GrantedAt, a.EndedAt})
	}
	if l := t.Lease; l != nil {
		ws = append(ws, window{l.Holder, t.Attempt, l.GrantedAt, now})
	}
	var out []TaskLine
	for _, w := range ws {
		lines, _, err := Tail(Path(root, w.holder), 0)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return out, err
		}
		for _, ln := range lines {
			var head struct {
				At string `json:"at"`
			}
			at, perr := time.Time{}, json.Unmarshal(ln, &head)
			if perr == nil {
				at, perr = time.Parse(time.RFC3339Nano, head.At)
			}
			if perr == nil && !at.Before(w.from) && !at.After(w.to) {
				out = append(out, TaskLine{Holder: w.holder, Attempt: w.n, Line: Redact(ln)})
			}
		}
	}
	return out, nil
}

// Outcomes are the redacted outcome log rows that name a task (field "task").
func Outcomes(root, taskID string) ([]json.RawMessage, error) {
	f, err := os.Open(OutcomePath(root))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []json.RawMessage
	sc := bufio.NewScanner(io.LimitReader(f, MaxRead))
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		var row struct {
			Task string `json:"task"`
		}
		if json.Unmarshal(sc.Bytes(), &row) == nil && row.Task == taskID {
			out = append(out, Redact(append([]byte(nil), sc.Bytes()...)))
		}
	}
	return out, sc.Err()
}
