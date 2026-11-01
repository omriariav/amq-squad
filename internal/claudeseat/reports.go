package claudeseat

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Hook reports (P1-10 of the 2b-2 milestone B second pass): every hook call
// of a Claude seat appends a report, so a hook that was removed or never ran
// shows: a seat that ran (the core's launch ledger holds its accepted proof)
// with no report since its launch is a silent hook. Since brief 31-1 that is
// a seat warning in status (claude_hooks_silent), never a quarantine.

// Report is one hook report. Lines written before 3.1 also carry the config
// dir digest the hook saw; only the time is read.
type Report struct {
	At time.Time `json:"at"`
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
