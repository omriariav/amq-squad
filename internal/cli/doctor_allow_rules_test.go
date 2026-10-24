package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// doctor warns when the repository's Claude settings files carry permission
// allow rules (they still merge into a seat's config; seat-exec refuses
// Claude seats there), names each rule, and is OK without them.
func TestDoctorWarnsOnRepositoryClaudeAllowRules(t *testing.T) {
	proj := t.TempDir()
	if c := claudeAllowRulesCheck(proj); c.Status != doctorOK {
		t.Fatalf("no rules: %+v", c)
	}
	if err := os.MkdirAll(filepath.Join(proj, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(proj, ".claude", "settings.local.json"), `{"permissions":{"allow":["Bash(git:*)","WebFetch"]}}`)
	c := claudeAllowRulesCheck(proj)
	if c.Status != doctorWarn || !strings.Contains(c.Detail, "Bash(git:*)") || !strings.Contains(c.Detail, "WebFetch") || !strings.Contains(c.Detail, "claude_allow_rules_present") {
		t.Fatalf("with rules: %+v", c)
	}
}
