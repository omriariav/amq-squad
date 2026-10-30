package redact

import (
	"strings"
	"testing"
	"time"
)

// longLine is n bytes (rounded down to whole pairs) of repeated name=value
// assignments, built at run time: a credential-named key with a one-letter
// value, never a credential-shaped literal.
func longLine(n int) string { return strings.Repeat("password"+"=x ", n/11) }

// timed is one timing of Block on line, and its output.
func timed(line string) (time.Duration, string) {
	start := time.Now()
	out := Block(line)
	return time.Since(start), out
}

// v3-5-verifier, item 4 of the third round: redaction is linear in a line,
// however many values it holds. The proof is a ratio measured in one
// process (3.0.1 t7, the reviewer ruling of 2026-10-30), not a wall-clock
// bound: the 1 MiB line holds 16x the 64 KiB line's values and must take no
// more than 64x its time (a generous linear factor; a quadratic scan takes
// about 256x). One warm-up call on the small line comes first. The two
// sizes are then timed in three interleaved pairs (small, then large), and
// the best of each size counts. Interleaving spreads a load change that
// lasts across the run over both sizes. It does not cancel a burst that
// lands on one timing, so a ratio over the limit gets one more interleaved
// pair before the test fails, and the log line records the retry.
//
// Before (the absolute bound, gate host, 2026-10-30): 1 MiB in 241 ms, and
// 4.922 s under -race (bounds 2 s and 20 s).
// After (this ratio, interleaved, same host): 64 KiB 13 ms, 1 MiB 213 ms,
// 16.5x; under -race 64 KiB 315 ms, 1 MiB 5.058 s, 16.0x.
func TestBlockIsLinearOnALongLine(t *testing.T) {
	const limit = 64
	small, big := longLine(1<<16), longLine(1<<20)
	Block(small) // warm-up: first-call costs stay out of the small timing
	var smallBest, bigBest time.Duration
	var out string
	pair := func(first bool) {
		s, _ := timed(small)
		b, o := timed(big)
		out = o
		if first || s < smallBest {
			smallBest = s
		}
		if first || b < bigBest {
			bigBest = b
		}
	}
	for i := 0; i < 3; i++ {
		pair(i == 0)
	}
	retried := false
	if bigBest > limit*smallBest {
		pair(false)
		retried = true
	}
	if strings.Contains(out, "=x ") {
		t.Fatal("a value of the long line was not redacted")
	}
	ratio := float64(bigBest) / float64(smallBest)
	t.Logf("64 KiB, %d values: %s; 1 MiB, %d values: %s; ratio %.1fx (limit %dx); retried %v",
		strings.Count(small, "="), smallBest.Round(time.Millisecond),
		strings.Count(big, "="), bigBest.Round(time.Millisecond), ratio, limit, retried)
	if bigBest > limit*smallBest {
		t.Fatalf("16x the input took %.1fx the time (over %dx, after one retry): Block is not linear in a line", ratio, limit)
	}
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
	// An unclosed quote runs to the end of the buffer (the fifth round's
	// (a)): no fall back to indentation while a quote is open.
	if out := Block("password:\n  '" + head + "\n    " + tail + "\nnext: " + tail + "\n"); strings.Contains(out, tail) {
		t.Fatal("an unclosed quoted scalar: not redacted to the end of the buffer")
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

// oneLineYAML are the fifth round's P1 inputs: a credential-named key with
// its value on the same line (bare key, quoted key, inside a flow mapping)
// and an indentless list under the key. The value is built at run time.
func oneLineYAML(value string) map[string]string {
	return map[string]string{
		"bare":       "password: " + value,
		"quoted_key": "'password': " + value,
		"flow":       "creds: {password: " + value + ", user: kept}",
		"indentless": "password:\n- " + value + "\nuser: kept",
	}
}

// The fifth round's P1: YAML on one line is redacted (a bare or quoted key
// after the line's start, a blank, "{" or ","; a bare or quoted scalar), and
// an indentless list item at the key's column belongs to the key's block.
func TestBlockRedactsOneLineYAML(t *testing.T) {
	value := "Ab9" + "xY7qR"
	for name, in := range oneLineYAML(value) {
		if out := Block(in); strings.Contains(out, value) {
			t.Errorf("%s: the value survived", name)
		}
	}
	if out := Block("user: kept, token: null, password: true"); !strings.Contains(out, "user: kept") || !strings.Contains(out, "token: null") {
		t.Fatal("a YAML null, a boolean or a non-credential key was redacted")
	}
}

// The fifth round's (a): the closing quote is searched to the end of the
// buffer: a quoted scalar continued over 63 lines is redacted through its
// last line.
func TestQuotedScalarHasNoLineBound(t *testing.T) {
	tail := "Y7" + "qR"
	in := "password:\n  'Ab9x\n" + strings.Repeat("    more\n", 63) + "    " + tail + "'\nnext: kept\n"
	if out := Block(in); strings.Contains(out, tail) || !strings.Contains(out, "next: kept") {
		t.Fatal("a long quoted scalar: its last line survived, or the next key went")
	}
}

// The fifth round's (b): inside a block scalar a quote is text: the scalar
// ends with the indentation, and the next key stays.
func TestBlockScalarQuotesAreText(t *testing.T) {
	out := Block("password: |\n  'a secret\nnext: kept\n")
	if strings.Contains(out, "a secret") || !strings.Contains(out, "next: kept") {
		t.Fatal("a block scalar: its line survived, or a quote in it swallowed the next key")
	}
}

// The fifth round's (c): redaction never puts back a value it removed:
// Block(Block(x)) holds no fixture value Block(x) removed (equality is not
// required), over the corpus of every shape the rows use.
func TestBlockOfBlockKeepsRemovedValuesOut(t *testing.T) {
	value := "Ab9" + "xY7qR" + "Lm2Wq8Zt"
	corpus := []string{
		"{\"password\":\n  \"" + value + "\"}",
		"password:\n  '" + value + "'",
		"password:\n  '" + value[:6] + "\n    " + value[6:] + "'",
		"password:\n  nested:\n    '" + value + "'",
		"password: |\n  " + value + "\nnext: kept",
		"password" + "=" + value,
		"x " + "xox" + "b-" + value,
	}
	for _, v := range oneLineYAML(value) {
		corpus = append(corpus, v)
	}
	for i, x := range corpus {
		once := Block(x)
		if strings.Contains(once, value) {
			t.Fatalf("corpus %d: Block kept the value", i)
		}
		if strings.Contains(Block(once), value) {
			t.Fatalf("corpus %d: Block(Block(x)) holds a value Block(x) removed", i)
		}
	}
}
