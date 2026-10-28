package seatexec

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/launch"
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

// PiNotEmbedded refuses a pi seat whose argv[0] is not this binary's pi
// launch shim (so not the embedded runtime the launch probed).
const PiNotEmbedded = "pi_not_embedded"

// piShimProvider checks argv[0] is this amq-squad's pi launch shim and
// returns the binary that runs the embedded runtime (this one).
func piShimProvider(o Options, rec launch.Record) (string, error) {
	if base := filepath.Base(o.Argv[0]); base != "pi" {
		return "", refusef("runtime_mismatch", "provider %q is not the recorded runtime %q", base, rec.Runtime)
	}
	if !filepath.IsAbs(rec.PiAgentDir) || filepath.Clean(rec.PiAgentDir) != rec.PiAgentDir {
		return "", refusef("record_invalid", "the launch record names no absolute pi agent dir (%q)", rec.PiAgentDir)
	}
	// The shim the launch record names (Codex A small (a)): never a path
	// derived from this process's HOME.
	if rec.PiShim == "" || filepath.Clean(o.Argv[0]) != rec.PiShim {
		return "", refusef(PiNotEmbedded, "argv[0] %s is not the pi shim the launch record names (%q)", o.Argv[0], rec.PiShim)
	}
	if err := piruntime.CheckShim(rec.PiShim, o.Self); err != nil {
		return "", refusef(PiNotEmbedded, "%v", err)
	}
	return o.Self, nil
}

// PiLauncherArgv is a pi seat's exec: `amq-squad pi run --seat H` with the
// seat's registry model and effort (launcher flags from the core-written
// record, never provider arguments: pi admits none, amendment 23 (b)) and
// the adapter's argv after "--". The launcher re-hashes the embedded runtime
// against the embedded manifest, checks the launch set, forces the pi
// environment (transpile cache off, the seat's private TMPDIR) and runs
// node as its child (amendment 26).
//
// The record's env_needs and credential_needs travel as --env-need (Codex
// A-P1): seat-exec's scrub kept those names, and the launcher's scrub keeps
// exactly those again, so a declared provider key reaches pi and nothing
// else of the kind does. The agent dir is the record's.
func PiLauncherArgv(self string, rec launch.Record, argv []string) []string {
	out := []string{self, "pi", "run", "--seat", rec.Handle, "--agent-dir", rec.PiAgentDir}
	for _, n := range append(append([]string{}, rec.EnvNeeds...), rec.CredNeeds...) {
		out = append(out, "--env-need", n)
	}
	if strings.Contains(rec.Model, "/") {
		out = append(out, "--model", rec.Model)
	}
	if rec.Effort != "" {
		out = append(out, "--thinking", rec.Effort)
	}
	out = append(out, "--")
	return append(out, argv[1:]...)
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
