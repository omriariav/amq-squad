//go:build unix

package cli

import (
	"os/exec"
	"testing"
)

// 3.3.1 (Codex pass 5 on the lead's staging, test strength): the lead's
// staging is taken back unless its pi ran, and the evidence is the real
// runProcess's started hook: it fires only after the child started, never
// for a spawn that failed.
func TestLeadStageTheStartedHookFiresOnlyForAStartedChild(t *testing.T) {
	prev := piStartedHook
	t.Cleanup(func() { piStartedHook = prev })
	var pids []int
	piStartedHook = func(pid int) { pids = append(pids, pid) }
	if code, err := runProcess("/nonexistent/pi", []string{"pi"}, nil); err == nil || code != -1 || len(pids) != 0 {
		t.Errorf("a failed spawn: code %d err %v, hook pids %v", code, err, pids)
	}
	truePath, err := exec.LookPath("true")
	if err != nil {
		t.Skip("true is not on PATH")
	}
	if code, err := runProcess(truePath, []string{"true"}, nil); err != nil || code != 0 || len(pids) != 1 || pids[0] <= 0 {
		t.Errorf("a started child: code %d err %v, hook pids %v", code, err, pids)
	}
}
