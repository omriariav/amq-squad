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
	write(RelayRecord{Schema: 1, RequestID: "q-aaaaaaaaaaaa", Thread: "cockpit/q-aaaaaaaaaaaa", Lead: "cto", UpdatedAt: now.Add(-2 * time.Minute),
		Turns: []RelayTurn{{Event: "e1"}, {Event: "e2"}}, Posts: []RelayPost{{ReplyID: "r1", Kind: "answer"}}, RelayedAt: &relayed})
	write(RelayRecord{Schema: 1, RequestID: "q-bbbbbbbbbbbb", Thread: "cockpit/q-bbbbbbbbbbbb", Lead: "cto", UpdatedAt: now.Add(-time.Minute),
		Turns: []RelayTurn{{Event: "e3"}}, Refusals: []RelayRefusal{{ReplyID: "r2", Reason: "relay_refused_gate_shape", SHA256: "sha256:00"}}})
	if err := os.WriteFile(filepath.Join(RelayDir(root), "q-cccccccccccc.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := Build(Collect(CollectOptions{Root: root, Session: "s", Now: now}))
	if len(st.Requests) != 2 {
		t.Fatalf("requests = %+v", st.Requests)
	}
	newest, older := st.Requests[0], st.Requests[1]
	if newest.RequestID != "q-bbbbbbbbbbbb" || newest.RelayedAt != nil || len(newest.Refused) != 1 || newest.Refused[0] != "relay_refused_gate_shape" || newest.Turns != 1 {
		t.Fatalf("the refused request: %+v", newest)
	}
	if older.RequestID != "q-aaaaaaaaaaaa" || older.RelayedAt == nil || !older.RelayedAt.Equal(relayed) || older.Posts != 1 || older.Turns != 2 || len(older.Refused) != 0 {
		t.Fatalf("the relayed request: %+v", older)
	}
	if !strings.Contains(strings.Join(st.Warnings, "\n"), "relay record q-cccccccccccc.json unreadable") {
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
