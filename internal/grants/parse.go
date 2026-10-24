package grants

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The file format is a strict YAML subset, '#' comments, two kinds of
// top-level entry: "key: scalar" (or "key: [a, b]") and "key:" followed by an
// indented block, which is a list of scalars ("- x"), a list of maps
// ("- k: v" with more "k: v" lines indented under it), or a map of
// "k: scalar" / "k:" + list lines. Schema 1 of the owner file:
//
//	schema: 1
//	mode: auto                    # auto | manual (plan 3.4)
//	max_seats: 12                 # session seat cap (plan 7.2)
//	cost_per_day_usd: 40          # optional
//	permitted_runtimes: [claude, codex, pi]   # optional; absent = any
//	privacy_class: vendor         # vendor | local
//	default_branch: main          # optional; else origin/HEAD, else main
//	protected_paths:              # ~/, absolute or repo-relative; trailing / = tree; * ? [ globs
//	  - conf/*.yaml
//	merge:                        # standing merge grants (plan 7.2, R34)
//	  - repository: omriariav/amq-noc
//	    base_branch: main
//	    ci_required: true
//	    size_cap_lines: 400
//	fleet:                        # reserved for the operator session (4-3)
//	  max_squads: 4
//	  cost_per_day_usd: 100
//	  permitted_repositories:
//	    - omriariav/amq-noc

type entry struct {
	key, value string
	line       int
	block      []blockLine
}

type blockLine struct {
	indent int
	text   string
	line   int
}

func stripComment(line string) string {
	inQuote := byte(0)
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
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

// splitEntries cuts a file into top-level entries. strict refuses content
// that is not an entry (and a repeated key); lenient skips it.
func splitEntries(text, source string, strict bool) ([]entry, error) {
	var out []entry
	seen := map[string]bool{}
	skipping := false
	for n, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(stripComment(raw), " \t\r")
		if strings.TrimSpace(line) == "" {
			continue
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
			out[len(out)-1].block = append(out[len(out)-1].block, blockLine{indent, strings.TrimSpace(line), n + 1})
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
		if seen[key] && strict {
			return nil, fmt.Errorf("%s:%d: key %q repeated", source, n+1, key)
		}
		seen[key], skipping = true, false
		out = append(out, entry{key: key, value: strings.TrimSpace(value), line: n + 1})
	}
	return out, nil
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\'') {
		return s[1 : len(s)-1]
	}
	return s
}

func (e entry) errf(format string, args ...any) error {
	return fmt.Errorf("line %d: %s: %s", e.line, e.key, fmt.Sprintf(format, args...))
}

func (e entry) scalar() (string, error) {
	if len(e.block) > 0 || e.value == "" {
		return "", e.errf("want a single value")
	}
	return unquote(e.value), nil
}

func (e entry) integer(minimum int) (int, error) {
	s, err := e.scalar()
	if err != nil {
		return 0, err
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < minimum {
		return 0, e.errf("want an integer >= %d, got %q", minimum, s)
	}
	return v, nil
}

func (e entry) money() (float64, error) {
	s, err := e.scalar()
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v <= 0 || v > 1e6 {
		return 0, e.errf("want an amount in USD above 0, got %q", s)
	}
	return v, nil
}

func (e entry) boolean() (bool, error) {
	s, err := e.scalar()
	if err != nil {
		return false, err
	}
	switch s {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, e.errf("want true or false, got %q", s)
}

func (e entry) oneOf(values ...string) (string, error) {
	s, err := e.scalar()
	if err != nil {
		return "", err
	}
	if !slices.Contains(values, s) {
		return "", e.errf("want one of %s, got %q", strings.Join(values, ", "), s)
	}
	return s, nil
}

// list reads "[a, b]", "[]" or a block of "- x" lines.
func (e entry) list() ([]string, error) {
	out := []string{}
	if e.value != "" {
		if len(e.block) > 0 || !strings.HasPrefix(e.value, "[") || !strings.HasSuffix(e.value, "]") {
			return nil, e.errf("want a list")
		}
		inner := strings.TrimSpace(e.value[1 : len(e.value)-1])
		if inner == "" {
			return out, nil
		}
		for _, item := range strings.Split(inner, ",") {
			if item = unquote(strings.TrimSpace(item)); item == "" {
				return nil, e.errf("empty list item")
			}
			out = append(out, item)
		}
		return out, nil
	}
	for _, b := range e.block {
		item, ok := strings.CutPrefix(b.text, "- ")
		if !ok || b.indent != e.block[0].indent {
			return nil, fmt.Errorf("line %d: %s: want a list item", b.line, e.key)
		}
		if item = unquote(strings.TrimSpace(item)); item == "" {
			return nil, fmt.Errorf("line %d: %s: empty list item", b.line, e.key)
		}
		out = append(out, item)
	}
	return out, nil
}

// sub turns block lines into entries one level down.
func subEntries(parent string, lines []blockLine) ([]entry, error) {
	var out []entry
	if len(lines) == 0 {
		return nil, nil
	}
	base := lines[0].indent
	seen := map[string]bool{}
	for _, b := range lines {
		if b.indent > base {
			if len(out) == 0 {
				return nil, fmt.Errorf("line %d: %s: misplaced indentation", b.line, parent)
			}
			out[len(out)-1].block = append(out[len(out)-1].block, b)
			continue
		}
		if b.indent < base {
			return nil, fmt.Errorf("line %d: %s: misplaced indentation", b.line, parent)
		}
		key, value, ok := strings.Cut(b.text, ":")
		key = strings.TrimSpace(key)
		if !ok || key == "" || strings.HasPrefix(key, "-") || seen[key] {
			return nil, fmt.Errorf("line %d: %s: want one \"key: value\" per key", b.line, parent)
		}
		seen[key] = true
		out = append(out, entry{key: parent + "." + key, value: strings.TrimSpace(value), line: b.line})
	}
	return out, nil
}

// mapItems reads a block that is a list of maps.
func (e entry) mapItems() ([][]entry, error) {
	if e.value != "" && e.value != "[]" {
		return nil, e.errf("want a list of maps")
	}
	var items [][]blockLine
	for _, b := range e.block {
		if rest, ok := strings.CutPrefix(b.text, "- "); ok && b.indent == e.block[0].indent {
			items = append(items, []blockLine{{b.indent + 2, strings.TrimSpace(rest), b.line}})
			continue
		}
		if len(items) == 0 || b.indent <= e.block[0].indent {
			return nil, fmt.Errorf("line %d: %s: want \"- key: value\" items", b.line, e.key)
		}
		items[len(items)-1] = append(items[len(items)-1], b)
	}
	var out [][]entry
	for _, it := range items {
		sub, err := subEntries(e.key, it)
		if err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, nil
}

var (
	repoPattern    = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}/[A-Za-z0-9._-]{1,100}$`)
	branchPattern  = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,200}$`)
	runtimePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
)

func pathList(e entry) ([]string, error) {
	items, err := e.list()
	if err != nil {
		return nil, err
	}
	for _, p := range items {
		if strings.HasPrefix(p, ManagedPrefix) || strings.ContainsAny(p, "\x00\r\n") {
			return nil, e.errf("bad path %q", p)
		}
	}
	return items, nil
}

func runtimeList(e entry) ([]string, error) {
	items, err := e.list()
	if err != nil {
		return nil, err
	}
	for _, r := range items {
		if !runtimePattern.MatchString(r) {
			return nil, e.errf("bad runtime %q", r)
		}
	}
	return items, nil
}

// ParseOwner parses the owner file strictly: every key known, every value
// valid, schema 1 when given.
func ParseOwner(text, source string) (Grants, error) {
	g := Defaults()
	entries, err := splitEntries(text, source, true)
	if err != nil {
		return g, err
	}
	wrap := func(err error) error { return fmt.Errorf("%s: %w", source, err) }
	for _, e := range entries {
		var err error
		switch e.key {
		case "schema":
			var v int
			if v, err = e.integer(1); err == nil && v != Schema {
				err = e.errf("schema %d is not %d", v, Schema)
			}
		case "mode":
			g.Mode, err = e.oneOf(ModeAuto, ModeManual)
		case "max_seats":
			g.MaxSeats, err = e.integer(1)
		case "cost_per_day_usd":
			g.CostPerDayUSD, err = e.money()
		case "permitted_runtimes":
			g.PermittedRuntimes, err = runtimeList(e)
		case "privacy_class":
			g.PrivacyClass, err = e.oneOf(PrivacyVendor, PrivacyLocal)
		case "default_branch":
			if g.DefaultBranch, err = e.scalar(); err == nil && !branchPattern.MatchString(g.DefaultBranch) {
				err = e.errf("bad branch %q", g.DefaultBranch)
			}
		case "protected_paths":
			g.ProtectedPaths, err = pathList(e)
		case "merge":
			g.Merge, err = parseMerge(e)
		case "fleet":
			g.Fleet, err = parseFleet(e)
		default:
			err = e.errf("unknown key")
		}
		if err != nil {
			return g, wrap(err)
		}
	}
	return g, nil
}

func parseMerge(e entry) ([]Merge, error) {
	items, err := e.mapItems()
	if err != nil {
		return nil, err
	}
	var out []Merge
	for _, it := range items {
		m := Merge{CIRequired: true}
		for _, f := range it {
			var err error
			switch f.key {
			case "merge.repository":
				if m.Repository, err = f.scalar(); err == nil && !repoPattern.MatchString(m.Repository) {
					err = f.errf("want owner/name, got %q", m.Repository)
				}
			case "merge.base_branch":
				if m.BaseBranch, err = f.scalar(); err == nil && !branchPattern.MatchString(m.BaseBranch) {
					err = f.errf("bad branch %q", m.BaseBranch)
				}
			case "merge.ci_required":
				m.CIRequired, err = f.boolean()
			case "merge.size_cap_lines":
				m.SizeCapLines, err = f.integer(1)
			default:
				// force push and protected-path changes are fixed refusals, not fields
				err = f.errf("unknown key")
			}
			if err != nil {
				return nil, err
			}
		}
		if m.Repository == "" || m.BaseBranch == "" || m.SizeCapLines == 0 {
			return nil, e.errf("each merge grant needs repository, base_branch and size_cap_lines")
		}
		if _, dup := (Grants{Merge: out}).MergeFor(m.Repository, m.BaseBranch); dup {
			return nil, e.errf("merge grant for %s %s repeated", m.Repository, m.BaseBranch)
		}
		out = append(out, m)
	}
	return out, nil
}

func parseFleet(e entry) (Fleet, error) {
	var f Fleet
	if e.value != "" {
		return f, e.errf("want a map")
	}
	sub, err := subEntries(e.key, e.block)
	if err != nil {
		return f, err
	}
	for _, s := range sub {
		switch s.key {
		case "fleet.max_squads":
			f.MaxSquads, err = s.integer(1)
		case "fleet.cost_per_day_usd":
			f.CostPerDayUSD, err = s.money()
		case "fleet.permitted_repositories":
			if f.PermittedRepositories, err = s.list(); err == nil {
				for _, r := range f.PermittedRepositories {
					if !repoPattern.MatchString(r) {
						err = s.errf("want owner/name, got %q", r)
					}
				}
			}
		default:
			err = s.errf("unknown key")
		}
		if err != nil {
			return f, err
		}
	}
	return f, nil
}

// ParseSquadNarrowing reads the grant keys of a squad.yaml leniently: other
// top-level keys (the rest of 2b-5's schema) are skipped, but a grant key
// with a bad value is an error, never silently dropped. Grant keys: mode,
// max_seats, cost_per_day_usd, permitted_runtimes, privacy_class,
// protected_paths, merge_repositories, merge_size_cap_lines. default_branch
// is the owner's alone and is ignored here.
func ParseSquadNarrowing(text, source string) (Narrowing, error) {
	var n Narrowing
	entries, err := splitEntries(text, source, false)
	if err != nil {
		return n, err
	}
	for _, e := range entries {
		var err error
		switch e.key {
		case "mode":
			n.Mode, err = e.oneOf(ModeAuto, ModeManual)
		case "max_seats":
			n.MaxSeats, err = e.integer(1)
		case "cost_per_day_usd":
			n.CostPerDayUSD, err = e.money()
		case "permitted_runtimes":
			n.PermittedRuntimes, err = runtimeList(e)
			n.RuntimesSet = err == nil
		case "privacy_class":
			n.PrivacyClass, err = e.oneOf(PrivacyVendor, PrivacyLocal)
		case "protected_paths":
			n.ProtectedPaths, err = pathList(e)
		case "merge_repositories":
			n.MergeRepositories, err = e.list()
		case "merge_size_cap_lines":
			n.MergeSizeCapLines, err = e.integer(1)
		}
		if err != nil {
			return n, fmt.Errorf("%s: %w", source, err)
		}
	}
	return n, nil
}
