package hostcheck

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// Redacted replaces a secret value in every diff line.
const Redacted = "<redacted>"

// withheld is the only line shown for a file that does not parse: nothing
// of an unparsed file is printed.
const withheld = "<file does not parse as one document; contents withheld>"

// secretKey reports whether a config key or table name names a secret.
func secretKey(key string) bool {
	k := strings.ToUpper(strings.Trim(key, `"' `))
	return SecretName(k) || strings.Contains(k, "PASSWORD") || strings.Contains(k, "SECRET") ||
		strings.Contains(k, "TOKEN") || strings.HasSuffix(k, "KEY") || k == "AUTHORIZATION" ||
		strings.Contains(k, "CREDENTIAL")
}

// redacted is a config file reduced to diffable lines with every secret
// value replaced, plus the raw secret values by path, so a changed secret is
// reported as changed without printing either side. bookkeeping holds the
// values codex-cli rewrites itself (amendment 17 (d)).
type redacted struct {
	lines       []string
	secrets     map[string]string
	bookkeeping map[string]string
}

// redactConfig parses settings.json as exactly one JSON document and
// config.toml with a TOML parser, then redacts by key at any depth: a value
// is redacted when its key, or any table or object above it, is
// secret-class, and any string that looks like a token is redacted. The
// redacted document is rendered as indented JSON for the diff. A file that
// does not parse (including trailing content after the JSON value) prints
// only the withheld line, and a change to it is a redacted pair.
func redactConfig(raw []byte, isTOML bool) redacted {
	r := redacted{secrets: map[string]string{}, bookkeeping: map[string]string{}}
	var doc any
	if isTOML {
		var m map[string]any
		if _, err := toml.Decode(string(raw), &m); err != nil {
			return withheldFile(r, raw)
		}
		takeCodexBookkeeping(m, r.bookkeeping)
		doc = m
	} else {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&doc); err != nil {
			return withheldFile(r, raw)
		}
		var extra any
		if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
			return withheldFile(r, raw)
		}
	}
	doc = redactValue(doc, "", false, r.secrets)
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return withheldFile(r, raw)
	}
	r.lines = splitLines(string(out))
	return r
}

func withheldFile(r redacted, raw []byte) redacted {
	r.lines = []string{withheld}
	r.secrets["<whole file>"] = string(raw)
	return r
}

// takeCodexBookkeeping removes [marketplaces.*] last_updated, which
// codex-cli rewrites at every start.
func takeCodexBookkeeping(m map[string]any, into map[string]string) {
	mk, ok := m["marketplaces"].(map[string]any)
	if !ok {
		return
	}
	for name, v := range mk {
		if t, ok := v.(map[string]any); ok {
			if lu, ok := t["last_updated"]; ok {
				raw, _ := json.Marshal(lu)
				into["marketplaces."+name+".last_updated"] = string(raw)
				delete(t, "last_updated")
			}
		}
	}
}

func redactValue(v any, path string, secret bool, secrets map[string]string) any {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			t[k] = redactValue(child, joinPath(path, k), secret || secretKey(k), secrets)
		}
		return t
	case []map[string]any: // a TOML array of tables
		out := make([]any, len(t))
		for i, child := range t {
			out[i] = redactValue(child, path+"[]", secret, secrets)
		}
		return out
	case []any:
		for i, child := range t {
			t[i] = redactValue(child, path+"[]", secret, secrets)
		}
		return t
	default:
		raw, _ := json.Marshal(t)
		if secret {
			secrets[path] += string(raw) + "\x00"
			return Redacted
		}
		if s, ok := t.(string); ok && tokenRE.MatchString(s) {
			secrets[path] += s + "\x00"
			return tokenRE.ReplaceAllString(s, Redacted)
		}
		return t
	}
}

func joinPath(base, key string) string {
	if base == "" {
		return key
	}
	return base + "." + key
}

// inlineKVRE finds key = value and "key": value pairs anywhere in a line.
// It backs RedactLine only, a last pass over lines that were already
// redacted structurally.
var inlineKVRE = regexp.MustCompile(`("?)([A-Za-z0-9_.-]+)("?\s*[:=]\s*)("(?:[^"\\]|\\.)*"|'[^']*'|[^,}\]\s]+)`)

func redactInline(line string) string {
	line = inlineKVRE.ReplaceAllStringFunc(line, func(pair string) string {
		m := inlineKVRE.FindStringSubmatch(pair)
		if !secretKey(m[2]) || m[4] == `"`+Redacted+`"` {
			return pair
		}
		return m[1] + m[2] + m[3] + `"` + Redacted + `"`
	})
	return tokenRE.ReplaceAllString(line, Redacted)
}

// changedSecrets names the paths whose values differ, sorted.
func changedSecrets(a, b map[string]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range []map[string]string{a, b} {
		for p := range m {
			if !seen[p] && a[p] != b[p] {
				out = append(out, p)
			}
			seen[p] = true
		}
	}
	sort.Strings(out)
	return out
}
