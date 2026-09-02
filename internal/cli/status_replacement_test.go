package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
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

func TestFourStaleRolesOneGenericPaneAgreeAcrossStatusAndResume(t *testing.T) {
	t.Skip("gh#758/t11: deferred to a named follow-up (not slice B or C, not deleted or rewritten) -- resume's plan-only path (now a thin alias for plan) no longer computes or exposes any per-member liveness/status classification of its own to compare against status --json's; it delegates that signal to launchapi's Observations entirely. The cross-command agreement invariant this test protects is real and worth keeping once status and resume's plan-only path share a single liveness source of truth again, but unifying that source of truth is a larger architectural question than gh#758's three slices cover. Tracked on task/t11 as a follow-up beyond this issue, to be filed as its own tracked item once slice C lands. Body stripped in slice B commit 3: it decoded via resumeEnvelopeData/resumeRestore, team_resume.go types deleted along with the classifier.")
}

var errStubLister = stubErr("tmux unavailable")

type stubErr string

func (e stubErr) Error() string { return string(e) }
