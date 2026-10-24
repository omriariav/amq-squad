package drafter

import (
	"fmt"
	"strings"
)

// Draft-text checks shared by the CLI prose consumers (brief --goal, team
// draft-rules, role draft). Each consumer validates its own document shape on
// top of these.

// NormalizeText is the shared pre-check of a drafted document (noun): size
// limit, LF line endings, no code fences. It returns the trimmed text.
func NormalizeText(raw string, limit int, noun string) (string, error) {
	if len(raw) > limit {
		return "", fmt.Errorf("%s exceeds %d bytes", noun, limit)
	}
	document := strings.ReplaceAll(raw, "\r\n", "\n")
	if strings.Contains(document, "\r") {
		return "", fmt.Errorf("%s contains unsupported carriage returns", noun)
	}
	document = strings.TrimSpace(document)
	if strings.Contains(document, "```") {
		return "", fmt.Errorf("%s must not contain code fences", noun)
	}
	return document, nil
}

// MatchRoleBullets matches each line to exactly one role by its bullet prefix
// (prefixes[role] followed by text) and returns that text per role.
func MatchRoleBullets(section, noun, miss string, lines []string, prefixes map[string]string) (map[string]string, error) {
	if len(lines) != len(prefixes) {
		return nil, fmt.Errorf("%s has %d %s; want %d", section, len(lines), noun, len(prefixes))
	}
	out := make(map[string]string, len(prefixes))
	for _, line := range lines {
		matched := ""
		for role, prefix := range prefixes {
			if strings.HasPrefix(line, prefix) && strings.TrimSpace(strings.TrimPrefix(line, prefix)) != "" {
				matched = role
			}
		}
		if matched == "" {
			return nil, fmt.Errorf("%s contains %s: %q", section, miss, line)
		}
		if _, duplicate := out[matched]; duplicate {
			return nil, fmt.Errorf("%s duplicates role %q", section, matched)
		}
		out[matched] = strings.TrimSpace(strings.TrimPrefix(line, prefixes[matched]))
	}
	return out, nil
}

// NonEmptyLines trims lines, drops blank ones and normalizes bullet markers.
func NonEmptyLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, NormalizeBullet(line))
		}
	}
	return out
}

// NormalizeBullet rewrites a leading CommonMark "* " or "+ " bullet marker to
// "- " (gh#760). Only a marker-plus-space prefix qualifies, so prose starting
// with "**bold**" or a bare "+" is left untouched.
func NormalizeBullet(line string) string {
	if strings.HasPrefix(line, "* ") || strings.HasPrefix(line, "+ ") {
		return "- " + line[2:]
	}
	return line
}

// AllBullets reports whether every line is a "- " bullet (and there is one).
func AllBullets(lines []string) bool {
	for _, line := range lines {
		if !strings.HasPrefix(line, "- ") {
			return false
		}
	}
	return len(lines) > 0
}
