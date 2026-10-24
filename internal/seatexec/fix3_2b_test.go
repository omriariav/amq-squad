//go:build unix

package seatexec

import (
	"path/filepath"
	"testing"
)

// 3-2 fix 2, P1-5: the variables that run git's transport or decide whom it
// trusts are scrubbed even when declared, and env_needs naming any of them is
// refused.
func TestGitTransportAndTrustVariablesNeverReachASeat(t *testing.T) {
	names := []string{"GIT_SSH_COMMAND", "GIT_SSH", "GIT_PROXY_COMMAND", "GIT_EXEC_PATH", "GIT_TEMPLATE_DIR", "CURL_CA_BUNDLE", "SSL_CERT_FILE"}
	var parent []string
	for _, n := range names {
		parent = append(parent, n+"=/x")
	}
	if env, _ := SeatEnv(parent, nil, names, nil); len(env) != 0 { // declared as credential needs: still scrubbed
		t.Fatalf("seat env carries %v", env)
	}
	for _, n := range names {
		f := newFixture(t, "claude")
		f.rec.EnvNeeds = []string{n}
		f.stage()
		if err := f.run(Options{Argv: []string{filepath.Join(f.bin, "claude")}}); refusalCode(err) != "env_needs_invalid" || f.execPath != "" {
			t.Fatalf("%s in env_needs ran: %v", n, err)
		}
	}
}
