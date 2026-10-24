package status

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func readGolden(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/status-v1.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// The golden fixture decodes strictly into the Go types: a field in the
// fixture that the type lost (a removed or renamed field) fails here.
func TestGoldenDecodesStrictly(t *testing.T) {
	dec := json.NewDecoder(bytes.NewReader(readGolden(t)))
	dec.DisallowUnknownFields()
	var s Status
	if err := dec.Decode(&s); err != nil {
		t.Fatalf("golden does not decode into status.Status: %v", err)
	}
	if s.SchemaVersion != SchemaVersion {
		t.Fatalf("schema_version %d, want %d", s.SchemaVersion, SchemaVersion)
	}
}

// Encoding the decoded fixture is additive against the fixture, and the
// fixture covers every field the type encodes (a new field must be added
// to the fixture, which keeps it additive for amq-noc's vendored copy).
func TestGoldenRoundTripAdditiveBothWays(t *testing.T) {
	golden := readGolden(t)
	var s Status
	if err := json.Unmarshal(golden, &s); err != nil {
		t.Fatal(err)
	}
	produced, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckAdditive(golden, produced); err != nil {
		t.Fatalf("producer is not additive against the golden fixture: %v", err)
	}
	if err := CheckAdditive(produced, golden); err != nil {
		t.Fatalf("golden fixture does not cover a field the producer emits (add it to the fixture): %v", err)
	}
}

func mutate(t *testing.T, raw []byte, fn func(m map[string]any)) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	fn(m)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRemovedFieldFails(t *testing.T) {
	golden := readGolden(t)
	cases := map[string]func(m map[string]any){
		"top-level producer_version": func(m map[string]any) { delete(m, "producer_version") },
		"seat tokens_in": func(m map[string]any) {
			delete(m["seats"].([]any)[1].(map[string]any), "tokens_in")
		},
		"task lease_holder": func(m map[string]any) {
			delete(m["tasks"].([]any)[0].(map[string]any), "lease_holder")
		},
		"new_task argv": func(m map[string]any) {
			delete(m["actions"].(map[string]any)["new_task"].(map[string]any), "argv")
		},
	}
	for name, fn := range cases {
		if err := CheckAdditive(golden, mutate(t, golden, fn)); err == nil || !strings.Contains(err.Error(), "removed") {
			t.Errorf("%s: removing the field passed the check (err %v)", name, err)
		}
	}
	retyped := mutate(t, golden, func(m map[string]any) { m["schema_version"] = "1" })
	if err := CheckAdditive(golden, retyped); err == nil {
		t.Error("a changed type passed the check")
	}
}

func TestAddedOptionalFieldPasses(t *testing.T) {
	golden := readGolden(t)
	added := mutate(t, golden, func(m map[string]any) {
		m["fleet"] = nil
		m["seats"].([]any)[0].(map[string]any)["worktree"] = "/tmp/wt"
		m["tasks"].([]any)[1].(map[string]any)["priority"] = 3
	})
	if err := CheckAdditive(golden, added); err != nil {
		t.Fatalf("an added optional field failed the check: %v", err)
	}
	nulled := mutate(t, golden, func(m map[string]any) {
		m["seats"].([]any)[0].(map[string]any)["tokens_in"] = nil
	})
	if err := CheckAdditive(golden, nulled); err != nil {
		t.Fatalf("a null nullable field failed the check: %v", err)
	}
}

// Every suggested action in the fixture is an argv array plus an action id.
func TestFixtureActionsAreArgv(t *testing.T) {
	var s Status
	if err := json.Unmarshal(readGolden(t), &s); err != nil {
		t.Fatal(err)
	}
	actions := []Action{s.Actions.NewTask}
	for _, a := range s.Attention {
		if a.Action != nil {
			actions = append(actions, *a.Action)
		}
	}
	for _, a := range actions {
		if err := ValidateAction(a); err != nil {
			t.Error(err)
		}
	}
	if err := ValidateAction(Action{ID: "x", Argv: []string{"sh -c 'rm -rf /'"}}); err == nil {
		t.Error("a shell string passed ValidateAction")
	}
}

func TestBucketTable(t *testing.T) {
	cases := []struct {
		s       State
		owner   bool
		task    bool
		want    Bucket
		mark    string
		comment string
	}{
		{StateNeedsYou, true, true, BucketNeedsYou, "", "open gate"},
		{StateNeedsYou, false, false, BucketNeedsYou, "", "needs_you is always Needs you"},
		{StateBlocked, true, true, BucketNeedsYou, "", "blocked, routed to the owner"},
		{StateBlocked, false, true, BucketWorking, "", "blocked, routed to the lead"},
		{StateWaiting, false, true, BucketWorking, "", "waiting"},
		{StateOnline, false, true, BucketWorking, "", "live lease with a task"},
		{StateOnline, false, false, BucketReady, "", "live seat, no task"},
		{StateStale, false, true, BucketInactive, "", "expired lease"},
		{StateUnknown, false, false, BucketInactive, MarkerUnknown, "unknown with a marker"},
		{State("bogus"), false, false, BucketInactive, MarkerUnknown, "anything else is unknown"},
	}
	for _, c := range cases {
		got, mark := BucketFor(c.s, c.owner, c.task)
		if got != c.want || mark != c.mark {
			t.Errorf("%s: BucketFor(%s, %v, %v) = %s %q, want %s %q", c.comment, c.s, c.owner, c.task, got, mark, c.want, c.mark)
		}
	}
	if len(Buckets) != 4 {
		t.Fatalf("want four buckets, got %d", len(Buckets))
	}
}
