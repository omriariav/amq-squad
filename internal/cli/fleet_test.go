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

// fleetTempDir is a test directory by its canonical path: the fleet
// registry is opened from / with O_NOFOLLOW, and macOS temp directories sit
// under the /var symlink.
func fleetTempDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// `status --fleet --json` over three registered squads is the fleet document
// of ruling 16 and validates against the fleet golden fixture.
func TestStatusFleetJSONIsTheFleetDocument(t *testing.T) {
	golden, err := os.ReadFile(filepath.Join("..", "status", "testdata", "status-fleet-v1.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	dir := fleetTempDir(t)
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

// Codex P2 7: a registered root that no longer exists keeps its squad row
// in status --fleet --json and carries a root_unavailable warning.
func TestStatusFleetMissingRootWarns(t *testing.T) {
	dir := fleetTempDir(t)
	file := filepath.Join(dir, "fleet.yaml")
	gone := filepath.Join(dir, "gone", ".agent-mail", "gone")
	if _, _, err := captureOutput(t, func() error {
		return runFleet([]string{"add", "--fleet-file", file, "--name", "gone", "--root", gone,
			"--project-path", filepath.Join(dir, "gone"), "--repository", "owner/gone", "--lead", "lead"})
	}); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := captureOutput(t, func() error {
		return runStatusWithVersion([]string{"--fleet", "--fleet-file", file, "--json"}, "x")
	})
	if err != nil {
		t.Fatal(err)
	}
	var f status.Fleet
	if err := json.Unmarshal([]byte(stdout), &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Squads) != 1 || f.Squads[0].Root != gone {
		t.Fatalf("the squad row is gone: %+v", f.Squads)
	}
	warned := false
	for _, w := range f.Squads[0].Warnings {
		warned = warned || strings.HasPrefix(w, fleet.WarningRootUnavailable+": squad gone")
	}
	if !warned {
		t.Fatalf("no root_unavailable warning: %q", f.Squads[0].Warnings)
	}
}

// 4-5 end checkpoint: when the operator session runs, the fleet document
// carries its seat row (handle user, role operator), bucketed like any seat;
// with no signal it is null; squad documents still skip the user handle.
func TestStatusFleetCarriesTheOperatorSeat(t *testing.T) {
	dir := fleetTempDir(t)
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	prevNow := statusNow
	statusNow = func() time.Time { return now }
	t.Cleanup(func() { statusNow = prevNow })
	file := filepath.Join(dir, "fleet.yaml")
	opRoot := filepath.Join(dir, "operator", ".agent-mail", "fleet")
	if _, _, err := captureOutput(t, func() error {
		return runFleet([]string{"operator", "--fleet-file", file, "--root", opRoot, "--amq-project", "fleet", "--session", "fleet"})
	}); err != nil {
		t.Fatal(err)
	}
	read := func() status.Fleet {
		t.Helper()
		stdout, _, err := captureOutput(t, func() error {
			return runStatusWithVersion([]string{"--fleet", "--fleet-file", file, "--json"}, "x")
		})
		if err != nil {
			t.Fatal(err)
		}
		var f status.Fleet
		if err := json.Unmarshal([]byte(stdout), &f); err != nil {
			t.Fatal(err)
		}
		return f
	}
	if f := read(); f.Operator != nil {
		t.Fatalf("no operator session yet, operator = %+v", f.Operator)
	}
	presence := filepath.Join(opRoot, "agents", "user", "presence.json")
	if err := os.MkdirAll(filepath.Dir(presence), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(presence, []byte(`{"status":"active","last_seen":"`+now.Add(-10*time.Second).Format(time.RFC3339)+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	f := read()
	if f.Operator == nil || f.Operator.Handle != "user" || f.Operator.Role != status.OperatorRole || f.Operator.State != status.StateOnline || f.Operator.Bucket != status.BucketReady {
		t.Fatalf("operator seat = %+v", f.Operator)
	}
	golden, _ := os.ReadFile(filepath.Join("..", "status", "testdata", "status-fleet-v1.golden.json"))
	raw, _ := json.Marshal(f)
	if err := status.CheckAdditive(golden, raw); err != nil {
		t.Fatalf("against the fleet golden: %v", err)
	}
}

// Codex P1 2 (second pass): `fleet remove` refuses a row the mayor has in
// phase reserved or launching; a partial launch is the operator's to remove.
func TestFleetRemoveRefusesAReservation(t *testing.T) {
	dir := fleetTempDir(t)
	file := filepath.Join(dir, "fleet.yaml")
	row := func(name, phase string) fleet.Squad {
		return fleet.Squad{Name: name, Root: filepath.Join(dir, name, ".agent-mail", name), ProjectPath: filepath.Join(dir, name), Repository: "owner/" + name,
			Lead: "lead", Phase: phase, Reservation: "0123456789abcdef01234567", Incarnation: "fedcba9876543210fedcba98", Expires: "2026-10-24T12:10:00Z"}
	}
	if err := fleet.Update(file, func(r *fleet.Registry) error {
		r.Squads = append(r.Squads, row("res", fleet.PhaseReserved), row("lau", fleet.PhaseLaunching), row("par", fleet.PhaseFailedPartial))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"res", "lau"} {
		if _, _, err := captureOutput(t, func() error { return runFleet([]string{"remove", "--fleet-file", file, "--name", name}) }); err == nil {
			t.Errorf("fleet remove %s (in a reservation) succeeded", name)
		}
	}
	if _, _, err := captureOutput(t, func() error { return runFleet([]string{"remove", "--fleet-file", file, "--name", "par"}) }); err != nil {
		t.Fatalf("removing a partial launch: %v", err)
	}
	r, _ := fleet.Read(file)
	if len(r.Squads) != 2 {
		t.Fatalf("registry = %+v", r.Squads)
	}
}

// An empty or missing registry is a valid fleet document with no squads.
func TestStatusFleetEmpty(t *testing.T) {
	stdout, _, err := captureOutput(t, func() error {
		return runStatusWithVersion([]string{"--fleet", "--fleet-file", filepath.Join(fleetTempDir(t), "absent.yaml"), "--json"}, "x")
	})
	if err != nil || !strings.Contains(stdout, `"squads": []`) || !strings.Contains(stdout, `"kind": "fleet"`) {
		t.Fatalf("empty fleet (%v): %s", err, stdout)
	}
}
