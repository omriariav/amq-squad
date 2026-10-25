package ledger

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/omriariav/amq-squad/v3/internal/flock"
	redactrule "github.com/omriariav/amq-squad/v3/internal/redact"
)

// The one writer of a seat's hook ledger (moved here from the evidence
// ingest verb by v3-5-verifier, so the commit step writes the verifier's
// evidence line through the same append). Every line is redacted on its way
// to disk (RedactForWrite), not only when it is read.

// LineSchema is the schema of a hook ledger line (the ingest verb's
// hookLedgerLine; the verifier's line uses it too).
const LineSchema = 1

// Append appends one JSON line to a ledger under an exclusive flock on the
// file itself, numbered one past the last parseable line (line builds the
// value for that seq), and fsyncs. A torn last line (a crash mid-write) is
// terminated first and skipped. The value is encoded without HTML escaping.
func Append(path string, line func(seq uint64) any) (uint64, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	var seq uint64
	err = flock.WithFile(f, path, func() error {
		last, torn, err := lastSeq(f)
		if err != nil {
			return err
		}
		seq = last + 1
		// An Encoder without HTML escaping: json.Marshal would grow a payload of
		// '<' sixfold. The Encoder appends the newline.
		var buf bytes.Buffer
		if torn {
			buf.WriteByte('\n')
		}
		raw, err := json.Marshal(line(seq))
		if err != nil {
			return err
		}
		red, err := RedactForWrite(raw)
		if err != nil {
			return err
		}
		buf.Write(red)
		buf.WriteByte('\n')
		if _, err := f.Write(buf.Bytes()); err != nil {
			return err
		}
		return f.Sync()
	})
	return seq, err
}

// RedactForWrite is the redaction a ledger line passes before it is
// written: every value under a secret-class name that is not a number
// becomes [redacted]; every other string passes the secret-argument rule
// and the credential-shape rule (redact.Text), an argv's element after a
// secret flag included; numbers stay (the usage counters status and routing
// read). The line is re-encoded without HTML escaping (its keys sorted).
func RedactForWrite(raw []byte) ([]byte, error) {
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(redactWrite(v)); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func redactWrite(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if _, isNumber := val.(json.Number); SecretName(k) && !isNumber {
				x[k] = "[redacted]"
			} else {
				x[k] = redactWrite(val)
			}
		}
	case []any:
		for i := range x {
			if prev, ok := x[max(i-1, 0)].(string); i > 0 && ok && secretFlag(prev) {
				x[i] = "[redacted]"
				continue
			}
			x[i] = redactWrite(x[i])
		}
	case string:
		return redactrule.Block(scrubArgs(x))
	}
	return v
}

// lastSeq finds the seq of the last parseable line, reading backwards in
// 64 KiB chunks until one parses, with no bound on line length. No side
// file, so no drift.
func lastSeq(f *os.File) (seq uint64, torn bool, err error) {
	st, err := f.Stat()
	if err != nil || st.Size() == 0 {
		return 0, false, err
	}
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, st.Size()-1); err != nil {
		return 0, false, err
	}
	torn = last[0] != '\n'
	var tail []byte // the unexamined, possibly partial, first line of what was read
	for off := st.Size(); off > 0; {
		n := min(int64(64<<10), off)
		off -= n
		chunk := make([]byte, n, n+int64(len(tail)))
		if _, err := f.ReadAt(chunk, off); err != nil {
			return 0, torn, err
		}
		lines := bytes.Split(append(chunk, tail...), []byte{'\n'})
		first := 1 // lines[0] may continue in the previous chunk
		if off == 0 {
			first = 0
		}
		for i := len(lines) - 1; i >= first; i-- {
			var l struct{ Schema, Seq uint64 }
			if json.Unmarshal(lines[i], &l) == nil && l.Schema == LineSchema && l.Seq > 0 {
				return l.Seq, torn, nil
			}
		}
		tail = lines[0]
	}
	return 0, torn, nil // nothing parseable: numbering starts over at 1
}
