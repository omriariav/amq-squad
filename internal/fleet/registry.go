// Package fleet is the fleet side of the operator session (plan 3.5, 16.3,
// 16.5; R38 to R41): the fleet registry ~/.amq-squad/fleet.yaml (each squad,
// its root, its lead handle), the fleet status document over it, the
// operator mailbox routes, the grouped gate list of manual mode, and the
// mayor of auto mode. The operator session is the same program as a squad
// lead; only its grant differs (internal/leadsession).
//
// The registry read and write are the 2a kept-dead extract of
// internal/cli/global_noc.go (the NOC registry), renamed: one strict file,
// a non-symlink directory, an exclusive lock and an atomic replace.
package fleet

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Schema is the fleet.yaml schema version.
const Schema = 1

// OperatorHandle is the operator mailbox: the owner's user handle (briefs
// ruling 16). The status contract carries no operator field.
const OperatorHandle = "user"

// Squad is one squad of the fleet.
type Squad struct {
	Name        string `json:"name"`         // the squad's id in the fleet
	Root        string `json:"root"`         // its AMQ session root
	ProjectPath string `json:"project_path"` // its repository root (squad.yaml lives here)
	Repository  string `json:"repository"`   // owner/name, checked against the fleet grant
	Lead        string `json:"lead"`         // the lead seat's handle
	Session     string `json:"session"`      // the AMQ session name; "" is the root's base name
	Project     string `json:"project"`      // the AMQ peer project name for cross-project routing; "" is Name
}

// Operator is where the operator session's mailbox lives.
type Operator struct {
	Root    string `json:"root"`    // the operator's AMQ session root
	Project string `json:"project"` // the AMQ peer project name squads route to
	Session string `json:"session"` // the operator's AMQ session name
}

// Waiting is a squad the mayor declined or deferred for the fleet cap
// (max_squads or cost_per_day_usd). It is not a registered squad; status
// --fleet shows it with wait_reason fleet_cap so the owner sees why it is
// not up. The mayor removes it when the squad starts.
type Waiting struct {
	Name        string `json:"name"`
	Repository  string `json:"repository"`
	ProjectPath string `json:"project_path"`
	Cap         string `json:"cap"`   // max_squads | cost_per_day
	Since       string `json:"since"` // RFC 3339 UTC
	Detail      string `json:"detail,omitempty"`
}

// Registry is fleet.yaml in memory.
type Registry struct {
	Schema   int       `json:"schema"`
	Operator Operator  `json:"operator"`
	Squads   []Squad   `json:"squads"`
	Waiting  []Waiting `json:"waiting,omitempty"`
}

// DefaultPath is ~/.amq-squad/fleet.yaml, "" without a home directory.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".amq-squad", "fleet.yaml")
}

var (
	namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	repoPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}/[A-Za-z0-9._-]{1,100}$`)
)

// SessionName is the squad's AMQ session name.
func (s Squad) SessionName() string {
	if s.Session != "" {
		return s.Session
	}
	return filepath.Base(s.Root)
}

// ProjectName is the squad's AMQ peer project name.
func (s Squad) ProjectName() string {
	if s.Project != "" {
		return s.Project
	}
	return s.Name
}

// Find returns the squad with this name.
func (r Registry) Find(name string) (Squad, bool) {
	i := slices.IndexFunc(r.Squads, func(s Squad) bool { return s.Name == name })
	if i < 0 {
		return Squad{}, false
	}
	return r.Squads[i], true
}

// Validate checks every field.
func (r Registry) Validate() error {
	if r.Schema != Schema {
		return fmt.Errorf("fleet.yaml: schema %d is not %d", r.Schema, Schema)
	}
	if r.Operator != (Operator{}) {
		if !absClean(r.Operator.Root) || !namePattern.MatchString(r.Operator.Project) || !namePattern.MatchString(r.Operator.Session) {
			return errors.New("fleet.yaml: operator needs an absolute root, a project name and a session name")
		}
	}
	seen := map[string]bool{}
	for _, s := range r.Squads {
		switch {
		case !namePattern.MatchString(s.Name) || seen[s.Name]:
			return fmt.Errorf("fleet.yaml: squad name %q is missing, invalid or repeated", s.Name)
		case !absClean(s.Root) || !absClean(s.ProjectPath):
			return fmt.Errorf("fleet.yaml: squad %s needs an absolute root and project_path", s.Name)
		case !repoPattern.MatchString(s.Repository):
			return fmt.Errorf("fleet.yaml: squad %s repository %q is not owner/name", s.Name, s.Repository)
		case !namePattern.MatchString(s.Lead) || s.Lead == OperatorHandle:
			return fmt.Errorf("fleet.yaml: squad %s lead handle %q is invalid or the operator's", s.Name, s.Lead)
		case s.Session != "" && !namePattern.MatchString(s.Session), s.Project != "" && !namePattern.MatchString(s.Project):
			return fmt.Errorf("fleet.yaml: squad %s has a bad session or project name", s.Name)
		}
		seen[s.Name] = true
	}
	seenWait := map[string]bool{}
	for _, w := range r.Waiting {
		_, err := time.Parse(time.RFC3339, w.Since)
		switch {
		case !namePattern.MatchString(w.Name) || seenWait[w.Name] || seen[w.Name]:
			return fmt.Errorf("fleet.yaml: waiting squad %q is invalid, repeated or registered", w.Name)
		case !repoPattern.MatchString(w.Repository) || !absClean(w.ProjectPath):
			return fmt.Errorf("fleet.yaml: waiting squad %s needs owner/name and an absolute project_path", w.Name)
		case w.Cap != ReasonMaxSquads && w.Cap != ReasonCostPerDay:
			return fmt.Errorf("fleet.yaml: waiting squad %s cap %q is not max_squads or cost_per_day", w.Name, w.Cap)
		case err != nil:
			return fmt.Errorf("fleet.yaml: waiting squad %s since %q is not RFC 3339", w.Name, w.Since)
		case strings.ContainsAny(w.Detail, "\"\x00\r\n"):
			return fmt.Errorf("fleet.yaml: waiting squad %s has a bad detail", w.Name)
		}
		seenWait[w.Name] = true
	}
	return nil
}

func absClean(p string) bool {
	return p != "" && filepath.IsAbs(p) && filepath.Clean(p) == p && !strings.ContainsAny(p, "\"\x00\r\n")
}

// Parse reads fleet.yaml strictly: '#' comments, top-level "schema: 1", an
// "operator:" map and a "squads:" list of maps, two-space indentation.
// Every key is known and none repeats.
func Parse(text, source string) (Registry, error) {
	r := Registry{}
	section, sawSchema := "", false
	var cur *Squad
	var wcur *Waiting
	// separate duplicate trackers: top-level keys, operator keys, and the
	// keys of the current list item (a list item resets only its own)
	topSeen, opSeen, seen := map[string]bool{}, map[string]bool{}, map[string]bool{}
	fail := func(n int, format string, args ...any) error {
		return fmt.Errorf("%s:%d: %s", source, n, fmt.Sprintf(format, args...))
	}
	for i, raw := range strings.Split(text, "\n") {
		n := i + 1
		line := strings.TrimRight(stripComment(raw), " \r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.Contains(line, "\t") {
			return r, fail(n, "tab in indentation or value")
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		body := strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(body, "- "); ok && indent == 2 && section == "squads" {
			r.Squads = append(r.Squads, Squad{})
			cur, body, indent = &r.Squads[len(r.Squads)-1], rest, 4
			seen = map[string]bool{}
		} else if ok && indent == 2 && section == "waiting" {
			r.Waiting = append(r.Waiting, Waiting{})
			wcur, body, indent = &r.Waiting[len(r.Waiting)-1], rest, 4
			seen = map[string]bool{}
		}
		key, value, ok := strings.Cut(body, ":")
		key, value = strings.TrimSpace(key), unquote(strings.TrimSpace(value))
		if !ok || key == "" {
			return r, fail(n, "malformed line %q", body)
		}
		switch {
		case indent == 0:
			if topSeen[key] {
				return r, fail(n, "key %q repeated", key)
			}
			topSeen[key] = true
			section = key
			switch key {
			case "schema":
				if value != "1" {
					return r, fail(n, "schema %q is not 1", value)
				}
				r.Schema, sawSchema = Schema, true
			case "operator", "squads", "waiting":
				if value != "" && !(key != "operator" && value == "[]") {
					return r, fail(n, "%s: want a block", key)
				}
			default:
				return r, fail(n, "unknown key %q", key)
			}
			continue
		case indent == 2 && section == "operator":
			if opSeen[key] {
				return r, fail(n, "operator.%s repeated", key)
			}
			opSeen[key] = true
			switch key {
			case "root":
				r.Operator.Root = value
			case "project":
				r.Operator.Project = value
			case "session":
				r.Operator.Session = value
			default:
				return r, fail(n, "unknown key operator.%s", key)
			}
		case indent == 4 && section == "squads" && cur != nil:
			if seen[key] {
				return r, fail(n, "squads.%s repeated", key)
			}
			seen[key] = true
			field := map[string]*string{"name": &cur.Name, "root": &cur.Root, "project_path": &cur.ProjectPath,
				"repository": &cur.Repository, "lead": &cur.Lead, "session": &cur.Session, "project": &cur.Project}[key]
			if field == nil {
				return r, fail(n, "unknown key squads.%s", key)
			}
			*field = value
		case indent == 4 && section == "waiting" && wcur != nil:
			if seen[key] {
				return r, fail(n, "waiting.%s repeated", key)
			}
			seen[key] = true
			field := map[string]*string{"name": &wcur.Name, "repository": &wcur.Repository, "project_path": &wcur.ProjectPath,
				"cap": &wcur.Cap, "since": &wcur.Since, "detail": &wcur.Detail}[key]
			if field == nil {
				return r, fail(n, "unknown key waiting.%s", key)
			}
			*field = value
		default:
			return r, fail(n, "misplaced line %q", body)
		}
		if value == "" {
			return r, fail(n, "%s: empty value", key)
		}
	}
	if !sawSchema {
		return r, fmt.Errorf("%s: schema missing (want schema: 1)", source)
	}
	return r, nil
}

func stripComment(line string) string {
	inQuote := false
	for i := 0; i < len(line); i++ {
		switch {
		case line[i] == '"':
			inQuote = !inQuote
		case line[i] == '#' && !inQuote && (i == 0 || line[i-1] == ' '):
			return line[:i]
		}
	}
	return line
}

func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// Render writes r in canonical form; Parse(Render(r)) == r.
func Render(r Registry) string {
	var b strings.Builder
	kv := func(indent, k, v string) {
		if v != "" {
			if strings.ContainsAny(v, "#:\"") || strings.TrimSpace(v) != v {
				v = `"` + v + `"`
			}
			fmt.Fprintf(&b, "%s%s: %s\n", indent, k, v)
		}
	}
	b.WriteString("# amq-squad fleet registry (plan 3.5, 16.3): each squad, its root, its lead handle.\n")
	fmt.Fprintf(&b, "schema: %d\n", Schema)
	if r.Operator != (Operator{}) {
		b.WriteString("operator:\n")
		kv("  ", "root", r.Operator.Root)
		kv("  ", "project", r.Operator.Project)
		kv("  ", "session", r.Operator.Session)
	}
	if len(r.Squads) == 0 {
		b.WriteString("squads: []\n")
	} else {
		b.WriteString("squads:\n")
	}
	for _, s := range r.Squads {
		fmt.Fprintf(&b, "  - name: %s\n", s.Name)
		kv("    ", "root", s.Root)
		kv("    ", "project_path", s.ProjectPath)
		kv("    ", "repository", s.Repository)
		kv("    ", "lead", s.Lead)
		kv("    ", "session", s.Session)
		kv("    ", "project", s.Project)
	}
	if len(r.Waiting) > 0 {
		b.WriteString("waiting:\n")
	}
	for _, w := range r.Waiting {
		fmt.Fprintf(&b, "  - name: %s\n", w.Name)
		kv("    ", "repository", w.Repository)
		kv("    ", "project_path", w.ProjectPath)
		kv("    ", "cap", w.Cap)
		kv("    ", "since", w.Since)
		kv("    ", "detail", w.Detail)
	}
	return b.String()
}
