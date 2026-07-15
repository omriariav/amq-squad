package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
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
	selected := ordinaryTypedQuestionForContext(project, profile, session, "none", "selected-named", "cto-named", "tag", "v1", time.Now().UTC())
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
		"--gate", selected.Thread, "--approved", "--kind", "tag", "--action", "tag", "--target", "v1",
		"--override-namespace-conflict", "--namespace-reason", "prove selected named typed question",
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
			q.Body = strings.Replace(q.Body, "Gate-Kind: tag", "Gate-Kind: release", 1)
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
			question := ordinaryTypedQuestionForContext(project, profile, session, "none", "ordinary-mismatch", "cto", "tag", "v1", time.Now().UTC())
			if tc.mutate != nil {
				tc.mutate(&question)
			}
			writeAuthorizationMessage(t, filepath.Join(project, ".agent-mail", profile), session, "user", "new", question)
			deliveryBefore := snapshotTestDirectory(t, deliveryReceiptDir(project, profile, session))
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
			if deliveryAfter := snapshotTestDirectory(t, deliveryReceiptDir(project, profile, session)); !reflect.DeepEqual(deliveryAfter, deliveryBefore) {
				t.Fatalf("mismatch reserved delivery receipt: before=%v after=%v", deliveryBefore, deliveryAfter)
			}
			if onSentAfter := snapshotTestDirectory(t, selfApprovalStoreDir(project, profile, session)); !reflect.DeepEqual(onSentAfter, onSentBefore) {
				t.Fatalf("mismatch invoked OnSent receipt: before=%v after=%v", onSentBefore, onSentAfter)
			}
		})
	}
}

func TestCompoundReleaseOperatorAnswerUsesGuardedExactSelectedRoot(t *testing.T) {
	fixture, active := newCLIActiveReleaseAttentionFixture(t)
	configureReleaseOperatorAnswerTeam(t, fixture)
	tag := activeReleaseChildByRole(t, active.Active.Children, operatorauth.ReleaseChildTag)
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent answer-tag to cto\n")

	err := runOperator([]string{
		"answer", "--project", fixture.adapter.project, "--session", fixture.adapter.session,
		"--gate", tag.Receipt.Thread, "--approved", "--reason", "release checks passed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("AMQ calls=%d, want one guarded send", len(*calls))
	}
	call := (*calls)[0]
	if got := amqFlagValue(call.Arg, "root"); got != fixture.adapter.root {
		t.Fatalf("send root=%q, want admitted exact root %q", got, fixture.adapter.root)
	}
	if got := amqFlagValue(call.Arg, "thread"); got != tag.Receipt.Thread {
		t.Fatalf("send thread=%q, want %q", got, tag.Receipt.Thread)
	}
	if !envHas(call.Env, "AM_ROOT", fixture.adapter.root) || !envHas(call.Env, "AM_BASE_ROOT", filepath.Dir(fixture.adapter.root)) || !envHas(call.Env, "AM_SESSION", fixture.adapter.session) {
		t.Fatalf("send env does not preserve selected default-profile tuple: %v", call.Env)
	}
}

func TestCompoundReleaseOperatorAnswerDeniedGitHubReleaseKeepsChildIdentityIsolated(t *testing.T) {
	fixture, active := newCLIActiveReleaseAttentionFixture(t)
	configureReleaseOperatorAnswerTeam(t, fixture)
	githubRelease := activeReleaseChildByRole(t, active.Active.Children, operatorauth.ReleaseChildGitHubRelease)
	tag := activeReleaseChildByRole(t, active.Active.Children, operatorauth.ReleaseChildTag)
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent answer-release to cto\n")

	if err := runOperator([]string{
		"answer", "--project", fixture.adapter.project, "--session", fixture.adapter.session,
		"--gate", githubRelease.Receipt.Thread, "--denied", "--reason", "release artifact withheld",
	}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("AMQ calls=%d, want one", len(*calls))
	}
	call := (*calls)[0]
	if thread := amqFlagValue(call.Arg, "thread"); thread != githubRelease.Receipt.Thread || thread == tag.Receipt.Thread {
		t.Fatalf("denial crossed child identity: thread=%q github=%q tag=%q", thread, githubRelease.Receipt.Thread, tag.Receipt.Thread)
	}
	if subject := amqFlagValue(call.Arg, "subject"); subject != "DENIED: "+strings.TrimPrefix(githubRelease.Receipt.Thread, "gate/") {
		t.Fatalf("denial subject=%q", subject)
	}
	var context struct {
		Approval operatorauth.ApprovalContext `json:"approval"`
	}
	if err := json.Unmarshal([]byte(amqFlagValue(call.Arg, "context")), &context); err != nil {
		t.Fatal(err)
	}
	if context.Approval.Action != "github_release" || context.Approval.GateKind != operatorauth.GateRelease || context.Approval.QuestionMessageID != githubRelease.QuestionMessageID {
		t.Fatalf("denial approval crossed child binding: %+v", context.Approval)
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

func TestSendOperatorAMQFinalizationAndOnSentEventOrder(t *testing.T) {
	t.Run("reconciled persists before OnSent without invoking AMQ", func(t *testing.T) {
		var events []string
		restore := installOperatorSendEventSeams(t, &events, nil, nil)
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
		if got := strings.Join(events, ","); got != "persist:reserved,guard:replay,persist:reconciled_existing,onsent:existing-answer" {
			t.Fatalf("event order=%s", got)
		}
	})

	t.Run("stable id plus send error still joins OnSent error", func(t *testing.T) {
		var events []string
		sendFailure := errors.New("send returned nonzero after stable id")
		onSentFailure := errors.New("verification receipt failed")
		restore := installOperatorSendEventSeams(t, &events, sendFailure, nil)
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
		if got := strings.Join(events, ","); got != "persist:reserved,persist:boundary,persist:stable,onsent:stable-answer" {
			t.Fatalf("event order=%s", got)
		}
	})

	t.Run("reconciled id plus guard error still runs OnSent", func(t *testing.T) {
		var events []string
		guardFailure := errors.New("guard release failed after replay")
		restore := installOperatorSendEventSeams(t, &events, nil, nil)
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
		if got := strings.Join(events, ","); got != "persist:reserved,guard:replay,persist:reconciled_existing,onsent:existing-answer" {
			t.Fatalf("event order=%s", got)
		}
	})

	t.Run("final persistence error always forbids OnSent", func(t *testing.T) {
		var events []string
		finalFailure := errors.New("final receipt disk failure")
		restore := installOperatorSendEventSeams(t, &events, nil, finalFailure)
		defer restore()
		o := exactOperatorSendFixture(t)
		o.OnSent = func(string) error {
			events = append(events, "onsent:forbidden")
			return nil
		}
		err := sendOperatorAMQ(o)
		var typed *durableFinalReceiptPersistError
		if !errors.As(err, &typed) || strings.Contains(strings.Join(events, ","), "onsent") {
			t.Fatalf("error=%v events=%v", err, events)
		}
	})

	t.Run("replay final persistence error also forbids OnSent", func(t *testing.T) {
		var events []string
		finalFailure := errors.New("replay final receipt disk failure")
		restore := installOperatorSendEventSeams(t, &events, nil, finalFailure)
		defer restore()
		boundary, err := newDurableInvocationBoundary(func(func() error) (durableInvocationResult, error) {
			return newDurableReconciledExistingResult("existing-answer")
		})
		if err != nil {
			t.Fatal(err)
		}
		o := exactOperatorSendFixture(t)
		o.Invocation = boundary
		o.OnSent = func(string) error {
			events = append(events, "onsent:forbidden")
			return nil
		}
		err = sendOperatorAMQ(o)
		var typed *durableFinalReceiptPersistError
		if !errors.As(err, &typed) || strings.Contains(strings.Join(events, ","), "onsent") {
			t.Fatalf("error=%v events=%v", err, events)
		}
	})
}

func TestCompoundReleaseOperatorAnswerIdenticalConcurrencyReconcilesAndOppositeFailsClosed(t *testing.T) {
	fixture, active := newCLIActiveReleaseAttentionFixture(t)
	configureReleaseOperatorAnswerTeam(t, fixture)
	tag := activeReleaseChildByRole(t, active.Active.Children, operatorauth.ReleaseChildTag)
	var sends atomic.Int32
	installPersistingOperatorAnswerAMQ(t, fixture.adapter.root, "answer-identical", &sends)
	args := []string{
		"answer", "--project", fixture.adapter.project, "--session", fixture.adapter.session,
		"--gate", tag.Receipt.Thread, "--approved", "--reason", "identical approval",
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- runOperator(args)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("identical concurrent answer: %v", err)
		}
	}
	if got := sends.Load(); got != 1 {
		t.Fatalf("identical concurrent AMQ sends=%d, want one send plus one replay", got)
	}
	replayArgs := append(append([]string(nil), args...), "--json")
	stdout, stderr, err := captureOutput(t, func() error { return runOperator(replayArgs) })
	if err != nil {
		t.Fatalf("JSON replay: %v\nstderr:\n%s", err, stderr)
	}
	replay := decodeJSONEnvelope[mutationResult](t, stdout).Data
	if replay.Status != deliveryStateReconciledExisting || replay.MessageID != "answer-identical" || replay.DeliveryReceipt == nil || replay.DeliveryReceipt.MessageID != "" || replay.DeliveryReceipt.ReconciledMessageID != "answer-identical" || replay.DeliveryReceipt.AMQInvoked {
		t.Fatalf("JSON replay mislabeled delivery by this attempt: %+v", replay)
	}
	if got := sends.Load(); got != 1 {
		t.Fatalf("JSON replay invoked AMQ; sends=%d", got)
	}

	opposite := append([]string(nil), args...)
	for i := range opposite {
		if opposite[i] == "--approved" {
			opposite[i] = "--denied"
		}
	}
	if err := runOperator(opposite); err == nil || !strings.Contains(err.Error(), "opposite or non-identical") {
		t.Fatalf("opposite answer error=%v, want guarded conflict", err)
	}
	if got := sends.Load(); got != 1 {
		t.Fatalf("opposite answer invoked AMQ; sends=%d", got)
	}
}

func installPersistingOperatorAnswerAMQ(t *testing.T, root, answerID string, sends *atomic.Int32) {
	t.Helper()
	previousResolve := resolveAMQEnvForAMQCommand
	previousRun := runAMQCommand
	resolveAMQEnvForAMQCommand = func(_ string, rootFlag, session, handle string) (amqEnv, error) {
		resolvedRoot := rootFlag
		if resolvedRoot == "" {
			resolvedRoot = root
		}
		return amqEnv{Root: resolvedRoot, BaseRoot: filepath.Dir(resolvedRoot), SessionName: session, Me: handle}, nil
	}
	runAMQCommand = func(req amqCommandRequest) ([]byte, error) {
		if len(req.Arg) == 0 || req.Arg[0] != "send" {
			return nil, fmt.Errorf("unexpected AMQ command: %v", req.Arg)
		}
		sends.Add(1)
		var context map[string]any
		if err := json.Unmarshal([]byte(amqFlagValue(req.Arg, "context")), &context); err != nil {
			return nil, err
		}
		created := time.Now().UTC().Add(time.Second)
		header := map[string]any{
			"schema": 1, "id": answerID, "from": amqFlagValue(req.Arg, "me"), "to": []string{amqFlagValue(req.Arg, "to")},
			"thread": amqFlagValue(req.Arg, "thread"), "subject": amqFlagValue(req.Arg, "subject"),
			"created": created.Format(time.RFC3339Nano), "priority": "normal", "kind": "answer", "context": context,
		}
		raw, err := json.Marshal(header)
		if err != nil {
			return nil, err
		}
		path := filepath.Join(root, "agents", amqFlagValue(req.Arg, "to"), "inbox", "new", answerID+".md")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		body := amqFlagValue(req.Arg, "body")
		if err := os.WriteFile(path, append(append([]byte("---json\n"), raw...), []byte("\n---\n"+body+"\n")...), 0o600); err != nil {
			return nil, err
		}
		return []byte("Sent " + answerID + " to " + amqFlagValue(req.Arg, "to") + "\n"), nil
	}
	t.Cleanup(func() {
		resolveAMQEnvForAMQCommand = previousResolve
		runAMQCommand = previousRun
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

func installOperatorSendEventSeams(t *testing.T, events *[]string, sendFailure, finalFailure error) func() {
	t.Helper()
	previousPersist := persistDeliveryReceipt
	previousRun := runAMQCommand
	persistDeliveryReceipt = func(_ string, _ string, _ string, receipt *deliveryReceiptData) error {
		stage := "reserved"
		switch {
		case receipt.ReconciledMessageID != "":
			stage = deliveryStateReconciledExisting
		case receipt.MessageID != "":
			stage = "stable"
		case receipt.AMQInvoked:
			stage = "boundary"
		}
		*events = append(*events, "persist:"+stage)
		if finalFailure != nil && (receipt.MessageID != "" || receipt.ReconciledMessageID != "") {
			return finalFailure
		}
		return nil
	}
	runAMQCommand = func(amqCommandRequest) ([]byte, error) {
		return []byte("Sent stable-answer to cto\n"), sendFailure
	}
	return func() {
		persistDeliveryReceipt = previousPersist
		runAMQCommand = previousRun
	}
}

func configureReleaseOperatorAnswerTeam(t *testing.T, fixture cliReleaseReceiptFixture) {
	t.Helper()
	cfg := team.Team{
		Project: fixture.adapter.project, Workstream: fixture.adapter.session,
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: fixture.adapter.session}},
	}
	if err := team.Write(fixture.adapter.project, cfg); err != nil {
		t.Fatal(err)
	}
}

func activeReleaseChildByRole(t *testing.T, children []operatorauth.ActiveReleaseChild, role string) operatorauth.ActiveReleaseChild {
	t.Helper()
	for _, child := range children {
		if child.Role == role {
			return child
		}
	}
	t.Fatalf("active release child role %q missing", role)
	return operatorauth.ActiveReleaseChild{}
}
