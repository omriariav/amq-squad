package cli

import (
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
)

func TestLaunchRuntimeIdentityProcessStartSkewBoundary(t *testing.T) {
	recordedAt := time.Now().UTC()
	rec := launch.Record{
		AgentPID: 42, Binary: "codex", AgentTTY: "/dev/ttys007",
		StartedAt: recordedAt,
	}
	startedAt := recordedAt.Add(launchProcessStartSkewEpsilon)
	probe := launchRuntimeProbe{
		PIDAlive:     func(int) bool { return true },
		ProcessMatch: func(int, func(string) bool) bool { return true },
		ProcessTTY:   func(int) (string, bool) { return "/dev/ttys007", true },
		ProcessStartTime: func(int) (time.Time, bool) {
			return startedAt, true
		},
	}
	if got := classifyLaunchRuntimeIdentity(rec, "codex", "", probe); !got.PIDLive {
		t.Fatalf("process at skew boundary classified reused: %+v", got)
	}
	startedAt = startedAt.Add(time.Nanosecond)
	if got := classifyLaunchRuntimeIdentity(rec, "codex", "", probe); got.PIDLive || got.Live {
		t.Fatalf("process just beyond skew boundary classified live: %+v", got)
	}
}

// #655: an in-place agent restart (e.g. a CLI self-upgrade re-exec) rewrites
// the pane's visible title while the pane, the pty, and the agent process all
// survive. The classifier must corroborate the pane through the process-tty
// tie instead of condemning it on the clobbered title.
func TestLaunchRuntimeIdentityPaneTTYTieSurvivesClobberedTitle(t *testing.T) {
	rec := launch.Record{
		AgentPID: 184, Binary: "claude", AgentTTY: "/dev/ttys011",
		Role: "lead", Handle: "lead", Session: "issue-655",
		StartedAt: time.Now().UTC(),
		Tmux:      &launch.TmuxInfo{PaneID: "%217", Session: "squad"},
	}
	probe := launchRuntimeProbe{
		PIDAlive:     func(pid int) bool { return pid == 184 },
		ProcessMatch: func(int, func(string) bool) bool { return true },
		ProcessTTY:   func(int) (string, bool) { return "/dev/ttys011", true },
		PaneTitle:    func(string) (string, bool) { return "✳ upgraded CLI set its own title", true },
		PaneTTY:      func(string) (string, bool) { return "/dev/ttys011", true },
	}
	got := classifyLaunchRuntimeIdentity(rec, "claude", "%217", probe)
	if !got.PIDLive || !got.PaneLive || !got.Live {
		t.Fatalf("clobbered title with matching pane tty must stay live: %+v", got)
	}
	if got.PaneTitleMatch {
		t.Fatalf("tty-tie fallback must not report a title match: %+v", got)
	}

	// A matching title stays the primary path and reports PaneTitleMatch.
	probe.PaneTitle = func(string) (string, bool) { return paneTitleToken("issue-655", "lead"), true }
	if got := classifyLaunchRuntimeIdentity(rec, "claude", "%217", probe); !got.PaneLive || !got.PaneTitleMatch {
		t.Fatalf("matching title must classify PaneLive via title: %+v", got)
	}
}

func TestLaunchRuntimeIdentityPaneTTYTieRequiresLivePIDAndSameTTY(t *testing.T) {
	rec := launch.Record{
		AgentPID: 184, Binary: "claude", AgentTTY: "/dev/ttys011",
		Role: "lead", Handle: "lead", Session: "issue-655",
		StartedAt: time.Now().UTC(),
		Tmux:      &launch.TmuxInfo{PaneID: "%217", Session: "squad"},
	}
	clobbered := func(string) (string, bool) { return "not-the-token", true }

	// The pane's pty differs from the live agent's tty: after a tmux server
	// restart a reused pane id can point at an unrelated pane. Not live.
	probe := launchRuntimeProbe{
		PIDAlive:     func(int) bool { return true },
		ProcessMatch: func(int, func(string) bool) bool { return true },
		ProcessTTY:   func(int) (string, bool) { return "/dev/ttys011", true },
		PaneTitle:    clobbered,
		PaneTTY:      func(string) (string, bool) { return "/dev/ttys042", true },
	}
	if got := classifyLaunchRuntimeIdentity(rec, "claude", "%217", probe); got.PaneLive {
		t.Fatalf("mismatched pane tty must not classify PaneLive: %+v", got)
	}

	// A dead recorded pid has no tty to tie: the fallback must stay inert.
	probe.PaneTTY = func(string) (string, bool) { return "/dev/ttys011", true }
	probe.PIDAlive = func(int) bool { return false }
	if got := classifyLaunchRuntimeIdentity(rec, "claude", "%217", probe); got.PaneLive || got.Live {
		t.Fatalf("dead pid must not classify PaneLive through the tty tie: %+v", got)
	}

	// An unavailable pane tty (gone pane, sandboxed tmux) must stay inert.
	probe.PIDAlive = func(int) bool { return true }
	probe.PaneTTY = func(string) (string, bool) { return "", false }
	if got := classifyLaunchRuntimeIdentity(rec, "claude", "%217", probe); got.PaneLive {
		t.Fatalf("unavailable pane tty must not classify PaneLive: %+v", got)
	}
}
