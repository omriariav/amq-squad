package cli

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func installTypedHumanAnswer(t *testing.T, project, profile, session, root string, question state.Message, decision string) state.Message {
	t.Helper()
	request := *question.AuthorizationRequest
	created := question.Created.Add(time.Second)
	approval := operatorauth.ApprovalContext{
		SchemaVersion: operatorauth.ApprovalSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion,
		Source: "human", GateKind: request.GateKind, Action: request.Action, Target: request.Target, Note: request.Note,
		QuestionMessageID: question.ID, AnsweredByRole: "operator", AnsweredByHandle: "user", VerifiedAt: created.Format(time.RFC3339Nano),
	}
	subject := strings.ToUpper(decision) + ": " + strings.TrimPrefix(question.Thread, "gate/")
	answer := withAuthorityRaw(state.Message{
		ID: "answer-" + question.ID, From: "user", To: []string{question.From}, Thread: question.Thread, RawThread: question.Thread,
		Kind: state.KindAnswer, Subject: subject, Body: question.Body, Created: created,
		ApprovalPresent: true, ApprovalValid: true, Approval: &approval,
	})
	answer.RawCreated = created.Format(time.RFC3339Nano)
	writeRawCLIReleaseMessage(t, root, question.From, "cur", answer, map[string]any{"approval": approval})
	receipt := operatorauth.Receipt{
		SchemaVersion: operatorauth.ReceiptSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion,
		Gate: question.Thread, GateKind: request.GateKind, Action: request.Action, Target: request.Target, Note: request.Note,
		Decision: decision, ApprovalSource: "human", QuestionMessageID: question.ID, AnswerMessageID: answer.ID, AnsweredBy: "user",
	}
	if err := writeSelfApprovalReceipt(project, profile, session, question.Thread, answer.ID, receipt); err != nil {
		t.Fatal(err)
	}
	return answer
}

func TestCompoundReleaseVerifyActionUsesGuardedHumanAuthority(t *testing.T) {
	fixture, active := newCLIActiveReleaseAttentionFixture(t)
	configureReleaseOperatorAnswerTeam(t, fixture)
	selected := selectedContextForCLIReleaseFixture(fixture)
	child := activeReleaseChildByRole(t, active.Active.Children, operatorauth.ReleaseChildTag)
	question := releaseQuestionForCLIClassification(t, selected.SessionRoot, child.QuestionMessageID)
	installTypedHumanAnswer(t, fixture.adapter.project, fixture.adapter.profile, fixture.adapter.session, fixture.adapter.root, question, actionDecisionApproved)
	var out bytes.Buffer
	err := executeVerifyActionInSelectedContext(verifyActionExecution{
		ProjectDir: selected.ProjectDir, Profile: selected.Profile, Session: selected.Session,
		Gate: question.Thread, Action: question.AuthorizationRequest.Action, Target: question.AuthorizationRequest.Target,
		Out: &out, JSON: true,
	}, selected)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Data verifyActionResult `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.Decision != actionDecisionApproved || env.Data.ApprovalSource != "human" || env.Data.SelfApproved || env.Data.Compound == nil || env.Data.Compound.Role != operatorauth.ReleaseChildTag {
		t.Fatalf("guarded release verification=%+v", env.Data)
	}
}

func TestCompoundReleaseVerifyActionMarkerStrippedFailsClosed(t *testing.T) {
	fixture, active := newCLIActiveReleaseAttentionFixture(t)
	configureReleaseOperatorAnswerTeam(t, fixture)
	selected := selectedContextForCLIReleaseFixture(fixture)
	child := activeReleaseChildByRole(t, active.Active.Children, operatorauth.ReleaseChildTag)
	question := releaseQuestionForCLIClassification(t, selected.SessionRoot, child.QuestionMessageID)
	context := cloneReleaseStateMessage(question).Context
	delete(context, "release_child")
	messages, _ := state.ScanSessionMessages(selected.SessionRoot, time.Now)
	for _, copy := range messages {
		if copy.ID == question.ID {
			writeRawCLIReleaseMessage(t, selected.SessionRoot, copy.Owner, string(copy.State), copy, context)
		}
	}
	var out bytes.Buffer
	err := executeVerifyActionInSelectedContext(verifyActionExecution{ProjectDir: selected.ProjectDir, Profile: selected.Profile, Session: selected.Session, Gate: question.Thread, Action: question.AuthorizationRequest.Action, Target: question.AuthorizationRequest.Target, Out: &out, JSON: true}, selected)
	if err == nil || !strings.Contains(out.String(), "compound_release_claim_ineligible") {
		t.Fatalf("marker-stripped verification err=%v out=%s", err, out.String())
	}
}

func TestVerifyActionListKindsIsContextFreeCanonical(t *testing.T) {
	out, _, err := captureOutput(t, func() error { return runVerifyAction([]string{"--list-kinds", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Data verifyActionKindsData `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(env.Data.Actions, operatorauth.SupportedActions()) {
		t.Fatalf("listed actions=%v want=%v", env.Data.Actions, operatorauth.SupportedActions())
	}
	if env.Data.CustomActionGuidance != verifyActionCustomGuidance || !strings.Contains(env.Data.CustomActionGuidance, "exact Action/Target") {
		t.Fatalf("JSON custom action guidance=%q", env.Data.CustomActionGuidance)
	}
	for _, capability := range operatorauth.ActionCapabilities() {
		for _, alias := range capability.Aliases {
			if slicesContains(env.Data.Actions, alias) {
				t.Fatalf("list-kinds exposed alias %q", alias)
			}
		}
	}
	human, _, err := captureOutput(t, func() error { return runVerifyAction([]string{"--list-kinds"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human, verifyActionCustomGuidance) || !strings.Contains(human, "manual verification") {
		t.Fatalf("human list-kinds omitted custom action guidance:\n%s", human)
	}
	if _, _, err := captureOutput(t, func() error { return runVerifyAction([]string{"--list-kinds", "unexpected"}) }); err == nil {
		t.Fatal("list-kinds accepted a positional argument")
	}
	if _, _, err := captureOutput(t, func() error {
		return runVerifyAction([]string{"--list-kinds", "--emit-authorization", "--signing-key-file", "/private/tmp/key"})
	}); err == nil {
		t.Fatal("list-kinds accepted authorization emission flags")
	}
}

func slicesContains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func TestVerifyActionEmitsSignedAuthorizationOnlyWithProvisionedKey(t *testing.T) {
	project, err := os.MkdirTemp("/private/tmp", "amq-cli-signed-project-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(project) })
	profile, session := team.DefaultProfile, "s"
	cfg := typedHumanTeam()
	cfg.Project, cfg.Workstream = project, session
	if err := team.Write(project, cfg); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	question := typedQuestion(project, session, "signed-question", "github_release", "publish v2.21.0", "exact release", now)
	base := filepath.Join(project, ".agent-mail")
	root := filepath.Join(base, session)
	writeAuthorizationMessage(t, base, session, "user", "new", question)
	answer := installTypedHumanAnswer(t, project, profile, session, root, question, actionDecisionApproved)
	cryptoDir, err := os.MkdirTemp("/private/tmp", "amq-cli-authz-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(cryptoDir) })
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(private)
	keyPath := filepath.Join(cryptoDir, "signer.pem")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(cryptoDir, "authorization.json")
	selected := cliReleaseSelectedContext{ProjectDir: project, Profile: profile, Session: session, NamespaceGeneration: "none", BaseRoot: base, SessionRoot: root}
	var out bytes.Buffer
	err = executeVerifyActionInSelectedContext(verifyActionExecution{ProjectDir: project, Profile: profile, Session: session, Gate: question.Thread, Action: question.AuthorizationRequest.Action, Target: question.AuthorizationRequest.Target, Out: &out, JSON: true, EmitAuthorization: true, SigningKeyFile: keyPath, AuthorizationOut: artifact}, selected)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := operatorauth.LoadAuthorizationEnvelope(artifact)
	if err != nil {
		t.Fatal(err)
	}
	trust := map[string]any{"schema_version": 1, "keys": []any{map[string]any{"key_id": operatorauth.KeyIDForEd25519(public), "algorithm": "ed25519", "public_key": base64.StdEncoding.EncodeToString(public), "status": "trusted"}}}
	raw, _ := json.Marshal(trust)
	trustPath := filepath.Join(cryptoDir, "trust.json")
	if err := os.WriteFile(trustPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := operatorauth.LoadAuthorizationTrustStore(trustPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := operatorauth.VerifyAuthorizationEnvelope(envelope, store); err != nil {
		t.Fatal(err)
	}
	verifyArgs := []string{"--file", artifact, "--action", question.AuthorizationRequest.Action, "--target", question.AuthorizationRequest.Target, "--trust-store", trustPath, "--json"}
	if _, _, err := captureOutput(t, func() error { return runVerifyAuthorization(verifyArgs) }); err != nil {
		t.Fatalf("fresh signed authorization did not reverify: %v", err)
	}
	receiptPath := selfApprovalReceiptPath(project, profile, session, question.Thread, question.ID, "answer-"+question.ID)
	receiptRaw, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(receiptPath, append(append([]byte(nil), receiptRaw...), ' '), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error { return runVerifyAuthorization(verifyArgs) }); err == nil {
		t.Fatal("authorization survived changed receipt bytes")
	}
	if err := os.WriteFile(receiptPath, receiptRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	preflightPath := filepath.Join(cryptoDir, "preflight.json")
	preflightRaw := []byte("{\"ok\":true}\n")
	if err := os.WriteFile(preflightPath, preflightRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	preflightSum := sha256.Sum256(preflightRaw)
	changedApproval := *answer.Approval
	changedApproval.PolicyRevision = 7
	changedApproval.PolicyHash = "sha256:" + strings.Repeat("a", 64)
	changedApproval.PreflightKind = "merge"
	changedApproval.PreflightPath = preflightPath
	changedApproval.PreflightSHA256 = "sha256:" + hex.EncodeToString(preflightSum[:])
	answer.Approval = &changedApproval
	writeRawCLIReleaseMessage(t, root, question.From, "cur", answer, map[string]any{"approval": changedApproval})
	changedReceipt, err := operatorauth.DecodeReceipt(receiptRaw)
	if err != nil {
		t.Fatal(err)
	}
	changedReceipt.PolicyRevision = changedApproval.PolicyRevision
	changedReceipt.PolicyHash = changedApproval.PolicyHash
	changedReceipt.Preflight = operatorauth.PreflightReceipt{Kind: changedApproval.PreflightKind, Path: changedApproval.PreflightPath, SHA256: changedApproval.PreflightSHA256, OK: true}
	changedReceiptRaw, _ := json.MarshalIndent(changedReceipt, "", "  ")
	if err := os.WriteFile(receiptPath, changedReceiptRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error { return runVerifyAuthorization(verifyArgs) }); err == nil {
		t.Fatal("authorization survived changed policy/preflight authority")
	}
	originalApproval := *answer.Approval
	originalApproval.PolicyRevision, originalApproval.PolicyHash = 0, ""
	originalApproval.PreflightKind, originalApproval.PreflightPath, originalApproval.PreflightSHA256 = "", "", ""
	answer.Approval = &originalApproval
	writeRawCLIReleaseMessage(t, root, question.From, "cur", answer, map[string]any{"approval": originalApproval})
	if err := os.WriteFile(receiptPath, receiptRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error { return runVerifyAuthorization(verifyArgs) }); err != nil {
		t.Fatalf("restored authority did not reverify: %v", err)
	}
	rotatedPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rotatedTrust := map[string]any{"schema_version": 1, "keys": []any{
		map[string]any{"key_id": operatorauth.KeyIDForEd25519(public), "algorithm": "ed25519", "public_key": base64.StdEncoding.EncodeToString(public), "status": "revoked"},
		map[string]any{"key_id": operatorauth.KeyIDForEd25519(rotatedPublic), "algorithm": "ed25519", "public_key": base64.StdEncoding.EncodeToString(rotatedPublic), "status": "trusted"},
	}}
	rotatedRaw, _ := json.Marshal(rotatedTrust)
	oldFinalCheck := verifyAuthorizationFinalCheck
	defer func() { verifyAuthorizationFinalCheck = oldFinalCheck }()
	verifyAuthorizationFinalCheck = func() {
		if writeErr := os.WriteFile(trustPath, rotatedRaw, 0o600); writeErr != nil {
			t.Fatalf("replace trust store during verification: %v", writeErr)
		}
	}
	if _, _, err := captureOutput(t, func() error { return runVerifyAuthorization(verifyArgs) }); err == nil || !strings.Contains(err.Error(), "revoked") {
		t.Fatalf("authorization survived trust revocation between checks: %v", err)
	}
	verifyAuthorizationFinalCheck = oldFinalCheck
	if err := os.WriteFile(trustPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(artifact)
	out.Reset()
	if err := executeVerifyActionInSelectedContext(verifyActionExecution{ProjectDir: project, Profile: profile, Session: session, Gate: question.Thread, Action: question.AuthorizationRequest.Action, Target: question.AuthorizationRequest.Target, Out: &out, JSON: true, EmitAuthorization: true, SigningKeyFile: keyPath, AuthorizationOut: artifact}, selected); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(artifact)
	if !bytes.Equal(first, second) {
		t.Fatal("identical signed re-verification changed immutable artifact")
	}

	preflightTarget := filepath.Join(cryptoDir, "preflight-target.json")
	preflightLink := filepath.Join(cryptoDir, "preflight-link.json")
	if err := os.WriteFile(preflightTarget, preflightRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(preflightTarget, preflightLink); err != nil {
		t.Fatal(err)
	}
	symlinkApproval := originalApproval
	symlinkApproval.PreflightKind = "merge"
	symlinkApproval.PreflightPath = preflightLink
	symlinkApproval.PreflightSHA256 = "sha256:" + hex.EncodeToString(preflightSum[:])
	answer.Approval = &symlinkApproval
	writeRawCLIReleaseMessage(t, root, question.From, "cur", answer, map[string]any{"approval": symlinkApproval})
	symlinkPreflightReceipt, err := operatorauth.DecodeReceipt(receiptRaw)
	if err != nil {
		t.Fatal(err)
	}
	symlinkPreflightReceipt.Preflight = operatorauth.PreflightReceipt{Kind: symlinkApproval.PreflightKind, Path: symlinkApproval.PreflightPath, SHA256: symlinkApproval.PreflightSHA256, OK: true}
	symlinkPreflightReceiptRaw, _ := json.MarshalIndent(symlinkPreflightReceipt, "", "  ")
	if err := os.WriteFile(receiptPath, symlinkPreflightReceiptRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := executeVerifyActionInSelectedContext(verifyActionExecution{ProjectDir: project, Profile: profile, Session: session, Gate: question.Thread, Action: question.AuthorizationRequest.Action, Target: question.AuthorizationRequest.Target, Out: &out, JSON: true, EmitAuthorization: true, SigningKeyFile: keyPath, AuthorizationOut: filepath.Join(cryptoDir, "symlink-preflight-authz.json")}, selected); err == nil {
		t.Fatal("digest-matching symlinked preflight evidence was signed")
	}

	answer.Approval = &originalApproval
	writeRawCLIReleaseMessage(t, root, question.From, "cur", answer, map[string]any{"approval": originalApproval})
	if err := os.WriteFile(receiptPath, receiptRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	receiptTarget := receiptPath + ".target"
	if err := os.Rename(receiptPath, receiptTarget); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(receiptTarget, receiptPath); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := executeVerifyActionInSelectedContext(verifyActionExecution{ProjectDir: project, Profile: profile, Session: session, Gate: question.Thread, Action: question.AuthorizationRequest.Action, Target: question.AuthorizationRequest.Target, Out: &out, JSON: true, EmitAuthorization: true, SigningKeyFile: keyPath, AuthorizationOut: filepath.Join(cryptoDir, "symlink-receipt-authz.json")}, selected); err == nil {
		t.Fatal("digest-matching symlinked approval receipt was signed")
	}
}

func TestSignedCompoundAuthorizationRejectsSuccessorGeneration(t *testing.T) {
	fixture, active := newCLIActiveReleaseAttentionFixture(t)
	configureReleaseOperatorAnswerTeam(t, fixture)
	selected := selectedContextForCLIReleaseFixture(fixture)
	child := activeReleaseChildByRole(t, active.Active.Children, operatorauth.ReleaseChildTag)
	question := releaseQuestionForCLIClassification(t, selected.SessionRoot, child.QuestionMessageID)
	installTypedHumanAnswer(t, fixture.adapter.project, fixture.adapter.profile, fixture.adapter.session, fixture.adapter.root, question, actionDecisionApproved)

	cryptoDir, err := os.MkdirTemp("/private/tmp", "amq-cli-compound-authz-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(cryptoDir) })
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(private)
	keyPath := filepath.Join(cryptoDir, "signer.pem")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(cryptoDir, "authorization.json")
	var out bytes.Buffer
	if err := executeVerifyActionInSelectedContext(verifyActionExecution{
		ProjectDir: selected.ProjectDir, Profile: selected.Profile, Session: selected.Session,
		Gate: question.Thread, Action: question.AuthorizationRequest.Action, Target: question.AuthorizationRequest.Target,
		Out: &out, JSON: true, EmitAuthorization: true, SigningKeyFile: keyPath, AuthorizationOut: artifact,
	}, selected); err != nil {
		t.Fatal(err)
	}
	trust := map[string]any{"schema_version": 1, "keys": []any{map[string]any{
		"key_id": operatorauth.KeyIDForEd25519(public), "algorithm": "ed25519", "public_key": base64.StdEncoding.EncodeToString(public), "status": "trusted",
	}}}
	trustRaw, _ := json.Marshal(trust)
	trustPath := filepath.Join(cryptoDir, "trust.json")
	if err := os.WriteFile(trustPath, trustRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	verifyArgs := []string{"--file", artifact, "--action", question.AuthorizationRequest.Action, "--target", question.AuthorizationRequest.Target, "--trust-store", trustPath, "--json"}
	if _, _, err := captureOutput(t, func() error { return runVerifyAuthorization(verifyArgs) }); err != nil {
		t.Fatalf("fresh compound authorization did not verify: %v", err)
	}

	spec := active.Prepared.Spec
	spec.TagTarget += "-successor"
	spec.GitHubReleaseTarget += "-successor"
	spec.Note.Summary += " successor"
	if _, err := fixture.store.PrepareSuccessor(active.Pointer.GenerationID, spec); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error { return runVerifyAuthorization(verifyArgs) }); err == nil {
		t.Fatal("signed compound authorization survived successor generation")
	}
}
