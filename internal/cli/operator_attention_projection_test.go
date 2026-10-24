package cli

import (
	"testing"
	"time"

	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/state"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// 4-3 deleted the v2 `operator status` verb (replaced by `operator inbox`
// over the fleet). These tests targeted operator_attention.go's projection
// through that verb; they now call the projection directly. The projection
// stays until 3-2 retires or re-targets operator_attention.go.

// projectAttentionForTest runs collectProjectedOperatorAttention for one
// session of a team profile, as the deleted verb did, and keeps the items
// that are not cleared.
func projectAttentionForTest(t *testing.T, project, profile, session, base string, probe state.Probe, now time.Time) []operatorAttention {
	t.Helper()
	cfg, err := team.ReadFor(project, profile)
	if err != nil {
		t.Fatal(err)
	}
	handle := team.EffectiveOperator(cfg).Handle
	if handle == "" {
		handle = team.DefaultOperatorHandle
	}
	snap, err := state.BuildWithThresholds(project, base, probe, state.Thresholds{OperatorHandle: handle})
	if err != nil {
		t.Fatal(err)
	}
	projected, err := collectProjectedOperatorAttention(cfg, project, profile, snap, handle, session, now)
	if err != nil {
		t.Fatal(err)
	}
	var active []operatorAttention
	for _, item := range projected.Items {
		if !item.Cleared {
			active = append(active, item)
		}
	}
	return active
}

// openGatesForTest counts the actionable gate items of a projection.
func openGatesForTest(items []operatorAttention) int {
	n := 0
	for _, item := range items {
		if item.Actionable && !item.Cleared && (item.EventType == "gate" || item.EventType == "compound_release_child") {
			n++
		}
	}
	return n
}

func TestOperatorAttentionReportsAgedGateEscalation(t *testing.T) {
	project, base, _ := seedNotifyProject(t)
	seedNotifyLaunch(t, project, base, "s", "cto")
	seedNotifyMessage(t, base, "s", team.DefaultOperatorHandle, "new", notifyMsg{
		ID: "gate-1", From: "cto", To: team.DefaultOperatorHandle, Thread: "gate/release",
		Subject: "APPROVAL: release", Kind: string(state.KindQuestion), Created: notifyNow.Add(-130 * time.Minute),
	})
	items := projectAttentionForTest(t, project, squadnamespace.DefaultProfile, "s", base, probeForNext(), notifyNow)
	if len(items) != 1 {
		t.Fatalf("attention = %+v, want one aged gate", items)
	}
	if got := items[0]; got.Escalation != string(state.OperatorGateEscalationStrongWarning) || got.Age != "2h10m0s" {
		t.Fatalf("aged gate attention = %+v, want strong-warning age 2h10m0s", got)
	}
}

// The gate is attention once, with the initial escalation; an FYI status and
// the operator's own directive are not gate attention.
func TestOperatorAttentionReportsOneGateWithoutDirectiveDuplicate(t *testing.T) {
	project, base, _ := seedNotifyProject(t)
	seedNotifyLaunch(t, project, base, "s", "cto")
	for _, m := range []struct {
		owner string
		msg   notifyMsg
	}{
		{team.DefaultOperatorHandle, notifyMsg{ID: "gate-1", From: "cto", To: team.DefaultOperatorHandle, Thread: "gate/release", Subject: "APPROVAL: release", Kind: string(state.KindQuestion), Created: notifyNow}},
		{team.DefaultOperatorHandle, notifyMsg{ID: "fyi-1", From: "cto", To: team.DefaultOperatorHandle, Thread: "note/operator", Subject: "FYI: note", Kind: string(state.KindStatus), Created: notifyNow}},
		{"cto", notifyMsg{ID: "directive-1", From: team.DefaultOperatorHandle, To: "cto", Thread: "p2p/cto__user", Subject: "DIRECTIVE: ship it", Kind: string(state.KindTodo), Created: notifyNow}},
	} {
		seedNotifyMessage(t, base, "s", m.owner, "new", m.msg)
	}
	items := projectAttentionForTest(t, project, squadnamespace.DefaultProfile, "s", base, probeForNext(), notifyNow)
	if len(items) != 1 || items[0].Thread != "gate/release" || openGatesForTest(items) != 1 {
		t.Fatalf("attention = %+v, want gate/release once", items)
	}
	if items[0].Escalation != string(state.OperatorGateEscalationInitial) {
		t.Fatalf("attention escalation = %q, want initial", items[0].Escalation)
	}
}

func seedGateWithLaterStatus(t *testing.T) (string, string) {
	t.Helper()
	project, base, _ := seedNotifyProject(t)
	seedNotifyLaunch(t, project, base, "s", "cto")
	seedNotifyMessage(t, base, "s", team.DefaultOperatorHandle, "cur", notifyMsg{
		ID: "2026-06-28T22-00-01.000Z_pid1_gate", From: "cto", To: team.DefaultOperatorHandle, Thread: "gate/release",
		Subject: "APPROVAL: release", Kind: string(state.KindQuestion), Created: notifyNow,
	})
	seedNotifyMessage(t, base, "s", team.DefaultOperatorHandle, "new", notifyMsg{
		ID: "2026-06-28T22-00-02.000Z_pid1_status", From: "cto", To: team.DefaultOperatorHandle, Thread: "gate/release",
		Subject: "STATUS: validation evidence added", Kind: string(state.KindStatus), Created: notifyNow.Add(time.Minute),
	})
	return project, base
}

func TestOperatorAttentionKeepsGateOpenAfterStatusUpdate(t *testing.T) {
	project, base := seedGateWithLaterStatus(t)
	items := projectAttentionForTest(t, project, squadnamespace.DefaultProfile, "s", base, probeForNext(), notifyNow.Add(2*time.Minute))
	if openGatesForTest(items) != 1 || len(items) != 1 || items[0].Thread != "gate/release" || items[0].LatestID != "2026-06-28T22-00-01.000Z_pid1_gate" {
		t.Fatalf("attention = %+v, want the pending gate question despite the later status", items)
	}
}

func TestOperatorAttentionClosesGateAfterOperatorAnswer(t *testing.T) {
	project, base := seedGateWithLaterStatus(t)
	seedNotifyMessage(t, base, "s", "cto", "cur", notifyMsg{
		ID: "2026-06-28T22-00-03.000Z_pid1_answer", From: team.DefaultOperatorHandle, To: "cto", Thread: "gate/release",
		Subject: "APPROVED: release", Kind: string(state.KindAnswer), Created: notifyNow.Add(2 * time.Minute),
	})
	items := projectAttentionForTest(t, project, squadnamespace.DefaultProfile, "s", base, probeForNext(), notifyNow.Add(3*time.Minute))
	if openGatesForTest(items) != 0 || len(items) != 0 {
		t.Fatalf("attention after answer = %+v, want the terminal gate suppressed", items)
	}
}
