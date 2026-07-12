package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/omriariav/amq-squad/v2/internal/attention"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
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

func assertInactiveWatcherTombstone(t *testing.T, path string) notificationWatcherRecord {
	t.Helper()
	rec, err := readNotificationWatcherRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	if rec.PID != 0 || rec.OwnerToken != "" || rec.Expected || rec.Health != "inactive" || rec.LeaseExpiresAt.After(time.Now()) {
		t.Fatalf("watcher did not publish an immediately reclaimable inactive tombstone: %+v", rec)
	}
	return rec
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

func TestNotificationWatcherSignalReleaseAllowsImmediateRestart(t *testing.T) {
	project, tm, base := notificationWatcherTeam(t, team.DefaultProfile, "s")
	path := notificationWatcherRuntimePath(project, team.DefaultProfile, "s")
	deliver := func(context.Context, time.Time) (notifyDeliverySummary, error) { return notifyDeliverySummary{}, nil }

	stop, done := startTestNotificationWatcher(t, tm, team.DefaultProfile, "s", base, "signal-old", deliver)
	waitWatcherRecord(t, path, time.Second, func(r notificationWatcherRecord) bool {
		return r.OwnerToken == "signal-old" && r.Health == "healthy"
	})
	stopTestNotificationWatcher(t, stop, done)
	assertInactiveWatcherTombstone(t, path)

	stop, done = startTestNotificationWatcher(t, tm, team.DefaultProfile, "s", base, "signal-new", deliver)
	waitWatcherRecord(t, path, time.Second, func(r notificationWatcherRecord) bool {
		return r.OwnerToken == "signal-new" && r.Health == "healthy"
	})
	stopTestNotificationWatcher(t, stop, done)
}

func TestNotificationWatcherPolicyDisableReleaseAllowsImmediateReenable(t *testing.T) {
	project, tm, base := notificationWatcherTeam(t, team.DefaultProfile, "s")
	path := notificationWatcherRuntimePath(project, team.DefaultProfile, "s")
	deliver := func(context.Context, time.Time) (notifyDeliverySummary, error) { return notifyDeliverySummary{}, nil }

	_, done := startTestNotificationWatcher(t, tm, team.DefaultProfile, "s", base, "policy-old", deliver)
	waitWatcherRecord(t, path, time.Second, func(r notificationWatcherRecord) bool {
		return r.OwnerToken == "policy-old" && r.Health == "healthy"
	})
	disabled := tm
	op := *tm.Operator
	notifications := *op.Notifications
	notifications.Enabled = false
	op.Notifications = &notifications
	disabled.Operator = &op
	if err := team.WriteProfile(project, team.DefaultProfile, disabled); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not exit after notification policy was disabled")
	}
	assertInactiveWatcherTombstone(t, path)

	if err := team.WriteProfile(project, team.DefaultProfile, tm); err != nil {
		t.Fatal(err)
	}
	stop, restarted := startTestNotificationWatcher(t, tm, team.DefaultProfile, "s", base, "policy-new", deliver)
	waitWatcherRecord(t, path, time.Second, func(r notificationWatcherRecord) bool {
		return r.OwnerToken == "policy-new" && r.Health == "healthy"
	})
	stopTestNotificationWatcher(t, stop, restarted)
}

func TestNotificationWatcherReleasedGenerationCannotClearOrResurrectLease(t *testing.T) {
	project, _, _ := notificationWatcherTeam(t, team.DefaultProfile, "s")
	path := notificationWatcherRuntimePath(project, team.DefaultProfile, "s")
	now := time.Now().UTC()
	old := notificationWatcherRecord{
		SchemaVersion: notificationWatcherSchema, ProjectDir: project, Profile: team.DefaultProfile,
		Session: "s", NamespaceID: "default/s", PID: 11, OwnerToken: "old", LeaseTTL: "1m",
		LeaseExpiresAt: now.Add(time.Minute), Expected: true, Health: "healthy",
	}
	if err := writeNotificationWatcherRecord(path, old); err != nil {
		t.Fatal(err)
	}
	if err := releaseNotificationWatcherLease(path, &old, "old", now); err != nil {
		t.Fatal(err)
	}
	if err := refreshNotificationWatcherLease(path, &old, "old", now.Add(time.Second)); err == nil || !strings.Contains(err.Error(), "lease lost") {
		t.Fatalf("released generation refreshed tombstone: %v", err)
	}
	assertInactiveWatcherTombstone(t, path)

	newOwner := old
	newOwner.PID = 22
	newOwner.OwnerToken = "new"
	newOwner.Expected = true
	newOwner.Health = "healthy"
	newOwner.LeaseExpiresAt = now.Add(time.Minute)
	if err := writeNotificationWatcherRecord(path, newOwner); err != nil {
		t.Fatal(err)
	}
	if err := releaseNotificationWatcherLease(path, &old, "old", now.Add(2*time.Second)); err == nil || !strings.Contains(err.Error(), "lease lost") {
		t.Fatalf("old generation release against replacement err=%v", err)
	}
	current, err := readNotificationWatcherRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	if current.OwnerToken != "new" || current.PID != 22 || !current.Expected {
		t.Fatalf("old generation altered replacement: %+v", current)
	}
}

func TestStopNotificationWatcherToleratesChildSelfReleaseAndESRCH(t *testing.T) {
	project, _, _ := notificationWatcherTeam(t, team.DefaultProfile, "s")
	path := notificationWatcherRuntimePath(project, team.DefaultProfile, "s")
	host, _ := os.Hostname()
	now := time.Now().UTC()
	rec := notificationWatcherRecord{
		SchemaVersion: notificationWatcherSchema, ProjectDir: project, Profile: team.DefaultProfile,
		Session: "s", NamespaceID: "default/s", PID: 44, Host: host, OwnerToken: "self-release",
		LeaseTTL: "1m", LeaseExpiresAt: now.Add(time.Minute), Expected: true, Health: "healthy",
	}
	if err := writeNotificationWatcherRecord(path, rec); err != nil {
		t.Fatal(err)
	}
	oldAlive, oldMatch, oldSignal := notificationWatcherPIDAlive, notificationWatcherProcessMatch, notificationWatcherSignal
	t.Cleanup(func() {
		notificationWatcherPIDAlive, notificationWatcherProcessMatch, notificationWatcherSignal = oldAlive, oldMatch, oldSignal
	})
	alive := true
	notificationWatcherPIDAlive = func(pid int) bool { return alive && pid == 44 }
	notificationWatcherProcessMatch = func(pid int, _ func(string) bool) bool { return alive && pid == 44 }
	var published notificationWatcherRecord
	notificationWatcherSignal = func(pid int, _ os.Signal) error {
		if pid != 44 {
			return fmt.Errorf("unexpected pid %d", pid)
		}
		childRecord := rec
		if err := releaseNotificationWatcherLease(path, &childRecord, "self-release", time.Now()); err != nil {
			return err
		}
		var err error
		published, err = readNotificationWatcherRecord(path)
		if err != nil {
			return err
		}
		alive = false
		return syscall.ESRCH
	}
	if err := stopNotificationWatcher(project, team.DefaultProfile, "s"); err != nil {
		t.Fatal(err)
	}
	current := assertInactiveWatcherTombstone(t, path)
	if current != published {
		t.Fatalf("self-released tombstone was rewritten:\n got: %+v\nwant: %+v", current, published)
	}
}

func TestStopNotificationWatcherSuccessfulSignalAcceptsChildTombstone(t *testing.T) {
	project, _, _ := notificationWatcherTeam(t, team.DefaultProfile, "s")
	path := notificationWatcherRuntimePath(project, team.DefaultProfile, "s")
	host, _ := os.Hostname()
	now := time.Now().UTC()
	rec := notificationWatcherRecord{
		SchemaVersion: notificationWatcherSchema, ProjectDir: project, Profile: team.DefaultProfile,
		Session: "s", NamespaceID: "default/s", PID: 44, Host: host, OwnerToken: "self-release",
		LeaseTTL: "1m", LeaseExpiresAt: now.Add(time.Minute), Expected: true, Health: "healthy",
	}
	if err := writeNotificationWatcherRecord(path, rec); err != nil {
		t.Fatal(err)
	}
	oldAlive, oldMatch, oldSignal := notificationWatcherPIDAlive, notificationWatcherProcessMatch, notificationWatcherSignal
	t.Cleanup(func() {
		notificationWatcherPIDAlive, notificationWatcherProcessMatch, notificationWatcherSignal = oldAlive, oldMatch, oldSignal
	})
	alive := true
	notificationWatcherPIDAlive = func(pid int) bool { return alive && pid == 44 }
	notificationWatcherProcessMatch = func(pid int, _ func(string) bool) bool { return alive && pid == 44 }
	var published notificationWatcherRecord
	notificationWatcherSignal = func(pid int, _ os.Signal) error {
		if pid != 44 {
			return fmt.Errorf("unexpected pid %d", pid)
		}
		childRecord := rec
		if err := releaseNotificationWatcherLease(path, &childRecord, "self-release", time.Now()); err != nil {
			return err
		}
		var err error
		published, err = readNotificationWatcherRecord(path)
		if err != nil {
			return err
		}
		alive = false
		return nil
	}
	if err := stopNotificationWatcher(project, team.DefaultProfile, "s"); err != nil {
		t.Fatal(err)
	}
	current := assertInactiveWatcherTombstone(t, path)
	if current != published {
		t.Fatalf("self-released tombstone was rewritten:\n got: %+v\nwant: %+v", current, published)
	}
}

func TestStopNotificationWatcherSuccessfulSignalClearsObservedGeneration(t *testing.T) {
	project, _, _ := notificationWatcherTeam(t, team.DefaultProfile, "s")
	path := notificationWatcherRuntimePath(project, team.DefaultProfile, "s")
	host, _ := os.Hostname()
	now := time.Now().UTC()
	rec := notificationWatcherRecord{
		SchemaVersion: notificationWatcherSchema, ProjectDir: project, Profile: team.DefaultProfile,
		Session: "s", NamespaceID: "default/s", PID: 44, Host: host, OwnerToken: "observed-owner",
		LeaseTTL: "1m", LeaseExpiresAt: now.Add(time.Minute), Expected: true, Health: "healthy",
	}
	if err := writeNotificationWatcherRecord(path, rec); err != nil {
		t.Fatal(err)
	}
	oldAlive, oldMatch, oldSignal := notificationWatcherPIDAlive, notificationWatcherProcessMatch, notificationWatcherSignal
	t.Cleanup(func() {
		notificationWatcherPIDAlive, notificationWatcherProcessMatch, notificationWatcherSignal = oldAlive, oldMatch, oldSignal
	})
	alive := true
	notificationWatcherPIDAlive = func(pid int) bool { return alive && pid == 44 }
	notificationWatcherProcessMatch = func(pid int, _ func(string) bool) bool { return alive && pid == 44 }
	notificationWatcherSignal = func(pid int, _ os.Signal) error {
		if pid != 44 {
			return fmt.Errorf("unexpected pid %d", pid)
		}
		alive = false
		return nil
	}
	if err := stopNotificationWatcher(project, team.DefaultProfile, "s"); err != nil {
		t.Fatal(err)
	}
	tombstone := assertInactiveWatcherTombstone(t, path)
	if tombstone.ProjectDir != project || tombstone.NamespaceID != "default/s" {
		t.Fatalf("stop changed watcher scope: %+v", tombstone)
	}
}

func TestStopNotificationWatcherSuccessfulSignalPreservesSameTokenCrossScopeRecord(t *testing.T) {
	project, _, _ := notificationWatcherTeam(t, team.DefaultProfile, "s")
	path := notificationWatcherRuntimePath(project, team.DefaultProfile, "s")
	host, _ := os.Hostname()
	now := time.Now().UTC()
	rec := notificationWatcherRecord{
		SchemaVersion: notificationWatcherSchema, ProjectDir: project, Profile: team.DefaultProfile,
		Session: "s", NamespaceID: "default/s", PID: 44, Host: host, OwnerToken: "observed-owner",
		LeaseTTL: "1m", LeaseExpiresAt: now.Add(time.Minute), Expected: true, Health: "healthy",
	}
	if err := writeNotificationWatcherRecord(path, rec); err != nil {
		t.Fatal(err)
	}
	oldAlive, oldMatch, oldSignal := notificationWatcherPIDAlive, notificationWatcherProcessMatch, notificationWatcherSignal
	t.Cleanup(func() {
		notificationWatcherPIDAlive, notificationWatcherProcessMatch, notificationWatcherSignal = oldAlive, oldMatch, oldSignal
	})
	alive := true
	notificationWatcherPIDAlive = func(pid int) bool { return alive && pid == 44 }
	notificationWatcherProcessMatch = func(pid int, _ func(string) bool) bool { return alive && pid == 44 }
	var published notificationWatcherRecord
	notificationWatcherSignal = func(pid int, _ os.Signal) error {
		if pid != 44 {
			return fmt.Errorf("unexpected pid %d", pid)
		}
		crossScope := rec
		crossScope.ProjectDir = filepath.Join(project, "other")
		published = crossScope
		if err := writeNotificationWatcherRecord(path, crossScope); err != nil {
			return err
		}
		alive = false
		return nil
	}
	err := stopNotificationWatcher(project, team.DefaultProfile, "s")
	if err == nil || !strings.Contains(err.Error(), "scope") {
		t.Fatalf("same-token cross-scope record err=%v", err)
	}
	current, readErr := readNotificationWatcherRecord(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if current != published {
		t.Fatalf("same-token cross-scope record was altered: %+v", current)
	}
}

func TestStopNotificationWatcherSignalErrorPreservesReplacementOwner(t *testing.T) {
	project, _, _ := notificationWatcherTeam(t, team.DefaultProfile, "s")
	path := notificationWatcherRuntimePath(project, team.DefaultProfile, "s")
	host, _ := os.Hostname()
	now := time.Now().UTC()
	rec := notificationWatcherRecord{
		SchemaVersion: notificationWatcherSchema, ProjectDir: project, Profile: team.DefaultProfile,
		Session: "s", NamespaceID: "default/s", PID: 44, Host: host, OwnerToken: "old-owner",
		LeaseTTL: "1m", LeaseExpiresAt: now.Add(time.Minute), Expected: true, Health: "healthy",
	}
	if err := writeNotificationWatcherRecord(path, rec); err != nil {
		t.Fatal(err)
	}
	oldAlive, oldMatch, oldSignal := notificationWatcherPIDAlive, notificationWatcherProcessMatch, notificationWatcherSignal
	t.Cleanup(func() {
		notificationWatcherPIDAlive, notificationWatcherProcessMatch, notificationWatcherSignal = oldAlive, oldMatch, oldSignal
	})
	alive := true
	notificationWatcherPIDAlive = func(pid int) bool { return alive && pid == 44 }
	notificationWatcherProcessMatch = func(pid int, _ func(string) bool) bool { return alive && pid == 44 }
	var published notificationWatcherRecord
	notificationWatcherSignal = func(pid int, _ os.Signal) error {
		if pid != 44 {
			return fmt.Errorf("unexpected pid %d", pid)
		}
		replacement := rec
		replacement.PID = 55
		replacement.OwnerToken = "replacement-owner"
		replacement.HeartbeatAt = time.Now().UTC()
		replacement.LeaseExpiresAt = time.Now().Add(time.Minute).UTC()
		replacement.UpdatedAt = time.Now().UTC()
		published = replacement
		if err := writeNotificationWatcherRecord(path, replacement); err != nil {
			return err
		}
		alive = false
		return syscall.ESRCH
	}
	err := stopNotificationWatcher(project, team.DefaultProfile, "s")
	if err == nil || !strings.Contains(err.Error(), "ownership changed") {
		t.Fatalf("replacement owner signal race err=%v", err)
	}
	current, readErr := readNotificationWatcherRecord(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if current != published {
		t.Fatalf("replacement owner was altered: %+v", current)
	}
}

func TestStopNotificationWatcherSuccessfulSignalPreservesReplacementOwner(t *testing.T) {
	project, _, _ := notificationWatcherTeam(t, team.DefaultProfile, "s")
	path := notificationWatcherRuntimePath(project, team.DefaultProfile, "s")
	host, _ := os.Hostname()
	now := time.Now().UTC()
	rec := notificationWatcherRecord{
		SchemaVersion: notificationWatcherSchema, ProjectDir: project, Profile: team.DefaultProfile,
		Session: "s", NamespaceID: "default/s", PID: 44, Host: host, OwnerToken: "old-owner",
		LeaseTTL: "1m", LeaseExpiresAt: now.Add(time.Minute), Expected: true, Health: "healthy",
	}
	if err := writeNotificationWatcherRecord(path, rec); err != nil {
		t.Fatal(err)
	}
	oldAlive, oldMatch, oldSignal := notificationWatcherPIDAlive, notificationWatcherProcessMatch, notificationWatcherSignal
	t.Cleanup(func() {
		notificationWatcherPIDAlive, notificationWatcherProcessMatch, notificationWatcherSignal = oldAlive, oldMatch, oldSignal
	})
	alive := true
	notificationWatcherPIDAlive = func(pid int) bool { return alive && pid == 44 }
	notificationWatcherProcessMatch = func(pid int, _ func(string) bool) bool { return alive && pid == 44 }
	var published notificationWatcherRecord
	notificationWatcherSignal = func(pid int, _ os.Signal) error {
		if pid != 44 {
			return fmt.Errorf("unexpected pid %d", pid)
		}
		replacement := rec
		replacement.PID = 55
		replacement.OwnerToken = "replacement-owner"
		replacement.HeartbeatAt = time.Now().UTC()
		replacement.LeaseExpiresAt = time.Now().Add(time.Minute).UTC()
		replacement.UpdatedAt = time.Now().UTC()
		published = replacement
		if err := writeNotificationWatcherRecord(path, replacement); err != nil {
			return err
		}
		alive = false
		return nil
	}
	err := stopNotificationWatcher(project, team.DefaultProfile, "s")
	if err == nil || !strings.Contains(err.Error(), "ownership changed") {
		t.Fatalf("replacement owner successful signal race err=%v", err)
	}
	current, readErr := readNotificationWatcherRecord(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if current != published {
		t.Fatalf("replacement owner was altered: %+v", current)
	}
}

func TestStopNotificationWatcherRejectsInvalidTombstoneAfterSignal(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*notificationWatcherRecord)
	}{
		{
			name: "cross-scope",
			mutate: func(rec *notificationWatcherRecord) {
				rec.ProjectDir = filepath.Join(rec.ProjectDir, "other")
			},
		},
		{
			name: "malformed-future-lease",
			mutate: func(rec *notificationWatcherRecord) {
				rec.LeaseExpiresAt = time.Now().Add(time.Minute).UTC()
			},
		},
	}
	signalResults := []struct {
		name string
		err  error
	}{{name: "success"}, {name: "esrch", err: syscall.ESRCH}}
	for _, tt := range tests {
		for _, signalResult := range signalResults {
			t.Run(tt.name+"/"+signalResult.name, func(t *testing.T) {
				project, _, _ := notificationWatcherTeam(t, team.DefaultProfile, "s")
				path := notificationWatcherRuntimePath(project, team.DefaultProfile, "s")
				host, _ := os.Hostname()
				now := time.Now().UTC()
				rec := notificationWatcherRecord{
					SchemaVersion: notificationWatcherSchema, ProjectDir: project, Profile: team.DefaultProfile,
					Session: "s", NamespaceID: "default/s", PID: 44, Host: host, OwnerToken: "old-owner",
					LeaseTTL: "1m", LeaseExpiresAt: now.Add(time.Minute), Expected: true, Health: "healthy",
				}
				if err := writeNotificationWatcherRecord(path, rec); err != nil {
					t.Fatal(err)
				}
				oldAlive, oldMatch, oldSignal := notificationWatcherPIDAlive, notificationWatcherProcessMatch, notificationWatcherSignal
				t.Cleanup(func() {
					notificationWatcherPIDAlive, notificationWatcherProcessMatch, notificationWatcherSignal = oldAlive, oldMatch, oldSignal
				})
				alive := true
				notificationWatcherPIDAlive = func(pid int) bool { return alive && pid == 44 }
				notificationWatcherProcessMatch = func(pid int, _ func(string) bool) bool { return alive && pid == 44 }
				var published notificationWatcherRecord
				notificationWatcherSignal = func(pid int, _ os.Signal) error {
					if pid != 44 {
						return fmt.Errorf("unexpected pid %d", pid)
					}
					tombstone := rec
					tombstone.PID = 0
					tombstone.OwnerToken = ""
					tombstone.Expected = false
					tombstone.Health = "inactive"
					tombstone.HeartbeatAt = time.Now().UTC()
					tombstone.LeaseExpiresAt = time.Now().UTC()
					tombstone.UpdatedAt = time.Now().UTC()
					tt.mutate(&tombstone)
					published = tombstone
					if err := writeNotificationWatcherRecord(path, tombstone); err != nil {
						return err
					}
					alive = false
					return signalResult.err
				}
				err := stopNotificationWatcher(project, team.DefaultProfile, "s")
				if err == nil || !strings.Contains(err.Error(), "invalid inactive tombstone") {
					t.Fatalf("invalid tombstone err=%v", err)
				}
				current, readErr := readNotificationWatcherRecord(path)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if current != published {
					t.Fatalf("invalid tombstone was altered: %+v", current)
				}
			})
		}
	}
}

func TestStopNotificationWatcherSignalErrorPreservesObservedOwner(t *testing.T) {
	project, _, _ := notificationWatcherTeam(t, team.DefaultProfile, "s")
	path := notificationWatcherRuntimePath(project, team.DefaultProfile, "s")
	host, _ := os.Hostname()
	now := time.Now().UTC()
	rec := notificationWatcherRecord{
		SchemaVersion: notificationWatcherSchema, ProjectDir: project, Profile: team.DefaultProfile,
		Session: "s", NamespaceID: "default/s", PID: 44, Host: host, OwnerToken: "observed-owner",
		LeaseTTL: "1m", LeaseExpiresAt: now.Add(time.Minute), Expected: true, Health: "healthy",
	}
	if err := writeNotificationWatcherRecord(path, rec); err != nil {
		t.Fatal(err)
	}
	oldAlive, oldMatch, oldSignal := notificationWatcherPIDAlive, notificationWatcherProcessMatch, notificationWatcherSignal
	t.Cleanup(func() {
		notificationWatcherPIDAlive, notificationWatcherProcessMatch, notificationWatcherSignal = oldAlive, oldMatch, oldSignal
	})
	notificationWatcherPIDAlive = func(pid int) bool { return pid == 44 }
	notificationWatcherProcessMatch = func(pid int, _ func(string) bool) bool { return pid == 44 }
	notificationWatcherSignal = func(int, os.Signal) error { return syscall.EPERM }
	err := stopNotificationWatcher(project, team.DefaultProfile, "s")
	if !errors.Is(err, syscall.EPERM) {
		t.Fatalf("signal error=%v, want wrapped EPERM", err)
	}
	current, readErr := readNotificationWatcherRecord(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if current != rec {
		t.Fatalf("observed owner was altered after signal error: %+v", current)
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

func TestNotificationWatcherReadinessRequiresScanForDegradedOnly(t *testing.T) {
	scanned := time.Now().UTC()
	tests := []struct {
		name   string
		health string
		scan   time.Time
		ready  bool
	}{
		{name: "healthy before scan", health: "healthy", ready: true},
		{name: "external active before scan", health: "external-active", ready: true},
		{name: "degraded before fallback scan", health: "degraded", ready: false},
		{name: "degraded after fallback scan", health: "degraded", scan: scanned, ready: true},
		{name: "starting", health: "starting", ready: false},
		{name: "unhealthy", health: "unhealthy", scan: scanned, ready: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := notificationWatcherReady(notificationWatcherStatus{Health: tt.health, LastScanAt: tt.scan})
			if got != tt.ready {
				t.Fatalf("ready=%t want=%t", got, tt.ready)
			}
		})
	}
}

func TestNotificationWatcherStartupPollingWaitsForDegradedFallbackScan(t *testing.T) {
	project, tm, base := notificationWatcherTeam(t, team.DefaultProfile, "s")
	path := notificationWatcherRuntimePath(project, team.DefaultProfile, "s")
	oldSpawn, oldSleep := notificationWatcherSpawn, notificationWatcherSleep
	t.Cleanup(func() {
		notificationWatcherSpawn, notificationWatcherSleep = oldSpawn, oldSleep
	})
	spawned := false
	var sleeps atomic.Int32
	notificationWatcherSpawn = func(projectDir, profile, session, _ string, token string) (notificationWatcherProcess, error) {
		spawned = true
		now := time.Now().UTC()
		return nil, writeNotificationWatcherRecord(path, notificationWatcherRecord{
			SchemaVersion: notificationWatcherSchema, ProjectDir: projectDir, Profile: profile,
			Session: session, NamespaceID: squadnamespace.ID(profile, session), PID: 7, Host: "remote-host",
			OwnerToken: token, LeaseTTL: "1m", LeaseExpiresAt: now.Add(time.Minute), HeartbeatAt: now,
			Expected: true, Health: "degraded", LastError: "periodic fallback initial scan pending",
		})
	}
	notificationWatcherSleep = func(time.Duration) {
		if sleeps.Add(1) != 1 {
			return
		}
		rec, err := readNotificationWatcherRecord(path)
		if err != nil {
			t.Fatal(err)
		}
		rec.LastScanAt = time.Now().UTC()
		if err := writeNotificationWatcherRecord(path, rec); err != nil {
			t.Fatal(err)
		}
	}
	if err := reconcileNotificationWatcherStarted(tm, team.DefaultProfile, "s", base); err != nil {
		t.Fatal(err)
	}
	if !spawned || sleeps.Load() == 0 {
		t.Fatalf("startup accepted degraded watcher before fallback scan: spawned=%t sleeps=%d", spawned, sleeps.Load())
	}
}

func TestNotificationWatcherRemoteHealthIsNotMaskedAndNeverSpawnsRival(t *testing.T) {
	project, tm, base := notificationWatcherTeam(t, team.DefaultProfile, "s")
	path := notificationWatcherRuntimePath(project, team.DefaultProfile, "s")
	writeRemote := func(health, detail string, scanned bool) {
		t.Helper()
		rec := notificationWatcherRecord{SchemaVersion: 1, ProjectDir: project, Profile: team.DefaultProfile, Session: "s", NamespaceID: "default/s", PID: 7, Host: "remote-host", OwnerToken: "remote-health", LeaseTTL: "1m", LeaseExpiresAt: time.Now().Add(time.Minute), Expected: true, Health: health, LastError: detail}
		if scanned {
			rec.LastScanAt = time.Now().Add(-time.Second).UTC()
		}
		if err := writeNotificationWatcherRecord(path, rec); err != nil {
			t.Fatal(err)
		}
	}
	oldSpawn := notificationWatcherSpawn
	t.Cleanup(func() { notificationWatcherSpawn = oldSpawn })
	spawned := false
	notificationWatcherSpawn = func(string, string, string, string, string) (notificationWatcherProcess, error) {
		spawned = true
		return nil, nil
	}

	writeRemote("degraded", "initial fallback scan pending", false)
	status := inspectNotificationWatcher(tm, team.DefaultProfile, "s", time.Now())
	if status.Health != "degraded" || !status.LastScanAt.IsZero() {
		t.Fatalf("remote initial degraded status=%+v", status)
	}
	if err := reconcileNotificationWatcherStarted(tm, team.DefaultProfile, "s", base); err == nil || !strings.Contains(err.Error(), "active but unhealthy") {
		t.Fatalf("remote initial degraded reconcile=%v", err)
	}
	if spawned {
		t.Fatal("remote initial degraded lease spawned a rival")
	}
	current, err := readNotificationWatcherRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	if current.OwnerToken != "remote-health" || !current.Expected {
		t.Fatalf("remote initial degraded lease was not preserved: %+v", current)
	}

	writeRemote("starting", "initial scan pending", false)
	status = inspectNotificationWatcher(tm, team.DefaultProfile, "s", time.Now())
	if status.Health != "unhealthy" || !strings.Contains(status.Reason, "remote-host") || !strings.Contains(status.Reason, "initial scan pending") {
		t.Fatalf("remote unhealthy status=%+v", status)
	}
	if err := reconcileNotificationWatcherStarted(tm, team.DefaultProfile, "s", base); err == nil || !strings.Contains(err.Error(), "active but unhealthy") {
		t.Fatalf("remote unhealthy reconcile=%v", err)
	}
	if spawned {
		t.Fatal("remote unhealthy lease spawned a rival")
	}

	writeRemote("healthy", "", false)
	status = inspectNotificationWatcher(tm, team.DefaultProfile, "s", time.Now())
	if status.Health != "external-active" || !status.LastScanAt.IsZero() {
		t.Fatalf("remote initial external-active status=%+v", status)
	}
	if err := reconcileNotificationWatcherStarted(tm, team.DefaultProfile, "s", base); err != nil {
		t.Fatalf("remote initial external-active reconcile=%v", err)
	}
	if spawned {
		t.Fatal("remote initial external-active lease spawned a rival")
	}

	writeRemote("degraded", "fsnotify unavailable", true)
	status = inspectNotificationWatcher(tm, team.DefaultProfile, "s", time.Now())
	if status.Health != "degraded" || !strings.Contains(status.Reason, "remote-host") || !strings.Contains(status.Reason, "fsnotify unavailable") {
		t.Fatalf("remote degraded status=%+v", status)
	}
	if err := reconcileNotificationWatcherStarted(tm, team.DefaultProfile, "s", base); err != nil {
		t.Fatalf("remote degraded reconcile=%v", err)
	}
	if spawned {
		t.Fatal("remote degraded lease spawned a rival")
	}
	warnings := statusNotificationWatcherWarnings(project, team.DefaultProfile, "s", time.Now())
	if len(warnings) != 1 || !strings.Contains(warnings[0].Detail, "remote-host") {
		t.Fatalf("remote degraded status warnings=%+v", warnings)
	}
	doctor := doctorCheckNotificationWatcher(doctorExecution{ProjectDir: project, Profile: team.DefaultProfile, Probe: duplicateLaunchProbe{Now: time.Now}}, "s")
	if doctor.Status != doctorWarn || !strings.Contains(doctor.Detail, "remote-host") {
		t.Fatalf("remote degraded doctor=%+v", doctor)
	}
	row := sessionBoardRow{State: boardStateRunning}
	enrichBoardNotificationWatcher([]boardProfile{{Name: team.DefaultProfile, Team: tm}}, state.Session{Name: "s", TeamProfile: team.DefaultProfile}, time.Now(), &row)
	if row.State != boardStateDegraded || row.NotificationWatcher == nil || row.NotificationWatcher.Health != "degraded" {
		t.Fatalf("remote degraded board=%+v state=%s", row.NotificationWatcher, row.State)
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

func stubHealthyNotificationWatcherSpawn(t *testing.T) {
	t.Helper()
	oldSpawn, oldAlive, oldMatch, oldSignal := notificationWatcherSpawn, notificationWatcherPIDAlive, notificationWatcherProcessMatch, notificationWatcherSignal
	t.Cleanup(func() {
		notificationWatcherSpawn, notificationWatcherPIDAlive, notificationWatcherProcessMatch, notificationWatcherSignal = oldSpawn, oldAlive, oldMatch, oldSignal
	})
	const pid = 4242
	alive := true
	notificationWatcherPIDAlive = func(got int) bool { return alive && got == pid }
	notificationWatcherProcessMatch = func(got int, _ func(string) bool) bool { return alive && got == pid }
	notificationWatcherSignal = func(got int, _ os.Signal) error {
		if got == pid {
			alive = false
		}
		return nil
	}
	notificationWatcherSpawn = func(projectDir, profile, session, baseRoot, token string) (notificationWatcherProcess, error) {
		host, _ := os.Hostname()
		now := time.Now().UTC()
		rec := notificationWatcherRecord{
			SchemaVersion: notificationWatcherSchema, ProjectDir: projectDir,
			Profile: squadnamespace.NormalizeProfile(profile), Session: session,
			NamespaceID: squadnamespace.ID(profile, session), PID: pid, Host: host,
			Owner: "supervised-test", OwnerToken: token, LeaseTTL: defaultNotificationWatcherTTL.String(),
			LeaseExpiresAt: now.Add(defaultNotificationWatcherTTL), HeartbeatAt: now, LastScanAt: now,
			StatePath: defaultNotifyStatePath(projectDir), Expected: true, Health: "healthy", UpdatedAt: now,
		}
		if err := writeNotificationWatcherRecord(notificationWatcherRuntimePath(projectDir, profile, session), rec); err != nil {
			return nil, err
		}
		return watcherTestProcess{pid: pid}, nil
	}
}

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

func TestLeadPaneDirectGateDeliversOnceBeforeRenotify(t *testing.T) {
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

func TestStopNotificationWatcherPreservesPreexistingInactiveTombstone(t *testing.T) {
	project, _, _ := notificationWatcherTeam(t, team.DefaultProfile, "s")
	path := notificationWatcherRuntimePath(project, team.DefaultProfile, "s")
	now := time.Now().UTC()
	seeded := notificationWatcherRecord{
		SchemaVersion: notificationWatcherSchema, ProjectDir: project, Profile: team.DefaultProfile,
		Session: "s", NamespaceID: "default/s", Owner: "prior-owner", StatePath: defaultNotifyStatePath(project),
		Expected: false, Health: "inactive", HeartbeatAt: now.Add(-time.Second), LastScanAt: now.Add(-time.Minute),
		LeaseExpiresAt: now.Add(-time.Second), UpdatedAt: now.Add(-time.Second),
	}
	if err := writeNotificationWatcherRecord(path, seeded); err != nil {
		t.Fatal(err)
	}
	if err := stopNotificationWatcher(project, team.DefaultProfile, "s"); err != nil {
		t.Fatal(err)
	}
	current, err := readNotificationWatcherRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	if current != seeded {
		t.Fatalf("preexisting inactive tombstone was altered:\n got: %+v\nwant: %+v", current, seeded)
	}
}

func TestStopNotificationWatcherPreservesInvalidPreexistingBlankOwnerRecord(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*notificationWatcherRecord, time.Time)
	}{
		{
			name: "cross-scope-tombstone",
			mutate: func(rec *notificationWatcherRecord, _ time.Time) {
				rec.ProjectDir = filepath.Join(rec.ProjectDir, "other")
			},
		},
		{
			name: "malformed-tombstone",
			mutate: func(rec *notificationWatcherRecord, now time.Time) {
				rec.LeaseExpiresAt = now.Add(time.Minute)
			},
		},
		{
			name: "active-looking",
			mutate: func(rec *notificationWatcherRecord, now time.Time) {
				rec.PID = 44
				rec.Host = "local-host"
				rec.Expected = true
				rec.Health = "healthy"
				rec.LeaseTTL = "1m"
				rec.LeaseExpiresAt = now.Add(time.Minute)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			project, _, _ := notificationWatcherTeam(t, team.DefaultProfile, "s")
			path := notificationWatcherRuntimePath(project, team.DefaultProfile, "s")
			now := time.Now().UTC()
			seeded := notificationWatcherRecord{
				SchemaVersion: notificationWatcherSchema, ProjectDir: project, Profile: team.DefaultProfile,
				Session: "s", NamespaceID: "default/s", StatePath: defaultNotifyStatePath(project),
				Expected: false, Health: "inactive", HeartbeatAt: now, LeaseExpiresAt: now, UpdatedAt: now,
			}
			tt.mutate(&seeded, now)
			if err := writeNotificationWatcherRecord(path, seeded); err != nil {
				t.Fatal(err)
			}
			err := stopNotificationWatcher(project, team.DefaultProfile, "s")
			if err == nil || !strings.Contains(err.Error(), "invalid blank-owner runtime record") {
				t.Fatalf("invalid blank-owner record err=%v", err)
			}
			current, readErr := readNotificationWatcherRecord(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if current != seeded {
				t.Fatalf("invalid blank-owner record was altered:\n got: %+v\nwant: %+v", current, seeded)
			}
		})
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
