package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
)

// Amendment 28 c: a gate raised before the squad identity keeps its id; a
// raise of the same id names the upgrade note.
func TestGateRaiseNamesTheUpgradeNote(t *testing.T) {
	f := newGateCLIFixture(t)
	store, _ := gatestore.Open(f.root)
	core, err := gateCore(context.Background(), contextResolution{Root: f.root, ProjectDir: f.project, Session: "s", Handle: "cto"}, false)
	if err != nil {
		t.Fatal(err)
	}
	old, err := gatestore.Render(gatestore.Spec{GateID: "rel-old", Action: "tag", Target: "git tag v2.9.0", RaisedBy: "cto"}, time.Now(), core.Identity())
	if err != nil || old.Squad != "" {
		t.Fatalf("pre-upgrade record: %+v %v", old, err)
	}
	if err := store.Create(old); err != nil {
		t.Fatal(err)
	}
	_, _, err = captureOutput(t, func() error {
		return runGate([]string{"raise", "--project", f.project, "--session", "s", "--me", "cto", "--gate", "rel-old", "--action", "tag", "--target", "git tag v2.9.0"})
	})
	if err == nil || !strings.Contains(err.Error(), GateUpgradeNote) {
		t.Fatalf("raise over a pre-upgrade gate: %v", err)
	}
}

// Medium 4: a refresh that fails does not keep the previous routing table:
// with the registry corrupt, nothing is routed onward.
func TestRemoteRefreshFailureDropsTheRoutingTable(t *testing.T) {
	f, file, _, ep, mailer := operatorFixture(t)
	_, stop := attachAsync(t, []string{"--fleet-file", file, "--project", f.project, "--session", "s", "--me", "cto", "--interval", "50ms", "--json"})
	eventually(t, "the endpoint to start", func() bool { _, starts, _ := ep.counts(); return starts == 1 })
	if err := os.WriteFile(file, []byte("schema: [not yaml\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond) // refreshes have failed
	f.answer(t, "@app/cto rebase", "")
	eventually(t, "the mailbox copy", func() bool { return mailer.to("user") })
	time.Sleep(100 * time.Millisecond)
	stop()
	if mailer.to("cto") {
		t.Fatal("a corrupt registry kept the previous routing table")
	}
}

// New defect: one squad whose lead has no launch record is left out with a
// problem; the others attach. A fleet with no valid squad refuses.
func TestFleetAttachLeavesOutAnInvalidSquad(t *testing.T) {
	f, file, _, ep, mailer := operatorFixture(t)
	lib := filepath.Join(t.TempDir(), "lib")
	if _, _, err := captureOutput(t, func() error {
		return runFleet([]string{"add", "--fleet-file", file, "--name", "lib", "--root", filepath.Join(lib, ".agent-mail", "w"), "--project-path", lib, "--repository", "owner/lib", "--lead", "ghost", "--session", "w"})
	}); err != nil {
		t.Fatal(err)
	}
	out, stop := attachAsync(t, []string{"--fleet-file", file, "--project", f.project, "--session", "s", "--me", "cto", "--interval", "50ms", "--json"})
	eventually(t, "the endpoint to start", func() bool { _, starts, _ := ep.counts(); return starts == 1 })
	f.answer(t, "@app/cto ship", "")
	eventually(t, "the valid squad routed", func() bool { return mailer.to("cto") })
	stop()
	if !strings.Contains(out.String(), "squad lib left out") {
		t.Fatalf("no problem for the invalid squad:\n%s", out.String())
	}
	// only invalid squads: refuse
	if _, _, err := captureOutput(t, func() error { return runFleet([]string{"remove", "--fleet-file", file, "--name", "app"}) }); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runRemote([]string{"attach", "--once", "--fleet-file", file, "--project", f.project, "--session", "s", "--me", "cto"}, "test")
	}); err == nil || !strings.Contains(err.Error(), "no squad of the fleet registry validates") {
		t.Fatalf("a fleet with no valid squad: %v", err)
	}
}

// Medium 6: a status document older than two tick intervals is a miss.
func TestRemotePresenceExpiresWhenStatusIsStale(t *testing.T) {
	f, file, _, ep, _ := operatorFixture(t)
	var stale atomic.Bool
	prev := statusNow
	statusNow = func() time.Time {
		if stale.Load() {
			return time.Now().Add(-time.Hour).UTC()
		}
		return time.Now().UTC()
	}
	t.Cleanup(func() { statusNow = prev })
	out, stop := attachAsync(t, []string{"--fleet-file", file, "--project", f.project, "--session", "s", "--me", "cto", "--interval", "50ms", "--json"})
	eventually(t, "the endpoint to start", func() bool { _, starts, _ := ep.counts(); return starts == 1 })
	stale.Store(true)
	eventually(t, "presence to expire on a stale document", func() bool { running, _, stops := ep.counts(); return !running && stops == 1 })
	stop()
	if !strings.Contains(out.String(), "is stale") {
		t.Fatalf("attach output:\n%s", out.String())
	}
}
