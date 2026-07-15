package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/compoundrelease"
	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

type cliReleaseReconcileAdapter struct {
	project, profile, session, root string
	overrides                       map[string][]byte
}

func (a *cliReleaseReconcileAdapter) ResolveSessionRoot(compoundrelease.Scope) (string, error) {
	return a.root, nil
}

func (a *cliReleaseReconcileAdapter) ExpectedReceiptPath(_ compoundrelease.Scope, attemptID string) (string, error) {
	root, dir, err := openReceiptDirRoot(a.project, a.profile, a.session, false)
	if err != nil {
		return "", err
	}
	defer root.Close()
	return filepath.Join(dir, attemptID+".json"), nil
}

func (a *cliReleaseReconcileAdapter) ReadReceipt(path string) ([]byte, error) {
	if raw, ok := a.overrides[path]; ok {
		return append([]byte(nil), raw...), nil
	}
	return os.ReadFile(path)
}

func (a *cliReleaseReconcileAdapter) ScanSessionMessages(root string, now func() time.Time) ([]state.Message, []state.Warning) {
	return state.ScanSessionMessages(root, now)
}

func (a *cliReleaseReconcileAdapter) InvokeReleaseChild(compoundrelease.ReleaseChildInvocation) compoundrelease.ReleaseChildInvokeOutcome {
	return compoundrelease.ReleaseChildInvokeOutcome{ProcessStarted: true, InvocationBegan: true}
}

type cliReleaseReceiptFixture struct {
	store      *compoundrelease.Store
	publishing compoundrelease.Snapshot
	adapter    *cliReleaseReconcileAdapter
	receipt    *deliveryReceiptData
	child      operatorauth.ReleaseChildPlan
}

func releaseReceiptTupleEqualForCLI(a, b operatorauth.ReleaseDeliveryReceiptTuple) bool {
	return reflect.DeepEqual(a, b)
}

func newCLIReleaseReceiptFixture(t *testing.T, writes int) cliReleaseReceiptFixture {
	t.Helper()
	project, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session := "issue-414"
	scope := compoundrelease.Scope{ProjectDir: project, Profile: team.DefaultProfile, Session: session, NamespaceGeneration: "none", ParentGate: "gate/release-414"}
	store, err := compoundrelease.Open(scope, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	spec := operatorauth.ReleaseSpec{
		SchemaVersion: operatorauth.ReleaseSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion,
		Namespace:  operatorauth.NamespaceBinding{ProjectDir: project, Profile: team.DefaultProfile, Session: session, NamespaceID: team.DefaultProfile + "/" + session, Generation: "none"},
		ParentGate: "gate/release-414", RequesterHandle: "cto", OperatorHandle: "user",
		TagTarget: "v2.20.1", GitHubReleaseTarget: "release v2.20.1 from exact commit deadbeef",
		Note: operatorauth.ReleaseNote{Summary: "publish accepted artifacts"},
	}
	planned, err := store.Create(spec)
	if err != nil {
		t.Fatal(err)
	}
	publishing, err := store.StartPublishing(planned.Pointer.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0); err != nil {
		t.Fatal(err)
	}
	child := publishing.Prepared.Children[0]
	root := filepath.Join(project, ".agent-mail", session)
	if err := os.MkdirAll(filepath.Join(root, "agents", child.Receipt.Recipient, "inbox", "new"), 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 15, 1, 0, 0, 0, time.UTC)
	receipt := &deliveryReceiptData{
		SchemaVersion: deliveryReceiptSchemaVersion, AttemptID: child.Receipt.AttemptID,
		Kind: child.Receipt.Kind, Method: "durable_amq", Status: "queued",
		Target:    deliveryReceiptTarget{ProjectDir: project, Profile: team.DefaultProfile, Session: session, NamespaceID: child.Receipt.NamespaceID, Role: child.Role, Handle: child.Receipt.Recipient},
		MessageID: "question-tag", Sender: child.Receipt.Sender, Recipient: child.Receipt.Recipient,
		Recipients: []string{child.Receipt.Recipient}, Consumers: []deliveryConsumerState{{Consumer: child.Receipt.Recipient, State: deliveryStateDeliveredNotDrained}},
		DeliveryState: deliveryStateDeliveredNotDrained, EvidenceSource: "amq_send_output", AMQInvoked: true,
		Root: root, Thread: child.Thread, Stages: []deliveryReceiptStage{{State: deliveryStateDeliveredNotDrained, At: now, Detail: "actual writer compatibility"}}, CreatedAt: now,
	}
	for i := 0; i < writes; i++ {
		if err := writeDeliveryReceipt(project, team.DefaultProfile, session, receipt); err != nil {
			t.Fatal(err)
		}
	}
	header := map[string]any{
		"schema": 1, "id": receipt.MessageID, "from": child.Receipt.Sender, "to": []string{child.Receipt.Recipient},
		"thread": child.Thread, "subject": child.Subject, "created": now.Format(time.RFC3339Nano), "priority": "normal", "kind": "question",
		"context": map[string]any{"authorization_request": child.AuthorizationRequest, "release_child": child.ReleaseChild},
	}
	headerBytes, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	message := append([]byte("---json\n"), headerBytes...)
	message = append(message, []byte("\n---\n"+child.Body+"\n")...)
	messagePath := filepath.Join(root, "agents", child.Receipt.Recipient, "inbox", "new", receipt.MessageID+".md")
	if err := os.WriteFile(messagePath, message, 0o600); err != nil {
		t.Fatal(err)
	}
	adapter := &cliReleaseReconcileAdapter{project: project, profile: team.DefaultProfile, session: session, root: root, overrides: map[string][]byte{}}
	return cliReleaseReceiptFixture{store: store, publishing: publishing, adapter: adapter, receipt: receipt, child: child}
}

func TestCompoundReleaseReconcileAcceptsCanonicalDeliveryReceiptWriterAndRejectsDrift(t *testing.T) {
	t.Run("actual writer and monotonic advance", func(t *testing.T) {
		fixture := newCLIReleaseReceiptFixture(t, 1)
		raw, err := os.ReadFile(fixture.receipt.Path)
		if err != nil || len(raw) == 0 {
			t.Fatalf("actual emitted receipt bytes=%d err=%v", len(raw), err)
		}
		result, err := fixture.store.Reconcile(fixture.publishing.Pointer.GenerationID, fixture.adapter)
		if err != nil || result.Disposition != compoundrelease.ReconcileInvoked || result.Role != operatorauth.ReleaseChildGitHubRelease || fixture.receipt.Generation != 1 {
			t.Fatalf("first reconcile=%+v receipt=%+v err=%v", result, fixture.receipt, err)
		}
		if err := writeDeliveryReceipt(fixture.adapter.project, fixture.adapter.profile, fixture.adapter.session, fixture.receipt); err != nil {
			t.Fatal(err)
		}
		if fixture.receipt.Generation != 2 {
			t.Fatalf("writer generation=%d, want 2", fixture.receipt.Generation)
		}
		child2 := fixture.publishing.Prepared.Children[1]
		now := time.Date(2026, 7, 15, 1, 0, 1, 0, time.UTC)
		receipt2 := &deliveryReceiptData{
			SchemaVersion: deliveryReceiptSchemaVersion, AttemptID: child2.Receipt.AttemptID,
			Kind: child2.Receipt.Kind, Method: "durable_amq", Status: "queued",
			Target:    deliveryReceiptTarget{ProjectDir: fixture.adapter.project, Profile: fixture.adapter.profile, Session: fixture.adapter.session, NamespaceID: child2.Receipt.NamespaceID, Role: child2.Role, Handle: child2.Receipt.Recipient},
			MessageID: "question-github-release", Sender: child2.Receipt.Sender, Recipient: child2.Receipt.Recipient,
			Recipients: []string{child2.Receipt.Recipient}, Consumers: []deliveryConsumerState{{Consumer: child2.Receipt.Recipient, State: deliveryStateDeliveredNotDrained}},
			DeliveryState: deliveryStateDeliveredNotDrained, EvidenceSource: "amq_send_output", AMQInvoked: true,
			Root: fixture.adapter.root, Thread: child2.Thread, Stages: []deliveryReceiptStage{{State: deliveryStateDeliveredNotDrained, At: now, Detail: "actual writer compatibility child two"}}, CreatedAt: now,
		}
		if err := writeDeliveryReceipt(fixture.adapter.project, fixture.adapter.profile, fixture.adapter.session, receipt2); err != nil {
			t.Fatal(err)
		}
		header := map[string]any{
			"schema": 1, "id": receipt2.MessageID, "from": child2.Receipt.Sender, "to": []string{child2.Receipt.Recipient},
			"thread": child2.Thread, "subject": child2.Subject, "created": now.Format(time.RFC3339Nano), "priority": "normal", "kind": "question",
			"context": map[string]any{"authorization_request": child2.AuthorizationRequest, "release_child": child2.ReleaseChild},
		}
		headerBytes, _ := json.Marshal(header)
		message := append([]byte("---json\n"), headerBytes...)
		message = append(message, []byte("\n---\n"+child2.Body+"\n")...)
		messagePath := filepath.Join(fixture.adapter.root, "agents", child2.Receipt.Recipient, "inbox", "new", receipt2.MessageID+".md")
		if err := os.WriteFile(messagePath, message, 0o600); err != nil {
			t.Fatal(err)
		}
		result, err = fixture.store.Reconcile(fixture.publishing.Pointer.GenerationID, fixture.adapter)
		if err != nil || result.Disposition != compoundrelease.ReconcileActivated || result.Snapshot.Active == nil {
			t.Fatalf("monotonic activation=%+v err=%v", result, err)
		}
		var tagActive *operatorauth.ActiveReleaseChild
		for i := range result.Snapshot.Active.Children {
			if result.Snapshot.Active.Children[i].Role == operatorauth.ReleaseChildTag {
				tagActive = &result.Snapshot.Active.Children[i]
			}
		}
		wantTag := operatorauth.ReleaseDeliveryReceiptTuple{
			AttemptID: fixture.child.Receipt.AttemptID, Kind: fixture.child.Receipt.Kind, Sender: fixture.child.Receipt.Sender,
			Recipients: []string{fixture.child.Receipt.Recipient}, Thread: fixture.child.Thread, MessageID: fixture.receipt.MessageID,
			Path: fixture.receipt.Path, Root: fixture.adapter.root, NamespaceID: fixture.child.Receipt.NamespaceID,
			TargetIdentity: fixture.child.Receipt.TargetIdentity, AdoptedGeneration: 1,
		}
		if tagActive == nil || tagActive.QuestionMessageID != fixture.receipt.MessageID || !releaseReceiptTupleEqualForCLI(tagActive.Receipt, wantTag) || fixture.receipt.Generation != 2 {
			t.Fatalf("active tag=%+v want=%+v live_generation=%d", tagActive, wantTag, fixture.receipt.Generation)
		}
	})

	mutations := map[string]func([]byte, *deliveryReceiptData) []byte{
		"duplicate key": func(raw []byte, _ *deliveryReceiptData) []byte {
			return []byte(strings.Replace(string(raw), `"schema_version": 2`, `"schema_version": 2, "schema_version": 2`, 1))
		},
		"schema": func(_ []byte, r *deliveryReceiptData) []byte { r.SchemaVersion = 1; b, _ := json.Marshal(r); return b },
		"attempt": func(_ []byte, r *deliveryReceiptData) []byte {
			r.AttemptID = "release-attempt-v2-" + strings.Repeat("a", 64)
			b, _ := json.Marshal(r)
			return b
		},
		"kind":   func(_ []byte, r *deliveryReceiptData) []byte { r.Kind += "-other"; b, _ := json.Marshal(r); return b },
		"sender": func(_ []byte, r *deliveryReceiptData) []byte { r.Sender = "other"; b, _ := json.Marshal(r); return b },
		"ordered recipients": func(_ []byte, r *deliveryReceiptData) []byte {
			r.Recipients = []string{"other", "user"}
			b, _ := json.Marshal(r)
			return b
		},
		"thread": func(_ []byte, r *deliveryReceiptData) []byte {
			r.Thread = "gate/other"
			b, _ := json.Marshal(r)
			return b
		},
		"message": func(_ []byte, r *deliveryReceiptData) []byte {
			r.MessageID = "other"
			b, _ := json.Marshal(r)
			return b
		},
		"path":       func(_ []byte, r *deliveryReceiptData) []byte { r.Path += ".other"; b, _ := json.Marshal(r); return b },
		"fresh root": func(_ []byte, r *deliveryReceiptData) []byte { r.Root += ".other"; b, _ := json.Marshal(r); return b },
		"namespace target": func(_ []byte, r *deliveryReceiptData) []byte {
			r.Target.NamespaceID = "other/session"
			b, _ := json.Marshal(r)
			return b
		},
		"amq invoked": func(_ []byte, r *deliveryReceiptData) []byte { r.AMQInvoked = false; b, _ := json.Marshal(r); return b },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			fixture := newCLIReleaseReceiptFixture(t, 1)
			raw, err := os.ReadFile(fixture.receipt.Path)
			if err != nil {
				t.Fatal(err)
			}
			copy := *fixture.receipt
			copy.Recipients = append([]string(nil), fixture.receipt.Recipients...)
			copy.Consumers = append([]deliveryConsumerState(nil), fixture.receipt.Consumers...)
			copy.Stages = append([]deliveryReceiptStage(nil), fixture.receipt.Stages...)
			fixture.adapter.overrides[fixture.receipt.Path] = mutate(raw, &copy)
			result, err := fixture.store.Reconcile(fixture.publishing.Pointer.GenerationID, fixture.adapter)
			if err != nil || result.Disposition != compoundrelease.ReconcileConflict || result.Role != operatorauth.ReleaseChildTag {
				t.Fatalf("mutation %s result=%+v err=%v", name, result, err)
			}
		})
	}

	t.Run("generation rollback", func(t *testing.T) {
		fixture := newCLIReleaseReceiptFixture(t, 2)
		result, err := fixture.store.Reconcile(fixture.publishing.Pointer.GenerationID, fixture.adapter)
		if err != nil || result.Disposition != compoundrelease.ReconcileInvoked || fixture.receipt.Generation != 2 {
			t.Fatalf("adopt generation two result=%+v receipt=%+v err=%v", result, fixture.receipt, err)
		}
		rolledBack := *fixture.receipt
		rolledBack.Generation = 1
		raw, _ := json.Marshal(rolledBack)
		fixture.adapter.overrides[fixture.receipt.Path] = raw
		result, err = fixture.store.Reconcile(fixture.publishing.Pointer.GenerationID, fixture.adapter)
		if err != nil || result.Disposition != compoundrelease.ReconcileConflict {
			t.Fatalf("rollback result=%+v err=%v", result, err)
		}
	})
}
