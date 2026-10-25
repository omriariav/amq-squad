package redact

import "testing"

// The fix round's item 19: a JSON number under a credential name is a
// value too, and the redaction keeps the line valid JSON; counts and
// timings of tokens are not credentials.
func TestJSONNumberUnderACredentialName(t *testing.T) {
	s := `{"password":123456,"output_tokens":17,"first_token_ms":40}`
	f := Find(s)
	if len(f) != 1 || f[0].Name != "password" {
		t.Fatalf("findings %+v", f)
	}
	out := Line(s)
	if out != `{"password":"`+Placeholder+`","output_tokens":17,"first_token_ms":40}` || len(Find(out)) != 0 {
		t.Fatalf("redacted %q", out)
	}
}

// The fix round's question 6: the fixture allowlist is an exact match, no
// quote stripping: a quoted 'hunter2' is not the allowlisted hunter2.
func TestQuotedFixtureIsNotExempt(t *testing.T) {
	if len(Find("password=hunter2")) != 0 {
		t.Fatal("the exact allowlisted pair was found")
	}
	for _, s := range []string{"password='hunter2'", `password="hunter2"`, `password="'hunter2'"`} {
		if len(Find(s)) == 0 {
			t.Errorf("a quoted fixture passed: %s", s)
		}
	}
}
