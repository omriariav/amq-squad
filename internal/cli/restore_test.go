package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/internal/launch"
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

func TestEmitCommandIncludesRoleAndSession(t *testing.T) {
	rec := launch.Record{
		CWD:     "/home/user/proj",
		Binary:  "claude",
		Session: "stream1",
		Handle:  "claude",
		Role:    "qa",
	}
	cmd := emitCommand(rec)
	for _, want := range []string{
		"cd /home/user/proj",
		"amq-squad launch",
		"--role qa",
		"--session stream1",
		"claude",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("emitCommand missing %q in: %s", want, cmd)
		}
	}
	// Handle equals defaultHandleFor(binary), so --me should be omitted.
	if strings.Contains(cmd, "--me") {
		t.Errorf("emitCommand should omit --me when handle == default; got: %s", cmd)
	}
}

func TestEmitCommandIncludesMeWhenHandleDiffers(t *testing.T) {
	rec := launch.Record{
		CWD:     "/p",
		Binary:  "codex",
		Session: "s",
		Handle:  "cpo",
		Role:    "cpo",
	}
	cmd := emitCommand(rec)
	if !strings.Contains(cmd, "--me cpo") {
		t.Errorf("expected --me cpo in: %s", cmd)
	}
}

func TestEmitCommandQuotesArgvWithSpaces(t *testing.T) {
	rec := launch.Record{
		CWD:    "/p",
		Binary: "claude",
		Argv:   []string{"--prompt", "hello world"},
	}
	cmd := emitCommand(rec)
	if !strings.Contains(cmd, "'hello world'") {
		t.Errorf("expected quoted argv in: %s", cmd)
	}
	if !strings.Contains(cmd, " -- ") {
		t.Errorf("expected -- separator before argv in: %s", cmd)
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
