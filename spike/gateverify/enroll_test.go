package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEnrollPolicyRetry drives the real enroll.sh with the real amq,
// amq-remote and amq-owner-sign binaries and a THROWAWAY owner key (dry-run
// pty path), against the local fake relay. It checks three things:
//
//  1. A first run whose policy publication fails (unreachable relay) still
//     enrolls the bundle, and records no publication.
//  2. A resumed run retries the publication instead of skipping signing,
//     and the policy reaches the relay, signed by the owner, for the body.
//  3. A run after a recorded publication does not sign again.
//
// The test is skipped when the binaries are not installed (-short also
// skips it). Each run stops at phase D (exit 11: no DM channel), so no
// endpoint is started.
func TestEnrollPolicyRetry(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	for _, bin := range []string{"amq", "amq-remote", "amq-owner-sign", "script", "python3"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	home := realTempDir(t)
	gv := filepath.Join(home, "gateverify")
	if out, err := exec.Command("go", "build", "-o", gv, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	owner := mustKey(t)
	nsec := filepath.Join(home, "owner-throwaway.secret")
	if err := os.WriteFile(nsec, []byte(owner.Hex()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	npub, _ := EncodeNpub(owner.PublicHex())
	script, _ := filepath.Abs("enroll.sh")
	enroll := func(relay string) (int, string) {
		cmd := exec.Command(script, npub, relay)
		cmd.Env = append(os.Environ(), "S1_HOME="+home, "GV="+gv, "S1_DRYRUN_NSEC_FILE="+nsec,
			"S1_DRYRUN_HOLD=6", "S1_NO_CLIPBOARD=1", "S1_DM_CHANNEL=", "S1_OWNER_KEY_CMD=", "S1_SKIP_POLICY=")
		out, err := cmd.CombinedOutput()
		rc := 0
		if ee, ok := err.(*exec.ExitError); ok {
			rc = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return rc, string(out)
	}
	marker := filepath.Join(home, "policy.published")

	// 1. Unreachable relay: bundle written and enrolled, publication not
	// recorded.
	rc, out := enroll("ws://127.0.0.1:1")
	if rc != 11 || !strings.Contains(out, "policy was not published") {
		t.Fatalf("run 1: rc %d\n%s", rc, redact(out, owner.Hex()))
	}
	if _, err := os.Stat(filepath.Join(home, "bundle.json")); err != nil {
		t.Fatal("run 1 wrote no bundle")
	}
	if _, err := os.Stat(filepath.Join(home, "root", "extensions", "remote", "keys", "s1gate", "share.json")); err != nil {
		t.Fatal("run 1 did not enroll the bundle")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("run 1 recorded a publication that failed")
	}

	// 2. Resume against a reachable relay: the publication is retried.
	relay := &fakeRelay{members: []string{owner.PublicHex()}} // plain NIP-42, owner only
	srv := relay.serve(t)
	defer srv.Close()
	rc, out = enroll(relay.url)
	if rc != 11 || !strings.Contains(out, "retrying the kind 30177 policy publication") || !strings.Contains(out, "published kind 30177 policy") {
		t.Fatalf("run 2: rc %d\n%s", rc, redact(out, owner.Hex()))
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("run 2 did not record the publication")
	}
	body := strings.TrimSpace(strings.SplitN(strings.SplitN(out, "body:  ", 2)[1], "\n", 2)[0])
	found := false
	relay.mu.Lock()
	for _, raw := range relay.stored {
		e, err := ParseEvent(raw)
		if err == nil && e.Kind == 30177 && e.PubKey == owner.PublicHex() && len(tagValues(e, "d")) == 1 && tagValues(e, "d")[0] == body && e.VerifySignature() == nil {
			found = true
		}
	}
	relay.mu.Unlock()
	if !found {
		t.Fatalf("no owner-signed kind 30177 policy for body %s on the relay", body)
	}

	// 3. Published: a further run does not sign again.
	start := time.Now()
	rc, out = enroll(relay.url)
	if rc != 11 || strings.Contains(out, "== B:") {
		t.Fatalf("run 3: rc %d\n%s", rc, redact(out, owner.Hex()))
	}
	if time.Since(start) > 30*time.Second {
		t.Fatal("run 3 took long enough to have signed")
	}
	// The throwaway secret never reached a log.
	if log, _ := os.ReadFile(filepath.Join(home, "owner-sign.log")); strings.Contains(string(log), owner.Hex()) {
		t.Fatal("owner secret in owner-sign.log")
	}
}

func redact(s, secret string) string { return strings.ReplaceAll(s, secret, "<redacted>") }
