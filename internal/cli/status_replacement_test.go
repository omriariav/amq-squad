package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

// withStubPaneLister swaps statusPaneLister for the test and restores it.
func withStubPaneLister(t *testing.T, panes []tmuxpane.TmuxPane, err error) {
	t.Helper()
	prev := statusPaneLister
	statusPaneLister = func() ([]tmuxpane.TmuxPane, error) { return panes, err }
	t.Cleanup(func() { statusPaneLister = prev })
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

var errStubLister = stubErr("tmux unavailable")

type stubErr string

func (e stubErr) Error() string { return string(e) }
