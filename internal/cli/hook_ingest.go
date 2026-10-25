package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/ledger"
	"github.com/omriariav/amq-squad/v3/internal/task"
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
const hookLedgerSchema = ledger.LineSchema

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
	Truncated     bool            `json:"truncated,omitempty"`     // over 1 MiB: only the first 64 KiB kept
	Payload       json.RawMessage `json:"payload,omitempty"`       // the compacted payload
	PayloadBase64 string          `json:"payload_b64,omitempty"`   // raw bytes when malformed or truncated
	LeaseRenewal  string          `json:"lease_renewal,omitempty"` // renewed | skipped (store lock busy); 2b-1
}

const hookTruncatedPrefix = 64 << 10

func runEvidenceIngest(args []string) error {
	h := newHookFlags("evidence ingest", evidenceIngestUsage)
	if err := h.parse(args); err != nil {
		return err
	}
	// the shared config-dir check and digest report (P2-3); a change
	// quarantines the seat, the payload is still recorded
	id, _, err := h.seatIdentity(hookNow())
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
	line.LeaseRenewal = renewLeaseOnToolResult(id, line)
	path := hookExtPath(id.Root, "ledger", id.Handle+".jsonl")
	if line.Seq, err = appendHookLedger(path, line); err != nil {
		return hookErrorf("evidence ingest: %v", err)
	}
	if *h.jsonOut {
		return writeCompactJSONEnvelope(os.Stdout, "evidence_ingest", map[string]any{"path": path, "seq": line.Seq, "lease_renewed": line.LeaseRenewal == "renewed"})
	}
	return nil
}

// ingestLeaseRenewInterval is the minimum age of a renewal before a tool
// result renews again (a tenth of the lease): most tool results cost a
// lock-free read only; one a minute writes the store (measured in
// docs/v3-spike/2b1-execution.md).
var ingestLeaseRenewInterval = task.DefaultLeaseDuration / 10

// renewLeaseOnToolResult renews the seat's claimed task lease on a tool
// result (review 1 ruling 3: renewal comes from evidence ingest on every
// tool result) and returns what the ledger line records: "renewed",
// "skipped" when another writer holds the store lock (the hook never waits
// for it; the next tool result renews), or "" when nothing was due. It never
// fails the ingest.
func renewLeaseOnToolResult(id hookIdentity, line hookLedgerLine) string {
	if line.Tool == "" || line.Outcome == "" || line.Malformed || line.Truncated {
		return ""
	}
	store, err := task.Open(id.Root)
	if err != nil {
		return ""
	}
	_, renewed, err := store.RenewNoWait(id.Handle, task.DefaultLeaseDuration, ingestLeaseRenewInterval, hookNow().UTC())
	switch {
	case errors.Is(err, task.ErrBusy):
		return "skipped"
	case err == nil && renewed:
		return "renewed"
	}
	return ""
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

// appendHookLedger appends line through the ledger's one writer
// (ledger.Append: a flock on the file, numbered one past the last parseable
// line, fsynced; a torn last line terminated first).
func appendHookLedger(path string, line hookLedgerLine) (uint64, error) {
	return ledger.Append(path, func(seq uint64) any {
		line.Seq = seq
		return line
	})
}
