package cli

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
)

// AC13: a message sent to an agent idle for 10+ minutes is acted on with ZERO
// human keystrokes in its pane. This is the release blocker in the plan — the
// 2.27 "idle agents go deaf" class.
//
// The contract, per the seamlessness bar: `start` spawns one session notifier
// per session. It watches the canonical root's inboxes and, on a new message for
// role X, nudges X's pane looked up from launch.json (record-first). Its only
// dependency is tmux send-keys, which spawn already relies on. Sending IS
// waking: the lead wakes a worker just by sending.
//
// The two behavioral bodies live in v228_contract_idle_wake_step3_test.go,
// written against the step-3 notifier seams and gated behind the v228step3 build
// tag. This file keeps the record-first pane lookup, pinnable on any base,
// because that is the part that regressed in 2.27.

// TestV228ContractWakeTargetIsResolvedFromTheLaunchRecord pins the half AC13
// rests on and which is checkable today: the pane a wake targets is the one the
// launch record names. The notifier is new, but this lookup is not, and it is
// where the 2.27 wake-loss bug lived.
func TestV228ContractWakeTargetIsResolvedFromTheLaunchRecord(t *testing.T) {
	requireV228Contract(t)
	setupFakeAMQSessionRoots(t)
	swapStatusPaneLister(t, nil, nil)

	project := canonicalFilesystemPath(t.TempDir())
	const (
		profile = "v228"
		session = "ac13"
		handle  = "dev"
		pid     = 5201
		paneID  = "%77"
	)
	v228SeedProfile(t, project, profile, session, v228StartMembers(session, "cto", handle))
	root := v228CanonicalRoot(project, profile, session)
	tmuxInfo := &launch.TmuxInfo{Session: session, WindowID: "@7", PaneID: paneID, Target: "new-session"}
	// Idle for well over ten minutes.
	v228SeedLiveRecord(t, root, launch.Record{
		Schema: launch.SchemaVersion, Binary: "codex",
		Role: handle, Handle: handle, Session: session,
		TeamProfile: profile, TeamHome: project, CWD: project,
		Root: root, BaseRoot: filepath.Dir(root),
		AgentPID: pid, StartedAt: v228Now.Add(-2 * time.Hour),
		Tmux: tmuxInfo, Terminal: launch.TerminalInfoFromTmux(tmuxInfo),
	})

	rec, err := launch.Read(filepath.Join(root, "agents", handle))
	if err != nil {
		t.Fatal(err)
	}
	resolved := tmuxRuntimeFromInfo(rec.Tmux)
	if resolved == nil || resolved.PaneID != paneID {
		t.Fatalf("wake target resolved from record = %+v, want pane %s", resolved, paneID)
	}
	// An idle agent is still live: idleness must never downgrade liveness, or the
	// notifier would decide there is nothing to nudge.
	identity := classifyLaunchRuntimeIdentity(rec, "codex", "", launchRuntimeProbe{
		PIDAlive:     func(got int) bool { return got == pid },
		ProcessMatch: func(int, func(string) bool) bool { return true },
		ProcessTTY:   func(int) (string, bool) { return "", false },
	})
	if !identity.Live || !identity.PIDLive {
		t.Fatalf("a 2-hour-idle agent classified as not live: %+v", identity)
	}
}
