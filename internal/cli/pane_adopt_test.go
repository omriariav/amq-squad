package cli

import (
	"os"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

// #95: adoptLivePane must resolve an externally-launched agent's live pane, and
// PID lineage must disambiguate peers that share cwd+engine.
func TestAdoptLivePane(t *testing.T) {
	panes := []tmuxpane.TmuxPane{
		{Session: "main", Pane: "1", PID: 100, Command: "codex", CWD: "/repo", PaneID: "%5", WindowID: "@1", WindowName: "w0"},
		{Session: "main", Pane: "2", PID: 200, Command: "codex", CWD: "/repo", PaneID: "%6", WindowID: "@1", WindowName: "w0"},
	}
	// agent 62195 runs under pane %5's shell (pid 100); 62277 under %6 (pid 200).
	tree := func(pid int) []int {
		return map[int][]int{100: {62195}, 200: {62277}}[pid]
	}

	if got := adoptLivePane("cto", "cto", "codex", "/repo", "main", 62277, panes, tree); got == nil || got.PaneID != "%6" {
		t.Fatalf("agent 62277 lives under %%6; got %+v", got)
	}
	if got := adoptLivePane("cto", "cto", "codex", "/repo", "main", 62195, panes, tree); got == nil || got.PaneID != "%5" {
		t.Fatalf("agent 62195 lives under %%5; got %+v", got)
	}
	// Adopted identity carries the pane's session/window/pane so focus/send/attach work.
	got := adoptLivePane("cto", "cto", "codex", "/repo", "main", 62195, panes, tree)
	if got.Session != "main" || got.WindowID != "@1" || got.PaneID != "%5" {
		t.Errorf("adopted identity incomplete: %+v", got)
	}
	// No PID-lineage match (pid 999 is in no pane's tree) AND no cwd match: the
	// heuristic gate applies and rejects. A PID match would bypass cwd since it
	// is definitive (exercised above).
	if got := adoptLivePane("cto", "cto", "codex", "/elsewhere", "main", 999, panes, tree); got != nil {
		t.Errorf("no pid match + no cwd match must yield nil, got %+v", got)
	}
	// No panes at all -> nil.
	if got := adoptLivePane("cto", "cto", "codex", "/repo", "main", 62195, nil, tree); got != nil {
		t.Errorf("no panes must yield nil, got %+v", got)
	}
}

// #95 regression: claude/codex rename their process, so a pane's foreground
// command may not match the engine name (e.g. "2.1.169"). A definitive PID
// lineage must adopt the pane anyway; without it, the engine gate rejects.
func TestAdoptLivePaneByPidWhenCommandMismatches(t *testing.T) {
	panes := []tmuxpane.TmuxPane{
		{Session: "main", Pane: "1", PID: 61773, Command: "2.1.169", CWD: "/repo", PaneID: "%145", WindowID: "@2"},
	}
	tree := func(pid int) []int {
		if pid == 61773 {
			return []int{62277}
		}
		return nil
	}
	if got := adoptLivePane("fullstack", "fullstack", "claude", "/repo", "main", 62277, panes, tree); got == nil || got.PaneID != "%145" {
		t.Fatalf("PID lineage must adopt despite a mismatched foreground command; got %+v", got)
	}
	if got := adoptLivePane("fullstack", "fullstack", "claude", "/repo", "main", 62277, panes, nil); got != nil {
		t.Errorf("without PID lineage, a mismatched command must NOT adopt; got %+v", got)
	}
}

// #95 review: PID-lineage adoption bypasses cwd+engine, so the anchoring pid
// MUST be verified live + correct-binary; a stale/reused or wrong-binary pid is
// rejected (returns 0 -> no lineage anchor).
func TestVerifiedAgentPID(t *testing.T) {
	if verifiedAgentPID(0, "codex") != 0 || verifiedAgentPID(-5, "codex") != 0 {
		t.Error("non-positive pid must be 0")
	}
	if verifiedAgentPID(os.Getpid(), "") != 0 {
		t.Error("empty binary must be 0")
	}
	// This live process is not running a binary named like this -> rejected.
	if verifiedAgentPID(os.Getpid(), "definitely-not-this-binary-xyz") != 0 {
		t.Error("a live pid running a DIFFERENT binary must be rejected (reuse guard)")
	}
	// A (very likely) dead pid -> rejected.
	if verifiedAgentPID(2147483646, "codex") != 0 {
		t.Error("a dead pid must be rejected")
	}
}
