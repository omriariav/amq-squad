package cli

import "testing"

func TestTerminalInfoFromEnvITerm2(t *testing.T) {
	t.Setenv(envTerminalBackend, "iterm2")
	t.Setenv(envTerminalSession, "issue-331")
	t.Setenv(envTerminalWindowID, "101")
	t.Setenv(envTerminalWindowName, "amq:issue-331:cto")
	t.Setenv(envTerminalTabID, "tab-1")
	t.Setenv(envTerminalSessionID, "session-1")
	t.Setenv(envTerminalTarget, "new-window")

	info := terminalInfoFromEnv()
	if info == nil {
		t.Fatal("expected terminal info from env")
	}
	if info.Backend != "iterm2" || info.Session != "issue-331" || info.WindowID != "101" || info.WindowName != "amq:issue-331:cto" || info.TabID != "tab-1" || info.SessionID != "session-1" || info.Target != "new-window" {
		t.Fatalf("terminal info = %+v", info)
	}
}

func TestTerminalInfoFromEmptyEnvIsNil(t *testing.T) {
	if info := terminalInfoFromEnv(); info != nil {
		t.Fatalf("empty env should not create terminal info: %+v", info)
	}
}
