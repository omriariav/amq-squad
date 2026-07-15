package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func typedQuestion(project, session, id, action, target, note string, at time.Time) state.Message {
	capability, err := operatorauth.LookupAction(action)
	if err != nil {
		panic(err)
	}
	gate := "gate/test"
	request := operatorauth.GateRequestContext{
		SchemaVersion: operatorauth.GateRequestSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion,
		Gate: gate, Thread: gate,
		Namespace: operatorauth.NamespaceBinding{ProjectDir: project, Profile: team.DefaultProfile, Session: session, NamespaceID: squadnamespace.ID(team.DefaultProfile, session), Generation: "none"},
		GateKind:  capability.GateKind, Action: capability.Action, Target: target, Note: note,
	}
	body := "Gate-Kind: " + capability.GateKind + "\nAction: " + capability.Action + "\nTarget: " + target
	if note != "" {
		body += "\nNote: " + note
	}
	return withAuthorityRaw(state.Message{ID: id, From: "cto", To: []string{"user"}, Thread: gate, RawThread: gate, Kind: state.KindQuestion, Subject: "APPROVAL: test", Body: body, Created: at, AuthorizationRequestPresent: true, AuthorizationRequestValid: true, AuthorizationRequest: &request})
}

func withAuthorityRaw(msg state.Message) state.Message {
	msg.RawSubject, msg.RawBody, msg.AuthorityRaw = msg.Subject, msg.Body, true
	return msg
}

func typedHumanTeam() team.Team {
	op := team.DefaultOperator()
	return team.Team{Operator: &op, Members: []team.Member{{Role: "cto", Handle: "cto", Session: "s"}}}
}

func TestAuthorizationCatalogDrivesHelpStatusAndPositionalRejection(t *testing.T) {
	wantHuman := map[string]bool{}
	for _, capability := range operatorauth.ActionCapabilities() {
		if capability.HumanOnly {
			wantHuman[capability.GateKind] = true
		}
	}
	gotHuman := humanOnlyCatalogGateKinds()
	if len(gotHuman) != len(wantHuman) {
		t.Fatalf("human-only status kinds=%v catalog=%v", gotHuman, wantHuman)
	}
	for _, kind := range gotHuman {
		if !wantHuman[kind] || !operatorauth.HumanOnlyGateKind(kind) {
			t.Fatalf("status human-only kind %q is not catalog-backed", kind)
		}
	}
	_, help, err := captureOutput(t, func() error { return runVerify([]string{"action", "--help"}) })
	if err != nil && !errors.Is(err, flag.ErrHelp) {
		t.Fatal(err)
	}
	for _, action := range catalogActionNames() {
		if !strings.Contains(help, action) {
			t.Fatalf("verify action help omitted catalog action %q:\n%s", action, help)
		}
	}
	if err := runGate([]string{"raise", "--list-kinds", "junk"}); err == nil {
		t.Fatal("gate raise accepted positional argument after --list-kinds")
	}
	if err := runOperator([]string{"answer", "--list-kinds", "junk"}); err == nil {
		t.Fatal("operator answer accepted positional argument after --list-kinds")
	}
}

func TestTypedVerifierLatestMalformedAndDuplicateConflictFailClosed(t *testing.T) {
	project := t.TempDir()
	now := time.Now().UTC()
	valid := typedQuestion(project, "s", "q1", "protected_branch_push", "exact target", "", now)
	malformed := valid
	malformed.ID, malformed.Created = "q2", now.Add(time.Second)
	malformed.AuthorizationRequest, malformed.AuthorizationRequestValid, malformed.AuthorizationRequestError = nil, false, "unknown field"
	got := decideTypedVerifyActionWithPolicy([]state.Message{valid, malformed}, valid.Thread, "protected_branch_push", "exact target", "user", typedHumanTeam(), "s", project, team.DefaultProfile)
	if got.Decision != actionDecisionPending || got.Failures[0].Code != "typed_gate_malformed" {
		t.Fatalf("latest malformed typed request fell back: %+v", got)
	}

	conflict := valid
	conflict.Body += "\nchanged"
	conflict = withAuthorityRaw(conflict)
	got = decideTypedVerifyActionWithPolicy([]state.Message{valid, conflict}, valid.Thread, "protected_branch_push", "exact target", "user", typedHumanTeam(), "s", project, team.DefaultProfile)
	if got.Failures[0].Code != "duplicate_message_conflict" {
		t.Fatalf("duplicate-ID conflict was not a barrier: %+v", got)
	}
}

func TestOperatorAnswerRejectsConflictingQuestionCopiesBeforeSend(t *testing.T) {
	project, base, _ := seedNotifyProject(t, team.DefaultOperator())
	seedNotifyLaunch(t, project, base, "s", "cto")
	now := time.Now().UTC()
	question := typedQuestion(project, "s", "q1", "github_release", "release target", "", now)
	question.To = []string{"user"}
	writeAuthorizationMessage(t, base, "s", "user", "new", question)
	conflict := question
	conflict.Body += "\nconflicting copy"
	conflict = withAuthorityRaw(conflict)
	writeAuthorizationMessage(t, base, "s", "cto", "cur", conflict)
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent should-not-send to cto\n")
	err := runOperator([]string{"answer", "--project", project, "--session", "s", "--gate", question.Thread, "--approved"})
	if err == nil || !strings.Contains(err.Error(), "conflicting mailbox copies") {
		t.Fatalf("operator answer conflict = %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("operator answer made AMQ calls after conflict: %+v", *calls)
	}
}

func TestAmbiguousRawGateFrontmatterBlocksVerifierAnswerAndAttention(t *testing.T) {
	project, base, _ := seedNotifyProject(t, team.DefaultOperator())
	seedNotifyLaunch(t, project, base, "s", "cto")
	question := typedQuestion(project, "s", "q-ambiguous", "tag", "v1", "", time.Now().UTC())
	writeAuthorizationMessage(t, base, "s", "user", "new", question)
	path := filepath.Join(base, "s", "agents", "user", "inbox", "new", question.ID+".md")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	mutated := strings.Replace(string(b), `"generation":"none"`, `"generation":"none","generation":"other"`, 1)
	if mutated == string(b) {
		t.Fatal("fixture generation field not found")
	}
	if err := os.WriteFile(path, []byte(mutated), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err = executeVerifyAction(verifyActionExecution{ProjectDir: project, Profile: team.DefaultProfile, Session: "s", Gate: question.Thread, Action: "tag", Target: "v1", BaseRoot: base, Out: &out, JSON: true})
	if err == nil || !strings.Contains(err.Error(), "message scan degraded") {
		t.Fatalf("verify ambiguous error=%v output=%s", err, out.String())
	}

	calls := withAMQCommandSeams(t, amqEnv{Root: filepath.Join(base, "{session}"), BaseRoot: base}, "Sent should-not-send to cto\n")
	err = runOperator([]string{"answer", "--project", project, "--session", "s", "--gate", question.Thread, "--approved"})
	if err == nil || !strings.Contains(err.Error(), "message scan degraded") {
		t.Fatalf("operator answer ambiguous error=%v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("ambiguous operator answer sent %d AMQ messages", len(*calls))
	}

	snap, err := state.Build(project, base, state.DefaultProbe)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := team.Read(project)
	if err != nil {
		t.Fatal(err)
	}
	items := collectGateAttentionProjection(cfg, project, team.DefaultProfile, snap, "user", "s", time.Now())
	for _, item := range items {
		if item.Thread == question.Thread && !item.Cleared {
			t.Fatalf("ambiguous raw gate became actionable attention: %+v", item)
		}
	}
}

func TestOperatorSelfApproveRejectsConflictingQuestionCopiesBeforeSend(t *testing.T) {
	fx := newSelfApprovalRecoveryFixture(t, false)
	base := filepath.Join(fx.project, ".agent-mail")
	conflict := fx.question
	conflict.Body += "\nconflicting copy"
	conflict = withAuthorityRaw(conflict)
	writeSelfApprovalTestMessage(t, filepath.Join(base, "s", "agents", "cto"), "cur", conflict, nil)
	oldActor := resolveVerifiedOperatorActor
	resolveVerifiedOperatorActor = func(_, profile, session, role, handle string) (verifiedOperatorActor, error) {
		return verifiedOperatorActor{Profile: profile, Session: session, Role: role, Handle: handle}, nil
	}
	t.Cleanup(func() { resolveVerifiedOperatorActor = oldActor })
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent should-not-send to cto\n")
	err := runOperatorSelfApprove([]string{"--project", fx.project, "--session", "s", "--gate", fx.gate, "--kind", "merge", "--action", fx.action, "--target", fx.target, "--evidence", filepath.Join(fx.project, "unused.json")})
	if err == nil || !strings.Contains(err.Error(), "conflicting mailbox copies") {
		t.Fatalf("operator self-approve conflict = %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("operator self-approve made AMQ calls after conflict: %+v", *calls)
	}
}

func TestAuthorityDuplicateVariantsFailClosedAcrossSurfaces(t *testing.T) {
	variants := []struct {
		name    string
		mutate  func(*state.Message)
		rewrite func(string) string
	}{
		{name: "created", mutate: func(m *state.Message) { m.Created = m.Created.Add(time.Second) }},
		{name: "to order", mutate: func(m *state.Message) { m.To = []string{"other", "user"} }},
		{name: "raw thread", rewrite: func(s string) string { return strings.Replace(s, `"thread":"gate/test"`, `"thread":"gate//test"`, 1) }},
		{name: "schema", rewrite: func(s string) string { return strings.Replace(s, `"schema":1`, `"schema":2`, 1) }},
		{name: "raw subject", rewrite: func(s string) string {
			return strings.Replace(s, `"subject":"APPROVAL: test"`, `"subject":" APPROVAL: test"`, 1)
		}},
		{name: "raw body", rewrite: func(s string) string { return strings.Replace(s, "\n---\nGate-Kind:", "\n---\n Gate-Kind:", 1) }},
		{name: "typed context", mutate: func(m *state.Message) {
			copy := *m.AuthorizationRequest
			copy.Note = "different"
			m.AuthorizationRequest = &copy
		}},
	}
	for _, variant := range variants {
		t.Run(variant.name, func(t *testing.T) {
			project, base, _ := seedNotifyProject(t, team.DefaultOperator())
			seedNotifyLaunch(t, project, base, "s", "cto")
			question := typedQuestion(project, "s", "q1", "github_release", "release target", "", time.Now().UTC())
			writeAuthorizationMessage(t, base, "s", "user", "new", question)
			conflict := question
			if variant.mutate != nil {
				variant.mutate(&conflict)
			}
			writeAuthorizationMessage(t, base, "s", "cto", "cur", conflict)
			conflictPath := filepath.Join(base, "s", "agents", "cto", "inbox", "cur", "q1.md")
			if variant.rewrite != nil {
				b, err := os.ReadFile(conflictPath)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(conflictPath, []byte(variant.rewrite(string(b))), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			snap, err := state.Build(project, base, state.DefaultProbe)
			if err != nil {
				t.Fatal(err)
			}
			sess, ok := findThreadsSession(snap.Sessions, team.DefaultProfile, "s")
			if !ok {
				t.Fatal("session missing")
			}
			msgs, _ := state.ScanSessionMessages(sess.Root, time.Now)
			cfg, err := team.Read(project)
			if err != nil {
				t.Fatal(err)
			}
			verified := decideTypedVerifyActionWithPolicy(msgs, question.Thread, "github_release", "release target", "user", cfg, "s", project, team.DefaultProfile)
			if verified.Decision == actionDecisionApproved || len(verified.Failures) == 0 || verified.Failures[0].Code != "duplicate_message_conflict" {
				t.Fatalf("verifier did not fail closed on duplicate: %+v", verified)
			}
			status, err := buildOperatorStatusData(operatorExecution{ProjectDir: project, Profile: team.DefaultProfile, Session: "s", BaseRoot: base, Probe: probeForNext(), Now: time.Now})
			if err != nil {
				t.Fatal(err)
			}
			if _, open := gateAttention(status.Attention, question.Thread); !open {
				t.Fatalf("conflicting duplicate did not remain visible: %+v", status.Attention)
			}
			calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent impossible\n")
			if err := runOperator([]string{"answer", "--project", project, "--session", "s", "--gate", question.Thread, "--approved"}); err == nil {
				t.Fatal("operator answer accepted conflicting duplicate")
			}
			if len(*calls) != 0 {
				t.Fatalf("operator answer wrote after duplicate: %+v", *calls)
			}

			fx := newSelfApprovalRecoveryFixture(t, false)
			selfConflict := fx.question
			if variant.mutate != nil {
				variant.mutate(&selfConflict)
			}
			writeSelfApprovalTestMessage(t, filepath.Join(fx.project, ".agent-mail", "s", "agents", "cto"), "cur", selfConflict, nil)
			selfPath := filepath.Join(fx.project, ".agent-mail", "s", "agents", "cto", "inbox", "cur", "q1.md")
			if variant.rewrite != nil {
				b, err := os.ReadFile(selfPath)
				if err != nil {
					t.Fatal(err)
				}
				rewritten := variant.rewrite(string(b))
				if variant.name == "raw thread" {
					rewritten = strings.Replace(rewritten, `"thread":"gate/merge-398"`, `"thread":"gate//merge-398"`, 1)
				}
				if variant.name == "raw subject" {
					rewritten = strings.Replace(rewritten, `"subject":"APPROVAL: merge-398"`, `"subject":" APPROVAL: merge-398"`, 1)
				}
				if err := os.WriteFile(selfPath, []byte(rewritten), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			oldActor := resolveVerifiedOperatorActor
			resolveVerifiedOperatorActor = func(_, profile, session, role, handle string) (verifiedOperatorActor, error) {
				return verifiedOperatorActor{Profile: profile, Session: session, Role: role, Handle: handle}, nil
			}
			t.Cleanup(func() { resolveVerifiedOperatorActor = oldActor })
			selfCalls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent impossible\n")
			if err := runOperatorSelfApprove([]string{"--project", fx.project, "--session", "s", "--gate", fx.gate, "--kind", "merge", "--action", fx.action, "--target", fx.target, "--evidence", filepath.Join(fx.project, "unused.json")}); err == nil {
				t.Fatal("self approve accepted conflicting duplicate")
			}
			if len(*selfCalls) != 0 {
				t.Fatalf("self approve wrote after duplicate: %+v", *selfCalls)
			}
			if _, err := os.Stat(fx.reservationPath); err != nil {
				t.Fatalf("self reservation changed after duplicate: %v", err)
			}
			if matches, _ := filepath.Glob(filepath.Join(selfApprovalStoreDir(fx.project, team.DefaultProfile, "s"), "*.receipt.json")); len(matches) != 0 {
				t.Fatalf("self receipt created after duplicate: %v", matches)
			}
		})
	}
}

func TestTypedVerifierRawAndV1AnswersAreDiagnosticOnly(t *testing.T) {
	project := t.TempDir()
	now := time.Now().UTC()
	raw := state.Message{ID: "q", From: "cto", Thread: "gate/test", Kind: state.KindQuestion, Subject: "APPROVAL", Body: "Gate-Kind: merge\nAction: protected_branch_push\nTarget: exact", Created: now}
	rawAnswer := state.Message{ID: "a", From: "user", Thread: raw.Thread, Kind: state.KindAnswer, Subject: "APPROVED", Body: raw.Body, Created: now.Add(time.Second)}
	got := decideTypedVerifyActionWithPolicy([]state.Message{raw, rawAnswer}, raw.Thread, "protected_branch_push", "exact", "user", team.Team{}, "s", project, team.DefaultProfile)
	if got.Decision != actionDecisionUnbound || got.Failures[0].Code != "legacy_gate_diagnostic_only" {
		t.Fatalf("raw gate authorized: %+v", got)
	}

	question := typedQuestion(project, "s", "q2", "protected_branch_push", "exact", "", now)
	v1 := operatorauth.ApprovalContext{SchemaVersion: operatorauth.ApprovalSchemaVersionV1, Source: "human", GateKind: operatorauth.GateMerge, Action: "protected_branch_push", Target: "exact", QuestionMessageID: question.ID, AnsweredByRole: "operator", AnsweredByHandle: "user", VerifiedAt: now.Format(time.RFC3339Nano)}
	answer := state.Message{ID: "a2", From: "user", To: []string{"cto"}, Thread: question.Thread, Kind: state.KindAnswer, Subject: "APPROVED: test", Body: question.Body, Created: now.Add(time.Second), ApprovalPresent: true, ApprovalValid: true, Approval: &v1}
	answer = withAuthorityRaw(answer)
	got = decideTypedVerifyActionWithPolicy([]state.Message{question, answer}, question.Thread, "protected_branch_push", "exact", "user", typedHumanTeam(), "s", project, team.DefaultProfile)
	if got.Decision != actionDecisionPending || got.Failures[0].Code != "legacy_approval_v1_diagnostic_only" {
		t.Fatalf("v1 answer authorized: %+v", got)
	}
}

func TestTypedVerifierHumanApprovalAndDenialRequireV2Receipts(t *testing.T) {
	project := t.TempDir()
	now := time.Now().UTC()
	question := typedQuestion(project, "s", "q", "github_release", "release target", "integrity note", now)
	makeAnswer := func(id, decision string, at time.Time) state.Message {
		approval := operatorauth.ApprovalContext{SchemaVersion: operatorauth.ApprovalSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion, Source: "human", GateKind: operatorauth.GateRelease, Action: "github_release", Target: "release target", Note: "integrity note", QuestionMessageID: question.ID, AnsweredByRole: "operator", AnsweredByHandle: "user", VerifiedAt: at.Format(time.RFC3339Nano)}
		answer := state.Message{ID: id, From: "user", To: []string{"cto"}, Thread: question.Thread, Kind: state.KindAnswer, Subject: decision + ": test", Body: question.Body, Created: at, ApprovalPresent: true, ApprovalValid: true, Approval: &approval}
		answer = withAuthorityRaw(answer)
		receipt := operatorauth.Receipt{SchemaVersion: operatorauth.ReceiptSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion, Gate: question.Thread, GateKind: approval.GateKind, Action: approval.Action, Target: approval.Target, Note: approval.Note, Decision: strings.ToLower(decision), ApprovalSource: "human", QuestionMessageID: question.ID, AnswerMessageID: id, AnsweredBy: "user"}
		if err := writeSelfApprovalReceipt(project, team.DefaultProfile, "s", question.Thread, id, receipt); err != nil {
			t.Fatal(err)
		}
		return answer
	}
	approved := makeAnswer("a1", "APPROVED", now.Add(time.Second))
	got := decideTypedVerifyActionWithPolicy([]state.Message{question, approved}, question.Thread, "github_release", "release target", "user", typedHumanTeam(), "s", project, team.DefaultProfile)
	if got.Decision != actionDecisionApproved || got.MessageID != approved.ID || got.ApprovalSource != "human" {
		t.Fatalf("v2 approved answer did not authorize: %+v", got)
	}
	denied := makeAnswer("a2", "DENIED", approved.Created)
	got = decideTypedVerifyActionWithPolicy([]state.Message{question, approved, denied}, question.Thread, "github_release", "release target", "user", typedHumanTeam(), "s", project, team.DefaultProfile)
	if got.Decision != actionDecisionDenied || got.MessageID != denied.ID || got.Failures[0].Code != "gate_denied" {
		t.Fatalf("v2 denial did not win deterministic precedence: %+v", got)
	}
}

func TestTypedGateAttentionParityAcrossOperatorModes(t *testing.T) {
	modes := []string{team.OperatorInteractionLeadPane, team.OperatorInteractionSeparateTerminal, team.OperatorInteractionSelfOperator}
	variants := []struct {
		name           string
		wantOpen       bool
		wantAnswerable bool
		wantReason     string
		decision       string
		reraised       bool
		configure      func(*state.Message, *operatorauth.ApprovalContext)
		receipt        func(operatorauth.ApprovalContext) *operatorauth.Receipt
		mutateReraise  func(*state.Message)
	}{
		{name: "raw answer", wantOpen: true, wantAnswerable: true, wantReason: "human_approval_context_missing_or_malformed"},
		{name: "malformed approval", wantOpen: true, wantAnswerable: true, wantReason: "human_approval_context_missing_or_malformed", configure: func(answer *state.Message, _ *operatorauth.ApprovalContext) {
			answer.ApprovalPresent = true
		}},
		{name: "v1 approval", wantOpen: true, wantAnswerable: true, wantReason: "legacy_approval_v1_diagnostic_only", configure: func(answer *state.Message, approval *operatorauth.ApprovalContext) {
			approval.SchemaVersion = operatorauth.ApprovalSchemaVersionV1
			approval.TaxonomyVersion = 0
			approval.Note = ""
			answer.ApprovalPresent, answer.ApprovalValid, answer.Approval = true, true, approval
		}},
		{name: "receiptless v2", wantOpen: true, wantAnswerable: true, wantReason: "human_receipt_invalid", configure: useTypedApproval},
		{name: "note mismatch", wantOpen: true, wantAnswerable: true, wantReason: "human_approval_note_mismatch", configure: func(answer *state.Message, approval *operatorauth.ApprovalContext) {
			approval.Note = "different note"
			answer.ApprovalPresent, answer.ApprovalValid, answer.Approval = true, true, approval
		}, receipt: exactHumanReceipt},
		{name: "exact v2 receipt", wantOpen: false, configure: useTypedApproval, receipt: exactHumanReceipt},
		{name: "exact v2 denial receipt", wantOpen: false, decision: "denied", configure: useTypedApproval, receipt: exactHumanReceipt},
		{name: "re-raised newer question", wantOpen: true, wantAnswerable: true, wantReason: "gate_pending", reraised: true, configure: useTypedApproval, receipt: exactHumanReceipt},
		{name: "later typed-looking wrong kind", wantOpen: true, wantAnswerable: true, wantReason: "gate_pending", reraised: true, configure: func(answer *state.Message, _ *operatorauth.ApprovalContext) { answer.Created = time.Time{} }, mutateReraise: func(q *state.Message) { q.Kind = state.KindDecision }},
		{name: "later wrong recipient", wantOpen: true, wantReason: "typed_gate_routing_invalid", reraised: true, mutateReraise: func(q *state.Message) { q.To = []string{"other"} }},
		{name: "later multiple recipients", wantOpen: true, wantReason: "typed_gate_routing_invalid", reraised: true, mutateReraise: func(q *state.Message) { q.To = []string{"user", "other"} }},
		{name: "later foreign requester", wantOpen: true, wantReason: "typed_gate_routing_invalid", reraised: true, mutateReraise: func(q *state.Message) { q.From = "intruder" }},
		{name: "later malformed typed question", wantOpen: true, wantReason: "typed_gate_malformed", reraised: true, mutateReraise: func(q *state.Message) {
			q.AuthorizationRequestValid = false
			q.AuthorizationRequest = nil
			q.AuthorizationRequestError = "malformed"
		}},
		{name: "later valid re-raise", wantOpen: true, wantAnswerable: true, wantReason: "gate_pending", reraised: true},
	}
	for _, mode := range modes {
		for _, variant := range variants {
			t.Run(mode+"/"+variant.name, func(t *testing.T) {
				project := t.TempDir()
				base := filepath.Join(project, ".agent-mail")
				op := team.DefaultOperator()
				op.InteractionMode = mode
				if mode == team.OperatorInteractionSelfOperator {
					op.SelfOperator = &team.SelfOperatorPolicy{LeadRole: "cto", PolicyRevision: 1, Sessions: map[string]team.SelfOperatorSessionPolicy{"s": {Enabled: true, AllowedGateKinds: []string{operatorauth.GateMerge}}}}
				}
				cfg := team.Team{Project: project, Workstream: "s", Orchestrated: true, Lead: "cto", Operator: &op, Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: "s"}}}
				if err := team.Write(project, cfg); err != nil {
					t.Fatal(err)
				}
				seedNotifyLaunch(t, project, base, "s", "cto")
				now := notifyNow
				question := typedQuestion(project, "s", "q1", "github_release", "release target", "integrity note", now)
				question.To = []string{"user"}
				writeAuthorizationMessage(t, base, "s", "user", "new", question)
				approval := operatorauth.ApprovalContext{SchemaVersion: operatorauth.ApprovalSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion, Source: "human", GateKind: operatorauth.GateRelease, Action: "github_release", Target: "release target", Note: "integrity note", QuestionMessageID: question.ID, AnsweredByRole: "operator", AnsweredByHandle: "user", VerifiedAt: now.Add(time.Second).Format(time.RFC3339Nano)}
				decision := variant.decision
				if decision == "" {
					decision = "approved"
				}
				answer := state.Message{ID: "a1", From: "user", To: []string{"cto"}, Thread: question.Thread, RawThread: question.Thread, Kind: state.KindAnswer, Subject: strings.ToUpper(decision) + ": test", Body: question.Body, Created: now.Add(time.Second)}
				if variant.configure != nil {
					variant.configure(&answer, &approval)
				}
				writeAuthorizationMessage(t, base, "s", "cto", "cur", answer)
				if variant.receipt != nil {
					receipt := variant.receipt(approval)
					receipt.Decision = decision
					receipt.Gate, receipt.QuestionMessageID, receipt.AnswerMessageID = question.Thread, question.ID, answer.ID
					if err := writeSelfApprovalReceipt(project, team.DefaultProfile, "s", question.Thread, answer.ID, *receipt); err != nil {
						t.Fatal(err)
					}
				}
				if variant.reraised {
					reraised := question
					reraised.ID, reraised.Created = "q2", now.Add(2*time.Second)
					if variant.mutateReraise != nil {
						variant.mutateReraise(&reraised)
					}
					writeAuthorizationMessage(t, base, "s", "user", "new", reraised)
				}

				status, err := buildOperatorStatusData(operatorExecution{ProjectDir: project, Profile: team.DefaultProfile, Session: "s", BaseRoot: base, Probe: probeForNext(), Now: func() time.Time { return now.Add(2 * time.Second) }})
				if err != nil {
					t.Fatal(err)
				}
				if got := status.OperatorLoop.GatesOpen; got != boolInt(variant.wantOpen) {
					t.Fatalf("status gates_open=%d want_open=%t attention=%+v", got, variant.wantOpen, status.Attention)
				}
				var projectedGate operatorAttention
				if variant.wantOpen {
					projectedGate = findGateAttention(t, status.Attention, question.Thread)
					if projectedGate.Summary != variant.wantReason {
						t.Fatalf("status reason=%q want=%q", projectedGate.Summary, variant.wantReason)
					}
					if projectedGate.Answerable != variant.wantAnswerable {
						t.Fatalf("status answerable=%t want=%t attention=%+v", projectedGate.Answerable, variant.wantAnswerable, projectedGate)
					}
					if variant.wantAnswerable && projectedGate.Respond == "" {
						t.Fatalf("answerable gate has empty respond command: %+v", projectedGate)
					}
					if !variant.wantAnswerable && projectedGate.Respond != "" {
						t.Fatalf("inspect-only gate has respond command: %+v", projectedGate)
					}
				}

				snap, buildErr := state.Build(project, base, probeForNext())
				if buildErr != nil {
					t.Fatal(buildErr)
				}
				sess, ok := findThreadsSession(snap.Sessions, team.DefaultProfile, "s")
				if !ok {
					t.Fatal("parity session missing")
				}
				msgs, warnings := state.ScanSessionMessages(sess.Root, func() time.Time { return now.Add(3 * time.Second) })
				if len(warnings) > 0 {
					t.Fatalf("parity scan warnings: %+v", warnings)
				}
				verified := decideTypedVerifyActionWithPolicy(msgs, question.Thread, "github_release", "release target", "user", cfg, "s", project, team.DefaultProfile)
				switch {
				case variant.wantOpen:
					if verified.Decision != actionDecisionPending || len(verified.Failures) == 0 || verified.Failures[0].Code != variant.wantReason {
						t.Fatalf("verifier=%+v, want pending/%s matching projection", verified, variant.wantReason)
					}
				case decision == "denied":
					if verified.Decision != actionDecisionDenied || len(verified.Failures) == 0 || verified.Failures[0].Code != "gate_denied" {
						t.Fatalf("verifier=%+v, want denied/gate_denied", verified)
					}
				default:
					if verified.Decision != actionDecisionApproved || len(verified.Failures) != 0 {
						t.Fatalf("verifier=%+v, want approved", verified)
					}
				}

				var notifyOut bytes.Buffer
				if err := executeNotify(notifyExecution{ProjectDir: project, Profile: team.DefaultProfile, Session: "s", BaseRoot: base, StatePath: filepath.Join(project, "notify.json"), RenotifyAfter: time.Hour, DryRun: true, JSON: true, Out: &notifyOut, Probe: probeForNext(), Now: func() time.Time { return now.Add(2 * time.Second) }}); err != nil {
					t.Fatal(err)
				}
				notifyData := decodeJSONEnvelope[notifyEnvelopeData](t, notifyOut.String()).Data
				_, notifyOpen := gateAttention(notifyData.Notifications, question.Thread)
				if notifyOpen != variant.wantOpen {
					t.Fatalf("notify open=%t want=%t notifications=%+v", notifyOpen, variant.wantOpen, notifyData.Notifications)
				}

				var nextOut bytes.Buffer
				nextErr := executeNext(nextExecution{ProjectDir: project, Profile: team.DefaultProfile, Session: "s", BaseRoot: base, JSON: true, Out: &nextOut, Probe: probeForNext(), Now: func() time.Time { return now.Add(2 * time.Second) }})
				next := decodeJSONEnvelope[nextActionData](t, nextOut.String()).Data
				if variant.wantOpen && projectedGate.Answerable {
					if nextErr != nil || next.ID != "gate_answer" {
						t.Fatalf("next err=%v action=%+v, want gate_answer", nextErr, next)
					}
				} else if variant.wantOpen {
					if nextErr != nil || next.ID != projectedGate.EventType || next.ActionKind != "display" || next.Command != projectedGate.Inspect || strings.Contains(next.Command, "operator answer") {
						t.Fatalf("next err=%v action=%+v, want inspect-only display for %+v", nextErr, next, projectedGate)
					}
				} else if next.ID == "gate_answer" {
					t.Fatalf("next err=%v action=%+v, exact typed receipt left gate open", nextErr, next)
				}
			})
		}
	}
}

func TestTypedGateNewerExactBoundAnswerReplacesInvalidAnswerBarrier(t *testing.T) {
	project := t.TempDir()
	base := filepath.Join(project, ".agent-mail")
	op := team.DefaultOperator()
	op.InteractionMode = team.OperatorInteractionLeadPane
	cfg := team.Team{Project: project, Workstream: "s", Orchestrated: true, Lead: "cto", Operator: &op, Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: "s"}}}
	if err := team.Write(project, cfg); err != nil {
		t.Fatal(err)
	}
	seedNotifyLaunch(t, project, base, "s", "cto")
	now := notifyNow
	question := typedQuestion(project, "s", "q1", "github_release", "release target", "integrity note", now)
	question.To = []string{"user"}
	writeAuthorizationMessage(t, base, "s", "user", "new", question)

	invalid := state.Message{ID: "a1", From: "user", To: []string{"cto"}, Thread: question.Thread, RawThread: question.Thread, Kind: state.KindAnswer, Subject: "APPROVED: invalid", Body: question.Body, Created: now.Add(time.Second)}
	writeAuthorizationMessage(t, base, "s", "cto", "cur", invalid)

	approval := operatorauth.ApprovalContext{SchemaVersion: operatorauth.ApprovalSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion, Source: "human", GateKind: operatorauth.GateRelease, Action: "github_release", Target: "release target", Note: "integrity note", QuestionMessageID: question.ID, AnsweredByRole: "operator", AnsweredByHandle: "user", VerifiedAt: now.Add(2 * time.Second).Format(time.RFC3339Nano)}
	exact := state.Message{ID: "a2", From: "user", To: []string{"cto"}, Thread: question.Thread, RawThread: question.Thread, Kind: state.KindAnswer, Subject: "APPROVED: test", Body: question.Body, Created: now.Add(2 * time.Second)}
	useTypedApproval(&exact, &approval)
	writeAuthorizationMessage(t, base, "s", "cto", "cur", exact)
	receipt := exactHumanReceipt(approval)
	receipt.Decision = "approved"
	receipt.Gate, receipt.QuestionMessageID, receipt.AnswerMessageID = question.Thread, question.ID, exact.ID
	if err := writeSelfApprovalReceipt(project, team.DefaultProfile, "s", question.Thread, exact.ID, *receipt); err != nil {
		t.Fatal(err)
	}

	status, err := buildOperatorStatusData(operatorExecution{ProjectDir: project, Profile: team.DefaultProfile, Session: "s", BaseRoot: base, Probe: probeForNext(), Now: func() time.Time { return now.Add(3 * time.Second) }})
	if err != nil {
		t.Fatal(err)
	}
	if status.OperatorLoop.GatesOpen != 0 {
		t.Fatalf("newer exact answer left gate open: %+v", status.Attention)
	}
	if _, open := gateAttention(status.Attention, question.Thread); open {
		t.Fatalf("newer exact answer left active attention: %+v", status.Attention)
	}

	snap, err := state.Build(project, base, probeForNext())
	if err != nil {
		t.Fatal(err)
	}
	sess, ok := findThreadsSession(snap.Sessions, team.DefaultProfile, "s")
	if !ok {
		t.Fatal("replacement session missing")
	}
	msgs, warnings := state.ScanSessionMessages(sess.Root, func() time.Time { return now.Add(3 * time.Second) })
	if len(warnings) > 0 {
		t.Fatalf("replacement scan warnings: %+v", warnings)
	}
	verified := decideTypedVerifyActionWithPolicy(msgs, question.Thread, "github_release", "release target", "user", cfg, "s", project, team.DefaultProfile)
	if verified.Decision != actionDecisionApproved || verified.MessageID != exact.ID || len(verified.Failures) != 0 {
		t.Fatalf("newer exact answer did not replace invalid barrier: %+v", verified)
	}
}

func useTypedApproval(answer *state.Message, approval *operatorauth.ApprovalContext) {
	answer.ApprovalPresent, answer.ApprovalValid, answer.Approval = true, true, approval
}

func exactHumanReceipt(approval operatorauth.ApprovalContext) *operatorauth.Receipt {
	return &operatorauth.Receipt{SchemaVersion: operatorauth.ReceiptSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion, GateKind: approval.GateKind, Action: approval.Action, Target: approval.Target, Note: approval.Note, Decision: "approved", ApprovalSource: "human", AnsweredBy: "user"}
}

func writeAuthorizationMessage(t *testing.T, base, session, owner, box string, msg state.Message) {
	t.Helper()
	meta := map[string]any{"schema": 1, "id": msg.ID, "from": msg.From, "to": msg.To, "thread": msg.Thread, "subject": msg.Subject, "created": msg.Created.UTC().Format(time.RFC3339Nano), "kind": string(msg.Kind)}
	context := map[string]any{}
	if msg.AuthorizationRequestPresent {
		if msg.AuthorizationRequest != nil {
			context["authorization_request"] = msg.AuthorizationRequest
		} else {
			context["authorization_request"] = map[string]any{"schema_version": 1, "unexpected": true}
		}
	}
	if msg.ApprovalPresent {
		if msg.Approval != nil {
			context["approval"] = msg.Approval
		} else {
			context["approval"] = map[string]any{"schema_version": 2, "unexpected": true}
		}
	}
	if len(context) > 0 {
		meta["context"] = context
	}
	b, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, session, "agents", owner, "inbox", box)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, msg.ID+".md"), []byte("---json\n"+string(b)+"\n---\n"+msg.Body+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func findGateAttention(t *testing.T, items []operatorAttention, gate string) operatorAttention {
	t.Helper()
	if item, ok := gateAttention(items, gate); ok {
		return item
	}
	t.Fatalf("gate %s absent from attention: %+v", gate, items)
	return operatorAttention{}
}

func gateAttention(items []operatorAttention, gate string) (operatorAttention, bool) {
	for _, item := range items {
		if item.EventType == "gate" && item.Thread == gate && !item.Cleared {
			return item, true
		}
	}
	return operatorAttention{}, false
}

func TestTypedVerifierExactTargetNoteAndDuplicateBody(t *testing.T) {
	project := t.TempDir()
	question := typedQuestion(project, "s", "q", "protected_branch_push", "two  spaces", "integrity note", time.Now().UTC())
	got := decideTypedVerifyActionWithPolicy([]state.Message{question}, question.Thread, "protected_branch_push", "two spaces", "user", typedHumanTeam(), "s", project, team.DefaultProfile)
	if got.Decision != actionDecisionUnbound || got.Failures[0].Code != "typed_gate_binding_mismatch" {
		t.Fatalf("target whitespace was collapsed: %+v", got)
	}
	got = decideTypedVerifyActionWithPolicy([]state.Message{question}, question.Thread, "protected_branch_push", "two  spaces", "user", typedHumanTeam(), "s", project, team.DefaultProfile)
	if got.Decision != actionDecisionPending || got.Failures[0].Code != "gate_pending" {
		t.Fatalf("non-binding Note changed requested action matching: %+v", got)
	}
	question.Body += "\nAction: protected_branch_push"
	question = withAuthorityRaw(question)
	got = decideTypedVerifyActionWithPolicy([]state.Message{question}, question.Thread, "protected_branch_push", "two  spaces", "user", typedHumanTeam(), "s", project, team.DefaultProfile)
	if got.Decision != actionDecisionPending || got.Failures[0].Code != "typed_gate_body_malformed" {
		t.Fatalf("duplicate body binding was accepted: %+v", got)
	}
}

func TestSelfEligibleDefaultPushStillRequiresCurrentPolicyAndPreflight(t *testing.T) {
	project := t.TempDir()
	now := time.Now().UTC()
	question := typedQuestion(project, "s", "q", "default_branch_push", "PR #1 head abcdef0 into main", "", now)
	op := team.DefaultOperator()
	op.InteractionMode = team.OperatorInteractionSelfOperator
	op.SelfOperator = &team.SelfOperatorPolicy{LeadRole: "cto", PolicyRevision: 7, Sessions: map[string]team.SelfOperatorSessionPolicy{"s": {Enabled: true, AllowedGateKinds: []string{operatorauth.GateMerge}}}}
	cfg := team.Team{Project: project, Members: []team.Member{{Role: "cto", Handle: "cto", Session: "s"}, {Role: "qa", Handle: "qa", Session: "s"}}, Operator: &op}
	view := team.EffectiveSelfOperator(cfg, "s")
	approval := operatorauth.ApprovalContext{SchemaVersion: operatorauth.ApprovalSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion, Source: "self_operator", SelfApproved: true, GateKind: operatorauth.GateMerge, Action: "default_branch_push", Target: question.AuthorizationRequest.Target, QuestionMessageID: question.ID, AnsweredByRole: "cto", AnsweredByHandle: "cto", PolicyRevision: view.PolicyRevision - 1, PolicyHash: view.PolicyHash, VerifiedAt: now.Format(time.RFC3339Nano)}
	answer := state.Message{ID: "a", From: "cto", To: []string{"cto"}, Thread: question.Thread, Kind: state.KindAnswer, Subject: "APPROVED: test", Body: question.Body, Created: now.Add(time.Second), ApprovalPresent: true, ApprovalValid: true, Approval: &approval}
	answer = withAuthorityRaw(answer)
	got := decideTypedVerifyActionWithPolicy([]state.Message{question, answer}, question.Thread, "default_branch_push", question.AuthorizationRequest.Target, "user", cfg, "s", project, team.DefaultProfile)
	if got.Failures[0].Code != "self_policy_revoked" {
		t.Fatalf("stale policy authorized default push: %+v", got)
	}

	approval.PolicyRevision = view.PolicyRevision
	answer.Approval = &approval
	receipt := operatorauth.Receipt{SchemaVersion: operatorauth.ReceiptSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion, Gate: question.Thread, GateKind: approval.GateKind, Action: approval.Action, Target: approval.Target, Decision: "approved", ApprovalSource: approval.Source, SelfApproved: true, QuestionMessageID: question.ID, AnswerMessageID: answer.ID, AnsweredBy: approval.AnsweredByHandle, PolicyRevision: approval.PolicyRevision, PolicyHash: approval.PolicyHash}
	path := selfApprovalReceiptPath(project, team.DefaultProfile, "s", question.Thread, question.ID, answer.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(receipt)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	got = decideTypedVerifyActionWithPolicy([]state.Message{question, answer}, question.Thread, "default_branch_push", question.AuthorizationRequest.Target, "user", cfg, "s", project, team.DefaultProfile)
	if got.Failures[0].Code != "self_preflight_stale" {
		t.Fatalf("missing preflight authorized default push: %+v", got)
	}
}

func TestTypedVerifierSelfAuthorityForBothMergePushActions(t *testing.T) {
	old := resolveVerifiedOperatorActor
	resolveVerifiedOperatorActor = func(_, profile, session, role, handle string) (verifiedOperatorActor, error) {
		return verifiedOperatorActor{Profile: profile, Session: session, Role: role, Handle: handle}, nil
	}
	t.Cleanup(func() { resolveVerifiedOperatorActor = old })
	t.Setenv("AM_ME", "qa")
	for _, action := range []string{"default_branch_push", "protected_branch_push"} {
		t.Run(action, func(t *testing.T) {
			project, cfg, target, msgs := selfVerifyFixture(t)
			questionID, answerID := "q-"+action, "a-"+action
			msgs[0].ID, msgs[1].ID = questionID, answerID
			request := *msgs[0].AuthorizationRequest
			request.Action = action
			msgs[0].AuthorizationRequest = &request
			msgs[0].Body = strings.ReplaceAll(msgs[0].Body, "protected_branch_push", action)
			msgs[0].RawBody = msgs[0].Body
			approval := *msgs[1].Approval
			approval.Action = action
			approval.QuestionMessageID = questionID
			msgs[1].Approval = &approval
			msgs[1].Body = strings.ReplaceAll(msgs[1].Body, "protected_branch_push", action)
			msgs[1].RawBody = msgs[1].Body
			receipt := operatorauth.Receipt{SchemaVersion: operatorauth.ReceiptSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion, Gate: msgs[0].Thread, GateKind: approval.GateKind, Action: action, Target: target, Decision: "approved", ApprovalSource: "self_operator", SelfApproved: true, QuestionMessageID: msgs[0].ID, AnswerMessageID: msgs[1].ID, AnsweredBy: "cto", PolicyRevision: approval.PolicyRevision, PolicyHash: approval.PolicyHash, Preflight: operatorauth.PreflightReceipt{Kind: approval.PreflightKind, SHA256: approval.PreflightSHA256, Path: approval.PreflightPath, OK: true}}
			if err := writeSelfApprovalReceipt(project, team.DefaultProfile, "s", msgs[0].Thread, msgs[1].ID, receipt); err != nil {
				t.Fatal(err)
			}
			got := decideTypedVerifyActionWithPolicy(msgs, msgs[0].Thread, action, target, "user", cfg, "s", project, team.DefaultProfile)
			if got.Decision != actionDecisionApproved || got.ApprovalSource != "self_operator" || !got.SelfApproved {
				t.Fatalf("typed self authority for %s = %+v", action, got)
			}
		})
	}
}

func TestTypedVerifierSpawnRemainsHumanOnlyDespiteSelfAllowlist(t *testing.T) {
	project := t.TempDir()
	now := time.Now().UTC()
	question := typedQuestion(project, "s", "q", "spawn", "worker qa", "", now)
	op := team.DefaultOperator()
	op.InteractionMode = team.OperatorInteractionSelfOperator
	op.SelfOperator = &team.SelfOperatorPolicy{LeadRole: "cto", PolicyRevision: 1, Sessions: map[string]team.SelfOperatorSessionPolicy{"s": {Enabled: true, AllowedGateKinds: []string{operatorauth.GateSpawn}}}}
	cfg := team.Team{Project: project, Operator: &op, Orchestrated: true, Lead: "cto", Members: []team.Member{{Role: "cto", Handle: "cto", Session: "s"}, {Role: "qa", Handle: "qa", Session: "s"}}}
	view := team.EffectiveSelfOperator(cfg, "s")
	approval := operatorauth.ApprovalContext{SchemaVersion: operatorauth.ApprovalSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion, Source: "self_operator", SelfApproved: true, GateKind: operatorauth.GateSpawn, Action: "spawn", Target: "worker qa", QuestionMessageID: question.ID, AnsweredByRole: "cto", AnsweredByHandle: "cto", PolicyRevision: view.PolicyRevision, PolicyHash: view.PolicyHash, VerifiedAt: now.Add(time.Second).Format(time.RFC3339Nano)}
	answer := state.Message{ID: "a", From: "cto", To: []string{"cto"}, Thread: question.Thread, Kind: state.KindAnswer, Subject: "APPROVED: test", Body: question.Body, Created: now.Add(time.Second), ApprovalPresent: true, ApprovalValid: true, Approval: &approval}
	answer = withAuthorityRaw(answer)
	receipt := operatorauth.Receipt{SchemaVersion: operatorauth.ReceiptSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion, Gate: question.Thread, GateKind: operatorauth.GateSpawn, Action: "spawn", Target: "worker qa", Decision: "approved", ApprovalSource: "self_operator", SelfApproved: true, QuestionMessageID: question.ID, AnswerMessageID: answer.ID, AnsweredBy: "cto", PolicyRevision: view.PolicyRevision, PolicyHash: view.PolicyHash}
	if err := writeSelfApprovalReceipt(project, team.DefaultProfile, "s", question.Thread, answer.ID, receipt); err != nil {
		t.Fatal(err)
	}
	got := decideTypedVerifyActionWithPolicy([]state.Message{question, answer}, question.Thread, "spawn", "worker qa", "user", cfg, "s", project, team.DefaultProfile)
	if got.Decision == actionDecisionApproved || len(got.Failures) == 0 || got.Failures[0].Code != "gate_pending" {
		t.Fatalf("typed spawn self-authorized despite human-only catalog: %+v", got)
	}
}

func TestCatalogListingAndVerifierUseSameCapabilities(t *testing.T) {
	want := operatorauth.ActionCapabilities()
	data := actionKindsData{TaxonomyVersion: operatorauth.ActionTaxonomyVersion, Actions: operatorauth.ActionCapabilities()}
	if data.TaxonomyVersion != operatorauth.ActionTaxonomyVersion || !reflect.DeepEqual(data.Actions, want) {
		t.Fatalf("listed actions diverged from catalog: %#v", data)
	}
	for _, capability := range want {
		got, err := normalizeHighRiskAction(capability.Action)
		if err != nil || got != capability.Action {
			t.Errorf("verifier catalog lookup %q = %q, %v", capability.Action, got, err)
		}
	}
	if _, err := normalizeHighRiskAction("release"); err == nil {
		t.Fatal("removed release alias was accepted")
	}
}

func TestAuthorizationArtifactPathsAreCollisionResistantAndImmutable(t *testing.T) {
	project := t.TempDir()
	if a, b := selfApprovalReservationPath(project, "default", "s", "gate/a/b", "q"), selfApprovalReservationPath(project, "default", "s", "gate/a_b", "q"); a == b {
		t.Fatalf("colliding gate reservation paths: %s", a)
	}
	if a, b := selfApprovalReceiptPath(project, "default", "s", "gate/a/b", "q", "a/1"), selfApprovalReceiptPath(project, "default", "s", "gate/a/b", "q", "a_1"); a == b {
		t.Fatalf("colliding answer receipt paths: %s", a)
	}
	if a, b := selfApprovalEvidencePath(project, "default", "s", "gate/a/b", "q", "sha256:a/b"), selfApprovalEvidencePath(project, "default", "s", "gate/a_b", "q", "sha256:a_b"); a == b {
		t.Fatalf("colliding evidence paths: %s", a)
	}
	receipt := operatorauth.Receipt{SchemaVersion: operatorauth.ReceiptSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion, Gate: "gate/a/b", GateKind: "tag", Action: "tag", Target: "v1", Note: "safe prose", Decision: "approved", ApprovalSource: "human", QuestionMessageID: "q", AnswerMessageID: "a", AnsweredBy: "user"}
	if err := writeSelfApprovalReceipt(project, "default", "s", receipt.Gate, receipt.AnswerMessageID, receipt); err != nil {
		t.Fatal(err)
	}
	if err := writeSelfApprovalReceipt(project, "default", "s", receipt.Gate, receipt.AnswerMessageID, receipt); err != nil {
		t.Fatalf("idempotent retry failed: %v", err)
	}
	path := selfApprovalReceiptPath(project, "default", "s", receipt.Gate, receipt.QuestionMessageID, receipt.AnswerMessageID)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tampered := receipt
	tampered.Note = "different"
	if err := writeSelfApprovalReceipt(project, "default", "s", receipt.Gate, receipt.AnswerMessageID, tampered); err == nil {
		t.Fatal("immutable receipt was overwritten")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("receipt content changed after collision")
	}
}

func TestLegacyLossyReceiptFilenameIsNeverAuthoritative(t *testing.T) {
	project := t.TempDir()
	now := time.Now().UTC()
	approval := operatorauth.ApprovalContext{SchemaVersion: operatorauth.ApprovalSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion, Source: "human", GateKind: "tag", Action: "tag", Target: "v1", QuestionMessageID: "q", AnsweredByRole: "operator", AnsweredByHandle: "user", VerifiedAt: now.Format(time.RFC3339Nano)}
	answer := state.Message{ID: "a", From: "user", To: []string{"cto"}, Thread: "gate/a/b", Kind: state.KindAnswer, Subject: "APPROVED: a/b", Body: "Gate-Kind: tag\nAction: tag\nTarget: v1", ApprovalPresent: true, ApprovalValid: true, Approval: &approval}
	legacy := filepath.Join(selfApprovalStoreDir(project, "default", "s"), "gate_a_b-a.receipt.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	receipt := operatorauth.Receipt{SchemaVersion: operatorauth.ReceiptSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion, Gate: answer.Thread, GateKind: approval.GateKind, Action: approval.Action, Target: approval.Target, Decision: "approved", ApprovalSource: "human", QuestionMessageID: "q", AnswerMessageID: "a", AnsweredBy: "user"}
	b, _ := json.Marshal(receipt)
	if err := os.WriteFile(legacy, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateApprovalReceipt(project, "default", "s", answer.Thread, answer, approval); err == nil {
		t.Fatal("legacy lossy filename supplied authority")
	}
}

func TestDedupeSecurityMessagesComparesEveryAuthorityField(t *testing.T) {
	now := time.Now().UTC()
	base := typedQuestion("/repo", "s", "same", "tag", "v1", "note", now)
	base.RawCreated = now.Format(time.RFC3339Nano)
	base.Priority, base.ReplyTo, base.Labels = "high", "parent", []string{"one", "two"}
	base.Context = map[string]any{"authorization_request": map[string]any{"x": "y"}}
	mutations := map[string]func(*state.Message){
		"from":          func(m *state.Message) { m.From = "other" },
		"to order":      func(m *state.Message) { m.To = []string{"other", "user"} },
		"thread":        func(m *state.Message) { m.Thread = "gate/other" },
		"raw thread":    func(m *state.Message) { m.RawThread = "gate/alias" },
		"subject":       func(m *state.Message) { m.Subject = "other" },
		"raw created":   func(m *state.Message) { m.RawCreated += " " },
		"created":       func(m *state.Message) { m.Created = m.Created.Add(time.Nanosecond) },
		"priority":      func(m *state.Message) { m.Priority = "normal" },
		"kind":          func(m *state.Message) { m.Kind = state.KindDecision },
		"reply to":      func(m *state.Message) { m.ReplyTo = "other" },
		"labels":        func(m *state.Message) { m.Labels = []string{"two", "one"} },
		"orchestrator":  func(m *state.Message) { m.Orchestrator = "other" },
		"from project":  func(m *state.Message) { m.FromProject = "other" },
		"reply project": func(m *state.Message) { m.ReplyProject = "other" },
		"event":         func(m *state.Message) { m.OrchestratorEvent = "other" },
		"external task": func(m *state.Message) { m.ExternalTaskID = "other" },
		"body":          func(m *state.Message) { m.Body += "\nother" },
		"schema":        func(m *state.Message) { m.SchemaOK = !m.SchemaOK },
		"context":       func(m *state.Message) { m.Context = map[string]any{"authorization_request": map[string]any{"x": "z"}} },
		"typed request": func(m *state.Message) {
			copy := *m.AuthorizationRequest
			copy.Note = "other"
			m.AuthorizationRequest = &copy
		},
		"approval": func(m *state.Message) {
			m.ApprovalPresent, m.ApprovalValid = true, true
			m.Approval = &operatorauth.ApprovalContext{SchemaVersion: 2, Target: "different"}
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			other := base
			mutate(&other)
			if _, conflict := dedupeSecurityMessages([]state.Message{base, other}); !conflict {
				t.Fatal("authority-significant duplicate difference was ignored")
			}
		})
	}
	copy := base
	copy.Path, copy.Owner, copy.State = "other", "other", "cur"
	if out, conflict := dedupeSecurityMessages([]state.Message{base, copy}); conflict || len(out) != 1 {
		t.Fatalf("allowed storage-location differences conflicted: conflict=%t out=%d", conflict, len(out))
	}
}

func TestTypedDecisionComesOnlyFromExactSubject(t *testing.T) {
	for _, tc := range []struct{ name, subject, bodyDecision, receiptDecision, want string }{
		{"approved/body denied", "APPROVED: test", "DENIED: test", "approved", actionDecisionApproved},
		{"denied/body approved", "DENIED: test", "APPROVED: test", "denied", actionDecisionDenied},
		{"wrong suffix", "APPROVED: other", "APPROVED: test", "approved", actionDecisionPending},
		{"status", "STATUS: test", "APPROVED: test", "approved", actionDecisionPending},
		{"empty", "", "APPROVED: test", "approved", actionDecisionPending},
		{"lowercase", "approved: test", "APPROVED: test", "approved", actionDecisionPending},
		{"multiline", "APPROVED: test\nDENIED: test", "APPROVED: test", "approved", actionDecisionPending},
		{"ambiguous", "APPROVED: test DENIED: test", "APPROVED: test", "approved", actionDecisionPending},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := t.TempDir()
			now := time.Now().UTC()
			question := typedQuestion(project, "s", "q", "github_release", "release target", "note", now)
			approval := operatorauth.ApprovalContext{SchemaVersion: operatorauth.ApprovalSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion, Source: "human", GateKind: "release", Action: "github_release", Target: "release target", Note: "note", QuestionMessageID: "q", AnsweredByRole: "operator", AnsweredByHandle: "user", VerifiedAt: now.Add(time.Second).Format(time.RFC3339Nano)}
			answer := state.Message{ID: "a", From: "user", To: []string{"cto"}, Thread: question.Thread, Kind: state.KindAnswer, Subject: tc.subject, Body: question.Body + "\n" + tc.bodyDecision, Created: now.Add(time.Second), ApprovalPresent: true, ApprovalValid: true, Approval: &approval}
			answer = withAuthorityRaw(answer)
			receipt := operatorauth.Receipt{SchemaVersion: operatorauth.ReceiptSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion, Gate: question.Thread, GateKind: approval.GateKind, Action: approval.Action, Target: approval.Target, Note: approval.Note, Decision: tc.receiptDecision, ApprovalSource: "human", QuestionMessageID: "q", AnswerMessageID: "a", AnsweredBy: "user"}
			if err := writeSelfApprovalReceipt(project, "default", "s", question.Thread, answer.ID, receipt); err != nil {
				t.Fatal(err)
			}
			got := decideTypedVerifyActionWithPolicy([]state.Message{question, answer}, question.Thread, approval.Action, approval.Target, "user", typedHumanTeam(), "s", project, "default")
			if got.Decision != tc.want {
				t.Fatalf("decision=%s want=%s result=%+v", got.Decision, tc.want, got)
			}
		})
	}
}

func TestTypedRawEnvelopeWhitespaceCannotAuthorize(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		mutate     func(string, bool) string
	}{
		{name: "leading subject space", want: "answer_not_decision", mutate: func(s string, answer bool) string {
			if answer {
				return strings.Replace(s, `"subject":"APPROVED: test"`, `"subject":" APPROVED: test"`, 1)
			}
			return s
		}},
		{name: "trailing subject space", want: "answer_not_decision", mutate: func(s string, answer bool) string {
			if answer {
				return strings.Replace(s, `"subject":"APPROVED: test"`, `"subject":"APPROVED: test "`, 1)
			}
			return s
		}},
		{name: "question leading body space", want: "typed_gate_body_malformed", mutate: func(s string, answer bool) string {
			if !answer {
				return strings.Replace(s, "---\nGate-Kind:", "---\n Gate-Kind:", 1)
			}
			return s
		}},
		{name: "answer trailing body space", want: "answer_body_malformed", mutate: func(s string, answer bool) string {
			if answer {
				return strings.Replace(s, "\nTarget: exact\n", "\nTarget: exact \n", 1)
			}
			return s
		}},
		{name: "answer extra blank line", want: "answer_body_malformed", mutate: func(s string, answer bool) string {
			if answer {
				return s + "\n"
			}
			return s
		}},
		{name: "canonical crlf", want: "approved", mutate: func(s string, _ bool) string { return strings.ReplaceAll(s, "\n", "\r\n") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project, base, _ := seedNotifyProject(t, team.DefaultOperator())
			seedNotifyLaunch(t, project, base, "s", "cto")
			now := time.Now().UTC()
			question := typedQuestion(project, "s", "q", "tag", "exact", "", now)
			writeAuthorizationMessage(t, base, "s", "user", "new", question)
			approval := operatorauth.ApprovalContext{SchemaVersion: operatorauth.ApprovalSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion, Source: "human", GateKind: "tag", Action: "tag", Target: "exact", QuestionMessageID: "q", AnsweredByRole: "operator", AnsweredByHandle: "user", VerifiedAt: now.Add(time.Second).Format(time.RFC3339Nano)}
			answer := state.Message{ID: "a", From: "user", To: []string{"cto"}, Thread: question.Thread, RawThread: question.Thread, Kind: state.KindAnswer, Subject: "APPROVED: test", Body: question.Body, Created: now.Add(time.Second), ApprovalPresent: true, ApprovalValid: true, Approval: &approval}
			writeAuthorizationMessage(t, base, "s", "cto", "cur", answer)
			receipt := operatorauth.Receipt{SchemaVersion: operatorauth.ReceiptSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion, Gate: question.Thread, GateKind: "tag", Action: "tag", Target: "exact", Decision: "approved", ApprovalSource: "human", QuestionMessageID: "q", AnswerMessageID: "a", AnsweredBy: "user"}
			if err := writeSelfApprovalReceipt(project, "default", "s", question.Thread, "a", receipt); err != nil {
				t.Fatal(err)
			}
			for path, isAnswer := range map[string]bool{
				filepath.Join(base, "s", "agents", "user", "inbox", "new", "q.md"): false,
				filepath.Join(base, "s", "agents", "cto", "inbox", "cur", "a.md"):  true,
			} {
				b, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(tc.mutate(string(b), isAnswer)), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			snap, err := state.Build(project, base, state.DefaultProbe)
			if err != nil {
				t.Fatal(err)
			}
			sess, ok := findThreadsSession(snap.Sessions, team.DefaultProfile, "s")
			if !ok {
				t.Fatal("session missing")
			}
			msgs, warnings := state.ScanSessionMessages(sess.Root, time.Now)
			if len(warnings) != 0 {
				t.Fatalf("warnings=%v", warnings)
			}
			cfg, err := team.Read(project)
			if err != nil {
				t.Fatal(err)
			}
			got := decideTypedVerifyActionWithPolicy(msgs, question.Thread, "tag", "exact", "user", cfg, "s", project, "default")
			if tc.want == "approved" {
				if got.Decision != actionDecisionApproved {
					t.Fatalf("canonical CRLF failed: %+v", got)
				}
			} else if got.Decision == actionDecisionApproved || len(got.Failures) == 0 || got.Failures[0].Code != tc.want {
				t.Fatalf("raw whitespace authorized or wrong barrier: %+v", got)
			}
		})
	}
}

func TestSharedTypedQuestionSelectorAndRoutingBarriers(t *testing.T) {
	project := t.TempDir()
	now := time.Now().UTC()
	cfg := typedHumanTeam()
	valid := typedQuestion(project, "s", "q1", "tag", "v1", "", now)
	wrongKind := valid
	wrongKind.ID, wrongKind.Kind, wrongKind.Created = "event", state.KindDecision, now.Add(time.Second)
	if got := latestGateQuestionCandidate([]state.Message{valid, wrongKind}, valid.Thread); got == nil || got.ID != valid.ID {
		t.Fatalf("typed-looking non-question displaced question: %+v", got)
	}
	verified := decideTypedVerifyActionWithPolicy([]state.Message{valid, wrongKind}, valid.Thread, "tag", "v1", "user", cfg, "s", project, "default")
	if verified.Decision != actionDecisionPending || verified.MessageID != valid.ID || verified.Failures[0].Code != "gate_pending" {
		t.Fatalf("verifier selector diverged for non-question: %+v", verified)
	}

	for _, tc := range []struct {
		name   string
		mutate func(*state.Message)
	}{
		{name: "wrong to", mutate: func(q *state.Message) { q.To = []string{"other"} }},
		{name: "multi to", mutate: func(q *state.Message) { q.To = []string{"user", "other"} }},
		{name: "foreign requester", mutate: func(q *state.Message) { q.From = "intruder" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := valid
			bad.ID, bad.Created = "q2", now.Add(2*time.Second)
			tc.mutate(&bad)
			got := decideTypedVerifyActionWithPolicy([]state.Message{valid, bad}, valid.Thread, "tag", "v1", "user", cfg, "s", project, "default")
			if got.Decision != actionDecisionPending || got.MessageID != bad.ID || got.Failures[0].Code != "typed_gate_routing_invalid" {
				t.Fatalf("latest invalid route did not block older valid question: %+v", got)
			}
		})
	}
	reRaised := valid
	reRaised.ID, reRaised.Created = "q3", now.Add(3*time.Second)
	got := decideTypedVerifyActionWithPolicy([]state.Message{valid, reRaised}, valid.Thread, "tag", "v1", "user", cfg, "s", project, "default")
	if got.Decision != actionDecisionPending || got.MessageID != reRaised.ID || got.Failures[0].Code != "gate_pending" {
		t.Fatalf("valid re-raise was not latest shared candidate: %+v", got)
	}
}

func TestVerifyEligibilityJSONIsDiagnosticOnlyInStageA(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result verifyActionResult
		reason string
		typed  bool
	}{
		{name: "typed pass", result: verifyActionResult{Decision: actionDecisionApproved, ApprovalSource: "human"}, reason: "signer_unconfigured", typed: true},
		{name: "legacy diagnostic", result: verifyActionResult{Decision: actionDecisionUnbound, Failures: []verifyMergeFailure{{Code: "legacy_gate_diagnostic_only"}}}, reason: "legacy_gate_diagnostic_only"},
		{name: "malformed typed", result: verifyActionResult{Decision: actionDecisionPending, Failures: []verifyMergeFailure{{Code: "typed_gate_malformed"}}}, reason: "typed_gate_malformed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			annotateVerifyActionEligibility(&tc.result)
			var out bytes.Buffer
			if err := writeJSONEnvelope(&out, "verify_action", tc.result); err != nil {
				t.Fatal(err)
			}
			env := decodeJSONEnvelope[verifyActionResult](t, out.String())
			if env.Data.EnvelopeEligible || env.Data.EnvelopeEligibilityReason != tc.reason || env.Data.TypedAuthority != tc.typed || env.Data.TypedEligible != tc.typed {
				t.Fatalf("Stage A eligibility JSON = %+v", env.Data)
			}
		})
	}
}
