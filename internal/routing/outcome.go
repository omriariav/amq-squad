package routing

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/flock"
	"github.com/omriariav/amq-squad/v3/internal/sealchain"
)

// RowSchema is the outcome log row schema.
const RowSchema = 1

// Policies (6.4), a closed list validated on every read. LeadPin is a task
// whose first attempt the lead pinned to a pool (task add --pool), written
// only when that pin exists. Manual is an attempt the router did not decide
// (a lead's task claim or dispatch to a pool, or a decision the router no
// longer has); its propensity is 0, meaning not drawn. jev_shadow is
// reserved for Jev (phase 5).
const (
	PolicyTable     = "table"
	PolicyExplore   = "explore"
	PolicyJevShadow = "jev_shadow"
	PolicyLeadPin   = "lead_pin"
	PolicyManual    = "manual"
)

// Policies is the closed policy list.
var Policies = []string{PolicyTable, PolicyExplore, PolicyJevShadow, PolicyLeadPin, PolicyManual}

// HistoryTampered is the reason code of an outcome row or decision whose
// tag does not verify: it is treated as absent and reported.
const HistoryTampered = "history_tampered"

// Verifier results of a row.
const (
	VerifierPass    = "pass"    // the attempt completed through its mechanical verifier
	VerifierFail    = "fail"    // the verifier (tests, exit code, schema) or the commit step refused it
	VerifierNotRun  = "not_run" // the attempt ended before a verdict (expired, released, ...)
	VerifierNoCheck = "none"    // the class has no verifier (summarize, plan, review)
	VerifierInvalid = "invalid" // the attempt's archived verdict is not the harness's (its seal failed)
)

// Skip is one chain entry the router passed over, with the reason code.
type Skip struct {
	Entry  string `json:"entry"`
	Reason string `json:"reason"`
}

// Features are the task facts the routing decision used.
type Features struct {
	DeclaredFiles     int    `json:"declared_files"`
	SizeBand          string `json:"size_band"`
	InputTokens       int    `json:"input_tokens"`
	Sensitive         bool   `json:"sensitive"`
	NoTests           bool   `json:"no_tests"`
	Privacy           string `json:"privacy"`
	PrivacyGlob       string `json:"privacy_glob,omitempty"`
	ImplementerFamily string `json:"implementer_family,omitempty"`
	Unchecked         string `json:"unchecked,omitempty"` // a declared tree that could not be checked for links
}

// Row is one outcome log row: one per attempt, with every field of plan 6.4
// (task, attempt, class, features, table tier, used tier, entry, skipped
// entries with reasons, policy, propensity, tokens, cost, wall time, wait
// time, outcome, verifier result, review findings, consumed smol outputs).
// Tokens and cost are null when the runtime gave no evidence; review
// findings are null outside a completed review.
type Row struct {
	Schema              int       `json:"schema"`
	At                  time.Time `json:"at"`
	Task                string    `json:"task"`
	Attempt             int       `json:"attempt"`
	Class               string    `json:"class"`
	Features            Features  `json:"features"`
	TableTier           string    `json:"table_tier"`
	UsedTier            string    `json:"used_tier"`
	Entry               string    `json:"entry"`
	Runtime             string    `json:"runtime"`
	Skipped             []Skip    `json:"skipped"`
	Policy              string    `json:"policy"`
	Propensity          float64   `json:"propensity"`
	ExplorationShare    float64   `json:"exploration_share"`
	TokensIn            *int64    `json:"tokens_in"`
	TokensOut           *int64    `json:"tokens_out"`
	CostUSD             *float64  `json:"cost_usd"`                // null without tokens or without an owner price
	UsageInvalid        string    `json:"usage_invalid,omitempty"` // why the usage is unknown: a counter that was not a count
	CostIndex           float64   `json:"cost_index"`              // the registry's relative cost of the entry
	WallMS              int64     `json:"wall_ms"`
	WaitMS              int64     `json:"wait_ms"`
	WaitReasons         []string  `json:"wait_reasons"`
	Outcome             string    `json:"outcome"`
	VerifierResult      string    `json:"verifier_result"`
	ReviewFindings      *int      `json:"review_findings"`
	ConsumedSmolOutputs []string  `json:"consumed_smol_outputs"` // "task/attempt" of each completed smol attempt this task consumed
	Holder              string    `json:"holder"`
	GrantedAt           time.Time `json:"granted_at"`
	EndedAt             time.Time `json:"ended_at"`
	// Seq and Prev chain the rows (chain.go): the row's place from 1 and the
	// tag of the row before it, both inside the signed bytes.
	Seq  int    `json:"seq"`
	Prev string `json:"prev"`
	// Tag is the HMAC-SHA256 of every other field, keyed by the session
	// seed (lead ruling, Codex pass 3): a hand edit or a same-size rewrite
	// fails it, and the row is treated as absent and reported.
	Tag string `json:"tag,omitempty"`
}

// hmacTag is the hex HMAC-SHA256 of v's JSON under key, after a domain
// label: sealchain.Tag, the one implementation the sealed state files share
// (phase 5 re-pass of 2b-2 B, ruling).
func hmacTag(key []byte, label string, v any) string { return sealchain.Tag(key, label, v) }

// Sign returns the row with its tag set.
func (r Row) Sign(key []byte) Row {
	r.Tag = ""
	r.Tag = hmacTag(key, "amq-squad routing outcome row v1", r)
	return r
}

// Verify reports whether the row's tag verifies under key and its policy
// is on the closed list.
func (r Row) Verify(key []byte) bool {
	want := r.Sign(key).Tag
	return r.Tag != "" && hmac.Equal([]byte(want), []byte(r.Tag)) && slices.Contains(Policies, r.Policy)
}

// SeedKey is the HMAC key of a session seed (hex).
func SeedKey(seed string) []byte { return sealchain.Key(seed) }

// VerifyRows splits rows into the ones whose tag verifies and the rest.
func VerifyRows(rows []Row, key []byte) (good, tampered []Row) {
	for _, r := range rows {
		if r.Verify(key) {
			good = append(good, r)
		} else {
			tampered = append(tampered, r)
		}
	}
	return good, tampered
}

// EvidenceComplete reports whether the row carries the runtime's token
// evidence (16.8: the weekly report uses only runtimes with complete
// evidence). A null cost with known tokens is a missing owner price, a
// configuration gap the report names per entry, not missing evidence.
func (r Row) EvidenceComplete() bool {
	return r.TokensIn != nil && r.TokensOut != nil
}

// Dir is the routing directory under an AMQ session root, beside the hook
// ledger (briefs ruling 3).
func Dir(root string) string { return sealchain.Dir(root) }

// OutcomePath is the outcome log of a session root.
func OutcomePath(root string) string { return filepath.Join(Dir(root), "outcomes.jsonl") }

// DecisionPath holds the router's dispatch decisions until the attempt ends,
// so a scheduler restart keeps the policy, propensity and skipped entries.
func DecisionPath(root string) string { return filepath.Join(Dir(root), "decisions.jsonl") }

// AppendJSONL appends v as one line under an exclusive flock on the file
// itself and fsyncs before the lock is released: the 2a-3 ledger's writer.
// A torn last line (a crash mid-write) is terminated first, so the new line
// stays whole and readers skip the torn one.
func AppendJSONL(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return flock.WithFile(f, path, func() error {
		st, err := f.Stat()
		if err != nil {
			return err
		}
		line := buf.Bytes()
		if st.Size() > 0 {
			last := make([]byte, 1)
			if _, err := f.ReadAt(last, st.Size()-1); err != nil {
				return err
			}
			if last[0] != '\n' {
				line = append([]byte{'\n'}, line...)
			}
		}
		if _, err := f.Write(line); err != nil {
			return err
		}
		return f.Sync()
	})
}

// AppendRow appends one outcome row.
func AppendRow(root string, r Row) error {
	if r.Schema == 0 {
		r.Schema = RowSchema
	}
	return AppendJSONL(OutcomePath(root), r)
}

// ReadRows reads an outcome log. A missing file is no rows; a line that does
// not parse (a torn tail) is skipped and counted.
func ReadRows(path string) (rows []Row, skipped int, err error) {
	err = readLines(path, func(line []byte) {
		var r Row
		if json.Unmarshal(line, &r) != nil || r.Schema != RowSchema || r.Task == "" {
			skipped++
			return
		}
		rows = append(rows, r)
	})
	return rows, skipped, err
}

func readLines(path string, fn func([]byte)) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	rd := bufio.NewReaderSize(f, 64<<10)
	for {
		line, err := rd.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			fn(bytes.TrimSpace(line))
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
	}
}
