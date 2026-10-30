//go:build unix

package seatexec

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
	"github.com/omriariav/amq-squad/v3/internal/launch"
)

// 3.0.1 t6 fix round, reviewer condition (b): a seat's config dir is never
// exec'd twice. After the first exec, Claude Code may write its own state
// into the dir (a defaults-only entry for the canonical repository root,
// userID); the launcher has marked the proof token consumed. A second
// seat-exec on that launch is refused before exec: Check's allowlist sees
// what the harness did not seed, and the consumed token is refused even on
// a dir that still holds only the seed. A new launch gets a new dir
// (claudeseat's TestEveryLaunchIsBornFresh).
func TestASecondExecOnAUsedConfigDirNeverExecs(t *testing.T) {
	for name, tc := range map[string]struct {
		runtime string // what Claude Code wrote after the first exec ("" for none)
		code    string
	}{
		"Claude Code's own state in the dir": {`{"hasCompletedOnboarding":true,"userID":"u","numStartups":1,"projects":{"WT":{"hasTrustDialogAccepted":true,"lastSessionId":"s"},"/repo":{"allowedTools":[],"mcpServers":{},"hasTrustDialogAccepted":false}}}`, ClaudeConfigUnexpected},
		"the seed only, token consumed":      {"", "proof_ticket_consumed"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, "claude")
			f.stage()
			if err := f.run(Options{}); err != nil || f.execPath == "" {
				t.Fatalf("first exec: %v", err)
			}
			dir := claudeseat.Dir(f.root, "cto", f.rec.LaunchID)
			if tc.runtime != "" {
				body := []byte(tc.runtime)
				body = []byte(strings.ReplaceAll(string(body), "WT", f.wt))
				write(t, filepath.Join(dir, ".claude.json"), string(body), 0o600)
				if d, _ := claudeseat.Digest(dir); d != f.rec.ClaudeConfig {
					t.Fatal("Claude Code's own state moved the digest")
				}
			}
			f.rec.Proof.Token = launch.TokenConsumed // what the launcher marks on the accepted proof
			f.stage()
			f.execPath = ""
			if err := f.run(Options{}); refusalCode(err) != tc.code || f.execPath != "" {
				t.Fatalf("second exec: want %s and no exec, got %v (exec %q)", tc.code, err, f.execPath)
			}
		})
	}
}
