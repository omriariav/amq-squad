package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/compoundrelease"
	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

type cliReleaseReconcileAdapter struct {
	project, profile, session, root string
}

func (a *cliReleaseReconcileAdapter) ResolveSessionRoot(compoundrelease.Scope) (string, error) {
	return a.root, nil
}

func (a *cliReleaseReconcileAdapter) ScanSessionMessages(root string, now func() time.Time) ([]state.Message, []state.Warning) {
	return state.ScanSessionMessages(root, now)
}

func (a *cliReleaseReconcileAdapter) InvokeReleaseChild(invocation compoundrelease.ReleaseChildInvocation) compoundrelease.ReleaseChildInvokeOutcome {
	messageID := ""
	switch invocation.Role {
	case operatorauth.ReleaseChildTag:
		messageID = "question-tag"
	case operatorauth.ReleaseChildGitHubRelease:
		messageID = "question-github-release"
	}
	return compoundrelease.ReleaseChildInvokeOutcome{ProcessStarted: true, InvocationBegan: true, MessageID: messageID}
}

type cliReleaseFixture struct {
	store      *compoundrelease.Store
	publishing compoundrelease.Snapshot
	adapter    *cliReleaseReconcileAdapter
	child      operatorauth.ReleaseChildPlan
}

func newCLIReleaseFixture(t *testing.T, _ int) cliReleaseFixture {
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
	writeExactCLIReleaseQuestion(t, root, publishing.Prepared.Spec, child, "question-tag", time.Date(2026, 7, 15, 1, 0, 0, 0, time.UTC))
	adapter := &cliReleaseReconcileAdapter{project: project, profile: team.DefaultProfile, session: session, root: root}
	return cliReleaseFixture{store: store, publishing: publishing, adapter: adapter, child: child}
}

func writeExactCLIReleaseQuestion(t *testing.T, root string, spec operatorauth.ReleaseSpec, child operatorauth.ReleaseChildPlan, messageID string, created time.Time) {
	t.Helper()
	header := map[string]any{
		"schema": 1, "id": messageID, "from": spec.RequesterHandle, "to": []string{spec.OperatorHandle},
		"thread": child.Thread, "subject": child.Subject, "created": created.Format(time.RFC3339Nano), "priority": "normal", "kind": "question",
		"context": map[string]any{"authorization_request": child.AuthorizationRequest, "release_child": child.ReleaseChild},
	}
	headerBytes, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	message := append([]byte("---json\n"), headerBytes...)
	message = append(message, []byte("\n---\n"+child.Body+"\n")...)
	dir := filepath.Join(root, "agents", spec.OperatorHandle, "inbox", "new")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, messageID+".md"), message, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCompoundReleaseReconcileUsesMailboxMessageIdentity(t *testing.T) {
	fixture := newCLIReleaseFixture(t, 0)
	result, err := fixture.store.Reconcile(fixture.publishing.Pointer.GenerationID, fixture.adapter)
	if err != nil || result.Disposition != compoundrelease.ReconcilePublished || result.Role != operatorauth.ReleaseChildGitHubRelease {
		t.Fatalf("Reconcile()=(%+v,%v)", result, err)
	}
	writeExactCLIReleaseQuestion(t, fixture.adapter.root, fixture.publishing.Prepared.Spec, fixture.publishing.Prepared.Children[1], "question-github-release", time.Date(2026, 7, 15, 1, 0, 1, 0, time.UTC))
	result, err = fixture.store.Reconcile(fixture.publishing.Pointer.GenerationID, fixture.adapter)
	if err != nil || result.Disposition != compoundrelease.ReconcileActivated || result.Snapshot.Active == nil {
		t.Fatalf("activation=(%+v,%v)", result, err)
	}
	if result.Snapshot.Active.Children[0].QuestionMessageID != "question-tag" || result.Snapshot.Active.Children[1].QuestionMessageID != "question-github-release" {
		t.Fatalf("active=%+v", result.Snapshot.Active)
	}
}
