// Package redact is the one credential redaction of the repository (briefs
// ruling 18, after the 2b-2 milestone B exposure): the hook ledger's
// secret-name rule, the provider-token patterns, the text redaction every
// evidence file passes through before it is committed, and the environment
// strip of every harness subprocess. The repository scan (repo_test.go)
// fails on anything Find reports under docs/ or an evidence directory, so
// what Text redacts and what the scan rejects are one rule.
package redact

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Placeholder replaces a redacted value.
const Placeholder = "<redacted>"

// ledgerMarkers is the hook ledger's rule for secret names (the v2 rule the
// ledger reader applies to every object key): a name that contains any of
// these, case folded, is secret-class.
var ledgerMarkers = []string{"TOKEN", "SECRET", "PASSWORD", "PASSWD", "CREDENTIAL", "PRIVATE_KEY", "API_KEY"}

// LedgerSecretName is the hook ledger's rule; the ledger reader calls it.
func LedgerSecretName(name string) bool {
	upper := strings.ToUpper(name)
	for _, m := range ledgerMarkers {
		if strings.Contains(upper, m) {
			return true
		}
	}
	return false
}

// locationSuffixes name where a credential lives or how many there are,
// never the credential itself (CLAUDE_TOKEN_FILE, SSH_AUTH_SOCK,
// input_tokens).
var locationSuffixes = []string{"_FILE", "_PATH", "_DIR", "_SOCK", "_TOKENS", "_NAME", "_ENV", "_VAR"}

// CredentialName is the ledger's rule plus a name ending in KEY or
// containing NSEC: the names whose NAME=value the scan rejects and the
// environment strip drops.
func CredentialName(name string) bool {
	upper := strings.ToUpper(strings.Trim(name, `"' `))
	for _, s := range locationSuffixes {
		if strings.HasSuffix(upper, s) {
			return false
		}
	}
	if strings.Contains(upper, "PUBKEY") || strings.Contains(upper, "PUBLIC_KEY") || strings.Contains(upper, "PUBLICKEY") {
		return false // a public key is published, not secret
	}
	return LedgerSecretName(upper) || strings.HasSuffix(upper, "KEY") || strings.Contains(upper, "NSEC")
}

// ProviderToken matches the known provider credential prefixes wherever
// they sit in a line: OpenAI and Anthropic sk-, GitHub, Slack, AWS, Google,
// JWTs, GitLab, npm, Hugging Face and nostr nsec.
var ProviderToken = regexp.MustCompile(`(?i)(sk-[a-z0-9_-]{8,}|gh[pousr]_[A-Za-z0-9]{16,}|github_pat_[A-Za-z0-9_]{20,}|xox[abpsr]-[A-Za-z0-9-]{8,}|AKIA[A-Z0-9]{12,}|AIza[0-9A-Za-z_-]{20,}|eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}|glpat-[A-Za-z0-9_-]{16,}|npm_[A-Za-z0-9]{30,}|hf_[A-Za-z0-9]{30,}|nsec1[02-9ac-hj-np-z]{20,})`)

// assignRE finds NAME=value (an environment entry, a flag, a shell line);
// jsonRE finds "name": "value". Group 1 is the name, group 2 the value. A
// flag (--api-key=v) names its credential with hyphens.
var (
	assignRE = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_-]*)=("(?:[^"\\\n]|\\.)*"|'[^'\n]*'|[^\s"'\[\](){},;<>|&` + "`" + `]+)`)
	jsonRE   = regexp.MustCompile(`"([A-Za-z0-9_.-]+)"\s*:\s*"((?:[^"\\\n]|\\.)*)"`)
)

// minSecretLen: a shorter value is a flag, a switch or a placeholder.
const minSecretLen = 8

// placeholderWords mark a fixture or an example value, never a credential
// (a real token is random text).
var placeholderWords = []string{"redacted", "example", "fake", "dummy", "placeholder", "canary", "fixture", "sample", "changeme", "notreal", "private-value", "parent-token", "your-"}

// secretValue reports whether a value under a credential name, or a token
// match, looks like a real credential rather than a placeholder, a
// reference or a path.
func secretValue(v string) bool {
	v = strings.Trim(v, `"'`)
	if len(v) < minSecretLen {
		return false
	}
	low := strings.ToLower(v)
	for _, w := range placeholderWords {
		if strings.Contains(low, w) {
			return false
		}
	}
	if strings.Contains(v, "...") || strings.Contains(v, "…") {
		return false
	}
	switch v[0] {
	case '$', '%', '<', '{', '/', '~', '.':
		return false
	}
	if strings.Trim(v, v[:1]) == "" { // xxxxxxxx, ********
		return false
	}
	for _, c := range v {
		if c < '0' || c > '9' {
			return true
		}
	}
	return false // all digits: a count or an id
}

// Finding is one credential-shaped value: its line and the rule that found
// it. The value itself is never carried.
type Finding struct {
	Line int    // 1-based
	Rule string // NAME=value, json, provider-token
	Name string // the credential name, or the token's prefix
}

// spans returns the byte ranges of every credential-shaped value in a line.
func spans(line string) (out [][2]int, found []Finding) {
	for _, m := range assignRE.FindAllStringSubmatchIndex(line, -1) {
		if m[2] > 0 && isAlnum(line[m[2]-1]) {
			continue // the tail of a longer word
		}
		name, val := line[m[2]:m[3]], line[m[4]:m[5]]
		if CredentialName(name) && secretValue(val) {
			out = append(out, [2]int{m[4], m[5]})
			found = append(found, Finding{Rule: "NAME=value", Name: name})
		}
	}
	for _, m := range jsonRE.FindAllStringSubmatchIndex(line, -1) {
		name, val := line[m[2]:m[3]], line[m[4]:m[5]]
		if strings.EqualFold(name, "key") {
			continue // a map entry's key, not a credential's name
		}
		if CredentialName(name) && secretValue(val) {
			out = append(out, [2]int{m[4] - 1, m[5] + 1}) // with its quotes
			found = append(found, Finding{Rule: "json", Name: name})
		}
	}
	for _, m := range ProviderToken.FindAllStringIndex(line, -1) {
		if m[0] > 0 && isWord(line[m[0]-1]) && line[m[0]-1] != '_' && line[m[0]-1] != '-' {
			continue // inside a longer word (task-..., disk-...)
		}
		if tok := line[m[0]:m[1]]; secretValue(tok) {
			out = append(out, [2]int{m[0], m[1]})
			found = append(found, Finding{Rule: "provider-token", Name: prefix(tok)})
		}
	}
	return out, found
}

func isAlnum(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func isWord(c byte) bool {
	return c == '_' || c == '-' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func prefix(tok string) string {
	if i := strings.IndexAny(tok, "-_"); i > 0 && i < 12 {
		return tok[:i+1]
	}
	return tok[:4]
}

// Count is the number of distinct credential-shaped values of a text (two
// rules matching one value count once).
func Count(text string) int {
	n := 0
	for _, line := range strings.Split(text, "\n") {
		if sp, _ := spans(line); len(sp) > 0 {
			n += len(mergeSpans(sp))
		}
	}
	return n
}

// Find reports every credential-shaped value of a text, by line.
func Find(text string) []Finding {
	var all []Finding
	for i, line := range strings.Split(text, "\n") {
		_, found := spans(line)
		for _, f := range found {
			f.Line = i + 1
			all = append(all, f)
		}
	}
	return all
}

// Line redacts one line: every credential-shaped value becomes Placeholder
// (a JSON string value stays a JSON string).
func Line(line string) string {
	sp, _ := spans(line)
	if len(sp) == 0 {
		return line
	}
	merged := mergeSpans(sp)
	for i := len(merged) - 1; i >= 0; i-- {
		s := merged[i]
		rep := Placeholder
		if s[1]-s[0] >= 2 && line[s[0]] == '"' && line[s[1]-1] == '"' {
			rep = `"` + Placeholder + `"`
		}
		line = line[:s[0]] + rep + line[s[1]:]
	}
	return line
}

func mergeSpans(sp [][2]int) [][2]int {
	for i := 1; i < len(sp); i++ { // a handful of spans: insertion sort
		for j := i; j > 0 && sp[j][0] < sp[j-1][0]; j-- {
			sp[j], sp[j-1] = sp[j-1], sp[j]
		}
	}
	out := [][2]int{sp[0]}
	for _, s := range sp[1:] {
		last := &out[len(out)-1]
		if s[0] < last[1] {
			last[1] = max(last[1], s[1])
			continue
		}
		out = append(out, s)
	}
	return out
}

// Text redacts every line of a text.
func Text(text string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = Line(l)
	}
	return strings.Join(lines, "\n")
}

// Bytes is Text for file contents; a binary file (a NUL byte) is returned
// unchanged.
func Bytes(b []byte) []byte {
	if bytes.IndexByte(b, 0) >= 0 {
		return b
	}
	return []byte(Text(string(b)))
}

// File redacts one file in place and returns how many values it replaced.
func File(path string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil || bytes.IndexByte(raw, 0) >= 0 {
		return 0, err
	}
	n := Count(string(raw))
	if n == 0 {
		return 0, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return n, os.WriteFile(path, Bytes(raw), info.Mode().Perm())
}

// Tree redacts every regular file under dir (dir may be one file) and
// returns the replaced counts by path.
func Tree(dir string) (map[string]int, error) {
	out := map[string]int{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		n, err := File(p)
		if n > 0 {
			out[p] = n
		}
		return err
	})
	return out, err
}

// StripEnv drops every credential-named entry of an environment (the
// strip of every harness subprocess, R2/P2 of the milestone B first pass),
// keeping the names in keep (a subprocess's own credential, such as gh's
// GH_TOKEN). It never returns a value it dropped.
func StripEnv(env []string, keep ...string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if CredentialName(name) && !contains(keep, name) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
