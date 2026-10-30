package compoundrelease

// The mailbox fixture of the v2 inspection path (eligibility.go, guard.go):
// fakeReconcileAdapter is the InspectionAdapter the series inspection tests
// use; the reconcile tests of 3.0 are in reconcile_test.go.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
	"github.com/omriariav/amq-squad/v3/internal/state"
)

type fakeReconcileAdapter struct {
	root     string
	rootErr  error
	warnings []state.Warning
	resolved int
	scans    int
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

// ChildGate lets the fixture stand as a 3.0 ReconcileAdapter for stores
// whose children it already adopted: Reconcile then only activates.
func (f *fakeReconcileAdapter) ChildGate(operatorauth.ReleaseChildPlan) (string, error) {
	return "", errors.New("fixture children are adopted from mail, not raised")
}
