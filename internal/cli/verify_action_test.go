package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

var verifyActionNow = time.Date(2026, 7, 9, 8, 0, 0, 0, time.UTC)

func TestVerifyActionBlocksGatedReleaseBeforeOperatorAnswer(t *testing.T) {
	base, project := seedVerifyActionFixture(t)
	seedVerifyActionMessage(t, filepath.Join(base, "issue-349", "agents", "user"), "new",
		"q1", "cto", []string{"user"}, "gate/release-v1-41-0", "APPROVAL: github_release",
		string(state.KindQuestion), "Action: github_release\nTarget: draft v1.41.0 release for omriariav/workspace-cli", verifyActionNow)

	out, err := runVerifyActionExec(t, base, project, verifyActionExecution{
		Session: "issue-349",
		Gate:    "release-v1-41-0",
		Action:  "github_release",
		Target:  "draft v1.41.0 release for omriariav/workspace-cli",
		JSON:    true,
	})
	if err == nil {
		t.Fatal("unanswered release gate should block")
	}
	if code := ExitCode(err); code != ExitActionPending {
		t.Fatalf("ExitCode = %d, want %d (%v)", code, ExitActionPending, err)
	}
	env := decodeJSONEnvelope[verifyActionResult](t, out)
	if env.Kind != "verify_action" || env.Data.Decision != actionDecisionPending || env.Data.MessageID != "q1" {
		t.Fatalf("unexpected pending envelope: %+v", env)
	}
}

func TestVerifyActionPassesBoundOperatorAnswer(t *testing.T) {
	base, project := seedVerifyActionFixture(t)
	body := "Action: github_release\nTarget: draft v1.41.0 release for omriariav/workspace-cli"
	seedVerifyActionMessage(t, filepath.Join(base, "issue-349", "agents", "user"), "new",
		"q1", "cto", []string{"user"}, "gate/release-v1-41-0", "APPROVAL: github_release",
		string(state.KindQuestion), body, verifyActionNow)
	seedVerifyActionMessage(t, filepath.Join(base, "issue-349", "agents", "cto"), "cur",
		"a1", "user", []string{"cto"}, "gate/release-v1-41-0", "APPROVED: github_release",
		string(state.KindAnswer), body, verifyActionNow.Add(time.Minute))

	out, err := runVerifyActionExec(t, base, project, verifyActionExecution{
		Session: "issue-349",
		Gate:    "gate/release-v1-41-0",
		Action:  "release",
		Target:  "draft v1.41.0 release for omriariav/workspace-cli",
		JSON:    true,
	})
	if err != nil {
		t.Fatalf("bound operator answer should pass: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[verifyActionResult](t, out)
	if env.Data.Decision != actionDecisionApproved || env.Data.AnsweredBy != "user" || env.Data.MessageID != "a1" {
		t.Fatalf("unexpected approved envelope: %+v", env)
	}
}

func TestVerifyActionRejectsApprovedAnswerForDifferentTarget(t *testing.T) {
	base, project := seedVerifyActionFixture(t)
	seedVerifyActionMessage(t, filepath.Join(base, "issue-349", "agents", "user"), "new",
		"q1", "cto", []string{"user"}, "gate/release-v1-41-0", "APPROVAL: github_release",
		string(state.KindQuestion), "Action: github_release\nTarget: draft v1.41.0 release for omriariav/workspace-cli", verifyActionNow)
	seedVerifyActionMessage(t, filepath.Join(base, "issue-349", "agents", "cto"), "cur",
		"a1", "user", []string{"cto"}, "gate/release-v1-41-0", "APPROVED: github_release",
		string(state.KindAnswer), "Action: github_release\nTarget: draft v1.40.0 release for omriariav/workspace-cli", verifyActionNow.Add(time.Minute))

	_, err := runVerifyActionExec(t, base, project, verifyActionExecution{
		Session: "issue-349",
		Gate:    "release-v1-41-0",
		Action:  "github_release",
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

func TestVerifyActionRejectsProseOnlyOperatorAnswer(t *testing.T) {
	base, project := seedVerifyActionFixture(t)
	target := "draft v1.41.0 release for omriariav/workspace-cli"
	seedVerifyActionMessage(t, filepath.Join(base, "issue-349", "agents", "user"), "new",
		"q1", "cto", []string{"user"}, "gate/release-v1-41-0", "APPROVAL: github_release",
		string(state.KindQuestion), "Action: github_release\nTarget: "+target, verifyActionNow)
	seedVerifyActionMessage(t, filepath.Join(base, "issue-349", "agents", "cto"), "cur",
		"a1", "user", []string{"cto"}, "gate/release-v1-41-0", "APPROVED: github_release",
		string(state.KindAnswer), "Approved to proceed with github_release for "+target, verifyActionNow.Add(time.Minute))

	_, err := runVerifyActionExec(t, base, project, verifyActionExecution{
		Session: "issue-349",
		Gate:    "release-v1-41-0",
		Action:  "github_release",
		Target:  target,
	})
	if err == nil {
		t.Fatal("prose-only operator answer should not pass without structured Action/Target fields")
	}
	if code := ExitCode(err); code != ExitActionUnbound {
		t.Fatalf("ExitCode = %d, want %d (%v)", code, ExitActionUnbound, err)
	}
}

func TestVerifyActionNoGateExitCode(t *testing.T) {
	base, project := seedVerifyActionFixture(t)
	_, err := runVerifyActionExec(t, base, project, verifyActionExecution{
		Session: "issue-349",
		Gate:    "missing-release-gate",
		Action:  "github_release",
		Target:  "draft v1.41.0 release for omriariav/workspace-cli",
	})
	if err == nil {
		t.Fatal("missing gate should fail")
	}
	if code := ExitCode(err); code != ExitActionNoGate {
		t.Fatalf("ExitCode = %d, want %d (%v)", code, ExitActionNoGate, err)
	}
}

func TestVerifyActionRequiresOperatorAnswerSender(t *testing.T) {
	base, project := seedVerifyActionFixture(t)
	body := "Action: tag\nTarget: tag v1.41.0 in omriariav/workspace-cli"
	seedVerifyActionMessage(t, filepath.Join(base, "issue-349", "agents", "user"), "new",
		"q1", "cto", []string{"user"}, "gate/tag-v1-41-0", "APPROVAL: tag",
		string(state.KindQuestion), body, verifyActionNow)
	seedVerifyActionMessage(t, filepath.Join(base, "issue-349", "agents", "cto"), "cur",
		"a1", "cto", []string{"cto"}, "gate/tag-v1-41-0", "APPROVED: tag",
		string(state.KindAnswer), body, verifyActionNow.Add(time.Minute))

	_, err := runVerifyActionExec(t, base, project, verifyActionExecution{
		Session: "issue-349",
		Gate:    "tag-v1-41-0",
		Action:  "tag",
		Target:  "tag v1.41.0 in omriariav/workspace-cli",
	})
	if err == nil {
		t.Fatal("answer from non-operator should not pass")
	}
	if code := ExitCode(err); code != ExitActionPending {
		t.Fatalf("ExitCode = %d, want %d (%v)", code, ExitActionPending, err)
	}
}

func TestVerifyActionRejectsApprovalBeforeLatestGateQuestion(t *testing.T) {
	base, project := seedVerifyActionFixture(t)
	body := "Action: tag\nTarget: tag v1.41.0 in omriariav/workspace-cli"
	seedVerifyActionMessage(t, filepath.Join(base, "issue-349", "agents", "cto"), "cur",
		"a1", "user", []string{"cto"}, "gate/tag-v1-41-0", "APPROVED: tag",
		string(state.KindAnswer), body, verifyActionNow)
	seedVerifyActionMessage(t, filepath.Join(base, "issue-349", "agents", "user"), "new",
		"q1", "cto", []string{"user"}, "gate/tag-v1-41-0", "APPROVAL: tag",
		string(state.KindQuestion), body, verifyActionNow.Add(time.Minute))

	_, err := runVerifyActionExec(t, base, project, verifyActionExecution{
		Session: "issue-349",
		Gate:    "tag-v1-41-0",
		Action:  "tag",
		Target:  "tag v1.41.0 in omriariav/workspace-cli",
	})
	if err == nil {
		t.Fatal("approval before the latest matching gate question should not pass")
	}
	if code := ExitCode(err); code != ExitActionPending {
		t.Fatalf("ExitCode = %d, want %d (%v)", code, ExitActionPending, err)
	}
}

func TestVerifyActionDeniedExitCode(t *testing.T) {
	base, project := seedVerifyActionFixture(t)
	body := "Action: external_send\nTarget: email release announcement to customers"
	seedVerifyActionMessage(t, filepath.Join(base, "issue-349", "agents", "user"), "new",
		"q1", "cto", []string{"user"}, "gate/send-release-email", "APPROVAL: external_send",
		string(state.KindQuestion), body, verifyActionNow)
	seedVerifyActionMessage(t, filepath.Join(base, "issue-349", "agents", "cto"), "cur",
		"a1", "user", []string{"cto"}, "gate/send-release-email", "DENIED: external_send",
		string(state.KindAnswer), body, verifyActionNow.Add(time.Minute))

	_, err := runVerifyActionExec(t, base, project, verifyActionExecution{
		Session: "issue-349",
		Gate:    "send-release-email",
		Action:  "external_send",
		Target:  "email release announcement to customers",
	})
	if err == nil {
		t.Fatal("denied gate should fail")
	}
	if code := ExitCode(err); code != ExitActionDenied {
		t.Fatalf("ExitCode = %d, want %d (%v)", code, ExitActionDenied, err)
	}
}

func seedVerifyActionFixture(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()
	project := t.TempDir()
	op := team.DefaultOperator()
	if err := team.Write(project, team.Team{
		Members:      []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-349"}},
		Operator:     &op,
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
	x.Profile = team.DefaultProfile
	x.BaseRoot = base
	x.Out = &out
	x.Probe = state.Probe{
		PIDAlive:     func(pid int) bool { return true },
		ProcessMatch: func(pid int, _ func(args string) bool) bool { return true },
		Now:          func() time.Time { return verifyActionNow },
	}
	x.Now = func() time.Time { return verifyActionNow }
	err := executeVerifyAction(x)
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
