package rules

import (
	"os"
	"path/filepath"
	"testing"
)

// Review pass 3, item 1: the precommit check covers every planned file,
// including the ones the plan found already up to date.
func TestVerifyCoversUnchangedPlans(t *testing.T) {
	dir := t.TempDir()
	first, err := Plan(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(first); err != nil {
		t.Fatal(err)
	}
	plans, err := Plan(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range plans {
		if !p.Unchanged {
			t.Fatalf("%s not up to date after apply", p.Target)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, ClaudeFile), []byte("edited between propose and apply\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Verify(plans); err == nil {
		t.Fatal("an edit to an up-to-date planned file passed the check")
	}
}
