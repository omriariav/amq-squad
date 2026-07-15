package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
)

// withStubPaneLister swaps statusPaneLister for the test and restores it. It
// also stubs the direct pane inspector to not-found so the pane_alive
// recorded-id fallback never shells real tmux for a pane outside the scan.
func withStubPaneLister(t *testing.T, panes []tmuxpane.TmuxPane, err error) {
	t.Helper()
	prev := statusPaneLister
	statusPaneLister = func() ([]tmuxpane.TmuxPane, error) { return panes, err }
	prevInspect := statusPaneInspector
	statusPaneInspector = func(string) (tmuxpane.TmuxPane, bool) { return tmuxpane.TmuxPane{}, false }
	t.Cleanup(func() { statusPaneLister = prev; statusPaneInspector = prevInspect })
}

// TestLiveReplacementPane_SameEngineFound: a member whose recorded PID is dead
// but with a live SAME-ENGINE pane in its cwd resolves to that pane (the
// relaunched-outside-amq-squad case) and is reported live-with-re-register.
func TestLiveReplacementPane_SameEngineFound(t *testing.T) {
	m := team.Member{Role: "qa", Handle: "qa", Binary: "codex", Session: "beta"}
	rec := statusRecord{Role: "qa", Handle: "qa", Binary: "codex", CWD: "/proj"}
	withStubPaneLister(t, []tmuxpane.TmuxPane{
		{Session: "main", Window: "0", Pane: "3", Command: "codex", CWD: "/proj"},
	}, nil)

	target, ok := liveReplacementPane(m, rec, "beta")
	if !ok {
		t.Fatal("a live same-engine pane in the member cwd must be detected as a replacement")
	}
	if !strings.Contains(target, "main:0.3") {
		t.Errorf("target should point at the live pane main:0.3, got %q", target)
	}
}

// TestLiveReplacementPane_CrossEngineRejected: the conservative guard — a live
// pane of a DIFFERENT engine must NOT be attributed to the member (roster says
// claude, only a codex pane is live), so the member stays stale.
func TestLiveReplacementPane_CrossEngineRejected(t *testing.T) {
	m := team.Member{Role: "qa", Handle: "qa", Binary: "claude", Session: "beta"}
	rec := statusRecord{Role: "qa", Handle: "qa", Binary: "claude", CWD: "/proj"}
	withStubPaneLister(t, []tmuxpane.TmuxPane{
		{Session: "main", Window: "0", Pane: "3", Command: "codex", CWD: "/proj"},
	}, nil)

	if _, ok := liveReplacementPane(m, rec, "beta"); ok {
		t.Fatal("a different-engine pane must NOT be attributed to the member (stays stale)")
	}
}

// TestLiveReplacementPane_TitleTokenWins: a pane carrying the deterministic
// amq:<session>:<role> title resolves even when another same-engine pane shares
// the cwd, and even if the member's engine differs (title is authoritative).
func TestLiveReplacementPane_TitleTokenWins(t *testing.T) {
	m := team.Member{Role: "qa", Handle: "qa", Binary: "claude", Session: "beta"}
	rec := statusRecord{Role: "qa", Handle: "qa", Binary: "claude", CWD: "/proj"}
	withStubPaneLister(t, []tmuxpane.TmuxPane{
		{Session: "beta", Window: "0", Pane: "1", Command: "codex", CWD: "/proj", Title: "amq:beta:qa"},
	}, nil)

	target, ok := liveReplacementPane(m, rec, "beta")
	if !ok {
		t.Fatal("a pane stamped amq:beta:qa must resolve for member qa")
	}
	if !strings.Contains(target, "beta:0.1") {
		t.Errorf("target should point at beta:0.1, got %q", target)
	}
}

// TestLiveReplacementPane_NoPanesOrError: no panes / a lister error degrades to
// "not found" so the caller cleanly stays stale (never panics, never false-pos).
func TestLiveReplacementPane_NoPanesOrError(t *testing.T) {
	m := team.Member{Role: "qa", Handle: "qa", Binary: "codex", Session: "beta"}
	rec := statusRecord{Role: "qa", Handle: "qa", Binary: "codex", CWD: "/proj"}

	withStubPaneLister(t, nil, nil)
	if _, ok := liveReplacementPane(m, rec, "beta"); ok {
		t.Fatal("no panes must yield no replacement")
	}
	withStubPaneLister(t, nil, errStubLister)
	if _, ok := liveReplacementPane(m, rec, "beta"); ok {
		t.Fatal("a lister error must yield no replacement (degrade to stale)")
	}
}

func TestBatchReplacementPaneResolverClaimsGenericPaneOnce(t *testing.T) {
	withStubPaneLister(t, []tmuxpane.TmuxPane{
		{Session: "main", Window: "0", Pane: "3", Command: "codex", CWD: "/proj"},
	}, nil)

	resolve := newBatchReplacementPaneResolver()
	if _, ok := resolve("cto", "cto", "codex", "/proj", "beta"); !ok {
		t.Fatal("first stale role should retain the single-member generic fallback")
	}
	if target, ok := resolve("qa", "qa", "codex", "/proj", "beta"); ok {
		t.Fatalf("one physical pane must not resolve to a second stale role: %q", target)
	}
}

func TestBatchReplacementPaneResolverPreservesExactRolePriority(t *testing.T) {
	withStubPaneLister(t, []tmuxpane.TmuxPane{
		{Session: "main", Window: "0", Pane: "3", Command: "codex", CWD: "/proj"},
		{Session: "beta", Window: "1", Pane: "0", Command: "codex", CWD: "/proj", Title: "amq:beta:qa"},
	}, nil)

	resolve := newBatchReplacementPaneResolver()
	cto, ok := resolve("cto", "cto", "codex", "/proj", "beta")
	if !ok || !strings.Contains(cto, "main:0.3") {
		t.Fatalf("generic role target = %q, ok=%t", cto, ok)
	}
	qa, ok := resolve("qa", "qa", "codex", "/proj", "beta")
	if !ok || !strings.Contains(qa, "beta:1.0") {
		t.Fatalf("exact titled role target = %q, ok=%t", qa, ok)
	}
}

func TestBuildStatusRowsDoesNotMapOneReplacementPaneToEveryStaleRole(t *testing.T) {
	dir := t.TempDir()
	base := setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)
	cfg := team.Team{Project: dir, Workstream: "beta", Members: []team.Member{
		{Role: "cto", Handle: "cto", Binary: "codex", Session: "beta"},
		{Role: "qa", Handle: "qa", Binary: "codex", Session: "beta"},
	}}
	if err := team.Write(dir, cfg); err != nil {
		t.Fatal(err)
	}
	for i, role := range []string{"cto", "qa"} {
		writeMemberLaunchRecord(t, base, "beta", role, launch.Record{
			CWD: dir, Role: role, Handle: role, Binary: "codex", AgentPID: 4200 + i, StartedAt: time.Now(),
		})
	}
	withStubPaneLister(t, []tmuxpane.TmuxPane{
		{Session: "main", Window: "0", Pane: "3", Command: "codex", CWD: canonicalPath(dir)},
	}, nil)
	probe := livenessProbe(map[int]bool{}, map[int]bool{}, time.Now())
	rows := buildStatusRowsWithLocalInputDetector(cfg, team.DefaultProfile, "beta", probe, func(string) (tmuxpane.LocalInputBlocker, bool) {
		return tmuxpane.LocalInputBlocker{}, false
	})

	replacementLive, stale := 0, 0
	for _, row := range rows {
		if row.Status == statusStateLive && strings.Contains(row.Detail, "recorded pid dead") {
			replacementLive++
		}
		if row.Status == statusStateStale {
			stale++
		}
	}
	if replacementLive != 1 || stale != 1 {
		t.Fatalf("rows = %+v, want exactly one replacement-live and one stale", rows)
	}
}

func TestFourStaleRolesOneGenericPaneAgreeAcrossStatusResumeAndRunStartWizard(t *testing.T) {
	dir := t.TempDir()
	base := setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)
	roles := []string{"cto", "platform-dev", "runtime-dev", "qa"}
	members := make([]team.Member, 0, len(roles))
	for i, role := range roles {
		members = append(members, team.Member{Role: role, Handle: role, Binary: "codex", Session: "issue-475"})
		writeMemberLaunchRecord(t, base, "issue-475", role, launch.Record{
			CWD: dir, Role: role, Handle: role, Binary: "codex", AgentPID: 4700 + i, StartedAt: time.Now(),
		})
	}
	cfg := team.Team{Project: dir, Workstream: "issue-475", Members: members}
	if err := team.Write(dir, cfg); err != nil {
		t.Fatal(err)
	}
	withStubPaneLister(t, []tmuxpane.TmuxPane{
		{Session: "unrelated", Window: "0", Pane: "1", Command: "codex", CWD: canonicalPath(dir)},
	}, nil)
	originalProbe := defaultDuplicateLaunchProbe
	defaultDuplicateLaunchProbe = livenessProbe(map[int]bool{}, map[int]bool{}, time.Now())
	t.Cleanup(func() { defaultDuplicateLaunchProbe = originalProbe })

	statusOut, _, err := captureOutput(t, func() error {
		return runStatus([]string{"--project", dir, "--session", "issue-475", "--json"})
	})
	if err != nil {
		t.Fatalf("status --json: %v", err)
	}
	resumeOut, _, err := captureOutput(t, func() error {
		return runResume([]string{"--project", dir, "--session", "issue-475", "--json"})
	})
	if err != nil {
		t.Fatalf("resume --json: %v", err)
	}
	statusEnv := decodeJSONEnvelope[statusEnvelopeData](t, statusOut)
	resumeEnv := decodeJSONEnvelope[resumeEnvelopeData](t, resumeOut)
	statusByRole := make(map[string]statusState, len(statusEnv.Data.Records))
	for _, row := range statusEnv.Data.Records {
		statusByRole[row.Role] = row.Status
	}
	live, stale, restore := 0, 0, 0
	for _, plan := range resumeEnv.Data.Plan {
		if plan.Liveness == nil {
			t.Fatalf("resume row %s omitted liveness", plan.Role)
		}
		if got := statusByRole[plan.Role]; got != statusState(plan.Liveness.Status) {
			t.Fatalf("status/resume disagree for %s: %s vs %s", plan.Role, got, plan.Liveness.Status)
		}
		switch plan.Liveness.Status {
		case string(statusStateLive):
			live++
		case string(statusStateStale):
			stale++
		}
		if plan.Action == string(resumeRestore) {
			restore++
		}
	}
	if live != 1 || stale != 3 || restore != 3 {
		t.Fatalf("public classification live=%d stale=%d restore=%d\nstatus=%s\nresume=%s", live, stale, restore, statusOut, resumeOut)
	}

	summary := discoverRunStartWizardSession(cfg, team.DefaultProfile, "issue-475", runwizard.SessionSourceMemberPin, []string{"issue-475"}, nil)
	if summary.Live != 1 || summary.Restore != 3 || summary.Classification.State != runwizard.RunStatePartly || summary.Classification.Backend != runwizard.BackendResume || !summary.Classification.Executable || !summary.Classification.RestoreExisting {
		t.Fatalf("run-start wizard must offer restoration, not read-only running: %+v", summary)
	}
}

var errStubLister = stubErr("tmux unavailable")

type stubErr string

func (e stubErr) Error() string { return string(e) }
