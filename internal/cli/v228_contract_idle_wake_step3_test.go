// AC13 session-notifier bodies, written against the step-3 seams in
// session_notifier.go. The audit at 829a9a4 found both seams the spec asked for:
//
//   - sessionNotifierSendKeys func(paneID, keys string) error, injected via
//     sessionNotifierExecution.SendKeys and threaded into
//     notifySessionInboxArrival — so a nudge is observable as (paneID, keys)
//     with no real tmux;
//   - package vars sessionNotifierSpawn / sessionNotifierPIDAlive /
//     sessionNotifierProcessMatch / sessionNotifierNow / sessionNotifierSleep,
//     which make reconcileSessionNotifierStarted and stopSessionNotifier
//     drivable without spawning a real process.
//
// Those symbols arrive with step 3, so this file is gated behind the v228step3
// build tag. P3 INTEGRATION STEP: delete the //go:build line above.
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// v228SeedInboxMessage drops a message into handle's inbox/new, the exact layout
// notifySessionInboxArrival recognizes (agents/<handle>/inbox/new/<file>).
func v228SeedInboxMessage(t *testing.T, root, handle, name string) string {
	t.Helper()
	dir := filepath.Join(root, "agents", handle, "inbox", "new")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("---json\n{\"id\":\""+name+"\"}\n---\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestV228ContractIdleAgentActsOnMessageWithoutKeystrokes(t *testing.T) {
	requireV228Contract(t)
	project := canonicalFilesystemPath(t.TempDir())
	const (
		profile = "v228"
		session = "ac13"
		handle  = "dev"
		pid     = 6101
		paneID  = "%77"
	)
	v228SeedProfile(t, project, profile, session, v228StartMembers(session, "cto", handle))
	root := v228CanonicalRoot(project, profile, session)
	tmuxInfo := &launch.TmuxInfo{Session: session, WindowID: "@7", PaneID: paneID, Target: "new-session"}
	// Idle for two hours: idleness must not suppress the nudge.
	v228SeedLiveRecord(t, root, launch.Record{
		Schema: launch.SchemaVersion, Binary: "codex",
		Role: handle, Handle: handle, Session: session,
		TeamProfile: profile, TeamHome: project, CWD: project,
		Root: root, BaseRoot: filepath.Dir(root),
		AgentPID: pid, StartedAt: v228Now.Add(-2 * time.Hour),
		Tmux: tmuxInfo, Terminal: launch.TerminalInfoFromTmux(tmuxInfo),
	})

	type nudge struct{ pane, keys string }
	var nudges []nudge
	sendKeys := func(pane, keys string) error {
		nudges = append(nudges, nudge{pane, keys})
		return nil
	}

	messagePath := v228SeedInboxMessage(t, root, handle, "m1.md")
	seen := map[string]struct{}{}
	nudged, err := notifySessionInboxArrival(root, profile, session, messagePath, seen, sendKeys)
	if err != nil {
		t.Fatalf("inbox arrival for an idle agent: %v", err)
	}
	if !nudged {
		t.Fatal("a message for an idle agent produced no nudge")
	}
	if len(nudges) != 1 {
		t.Fatalf("nudges = %+v, want exactly one", nudges)
	}
	// Record-first: the pane comes from launch.json, never from a scan.
	if nudges[0].pane != paneID {
		t.Errorf("nudge went to pane %q, want the recorded %q", nudges[0].pane, paneID)
	}
	// Zero human keystrokes: the notifier supplies the whole input.
	if strings.TrimSpace(nudges[0].keys) == "" {
		t.Error("nudge carried no keys; the agent would need a human keystroke to act")
	}
	if nudges[0].keys != dispatchNudgePrompt(root) {
		t.Errorf("nudge keys = %q, want the standard drain instruction %q", nudges[0].keys, dispatchNudgePrompt(root))
	}

	// Same arrival again must not double-nudge.
	again, err := notifySessionInboxArrival(root, profile, session, messagePath, seen, sendKeys)
	if err != nil {
		t.Fatal(err)
	}
	if again || len(nudges) != 1 {
		t.Errorf("second delivery of the same message nudged again: %+v", nudges)
	}

	// A second, distinct message nudges once more: one nudge per arrival.
	next := v228SeedInboxMessage(t, root, handle, "m2.md")
	if _, err := notifySessionInboxArrival(root, profile, session, next, seen, sendKeys); err != nil {
		t.Fatal(err)
	}
	if len(nudges) != 2 {
		t.Errorf("nudges after a second arrival = %+v, want two", nudges)
	}

	// A handle with no launch record is an error, not a scan fallback: guessing a
	// pane is what woke the wrong agent in 2.27.
	orphan := v228SeedInboxMessage(t, root, "ghost", "m3.md")
	if _, err := notifySessionInboxArrival(root, profile, session, orphan, seen, sendKeys); err == nil {
		t.Error("a recordless handle must fail closed rather than fall back to a pane scan")
	}
	if len(nudges) != 2 {
		t.Errorf("recordless handle produced a nudge: %+v", nudges)
	}
}

// v228NotifierFixture is a project whose canonical root holds one recorded,
// pane-owning agent — the minimum a notifier needs to have somewhere to nudge.
type v228NotifierFixture struct {
	Project string
	Profile string
	Session string
	Root    string
	Handle  string
	PaneID  string
}

func v228NewNotifierFixture(t *testing.T, session, handle, paneID string, pid int) v228NotifierFixture {
	t.Helper()
	project := canonicalFilesystemPath(t.TempDir())
	const profile = "v228"
	v228SeedProfile(t, project, profile, session, v228StartMembers(session, handle))
	root := v228CanonicalRoot(project, profile, session)
	info := &launch.TmuxInfo{Session: session, WindowID: "@9", PaneID: paneID, Target: "new-session"}
	v228SeedLiveRecord(t, root, launch.Record{
		Schema: launch.SchemaVersion, Binary: "codex",
		Role: handle, Handle: handle, Session: session,
		TeamProfile: profile, TeamHome: project, CWD: project,
		Root: root, BaseRoot: filepath.Dir(root),
		AgentPID: pid, StartedAt: v228Now.Add(-time.Hour),
		Tmux: info, Terminal: launch.TerminalInfoFromTmux(info),
	})
	return v228NotifierFixture{
		Project: project, Profile: profile, Session: session,
		Root: root, Handle: handle, PaneID: paneID,
	}
}

// v228RunNotifierUntil starts a real notifier, waits for wantNudges nudges, stops
// it, and returns every pane nudged (including any that arrived after the wait
// was satisfied). The bounded wait is a failure bound only: ordering never
// depends on it, and a lost nudge fails the test rather than passing slowly.
func v228RunNotifierUntil(t *testing.T, fixture v228NotifierFixture, token string, wantNudges int) []string {
	t.Helper()
	nudged := make(chan string, 32)
	stop := make(chan os.Signal, 1)
	done := make(chan error, 1)
	go func() {
		done <- executeSessionNotifier(sessionNotifierExecution{
			ProjectDir: fixture.Project, Profile: fixture.Profile, Session: fixture.Session,
			Root: fixture.Root, Token: token,
			// Long TTL/heartbeat: this test is about inbox snapshots, not leases.
			TTL: time.Hour, Heartbeat: time.Hour,
			Stop: stop, Now: func() time.Time { return v228Now },
			NewWatcher: fsnotify.NewWatcher,
			SendKeys: func(pane, _ string) error {
				nudged <- pane
				return nil
			},
		})
	}()

	var panes []string
	for i := 0; i < wantNudges; i++ {
		select {
		case pane := <-nudged:
			panes = append(panes, pane)
		case <-time.After(v228ContractWaitBudget):
			stop <- syscall.SIGTERM
			<-done
			t.Fatalf("notifier delivered %d of %d expected nudges within %s. Either a pending inbox message was never announced (wake loss — the defect this test guards), "+
				"or the machine was saturated and the notifier had not reached it yet. Check load before filing this as a wake-loss regression.",
				len(panes), wantNudges, v228ContractWaitBudget)
		}
	}
	stop <- syscall.SIGTERM
	if err := <-done; err != nil {
		t.Fatalf("notifier exited with error: %v", err)
	}
	for {
		select {
		case pane := <-nudged:
			panes = append(panes, pane)
		default:
			return panes
		}
	}
}

// A message already sitting in inbox/new when the notifier starts MUST be
// announced. inbox/new is the pending-work set, so a message that arrived while
// the notifier was down is still pending work — skipping it is wake loss, which
// is the 2.27 bug class this criterion exists to prevent.
//
// Semantics per senior-dev's ruling: exactly-once WITHIN one notifier process,
// at-least-once ACROSS a restart. A duplicate wake is accepted; a lost wake is not.
func TestV228ContractNotifierAnnouncesPendingInboxOnStartup(t *testing.T) {
	requireV228Contract(t)
	fixture := v228NewNotifierFixture(t, "ac13", "dev", "%88", 6121)

	// Seeded BEFORE the notifier's first snapshot: this is the arrival that was
	// missed while it was down.
	v228SeedInboxMessage(t, fixture.Root, fixture.Handle, "pending-1.md")

	first := v228RunNotifierUntil(t, fixture, "token-run-1", 1)
	// Exactly-once within one process: one pending message, one nudge.
	if len(first) != 1 {
		t.Fatalf("startup nudges = %v, want exactly one for the pending message", first)
	}
	if first[0] != fixture.PaneID {
		t.Errorf("startup nudge went to pane %q, want the recorded %q", first[0], fixture.PaneID)
	}

	// Restart with the already-announced message still pending, plus a second one
	// that arrived while this notifier was down.
	v228SeedInboxMessage(t, fixture.Root, fixture.Handle, "pending-2.md")
	second := v228RunNotifierUntil(t, fixture, "token-run-2", 1)

	// At-least-once across the restart boundary. Re-announcing pending-1 is
	// ALLOWED (duplicate wake beats wake loss), so the count is a range, not an
	// equality: the range IS the contract. What is forbidden is zero.
	if len(second) < 1 {
		t.Fatalf("restart announced nothing; pending inbox work must survive a notifier restart")
	}
	if len(second) > 2 {
		t.Errorf("restart produced %d nudges for 2 pending messages: %v", len(second), second)
	}
	for _, pane := range second {
		if pane != fixture.PaneID {
			t.Errorf("restart nudge went to pane %q, want the recorded %q", pane, fixture.PaneID)
		}
	}
}

func TestV228ContractNotifierIsPerSessionAndSpawnedByStart(t *testing.T) {
	requireV228Contract(t)
	project := canonicalFilesystemPath(t.TempDir())
	const (
		profile = "v228"
		session = "ac13"
		notiPID = 6111
	)
	members := v228StartMembers(session, "cto", "dev")
	v228SeedProfile(t, project, profile, session, members)
	tm, err := team.ReadProfile(project, profile)
	if err != nil {
		t.Fatal(err)
	}

	now := v228Now
	restoreNow, restoreSleep := sessionNotifierNow, sessionNotifierSleep
	restoreAlive, restoreMatch := sessionNotifierPIDAlive, sessionNotifierProcessMatch
	restoreSpawn, restoreSignal := sessionNotifierSpawn, notificationWatcherSignal
	t.Cleanup(func() {
		sessionNotifierNow, sessionNotifierSleep = restoreNow, restoreSleep
		sessionNotifierPIDAlive, sessionNotifierProcessMatch = restoreAlive, restoreMatch
		sessionNotifierSpawn, notificationWatcherSignal = restoreSpawn, restoreSignal
	})
	sessionNotifierNow = func() time.Time { return now }
	sessionNotifierSleep = func(time.Duration) {}
	notifierAlive := false
	sessionNotifierPIDAlive = func(pid int) bool { return notifierAlive && pid == notiPID }
	sessionNotifierProcessMatch = func(pid int, predicate func(string) bool) bool {
		return notifierAlive && pid == notiPID
	}
	notificationWatcherSignal = func(int, os.Signal) error { notifierAlive = false; return nil }

	// The spawn stub stands in for the child process: it publishes the healthy
	// lease the real _session-notify writes on startup.
	spawns := 0
	sessionNotifierSpawn = func(project, profile, session, root, token string) (notificationWatcherProcess, error) {
		spawns++
		notifierAlive = true
		path := sessionNotifierRuntimePath(project, profile, session)
		if err := writeSessionNotifierRecord(path, sessionNotifierRecord{
			SchemaVersion: sessionNotifierSchema, ProjectDir: project, Profile: profile,
			Session: session, Root: root, PID: notiPID, OwnerToken: token,
			Expected: true, Health: "healthy",
			HeartbeatAt: now, LeaseExpiresAt: now.Add(sessionNotifierTTL),
		}); err != nil {
			return nil, err
		}
		return nil, nil
	}

	// start reconciles the notifier: one per session.
	if err := reconcileSessionNotifierStarted(tm, profile, session, ""); err != nil {
		t.Fatalf("first notifier reconcile: %v", err)
	}
	if spawns != 1 {
		t.Fatalf("notifier spawns = %d, want 1", spawns)
	}

	// A rolled-forward rerun ADOPTS the healthy one. A second notifier would
	// double-nudge every message.
	if err := reconcileSessionNotifierStarted(tm, profile, session, ""); err != nil {
		t.Fatalf("second notifier reconcile: %v", err)
	}
	if spawns != 1 {
		t.Fatalf("rerun started another notifier: spawns = %d, want 1", spawns)
	}

	root := v228CanonicalRoot(project, profile, session)
	if status := inspectSessionNotifier(project, profile, session, root, now); status.Health != "healthy" {
		t.Fatalf("notifier health = %q (%s), want healthy", status.Health, status.Reason)
	}

	// down stops it, and reports that it had something to stop.
	stopped, err := stopSessionNotifier(project, profile, session)
	if err != nil {
		t.Fatalf("stop notifier: %v", err)
	}
	if !stopped {
		t.Error("stop reported nothing to stop for an expected, healthy notifier")
	}
	status := inspectSessionNotifier(project, profile, session, root, now)
	if status.Health == "healthy" {
		t.Errorf("notifier still healthy after stop: %+v", status.Record)
	}
	if status.Record.PID != 0 || strings.TrimSpace(status.Record.OwnerToken) != "" || status.Record.Expected {
		t.Errorf("stopped notifier record still claims ownership: %+v", status.Record)
	}
}
