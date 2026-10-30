package redact

import (
	"strings"
	"testing"
)

// Phase 5 re-pass of 2b-2 B, P1-2: under a credential-class name every value
// is a credential, whatever its length or first character; only the
// placeholders and an exact allowlisted fixture pair pass.
func TestCredentialNameRedactsEveryValue(t *testing.T) {
	for _, s := range []string{"DB_PASSWORD=p4ss7!", "DB_PASSWORD=/Ab9xY7q", "DB_PASSWORD=$x", `{"password": "ab"}`, "GH_TOKEN=t1", "key=other"} {
		if len(Find(s)) == 0 {
			t.Errorf("not found: %q", s)
		}
		if out := Line(s); len(Find(out)) != 0 || !strings.Contains(out, Placeholder) {
			t.Errorf("Line left a finding in %q: %q", s, out)
		}
	}
	for _, s := range []string{"DB_PASSWORD=" + Placeholder, "key=value", "GH_TOKEN=t", `"token": "[redacted]"`} {
		if f := Find(s); len(f) != 0 {
			t.Errorf("an allowlisted value was found: %q %+v", s, f)
		}
	}
}
