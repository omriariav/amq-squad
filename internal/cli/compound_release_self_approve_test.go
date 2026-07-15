package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/compoundrelease"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestOperatorSelfApproveRejectsReleaseDomainBeforeSideEffects(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, cliReleaseReceiptFixture, compoundrelease.Snapshot, state.Message) state.Message
		fault  bool
	}{
		{name: "eligible"},
		{name: "ineligible valid physical marker", mutate: func(t *testing.T, fixture cliReleaseReceiptFixture, _ compoundrelease.Snapshot, question state.Message) state.Message {
			question = cloneReleaseStateMessage(question)
			question.ID = "release-owned-ineligible"
			question.Created = question.Created.Add(time.Minute)
			question.RawCreated = question.Created.Format(time.RFC3339Nano)
			writeRawCLIReleaseMessage(t, fixture.adapter.root, "user", "new", question, question.Context)
			return releaseQuestionForCLIClassification(t, fixture.adapter.root, question.ID)
		}},
		{name: "malformed physical marker", mutate: func(t *testing.T, fixture cliReleaseReceiptFixture, _ compoundrelease.Snapshot, question state.Message) state.Message {
			question = cloneReleaseStateMessage(question)
			question.ID = "release-owned-malformed"
			question.Created = question.Created.Add(time.Minute)
			question.RawCreated = question.Created.Format(time.RFC3339Nano)
			question.Context["release_child"] = "{"
			writeRawCLIReleaseMessage(t, fixture.adapter.root, "user", "new", question, question.Context)
			return releaseQuestionForCLIClassification(t, fixture.adapter.root, question.ID)
		}},
		{name: "exact suppressed marker stripped", mutate: func(t *testing.T, fixture cliReleaseReceiptFixture, _ compoundrelease.Snapshot, question state.Message) state.Message {
			context := cloneReleaseStateMessage(question).Context
			delete(context, "release_child")
			messages, _ := state.ScanSessionMessages(fixture.adapter.root, time.Now)
			for _, copy := range messages {
				if copy.ID == question.ID {
					writeRawCLIReleaseMessage(t, fixture.adapter.root, copy.Owner, string(copy.State), copy, context)
				}
			}
			return releaseQuestionForCLIClassification(t, fixture.adapter.root, question.ID)
		}},
		{name: "classifier error", fault: true, mutate: func(t *testing.T, fixture cliReleaseReceiptFixture, _ compoundrelease.Snapshot, question state.Message) state.Message {
			ordinary := ordinarySelfApprovalQuestion(fixture.adapter.project, fixture.adapter.profile, fixture.adapter.session, "ordinary-classifier-error", question.Thread, question.Created.Add(time.Minute))
			writeRawCLIReleaseMessage(t, fixture.adapter.root, "user", "new", ordinary, ordinary.Context)
			return releaseQuestionForCLIClassification(t, fixture.adapter.root, ordinary.ID)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture, active := newCLIActiveReleaseAttentionFixture(t)
			question := releaseQuestionForCLIClassification(t, fixture.adapter.root, active.Active.Children[0].QuestionMessageID)
			if tc.mutate != nil {
				question = tc.mutate(t, fixture, active, question)
			}
			installReleaseSelfApprovalPolicy(t, fixture.adapter.project, fixture.adapter.profile, fixture.adapter.session, question.AuthorizationRequest.GateKind)
			if tc.fault {
				old := resolveCompoundReleaseAttention
				resolveCompoundReleaseAttention = func(compoundrelease.SessionScope, compoundrelease.ResolveQuery, compoundrelease.InspectionAdapter) (compoundrelease.Resolution, error) {
					return compoundrelease.Resolution{}, errors.New("injected classifier failure")
				}
				t.Cleanup(func() { resolveCompoundReleaseAttention = old })
			}
			assertReleaseSelfApproveRejectedWithoutSideEffects(t, fixture.adapter.project, fixture.adapter.profile, fixture.adapter.session, fixture.adapter.root, question)
		})
	}
}

func TestOperatorSelfApproveReleaseOwnedPrecedesDisabledSelfPolicy(t *testing.T) {
	fixture, active := newCLIActiveReleaseAttentionFixture(t)
	question := releaseQuestionForCLIClassification(t, fixture.adapter.root, active.Active.Children[0].QuestionMessageID)
	op := team.DefaultOperator()
	cfg := team.Team{Project: fixture.adapter.project, Operator: &op, Orchestrated: true, Lead: "cto", Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: fixture.adapter.session}}}
	if err := team.WriteProfile(fixture.adapter.project, fixture.adapter.profile, cfg); err != nil {
		t.Fatal(err)
	}
	assertReleaseSelfApproveRejectedWithoutSideEffects(t, fixture.adapter.project, fixture.adapter.profile, fixture.adapter.session, fixture.adapter.root, question)
}

func TestOperatorSelfApproveNamedExactRootRejectsBeforeSideEffects(t *testing.T) {
	project := t.TempDir()
	profile, session := "release", "issue-457"
	exactRoot := squadnamespace.AMQRoot(project, profile, session)
	poisonRoot := squadnamespace.AMQRoot(project, team.DefaultProfile, session)
	question := ordinarySelfApprovalQuestion(project, profile, session, "named-owned", "gate/named-owned", notifyNow)
	question.Context["release_child"] = "{"
	writeRawCLIReleaseMessage(t, exactRoot, "user", "new", question, question.Context)
	poison := ordinarySelfApprovalQuestion(project, profile, session, "poison-ordinary", question.Thread, question.Created.Add(time.Minute))
	writeRawCLIReleaseMessage(t, poisonRoot, "user", "new", poison, poison.Context)
	installReleaseSelfApprovalPolicy(t, project, profile, session, operatorauth.GateMerge)

	oldResolve := resolveCompoundReleaseAttention
	resolveCalls := 0
	resolveCompoundReleaseAttention = func(scope compoundrelease.SessionScope, query compoundrelease.ResolveQuery, adapter compoundrelease.InspectionAdapter) (compoundrelease.Resolution, error) {
		resolveCalls++
		return compoundrelease.ResolveSessionSeries(scope, query, adapter)
	}
	t.Cleanup(func() { resolveCompoundReleaseAttention = oldResolve })
	assertReleaseSelfApproveRejectedWithoutSideEffects(t, project, profile, session, exactRoot, question)
	if resolveCalls != 1 {
		t.Fatalf("classifier resolver calls=%d, want 1", resolveCalls)
	}
}

func TestOperatorSelfApproveOrdinaryDistinctSuppressedIDContinues(t *testing.T) {
	fixture, active := newCLIActiveReleaseAttentionFixture(t)
	child := releaseQuestionForCLIClassification(t, fixture.adapter.root, active.Active.Children[0].QuestionMessageID)
	ordinary := ordinarySelfApprovalQuestion(fixture.adapter.project, fixture.adapter.profile, fixture.adapter.session, "ordinary-after-release", child.Thread, child.Created.Add(time.Minute))
	writeRawCLIReleaseMessage(t, fixture.adapter.root, "user", "new", ordinary, ordinary.Context)
	ordinary = releaseQuestionForCLIClassification(t, fixture.adapter.root, ordinary.ID)
	installReleaseSelfApprovalPolicy(t, fixture.adapter.project, fixture.adapter.profile, fixture.adapter.session, operatorauth.GateMerge)

	actorCalls := 0
	oldActor := resolveVerifiedOperatorActor
	resolveVerifiedOperatorActor = func(_, profile, session, role, handle string) (verifiedOperatorActor, error) {
		actorCalls++
		return verifiedOperatorActor{Profile: profile, Session: session, Role: role, Handle: handle}, nil
	}
	t.Cleanup(func() { resolveVerifiedOperatorActor = oldActor })
	calls := withAMQCommandSeams(t, amqEnv{Root: filepath.Join(fixture.adapter.project, ".agent-mail", "{session}"), BaseRoot: filepath.Join(fixture.adapter.project, ".agent-mail")}, "Sent should-not-send to cto\n")
	evidence := filepath.Join(fixture.adapter.project, "missing-evidence.json")
	err := runOperatorSelfApprove(selfApproveArgs(fixture.adapter.project, fixture.adapter.profile, fixture.adapter.session, ordinary, evidence))
	if err == nil || strings.Contains(err.Error(), "human approval required") || !os.IsNotExist(err) {
		t.Fatalf("ordinary suppressed-thread continuation error=%v", err)
	}
	if actorCalls != 1 || len(*calls) != 0 {
		t.Fatalf("ordinary path actorCalls=%d amqCalls=%d", actorCalls, len(*calls))
	}
	if _, statErr := os.Stat(selfApprovalReservationPath(fixture.adapter.project, fixture.adapter.profile, fixture.adapter.session, ordinary.Thread, ordinary.ID)); !os.IsNotExist(statErr) {
		t.Fatalf("ordinary failed preflight left reservation: %v", statErr)
	}
}

func assertReleaseSelfApproveRejectedWithoutSideEffects(t *testing.T, project, profile, session, root string, question state.Message) {
	t.Helper()
	reservationPath := selfApprovalReservationPath(project, profile, session, question.Thread, question.ID)
	if err := os.MkdirAll(filepath.Dir(reservationPath), 0o755); err != nil {
		t.Fatal(err)
	}
	reservationCanary := []byte(`{"sending":true,"answer_message_id":"must-remain-byte-identical"}`)
	if err := os.WriteFile(reservationPath, reservationCanary, 0o600); err != nil {
		t.Fatal(err)
	}
	storeBefore := snapshotTestDirectory(t, selfApprovalStoreDir(project, profile, session))
	deliveryBefore := snapshotTestDirectory(t, deliveryReceiptDir(project, profile, session))
	evidence := filepath.Join(project, "must-not-read-or-write-evidence.json")
	actorCalls := 0
	oldActor := resolveVerifiedOperatorActor
	resolveVerifiedOperatorActor = func(_, profile, session, role, handle string) (verifiedOperatorActor, error) {
		actorCalls++
		return verifiedOperatorActor{}, errors.New("actor setup must not run")
	}
	t.Cleanup(func() { resolveVerifiedOperatorActor = oldActor })
	calls := withAMQCommandSeams(t, amqEnv{Root: root, BaseRoot: root}, "Sent impossible\n")
	err := runOperatorSelfApprove(selfApproveArgs(project, profile, session, question, evidence))
	if err == nil || !strings.Contains(err.Error(), "human approval required") {
		t.Fatalf("release self-approve error=%v", err)
	}
	if actorCalls != 0 || len(*calls) != 0 {
		t.Fatalf("release rejection actorCalls=%d amqCalls=%d", actorCalls, len(*calls))
	}
	got, readErr := os.ReadFile(reservationPath)
	if readErr != nil || !bytes.Equal(got, reservationCanary) {
		t.Fatalf("pending reservation changed: bytes=%q err=%v", got, readErr)
	}
	if _, statErr := os.Stat(evidence); !os.IsNotExist(statErr) {
		t.Fatalf("evidence path touched: %v", statErr)
	}
	if matches, _ := filepath.Glob(filepath.Join(selfApprovalStoreDir(project, profile, session), "*.receipt.json")); len(matches) != 0 {
		t.Fatalf("self approval receipt created: %v", matches)
	}
	if storeAfter := snapshotTestDirectory(t, selfApprovalStoreDir(project, profile, session)); !reflect.DeepEqual(storeAfter, storeBefore) {
		t.Fatalf("self approval evidence/store artifacts changed: before=%v after=%v", storeBefore, storeAfter)
	}
	if deliveryAfter := snapshotTestDirectory(t, deliveryReceiptDir(project, profile, session)); !reflect.DeepEqual(deliveryAfter, deliveryBefore) {
		t.Fatalf("delivery receipt artifacts changed: before=%v after=%v", deliveryBefore, deliveryAfter)
	}
}

func snapshotTestDirectory(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = string(content)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return out
}

func installReleaseSelfApprovalPolicy(t *testing.T, project, profile, session, _ string) {
	t.Helper()
	op := team.DefaultOperator()
	op.InteractionMode = team.OperatorInteractionSelfOperator
	op.SelfOperator = &team.SelfOperatorPolicy{
		LeadRole: "cto", PolicyRevision: 7,
		Sessions: map[string]team.SelfOperatorSessionPolicy{session: {Enabled: true, AllowedGateKinds: []string{operatorauth.GateMerge}}},
	}
	cfg := team.Team{Project: project, Operator: &op, Orchestrated: true, Lead: "cto", Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: session}}}
	if err := team.WriteProfile(project, profile, cfg); err != nil {
		t.Fatal(err)
	}
}

func ordinarySelfApprovalQuestion(project, profile, session, id, gate string, created time.Time) state.Message {
	action, target := "protected_branch_push", "PR #457 head abc1234 into main"
	request := operatorauth.GateRequestContext{
		SchemaVersion: operatorauth.GateRequestSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion,
		Gate: gate, Thread: gate,
		Namespace: operatorauth.NamespaceBinding{ProjectDir: project, Profile: profile, Session: session, NamespaceID: squadnamespace.ID(profile, session), Generation: "none"},
		GateKind:  operatorauth.GateMerge, Action: action, Target: target,
	}
	body := "Gate-Kind: merge\nAction: " + action + "\nTarget: " + target
	return state.Message{
		ID: id, From: "cto", To: []string{"user"}, Thread: gate, RawThread: gate,
		Subject: "APPROVAL: " + strings.TrimPrefix(gate, "gate/"), RawSubject: "APPROVAL: " + strings.TrimPrefix(gate, "gate/"),
		Created: created, RawCreated: created.Format(time.RFC3339Nano), Priority: state.PriorityNormal, Kind: state.KindQuestion,
		Body: body, RawBody: body, AuthorityRaw: true, SchemaOK: true,
		Context: map[string]any{"authorization_request": request}, AuthorizationRequestPresent: true, AuthorizationRequestValid: true, AuthorizationRequest: &request,
	}
}

func selfApproveArgs(project, profile, session string, question state.Message, evidence string) []string {
	request := question.AuthorizationRequest
	return []string{
		"--project", project, "--profile", profile, "--session", session, "--gate", question.Thread,
		"--kind", request.GateKind, "--action", request.Action, "--target", request.Target, "--evidence", evidence,
	}
}
