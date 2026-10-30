package yamlsub

import (
	"reflect"
	"strings"
	"testing"
)

func list(t *testing.T, text string) ([]string, error) {
	t.Helper()
	entries, err := Split(text, "f", true)
	if err != nil {
		return nil, err
	}
	return entries[0].List()
}

// Codex P1-3: a comma inside quotes is part of one item.
func TestFlowListSplitsOutsideQuotesOnly(t *testing.T) {
	got, err := list(t, `protected_paths: ["secret,a", 'b, c', d]`+"\n")
	if err != nil || !reflect.DeepEqual(got, []string{"secret,a", "b, c", "d"}) {
		t.Fatalf("items = %q, %v", got, err)
	}
	if _, err := list(t, `p: ["open, b]`+"\n"); err == nil {
		t.Fatal("unterminated quote accepted")
	}
}

// Codex P1-3: anchors, aliases, tags and flow maps are unsupported syntax,
// never literal paths.
func TestUnsupportedYAMLIsRefused(t *testing.T) {
	for _, text := range []string{
		"p: [&p secrets/]\n", "p: [*p]\n", "p: [!!str secrets/]\n", "p: [{path: secrets/}]\n", "p: [[a]]\n",
		"p:\n  - &p secrets/\n", "p:\n  - *p\n", "p:\n  - !!str x\n", "p:\n  - {path: x}\n",
	} {
		if got, err := list(t, text); err == nil || !strings.Contains(err.Error(), "unsupported YAML syntax") {
			t.Errorf("%q = %q, %v", text, got, err)
		}
	}
	entries, _ := Split("k: &a v\n", "f", true)
	if _, err := entries[0].Scalar(); err == nil {
		t.Error("anchored scalar accepted")
	}
}

// Codex P2-4: a document marker is an error in both modes.
func TestDocumentMarkerIsAnErrorInBothModes(t *testing.T) {
	for _, strict := range []bool{true, false} {
		for _, text := range []string{"a: 1\n---\na: 2\n", "---\na: 1\n", "a: 1\n...\n", "a: 1\n--- # c\n"} {
			if _, err := Split(text, "f", strict); err == nil || !strings.Contains(err.Error(), "document marker") {
				t.Errorf("strict=%t %q: %v", strict, text, err)
			}
		}
	}
}

// Codex P2-6: one escaping rule on both sides.
func TestQuoteUnquoteRoundTrip(t *testing.T) {
	for _, v := range []string{`a"b`, `c\d`, `it's`, `#x`, ` lead`, `-dash`, `a: b`, `[x]`, `&anchor`, "plain/path", `"q"`, `\`} {
		got, err := Unquote(Quote(v))
		if err != nil || got != v {
			t.Errorf("%q -> %s -> %q, %v", v, Quote(v), got, err)
		}
		entries, err := Split("k: "+Quote(v)+"\n", "f", true)
		if err != nil {
			t.Fatalf("%q: %v", v, err)
		}
		if s, err := entries[0].Scalar(); err != nil || s != v {
			t.Errorf("scalar %q -> %q, %v", v, s, err)
		}
	}
	if got, err := Unquote(`'it''s'`); err != nil || got != "it's" {
		t.Fatalf("single quotes = %q, %v", got, err)
	}
	if got, err := Unquote(`"a\"b # not a comment"`); err != nil || got != `a"b # not a comment` {
		t.Fatalf("escaped quote = %q, %v", got, err)
	}
	entries, _ := Split(`k: "a\" # still inside"`+"\n", "f", true)
	if s, err := entries[0].Scalar(); err != nil || s != `a" # still inside` {
		t.Fatalf("comment strip inside an escaped string = %q, %v", s, err)
	}
}
