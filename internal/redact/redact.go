// Package redact is the one credential redaction of the repository (briefs
// ruling 18, after the 2b-2 milestone B exposure): the hook ledger's
// secret-name rule, the provider-token patterns, the text redaction every
// evidence file passes through before it is committed, and the environment
// strip of every harness subprocess. The repository scan (repo_test.go)
// fails on anything Find reports under docs/ or an evidence directory, so
// what Text redacts and what the scan rejects are one rule.
//
// The cap rule (v3-5-verifier): wherever a size cap applies to redacted
// text (the verifier's capture and its 4 KiB tail, the hook ingest's
// prefix), redact first, then cut, and never keep a partial last line: a cut
// can split a value from what its matcher needs (a closing quote, the next
// line), so the cut comes after the redaction and drops the line it lands
// in. Capped is that rule.
//
// The prose-scan trade-off of the one-line YAML rule (v3-5-verifier): the
// repository scan runs the same rules over the docs and the evidence, so a
// bare one-line value counts only when it is a single token with a letter or
// digit. A bare multi-word value (`password: x y`) is therefore skipped by
// the one-line rule, the shape rules still running inside it, and so is a
// value with no letter or digit (`${VAR}`); a quoted value always counts.
package redact

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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

// locationSuffixes name where a credential lives, never the credential
// itself (CLAUDE_TOKEN_FILE, SSH_AUTH_SOCK). A count of tokens is exempt only
// by its exact name, in CountFields.
var locationSuffixes = []string{"_FILE", "_PATH", "_DIR", "_SOCK", "_NAME", "_ENV", "_VAR"}

// CountFields are the exact field names whose numeric value counts or times
// tokens, never a token (ruling 18 and 3-2's P1-7: an explicit list, never
// a shape; the narrow pass on the fix round): the usage counters and the
// count and timing fields the committed evidence carries. The exemption is
// for a number only: a string under one of these names is still redacted,
// and any other credential-class name redacts whatever its value's type.
var CountFields = map[string]bool{
	"input_tokens": true, "output_tokens": true, "cache_read_tokens": true, "cache_creation_tokens": true,
	"cache_read_input_tokens": true, "cache_creation_input_tokens": true,
	"totalTokens": true, "maxTokens": true, "max_tokens": true, "tokens_in": true, "tokens_out": true,
	"first_token_ms": true, "first_token_s": true, "cold_prompt_first_token_s": true, "warm_prompt_first_token_s_median": true,
	"cached_input_tokens": true, "cache_write_input_tokens": true, "ephemeral_5m_input_tokens": true, "ephemeral_1h_input_tokens": true,
	"reasoning_output_tokens": true, "thinking_tokens": true, "reset_tokens": true,
}

var numberRE = regexp.MustCompile(`^-?[0-9][0-9.eE+-]*$`)

// countValue reports a number under an exact CountFields name.
func countValue(name, value string) bool { return CountFields[name] && numberRE.MatchString(value) }

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

// RealCredentialShapes are the exact shapes of real provider credentials,
// the merge gate's secret scan (and GitHub push protection's kind): AWS
// access keys, OpenAI keys, GitHub classic tokens, Google API keys, nostr
// secret keys and PEM private keys. A value of one of these shapes trips
// every scanner whatever it is meant to be, so no placeholder word (a
// CANARY marker included) exempts it: it is always a finding and is always
// redacted (the milestone B merge gate).
var RealCredentialShapes = []*regexp.Regexp{
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`sk-[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`ghp_[A-Za-z0-9]{30,}`),
	regexp.MustCompile(`AIza[0-9A-Za-z_-]{30,}`),
	regexp.MustCompile(`nsec1[a-z0-9]{20,}`),
	regexp.MustCompile(`BEGIN [A-Z ]*PRIVATE KEY`),
}

// assignRE finds NAME=value (an environment entry, a flag, a shell line);
// jsonRE finds "name": "value". Group 1 is the name, group 2 the value. A
// flag (--api-key=v) names its credential with hyphens.
var (
	assignRE = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_-]*)=("(?:[^"\\\n]|\\.)*"|'[^'\n]*'|[^\s"'\[\](){},;<>|&` + "`" + `]+)`)
	// The value is any JSON scalar: a string (group 2, with its quotes) or
	// a number (the fix round's item 19: {"password":123456} is a value).
	jsonRE = regexp.MustCompile(`"([A-Za-z0-9_.-]+)"\s*:\s*("(?:[^"\\\n]|\\.)*"|-?[0-9][0-9.eE+-]*)`)
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
	// No numeric exemption under a credential name (the second pass on ruling
	// 18: DB_PASSWORD=12345678 is a credential); a count is exempt only by its
	// name (locationSuffixes: _TOKENS and the like).
	return true
}

// namedSecret reports whether a value under a credential-class name is a
// credential: every value is, whatever its length or first character
// (phase 5 re-pass of 2b-2 B, P1-2: DB_PASSWORD=p4ss7! and
// DB_PASSWORD=/Ab9xY7q are credentials); only the redaction's own
// placeholders, an empty value, or an exact AllowedFixtures entry for that
// name are exempt. No shape (length, first character, a placeholder word)
// exempts a value any more.
func namedSecret(name, v string) bool {
	// Exact match, no quote stripping (the fix round's question 6: a quoted
	// 'hunter2' is not the allowlisted hunter2): only the redaction's own
	// placeholder forms and an exact allowlisted pair are exempt.
	if v == "" || placeholderForms[v] {
		return false
	}
	return !AllowedFixtures[name+"="+v]
}

// placeholderForms are the exact values the redaction itself writes (bare,
// and in the quotes of the value it replaced).
var placeholderForms = map[string]bool{
	Placeholder: true, `"` + Placeholder + `"`: true, `'` + Placeholder + `'`: true,
	"[redacted]": true, `"[redacted]"`: true,
}

// AllowedFixtures are the exact NAME=value pairs the repository's docs and
// evidence carry as fixtures or examples (help text, test values in revert
// logs): a whole pair, never a pattern. A new fixture under a credential
// name is added here by its exact pair, or redacted.
var AllowedFixtures = map[string]bool{
	"key=value": true, "ckey=value": true, // codex -c key=value help text and its argv evidence
	"CLAUDE_CODE_OAUTH_TOKEN=parent-token": true, "GEMINI_API_KEY=private-value": true,
	"API_KEY=k77": true, "GH_TOKEN=t": true, "api_key=k1": true, "secret=s": true, "password=hunter2": true,
	"password=...": true, "apiKey=ollama": true,
	"password=1234": true, "pin_secret=4242": true, // the ledger reader's numeric fixtures in 3-2's revert log
}

// Finding is one credential-shaped value: its line and the rule that found
// it. The value itself is never carried.
type Finding struct {
	Line int    // 1-based
	Rule string // NAME=value, json, provider-token
	Name string // the credential name, or the token's prefix
}

// workHook, when set (only by tests), is told the units of work the
// algorithm does: the bytes of a line a scan reads, and each match, merge
// entry or line it handles. Nil in production: the cost is one nil check.
var workHook func(n int)

func work(n int) {
	if workHook != nil {
		workHook(n)
	}
}

// spans returns the byte ranges of every credential-shaped value in a line.
func spans(line string) (out [][2]int, found []Finding) {
	work(len(line))
	for _, m := range assignRE.FindAllStringSubmatchIndex(line, -1) {
		work(1)
		if m[2] > 0 && isAlnum(line[m[2]-1]) {
			continue // the tail of a longer word
		}
		name, val := line[m[2]:m[3]], line[m[4]:m[5]]
		if CredentialName(name) && !countValue(name, val) && namedSecret(name, val) {
			out = append(out, [2]int{m[4], m[5]})
			found = append(found, Finding{Rule: "NAME=value", Name: name})
		}
	}
	for _, m := range yamlLineRE.FindAllStringSubmatchIndex(line, -1) {
		work(1)
		open, name, closeQ := line[m[2]:m[3]], line[m[4]:m[5]], line[m[6]:m[7]]
		if open != closeQ {
			continue
		}
		a, b := m[8], m[9]
		if ia, ib, iname, ok := innerCredential(line, a); ok {
			out = append(out, [2]int{ia, ib}) // err: api_key: V, the inner key wins
			found = append(found, Finding{Rule: "yaml", Name: iname})
			continue
		}
		val := line[a:b]
		if q := val[0]; q != '\'' && q != '"' {
			if c := yamlComment(val); c >= 0 {
				val = val[:c] // a comment: # after any blank
			}
			val = strings.TrimRight(val, " \t\r")
			b = a + len(val)
		}
		if val == "" || strings.EqualFold(name, "key") || !yamlValue(val) {
			continue
		}
		if CredentialName(name) && !countValue(name, val) && namedSecret(name, strings.Trim(val, `"'`)) {
			out = append(out, [2]int{a, b})
			found = append(found, Finding{Rule: "yaml", Name: name})
		}
	}
	lead := -1
	for _, m := range colonKeyRE.FindAllStringSubmatchIndex(line, -1) {
		work(1)
		open, name, closeQ := line[m[2]:m[3]], line[m[4]:m[5]], line[m[6]:m[7]]
		if open != closeQ || strings.EqualFold(name, "key") {
			continue
		}
		val := line[m[8]:m[9]]
		if ia, ib, iname, ok := innerCredential(line, m[8]); ok {
			out = append(out, [2]int{ia, ib}) // warn: token:V, err:api_key: V
			found = append(found, Finding{Rule: "yaml", Name: iname})
			continue
		}
		if !colonShape(name, val, line, m[9]) {
			continue // prose, a flag, a switch, a call: not credential-shaped
		}
		if lead < 0 {
			lead = leadEnd(line) // once per line: each byte of the prefix is examined once
		}
		if m[8] > m[7]+1 && m[4] <= lead {
			continue // a line-start key with a space after its colon: the yaml rule's
		}
		out = append(out, [2]int{m[8], m[9]})
		found = append(found, Finding{Rule: "yaml", Name: name})
	}
	for _, m := range jsonRE.FindAllStringSubmatchIndex(line, -1) {
		work(1)
		name, val := line[m[2]:m[3]], line[m[4]:m[5]]
		if strings.EqualFold(name, "key") {
			continue // a map entry's key, not a credential's name
		}
		inner := val
		if strings.HasPrefix(val, `"`) {
			inner = val[1 : len(val)-1] // the JSON string's content, compared exactly
		}
		if CredentialName(name) && !countValue(name, val) && namedSecret(name, inner) {
			out = append(out, [2]int{m[4], m[5]}) // the whole value, quotes included
			found = append(found, Finding{Rule: "json", Name: name})
		}
	}
	for _, m := range ProviderToken.FindAllStringIndex(line, -1) {
		work(1)
		if m[0] > 0 && isWord(line[m[0]-1]) && line[m[0]-1] != '_' && line[m[0]-1] != '-' {
			continue // inside a longer word (task-..., disk-...)
		}
		if tok := line[m[0]:m[1]]; secretValue(tok) {
			out = append(out, [2]int{m[0], m[1]})
			found = append(found, Finding{Rule: "provider-token", Name: prefix(tok)})
		}
	}
	for _, re := range RealCredentialShapes {
		work(1)
		for _, m := range re.FindAllStringIndex(line, -1) {
			work(1)
			out = append(out, [2]int{m[0], m[1]})
			found = append(found, Finding{Rule: "real-shape", Name: line[m[0]:min(m[0]+4, m[1])]})
		}
	}
	return out, found
}

// leadEnd is the index of the first byte of a line that is not indentation,
// a list dash or a quote: a key starting at or before it opens the line.
func leadEnd(line string) int {
	i := 0
	for i < len(line) && strings.IndexByte(" \t-\"'", line[i]) >= 0 {
		i++
	}
	work(i + 1)
	return i
}

// colonShape reports whether a colon-form value under name is a credential:
// one token (or a quoted scalar) of at least minSecretLen with a letter and a
// digit, not a Go pointer, not followed at once by "(" (a call), under a
// credential name that is not "key", not a count, and a namedSecret.
func colonShape(name, val, line string, end int) bool {
	if strings.EqualFold(name, "key") || (end < len(line) && line[end] == '(') {
		return false
	}
	inner := strings.Trim(val, `"'`)
	if len(inner) < minSecretLen || !yamlValue(val) || !letterAndDigit(inner) || pointerRE.MatchString(inner) {
		return false
	}
	return CredentialName(name) && !countValue(name, val) && namedSecret(name, inner)
}

// maxPeel bounds the plain keys innerCredential skips: a chain of keys
// (a: a: a: ...) costs a constant per match, not the rest of the line.
const maxPeel = 3

// innerCredential finds a credential key and its value at the start of
// line[a:], past any run of plain keys (err: api_key: V, warn: token:V, a: b:
// password: V): when an outer rule's captured value begins with a key and a
// colon, the inner key wins. ok is false unless the first credential-class
// key has a credential-shaped value (prose such as password: reverted stays).
func innerCredential(line string, a int) (from, to int, name string, ok bool) {
	for pos, peeled := a, 0; pos < len(line) && peeled < maxPeel; peeled++ {
		work(1)
		m := innerKeyRE.FindStringSubmatchIndex(line[pos:])
		if m == nil || line[pos+m[2]:pos+m[3]] != line[pos+m[6]:pos+m[7]] {
			return 0, 0, "", false
		}
		n, vs, ve := line[pos+m[4]:pos+m[5]], pos+m[8], pos+m[9]
		if CredentialName(n) {
			if colonShape(n, line[vs:ve], line, ve) {
				return vs, ve, n, true
			}
			return 0, 0, "", false
		}
		pos = vs
	}
	return 0, 0, "", false
}

// pointerRE is a Go pointer or address as %+v prints it (TokensIn:0xc0001a2b3c).
var pointerRE = regexp.MustCompile(`^0x[0-9a-fA-F]+$`)

// letterAndDigit reports a value with both an ASCII letter and a digit: the
// shape that keeps a word (reverted) or a path (runtime_actions/x) out of the
// colon rules, which have no blank after the colon or no line start to lean on.
// The equals and YAML rules do not share this limit: a letters-only or
// digits-only value is redacted there and kept here.
func letterAndDigit(v string) bool {
	var l, d bool
	for i := 0; i < len(v); i++ {
		work(1)
		switch c := v[i]; {
		case c >= '0' && c <= '9':
			d = true
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
			l = true
		}
	}
	return l && d
}

// yamlValue reports whether a one-line YAML value can be a credential: a
// quoted scalar always; a bare one when it is a single token with a letter
// or digit, and not a YAML null or boolean, a masked value (***) or a call
// expression (Go or code in prose: "Key: the owner's key", "Credential:
// credentialSource(rt)", "Token: gho_****").
func yamlValue(v string) bool {
	if v[0] == '\'' || v[0] == '"' {
		return true
	}
	switch strings.ToLower(v) {
	case "null", "~", "true", "false":
		return false
	}
	if strings.ContainsAny(v, " \t") || strings.Contains(v, "***") || callExpression(v) {
		return false
	}
	return strings.IndexFunc(v, func(r rune) bool { return r < 128 && isAlnum(byte(r)) && r != '_' }) >= 0
}

// callRE is a call expression: an identifier (dotted) and a parenthesized
// argument list; longRunRE a run of 8 or more letters and digits.
var (
	callRE    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*\((.*)\)$`)
	longRunRE = regexp.MustCompile(`[A-Za-z0-9]{8,}`)
)

// callExpression reports a call expression whose arguments carry no run of
// 8 or more letters and digits (credentialSource(rt)); one that does
// (evil(Ab9xY7qRLm2W)) is a value like any other.
func callExpression(v string) bool {
	m := callRE.FindStringSubmatch(v)
	return m != nil && !longRunRE.MatchString(m[1])
}

// yamlComment is the index of a comment's # in a bare one-line value (a #
// after a space or a tab), -1 when none.
func yamlComment(v string) int {
	for i := 1; i < len(v); i++ {
		work(1)
		if v[i] == '#' && (v[i-1] == ' ' || v[i-1] == '\t') {
			return i - 1
		}
	}
	return -1
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
// (a JSON string value stays a JSON string). One builder pass over the
// sorted, merged spans: linear in the line, however many values it holds.
func Line(line string) string {
	sp, _ := spans(line)
	if len(sp) == 0 {
		return line
	}
	merged := mergeSpans(sp)
	var out strings.Builder
	out.Grow(len(line))
	at := 0
	for _, s := range merged {
		work(1)
		rep := Placeholder
		if s[1]-s[0] >= 2 && line[s[0]] == '"' && line[s[1]-1] == '"' || jsonValueAt(line, s[0]) {
			rep = `"` + Placeholder + `"` // a JSON value stays a JSON string
		}
		out.WriteString(line[at:s[0]])
		out.WriteString(rep)
		at = s[1]
	}
	out.WriteString(line[at:])
	return out.String()
}

// Capped is the cap rule: the text is redacted whole (Block), then cut to at
// most limit bytes at a line end (the line the cut lands in is dropped).
// cut reports that something was dropped.
func Capped(text string, limit int) (out string, cut bool) {
	out = Block(text)
	if len(out) <= limit {
		return out, false
	}
	out = out[:limit]
	if i := strings.LastIndexByte(out, '\n'); i >= 0 {
		return out[:i+1], true
	}
	return "", true
}

// jsonValueAt reports whether the span at i is a JSON object value (the
// first non-space byte before it is a colon after a quoted name).
func jsonValueAt(line string, i int) bool {
	j := i - 1
	for j >= 0 && (line[j] == ' ' || line[j] == '\t') {
		work(1)
		j--
	}
	return j > 0 && line[j] == ':' && strings.TrimRight(line[:j], " \t") != "" && strings.HasSuffix(strings.TrimRight(line[:j], " \t"), `"`)
}

func mergeSpans(sp [][2]int) [][2]int {
	work(len(sp))
	sort.Slice(sp, func(i, j int) bool { work(1); return sp[i][0] < sp[j][0] })
	out := [][2]int{sp[0]}
	for _, s := range sp[1:] {
		work(1)
		last := &out[len(out)-1]
		if s[0] < last[1] {
			last[1] = max(last[1], s[1])
			continue
		}
		out = append(out, s)
	}
	return out
}

// jsonBlockRE is jsonRE across lines: JSON whitespace, newlines included,
// between the quoted name, the colon and the value (a pretty-printed or
// wrapped object). Group 1 is the name, group 2 the value. yamlKeyRE is a
// YAML key line (group 1 the indentation up to the name, group 2 the name,
// group 3 what follows the colon); yamlBlockIndicatorRE a block scalar's
// indicator (| or >).
var (
	jsonBlockRE = regexp.MustCompile(`"([A-Za-z0-9_.-]+)"[ \t\r\n]*:[ \t\r\n]*("(?:[^"\\\n]|\\.)*"|-?[0-9][0-9.eE+-]*)`)
	yamlKeyRE   = regexp.MustCompile(`^([ \t]*(?:- )?)(['"]?)([A-Za-z_][A-Za-z0-9_.-]*)['"]?[ \t]*:[ \t]*(.*)$`)
	// yamlLineRE is a YAML key: value on one line (the fifth round's P1): a
	// bare or quoted key, after the line's start, a blank, "{" or "," and an
	// optional "- "; a quoted scalar, or a bare one up to a flow delimiter.
	// Group 1 and 3 are the key's quotes, group 2 the name, group 4 the value.
	yamlLineRE = regexp.MustCompile(`(?:^|[\s{,])(?:- )?(['"]?)([A-Za-z_][A-Za-z0-9_.-]*)(['"]?)[ \t]*:[ \t]+('(?:[^'\n]|'')*'|"(?:[^"\\\n]|\\.)*"|[^\s'"#,{}\[\]][^\n,{}]*)`)
	// colonKeyRE closes two forms yamlLineRE misses: a credential key with no
	// space after its colon (API_KEY:Zq9xV7mP2wL4) and one after prose on the
	// same line with text between the colon and the key (error: see api_key:
	// Zq9xV7mP2wL4 now: the one-line YAML rule's value "see api_key: ..." took
	// the span, but this rule matches the key itself). It does not own the key
	// that follows another key's colon at once (err: api_key: Zq9xV7mP2wL4):
	// there the preceding match, the YAML rule's value or this rule's own
	// token "api_key:", had already claimed the key, so no match can start at
	// it. innerCredential owns that form. The key follows a blank, a quote, a comma, a
	// semicolon, a parenthesis or a bracket; the value is one token of the
	// equals rule's value class, whole or not at all. Groups
	// 1 and 3 are the key's quotes, group 2 the name, group 4 the value.
	colonKeyRE = regexp.MustCompile(`(?:^|[ \t"',;(\[])(['"]?)([A-Za-z_][A-Za-z0-9_.-]*)(['"]?):[ \t]*('(?:[^'\n]|'')*'|"(?:[^"\\\n]|\\.)*"|[^\s"'\[\](){},;<>|&` + "`" + `]+)`)
	// innerKeyRE is a key, a colon, optional blanks and a value at the start
	// of a string, the colon rule's own shape without the delimiter before it.
	innerKeyRE           = regexp.MustCompile(`^(['"]?)([A-Za-z_][A-Za-z0-9_.-]*)(['"]?):[ \t]*('(?:[^'\n]|'')*'|"(?:[^"\\\n]|\\.)*"|[^\s"'\[\](){},;<>|&` + "`" + `]+)`)
	yamlBlockIndicatorRE = regexp.MustCompile(`^[|>][-+0-9]*[ \t]*(#.*)?$`)
)

// yamlBlocks redacts the value of every credential-named YAML key whose
// value is not on its own line (v3-5-verifier, the fourth round): a key owns
// its block. A quoted scalar opened on the value line is redacted through
// its closing quote across lines, or to the end of the buffer when it never
// closes (the buffer is capped already; no fall back to indentation while a
// quote is open); then every line indented deeper than the key is redacted
// whole until the indentation returns (a bare scalar, a nested mapping, a
// block scalar, whose quotes are text, not scalars), and so is an
// indentless list item at the key's own column.
func yamlBlocks(text string) string {
	lines := strings.Split(text, "\n")
	work(len(text))
	changed := false
	for i := 0; i < len(lines); i++ {
		work(1)
		line := strings.TrimRight(lines[i], "\r")
		mi := yamlKeyRE.FindStringSubmatchIndex(line)
		if mi == nil || !CredentialName(line[mi[6]:mi[7]]) {
			continue
		}
		m := []string{line, line[mi[2]:mi[3]], line[mi[4]:mi[5]], line[mi[6]:mi[7]], line[mi[8]:mi[9]]}
		rest := strings.TrimSpace(m[4])
		blockScalar := yamlBlockIndicatorRE.MatchString(rest)
		if q := quoteOpen(rest); q != 0 {
			// A quoted scalar opened on the key's line: closed there, it is the
			// one-line rule's; left open, the quote search runs from it, the
			// rule of a value on the next line.
			at := mi[8] + strings.Index(m[4], rest)
			if end := closingFrom(lines, i, at+1, q); end > i {
				lines[i] = lines[i][:at] + Placeholder
				for k := i + 1; k <= end; k++ {
					work(1)
					lines[k] = redactLineContent(lines[k])
				}
				changed, i = true, end
			}
			continue
		}
		if rest != "" && !strings.HasPrefix(rest, "#") && !blockScalar {
			continue // the value is on the key's line (Line's rules)
		}
		keyCol := len(m[1])
		j := i + 1
		if j < len(lines) && !blockScalar {
			if q := quoteOpen(lines[j]); q != 0 {
				end := closingLine(lines, j, q)
				for k := j; k <= end; k++ {
					work(1)
					lines[k] = redactLineContent(lines[k])
				}
				changed, j = true, end+1
			}
		}
		for ; j < len(lines); j++ {
			work(1)
			l := strings.TrimRight(lines[j], "\r")
			if strings.TrimSpace(l) == "" {
				continue
			}
			ind := indentOf(l)
			if ind < keyCol || ind == keyCol && !strings.HasPrefix(strings.TrimSpace(l), "- ") {
				break // the indentation returned (an indentless list item stays the key's)
			}
			lines[j] = redactLineContent(lines[j])
			changed = true
		}
		i = j - 1
	}
	if !changed {
		return text
	}
	return strings.Join(lines, "\n")
}

// quoteOpen is the quote a line's content opens with (0 when none).
func quoteOpen(line string) byte {
	t := strings.TrimLeft(line, " \t")
	if t != "" && (t[0] == '\'' || t[0] == '"') {
		return t[0]
	}
	return 0
}

// closingLine is the index of the line that closes the quoted scalar opened
// on lines[j] (a doubled single quote inside single quotes and a backslash
// escape inside double quotes do not close); the last line of the buffer when
// it never closes.
func closingLine(lines []string, j int, q byte) int {
	return closingFrom(lines, j, strings.IndexByte(lines[j], q)+1, q)
}

// closingFrom is closingLine with the search starting at byte start of
// lines[j] (just after the opening quote).
func closingFrom(lines []string, j, start int, q byte) int {
	for k := j; k < len(lines); k++ {
		l := lines[k]
		from := 0
		if k == j {
			from = start
		}
		for p := from; p < len(l); p++ {
			work(1)
			c := l[p]
			switch {
			case q == '"' && c == '\\':
				p++
			case c == q && q == '\'' && p+1 < len(l) && l[p+1] == '\'':
				p++
			case c == q:
				return k
			}
		}
	}
	return len(lines) - 1
}

func indentOf(line string) int { return len(line) - len(strings.TrimLeft(line, " \t")) }

// redactLineContent replaces a line's content (after its indentation) with
// Placeholder; a line that is already a placeholder stays.
func redactLineContent(line string) string {
	ind := line[:indentOf(line)]
	content := strings.TrimRight(line[len(ind):], "\r")
	if content == "" || placeholderForms[content] {
		return line
	}
	cr := ""
	if strings.HasSuffix(line, "\r") {
		cr = "\r"
	}
	return ind + Placeholder + cr
}

// Block redacts a whole text (v3-5-verifier, P1-2 of the second pass): the
// credential-name rules run over the text as one buffer, so a value split
// from its name across lines (`{"password":` then the value on the next
// line, or a YAML key that owns the block under it, yamlBlocks) is replaced;
// then every line passes Line's rules as well.
func Block(text string) string {
	type span struct{ a, b int }
	var sp []span
	work(len(text))
	for _, m := range jsonBlockRE.FindAllStringSubmatchIndex(text, -1) {
		work(1)
		name, val := text[m[2]:m[3]], text[m[4]:m[5]]
		if strings.EqualFold(name, "key") {
			continue
		}
		inner := val
		if strings.HasPrefix(val, `"`) {
			inner = val[1 : len(val)-1]
		}
		if CredentialName(name) && !countValue(name, val) && namedSecret(name, inner) {
			sp = append(sp, span{m[4], m[5]})
		}
	}
	sort.Slice(sp, func(i, j int) bool { work(1); return sp[i].a < sp[j].a })
	var out strings.Builder
	at := 0
	for _, s := range sp {
		work(1)
		if s.a < at {
			continue // inside a span already replaced
		}
		rep := Placeholder
		if strings.HasPrefix(text[s.a:s.b], `"`) {
			rep = `"` + Placeholder + `"`
		}
		out.WriteString(text[at:s.a])
		out.WriteString(rep)
		at = s.b
	}
	out.WriteString(text[at:])
	return Text(yamlBlocks(out.String()))
}

// Text redacts every line of a text.
func Text(text string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		work(1)
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
