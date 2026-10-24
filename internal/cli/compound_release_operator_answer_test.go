package cli

import (
	"bytes"
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
			profile, session := squadnamespace.DefaultProfile, "issue-414"
			cfg := team.Team{
				Project: project, Session: session,
				Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: session}},
			}
			if err := team.Write(project, cfg); err != nil {
				t.Fatal(err)
			}
			question := ordinaryTypedQuestionForContext(project, profile, session, "none", "ordinary-mismatch", "cto", "feature_branch_push", "v1", time.Now().UTC())
			if tc.mutate != nil {
				tc.mutate(&question)
			}
			writeAuthorizationMessage(t, filepath.Join(project, ".agent-mail"), session, "user", "new", question)
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
	profile, session := squadnamespace.DefaultProfile, "issue-414"
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
		t.Fatalf("named exact request lost root identity: args=%v AM_ROOT=%v AM_BASE_ROOT=%v AM_SESSION set=%v", request.Arg, envHas(request.Env, "AM_ROOT", root), envHas(request.Env, "AM_BASE_ROOT", root), envHasPrefix(request.Env, "AM_SESSION", ""))
	}
}

func TestSendOperatorAMQTransportAndOnSentEventOrder(t *testing.T) {
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
}

func exactOperatorSendFixture(t *testing.T) operatorSendOptions {
	t.Helper()
	project := t.TempDir()
	session := "issue-414"
	base := filepath.Join(project, ".agent-mail")
	root := filepath.Join(base, session)
	ctx := &amqContext{
		ProjectDir: project, Profile: squadnamespace.DefaultProfile, Root: root, Me: "user", Session: session,
		PinMode: amqPinSessionful, NamespaceGeneration: "generation-default",
		Env: amqEnv{Root: root, BaseRoot: base, SessionName: session, Me: "user"},
	}
	return operatorSendOptions{
		Command: "operator answer", Project: project, Profile: squadnamespace.DefaultProfile, Session: session,
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
