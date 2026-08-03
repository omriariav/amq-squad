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
	root        string
	rootErr     error
	outcome     ReleaseChildInvokeOutcome
	invoke      func(ReleaseChildInvocation) ReleaseChildInvokeOutcome
	warnings    []state.Warning
	resolved    int
	scans       int
	invocations []ReleaseChildInvocation
}

func (f *fakeReconcileAdapter) ResolveSessionRoot(Scope) (string, error) {
	f.resolved++
	if f.rootErr != nil {
		return "", f.rootErr
	}
	return f.root, nil
}

func (f *fakeReconcileAdapter) ScanSessionMessages(root string, now func() time.Time) ([]state.Message, []state.Warning) {
	f.scans++
	messages, warnings := state.ScanSessionMessages(root, now)
	return messages, append(warnings, f.warnings...)
}

func (f *fakeReconcileAdapter) InvokeReleaseChild(invocation ReleaseChildInvocation) ReleaseChildInvokeOutcome {
	f.invocations = append(f.invocations, invocation)
	if f.invoke != nil {
		return f.invoke(invocation)
	}
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
	return store, publishing, &fakeReconcileAdapter{root: root}
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
	Labels       []string       `json:"labels,omitempty"`
	Orchestrator string         `json:"orchestrator,omitempty"`
	Context      map[string]any `json:"context"`
}

func writeReleaseQuestion(t *testing.T, root, owner, mailbox, id, created string, child operatorauth.ReleaseChildPlan, spec operatorauth.ReleaseSpec, mutate func(*releaseMessageHeader)) {
	t.Helper()
	header := releaseMessageHeader{
		Schema: 1, ID: id, From: spec.RequesterHandle, To: []string{spec.OperatorHandle},
		Thread: child.Thread, Subject: child.Subject, Created: created, Priority: "normal", Kind: "question",
		Context: map[string]any{"authorization_request": child.AuthorizationRequest, "release_child": child.ReleaseChild},
	}
	if mutate != nil {
		mutate(&header)
	}
	raw, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "agents", owner, "inbox", mailbox)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data := append([]byte("---json\n"), raw...)
	data = append(data, []byte("\n---\n"+child.Body+"\n")...)
	if err := os.WriteFile(filepath.Join(dir, id+".md"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func installExactReleaseQuestion(t *testing.T, publishing Snapshot, adapter *fakeReconcileAdapter, ordinal int, id string) {
	t.Helper()
	child := publishing.Prepared.Children[ordinal]
	writeReleaseQuestion(t, adapter.root, publishing.Prepared.Spec.OperatorHandle, "new", id, "2026-07-15T01:00:00Z", child, publishing.Prepared.Spec, nil)
}

func stageAllReconcileEvidence(t *testing.T, store *Store, publishing Snapshot, adapter *fakeReconcileAdapter) {
	t.Helper()
	if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0); err != nil {
		t.Fatal(err)
	}
	installExactReleaseQuestion(t, publishing, adapter, 0, "question-tag")
	if err := store.AdoptChildPublication(publishing.Pointer.GenerationID, 0, "question-tag"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 1); err != nil {
		t.Fatal(err)
	}
	installExactReleaseQuestion(t, publishing, adapter, 1, "question-github-release")
	if err := store.AdoptChildPublication(publishing.Pointer.GenerationID, 1, "question-github-release"); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileAcceptedSendPersistsTransportMessageIdentity(t *testing.T) {
	store, publishing, adapter := reconcileFixture(t)
	adapter.outcome = ReleaseChildInvokeOutcome{ProcessStarted: true, InvocationBegan: true, MessageID: "question-tag"}

	result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
	if err != nil || result.Disposition != ReconcilePublished || result.Role != operatorauth.ReleaseChildTag {
		t.Fatalf("Reconcile()=(%+v,%v)", result, err)
	}
	if len(adapter.invocations) != 1 {
		t.Fatalf("invocations=%d", len(adapter.invocations))
	}
	invocation := adapter.invocations[0]
	child := publishing.Prepared.Children[0]
	if invocation.AttemptID != child.ReleaseChild.AttemptID || invocation.Sender != publishing.Prepared.Spec.RequesterHandle || invocation.Recipient != publishing.Prepared.Spec.OperatorHandle || invocation.Thread != child.Thread || invocation.AuthorizationRequest != child.AuthorizationRequest || invocation.ReleaseChild != child.ReleaseChild {
		t.Fatalf("invocation=%+v child=%+v", invocation, child)
	}
	record := mustGenerationRecord(t, store, 1)
	if record.Children[0].State != childPublicationPublished || record.Children[0].QuestionMessageID != "question-tag" || record.Children[1].State != childPublicationPlanned {
		t.Fatalf("record=%+v", record)
	}
}

func TestReconcilePreInvocationFailureRollsBackClaim(t *testing.T) {
	store, publishing, adapter := reconcileFixture(t)
	adapter.outcome = ReleaseChildInvokeOutcome{Err: errors.New("exec rejected")}

	result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
	if err == nil || result.Disposition != ReconcileAmbiguous || result.Role != operatorauth.ReleaseChildTag {
		t.Fatalf("Reconcile()=(%+v,%v)", result, err)
	}
	record := mustGenerationRecord(t, store, 1)
	if record.Children[0].State != childPublicationPlanned || record.Children[0].ClaimRevision != 1 || record.Children[0].ClaimToken != "" || record.Children[0].QuestionMessageID != "" {
		t.Fatalf("rolled back child=%+v", record.Children[0])
	}
}

func TestReconcileSuccessWithoutMessageIdentityIsAmbiguous(t *testing.T) {
	store, publishing, adapter := reconcileFixture(t)
	adapter.outcome = ReleaseChildInvokeOutcome{ProcessStarted: true, InvocationBegan: true}

	result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
	if err == nil || result.Disposition != ReconcileAmbiguous || result.Role != operatorauth.ReleaseChildTag {
		t.Fatalf("Reconcile()=(%+v,%v)", result, err)
	}
	record := mustGenerationRecord(t, store, 1)
	if record.Children[0].State != childPublicationSending || record.Children[0].QuestionMessageID != "" {
		t.Fatalf("uncertain child=%+v", record.Children[0])
	}
}

func TestReconcileCrashRecoveryAdoptsExactMailboxMessageWithoutResend(t *testing.T) {
	store, publishing, adapter := reconcileFixture(t)
	if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0); err != nil {
		t.Fatal(err)
	}
	installExactReleaseQuestion(t, publishing, adapter, 0, "question-tag")
	adapter.outcome = ReleaseChildInvokeOutcome{ProcessStarted: true, InvocationBegan: true, MessageID: "question-github-release"}

	result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
	if err != nil || result.Disposition != ReconcilePublished || result.Role != operatorauth.ReleaseChildGitHubRelease {
		t.Fatalf("Reconcile()=(%+v,%v)", result, err)
	}
	if len(adapter.invocations) != 1 || adapter.invocations[0].Role != operatorauth.ReleaseChildGitHubRelease {
		t.Fatalf("invocations=%+v", adapter.invocations)
	}
	record := mustGenerationRecord(t, store, 1)
	if record.Children[0].State != childPublicationPublished || record.Children[0].QuestionMessageID != "question-tag" || record.Children[1].State != childPublicationPublished || record.Children[1].QuestionMessageID != "question-github-release" {
		t.Fatalf("record=%+v", record)
	}
}

func TestReconcileDuplicateExactMessagesTerminalizeConflict(t *testing.T) {
	store, publishing, adapter := reconcileFixture(t)
	if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0); err != nil {
		t.Fatal(err)
	}
	installExactReleaseQuestion(t, publishing, adapter, 0, "question-tag-a")
	installExactReleaseQuestion(t, publishing, adapter, 0, "question-tag-b")

	result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
	if err != nil || result.Disposition != ReconcileConflict || result.Role != operatorauth.ReleaseChildTag {
		t.Fatalf("Reconcile()=(%+v,%v)", result, err)
	}
	if len(adapter.invocations) != 0 {
		t.Fatalf("conflict invoked transport: %+v", adapter.invocations)
	}
	record := mustGenerationRecord(t, store, 1)
	if record.State != operatorauth.ReleaseStateConflict || !reflect.DeepEqual(record.Children[0].ObservedMessageIDs, []string{"question-tag-a", "question-tag-b"}) {
		t.Fatalf("record=%+v", record)
	}
}

func TestReconcilePublishedMessageIdentityDivergenceConflicts(t *testing.T) {
	store, publishing, adapter := reconcileFixture(t)
	if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.AdoptChildPublication(publishing.Pointer.GenerationID, 0, "question-tag-original"); err != nil {
		t.Fatal(err)
	}
	installExactReleaseQuestion(t, publishing, adapter, 0, "question-tag-other")

	result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
	if err != nil || result.Disposition != ReconcileConflict || result.Role != operatorauth.ReleaseChildTag {
		t.Fatalf("Reconcile()=(%+v,%v)", result, err)
	}
	if len(adapter.invocations) != 0 {
		t.Fatalf("identity conflict invoked transport: %+v", adapter.invocations)
	}
}

func TestReconcilePublishedMissingStoredMessageIsRecordInvalid(t *testing.T) {
	store, publishing, adapter := reconcileFixture(t)
	if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0); err != nil {
		t.Fatal(err)
	}
	const missingID = "question-tag-missing"
	if err := store.AdoptChildPublication(publishing.Pointer.GenerationID, 0, missingID); err != nil {
		t.Fatal(err)
	}

	result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
	if err != nil || result.Disposition != ReconcileConflict || result.Role != operatorauth.ReleaseChildTag {
		t.Fatalf("Reconcile()=(%+v,%v)", result, err)
	}
	record := mustGenerationRecord(t, store, 1)
	recordPath := filepath.Join(store.dirPath, store.generationName(1))
	child := record.Children[0]
	if record.State != operatorauth.ReleaseStateConflict || child.QuestionMessageID != missingID || !strings.Contains(child.ConflictReason, "record_invalid") || !strings.Contains(child.ConflictReason, recordPath) || !strings.Contains(child.ConflictReason, missingID) {
		t.Fatalf("record=%+v path=%q", record, recordPath)
	}
}

func TestReconcileActivatesAfterBothExactMailboxMessages(t *testing.T) {
	store, publishing, adapter := reconcileFixture(t)
	stageAllReconcileEvidence(t, store, publishing, adapter)

	result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
	if err != nil || result.Disposition != ReconcileActivated || result.Snapshot.Pointer.State != operatorauth.ReleaseStateActive || result.Snapshot.Active == nil {
		t.Fatalf("Reconcile()=(%+v,%v)", result, err)
	}
	if len(adapter.invocations) != 0 {
		t.Fatalf("activation invoked transport: %+v", adapter.invocations)
	}
	if result.Snapshot.Active.Children[0].QuestionMessageID != "question-tag" || result.Snapshot.Active.Children[1].QuestionMessageID != "question-github-release" {
		t.Fatalf("active=%+v", result.Snapshot.Active)
	}
	resolved, scans := adapter.resolved, adapter.scans
	again, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
	if err != nil || again.Disposition != ReconcileActivated || adapter.resolved != resolved || adapter.scans != scans {
		t.Fatalf("terminal replay=%+v err=%v adapter=%+v", again, err, adapter)
	}
}

func TestReconcileNearMessageConflictsBeforeInvocation(t *testing.T) {
	store, publishing, adapter := reconcileFixture(t)
	child := publishing.Prepared.Children[0]
	writeReleaseQuestion(t, adapter.root, publishing.Prepared.Spec.OperatorHandle, "new", "near-tag", "2026-07-15T01:00:00Z", child, publishing.Prepared.Spec, func(header *releaseMessageHeader) {
		header.Subject += " changed"
	})

	result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
	if err != nil || result.Disposition != ReconcileConflict || result.Role != operatorauth.ReleaseChildTag {
		t.Fatalf("Reconcile()=(%+v,%v)", result, err)
	}
	if len(adapter.invocations) != 0 {
		t.Fatalf("near message invoked transport: %+v", adapter.invocations)
	}
}

func TestExactReleaseMessageRejectsMailboxIdentityMutations(t *testing.T) {
	_, publishing, adapter := reconcileFixture(t)
	child, spec := publishing.Prepared.Children[0], publishing.Prepared.Spec
	installExactReleaseQuestion(t, publishing, adapter, 0, "question-tag")
	messages, warnings := state.ScanSessionMessages(adapter.root, time.Now)
	if len(warnings) != 0 {
		t.Fatalf("warnings=%v", warnings)
	}
	groups := groupReleaseMessages(messages)
	if len(groups) != 1 || !exactReleaseMessage(groups[0], child, spec) {
		t.Fatalf("base group=%+v", groups)
	}
	base := groups[0]
	mutations := map[string]func(*releaseMessageGroup){
		"sender":             func(g *releaseMessageGroup) { g.Message.From = "other" },
		"ordered recipients": func(g *releaseMessageGroup) { g.Message.To = []string{"other", spec.OperatorHandle} },
		"thread":             func(g *releaseMessageGroup) { g.Message.Thread += "/other" },
		"raw thread":         func(g *releaseMessageGroup) { g.Message.RawThread += "/other" },
		"subject":            func(g *releaseMessageGroup) { g.Message.RawSubject += " changed" },
		"message":            func(g *releaseMessageGroup) { g.Message.RawBody += " changed" },
		"priority":           func(g *releaseMessageGroup) { g.Message.Priority = state.PriorityUrgent },
		"kind":               func(g *releaseMessageGroup) { g.Message.Kind = state.KindStatus },
		"reply to":           func(g *releaseMessageGroup) { g.Message.ReplyTo = "prior" },
		"labels":             func(g *releaseMessageGroup) { g.Message.Labels = []string{"unexpected"} },
		"orchestrator":       func(g *releaseMessageGroup) { g.Message.Orchestrator = "unexpected" },
		"group owner":        func(g *releaseMessageGroup) { g.Owners = append(g.Owners, "outsider") },
		"malformed created":  func(g *releaseMessageGroup) { g.Message.RawCreated = "2026-07-15 01:00:00Z" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			group := cloneReleaseMessageGroup(base)
			mutate(&group)
			if exactReleaseMessage(group, child, spec) {
				t.Fatalf("mutation %q remained exact: %+v", name, group)
			}
		})
	}
}

func TestClassifyReleaseMessageGroupsOwnershipBranches(t *testing.T) {
	_, publishing, adapter := reconcileFixture(t)
	child, spec := publishing.Prepared.Children[0], publishing.Prepared.Spec
	installExactReleaseQuestion(t, publishing, adapter, 0, "question-tag")
	messages, warnings := state.ScanSessionMessages(adapter.root, time.Now)
	if len(warnings) != 0 {
		t.Fatal(warnings)
	}
	base := groupReleaseMessages(messages)[0]

	exact, near, uncertain := classifyReleaseMessageGroups([]releaseMessageGroup{base}, child, spec)
	if len(exact) != 1 || len(near) != 0 || uncertain {
		t.Fatalf("exact=%v near=%v uncertain=%t", exact, near, uncertain)
	}

	senderOnly := cloneReleaseMessageGroup(base)
	senderOnly.Owners = []string{spec.RequesterHandle}
	for i := range senderOnly.Copies {
		senderOnly.Copies[i].Owner = spec.RequesterHandle
	}
	exact, near, uncertain = classifyReleaseMessageGroups([]releaseMessageGroup{senderOnly}, child, spec)
	if len(exact) != 0 || len(near) != 0 || !uncertain {
		t.Fatalf("sender-only exact=%v near=%v uncertain=%t", exact, near, uncertain)
	}

	wrongSender := cloneReleaseMessageGroup(base)
	wrongSender.Message.From = "other"
	wrongSender.Copies[0].From = "other"
	exact, near, uncertain = classifyReleaseMessageGroups([]releaseMessageGroup{wrongSender}, child, spec)
	if len(exact) != 0 || !reflect.DeepEqual(near, []string{"question-tag"}) || uncertain {
		t.Fatalf("near exact=%v near=%v uncertain=%t", exact, near, uncertain)
	}
}

func cloneReleaseMessageGroup(group releaseMessageGroup) releaseMessageGroup {
	clone := group
	clone.Message.To = append([]string(nil), group.Message.To...)
	clone.Message.Labels = append([]string(nil), group.Message.Labels...)
	clone.Copies = append([]state.Message(nil), group.Copies...)
	for i := range clone.Copies {
		clone.Copies[i].To = append([]string(nil), clone.Copies[i].To...)
		clone.Copies[i].Labels = append([]string(nil), clone.Copies[i].Labels...)
	}
	clone.Owners = append([]string(nil), group.Owners...)
	return clone
}

func TestReconcilePhysicalCopyAndWarningBarriers(t *testing.T) {
	t.Run("equal physical copies dedupe", func(t *testing.T) {
		store, publishing, adapter := reconcileFixture(t)
		if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0); err != nil {
			t.Fatal(err)
		}
		child := publishing.Prepared.Children[0]
		created := "2026-07-15T01:00:00Z"
		writeReleaseQuestion(t, adapter.root, publishing.Prepared.Spec.OperatorHandle, "cur", "question-tag", created, child, publishing.Prepared.Spec, nil)
		writeReleaseQuestion(t, adapter.root, publishing.Prepared.Spec.OperatorHandle, "new", "question-tag", created, child, publishing.Prepared.Spec, nil)
		adapter.outcome = ReleaseChildInvokeOutcome{ProcessStarted: true, InvocationBegan: true, MessageID: "question-github-release"}
		result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
		if err != nil || result.Disposition != ReconcilePublished || len(adapter.invocations) != 1 || mustGenerationRecord(t, store, 1).Children[0].QuestionMessageID != "question-tag" {
			t.Fatalf("result=%+v invocations=%+v err=%v", result, adapter.invocations, err)
		}
	})

	t.Run("unequal same id conflicts", func(t *testing.T) {
		store, publishing, adapter := reconcileFixture(t)
		child := publishing.Prepared.Children[0]
		created := "2026-07-15T01:00:00Z"
		writeReleaseQuestion(t, adapter.root, publishing.Prepared.Spec.OperatorHandle, "cur", "question-tag", created, child, publishing.Prepared.Spec, nil)
		writeReleaseQuestion(t, adapter.root, publishing.Prepared.Spec.OperatorHandle, "new", "question-tag", created, child, publishing.Prepared.Spec, func(header *releaseMessageHeader) { header.Subject += " changed" })
		result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
		if err != nil || result.Disposition != ReconcileConflict || len(adapter.invocations) != 0 {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})

	t.Run("scan warning blocks mutation", func(t *testing.T) {
		store, publishing, adapter := reconcileFixture(t)
		adapter.warnings = []state.Warning{{Path: adapter.root, Reason: "fixture warning"}}
		result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
		record := mustGenerationRecord(t, store, 1)
		if err == nil || result.Disposition != ReconcileAmbiguous || len(adapter.invocations) != 0 || record.Children[0].State != childPublicationPlanned || record.Children[0].ClaimRevision != 0 {
			t.Fatalf("result=%+v record=%+v err=%v", result, record, err)
		}
	})
}

func TestReconcilePlannedEvidenceAndEarlySecondChildMatrix(t *testing.T) {
	t.Run("planned exact conflicts without retro claim", func(t *testing.T) {
		store, publishing, adapter := reconcileFixture(t)
		installExactReleaseQuestion(t, publishing, adapter, 0, "question-tag")
		result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
		record := mustGenerationRecord(t, store, 1)
		if err != nil || result.Disposition != ReconcileConflict || record.Children[0].ClaimRevision != 0 || record.Children[0].ClaimToken != "" || len(adapter.invocations) != 0 {
			t.Fatalf("result=%+v record=%+v err=%v", result, record, err)
		}
	})

	t.Run("planned sender-only evidence is ambiguous", func(t *testing.T) {
		store, publishing, adapter := reconcileFixture(t)
		child := publishing.Prepared.Children[0]
		writeReleaseQuestion(t, adapter.root, publishing.Prepared.Spec.RequesterHandle, "new", "question-tag", "2026-07-15T01:00:00Z", child, publishing.Prepared.Spec, nil)
		result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
		record := mustGenerationRecord(t, store, 1)
		if err == nil || result.Disposition != ReconcileAmbiguous || record.Children[0].State != childPublicationPlanned || record.Children[0].ClaimRevision != 0 || len(adapter.invocations) != 0 {
			t.Fatalf("result=%+v record=%+v err=%v", result, record, err)
		}
	})

	t.Run("early second child evidence conflicts before tag invoke", func(t *testing.T) {
		store, publishing, adapter := reconcileFixture(t)
		installExactReleaseQuestion(t, publishing, adapter, 1, "question-github-release")
		result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
		if err != nil || result.Disposition != ReconcileConflict || result.Role != operatorauth.ReleaseChildGitHubRelease || len(adapter.invocations) != 0 {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})
}

func TestReconcileFaultSeamsConvergeWithoutResend(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stage string
		setup func(*testing.T, *Store, Snapshot, *fakeReconcileAdapter)
	}{
		{name: "after complete scan", stage: "after_complete_scan"},
		{name: "pre invoke", stage: "pre_invoke:0"},
		{name: "after send return", stage: "after_send_return:0"},
		{name: "after child adoption", stage: "after_child_adoption:0", setup: func(t *testing.T, store *Store, publishing Snapshot, adapter *fakeReconcileAdapter) {
			if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0); err != nil {
				t.Fatal(err)
			}
			installExactReleaseQuestion(t, publishing, adapter, 0, "question-tag")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, publishing, adapter := reconcileFixture(t)
			if tc.setup != nil {
				tc.setup(t, store, publishing, adapter)
			}
			adapter.invoke = func(invocation ReleaseChildInvocation) ReleaseChildInvokeOutcome {
				id := "question-" + strings.ReplaceAll(invocation.Role, "_", "-")
				installExactReleaseQuestion(t, publishing, adapter, invocation.Ordinal, id)
				return ReleaseChildInvokeOutcome{ProcessStarted: true, InvocationBegan: true, MessageID: id}
			}
			oldFault := reconcileFault
			fired := false
			reconcileFault = func(stage string) error {
				if stage == tc.stage && !fired {
					fired = true
					return errors.New("fault " + tc.stage)
				}
				return nil
			}
			t.Cleanup(func() { reconcileFault = oldFault })
			if _, err := store.Reconcile(publishing.Pointer.GenerationID, adapter); err == nil || !fired {
				t.Fatalf("fault stage=%s fired=%t err=%v", tc.stage, fired, err)
			}
			reconcileFault = oldFault
			var result ReconcileResult
			var err error
			for attempt := 0; attempt < 4; attempt++ {
				result, err = store.Reconcile(publishing.Pointer.GenerationID, adapter)
				if err != nil {
					t.Fatalf("recovery attempt %d: %v", attempt, err)
				}
				if result.Disposition == ReconcileActivated {
					break
				}
			}
			if result.Disposition != ReconcileActivated || result.Snapshot.Active == nil {
				t.Fatalf("result=%+v", result)
			}
			counts := map[string]int{}
			for _, invocation := range adapter.invocations {
				counts[invocation.Role]++
			}
			for role, count := range counts {
				if count != 1 {
					t.Fatalf("role %s invoked %d times: %+v", role, count, adapter.invocations)
				}
			}
		})
	}
}
