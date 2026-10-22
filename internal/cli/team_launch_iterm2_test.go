package cli

import (
	"strings"
	"testing"
)

func TestITerm2FocusArgvShape(t *testing.T) {
	argv := iterm2FocusArgv("101")
	if len(argv) != 3 || argv[0] != "-e" || argv[2] != "101" {
		t.Fatalf("focus argv = %#v", argv)
	}
	for _, want := range []string{`tell application "iTerm2"`, `targetID`, `tell w to select`, `return "OK"`, `return "MISS"`} {
		if !strings.Contains(argv[1], want) {
			t.Fatalf("focus script missing %q:\n%s", want, argv[1])
		}
	}
}
