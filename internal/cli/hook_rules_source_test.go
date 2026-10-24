package cli

import (
	"path/filepath"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/launch"
)

// 2b-2 fix 3: the seat reads a rules snapshot (AMQ_SQUAD_RULES_PATH), and
// guard protects the recorded rules source as well as the snapshot, so a
// custom rules source keeps its protection.
func TestGuardProtectsTheRecordedRulesSource(t *testing.T) {
	f := newHookFixture(t)
	source := filepath.Join(f.project, "custom", "squad-rules.md")
	snapshot := filepath.Join(launch.ExtensionDir(f.agentDir), "rules.md")
	mustWrite(t, source, "# rules\n")
	mustWrite(t, snapshot, "# rules\n")
	if err := launch.WriteUnderRecordLock(f.agentDir, launch.Record{Handle: "cto", Root: f.root, Session: "s1", RulesPath: source}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AMQ_SQUAD_RULES_PATH", snapshot)
	for _, p := range []string{source, snapshot} {
		if v := evaluateGuard(f.id(), guardCall{Tool: "Write", Args: map[string]string{"file_path": p}, CWD: f.repo}, f.now); v.ReasonCode != "protected_path" {
			t.Errorf("write %s: %s %s", p, v.Verdict, v.ReasonCode)
		}
	}
}
