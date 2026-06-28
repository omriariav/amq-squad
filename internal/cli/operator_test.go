package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestOperatorStatusJSONReportsPollContractAndAttention(t *testing.T) {
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
	if data.Operator.Handle != team.DefaultOperatorHandle || data.Operator.CanonicalInbox == nil {
		t.Fatalf("operator = %+v, want canonical user inbox", data.Operator)
	}
	if data.Operator.CanonicalInbox.Session != "s" || data.Operator.CanonicalInbox.Root == "" {
		t.Fatalf("canonical inbox = %+v, want session s with root", data.Operator.CanonicalInbox)
	}
	if data.OperatorLoop.Mode != "poll" || data.OperatorLoop.State != "poll_required_unowned" || data.OperatorLoop.Owner != "none" {
		t.Fatalf("operator loop = %+v, want unowned poll loop", data.OperatorLoop)
	}
	if data.OperatorLoop.Backlog != 2 || data.OperatorLoop.GatesOpen != 1 || data.OperatorLoop.DirectivesUnacked != 1 {
		t.Fatalf("operator loop counts = %+v, want backlog=2, gates_open=1, directives_unacked=1", data.OperatorLoop)
	}
	if data.Operator.Poll == nil || data.Operator.Poll.Unread != 2 || data.Operator.Poll.OpenGates != 1 || data.Operator.Poll.OpenBlockers != 0 {
		t.Fatalf("operator poll = %+v, want unread/open gate counts without directive duplicate", data.Operator.Poll)
	}
	if len(data.Attention) != 1 || data.Attention[0].Thread != "gate/release" {
		t.Fatalf("attention = %+v, want gate/release", data.Attention)
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
	if env.Data.OperatorLoop.Mode != "disabled" || env.Data.OperatorLoop.State != "unconfigured" {
		t.Fatalf("operator loop = %+v, want disabled/unconfigured", env.Data.OperatorLoop)
	}
	if !strings.Contains(env.Data.Message, "disabled") {
		t.Fatalf("message = %q, want disabled guidance", env.Data.Message)
	}
}
