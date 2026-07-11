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
	cfg := team.Team{Operator: &op, Orchestrated: true, Lead: "cto", Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex"}, {Role: "qa", Handle: "qa", Binary: "codex"}}}
	view := team.EffectiveSelfOperator(cfg, "s")
	target := "PR #398 head abc1234 into main"
	evidence := []byte(`{"subject":"PR #398","head_sha":"abc1234","base":"main","ci":{"state":"success","sha":"abc1234","source":"ci","checked_at":"2026-07-11T00:00:00Z"},"review":{"state":"clean","sha":"abc1234","source":"review","checked_at":"2026-07-11T00:00:00Z"},"exceptions":[]}`)
	sum := sha256.Sum256(evidence)
	digest := fmt.Sprintf("sha256:%x", sum)
	evidencePath := selfApprovalEvidencePath(project, "default", "s", "gate/merge-398", "q1", digest)
	if err := atomicWriteJSONBytes(evidencePath, evidence); err != nil {
		t.Fatal(err)
	}
	approval := operatorauth.ApprovalContext{SchemaVersion: 1, Source: "self_operator", SelfApproved: true, GateKind: "merge", Action: "protected_branch_push", Target: target, QuestionMessageID: "q1", AnsweredByRole: "cto", AnsweredByHandle: "cto", PolicyRevision: view.PolicyRevision, PolicyHash: view.PolicyHash, PreflightKind: "verify_merge", PreflightSHA256: digest, PreflightPath: evidencePath, VerifiedAt: "2026-07-11T00:01:00Z"}
	receipt := operatorauth.Receipt{Gate: "gate/merge-398", GateKind: "merge", Action: "protected_branch_push", Target: target, Decision: "approved", ApprovalSource: "self_operator", SelfApproved: true, QuestionMessageID: "q1", AnswerMessageID: "a1", AnsweredBy: "cto", PolicyRevision: view.PolicyRevision, PolicyHash: view.PolicyHash, Preflight: operatorauth.PreflightReceipt{Kind: "verify_merge", SHA256: approval.PreflightSHA256, Path: evidencePath, OK: true}}
	if err := writeSelfApprovalReceipt(project, "default", "s", "gate/merge-398", "a1", receipt); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	msgs := []state.Message{
		{ID: "q1", From: "cto", Thread: "gate/merge-398", Kind: state.KindQuestion, Body: "Gate-Kind: merge\nAction: protected_branch_push\nTarget: " + target, Created: now},
		{ID: "a1", From: "cto", Thread: "gate/merge-398", Kind: state.KindAnswer, Subject: "APPROVED: merge-398", Body: "Gate-Kind: merge\nAction: protected_branch_push\nTarget: " + target, Created: now.Add(time.Second), ApprovalPresent: true, ApprovalValid: true, Approval: &approval},
	}
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
	got := decideVerifyActionWithPolicy(msgs, "gate/merge-398", "protected_branch_push", target, "user", cfg, "s", project, "default")
	if got.Decision != actionDecisionApproved || !got.SelfApproved || got.ApprovalSource != "self_operator" {
		t.Fatalf("self approval = %+v", got)
	}
	t.Setenv("AM_ME", "cto")
	got = decideVerifyActionWithPolicy(msgs, "gate/merge-398", "protected_branch_push", target, "user", cfg, "s", project, "default")
	if !failureHasCode(got.Failures, "self_merge_actor_conflict") {
		t.Fatalf("lead executed self merge: %+v", got)
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
	got := decideVerifyActionWithPolicy(msgs, "gate/merge-398", "protected_branch_push", target, "user", cfg, "s", project, "default")
	if got.Decision == actionDecisionApproved {
		t.Fatalf("paused policy approved: %+v", got)
	}
	cfg.Operator.SelfOperator.Sessions["s"] = team.SelfOperatorSessionPolicy{Enabled: true, AllowedGateKinds: []string{"merge"}}
	msgs = append(msgs, state.Message{ID: "h1", From: "user", Thread: "gate/merge-398", Kind: state.KindAnswer, Subject: "DENIED: merge-398", Body: "Action: protected_branch_push\nTarget: " + target, Created: msgs[1].Created.Add(time.Second)})
	got = decideVerifyActionWithPolicy(msgs, "gate/merge-398", "protected_branch_push", target, "user", cfg, "s", project, "default")
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
			path := filepath.Join(selfApprovalStoreDir(project, "default", "s"), safeGateFile("gate/merge-398")+"-a1.receipt.json")
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, tc.mutate(b), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := validateSelfApprovalReceipt(project, "default", "s", "gate/merge-398", "a1", *msgs[1].Approval); err == nil {
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
	msgs = append(msgs, state.Message{ID: "q2", From: "cto", Thread: "gate/merge-398", Kind: state.KindQuestion, Body: "Gate-Kind: merge\nAction: protected_branch_push\nTarget: PR #398 head deadbee into main", Created: now})
	got := decideVerifyActionWithPolicy(msgs, "gate/merge-398", "protected_branch_push", target, "user", cfg, "s", project, "default")
	if !failureHasCode(got.Failures, "latest_gate_binding_mismatch") {
		t.Fatalf("newer mismatched question did not block: %+v", got)
	}
	msgs = msgs[:2]
	msgs = append(msgs, state.Message{ID: "h1", From: "user", Thread: "gate/merge-398", Kind: state.KindAnswer, Subject: "APPROVED: merge-398", Created: now, ApprovalPresent: true, ApprovalValid: false, ApprovalError: "unknown field"})
	got = decideVerifyActionWithPolicy(msgs, "gate/merge-398", "protected_branch_push", target, "user", cfg, "s", project, "default")
	if got.Decision == actionDecisionApproved || !failureHasCode(got.Failures, "human_intervention_pending") {
		t.Fatalf("malformed typed human context did not create barrier: %+v", got)
	}
}
