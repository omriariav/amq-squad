package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func writeSessionNotifierMessage(t *testing.T, path, id string) {
	t.Helper()
	body := `---json
{"schema":1,"id":"` + id + `","from":"lead","to":["dev"],"thread":"task/test","created":"2026-08-09T00:00:00Z","kind":"todo"}
---
body
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func sessionNotifierWakeAbsent(string, string, string, string, string, launch.Record) bool {
	return false
}

func TestSessionNotifierUnverifiedWakeFallsBackExactlyOncePerMessage(t *testing.T) {
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
	writeSessionNotifierMessage(t, message, "m1")
	type nudge struct{ pane, keys string }
	var nudges []nudge
	ledger := newSessionNotifierAttemptLedger(nil)
	sendKeys := func(pane, keys string) error {
		nudges = append(nudges, nudge{pane: pane, keys: keys})
		return nil
	}
	for i := 0; i < 2; i++ {
		nudged, err := notifySessionInboxArrival(root, profile, session, message, ledger, sessionNotifierWakeAbsent, sendKeys)
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

func TestSessionNotifierAttemptLedgerNamespacesMessageIDsByHandle(t *testing.T) {
	ledger := newSessionNotifierAttemptLedger(nil)
	for _, tc := range []struct {
		handle string
		want   bool
	}{
		{handle: "dev", want: true},
		{handle: "qa", want: true},
		{handle: "dev", want: false},
		{handle: "qa", want: false},
	} {
		got, err := ledger.Reserve(tc.handle, "shared-id")
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Fatalf("Reserve(%q, shared-id) = %t, want %t", tc.handle, got, tc.want)
		}
	}
}

func TestSessionNotifierAttemptLedgerIsBoundedAndPruned(t *testing.T) {
	ledger := newSessionNotifierAttemptLedger(nil)
	for i := 0; i < sessionNotifierAttemptLimit; i++ {
		if reserved, err := ledger.Reserve("dev", fmt.Sprintf("message-%03d", i)); err != nil || !reserved {
			t.Fatalf("reserve message %d: reserved=%t err=%v", i, reserved, err)
		}
	}
	if got := len(ledger.attempted["dev"]); got != sessionNotifierAttemptLimit {
		t.Fatalf("bounded attempt count = %d, want %d", got, sessionNotifierAttemptLimit)
	}
	if reserved, err := ledger.Reserve("dev", "overflow"); err == nil || reserved {
		t.Fatalf("overflow reserve = %t, %v; want fail-closed capacity error", reserved, err)
	}
	if !sessionNotifierAttempted(ledger.attempted, "dev", "message-000") {
		t.Fatal("full ledger evicted a still-pending attempt")
	}
	newest := fmt.Sprintf("message-%03d", sessionNotifierAttemptLimit-1)
	if err := ledger.Prune(map[string]map[string]struct{}{
		"dev": {newest: {}},
	}); err != nil {
		t.Fatal(err)
	}
	if got := ledger.attempted["dev"]; len(got) != 1 || got[0] != newest {
		t.Fatalf("pruned attempts = %+v, want [%s]", got, newest)
	}
	if reserved, err := ledger.Reserve("dev", "after-prune"); err != nil || !reserved {
		t.Fatalf("reserve after prune = %t, %v; want capacity restored", reserved, err)
	}
}

func TestSessionNotifierMalformedMessageFailsClosed(t *testing.T) {
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
		Root: root, BaseRoot: filepath.Dir(root), Tmux: &launch.TmuxInfo{PaneID: "%10"},
	}); err != nil {
		t.Fatal(err)
	}
	message := filepath.Join(agentDir, "inbox", "new", "malformed.md")
	body := "---json\n{\"schema\":1,\"id\":\"malformed\",\"id\":\"malformed\"}\n---\nbody\n"
	if err := os.WriteFile(message, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	ledger := newSessionNotifierAttemptLedger(nil)
	sends := 0
	for i := 0; i < 2; i++ {
		nudged, err := notifySessionInboxArrival(root, profile, session, message, ledger, sessionNotifierWakeAbsent, func(string, string) error {
			sends++
			return nil
		})
		if err == nil || nudged {
			t.Fatalf("malformed arrival %d nudged=%t err=%v, want fail-closed error", i, nudged, err)
		}
	}
	if sends != 0 || len(ledger.attempted) != 0 {
		t.Fatalf("malformed message sends=%d attempts=%+v, want neither", sends, ledger.attempted)
	}
}

func TestSessionNotifierMalformedStartupMessageDoesNotBlockValidWork(t *testing.T) {
	project := canonicalFilesystemPath(t.TempDir())
	const (
		profile = "review"
		session = "wake"
		handle  = "dev"
		paneID  = "%11"
	)
	root := filepath.Join(project, ".agent-mail", profile, session)
	agentDir := filepath.Join(root, "agents", handle)
	inbox := filepath.Join(agentDir, "inbox", "new")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := launch.Write(agentDir, launch.Record{
		Schema: launch.SchemaVersion, Role: handle, Handle: handle, Binary: "codex",
		Session: session, TeamProfile: profile, TeamHome: project, CWD: project,
		Root: root, BaseRoot: filepath.Dir(root), Tmux: &launch.TmuxInfo{PaneID: paneID},
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inbox, "malformed.md"), []byte("not frontmatter\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeSessionNotifierMessage(t, filepath.Join(inbox, "valid.md"), "valid")
	stop := make(chan os.Signal, 1)
	sends := 0
	err := executeSessionNotifier(sessionNotifierExecution{
		ProjectDir: project, Profile: profile, Session: session, Root: root, Token: "malformed-startup",
		TTL: time.Minute, Heartbeat: time.Hour, Stop: stop, Now: time.Now,
		WakeLive: sessionNotifierWakeAbsent,
		SendKeys: func(gotPane, _ string) error {
			if gotPane != paneID {
				t.Fatalf("nudge pane = %q, want %q", gotPane, paneID)
			}
			sends++
			stop <- os.Interrupt
			return nil
		},
	})
	if err != nil {
		t.Fatalf("malformed startup neighbor stopped notifier: %v", err)
	}
	if sends != 1 {
		t.Fatalf("valid startup sends = %d, want one", sends)
	}
}

func TestSessionNotifierFailedNudgeIsReservedWithoutRetry(t *testing.T) {
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
	writeSessionNotifierMessage(t, message, "m2")
	ledger := newSessionNotifierAttemptLedger(nil)
	attempts := 0
	sendKeys := func(string, string) error {
		attempts++
		if attempts == 1 {
			return errors.New("temporary tmux failure")
		}
		return nil
	}
	if nudged, err := notifySessionInboxArrival(root, profile, session, message, ledger, sessionNotifierWakeAbsent, sendKeys); err == nil || nudged {
		t.Fatalf("first arrival nudged=%t err=%v, want retryable failure", nudged, err)
	}
	if nudged, err := notifySessionInboxArrival(root, profile, session, message, ledger, sessionNotifierWakeAbsent, sendKeys); err != nil || nudged {
		t.Fatalf("post-success arrival nudged=%t err=%v, want deduped", nudged, err)
	}
	if attempts != 1 {
		t.Fatalf("send attempts=%d, want one reserved attempt despite failure", attempts)
	}
}

func TestSessionNotifierVerifiedWakeOwnsInput(t *testing.T) {
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
	message := filepath.Join(agentDir, "inbox", "new", "wake-owned.md")
	writeSessionNotifierMessage(t, message, "wake-owned")
	ledger := newSessionNotifierAttemptLedger(nil)
	sends := 0
	nudged, err := notifySessionInboxArrival(root, profile, session, message, ledger,
		func(gotAgentDir, gotRoot, gotProfile, gotSession, gotHandle string, _ launch.Record) bool {
			if gotAgentDir != agentDir || gotRoot != root || gotProfile != profile || gotSession != session || gotHandle != handle {
				t.Fatalf("wake-live scope = %q %q %q %q %q", gotAgentDir, gotRoot, gotProfile, gotSession, gotHandle)
			}
			return true
		},
		func(string, string) error { sends++; return nil },
	)
	if err != nil || nudged || sends != 0 {
		t.Fatalf("wake-owned notification nudged=%t sends=%d err=%v, want observed with no pane input", nudged, sends, err)
	}
	if again, err := notifySessionInboxArrival(root, profile, session, message, ledger, sessionNotifierWakeAbsent, func(string, string) error { sends++; return nil }); err != nil || again || sends != 0 {
		t.Fatalf("wake-owned ID was retried after wake state changed: nudged=%t sends=%d err=%v", again, sends, err)
	}
}

func TestSessionNotifierForeignRootWakeFallsBackBeforeReservationBecomesPermanent(t *testing.T) {
	project := canonicalFilesystemPath(t.TempDir())
	const (
		profile = "review"
		session = "wake"
		handle  = "dev"
		paneID  = "%81"
		wakePID = 4281
	)
	root := filepath.Join(project, ".agent-mail", profile, session)
	foreignRoot := filepath.Join(project, ".agent-mail", profile, "foreign")
	agentDir := filepath.Join(root, "agents", handle)
	if err := os.MkdirAll(filepath.Join(agentDir, "inbox", "new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := launch.Write(agentDir, launch.Record{
		Schema: launch.SchemaVersion, Role: handle, Handle: handle, Binary: "codex",
		Session: session, TeamProfile: profile, TeamHome: project, CWD: project,
		Root: root, BaseRoot: filepath.Dir(root), Tmux: &launch.TmuxInfo{PaneID: paneID},
	}); err != nil {
		t.Fatal(err)
	}
	writeWakeLock(t, agentDir, wakeLockFile{PID: wakePID, Root: foreignRoot, Agent: handle, Started: time.Now()})

	previousProbe := defaultDuplicateLaunchProbe
	defaultDuplicateLaunchProbe = duplicateLaunchProbe{
		PIDAlive: func(pid int) bool { return pid == wakePID },
		ProcessMatch: func(pid int, predicate func(string) bool) bool {
			return pid == wakePID && predicate("amq wake --me "+handle+" --root "+foreignRoot)
		},
		Now: time.Now,
	}
	t.Cleanup(func() { defaultDuplicateLaunchProbe = previousProbe })

	message := filepath.Join(agentDir, "inbox", "new", "foreign-root.md")
	writeSessionNotifierMessage(t, message, "foreign-root")
	sends := 0
	nudged, err := notifySessionInboxArrival(root, profile, session, message, newSessionNotifierAttemptLedger(nil), nil, func(gotPane, _ string) error {
		if gotPane != paneID {
			t.Fatalf("fallback pane = %q, want %q", gotPane, paneID)
		}
		sends++
		return nil
	})
	if err != nil || !nudged || sends != 1 {
		t.Fatalf("foreign-root wake fallback nudged=%t sends=%d err=%v, want exactly one fallback", nudged, sends, err)
	}
}

func TestSessionNotifierPersistentReservationSurvivesFailedSend(t *testing.T) {
	project := canonicalFilesystemPath(t.TempDir())
	const (
		profile = "review"
		session = "wake"
		handle  = "dev"
		token   = "attempt-owner"
	)
	root := filepath.Join(project, ".agent-mail", profile, session)
	agentDir := filepath.Join(root, "agents", handle)
	if err := os.MkdirAll(filepath.Join(agentDir, "inbox", "new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := launch.Write(agentDir, launch.Record{
		Schema: launch.SchemaVersion, Role: handle, Handle: handle, Binary: "codex",
		Session: session, TeamProfile: profile, TeamHome: project, CWD: project,
		Root: root, BaseRoot: filepath.Dir(root), Tmux: &launch.TmuxInfo{PaneID: "%9"},
	}); err != nil {
		t.Fatal(err)
	}
	message := filepath.Join(agentDir, "inbox", "new", "crash-safe.md")
	writeSessionNotifierMessage(t, message, "crash-safe")
	path := sessionNotifierRuntimePath(project, profile, session)
	rec := sessionNotifierRecord{
		SchemaVersion: sessionNotifierSchema, ProjectDir: project, Profile: profile, Session: session,
		Root: root, PID: os.Getpid(), OwnerToken: token, Expected: true, Health: "healthy",
	}
	if err := writeSessionNotifierRecord(path, rec); err != nil {
		t.Fatal(err)
	}
	ledger := newPersistentSessionNotifierAttemptLedger(path, token, root, &rec)
	sends := 0
	if nudged, err := notifySessionInboxArrival(root, profile, session, message, ledger, sessionNotifierWakeAbsent, func(string, string) error {
		sends++
		return errors.New("injected tmux failure")
	}); err == nil || nudged {
		t.Fatalf("failed reserved attempt nudged=%t err=%v, want one failure", nudged, err)
	}
	persisted, err := readSessionNotifierRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	if !sessionNotifierAttempted(persisted.AttemptedMessageIDs, handle, "crash-safe") {
		t.Fatalf("failed attempt was not durably reserved: %+v", persisted.AttemptedMessageIDs)
	}
	restarted := newPersistentSessionNotifierAttemptLedger(path, token, root, &persisted)
	if nudged, err := notifySessionInboxArrival(root, profile, session, message, restarted, sessionNotifierWakeAbsent, func(string, string) error {
		sends++
		return nil
	}); err != nil || nudged || sends != 1 {
		t.Fatalf("restart retried reserved failure: nudged=%t sends=%d err=%v", nudged, sends, err)
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
	writeSessionNotifierMessage(t, message, "pending")

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
		WakeLive: sessionNotifierWakeAbsent,
	})
	if err != nil {
		t.Fatal(err)
	}
	if nudges != 1 {
		t.Fatalf("startup nudges = %d, want exactly one for pending inbox message", nudges)
	}
	persisted, err := readSessionNotifierRecord(sessionNotifierRuntimePath(project, profile, session))
	if err != nil {
		t.Fatal(err)
	}
	if !sessionNotifierAttempted(persisted.AttemptedMessageIDs, handle, "pending") {
		t.Fatalf("graceful startup stop lost durable reservation: %+v", persisted.AttemptedMessageIDs)
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
	writeSessionNotifierMessage(t, message, "missed-event")
	ledger := newSessionNotifierAttemptLedger(nil)
	nudges := 0
	nudged, err := rescanSessionInboxMessages(root, profile, session, ledger, sessionNotifierWakeAbsent, func(gotPane, _ string) error {
		if gotPane != paneID {
			t.Fatalf("nudge pane = %q, want %q", gotPane, paneID)
		}
		nudges++
		return nil
	})
	if err != nil || !nudged || nudges != 1 {
		t.Fatalf("heartbeat rescan nudged=%t count=%d err=%v", nudged, nudges, err)
	}
	nudged, err = rescanSessionInboxMessages(root, profile, session, ledger, sessionNotifierWakeAbsent, func(string, string) error {
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
