package status

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func readFleetGolden(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/status-fleet-v1.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// The fleet golden decodes strictly, round-trips additively both ways, and
// its squad is the single-squad golden unchanged except lead_handle null
// (ruling 16: one schema 1 document per squad, unchanged).
func TestFleetGoldenIsTheSquadGoldenWrapped(t *testing.T) {
	raw := readFleetGolden(t)
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var f Fleet
	if err := dec.Decode(&f); err != nil {
		t.Fatalf("fleet golden does not decode into status.Fleet: %v", err)
	}
	if f.SchemaVersion != 1 || f.Kind != KindFleet || f.Producer != "amq-squad" || len(f.Squads) != 1 {
		t.Fatalf("fleet header = %+v", f)
	}
	produced, _ := json.Marshal(f)
	if err := CheckAdditive(raw, produced); err != nil {
		t.Fatalf("producer not additive against the fleet golden: %v", err)
	}
	if err := CheckAdditive(produced, raw); err != nil {
		t.Fatalf("fleet golden does not cover a field the producer emits: %v", err)
	}
	var single, wrapped map[string]any
	_ = json.Unmarshal(readGolden(t), &single)
	var doc map[string][]map[string]any
	_ = json.Unmarshal(raw, &doc)
	wrapped = doc["squads"][0]
	if wrapped["lead_handle"] != nil || single["lead_handle"] != "lead" {
		t.Fatalf("lead_handle: fleet %v, single %v; want null and \"lead\"", wrapped["lead_handle"], single["lead_handle"])
	}
	single["lead_handle"] = nil
	if !reflect.DeepEqual(single, wrapped) {
		t.Fatal("the fleet golden's squad is not the single-squad golden (regenerate it from status-v1.golden.json)")
	}
	if err := CheckAdditive(readGolden(t), mustJSON(t, wrapped)); err != nil {
		t.Fatalf("the wrapped squad is not a schema 1 document: %v", err)
	}
}

func TestIsFleet(t *testing.T) {
	if !IsFleet(readFleetGolden(t)) || IsFleet(readGolden(t)) || IsFleet([]byte("[]")) {
		t.Fatal("IsFleet: a document with squads is a fleet, anything else one squad")
	}
	empty, _ := json.Marshal(BuildFleet("x", now, nil))
	if !bytes.Contains(empty, []byte(`"squads":[]`)) {
		t.Fatalf("an empty fleet must carry squads: []: %s", empty)
	}
}

// Acceptance 7: lead_handle is the seat whose role is lead, null with none.
func TestLeadHandle(t *testing.T) {
	st := Build(Input{Now: now, Seats: []SeatInput{{Handle: "dev", Role: "implementer"}, {Handle: "boss", Role: LeadRole}}})
	if st.LeadHandle == nil || *st.LeadHandle != "boss" {
		t.Fatalf("lead_handle = %v, want boss", st.LeadHandle)
	}
	st = Build(Input{Now: now, Seats: []SeatInput{{Handle: "dev", Role: "implementer"}}})
	raw, _ := json.Marshal(st)
	if st.LeadHandle != nil || !bytes.Contains(raw, []byte(`"lead_handle":null`)) {
		t.Fatalf("no lead seat: lead_handle must be null: %s", raw)
	}
	if !NullableFields["lead_handle"] {
		t.Fatal("lead_handle is not in NullableFields")
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
