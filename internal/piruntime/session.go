package piruntime

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// pi 1.3.0 session layout (docs/session-format.md of the pinned package,
// checked against the embedded pi in TestLiveSessionLayout):
//
//	<agent-dir>/sessions/--<path>--/<timestamp>_<session-id>.jsonl
//
// where <path> is the cwd with the leading separator removed and every
// "/", "\" and ":" replaced by "-". `--session-id <id>` opens that file or
// creates it on the first save (create-if-missing).

// sessionIDRE is pi's rule for --session-id: letters, digits, '.', '_' and
// '-', starting and ending with a letter or digit.
var sessionIDRE = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`)

// SessionDir is the directory pi keeps cwd's sessions in.
func SessionDir(agentDir, cwd string) string {
	p := strings.TrimLeft(filepath.Clean(cwd), `/\`)
	p = strings.NewReplacer("/", "-", `\`, "-", ":", "-").Replace(p)
	return filepath.Join(agentDir, "sessions", "--"+p+"--")
}

// SessionFile returns the session file pi would open for id in cwd, or ""
// when none exists. Only an exact "<timestamp>_<id>.jsonl" name matches, so
// an id that is a substring of another never matches its file.
func SessionFile(agentDir, cwd, id string) (string, error) {
	if !sessionIDRE.MatchString(id) {
		return "", fmt.Errorf("invalid pi session id %q", id)
	}
	ents, err := os.ReadDir(SessionDir(agentDir, cwd))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	suffix := "_" + id + ".jsonl"
	for _, d := range ents {
		name := d.Name()
		if d.Type().IsRegular() && strings.HasSuffix(name, suffix) && !strings.Contains(strings.TrimSuffix(name, suffix), "_") {
			return filepath.Join(SessionDir(agentDir, cwd), name), nil
		}
	}
	return "", nil
}

// SessionIDArg returns the value of --session-id in pi's argv (the AMQ pi
// adapter composes `pi --session-id <launch nonce>`).
func SessionIDArg(argv []string) string {
	for i, a := range argv {
		if a == "--session-id" && i+1 < len(argv) {
			return argv[i+1]
		}
		if v, ok := strings.CutPrefix(a, "--session-id="); ok {
			return v
		}
	}
	return ""
}

// LedgerPath is the seat's evidence ledger written by `evidence ingest`.
func LedgerPath(root, handle string) string {
	return filepath.Join(root, "extensions", "amq-squad", "ledger", handle+".jsonl")
}

// LedgerHasToolResult reports whether the ledger holds a tool result: a pi
// tool_result line, a Claude PostToolUse line, or any line with a tool and an
// outcome. A missing ledger holds none.
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
	sc.Buffer(make([]byte, 64<<10), 64<<20)
	for sc.Scan() {
		var line struct {
			Event   string `json:"event"`
			Tool    string `json:"tool"`
			Outcome string `json:"outcome"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		if line.Event == "tool_result" || line.Event == "PostToolUse" || (line.Tool != "" && line.Outcome != "") {
			return true, nil
		}
	}
	return false, sc.Err()
}

// ResumeRefusal is the amendment 23 refusal.
type ResumeRefusal struct {
	Code   string
	Detail string
}

func (r *ResumeRefusal) Error() string {
	return fmt.Sprintf("pi resume refused (%s): %s", r.Code, r.Detail)
}

// CheckResume applies the amendment 23 (a) rule on the pi side of seat-exec
// (plan 4.1, R5): upstream resumes with `pi --session-id <recorded id>` and
// relies on create-if-missing, which loses context silently in exactly one
// case: the seat already took a turn (its ledger shows a tool result) and the
// session file for the recorded id is gone. That resume is refused; a seat
// that never took a turn resumes into a fresh session as upstream intends.
func CheckResume(ledger, agentDir, cwd string, argv []string) error {
	id := SessionIDArg(argv)
	if id == "" {
		return &ResumeRefusal{"pi_session_id_missing", "pi argv carries no --session-id; the AMQ pi adapter always passes one"}
	}
	if !sessionIDRE.MatchString(id) {
		return &ResumeRefusal{"pi_session_id_invalid", fmt.Sprintf("--session-id %q is not a pi session id", id)}
	}
	took, err := LedgerHasToolResult(ledger)
	if err != nil {
		return &ResumeRefusal{"ledger_unreadable", fmt.Sprintf("read %s: %v", ledger, err)}
	}
	if !took {
		return nil
	}
	file, err := SessionFile(agentDir, cwd, id)
	if err != nil {
		return &ResumeRefusal{"pi_session_unreadable", err.Error()}
	}
	if file == "" {
		return &ResumeRefusal{"pi_resume_context_lost", fmt.Sprintf(
			"the ledger %s shows a tool result but no pi session file for %s exists in %s; a resume would start an empty session (amendment 23)",
			ledger, id, SessionDir(agentDir, cwd))}
	}
	return nil
}
