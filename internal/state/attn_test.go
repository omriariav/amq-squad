package state

import (
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
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
