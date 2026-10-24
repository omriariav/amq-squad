package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/compoundrelease"
	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/state"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

func TestCompoundReleaseBarrierClearsWithStableTombstone(t *testing.T) {
	project := t.TempDir()
	session := "issue-414"
	root := squadnamespace.AMQRoot(project, squadnamespace.DefaultProfile, session)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	degradedSession := state.Session{Name: session, TeamProfile: squadnamespace.DefaultProfile, NamespaceID: squadnamespace.ID(squadnamespace.DefaultProfile, session), Root: root + ".wrong"}
	degraded, _, err := projectCompoundReleaseSession(project, squadnamespace.DefaultProfile, "none", team.DefaultOperatorHandle, filepath.Dir(root), degradedSession, notifyNow)
	if err != nil {
		t.Fatal(err)
	}
	active := findReleaseAttention(t, degraded.Items, "compound_release_degraded")
	if active.Cleared || !active.Actionable || active.Answerable || active.Respond != "" {
		t.Fatalf("active barrier=%+v", active)
	}

	cleanSession := degradedSession
	cleanSession.Root = root
	clean, _, err := projectCompoundReleaseSession(project, squadnamespace.DefaultProfile, "none", team.DefaultOperatorHandle, filepath.Dir(root), cleanSession, notifyNow.Add(time.Minute))
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

	projected, err := collectProjectedOperatorAttention(cfg, project, squadnamespace.DefaultProfile, snapshot, team.DefaultOperatorHandle, "issue-414", notifyNow.Add(time.Minute))
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

	projected, err := collectProjectedOperatorAttention(cfg, project, squadnamespace.DefaultProfile, snapshot, team.DefaultOperatorHandle, "issue-414", notifyNow.Add(time.Minute))
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
		{name: "custom default base", profile: squadnamespace.DefaultProfile, root: func(project, session string) (string, string) {
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
		{name: "wrong observed root", profile: squadnamespace.DefaultProfile, roots: func(project, session string) (string, string) {
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

	projected, err := collectProjectedOperatorAttention(cfg, project, squadnamespace.DefaultProfile, snapshot, team.DefaultOperatorHandle, "issue-414", notifyNow.Add(time.Minute))
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
	projected, err := collectProjectedOperatorAttention(cfg, project, squadnamespace.DefaultProfile, snapshot, team.DefaultOperatorHandle, "issue-414", notifyNow.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	surface, ok := attentionByLatestID(projected.Items, "surface")
	if !ok || !surface.Actionable || surface.Answerable || openGatesForTest(projected.Items) != 0 {
		t.Fatalf("surface=%+v open=%d all=%+v", surface, openGatesForTest(projected.Items), projected.Items)
	}
}

func compoundReleaseProjectionFixture(t *testing.T) (string, state.Snapshot, team.Team, string) {
	t.Helper()
	project := t.TempDir()
	session := "issue-414"
	root := squadnamespace.AMQRoot(project, squadnamespace.DefaultProfile, session)
	if err := os.MkdirAll(filepath.Join(root, "agents", "user", "inbox", "new"), 0o755); err != nil {
		t.Fatal(err)
	}
	agents := []state.Agent{{Handle: "cto", Liveness: state.LivenessAlive}}
	snapshot := state.Snapshot{BaseRoot: filepath.Dir(root), Sessions: []state.Session{{Name: session, TeamProfile: squadnamespace.DefaultProfile, NamespaceID: squadnamespace.ID(squadnamespace.DefaultProfile, session), Root: root, Agents: agents}}}
	cfg := team.Team{Project: project, Members: []team.Member{{Role: "cto", Handle: "cto"}}}
	return project, snapshot, cfg, root
}

func newCLIActiveReleaseAttentionFixture(t *testing.T) (cliReleaseFixture, compoundrelease.Snapshot) {
	t.Helper()
	fixture := newCLIReleaseFixture(t, 1)
	child := fixture.publishing.Prepared.Children[1]
	now := time.Date(2026, 7, 15, 1, 0, 1, 0, time.UTC)
	writeExactCLIReleaseQuestion(t, fixture.adapter.root, fixture.publishing.Prepared.Spec, child, "question-github-release", now)
	result, err := fixture.store.Reconcile(fixture.publishing.Pointer.GenerationID, fixture.adapter)
	if err != nil || result.Disposition != compoundrelease.ReconcileActivated || result.Snapshot.Active == nil {
		t.Fatalf("activation=%+v err=%v", result, err)
	}
	return fixture, result.Snapshot
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

// The compound release children stay two open gates after the operator reads
// them (4-3: the projection directly; the deleted verb's unread backlog count
// went with it).
func TestCompoundReleaseAttentionKeepsOpenGatesAfterRead(t *testing.T) {
	fixture, _ := newCLIActiveReleaseAttentionFixture(t)
	cfg := team.Team{Project: fixture.adapter.project, Session: fixture.adapter.session, Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: fixture.adapter.session}}}
	if err := team.Write(fixture.adapter.project, cfg); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(fixture.adapter.project, ".agent-mail")
	seedNotifyLaunch(t, fixture.adapter.project, base, fixture.adapter.session, "cto")
	items := projectAttentionForTest(t, fixture.adapter.project, squadnamespace.DefaultProfile, fixture.adapter.session, base, probeForNext(), notifyNow.Add(time.Minute))
	if openGatesForTest(items) != 2 {
		t.Fatalf("open=%d attention=%+v", openGatesForTest(items), items)
	}
	for _, child := range releaseChildAttentionByRole(items) {
		path := filepath.Join(fixture.adapter.root, "agents", team.DefaultOperatorHandle, "inbox", "new", child.LatestID+".md")
		cur := filepath.Join(fixture.adapter.root, "agents", team.DefaultOperatorHandle, "inbox", "cur", child.LatestID+".md")
		if err := os.MkdirAll(filepath.Dir(cur), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(path, cur); err != nil {
			t.Fatal(err)
		}
	}
	items = projectAttentionForTest(t, fixture.adapter.project, squadnamespace.DefaultProfile, fixture.adapter.session, base, probeForNext(), notifyNow.Add(2*time.Minute))
	if openGatesForTest(items) != 2 {
		t.Fatalf("read open=%d", openGatesForTest(items))
	}
}
