//go:build unix

package seatexec

import (
	"path/filepath"
	"strings"
	"testing"
)

// 3-2 fix 1, P1-5: the GIT_CONFIG_* family and the gh token names never
// reach a seat, declared or not, and a launch record that declares one in
// env_needs is refused before anything is published or exec'd.
func TestGitConfigFamilyAndGHTokensNeverReachASeat(t *testing.T) {
	parent := []string{"PATH=/usr/bin", "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.extraHeader", "GIT_CONFIG_VALUE_0=Authorization: Bearer x",
		"GIT_CONFIG_PARAMETERS='http.extraheader=Authorization: Bearer y'", "GH_TOKEN=t", "GITHUB_TOKEN=t", "GH_ENTERPRISE_TOKEN=t", "GH_CONFIG_DIR=/x"}
	env, _ := SeatEnv(parent, nil, []string{"GIT_CONFIG_COUNT", "GH_TOKEN"}, nil) // declared as credential needs: still scrubbed
	for _, e := range env {
		if strings.HasPrefix(e, "GIT_CONFIG_") || strings.HasPrefix(e, "GH_") || strings.HasPrefix(e, "GITHUB_") {
			t.Fatalf("seat env carries %s", e)
		}
	}
	f := newFixture(t, "claude")
	f.rec.EnvNeeds = []string{"GIT_CONFIG_PARAMETERS"}
	f.stage()
	err := f.run(Options{Argv: []string{filepath.Join(f.bin, "claude")}})
	if refusalCode(err) != "env_needs_invalid" || f.execPath != "" {
		t.Fatalf("a record declaring GIT_CONFIG_PARAMETERS ran: %v (exec %q)", err, f.execPath)
	}
}
