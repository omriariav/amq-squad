package cli

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gatetarget"
)

// A1-3: the hook context reads the window re-ask as gatestore.State does: a
// lapsed gate asked again is open until the derived end, with that end as
// its not_after.
func TestA13HookContextReadsTheWindowReask(t *testing.T) {
	store, err := gatestore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	rec, err := gatestore.Render(gatestore.Spec{GateID: "g1", Action: "tag", Target: gatetarget.Command("git tag", "v1"), RaisedBy: "lead", Squad: store.Squad()},
		now, gatestore.Identity{OwnerPubKey: strings.Repeat("a1", 32), IssuerKeyID: "ed25519-sha256:" + strings.Repeat("b2", 32)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Create(rec); err != nil {
		t.Fatal(err)
	}
	at := now.Add(2 * time.Hour)
	if gates, _ := readHookGates(store.Root, at); len(gates) != 1 || gates[0].State != "expired" {
		t.Fatalf("lapsed: %+v", gates)
	}
	if err := store.PutWindow(gatestore.Reask{GateID: "g1", TriggerEventID: strings.Repeat("d4", 32), EventID: strings.Repeat("e5", 32),
		RawEvent: fmt.Sprintf(`{"created_at":%d}`, at.Unix()), At: at.Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	// a team-plan yes replying to the window re-ask is in a gate thread
	// (lead_start.go: conflicting_thread)
	if !(&leadStarter{root: store.Root}).gateAnchors()[strings.Repeat("e5", 32)] {
		t.Fatal("the lead's gate anchors miss the window re-ask")
	}
	end := at.Add(30 * time.Minute)
	if gates, _ := readHookGates(store.Root, end); len(gates) != 1 || gates[0].State != "open" || gates[0].NotAfter != end.Format(time.RFC3339) {
		t.Fatalf("asked again: %+v", gates)
	}
	if gates, _ := readHookGates(store.Root, end.Add(time.Second)); len(gates) != 1 || gates[0].State != "expired" {
		t.Fatalf("after the derived end: %+v", gates)
	}
}
