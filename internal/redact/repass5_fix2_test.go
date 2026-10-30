package redact

import (
	"strings"
	"testing"
)

// The narrow pass on the fix round, item 19's ruling: the count exemption
// is an explicit list of exact field names (CountFields), for numeric
// values only. A string under a listed name, a token under "tokens", and a
// credential-shaped name outside the list are redacted whatever the type.
func TestCountFieldExemptionIsExactNumericOnly(t *testing.T) {
	sk := "sk-" + strings.Repeat("Ab3", 8)   // built at run time: no source carries the shape
	ghp := "ghp_" + strings.Repeat("Q7", 16) // likewise
	for _, s := range []string{
		`{"tokens":"` + sk + `"}`, `{"latency_ms":"` + ghp + `"}`,
		`{"api_tokens":5}`, `{"session_token_ms":7}`, `{"input_tokens":"abc"}`, "api_tokens=5",
	} {
		if len(Find(s)) == 0 {
			t.Errorf("not found: %s", strings.NewReplacer(sk, "<sk>", ghp, "<ghp>").Replace(s))
		}
		if out := Line(s); len(Find(out)) != 0 {
			t.Errorf("Line left a finding")
		}
	}
	for _, s := range []string{`{"input_tokens":1234}`, `{"cache_read_tokens":0,"output_tokens":17}`, "input_tokens=123456789"} {
		if f := Find(s); len(f) != 0 {
			t.Errorf("a listed count was found: %s %+v", s, f)
		}
	}
}
