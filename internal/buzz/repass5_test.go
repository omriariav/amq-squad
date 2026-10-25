package buzz

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Phase 5 re-pass of 4-5, HIGH, the script's row: enroll.sh applies the
// parsed relay rule before anything reaches amq-remote: userinfo and a
// look-alike loopback host are refused (exit 2, "relay url must be"), a
// wss:// or an exact loopback ws:// URL passes the rule (the run then stops
// at the owner npub check, against a stand-in that refuses every npub).
func TestEnrollScriptRelayRule(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the owner script is bash")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	script, _ := filepath.Abs("../../docs/v3-buzz/enroll.sh")
	home := realTempDir(t)
	run := func(relay string) (int, string) {
		cmd := exec.Command("bash", script, "npub1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq", relay)
		cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "AMQ_SQUAD=/usr/bin/false"}
		var errOut bytes.Buffer
		cmd.Stderr = &errOut
		err := cmd.Run()
		rc := 0
		if ee, ok := err.(*exec.ExitError); ok {
			rc = ee.ExitCode()
		}
		return rc, errOut.String()
	}
	for _, bad := range []string{"ws://localhost.attacker.example/", "ws://127.0.0.1@relay.attacker.example/", "wss://user@relay.example/", "ws://relay.example/", "ws://[::1].evil/"} {
		if rc, msg := run(bad); rc != 2 || !strings.Contains(msg, "relay url must be") {
			t.Errorf("%s: rc %d, %q", bad, rc, msg)
		}
	}
	for _, good := range []string{"wss://relay.example/", "ws://127.0.0.1:7777/", "ws://localhost/", "ws://[::1]:9/"} {
		if rc, msg := run(good); strings.Contains(msg, "relay url must be") || !strings.Contains(msg, "bad owner npub") {
			t.Errorf("%s did not pass the relay rule: rc %d, %q", good, rc, msg)
		}
	}
}
