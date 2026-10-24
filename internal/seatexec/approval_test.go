//go:build unix

package seatexec

import "testing"

// 3-2 (plan 7.3): a Codex seat launches only with --ask-for-approval
// on-request beside the sandbox; a missing, other or overridden approval
// policy is refused.
func TestCodexSeatRequiresApprovalOnRequest(t *testing.T) {
	for _, argv := range [][]string{
		{"--sandbox", "workspace-write"},
		{"--sandbox", "workspace-write", "--ask-for-approval", "never"},
		{"--sandbox", "workspace-write", "-a", "untrusted"},
		{"--sandbox", "workspace-write", "--ask-for-approval=on-failure"},
		{"--sandbox", "workspace-write", "--ask-for-approval", "on-request", "-c", "approval_policy=never"},
		{"--sandbox", "workspace-write", "--ask-for-approval", "on-request", "--config=approval_policy=never"},
	} {
		if err := CheckCodexSandbox(append([]string{"codex"}, argv...)); refusalCode(err) != "codex_approval_missing" {
			t.Errorf("codex %q: %v", argv, err)
		}
	}
	if err := CheckCodexSandbox([]string{"codex", "--sandbox", "workspace-write", "--ask-for-approval", "on-request"}); err != nil {
		t.Fatalf("the launch default refused: %v", err)
	}
	f := newFixture(t, "codex")
	f.stage()
	if err := f.run(Options{Argv: []string{f.bin + "/codex", "--sandbox", "workspace-write"}}); refusalCode(err) != "codex_approval_missing" || f.execPath != "" {
		t.Fatalf("seat-exec ran a Codex seat without the approval policy: %v (exec %q)", err, f.execPath)
	}
}
