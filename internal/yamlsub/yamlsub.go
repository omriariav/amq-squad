// Package yamlsub reads the strict YAML subset of the owner grants file and
// squad.yaml: '#' comments, two kinds of top-level entry: "key: scalar" (or
// "key: [a, b]") and "key:" followed by an indented block, which is a list of
// scalars ("- x"), a list of maps ("- k: v" with more "k: v" lines indented
// under it), or a map of "k: scalar" / "k:" + list lines. It has no
// dependency; grants and team (squad.yaml) both parse through it, so the two
// files cannot drift apart in syntax.
//
// Quoting has one rule on both sides (Quote and Unquote): a double-quoted
// value is a Go string literal (backslash escapes), a single-quoted value
// writes each single quote inside it twice. A document marker (--- or ...) and the YAML
// features this subset does not implement (anchors &, aliases *, tags !,
// flow maps {, nested flow lists [) are errors, never literal text.
package yamlsub

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// documentMarker is --- or ... followed by whitespace or the end of the line.
var documentMarker = regexp.MustCompile(`^(---|\.\.\.)(\s|$)`)

// Entry is one key with its scalar value or its indented block.
type Entry struct {
	Key, Value string
	Line       int
	Block      []BlockLine
}

// BlockLine is one indented line under an entry.
type BlockLine struct {
	Indent int
	Text   string
	Line   int
}

func stripComment(line string) string {
	inQuote := byte(0)
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case inQuote == '"' && c == '\\':
			i++ // an escaped character never closes the string
		case inQuote != 0:
			if c == inQuote {
				inQuote = 0
			}
		case c == '"' || c == '\'':
			inQuote = c
		case c == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t'):
			return line[:i]
		}
	}
	return line
}

// Split cuts a file into top-level entries. strict refuses content that is
// not an entry (and a repeated key); lenient skips it.
func Split(text, source string, strict bool) ([]Entry, error) {
	var out []Entry
	seen := map[string]bool{}
	skipping := false
	for n, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(stripComment(raw), " \t\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if t := strings.TrimSpace(line); documentMarker.MatchString(t) {
			// one document per file, in both modes: a second document must
			// not replace the first one's keys
			return nil, fmt.Errorf("%s:%d: document marker %q: one YAML document per file", source, n+1, t)
		}
		if strings.Contains(line, "\t") && strict {
			return nil, fmt.Errorf("%s:%d: tab in indentation or value", source, n+1)
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if indent > 0 {
			if skipping || len(out) == 0 {
				if strict {
					return nil, fmt.Errorf("%s:%d: indented line outside an entry", source, n+1)
				}
				continue
			}
			out[len(out)-1].Block = append(out[len(out)-1].Block, BlockLine{indent, strings.TrimSpace(line), n + 1})
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		key = strings.TrimSpace(key)
		if !ok || key == "" || strings.HasPrefix(key, "-") {
			if strict {
				return nil, fmt.Errorf("%s:%d: malformed line %q", source, n+1, strings.TrimSpace(line))
			}
			skipping = true
			continue
		}
		if seen[key] {
			// repeated keys are an error in both modes (amendment 27): a later
			// value must never replace an earlier one
			return nil, fmt.Errorf("%s:%d: key %q repeated", source, n+1, key)
		}
		seen[key], skipping = true, false
		out = append(out, Entry{Key: key, Value: strings.TrimSpace(value), Line: n + 1})
	}
	return out, nil
}

// Unquote reads a scalar: a double-quoted Go string literal, a single-quoted
// value with each inner single quote written twice, or plain text.
// YAML feature this subset does not implement is an error.
func Unquote(s string) (string, error) {
	switch {
	case len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"':
		v, err := strconv.Unquote(s)
		if err != nil {
			return "", fmt.Errorf("bad double-quoted value %s", s)
		}
		return v, nil
	case len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'':
		inner := s[1 : len(s)-1]
		if strings.Count(strings.ReplaceAll(inner, "''", ""), "'") > 0 {
			return "", fmt.Errorf("bad single-quoted value %s", s)
		}
		return strings.ReplaceAll(inner, "''", "'"), nil
	case s != "" && strings.ContainsRune("&*!{[|>%@`\"'", rune(s[0])):
		return "", fmt.Errorf("unsupported YAML syntax %q (anchors, aliases, tags, flow maps and nested lists are not part of this format)", s)
	case strings.Contains(s, ": ") || strings.HasSuffix(s, ":"):
		return "", fmt.Errorf("unsupported YAML syntax %q: a mapping is not a value here", s)
	}
	return s, nil
}

// Quote renders a value so that Unquote reads it back unchanged.
func Quote(v string) string {
	if v == "" || strings.ContainsAny(v, "#:'\"[]{},&*!|>%@`\\") || strings.TrimSpace(v) != v || strings.HasPrefix(v, "-") || strings.ContainsAny(v, "\x00\r\n") {
		return strconv.Quote(v)
	}
	return v
}

// splitFlow splits the inside of a flow list on the commas outside quotes.
func splitFlow(inner string) ([]string, error) {
	var out []string
	start, quote := 0, byte(0)
	for i := 0; i < len(inner); i++ {
		c := inner[i]
		switch {
		case quote == '"' && c == '\\':
			i++
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == ',':
			out = append(out, inner[start:i])
			start = i + 1
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote")
	}
	return append(out, inner[start:]), nil
}

// Errf is an error that names the entry's line and key.
func (e Entry) Errf(format string, args ...any) error {
	return fmt.Errorf("line %d: %s: %s", e.Line, e.Key, fmt.Sprintf(format, args...))
}

// Scalar is the entry's single value.
func (e Entry) Scalar() (string, error) {
	if len(e.Block) > 0 || e.Value == "" {
		return "", e.Errf("want a single value")
	}
	v, err := Unquote(e.Value)
	if err != nil {
		return "", e.Errf("%v", err)
	}
	return v, nil
}

// Integer is the entry's value as an integer at or above minimum.
func (e Entry) Integer(minimum int) (int, error) {
	s, err := e.Scalar()
	if err != nil {
		return 0, err
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < minimum {
		return 0, e.Errf("want an integer >= %d, got %q", minimum, s)
	}
	return v, nil
}

// Money is an amount in USD above 0 and at most one million.
func (e Entry) Money() (float64, error) {
	s, err := e.Scalar()
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v <= 0 || v > 1e6 {
		return 0, e.Errf("want an amount in USD above 0, got %q", s)
	}
	return v, nil
}

// Boolean is true or false.
func (e Entry) Boolean() (bool, error) {
	s, err := e.Scalar()
	if err != nil {
		return false, err
	}
	switch s {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, e.Errf("want true or false, got %q", s)
}

// OneOf is a scalar from a closed list.
func (e Entry) OneOf(values ...string) (string, error) {
	s, err := e.Scalar()
	if err != nil {
		return "", err
	}
	if !slices.Contains(values, s) {
		return "", e.Errf("want one of %s, got %q", strings.Join(values, ", "), s)
	}
	return s, nil
}

// List reads "[a, b]", "[]" or a block of "- x" lines.
func (e Entry) List() ([]string, error) {
	out := []string{}
	if e.Value != "" {
		if len(e.Block) > 0 || !strings.HasPrefix(e.Value, "[") || !strings.HasSuffix(e.Value, "]") {
			return nil, e.Errf("want a list")
		}
		inner := strings.TrimSpace(e.Value[1 : len(e.Value)-1])
		if inner == "" {
			return out, nil
		}
		items, err := splitFlow(inner)
		if err != nil {
			return nil, e.Errf("%v", err)
		}
		for _, item := range items {
			v, err := Unquote(strings.TrimSpace(item))
			if err != nil {
				return nil, e.Errf("%v", err)
			}
			if v == "" {
				return nil, e.Errf("empty list item")
			}
			out = append(out, v)
		}
		return out, nil
	}
	for _, b := range e.Block {
		item, ok := strings.CutPrefix(b.Text, "- ")
		if !ok || b.Indent != e.Block[0].Indent {
			return nil, fmt.Errorf("line %d: %s: want a list item", b.Line, e.Key)
		}
		v, err := Unquote(strings.TrimSpace(item))
		if err != nil {
			return nil, fmt.Errorf("line %d: %s: %v", b.Line, e.Key, err)
		}
		if v == "" {
			return nil, fmt.Errorf("line %d: %s: empty list item", b.Line, e.Key)
		}
		out = append(out, v)
	}
	return out, nil
}

// Sub turns block lines into entries one level down, keyed parent.key.
func Sub(parent string, lines []BlockLine) ([]Entry, error) {
	var out []Entry
	if len(lines) == 0 {
		return nil, nil
	}
	base := lines[0].Indent
	seen := map[string]bool{}
	for _, b := range lines {
		if b.Indent > base {
			if len(out) == 0 {
				return nil, fmt.Errorf("line %d: %s: misplaced indentation", b.Line, parent)
			}
			out[len(out)-1].Block = append(out[len(out)-1].Block, b)
			continue
		}
		if b.Indent < base {
			return nil, fmt.Errorf("line %d: %s: misplaced indentation", b.Line, parent)
		}
		key, value, ok := strings.Cut(b.Text, ":")
		key = strings.TrimSpace(key)
		if !ok || key == "" || strings.HasPrefix(key, "-") || seen[key] {
			return nil, fmt.Errorf("line %d: %s: want one \"key: value\" per key", b.Line, parent)
		}
		seen[key] = true
		out = append(out, Entry{Key: parent + "." + key, Value: strings.TrimSpace(value), Line: b.Line})
	}
	return out, nil
}

// Map reads a block that is a map, one level down.
func (e Entry) Map() ([]Entry, error) {
	if e.Value != "" {
		return nil, e.Errf("want a map")
	}
	return Sub(e.Key, e.Block)
}

// MapItems reads a block that is a list of maps.
func (e Entry) MapItems() ([][]Entry, error) {
	if e.Value != "" && e.Value != "[]" {
		return nil, e.Errf("want a list of maps")
	}
	var items [][]BlockLine
	for _, b := range e.Block {
		if rest, ok := strings.CutPrefix(b.Text, "- "); ok && b.Indent == e.Block[0].Indent {
			items = append(items, []BlockLine{{b.Indent + 2, strings.TrimSpace(rest), b.Line}})
			continue
		}
		if len(items) == 0 || b.Indent <= e.Block[0].Indent {
			return nil, fmt.Errorf("line %d: %s: want \"- key: value\" items", b.Line, e.Key)
		}
		items[len(items)-1] = append(items[len(items)-1], b)
	}
	var out [][]Entry
	for _, it := range items {
		sub, err := Sub(e.Key, it)
		if err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, nil
}
