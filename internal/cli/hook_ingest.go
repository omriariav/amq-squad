package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/flock"
)

const evidenceIngestUsage = `amq-squad evidence ingest - append one hook payload to the seat's ledger

Usage:
  amq-squad evidence ingest --me HANDLE [--root DIR] [--session S] [--json] < payload.json

Appends one line with a monotonic seq to <session root>/extensions/amq-squad/
ledger/HANDLE.jsonl (0600, O_APPEND, fsynced, flock held across the seq read
and the append). Malformed JSON is recorded (malformed: true, base64); over
1 MiB is recorded truncated (sha256 of the full bytes, the first 64 KiB). Both
exit 0; a usage or identity error exits 2.

Examples:
  amq-squad evidence ingest --me cto < posttooluse.json
`

// hookLedgerSchema versions hookLedgerLine, the seed of the 2b evidence
// ledger: one line per ingested payload.
const hookLedgerSchema = 1

type hookLedgerLine struct {
	Schema        int             `json:"schema"`
	Seq           uint64          `json:"seq"`
	At            string          `json:"at"` // wall clock, RFC 3339 UTC
	Handle        string          `json:"handle"`
	Session       string          `json:"session,omitempty"`
	Event         string          `json:"event,omitempty"` // hook_event_name
	Tool          string          `json:"tool,omitempty"`
	ToolUseID     string          `json:"tool_use_id,omitempty"`
	Outcome       string          `json:"outcome,omitempty"` // ok | error | the payload's own outcome
	PayloadSHA256 string          `json:"payload_sha256"`    // over the full raw stdin bytes
	PayloadBytes  int64           `json:"payload_bytes"`
	Malformed     bool            `json:"malformed,omitempty"`
	Truncated     bool            `json:"truncated,omitempty"`   // over 1 MiB: only the first 64 KiB kept
	Payload       json.RawMessage `json:"payload,omitempty"`     // the compacted payload
	PayloadBase64 string          `json:"payload_b64,omitempty"` // raw bytes when malformed or truncated
}

const hookTruncatedPrefix = 64 << 10

func runEvidenceIngest(args []string) error {
	h := newHookFlags("evidence ingest", evidenceIngestUsage)
	if err := h.parse(args); err != nil {
		return err
	}
	id, err := h.identity()
	if err != nil {
		return err
	}
	sum := sha256.New()
	head, err := io.ReadAll(io.TeeReader(io.LimitReader(hookStdin, hookMaxPayload+1), sum))
	if err != nil {
		return hookErrorf("evidence ingest: read stdin: %v", err)
	}
	line := newHookLedgerLine(id, head, hookNow())
	if len(head) > hookMaxPayload { // keep hashing the rest; record the prefix only
		rest, err := io.Copy(sum, hookStdin)
		if err != nil {
			return hookErrorf("evidence ingest: read stdin: %v", err)
		}
		line = newHookLedgerLine(id, head[:hookTruncatedPrefix], hookNow())
		line.Truncated, line.Malformed, line.Payload = true, false, nil
		line.PayloadBase64 = base64.StdEncoding.EncodeToString(head[:hookTruncatedPrefix])
		line.PayloadBytes, line.PayloadSHA256 = int64(len(head))+rest, "sha256:"+hex.EncodeToString(sum.Sum(nil))
	}
	path := hookExtPath(id.Root, "ledger", id.Handle+".jsonl")
	if line.Seq, err = appendHookLedger(path, line); err != nil {
		return hookErrorf("evidence ingest: %v", err)
	}
	if *h.jsonOut {
		return writeCompactJSONEnvelope(os.Stdout, "evidence_ingest", map[string]any{"path": path, "seq": line.Seq})
	}
	return nil
}

func newHookLedgerLine(id hookIdentity, raw []byte, now time.Time) hookLedgerLine {
	sum := sha256.Sum256(raw)
	line := hookLedgerLine{Schema: hookLedgerSchema, At: now.UTC().Format(time.RFC3339Nano), Handle: id.Handle, Session: id.Session, PayloadSHA256: "sha256:" + hex.EncodeToString(sum[:]), PayloadBytes: int64(len(raw))}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		line.Malformed, line.PayloadBase64 = true, base64.StdEncoding.EncodeToString(raw)
		return line
	}
	line.Payload = compact.Bytes()
	var p map[string]json.RawMessage
	_ = json.Unmarshal(raw, &p)
	str := func(keys ...string) string {
		for _, k := range keys {
			var s string
			if json.Unmarshal(p[k], &s) == nil && s != "" {
				return s
			}
		}
		return ""
	}
	line.Event, line.Tool, line.ToolUseID, line.Outcome = str("hook_event_name", "type"), str("tool_name", "toolName"), str("tool_use_id", "toolCallId"), str("outcome")
	for _, k := range []string{"tool_response", "toolResult", "result"} {
		var r map[string]any
		if line.Outcome == "" && len(p[k]) > 0 {
			line.Outcome = "ok"
			if json.Unmarshal(p[k], &r) == nil && (r["is_error"] == true || r["isError"] == true || (r["error"] != nil && r["error"] != "")) {
				line.Outcome = "error"
			}
		}
	}
	if string(p["isError"]) == "true" || string(p["is_error"]) == "true" {
		line.Outcome = "error"
	}
	return line
}

// appendHookLedger appends line under an exclusive flock on the ledger file
// itself, numbered one past the last parseable line, and fsyncs. A torn last
// line (a crash mid-write) is terminated first and skipped.
func appendHookLedger(path string, line hookLedgerLine) (uint64, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	err = flock.WithFile(f, path, func() error {
		last, torn, err := lastHookLedgerSeq(f)
		if err != nil {
			return err
		}
		line.Seq = last + 1
		b, _ := json.Marshal(line)
		if torn {
			b = append([]byte{'\n'}, b...)
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			return err
		}
		return f.Sync()
	})
	return line.Seq, err
}

// lastHookLedgerSeq finds the seq of the last parseable line: it reads the
// last 64 KiB, then (for long lines) the last 4 MiB, which holds at least one
// whole line since a line stays under 2 MiB. No side file, so no drift.
func lastHookLedgerSeq(f *os.File) (seq uint64, torn bool, err error) {
	st, err := f.Stat()
	if err != nil || st.Size() == 0 {
		return 0, false, err
	}
	for _, n := range []int64{64 << 10, 4 << 20} {
		n = min(n, st.Size())
		buf := make([]byte, n)
		if _, err := f.ReadAt(buf, st.Size()-n); err != nil {
			return 0, false, err
		}
		torn = buf[n-1] != '\n'
		lines := bytes.Split(buf, []byte{'\n'})
		for i := len(lines) - 1; i >= 0 && (i > 0 || n == st.Size()); i-- {
			var l struct{ Schema, Seq uint64 }
			if json.Unmarshal(lines[i], &l) == nil && l.Schema == hookLedgerSchema && l.Seq > 0 {
				return l.Seq, torn, nil
			}
		}
		if n == st.Size() {
			break
		}
	}
	return 0, torn, nil // nothing parseable: numbering starts over at 1
}
