package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
)

// v3-5-verifier rule 12: a commit step waiting for a verifier slot is a
// status row (verifier_waiting, working), and the harness's verifier line in
// a seat's evidence ledger is not that seat's activity.
func TestStatusNamesACommitStepWaitingForAVerifierSlot(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(seatcommit.VerifierDir(root), "waiting")
	os.MkdirAll(dir, 0o700)
	raw, _ := json.Marshal(seatcommit.VerifierWait{Seat: "s1", Task: "t4", Since: time.Unix(50, 0).UTC(), Parallelism: 2})
	os.WriteFile(filepath.Join(dir, "s1.json"), raw, 0o600)
	st := Build(Collect(CollectOptions{Root: root, Now: time.Unix(100, 0)}))
	n := 0
	for _, a := range st.Attention {
		if a.Kind == "verifier_waiting" {
			n++
			if a.Seat != "s1" || a.Bucket != BucketWorking {
				t.Fatalf("the waiting row: %s %s", a.Seat, a.Bucket)
			}
		}
	}
	if n != 1 {
		t.Fatalf("%d verifier_waiting rows, want 1", n)
	}
	ledgerPath := filepath.Join(root, "extensions", "amq-squad", "ledger", "s2.jsonl")
	os.MkdirAll(filepath.Dir(ledgerPath), 0o700)
	os.WriteFile(ledgerPath, []byte(`{"schema":1,"seq":1,"at":"2026-10-25T09:00:00Z","handle":"s2","event":"Stop"}`+"\n"+
		`{"schema":1,"seq":2,"at":"2026-10-25T09:05:00Z","handle":"s2","event":"`+seatcommit.EventVerifier+`","outcome":"pass"}`+"\n"), 0o600)
	tail, err := readLedgerTail(ledgerPath)
	if err != nil || tail.prompt != "idle" || !tail.last.Equal(time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("the verifier line counted as the seat's activity: %s %s %v", tail.prompt, tail.last, err)
	}
}
