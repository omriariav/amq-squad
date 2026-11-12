package mergetrain

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
)

const rollbackLegacyGateID = "clear-0a227ca8d88dc97c65c540a4d0fa18a7"

// Frozen in the 3.0.x record's field order. No value is copied from a live
// store and no current gatestore type participates in writing the fixture.
const rollbackLegacyGate = `{
  "schema": "amq-squad-gate/1",
  "gate_id": "clear-0a227ca8d88dc97c65c540a4d0fa18a7",
  "class": "quarantine_clear",
  "gate_kind": "quarantine_clear",
  "typed_class": "clear",
  "target": "clear scribe nonce=0a227ca8d88dc97c65c540a4d0fa18a7 incarnation=t2@1 config=sha256:0000000000000000000000000000000000000000000000000000000000000000",
  "digest": "sha256:1111111111111111111111111111111111111111111111111111111111111111",
  "text": "frozen 3.0.x quarantine clear",
  "text_sha256": "sha256:2222222222222222222222222222222222222222222222222222222222222222",
  "issued_at": "2026-11-05T17:00:00Z",
  "not_before": "2026-11-05T16:59:00Z",
  "not_after": "2026-11-05T17:30:00Z",
  "raised_by": "owner",
  "owner_pubkey": "3333333333333333333333333333333333333333333333333333333333333333",
  "issuer_key_id": "ed25519-sha256:4444444444444444444444444444444444444444444444444444444444444444"
}`

func TestRollbackLegacyGateDoesNotRaiseTrainAlert(t *testing.T) {
	f := newFixture(t)
	root := filepath.Join(f.dir, "rollback-root")
	f.train.ReleaseGates, f.train.Root, f.train.DryRun, f.train.VerifyGate = &f.store, root, true, decisionAccepted
	f.req.PRs = f.req.PRs[:1]
	if _, err := f.train.Run(context.Background(), f.req); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	path := filepath.Join(root, "extensions", "amq-squad", "gates", rollbackLegacyGateID+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(rollbackLegacyGate), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := f.train.Run(context.Background(), f.req)
	want := gatestore.LegacyLine(rollbackLegacyGateID, "quarantine_clear")
	if err != nil || len(result.Alerts) != 0 || len(ReadAlerts(root)) != 0 || !slices.Equal(result.Notes, []string{want}) {
		t.Fatalf("legacy dry run: err=%v alerts=%+v disk=%+v notes=%q", err, result.Alerts, ReadAlerts(root), result.Notes)
	}
}
