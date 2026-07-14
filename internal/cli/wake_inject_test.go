package cli

import "testing"

func TestResolveWakeInjectModeForBinary(t *testing.T) {
	tests := []struct {
		mode, binary, want string
	}{
		{"", "codex", "raw"},
		{"auto", "/opt/bin/codex", "raw"},
		{"", "CLAUDE", "raw"},
		{"auto", "claude", "raw"},
		{"none", "codex", "none"},
		{"raw", "claude", "raw"},
		{"paste", "codex", "paste"},
		{"", "custom-agent", ""},
		{"auto", "custom-agent", "auto"},
	}
	for _, test := range tests {
		if got := resolveWakeInjectModeForBinary(test.mode, test.binary); got != test.want {
			t.Errorf("resolveWakeInjectModeForBinary(%q, %q) = %q, want %q", test.mode, test.binary, got, test.want)
		}
	}
}
