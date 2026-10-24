package routing

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/flock"
)

// RowSchema is the outcome log row schema.
const RowSchema = 1

// Policies (6.4). LeadPin is a task whose first attempt the lead pinned to a
// pool (task add --pool); jev_shadow is reserved for Jev (phase 5).
const (
	PolicyTable     = "table"
	PolicyExplore   = "explore"
	PolicyJevShadow = "jev_shadow"
	PolicyLeadPin   = "lead_pin"
)

// Verifier results of a row.
const (
	VerifierPass    = "pass"    // the attempt completed through its mechanical verifier
	VerifierFail    = "fail"    // the verifier (tests, exit code, schema) or the commit step refused it
	VerifierNotRun  = "not_run" // the attempt ended before a verdict (expired, released, ...)
	VerifierNoCheck = "none"    // the class has no verifier (summarize, plan, review)
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
	CostUSD             *float64  `json:"cost_usd"`
	WallMS              int64     `json:"wall_ms"`
	WaitMS              int64     `json:"wait_ms"`
	WaitReasons         []string  `json:"wait_reasons"`
	Outcome             string    `json:"outcome"`
	VerifierResult      string    `json:"verifier_result"`
	ReviewFindings      *int      `json:"review_findings"`
	ConsumedSmolOutputs []string  `json:"consumed_smol_outputs"`
	Holder              string    `json:"holder"`
	GrantedAt           time.Time `json:"granted_at"`
	EndedAt             time.Time `json:"ended_at"`
}

// EvidenceComplete reports whether the row carries the runtime's token and
// cost evidence (16.8: the weekly report uses only runtimes with complete
// evidence).
func (r Row) EvidenceComplete() bool {
	return r.TokensIn != nil && r.TokensOut != nil && r.CostUSD != nil
}

// Dir is the routing directory under an AMQ session root, beside the hook
// ledger (briefs ruling 3).
func Dir(root string) string { return filepath.Join(root, "extensions", "amq-squad", "routing") }

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
