package claudeseat

import "testing"

// 3.3.1 item 5, D5 (c): a launch that records no absolute worktree has no
// tree to check, so the check refuses rather than read paths relative to
// seat-exec's own directory.
func TestProjectMCPReasonsRefusesWithoutAnAbsoluteWorktree(t *testing.T) {
	for _, wt := range []string{"", "wt", "./wt"} {
		if r := ProjectMCPReasons(wt); len(r) == 0 {
			t.Errorf("worktree %q: no reason", wt)
		}
	}
	if r := ProjectMCPReasons(t.TempDir()); len(r) != 0 {
		t.Errorf("an empty worktree: %v", r)
	}
}
