package migrate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/redact"
)

// Refusal codes of migrate.
const (
	CodeSafetyDefault = "migrate_refused_safety_default" // a v2 field would change a safety default
	CodeCredential    = "migrate_refused_credential"     // an input or output value has the shape of a real credential
	CodeSquadExists   = "squad_yaml_exists"              // squad.yaml is present; migrate never overwrites it
	CodeProposedExist = "migrate_proposed_exists"        // grants.yaml.proposed is present; migrate never overwrites it
	CodeInput         = "migrate_input_invalid"          // the v2 files are not a readable profile
	CodeSquadInvalid  = "migrate_squad_invalid"          // the squad.yaml migrate would write does not load
	CodeRemoveNoSquad = "migrate_remove_needs_squad"     // --remove-v2 with no (valid) squad.yaml
	CodeSymlink       = "migrate_refused_symlink"        // a symlink on the way to a v2 file
	CodeRootChanged   = "root_changed"                   // the project root is no longer the directory the run opened
)

// Refusal is a migrate refusal. Fields names the v2 fields (or files) behind
// it, never a value.
type Refusal struct {
	Code   string
	Fields []string
	Detail string
}

func (r *Refusal) Error() string {
	s := r.Code + ": " + r.Detail
	if len(r.Fields) > 0 {
		s += " [" + strings.Join(r.Fields, ", ") + "]"
	}
	return s
}

// wideningFlag are the substrings of a native CLI argument that change a
// safety default: a flag that skips permission or approval prompts, widens
// the sandbox or pre-allows tools. wideningPair are (flag, value) pairs,
// matched on normalized argv tokens: "--flag value" and "--flag=value" are
// the same pair. Both lists are closed and lower-case, and over-refusal is
// accepted; an argument that matches neither is only reported as unmapped.
var wideningFlag = []string{"dangerous", "bypass", "danger-full-access", "yolo", "full-auto", "allowedtools", "allowed-tools"}

var wideningPair = []struct{ Flag, Value string }{
	{"--ask-for-approval", "never"}, {"--ask-for-approval", "on-failure"}, {"-a", "never"},
	{"--approval-policy", "never"},
	{"--sandbox", "danger-full-access"},
	{"--permission-mode", "bypasspermissions"}, {"--permission-mode", "dontask"},
}

// widensArgs reports the first argument (or flag and value) that matches
// wideningFlag or wideningPair.
func widensArgs(args []string) (string, bool) {
	toks := make([]string, 0, len(args))
	for _, a := range args {
		l := strings.ToLower(strings.TrimSpace(a))
		if strings.HasPrefix(l, "-") {
			if flag, value, ok := strings.Cut(l, "="); ok {
				toks = append(toks, flag, value)
				continue
			}
		}
		toks = append(toks, l)
	}
	for i, t := range toks {
		for _, w := range wideningFlag {
			if strings.Contains(t, w) {
				return t, true
			}
		}
		for _, pair := range wideningPair {
			if t == pair.Flag && i+1 < len(toks) && toks[i+1] == pair.Value {
				return t + " " + pair.Value, true
			}
		}
	}
	return "", false
}

// safetyKey reports a v2 key whose name says it is an allow list or a
// launcher preference: it would carry authority 3.0 takes from the owner's
// grant alone. A name with disallow, deny or block in it narrows and is not
// one.
func safetyKey(name string) bool {
	l := strings.ToLower(name)
	if strings.Contains(l, "disallow") || strings.Contains(l, "deny") || strings.Contains(l, "block") {
		return false
	}
	return l == "launcher_preference" || strings.Contains(l, "allow")
}

// checkSafety adds path when the field named name holds a value and the name
// is a safetyKey, else scans the fields under v.
func checkSafety(path, name string, v any, add func(path string)) {
	if safetyKey(name) && !isEmpty(v) {
		add(path)
		return
	}
	scanSafety(path, v, add)
}

// scanSafety adds the path of every set field under v whose key is a
// safetyKey.
func scanSafety(path string, v any, add func(path string)) {
	switch x := v.(type) {
	case map[string]any:
		for _, k := range sortedKeys(x) {
			next := path + "." + k
			if path == "" {
				next = k
			}
			if safetyKey(k) && !isEmpty(x[k]) {
				add(next)
				continue
			}
			scanSafety(next, x[k], add)
		}
	case []any:
		for i, e := range x {
			scanSafety(fmt.Sprintf("%s[%d]", path, i), e, add)
		}
	}
}

// credentialFields returns the sorted names of the places where a string
// matches a real credential shape (redact.RealCredentialShapes): a JSON path
// inside a file, or the file alone for text. It never returns a value.
func credentialFields(where string, v any) []string {
	var out []string
	walkStrings("", v, func(path, s string) {
		if looksLikeCredential(s) {
			out = append(out, where+": "+path)
		}
	})
	sort.Strings(out)
	return out
}

func looksLikeCredential(s string) bool {
	for _, re := range redact.RealCredentialShapes {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

// walkKeys calls fn for every object key under v with the path of its parent.
func walkKeys(path string, v any, fn func(parent, key string)) {
	switch x := v.(type) {
	case []any:
		for i, e := range x {
			walkKeys(fmt.Sprintf("%s[%d]", path, i), e, fn)
		}
	case map[string]any:
		for _, k := range sortedKeys(x) {
			fn(path, k)
			next := k
			if path != "" {
				next = path + "." + k
			}
			walkKeys(next, x[k], fn)
		}
	}
}

// credentialKeyFields names the places where an object KEY has a real
// credential shape. The location is the parent path and "a key name", never
// the key's text, which is itself the credential.
func credentialKeyFields(where string, v any) []string {
	var out []string
	walkKeys("", v, func(parent, key string) {
		if looksLikeCredential(key) {
			loc := parent
			if loc == "" {
				loc = "top level"
			}
			out = append(out, where+": a key name under "+loc)
		}
	})
	sort.Strings(out)
	return dedupe(out)
}

// unsafeText reports text migrate must not print or write: a real credential
// shape, or anything redact.Block would change (rule 18: every byte migrate
// prints or writes passes redact.Block).
func unsafeText(s string) bool { return looksLikeCredential(s) || redact.Block(s) != s }
