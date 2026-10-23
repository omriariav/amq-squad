package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/state"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

func TestOperatorStatusReportsAgedGateEscalation(t *testing.T) {
	project, base, _ := seedNotifyProject(t, team.DefaultOperator())
	seedNotifyLaunch(t, project, base, "s", "cto")
	seedNotifyMessage(t, base, "s", team.DefaultOperatorHandle, "new", notifyMsg{
		ID:      "gate-1",
		From:    "cto",
		To:      team.DefaultOperatorHandle,
		Thread:  "gate/release",
		Subject: "APPROVAL: release",
		Kind:    string(state.KindQuestion),
		Created: notifyNow.Add(-130 * time.Minute),
	})

	var out bytes.Buffer
	err := executeOperatorStatus(operatorExecution{
		ProjectDir: project,
		Profile:    team.DefaultProfile,
		Session:    "s",
		BaseRoot:   base,
		JSON:       true,
		Out:        &out,
		Probe: state.Probe{
			PIDAlive:     func(pid int) bool { return true },
			ProcessMatch: func(pid int, _ func(args string) bool) bool { return true },
			Now:          func() time.Time { return notifyNow },
		},
		Now: func() time.Time { return notifyNow },
	})
	if err != nil {
		t.Fatalf("operator status: %v", err)
	}
	env := decodeJSONEnvelope[operatorStatusEnvelopeData](t, out.String())
	if len(env.Data.Attention) != 1 {
		t.Fatalf("attention = %+v, want one aged gate", env.Data.Attention)
	}
	got := env.Data.Attention[0]
	if got.Escalation != string(state.OperatorGateEscalationStrongWarning) || got.Age != "2h10m0s" {
		t.Fatalf("aged gate attention = %+v, want strong-warning age 2h10m0s", got)
	}
}

func TestOperatorAnswerSendsApprovedGateAnswer(t *testing.T) {
	project, _, _ := seedNotifyProject(t, team.DefaultOperator())
	seedLegacyOperatorQuestion(t, project, team.DefaultProfile, "s", "cto", "gate/release")
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-answer to cto\n")

	stdout, stderr, err := captureOutput(t, func() error {
		return runOperator([]string{
			"answer",
			"--project", project,
			"--session", "s",
			"--gate", "release",
			"--to", "cto",
			"--approved",
			"--reason", "checks passed",
			"--json",
		})
	})
	if err != nil {
		t.Fatalf("operator answer: %v\nstderr:\n%s", err, stderr)
	}
	if len(*calls) != 1 {
		t.Fatalf("amq calls = %d, want 1", len(*calls))
	}
	args := (*calls)[0].Arg
	for _, want := range []string{"send", "--me", "user", "--to", "cto", "--thread", "gate/release", "--kind", "answer", "--subject", "APPROVED: release", "--body", "checks passed"} {
		if !argListContains(args, want) {
			t.Fatalf("operator answer args missing %q: %v", want, args)
		}
	}
	env := decodeJSONEnvelope[mutationResult](t, stdout)
	if env.Kind != "operator_send" || env.Data.MessageID != "msg-answer" || env.Data.Thread != "gate/release" {
		t.Fatalf("operator answer json = %+v", env)
	}
}

func TestOperatorAnswerNamedProfileUsesExactExplicitSessionRoot(t *testing.T) {
	project := t.TempDir()
	profile, session := "release", "session-s"
	if err := team.WriteProfile(project, profile, team.Team{
		Project: project,
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: session}},
	}); err != nil {
		t.Fatal(err)
	}
	seedLegacyOperatorQuestion(t, project, profile, session, "cto", "gate/release")
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent named-answer to cto\n")

	_, stderr, err := captureOutput(t, func() error {
		return runOperator([]string{
			"answer", "--project", project, "--profile", profile, "--session", session,
			"--gate", "release", "--to", "cto", "--approved", "--reason", "verified",
		})
	})
	if err != nil {
		t.Fatalf("operator answer: %v\nstderr:\n%s", err, stderr)
	}
	if len(*calls) != 1 {
		t.Fatalf("amq calls = %d, want 1", len(*calls))
	}
	wantRoot := filepath.Join(project, ".agent-mail", profile, session)
	call := (*calls)[0]
	for _, want := range []string{"send", "--root", wantRoot, "--me", "user", "--to", "cto", "--thread", "gate/release", "--kind", "answer", "--subject", "APPROVED: release", "--body", "verified"} {
		if !argListContains(call.Arg, want) {
			t.Fatalf("operator answer args missing %q: %v", want, call.Arg)
		}
	}
	for key, want := range map[string]string{"AM_ROOT": wantRoot, "AM_BASE_ROOT": wantRoot, "AM_ME": team.DefaultOperatorHandle} {
		if !envHas(call.Env, key, want) {
			t.Fatalf("%s=%q missing from exact-root env: %v", key, want, call.Env)
		}
	}
	if envHasPrefix(call.Env, "AM_SESSION", "") {
		t.Fatalf("named-profile operator answer must omit AM_SESSION: %v", call.Env)
	}
}

func TestOperatorAnswerSendsDeniedGateAnswerWithGatePrefix(t *testing.T) {
	project, _, _ := seedNotifyProject(t, team.DefaultOperator())
	seedLegacyOperatorQuestion(t, project, team.DefaultProfile, "s", "cto", "gate/release")
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-denied to cto\n")

	_, stderr, err := captureOutput(t, func() error {
		return runOperator([]string{
			"answer",
			"--project", project,
			"--session", "s",
			"--gate", "gate/release",
			"--to", "cto",
			"--denied",
		})
	})
	if err != nil {
		t.Fatalf("operator denied answer: %v\nstderr:\n%s", err, stderr)
	}
	args := (*calls)[0].Arg
	for _, want := range []string{"--thread", "gate/release", "--subject", "DENIED: release", "--body", ""} {
		if !argListContains(args, want) {
			t.Fatalf("operator denied args missing %q: %v", want, args)
		}
	}
}

func seedLegacyOperatorQuestion(t *testing.T, project, profile, session, from, gate string) {
	t.Helper()
	base := filepath.Join(project, ".agent-mail")
	if profile != team.DefaultProfile {
		base = filepath.Join(base, profile)
	}
	root := filepath.Join(base, session)
	seedAgentRecord(t, base, session, from, launch.Record{CWD: project, Binary: "codex", Role: from, Handle: from, Session: session, Root: root, TeamProfile: profile, AgentPID: 42})
	seedNotifyMessageToDir(t, filepath.Join(root, "agents", team.DefaultOperatorHandle), "new", notifyMsg{ID: "legacy-question", From: from, To: team.DefaultOperatorHandle, Thread: gate, Subject: "APPROVAL: legacy", Kind: string(state.KindQuestion), Created: notifyNow})
}

func TestOperatorCommandsRefuseOperatorTarget(t *testing.T) {
	project, _, _ := seedNotifyProject(t, team.DefaultOperator())
	seedLegacyOperatorQuestion(t, project, team.DefaultProfile, "s", "cto", "gate/release")
	_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg to user\n")

	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "answer", args: []string{"answer", "--project", project, "--session", "s", "--gate", "release", "--to", "user", "--approved"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := captureOutput(t, func() error { return runOperator(tc.args) })
			assertOperatorMailboxOnlyError(t, err)
		})
	}
}

func argListContains(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func TestOperatorStatusJSONReportsAttentionAndUnreadCounts(t *testing.T) {
	project, base, _ := seedNotifyProject(t, team.DefaultOperator())
	seedNotifyLaunch(t, project, base, "s", "cto")
	seedNotifyMessage(t, base, "s", team.DefaultOperatorHandle, "new", notifyMsg{
		ID:      "gate-1",
		From:    "cto",
		To:      team.DefaultOperatorHandle,
		Thread:  "gate/release",
		Subject: "APPROVAL: release",
		Kind:    string(state.KindQuestion),
		Created: notifyNow,
	})
	seedNotifyMessage(t, base, "s", team.DefaultOperatorHandle, "new", notifyMsg{
		ID:      "fyi-1",
		From:    "cto",
		To:      team.DefaultOperatorHandle,
		Thread:  "note/operator",
		Subject: "FYI: note",
		Kind:    string(state.KindStatus),
		Created: notifyNow,
	})
	seedNotifyMessage(t, base, "s", "cto", "new", notifyMsg{
		ID:      "directive-1",
		From:    team.DefaultOperatorHandle,
		To:      "cto",
		Thread:  "p2p/cto__user",
		Subject: "DIRECTIVE: ship it",
		Kind:    string(state.KindTodo),
		Created: notifyNow,
	})

	var out bytes.Buffer
	err := executeOperatorStatus(operatorExecution{
		ProjectDir: project,
		Profile:    team.DefaultProfile,
		Session:    "s",
		BaseRoot:   base,
		JSON:       true,
		Out:        &out,
		Probe: state.Probe{
			PIDAlive:     func(pid int) bool { return true },
			ProcessMatch: func(pid int, _ func(args string) bool) bool { return true },
			Now:          func() time.Time { return notifyNow },
		},
		Now: func() time.Time { return notifyNow },
	})
	if err != nil {
		t.Fatalf("operator status: %v", err)
	}
	env := decodeJSONEnvelope[operatorStatusEnvelopeData](t, out.String())
	if env.Kind != "operator_status" {
		t.Fatalf("kind = %q, want operator_status", env.Kind)
	}
	data := env.Data
	if !data.ReadOnly {
		t.Fatalf("readonly = false, want true for operator status")
	}
	if data.Operator.Handle != team.DefaultOperatorHandle || data.Operator.CanonicalInbox == nil {
		t.Fatalf("operator = %+v, want canonical user inbox", data.Operator)
	}
	if data.Operator.CanonicalInbox.Session != "s" || data.Operator.CanonicalInbox.Root == "" {
		t.Fatalf("canonical inbox = %+v, want session s with root", data.Operator.CanonicalInbox)
	}
	if data.Operator.Poll == nil || data.Operator.Poll.Owner != "operator_or_parent" || data.Operator.Poll.Unread != 2 || data.Operator.Poll.OpenGates == nil || *data.Operator.Poll.OpenGates != 1 {
		t.Fatalf("operator poll = %+v, want unread/open gate counts without directive duplicate", data.Operator.Poll)
	}
	if len(data.Attention) != 1 || data.Attention[0].Thread != "gate/release" {
		t.Fatalf("attention = %+v, want gate/release", data.Attention)
	}
	if data.Attention[0].Escalation != string(state.OperatorGateEscalationInitial) {
		t.Fatalf("attention escalation = %q, want initial", data.Attention[0].Escalation)
	}
}

func TestOperatorStatusDisabledProfileIsUnconfigured(t *testing.T) {
	project, _, _ := seedNotifyProject(t, team.DisabledOperator())

	var out bytes.Buffer
	err := executeOperatorStatus(operatorExecution{
		ProjectDir: project,
		Profile:    team.DefaultProfile,
		Session:    "s",
		BaseRoot:   "",
		JSON:       true,
		Out:        &out,
	})
	if err != nil {
		t.Fatalf("operator status disabled: %v", err)
	}
	env := decodeJSONEnvelope[operatorStatusEnvelopeData](t, out.String())
	if env.Data.OperatorGates {
		t.Fatalf("operator gates = true, want false")
	}
	if !strings.Contains(env.Data.Message, "disabled") {
		t.Fatalf("message = %q, want disabled guidance", env.Data.Message)
	}
}

func TestOperatorStatusKeepsGateOpenAfterStatusUpdate(t *testing.T) {
	project, base, _ := seedNotifyProject(t, team.DefaultOperator())
	seedNotifyLaunch(t, project, base, "s", "cto")
	seedNotifyMessage(t, base, "s", team.DefaultOperatorHandle, "cur", notifyMsg{
		ID:      "2026-06-28T22-00-01.000Z_pid1_gate",
		From:    "cto",
		To:      team.DefaultOperatorHandle,
		Thread:  "gate/release",
		Subject: "APPROVAL: release",
		Kind:    string(state.KindQuestion),
		Created: notifyNow,
	})
	seedNotifyMessage(t, base, "s", team.DefaultOperatorHandle, "new", notifyMsg{
		ID:      "2026-06-28T22-00-02.000Z_pid1_status",
		From:    "cto",
		To:      team.DefaultOperatorHandle,
		Thread:  "gate/release",
		Subject: "STATUS: validation evidence added",
		Kind:    string(state.KindStatus),
		Created: notifyNow.Add(time.Minute),
	})

	var out bytes.Buffer
	err := executeOperatorStatus(operatorExecution{
		ProjectDir: project,
		Profile:    team.DefaultProfile,
		Session:    "s",
		BaseRoot:   base,
		JSON:       true,
		Out:        &out,
		Probe: state.Probe{
			PIDAlive:     func(pid int) bool { return true },
			ProcessMatch: func(pid int, _ func(args string) bool) bool { return true },
			Now:          func() time.Time { return notifyNow },
		},
		Now: func() time.Time { return notifyNow.Add(2 * time.Minute) },
	})
	if err != nil {
		t.Fatalf("operator status: %v", err)
	}
	env := decodeJSONEnvelope[operatorStatusEnvelopeData](t, out.String())
	if operatorGatesOpenForTest(env.Data) != 1 || env.Data.Operator.Poll.OpenGates == nil || *env.Data.Operator.Poll.OpenGates != 1 {
		t.Fatalf("open gate counts = loop:%d poll:%v, want 1/1", operatorGatesOpenForTest(env.Data), env.Data.Operator.Poll.OpenGates)
	}
	if len(env.Data.Attention) != 1 || env.Data.Attention[0].Thread != "gate/release" || env.Data.Attention[0].LatestID != "2026-06-28T22-00-01.000Z_pid1_gate" {
		t.Fatalf("attention = %+v, want pending gate question despite later status", env.Data.Attention)
	}
}

func TestOperatorStatusClosesGateAfterOperatorAnswer(t *testing.T) {
	project, base, _ := seedNotifyProject(t, team.DefaultOperator())
	seedNotifyLaunch(t, project, base, "s", "cto")
	seedNotifyMessage(t, base, "s", team.DefaultOperatorHandle, "cur", notifyMsg{
		ID:      "2026-06-28T22-00-01.000Z_pid1_gate",
		From:    "cto",
		To:      team.DefaultOperatorHandle,
		Thread:  "gate/release",
		Subject: "APPROVAL: release",
		Kind:    string(state.KindQuestion),
		Created: notifyNow,
	})
	seedNotifyMessage(t, base, "s", team.DefaultOperatorHandle, "new", notifyMsg{
		ID:      "2026-06-28T22-00-02.000Z_pid1_status",
		From:    "cto",
		To:      team.DefaultOperatorHandle,
		Thread:  "gate/release",
		Subject: "STATUS: validation evidence added",
		Kind:    string(state.KindStatus),
		Created: notifyNow.Add(time.Minute),
	})
	seedNotifyMessage(t, base, "s", "cto", "cur", notifyMsg{
		ID:      "2026-06-28T22-00-03.000Z_pid1_answer",
		From:    team.DefaultOperatorHandle,
		To:      "cto",
		Thread:  "gate/release",
		Subject: "APPROVED: release",
		Kind:    string(state.KindAnswer),
		Created: notifyNow.Add(2 * time.Minute),
	})

	var out bytes.Buffer
	err := executeOperatorStatus(operatorExecution{
		ProjectDir: project,
		Profile:    team.DefaultProfile,
		Session:    "s",
		BaseRoot:   base,
		JSON:       true,
		Out:        &out,
		Probe: state.Probe{
			PIDAlive:     func(pid int) bool { return true },
			ProcessMatch: func(pid int, _ func(args string) bool) bool { return true },
			Now:          func() time.Time { return notifyNow },
		},
		Now: func() time.Time { return notifyNow.Add(3 * time.Minute) },
	})
	if err != nil {
		t.Fatalf("operator status: %v", err)
	}
	env := decodeJSONEnvelope[operatorStatusEnvelopeData](t, out.String())
	if operatorGatesOpenForTest(env.Data) != 0 || operatorBacklogForTest(env.Data) != 0 || len(env.Data.Attention) != 0 {
		t.Fatalf("operator status after answer = gates:%d backlog:%d attention:%+v, want terminal gate suppressed", operatorGatesOpenForTest(env.Data), operatorBacklogForTest(env.Data), env.Data.Attention)
	}
	if env.Data.Operator.Poll == nil || env.Data.Operator.Poll.OpenGates == nil || *env.Data.Operator.Poll.OpenGates != 0 || !strings.Contains(out.String(), `"open_gates"`) {
		t.Fatalf("operator poll = %+v json=%s, want explicit open_gates zero", env.Data.Operator.Poll, out.String())
	}
}

// operatorGatesOpenForTest and operatorBacklogForTest read the counts that the
// v2 operator_loop block carried; 3.0 (2a-2) keeps them on operator.poll only.
func operatorGatesOpenForTest(data operatorStatusEnvelopeData) int {
	if data.Operator.Poll == nil || data.Operator.Poll.OpenGates == nil {
		return 0
	}
	return *data.Operator.Poll.OpenGates
}

func operatorBacklogForTest(data operatorStatusEnvelopeData) int {
	if data.Operator.Poll == nil {
		return 0
	}
	return data.Operator.Poll.Unread
}

func TestOperatorAnswerExplicitSessionIgnoresLegacyCrossSessionResidue(t *testing.T) {
	project := t.TempDir()
	profile, targetSession, staleSession := "release", "session-s", "session-t"
	cfg := team.Team{
		Project: project, Orchestrated: true, Lead: "orchestrator", Workstream: staleSession,
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: staleSession},
			{Role: "orchestrator", Binary: "codex", Handle: "orchestrator", Session: staleSession},
		},
	}
	if err := team.WriteProfile(project, profile, cfg); err != nil {
		t.Fatal(err)
	}
	seedLegacyOperatorQuestion(t, project, profile, targetSession, "cto", "gate/release")
	profilePath := team.ProfilePath(project, profile)
	before, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	staleBase := filepath.Join(project, ".agent-mail", profile)
	staleRoot := filepath.Join(staleBase, staleSession)
	seedAgentRecord(t, staleBase, staleSession, "cto", launch.Record{
		CWD: project, Binary: "codex", Role: "cto", Handle: "cto", Session: staleSession,
		Root: staleRoot, TeamProfile: profile, External: true,
	})
	seedAgentRecord(t, staleBase, staleSession, "orchestrator", launch.Record{
		CWD: project, Binary: "codex", Role: "orchestrator", Handle: "orchestrator", Session: staleSession,
		Root: staleRoot, TeamProfile: profile, External: true,
	})
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent explicit-answer to cto\n")

	_, stderr, err := captureOutput(t, func() error {
		return runOperator([]string{
			"answer", "--project", project, "--profile", profile, "--session", targetSession,
			"--gate", "release", "--to", "cto", "--denied", "--reason", "not ready",
		})
	})
	if err != nil {
		t.Fatalf("operator answer: %v\nstderr:\n%s", err, stderr)
	}
	if len(*calls) != 1 {
		t.Fatalf("amq calls = %d, want 1", len(*calls))
	}
	wantRoot := filepath.Join(project, ".agent-mail", profile, targetSession)
	call := (*calls)[0]
	if amqFlagValue(call.Arg, "to") != "cto" || !argListContains(call.Arg, wantRoot) || argListContains(call.Arg, staleRoot) {
		t.Fatalf("operator answer args = %v, want target cto at exact %s and no stale %s", call.Arg, wantRoot, staleRoot)
	}
	for key, want := range map[string]string{"AM_ROOT": wantRoot, "AM_BASE_ROOT": wantRoot, "AM_ME": team.DefaultOperatorHandle} {
		if !envHas(call.Env, key, want) {
			t.Fatalf("%s=%q missing from exact-root env: %v", key, want, call.Env)
		}
	}
	if envHasPrefix(call.Env, "AM_SESSION", "") {
		t.Fatalf("named-profile operator answer must omit AM_SESSION: %v", call.Env)
	}
	after, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("operator answer mutated team profile\nbefore:\n%s\nafter:\n%s", before, after)
	}
	persisted, err := team.ReadProfile(project, profile)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Lead != "orchestrator" || persisted.Workstream != staleSession {
		t.Fatalf("legacy authority changed: lead=%q workstream=%q, want orchestrator/%s", persisted.Lead, persisted.Workstream, staleSession)
	}
}
