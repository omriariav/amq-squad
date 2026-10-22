package cli

import (
	"os"
	"path/filepath"
	"strings"
)

func defaultHandleFor(binary string) string {
	return strings.ToLower(filepath.Base(binary))
}

// shellQuote wraps a string in single quotes for safe shell pasting.
// If the string has no special chars, returns it as-is.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !(r == '/' || r == '.' || r == '-' || r == '_' || r == '=' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shellCommand(bin string, args ...string) string {
	if bin == "amq-squad" {
		bin = generatedSquadCommand()
	}
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, shellQuote(bin))
	for _, arg := range args {
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " ")
}

var generatedSquadCommandOverride string

func generatedSquadCommand() string {
	if generatedSquadCommandOverride != "" {
		return generatedSquadCommandOverride
	}
	p, err := os.Executable()
	if err != nil {
		return "amq-squad"
	}
	base := filepath.Base(p)
	if base == "" || strings.HasSuffix(base, ".test") {
		return "amq-squad"
	}
	return p
}
