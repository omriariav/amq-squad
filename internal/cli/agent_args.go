package cli

import (
	"fmt"
	"strings"
	"unicode"
)

func parseBinaryArgFlags(codexRaw, claudeRaw string) (map[string][]string, error) {
	out := map[string][]string{}
	for _, f := range []struct{ binary, raw string }{{"codex", codexRaw}, {"claude", claudeRaw}} {
		args, err := parseAgentArgs(f.raw)
		if err != nil {
			return nil, fmt.Errorf("parse --%s-args: %w", f.binary, err)
		}
		if len(args) > 0 {
			out[f.binary] = args
		}
	}
	return out, nil
}

func parseAgentArgs(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	args := []string{}
	var b strings.Builder
	var quote rune
	tokenStarted := false
	escaped := false
	for _, r := range raw {
		if escaped {
			b.WriteRune(r)
			tokenStarted = true
			escaped = false
			continue
		}
		if quote == '\'' {
			if r == '\'' {
				quote = 0
			} else {
				b.WriteRune(r)
				tokenStarted = true
			}
			continue
		}
		if r == '\\' {
			escaped = true
			tokenStarted = true
			continue
		}
		if quote == '"' {
			if r == '"' {
				quote = 0
			} else {
				b.WriteRune(r)
				tokenStarted = true
			}
			continue
		}
		switch {
		case r == '\'' || r == '"':
			quote = r
			tokenStarted = true
		case unicode.IsSpace(r):
			if tokenStarted {
				args = append(args, b.String())
				b.Reset()
				tokenStarted = false
			}
		default:
			b.WriteRune(r)
			tokenStarted = true
		}
	}
	if escaped {
		return nil, fmt.Errorf("trailing escape")
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote")
	}
	if tokenStarted {
		args = append(args, b.String())
	}
	return args, nil
}
