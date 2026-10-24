package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
	"github.com/omriariav/amq-squad/v3/internal/state"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

func ordinaryTypedQuestionForContext(project, profile, session, generation, id, owner, action, target string, created time.Time) state.Message {
	capability, err := operatorauth.LookupAction(action)
	if err != nil {
		panic(err)
	}
	gate := "gate/ordinary-selected"
	request := operatorauth.GateRequestContext{
		SchemaVersion: operatorauth.GateRequestSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion,
		Gate: gate, Thread: gate,
		Namespace: operatorauth.NamespaceBinding{
			ProjectDir: project, Profile: profile, Session: session,
			NamespaceID: squadnamespace.ID(profile, session), Generation: generation,
		},
		GateKind: capability.GateKind, Action: capability.Action, Target: target,
	}
	body := "Gate-Kind: " + capability.GateKind + "\nAction: " + capability.Action + "\nTarget: " + target
	return withAuthorityRaw(state.Message{
		ID: id, From: owner, To: []string{"user"}, Thread: gate, RawThread: gate,
		Subject: "APPROVAL: ordinary-selected", Kind: state.KindQuestion, Body: body, Created: created,
		AuthorizationRequestPresent: true, AuthorizationRequestValid: true, AuthorizationRequest: &request,
	})
}

func TestOrdinaryTypedOperatorAnswerUsesNamedSelectedQuestionDespiteDefaultRootResidue(t *testing.T) {
	project := t.TempDir()
	profile, session := "release", "issue-414"
	cfg := team.Team{
		Project: project, Workstream: session,
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto-named", Session: session}},
	}
	if err := team.WriteProfile(project, profile, cfg); err != nil {
		t.Fatal(err)
	}
	namedBase := filepath.Join(project, ".agent-mail", profile)
	selected := ordinaryTypedQuestionForContext(project, profile, session, "none", "selected-named", "cto-named", "feature_branch_push", "v1", time.Now().UTC())
	writeAuthorizationMessage(t, namedBase, session, "user", "new", selected)
	poison := withAuthorityRaw(state.Message{
		ID: "newer-default-legacy", From: "cto-default", To: []string{"user"},
		Thread: selected.Thread, RawThread: selected.Thread, Subject: "APPROVAL: default poison",
		Kind: state.KindQuestion, Body: "legacy default-root poison", Created: selected.Created.Add(time.Hour),
	})
	writeAuthorizationMessage(t, filepath.Join(project, ".agent-mail"), session, "user", "new", poison)
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent selected-answer to cto-named\n")

	if err := runOperator([]string{
		"answer", "--project", project, "--profile", profile, "--session", session,
		"--gate", selected.Thread, "--approved", "--kind", "feature_branch_push", "--action", "feature_branch_push", "--target", "v1",
	}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("AMQ calls=%d, want one", len(*calls))
	}
	call := (*calls)[0]
	wantRoot := filepath.Join(namedBase, session)
	if amqFlagValue(call.Arg, "root") != wantRoot || amqFlagValue(call.Arg, "to") != "cto-named" {
		t.Fatalf("ordinary typed answer left selected namespace: args=%v", call.Arg)
	}
	var context struct {
		Approval operatorauth.ApprovalContext `json:"approval"`
	}
	if err := json.Unmarshal([]byte(amqFlagValue(call.Arg, "context")), &context); err != nil {
		t.Fatal(err)
	}
	if context.Approval.QuestionMessageID != selected.ID || context.Approval.AnsweredByHandle != "user" {
		t.Fatalf("ordinary typed answer selected wrong owner/question: %+v", context.Approval)
	}
}

func TestOrdinaryTypedOperatorAnswerSelectedQuestionMismatchesFailBeforeSend(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*state.Message)
		extraArgs []string
		want      string
	}{
		{name: "namespace", mutate: func(q *state.Message) { q.AuthorizationRequest.Namespace.Generation = "other-generation" }, want: "namespace does not match admitted context"},
		{name: "routing", mutate: func(q *state.Message) { q.To = []string{"other-operator"} }, want: "addressed only to configured operator"},
		{name: "body", mutate: func(q *state.Message) {
			q.Body = strings.Replace(q.Body, "Gate-Kind: feature_branch_push", "Gate-Kind: merge", 1)
			q.RawBody = q.Body
		}, want: "exact Gate-Kind/Action/Target binding"},
		{name: "owner", mutate: func(q *state.Message) { q.From = "outside-owner" }, want: "requester is not a roster member"},
		{name: "override", extraArgs: []string{"--target", "v2"}, want: "override does not exactly match"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			project := t.TempDir()
			profile, session := "release", "issue-414"
			cfg := team.Team{
				Project: project, Workstream: session,
				Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: session}},
			}
			if err := team.WriteProfile(project, profile, cfg); err != nil {
				t.Fatal(err)
			}
			question := ordinaryTypedQuestionForContext(project, profile, session, "none", "ordinary-mismatch", "cto", "feature_branch_push", "v1", time.Now().UTC())
			if tc.mutate != nil {
				tc.mutate(&question)
			}
			writeAuthorizationMessage(t, filepath.Join(project, ".agent-mail", profile), session, "user", "new", question)
			deliveryBefore := v228ReceiptArtifactPaths(t, project)
			onSentBefore := snapshotTestDirectory(t, selfApprovalStoreDir(project, profile, session))
			calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent forbidden to cto\n")
			args := []string{"answer", "--project", project, "--profile", profile, "--session", session, "--gate", question.Thread, "--approved"}
			args = append(args, tc.extraArgs...)
			err := runOperator(args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v, want %q", err, tc.want)
			}
			if len(*calls) != 0 {
				t.Fatalf("mismatch invoked AMQ: %+v", *calls)
			}
			if deliveryAfter := v228ReceiptArtifactPaths(t, project); !reflect.DeepEqual(deliveryAfter, deliveryBefore) {
				t.Fatalf("mismatch created delivery receipt artifacts: before=%v after=%v", deliveryBefore, deliveryAfter)
			}
			if onSentAfter := snapshotTestDirectory(t, selfApprovalStoreDir(project, profile, session)); !reflect.DeepEqual(onSentAfter, onSentBefore) {
				t.Fatalf("mismatch invoked OnSent receipt: before=%v after=%v", onSentBefore, onSentAfter)
			}
		})
	}
}

func TestSendOperatorAMQExactNamedContextSkipsResolver(t *testing.T) {
	project := t.TempDir()
	profile, session := "release", "issue-414"
	root := filepath.Join(project, ".agent-mail", profile, session)
	ctx := amqContext{
		ProjectDir: project, Profile: profile, Root: root, Me: "user", Session: session,
		PinMode: amqPinExactRoot, NamespaceGeneration: "generation-named",
		Env: amqEnv{Root: root, BaseRoot: root, SessionName: session, Me: "user"},
	}
	previousResolve := resolveAMQEnvForAMQCommand
	previousRun := runAMQCommand
	resolverCalls := 0
	var request amqCommandRequest
	resolveAMQEnvForAMQCommand = func(string, string, string, string) (amqEnv, error) {
		resolverCalls++
		return amqEnv{}, errors.New("unexpected resolver fallback")
	}
	runAMQCommand = func(req amqCommandRequest) ([]byte, error) {
		request = req
		return []byte("Sent named-exact to cto\n"), nil
	}
	t.Cleanup(func() {
		resolveAMQEnvForAMQCommand = previousResolve
		runAMQCommand = previousRun
	})

	if err := sendOperatorAMQ(operatorSendOptions{
		Command: "operator answer", Project: project, Profile: profile, Session: session,
		From: "user", To: "cto", Thread: "gate/named", Kind: "answer", Subject: "APPROVED: named", Body: "approved",
		ExactContext: &ctx, Out: &bytes.Buffer{},
	}); err != nil {
		t.Fatal(err)
	}
	if resolverCalls != 0 {
		t.Fatalf("exact named context used resolver fallback %d times", resolverCalls)
	}
	if amqFlagValue(request.Arg, "root") != root || !envHas(request.Env, "AM_ROOT", root) || !envHas(request.Env, "AM_BASE_ROOT", root) || envHasPrefix(request.Env, "AM_SESSION", "") {
		t.Fatalf("named exact request lost root identity: args=%v env=%v", request.Arg, request.Env)
	}
}

func TestSendOperatorAMQTransportAndOnSentEventOrder(t *testing.T) {
	t.Run("reconciled runs OnSent without invoking AMQ", func(t *testing.T) {
		var events []string
		restore := installOperatorSendEventSeams(t, &events, nil)
		defer restore()
		boundary, err := newDurableInvocationBoundary(func(func() error) (durableInvocationResult, error) {
			events = append(events, "guard:replay")
			return newDurableReconciledExistingResult("existing-answer")
		})
		if err != nil {
			t.Fatal(err)
		}
		o := exactOperatorSendFixture(t)
		o.Invocation = boundary
		o.OnSent = func(id string) error {
			events = append(events, "onsent:"+id)
			return nil
		}
		if err := sendOperatorAMQ(o); err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(events, ","); got != "guard:replay,onsent:existing-answer" {
			t.Fatalf("event order=%s", got)
		}
	})

	t.Run("stable id plus send error still joins OnSent error", func(t *testing.T) {
		var events []string
		sendFailure := errors.New("send returned nonzero after stable id")
		onSentFailure := errors.New("verification receipt failed")
		restore := installOperatorSendEventSeams(t, &events, sendFailure)
		defer restore()
		o := exactOperatorSendFixture(t)
		o.OnSent = func(id string) error {
			events = append(events, "onsent:"+id)
			return onSentFailure
		}
		err := sendOperatorAMQ(o)
		if !errors.Is(err, sendFailure) || !errors.Is(err, onSentFailure) {
			t.Fatalf("joined error=%v", err)
		}
		if got := strings.Join(events, ","); got != "send,onsent:stable-answer" {
			t.Fatalf("event order=%s", got)
		}
	})

	t.Run("reconciled id plus guard error still runs OnSent", func(t *testing.T) {
		var events []string
		guardFailure := errors.New("guard release failed after replay")
		restore := installOperatorSendEventSeams(t, &events, nil)
		defer restore()
		boundary, err := newDurableInvocationBoundary(func(func() error) (durableInvocationResult, error) {
			events = append(events, "guard:replay")
			result, resultErr := newDurableReconciledExistingResult("existing-answer")
			return result, errors.Join(guardFailure, resultErr)
		})
		if err != nil {
			t.Fatal(err)
		}
		o := exactOperatorSendFixture(t)
		o.Invocation = boundary
		o.OnSent = func(id string) error {
			events = append(events, "onsent:"+id)
			return nil
		}
		err = sendOperatorAMQ(o)
		if !errors.Is(err, guardFailure) {
			t.Fatalf("guard error=%v", err)
		}
		if got := strings.Join(events, ","); got != "guard:replay,onsent:existing-answer" {
			t.Fatalf("event order=%s", got)
		}
	})
}

func exactOperatorSendFixture(t *testing.T) operatorSendOptions {
	t.Helper()
	project := t.TempDir()
	session := "issue-414"
	base := filepath.Join(project, ".agent-mail")
	root := filepath.Join(base, session)
	ctx := &amqContext{
		ProjectDir: project, Profile: team.DefaultProfile, Root: root, Me: "user", Session: session,
		PinMode: amqPinSessionful, NamespaceGeneration: "generation-default",
		Env: amqEnv{Root: root, BaseRoot: base, SessionName: session, Me: "user"},
	}
	return operatorSendOptions{
		Command: "operator answer", Project: project, Profile: team.DefaultProfile, Session: session,
		From: "user", To: "cto", Thread: "gate/release", Kind: "answer", Subject: "APPROVED: release", Body: "approved",
		ExactContext: ctx, Out: &bytes.Buffer{},
	}
}

func installOperatorSendEventSeams(t *testing.T, events *[]string, sendFailure error) func() {
	t.Helper()
	previousRun := runAMQCommand
	runAMQCommand = func(amqCommandRequest) ([]byte, error) {
		*events = append(*events, "send")
		return []byte("Sent stable-answer to cto\n"), sendFailure
	}
	return func() {
		runAMQCommand = previousRun
	}
}
