package cli

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/fleet"
	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gatetarget"
)

// Acceptance 3 through the verb: `operator inbox --json` lists the open
// gate-store gates of two squads in one list with their squad.
func TestOperatorInboxGroupsGatesOfTwoSquads(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	prevNow, prevAlive := statusNow, statusPIDAlive
	statusNow, statusPIDAlive = func() time.Time { return now.Add(time.Minute) }, func(int) bool { return true }
	t.Cleanup(func() { statusNow, statusPIDAlive = prevNow, prevAlive })
	file := filepath.Join(dir, "fleet.yaml")
	id := gatestore.Identity{OwnerPubKey: strings.Repeat("a", 64), IssuerKeyID: "ed25519-sha256:" + strings.Repeat("b", 64)}
	for i, sq := range []struct{ name, gate, action, target string }{
		{"app", "g-app-tag", "tag", gatetarget.Command("git tag", "v1.0.0")},
		{"noc", "g-noc-send", "external_send", gatetarget.MCPSend("mcp__slack__send_message", "C1")},
	} {
		root := filepath.Join(dir, sq.name, ".agent-mail", sq.name)
		store, err := gatestore.Open(root)
		if err != nil {
			t.Fatal(err)
		}
		rec, err := gatestore.Render(gatestore.Spec{GateID: sq.gate, Action: sq.action, Target: sq.target, RaisedBy: "lead"}, now.Add(time.Duration(i)*time.Second), id)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Create(rec); err != nil {
			t.Fatal(err)
		}
		if _, _, err := captureOutput(t, func() error {
			return runFleet([]string{"add", "--fleet-file", file, "--name", sq.name, "--root", root,
				"--project-path", filepath.Join(dir, sq.name), "--repository", "owner/" + sq.name, "--lead", "lead"})
		}); err != nil {
			t.Fatal(err)
		}
	}
	stdout, _, err := captureOutput(t, func() error { return runOperator([]string{"inbox", "--fleet-file", file, "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	in := decodeJSONEnvelope[fleet.Inbox](t, stdout).Data
	if len(in.Gates) != 2 {
		t.Fatalf("inbox gates = %+v, want two", in.Gates)
	}
	got := map[string]string{}
	for _, g := range in.Gates {
		got[g.Squad] = g.Gate.ID
		if g.Gate.State != "open" {
			t.Fatalf("gate %+v is not open", g)
		}
	}
	if got["app"] != "g-app-tag" || got["noc"] != "g-noc-send" {
		t.Fatalf("gates not listed with their squad: %v", got)
	}
	text, _, err := captureOutput(t, func() error { return runOperator([]string{"inbox", "--fleet-file", file}) })
	if err != nil || !strings.Contains(text, "g-app-tag") || !strings.Contains(text, "noc") {
		t.Fatalf("human inbox (%v):\n%s", err, text)
	}
}
