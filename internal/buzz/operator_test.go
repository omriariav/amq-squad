package buzz

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/fleet"
	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/status"
)

// FleetLeads is the lead authority after 4-3: a squad's registered lead is a
// destination in that squad's root, reached across projects from the
// operator's root; anything else is refused.
func TestFleetLeads(t *testing.T) {
	dir := realTempDir(t)
	app, lib := filepath.Join(dir, "app", ".agent-mail", "s"), filepath.Join(dir, "lib", ".agent-mail", "w")
	writeLaunchRecord(t, app, "lead-app", "lead")
	writeLaunchRecord(t, lib, "lead-lib", "cto")
	op := filepath.Join(dir, "op")
	r := fleet.Registry{Operator: fleet.Operator{Root: op, Project: "fleet", Session: "ops"}, Squads: []fleet.Squad{
		{Name: "app", Root: app, Lead: "lead-app", Session: "s"},
		{Name: "lib", Root: lib, Lead: "lead-lib", Project: "libp"},
	}}
	reg := FleetLeads{Registry: r}
	d, err := reg.Lead(op, "lead-lib")
	if err != nil || d.Root != lib || d.Handle != "lead-lib" || d.Project != "libp" || d.Session != "w" || d.Squad != "lib" {
		t.Fatalf("lead-lib = %+v, %v", d, err)
	}
	if d, _ := reg.Lead(app, "lead-app"); d.Project != "" {
		t.Fatalf("a lead in the mailbox's own root got a cross-project route: %+v", d)
	}
	if _, err := reg.Lead(op, "worker"); err == nil {
		t.Fatal("a handle that leads no squad was a destination")
	}
	op2, err := Resolve(op, "", []string{"lead-app", "app/lead-app"})
	if err != nil || op2.Handle != OperatorHandle || op2.Root != op {
		t.Fatalf("operator target = %+v, %v", op2, err)
	}
	if lead, _ := Resolve(app, "lead-app", nil); lead.Handle != "lead-app" {
		t.Fatalf("an explicit --lead = %+v", lead)
	}
}

// Presence of the operator session comes from the fleet document's operator
// row (the user handle, which squad documents skip).
func TestOperatorPresenceFromTheFleetRow(t *testing.T) {
	now := time.Now().UTC()
	squad := status.Build(status.Input{Now: now, Seats: []status.SeatInput{{Handle: "lead-app", Live: true, HasSignal: true, LastActivity: &now}}})
	for _, live := range []bool{true, false} {
		f := status.BuildFleet("t", now, []status.Status{squad}, nil)
		f.Operator = &status.Seat{Handle: OperatorHandle, Role: status.OperatorRole, State: status.StateOnline, Live: live}
		raw, _ := json.Marshal(f)
		p, err := FromStatus(raw, OperatorHandle)
		if err != nil || p.Present != live || p.Squads != 1 {
			t.Fatalf("operator live %v: %+v, %v", live, p, err)
		}
	}
	f := status.BuildFleet("t", now, []status.Status{squad}, nil) // no operator row: not present
	raw, _ := json.Marshal(f)
	if p, _ := FromStatus(raw, OperatorHandle); p.Present {
		t.Fatalf("no operator row = %+v", p)
	}
}

// Operator mode: each squad's gates live in that squad's root, so the pump
// hands an answer to the core whose store holds the gate.
func TestPumpAnswersAGateInAnotherSquadsStore(t *testing.T) {
	f, p, _, _ := pumpFixture(t)
	ctx := context.Background()
	other := *f.core
	otherRoot := filepath.Join(realTempDir(t), "squad-b")
	store, err := gatestore.Open(otherRoot)
	if err != nil {
		t.Fatal(err)
	}
	other.Store = store
	p.Cores = []*gateverify.Core{&other}
	rec, q, err := other.Raise(ctx, gatestore.Spec{GateID: "rel-b", Action: "tag", Target: "git tag v3.0.0-b", RaisedBy: "lead-b", Window: 30 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	f.ownerDM(t, "approve rel-b release "+strings.TrimPrefix(rec.Digest, "sha256:")[:16], q.EventID)
	got, err := p.Step(ctx)
	if err != nil || len(got) != 1 || got[0].Gate == nil || got[0].Gate.Outcome != "approved" {
		t.Fatalf("answer for squad b: %+v, %v", got, err)
	}
	if a, err := other.Store.ReadAnswer("rel-b"); err != nil || a == nil {
		t.Fatalf("the answer is not in squad b's store: %v", err)
	}
	if a, err := f.core.Store.ReadAnswer("rel-b"); err == nil && a != nil {
		t.Fatal("the answer landed in the primary store")
	}
}
