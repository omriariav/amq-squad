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
	if out := Block("password:\n  nested: map\n"); !strings.Contains(out, "nested: map") {
		t.Fatal("a bare nested map entry was taken for the key's scalar")
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
