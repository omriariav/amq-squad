package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/attention"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestCollectSelfOperatorVisibilityCurrentStateAndClear(t *testing.T) {
	project, cfg, target, msgs := selfVerifyFixture(t)
	root := filepath.Join(project, "mail", "s")
	for _, m := range msgs {
		writeSelfApprovalTestMessage(t, filepath.Join(root, "agents", m.From), "cur", m, m.Approval)
	}
	snap := state.Snapshot{Sessions: []state.Session{{Name: "s", TeamProfile: "default", NamespaceID: "default/s", Root: root}}}
	old := resolveVerifiedOperatorActor
	t.Cleanup(func() { resolveVerifiedOperatorActor = old })
	t.Setenv("AM_ME", "qa")
	resolveVerifiedOperatorActor = func(_, profile, session, role, handle string) (verifiedOperatorActor, error) {
		return verifiedOperatorActor{Role: role, Handle: handle}, nil
	}
	before := decideVerifyActionWithPolicy(msgs, "gate/merge-398", "protected_branch_push", target, "user", cfg, "s", project, "default")
	items := collectSelfOperatorVisibilityAttention(cfg, project, "default", snap, "s", time.Now())
	after := decideVerifyActionWithPolicy(msgs, "gate/merge-398", "protected_branch_push", target, "user", cfg, "s", project, "default")
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("collection changed authorization: before=%+v after=%+v", before, after)
	}
	if !attentionState(items, "self_approved", false) || !attentionState(items, "human_only_gate", true) {
		t.Fatalf("valid self state=%+v", items)
	}
	answer := state.Message{ID: "h1", From: "user", Thread: "gate/merge-398", Kind: state.KindAnswer, Subject: "DENIED: merge", Body: "Gate-Kind: merge\nAction: protected_branch_push\nTarget: " + target, Created: time.Now().Add(time.Minute)}
	writeSelfApprovalTestMessage(t, filepath.Join(root, "agents", "user"), "cur", answer, nil)
	items = collectSelfOperatorVisibilityAttention(cfg, project, "default", snap, "s", time.Now().Add(2*time.Minute))
	if !attentionState(items, "self_approved", true) || !attentionState(items, "human_only_gate", true) {
		t.Fatalf("resolved denial did not clear=%+v", items)
	}
}

func TestCollectHumanOnlyGateConflictAndReopen(t *testing.T) {
	project := t.TempDir()
	root := filepath.Join(project, "mail", "s")
	op := team.DefaultOperator()
	op.InteractionMode = team.OperatorInteractionSelfOperator
	op.SelfOperator = &team.SelfOperatorPolicy{LeadRole: "cto", PolicyRevision: 1, Sessions: map[string]team.SelfOperatorSessionPolicy{"s": {Enabled: true, AllowedGateKinds: []string{"merge"}}}}
	cfg := team.Team{Project: project, Operator: &op, Orchestrated: true, Lead: "cto", Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex"}}}
	q := state.Message{ID: "q1", From: "cto", Thread: "gate/spawn", Kind: state.KindQuestion, Subject: "APPROVAL: spawn", Body: "Gate-Kind: spawn\nAction: spawn\nTarget: worker qa", Created: time.Now()}
	writeSelfApprovalTestMessage(t, filepath.Join(root, "agents", "cto"), "cur", q, nil)
	snap := state.Snapshot{Sessions: []state.Session{{Name: "s", TeamProfile: "default", NamespaceID: "default/s", Root: root}}}
	items := collectSelfOperatorVisibilityAttention(cfg, project, "default", snap, "s", time.Now())
	if !attentionState(items, "human_only_gate", false) {
		t.Fatalf("pending human-only missing=%+v", items)
	}
	q.Subject = "conflict"
	writeSelfApprovalTestMessage(t, filepath.Join(root, "agents", "qa"), "cur", q, nil)
	items = collectSelfOperatorVisibilityAttention(cfg, project, "default", snap, "s", time.Now())
	if !attentionState(items, "human_only_gate", false) {
		t.Fatal("conflict failed open")
	}
	_ = os.Remove(filepath.Join(root, "agents", "qa", "inbox", "cur", "q1.md"))
	a := state.Message{ID: "a1", From: "user", Thread: "gate/spawn", Kind: state.KindAnswer, Subject: "APPROVED: spawn", Body: "Gate-Kind: spawn\nAction: spawn\nTarget: worker qa", Created: time.Now().Add(time.Minute)}
	writeSelfApprovalTestMessage(t, filepath.Join(root, "agents", "user"), "cur", a, nil)
	items = collectSelfOperatorVisibilityAttention(cfg, project, "default", snap, "s", time.Now())
	if !attentionState(items, "human_only_gate", true) {
		t.Fatalf("resolved did not clear=%+v", items)
	}
}

func attentionState(items []operatorAttention, kind string, cleared bool) bool {
	for _, i := range items {
		if i.EventType == kind && i.Cleared == cleared {
			return true
		}
	}
	return false
}

func TestSelfOperatorEventSelectClearReopen(t *testing.T) {
	now := time.Now()
	key := "default/s\x00self_approved\x00gate/x"
	active := operatorAttention{EventType: "self_approved", Key: key, LatestID: "a1"}
	sel, _, st := selectNotifications([]operatorAttention{active}, notifyStateFile{Schema: 2, Items: map[string]notifyStateRecord{}}, time.Hour, now)
	if len(sel) != 1 || !st.Items[key].Active {
		t.Fatal("initial event not selected")
	}
	_, _, st = selectNotifications([]operatorAttention{{EventType: "self_approved", Key: key, LatestID: "a1", Cleared: true}}, st, time.Hour, now.Add(time.Minute))
	if st.Items[key].Active {
		t.Fatal("clear remained active")
	}
	sel, _, st = selectNotifications([]operatorAttention{active}, st, time.Hour, now.Add(2*time.Minute))
	if len(sel) != 1 || !st.Items[key].Active {
		t.Fatal("same-fingerprint reopen not selected")
	}
}

func TestScopedSelfOperatorTombstonesModeFlipAndRemoval(t *testing.T) {
	self := attention.SelfApprovedKey("default", "s", "gate/x")
	human := attention.HumanOnlyGateKey("default", "s", "gate/x")
	other := attention.SelfApprovedKey("other", "s", "gate/x")
	prior := notifyStateFile{Schema: 2, Items: map[string]notifyStateRecord{self: {Active: true}, human: {Active: true}, other: {Active: true}}}
	items := scopedSelfOperatorTombstones(nil, prior, "default", "s")
	if !attentionState(items, "self_approved", true) || !attentionState(items, "human_only_gate", true) || len(items) != 2 {
		t.Fatalf("scope tombstones=%+v", items)
	}
	_, _, next := selectNotifications(items, prior, time.Hour, time.Now())
	if next.Items[self].Active || next.Items[human].Active || !next.Items[other].Active {
		t.Fatalf("scope clear=%+v", next.Items)
	}
}

func TestCollectSelfApprovalAdversarialTable(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, string, *team.Team, string, *[]state.Message)
	}{
		{"wrong sender", func(_ *testing.T, _ string, _ *team.Team, _ string, m *[]state.Message) { (*m)[1].From = "qa" }},
		{"wrong typed role", func(_ *testing.T, _ string, _ *team.Team, _ string, m *[]state.Message) {
			(*m)[1].Approval.AnsweredByRole = "qa"
		}},
		{"stale question id", func(_ *testing.T, _ string, _ *team.Team, _ string, m *[]state.Message) {
			(*m)[1].Approval.QuestionMessageID = "old"
		}},
		{"wrong binding", func(_ *testing.T, _ string, _ *team.Team, _ string, m *[]state.Message) {
			(*m)[1].Approval.Target = "PR #398 head deadbee into main"
		}},
		{"newer question", func(_ *testing.T, _ string, _ *team.Team, target string, m *[]state.Message) {
			*m = append(*m, state.Message{ID: "q2", From: "cto", Thread: "gate/merge-398", Kind: state.KindQuestion, Body: "Gate-Kind: merge\nAction: protected_branch_push\nTarget: " + target, Created: time.Now().Add(time.Hour)})
		}},
		{"paused", func(_ *testing.T, _ string, c *team.Team, _ string, _ *[]state.Message) {
			e := c.Operator.SelfOperator.Sessions["s"]
			e.Paused = true
			c.Operator.SelfOperator.Sessions["s"] = e
		}},
		{"disabled", func(_ *testing.T, _ string, c *team.Team, _ string, _ *[]state.Message) {
			e := c.Operator.SelfOperator.Sessions["s"]
			e.Enabled = false
			c.Operator.SelfOperator.Sessions["s"] = e
		}},
		{"stale revision", func(_ *testing.T, _ string, c *team.Team, _ string, _ *[]state.Message) {
			c.Operator.SelfOperator.PolicyRevision++
		}},
		{"stale hash", func(_ *testing.T, _ string, _ *team.Team, _ string, m *[]state.Message) {
			(*m)[1].Approval.PolicyHash = "sha256:stale"
		}},
		{"wrong session", func(_ *testing.T, _ string, c *team.Team, _ string, _ *[]state.Message) {
			e := c.Operator.SelfOperator.Sessions["s"]
			delete(c.Operator.SelfOperator.Sessions, "s")
			c.Operator.SelfOperator.Sessions["other"] = e
		}},
		{"missing receipt", func(t *testing.T, p string, _ *team.Team, _ string, _ *[]state.Message) {
			path := filepath.Join(selfApprovalStoreDir(p, "default", "s"), safeGateFile("gate/merge-398")+"-a1.receipt.json")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}},
		{"tampered receipt", func(t *testing.T, p string, _ *team.Team, _ string, _ *[]state.Message) {
			path := filepath.Join(selfApprovalStoreDir(p, "default", "s"), safeGateFile("gate/merge-398")+"-a1.receipt.json")
			f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = f.WriteString("{}")
			_ = f.Close()
		}},
		{"tampered evidence", func(t *testing.T, _ string, _ *team.Team, _ string, m *[]state.Message) {
			f, err := os.OpenFile((*m)[1].Approval.PreflightPath, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = f.WriteString(" ")
			_ = f.Close()
		}},
		{"human approval", func(_ *testing.T, _ string, _ *team.Team, target string, m *[]state.Message) {
			*m = append(*m, state.Message{ID: "h1", From: "user", Thread: "gate/merge-398", Kind: state.KindAnswer, Subject: "APPROVED: merge", Body: "Gate-Kind: merge\nAction: protected_branch_push\nTarget: " + target, Created: time.Now().Add(time.Hour)})
		}},
		{"human barrier", func(_ *testing.T, _ string, _ *team.Team, _ string, m *[]state.Message) {
			*m = append(*m, state.Message{ID: "h1", From: "user", Thread: "gate/merge-398", Kind: state.KindStatus, Subject: "hold", Created: time.Now().Add(time.Hour)})
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			project, cfg, target, msgs := selfVerifyFixture(t)
			tc.mutate(t, project, &cfg, target, &msgs)
			root := filepath.Join(project, "mail", "s")
			for _, m := range msgs {
				writeSelfApprovalTestMessage(t, filepath.Join(root, "agents", m.From), "cur", m, m.Approval)
			}
			snap := state.Snapshot{Sessions: []state.Session{{Name: "s", TeamProfile: "default", NamespaceID: "default/s", Root: root}}}
			items := collectSelfOperatorVisibilityAttention(cfg, project, "default", snap, "s", time.Now().Add(2*time.Hour))
			if attentionState(items, "self_approved", false) {
				t.Fatalf("invalid self approval emitted: %+v", items)
			}
			for _, i := range items {
				if i.EventType == "self_approved" && i.Key == attention.HumanOnlyGateKey("default", "s", i.Thread) {
					t.Fatal("event keys collided")
				}
			}
		})
	}
}
