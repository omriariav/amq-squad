package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// --- Unit tests for deriveNextAction ---

func TestNextReturnsGateAnswerActionWhenGateOpen(t *testing.T) {
	project, base, _ := seedNotifyProject(t, team.DefaultOperator())
	seedNotifyLaunch(t, project, base, "s", "cto")
	seedNotifyMessage(t, base, "s", team.DefaultOperatorHandle, "new", notifyMsg{
		ID:      "gate-1",
		From:    "cto",
		To:      team.DefaultOperatorHandle,
		Thread:  "gate/release",
		Subject: "APPROVAL: release v2.12.0",
		Kind:    string(state.KindQuestion),
		Created: notifyNow,
	})

	var out bytes.Buffer
	err := executeNext(nextExecution{
		ProjectDir: project,
		Profile:    team.DefaultProfile,
		Session:    "s",
		BaseRoot:   base,
		JSON:       true,
		Out:        &out,
		Probe:      probeForNext(),
		Now:        func() time.Time { return notifyNow },
	})
	if err != nil {
		t.Fatalf("executeNext: %v\nout:\n%s", err, out.String())
	}
	env := decodeJSONEnvelope[nextActionData](t, out.String())
	if env.Kind != "next" {
		t.Fatalf("kind = %q, want next", env.Kind)
	}
	a := env.Data
	if a.ID != "gate_answer" || a.ActionKind != "gate_answer" {
		t.Fatalf("id/action_kind = %q/%q, want gate_answer/gate_answer", a.ID, a.ActionKind)
	}
	if !a.Available {
		t.Errorf("available = false, want true for open gate")
	}
	if !strings.Contains(a.Command, "operator answer") {
		t.Errorf("command %q should reference 'operator answer'", a.Command)
	}
	if !strings.Contains(a.Command, "release") {
		t.Errorf("command %q should reference gate topic 'release'", a.Command)
	}
	if !strings.Contains(a.Command, "cto") {
		t.Errorf("command %q should reference gate sender 'cto'", a.Command)
	}
	if a.GateTopic != "gate/release" {
		t.Errorf("gate_topic = %q, want gate/release", a.GateTopic)
	}
	if a.Label == "" {
		t.Error("label must be non-empty")
	}
}

func TestNextReturnsGateAnswerWhenStatusFollowsGateQuestion(t *testing.T) {
	project, base, _ := seedNotifyProject(t, team.DefaultOperator())
	seedNotifyLaunch(t, project, base, "s", "cto")
	seedNotifyMessage(t, base, "s", team.DefaultOperatorHandle, "cur", notifyMsg{
		ID:      "2026-06-28T22-00-01.000Z_pid1_gate",
		From:    "cto",
		To:      team.DefaultOperatorHandle,
		Thread:  "gate/release",
		Subject: "APPROVAL: release v2.12.0",
		Kind:    string(state.KindQuestion),
		Created: notifyNow,
	})
	seedNotifyMessage(t, base, "s", team.DefaultOperatorHandle, "new", notifyMsg{
		ID:      "2026-06-28T22-00-02.000Z_pid1_status",
		From:    "cto",
		To:      team.DefaultOperatorHandle,
		Thread:  "gate/release",
		Subject: "STATUS: release notes draft added",
		Kind:    string(state.KindStatus),
		Created: notifyNow.Add(time.Minute),
	})

	var out bytes.Buffer
	err := executeNext(nextExecution{
		ProjectDir: project,
		Profile:    team.DefaultProfile,
		Session:    "s",
		BaseRoot:   base,
		JSON:       true,
		Out:        &out,
		Probe:      probeForNext(),
		Now:        func() time.Time { return notifyNow.Add(2 * time.Minute) },
	})
	if err != nil {
		t.Fatalf("executeNext: %v\nout:\n%s", err, out.String())
	}
	env := decodeJSONEnvelope[nextActionData](t, out.String())
	if env.Data.ID != "gate_answer" || env.Data.GateTopic != "gate/release" {
		t.Fatalf("next action = %+v, want gate_answer for still-open gate", env.Data)
	}
}

func TestNextReturnsIdleWhenNoPendingAction(t *testing.T) {
	project, base, _ := seedNotifyProject(t, team.DefaultOperator())
	seedNotifyLaunch(t, project, base, "s", "cto")
	// No messages seeded — inbox is empty.

	var out bytes.Buffer
	err := executeNext(nextExecution{
		ProjectDir: project,
		Profile:    team.DefaultProfile,
		Session:    "s",
		BaseRoot:   base,
		JSON:       true,
		Out:        &out,
		Probe:      probeForNext(),
		Now:        func() time.Time { return notifyNow },
	})
	if err == nil {
		t.Fatal("executeNext: want non-nil error for idle, got nil")
	}
	var ue UsageError
	if !errors.As(err, &ue) {
		t.Fatalf("error type = %T, want UsageError for idle (exit 1)", err)
	}
	if !strings.Contains(err.Error(), "idle") {
		t.Errorf("idle error = %q, want 'idle' in message", err.Error())
	}
	// JSON envelope should still have been written with id=idle.
	if strings.TrimSpace(out.String()) != "" {
		env := decodeJSONEnvelope[nextActionData](t, out.String())
		if env.Data.ID != "idle" {
			t.Errorf("idle envelope id = %q, want idle", env.Data.ID)
		}
		if env.Data.Available {
			t.Error("idle envelope available = true, want false")
		}
	}
}

func TestNextJSONConformsToCanonicalActionShape(t *testing.T) {
	project, base, _ := seedNotifyProject(t, team.DefaultOperator())
	seedNotifyLaunch(t, project, base, "s", "cto")
	seedNotifyMessage(t, base, "s", team.DefaultOperatorHandle, "new", notifyMsg{
		ID:      "gate-2",
		From:    "cto",
		To:      team.DefaultOperatorHandle,
		Thread:  "gate/spawn",
		Subject: "APPROVAL: spawn dev",
		Kind:    string(state.KindQuestion),
		Created: notifyNow,
	})

	var out bytes.Buffer
	if err := executeNext(nextExecution{
		ProjectDir: project,
		Profile:    team.DefaultProfile,
		Session:    "s",
		BaseRoot:   base,
		JSON:       true,
		Out:        &out,
		Probe:      probeForNext(),
		Now:        func() time.Time { return notifyNow },
	}); err != nil {
		t.Fatalf("executeNext: %v", err)
	}
	env := decodeJSONEnvelope[nextActionData](t, out.String())
	a := env.Data
	// All canonical fields must be present and non-empty for an actionable result.
	if a.ID == "" {
		t.Error("id must be non-empty")
	}
	if a.Kind == "" {
		t.Error("kind must be non-empty")
	}
	if a.Label == "" {
		t.Error("label must be non-empty")
	}
	if a.ActionKind == "" {
		t.Error("action_kind must be non-empty")
	}
	if a.Command == "" {
		t.Error("command must be non-empty for gate_answer action")
	}
	if !a.Available {
		t.Error("available must be true for an actionable result")
	}
	if a.Session != "s" {
		t.Errorf("session = %q, want s", a.Session)
	}
	if a.Namespace.ID == "" {
		t.Error("namespace.id must be non-empty")
	}
}

func TestNextIdleJSONConformsToCanonicalActionShape(t *testing.T) {
	project, base, _ := seedNotifyProject(t, team.DefaultOperator())
	seedNotifyLaunch(t, project, base, "s", "cto")

	var out bytes.Buffer
	_ = executeNext(nextExecution{
		ProjectDir: project,
		Profile:    team.DefaultProfile,
		Session:    "s",
		BaseRoot:   base,
		JSON:       true,
		Out:        &out,
		Probe:      probeForNext(),
		Now:        func() time.Time { return notifyNow },
	})
	if strings.TrimSpace(out.String()) == "" {
		t.Fatal("idle JSON mode must write an envelope even when idle")
	}
	env := decodeJSONEnvelope[nextActionData](t, out.String())
	a := env.Data
	if a.ID != "idle" {
		t.Errorf("idle id = %q, want idle", a.ID)
	}
	if a.Kind != "idle" {
		t.Errorf("idle kind = %q, want idle", a.Kind)
	}
	if a.Label == "" {
		t.Error("idle label must be non-empty")
	}
	if a.Available {
		t.Error("idle available must be false")
	}
	if a.UnavailableReason == "" {
		t.Error("idle unavailable_reason must be set")
	}
	if a.UnavailableReason != a.Reason {
		t.Errorf("unavailable_reason=%q reason=%q must mirror each other", a.UnavailableReason, a.Reason)
	}
}

func TestNextTextModeIdlePrintsConciseMessage(t *testing.T) {
	project, base, _ := seedNotifyProject(t, team.DefaultOperator())
	seedNotifyLaunch(t, project, base, "s", "cto")

	var out bytes.Buffer
	err := executeNext(nextExecution{
		ProjectDir: project,
		Profile:    team.DefaultProfile,
		Session:    "s",
		BaseRoot:   base,
		JSON:       false,
		Out:        &out,
		Probe:      probeForNext(),
		Now:        func() time.Time { return notifyNow },
	})
	if err == nil {
		t.Fatal("executeNext text idle: want non-nil error for exit 1")
	}
	if got := strings.TrimSpace(out.String()); got != "no action pending for default/s" {
		t.Fatalf("text idle output = %q, want concise idle line", got)
	}
}

func TestNextTextModeForGatePrintsOneLine(t *testing.T) {
	project, base, _ := seedNotifyProject(t, team.DefaultOperator())
	seedNotifyLaunch(t, project, base, "s", "cto")
	seedNotifyMessage(t, base, "s", team.DefaultOperatorHandle, "new", notifyMsg{
		ID:      "gate-3",
		From:    "cto",
		To:      team.DefaultOperatorHandle,
		Thread:  "gate/deploy",
		Subject: "APPROVAL: deploy",
		Kind:    string(state.KindQuestion),
		Created: notifyNow,
	})

	var out bytes.Buffer
	if err := executeNext(nextExecution{
		ProjectDir: project,
		Profile:    team.DefaultProfile,
		Session:    "s",
		BaseRoot:   base,
		JSON:       false,
		Out:        &out,
		Probe:      probeForNext(),
		Now:        func() time.Time { return notifyNow },
	}); err != nil {
		t.Fatalf("executeNext text mode: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 {
		t.Errorf("text output has %d lines, want exactly 1: %q", len(lines), out.String())
	}
	if !strings.Contains(lines[0], "operator answer") {
		t.Errorf("text output %q should reference 'operator answer'", lines[0])
	}
}

// --- Integration tests via runNext ---

func TestRunNextHelpIncludesNextSubcommand(t *testing.T) {
	_, stderr, err := captureOutput(t, func() error {
		return runNext([]string{"--help"})
	})
	_ = err // flag.ErrHelp swallowed upstream
	if !strings.Contains(stderr, "amq-squad next") {
		t.Errorf("--help stderr %q missing 'amq-squad next'", stderr)
	}
}

func TestNextAppearsInTopLevelHelp(t *testing.T) {
	_, _, _ = captureOutput(t, func() error {
		printUsage()
		return nil
	})
	// Verify commandCatalog entry exists.
	found := false
	for _, cmd := range commandCatalog {
		if cmd.Name == "next" {
			found = true
			break
		}
	}
	if !found {
		t.Error("'next' missing from commandCatalog")
	}
}

func TestNextAppearsInCompletionOutput(t *testing.T) {
	stdout, _, err := captureOutput(t, func() error {
		return runCompletion([]string{"bash"})
	})
	if err != nil {
		t.Fatalf("completion bash: %v", err)
	}
	if !strings.Contains(stdout, "next") {
		t.Errorf("bash completion output missing 'next': %q", stdout[:min(len(stdout), 200)])
	}
}

// --- Helper ---

func probeForNext() state.Probe {
	return state.Probe{
		PIDAlive:     func(pid int) bool { return true },
		ProcessMatch: func(pid int, _ func(args string) bool) bool { return true },
		Now:          func() time.Time { return notifyNow },
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
