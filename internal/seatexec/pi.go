package seatexec

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/piruntime"
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

// CheckPiResume applies the caveat with the pi 1.3.0 session layout that
// 4-1 pinned (piruntime.SessionDir and SessionFile: the exact
// <agent-dir>/sessions/--<cwd>--/<timestamp>_<id>.jsonl, not a substring
// walk): a resume with a tool result in the ledger and no session file for
// the id in the seat's cwd is refused.
func CheckPiResume(ledgerPath, agentDir, cwd string, argv []string) error {
	err := piruntime.CheckResume(ledgerPath, agentDir, cwd, argv)
	var rr *piruntime.ResumeRefusal
	if errors.As(err, &rr) {
		return refusef(rr.Code, "%s", rr.Detail)
	}
	return err
}
