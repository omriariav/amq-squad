package redact

import (
	"strings"
	"testing"
	"time"
)

// v3-5-verifier, item 4 of the third round: redaction is linear in a line,
// however many values it holds: a 1 MiB single line of repeated name=value
// assignments redacts within 2 s on the gate host (the bound recorded in the
// execution log; under the race detector's instrumentation, raceBound).
func TestBlockIsLinearOnALongLine(t *testing.T) {
	line := strings.Repeat("password"+"=x ", (1<<20)/11)
	start := time.Now()
	done := make(chan string, 1)
	go func() { done <- Block(line) }()
	var out string
	select {
	case out = <-done:
	case <-time.After(linearBound):
		t.Fatalf("a 1 MiB line with %d values took over %s", strings.Count(line, "="), linearBound)
	}
	took := time.Since(start)
	if strings.Contains(out, "=x ") {
		t.Fatal("a value of the long line was not redacted")
	}
	t.Logf("1 MiB, %d values: %s", strings.Count(line, "="), took.Round(time.Millisecond))
}

// Item 2 of the third round: a quoted YAML scalar on the line after its key
// is a value whatever it holds (a colon and a blank included).
func TestBlockRedactsAQuotedYAMLScalarWithAColon(t *testing.T) {
	value := "Ab9" + "xY7qR: uv" // built at run time, never a literal
	for _, in := range []string{"password:\n  '" + value + "'\n", "password:\n  \"" + value + "\"\n"} {
		if out := Block(in); strings.Contains(out, value[:8]) {
			t.Fatal("a quoted YAML value with a colon survived")
		}
	}
}

// The fourth round, item 4: a quoted scalar opened on a credential key's
// value line is redacted through its closing quote across lines, in both
// quote kinds, and an unclosed one through the last more-indented line.
func TestBlockRedactsAContinuedQuotedYAMLScalar(t *testing.T) {
	head, tail := "Ab9"+"x", "Y7"+"qR" // built at run time, never a literal
	for _, q := range []string{"'", `"`} {
		in := "password:\n  " + q + head + "\n    " + tail + q + "\nnext: kept\n"
		out := Block(in)
		if strings.Contains(out, head) || strings.Contains(out, tail) || !strings.Contains(out, "next: kept") {
			t.Fatalf("a continued %s-quoted scalar: the value survived or the next key went", q)
		}
	}
	if out := Block("password:\n  '" + head + "\n    " + tail + "\nnext: kept\n"); strings.Contains(out, tail) || !strings.Contains(out, "next: kept") {
		t.Fatal("an unclosed quoted scalar: not redacted through its more-indented lines")
	}
	// The closing quote bounds the scalar, not the indentation: a
	// continuation line back at the key's column is still the value.
	if out := Block("password:\n  '" + head + "\n" + tail + "'\nnext: kept\n"); strings.Contains(out, tail) || !strings.Contains(out, "next: kept") {
		t.Fatal("a quoted scalar continued at the key's column: redacted short of its closing quote")
	}
}

// The fourth round, item 5: a credential key owns its block: every line
// indented deeper than the key is redacted until the indentation returns.
func TestBlockRedactsANestedMappingUnderACredentialKey(t *testing.T) {
	value := "Ab9" + "xY7qR"
	out := Block("password:\n  nested:\n    '" + value + "'\nuser: kept\n")
	if strings.Contains(out, value) || strings.Contains(out, "nested:") || !strings.Contains(out, "user: kept") {
		t.Fatal("a nested mapping under a credential key survived, or the next key went")
	}
}

// Item 3 of the third round, the cap rule: redact first, then cut, and
// never keep a partial last line.
func TestCappedRedactsBeforeTheCut(t *testing.T) {
	value := "Ab9" + "xY7qR" + "Lm2Wq8ZtPv4"
	in := strings.Repeat(" ", 4080) + "{\"password\":\n\"" + value + "\"}"
	out, cut := Capped(in, 4096)
	if !cut || strings.Contains(out, value[:8]) || len(out) > 4096 {
		t.Fatalf("capped: cut %v, %d bytes, value kept %v", cut, len(out), strings.Contains(out, value[:8]))
	}
}
