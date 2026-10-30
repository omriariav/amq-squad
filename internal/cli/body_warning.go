package cli

import (
	"fmt"
	"io"
	"os"
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

// readPromptBody resolves the prompt text from --body, --body-file (a path or
// "-" for stdin), or bare stdin when neither flag is set and stdin is piped.
// interactiveStdin reports whether stdin is a terminal; the bare-stdin path
// then returns a usage error instead of blocking forever waiting for EOF.
func readPromptBody(body, bodyFile string, bodySet, fileSet bool, stdin io.Reader, interactiveStdin bool) (string, error) {
	if bodySet && fileSet {
		return "", usageErrorf("use either --body or --body-file, not both")
	}
	if bodySet {
		if strings.TrimSpace(body) == "" {
			return "", usageErrorf("--body cannot be empty")
		}
		return body, nil
	}
	if fileSet {
		if bodyFile == "-" {
			return readAllPrompt(stdin)
		}
		b, err := os.ReadFile(bodyFile)
		if err != nil {
			return "", fmt.Errorf("read --body-file: %w", err)
		}
		if strings.TrimSpace(string(b)) == "" {
			return "", fmt.Errorf("--body-file %s is empty", bodyFile)
		}
		return string(b), nil
	}
	if interactiveStdin {
		return "", usageErrorf("no prompt provided; pass --body, --body-file FILE, or pipe text on stdin")
	}
	return readAllPrompt(stdin)
}

// stdinIsInteractive reports whether os.Stdin is a terminal (char device)
// rather than a pipe or file, so a command can refuse to block on TTY input.
func stdinIsInteractive() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func readAllPrompt(stdin io.Reader) (string, error) {
	b, err := io.ReadAll(stdin)
	if err != nil {
		return "", fmt.Errorf("read prompt from stdin: %w", err)
	}
	if strings.TrimSpace(string(b)) == "" {
		return "", usageErrorf("no prompt provided; pass --body, --body-file, or pipe text on stdin")
	}
	return string(b), nil
}
