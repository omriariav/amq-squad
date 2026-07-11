package cli

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestSelfApprovalReservationConcurrentSingleWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gate.reservation.json")
	now := time.Now().UTC()
	oldNow := selfOperatorNow
	selfOperatorNow = func() time.Time { return now }
	t.Cleanup(func() { selfOperatorNow = oldNow })
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, token := range []string{"one", "two"} {
		wg.Add(1)
		go func(token string) {
			defer wg.Done()
			<-start
			results <- reserveSelfApproval(path, selfApprovalReservation{Token: token, QuestionMessageID: "q1", ExpiresAt: now.Add(time.Minute)})
		}(token)
	}
	close(start)
	wg.Wait()
	close(results)
	var succeeded, blocked int
	for err := range results {
		if err == nil {
			succeeded++
		} else if strings.Contains(err.Error(), "already reserved") {
			blocked++
		} else {
			t.Fatalf("unexpected reserve error: %v", err)
		}
	}
	if succeeded != 1 || blocked != 1 {
		t.Fatalf("succeeded=%d blocked=%d", succeeded, blocked)
	}
}

func TestSelfApprovalSendingWithoutAnswerBlocksUntilExpiry(t *testing.T) {
	fx := newSelfApprovalRecoveryFixture(t, false)
	oldNow := selfOperatorNow
	selfOperatorNow = func() time.Time { return fx.now }
	t.Cleanup(func() { selfOperatorNow = oldNow })
	err := reconcileSentSelfApproval(fx.project, team.DefaultProfile, "s", fx.gate, "merge", fx.action, fx.target, fx.question, fx.reservation, fx.reservationPath)
	if err == nil || errors.Is(err, errSelfApprovalRetrySafe) {
		t.Fatalf("unexpired send became retryable: %v", err)
	}
	if _, statErr := os.Stat(fx.reservationPath); statErr != nil {
		t.Fatalf("reservation removed early: %v", statErr)
	}
	selfOperatorNow = func() time.Time { return fx.now.Add(2 * time.Minute) }
	err = reconcileSentSelfApproval(fx.project, team.DefaultProfile, "s", fx.gate, "merge", fx.action, fx.target, fx.question, fx.reservation, fx.reservationPath)
	if !errors.Is(err, errSelfApprovalRetrySafe) {
		t.Fatalf("expired zero-answer send = %v", err)
	}
	if _, statErr := os.Stat(fx.reservationPath); !os.IsNotExist(statErr) {
		t.Fatalf("expired reservation remains: %v", statErr)
	}
}

func TestSelfApprovalReconcilesDuplicateMailboxCopiesByID(t *testing.T) {
	fx := newSelfApprovalRecoveryFixture(t, true)
	oldNow := selfOperatorNow
	selfOperatorNow = func() time.Time { return fx.now }
	t.Cleanup(func() { selfOperatorNow = oldNow })
	if err := reconcileSentSelfApproval(fx.project, team.DefaultProfile, "s", fx.gate, "merge", fx.action, fx.target, fx.question, fx.reservation, fx.reservationPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fx.reservationPath); !os.IsNotExist(err) {
		t.Fatalf("reservation remains: %v", err)
	}
	receiptPath := filepath.Join(selfApprovalStoreDir(fx.project, team.DefaultProfile, "s"), safeGateFile(fx.gate)+"-a1.receipt.json")
	if _, err := os.Stat(receiptPath); err != nil {
		t.Fatalf("receipt missing: %v", err)
	}
}

func TestSelfApprovalRejectsConflictingMailboxCopiesByID(t *testing.T) {
	fx := newSelfApprovalRecoveryFixture(t, true)
	path := filepath.Join(fx.project, ".agent-mail", "s", "agents", "user", "inbox", "cur", "a1.md")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b = []byte(strings.Replace(string(b), "APPROVED: merge-398", "DENIED: merge-398", 1))
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	oldNow := selfOperatorNow
	selfOperatorNow = func() time.Time { return fx.now }
	t.Cleanup(func() { selfOperatorNow = oldNow })
	err = reconcileSentSelfApproval(fx.project, team.DefaultProfile, "s", fx.gate, "merge", fx.action, fx.target, fx.question, fx.reservation, fx.reservationPath)
	if err == nil || !strings.Contains(err.Error(), "conflicting mailbox copies") {
		t.Fatalf("conflicting copies = %v", err)
	}
	if _, statErr := os.Stat(fx.reservationPath); statErr != nil {
		t.Fatalf("reservation removed after conflict: %v", statErr)
	}
}

type selfApprovalRecoveryFixture struct {
	project, gate, action, target, reservationPath string
	now                                            time.Time
	question                                       state.Message
	reservation                                    selfApprovalReservation
}

func newSelfApprovalRecoveryFixture(t *testing.T, withAnswer bool) selfApprovalRecoveryFixture {
	t.Helper()
	project := t.TempDir()
	base := filepath.Join(project, ".agent-mail")
	now := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
	op := team.DefaultOperator()
	op.InteractionMode = team.OperatorInteractionSelfOperator
	op.SelfOperator = &team.SelfOperatorPolicy{LeadRole: "cto", PolicyRevision: 3, Sessions: map[string]team.SelfOperatorSessionPolicy{"s": {Enabled: true, AllowedGateKinds: []string{"merge"}}}}
	cfg := team.Team{Operator: &op, Orchestrated: true, Lead: "cto", Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: "s"}}}
	if err := team.Write(project, cfg); err != nil {
		t.Fatal(err)
	}
	seedAgentRecord(t, base, "s", "cto", launch.Record{Binary: "codex", Role: "cto", Handle: "cto", Session: "s", AgentPID: 1})
	gate, action, target := "gate/merge-398", "protected_branch_push", "PR #398 head abc1234 into main"
	question := state.Message{ID: "q1", From: "cto", Thread: gate, Kind: state.KindQuestion, Subject: "APPROVAL: merge-398", Body: "Gate-Kind: merge\nAction: " + action + "\nTarget: " + target, Created: now}
	writeSelfApprovalTestMessage(t, filepath.Join(base, "s", "agents", "user"), "new", question, nil)
	view := team.EffectiveSelfOperator(cfg, "s")
	evidence := []byte(`{"subject":"PR #398","head_sha":"abc1234","base":"main","ci":{"state":"success","sha":"abc1234","source":"ci","checked_at":"2026-07-11T00:00:00Z"},"review":{"state":"clean","sha":"abc1234","source":"review","checked_at":"2026-07-11T00:00:00Z"},"exceptions":[]}`)
	sum := sha256.Sum256(evidence)
	digest := fmt.Sprintf("sha256:%x", sum)
	evidencePath := selfApprovalEvidencePath(project, team.DefaultProfile, "s", gate, "q1", digest)
	if err := atomicWriteJSONBytes(evidencePath, evidence); err != nil {
		t.Fatal(err)
	}
	approval := operatorauth.ApprovalContext{SchemaVersion: 1, Source: "self_operator", SelfApproved: true, GateKind: "merge", Action: action, Target: target, QuestionMessageID: "q1", AnsweredByRole: "cto", AnsweredByHandle: "cto", PolicyRevision: view.PolicyRevision, PolicyHash: view.PolicyHash, PreflightKind: "verify_merge", PreflightSHA256: digest, PreflightPath: evidencePath, VerifiedAt: now.Format(time.RFC3339Nano)}
	if withAnswer {
		answer := state.Message{ID: "a1", From: "cto", Thread: gate, Kind: state.KindAnswer, Subject: "APPROVED: merge-398", Body: "Gate-Kind: merge\nAction: " + action + "\nTarget: " + target, Created: now.Add(time.Second)}
		writeSelfApprovalTestMessage(t, filepath.Join(base, "s", "agents", "cto"), "cur", answer, &approval)
		writeSelfApprovalTestMessage(t, filepath.Join(base, "s", "agents", "user"), "cur", answer, &approval)
	}
	reservationPath := selfApprovalReservationPath(project, team.DefaultProfile, "s", gate)
	reservation := selfApprovalReservation{Token: "token", QuestionMessageID: "q1", PolicyRevision: view.PolicyRevision, PolicyHash: view.PolicyHash, ExpiresAt: now.Add(time.Minute), Sending: true}
	b, _ := json.Marshal(reservation)
	if err := atomicWriteJSONBytes(reservationPath, b); err != nil {
		t.Fatal(err)
	}
	return selfApprovalRecoveryFixture{project: project, gate: gate, action: action, target: target, now: now, question: question, reservation: reservation, reservationPath: reservationPath}
}

func writeSelfApprovalTestMessage(t *testing.T, agentDir, box string, msg state.Message, approval *operatorauth.ApprovalContext) {
	t.Helper()
	meta := map[string]any{"schema": 1, "id": msg.ID, "from": msg.From, "to": []string{"cto"}, "thread": msg.Thread, "subject": msg.Subject, "created": msg.Created.UTC().Format(time.RFC3339Nano), "kind": string(msg.Kind)}
	if approval != nil {
		meta["context"] = map[string]any{"approval": approval}
	}
	b, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(agentDir, "inbox", box)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, msg.ID+".md"), []byte("---json\n"+string(b)+"\n---\n"+msg.Body+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
