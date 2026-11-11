package cli

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	taskstore "github.com/omriariav/amq-squad/v3/internal/task"
)

func TestRollbackReadersConsumeOldProcessRows(t *testing.T) {
	root := os.Getenv("AMQSQ_ROLLFORWARD_ROOT")
	if root == "" {
		t.Skip("set AMQSQ_ROLLFORWARD_ROOT")
	}
	detail, seats, err := UpRosterOutcome(root, root, "v3.2.0-rc.2", 0, 0, 0, nil, "rollback reader probe")
	var refusal *upRefusal
	if !errors.As(err, &refusal) || refusal.Name != "roster_wait_timeout" || detail != "" || len(seats) != 0 {
		t.Fatalf("UpRosterOutcome over old rows = detail %q seats %+v err %v", detail, seats, err)
	}
	contract := buildStatusContract(root, root, "v3.2.0-rc.2", time.Unix(109, 0).UTC())
	var tampered, unwritable int
	for _, decision := range contract.Decisions {
		switch decision.Kind {
		case "ledger_tampered":
			tampered++
		case "exits_ledger_unwritable":
			unwritable++
		}
	}
	if tampered == 0 || unwritable == 0 {
		t.Fatalf("status decisions did not retain the old process rows: tampered=%d unwritable=%d decisions=%+v", tampered, unwritable, contract.Decisions)
	}
	t.Logf("UpRosterOutcome: %s: %s", refusal.Name, refusal.Detail)
	t.Logf("status old decisions: ledger_tampered=%d exits_ledger_unwritable=%d", tampered, unwritable)
}

const cliRollbackLegacyGateID = "clear-0a227ca8d88dc97c65c540a4d0fa18a7"

const cliRollbackLegacyGate = `{
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

func TestRollbackLegacyGateLineInGateStatusAndStatus(t *testing.T) {
	f := newGateCLIFixture(t)
	path := filepath.Join(f.root, "extensions", "amq-squad", "gates", cliRollbackLegacyGateID+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(cliRollbackLegacyGate), 0o600); err != nil {
		t.Fatal(err)
	}
	want := gatestore.LegacyLine(cliRollbackLegacyGateID, "quarantine_clear")
	_, stderr, err := captureOutput(t, func() error {
		return runGate([]string{"status", "--project", f.project, "--session", "s"})
	})
	if err != nil || !strings.Contains(stderr, "gate status: "+want) {
		t.Fatalf("gate status: err=%v stderr=%q", err, stderr)
	}
	status := buildStatusContract(f.root, f.project, "v3.2.0-rc.2", time.Unix(1, 0))
	if len(status.Warnings) == 0 || status.Warnings[0] != "gate store: "+want {
		t.Fatalf("status warnings = %q", status.Warnings)
	}
}

func TestRollbackRC1RewriteLeavesUnlabelledRenewals(t *testing.T) {
	f := newHookFixture(t)
	store, err := taskstore.Open(f.root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Add(taskstore.AddInput{Title: "rollback renewal", Class: "script"}, f.now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim("t1", "cto", nil, 10*time.Minute, f.now.Add(-5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if !ingestToolResult(t, "cto") {
		t.Fatal("base activity renewal did not run")
	}
	decisions, err := schedproc.ReadDecisionsTail(f.root)
	if err != nil || len(decisions) != 1 || decisions[0].Reason != "first activity renewal of attempt 1" || strings.Contains(decisions[0].Reason, "unlabelled") {
		t.Fatalf("base decisions = %+v err=%v", decisions, err)
	}

	// The manual rc.1 renewal in the process probe rewrites this object with
	// the three unknown labels absent. Freeze that resulting shape here so
	// the current reader and its output stay asserted in the package test.
	path := filepath.Join(store.Dir, "t1.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var taskDoc map[string]any
	if err := json.Unmarshal(raw, &taskDoc); err != nil {
		t.Fatal(err)
	}
	lease := taskDoc["lease"].(map[string]any)
	delete(lease, "activity_renewals")
	delete(lease, "manual_renewals")
	delete(lease, "last_renewal")
	lease["renewals"] = float64(2)
	lease["renewed_at"] = f.now.Add(-2 * time.Minute).Format(time.RFC3339)
	lease["expires_at"] = f.now.Add(8 * time.Minute).Format(time.RFC3339)
	raw, err = json.MarshalIndent(taskDoc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	shown, err := taskCLI(t, "show", "t1")
	if err != nil || !strings.Contains(shown, "2 renewals (0 activity, 0 manual, 2 unlabelled)") || !strings.Contains(shown, "expires 2026-10-23T12:08:00Z") {
		t.Fatalf("task show after rc.1 rewrite = %q err=%v", shown, err)
	}
	if !ingestToolResult(t, "cto") {
		t.Fatal("first activity after rc.1 rewrite did not renew")
	}
	decisions, err = schedproc.ReadDecisionsTail(f.root)
	if err != nil || len(decisions) != 2 || decisions[1].Reason != "first activity renewal of attempt 1 after 2 unlabelled renewals" {
		t.Fatalf("post-rc.1 decisions = %+v err=%v", decisions, err)
	}
	if ingestToolResult(t, "cto") {
		t.Fatal("second activity inside the interval renewed again")
	}
	again, err := schedproc.ReadDecisionsTail(f.root)
	if err != nil || len(again) != 2 {
		t.Fatalf("second activity appended a decision: %+v err=%v", again, err)
	}
}

func TestRollbackRealRC1RewriteLeavesUnlabelledRenewals(t *testing.T) {
	bin := os.Getenv("AMQSQ_RC1_BIN")
	if bin == "" {
		t.Skip("set AMQSQ_RC1_BIN to the offline v3.2.0-rc.1 amq-squad binary")
	}
	f := newHookFixture(t)
	current := time.Now().UTC().Truncate(time.Second)
	hookNow = func() time.Time { return current }
	store, err := taskstore.Open(f.root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Add(taskstore.AddInput{Title: "real rc.1 rollback renewal", Class: "script"}, current.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim("t1", "cto", nil, 10*time.Minute, current.Add(-5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if !ingestToolResult(t, "cto") {
		t.Fatal("base activity renewal did not run")
	}
	version, err := exec.Command(bin, "version").CombinedOutput()
	if err != nil {
		t.Fatalf("rc.1 version: %v: %s", err, version)
	}
	renew := exec.Command(bin, "task", "renew", "--me", "cto", "--root", f.root)
	renew.Env = os.Environ()
	renewOut, err := renew.CombinedOutput()
	if err != nil {
		t.Fatalf("real rc.1 task renew: %v: %s", err, renewOut)
	}
	afterRC1, err := store.Get("t1")
	if err != nil || afterRC1.Lease == nil || afterRC1.Lease.Renewals != 2 || afterRC1.Lease.ActivityRenewals != 0 || afterRC1.Lease.ManualRenewals != 0 || afterRC1.Lease.LastRenewal != "" {
		t.Fatalf("real rc.1 rewrite = %+v err=%v output=%s", afterRC1.Lease, err, renewOut)
	}
	shown, err := taskCLI(t, "show", "t1")
	expires := afterRC1.Lease.ExpiresAt.UTC().Format(time.RFC3339)
	if err != nil || !strings.Contains(shown, "2 renewals (0 activity, 0 manual, 2 unlabelled)") || !strings.Contains(shown, "expires "+expires) {
		t.Fatalf("task show after real rc.1 rewrite = %q err=%v", shown, err)
	}
	current = afterRC1.Lease.RenewedAt.UTC().Add(ingestLeaseRenewInterval + time.Second)
	if !ingestToolResult(t, "cto") {
		t.Fatal("first activity after real rc.1 rewrite did not renew")
	}
	decisions, err := schedproc.ReadDecisionsTail(f.root)
	if err != nil || len(decisions) != 2 || decisions[1].Reason != "first activity renewal of attempt 1 after 2 unlabelled renewals" {
		t.Fatalf("real rc.1 post-rewrite decisions = %+v err=%v", decisions, err)
	}
	if ingestToolResult(t, "cto") {
		t.Fatal("second activity inside the interval renewed again")
	}
	t.Logf("rc.1 version: %s", strings.TrimSpace(string(version)))
	t.Logf("rc.1 renew: %s", strings.TrimSpace(string(renewOut)))
	t.Logf("current task show: %s", strings.TrimSpace(shown))
	t.Logf("current decision: %s", decisions[1].Reason)
}
