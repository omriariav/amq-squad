package cli

import (
	"fmt"
	"path/filepath"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

// runShellLogPath is the append-only log every tmux `run-shell -b` payload's
// combined output is redirected to under controlRoot, so a scheduled
// helper's failure or stray print can never surface on the active pane as a
// view-mode overlay (#525).
func runShellLogPath(controlRoot string) string {
	return filepath.Join(controlRoot, team.DirName, "run-shell.log")
}

// silentRunShellPayload wraps cmd so a tmux `run-shell -b` invocation can
// never display output or a nonzero-exit "'<cmd>' returned N" line on the
// pane it targets (#525): combined stdout/stderr is appended to the
// run-shell log under controlRoot, and the wrapper itself always reports
// success to tmux regardless of what cmd does. The log directory is created
// (and its own failure silenced) up front, ahead of the append redirect: a
// shell cannot open `>>logPath` for append before that directory exists, and
// an unopenable redirect target would otherwise leak its own error to the
// real stderr the append was supposed to protect.
func silentRunShellPayload(controlRoot, cmd string) string {
	logPath := runShellLogPath(controlRoot)
	return fmt.Sprintf("mkdir -p %s 2>/dev/null; ( %s ) >>%s 2>&1 || true",
		shellQuote(filepath.Dir(logPath)), cmd, shellQuote(logPath))
}
