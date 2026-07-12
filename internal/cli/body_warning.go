package cli

import (
	"fmt"
	"io"
	"strings"
)

// warnSuspiciousInlineBody emits an advisory when an inline --body looks as
// though the invoking shell consumed part of it before amq-squad received the
// argv value. File and stdin bodies deliberately bypass this check.
func warnSuspiciousInlineBody(command, body string, inline bool, out io.Writer) {
	if !inline {
		return
	}
	reasons := suspiciousInlineBodyReasons(body)
	if len(reasons) == 0 {
		return
	}
	fmt.Fprintf(out, "warning: amq-squad %s: inline --body contains patterns commonly left by shell mangling (%s) and may have been changed before execution; use --body-file FILE or --body-file - (stdin) for bodies containing code, commands, backticks, or $() syntax. Shell expansion happens before amq-squad receives argv, so another literal flag cannot recover substituted text.\n",
		command, strings.Join(reasons, ", "))
}

func suspiciousInlineBodyReasons(body string) []string {
	var reasons []string
	if strings.Count(body, "`")%2 != 0 {
		reasons = append(reasons, "unbalanced backticks")
	}
	if hasUnclosedCommandSubstitution(body) {
		reasons = append(reasons, "opening $( without a close")
	}
	if hasCommandNotFoundResidue(body) {
		reasons = append(reasons, "command-not-found residue")
	}
	return reasons
}

func hasUnclosedCommandSubstitution(body string) bool {
	depth := 0
	for i := 0; i < len(body); i++ {
		if body[i] == '$' && i+1 < len(body) && body[i+1] == '(' {
			depth++
			i++
			continue
		}
		if body[i] == '(' && depth > 0 {
			depth++
			continue
		}
		if body[i] == ')' && depth > 0 {
			depth--
		}
	}
	return depth > 0
}

func hasCommandNotFoundResidue(body string) bool {
	for _, line := range strings.Split(strings.ToLower(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "command not found" || strings.HasPrefix(line, "command not found:") {
			return true
		}
		for _, shell := range []string{"sh:", "bash:", "dash:", "zsh:", "fish:", "/bin/sh:", "/bin/bash:", "/bin/dash:", "/bin/zsh:"} {
			if strings.HasPrefix(line, shell) &&
				(strings.Contains(line, "command not found") || strings.Contains(line, ": not found")) {
				return true
			}
		}
	}
	return false
}
