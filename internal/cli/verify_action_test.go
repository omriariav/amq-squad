package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
	"github.com/omriariav/amq-squad/v3/internal/state"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

var verifyActionNow = time.Date(2026, 7, 9, 8, 0, 0, 0, time.UTC)

func TestVerifyActionBlocksGatedReleaseBeforeOperatorAnswer(t *testing.T) {
	base, project := seedVerifyActionFixture(t)
	seedVerifyActionMessage(t, filepath.Join(base, "issue-349", "agents", "user"), "new",
		"q1", "cto", []string{"user"}, "gate/release-v1-41-0", "APPROVAL: github_release",
		string(state.KindQuestion), "Action: feature_branch_push\nTarget: draft v1.41.0 release for omriariav/workspace-cli", verifyActionNow)

	out, err := runVerifyActionExec(t, base, project, verifyActionExecution{
		Session: "issue-349",
		Gate:    "release-v1-41-0",
		Action:  "feature_branch_push",
		Target:  "draft v1.41.0 release for omriariav/workspace-cli",
		JSON:    true,
	})
	if err == nil {
		t.Fatal("unanswered release gate should block")
	}
	if code := ExitCode(err); code != ExitActionUnbound {
		t.Fatalf("ExitCode = %d, want %d (%v)", code, ExitActionUnbound, err)
	}
	env := decodeJSONEnvelope[verifyActionResult](t, out)
	if env.Kind != "verify_action" || env.Data.Decision != actionDecisionUnbound || env.Data.MessageID != "q1" {
		t.Fatalf("unexpected diagnostic-only envelope: %+v", env)
	}
}

func TestVerifyActionPassesBoundOperatorAnswer(t *testing.T) {
	base, project := seedVerifyActionFixture(t)
	body := "Action: feature_branch_push\nTarget: draft v1.41.0 release for omriariav/workspace-cli"
	seedVerifyActionMessage(t, filepath.Join(base, "issue-349", "agents", "user"), "new",
		"q1", "cto", []string{"user"}, "gate/release-v1-41-0", "APPROVAL: github_release",
		string(state.KindQuestion), body, verifyActionNow)
	seedVerifyActionMessage(t, filepath.Join(base, "issue-349", "agents", "cto"), "cur",
		"a1", "user", []string{"cto"}, "gate/release-v1-41-0", "APPROVED: github_release",
		string(state.KindAnswer), body, verifyActionNow.Add(time.Minute))

	out, err := runVerifyActionExec(t, base, project, verifyActionExecution{
		Session: "issue-349",
		Gate:    "gate/release-v1-41-0",
		Action:  "feature_branch_push",
		Target:  "draft v1.41.0 release for omriariav/workspace-cli",
		JSON:    true,
	})
	if err == nil || ExitCode(err) != ExitActionUnbound {
		t.Fatalf("raw answer unexpectedly authorized: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[verifyActionResult](t, out)
	if env.Data.Decision != actionDecisionUnbound || env.Data.MessageID != "q1" {
		t.Fatalf("unexpected raw diagnostic envelope: %+v", env)
	}
}

func TestVerifyActionLatestOperatorAnswerWinsOverOlderApproval(t *testing.T) {
	base, project := seedVerifyActionFixture(t)
	body := "Action: feature_branch_push\nTarget: draft v1.41.0 release for omriariav/workspace-cli"
	seedVerifyActionMessage(t, filepath.Join(base, "issue-349", "agents", "user"), "new",
		"q1", "cto", []string{"user"}, "gate/release-v1-41-0", "APPROVAL: github_release",
		string(state.KindQuestion), body, verifyActionNow)
	seedVerifyActionMessage(t, filepath.Join(base, "issue-349", "agents", "cto"), "cur",
		"a1", "user", []string{"cto"}, "gate/release-v1-41-0", "APPROVED: github_release",
		string(state.KindAnswer), body, verifyActionNow.Add(time.Minute))
	seedVerifyActionMessage(t, filepath.Join(base, "issue-349", "agents", "cto"), "cur",
		"a2", "user", []string{"cto"}, "gate/release-v1-41-0", "DENIED: hold",
		string(state.KindAnswer), "hold this release", verifyActionNow.Add(2*time.Minute))

	out, err := runVerifyActionExec(t, base, project, verifyActionExecution{
		Session: "issue-349",
		Gate:    "release-v1-41-0",
		Action:  "feature_branch_push",
		Target:  "draft v1.41.0 release for omriariav/workspace-cli",
		JSON:    true,
	})
	if err == nil {
		t.Fatal("newer unbound denial must outrank older bound approval")
	}
	if code := ExitCode(err); code != ExitActionUnbound {
		t.Fatalf("ExitCode = %d, want %d (%v)", code, ExitActionUnbound, err)
	}
	env := decodeJSONEnvelope[verifyActionResult](t, out)
	if env.Data.Decision != actionDecisionUnbound || env.Data.MessageID != "q1" {
		t.Fatalf("latest unbound answer not reported: %+v", env.Data)
	}
}

func TestVerifyActionSameTimestampUsesIDTiebreak(t *testing.T) {
	base, project := seedVerifyActionFixture(t)
	body := "Action: feature_branch_push\nTarget: tag v1.41.0 in omriariav/workspace-cli"
	seedVerifyActionMessage(t, filepath.Join(base, "issue-349", "agents", "user"), "new",
		"q1", "cto", []string{"user"}, "gate/tag-v1-41-0", "APPROVAL: tag",
		string(state.KindQuestion), body, verifyActionNow)
	seedVerifyActionMessage(t, filepath.Join(base, "issue-349", "agents", "cto"), "cur",
		"a1", "user", []string{"cto"}, "gate/tag-v1-41-0", "APPROVED: tag",
		string(state.KindAnswer), body, verifyActionNow.Add(time.Minute))
	seedVerifyActionMessage(t, filepath.Join(base, "issue-349", "agents", "cto"), "cur",
		"a2", "user", []string{"cto"}, "gate/tag-v1-41-0", "DENIED: tag",
		string(state.KindAnswer), body, verifyActionNow.Add(time.Minute))

	out, err := runVerifyActionExec(t, base, project, verifyActionExecution{
		Session: "issue-349",
		Gate:    "tag-v1-41-0",
		Action:  "feature_branch_push",
		Target:  "tag v1.41.0 in omriariav/workspace-cli",
		JSON:    true,
	})
	if err == nil {
		t.Fatal("later same-timestamp ID denial should block")
	}
	if code := ExitCode(err); code != ExitActionUnbound {
		t.Fatalf("ExitCode = %d, want %d (%v)", code, ExitActionUnbound, err)
	}
	env := decodeJSONEnvelope[verifyActionResult](t, out)
	if env.Data.Decision != actionDecisionUnbound || env.Data.MessageID != "q1" {
		t.Fatalf("same timestamp tiebreak not pinned: %+v", env.Data)
	}
}

func TestVerifyActionRejectsApprovedAnswerForDifferentTarget(t *testing.T) {
	base, project := seedVerifyActionFixture(t)
	seedVerifyActionMessage(t, filepath.Join(base, "issue-349", "agents", "user"), "new",
		"q1", "cto", []string{"user"}, "gate/release-v1-41-0", "APPROVAL: github_release",
		string(state.KindQuestion), "Action: feature_branch_push\nTarget: draft v1.41.0 release for omriariav/workspace-cli", verifyActionNow)
	seedVerifyActionMessage(t, filepath.Join(base, "issue-349", "agents", "cto"), "cur",
		"a1", "user", []string{"cto"}, "gate/release-v1-41-0", "APPROVED: github_release",
		string(state.KindAnswer), "Action: feature_branch_push\nTarget: draft v1.40.0 release for omriariav/workspace-cli", verifyActionNow.Add(time.Minute))

	_, err := runVerifyActionExec(t, base, project, verifyActionExecution{
		Session: "issue-349",
		Gate:    "release-v1-41-0",
		Action:  "feature_branch_push",
		Target:  "draft v1.41.0 release for omriariav/workspace-cli",
		JSON:    true,
	})
	if err == nil {
		t.Fatal("approved answer for a different target should not pass")
	}
	if code := ExitCode(err); code != ExitActionUnbound {
		t.Fatalf("ExitCode = %d, want %d (%v)", code, ExitActionUnbound, err)
	}
}

func TestVerifyActionQuestionRequiresStrictBindingFields(t *testing.T) {
	base, project := seedVerifyActionFixture(t)
	seedVerifyActionMessage(t, filepath.Join(base, "issue-349", "agents", "user"), "new",
		"q1", "cto", []string{"user"}, "gate/tag-main", "APPROVAL: do not tag main",
		string(state.KindQuestion), "Please approve tag only after checking main", verifyActionNow)
	seedVerifyActionMessage(t, filepath.Join(base, "issue-349", "agents", "cto"), "cur",
		"a1", "user", []string{"cto"}, "gate/tag-main", "APPROVED: tag",
		string(state.KindAnswer), "Action: feature_branch_push\nTarget: main", verifyActionNow.Add(time.Minute))

	_, err := runVerifyActionExec(t, base, project, verifyActionExecution{
		Session: "issue-349",
		Gate:    "tag-main",
		Action:  "feature_branch_push",
		Target:  "main",
	})
	if err == nil {
		t.Fatal("prose question mentioning action/target should not bind without Action/Target fields")
	}
	if code := ExitCode(err); code != ExitActionUnbound {
		t.Fatalf("ExitCode = %d, want %d (%v)", code, ExitActionUnbound, err)
	}
}

func TestVerifyActionRejectsProseOnlyOperatorAnswer(t *testing.T) {
	base, project := seedVerifyActionFixture(t)
	target := "draft v1.41.0 release for omriariav/workspace-cli"
	seedVerifyActionMessage(t, filepath.Join(base, "issue-349", "agents", "user"), "new",
		"q1", "cto", []string{"user"}, "gate/release-v1-41-0", "APPROVAL: github_release",
		string(state.KindQuestion), "Action: feature_branch_push\nTarget: "+target, verifyActionNow)
	seedVerifyActionMessage(t, filepath.Join(base, "issue-349", "agents", "cto"), "cur",
		"a1", "user", []string{"cto"}, "gate/release-v1-41-0", "APPROVED: github_release",
		string(state.KindAnswer), "Approved to proceed with feature_branch_push for "+target, verifyActionNow.Add(time.Minute))

	_, err := runVerifyActionExec(t, base, project, verifyActionExecution{
		Session: "issue-349",
		Gate:    "release-v1-41-0",
		Action:  "feature_branch_push",
		Target:  target,
	})
	if err == nil {
		t.Fatal("prose-only operator answer should not pass without structured Action/Target fields")
	}
	if code := ExitCode(err); code != ExitActionUnbound {
		t.Fatalf("ExitCode = %d, want %d (%v)", code, ExitActionUnbound, err)
	}
}

func TestVerifyActionRequiresOperatorAnswerSender(t *testing.T) {
	base, project := seedVerifyActionFixture(t)
	body := "Action: feature_branch_push\nTarget: tag v1.41.0 in omriariav/workspace-cli"
	seedVerifyActionMessage(t, filepath.Join(base, "issue-349", "agents", "user"), "new",
		"q1", "cto", []string{"user"}, "gate/tag-v1-41-0", "APPROVAL: tag",
		string(state.KindQuestion), body, verifyActionNow)
	seedVerifyActionMessage(t, filepath.Join(base, "issue-349", "agents", "cto"), "cur",
		"a1", "cto", []string{"cto"}, "gate/tag-v1-41-0", "APPROVED: tag",
		string(state.KindAnswer), body, verifyActionNow.Add(time.Minute))

	_, err := runVerifyActionExec(t, base, project, verifyActionExecution{
		Session: "issue-349",
		Gate:    "tag-v1-41-0",
		Action:  "feature_branch_push",
		Target:  "tag v1.41.0 in omriariav/workspace-cli",
	})
	if err == nil {
		t.Fatal("answer from non-operator should not pass")
	}
	if code := ExitCode(err); code != ExitActionUnbound {
		t.Fatalf("ExitCode = %d, want %d (%v)", code, ExitActionUnbound, err)
	}
}

func TestVerifyActionRejectsApprovalBeforeLatestGateQuestion(t *testing.T) {
	base, project := seedVerifyActionFixture(t)
	body := "Action: feature_branch_push\nTarget: tag v1.41.0 in omriariav/workspace-cli"
	seedVerifyActionMessage(t, filepath.Join(base, "issue-349", "agents", "cto"), "cur",
		"a1", "user", []string{"cto"}, "gate/tag-v1-41-0", "APPROVED: tag",
		string(state.KindAnswer), body, verifyActionNow)
	seedVerifyActionMessage(t, filepath.Join(base, "issue-349", "agents", "user"), "new",
		"q1", "cto", []string{"user"}, "gate/tag-v1-41-0", "APPROVAL: tag",
		string(state.KindQuestion), body, verifyActionNow.Add(time.Minute))

	_, err := runVerifyActionExec(t, base, project, verifyActionExecution{
		Session: "issue-349",
		Gate:    "tag-v1-41-0",
		Action:  "feature_branch_push",
		Target:  "tag v1.41.0 in omriariav/workspace-cli",
	})
	if err == nil {
		t.Fatal("approval before the latest matching gate question should not pass")
	}
	if code := ExitCode(err); code != ExitActionUnbound {
		t.Fatalf("ExitCode = %d, want %d (%v)", code, ExitActionUnbound, err)
	}
}

func TestVerifyActionDeniedExitCode(t *testing.T) {
	base, project := seedVerifyActionFixture(t)
	body := "Action: feature_branch_push\nTarget: email release announcement to customers"
	seedVerifyActionMessage(t, filepath.Join(base, "issue-349", "agents", "user"), "new",
		"q1", "cto", []string{"user"}, "gate/send-release-email", "APPROVAL: external_send",
		string(state.KindQuestion), body, verifyActionNow)
	seedVerifyActionMessage(t, filepath.Join(base, "issue-349", "agents", "cto"), "cur",
		"a1", "user", []string{"cto"}, "gate/send-release-email", "DENIED: external_send",
		string(state.KindAnswer), body, verifyActionNow.Add(time.Minute))

	_, err := runVerifyActionExec(t, base, project, verifyActionExecution{
		Session: "issue-349",
		Gate:    "send-release-email",
		Action:  "feature_branch_push",
		Target:  "email release announcement to customers",
	})
	if err == nil {
		t.Fatal("denied gate should fail")
	}
	if code := ExitCode(err); code != ExitActionUnbound {
		t.Fatalf("ExitCode = %d, want %d (%v)", code, ExitActionUnbound, err)
	}
}

// 3-2 fix 1 (ruling 4): the v2 merge kinds are the merge train's; verify
// action refuses them even with a bound operator approval.
func TestVerifyActionRefusesMergeKindsForTheTrain(t *testing.T) {
	for _, action := range []string{"default_branch_push", "protected_branch_push"} {
		base, project := seedVerifyActionFixture(t)
		body := "Action: " + action + "\nTarget: PR #7 head abc into main"
		seedVerifyActionMessage(t, filepath.Join(base, "issue-349", "agents", "user"), "new",
			"q1", "cto", []string{"user"}, "gate/merge-7", "APPROVAL: "+action, string(state.KindQuestion), body, verifyActionNow)
		seedVerifyActionMessage(t, filepath.Join(base, "issue-349", "agents", "cto"), "cur",
			"a1", "user", []string{"cto"}, "gate/merge-7", "APPROVED: "+action, string(state.KindAnswer), body, verifyActionNow.Add(time.Minute))
		out, err := runVerifyActionExec(t, base, project, verifyActionExecution{Session: "issue-349", Gate: "merge-7", Action: action, Target: "PR #7 head abc into main"})
		if err == nil || !strings.Contains(out+err.Error(), "merge_train") {
			t.Fatalf("%s: verify action approved a merge kind: %v\n%s", action, err, out)
		}
	}
}

func seedVerifyActionFixture(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()
	project := t.TempDir()
	if err := team.Write(project, team.Team{
		Members:      []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-349"}},
		Orchestrated: true,
		Lead:         "cto",
	}); err != nil {
		t.Fatal(err)
	}
	seedAgentRecord(t, base, "issue-349", "cto", launch.Record{
		Binary: "codex", Handle: "cto", Role: "cto", Session: "issue-349", AgentPID: 111,
	})
	return base, project
}

func runVerifyActionExec(t *testing.T, base, project string, x verifyActionExecution) (string, error) {
	t.Helper()
	var out bytes.Buffer
	x.ProjectDir = project
	x.Profile = squadnamespace.DefaultProfile
	x.Out = &out
	x.Now = func() time.Time { return verifyActionNow }
	selected := cliReleaseSelectedContext{ProjectDir: project, Profile: squadnamespace.DefaultProfile, Session: x.Session, NamespaceGeneration: "test", BaseRoot: base, SessionRoot: filepath.Join(base, x.Session)}
	err := executeVerifyActionInSelectedContext(x, selected)
	return out.String(), err
}

func seedVerifyActionMessage(t *testing.T, agentDir, box, id, from string, to []string, thread, subject, kind, body string, created time.Time) {
	t.Helper()
	dir := filepath.Join(agentDir, "inbox", box)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var recipients []string
	for _, r := range to {
		recipients = append(recipients, fmt.Sprintf("%q", r))
	}
	msg := fmt.Sprintf(`---json
{
  "schema": 1,
  "id": %q,
  "from": %q,
  "to": [%s],
  "thread": %q,
  "subject": %q,
  "created": %q,
  "kind": %q
}
---
%s
`, id, from, strings.Join(recipients, ", "), thread, subject, created.UTC().Format(time.RFC3339Nano), kind, body)
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(msg), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestVerifyActionLatestQuestionAndMalformedHumanAreBarriers is restored from
// self_operator_verify_test.go (2a-2) with a human-only fixture: a newer
// mismatched question and a malformed human answer both block approval.
func TestVerifyActionLatestQuestionAndMalformedHumanAreBarriers(t *testing.T) {
	project := t.TempDir()
	cfg := team.Team{Project: project, Orchestrated: true, Lead: "cto", Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: "s"}}}
	target := "PR #398 head abc1234 into main"
	request := operatorauth.GateRequestContext{SchemaVersion: operatorauth.GateRequestSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion, Gate: "gate/merge-398", Thread: "gate/merge-398", Namespace: operatorauth.NamespaceBinding{ProjectDir: project, Profile: "default", Session: "s", NamespaceID: "default/s", Generation: "none"}, GateKind: "merge", Action: "protected_branch_push", Target: target}
	now := time.Now()
	question := func(id, target string, req *operatorauth.GateRequestContext, at time.Time) state.Message {
		return withAuthorityRaw(state.Message{ID: id, From: "cto", To: []string{"user"}, Thread: "gate/merge-398", RawThread: "gate/merge-398", Kind: state.KindQuestion, Body: "Gate-Kind: merge\nAction: protected_branch_push\nTarget: " + target, Created: at, AuthorizationRequestPresent: true, AuthorizationRequestValid: true, AuthorizationRequest: req})
	}
	hasCode := func(failures []verifyMergeFailure, code string) bool {
		for _, failure := range failures {
			if failure.Code == code {
				return true
			}
		}
		return false
	}
	q1 := question("q1", target, &request, now)
	newRequest := request
	newRequest.Target = "PR #398 head deadbee into main"
	q2 := question("q2", newRequest.Target, &newRequest, now.Add(time.Second))
	got := decideTypedVerifyActionWithPolicy([]state.Message{q1, q2}, "gate/merge-398", "protected_branch_push", target, "user", cfg, "s", project, "default")
	if !hasCode(got.Failures, "typed_gate_binding_mismatch") {
		t.Fatalf("newer mismatched question did not block: %+v", got)
	}
	h1 := withAuthorityRaw(state.Message{ID: "h1", From: "user", To: []string{"cto"}, Thread: "gate/merge-398", Kind: state.KindAnswer, Subject: "APPROVED: merge-398", Body: q1.Body, Created: now.Add(time.Second), ApprovalPresent: true, ApprovalValid: false, ApprovalError: "unknown field"})
	got = decideTypedVerifyActionWithPolicy([]state.Message{q1, h1}, "gate/merge-398", "protected_branch_push", target, "user", cfg, "s", project, "default")
	if got.Decision == actionDecisionApproved || !hasCode(got.Failures, "human_approval_context_missing_or_malformed") {
		t.Fatalf("malformed typed human context did not create barrier: %+v", got)
	}
}
