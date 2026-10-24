//go:build unix

package seatexec

import (
	"strings"
	"testing"
)

// Lead ruling at the 2b-2 end checkpoint: a launcher environment carrying a
// host agent session's variables (here the orchestrating Claude Code and
// Codex companion session of the real run) yields a seat environment
// without them; ordinary variables stay.
func TestSeatExecScrubsHostAgentSession(t *testing.T) {
	host := []string{
		"CLAUDECODE=1", "AI_AGENT=claude-code", "CLAUDE_CODE_SESSION_ID=6dd4", "CLAUDE_CODE_ENTRYPOINT=cli",
		"CLAUDE_CODE_MESSAGING_SOCKET=/tmp/s", "CLAUDE_PID=42", "CLAUDE_EFFORT=high", "CLAUDE_JOB_DIR=/tmp/j",
		"CLAUDE_PLUGIN_DATA=/p", "CODEX_COMPANION_SESSION_ID=6dd4", "CODEX_COMPANION_TRANSCRIPT_PATH=/t.jsonl",
		"CODEX_THREAD_ID=01a1", "CODEX_SANDBOX=seatbelt",
	}
	f := newFixture(t, "claude")
	f.stage()
	if err := f.run(Options{Env: f.env(host...)}); err != nil {
		t.Fatal(err)
	}
	for _, kv := range host {
		name, _, _ := strings.Cut(kv, "=")
		if Getenv(f.execEnv, name) != "" || Getenv(f.drainEnv, name) != "" {
			t.Errorf("%s reached the seat", name)
		}
	}
	if Getenv(f.execEnv, "LANG") != "C" || Getenv(f.execEnv, "AM_ME") != "cto" {
		t.Fatal("ordinary and identity variables must stay")
	}
	for _, keep := range []string{"CLAUDE_PULSE_DENSITY", "CODEX_HOME", "TERM_PROGRAM"} {
		if Scrubbed(keep) {
			t.Errorf("%s is not a host session variable", keep)
		}
	}
}
