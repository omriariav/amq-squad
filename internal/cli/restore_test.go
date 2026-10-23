package cli

import (
	"reflect"
	"strings"
	"testing"
)

func TestShellQuoteSafeString(t *testing.T) {
	cases := map[string]string{
		"claude":          "claude",
		"/usr/bin/amq":    "/usr/bin/amq",
		"stream1":         "stream1",
		"role_123":        "role_123",
		"with space":      "'with space'",
		"with'apostrophe": `'with'\''apostrophe'`,
		"":                "''",
		"a;b":             "'a;b'",
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDefaultHandleFromPath(t *testing.T) {
	if got := defaultHandleFor("/usr/local/bin/Claude"); got != "claude" {
		t.Errorf("defaultHandleFor lower-cases basename, got %q", got)
	}
	if got := defaultHandleFor("codex"); got != "codex" {
		t.Errorf("defaultHandleFor plain = %q", got)
	}
}

// joinedAgentArgs writes a value that parseAgentArgs must re-tokenize
// identically; pin the round-trip for spaces, single quotes, double quotes,
// and backslash escapes.
func TestBinaryArgsJoinedRoundTripPreservesQuotingAndEscapes(t *testing.T) {
	cases := [][]string{
		{"--enable", "goals"},
		{"--prompt", "hello world"},
		{"--label", "it's fine"},
		{`--regex`, `a\nb`},
		{"--quoted", `say "hi"`},
		{"--empty", ""},
	}
	for _, args := range cases {
		quoted := make([]string, 0, len(args))
		for _, arg := range args {
			quoted = append(quoted, shellQuote(arg))
		}
		joined := strings.Join(quoted, " ")
		got, err := parseAgentArgs(joined)
		if err != nil {
			t.Fatalf("parseAgentArgs(%q) error: %v", joined, err)
		}
		if !reflect.DeepEqual(got, args) {
			t.Errorf("round-trip %q -> %q -> %v, want %v", args, joined, got, args)
		}
	}
}
