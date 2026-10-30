package claudeseat

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Hook reports (P1-10 of the 2b-2 milestone B second pass): every context
// call of a Claude seat's hook records the config dir digest it saw, so a
// hook that was removed is itself the drift signal: a seat that ran (the
// core's launch ledger holds its accepted proof) with no report since its
// launch is quarantined at its exit (claude_hooks_silent). Residual: the
// reports file is same-UID writable, so a seat can forge a report; a forged
// report hides a removed hook, never a changed digest, which the exit check
// re-hashes itself.

// HooksSilent is the quarantine code of a Claude seat that ran with no hook
// report.
const HooksSilent = "claude_hooks_silent"

// Report is one hook report.
type Report struct {
	At     time.Time `json:"at"`
	Digest string    `json:"digest"`
	OK     bool      `json:"ok"`
}

// ReportsPath is <root>/extensions/amq-squad/claude-config/<handle>.jsonl.
func ReportsPath(root, handle string) string {
	return filepath.Join(root, "extensions", "amq-squad", "claude-config", handle+".jsonl")
}

// RecordReport appends one report (0600 in a 0700 directory).
func RecordReport(root, handle string, r Report) error {
	p := ReportsPath(root, handle)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	b, _ := json.Marshal(r)
	_, err = f.Write(append(b, '\n'))
	return err
}

// ReportsSince counts the reports at or after since (a missing file is none).
func ReportsSince(root, handle string, since time.Time) (int, error) {
	f, err := os.Open(ReportsPath(root, handle))
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var r Report
		if json.Unmarshal(sc.Bytes(), &r) == nil && !r.At.Before(since) {
			n++
		}
	}
	return n, sc.Err()
}
