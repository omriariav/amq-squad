package launch

import "testing"

func TestTerminalInfoFromTmux(t *testing.T) {
	got := TerminalInfoFromTmux(&TmuxInfo{
		Session:    "main",
		WindowID:   "@1",
		WindowName: "lead",
		PaneID:     "%5",
		Target:     "external",
	})
	if got == nil {
		t.Fatalf("terminal info should be present")
	}
	if got.Backend != "tmux" || got.Session != "main" || got.WindowID != "@1" || got.WindowName != "lead" || got.PaneID != "%5" || got.Target != "external" {
		t.Fatalf("terminal info = %+v", got)
	}
}

func TestTerminalInfoFromEmptyTmuxIsNil(t *testing.T) {
	if got := TerminalInfoFromTmux(&TmuxInfo{}); got != nil {
		t.Fatalf("empty tmux identity should map to nil, got %+v", got)
	}
	if got := TerminalInfoFromTmux(nil); got != nil {
		t.Fatalf("nil tmux identity should map to nil, got %+v", got)
	}
}
