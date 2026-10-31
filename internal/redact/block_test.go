package redact

import (
	"runtime"
	"strings"
	"testing"
)

// longLine is n bytes (rounded down to whole pairs) of repeated name=value
// assignments, built at run time: a credential-named key with a one-letter
// value, never a credential-shaped literal.
func longLine(n int) string { return strings.Repeat("password"+"=x ", n/11) }

// v3-5-verifier, item 4 of the third round; 3.1 plan task t2: redaction is
// linear in a line, however many values it holds. The row measures work, not
// time (wall-clock ratios flaked on shared CI runners, twice on 2026-10-31).
// Two deterministic measures: the units of work Block reports through
// workHook (bytes scanned, matches and entries handled), and the bytes it
// allocates (runtime.MemStats.TotalAlloc). The 1 MiB line holds 16x the
// 64 KiB line's values, so each measure must grow by no more than 24x; a
// quadratic Block grows by about 256x.
// Real counts (2026-10-31), 64 KiB to 1 MiB: assign line 16.0x work, 20.9x
// bytes; mixed line (YAML flow, JSON, provider token) 16.5x, 21.7x; mixed
// block (YAML blocks, a quoted scalar across lines, JSON) 16.0x, 16.4x. The
// limits are 24x and 40x. Mutations that fail it: a per-match string copy
// (236.6x bytes only; work stays 16.0x), a prefix rescan reported to the hook
// (255.8x work only; bytes 20.9x), and an insertion sort that finds its place
// from the head (253.2x work only; bytes 21.0x), so the two measures catch
// different shapes.
// Limit of the row: a quadratic loop that neither calls the work hook nor
// allocates (a strings.Index rescan, say) slips through; the shapes it
// catches are a quadratic copy and a regex rescan.
func TestBlockIsLinearOnALongLine(t *testing.T) {
	const limit = 24
	const allocLimit = 40 // builder and slice growth leave bytes a little over linear
	// Canaries are built at run time and match no RealCredentialShapes.
	c1, c2, c3, c4, c5 := "Ab9"+"xY7q1", "Ab9"+"xY7q2", "Ab9"+"xY7q3", "Ab9"+"xY7q4", "Ab9"+"xY7q5"
	fixtures := []struct {
		name     string
		build    func(n int) string
		canaries []string
	}{
		{"assign", longLine, []string{"=x "}},
		// One long line: YAML flow, JSON and provider-token paths.
		{"mixed line", func(n int) string {
			unit := "password: '" + c1 + "', {\"password\": \"" + c2 + "\"} token sk-" + c3 + "zz "
			return strings.Repeat(unit, n/len(unit))
		}, []string{c1, c2, c3}},
		// Many lines: YAML blocks, a quoted scalar across lines, JSON.
		{"mixed block", func(n int) string {
			unit := "password:\n  '" + c4 + "\n  " + c5 + "'\nnext: kept\n{\"password\":\n \"" + c2 + "\"}\npassword:\n  " + c1 + "\n"
			return strings.Repeat(unit, n/len(unit))
		}, []string{c4, c5, c2, c1}},
	}
	measure := func(line string) (units int, alloc uint64, out string) {
		workHook = func(n int) { units += n }
		defer func() { workHook = nil }()
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		out = Block(line)
		runtime.ReadMemStats(&after)
		return units, after.TotalAlloc - before.TotalAlloc, out
	}
	for _, f := range fixtures {
		small, big := f.build(1<<16), f.build(1<<20)
		measure(small) // warm-up: one-time allocations stay out of the sizes
		su, sa, sout := measure(small)
		bu, ba, bout := measure(big)
		for _, o := range []string{sout, bout} {
			for _, c := range f.canaries {
				if strings.Contains(o, c) {
					t.Fatalf("%s: a value of the long input was not redacted (%q survived)", f.name, c)
				}
			}
		}
		if su == 0 || sa == 0 {
			t.Fatalf("%s: nothing measured (units %d, bytes %d): the hook or the counter is dead", f.name, su, sa)
		}
		uRatio, aRatio := float64(bu)/float64(su), float64(ba)/float64(sa)
		t.Logf("%s: 64 KiB %d units, %d bytes; 1 MiB %d units, %d bytes; ratios %.1fx units, %.1fx bytes (limits %dx units, %dx bytes)",
			f.name, su, sa, bu, ba, uRatio, aRatio, limit, allocLimit)
		if uRatio > limit {
			t.Fatalf("%s: 16x the input did %.1fx the work (over %dx): Block is not linear in a line", f.name, uRatio, limit)
		}
		if aRatio > allocLimit {
			t.Fatalf("%s: 16x the input allocated %.1fx the bytes (over %dx): Block is not linear in a line", f.name, aRatio, allocLimit)
		}
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
