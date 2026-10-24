package registry

import (
	"fmt"
	"strconv"
	"strings"
)

// The file is a strict YAML subset written only by Format: '#' comment lines,
// "schema: 1", a "host:" map, and "tools:" and "entries:" lists of maps
// ("  - key: value" then "    key: value"). A value is a bare word, a Go
// quoted string, a number, true/false, or an inline list "[a, b]". Parse
// rejects an unknown key, so a hand edit that misspells a field fails loudly.

const header = `# amq-squad registry (plan 5), schema 1. Written by 'amq-squad doctor --write-registry'.
# One entry per runtime x model x effort. The lead proposes seats from verified entries only.
# Hand edits survive only in probe_* fields; detection rewrites the rest.
`

type kv struct{ k, v string }

func (h Host) fields() []kv {
	return []kv{{"ram_gb", strconv.Itoa(h.RAMGB)}, {"cpu", q(h.CPU)}, {"gpu", q(h.GPU)}}
}

func (h *Host) set(k, v string) error {
	switch k {
	case "ram_gb":
		return setInt(&h.RAMGB, v)
	case "cpu":
		return setStr(&h.CPU, v)
	case "gpu":
		return setStr(&h.GPU, v)
	}
	return unknownKey(k)
}

func (t Tool) fields() []kv {
	return []kv{
		{"name", q(t.Name)}, {"found", strconv.FormatBool(t.Found)}, {"path", q(t.Path)},
		{"version", q(t.Version)}, {"verified_version", q(t.VerifiedVersion)},
		{"version_check", q(t.VersionCheck)}, {"login", q(t.Login)}, {"login_source", q(t.LoginSource)},
	}
}

func (t *Tool) set(k, v string) error {
	switch k {
	case "name":
		return setStr(&t.Name, v)
	case "found":
		return setBool(&t.Found, v)
	case "path":
		return setStr(&t.Path, v)
	case "version":
		return setStr(&t.Version, v)
	case "verified_version":
		return setStr(&t.VerifiedVersion, v)
	case "version_check":
		return setStr(&t.VersionCheck, v)
	case "login":
		return setStr(&t.Login, v)
	case "login_source":
		return setStr(&t.LoginSource, v)
	}
	return unknownKey(k)
}

func (e Entry) fields() []kv {
	return []kv{
		{"runtime", q(e.Runtime)}, {"provider", q(e.Provider)}, {"model", q(e.Model)}, {"effort", q(e.Effort)},
		{"verified", strconv.FormatBool(e.Verified)}, {"state", q(e.State)}, {"reason", q(e.Reason)},
		{"cost_tier", q(e.CostTier)}, {"privacy_class", q(e.PrivacyClass)}, {"quota_pool", q(e.QuotaPool)},
		{"concurrency_pool", q(e.ConcurrencyPool)}, {"concurrency_cap", strconv.Itoa(e.ConcurrencyCap)},
		{"concurrency_source", q(e.ConcurrencySource)},
		{"untrusted_input", strconv.FormatBool(e.UntrustedInput)}, {"probation", strconv.FormatBool(e.Probation)},
		{"latency_ms", strconv.Itoa(e.LatencyMS)}, {"latency_reasoning_ms", strconv.Itoa(e.LatencyReasoningMS)},
		{"capability_tier", q(e.CapabilityTier)}, {"cost_index", fnum(e.CostIndex)}, {"latency_index", fnum(e.LatencyIndex)},
		{"strengths", list(e.Strengths)}, {"work_classes", list(e.WorkClasses)},
		{"probe_runs", strconv.Itoa(e.ProbeRuns)}, {"probe_passes", strconv.Itoa(e.ProbePasses)},
		{"probe_false_successes", strconv.Itoa(e.ProbeFalseSuccess)}, {"probe_tool_errors", strconv.Itoa(e.ProbeToolErrors)},
		{"probe_predicate", q(e.ProbePredicate)}, {"probe_version", q(e.ProbeVersion)},
	}
}

func (e *Entry) set(k, v string) error {
	switch k {
	case "runtime":
		return setStr(&e.Runtime, v)
	case "provider":
		return setStr(&e.Provider, v)
	case "model":
		return setStr(&e.Model, v)
	case "effort":
		return setStr(&e.Effort, v)
	case "verified":
		return setBool(&e.Verified, v)
	case "state":
		return setStr(&e.State, v)
	case "reason":
		return setStr(&e.Reason, v)
	case "cost_tier":
		return setStr(&e.CostTier, v)
	case "privacy_class":
		return setStr(&e.PrivacyClass, v)
	case "quota_pool":
		return setStr(&e.QuotaPool, v)
	case "concurrency_pool":
		return setStr(&e.ConcurrencyPool, v)
	case "concurrency_cap":
		return setInt(&e.ConcurrencyCap, v)
	case "concurrency_source":
		return setStr(&e.ConcurrencySource, v)
	case "untrusted_input":
		return setBool(&e.UntrustedInput, v)
	case "probation":
		return setBool(&e.Probation, v)
	case "latency_ms":
		return setInt(&e.LatencyMS, v)
	case "latency_reasoning_ms":
		return setInt(&e.LatencyReasoningMS, v)
	case "capability_tier":
		return setStr(&e.CapabilityTier, v)
	case "cost_index":
		return setFloat(&e.CostIndex, v)
	case "latency_index":
		return setFloat(&e.LatencyIndex, v)
	case "strengths":
		return setList(&e.Strengths, v)
	case "work_classes":
		return setList(&e.WorkClasses, v)
	case "probe_runs":
		return setInt(&e.ProbeRuns, v)
	case "probe_passes":
		return setInt(&e.ProbePasses, v)
	case "probe_false_successes":
		return setInt(&e.ProbeFalseSuccess, v)
	case "probe_tool_errors":
		return setInt(&e.ProbeToolErrors, v)
	case "probe_predicate":
		return setStr(&e.ProbePredicate, v)
	case "probe_version":
		return setStr(&e.ProbeVersion, v)
	}
	return unknownKey(k)
}

// Format renders r in the file format. It is deterministic.
func Format(r Registry) string {
	var b strings.Builder
	b.WriteString(header)
	fmt.Fprintf(&b, "schema: %d\n", Schema)
	b.WriteString("host:\n")
	for _, f := range r.Host.fields() {
		fmt.Fprintf(&b, "  %s: %s\n", f.k, f.v)
	}
	writeList(&b, "tools", len(r.Tools), func(i int) []kv { return r.Tools[i].fields() })
	writeList(&b, "entries", len(r.Entries), func(i int) []kv { return r.Entries[i].fields() })
	if len(r.Pins) > 0 { // owner data; absent from a file that has none
		writeList(&b, "pins", len(r.Pins), func(i int) []kv { return r.Pins[i].fields() })
	}
	return b.String()
}

func writeList(b *strings.Builder, name string, n int, fields func(int) []kv) {
	if n == 0 {
		fmt.Fprintf(b, "%s: []\n", name)
		return
	}
	fmt.Fprintf(b, "%s:\n", name)
	for i := 0; i < n; i++ {
		for j, f := range fields(i) {
			lead := "    "
			if j == 0 {
				lead = "  - "
			}
			fmt.Fprintf(b, "%s%s: %s\n", lead, f.k, f.v)
		}
	}
}

// Parse reads the file format.
func Parse(text string) (Registry, error) {
	var r Registry
	section := ""
	var setter func(k, v string) error
	for n, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, " \t\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		fail := func(err error) (Registry, error) { return Registry{}, fmt.Errorf("line %d: %w", n+1, err) }
		switch {
		case !strings.HasPrefix(line, " "):
			k, v, ok := splitKV(line)
			if !ok {
				return fail(fmt.Errorf("malformed line %q", line))
			}
			section, setter = k, nil
			switch k {
			case "schema":
				if err := setInt(&r.Schema, v); err != nil {
					return fail(err)
				}
				if r.Schema != Schema {
					return fail(fmt.Errorf("schema %d, want %d", r.Schema, Schema))
				}
			case "host":
				setter = r.Host.set
			case "tools", "entries", "pins":
				if v != "" && v != "[]" {
					return fail(fmt.Errorf("%s: want a block list", k))
				}
			default:
				return fail(unknownKey(k))
			}
		case strings.HasPrefix(line, "  - "):
			k, v, ok := splitKV(line[4:])
			if !ok {
				return fail(fmt.Errorf("malformed line %q", line))
			}
			switch section {
			case "tools":
				r.Tools = append(r.Tools, Tool{})
				t := &r.Tools[len(r.Tools)-1]
				setter = t.set
			case "entries":
				r.Entries = append(r.Entries, Entry{})
				e := &r.Entries[len(r.Entries)-1]
				setter = e.set
			case "pins":
				r.Pins = append(r.Pins, Pin{})
				p := &r.Pins[len(r.Pins)-1]
				setter = p.set
			default:
				return fail(fmt.Errorf("list item outside tools, entries or pins"))
			}
			if err := setter(k, v); err != nil {
				return fail(err)
			}
		default:
			k, v, ok := splitKV(strings.TrimSpace(line))
			if !ok || setter == nil {
				return fail(fmt.Errorf("malformed line %q", line))
			}
			if section == "host" && !strings.HasPrefix(line, "  ") || section != "host" && !strings.HasPrefix(line, "    ") {
				return fail(fmt.Errorf("bad indent %q", line))
			}
			if err := setter(k, v); err != nil {
				return fail(err)
			}
		}
	}
	if r.Schema == 0 {
		return Registry{}, fmt.Errorf("no schema line")
	}
	return r, nil
}

func splitKV(s string) (string, string, bool) {
	i := strings.Index(s, ":")
	if i <= 0 {
		return "", "", false
	}
	return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:]), true
}

func unknownKey(k string) error { return fmt.Errorf("unknown key %q", k) }

func q(s string) string {
	if s == "" {
		return `""`
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._/-:", c)) {
			return strconv.Quote(s)
		}
	}
	if s == "true" || s == "false" || s[0] == '-' {
		return strconv.Quote(s)
	}
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return strconv.Quote(s)
	}
	return s
}

func fnum(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

func list(in []string) string {
	in = normalizeList(in)
	parts := make([]string, len(in))
	for i, s := range in {
		parts[i] = q(s)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func unquote(v string) (string, error) {
	if strings.HasPrefix(v, `"`) {
		return strconv.Unquote(v)
	}
	return v, nil
}

func setStr(dst *string, v string) error {
	s, err := unquote(v)
	*dst = s
	return err
}

func setInt(dst *int, v string) error {
	n, err := strconv.Atoi(v)
	*dst = n
	return err
}

func setFloat(dst *float64, v string) error {
	f, err := strconv.ParseFloat(v, 64)
	*dst = f
	return err
}

func setBool(dst *bool, v string) error {
	b, err := strconv.ParseBool(v)
	*dst = b
	return err
}

// setList reads "[a, b]". Items are bare words or Go quoted strings without
// commas inside quotes (Format never writes one).
func setList(dst *[]string, v string) error {
	if !strings.HasPrefix(v, "[") || !strings.HasSuffix(v, "]") {
		return fmt.Errorf("want an inline list, got %q", v)
	}
	inner := strings.TrimSpace(v[1 : len(v)-1])
	*dst = nil
	if inner == "" {
		return nil
	}
	for _, part := range strings.Split(inner, ",") {
		s, err := unquote(strings.TrimSpace(part))
		if err != nil {
			return err
		}
		*dst = append(*dst, s)
	}
	return nil
}
