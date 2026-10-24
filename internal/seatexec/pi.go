package seatexec

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// pi resume caveat (amendment 23 (a), plan 4.1, R5): upstream resumes a pi
// seat with `pi --session-id <recorded id>` and relies on pi's
// create-if-missing. That loses context silently in exactly one case: the
// seat already took a turn (its evidence ledger shows a tool result) and the
// session file for the recorded id is gone. seat-exec refuses that resume.

// PiSessionID returns the value of --session-id (or --session-id=V) in the
// provider argv that AMQ's pi adapter composed.
func PiSessionID(argv []string) string {
	for i, arg := range argv {
		if arg == "--session-id" && i+1 < len(argv) {
			return argv[i+1]
		}
		if v, ok := strings.CutPrefix(arg, "--session-id="); ok {
			return v
		}
	}
	return ""
}

// LedgerHasToolResult reports whether the seat's evidence ledger
// (<root>/extensions/amq-squad/ledger/<handle>.jsonl, written by `evidence
// ingest`) holds at least one tool result: a line with a tool and an outcome,
// or a PostToolUse / tool_result event. A missing ledger has none.
func LedgerHasToolResult(path string) (bool, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var line struct {
			Event   string `json:"event"`
			Tool    string `json:"tool"`
			Outcome string `json:"outcome"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		if (line.Tool != "" && line.Outcome != "") || line.Event == "PostToolUse" || line.Event == "tool_result" {
			return true, nil
		}
	}
	return false, sc.Err()
}

// PiSessionFileExists reports whether a session file for id exists under the
// pi agent dir's sessions tree (pi names files <time>_<id>.jsonl inside a
// per-cwd folder; the match is on the id inside a .jsonl name). Provisional
// until 4-1 pins the embedded pi's layout.
func PiSessionFileExists(agentDir, id string) bool {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(agentDir) == "" {
		return false
	}
	found := false
	filepath.WalkDir(filepath.Join(agentDir, "sessions"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if name := d.Name(); strings.HasSuffix(name, ".jsonl") && strings.Contains(name, id) {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// CheckPiResume applies the caveat: a resume with a tool result in the
// ledger and no session file for the id is refused.
func CheckPiResume(ledgerPath, agentDir string, argv []string) error {
	id := PiSessionID(argv)
	if id == "" {
		return refusef("pi_session_id_missing", "pi argv carries no --session-id; the AMQ pi adapter always passes one")
	}
	took, err := LedgerHasToolResult(ledgerPath)
	if err != nil {
		return refusef("ledger_unreadable", "read %s: %v", ledgerPath, err)
	}
	if took && !PiSessionFileExists(agentDir, id) {
		return refusef("pi_resume_context_lost", "the ledger shows a tool result but no pi session file for %s exists under %s; a resume would start an empty session (amendment 23 (a))", id, filepath.Join(agentDir, "sessions"))
	}
	return nil
}
