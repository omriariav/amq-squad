package cli

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func selfVerifyFixture(t *testing.T) (string, team.Team, string, []state.Message) {
	t.Helper()
	project := t.TempDir()
	op := team.DefaultOperator()
	op.InteractionMode = team.OperatorInteractionSelfOperator
	op.SelfOperator = &team.SelfOperatorPolicy{LeadRole: "cto", PolicyRevision: 3, Sessions: map[string]team.SelfOperatorSessionPolicy{"s": {Enabled: true, AllowedGateKinds: []string{"merge"}}}}
	cfg := team.Team{Project: project, Operator: &op, Orchestrated: true, Lead: "cto", Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: "s"}, {Role: "qa", Handle: "qa", Binary: "codex", Session: "s"}}}
	view := team.EffectiveSelfOperator(cfg, "s")
	target := "PR #398 head abc1234 into main"
	evidence := []byte(`{"subject":"PR #398","head_sha":"abc1234","base":"main","ci":{"state":"success","sha":"abc1234","source":"ci","checked_at":"2026-07-11T00:00:00Z"},"review":{"state":"clean","sha":"abc1234","source":"review","checked_at":"2026-07-11T00:00:00Z"},"exceptions":[]}`)
	sum := sha256.Sum256(evidence)
	digest := fmt.Sprintf("sha256:%x", sum)
	evidencePath := selfApprovalEvidencePath(project, "default", "s", "gate/merge-398", "q1", digest)
	if err := atomicWriteJSONBytes(evidencePath, evidence); err != nil {
		t.Fatal(err)
	}
	request := operatorauth.GateRequestContext{SchemaVersion: operatorauth.GateRequestSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion, Gate: "gate/merge-398", Thread: "gate/merge-398", Namespace: operatorauth.NamespaceBinding{ProjectDir: project, Profile: "default", Session: "s", NamespaceID: "default/s", Generation: "none"}, GateKind: "merge", Action: "protected_branch_push", Target: target}
	approval := operatorauth.ApprovalContext{SchemaVersion: operatorauth.ApprovalSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion, Source: "self_operator", SelfApproved: true, GateKind: "merge", Action: "protected_branch_push", Target: target, QuestionMessageID: "q1", AnsweredByRole: "cto", AnsweredByHandle: "cto", PolicyRevision: view.PolicyRevision, PolicyHash: view.PolicyHash, PreflightKind: "verify_merge", PreflightSHA256: digest, PreflightPath: evidencePath, VerifiedAt: "2026-07-11T00:01:00Z"}
	receipt := operatorauth.Receipt{SchemaVersion: operatorauth.ReceiptSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion, Gate: "gate/merge-398", GateKind: "merge", Action: "protected_branch_push", Target: target, Decision: "approved", ApprovalSource: "self_operator", SelfApproved: true, QuestionMessageID: "q1", AnswerMessageID: "a1", AnsweredBy: "cto", PolicyRevision: view.PolicyRevision, PolicyHash: view.PolicyHash, Preflight: operatorauth.PreflightReceipt{Kind: "verify_merge", SHA256: approval.PreflightSHA256, Path: evidencePath, OK: true}}
	if err := writeSelfApprovalReceipt(project, "default", "s", "gate/merge-398", "a1", receipt); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	msgs := []state.Message{
		{ID: "q1", From: "cto", To: []string{"user"}, Thread: "gate/merge-398", RawThread: "gate/merge-398", Kind: state.KindQuestion, Body: "Gate-Kind: merge\nAction: protected_branch_push\nTarget: " + target, Created: now, AuthorizationRequestPresent: true, AuthorizationRequestValid: true, AuthorizationRequest: &request},
		{ID: "a1", From: "cto", To: []string{"cto"}, Thread: "gate/merge-398", Kind: state.KindAnswer, Subject: "APPROVED: merge-398", Body: "Gate-Kind: merge\nAction: protected_branch_push\nTarget: " + target, Created: now.Add(time.Second), ApprovalPresent: true, ApprovalValid: true, Approval: &approval},
	}
	msgs[0], msgs[1] = withAuthorityRaw(msgs[0]), withAuthorityRaw(msgs[1])
	return project, cfg, target, msgs
}

func TestVerifyActionSelfApprovalRequiresDistinctVerifiedMergeActor(t *testing.T) {
	project, cfg, target, msgs := selfVerifyFixture(t)
	old := resolveVerifiedOperatorActor
	t.Cleanup(func() { resolveVerifiedOperatorActor = old })
	t.Setenv("AM_ME", "qa")
	resolveVerifiedOperatorActor = func(_, profile, session, role, handle string) (verifiedOperatorActor, error) {
		return verifiedOperatorActor{Role: role, Handle: handle, Profile: profile, Session: session}, nil
	}
	got := decideTypedVerifyActionWithPolicy(msgs, "gate/merge-398", "protected_branch_push", target, "user", cfg, "s", project, "default")
	if got.Decision != actionDecisionApproved || !got.SelfApproved || got.ApprovalSource != "self_operator" {
		t.Fatalf("self approval = %+v", got)
	}
	t.Setenv("AM_ME", "cto")
	got = decideTypedVerifyActionWithPolicy(msgs, "gate/merge-398", "protected_branch_push", target, "user", cfg, "s", project, "default")
	if !failureHasCode(got.Failures, "self_merge_actor_conflict") {
		t.Fatalf("lead executed self merge: %+v", got)
	}
}

func TestTypedSelfAnswerRequiresExactRoutingSubjectBodyAndNote(t *testing.T) {
	old := resolveVerifiedOperatorActor
	resolveVerifiedOperatorActor = func(_, profile, session, role, handle string) (verifiedOperatorActor, error) {
		return verifiedOperatorActor{Role: role, Handle: handle, Profile: profile, Session: session}, nil
	}
	t.Cleanup(func() { resolveVerifiedOperatorActor = old })
	t.Setenv("AM_ME", "qa")
	for _, tc := range []struct {
		name   string
		mutate func(*state.Message)
	}{
		{name: "wrong recipient", mutate: func(a *state.Message) { a.To = []string{"qa"} }},
		{name: "subject alias", mutate: func(a *state.Message) { a.Subject = "APPROVED: merge" }},
		{name: "missing binding", mutate: func(a *state.Message) { a.Body = "Evidence only" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project, cfg, target, msgs := selfVerifyFixture(t)
			tc.mutate(&msgs[1])
			msgs[1] = withAuthorityRaw(msgs[1])
			got := decideTypedVerifyActionWithPolicy(msgs, msgs[0].Thread, "protected_branch_push", target, "user", cfg, "s", project, "default")
			if got.Decision == actionDecisionApproved || !failureHasCode(got.Failures, "self_answer_envelope_invalid") {
				t.Fatalf("malformed self answer authorized: %+v", got)
			}
		})
	}
	project, cfg, target, msgs := selfVerifyFixture(t)
	request := *msgs[0].AuthorizationRequest
	request.Note = "safe prose"
	msgs[0].AuthorizationRequest = &request
	msgs[0].Body += "\nNote: safe prose"
	msgs[0] = withAuthorityRaw(msgs[0])
	approval := *msgs[1].Approval
	approval.Note = request.Note
	msgs[1].Approval = &approval
	msgs[1].Body += "\nNote: safe prose"
	msgs[1] = withAuthorityRaw(msgs[1])
	receipt := operatorauth.Receipt{SchemaVersion: operatorauth.ReceiptSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion, Gate: msgs[0].Thread, GateKind: approval.GateKind, Action: approval.Action, Target: approval.Target, Note: approval.Note, Decision: "approved", ApprovalSource: "self_operator", SelfApproved: true, QuestionMessageID: msgs[0].ID, AnswerMessageID: msgs[1].ID, AnsweredBy: "cto", PolicyRevision: approval.PolicyRevision, PolicyHash: approval.PolicyHash, Preflight: operatorauth.PreflightReceipt{Kind: approval.PreflightKind, SHA256: approval.PreflightSHA256, Path: approval.PreflightPath, OK: true}}
	path := selfApprovalReceiptPath(project, "default", "s", msgs[0].Thread, msgs[0].ID, msgs[1].ID)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := writeSelfApprovalReceipt(project, "default", "s", msgs[0].Thread, msgs[1].ID, receipt); err != nil {
		t.Fatal(err)
	}
	if got := decideTypedVerifyActionWithPolicy(msgs, msgs[0].Thread, "protected_branch_push", target, "user", cfg, "s", project, "default"); got.Decision != actionDecisionApproved {
		t.Fatalf("safe Note self answer failed round trip: %+v", got)
	}
}

func TestVerifyActionSelfApprovalRevocationAndHumanPrecedence(t *testing.T) {
	project, cfg, target, msgs := selfVerifyFixture(t)
	old := resolveVerifiedOperatorActor
	t.Cleanup(func() { resolveVerifiedOperatorActor = old })
	t.Setenv("AM_ME", "qa")
	resolveVerifiedOperatorActor = func(_, profile, session, role, handle string) (verifiedOperatorActor, error) {
		return verifiedOperatorActor{Role: role, Handle: handle}, nil
	}
	entry := cfg.Operator.SelfOperator.Sessions["s"]
	entry.Paused = true
	cfg.Operator.SelfOperator.Sessions["s"] = entry
	got := decideTypedVerifyActionWithPolicy(msgs, "gate/merge-398", "protected_branch_push", target, "user", cfg, "s", project, "default")
	if got.Decision == actionDecisionApproved {
		t.Fatalf("paused policy approved: %+v", got)
	}
	cfg.Operator.SelfOperator.Sessions["s"] = team.SelfOperatorSessionPolicy{Enabled: true, AllowedGateKinds: []string{"merge"}}
	deniedAt := msgs[1].Created.Add(time.Second)
	human := operatorauth.ApprovalContext{SchemaVersion: operatorauth.ApprovalSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion, Source: "human", GateKind: "merge", Action: "protected_branch_push", Target: target, QuestionMessageID: "q1", AnsweredByRole: "operator", AnsweredByHandle: "user", VerifiedAt: deniedAt.UTC().Format(time.RFC3339Nano)}
	denial := state.Message{ID: "h1", From: "user", To: []string{"cto"}, Thread: "gate/merge-398", Kind: state.KindAnswer, Subject: "DENIED: merge-398", Body: msgs[0].Body, Created: deniedAt, ApprovalPresent: true, ApprovalValid: true, Approval: &human}
	denial = withAuthorityRaw(denial)
	receipt := operatorauth.Receipt{SchemaVersion: operatorauth.ReceiptSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion, Gate: denial.Thread, GateKind: human.GateKind, Action: human.Action, Target: human.Target, Decision: "denied", ApprovalSource: "human", QuestionMessageID: "q1", AnswerMessageID: denial.ID, AnsweredBy: "user"}
	if err := writeSelfApprovalReceipt(project, "default", "s", denial.Thread, denial.ID, receipt); err != nil {
		t.Fatal(err)
	}
	msgs = append(msgs, denial)
	got = decideTypedVerifyActionWithPolicy(msgs, "gate/merge-398", "protected_branch_push", target, "user", cfg, "s", project, "default")
	if got.Decision != actionDecisionDenied || got.ApprovalSource != "human" {
		t.Fatalf("human denial did not win: %+v", got)
	}
}

func failureHasCode(failures []verifyMergeFailure, code string) bool {
	for _, failure := range failures {
		if failure.Code == code {
			return true
		}
	}
	return false
}

func TestSelfEvidencePathStaysInsideProject(t *testing.T) {
	project, _, _, _ := selfVerifyFixture(t)
	outside := filepath.Join(t.TempDir(), "evidence.json")
	if err := os.WriteFile(outside, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := revalidateSelfApprovalEvidence(project, operatorauth.ApprovalContext{PreflightPath: outside}, "target")
	if err == nil || !strings.Contains(err.Error(), "outside project") {
		t.Fatalf("outside evidence = %v", err)
	}
}

func TestSelfApprovalReceiptRejectsTrailingUnknownAndBindingTamper(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{"trailing", func(b []byte) []byte { return append(b, []byte("\n{}")...) }},
		{"unknown", func(b []byte) []byte {
			var v map[string]any
			_ = json.Unmarshal(b, &v)
			v["unknown"] = true
			out, _ := json.Marshal(v)
			return out
		}},
		{"binding", func(b []byte) []byte {
			var v map[string]any
			_ = json.Unmarshal(b, &v)
			v["target"] = "PR #398 head deadbee into main"
			out, _ := json.Marshal(v)
			return out
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project, _, _, msgs := selfVerifyFixture(t)
			path := selfApprovalReceiptPath(project, "default", "s", "gate/merge-398", "q1", "a1")
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, tc.mutate(b), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := validateApprovalReceipt(project, "default", "s", "gate/merge-398", msgs[1], *msgs[1].Approval); err == nil {
				t.Fatal("tampered receipt accepted")
			}
		})
	}
}

func TestSelfApprovalEvidenceRejectsStrictAndStaleBindings(t *testing.T) {
	base := `{"subject":"PR #398","head_sha":"abc1234","base":"main","ci":{"state":"success","sha":"abc1234","source":"ci","checked_at":"2026-07-11T00:00:00Z"},"review":{"state":"clean","sha":"abc1234","source":"review","checked_at":"2026-07-11T00:00:00Z"},"exceptions":[]}`
	for _, tc := range []struct{ name, evidence, target string }{
		{"unknown", strings.TrimSuffix(base, "}") + `,"unknown":true}`, "PR #398 head abc1234 into main"},
		{"trailing", base + ` {}`, "PR #398 head abc1234 into main"},
		{"stale pr", base, "PR #399 head abc1234 into main"},
		{"stale head", base, "PR #398 head deadbee into main"},
		{"stale base", base, "PR #398 head abc1234 into release"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := t.TempDir()
			path := filepath.Join(project, team.DirName, "evidence", "default", "s", "self-operator", "e.json")
			if err := atomicWriteJSONBytes(path, []byte(tc.evidence)); err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256([]byte(tc.evidence))
			a := operatorauth.ApprovalContext{PreflightPath: path, PreflightSHA256: fmt.Sprintf("sha256:%x", sum)}
			if err := revalidateSelfApprovalEvidence(project, a, tc.target); err == nil {
				t.Fatal("tampered/stale evidence accepted")
			}
		})
	}
}

func TestVerifyActionLatestQuestionAndMalformedHumanAreBarriers(t *testing.T) {
	project, cfg, target, msgs := selfVerifyFixture(t)
	now := msgs[1].Created.Add(time.Second)
	newRequest := *msgs[0].AuthorizationRequest
	newRequest.Target = "PR #398 head deadbee into main"
	msgs = append(msgs, withAuthorityRaw(state.Message{ID: "q2", From: "cto", To: []string{"user"}, Thread: "gate/merge-398", Kind: state.KindQuestion, Body: "Gate-Kind: merge\nAction: protected_branch_push\nTarget: " + newRequest.Target, Created: now, AuthorizationRequestPresent: true, AuthorizationRequestValid: true, AuthorizationRequest: &newRequest}))
	got := decideTypedVerifyActionWithPolicy(msgs, "gate/merge-398", "protected_branch_push", target, "user", cfg, "s", project, "default")
	if !failureHasCode(got.Failures, "typed_gate_binding_mismatch") {
		t.Fatalf("newer mismatched question did not block: %+v", got)
	}
	msgs = msgs[:2]
	msgs = append(msgs, withAuthorityRaw(state.Message{ID: "h1", From: "user", To: []string{"cto"}, Thread: "gate/merge-398", Kind: state.KindAnswer, Subject: "APPROVED: merge-398", Body: msgs[0].Body, Created: now, ApprovalPresent: true, ApprovalValid: false, ApprovalError: "unknown field"}))
	got = decideTypedVerifyActionWithPolicy(msgs, "gate/merge-398", "protected_branch_push", target, "user", cfg, "s", project, "default")
	if got.Decision == actionDecisionApproved || !failureHasCode(got.Failures, "human_approval_context_missing_or_malformed") {
		t.Fatalf("malformed typed human context did not create barrier: %+v", got)
	}
}
