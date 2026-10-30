// Package ledger reads the evidence 3.0 keeps per session: the hook ledger
// (2a-3, one JSONL file per seat under <root>/extensions/amq-squad/ledger/)
// and the outcome log (2b-4, beside it). It replaces the v2 command evidence
// store (internal/commandevidence/evidence.go, retired by 3-2) and carries its
// two semantics: redaction (the secret-name rule and the secret-argument
// rule) and the task link (the lines a task's lease holders wrote inside
// their lease windows). Its readers never write; append.go is the one writer.
package ledger

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	redactrule "github.com/omriariav/amq-squad/v3/internal/redact"
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
	return filepath.Join(root, "extensions", "amq-squad", "routing", "outcomes.jsonl")
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

// SecretName is the v2 redaction rule for names (evidenceSecretName). The
// rule itself lives in internal/redact, which the repository's evidence scan
// and the redaction step share (briefs ruling 18: one rule).
func SecretName(name string) bool { return redactrule.LedgerSecretName(name) }

// secretFlag reports a flag that takes a secret (--token, --api-key,
// --access-token, --password ...): the name after the dashes is a secret name.
func secretFlag(word string) bool {
	if !strings.HasPrefix(word, "-") || strings.Contains(word, "=") {
		return false
	}
	return SecretName(strings.ReplaceAll(strings.TrimLeft(word, "-"), "-", "_"))
}

// assignedSecret returns the offset of the value in a key=value word whose
// key names a secret (--token=x, password=x, API_KEY=x), or -1.
func assignedSecret(word string) int {
	key, _, ok := strings.Cut(word, "=")
	if !ok || strings.ContainsAny(key, " \t\n") || !SecretName(strings.ReplaceAll(strings.TrimLeft(key, "-"), "-", "_")) {
		return -1
	}
	return len(key) + 1
}

// span is one shell word of a command string: its unquoted text, where it
// sits in the original (quotes included), and whether it was quoted.
type span struct {
	text       string
	start, end int
	quoted     bool
}

// shellWords splits a command line into words, honoring single and double
// quotes and backslashes, so a quoted secret is one word. ok is false when
// the line ends inside a quote: the caller falls back to whitespace.
func shellWords(s string) (out []span, ok bool) {
	var b strings.Builder
	start, in, quoted, quote := 0, false, false, byte(0)
	flush := func(end int) {
		if in {
			out = append(out, span{b.String(), start, end, quoted})
		}
		b.Reset()
		in, quoted = false, false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0 && c == quote:
			quote = 0
		case quote != 0:
			if c == '\\' && quote == '"' && i+1 < len(s) {
				i++
				c = s[i]
			}
			b.WriteByte(c)
		case c == '\'' || c == '"':
			if !in {
				start, in = i, true
			}
			quote, quoted = c, true
		case c == '\\' && i+1 < len(s):
			if !in {
				start, in = i, true
			}
			i++
			b.WriteByte(s[i])
		case c == ' ' || c == '\t' || c == '\n' || c == ';' || c == '&' || c == '|':
			flush(i)
		default:
			if !in {
				start, in = i, true
			}
			b.WriteByte(c)
		}
	}
	flush(len(s))
	return out, quote == 0
}

// urlUserinfo is the user:password@ part of a URL.
var urlUserinfo = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)[^/\s@'"]+@`)

// scrubArgs is the v2 secret-argument rule (obviousSecretArg) over a command
// line, quote-aware: the word after a secret flag, the value of a secret
// key=value, the credential after "Bearer"/"Basic", an "Authorization: ..."
// header and a URL's userinfo are replaced. A quoted word (a nested
// `sh -c '...'` included) is scrubbed again inside; a line that does not
// parse (an open quote) falls back to whitespace words (fix 2, N1).
func scrubArgs(s string) string {
	s = urlUserinfo.ReplaceAllString(s, "${1}[redacted]@")
	words, ok := shellWords(s)
	if !ok {
		return scrubFields(s)
	}
	type cut struct {
		start, end int
		with       string
	}
	var cuts []cut
	for i, w := range words {
		lw := strings.ToLower(w.text)
		switch {
		case i > 0 && (secretFlag(words[i-1].text) || strings.EqualFold(words[i-1].text, "bearer") || strings.EqualFold(words[i-1].text, "basic")):
			cuts = append(cuts, cut{w.start, w.end, "[redacted]"})
		case strings.HasPrefix(lw, "authorization:"):
			cuts = append(cuts, cut{w.start, w.end, "[redacted]"})
		case assignedSecret(w.text) >= 0 && !w.quoted:
			cuts = append(cuts, cut{w.start + assignedSecret(w.text), w.end, "[redacted]"})
		case assignedSecret(w.text) >= 0:
			cuts = append(cuts, cut{w.start, w.end, "[redacted]"})
		case w.quoted:
			if inner := scrubArgs(w.text); inner != w.text {
				q := s[w.start : w.start+1]
				if q != "'" && q != "\"" {
					q = "'"
				}
				cuts = append(cuts, cut{w.start, w.end, q + inner + q})
			}
		}
	}
	for i := len(cuts) - 1; i >= 0; i-- {
		s = s[:cuts[i].start] + cuts[i].with + s[cuts[i].end:]
	}
	return s
}

// scrubFields is the fallback for a line that does not parse (an open
// quote): the word boundaries are unknown, so everything after a secret flag,
// "Bearer"/"Basic" or a secret key= is redacted, to the end of the line; an
// unterminated quoted secret ('alpha beta) never leaves its tail behind (5
// re-pass, P1-3).
func scrubFields(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		switch {
		case i > 0 && (secretFlag(words[i-1]) || strings.EqualFold(words[i-1], "bearer") || strings.EqualFold(words[i-1], "basic")):
			return strings.Join(append(words[:i:i], "[redacted]"), " ")
		case assignedSecret(strings.TrimLeft(w, "'\"")) >= 0:
			bare := strings.TrimLeft(w, "'\"")
			key := w[:len(w)-len(bare)] + bare[:assignedSecret(bare)]
			return strings.Join(append(words[:i:i], key+"[redacted]"), " ")
		}
	}
	return strings.Join(words, " ")
}

// countField is a key whose numeric value is a count, not a secret: the
// explicit list input_tokens, output_tokens and cache_*_tokens (fix 2).
func countField(key string) bool {
	k := strings.ToLower(key)
	return k == "input_tokens" || k == "output_tokens" || strings.HasPrefix(k, "cache_") && strings.HasSuffix(k, "_tokens")
}

// Redact replaces the value of every key that names a secret anywhere in a
// line, payload included (only numeric count fields such as input_tokens
// stay), applies the secret-argument rule to every string, and in an argv
// array replaces the element after a secret flag.
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
			if _, isNumber := val.(json.Number); SecretName(k) && !(isNumber && countField(k)) {
				x[k] = "[redacted]"
			} else {
				x[k] = redact(val)
			}
		}
	case []any:
		for i := range x {
			if prev, ok := x[max(i-1, 0)].(string); i > 0 && ok && secretFlag(prev) {
				x[i] = "[redacted]"
				continue
			}
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

// MaxLine bounds one evidence line a task read keeps; a longer line is
// skipped and named as incomplete evidence.
const MaxLine = 4 << 20

// ForTask is the task link (v2 evidenceTaskLink, rebuilt on 3.0's stores):
// for every ended attempt and the live lease, the holder's ledger lines whose
// time falls in the lease window. The live lease's window ends at its expiry
// or now, whichever is first: a line written after an expiry the scheduler
// has not processed yet is not the task's (5 re-pass, P2-8). Each ledger is
// streamed whole; whatever could not be read is returned as incomplete, with
// the reason, never dropped in silence (P2-7).
func ForTask(root string, t task.Task, now time.Time) (out []TaskLine, incomplete []string, err error) {
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
		end := now
		if !l.ExpiresAt.IsZero() && l.ExpiresAt.Before(end) {
			end = l.ExpiresAt
		}
		ws = append(ws, window{l.Holder, t.Attempt, l.GrantedAt, end})
	}
	for _, w := range ws {
		why, err := streamLines(Path(root, w.holder), func(ln []byte) {
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
		})
		incomplete = append(incomplete, why...)
		if err != nil {
			return out, incomplete, err
		}
	}
	return out, incomplete, nil
}

// Outcomes are the redacted outcome log rows that name a task (field
// "task"), from the whole log, streamed (5 re-pass, P2-7: no read bound
// drops a later row); lines that could not be read are named as incomplete.
func Outcomes(root, taskID string) (out []json.RawMessage, incomplete []string, err error) {
	incomplete, err = streamLines(OutcomePath(root), func(ln []byte) {
		var row struct {
			Task string `json:"task"`
		}
		if json.Unmarshal(ln, &row) == nil && row.Task == taskID {
			out = append(out, Redact(ln))
		}
	})
	return out, incomplete, err
}

// streamLines calls fn with each line of a file, the whole file, in order. A
// missing file is no lines. A line longer than MaxLine is skipped and named.
func streamLines(path string, fn func([]byte)) ([]string, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var incomplete []string
	r := bufio.NewReaderSize(f, 64<<10)
	for n := 1; ; n++ {
		ln, err := r.ReadBytes('\n')
		switch {
		case len(ln) > MaxLine:
			incomplete = append(incomplete, fmt.Sprintf("%s line %d is over %d bytes and was not read", filepath.Base(path), n, MaxLine))
		case len(bytes.TrimSpace(ln)) > 0:
			fn(bytes.TrimRight(ln, "\r\n"))
		}
		if err == io.EOF {
			return incomplete, nil
		}
		if err != nil {
			return incomplete, err
		}
	}
}
