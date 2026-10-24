package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/fleet"
	"github.com/omriariav/amq-squad/v3/internal/status"
	taskstore "github.com/omriariav/amq-squad/v3/internal/task"
)

// `status --fleet --json` over three registered squads is the fleet document
// of ruling 16 and validates against the fleet golden fixture.
func TestStatusFleetJSONIsTheFleetDocument(t *testing.T) {
	golden, err := os.ReadFile(filepath.Join("..", "status", "testdata", "status-fleet-v1.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	chdir(t, dir)
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	prevNow, prevAlive := statusNow, statusPIDAlive
	statusNow, statusPIDAlive = func() time.Time { return now }, func(int) bool { return true }
	t.Cleanup(func() { statusNow, statusPIDAlive = prevNow, prevAlive })
	file := filepath.Join(dir, "home", ".amq-squad", "fleet.yaml")
	for _, name := range []string{"app", "noc", "docs"} {
		root := filepath.Join(dir, name, ".agent-mail", name)
		store, err := taskstore.Open(root)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Add(taskstore.AddInput{Title: "work in " + name, Class: "implement"}, now.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
		_, _, err = captureOutput(t, func() error {
			return runFleet([]string{"add", "--fleet-file", file, "--name", name, "--root", root,
				"--project-path", filepath.Join(dir, name), "--repository", "owner/" + name, "--lead", "lead-" + name})
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	stdout, _, err := captureOutput(t, func() error {
		return runStatusWithVersion([]string{"--fleet", "--fleet-file", file, "--json"}, "3.0.0-test")
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := status.CheckAdditive(golden, []byte(stdout)); err != nil {
		t.Fatalf("status --fleet --json against the fleet golden: %v\n%s", err, stdout)
	}
	var f status.Fleet
	if err := json.Unmarshal([]byte(stdout), &f); err != nil {
		t.Fatal(err)
	}
	if f.Kind != "fleet" || len(f.Squads) != 3 || f.ProducerVersion != "3.0.0-test" {
		t.Fatalf("fleet = %+v", f)
	}
	for i, name := range []string{"app", "noc", "docs"} {
		st := f.Squads[i]
		if st.Session != name || len(st.Tasks) != 1 || st.LeadHandle == nil || *st.LeadHandle != "lead-"+name {
			t.Fatalf("squad %s: session %q tasks %d lead %v", name, st.Session, len(st.Tasks), st.LeadHandle)
		}
	}
	text, _, err := captureOutput(t, func() error { return runStatusWithVersion([]string{"--fleet", "--fleet-file", file}, "x") })
	if err != nil || !strings.Contains(text, "Fleet: 3 squads") || !strings.Contains(text, "noc (lead lead-noc)") {
		t.Fatalf("human fleet status (%v):\n%s", err, text)
	}
	list, _, err := captureOutput(t, func() error { return runFleet([]string{"list", "--fleet-file", file, "--json"}) })
	var r fleet.Registry
	if err != nil || json.Unmarshal([]byte(list), &r) != nil || len(r.Squads) != 3 {
		t.Fatalf("fleet list --json (%v): %s", err, list)
	}
	if _, _, err := captureOutput(t, func() error { return runFleet([]string{"remove", "--fleet-file", file, "--name", "noc"}) }); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error { return runFleet([]string{"remove", "--fleet-file", file, "--name", "noc"}) }); err == nil {
		t.Fatal("removing an absent squad succeeded")
	}
}

// An empty or missing registry is a valid fleet document with no squads.
func TestStatusFleetEmpty(t *testing.T) {
	stdout, _, err := captureOutput(t, func() error {
		return runStatusWithVersion([]string{"--fleet", "--fleet-file", filepath.Join(t.TempDir(), "absent.yaml"), "--json"}, "x")
	})
	if err != nil || !strings.Contains(stdout, `"squads": []`) || !strings.Contains(stdout, `"kind": "fleet"`) {
		t.Fatalf("empty fleet (%v): %s", err, stdout)
	}
}
