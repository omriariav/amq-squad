package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestSessionNotifierNudgesRecordedPaneExactlyOncePerMessage(t *testing.T) {
	project := canonicalFilesystemPath(t.TempDir())
	const (
		profile = "review"
		session = "wake"
		handle  = "dev"
		paneID  = "%77"
	)
	root := filepath.Join(project, ".agent-mail", profile, session)
	agentDir := filepath.Join(root, "agents", handle)
	if err := os.MkdirAll(filepath.Join(agentDir, "inbox", "new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := launch.Write(agentDir, launch.Record{
		Schema: launch.SchemaVersion, Role: handle, Handle: handle, Binary: "codex",
		Session: session, TeamProfile: profile, TeamHome: project, CWD: project,
		Root: root, BaseRoot: filepath.Dir(root),
		Tmux: &launch.TmuxInfo{PaneID: paneID, Session: session},
	}); err != nil {
		t.Fatal(err)
	}
	message := filepath.Join(agentDir, "inbox", "new", "m1.md")
	if err := os.WriteFile(message, []byte("message"), 0o600); err != nil {
		t.Fatal(err)
	}
	type nudge struct{ pane, keys string }
	var nudges []nudge
	seen := map[string]struct{}{}
	sendKeys := func(pane, keys string) error {
		nudges = append(nudges, nudge{pane: pane, keys: keys})
		return nil
	}
	for i := 0; i < 2; i++ {
		nudged, err := notifySessionInboxArrival(root, profile, session, message, seen, sendKeys)
		if err != nil {
			t.Fatal(err)
		}
		if nudged != (i == 0) {
			t.Fatalf("arrival %d nudged=%t, want %t", i, nudged, i == 0)
		}
	}
	if len(nudges) != 1 || nudges[0].pane != paneID {
		t.Fatalf("nudges = %+v, want exactly one to recorded pane %s", nudges, paneID)
	}
	if !strings.Contains(nudges[0].keys, root) || !strings.Contains(nudges[0].keys, "amq drain --include-body") {
		t.Fatalf("nudge does not identify the exact inbox root: %q", nudges[0].keys)
	}
}

func TestSessionNotifierRetriesFailedNudgeWithoutDoubleSending(t *testing.T) {
	project := canonicalFilesystemPath(t.TempDir())
	const (
		profile = "review"
		session = "wake"
		handle  = "dev"
	)
	root := filepath.Join(project, ".agent-mail", profile, session)
	agentDir := filepath.Join(root, "agents", handle)
	if err := os.MkdirAll(filepath.Join(agentDir, "inbox", "new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := launch.Write(agentDir, launch.Record{
		Schema: launch.SchemaVersion, Role: handle, Handle: handle, Binary: "codex",
		Session: session, TeamProfile: profile, TeamHome: project, CWD: project,
		Root: root, BaseRoot: filepath.Dir(root), Tmux: &launch.TmuxInfo{PaneID: "%8"},
	}); err != nil {
		t.Fatal(err)
	}
	message := filepath.Join(agentDir, "inbox", "new", "m2.md")
	if err := os.WriteFile(message, []byte("message"), 0o600); err != nil {
		t.Fatal(err)
	}
	seen := map[string]struct{}{}
	attempts := 0
	sendKeys := func(string, string) error {
		attempts++
		if attempts == 1 {
			return errors.New("temporary tmux failure")
		}
		return nil
	}
	if nudged, err := notifySessionInboxArrival(root, profile, session, message, seen, sendKeys); err == nil || nudged {
		t.Fatalf("first arrival nudged=%t err=%v, want retryable failure", nudged, err)
	}
	if nudged, err := notifySessionInboxArrival(root, profile, session, message, seen, sendKeys); err != nil || !nudged {
		t.Fatalf("retry arrival nudged=%t err=%v, want successful nudge", nudged, err)
	}
	if nudged, err := notifySessionInboxArrival(root, profile, session, message, seen, sendKeys); err != nil || nudged {
		t.Fatalf("post-success arrival nudged=%t err=%v, want deduped", nudged, err)
	}
	if attempts != 2 {
		t.Fatalf("send attempts=%d, want one failure plus one success", attempts)
	}
}

func TestSessionNotifierStartupNudgesPendingInboxMessage(t *testing.T) {
	project := canonicalFilesystemPath(t.TempDir())
	const (
		profile = "review"
		session = "wake"
		handle  = "dev"
		paneID  = "%91"
	)
	root := filepath.Join(project, ".agent-mail", profile, session)
	agentDir := filepath.Join(root, "agents", handle)
	if err := os.MkdirAll(filepath.Join(agentDir, "inbox", "new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := launch.Write(agentDir, launch.Record{
		Schema: launch.SchemaVersion, Role: handle, Handle: handle, Binary: "codex",
		Session: session, TeamProfile: profile, TeamHome: project, CWD: project,
		Root: root, BaseRoot: filepath.Dir(root),
		Tmux: &launch.TmuxInfo{PaneID: paneID, Session: session},
	}); err != nil {
		t.Fatal(err)
	}
	message := filepath.Join(agentDir, "inbox", "new", "pending.md")
	if err := os.WriteFile(message, []byte("pending"), 0o600); err != nil {
		t.Fatal(err)
	}

	stop := make(chan os.Signal, 1)
	nudges := 0
	err := executeSessionNotifier(sessionNotifierExecution{
		ProjectDir: project, Profile: profile, Session: session, Root: root, Token: "startup-catch-up",
		TTL: time.Minute, Heartbeat: time.Hour, Stop: stop, Now: time.Now,
		SendKeys: func(gotPane, _ string) error {
			if gotPane != paneID {
				t.Fatalf("nudge pane = %q, want %q", gotPane, paneID)
			}
			nudges++
			stop <- os.Interrupt
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if nudges != 1 {
		t.Fatalf("startup nudges = %d, want exactly one for pending inbox message", nudges)
	}
}

func TestSessionNotifierHeartbeatRescansUnseenInboxMessage(t *testing.T) {
	project := canonicalFilesystemPath(t.TempDir())
	const (
		profile = "review"
		session = "wake"
		handle  = "dev"
		paneID  = "%92"
	)
	root := filepath.Join(project, ".agent-mail", profile, session)
	agentDir := filepath.Join(root, "agents", handle)
	if err := os.MkdirAll(filepath.Join(agentDir, "inbox", "new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := launch.Write(agentDir, launch.Record{
		Schema: launch.SchemaVersion, Role: handle, Handle: handle, Binary: "codex",
		Session: session, TeamProfile: profile, TeamHome: project, CWD: project,
		Root: root, BaseRoot: filepath.Dir(root), Tmux: &launch.TmuxInfo{PaneID: paneID, Session: session},
	}); err != nil {
		t.Fatal(err)
	}
	message := filepath.Join(agentDir, "inbox", "new", "missed-event.md")
	if err := os.WriteFile(message, []byte("pending"), 0o600); err != nil {
		t.Fatal(err)
	}
	seen := map[string]struct{}{}
	nudges := 0
	nudged, err := rescanSessionInboxMessages(root, profile, session, seen, func(gotPane, _ string) error {
		if gotPane != paneID {
			t.Fatalf("nudge pane = %q, want %q", gotPane, paneID)
		}
		nudges++
		return nil
	})
	if err != nil || !nudged || nudges != 1 {
		t.Fatalf("heartbeat rescan nudged=%t count=%d err=%v", nudged, nudges, err)
	}
	nudged, err = rescanSessionInboxMessages(root, profile, session, seen, func(string, string) error {
		nudges++
		return nil
	})
	if err != nil || nudged || nudges != 1 {
		t.Fatalf("deduped heartbeat rescan nudged=%t count=%d err=%v", nudged, nudges, err)
	}
}

func TestSessionNotifierRerunAdoptsExistingProcessWithoutTmux(t *testing.T) {
	project := canonicalFilesystemPath(t.TempDir())
	const (
		profile = "review"
		session = "wake"
		pid     = 8123
	)
	teamConfig := team.Team{Project: project}
	root := filepath.Join(project, ".agent-mail", profile, session)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(10_000, 0).UTC()
	previousNow, previousAlive, previousMatch, previousSpawn := sessionNotifierNow, sessionNotifierPIDAlive, sessionNotifierProcessMatch, sessionNotifierSpawn
	t.Cleanup(func() {
		sessionNotifierNow, sessionNotifierPIDAlive, sessionNotifierProcessMatch, sessionNotifierSpawn = previousNow, previousAlive, previousMatch, previousSpawn
	})
	sessionNotifierNow = func() time.Time { return now }
	sessionNotifierPIDAlive = func(got int) bool { return got == pid }
	sessionNotifierProcessMatch = func(got int, _ func(string) bool) bool { return got == pid }
	spawned := 0
	sessionNotifierSpawn = func(gotProject, gotProfile, gotSession, gotRoot, token string) (notificationWatcherProcess, error) {
		spawned++
		host, _ := os.Hostname()
		rec := sessionNotifierRecord{
			SchemaVersion: sessionNotifierSchema, ProjectDir: gotProject, Profile: gotProfile, Session: gotSession,
			Root: gotRoot, PID: pid, Host: host, OwnerToken: token, Expected: true, Health: "healthy",
			HeartbeatAt: now, LeaseExpiresAt: now.Add(sessionNotifierTTL),
		}
		if err := writeSessionNotifierRecord(sessionNotifierRuntimePath(gotProject, gotProfile, gotSession), rec); err != nil {
			return nil, err
		}
		return nil, nil
	}
	for i := 0; i < 2; i++ {
		if err := reconcileSessionNotifierStarted(teamConfig, profile, session, ""); err != nil {
			t.Fatalf("reconcile %d: %v", i, err)
		}
	}
	if spawned != 1 {
		t.Fatalf("spawn count = %d, want one notifier adopted on rerun", spawned)
	}
}

func TestStopSessionNotifierRecreatesRuntimeParentBeforeLock(t *testing.T) {
	project := canonicalFilesystemPath(t.TempDir())
	const (
		profile = "review"
		session = "wake"
		pid     = 8124
	)
	path := sessionNotifierRuntimePath(project, profile, session)
	now := time.Unix(20_000, 0).UTC()
	host, _ := os.Hostname()
	if err := writeSessionNotifierRecord(path, sessionNotifierRecord{
		SchemaVersion: sessionNotifierSchema, ProjectDir: project, Profile: profile, Session: session,
		Root: filepath.Join(project, ".agent-mail", profile, session), PID: pid, Host: host,
		OwnerToken: "stop-parent", Expected: true, Health: "healthy",
		HeartbeatAt: now, LeaseExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	previousNow, previousAlive, previousMatch, previousSignal := sessionNotifierNow, sessionNotifierPIDAlive, sessionNotifierProcessMatch, notificationWatcherSignal
	t.Cleanup(func() {
		sessionNotifierNow, sessionNotifierPIDAlive, sessionNotifierProcessMatch, notificationWatcherSignal = previousNow, previousAlive, previousMatch, previousSignal
	})
	alive := true
	sessionNotifierNow = func() time.Time { return now }
	sessionNotifierPIDAlive = func(got int) bool { return got == pid && alive }
	sessionNotifierProcessMatch = func(got int, _ func(string) bool) bool { return got == pid }
	notificationWatcherSignal = func(got int, _ os.Signal) error {
		if got != pid {
			t.Fatalf("signal pid = %d, want %d", got, pid)
		}
		alive = false
		return os.RemoveAll(filepath.Dir(path))
	}
	stopped, err := stopSessionNotifier(project, profile, session)
	if err != nil || !stopped {
		t.Fatalf("stop notifier stopped=%t err=%v", stopped, err)
	}
	rec, err := readSessionNotifierRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Expected || rec.OwnerToken != "" || rec.Health != "inactive" {
		t.Fatalf("stopped notifier record = %+v", rec)
	}
}
