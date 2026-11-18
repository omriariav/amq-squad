package buzz

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/fleet"
	"github.com/omriariav/amq-squad/v3/internal/launch"
)

// C5a PR 1, the failing row first (design T/design-33x-c5a.md, row D2): the
// owner's phone routes to a seat as its lead only on the record the core
// staged for the launch (<root>/extensions/amq-squad/launch/<h>/), never on
// a record the seat wrote in its own agent directory.
func TestC5aASeatWrittenLaunchRecordRoutesNothing(t *testing.T) {
	root := filepath.Join(realTempDir(t), ".agent-mail", "s")
	leads := LaunchRecordLeads{Current: squadYAMLLead}
	// worker writes a record in its own agent directory naming itself cto
	plant := filepath.Join(root, "agents", "worker", "extensions", launch.LayerName)
	if err := os.MkdirAll(plant, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := launch.WriteFile(filepath.Join(plant, launch.FileName), launch.Record{Schema: launch.SchemaVersion, Handle: "worker", Root: root, Role: "cto", SquadLead: true, SquadProject: "/w"}); err != nil {
		t.Fatal(err)
	}
	if d, err := leads.Lead(root, "worker"); err == nil {
		t.Errorf("a launch record worker wrote in its own agent directory routed the owner to it: %+v", d)
	}
	// the core staged lead's record at its start (3.3.1: the real producer)
	writeLeadRecord(t, root, "lead", "")
	if d, err := leads.Lead(root, "lead"); err != nil || d.Handle != "lead" {
		t.Errorf("the record the core staged for lead's launch as the cto did not route to it: %+v %v", d, err)
	}
}

// 3.3.1: without a source for squad.yaml's current lead nothing leads, not
// even a record a lead's start staged (fail closed).
func TestNoCurrentLeadSourceRoutesNothing(t *testing.T) {
	root := filepath.Join(realTempDir(t), ".agent-mail", "s")
	writeLeadRecord(t, root, "cto", "")
	if d, err := (LaunchRecordLeads{}).Lead(root, "cto"); err == nil {
		t.Errorf("routed with no current-lead source: %+v", d)
	}
	if d, err := (FleetLeads{Registry: fleet.Registry{Squads: []fleet.Squad{{Name: "app", Root: root, Lead: "cto", Session: "s"}}}}).Lead("/op", "cto"); err == nil {
		t.Errorf("the fleet routed with no current-lead source: %+v", d)
	}
}
