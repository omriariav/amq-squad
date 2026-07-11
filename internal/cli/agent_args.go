package cli

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

func parseBinaryArgFlags(codexRaw, claudeRaw string) (map[string][]string, error) {
	out := map[string][]string{}
	if strings.TrimSpace(codexRaw) != "" {
		args, err := parseAgentArgs(codexRaw)
		if err != nil {
			return nil, fmt.Errorf("parse --codex-args: %w", err)
		}
		if len(args) > 0 {
			out["codex"] = args
		}
	}
	if strings.TrimSpace(claudeRaw) != "" {
		args, err := parseAgentArgs(claudeRaw)
		if err != nil {
			return nil, fmt.Errorf("parse --claude-args: %w", err)
		}
		if len(args) > 0 {
			out["claude"] = args
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

func binaryArgsFor(binary string, binaryArgs map[string][]string) []string {
	if len(binaryArgs) == 0 {
		return nil
	}
	key := normalizedAgentBinary(binary)
	return composeBinaryArgs(key, binaryArgs[key])
}

func mergeBinaryArgs(base, extra map[string][]string) map[string][]string {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	out := map[string][]string{}
	for k, args := range base {
		key := strings.ToLower(strings.TrimSpace(k))
		if key == "" {
			continue
		}
		out[key] = composeBinaryArgs(key, out[key], args)
	}
	for k, args := range extra {
		key := strings.ToLower(strings.TrimSpace(k))
		if key == "" {
			continue
		}
		out[key] = composeBinaryArgs(key, out[key], args)
	}
	return out
}

// composeBinaryArgs combines ordered configuration layers while removing only
// binary-known singleton options. Unknown and repeatable arguments (including
// everything after a literal --) remain byte-for-byte ordered. Keeping this
// parser narrow is intentional: pass-through argv is not a generic flag set.
func composeBinaryArgs(binary string, layers ...[]string) []string {
	type span struct {
		args []string
		key  string
	}
	var spans []span
	parseOptions := true
	for _, layer := range layers {
		for i := 0; i < len(layer); {
			arg := layer[i]
			if !parseOptions || arg == "--" {
				spans = append(spans, span{args: []string{arg}})
				if arg == "--" {
					parseOptions = false
				}
				i++
				continue
			}
			key, width := binarySingletonSpan(normalizedAgentBinary(binary), layer, i)
			if width < 1 {
				width = 1
			}
			end := i + width
			if end > len(layer) {
				end = len(layer)
			}
			spans = append(spans, span{args: append([]string(nil), layer[i:end]...), key: key})
			i = end
		}
	}
	last := map[string]int{}
	for i, s := range spans {
		if s.key != "" {
			last[s.key] = i
		}
	}
	var out []string
	for i, s := range spans {
		if s.key == "" || last[s.key] == i {
			out = append(out, s.args...)
		}
	}
	return out
}

func binarySingletonSpan(binary string, args []string, i int) (string, int) {
	arg := args[i]
	if binary == "codex" {
		if (arg == "-c" || arg == "--config") && i+1 < len(args) {
			if key, _, ok := strings.Cut(args[i+1], "="); ok && strings.TrimSpace(key) != "" {
				return "codex:config:" + strings.TrimSpace(key), 2
			}
		}
		if strings.HasPrefix(arg, "--config=") {
			if key, _, ok := strings.Cut(strings.TrimPrefix(arg, "--config="), "="); ok && strings.TrimSpace(key) != "" {
				return "codex:config:" + strings.TrimSpace(key), 1
			}
		}
		if strings.HasPrefix(arg, "-c=") || (strings.HasPrefix(arg, "-c") && len(arg) > 2) {
			raw := strings.TrimPrefix(arg, "-c")
			raw = strings.TrimPrefix(raw, "=")
			if key, _, ok := strings.Cut(raw, "="); ok && strings.TrimSpace(key) != "" {
				return "codex:config:" + strings.TrimSpace(key), 1
			}
		}
	}
	spec, inline, ok := nativeValueSpecForArg(binary, arg)
	if !ok || !spec.Singleton {
		return "", 1
	}
	width := 1
	if !inline && i+1 < len(args) && args[i+1] != "--" && !strings.HasPrefix(args[i+1], "-") {
		width = 2
	}
	return binary + ":" + spec.Canonical, width
}

func joinedAgentArgs(args []string) string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " ")
}

func formatBinaryArgs(binaryArgs map[string][]string) string {
	if len(binaryArgs) == 0 {
		return ""
	}
	keys := make([]string, 0, len(binaryArgs))
	for k := range binaryArgs {
		if len(binaryArgs[k]) > 0 {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+": "+joinedAgentArgs(binaryArgs[k]))
	}
	return strings.Join(parts, ", ")
}
