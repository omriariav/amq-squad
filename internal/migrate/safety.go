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
// the sandbox or pre-allows tools. The list is closed and lower-case; an
// argument that matches none of it is only reported as unmapped.
var wideningFlag = []string{"dangerous", "bypass", "danger-full-access", "yolo", "full-auto", "allowedtools", "allowed-tools", "permission-mode"}

// widensArgs reports the first argument that matches wideningFlag.
func widensArgs(args []string) (string, bool) {
	for _, a := range args {
		l := strings.ToLower(a)
		for _, w := range wideningFlag {
			if strings.Contains(l, w) {
				return a, true
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
