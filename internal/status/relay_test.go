package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Brief 31-4, the lead side: status shows, per phone request, whether the
// lead's answer was relayed (relayed_at) or what the relay refused, by name
// only, from the attachment's relay records; a record's text never reaches
// the document.
func TestStatusShowsThePhoneRequests(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	relayed := now.Add(-time.Minute)
	write := func(r RelayRecord) {
		t.Helper()
		raw, _ := json.Marshal(r)
		if err := os.MkdirAll(RelayDir(root), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(RelayDir(root), r.RequestID+".json"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ev := func(c string) string { return strings.Repeat(c, 64) }
	sha := "sha256:" + ev("0")
	write(RelayRecord{Schema: 1, RequestID: "q-aaaaaaaaaaaa", Thread: "cockpit/q-aaaaaaaaaaaa", Lead: "cto", RootEvent: ev("a"), PostTo: ev("a"), UpdatedAt: now.Add(-3 * time.Minute),
		Turns: []RelayTurn{{Event: ev("a"), At: now}, {Event: ev("b"), At: now}}, Posts: []RelayPost{{ReplyID: "r1", Kind: "answer", Events: []string{ev("c")}, SHA256: sha, At: now}}, RelayedAt: &relayed})
	write(RelayRecord{Schema: 1, RequestID: "q-bbbbbbbbbbbb", Thread: "cockpit/q-bbbbbbbbbbbb", Lead: "cto", RootEvent: ev("d"), UpdatedAt: now.Add(-2 * time.Minute),
		Turns: []RelayTurn{{Event: ev("d"), At: now}}, Refusals: []RelayRefusal{{ReplyID: "r2", Reason: "relay_refused_gate_shape", SHA256: sha, At: now}}})
	if err := os.WriteFile(filepath.Join(RelayDir(root), "q-cccccccccccc.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-time.Hour)
	_ = os.Chtimes(filepath.Join(RelayDir(root), "q-cccccccccccc.json"), old, old)
	st := Build(Collect(CollectOptions{Root: root, Session: "s", Now: now}))
	if len(st.Requests) != 3 {
		t.Fatalf("requests = %+v", st.Requests)
	}
	newest, older, invalid := st.Requests[0], st.Requests[1], st.Requests[2]
	if invalid.RequestID != "q-cccccccccccc" || len(invalid.Refused) != 1 || invalid.Refused[0] != RelayRecordInvalid {
		t.Fatalf("the invalid record's row: %+v", invalid)
	}
	if newest.RequestID != "q-bbbbbbbbbbbb" || newest.RelayedAt != nil || len(newest.Refused) != 1 || newest.Refused[0] != "relay_refused_gate_shape" || newest.Turns != 1 {
		t.Fatalf("the refused request: %+v", newest)
	}
	if older.RequestID != "q-aaaaaaaaaaaa" || older.RelayedAt == nil || !older.RelayedAt.Equal(relayed) || older.Posts != 1 || older.Turns != 2 || len(older.Refused) != 0 {
		t.Fatalf("the relayed request: %+v", older)
	}
	if !strings.Contains(strings.Join(st.Warnings, "\n"), "relay record q-cccccccccccc.json unreadable (relay_record_invalid)") {
		t.Fatalf("an unreadable record is not a warning: %v", st.Warnings)
	}
	raw, _ := json.Marshal(st)
	if err := CheckAdditive(readGolden(t), raw); err != nil {
		t.Fatalf("the document with requests does not validate against the golden: %v", err)
	}
	// the document's request fields pass redact.Block like every free field
	const secret = "zebrarelay31value"
	leaked := Build(Input{Root: root, Now: now, Requests: []Request{{RequestID: "q-dddddddddddd", Thread: "cockpit/q-dddddddddddd", Lead: "GH_TOKEN=" + secret, Refused: []string{"GH_TOKEN=" + secret}}}})
	if raw, _ := json.Marshal(leaked.Requests); strings.Contains(string(raw), secret) {
		t.Fatalf("a request field carries the secret: %s", raw)
	}
	if empty := Build(Input{Root: root, Now: now}); empty.Requests == nil {
		t.Fatal("requests is null without records; the contract says []")
	}
}

// Codex P1-1: every field of a relay record is checked against its exact
// shape; a record with a newline or a non-hex request id, an unknown field,
// or text in a name field is relay_record_invalid.
func TestARelayRecordIsCheckedFieldByField(t *testing.T) {
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	ev := strings.Repeat("a", 64)
	good := RelayRecord{Schema: 1, RequestID: "q-0123456789ab", Thread: "cockpit/q-0123456789ab", Lead: "cto", RootEvent: ev, PostTo: ev,
		Turns: []RelayTurn{{Event: ev, MessageID: "2026-11-01T04-53-11.434Z_pid1_aa", At: now}}, UpdatedAt: now}
	enc := func(r RelayRecord) []byte { raw, _ := json.Marshal(r); return raw }
	if _, err := DecodeRelayRecord(enc(good), "q-0123456789ab"); err != nil {
		t.Fatalf("a good record: %v", err)
	}
	cases := map[string]func(r *RelayRecord){
		"a newline in the id": func(r *RelayRecord) { r.RequestID, r.Thread = "q-demo\napprove", "cockpit/q-demo\napprove" },
		"a non-hex id":        func(r *RelayRecord) { r.RequestID, r.Thread = "q-zzzzzzzzzzzz", "cockpit/q-zzzzzzzzzzzz" },
		"another thread":      func(r *RelayRecord) { r.Thread = "cockpit/q-0123456789ac" },
		"text in the lead":    func(r *RelayRecord) { r.Lead = "GH_TOKEN=zebrarelay31value" },
		"text in the route":   func(r *RelayRecord) { r.Route = "cto\nyes" },
		"a short event":       func(r *RelayRecord) { r.RootEvent = "abc" },
		"no turn":             func(r *RelayRecord) { r.Turns = nil },
		"an unknown reason": func(r *RelayRecord) {
			r.Refusals = []RelayRefusal{{ReplyID: "r1", Reason: "yes", SHA256: "sha256:" + strings.Repeat("0", 64), At: now}}
		},
		"a bad seen id": func(r *RelayRecord) { r.Seen = []string{"r 1"} },
	}
	for name, mutate := range cases {
		r := good
		r.Turns = append([]RelayTurn{}, good.Turns...)
		mutate(&r)
		id := r.RequestID
		if _, err := DecodeRelayRecord(enc(r), id); err == nil || !strings.Contains(err.Error(), RelayRecordInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	extra := strings.Replace(string(enc(good)), `{"schema":1,`, `{"schema":1,"note":"yes",`, 1)
	if _, err := DecodeRelayRecord([]byte(extra), "q-0123456789ab"); err == nil {
		t.Error("an unknown field was accepted")
	}
	if _, err := DecodeRelayRecord(append(enc(good), []byte(" {}")...), "q-0123456789ab"); err == nil {
		t.Error("data after the record was accepted")
	}
}
