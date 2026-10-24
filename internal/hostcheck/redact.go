package hostcheck

import (
	"bytes"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

// Redacted replaces a secret value in every diff line.
const Redacted = "<redacted>"

// secretKey reports whether a config key names a secret, at any depth.
func secretKey(key string) bool {
	k := strings.ToUpper(strings.Trim(key, `"' `))
	return SecretName(k) || strings.Contains(k, "PASSWORD") || strings.Contains(k, "SECRET") ||
		strings.Contains(k, "TOKEN") || strings.HasSuffix(k, "KEY") || k == "AUTHORIZATION" || strings.Contains(k, "CREDENTIAL")
}

// redacted is a config file reduced to diffable lines with every secret
// value replaced, plus the raw secret values by path, so a changed secret is
// reported as changed without printing either side.
type redacted struct {
	lines   []string
	secrets map[string]string
}

// redactConfig parses JSON (settings.json) or TOML (config.toml) and
// redacts secret-class fields recursively before any line is diffed. A file
// that does not parse falls back to line redaction of every key = value or
// "key": value occurrence in the line, at any position.
func redactConfig(raw []byte, toml bool) redacted {
	if toml {
		return redactTOML(string(raw))
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err == nil {
		r := redacted{secrets: map[string]string{}}
		v = redactJSONValue(v, "", false, r.secrets)
		out, _ := json.MarshalIndent(v, "", "  ")
		r.lines = splitLines(string(out))
		return r
	}
	r := redacted{secrets: map[string]string{}}
	for _, l := range splitLines(string(raw)) {
		r.lines = append(r.lines, redactInline(l, r.secrets, ""))
	}
	return r
}

func redactJSONValue(v any, path string, secret bool, secrets map[string]string) any {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			p := joinPath(path, k)
			t[k] = redactJSONValue(child, p, secret || secretKey(k), secrets)
		}
		return t
	case []any:
		for i, child := range t {
			t[i] = redactJSONValue(child, path+"[]", secret, secrets)
		}
		return t
	default:
		raw, _ := json.Marshal(t)
		if secret {
			secrets[path] += string(raw)
			return Redacted
		}
		if s, ok := t.(string); ok && tokenRE.MatchString(s) {
			secrets[path] += s
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

var (
	tomlTableRE = regexp.MustCompile(`^\s*\[\[?\s*([^\]]+?)\s*\]\]?\s*(#.*)?$`)
	tomlKVRE    = regexp.MustCompile(`^(\s*)((?:"[^"]*"|'[^']*'|[A-Za-z0-9_.-]+)(?:\s*\.\s*(?:"[^"]*"|'[^']*'|[A-Za-z0-9_.-]+))*)(\s*=\s*)(.*)$`)
	// inlineKVRE finds key = value and "key": value pairs anywhere in a line
	// (inline tables, nested JSON on one line).
	inlineKVRE = regexp.MustCompile(`("?)([A-Za-z0-9_.-]+)("?\s*[:=]\s*)("(?:[^"\\]|\\.)*"|'[^']*'|[^,}\]\s]+)`)
)

// redactTOML is a line reader for the TOML subset codex-cli writes: table
// headers, key = value lines (dotted and quoted keys), inline tables.
func redactTOML(text string) redacted {
	r := redacted{secrets: map[string]string{}}
	table := ""
	for _, line := range splitLines(text) {
		if m := tomlTableRE.FindStringSubmatch(line); m != nil {
			table = strings.TrimSpace(m[1])
			r.lines = append(r.lines, line)
			continue
		}
		m := tomlKVRE.FindStringSubmatch(line)
		if m == nil {
			r.lines = append(r.lines, redactInline(line, r.secrets, table))
			continue
		}
		key := m[2]
		parts := strings.Split(key, ".")
		path := joinPath(table, strings.TrimSpace(key))
		if secretKey(parts[len(parts)-1]) {
			r.secrets[path] += m[4]
			r.lines = append(r.lines, m[1]+m[2]+m[3]+`"`+Redacted+`"`)
			continue
		}
		r.lines = append(r.lines, m[1]+m[2]+m[3]+redactInline(m[4], r.secrets, path))
	}
	return r
}

// redactInline redacts every secret-named pair and every token-looking
// value in one line.
func redactInline(line string, secrets map[string]string, path string) string {
	line = inlineKVRE.ReplaceAllStringFunc(line, func(pair string) string {
		m := inlineKVRE.FindStringSubmatch(pair)
		if !secretKey(m[2]) {
			return pair
		}
		secrets[joinPath(path, m[2])] += m[4]
		return m[1] + m[2] + m[3] + `"` + Redacted + `"`
	})
	if tokenRE.MatchString(line) {
		secrets[joinPath(path, "<token>")] += strings.Join(tokenRE.FindAllString(line, -1), ",")
		line = tokenRE.ReplaceAllString(line, Redacted)
	}
	return line
}

// changedSecrets names the secret paths whose values differ, sorted.
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
