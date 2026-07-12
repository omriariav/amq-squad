package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/omriariav/amq-squad/v2/internal/attention"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/notifier"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func notificationWatcherTeam(t *testing.T, profile, session string) (string, team.Team, string) {
	t.Helper()
	project := t.TempDir()
	op := team.DefaultOperator()
	op.InteractionMode = team.OperatorInteractionLeadPane
	op.PollRequired = false
	op.Notifications = &team.OperatorNotificationPolicy{
		Enabled: true,
		Events:  []string{"gate"},
		Sinks:   []team.OperatorNotificationSinkConfig{{ID: "hook", Type: "command", Argv: []string{"hook"}, Timeout: "1s"}},
	}
	tm := team.Team{Project: project, Operator: &op, Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: session}}}
	if err := team.WriteProfile(project, profile, tm); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(project, ".agent-mail-test")
	if profile != team.DefaultProfile {
		base = filepath.Join(base, profile)
	}
	for _, handle := range []string{"cto", "user"} {
		for _, box := range []string{"new", "cur"} {
			if err := os.MkdirAll(filepath.Join(base, session, "agents", handle, "inbox", box), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	tm, err := team.ReadProfile(project, profile)
	if err != nil {
		t.Fatal(err)
	}
	return project, tm, base
}

func waitWatcherRecord(t *testing.T, path string, timeout time.Duration, pred func(notificationWatcherRecord) bool) notificationWatcherRecord {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		rec, err := readNotificationWatcherRecord(path)
		if err == nil && pred(rec) {
			return rec
		}
		time.Sleep(5 * time.Millisecond)
	}
	rec, err := readNotificationWatcherRecord(path)
	t.Fatalf("watcher record condition timed out: rec=%+v err=%v", rec, err)
	return notificationWatcherRecord{}
}

func startTestNotificationWatcher(t *testing.T, tm team.Team, profile, session, base, token string, deliver func(context.Context, time.Time) (notifyDeliverySummary, error)) (chan os.Signal, <-chan error) {
	t.Helper()
	stop := make(chan os.Signal, 1)
	done := make(chan error, 1)
	go func() {
		err := executeNotificationWatcher(notificationWatcherExecution{
			ProjectDir: tm.Project, Profile: profile, Session: session, BaseRoot: base, Token: token,
			TTL: 250 * time.Millisecond, Heartbeat: 25 * time.Millisecond, Rescan: 40 * time.Millisecond,
			Now: time.Now, Stop: stop, Deliver: deliver,
		})
		if err != nil {
			t.Logf("watcher exited early: %v", err)
		}
		done <- err
	}()
	return stop, done
}

func stopTestNotificationWatcher(t *testing.T, stop chan os.Signal, done <-chan error) {
	t.Helper()
	stop <- os.Interrupt
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not stop")
	}
}

func TestNotificationWatcherInitialScanFSNotifyAndPeriodicRescan(t *testing.T) {
	project, tm, base := notificationWatcherTeam(t, team.DefaultProfile, "s")
	var scans atomic.Int32
	stop, done := startTestNotificationWatcher(t, tm, team.DefaultProfile, "s", base, "token-a", func(context.Context, time.Time) (notifyDeliverySummary, error) {
		scans.Add(1)
		return notifyDeliverySummary{}, nil
	})
	path := notificationWatcherRuntimePath(project, team.DefaultProfile, "s")
	waitWatcherRecord(t, path, time.Second, func(r notificationWatcherRecord) bool { return r.Health == "healthy" && !r.LastScanAt.IsZero() })
	initial := scans.Load()
	gatePath := filepath.Join(base, "s", "agents", "user", "inbox", "new", "gate.json")
	if err := os.WriteFile(gatePath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitWatcherRecord(t, path, time.Second, func(r notificationWatcherRecord) bool { return scans.Load() > initial && !r.LastEventAt.IsZero() })
	afterEvent := scans.Load()
	waitWatcherRecord(t, path, time.Second, func(notificationWatcherRecord) bool { return scans.Load() > afterEvent })
	stopTestNotificationWatcher(t, stop, done)
	if got := inspectNotificationWatcher(tm, team.DefaultProfile, "s", time.Now()).Health; got != "inactive" {
		t.Fatalf("health after clean stop=%s", got)
	}
}

func TestNotificationWatcherStartsBeforeSessionWithoutCreatingIt(t *testing.T) {
	project, tm, base := notificationWatcherTeam(t, team.DefaultProfile, "s")
	root := filepath.Join(base, "s")
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	var scans atomic.Int32
	stop, done := startTestNotificationWatcher(t, tm, team.DefaultProfile, "s", base, "prelaunch-token", func(context.Context, time.Time) (notifyDeliverySummary, error) {
		scans.Add(1)
		return notifyDeliverySummary{}, nil
	})
	path := notificationWatcherRuntimePath(project, team.DefaultProfile, "s")
	waitWatcherRecord(t, path, time.Second, func(r notificationWatcherRecord) bool { return r.Health == "healthy" && !r.LastScanAt.IsZero() })
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("pre-backend watcher created session root: %v", err)
	}
	initial := scans.Load()
	newBox := filepath.Join(root, "agents", "user", "inbox", "new")
	if err := os.MkdirAll(newBox, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newBox, "gate.md"), []byte("gate"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitWatcherRecord(t, path, time.Second, func(r notificationWatcherRecord) bool { return scans.Load() > initial && !r.LastEventAt.IsZero() })
	stopTestNotificationWatcher(t, stop, done)
}

func TestNotificationWatcherLeaseExpiryRecoveryAndSingleOwner(t *testing.T) {
	project, tm, base := notificationWatcherTeam(t, team.DefaultProfile, "s")
	path := notificationWatcherRuntimePath(project, team.DefaultProfile, "s")
	host, _ := os.Hostname()
	stale := notificationWatcherRecord{SchemaVersion: 1, ProjectDir: project, Profile: team.DefaultProfile, Session: "s", NamespaceID: "default/s", PID: 999, Host: host, OwnerToken: "old", LeaseTTL: "100ms", LeaseExpiresAt: time.Now().Add(-time.Second), Expected: true, Health: "healthy"}
	if err := writeNotificationWatcherRecord(path, stale); err != nil {
		t.Fatal(err)
	}
	stop, done := startTestNotificationWatcher(t, tm, team.DefaultProfile, "s", base, "new", func(context.Context, time.Time) (notifyDeliverySummary, error) { return notifyDeliverySummary{}, nil })
	waitWatcherRecord(t, path, time.Second, func(r notificationWatcherRecord) bool { return r.OwnerToken == "new" && r.Health == "healthy" })
	err := executeNotificationWatcher(notificationWatcherExecution{ProjectDir: project, Profile: team.DefaultProfile, Session: "s", BaseRoot: base, Token: "rival", TTL: time.Second, Heartbeat: 100 * time.Millisecond, Rescan: time.Second, Stop: make(chan os.Signal), Deliver: func(context.Context, time.Time) (notifyDeliverySummary, error) { return notifyDeliverySummary{}, nil }})
	if err == nil || !strings.Contains(err.Error(), "lease held") {
		t.Fatalf("rival claim err=%v", err)
	}
	stopTestNotificationWatcher(t, stop, done)
}

func TestNotificationWatcherFreshRemoteLeaseFencesAndStopRefuses(t *testing.T) {
	project, _, _ := notificationWatcherTeam(t, team.DefaultProfile, "s")
	path := notificationWatcherRuntimePath(project, team.DefaultProfile, "s")
	rec := notificationWatcherRecord{SchemaVersion: 1, ProjectDir: project, Profile: team.DefaultProfile, Session: "s", NamespaceID: "default/s", PID: 7, Host: "remote-host", OwnerToken: "remote", LeaseTTL: "1m", LeaseExpiresAt: time.Now().Add(time.Minute), Expected: true, Health: "healthy"}
	if err := writeNotificationWatcherRecord(path, rec); err != nil {
		t.Fatal(err)
	}
	err := stopNotificationWatcher(project, team.DefaultProfile, "s")
	if err == nil || !strings.Contains(err.Error(), "remote active watcher") {
		t.Fatalf("remote stop err=%v", err)
	}
	current, _ := readNotificationWatcherRecord(path)
	if current.OwnerToken != "remote" {
		t.Fatalf("remote ownership changed: %+v", current)
	}
}

func TestNotificationWatcherNamespaceIsolation(t *testing.T) {
	project, tm, base := notificationWatcherTeam(t, "release", "one")
	tm.Members[0].Session = "two"
	if err := team.WriteProfile(project, "release", tm); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(base, "two"), 0o755); err != nil {
		t.Fatal(err)
	}
	stop1, done1 := startTestNotificationWatcher(t, tm, "release", "one", base, "one-token", func(context.Context, time.Time) (notifyDeliverySummary, error) { return notifyDeliverySummary{}, nil })
	stop2, done2 := startTestNotificationWatcher(t, tm, "release", "two", base, "two-token", func(context.Context, time.Time) (notifyDeliverySummary, error) { return notifyDeliverySummary{}, nil })
	p1 := notificationWatcherRuntimePath(project, "release", "one")
	p2 := notificationWatcherRuntimePath(project, "release", "two")
	waitWatcherRecord(t, p1, time.Second, func(r notificationWatcherRecord) bool { return r.OwnerToken == "one-token" && r.Health == "healthy" })
	waitWatcherRecord(t, p2, time.Second, func(r notificationWatcherRecord) bool { return r.OwnerToken == "two-token" && r.Health == "healthy" })
	if p1 == p2 {
		t.Fatal("scoped runtime paths collided")
	}
	stopTestNotificationWatcher(t, stop1, done1)
	stopTestNotificationWatcher(t, stop2, done2)
}

func TestNotificationWatcherSinkFailureDegradesThenRetries(t *testing.T) {
	project, tm, base := notificationWatcherTeam(t, team.DefaultProfile, "s")
	var calls atomic.Int32
	stop, done := startTestNotificationWatcher(t, tm, team.DefaultProfile, "s", base, "retry-token", func(context.Context, time.Time) (notifyDeliverySummary, error) {
		if calls.Add(1) == 1 {
			return notifyDeliverySummary{Selected: 1, Failed: 1}, nil
		}
		return notifyDeliverySummary{Selected: 1, Delivered: 1}, nil
	})
	path := notificationWatcherRuntimePath(project, team.DefaultProfile, "s")
	waitWatcherRecord(t, path, time.Second, func(r notificationWatcherRecord) bool {
		return r.Health == "degraded" && strings.Contains(r.LastError, "failed")
	})
	waitWatcherRecord(t, path, time.Second, func(r notificationWatcherRecord) bool { return r.Health == "healthy" && calls.Load() >= 2 })
	stopTestNotificationWatcher(t, stop, done)
}

func TestNotificationWatcherHeartbeatContinuesAndStopCancelsBlockedSink(t *testing.T) {
	project, tm, base := notificationWatcherTeam(t, team.DefaultProfile, "s")
	started := make(chan struct{})
	stop, done := startTestNotificationWatcher(t, tm, team.DefaultProfile, "s", base, "blocking-token", func(ctx context.Context, _ time.Time) (notifyDeliverySummary, error) {
		close(started)
		<-ctx.Done()
		return notifyDeliverySummary{}, ctx.Err()
	})
	<-started
	path := notificationWatcherRuntimePath(project, team.DefaultProfile, "s")
	first := waitWatcherRecord(t, path, time.Second, func(r notificationWatcherRecord) bool { return r.OwnerToken == "blocking-token" })
	second := waitWatcherRecord(t, path, time.Second, func(r notificationWatcherRecord) bool {
		return r.HeartbeatAt.After(first.HeartbeatAt) && r.LeaseExpiresAt.After(first.LeaseExpiresAt)
	})
	if !second.LastScanAt.IsZero() {
		t.Fatal("blocked delivery incorrectly marked complete")
	}
	stopTestNotificationWatcher(t, stop, done)
}

func TestNotificationWatcherFSNotifyFailureUsesVisiblePeriodicFallback(t *testing.T) {
	project, _, base := notificationWatcherTeam(t, team.DefaultProfile, "s")
	stop := make(chan os.Signal, 1)
	done := make(chan error, 1)
	var scans atomic.Int32
	go func() {
		done <- executeNotificationWatcher(notificationWatcherExecution{
			ProjectDir: project, Profile: team.DefaultProfile, Session: "s", BaseRoot: base, Token: "fallback-token",
			TTL: 250 * time.Millisecond, Heartbeat: 25 * time.Millisecond, Rescan: 40 * time.Millisecond,
			Stop: stop, NewFSWatch: func() (*fsnotify.Watcher, error) { return nil, errors.New("watch handles exhausted") },
			Deliver: func(context.Context, time.Time) (notifyDeliverySummary, error) {
				scans.Add(1)
				return notifyDeliverySummary{}, nil
			},
		})
	}()
	path := notificationWatcherRuntimePath(project, team.DefaultProfile, "s")
	waitWatcherRecord(t, path, time.Second, func(r notificationWatcherRecord) bool {
		return r.Health == "degraded" && strings.Contains(r.LastError, "periodic rescan") && !r.LastScanAt.IsZero()
	})
	initial := scans.Load()
	waitWatcherRecord(t, path, time.Second, func(notificationWatcherRecord) bool { return scans.Load() > initial })
	stopTestNotificationWatcher(t, stop, done)
}

type watcherTestProcess struct{ pid int }

func (p watcherTestProcess) PID() int             { return p.pid }
func (watcherTestProcess) Signal(os.Signal) error { return nil }
func (watcherTestProcess) Release() error         { return nil }

func TestLeadPaneNotificationsStartDespitePollNotRequired(t *testing.T) {
	project, tm, base := notificationWatcherTeam(t, team.DefaultProfile, "s")
	if operatorDeliveryForTeam(tm).PollRequired {
		t.Fatal("fixture must be lead_pane poll_required=false")
	}
	oldSpawn, oldAlive, oldMatch := notificationWatcherSpawn, notificationWatcherPIDAlive, notificationWatcherProcessMatch
	t.Cleanup(func() {
		notificationWatcherSpawn, notificationWatcherPIDAlive, notificationWatcherProcessMatch = oldSpawn, oldAlive, oldMatch
	})
	notificationWatcherPIDAlive = func(pid int) bool { return pid == 4242 }
	notificationWatcherProcessMatch = func(pid int, pred func(string) bool) bool {
		return pid == 4242
	}
	host, _ := os.Hostname()
	// A crashed local generation can still have an unexpired lease. Reconcile
	// must fence it and recover immediately rather than waiting out the TTL.
	crashed := notificationWatcherRecord{SchemaVersion: 1, ProjectDir: project, Profile: team.DefaultProfile, Session: "s", NamespaceID: "default/s", PID: 111, Host: host, OwnerToken: "crashed", LeaseTTL: "15s", LeaseExpiresAt: time.Now().Add(time.Minute), Expected: true, Health: "healthy"}
	if err := writeNotificationWatcherRecord(notificationWatcherRuntimePath(project, team.DefaultProfile, "s"), crashed); err != nil {
		t.Fatal(err)
	}
	notificationWatcherSpawn = func(projectDir, profile, session, baseRoot, token string) (notificationWatcherProcess, error) {
		rec := notificationWatcherRecord{SchemaVersion: 1, ProjectDir: projectDir, Profile: profile, Session: session, NamespaceID: "default/s", PID: 4242, Host: host, Owner: "supervised", OwnerToken: token, LeaseTTL: "15s", LeaseExpiresAt: time.Now().Add(time.Minute), HeartbeatAt: time.Now(), LastScanAt: time.Now(), StatePath: defaultNotifyStatePath(projectDir), Expected: true, Health: "healthy"}
		if err := writeNotificationWatcherRecord(notificationWatcherRuntimePath(projectDir, profile, session), rec); err != nil {
			return nil, err
		}
		return watcherTestProcess{pid: 4242}, nil
	}
	if err := reconcileNotificationWatcherStarted(tm, team.DefaultProfile, "s", base); err != nil {
		t.Fatal(err)
	}
	if got := inspectNotificationWatcher(tm, team.DefaultProfile, "s", time.Now()).Health; got != "healthy" {
		t.Fatalf("watcher health=%s at %s", got, project)
	}
}

type watcherCountingSink struct{ calls *atomic.Int32 }

func (watcherCountingSink) ID() string { return "hook" }
func (s watcherCountingSink) Deliver(context.Context, attention.Event) error {
	s.calls.Add(1)
	return nil
}

func TestLeadPaneDirectGateDeliversExactlyOnceWithoutOperatorCommand(t *testing.T) {
	project, tm, base := notificationWatcherTeam(t, team.DefaultProfile, "s")
	seedNotifyLaunch(t, project, base, "s", "cto")
	oldFactory := notificationSinkFactory
	defer func() { notificationSinkFactory = oldFactory }()
	var calls atomic.Int32
	notificationSinkFactory = func(team.OperatorNotificationSinkConfig) notifier.Sink { return watcherCountingSink{calls: &calls} }
	stop, done := startTestNotificationWatcher(t, tm, team.DefaultProfile, "s", base, "integration-token", nil)
	path := notificationWatcherRuntimePath(project, team.DefaultProfile, "s")
	waitWatcherRecord(t, path, time.Second, func(r notificationWatcherRecord) bool { return r.Health == "healthy" && !r.LastScanAt.IsZero() })
	seedNotifyMessage(t, base, "s", "user", "new", notifyMsg{ID: "gate-1", From: "cto", To: "user", Thread: "gate/release", Subject: "APPROVAL: release", Kind: "question", Created: time.Now()})
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if calls.Load() != 1 {
		t.Fatalf("direct gate deliveries=%d", calls.Load())
	}
	if err := os.WriteFile(filepath.Join(base, "s", "agents", "user", "inbox", "new", "noise"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatalf("duplicate delivery after rescan=%d", calls.Load())
	}
	stopTestNotificationWatcher(t, stop, done)
}

func TestNotificationReservationCoversConfiguredSinkTimeout(t *testing.T) {
	if got := notificationReservationTTL(team.OperatorNotificationSinkConfig{Timeout: "60s"}); got != 65*time.Second {
		t.Fatalf("reservation ttl=%s", got)
	}
	if got := notificationReservationTTL(team.OperatorNotificationSinkConfig{}); got != 15*time.Second {
		t.Fatalf("default reservation ttl=%s", got)
	}
}

func TestNotificationWatcherRejectsTraversalScope(t *testing.T) {
	err := executeNotificationWatcher(notificationWatcherExecution{ProjectDir: t.TempDir(), Profile: team.DefaultProfile, Session: "../escape", BaseRoot: t.TempDir(), Token: "x", TTL: time.Second, Heartbeat: time.Millisecond, Rescan: time.Second})
	if err == nil {
		t.Fatal("traversal session accepted")
	}
}

func TestNotificationWatcherStopTimeoutPreservesOwnership(t *testing.T) {
	project, _, _ := notificationWatcherTeam(t, team.DefaultProfile, "s")
	host, _ := os.Hostname()
	path := notificationWatcherRuntimePath(project, team.DefaultProfile, "s")
	rec := notificationWatcherRecord{SchemaVersion: 1, ProjectDir: project, Profile: team.DefaultProfile, Session: "s", NamespaceID: "default/s", PID: 99, Host: host, OwnerToken: "stuck", LeaseTTL: "1m", LeaseExpiresAt: time.Now().Add(time.Minute), Expected: true, Health: "healthy"}
	if err := writeNotificationWatcherRecord(path, rec); err != nil {
		t.Fatal(err)
	}
	oldNow, oldAlive, oldMatch, oldSignal, oldSleep := notificationWatcherNow, notificationWatcherPIDAlive, notificationWatcherProcessMatch, notificationWatcherSignal, notificationWatcherSleep
	t.Cleanup(func() {
		notificationWatcherNow, notificationWatcherPIDAlive, notificationWatcherProcessMatch, notificationWatcherSignal, notificationWatcherSleep = oldNow, oldAlive, oldMatch, oldSignal, oldSleep
	})
	clock := time.Now()
	notificationWatcherNow = func() time.Time { clock = clock.Add(time.Second); return clock }
	notificationWatcherPIDAlive = func(int) bool { return true }
	notificationWatcherProcessMatch = func(int, func(string) bool) bool { return true }
	notificationWatcherSignal = func(int, os.Signal) error { return nil }
	notificationWatcherSleep = func(time.Duration) {}
	err := stopNotificationWatcher(project, team.DefaultProfile, "s")
	if err == nil || !strings.Contains(err.Error(), "ownership preserved") {
		t.Fatalf("stop timeout err=%v", err)
	}
	current, _ := readNotificationWatcherRecord(path)
	if current.OwnerToken != "stuck" || !current.Expected {
		t.Fatalf("stuck ownership cleared: %+v", current)
	}
}

func TestNotificationWatcherAliveIdentityMismatchFailsClosed(t *testing.T) {
	project, tm, base := notificationWatcherTeam(t, team.DefaultProfile, "s")
	host, _ := os.Hostname()
	path := notificationWatcherRuntimePath(project, team.DefaultProfile, "s")
	rec := notificationWatcherRecord{SchemaVersion: 1, ProjectDir: project, Profile: team.DefaultProfile, Session: "s", NamespaceID: "default/s", PID: 88, Host: host, OwnerToken: "ambiguous", LeaseTTL: "1m", LeaseExpiresAt: time.Now().Add(time.Minute), Expected: true, Health: "healthy"}
	if err := writeNotificationWatcherRecord(path, rec); err != nil {
		t.Fatal(err)
	}
	oldAlive, oldMatch, oldSpawn := notificationWatcherPIDAlive, notificationWatcherProcessMatch, notificationWatcherSpawn
	t.Cleanup(func() {
		notificationWatcherPIDAlive, notificationWatcherProcessMatch, notificationWatcherSpawn = oldAlive, oldMatch, oldSpawn
	})
	notificationWatcherPIDAlive = func(pid int) bool { return pid == 88 }
	notificationWatcherProcessMatch = func(int, func(string) bool) bool { return false }
	spawned := false
	notificationWatcherSpawn = func(string, string, string, string, string) (notificationWatcherProcess, error) {
		spawned = true
		return nil, nil
	}
	err := reconcileNotificationWatcherStarted(tm, team.DefaultProfile, "s", base)
	if err == nil || !strings.Contains(err.Error(), "active but unhealthy") || spawned {
		t.Fatalf("ambiguous reconcile err=%v spawned=%t", err, spawned)
	}
	err = stopNotificationWatcher(project, team.DefaultProfile, "s")
	if err == nil || !strings.Contains(err.Error(), "identity is unverified") {
		t.Fatalf("ambiguous stop err=%v", err)
	}
	current, _ := readNotificationWatcherRecord(path)
	if current.OwnerToken != "ambiguous" {
		t.Fatalf("ambiguous owner cleared: %+v", current)
	}
}

func TestNotificationWatcherCopiedRuntimeProjectBindingRejected(t *testing.T) {
	project, tm, _ := notificationWatcherTeam(t, team.DefaultProfile, "s")
	host, _ := os.Hostname()
	rec := notificationWatcherRecord{SchemaVersion: 1, ProjectDir: filepath.Join(project, "other-project"), Profile: team.DefaultProfile, Session: "s", NamespaceID: "default/s", PID: os.Getpid(), Host: host, OwnerToken: "copied", LeaseTTL: "1m", LeaseExpiresAt: time.Now().Add(time.Minute), Expected: true, Health: "healthy"}
	if err := writeNotificationWatcherRecord(notificationWatcherRuntimePath(project, team.DefaultProfile, "s"), rec); err != nil {
		t.Fatal(err)
	}
	status := inspectNotificationWatcher(tm, team.DefaultProfile, "s", time.Now())
	if status.Health != "unhealthy" || !strings.Contains(status.Reason, "project/profile/session binding") {
		t.Fatalf("copied runtime accepted: %+v", status)
	}
}

func TestNotificationWatcherMissingNamespaceWatchIsReadOnly(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing-session")
	w, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := addNotificationWatchTree(w, root); !os.IsNotExist(err) {
		t.Fatalf("missing root err=%v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("watch recreated removed namespace: %v", err)
	}
}

func TestNotificationWatcherStatusAndDoctorHealthSurfaces(t *testing.T) {
	project, tm, _ := notificationWatcherTeam(t, team.DefaultProfile, "s")
	warnings := statusNotificationWatcherWarnings(project, team.DefaultProfile, "s", time.Now())
	if len(warnings) != 1 || warnings[0].Kind != "notification_watcher_unhealthy" || !strings.Contains(warnings[0].Detail, "notifications_enabled=true") {
		t.Fatalf("warnings=%+v", warnings)
	}
	check := doctorCheckNotificationWatcher(doctorExecution{ProjectDir: project, Profile: team.DefaultProfile, Probe: duplicateLaunchProbe{Now: time.Now}}, "s")
	if check.Status != doctorFail || !strings.Contains(check.Detail, "UNHEALTHY") {
		t.Fatalf("doctor check=%+v", check)
	}
	now := time.Now().UTC()
	inactive := notificationWatcherRecord{SchemaVersion: 1, ProjectDir: project, Profile: team.DefaultProfile, Session: "s", NamespaceID: "default/s", StatePath: defaultNotifyStatePath(project), Expected: false, Health: "inactive", UpdatedAt: now}
	if err := writeNotificationWatcherRecord(notificationWatcherRuntimePath(project, team.DefaultProfile, "s"), inactive); err != nil {
		t.Fatal(err)
	}
	if got := inspectNotificationWatcher(tm, team.DefaultProfile, "s", now).Health; got != "inactive" {
		t.Fatalf("inactive health=%s", got)
	}
	if got := statusNotificationWatcherWarnings(project, team.DefaultProfile, "s", now); len(got) != 0 {
		t.Fatalf("inactive warnings=%+v", got)
	}
	check = doctorCheckNotificationWatcher(doctorExecution{ProjectDir: project, Profile: team.DefaultProfile, Probe: duplicateLaunchProbe{Now: func() time.Time { return now }}}, "s")
	if check.Status != doctorOK || !strings.Contains(check.Detail, "inactive") {
		t.Fatalf("inactive doctor=%+v", check)
	}
}

func TestStopNotificationWatcherWithoutRuntimeWritesInactiveTombstone(t *testing.T) {
	project, tm, _ := notificationWatcherTeam(t, team.DefaultProfile, "s")
	if err := stopNotificationWatcher(project, team.DefaultProfile, "s"); err != nil {
		t.Fatal(err)
	}
	status := inspectNotificationWatcher(tm, team.DefaultProfile, "s", time.Now())
	if status.Health != "inactive" || status.record.Expected {
		t.Fatalf("stop tombstone=%+v", status)
	}
}

func TestNotificationWatcherPartialAndFinalRoleStopDecision(t *testing.T) {
	cto := team.Member{Role: "cto"}
	qa := team.Member{Role: "qa"}
	rows := []statusRecord{{Role: "cto", Status: statusStateLive}, {Role: "qa", Status: statusStateLive}}
	if shouldStopNotificationWatcherAfterDown(false, []team.Member{cto}, rows) {
		t.Fatal("partial role stop removed watcher while qa remained operational")
	}
	if !shouldStopNotificationWatcherAfterDown(false, []team.Member{cto}, rows[:1]) {
		t.Fatal("final role stop retained watcher")
	}
	if !shouldStopNotificationWatcherAfterDown(true, []team.Member{cto, qa}, rows) {
		t.Fatal("stop --all retained watcher")
	}
}

func TestStatusBoardNotificationWatcherDegradesLiveSession(t *testing.T) {
	_, tm, _ := notificationWatcherTeam(t, team.DefaultProfile, "s")
	row := sessionBoardRow{State: boardStateRunning}
	enrichBoardNotificationWatcher([]boardProfile{{Name: team.DefaultProfile, Team: tm}}, state.Session{Name: "s", TeamProfile: team.DefaultProfile}, time.Now(), &row)
	if row.NotificationWatcher == nil || row.NotificationWatcher.Health != "unhealthy" || row.State != boardStateDegraded {
		t.Fatalf("board watcher surface=%+v state=%s", row.NotificationWatcher, row.State)
	}
	if cell := boardNotificationWatcherCell(row); !strings.Contains(cell, "unhealthy") {
		t.Fatalf("board watcher cell=%q", cell)
	}
}

func TestExecuteRmRefusesRemoteWatcherBeforeNamespaceMutation(t *testing.T) {
	project, _, base := notificationWatcherTeam(t, team.DefaultProfile, "s")
	root := filepath.Join(base, "s")
	rec := notificationWatcherRecord{SchemaVersion: 1, ProjectDir: project, Profile: team.DefaultProfile, Session: "s", NamespaceID: "default/s", PID: 7, Host: "remote-host", OwnerToken: "remote-rm", LeaseTTL: "1m", LeaseExpiresAt: time.Now().Add(time.Minute), Expected: true, Health: "healthy"}
	if err := writeNotificationWatcherRecord(notificationWatcherRuntimePath(project, team.DefaultProfile, "s"), rec); err != nil {
		t.Fatal(err)
	}
	err := executeRm(rmExecution{
		ProjectDir: project, Profile: team.DefaultProfile, Session: "s", BaseRoot: base,
		Mode: rmModeDelete, Yes: true, Force: true, Out: io.Discard,
		Probe: state.Probe{PIDAlive: func(int) bool { return false }, ProcessMatch: func(int, func(string) bool) bool { return false }, Now: time.Now},
	})
	if err == nil || !strings.Contains(err.Error(), "before notification watcher is stopped") {
		t.Fatalf("rm remote watcher err=%v", err)
	}
	if info, statErr := os.Stat(root); statErr != nil || !info.IsDir() {
		t.Fatalf("rm mutated namespace before remote refusal: %v", statErr)
	}
}

func TestLaunchFailureCleanupIsGenerationAwareAndRetainsPartialAgent(t *testing.T) {
	project, tm, _ := notificationWatcherTeam(t, team.DefaultProfile, "s")
	host, _ := os.Hostname()
	path := notificationWatcherRuntimePath(project, team.DefaultProfile, "s")
	writeGeneration := func(token string) {
		t.Helper()
		rec := notificationWatcherRecord{SchemaVersion: 1, ProjectDir: project, Profile: team.DefaultProfile, Session: "s", NamespaceID: "default/s", PID: 4242, Host: host, OwnerToken: token, LeaseTTL: "1m", LeaseExpiresAt: time.Now().Add(time.Minute), Expected: true, Health: "healthy"}
		if err := writeNotificationWatcherRecord(path, rec); err != nil {
			t.Fatal(err)
		}
	}
	oldAlive, oldMatch, oldSignal := notificationWatcherPIDAlive, notificationWatcherProcessMatch, notificationWatcherSignal
	oldGrace, oldInterval := notificationWatcherPartialLaunchGrace, notificationWatcherPartialLaunchInterval
	t.Cleanup(func() {
		notificationWatcherPIDAlive, notificationWatcherProcessMatch, notificationWatcherSignal = oldAlive, oldMatch, oldSignal
		notificationWatcherPartialLaunchGrace, notificationWatcherPartialLaunchInterval = oldGrace, oldInterval
	})
	notificationWatcherPartialLaunchGrace = 250 * time.Millisecond
	notificationWatcherPartialLaunchInterval = 5 * time.Millisecond
	watcherAlive := true
	notificationWatcherPIDAlive = func(pid int) bool { return pid == 4242 && watcherAlive }
	notificationWatcherProcessMatch = func(pid int, pred func(string) bool) bool { return pid == 4242 && watcherAlive }
	notificationWatcherSignal = func(int, os.Signal) error { watcherAlive = false; return nil }
	probe := duplicateLaunchProbe{PIDAlive: func(int) bool { return false }, ProcessMatch: func(int, func(string) bool) bool { return false }, Now: time.Now}

	writeGeneration("created-clean")
	if err := cleanupCreatedNotificationWatcherAfterLaunchFailure(tm, team.DefaultProfile, "s", "created-clean", probe); err != nil {
		t.Fatal(err)
	}
	if got := inspectNotificationWatcher(tm, team.DefaultProfile, "s", time.Now()).Health; got != "inactive" {
		t.Fatalf("clean failure watcher=%s", got)
	}

	watcherAlive = true
	writeGeneration("created-partial")
	env, err := resolveAMQEnvForTeamProfile(project, team.DefaultProfile, "s", "cto")
	if err != nil {
		t.Fatal(err)
	}
	agentDir := filepath.Join(absoluteAMQRoot(project, env.Root), "agents", "cto")
	partialProbe := duplicateLaunchProbe{PIDAlive: func(pid int) bool { return pid == 5151 }, ProcessMatch: func(pid int, pred func(string) bool) bool { return pid == 5151 && pred("codex") }, Now: time.Now}
	writeDone := make(chan error, 1)
	go func() {
		time.Sleep(30 * time.Millisecond)
		writeDone <- launch.Write(agentDir, launch.Record{CWD: project, Binary: "codex", Handle: "cto", Role: "cto", Session: "s", Root: absoluteAMQRoot(project, env.Root), AgentPID: 5151, StartedAt: time.Now(), TeamProfile: team.DefaultProfile})
	}()
	if err := cleanupCreatedNotificationWatcherAfterLaunchFailure(tm, team.DefaultProfile, "s", "created-partial", partialProbe); err != nil {
		t.Fatal(err)
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	current, _ := readNotificationWatcherRecord(path)
	if current.OwnerToken != "created-partial" || !current.Expected {
		t.Fatalf("partial launch watcher was removed: %+v", current)
	}

	if err := cleanupCreatedNotificationWatcherAfterLaunchFailure(tm, team.DefaultProfile, "s", "different-generation", probe); err != nil {
		t.Fatal(err)
	}
	current, _ = readNotificationWatcherRecord(path)
	if current.OwnerToken != "created-partial" {
		t.Fatalf("pre-existing generation removed: %+v", current)
	}
}
