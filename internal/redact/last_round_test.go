package redact

import (
	"strings"
	"testing"
)

// The last round of v3-5-verifier, item 1: a comment starts at a # after any
// blank: a tab before the comment leaves the value a single token, and it is
// redacted.
func TestYAMLCommentAfterATab(t *testing.T) {
	value := "Ab9" + "xY7qR"
	if out := Block("password: " + value + "\t# the comment"); strings.Contains(out, value) || !strings.Contains(out, "# the comment") {
		t.Fatal("a value before a tab and a comment survived, or the comment went")
	}
}

// Item 2: a quoted scalar opened on the key's line and continued on the next
// is redacted through its closing quote, in both quote kinds; the next key
// stays.
func TestInlineQuotedScalarContinued(t *testing.T) {
	head, tail := "Ab9"+"x", "Y7"+"qR"
	for _, q := range []string{"'", `"`} {
		out := Block("password: " + q + head + "\n  " + tail + q + "\nnext: kept\n")
		if strings.Contains(out, head) || strings.Contains(out, tail) || !strings.Contains(out, "next: kept") {
			t.Fatalf("an inline %s-quoted scalar continued: the value survived or the next key went", q)
		}
	}
}

// Item 3: a call expression is exempt only when its arguments carry no run
// of 8 or more letters and digits.
func TestCallExemptionNeedsNoLongRun(t *testing.T) {
	value := "Ab9" + "xY7qRLm2W"
	if out := Block("password: evil(" + value + ")"); strings.Contains(out, value) {
		t.Fatal("a value inside a call's parentheses survived")
	}
	if out := Block("credential: credentialSource(rt)"); !strings.Contains(out, "credentialSource(rt)") {
		t.Fatal("a call with short arguments was redacted")
	}
}
