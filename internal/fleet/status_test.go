package fleet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/status"
)

var now = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func golden(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "status", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// threeSquads is the fixture fleet: app has a lead seat and an open gate,
// noc has no lead seat (fleet.yaml fills lead_handle) and an open gate and a
// question to the owner, docs is idle.
func threeSquads() (Registry, Collector) {
	r := sampleRegistry()
	r.Squads = append(r.Squads, Squad{Name: "docs", Root: "/Users/owner/code/docs/.agent-mail/d", ProjectPath: "/Users/owner/code/docs", Repository: "owner/docs", Lead: "lead"})
	inputs := map[string]status.Input{
		"app": {Seats: []status.SeatInput{{Handle: "lead", Role: "lead", Live: true, HasSignal: true}, {Handle: "dev", Role: "implementer"}},
			Gates: []status.GateInput{{ID: "g-tag", Class: "tag", Digest: "sha256:aa", State: "open", RaisedBy: "lead", Since: now.Add(-2 * time.Minute)},
				{ID: "g-old", Class: "spawn", Digest: "sha256:bb", State: "accepted", RaisedBy: "lead"}}},
		"noc": {Seats: []status.SeatInput{{Handle: "cto", Role: "cto", Live: true, HasSignal: true}},
			Gates:    []status.GateInput{{ID: "g-send", Class: "external_send", Digest: "sha256:cc", State: "open", RaisedBy: "cto", Since: now.Add(-5 * time.Minute)}},
			NeedsYou: []status.ThreadInput{{ID: "p2p/cto__user", Seat: "cto", Subject: "which base branch?", Since: now.Add(-time.Minute)}}},
		"docs": {},
	}
	collect := func(s Squad) status.Status {
		in := inputs[s.Name]
		in.Root, in.Session, in.ProjectPath, in.ProducerVersion, in.Now = s.Root, s.SessionName(), s.ProjectPath, "3.0.0-test", now
		return status.Build(in)
	}
	return r, collect
}

// Acceptance 2: three fixture squads give one `status --fleet --json` that
// validates against the fleet golden fixture; each squad is a schema 1
// document that validates against the single-squad golden.
func TestFleetStatusValidatesAgainstTheFleetGolden(t *testing.T) {
	r, collect := threeSquads()
	f := Status(r, collect, "3.0.0-test", now)
	raw, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := status.CheckAdditive(golden(t, "status-fleet-v1.golden.json"), raw); err != nil {
		t.Fatalf("fleet document against the fleet golden: %v\n%s", err, raw)
	}
	if !status.IsFleet(raw) || f.Kind != status.KindFleet || len(f.Squads) != 3 {
		t.Fatalf("fleet header: %+v", f)
	}
	single := golden(t, "status-v1.golden.json")
	for i, st := range f.Squads {
		one, _ := json.Marshal(st)
		if err := status.CheckAdditive(single, one); err != nil {
			t.Fatalf("squad %d is not a schema 1 document: %v", i, err)
		}
		if status.IsFleet(one) {
			t.Fatalf("squad %d reads as a fleet", i)
		}
		if st.Root != r.Squads[i].Root {
			t.Fatalf("squad %d out of registry order: %s", i, st.Root)
		}
	}
	// lead_handle: the producer's lead seat wins; fleet.yaml fills a null one
	if h := f.Squads[0].LeadHandle; h == nil || *h != "lead" {
		t.Fatalf("app lead_handle = %v", h)
	}
	if h := f.Squads[1].LeadHandle; h == nil || *h != "cto" {
		t.Fatalf("noc lead_handle from fleet.yaml = %v", h)
	}
	// unchanged otherwise: the wrapped squad equals the producer's document
	direct := collect(r.Squads[0])
	a, _ := json.Marshal(direct)
	b, _ := json.Marshal(f.Squads[0])
	if string(a) != string(b) {
		t.Fatalf("the fleet changed a squad document:\n%s\n%s", a, b)
	}
}

// Acceptance 3: open gates from two squads appear in one list with their
// squad; an accepted gate does not; the lead question carries a relay argv.
func TestGateGrouping(t *testing.T) {
	r, collect := threeSquads()
	in := GroupInbox(r, Status(r, collect, "x", now))
	if len(in.Gates) != 2 {
		t.Fatalf("want two open gates, got %+v", in.Gates)
	}
	if in.Gates[0].Squad != "noc" || in.Gates[0].Gate.ID != "g-send" || in.Gates[1].Squad != "app" || in.Gates[1].Gate.ID != "g-tag" {
		t.Fatalf("gates not grouped with their squad, oldest first: %+v", in.Gates)
	}
	if in.Gates[0].Root != r.Squads[1].Root || in.Gates[0].Since.IsZero() {
		t.Fatalf("gate row: %+v", in.Gates[0])
	}
	if len(in.Questions) != 1 || in.Questions[0].Squad != "noc" {
		t.Fatalf("questions: %+v", in.Questions)
	}
	reply := in.Questions[0].Reply
	if len(reply) < 2 || reply[0] != "amq" || reply[1] != "send" || reply[len(reply)-1] != "p2p/cto__user" {
		t.Fatalf("relay argv: %q", reply)
	}
}
