package compoundrelease

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
	"github.com/omriariav/amq-squad/v2/internal/state"
)

type fakeReconcileAdapter struct {
	root            string
	receiptPaths    map[string]string
	receiptPathErrs map[string]error
	receipts        map[string][]byte
	receiptErrs     map[string]error
	outcome         ReleaseChildInvokeOutcome
	resolved        int
	reads           int
	scans           int
	order           []string
	invocations     []ReleaseChildInvocation
}

func (f *fakeReconcileAdapter) ResolveSessionRoot(Scope) (string, error) {
	f.resolved++
	f.order = append(f.order, "root")
	return f.root, nil
}

func (f *fakeReconcileAdapter) ExpectedReceiptPath(_ Scope, attemptID string) (string, error) {
	f.order = append(f.order, "path:"+attemptID)
	if err, ok := f.receiptPathErrs[attemptID]; ok {
		return "", err
	}
	return f.receiptPaths[attemptID], nil
}

func (f *fakeReconcileAdapter) ReadReceipt(path string) ([]byte, error) {
	f.reads++
	f.order = append(f.order, "read:"+path)
	if err, ok := f.receiptErrs[path]; ok {
		return nil, err
	}
	raw, ok := f.receipts[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return append([]byte(nil), raw...), nil
}

func (f *fakeReconcileAdapter) ScanSessionMessages(root string, now func() time.Time) ([]state.Message, []state.Warning) {
	f.scans++
	f.order = append(f.order, "scan")
	return state.ScanSessionMessages(root, now)
}

func (f *fakeReconcileAdapter) InvokeReleaseChild(invocation ReleaseChildInvocation) ReleaseChildInvokeOutcome {
	f.invocations = append(f.invocations, invocation)
	return f.outcome
}

func reconcileFixture(t *testing.T) (*Store, Snapshot, *fakeReconcileAdapter) {
	t.Helper()
	scope := testScope(t)
	store := openTestStore(t, scope)
	planned, err := store.Create(specForScope(scope))
	if err != nil {
		t.Fatal(err)
	}
	publishing, err := store.StartPublishing(planned.Pointer.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(scope.ProjectDir, ".agent-mail", scope.Session)
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	adapter := &fakeReconcileAdapter{
		root: root, receiptPaths: map[string]string{}, receiptPathErrs: map[string]error{}, receipts: map[string][]byte{}, receiptErrs: map[string]error{},
	}
	for _, child := range publishing.Prepared.Children {
		adapter.receiptPaths[child.Receipt.AttemptID] = filepath.Join(scope.ProjectDir, ".amq-squad", "receipts", child.Receipt.AttemptID+".json")
	}
	return store, publishing, adapter
}

type releaseMessageHeader struct {
	Schema       int            `json:"schema"`
	ID           string         `json:"id"`
	From         string         `json:"from"`
	To           []string       `json:"to"`
	Thread       string         `json:"thread"`
	Subject      string         `json:"subject"`
	Created      string         `json:"created"`
	Priority     string         `json:"priority"`
	Kind         string         `json:"kind"`
	ReplyTo      string         `json:"reply_to,omitempty"`
	Orchestrator string         `json:"orchestrator,omitempty"`
	FromProject  string         `json:"from_project,omitempty"`
	ReplyProject string         `json:"reply_project,omitempty"`
	Context      map[string]any `json:"context"`
}

func writeReleaseQuestion(t *testing.T, root, owner, mailbox, id, created string, child operatorauth.ReleaseChildPlan, mutate func(*releaseMessageHeader)) {
	t.Helper()
	header := releaseMessageHeader{
		Schema: 1, ID: id, From: child.Receipt.Sender, To: []string{child.Receipt.Recipient},
		Thread: child.Thread, Subject: child.Subject, Created: created, Priority: "normal", Kind: "question",
		Context: map[string]any{"authorization_request": child.AuthorizationRequest, "release_child": child.ReleaseChild},
	}
	if mutate != nil {
		mutate(&header)
	}
	b, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "agents", owner, "inbox", mailbox)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data := append([]byte("---json\n"), b...)
	data = append(data, []byte("\n---\n"+child.Body+"\n")...)
	if err := os.WriteFile(filepath.Join(dir, id+".md"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func rawReleaseReceipt(t *testing.T, scope Scope, child operatorauth.ReleaseChildPlan, path, root, messageID string) []byte {
	t.Helper()
	now := time.Date(2026, 7, 15, 1, 0, 0, 0, time.UTC)
	deliveryState := "delivered_not_drained"
	invoked := true
	if messageID == "" {
		deliveryState = "ambiguous_unknown"
		invoked = false
	}
	receipt := releaseDeliveryReceiptV2{
		SchemaVersion: deliveryReceiptSchemaV2, Generation: 1, AttemptID: child.Receipt.AttemptID,
		Kind: child.Receipt.Kind, Method: "durable_amq", Status: "queued",
		Target:    releaseDeliveryTargetV2{ProjectDir: scope.ProjectDir, Profile: scope.Profile, Session: scope.Session, NamespaceID: child.Receipt.NamespaceID, Role: child.Role, Handle: child.Receipt.Recipient},
		MessageID: messageID, Sender: child.Receipt.Sender, Recipient: child.Receipt.Recipient,
		Recipients:    []string{child.Receipt.Recipient},
		Consumers:     []releaseDeliveryConsumerV2{{Consumer: child.Receipt.Recipient, State: deliveryState}},
		DeliveryState: deliveryState, EvidenceSource: "amq_send_output", AMQInvoked: invoked,
		Root: root, Thread: child.Thread, Stages: []releaseDeliveryStageV2{{State: deliveryState, At: now, Detail: "current writer fixture"}},
		Path: path, CreatedAt: now,
	}
	b, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func receiptAtGeneration(t *testing.T, raw []byte, generation uint64) []byte {
	t.Helper()
	var receipt releaseDeliveryReceiptV2
	if err := json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	receipt.Generation = generation
	b, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func stageAllReconcileEvidence(t *testing.T, store *Store, publishing Snapshot, adapter *fakeReconcileAdapter) {
	t.Helper()
	for i, child := range publishing.Prepared.Children {
		path := adapter.receiptPaths[child.Receipt.AttemptID]
		raw := rawReleaseReceipt(t, store.scope, child, path, adapter.root, "question-"+child.Role)
		bound, err := decodeBoundReleaseReceiptV2(raw, store.scope, child, path, adapter.root)
		if err != nil || bound.Tuple == nil {
			t.Fatalf("child %d bound=%+v err=%v", i, bound, err)
		}
		if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, i); err != nil {
			t.Fatal(err)
		}
		if err := store.AdoptChildPublication(publishing.Pointer.GenerationID, i, *bound.Tuple); err != nil {
			t.Fatal(err)
		}
		writeReleaseQuestion(t, adapter.root, child.Receipt.Recipient, "new", "question-"+child.Role, "2026-07-15T01:00:0"+string(rune('0'+i))+"Z", child, nil)
		adapter.receipts[path] = raw
	}
}

func installReconcileEvidence(t *testing.T, store *Store, publishing Snapshot, adapter *fakeReconcileAdapter, ordinal int) operatorauth.ReleaseDeliveryReceiptTuple {
	t.Helper()
	child := publishing.Prepared.Children[ordinal]
	path := adapter.receiptPaths[child.Receipt.AttemptID]
	raw := rawReleaseReceipt(t, store.scope, child, path, adapter.root, "question-"+child.Role)
	bound, err := decodeBoundReleaseReceiptV2(raw, store.scope, child, path, adapter.root)
	if err != nil || bound.Tuple == nil {
		t.Fatalf("child %d bound=%+v err=%v", ordinal, bound, err)
	}
	writeReleaseQuestion(t, adapter.root, child.Receipt.Recipient, "new", "question-"+child.Role, "2026-07-15T01:01:0"+string(rune('0'+ordinal))+"Z", child, nil)
	adapter.receipts[path] = raw
	return *bound.Tuple
}

func TestStrictReleaseReceiptV2Decode(t *testing.T) {
	store, publishing, adapter := reconcileFixture(t)
	child := publishing.Prepared.Children[0]
	path := adapter.receiptPaths[child.Receipt.AttemptID]
	raw := rawReleaseReceipt(t, store.scope, child, path, adapter.root, "question-tag")
	bound, err := decodeBoundReleaseReceiptV2(raw, store.scope, child, path, adapter.root)
	if err != nil || bound.Tuple == nil || bound.Tuple.MessageID != "question-tag" || bound.Tuple.Path != path || bound.Tuple.Root != adapter.root || bound.Tuple.TargetIdentity != child.Receipt.TargetIdentity {
		t.Fatalf("bound=%+v err=%v", bound, err)
	}
	var diagnostic releaseDeliveryReceiptV2
	if err := json.Unmarshal(raw, &diagnostic); err != nil {
		t.Fatal(err)
	}
	diagnostic.Status = "future-diagnostic-status"
	diagnostic.DeliveryState = "future-diagnostic-state"
	diagnostic.Consumers = nil
	diagnostic.Stages = nil
	diagnosticRaw, _ := json.Marshal(diagnostic)
	if _, err := decodeBoundReleaseReceiptV2(diagnosticRaw, store.scope, child, path, adapter.root); err != nil {
		t.Fatalf("mutable diagnostic projection rejected: %v", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatal(err)
	}
	unknown := append([]byte(nil), raw[:len(raw)-1]...)
	unknown = append(unknown, []byte(`,"unknown":true}`)...)
	for name, malformed := range map[string][]byte{
		"unknown":          unknown,
		"duplicate":        []byte(strings.Replace(string(raw), `"schema_version":2`, `"schema_version":2,"schema_version":2`, 1)),
		"wrong type":       []byte(strings.Replace(string(raw), `"generation":1`, `"generation":"1"`, 1)),
		"trailing":         append(append([]byte(nil), raw...), []byte(` null`)...),
		"unsupported":      []byte(strings.Replace(string(raw), `"schema_version":2`, `"schema_version":1`, 1)),
		"invalid utf8":     append(append([]byte(nil), raw...), 0xff),
		"unknown nested":   []byte(strings.Replace(string(raw), `"profile":"default"`, `"profile":"default","unknown":true`, 1)),
		"wrong method":     []byte(strings.Replace(string(raw), `"method":"durable_amq"`, `"method":"other"`, 1)),
		"target mode":      []byte(strings.Replace(string(raw), `"handle":"user"`, `"handle":"user","execution_mode":"tmux"`, 1)),
		"task provenance":  []byte(strings.Replace(string(raw), `"amq_invoked":true`, `"amq_invoked":true,"task_id":"task-1"`, 1)),
		"fallback":         []byte(strings.Replace(string(raw), `"fallback":false`, `"fallback":true`, 1)),
		"id before invoke": []byte(strings.Replace(string(raw), `"amq_invoked":true`, `"amq_invoked":false`, 1)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeBoundReleaseReceiptV2(malformed, store.scope, child, path, adapter.root); err == nil {
				t.Fatalf("malformed %s receipt accepted", name)
			}
		})
	}
}

func TestReconcileDedupesExactPhysicalCopiesAndPublishes(t *testing.T) {
	store, publishing, adapter := reconcileFixture(t)
	child := publishing.Prepared.Children[0]
	if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0); err != nil {
		t.Fatal(err)
	}
	created := "2026-07-15T01:00:00Z"
	writeReleaseQuestion(t, adapter.root, child.Receipt.Recipient, "cur", "question-tag", created, child, nil)
	writeReleaseQuestion(t, adapter.root, child.Receipt.Recipient, "new", "question-tag", created, child, nil)
	path := adapter.receiptPaths[child.Receipt.AttemptID]
	adapter.receipts[path] = rawReleaseReceipt(t, store.scope, child, path, adapter.root, "question-tag")
	adapter.outcome = ReleaseChildInvokeOutcome{ProcessStarted: true, InvocationBegan: true}
	result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
	if err != nil || result.Disposition != ReconcileInvoked || result.Role != operatorauth.ReleaseChildGitHubRelease || adapter.resolved != 1 || adapter.reads != 2 || adapter.scans != 1 || len(adapter.invocations) != 1 {
		t.Fatalf("result=%+v adapter=%+v err=%v", result, adapter, err)
	}
	if len(adapter.order) != 6 || !strings.HasPrefix(adapter.order[1], "path:") || !strings.HasPrefix(adapter.order[2], "read:") || !strings.HasPrefix(adapter.order[3], "path:") || !strings.HasPrefix(adapter.order[4], "read:") || adapter.order[5] != "scan" {
		t.Fatalf("receipt-first order=%v", adapter.order)
	}
	record := mustGenerationRecord(t, store, 1)
	if record.Children[0].State != childPublicationPublished || record.Children[0].QuestionMessageID != "question-tag" || record.Children[0].Receipt == nil || record.Children[1].State != childPublicationSending {
		t.Fatalf("tag publication=%+v", record.Children[0])
	}
}

func TestReconcileUnequalSameIDCopiesTerminalizeConflict(t *testing.T) {
	store, publishing, adapter := reconcileFixture(t)
	child := publishing.Prepared.Children[0]
	created := "2026-07-15T01:00:00Z"
	writeReleaseQuestion(t, adapter.root, child.Receipt.Recipient, "cur", "question-tag", created, child, nil)
	writeReleaseQuestion(t, adapter.root, child.Receipt.Recipient, "new", "question-tag", created, child, func(header *releaseMessageHeader) {
		header.Subject += " changed"
	})
	result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
	if err != nil || result.Disposition != ReconcileConflict || result.Snapshot.Pointer.State != operatorauth.ReleaseStateConflict || len(adapter.invocations) != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	record := mustGenerationRecord(t, store, 1)
	if record.Children[0].ConflictReason != "message evidence targets release child but is not exact" || len(record.Children[0].ObservedMessageIDs) != 1 || record.Children[0].ObservedMessageIDs[0] != "question-tag" {
		t.Fatalf("conflict=%+v", record.Children[0])
	}
}

func TestReconcileWarningIsBarrierBeforeClaimOrInvoke(t *testing.T) {
	store, publishing, adapter := reconcileFixture(t)
	dir := filepath.Join(adapter.root, "agents", "user", "inbox", "new")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "torn.md"), []byte("not frontmatter"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
	if err == nil || result.Disposition != ReconcileAmbiguous || len(adapter.invocations) != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	record := mustGenerationRecord(t, store, 1)
	if record.Children[0].State != childPublicationPlanned {
		t.Fatalf("warning mutated child: %+v", record.Children[0])
	}
}

func TestReconcilePreProcessFailureIsOnlyRollbackLane(t *testing.T) {
	store, publishing, adapter := reconcileFixture(t)
	adapter.outcome = ReleaseChildInvokeOutcome{Err: errors.New("exec unavailable")}
	result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
	if err == nil || result.Disposition != ReconcileAmbiguous || len(adapter.invocations) != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	record := mustGenerationRecord(t, store, 1)
	if record.Children[0].State != childPublicationPlanned || record.Children[0].ClaimRevision != 1 || record.Children[0].ClaimToken != "" {
		t.Fatalf("pre-process rollback child=%+v", record.Children[0])
	}
}

func TestReconcilePlannedAndSendingEvidenceMatrix(t *testing.T) {
	t.Run("planned reserved receipt conflicts", func(t *testing.T) {
		store, publishing, adapter := reconcileFixture(t)
		child := publishing.Prepared.Children[0]
		path := adapter.receiptPaths[child.Receipt.AttemptID]
		adapter.receipts[path] = rawReleaseReceipt(t, store.scope, child, path, adapter.root, "")
		result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
		if err != nil || result.Disposition != ReconcileConflict || result.Role != child.Role {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})

	t.Run("planned exact message conflicts without retro claim", func(t *testing.T) {
		store, publishing, adapter := reconcileFixture(t)
		child := publishing.Prepared.Children[0]
		writeReleaseQuestion(t, adapter.root, child.Receipt.Recipient, "new", "question-tag", "2026-07-15T01:00:00Z", child, nil)
		result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
		if err != nil || result.Disposition != ReconcileConflict {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		record := mustGenerationRecord(t, store, 1)
		if record.Children[0].ClaimRevision != 0 || record.Children[0].ClaimToken != "" {
			t.Fatalf("planned evidence was retro-claimed: %+v", record.Children[0])
		}
	})

	t.Run("sending reserved receipt remains ambiguous", func(t *testing.T) {
		store, publishing, adapter := reconcileFixture(t)
		child := publishing.Prepared.Children[0]
		claim, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0)
		if err != nil {
			t.Fatal(err)
		}
		path := adapter.receiptPaths[child.Receipt.AttemptID]
		adapter.receipts[path] = rawReleaseReceipt(t, store.scope, child, path, adapter.root, "")
		result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
		if err == nil || result.Disposition != ReconcileAmbiguous || len(adapter.invocations) != 0 {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		record := mustGenerationRecord(t, store, 1)
		if record.Children[0].State != childPublicationSending || record.Children[0].ClaimToken != claim.Token {
			t.Fatalf("sending claim changed: %+v", record.Children[0])
		}
	})

	t.Run("invoked receipt without exact message remains ambiguous", func(t *testing.T) {
		store, publishing, adapter := reconcileFixture(t)
		child := publishing.Prepared.Children[0]
		if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0); err != nil {
			t.Fatal(err)
		}
		path := adapter.receiptPaths[child.Receipt.AttemptID]
		adapter.receipts[path] = rawReleaseReceipt(t, store.scope, child, path, adapter.root, "missing-question")
		result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
		if err == nil || result.Disposition != ReconcileAmbiguous || len(adapter.invocations) != 0 {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})
}

func TestReconcileReleaseMessageRelevanceAndOwnership(t *testing.T) {
	t.Run("sender-only sending copy is uncertain", func(t *testing.T) {
		store, publishing, adapter := reconcileFixture(t)
		child := publishing.Prepared.Children[0]
		if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0); err != nil {
			t.Fatal(err)
		}
		writeReleaseQuestion(t, adapter.root, child.Receipt.Sender, "new", "question-tag", "2026-07-15T01:00:00Z", child, nil)
		result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
		if err == nil || result.Disposition != ReconcileAmbiguous || len(adapter.invocations) != 0 {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})

	t.Run("sender-only planned copy does not claim", func(t *testing.T) {
		store, publishing, adapter := reconcileFixture(t)
		child := publishing.Prepared.Children[0]
		writeReleaseQuestion(t, adapter.root, child.Receipt.Sender, "new", "question-tag", "2026-07-15T01:00:00Z", child, nil)
		result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
		if err == nil || result.Disposition != ReconcileAmbiguous || len(adapter.invocations) != 0 {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		record := mustGenerationRecord(t, store, 1)
		if record.Children[0].State != childPublicationPlanned || record.Children[0].ClaimRevision != 0 {
			t.Fatalf("sender-only copy caused claim: %+v", record.Children[0])
		}
	})

	t.Run("unrelated unequal duplicate is inert", func(t *testing.T) {
		store, publishing, adapter := reconcileFixture(t)
		child := publishing.Prepared.Children[0]
		mutate := func(subject string) func(*releaseMessageHeader) {
			return func(header *releaseMessageHeader) {
				header.Thread = "p2p/unrelated__user"
				header.Subject = subject
				header.Context = map[string]any{}
			}
		}
		writeReleaseQuestion(t, adapter.root, child.Receipt.Recipient, "new", "unrelated-id", "2026-07-15T01:00:00Z", child, mutate("unrelated one"))
		writeReleaseQuestion(t, adapter.root, child.Receipt.Recipient, "cur", "unrelated-id", "2026-07-15T01:00:00Z", child, mutate("unrelated two"))
		adapter.outcome = ReleaseChildInvokeOutcome{ProcessStarted: true, InvocationBegan: true}
		result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
		if err != nil || result.Disposition != ReconcileInvoked || result.Role != operatorauth.ReleaseChildTag {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})

	t.Run("old generation marker is inert", func(t *testing.T) {
		store, publishing, adapter := reconcileFixture(t)
		child := publishing.Prepared.Children[0]
		writeReleaseQuestion(t, adapter.root, child.Receipt.Recipient, "new", "old-question", "2026-07-15T01:00:00Z", child, func(header *releaseMessageHeader) {
			header.Thread = "gate/old-generation"
			header.Subject = "APPROVAL: old-generation"
			old := child.ReleaseChild
			old.Generation++
			old.GenerationID = "release-generation-v1-" + strings.Repeat("a", 64)
			old.PreparedManifestID = "release-prepared-v1-" + strings.Repeat("b", 64)
			old.Thread = header.Thread
			old.AttemptID = "release-attempt-v2-" + strings.Repeat("c", 64)
			header.Context["release_child"] = old
		})
		adapter.outcome = ReleaseChildInvokeOutcome{ProcessStarted: true, InvocationBegan: true}
		result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
		if err != nil || result.Disposition != ReconcileInvoked {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})
}

func TestReconcileEarlySecondChildEvidenceConflictsBeforeTagInvoke(t *testing.T) {
	store, publishing, adapter := reconcileFixture(t)
	child := publishing.Prepared.Children[1]
	writeReleaseQuestion(t, adapter.root, child.Receipt.Recipient, "new", "question-release", "2026-07-15T01:00:00Z", child, nil)
	path := adapter.receiptPaths[child.Receipt.AttemptID]
	adapter.receipts[path] = rawReleaseReceipt(t, store.scope, child, path, adapter.root, "question-release")
	result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
	if err != nil || result.Disposition != ReconcileConflict || result.Role != operatorauth.ReleaseChildGitHubRelease || len(adapter.invocations) != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestReconcilePublishedChildMustRevalidateBeforeSecondClaim(t *testing.T) {
	store, publishing, adapter := reconcileFixture(t)
	child := publishing.Prepared.Children[0]
	path := adapter.receiptPaths[child.Receipt.AttemptID]
	raw := rawReleaseReceipt(t, store.scope, child, path, adapter.root, "question-tag")
	bound, err := decodeBoundReleaseReceiptV2(raw, store.scope, child, path, adapter.root)
	if err != nil || bound.Tuple == nil {
		t.Fatalf("bound=%+v err=%v", bound, err)
	}
	if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.AdoptChildPublication(publishing.Pointer.GenerationID, 0, *bound.Tuple); err != nil {
		t.Fatal(err)
	}
	result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
	if err == nil || result.Disposition != ReconcileAmbiguous || len(adapter.invocations) != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	record := mustGenerationRecord(t, store, 1)
	if record.Children[1].State != childPublicationPlanned || record.Children[1].ClaimRevision != 0 {
		t.Fatalf("second child claimed before tag revalidation: %+v", record.Children[1])
	}
}

func TestReconcilePublishedChildMissingReceiptIsAmbiguousUntilRestored(t *testing.T) {
	store, publishing, adapter := reconcileFixture(t)
	child := publishing.Prepared.Children[0]
	path := adapter.receiptPaths[child.Receipt.AttemptID]
	receipt := installReconcileEvidence(t, store, publishing, adapter, 0)
	digest, err := operatorauth.ReleaseDeliveryReceiptSHA256(receipt)
	if err != nil {
		t.Fatal(err)
	}
	raw := append([]byte(nil), adapter.receipts[path]...)
	if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.AdoptChildPublication(publishing.Pointer.GenerationID, 0, receipt); err != nil {
		t.Fatal(err)
	}
	delete(adapter.receipts, path)
	result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
	if err == nil || result.Disposition != ReconcileAmbiguous || len(adapter.invocations) != 0 {
		t.Fatalf("missing receipt result=%+v invocations=%d err=%v", result, len(adapter.invocations), err)
	}
	current, err := store.ReadCurrent()
	if err != nil || current.Pointer.State != operatorauth.ReleaseStatePublishing {
		t.Fatalf("missing receipt pointer=%+v err=%v", current.Pointer, err)
	}
	record := mustGenerationRecord(t, store, 1)
	if record.State != operatorauth.ReleaseStatePublishing || record.Children[0].State != childPublicationPublished || record.Children[0].Receipt == nil || !deliveryReceiptTupleEqual(*record.Children[0].Receipt, receipt) || record.Children[0].ReceiptSHA256 != digest || record.Children[1].State != childPublicationPlanned || record.Children[0].ConflictReason != "" {
		t.Fatalf("missing receipt mutated lifecycle: %+v", record)
	}

	adapter.receipts[path] = raw
	adapter.outcome = ReleaseChildInvokeOutcome{ProcessStarted: true, InvocationBegan: true}
	result, err = store.Reconcile(publishing.Pointer.GenerationID, adapter)
	if err != nil || result.Disposition != ReconcileInvoked || result.Role != operatorauth.ReleaseChildGitHubRelease || len(adapter.invocations) != 1 {
		t.Fatalf("restored receipt result=%+v invocations=%d err=%v", result, len(adapter.invocations), err)
	}
	record = mustGenerationRecord(t, store, 1)
	if record.Children[0].State != childPublicationPublished || record.Children[0].Receipt == nil || !deliveryReceiptTupleEqual(*record.Children[0].Receipt, receipt) || record.Children[0].ReceiptSHA256 != digest || record.Children[1].State != childPublicationSending {
		t.Fatalf("restored receipt continuation: %+v", record)
	}
}

func TestReconcileTransactionWideAmbiguityBarrier(t *testing.T) {
	assertUnchangedPublishing := func(t *testing.T, store *Store, before generationRecord, invocations int, adapter *fakeReconcileAdapter) {
		t.Helper()
		after := mustGenerationRecord(t, store, before.Generation)
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("ambiguous reconcile mutated generation\nbefore=%+v\nafter=%+v", before, after)
		}
		current, err := store.ReadCurrent()
		if err != nil || current.Pointer.State != operatorauth.ReleaseStatePublishing {
			t.Fatalf("ambiguous pointer=%+v err=%v", current.Pointer, err)
		}
		if len(adapter.invocations) != invocations {
			t.Fatalf("ambiguous reconcile invocations=%d want=%d", len(adapter.invocations), invocations)
		}
		if _, err := os.Stat(filepath.Join(store.dirPath, store.activeName(before.Generation))); !os.IsNotExist(err) {
			t.Fatalf("ambiguous reconcile wrote active artifact: %v", err)
		}
	}

	t.Run("missing tag receipt blocks adoptable second child", func(t *testing.T) {
		store, publishing, adapter := reconcileFixture(t)
		tag := publishing.Prepared.Children[0]
		tagPath := adapter.receiptPaths[tag.Receipt.AttemptID]
		tagReceipt := installReconcileEvidence(t, store, publishing, adapter, 0)
		tagRaw := append([]byte(nil), adapter.receipts[tagPath]...)
		if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0); err != nil {
			t.Fatal(err)
		}
		if err := store.AdoptChildPublication(publishing.Pointer.GenerationID, 0, tagReceipt); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 1); err != nil {
			t.Fatal(err)
		}
		_ = installReconcileEvidence(t, store, publishing, adapter, 1)
		delete(adapter.receipts, tagPath)
		before := mustGenerationRecord(t, store, 1)
		result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
		if err == nil || result.Disposition != ReconcileAmbiguous || result.Role != operatorauth.ReleaseChildTag {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		assertUnchangedPublishing(t, store, before, 0, adapter)

		adapter.receipts[tagPath] = tagRaw
		result, err = store.Reconcile(publishing.Pointer.GenerationID, adapter)
		if err != nil || result.Disposition != ReconcileActivated || result.Snapshot.Pointer.State != operatorauth.ReleaseStateActive || len(adapter.invocations) != 0 {
			t.Fatalf("restored result=%+v invocations=%d err=%v", result, len(adapter.invocations), err)
		}
	})

	t.Run("missing tag receipt blocks activation of both published children", func(t *testing.T) {
		store, publishing, adapter := reconcileFixture(t)
		stageAllReconcileEvidence(t, store, publishing, adapter)
		tag := publishing.Prepared.Children[0]
		tagPath := adapter.receiptPaths[tag.Receipt.AttemptID]
		tagRaw := append([]byte(nil), adapter.receipts[tagPath]...)
		delete(adapter.receipts, tagPath)
		before := mustGenerationRecord(t, store, 1)
		result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
		if err == nil || result.Disposition != ReconcileAmbiguous || result.Role != operatorauth.ReleaseChildTag {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		assertUnchangedPublishing(t, store, before, 0, adapter)

		adapter.receipts[tagPath] = tagRaw
		result, err = store.Reconcile(publishing.Pointer.GenerationID, adapter)
		if err != nil || result.Disposition != ReconcileActivated || result.Snapshot.Pointer.State != operatorauth.ReleaseStateActive || len(adapter.invocations) != 0 {
			t.Fatalf("restored result=%+v invocations=%d err=%v", result, len(adapter.invocations), err)
		}
	})

	t.Run("ambiguous second child blocks adoptable first child", func(t *testing.T) {
		store, publishing, adapter := reconcileFixture(t)
		if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0); err != nil {
			t.Fatal(err)
		}
		_ = installReconcileEvidence(t, store, publishing, adapter, 0)
		second := publishing.Prepared.Children[1]
		writeReleaseQuestion(t, adapter.root, second.Receipt.Sender, "new", "question-"+second.Role, "2026-07-15T01:02:00Z", second, nil)
		before := mustGenerationRecord(t, store, 1)
		result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
		if err == nil || result.Disposition != ReconcileAmbiguous || result.Role != operatorauth.ReleaseChildGitHubRelease {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		assertUnchangedPublishing(t, store, before, 0, adapter)

		senderCopy := filepath.Join(adapter.root, "agents", second.Receipt.Sender, "inbox", "new", "question-"+second.Role+".md")
		if err := os.Remove(senderCopy); err != nil {
			t.Fatal(err)
		}
		adapter.outcome = ReleaseChildInvokeOutcome{ProcessStarted: true, InvocationBegan: true}
		result, err = store.Reconcile(publishing.Pointer.GenerationID, adapter)
		if err != nil || result.Disposition != ReconcileInvoked || result.Role != operatorauth.ReleaseChildGitHubRelease || len(adapter.invocations) != 1 {
			t.Fatalf("restored result=%+v invocations=%d err=%v", result, len(adapter.invocations), err)
		}
		record := mustGenerationRecord(t, store, 1)
		if record.Children[0].State != childPublicationPublished || record.Children[1].State != childPublicationSending {
			t.Fatalf("restored safe progress=%+v", record)
		}
	})
}

func TestReconcilePublishedReceiptGenerationIsMonotonic(t *testing.T) {
	for name, liveGeneration := range map[string]uint64{"equal accepted before second claim": 1, "advance accepted before second claim": 2} {
		t.Run(name, func(t *testing.T) {
			store, publishing, adapter := reconcileFixture(t)
			child := publishing.Prepared.Children[0]
			path := adapter.receiptPaths[child.Receipt.AttemptID]
			raw := rawReleaseReceipt(t, store.scope, child, path, adapter.root, "question-tag")
			bound, err := decodeBoundReleaseReceiptV2(raw, store.scope, child, path, adapter.root)
			if err != nil || bound.Tuple == nil {
				t.Fatalf("bound=%+v err=%v", bound, err)
			}
			if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0); err != nil {
				t.Fatal(err)
			}
			if err := store.AdoptChildPublication(publishing.Pointer.GenerationID, 0, *bound.Tuple); err != nil {
				t.Fatal(err)
			}
			writeReleaseQuestion(t, adapter.root, child.Receipt.Recipient, "new", "question-tag", "2026-07-15T01:00:00Z", child, nil)
			adapter.receipts[path] = receiptAtGeneration(t, raw, liveGeneration)
			adapter.outcome = ReleaseChildInvokeOutcome{ProcessStarted: true, InvocationBegan: true}
			result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
			if err != nil || result.Disposition != ReconcileInvoked || result.Role != operatorauth.ReleaseChildGitHubRelease {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			record := mustGenerationRecord(t, store, 1)
			if record.Children[0].Receipt.AdoptedGeneration != 1 {
				t.Fatalf("reread replaced immutable adopted generation: %+v", record.Children[0].Receipt)
			}
		})
	}

	t.Run("rollback rejected", func(t *testing.T) {
		store, publishing, adapter := reconcileFixture(t)
		child := publishing.Prepared.Children[0]
		path := adapter.receiptPaths[child.Receipt.AttemptID]
		raw := receiptAtGeneration(t, rawReleaseReceipt(t, store.scope, child, path, adapter.root, "question-tag"), 2)
		bound, err := decodeBoundReleaseReceiptV2(raw, store.scope, child, path, adapter.root)
		if err != nil || bound.Tuple == nil {
			t.Fatalf("bound=%+v err=%v", bound, err)
		}
		if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0); err != nil {
			t.Fatal(err)
		}
		if err := store.AdoptChildPublication(publishing.Pointer.GenerationID, 0, *bound.Tuple); err != nil {
			t.Fatal(err)
		}
		writeReleaseQuestion(t, adapter.root, child.Receipt.Recipient, "new", "question-tag", "2026-07-15T01:00:00Z", child, nil)
		adapter.receipts[path] = receiptAtGeneration(t, raw, 1)
		result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
		if err != nil || result.Disposition != ReconcileConflict || result.Role != child.Role {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})
}

func TestReconcileActivatesOnlyAfterBothFreshEvidenceSetsValidate(t *testing.T) {
	store, publishing, adapter := reconcileFixture(t)
	stageAllReconcileEvidence(t, store, publishing, adapter)
	for path, raw := range adapter.receipts {
		adapter.receipts[path] = receiptAtGeneration(t, raw, 2)
	}
	result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
	if err != nil || result.Disposition != ReconcileActivated || result.Snapshot.Pointer.State != operatorauth.ReleaseStateActive || result.Snapshot.Active == nil {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	resolved, reads, scans := adapter.resolved, adapter.reads, adapter.scans
	again, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
	if err != nil || again.Disposition != ReconcileActivated || adapter.resolved != resolved || adapter.reads != reads || adapter.scans != scans {
		t.Fatalf("terminal replay=%+v err=%v adapter=%+v", again, err, adapter)
	}
}

func TestReconcileActivationFaultSeamsRecover(t *testing.T) {
	for _, stage := range []string{"active-write", "pointer-update"} {
		t.Run(stage, func(t *testing.T) {
			store, publishing, adapter := reconcileFixture(t)
			stageAllReconcileEvidence(t, store, publishing, adapter)
			oldFault := reconcileFault
			fired := false
			reconcileFault = func(got string) error {
				if got == stage && !fired {
					fired = true
					return errors.New("fault " + stage)
				}
				return nil
			}
			t.Cleanup(func() { reconcileFault = oldFault })
			if _, err := store.Reconcile(publishing.Pointer.GenerationID, adapter); err == nil {
				t.Fatalf("%s fault unexpectedly succeeded", stage)
			}
			reconcileFault = oldFault
			result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
			if err != nil || result.Disposition != ReconcileActivated || result.Snapshot.Pointer.State != operatorauth.ReleaseStateActive {
				t.Fatalf("%s recovery result=%+v err=%v", stage, result, err)
			}
		})
	}
}

func TestReconcileAdoptionFaultSeamsRecoverWithoutResend(t *testing.T) {
	t.Run("ordinal zero persists before fault and is not resent", func(t *testing.T) {
		store, publishing, adapter := reconcileFixture(t)
		if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0); err != nil {
			t.Fatal(err)
		}
		tagReceipt := installReconcileEvidence(t, store, publishing, adapter, 0)
		tagDigest, err := operatorauth.ReleaseDeliveryReceiptSHA256(tagReceipt)
		if err != nil {
			t.Fatal(err)
		}
		adapter.outcome = ReleaseChildInvokeOutcome{ProcessStarted: true, InvocationBegan: true}
		oldFault := reconcileFault
		reconcileFault = func(stage string) error {
			if stage == "after_child_adoption:0" {
				return errors.New("fault after ordinal zero adoption")
			}
			return nil
		}
		t.Cleanup(func() { reconcileFault = oldFault })
		if _, err := store.Reconcile(publishing.Pointer.GenerationID, adapter); err == nil {
			t.Fatal("ordinal zero adoption fault unexpectedly succeeded")
		}
		record := mustGenerationRecord(t, store, 1)
		if record.State != operatorauth.ReleaseStatePublishing || record.Children[0].State != childPublicationPublished || record.Children[0].Receipt == nil || !deliveryReceiptTupleEqual(*record.Children[0].Receipt, tagReceipt) || record.Children[0].ReceiptSHA256 != tagDigest || record.Children[1].State != childPublicationPlanned || len(adapter.invocations) != 0 {
			t.Fatalf("post-fault record=%+v invocations=%d", record, len(adapter.invocations))
		}
		current, err := store.ReadCurrent()
		if err != nil || current.Pointer.State != operatorauth.ReleaseStatePublishing {
			t.Fatalf("post-fault pointer=%+v err=%v", current.Pointer, err)
		}
		if adapter.reads != 2 || adapter.scans != 1 {
			t.Fatalf("post-fault adapter reads=%d scans=%d", adapter.reads, adapter.scans)
		}
		if _, err := os.Stat(filepath.Join(store.dirPath, store.activeName(1))); !os.IsNotExist(err) {
			t.Fatalf("ordinal zero fault wrote active artifact: %v", err)
		}
		reconcileFault = oldFault
		result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
		if err != nil || result.Disposition != ReconcileInvoked || result.Role != operatorauth.ReleaseChildGitHubRelease || len(adapter.invocations) != 1 {
			t.Fatalf("recovery result=%+v invocations=%d err=%v", result, len(adapter.invocations), err)
		}
		if adapter.reads != 4 || adapter.scans != 2 {
			t.Fatalf("post-invoke adapter reads=%d scans=%d", adapter.reads, adapter.scans)
		}
		record = mustGenerationRecord(t, store, 1)
		if record.Children[0].State != childPublicationPublished || record.Children[1].State != childPublicationSending {
			t.Fatalf("post-invoke record=%+v", record)
		}
		_ = installReconcileEvidence(t, store, publishing, adapter, 1)
		result, err = store.Reconcile(publishing.Pointer.GenerationID, adapter)
		if err != nil || result.Disposition != ReconcileActivated || result.Snapshot.Pointer.State != operatorauth.ReleaseStateActive || len(adapter.invocations) != 1 {
			t.Fatalf("final activation result=%+v invocations=%d err=%v", result, len(adapter.invocations), err)
		}
		if adapter.reads != 6 || adapter.scans != 3 {
			t.Fatalf("post-activation adapter reads=%d scans=%d", adapter.reads, adapter.scans)
		}
	})

	t.Run("ordinal one persists before fault and retry activates", func(t *testing.T) {
		store, publishing, adapter := reconcileFixture(t)
		tagReceipt := installReconcileEvidence(t, store, publishing, adapter, 0)
		if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0); err != nil {
			t.Fatal(err)
		}
		if err := store.AdoptChildPublication(publishing.Pointer.GenerationID, 0, tagReceipt); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 1); err != nil {
			t.Fatal(err)
		}
		releaseReceipt := installReconcileEvidence(t, store, publishing, adapter, 1)
		releaseDigest, err := operatorauth.ReleaseDeliveryReceiptSHA256(releaseReceipt)
		if err != nil {
			t.Fatal(err)
		}
		oldFault := reconcileFault
		reconcileFault = func(stage string) error {
			if stage == "after_child_adoption:1" {
				return errors.New("fault after ordinal one adoption")
			}
			return nil
		}
		t.Cleanup(func() { reconcileFault = oldFault })
		if _, err := store.Reconcile(publishing.Pointer.GenerationID, adapter); err == nil {
			t.Fatal("ordinal one adoption fault unexpectedly succeeded")
		}
		record := mustGenerationRecord(t, store, 1)
		if record.State != operatorauth.ReleaseStatePublishing || !allReleaseChildrenPublished(record) || record.Children[1].Receipt == nil || !deliveryReceiptTupleEqual(*record.Children[1].Receipt, releaseReceipt) || record.Children[1].ReceiptSHA256 != releaseDigest || len(adapter.invocations) != 0 {
			t.Fatalf("post-fault record=%+v invocations=%d", record, len(adapter.invocations))
		}
		current, err := store.ReadCurrent()
		if err != nil || current.Pointer.State != operatorauth.ReleaseStatePublishing {
			t.Fatalf("post-fault pointer=%+v err=%v", current.Pointer, err)
		}
		if _, err := os.Stat(filepath.Join(store.dirPath, store.activeName(1))); !os.IsNotExist(err) {
			t.Fatalf("ordinal one fault wrote active artifact: %v", err)
		}
		reconcileFault = oldFault
		result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
		if err != nil || result.Disposition != ReconcileActivated || result.Snapshot.Pointer.State != operatorauth.ReleaseStateActive || len(adapter.invocations) != 0 {
			t.Fatalf("recovery result=%+v invocations=%d err=%v", result, len(adapter.invocations), err)
		}
	})
}
