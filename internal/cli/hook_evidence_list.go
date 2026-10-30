package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/ledger"
)

// The evidence verb: `ingest` (2a-3, the hook writer) and `list`, the one
// read of the hook ledger for the owner and amq-noc until 2b-4's outcome log
// (lead ruling, 2b-1). The v2 task-bound subverbs (run, show, recover,
// lookup and the v2 list) went with the v2 task store.

const evidenceUsage = `amq-squad evidence - the seat's evidence ledger

Usage:
  amq-squad evidence ingest --me HANDLE < hook-payload.json
  amq-squad evidence list --me HANDLE [--root DIR] [--last N] [--json]

ingest appends one hook payload to <session root>/extensions/amq-squad/ledger/
HANDLE.jsonl and renews the seat's claimed task lease (never waiting for the
store lock). list prints the last N lines of that ledger (default 20), with
values under secret-looking keys (token, secret, password, credential,
private key, api key) and secret-looking arguments (--token X, password=...)
redacted. It never writes.

Examples:
  amq-squad evidence list --me impl-1
  amq-squad evidence list --me impl-1 --last 5 --json
`

func runEvidence(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, evidenceUsage)
		return nil
	}
	switch args[0] {
	case "ingest":
		return runEvidenceIngest(args[1:])
	case "list":
		return runEvidenceList(args[1:])
	default:
		return unknownSubcommandError("evidence", args[0], "ingest", "list")
	}
}

// evidenceListMaxRead bounds the ledger tail read for list.
const evidenceListMaxRead = 8 << 20

type evidenceListData struct {
	Handle    string            `json:"handle"`
	Path      string            `json:"path"`
	Lines     []json.RawMessage `json:"lines"`
	Truncated bool              `json:"truncated,omitempty"` // the file was larger than the tail read
}

func runEvidenceList(args []string) error {
	fs := flag.NewFlagSet("evidence list", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, evidenceUsage) }
	me := fs.String("me", "", "seat whose ledger to read (required)")
	root := fs.String("root", "", "session root (default AM_ROOT)")
	last := fs.Int("last", 20, "how many of the last lines")
	jsonOut := fs.Bool("json", false, "emit a JSON envelope")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	handle := strings.TrimSpace(*me)
	if !hookHandlePattern.MatchString(handle) {
		return usageErrorf("evidence list: --me %q is missing or not a seat handle", handle)
	}
	r := strings.TrimSpace(*root)
	if r == "" {
		r = strings.TrimSpace(os.Getenv("AM_ROOT"))
	}
	if r == "" || !filepath.IsAbs(r) || filepath.Clean(r) != r {
		return usageErrorf("evidence list: session root %q must be a clean absolute path (--root or AM_ROOT)", r)
	}
	if *last <= 0 {
		return usageErrorf("evidence list: --last must be positive")
	}
	path := ledger.Path(r, handle)
	lines, truncated, err := ledger.Tail(path, *last)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("evidence list: %w", err)
	}
	data := evidenceListData{Handle: handle, Path: path, Lines: []json.RawMessage{}, Truncated: truncated}
	for _, ln := range lines {
		data.Lines = append(data.Lines, ledger.Redact(ln))
	}
	if *jsonOut {
		return printJSONEnvelope("evidence_list", data)
	}
	if len(data.Lines) == 0 {
		fmt.Printf("No evidence for %s (%s).\n", handle, path)
		return nil
	}
	for _, raw := range data.Lines {
		var l hookLedgerLine
		if json.Unmarshal(raw, &l) != nil {
			continue
		}
		extra := ""
		if l.Malformed || l.Truncated {
			extra = " (payload kept as bytes)"
		}
		if l.LeaseRenewal != "" {
			extra += " lease " + l.LeaseRenewal
		}
		fmt.Printf("%d\t%s\t%s\t%s\t%s%s\n", l.Seq, l.At, orDash(l.Event), orDash(l.Tool), orDash(l.Outcome), extra)
	}
	return nil
}
