package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/compoundrelease"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestCompoundReleaseBarrierClearsWithStableTombstone(t *testing.T) {
	project := t.TempDir()
	session := "issue-414"
	root := squadnamespace.AMQRoot(project, team.DefaultProfile, session)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	degradedSession := state.Session{Name: session, TeamProfile: team.DefaultProfile, NamespaceID: squadnamespace.ID(team.DefaultProfile, session), Root: root + ".wrong"}
	degraded, _, err := projectCompoundReleaseSession(project, team.DefaultProfile, "none", team.DefaultOperatorHandle, filepath.Dir(root), degradedSession, notifyNow)
	if err != nil {
		t.Fatal(err)
	}
	active := findReleaseAttention(t, degraded.Items, "compound_release_degraded")
	if active.Cleared || !active.Actionable || active.Answerable || active.Respond != "" {
		t.Fatalf("active barrier=%+v", active)
	}

	cleanSession := degradedSession
	cleanSession.Root = root
	clean, _, err := projectCompoundReleaseSession(project, team.DefaultProfile, "none", team.DefaultOperatorHandle, filepath.Dir(root), cleanSession, notifyNow.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	tombstone := findReleaseAttention(t, clean.Items, "compound_release_degraded")
	if !tombstone.Cleared || tombstone.Actionable || tombstone.Answerable || tombstone.Key != active.Key || tombstone.LatestID == active.LatestID {
		t.Fatalf("active=%+v tombstone=%+v", active, tombstone)
	}
}

func TestCompoundReleaseClaimedIDPreservesOrdinarySameThread(t *testing.T) {
	project, snapshot, cfg, root := compoundReleaseProjectionFixture(t)
	thread := "gate/shared"
	writeCompoundAttentionMessage(t, root, "user", "new", "release-v1", "cto", "user", thread, "APPROVAL: marker", "question", notifyNow, map[string]any{"release_child": map[string]any{"schema_version": 1}})
	writeCompoundAttentionMessage(t, root, "user", "new", "ordinary", "cto", "user", thread, "APPROVAL: ordinary", "question", notifyNow.Add(time.Second), nil)

	projected, err := collectProjectedOperatorAttention(cfg, project, team.DefaultProfile, snapshot, team.DefaultOperatorHandle, "issue-414", notifyNow.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	var ordinary *operatorAttention
	for i := range projected.Items {
		if projected.Items[i].LatestID == "release-v1" {
			t.Fatalf("claimed v1 marker leaked into fallback: %+v", projected.Items[i])
		}
		if projected.Items[i].LatestID == "ordinary" {
			ordinary = &projected.Items[i]
		}
	}
	if ordinary == nil || !ordinary.Actionable || !ordinary.Answerable || ordinary.Thread != thread {
		t.Fatalf("ordinary same-thread attention=%+v all=%+v", ordinary, projected.Items)
	}
}

func TestCompoundReleaseProjectionOrdinaryGateRegressionAndOneScan(t *testing.T) {
	project, snapshot, cfg, root := compoundReleaseProjectionFixture(t)
	writeCompoundAttentionMessage(t, root, "user", "new", "ordinary-gate", "cto", "user", "gate/ordinary", "APPROVAL: ordinary", "question", notifyNow, nil)
	oldResolve := resolveCompoundReleaseAttention
	defer func() { resolveCompoundReleaseAttention = oldResolve }()
	oldScan := scanOperatorSessionMessages
	defer func() { scanOperatorSessionMessages = oldScan }()
	resolveCalls, scanCalls := 0, 0
	scanOperatorSessionMessages = func(root string, now func() time.Time) ([]state.Message, []state.Warning) {
		scanCalls++
		return state.ScanSessionMessages(root, now)
	}
	resolveCompoundReleaseAttention = func(scope compoundrelease.SessionScope, query compoundrelease.ResolveQuery, adapter compoundrelease.InspectionAdapter) (compoundrelease.Resolution, error) {
		resolveCalls++
		return compoundrelease.ResolveSessionSeries(scope, query, adapter)
	}

	projected, err := collectProjectedOperatorAttention(cfg, project, team.DefaultProfile, snapshot, team.DefaultOperatorHandle, "issue-414", notifyNow.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if resolveCalls != 1 || scanCalls != 1 {
		t.Fatalf("resolve calls=%d physical scans=%d", resolveCalls, scanCalls)
	}
	var ordinary *operatorAttention
	for i := range projected.Items {
		if projected.Items[i].LatestID == "ordinary-gate" {
			ordinary = &projected.Items[i]
		}
	}
	if ordinary == nil || !ordinary.Actionable || !ordinary.Answerable || ordinary.Respond == "" {
		t.Fatalf("ordinary gate=%+v all=%+v", ordinary, projected.Items)
	}
}

func TestCompoundReleaseProjectionSelectedBaseRootLayoutsScanOnce(t *testing.T) {
	oldResolve := resolveCompoundReleaseAttention
	defer func() { resolveCompoundReleaseAttention = oldResolve }()
	oldScan := scanOperatorSessionMessages
	defer func() { scanOperatorSessionMessages = oldScan }()

	for _, tc := range []struct {
		name    string
		profile string
		root    func(string, string) (string, string)
	}{
		{name: "custom default base", profile: team.DefaultProfile, root: func(project, session string) (string, string) {
			base := filepath.Join(project, "custom-amq")
			return base, filepath.Join(base, session)
		}},
		{name: "named profile exact root", profile: "release", root: func(project, session string) (string, string) {
			root := filepath.Join(project, "custom-amq", "release", session)
			return root, root
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := t.TempDir()
			session := "issue-457"
			base, root := tc.root(project, session)
			if err := os.MkdirAll(filepath.Join(root, "agents", "user", "inbox", "new"), 0o755); err != nil {
				t.Fatal(err)
			}
			writeCompoundAttentionMessage(t, root, "user", "new", "ordinary-gate", "cto", "user", "gate/ordinary", "APPROVAL: ordinary", "question", notifyNow, nil)
			snapshot := state.Snapshot{BaseRoot: base, Sessions: []state.Session{{Name: session, TeamProfile: tc.profile, NamespaceID: squadnamespace.ID(tc.profile, session), Root: root, Agents: []state.Agent{{Handle: "cto", Liveness: state.LivenessAlive}}}}}
			cfg := team.Team{Project: project, Members: []team.Member{{Role: "cto", Handle: "cto"}}}
			scans := 0
			scanOperatorSessionMessages = func(root string, now func() time.Time) ([]state.Message, []state.Warning) {
				scans++
				return state.ScanSessionMessages(root, now)
			}
			resolveCompoundReleaseAttention = func(scope compoundrelease.SessionScope, query compoundrelease.ResolveQuery, adapter compoundrelease.InspectionAdapter) (compoundrelease.Resolution, error) {
				return compoundrelease.ResolveSessionSeries(scope, query, adapter)
			}

			projected, err := collectProjectedOperatorAttention(cfg, project, tc.profile, snapshot, team.DefaultOperatorHandle, session, notifyNow.Add(time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if scans != 1 || !projected.Captures[session].Scanned {
				t.Fatalf("physical scans=%d capture=%+v", scans, projected.Captures[session])
			}
			gate, ok := attentionByLatestID(projected.Items, "ordinary-gate")
			if !ok || !gate.Actionable || !gate.Answerable || gate.Respond == "" {
				t.Fatalf("selected-root gate=%+v all=%+v", gate, projected.Items)
			}
			barrier := findReleaseAttention(t, projected.Items, "compound_release_degraded")
			if !barrier.Cleared {
				t.Fatalf("selected-root barrier remained active: %+v", barrier)
			}
		})
	}
}

func TestCompoundReleaseProjectionSelectedRootMismatchIsPresentUnscannedBarrier(t *testing.T) {
	for _, tc := range []struct {
		name, profile string
		roots         func(string, string) (string, string)
	}{
		{name: "wrong observed root", profile: team.DefaultProfile, roots: func(project, session string) (string, string) {
			base := filepath.Join(project, "custom-amq")
			return base, filepath.Join(base, session+"-wrong")
		}},
		{name: "named profile layout mismatch", profile: "release", roots: func(project, session string) (string, string) {
			base := filepath.Join(project, "custom-amq", "release", session)
			return base, filepath.Join(base, session)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := t.TempDir()
			session := "issue-457"
			base, observed := tc.roots(project, session)
			if err := os.MkdirAll(observed, 0o755); err != nil {
				t.Fatal(err)
			}
			snapshot := state.Snapshot{BaseRoot: base, Sessions: []state.Session{{Name: session, TeamProfile: tc.profile, NamespaceID: squadnamespace.ID(tc.profile, session), Root: observed}}}
			scans := 0
			oldScan := scanOperatorSessionMessages
			scanOperatorSessionMessages = func(root string, now func() time.Time) ([]state.Message, []state.Warning) {
				scans++
				return state.ScanSessionMessages(root, now)
			}
			t.Cleanup(func() { scanOperatorSessionMessages = oldScan })

			projected, err := collectProjectedOperatorAttention(team.Team{Project: project}, project, tc.profile, snapshot, team.DefaultOperatorHandle, session, notifyNow)
			if err != nil {
				t.Fatal(err)
			}
			capture, present := projected.Captures[session]
			if !present || capture.Scanned || scans != 0 {
				t.Fatalf("capture present=%t value=%+v physical scans=%d", present, capture, scans)
			}
			barrier := findReleaseAttention(t, projected.Items, "compound_release_degraded")
			if barrier.Cleared || !barrier.Actionable || barrier.Answerable || barrier.Respond != "" {
				t.Fatalf("root mismatch barrier=%+v", barrier)
			}
		})
	}
}

func TestCompoundReleaseRootBarrierPreservesExistingOrdinaryAttention(t *testing.T) {
	project, snapshot, cfg, root := compoundReleaseProjectionFixture(t)
	writeCompoundAttentionMessage(t, root, "user", "new", "ordinary", "cto", "user", "gate/ordinary", "APPROVAL: ordinary", "question", notifyNow, nil)
	messages, warnings := state.ScanSessionMessages(root, func() time.Time { return notifyNow })
	snapshot.Sessions[0].Coordination = state.ProjectCoordination(messages, snapshot.Sessions[0].Agents, warnings, notifyNow, state.Thresholds{OperatorHandle: team.DefaultOperatorHandle})
	snapshot.Sessions[0].Root = root + ".wrong"
	oldScan := scanOperatorSessionMessages
	defer func() { scanOperatorSessionMessages = oldScan }()
	fallbackScans := 0
	scanOperatorSessionMessages = func(root string, now func() time.Time) ([]state.Message, []state.Warning) {
		fallbackScans++
		return state.ScanSessionMessages(root, now)
	}

	projected, err := collectProjectedOperatorAttention(cfg, project, team.DefaultProfile, snapshot, team.DefaultOperatorHandle, "issue-414", notifyNow.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if fallbackScans != 0 {
		t.Fatalf("zero-scan barrier triggered %d fallback scan(s)", fallbackScans)
	}
	if _, ok := attentionByLatestID(projected.Items, "ordinary"); !ok {
		t.Fatalf("root barrier erased ordinary attention: %+v", projected.Items)
	}
	barrier := findReleaseAttention(t, projected.Items, "compound_release_degraded")
	if barrier.Cleared || !barrier.Actionable || barrier.Answerable {
		t.Fatalf("barrier=%+v", barrier)
	}
}

func TestExplicitActionabilityPreservesNonGateSurfaceBehavior(t *testing.T) {
	project, snapshot, cfg, root := compoundReleaseProjectionFixture(t)
	writeCompoundAttentionMessage(t, root, "user", "new", "surface", "cto", "user", "ask/ordinary", "Which option?", "question", notifyNow, nil)
	projected, err := collectProjectedOperatorAttention(cfg, project, team.DefaultProfile, snapshot, team.DefaultOperatorHandle, "issue-414", notifyNow.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	surface, ok := attentionByLatestID(projected.Items, "surface")
	if !ok || !surface.Actionable || surface.Answerable || operatorOpenGates(projected.Items) != 0 {
		t.Fatalf("surface=%+v open=%d all=%+v", surface, operatorOpenGates(projected.Items), projected.Items)
	}
	data := operatorStatusEnvelopeData{ProjectDir: project, Profile: team.DefaultProfile, Session: "issue-414", Namespace: squadnamespace.Resolve(project, team.DefaultProfile, "issue-414"), Attention: activeOperatorAttention(projected.Items), OperatorLoop: operatorLoopStatus{Backlog: 1}}
	action, found := deriveNextAction(data, project)
	if !found || action.ID != "operator_status" {
		t.Fatalf("non-gate surface displaced backlog next action: found=%t action=%+v", found, action)
	}
}

func TestSelfOperatorVisibilityReusesFilteredSingleCapture(t *testing.T) {
	project, snapshot, cfg, root := compoundReleaseProjectionFixture(t)
	cfg.Operator = &team.OperatorConfig{Enabled: true, Handle: team.DefaultOperatorHandle, InteractionMode: team.OperatorInteractionSelfOperator, Participant: true}
	writeCompoundAttentionMessage(t, root, "user", "new", "release-v1", "cto", "user", "gate/shared", "APPROVAL: marker", "question", notifyNow, map[string]any{"release_child": map[string]any{"schema_version": 1}})
	writeCompoundAttentionMessage(t, root, "user", "new", "ordinary", "cto", "user", "gate/shared", "APPROVAL: ordinary", "question", notifyNow.Add(time.Second), nil)
	oldResolve := resolveCompoundReleaseAttention
	defer func() { resolveCompoundReleaseAttention = oldResolve }()
	oldScan := scanOperatorSessionMessages
	defer func() { scanOperatorSessionMessages = oldScan }()
	scans := 0
	scanOperatorSessionMessages = func(root string, now func() time.Time) ([]state.Message, []state.Warning) {
		scans++
		return state.ScanSessionMessages(root, now)
	}
	resolveCompoundReleaseAttention = func(scope compoundrelease.SessionScope, query compoundrelease.ResolveQuery, adapter compoundrelease.InspectionAdapter) (compoundrelease.Resolution, error) {
		return compoundrelease.ResolveSessionSeries(scope, query, adapter)
	}
	projected, err := collectProjectedOperatorAttention(cfg, project, team.DefaultProfile, snapshot, team.DefaultOperatorHandle, "issue-414", notifyNow.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	visibility := collectSelfOperatorVisibilityAttentionCaptured(cfg, project, team.DefaultProfile, projected.Snapshot, "issue-414", notifyNow.Add(time.Minute), projected.Captures)
	if scans != 1 {
		t.Fatalf("self-operator projection scans=%d", scans)
	}
	for _, item := range visibility {
		if item.LatestID == "release-v1" {
			t.Fatalf("self-operator visibility reused claimed release id: %+v", visibility)
		}
	}
}

func TestProjectedUnreadBacklogUsesThreadUnionAndExactMailboxState(t *testing.T) {
	ordinary := []state.ThreadSummary{{ID: "gate/shared", UnreadBy: []string{team.DefaultOperatorHandle}}}
	child := operatorAttention{EventType: "compound_release_child", Thread: "gate/shared", Actionable: true, Answerable: true, Unread: true}
	if got := operatorUnreadBacklogWithProjected(ordinary, team.DefaultOperatorHandle, []operatorAttention{child}); got != 1 {
		t.Fatalf("same-thread backlog=%d, want 1", got)
	}
	child.Thread = "gate/distinct"
	if got := operatorUnreadBacklogWithProjected(ordinary, team.DefaultOperatorHandle, []operatorAttention{child}); got != 2 {
		t.Fatalf("distinct-thread backlog=%d, want 2", got)
	}
	child.Unread = false
	if got := operatorUnreadBacklogWithProjected(nil, team.DefaultOperatorHandle, []operatorAttention{child}); got != 0 {
		t.Fatalf("read child backlog=%d, want 0", got)
	}
}

func TestProjectedUnreadBacklogComposesTerminalLifecycleWithSecureBarriers(t *testing.T) {
	unreadGate := func(thread string, gateState state.OperatorGateState) []state.ThreadSummary {
		return []state.ThreadSummary{{ID: thread, UnreadBy: []string{team.DefaultOperatorHandle}, OperatorGateState: gateState}}
	}

	for _, kind := range []string{"securely_answered", "legacy_answered"} {
		t.Run(kind, func(t *testing.T) {
			if got := operatorUnreadBacklogWithProjected(unreadGate("gate/answered", state.OperatorGateStateAnswered), team.DefaultOperatorHandle, nil); got != 0 {
				t.Fatalf("answered gate without active projection backlog=%d, want 0", got)
			}
		})
	}

	t.Run("structurally_answered_secure_barrier", func(t *testing.T) {
		barrier := operatorAttention{
			EventType: "gate", Thread: "gate/secure-barrier", Summary: "approval_receipt_missing",
			Actionable: true, Answerable: false,
		}
		unrelated := operatorAttention{EventType: "gate", Thread: "gate/unrelated", Actionable: true}
		for order, items := range [][]operatorAttention{{barrier, unrelated}, {unrelated, barrier}} {
			if got := operatorUnreadBacklogWithProjected(unreadGate(barrier.Thread, state.OperatorGateStateAnswered), team.DefaultOperatorHandle, items); got != 1 {
				t.Fatalf("answered gate with active secure barrier order %d backlog=%d, want 1", order, got)
			}
		}
	})

	for _, terminalState := range []state.OperatorGateState{state.OperatorGateStateClosed, state.OperatorGateStateWithdrawn} {
		t.Run(string(terminalState), func(t *testing.T) {
			staleBarrier := operatorAttention{EventType: "gate", Thread: "gate/terminal", Actionable: true}
			if got := operatorUnreadBacklogWithProjected(unreadGate(staleBarrier.Thread, terminalState), team.DefaultOperatorHandle, []operatorAttention{staleBarrier}); got != 0 {
				t.Fatalf("%s gate backlog=%d, want 0", terminalState, got)
			}
		})
	}

	t.Run("compound_release_child_remains_independent_union_member", func(t *testing.T) {
		child := operatorAttention{EventType: "compound_release_child", Thread: "gate/answered", Actionable: true, Answerable: true, Unread: true}
		if got := operatorUnreadBacklogWithProjected(unreadGate(child.Thread, state.OperatorGateStateAnswered), team.DefaultOperatorHandle, []operatorAttention{child}); got != 1 {
			t.Fatalf("answered ordinary gate with unread compound child backlog=%d, want 1", got)
		}
	})
}

func TestCompoundReleaseChildrenStableKeysAndEvidenceLossTombstone(t *testing.T) {
	fixture, active := newCLIActiveReleaseAttentionFixture(t)
	sess := state.Session{Name: fixture.adapter.session, TeamProfile: fixture.adapter.profile, NamespaceID: squadnamespace.ID(fixture.adapter.profile, fixture.adapter.session), Root: fixture.adapter.root}
	projected, _, err := projectCompoundReleaseSession(fixture.adapter.project, fixture.adapter.profile, "none", team.DefaultOperatorHandle, filepath.Dir(fixture.adapter.root), sess, notifyNow.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	activeChildren := releaseChildAttentionByRole(projected.Items)
	if len(activeChildren) != 2 {
		t.Fatalf("active children=%+v", activeChildren)
	}
	for _, child := range active.Prepared.Children {
		item := activeChildren[child.Role]
		if item.Cleared || !item.Actionable || !item.Answerable || item.LatestID == "" || item.Key == "" {
			t.Fatalf("active child %s=%+v", child.Role, item)
		}
	}

	broken := active.Prepared.Children[0]
	if err := os.Remove(filepath.Join(deliveryReceiptDir(fixture.adapter.project, fixture.adapter.profile, fixture.adapter.session), broken.Receipt.AttemptID+".json")); err != nil {
		t.Fatal(err)
	}
	degraded, _, err := projectCompoundReleaseSession(fixture.adapter.project, fixture.adapter.profile, "none", team.DefaultOperatorHandle, filepath.Dir(fixture.adapter.root), sess, notifyNow.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	degradedChildren := releaseChildAttentionByRole(degraded.Items)
	if got := degradedChildren[broken.Role]; !got.Cleared || got.Actionable || got.Answerable || got.Key != activeChildren[broken.Role].Key {
		t.Fatalf("broken child active=%+v tombstone=%+v", activeChildren[broken.Role], got)
	}
	sibling := active.Prepared.Children[1]
	if got := degradedChildren[sibling.Role]; got.Cleared || !got.Actionable || !got.Answerable || got.Key != activeChildren[sibling.Role].Key {
		t.Fatalf("sibling active=%+v degraded=%+v", activeChildren[sibling.Role], got)
	}
	recovery := findReleaseAttention(t, degraded.Items, "compound_release_recovery")
	if recovery.Cleared || !recovery.Actionable || recovery.Answerable || recovery.Respond != "" {
		t.Fatalf("recovery=%+v", recovery)
	}
	prior := notifyStateFile{Schema: notifyStateSchema, Items: map[string]notifyStateRecord{}}
	_, _, prior = selectNotifications(projected.Items, prior, time.Hour, notifyNow)
	_, _, next := selectNotifications(degraded.Items, prior, time.Hour, notifyNow.Add(time.Minute))
	if next.Items[activeChildren[broken.Role].Key].Active {
		t.Fatalf("broken child tombstone did not clear notify state: %+v", next.Items[activeChildren[broken.Role].Key])
	}
}

func TestCompoundReleaseChildrenTombstoneOnInactiveSuccessor(t *testing.T) {
	fixture, active := newCLIActiveReleaseAttentionFixture(t)
	sess := state.Session{Name: fixture.adapter.session, TeamProfile: fixture.adapter.profile, NamespaceID: squadnamespace.ID(fixture.adapter.profile, fixture.adapter.session), Root: fixture.adapter.root}
	before, _, err := projectCompoundReleaseSession(fixture.adapter.project, fixture.adapter.profile, "none", team.DefaultOperatorHandle, filepath.Dir(fixture.adapter.root), sess, notifyNow)
	if err != nil {
		t.Fatal(err)
	}
	spec := active.Prepared.Spec
	spec.TagTarget += "-next"
	spec.GitHubReleaseTarget += "-next"
	spec.Note.Summary += " next"
	if _, err := fixture.store.PrepareSuccessor(active.Pointer.GenerationID, spec); err != nil {
		t.Fatal(err)
	}
	after, _, err := projectCompoundReleaseSession(fixture.adapter.project, fixture.adapter.profile, "none", team.DefaultOperatorHandle, filepath.Dir(fixture.adapter.root), sess, notifyNow.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	beforeChildren, afterChildren := releaseChildAttentionByRole(before.Items), releaseChildAttentionByRole(after.Items)
	for role, activeItem := range beforeChildren {
		cleared := afterChildren[role]
		if !cleared.Cleared || cleared.Actionable || cleared.Answerable || cleared.Key != activeItem.Key {
			t.Fatalf("role %s active=%+v inactive=%+v", role, activeItem, cleared)
		}
	}
}

func TestCompoundReleaseStatusPreservesPhysicalCursorAndReadBacklog(t *testing.T) {
	fixture, _ := newCLIActiveReleaseAttentionFixture(t)
	cfg := team.Team{Project: fixture.adapter.project, Workstream: fixture.adapter.session, Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: fixture.adapter.session}}}
	if err := team.Write(fixture.adapter.project, cfg); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(fixture.adapter.project, ".agent-mail")
	seedNotifyLaunch(t, fixture.adapter.project, base, fixture.adapter.session, "cto")
	data, err := buildOperatorStatusData(operatorExecution{ProjectDir: fixture.adapter.project, Profile: team.DefaultProfile, Session: fixture.adapter.session, BaseRoot: base, Probe: probeForNext(), Now: func() time.Time { return notifyNow.Add(time.Minute) }})
	if err != nil {
		t.Fatal(err)
	}
	if data.operatorCursor != "question-tag" || data.OperatorLoop.Backlog != 2 || data.OperatorLoop.GatesOpen != 2 {
		t.Fatalf("cursor/backlog/open=%q/%d/%d attention=%+v", data.operatorCursor, data.OperatorLoop.Backlog, data.OperatorLoop.GatesOpen, data.Attention)
	}
	for _, child := range releaseChildAttentionByRole(data.Attention) {
		path := filepath.Join(fixture.adapter.root, "agents", team.DefaultOperatorHandle, "inbox", "new", child.LatestID+".md")
		cur := filepath.Join(fixture.adapter.root, "agents", team.DefaultOperatorHandle, "inbox", "cur", child.LatestID+".md")
		if err := os.MkdirAll(filepath.Dir(cur), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(path, cur); err != nil {
			t.Fatal(err)
		}
	}
	data, err = buildOperatorStatusData(operatorExecution{ProjectDir: fixture.adapter.project, Profile: team.DefaultProfile, Session: fixture.adapter.session, BaseRoot: base, Probe: probeForNext(), Now: func() time.Time { return notifyNow.Add(2 * time.Minute) }})
	if err != nil {
		t.Fatal(err)
	}
	if data.OperatorLoop.Backlog != 0 || data.OperatorLoop.GatesOpen != 2 || data.operatorCursor != "question-tag" {
		t.Fatalf("read cursor/backlog/open=%q/%d/%d", data.operatorCursor, data.OperatorLoop.Backlog, data.OperatorLoop.GatesOpen)
	}
}

func TestCompoundReleaseReceiptRawReadRejectsHardlink(t *testing.T) {
	fixture := newCLIReleaseReceiptFixture(t, 1)
	link := fixture.receipt.Path + ".hardlink"
	if err := os.Link(fixture.receipt.Path, link); err != nil {
		t.Fatal(err)
	}
	root, dir, err := openReceiptDirRoot(fixture.adapter.project, fixture.adapter.profile, fixture.adapter.session, false)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	path := filepath.Join(dir, filepath.Base(fixture.receipt.Path))
	if _, err := readDeliveryReceiptRawAt(root, filepath.Base(path), path); err == nil {
		t.Fatal("hardlinked delivery receipt was accepted by raw inspection read")
	}
}

func TestCompoundReleaseRecoveryNextIsInspectOnlyAndNotAgedGate(t *testing.T) {
	inspect := "amq-squad operator status --project /project --profile default --session s --json"
	recovery := operatorAttention{EventType: "compound_release_recovery", Key: "recovery", Profile: team.DefaultProfile, Session: "s", Thread: "gate/release", Subject: "compound release recovery", Escalation: string(state.OperatorGateEscalationStrongWarning), Inspect: inspect, Actionable: true, Answerable: false}
	data := operatorStatusEnvelopeData{ProjectDir: "/project", Profile: team.DefaultProfile, Session: "s", Namespace: squadnamespace.Resolve("/project", team.DefaultProfile, "s"), Attention: []operatorAttention{recovery}}
	action, found := deriveNextAction(data, "/project")
	if !found || action.ID != "compound_release_recovery" || action.ActionKind != "display" || action.Command != inspect || strings.Contains(action.Command, "operator answer") {
		t.Fatalf("inspect-only next found=%t action=%+v", found, action)
	}
	if warnings := statusWarningsForAgedOperatorGates(data); len(warnings) != 0 {
		t.Fatalf("non-answerable recovery produced aged gate warning: %+v", warnings)
	}
	answerable := recovery
	answerable.EventType = "compound_release_child"
	answerable.Answerable = true
	if warnings := statusWarningsForAgedOperatorGates(operatorStatusEnvelopeData{Attention: []operatorAttention{answerable}}); len(warnings) != 1 {
		t.Fatalf("answerable aged child warnings=%+v", warnings)
	}
}

func TestCompoundReleaseNotifyStatusNextShareProjectedChildren(t *testing.T) {
	fixture, _ := newCLIActiveReleaseAttentionFixture(t)
	base := installCLIReleaseAttentionTeam(t, fixture)
	now := time.Date(2026, 7, 15, 2, 0, 0, 0, time.UTC)
	status, err := buildOperatorStatusData(operatorExecution{ProjectDir: fixture.adapter.project, Profile: fixture.adapter.profile, Session: fixture.adapter.session, BaseRoot: base, Probe: probeForNext(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	statusKeys := activeAttentionKeys(status.Attention)
	if len(statusKeys) != 2 || status.OperatorLoop.GatesOpen != 2 {
		t.Fatalf("status keys/open=%v/%d attention=%+v", statusKeys, status.OperatorLoop.GatesOpen, status.Attention)
	}

	notifyOut := executeNotifyForTest(t, notifyExecution{ProjectDir: fixture.adapter.project, Profile: fixture.adapter.profile, Session: fixture.adapter.session, BaseRoot: base, StatePath: filepath.Join(fixture.adapter.project, "notify-test.json"), RenotifyAfter: time.Hour, DryRun: true, JSON: true, Now: func() time.Time { return now }})
	notifyData := decodeJSONEnvelope[notifyEnvelopeData](t, notifyOut).Data
	if notifyKeys := activeAttentionKeys(notifyData.Notifications); !slices.Equal(notifyKeys, statusKeys) {
		t.Fatalf("notify keys=%v status keys=%v notifications=%+v", notifyKeys, statusKeys, notifyData.Notifications)
	}

	var nextOut bytes.Buffer
	if err := executeNext(nextExecution{ProjectDir: fixture.adapter.project, Profile: fixture.adapter.profile, Session: fixture.adapter.session, BaseRoot: base, JSON: true, Out: &nextOut, Probe: probeForNext(), Now: func() time.Time { return now }}); err != nil {
		t.Fatal(err)
	}
	next := decodeJSONEnvelope[nextActionData](t, nextOut.String()).Data
	if next.ID != "gate_answer" || !slices.Contains(statusKeys, compoundReleaseChildKeyForThread(status.Attention, next.GateTopic)) {
		t.Fatalf("next=%+v status=%+v", next, status.Attention)
	}
}

func compoundReleaseProjectionFixture(t *testing.T) (string, state.Snapshot, team.Team, string) {
	t.Helper()
	project := t.TempDir()
	session := "issue-414"
	root := squadnamespace.AMQRoot(project, team.DefaultProfile, session)
	if err := os.MkdirAll(filepath.Join(root, "agents", "user", "inbox", "new"), 0o755); err != nil {
		t.Fatal(err)
	}
	agents := []state.Agent{{Handle: "cto", Liveness: state.LivenessAlive}}
	snapshot := state.Snapshot{BaseRoot: filepath.Dir(root), Sessions: []state.Session{{Name: session, TeamProfile: team.DefaultProfile, NamespaceID: squadnamespace.ID(team.DefaultProfile, session), Root: root, Agents: agents}}}
	cfg := team.Team{Project: project, Members: []team.Member{{Role: "cto", Handle: "cto"}}}
	return project, snapshot, cfg, root
}

func newCLIActiveReleaseAttentionFixture(t *testing.T) (cliReleaseReceiptFixture, compoundrelease.Snapshot) {
	t.Helper()
	fixture := newCLIReleaseReceiptFixture(t, 1)
	result, err := fixture.store.Reconcile(fixture.publishing.Pointer.GenerationID, fixture.adapter)
	if err != nil || result.Disposition != compoundrelease.ReconcileInvoked {
		t.Fatalf("first child reconcile=%+v err=%v", result, err)
	}
	child := fixture.publishing.Prepared.Children[1]
	now := time.Date(2026, 7, 15, 1, 0, 1, 0, time.UTC)
	receipt := &deliveryReceiptData{
		SchemaVersion: deliveryReceiptSchemaVersion, AttemptID: child.Receipt.AttemptID,
		Kind: child.Receipt.Kind, Method: "durable_amq", Status: "queued",
		Target:    deliveryReceiptTarget{ProjectDir: fixture.adapter.project, Profile: fixture.adapter.profile, Session: fixture.adapter.session, NamespaceID: child.Receipt.NamespaceID, Role: child.Role, Handle: child.Receipt.Recipient},
		MessageID: "question-github-release", Sender: child.Receipt.Sender, Recipient: child.Receipt.Recipient,
		Recipients: []string{child.Receipt.Recipient}, Consumers: []deliveryConsumerState{{Consumer: child.Receipt.Recipient, State: deliveryStateDeliveredNotDrained}},
		DeliveryState: deliveryStateDeliveredNotDrained, EvidenceSource: "amq_send_output", AMQInvoked: true,
		Root: fixture.adapter.root, Thread: child.Thread, Stages: []deliveryReceiptStage{{State: deliveryStateDeliveredNotDrained, At: now, Detail: "attention fixture child two"}}, CreatedAt: now,
	}
	if err := writeDeliveryReceipt(fixture.adapter.project, fixture.adapter.profile, fixture.adapter.session, receipt); err != nil {
		t.Fatal(err)
	}
	writeExactCLIReleaseQuestion(t, fixture.adapter.root, child, receipt.MessageID, now)
	result, err = fixture.store.Reconcile(fixture.publishing.Pointer.GenerationID, fixture.adapter)
	if err != nil || result.Disposition != compoundrelease.ReconcileActivated || result.Snapshot.Active == nil {
		t.Fatalf("activation=%+v err=%v", result, err)
	}
	return fixture, result.Snapshot
}

func writeExactCLIReleaseQuestion(t *testing.T, root string, child operatorauth.ReleaseChildPlan, messageID string, created time.Time) {
	t.Helper()
	header := map[string]any{
		"schema": 1, "id": messageID, "from": child.Receipt.Sender, "to": []string{child.Receipt.Recipient},
		"thread": child.Thread, "subject": child.Subject, "created": created.Format(time.RFC3339Nano), "priority": "normal", "kind": "question",
		"context": map[string]any{"authorization_request": child.AuthorizationRequest, "release_child": child.ReleaseChild},
	}
	b, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "agents", child.Receipt.Recipient, "inbox", "new", messageID+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := append(append([]byte("---json\n"), b...), []byte("\n---\n"+child.Body+"\n")...)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func releaseChildAttentionByRole(items []operatorAttention) map[string]operatorAttention {
	out := map[string]operatorAttention{}
	for _, item := range items {
		if item.EventType == "compound_release_child" {
			out[item.Role] = item
		}
	}
	return out
}

func installCLIReleaseAttentionTeam(t *testing.T, fixture cliReleaseReceiptFixture) string {
	t.Helper()
	cfg := team.Team{Project: fixture.adapter.project, Workstream: fixture.adapter.session, Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: fixture.adapter.session}}}
	if err := team.WriteProfile(fixture.adapter.project, fixture.adapter.profile, cfg); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(fixture.adapter.project, ".agent-mail")
	seedNotifyLaunch(t, fixture.adapter.project, base, fixture.adapter.session, "cto")
	return base
}

func activeAttentionKeys(items []operatorAttention) []string {
	var keys []string
	for _, item := range items {
		if item.Actionable && !item.Cleared {
			keys = append(keys, item.Key)
		}
	}
	slices.Sort(keys)
	return keys
}

func compoundReleaseChildKeyForThread(items []operatorAttention, thread string) string {
	for _, item := range items {
		if item.EventType == "compound_release_child" && item.Thread == thread {
			return item.Key
		}
	}
	return ""
}

func writeCompoundAttentionMessage(t *testing.T, root, owner, box, id, from, to, thread, subject, kind string, created time.Time, context map[string]any) {
	t.Helper()
	header := map[string]any{"schema": 1, "id": id, "from": from, "to": []string{to}, "thread": thread, "subject": subject, "created": created.Format(time.RFC3339Nano), "priority": "normal", "kind": kind}
	if context != nil {
		header["context"] = context
	}
	b, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "agents", owner, "inbox", box, id+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(append([]byte("---json\n"), b...), []byte("\n---\nbody\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
}

func findReleaseAttention(t *testing.T, items []operatorAttention, eventType string) operatorAttention {
	t.Helper()
	for _, item := range items {
		if item.EventType == eventType {
			return item
		}
	}
	t.Fatalf("event %q not found in %+v", eventType, items)
	return operatorAttention{}
}

func attentionByLatestID(items []operatorAttention, id string) (operatorAttention, bool) {
	for _, item := range items {
		if item.LatestID == id {
			return item, true
		}
	}
	return operatorAttention{}, false
}
