package state

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
)

// TestClassifyAttnReason_FromOperatorAddressedSubject proves PR13c part C
// detection: a needs-you thread (a message addressed to the operator handle
// "user") is classified into a typed AttnReason from its subject markers —
// APPROVE (paused awaiting permission) wins over GOAL-REACHED (done/goal), and a
// plain question with neither marker is AttnGeneric. The classification runs
// through the SAME parser + collapse path real data uses (no internal poking).
func TestClassifyAttnReason_FromOperatorAddressedSubject(t *testing.T) {
	cases := []struct {
		name    string
		kind    string
		subject string
		want    AttnReason
	}{
		{"approve-word", "question", "OK to proceed with the migration?", AttnApprove},
		{"approval-permission", "review_request", "permission to run this command?", AttnApprove},
		{"yn-prompt", "question", "delete the branch? [y/n]", AttnApprove},
		{"confirm", "decision", "please confirm before I deploy", AttnApprove},
		{"done", "question", "shipped — ready to close?", AttnGoalReached},
		{"goal-reached", "review_request", "goal reached, review and close", AttnGoalReached},
		{"completed-check", "question", "migration completed ✅", AttnGoalReached},
		{"plain-question", "question", "which database should we target?", AttnGeneric},
		// APPROVE precedence: a subject carrying BOTH markers classifies APPROVE.
		{"approve-beats-done", "question", "approve the completed migration?", AttnApprove},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			proj := t.TempDir()
			userDir := seedAgent(t, base, "s", "user", launch.Record{Binary: "claude", Handle: "user", Session: "s", AgentPID: 9})
			_ = seedAgent(t, base, "s", "cto", launch.Record{Binary: "codex", Handle: "cto", Session: "s", AgentPID: 1})
			seedMessage(t, userDir, "new", msgSpec{
				id: "m1", from: "cto", to: []string{"user"}, thread: "decision/x",
				kind: tc.kind, subject: tc.subject, createdAt: coordNow.Add(-1 * time.Minute),
			})
			snap, err := Build(proj, base, coordProbe())
			if err != nil {
				t.Fatal(err)
			}
			th := findThread(t, snap.Sessions[0].Coordination, "decision/x")
			if th.Triage != TriageNeedsYou {
				t.Fatalf("expected NeedsYou triage for an operator-addressed ask, got %s", th.Triage)
			}
			if th.AttnReason != tc.want {
				t.Errorf("AttnReason = %q, want %q (subject %q)", th.AttnReason, tc.want, tc.subject)
			}
		})
	}
}

// TestAttnReason_NoneOnNonNeedsYouThread proves AttnReason is meaningful ONLY on
// a needs-you thread: an agent<->agent thread (no operator recipient) carries
// AttnNone even when its subject contains approve/done markers.
func TestAttnReason_NoneOnNonNeedsYouThread(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	ctoDir := seedAgent(t, base, "s", "cto", launch.Record{Binary: "codex", Handle: "cto", Session: "s", AgentPID: 1})
	_ = seedAgent(t, base, "s", "senior-dev", launch.Record{Binary: "codex", Handle: "senior-dev", Session: "s", AgentPID: 2})
	// senior-dev -> cto (NOT the operator), so this is not needs-you. The cto's
	// own inbox holds it unread, but its subject carries approve/done markers.
	seedMessage(t, ctoDir, "new", msgSpec{
		id: "a1", from: "senior-dev", to: []string{"cto"}, thread: "p2p/cto__senior-dev",
		kind: "question", subject: "approve the shipped build?", createdAt: coordNow.Add(-1 * time.Minute),
	})
	snap, err := Build(proj, base, coordProbe())
	if err != nil {
		t.Fatal(err)
	}
	th := findThread(t, snap.Sessions[0].Coordination, "p2p/cto__senior-dev")
	if th.Triage == TriageNeedsYou {
		t.Fatalf("agent<->agent thread should not be needs-you")
	}
	if th.AttnReason != AttnNone {
		t.Errorf("non-needs-you thread should carry AttnNone, got %q", th.AttnReason)
	}
}

// TestNeedsYouThreads_SortedByReasonThenAge proves the session-level NEEDS YOU
// ordering: APPROVE above GOAL-REACHED above generic; within a reason the oldest
// (longest-waiting) ask leads. needs-you never decays to stale (PR13b), so even
// an ancient operator-addressed ask still appears here.
func TestNeedsYouThreads_SortedByReasonThenAge(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	userDir := seedAgent(t, base, "s", "user", launch.Record{Binary: "claude", Handle: "user", Session: "s", AgentPID: 9})
	_ = seedAgent(t, base, "s", "cto", launch.Record{Binary: "codex", Handle: "cto", Session: "s", AgentPID: 1})

	// A generic ask (newest), a goal-reached ask, and two approve asks with
	// different ages — seeded out of priority order on purpose.
	seedMessage(t, userDir, "new", msgSpec{
		id: "g1", from: "cto", to: []string{"user"}, thread: "ask/generic",
		kind: "question", subject: "which region?", createdAt: coordNow.Add(-1 * time.Minute),
	})
	seedMessage(t, userDir, "new", msgSpec{
		id: "d1", from: "qa", to: []string{"user"}, thread: "ask/done",
		kind: "question", subject: "shipped, ready to close?", createdAt: coordNow.Add(-2 * time.Minute),
	})
	seedMessage(t, userDir, "new", msgSpec{
		id: "a1", from: "dev", to: []string{"user"}, thread: "ask/approve-new",
		kind: "question", subject: "ok to proceed?", createdAt: coordNow.Add(-3 * time.Minute),
	})
	seedMessage(t, userDir, "new", msgSpec{
		id: "a2", from: "dev", to: []string{"user"}, thread: "ask/approve-old",
		kind: "question", subject: "approve the rollout?", createdAt: coordNow.Add(-90 * time.Minute),
	})

	snap, err := Build(proj, base, coordProbe())
	if err != nil {
		t.Fatal(err)
	}
	got := snap.Sessions[0].Coordination.NeedsYouThreads()
	if len(got) != 4 {
		t.Fatalf("expected 4 needs-you threads, got %d", len(got))
	}
	wantOrder := []struct {
		id     string
		reason AttnReason
	}{
		{"ask/approve-old", AttnApprove}, // approve, oldest first
		{"ask/approve-new", AttnApprove},
		{"ask/done", AttnGoalReached}, // goal-reached below approve
		{"ask/generic", AttnGeneric},  // generic last
	}
	for i, w := range wantOrder {
		if got[i].ID != w.id {
			t.Errorf("needs-you[%d] id = %q, want %q", i, got[i].ID, w.id)
		}
		if got[i].AttnReason != w.reason {
			t.Errorf("needs-you[%d] reason = %q, want %q", i, got[i].AttnReason, w.reason)
		}
	}

	// TopAttnReason leads with the most urgent reason (APPROVE here).
	if top := snap.Sessions[0].Coordination.TopAttnReason(); top != AttnApprove {
		t.Errorf("TopAttnReason = %q, want %q", top, AttnApprove)
	}
}

func TestOperatorGateSignalUsesLastUnansweredOperatorMessage(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	userDir := filepath.Join(base, "s", "agents", "user")
	_ = seedAgent(t, base, "s", "cto", launch.Record{Binary: "codex", Handle: "cto", Session: "s", AgentPID: 1})

	seedMessage(t, userDir, "new", msgSpec{
		id: "old", from: "cto", to: []string{"user"}, thread: "gate/spawn-dev",
		kind: "question", subject: "APPROVAL: spawn dev?", createdAt: coordNow.Add(-3 * time.Hour),
	})
	seedMessage(t, userDir, "new", msgSpec{
		id: "new", from: "cto", to: []string{"user"}, thread: "gate/spawn-dev",
		kind: "question", subject: "APPROVAL: updated spawn dev?", createdAt: coordNow.Add(-20 * time.Minute),
	})

	snap, err := Build(proj, base, coordProbe())
	if err != nil {
		t.Fatal(err)
	}
	th := findThread(t, snap.Sessions[0].Coordination, "gate/spawn-dev")
	if th.Triage != TriageNeedsYou || th.OperatorGate == nil {
		t.Fatalf("gate triage/operator signal = %q/%+v, want needs-you signal", th.Triage, th.OperatorGate)
	}
	if th.OperatorGate.LatestID != "new" || th.OperatorGate.Age != 20*time.Minute {
		t.Fatalf("operator gate signal = %+v, want latest new age 20m", th.OperatorGate)
	}
	if th.OperatorGate.Escalation != OperatorGateEscalationInitial {
		t.Fatalf("gate escalation = %q, want initial from the updated gate age", th.OperatorGate.Escalation)
	}
}

func TestOperatorGateSignalClearsAnsweredGateAcrossMailboxOrder(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	userDir := filepath.Join(base, "s", "agents", "user")
	ctoDir := seedAgent(t, base, "s", "cto", launch.Record{Binary: "codex", Handle: "cto", Session: "s", AgentPID: 1})

	// The cto mailbox is scanned before the virtual operator mailbox, so this
	// answer is observed before the older question unless gate replay sorts by
	// message creation time.
	seedMessage(t, ctoDir, "new", msgSpec{
		id: "answer", from: "user", to: []string{"cto"}, thread: "gate/release",
		kind: "answer", subject: "APPROVED: release", createdAt: coordNow.Add(-55 * time.Minute),
	})
	seedMessage(t, userDir, "new", msgSpec{
		id: "question", from: "cto", to: []string{"user"}, thread: "gate/release",
		kind: "question", subject: "APPROVAL: release?", createdAt: coordNow.Add(-1 * time.Hour),
	})

	snap, err := Build(proj, base, coordProbe())
	if err != nil {
		t.Fatal(err)
	}
	th := findThread(t, snap.Sessions[0].Coordination, "gate/release")
	if th.OperatorGate != nil {
		t.Fatalf("operator gate signal = %+v, want nil for answered gate", th.OperatorGate)
	}
	if th.OperatorGateState != OperatorGateStateAnswered {
		t.Fatalf("operator gate state = %q, want answered", th.OperatorGateState)
	}
	if th.Triage == TriageNeedsYou {
		t.Fatalf("answered gate should not need operator attention")
	}
}

func TestOperatorGateTerminalCloseAndWithdrawSuppressAttention(t *testing.T) {
	for _, terminal := range []OperatorGateState{OperatorGateStateClosed, OperatorGateStateWithdrawn} {
		t.Run(string(terminal), func(t *testing.T) {
			base := t.TempDir()
			proj := t.TempDir()
			userDir := filepath.Join(base, "s", "agents", "user")
			ctoDir := seedAgent(t, base, "s", "cto", launch.Record{Binary: "codex", Handle: "cto", Session: "s", AgentPID: 1})

			seedMessage(t, userDir, "new", msgSpec{
				id: "question", from: "cto", to: []string{"user"}, thread: "gate/release",
				kind: "question", subject: "APPROVAL: release?", createdAt: coordNow.Add(-3 * time.Hour),
			})
			seedMessage(t, ctoDir, "new", msgSpec{
				id: "terminal", from: "cto", to: []string{"user"}, thread: "gate/release",
				kind: "status", subject: strings.ToUpper(string(terminal)) + ": release", createdAt: coordNow.Add(-2 * time.Hour),
				replyTo: "question",
				context: `{"gate":{"state":"` + string(terminal) + `","request_message_id":"question","requester":"cto","thread":"gate/release","actor":"cto"}}`,
			})

			snap, err := Build(proj, base, coordProbe())
			if err != nil {
				t.Fatal(err)
			}
			th := findThread(t, snap.Sessions[0].Coordination, "gate/release")
			if th.OperatorGate != nil || th.OperatorGateState != terminal || th.Triage == TriageNeedsYou {
				t.Fatalf("terminal gate = state:%q signal:%+v triage:%q", th.OperatorGateState, th.OperatorGate, th.Triage)
			}
		})
	}
}

func TestResolveOperatorGateTerminalSafetyAndReraise(t *testing.T) {
	question := Message{
		ID: "question", From: "cto", To: []string{"user"}, Thread: "gate/release",
		RawThread: "gate/release", SchemaOK: true,
		Kind: KindQuestion, Subject: "APPROVAL: release?", Created: coordNow.Add(-3 * time.Hour),
	}
	exactClose := func(m Message, requestID string) Message {
		m.SchemaOK = true
		m.RawThread = m.Thread
		m.ReplyTo = requestID
		return m
	}

	t.Run("unrelated status chatter cannot close", func(t *testing.T) {
		status := Message{
			ID: "status", From: "cto", To: []string{"user"}, Thread: "gate/release",
			Kind: KindStatus, Subject: "CLOSED: prose only", Created: coordNow.Add(-2 * time.Hour),
		}
		got, signal := ResolveOperatorGate([]Message{status, question}, "user", coordNow)
		if got != OperatorGateStateOpen || signal == nil || signal.LatestID != question.ID {
			t.Fatalf("gate = %q %+v, want original request open", got, signal)
		}
	})

	t.Run("non-operator answer cannot answer", func(t *testing.T) {
		answer := Message{
			ID: "answer", From: "qa", To: []string{"cto"}, Thread: "gate/release",
			Kind: KindAnswer, Subject: "APPROVED: release", Created: coordNow.Add(-2 * time.Hour),
		}
		got, signal := ResolveOperatorGate([]Message{answer, question}, "user", coordNow)
		if got != OperatorGateStateOpen || signal == nil {
			t.Fatalf("gate = %q %+v, want open after non-operator answer", got, signal)
		}
	})

	t.Run("operator answer reply binding must match when present", func(t *testing.T) {
		answer := Message{
			ID: "answer", From: "user", To: []string{"cto"}, Thread: "gate/release",
			Kind: KindAnswer, ReplyTo: "other-request", Subject: "APPROVED: release", Created: coordNow.Add(-2 * time.Hour),
		}
		got, signal := ResolveOperatorGate([]Message{answer, question}, "user", coordNow)
		if got != OperatorGateStateOpen || signal == nil {
			t.Fatalf("gate = %q %+v, want open after mismatched answer reply_to", got, signal)
		}
	})

	t.Run("typed approval answer must bind to the pending request", func(t *testing.T) {
		base := Message{
			ID: "answer", From: "user", To: []string{"cto"}, Thread: "gate/release",
			Kind: KindAnswer, ReplyTo: "question", ApprovalPresent: true, Created: coordNow.Add(-2 * time.Hour),
		}
		for _, tc := range []struct {
			name  string
			valid bool
			ctx   *operatorauth.ApprovalContext
		}{
			{name: "invalid typed context", ctx: &operatorauth.ApprovalContext{QuestionMessageID: "question"}},
			{name: "missing typed context", valid: true},
			{name: "mismatched question id", valid: true, ctx: &operatorauth.ApprovalContext{QuestionMessageID: "other"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				answer := base
				answer.ApprovalValid = tc.valid
				answer.Approval = tc.ctx
				got, signal := ResolveOperatorGate([]Message{answer, question}, "user", coordNow)
				if got != OperatorGateStateOpen || signal == nil {
					t.Fatalf("gate = %q %+v, want open after unbound typed approval", got, signal)
				}
			})
		}

		answer := base
		answer.ApprovalValid = true
		answer.Approval = &operatorauth.ApprovalContext{QuestionMessageID: "question"}
		got, signal := ResolveOperatorGate([]Message{answer, question}, "user", coordNow)
		if got != OperatorGateStateAnswered || signal != nil {
			t.Fatalf("gate = %q %+v, want matching typed approval to answer", got, signal)
		}
	})

	t.Run("late unbound legacy answer cannot answer reraised generation", func(t *testing.T) {
		reraised := question
		reraised.ID = "reraised"
		reraised.Created = coordNow.Add(-2 * time.Hour)
		answer := Message{
			ID: "answer", From: "user", To: []string{"cto"}, Thread: "gate/release",
			Kind: KindAnswer, Subject: "APPROVED: release", Created: coordNow.Add(-time.Hour),
		}
		got, signal := ResolveOperatorGate([]Message{answer, reraised, question}, "user", coordNow)
		if got != OperatorGateStateOpen || signal == nil || signal.LatestID != reraised.ID || signal.Age != 2*time.Hour {
			t.Fatalf("gate = %q %+v, want reraised generation open after unbound legacy answer", got, signal)
		}
	})

	t.Run("legacy answer to first request does not suppress later generation", func(t *testing.T) {
		answer := Message{
			ID: "answer", From: "user", To: []string{"cto"}, Thread: "gate/release",
			Kind: KindAnswer, Subject: "APPROVED: first release", Created: coordNow.Add(-2 * time.Hour),
		}
		reraised := question
		reraised.ID = "reraised"
		reraised.Subject = "APPROVAL: release with hotfix?"
		reraised.Created = coordNow.Add(-20 * time.Minute)
		got, signal := ResolveOperatorGate([]Message{reraised, question, answer}, "user", coordNow)
		if got != OperatorGateStateOpen || signal == nil || signal.LatestID != reraised.ID || signal.Age != 20*time.Minute {
			t.Fatalf("gate = %q %+v, want fresh reraised generation open", got, signal)
		}
	})

	t.Run("typed approval can answer current reraised generation", func(t *testing.T) {
		reraised := question
		reraised.ID = "reraised"
		reraised.Created = coordNow.Add(-2 * time.Hour)
		answer := Message{
			ID: "answer", From: "user", To: []string{"cto"}, Thread: "gate/release",
			Kind: KindAnswer, ApprovalPresent: true, ApprovalValid: true,
			Approval: &operatorauth.ApprovalContext{QuestionMessageID: "reraised"}, Created: coordNow.Add(-time.Hour),
		}
		got, signal := ResolveOperatorGate([]Message{answer, reraised, question}, "user", coordNow)
		if got != OperatorGateStateAnswered || signal != nil {
			t.Fatalf("gate = %q %+v, want bound typed approval to answer reraised generation", got, signal)
		}
	})

	t.Run("terminal event on non-gate thread is ignored", func(t *testing.T) {
		terminal := Message{
			ID: "close", From: "cto", To: []string{"user"}, Thread: "p2p/cto__user",
			Kind: KindStatus, Created: coordNow.Add(-2 * time.Hour),
			Context: map[string]any{"gate": map[string]any{"state": "closed", "request_message_id": "question", "requester": "cto", "thread": "gate/release", "actor": "cto"}},
		}
		terminal = exactClose(terminal, "question")
		got, signal := ResolveOperatorGate([]Message{terminal, question}, "user", coordNow)
		if got != OperatorGateStateOpen || signal == nil {
			t.Fatalf("gate = %q %+v, want open after non-gate terminal event", got, signal)
		}
	})

	t.Run("later valid request reopens with fresh age", func(t *testing.T) {
		terminal := Message{
			ID: "close", From: "cto", To: []string{"user"}, Thread: "gate/release",
			Kind: KindStatus, Created: coordNow.Add(-2 * time.Hour),
			Context: map[string]any{"gate": map[string]any{"state": "closed", "request_message_id": "question", "requester": "cto", "thread": "gate/release", "actor": "cto"}},
		}
		terminal = exactClose(terminal, "question")
		reraised := question
		reraised.ID = "reraised"
		reraised.Created = coordNow.Add(-12 * time.Minute)
		got, signal := ResolveOperatorGate([]Message{reraised, terminal, question}, "user", coordNow)
		if got != OperatorGateStateOpen || signal == nil || signal.LatestID != reraised.ID || signal.Age != 12*time.Minute {
			t.Fatalf("gate = %q %+v, want reraised request with 12m age", got, signal)
		}
	})

	t.Run("stale close after reraised request cannot close", func(t *testing.T) {
		reraised := question
		reraised.ID = "reraised"
		reraised.Created = coordNow.Add(-30 * time.Minute)
		staleClose := Message{
			ID: "stale-close", From: "cto", To: []string{"user"}, Thread: "gate/release",
			Kind: KindStatus, Created: coordNow.Add(-20 * time.Minute),
			Context: map[string]any{"gate": map[string]any{"state": "closed", "request_message_id": "question", "requester": "cto", "thread": "gate/release", "actor": "cto"}},
		}
		staleClose = exactClose(staleClose, "question")
		got, signal := ResolveOperatorGate([]Message{staleClose, reraised, question}, "user", coordNow)
		if got != OperatorGateStateOpen || signal == nil || signal.LatestID != reraised.ID {
			t.Fatalf("gate = %q %+v, want reraised request to survive stale close", got, signal)
		}
	})

	for _, tc := range []struct {
		name      string
		from      string
		actor     string
		request   string
		requester string
		thread    string
	}{
		{name: "wrong request id", from: "cto", actor: "cto", request: "other", requester: "cto", thread: "gate/release"},
		{name: "wrong actor", from: "cto", actor: "qa", request: "question", requester: "cto", thread: "gate/release"},
		{name: "wrong requester", from: "cto", actor: "cto", request: "question", requester: "qa", thread: "gate/release"},
		{name: "wrong context thread", from: "cto", actor: "cto", request: "question", requester: "cto", thread: "gate/other"},
		{name: "different agent close", from: "qa", actor: "qa", request: "question", requester: "cto", thread: "gate/release"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			close := Message{
				ID: "close", From: tc.from, To: []string{"user"}, Thread: "gate/release",
				Kind: KindStatus, Created: coordNow.Add(-2 * time.Hour),
				Context: map[string]any{"gate": map[string]any{"state": "closed", "request_message_id": tc.request, "requester": tc.requester, "thread": tc.thread, "actor": tc.actor}},
			}
			close = exactClose(close, "question")
			got, signal := ResolveOperatorGate([]Message{close, question}, "user", coordNow)
			if got != OperatorGateStateOpen || signal == nil {
				t.Fatalf("gate = %q %+v, want open after invalid close binding", got, signal)
			}
		})
	}

	t.Run("duplicate mailbox copies and input order are deterministic", func(t *testing.T) {
		answer := Message{
			ID: "answer", From: "user", To: []string{"cto"}, Thread: "gate/release",
			Kind: KindAnswer, Subject: "APPROVED: release", Created: coordNow.Add(-2 * time.Hour),
		}
		got, signal := ResolveOperatorGate([]Message{answer, question, answer, question}, "user", coordNow)
		if got != OperatorGateStateAnswered || signal != nil {
			t.Fatalf("gate = %q %+v, want answered regardless of duplicate scan order", got, signal)
		}
	})

	t.Run("same timestamp uses stable id ordering", func(t *testing.T) {
		at := coordNow.Add(-time.Hour)
		q := question
		q.ID, q.Created = "a-question", at
		close := Message{
			ID: "z-close", From: "cto", To: []string{"user"}, Thread: "gate/release",
			Kind: KindStatus, Created: at,
			Context: map[string]any{"gate": map[string]any{"state": "closed", "request_message_id": "a-question", "requester": "cto", "thread": "gate/release", "actor": "cto"}},
		}
		close = exactClose(close, "a-question")
		for _, input := range [][]Message{{close, q}, {q, close}} {
			got, signal := ResolveOperatorGate(input, "user", coordNow)
			if got != OperatorGateStateClosed || signal != nil {
				t.Fatalf("gate = %q %+v, want deterministic closed state", got, signal)
			}
		}
	})

	t.Run("conflicting duplicate terminal evidence fails open", func(t *testing.T) {
		valid := Message{
			ID: "close", From: "cto", To: []string{"user"}, Thread: "gate/release",
			Kind: KindStatus, Created: coordNow.Add(-2 * time.Hour),
			Context: map[string]any{"gate": map[string]any{"state": "closed", "request_message_id": "question", "requester": "cto", "thread": "gate/release", "actor": "cto"}},
		}
		valid = exactClose(valid, "question")
		conflict := valid
		conflict.Context = map[string]any{"gate": map[string]any{"state": "withdrawn", "request_message_id": "question", "requester": "cto", "thread": "gate/release", "actor": "cto"}}
		got, signal := ResolveOperatorGate([]Message{valid, question, conflict}, "user", coordNow)
		if got != OperatorGateStateOpen || signal == nil {
			t.Fatalf("gate = %q %+v, want open after conflicting duplicate evidence", got, signal)
		}
	})

	t.Run("conflicting duplicate request stays open independent of input order", func(t *testing.T) {
		copyA := question
		copyA.Subject = "APPROVAL: release A?"
		copyB := question
		copyB.Subject = "APPROVAL: release B?"
		for _, input := range [][]Message{{copyA, copyB}, {copyB, copyA}} {
			got, signal := ResolveOperatorGate(input, "user", coordNow)
			if got != OperatorGateStateOpen || signal == nil || signal.LatestID != question.ID || !signal.Conflicted {
				t.Fatalf("gate = %q %+v, want conflicted request ID conservatively open", got, signal)
			}
		}
	})

	t.Run("request schema conflict stays open independent of input order", func(t *testing.T) {
		valid := question
		degraded := question
		degraded.SchemaOK = false
		for _, input := range [][]Message{{valid, degraded}, {degraded, valid}} {
			got, signal := ResolveOperatorGate(input, "user", coordNow)
			if got != OperatorGateStateOpen || signal == nil || signal.LatestID != question.ID || !signal.Conflicted || signal.Terminalizable {
				t.Fatalf("gate = %q %+v, want schema-conflicted request conservatively open", got, signal)
			}
		}
	})

	t.Run("bound close cannot terminalize conflicted current request", func(t *testing.T) {
		copyA := question
		copyA.Subject = "APPROVAL: release A?"
		copyB := question
		copyB.Subject = "APPROVAL: release B?"
		close := Message{
			ID: "close", From: "cto", To: []string{"user"}, Thread: "gate/release",
			Kind: KindStatus, Created: coordNow.Add(-2 * time.Hour),
			Context: map[string]any{"gate": map[string]any{"state": "closed", "request_message_id": "question", "requester": "cto", "thread": "gate/release", "actor": "cto"}},
		}
		close = exactClose(close, "question")
		got, signal := ResolveOperatorGate([]Message{copyA, close, copyB}, "user", coordNow)
		if got != OperatorGateStateOpen || signal == nil || !signal.Conflicted {
			t.Fatalf("gate = %q %+v, want conflicted generation open despite bound close", got, signal)
		}
	})

	t.Run("same id reply-to conflict discards terminal evidence", func(t *testing.T) {
		answerA := Message{
			ID: "answer", From: "user", To: []string{"cto"}, Thread: "gate/release",
			Kind: KindAnswer, ReplyTo: "question", Created: coordNow.Add(-2 * time.Hour),
		}
		answerB := answerA
		answerB.ReplyTo = "other"
		got, signal := ResolveOperatorGate([]Message{answerA, question, answerB}, "user", coordNow)
		if got != OperatorGateStateOpen || signal == nil {
			t.Fatalf("gate = %q %+v, want open after conflicting answer reply_to evidence", got, signal)
		}
	})

	t.Run("exact duplicate terminal event is idempotent", func(t *testing.T) {
		close := Message{
			ID: "close", From: "cto", To: []string{"user"}, Thread: "gate/release",
			Kind: KindStatus, Created: coordNow.Add(-2 * time.Hour),
			Context: map[string]any{"gate": map[string]any{"state": "closed", "request_message_id": "question", "requester": "cto", "thread": "gate/release", "actor": "cto"}},
		}
		close = exactClose(close, "question")
		got, signal := ResolveOperatorGate([]Message{close, question, close}, "user", coordNow)
		if got != OperatorGateStateClosed || signal != nil {
			t.Fatalf("gate = %q %+v, want idempotent closed state", got, signal)
		}
	})

	t.Run("refs-only and matching mixed terminal bindings are accepted", func(t *testing.T) {
		for _, mixed := range []bool{false, true} {
			close := Message{
				ID: "close", From: "cto", To: []string{"user"}, Thread: "gate/release",
				RawThread: "gate/release", SchemaOK: true, Kind: KindStatus, Refs: []string{"question"}, RefsPresent: true, RefsValid: true, Created: coordNow.Add(-2 * time.Hour),
				Context: map[string]any{"gate": map[string]any{"state": "closed", "request_message_id": "question", "requester": "cto", "thread": "gate/release", "actor": "cto"}},
			}
			if mixed {
				close.ReplyTo = "question"
			}
			got, signal := ResolveOperatorGate([]Message{close, question}, "user", coordNow)
			if got != OperatorGateStateClosed || signal != nil {
				t.Fatalf("mixed=%t gate = %q %+v, want closed", mixed, got, signal)
			}
		}
	})

	t.Run("stale refs cannot close reraised generation", func(t *testing.T) {
		reraised := question
		reraised.ID = "reraised"
		reraised.Created = coordNow.Add(-30 * time.Minute)
		close := Message{
			ID: "close", From: "cto", To: []string{"user"}, Thread: "gate/release",
			RawThread: "gate/release", SchemaOK: true, Kind: KindStatus, Refs: []string{"question"}, RefsPresent: true, RefsValid: true, Created: coordNow.Add(-20 * time.Minute),
			Context: map[string]any{"gate": map[string]any{"state": "closed", "request_message_id": "question", "requester": "cto", "thread": "gate/release", "actor": "cto"}},
		}
		got, signal := ResolveOperatorGate([]Message{close, reraised, question}, "user", coordNow)
		if got != OperatorGateStateOpen || signal == nil || signal.LatestID != "reraised" {
			t.Fatalf("gate = %q %+v, want reraised generation open", got, signal)
		}
	})

	t.Run("refs bind current reraised generation", func(t *testing.T) {
		reraised := question
		reraised.ID = "reraised"
		reraised.Created = coordNow.Add(-30 * time.Minute)
		close := Message{
			ID: "close", From: "cto", To: []string{"user"}, Thread: "gate/release",
			RawThread: "gate/release", SchemaOK: true, Kind: KindStatus, Refs: []string{"reraised"}, RefsPresent: true, RefsValid: true, Created: coordNow.Add(-20 * time.Minute),
			Context: map[string]any{"gate": map[string]any{"state": "closed", "request_message_id": "reraised", "requester": "cto", "thread": "gate/release", "actor": "cto"}},
		}
		got, signal := ResolveOperatorGate([]Message{close, reraised, question}, "user", coordNow)
		if got != OperatorGateStateClosed || signal != nil {
			t.Fatalf("gate = %q %+v, want current reraised generation closed", got, signal)
		}
	})

	t.Run("same id conflicting refs discard terminal evidence", func(t *testing.T) {
		valid := Message{
			ID: "close", From: "cto", To: []string{"user"}, Thread: "gate/release",
			RawThread: "gate/release", SchemaOK: true, Kind: KindStatus, Refs: []string{"question"}, RefsPresent: true, RefsValid: true, Created: coordNow.Add(-2 * time.Hour),
			Context: map[string]any{"gate": map[string]any{"state": "closed", "request_message_id": "question", "requester": "cto", "thread": "gate/release", "actor": "cto"}},
		}
		conflict := valid
		conflict.Refs = []string{"other"}
		for _, input := range [][]Message{{valid, question, conflict}, {conflict, question, valid}} {
			got, signal := ResolveOperatorGate(input, "user", coordNow)
			if got != OperatorGateStateOpen || signal == nil {
				t.Fatalf("gate = %q %+v, want conflicting refs to fail open", got, signal)
			}
		}
	})

	t.Run("same id absent refs conflicts with explicit empty or null", func(t *testing.T) {
		absent := Message{
			ID: "close", From: "cto", To: []string{"user"}, Thread: "gate/release",
			RawThread: "gate/release", SchemaOK: true, Kind: KindStatus, ReplyTo: "question", Created: coordNow.Add(-2 * time.Hour),
			Context: map[string]any{"gate": map[string]any{"state": "closed", "request_message_id": "question", "requester": "cto", "thread": "gate/release", "actor": "cto"}},
		}
		for _, conflicting := range []Message{
			func() Message { m := absent; m.Refs = []string{}; m.RefsPresent = true; m.RefsValid = true; return m }(),
			func() Message { m := absent; m.RefsPresent = true; m.RefsValid = false; m.RefsRaw = "null"; return m }(),
		} {
			for _, input := range [][]Message{{absent, question, conflicting}, {conflicting, question, absent}} {
				got, signal := ResolveOperatorGate(input, "user", coordNow)
				if got != OperatorGateStateOpen || signal == nil {
					t.Fatalf("gate = %q %+v, want refs-presence conflict to fail open", got, signal)
				}
			}
		}
	})

	t.Run("malformed terminal evidence stays open", func(t *testing.T) {
		base := Message{
			ID: "close", From: "cto", To: []string{"user"}, Thread: "gate/release",
			Kind: KindStatus, Created: coordNow.Add(-2 * time.Hour),
			Context: map[string]any{"gate": map[string]any{"state": "closed", "request_message_id": "question", "requester": "cto", "thread": "gate/release", "actor": "cto"}},
		}
		base = exactClose(base, "question")
		setRefs := func(m *Message, refs []string, valid bool) {
			m.Refs = refs
			m.RefsPresent = true
			m.RefsValid = valid
		}
		for _, tc := range []struct {
			name   string
			mutate func(*Message)
		}{
			{name: "missing reply to", mutate: func(m *Message) { m.ReplyTo = "" }},
			{name: "wrong reply to", mutate: func(m *Message) { m.ReplyTo = "other" }},
			{name: "whitespace repaired reply to", mutate: func(m *Message) { m.ReplyTo = " question " }},
			{name: "schema mismatch", mutate: func(m *Message) { m.SchemaOK = false }},
			{name: "wrong kind", mutate: func(m *Message) { m.Kind = KindDecision }},
			{name: "missing raw thread", mutate: func(m *Message) { m.RawThread = "" }},
			{name: "nonexact raw thread", mutate: func(m *Message) { m.RawThread = " gate/release " }},
			{name: "empty refs array", mutate: func(m *Message) { m.ReplyTo = ""; setRefs(m, []string{}, true) }},
			{name: "null refs", mutate: func(m *Message) { m.ReplyTo = ""; setRefs(m, nil, false) }},
			{name: "empty ref", mutate: func(m *Message) { m.ReplyTo = ""; setRefs(m, []string{""}, true) }},
			{name: "multiple refs", mutate: func(m *Message) { m.ReplyTo = ""; setRefs(m, []string{"question", "other"}, true) }},
			{name: "duplicate refs", mutate: func(m *Message) { m.ReplyTo = ""; setRefs(m, []string{"question", "question"}, true) }},
			{name: "whitespace repaired ref", mutate: func(m *Message) { m.ReplyTo = ""; setRefs(m, []string{" question "}, true) }},
			{name: "mismatched ref", mutate: func(m *Message) { m.ReplyTo = ""; setRefs(m, []string{"other"}, true) }},
			{name: "mixed bindings disagree", mutate: func(m *Message) { setRefs(m, []string{"other"}, true) }},
			{name: "mixed binding is ambiguous", mutate: func(m *Message) { setRefs(m, []string{"question", "question"}, true) }},
			{name: "mixed binding has empty refs array", mutate: func(m *Message) { setRefs(m, []string{}, true) }},
			{name: "mixed binding has null refs", mutate: func(m *Message) { setRefs(m, nil, false) }},
		} {
			t.Run(tc.name, func(t *testing.T) {
				terminal := base
				tc.mutate(&terminal)
				got, signal := ResolveOperatorGate([]Message{terminal, question}, "user", coordNow)
				if got != OperatorGateStateOpen || signal == nil {
					t.Fatalf("gate = %q %+v, want malformed terminal evidence ignored", got, signal)
				}
			})
		}
	})

	t.Run("typed close without open generation is ignored", func(t *testing.T) {
		close := Message{
			ID: "close", From: "cto", To: []string{"user"}, Thread: "gate/release",
			Kind: KindStatus, Created: coordNow,
			Context: map[string]any{"gate": map[string]any{"state": "closed", "request_message_id": "question", "requester": "cto", "thread": "gate/release", "actor": "cto"}},
		}
		close = exactClose(close, "question")
		got, signal := ResolveOperatorGate([]Message{close}, "user", coordNow)
		if got != OperatorGateStateUnknown || signal != nil {
			t.Fatalf("gate = %q %+v, want no lifecycle without an open generation", got, signal)
		}
	})

	t.Run("terminal raw thread mismatch is ignored", func(t *testing.T) {
		close := Message{
			ID: "close", From: "cto", To: []string{"user"}, Thread: "gate/release", RawThread: "gate/other",
			Kind: KindStatus, SchemaOK: true, ReplyTo: "question", Created: coordNow.Add(-2 * time.Hour),
			Context: map[string]any{"gate": map[string]any{"state": "closed", "request_message_id": "question", "requester": "cto", "thread": "gate/release", "actor": "cto"}},
		}
		got, signal := ResolveOperatorGate([]Message{close, question}, "user", coordNow)
		if got != OperatorGateStateOpen || signal == nil {
			t.Fatalf("gate = %q %+v, want open after raw-thread mismatch", got, signal)
		}
	})

	t.Run("repaired raw thread is not accepted for terminal evidence", func(t *testing.T) {
		close := Message{
			ID: "close", From: "cto", To: []string{"user"}, Thread: "gate/release", RawThread: "gate//release",
			Kind: KindStatus, SchemaOK: true, ReplyTo: "question", Created: coordNow.Add(-2 * time.Hour),
			Context: map[string]any{"gate": map[string]any{"state": "closed", "request_message_id": "question", "requester": "cto", "thread": "gate/release", "actor": "cto"}},
		}
		got, signal := ResolveOperatorGate([]Message{close, question}, "user", coordNow)
		if got != OperatorGateStateOpen || signal == nil {
			t.Fatalf("gate = %q %+v, want open after repaired raw-thread evidence", got, signal)
		}
	})

	t.Run("bound close cannot suppress repaired pending request", func(t *testing.T) {
		repaired := question
		repaired.RawThread = "gate//release"
		close := Message{
			ID: "close", From: "cto", To: []string{"user"}, Thread: "gate/release",
			Kind: KindStatus, Created: coordNow.Add(-2 * time.Hour),
			Context: map[string]any{"gate": map[string]any{"state": "closed", "request_message_id": "question", "requester": "cto", "thread": "gate/release", "actor": "cto"}},
		}
		close = exactClose(close, "question")
		got, signal := ResolveOperatorGate([]Message{close, repaired}, "user", coordNow)
		if got != OperatorGateStateOpen || signal == nil || signal.Terminalizable {
			t.Fatalf("gate = %q %+v, want non-terminalizable repaired request open", got, signal)
		}
	})

	t.Run("done decision prose does not open a gate", func(t *testing.T) {
		done := Message{
			ID: "done", From: "cto", To: []string{"user"}, Thread: "gate/release",
			Kind: KindDecision, Subject: "DONE: release completed", Created: coordNow,
		}
		got, signal := ResolveOperatorGate([]Message{done}, "user", coordNow)
		if got != OperatorGateStateUnknown || signal != nil {
			t.Fatalf("gate = %q %+v, want DONE decision prose ignored", got, signal)
		}
	})
}

func TestOperatorGateSignalReraisedGateUsesFreshClockAfterAnswer(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	userDir := filepath.Join(base, "s", "agents", "user")
	ctoDir := seedAgent(t, base, "s", "cto", launch.Record{Binary: "codex", Handle: "cto", Session: "s", AgentPID: 1})

	// Scan order observes the answer first, then both operator-mailbox
	// questions. Chronological replay should clear the old gate and leave only
	// the newer re-raised gate pending.
	seedMessage(t, ctoDir, "new", msgSpec{
		id: "answer", from: "user", to: []string{"cto"}, thread: "gate/release",
		kind: "answer", subject: "APPROVED: previous release gate", createdAt: coordNow.Add(-55 * time.Minute),
	})
	seedMessage(t, userDir, "new", msgSpec{
		id: "old-question", from: "cto", to: []string{"user"}, thread: "gate/release",
		kind: "question", subject: "APPROVAL: release?", createdAt: coordNow.Add(-1 * time.Hour),
	})
	seedMessage(t, userDir, "new", msgSpec{
		id: "new-question", from: "cto", to: []string{"user"}, thread: "gate/release",
		kind: "question", subject: "APPROVAL: release with hotfix?", createdAt: coordNow.Add(-20 * time.Minute),
	})

	snap, err := Build(proj, base, coordProbe())
	if err != nil {
		t.Fatal(err)
	}
	th := findThread(t, snap.Sessions[0].Coordination, "gate/release")
	if th.Triage != TriageNeedsYou || th.OperatorGate == nil {
		t.Fatalf("gate triage/operator signal = %q/%+v, want needs-you signal", th.Triage, th.OperatorGate)
	}
	if th.OperatorGate.LatestID != "new-question" || th.OperatorGate.Age != 20*time.Minute {
		t.Fatalf("operator gate signal = %+v, want latest new-question age 20m", th.OperatorGate)
	}
	if th.OperatorGate.Escalation != OperatorGateEscalationInitial {
		t.Fatalf("gate escalation = %q, want initial from the re-raised gate age", th.OperatorGate.Escalation)
	}
}

func TestOperatorGateSignalSurvivesLaterNonAnswerChatter(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	userDir := filepath.Join(base, "s", "agents", "user")
	_ = seedAgent(t, base, "s", "cto", launch.Record{Binary: "codex", Handle: "cto", Session: "s", AgentPID: 1})

	seedMessage(t, userDir, "new", msgSpec{
		id: "gate", from: "cto", to: []string{"user"}, thread: "gate/release",
		kind: "question", subject: "APPROVAL: release?", createdAt: coordNow.Add(-3 * time.Hour),
	})
	seedMessage(t, userDir, "new", msgSpec{
		id: "note", from: "cto", to: []string{"user"}, thread: "gate/release",
		kind: "status", subject: "status: checks still passing", createdAt: coordNow.Add(-5 * time.Minute),
	})

	snap, err := Build(proj, base, coordProbe())
	if err != nil {
		t.Fatal(err)
	}
	th := findThread(t, snap.Sessions[0].Coordination, "gate/release")
	if th.Triage != TriageNeedsYou || th.OperatorGate == nil {
		t.Fatalf("gate triage/operator signal = %q/%+v, want needs-you signal", th.Triage, th.OperatorGate)
	}
	if th.OperatorGate.LatestID != "gate" || th.OperatorGate.Age != 3*time.Hour {
		t.Fatalf("operator gate signal = %+v, want pending gate age 3h despite later status", th.OperatorGate)
	}
	if th.OperatorGate.Escalation != OperatorGateEscalationStrongWarning {
		t.Fatalf("gate escalation = %q, want strong-warning", th.OperatorGate.Escalation)
	}
}

// TestAttnReason_TaughtPrefixes proves the agent-side convention taught in
// bootstrap.md + team-rules end-to-end: a thread addressed to the operator
// handle "user" with the literal `APPROVAL:` subject prefix classifies
// AttnApprove, the `DONE:` prefix classifies AttnGoalReached, and a normal
// status subject that merely embeds the substring "done" ("abandoned") does NOT
// classify goal-reached — guarding the bare-"done" false positive. This is the
// signal the needs-you tier (APPROVE / GOAL-REACHED) lights up on for real
// squads. Runs through the SAME parser + collapse path real data uses.
func TestAttnReason_TaughtPrefixes(t *testing.T) {
	cases := []struct {
		name       string
		kind       string
		subject    string
		wantTriage Triage
		wantReason AttnReason
	}{
		// Taught APPROVAL: prefix -> approve.
		{"approval-prefix", "question", "APPROVAL: run X?", TriageNeedsYou, AttnApprove},
		// Taught DONE: prefix (kind=decision, as taught) -> goal-reached.
		{"done-prefix", "decision", "DONE: epic complete", TriageNeedsYou, AttnGoalReached},
		// False-positive guard: "abandoned" embeds "done" but is NOT goal-reached,
		// and status prose is not live needs-you without an action signal.
		{"abandoned-not-goal", "question", "status: abandoned the retry", TriageClear, AttnNone},
		// Standalone "done" word still classifies goal-reached (word match kept).
		{"standalone-done", "decision", "all done, ready for review", TriageNeedsYou, AttnGoalReached},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			proj := t.TempDir()
			userDir := seedAgent(t, base, "s", "user", launch.Record{Binary: "claude", Handle: "user", Session: "s", AgentPID: 9})
			_ = seedAgent(t, base, "s", "cto", launch.Record{Binary: "codex", Handle: "cto", Session: "s", AgentPID: 1})
			seedMessage(t, userDir, "new", msgSpec{
				id: "m1", from: "cto", to: []string{"user"}, thread: "decision/x",
				kind: tc.kind, subject: tc.subject, createdAt: coordNow.Add(-1 * time.Minute),
			})
			snap, err := Build(proj, base, coordProbe())
			if err != nil {
				t.Fatal(err)
			}
			th := findThread(t, snap.Sessions[0].Coordination, "decision/x")
			if th.Triage != tc.wantTriage {
				t.Fatalf("Triage = %q, want %q", th.Triage, tc.wantTriage)
			}
			if th.AttnReason != tc.wantReason {
				t.Errorf("AttnReason = %q, want %q (subject %q)", th.AttnReason, tc.wantReason, tc.subject)
			}
			// Explicit anti-false-positive assertion for the "abandoned" case.
			if tc.name == "abandoned-not-goal" && th.AttnReason == AttnGoalReached {
				t.Errorf("'abandoned' must NOT classify goal-reached (subject %q)", tc.subject)
			}
		})
	}
}

// TestHasGoalWord unit-guards the word-boundary "done" matcher: it matches the
// taught DONE: prefix and standalone "done" tokens, and rejects substring-only
// embeds that previously false-positived under a bare "done" substring marker.
func TestHasGoalWord(t *testing.T) {
	hit := []string{"done:", "done: epic complete", "all done", "we are done", "done"}
	miss := []string{"abandoned", "condoned the change", "abandoned the retry", "redone task", ""}
	for _, s := range hit {
		if !hasGoalWord(strings.ToLower(s)) {
			t.Errorf("hasGoalWord(%q) = false, want true", s)
		}
	}
	for _, s := range miss {
		if hasGoalWord(strings.ToLower(s)) {
			t.Errorf("hasGoalWord(%q) = true, want false", s)
		}
	}
}
