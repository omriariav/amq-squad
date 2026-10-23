package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/state"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

func seedWaitPostureContextMessage(t *testing.T, base, session, owner, box string, header map[string]any) {
	t.Helper()
	dir := filepath.Join(base, session, "agents", owner, "inbox", box)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.MarshalIndent(header, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := header["id"].(string)
	content := append([]byte("---json\n"), b...)
	content = append(content, []byte("\n---\nbody\n")...)
	if err := os.WriteFile(filepath.Join(dir, id+".md"), content, 0o600); err != nil {
		t.Fatal(err)
	}
}

type waitPostureFixture struct {
	dir, base, root, session string
}

func (f waitPostureFixture) installSnapshot(t *testing.T) {
	t.Helper()
	snap, err := state.BuildWithThresholds(f.dir, f.base, state.DefaultProbe, state.Thresholds{OperatorHandle: "user"})
	if err != nil {
		t.Fatal(err)
	}
	previous := waitPostureLoadSnapshot
	waitPostureLoadSnapshot = func(string, string) (state.Snapshot, error) { return snap, nil }
	t.Cleanup(func() { waitPostureLoadSnapshot = previous })
}

func (f waitPostureFixture) request(timeout time.Duration) waitPostureRequest {
	return waitPostureRequest{
		Command: "collect", WaitKind: "collect_watch", ProjectDir: f.dir,
		Profile: team.DefaultProfile, Session: f.session, Root: f.root,
		Timeout: timeout, Blocking: true,
	}
}

func TestGuardOwnedWaitUsesCollapsedCallerGateState(t *testing.T) {
	fx := newWaitPostureFixture(t, team.OperatorInteractionLeadPane)
	now := time.Date(2026, 7, 15, 9, 0, 0, 0, time.UTC)
	seedNotifyMessage(t, fx.base, fx.session, "user", "new", notifyMsg{ID: "a", From: "cto", To: "user", Thread: "gate/a", Subject: "APPROVAL: a", Kind: "question", Created: now})
	seedNotifyMessage(t, fx.base, fx.session, "user", "new", notifyMsg{ID: "z", From: "cto", To: "user", Thread: "gate/z", Subject: "APPROVAL: z", Kind: "question", Created: now.Add(time.Second)})
	seedNotifyMessage(t, fx.base, fx.session, "user", "cur", notifyMsg{ID: "resolved-q", From: "cto", To: "user", Thread: "gate/resolved", Subject: "APPROVAL: resolved", Kind: "question", Created: now.Add(2 * time.Second)})
	seedNotifyMessage(t, fx.base, fx.session, "cto", "cur", notifyMsg{ID: "resolved-a", From: "user", To: "cto", Thread: "gate/resolved", Subject: "APPROVED: resolved", Kind: "answer", Created: now.Add(3 * time.Second)})
	seedNotifyMessage(t, fx.base, fx.session, "user", "new", notifyMsg{ID: "foreign", From: "qa", To: "user", Thread: "gate/foreign", Subject: "APPROVAL: foreign", Kind: "question", Created: now.Add(4 * time.Second)})
	seedNotifyMessage(t, fx.base, fx.session, "user", "cur", notifyMsg{ID: "reraised-q1", From: "cto", To: "user", Thread: "gate/reraised", Subject: "APPROVAL: first", Kind: "question", Created: now.Add(5 * time.Second)})
	seedNotifyMessage(t, fx.base, fx.session, "cto", "cur", notifyMsg{ID: "reraised-a", From: "user", To: "cto", Thread: "gate/reraised", Subject: "DENIED: first", Kind: "answer", Created: now.Add(6 * time.Second)})
	seedNotifyMessage(t, fx.base, fx.session, "user", "new", notifyMsg{ID: "reraised-q2", From: "cto", To: "user", Thread: "gate/reraised", Subject: "APPROVAL: second", Kind: "question", Created: now.Add(7 * time.Second)})
	writeMemberLaunchRecord(t, fx.base, "other", "cto", launch.Record{CWD: fx.dir, Binary: "codex", Role: "cto", Root: filepath.Join(fx.base, "other"), TeamHome: fx.dir})
	seedNotifyMessage(t, fx.base, "other", "user", "new", notifyMsg{ID: "other", From: "cto", To: "user", Thread: "gate/other-session", Subject: "APPROVAL: other", Kind: "question", Created: now.Add(8 * time.Second)})
	fx.installSnapshot(t)

	audits := 0
	previousAudit := waitPostureAppendAudit
	waitPostureAppendAudit = func(waitPostureAuditRecord) error { audits++; return nil }
	t.Cleanup(func() { waitPostureAppendAudit = previousAudit })

	err := guardOwnedWait(fx.request(30 * time.Second))
	if err == nil {
		t.Fatal("pending caller gates should refuse the wait")
	}
	got := err.Error()
	for _, want := range []string{"gate/a", "gate/reraised", "gate/z", "Park/end the turn"} {
		if !strings.Contains(got, want) {
			t.Fatalf("error missing %q: %v", want, err)
		}
	}
	for _, excluded := range []string{"gate/resolved", "gate/foreign", "gate/other-session"} {
		if strings.Contains(got, excluded) {
			t.Fatalf("error includes nonmatching gate %q: %v", excluded, err)
		}
	}
	if audits != 0 {
		t.Fatalf("default refusal wrote %d audit records, want 0", audits)
	}
}

func TestGuardOwnedWaitGateTerminalLifecycleIntegration(t *testing.T) {
	fx := newWaitPostureFixture(t, team.OperatorInteractionLeadPane)
	now := time.Date(2026, 7, 15, 9, 0, 0, 0, time.UTC)
	seedNotifyMessage(t, fx.base, fx.session, "user", "new", notifyMsg{ID: "answered-q", From: "cto", To: "user", Thread: "gate/answered", Subject: "APPROVAL: answered", Kind: "question", Created: now})
	seedNotifyMessage(t, fx.base, fx.session, "cto", "cur", notifyMsg{ID: "answered-a", From: "user", To: "cto", Thread: "gate/answered", Subject: "APPROVED: answered", Kind: "answer", Created: now.Add(time.Second)})

	for i, terminal := range []string{"closed", "withdrawn"} {
		thread := "gate/" + terminal
		requestID := terminal + "-q"
		seedNotifyMessage(t, fx.base, fx.session, "user", "new", notifyMsg{ID: requestID, From: "cto", To: "user", Thread: thread, Subject: "APPROVAL: " + terminal, Kind: "question", Created: now.Add(time.Duration(2+i*2) * time.Second)})
		seedWaitPostureContextMessage(t, fx.base, fx.session, "user", "new", map[string]any{
			"schema": 1, "id": terminal + "-terminal", "from": "cto", "to": []string{"user"}, "thread": thread,
			"subject": strings.ToUpper(terminal) + ": no longer pending", "kind": "status", "created": now.Add(time.Duration(3+i*2) * time.Second).Format(time.RFC3339Nano),
			"reply_to": requestID,
			"context":  map[string]any{"gate": map[string]any{"state": terminal, "request_message_id": requestID, "requester": "cto", "thread": thread, "actor": "cto"}},
		})
	}

	seedNotifyMessage(t, fx.base, fx.session, "user", "new", notifyMsg{ID: "stale-q1", From: "cto", To: "user", Thread: "gate/stale-terminal", Subject: "APPROVAL: old generation", Kind: "question", Created: now.Add(6 * time.Second)})
	seedNotifyMessage(t, fx.base, fx.session, "user", "new", notifyMsg{ID: "stale-q2", From: "cto", To: "user", Thread: "gate/stale-terminal", Subject: "APPROVAL: current generation", Kind: "question", Created: now.Add(7 * time.Second)})
	seedWaitPostureContextMessage(t, fx.base, fx.session, "user", "new", map[string]any{
		"schema": 1, "id": "stale-close", "from": "cto", "to": []string{"user"}, "thread": "gate/stale-terminal",
		"subject": "CLOSED: old generation", "kind": "status", "created": now.Add(8 * time.Second).Format(time.RFC3339Nano),
		"reply_to": "stale-q1",
		"context":  map[string]any{"gate": map[string]any{"state": "closed", "request_message_id": "stale-q1", "requester": "cto", "thread": "gate/stale-terminal", "actor": "cto"}},
	})

	seedNotifyMessage(t, fx.base, fx.session, "user", "new", notifyMsg{ID: "reraised-q1", From: "cto", To: "user", Thread: "gate/reraised-terminal", Subject: "APPROVAL: first", Kind: "question", Created: now.Add(9 * time.Second)})
	seedNotifyMessage(t, fx.base, fx.session, "cto", "cur", notifyMsg{ID: "reraised-a", From: "user", To: "cto", Thread: "gate/reraised-terminal", Subject: "DENIED: first", Kind: "answer", Created: now.Add(10 * time.Second)})
	seedNotifyMessage(t, fx.base, fx.session, "user", "new", notifyMsg{ID: "reraised-q2", From: "cto", To: "user", Thread: "gate/reraised-terminal", Subject: "APPROVAL: second", Kind: "question", Created: now.Add(11 * time.Second)})
	fx.installSnapshot(t)

	err := guardOwnedWait(fx.request(30 * time.Second))
	if err == nil {
		t.Fatal("open stale-terminal and re-raised generations should refuse")
	}
	got := err.Error()
	for _, want := range []string{"gate/stale-terminal", "gate/reraised-terminal"} {
		if !strings.Contains(got, want) {
			t.Fatalf("terminal lifecycle error missing %q: %v", want, err)
		}
	}
	for _, terminal := range []string{"gate/answered", "gate/closed", "gate/withdrawn"} {
		if strings.Contains(got, terminal) {
			t.Fatalf("terminal lifecycle error includes %q: %v", terminal, err)
		}
	}
}

func TestGuardOwnedWaitTimeoutBoundaryAndOverrideAudit(t *testing.T) {
	fx := newWaitPostureFixture(t, team.OperatorInteractionLeadPane)
	fx.installSnapshot(t)
	if err := guardOwnedWait(fx.request(120 * time.Second)); err != nil {
		t.Fatalf("exact 120s wait should pass: %v", err)
	}
	if err := guardOwnedWait(fx.request(120*time.Second + time.Nanosecond)); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("over-limit wait error = %v", err)
	}
	unbounded := fx.request(0)
	unbounded.Unbounded = true
	if err := guardOwnedWait(unbounded); err == nil || !strings.Contains(err.Error(), "unbounded") {
		t.Fatalf("unbounded wait error = %v", err)
	}

	missing := fx.request(121 * time.Second)
	missing.Override = true
	if err := guardOwnedWait(missing); err == nil || !strings.Contains(err.Error(), "--wait-posture-reason") {
		t.Fatalf("missing override reason error = %v", err)
	}

	var audit waitPostureAuditRecord
	previousAudit := waitPostureAppendAudit
	waitPostureAppendAudit = func(rec waitPostureAuditRecord) error { audit = rec; return nil }
	t.Cleanup(func() { waitPostureAppendAudit = previousAudit })
	override := fx.request(121 * time.Second)
	override.Override = true
	override.Reason = "bounded recovery observation"
	if err := guardOwnedWait(override); err != nil {
		t.Fatalf("audited override: %v", err)
	}
	if audit.Command != "collect" || audit.WaitKind != "collect_watch" || audit.Actor != "cto" || audit.Timeout != "2m1s" || audit.Reason != override.Reason || audit.Namespace != "default/s" {
		t.Fatalf("audit = %+v", audit)
	}

	waitPostureAppendAudit = func(waitPostureAuditRecord) error { return errors.New("disk full") }
	if err := guardOwnedWait(override); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("audit failure error = %v", err)
	}
}

func TestGuardOwnedWaitPreservesOutsideModeAndNonLead(t *testing.T) {
	separate := newWaitPostureFixture(t, team.OperatorInteractionSeparateTerminal)
	if err := guardOwnedWait(separate.request(time.Hour)); err != nil {
		t.Fatalf("separate-terminal wait changed: %v", err)
	}

	leadPane := newWaitPostureFixture(t, team.OperatorInteractionLeadPane)
	req := leadPane.request(time.Hour)
	previousActor := waitPostureResolveCurrentActor
	waitPostureResolveCurrentActor = func(string, string, string, team.Team) (verifiedOperatorActor, bool, error) {
		return verifiedOperatorActor{Role: "qa", Handle: "qa", Profile: team.DefaultProfile, Session: "s", Root: leadPane.root}, true, nil
	}
	if err := guardOwnedWait(req); err != nil {
		t.Fatalf("nonlead wait changed: %v", err)
	}

	waitPostureResolveCurrentActor = func(string, string, string, team.Team) (verifiedOperatorActor, bool, error) {
		return verifiedOperatorActor{}, true, errors.New("identity unknown")
	}
	t.Cleanup(func() { waitPostureResolveCurrentActor = previousActor })
	if err := guardOwnedWait(leadPane.request(time.Second)); err == nil || !strings.Contains(err.Error(), "park/end the turn") {
		t.Fatalf("lead-pane uncertainty error = %v", err)
	}
}

func TestGuardOwnedWaitNamedProfileUsesExactSelectedRoot(t *testing.T) {
	t.Setenv("AM_ME", "cto")
	dir := t.TempDir()
	op := team.DefaultOperator()
	op.InteractionMode = team.OperatorInteractionLeadPane
	cfg := team.Team{
		Project: dir, Orchestrated: true, Lead: "cto", ExecutionMode: executionModeProjectTeam, Operator: &op,
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"}},
	}
	if err := team.WriteProfile(dir, "review", cfg); err != nil {
		t.Fatal(err)
	}
	selectedRoot := squadnamespace.AMQRoot(dir, "review", "s")
	legacyRoot := squadnamespace.AMQRoot(dir, team.DefaultProfile, "s")
	previousActor := waitPostureResolveCurrentActor
	waitPostureResolveCurrentActor = func(string, string, string, team.Team) (verifiedOperatorActor, bool, error) {
		return verifiedOperatorActor{Role: "cto", Handle: "cto", Profile: "review", Session: "s", Root: selectedRoot}, true, nil
	}
	t.Cleanup(func() { waitPostureResolveCurrentActor = previousActor })

	selected := state.Session{Name: "s", TeamProfile: "review", NamespaceID: "review/s", Root: selectedRoot}
	legacy := state.Session{Name: "s", TeamProfile: team.DefaultProfile, NamespaceID: "default/s", Root: legacyRoot, Coordination: state.Coordination{Threads: []state.ThreadSummary{{
		ID: "gate/legacy", OperatorGate: &state.OperatorGateSignal{From: "cto"},
	}}}}
	previousSnapshot := waitPostureLoadSnapshot
	waitPostureLoadSnapshot = func(string, string) (state.Snapshot, error) {
		return state.Snapshot{Sessions: []state.Session{selected, legacy}}, nil
	}
	t.Cleanup(func() { waitPostureLoadSnapshot = previousSnapshot })

	req := waitPostureRequest{Command: "collect", WaitKind: "collect_watch", ProjectDir: dir, Profile: "review", Session: "s", Root: selectedRoot, Timeout: time.Minute, Blocking: true}
	if err := guardOwnedWait(req); err != nil {
		t.Fatalf("legacy/default gate affected selected named root: %v", err)
	}

	selected.Coordination.Threads = []state.ThreadSummary{{ID: "gate/selected", OperatorGate: &state.OperatorGateSignal{From: "cto"}}}
	var audit waitPostureAuditRecord
	previousAudit := waitPostureAppendAudit
	waitPostureAppendAudit = func(rec waitPostureAuditRecord) error { audit = rec; return nil }
	t.Cleanup(func() { waitPostureAppendAudit = previousAudit })
	req.Override = true
	req.Reason = "selected-root diagnostic"
	if err := guardOwnedWait(req); err != nil {
		t.Fatalf("selected-root override: %v", err)
	}
	if audit.Profile != "review" || audit.Namespace != "review/s" || audit.Root != selectedRoot || strings.Join(audit.Gates, ",") != "gate/selected" {
		t.Fatalf("audit selected wrong namespace/gates: %+v", audit)
	}
}

func TestWriteWaitPostureAuditSerializesCompleteJSONLines(t *testing.T) {
	dir := t.TempDir()
	const writers = 32
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- writeWaitPostureAudit(waitPostureAuditRecord{
				At: time.Unix(int64(i), 0).UTC(), Command: "collect", WaitKind: "collect_watch",
				Project: dir, Profile: team.DefaultProfile, Session: "s", Namespace: "default/s",
				Root: filepath.Join(dir, ".agent-mail", "s"), Actor: "cto",
				Timeout: "2m1s", Reason: "writer",
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, team.DirName, "wait-posture-audit", team.DefaultProfile, "s.jsonl")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != writers {
		t.Fatalf("audit lines = %d, want %d", len(lines), writers)
	}
	for i, line := range lines {
		var rec waitPostureAuditRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("line %d is not complete JSON: %v\n%s", i, err, line)
		}
		if rec.Reason != "writer" || rec.Namespace != "default/s" {
			t.Fatalf("line %d record = %+v", i, rec)
		}
	}
}

func TestGuardOwnedWaitRealAuditWriteAndSyncFailuresRefuse(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func()
		want string
	}{
		{name: "write", want: "write failed", set: func() {
			waitPostureAuditWrite = func(*os.File, []byte) (int, error) { return 0, errors.New("write failed") }
		}},
		{name: "sync", want: "sync failed", set: func() {
			waitPostureAuditSync = func(*os.File) error { return errors.New("sync failed") }
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newWaitPostureFixture(t, team.OperatorInteractionLeadPane)
			fx.installSnapshot(t)
			previousAppend := waitPostureAppendAudit
			previousWrite := waitPostureAuditWrite
			previousSync := waitPostureAuditSync
			waitPostureAppendAudit = writeWaitPostureAudit
			tc.set()
			t.Cleanup(func() {
				waitPostureAppendAudit = previousAppend
				waitPostureAuditWrite = previousWrite
				waitPostureAuditSync = previousSync
			})
			req := fx.request(121 * time.Second)
			req.Override = true
			req.Reason = "failure injection"
			if err := guardOwnedWait(req); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("audit %s failure error = %v", tc.name, err)
			}
		})
	}
}

func newWaitPostureFixture(t *testing.T, mode string) waitPostureFixture {
	t.Helper()
	t.Setenv("AM_ME", "")
	dir := t.TempDir()
	chdir(t, dir)
	op := team.DefaultOperator()
	op.InteractionMode = mode
	cfg := team.Team{
		Project: dir, Orchestrated: true, Lead: "cto", ExecutionMode: executionModeProjectTeam, Operator: &op,
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"}, {Role: "qa", Binary: "claude", Handle: "qa", Session: "s"}},
	}
	if err := team.WriteProfile(dir, team.DefaultProfile, cfg); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dir, ".agent-mail")
	root := filepath.Join(base, "s")
	writeMemberLaunchRecord(t, base, "s", "cto", launch.Record{CWD: dir, Binary: "codex", Role: "cto", Root: root, TeamHome: dir})
	previousActor := waitPostureResolveCurrentActor
	waitPostureResolveCurrentActor = func(string, string, string, team.Team) (verifiedOperatorActor, bool, error) {
		return verifiedOperatorActor{Role: "cto", Handle: "cto", Profile: team.DefaultProfile, Session: "s", Root: root}, true, nil
	}
	t.Cleanup(func() { waitPostureResolveCurrentActor = previousActor })
	return waitPostureFixture{dir: dir, base: base, root: root, session: "s"}
}

// TestGuardOwnedWaitDefaultEnvActorIdentity replaces the v2 verified-pane
// identity tests: 3.0 (2a-2) identifies the lead by injected AM_ME/AM_ROOT.
func TestGuardOwnedWaitDefaultEnvActorIdentity(t *testing.T) {
	t.Run("no injected identity remains unchanged", func(t *testing.T) {
		req := installEnvWaitPostureTeam(t)
		t.Setenv("AM_ME", "")
		t.Setenv("AM_ROOT", "")
		if err := guardOwnedWait(req); err != nil {
			t.Fatalf("identity-free wait changed: %v", err)
		}
	})
	t.Run("lead identity is guarded", func(t *testing.T) {
		req := installEnvWaitPostureTeam(t)
		t.Setenv("AM_ME", "cto")
		t.Setenv("AM_ROOT", req.Root)
		if err := guardOwnedWait(req); err == nil || !strings.Contains(err.Error(), "exceeds") || !strings.Contains(err.Error(), "as lead cto") {
			t.Fatalf("lead posture error = %v", err)
		}
	})
	t.Run("nonlead identity remains unchanged", func(t *testing.T) {
		req := installEnvWaitPostureTeam(t)
		t.Setenv("AM_ME", "qa")
		t.Setenv("AM_ROOT", req.Root)
		if err := guardOwnedWait(req); err != nil {
			t.Fatalf("nonlead wait changed: %v", err)
		}
	})
	t.Run("lead handle with a foreign root fails closed", func(t *testing.T) {
		req := installEnvWaitPostureTeam(t)
		t.Setenv("AM_ME", "cto")
		t.Setenv("AM_ROOT", filepath.Join(t.TempDir(), "elsewhere"))
		if err := guardOwnedWait(req); err == nil || !strings.Contains(err.Error(), "could not be verified") {
			t.Fatalf("foreign-root posture error = %v", err)
		}
	})
}

func installEnvWaitPostureTeam(t *testing.T) waitPostureRequest {
	t.Helper()
	t.Setenv("AM_BASE_ROOT", "")
	t.Setenv("AM_SESSION", "")
	dir := t.TempDir()
	chdir(t, dir)
	op := team.DefaultOperator()
	op.InteractionMode = team.OperatorInteractionLeadPane
	cfg := team.Team{
		Project: dir, Orchestrated: true, Lead: "cto", ExecutionMode: executionModeProjectTeam, Operator: &op,
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"}, {Role: "qa", Binary: "codex", Handle: "qa", Session: "s"}},
	}
	if err := team.WriteProfile(dir, team.DefaultProfile, cfg); err != nil {
		t.Fatal(err)
	}
	root := squadnamespace.AMQRoot(dir, team.DefaultProfile, "s")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	previousActor := waitPostureResolveCurrentActor
	waitPostureResolveCurrentActor = currentEnvRosterActor
	t.Cleanup(func() { waitPostureResolveCurrentActor = previousActor })
	previousVerified := resolveVerifiedOperatorActor
	resolveVerifiedOperatorActor = defaultVerifiedOperatorActor
	t.Cleanup(func() { resolveVerifiedOperatorActor = previousVerified })
	previousSnapshot := waitPostureLoadSnapshot
	waitPostureLoadSnapshot = func(string, string) (state.Snapshot, error) {
		return state.Snapshot{Sessions: []state.Session{{Name: "s", TeamProfile: team.DefaultProfile, NamespaceID: "default/s", Root: root}}}, nil
	}
	t.Cleanup(func() { waitPostureLoadSnapshot = previousSnapshot })
	return waitPostureRequest{Command: "collect", WaitKind: "collect_watch", ProjectDir: dir, Profile: team.DefaultProfile, Session: "s", Root: root, Timeout: 121 * time.Second, Blocking: true}
}
