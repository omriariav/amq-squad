package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/flock"
	"github.com/omriariav/amq-squad/v2/internal/procinfo"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

const notificationAMQRealChildEnv = "AMQ_SQUAD_NOTIFICATION_WATCH_REAL_CHILD"

func TestNotificationAMQWatchRealChildHelper(t *testing.T) {
	if os.Getenv(notificationAMQRealChildEnv) != "1" {
		return
	}
	time.Sleep(time.Hour)
}

func TestManagedNotificationAMQWatchCollectsExactMailboxOncePerSignal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	project := t.TempDir()
	execution := notificationWatcherExecution{
		ProjectDir: project,
		Profile:    team.DefaultProfile,
		Session:    "run",
		BaseRoot:   filepath.Join(project, "mail"),
	}
	amqCtx := notificationAMQContext(execution, team.DefaultProfile, "user")
	var watches atomic.Int32
	var collects atomic.Int32
	watch := func(ctx context.Context, got amqContext, timeout time.Duration) (bool, error) {
		if got.Root != filepath.Join(execution.BaseRoot, execution.Session) || got.Me != "user" || got.PinMode != amqPinExactRoot {
			t.Errorf("watch context = %+v", got)
		}
		if timeout != 20*time.Millisecond {
			t.Errorf("watch timeout = %s", timeout)
		}
		if watches.Add(1) == 1 {
			return true, nil
		}
		<-ctx.Done()
		return false, ctx.Err()
	}
	collect := func(_ context.Context, got amqContext) (bool, error) {
		if got.Root != amqCtx.Root || got.Me != amqCtx.Me {
			t.Errorf("collect context = %+v", got)
		}
		collects.Add(1)
		return true, nil
	}
	results := make(chan notificationAMQWatchResult, 1)
	go runManagedNotificationAMQWatch(ctx, amqCtx, false, 20*time.Millisecond, 3, time.Millisecond, 2*time.Millisecond, watch, collect, results)
	result := <-results
	if !result.Signaled || !result.CollectAttempted || !result.Collected || result.Err != nil {
		t.Fatalf("result = %+v", result)
	}
	if collects.Load() != 1 {
		t.Fatalf("collect calls = %d", collects.Load())
	}
	cancel()
	select {
	case _, ok := <-results:
		if ok {
			t.Fatal("unexpected result after cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("managed watch did not stop after cancellation")
	}
}

func TestManagedNotificationAMQWatchReplaysCollectBeforeFirstSignal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	results := make(chan notificationAMQWatchResult, 1)
	var watches atomic.Int32
	var collects atomic.Int32
	go runManagedNotificationAMQWatch(
		ctx,
		amqContext{},
		true,
		time.Hour,
		3,
		time.Millisecond,
		time.Millisecond,
		func(ctx context.Context, _ amqContext, _ time.Duration) (bool, error) {
			watches.Add(1)
			<-ctx.Done()
			return false, ctx.Err()
		},
		func(context.Context, amqContext) (bool, error) {
			collects.Add(1)
			return true, nil
		},
		results,
	)
	result := <-results
	if result.Signaled || !result.CollectAttempted || !result.Collected ||
		result.PendingCollect || result.Err != nil || collects.Load() != 1 {
		t.Fatalf("startup collect replay = %+v collects=%d", result, collects.Load())
	}
	cancel()
	select {
	case _, ok := <-results:
		if ok {
			t.Fatal("unexpected result after startup replay cancellation")
		}
	case <-time.After(time.Second):
		t.Fatalf("startup replay watch did not stop; watches=%d", watches.Load())
	}
}

func TestManagedNotificationAMQWatchRetriesPendingCollectWithoutAnotherSignal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := make(chan notificationAMQWatchResult, 3)
	var watches atomic.Int32
	var collects atomic.Int32
	retryStarted := make(chan struct{})
	allowRetry := make(chan struct{})
	go runManagedNotificationAMQWatch(
		ctx,
		amqContext{},
		false,
		time.Second,
		3,
		time.Millisecond,
		time.Millisecond,
		func(ctx context.Context, _ amqContext, _ time.Duration) (bool, error) {
			if watches.Add(1) == 1 {
				return true, nil
			}
			<-ctx.Done()
			return false, ctx.Err()
		},
		func(context.Context, amqContext) (bool, error) {
			if collects.Add(1) == 1 {
				return true, errors.New("journal completion failed")
			}
			close(retryStarted)
			<-allowRetry
			return true, nil
		},
		results,
	)
	failed := <-results
	retryState := <-results
	<-retryStarted
	if watches.Load() != 1 {
		t.Fatalf("pending collect waited for another watch signal: watches=%d", watches.Load())
	}
	close(allowRetry)
	recovered := <-results
	if failed.Err == nil || !failed.PendingCollect || failed.CollectRetries != 0 ||
		failed.WatchRestarts != 0 {
		t.Fatalf("failed pending collect = %+v watches=%d", failed, watches.Load())
	}
	if !retryState.RetryStarted || !retryState.PendingCollect ||
		retryState.CollectRetries != 1 || retryState.WatchRestarts != 0 ||
		retryState.FailureStreak != 1 || retryState.Err == nil {
		t.Fatalf("pending collect retry state = %+v", retryState)
	}
	if recovered.Err != nil || recovered.PendingCollect || !recovered.CollectAttempted ||
		!recovered.Collected || recovered.CollectRetries != 1 || collects.Load() != 2 {
		t.Fatalf("recovered pending collect = %+v watches=%d collects=%d", recovered, watches.Load(), collects.Load())
	}
	cancel()
	select {
	case _, ok := <-results:
		if ok {
			t.Fatal("unexpected result after cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("managed watch did not stop after pending collect recovery")
	}
}

func TestManagedNotificationAMQWatchBoundsCrashRestarts(t *testing.T) {
	results := make(chan notificationAMQWatchResult, 1)
	var calls atomic.Int32
	var collectCalled atomic.Bool
	go runManagedNotificationAMQWatch(
		context.Background(),
		amqContext{},
		false,
		time.Millisecond,
		3,
		time.Millisecond,
		2*time.Millisecond,
		func(context.Context, amqContext, time.Duration) (bool, error) {
			calls.Add(1)
			return false, errors.New("watch crashed")
		},
		func(context.Context, amqContext) (bool, error) {
			collectCalled.Store(true)
			return false, errors.New("unexpected collect")
		},
		results,
	)
	for want := 1; want <= 3; want++ {
		result := <-results
		wantRestarts := want - 1
		if result.FailureStreak != want || result.WatchRestarts != wantRestarts ||
			result.Exhausted != (want == 3) || !strings.Contains(result.Err.Error(), "watch crashed") {
			t.Fatalf("attempt %d result = %+v", want, result)
		}
		if want < 3 {
			retry := <-results
			if !retry.RetryStarted || retry.WatchRestarts != want ||
				retry.FailureStreak != want || retry.Err == nil {
				t.Fatalf("attempt %d retry = %+v", want, retry)
			}
		}
	}
	if _, ok := <-results; ok {
		t.Fatal("managed watch remained active after bounded exhaustion")
	}
	if calls.Load() != 3 {
		t.Fatalf("watch calls = %d", calls.Load())
	}
	if collectCalled.Load() {
		t.Fatal("collect called after failed watch")
	}
}

func TestManagedNotificationAMQWatchRecoveryResetsFailureCount(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := make(chan notificationAMQWatchResult, 2)
	var calls atomic.Int32
	go runManagedNotificationAMQWatch(
		ctx,
		amqContext{},
		false,
		time.Millisecond,
		3,
		time.Millisecond,
		time.Millisecond,
		func(ctx context.Context, _ amqContext, _ time.Duration) (bool, error) {
			switch calls.Add(1) {
			case 1:
				return false, errors.New("temporary")
			case 2:
				return false, nil
			default:
				<-ctx.Done()
				return false, ctx.Err()
			}
		},
		func(context.Context, amqContext) (bool, error) { return false, nil },
		results,
	)
	failed := <-results
	retry := <-results
	recovered := <-results
	if failed.FailureStreak != 1 || failed.WatchRestarts != 0 || failed.Err == nil {
		t.Fatalf("failure result = %+v", failed)
	}
	if !retry.RetryStarted || retry.FailureStreak != 1 || retry.WatchRestarts != 1 || retry.Err == nil {
		t.Fatalf("retry result = %+v", retry)
	}
	if recovered.Err != nil || recovered.FailureStreak != 0 || recovered.WatchRestarts != 1 || recovered.Signaled {
		t.Fatalf("recovery result = %+v", recovered)
	}
	cancel()
}

func TestManagedNotificationAMQWatchOwnershipLossIsFatalWithoutRetry(t *testing.T) {
	results := make(chan notificationAMQWatchResult, 1)
	var calls atomic.Int32
	go runManagedNotificationAMQWatch(
		context.Background(),
		amqContext{},
		false,
		time.Second,
		5,
		time.Millisecond,
		time.Millisecond,
		func(context.Context, amqContext, time.Duration) (bool, error) {
			calls.Add(1)
			return false, fmt.Errorf("%w: replaced generation", errNotificationAMQOwnershipLost)
		},
		func(context.Context, amqContext) (bool, error) { return false, nil },
		results,
	)
	result := <-results
	if !result.Fatal || !result.Exhausted || result.FailureStreak != 1 || result.WatchRestarts != 0 ||
		!errors.Is(result.Err, errNotificationAMQOwnershipLost) {
		t.Fatalf("ownership-loss result = %+v", result)
	}
	if _, ok := <-results; ok {
		t.Fatal("ownership loss did not stop managed watch")
	}
	if calls.Load() != 1 {
		t.Fatalf("ownership-loss retries = %d", calls.Load())
	}
}

func TestManagedNotificationAMQWatchStopCancelsBlockedWatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	results := make(chan notificationAMQWatchResult, 1)
	go runManagedNotificationAMQWatch(
		ctx,
		amqContext{},
		false,
		time.Hour,
		3,
		time.Millisecond,
		time.Millisecond,
		func(ctx context.Context, _ amqContext, _ time.Duration) (bool, error) {
			close(started)
			<-ctx.Done()
			return false, ctx.Err()
		},
		func(context.Context, amqContext) (bool, error) { return false, nil },
		results,
	)
	<-started
	cancel()
	select {
	case _, ok := <-results:
		if ok {
			t.Fatal("cancellation produced a retry result")
		}
	case <-time.After(time.Second):
		t.Fatal("blocked managed watch did not stop")
	}
}

func TestNotificationWatcherReapsRealAMQChildOnEveryExitClass(t *testing.T) {
	for _, exitClass := range []string{"signal", "policy_disabled", "ownership_lost"} {
		t.Run(exitClass, func(t *testing.T) {
			project, _, base := notificationWatcherTeam(t, team.DefaultProfile, "s")
			stop := make(chan os.Signal, 1)
			done := make(chan error, 1)
			childStarted := make(chan int, 1)
			childWaited := make(chan error, 1)
			token := "real-child-" + exitClass
			go func() {
				done <- executeNotificationWatcher(notificationWatcherExecution{
					ProjectDir: project, Profile: team.DefaultProfile, Session: "s", BaseRoot: base, Token: token,
					TTL: time.Second, Heartbeat: 20 * time.Millisecond, Rescan: 20 * time.Millisecond,
					Stop: stop,
					WatchAMQ: func(ctx context.Context, _ amqContext, _ time.Duration) (bool, error) {
						cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNotificationAMQWatchRealChildHelper$")
						cmd.Env = append(os.Environ(), notificationAMQRealChildEnv+"=1")
						if err := cmd.Start(); err != nil {
							return false, err
						}
						childStarted <- cmd.Process.Pid
						err := cmd.Wait()
						childWaited <- err
						if ctx.Err() != nil {
							return false, ctx.Err()
						}
						return false, err
					},
					CollectAMQ: func(context.Context, amqContext) (bool, error) {
						return false, nil
					},
					AMQWatchTimeout: time.Hour, AMQWatchMaxRetries: 3,
					Deliver: func(context.Context, time.Time) (notifyDeliverySummary, error) {
						return notifyDeliverySummary{}, nil
					},
				})
			}()
			var childPID int
			select {
			case childPID = <-childStarted:
			case <-time.After(time.Second):
				t.Fatal("real AMQ child did not start")
			}
			path := notificationWatcherRuntimePath(project, team.DefaultProfile, "s")
			rec := waitWatcherRecord(t, path, time.Second, func(r notificationWatcherRecord) bool {
				return r.OwnerToken == token && r.Health == "healthy" && !r.LastScanAt.IsZero()
			})
			switch exitClass {
			case "signal":
				stop <- os.Interrupt
			case "policy_disabled":
				current, err := team.ReadProfile(project, team.DefaultProfile)
				if err != nil {
					t.Fatal(err)
				}
				current.Operator.Notifications.Enabled = false
				if err := team.WriteProfile(project, team.DefaultProfile, current); err != nil {
					t.Fatal(err)
				}
			case "ownership_lost":
				if err := releaseNotificationWatcherLease(path, &rec, token, time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			var watcherErr error
			select {
			case watcherErr = <-done:
			case <-time.After(2 * time.Second):
				t.Fatalf("%s exit did not join the real AMQ child", exitClass)
			}
			if exitClass == "ownership_lost" {
				if watcherErr == nil || !strings.Contains(watcherErr.Error(), "lease lost") {
					t.Fatalf("ownership loss error = %v", watcherErr)
				}
			} else if watcherErr != nil {
				t.Fatalf("%s exit error = %v", exitClass, watcherErr)
			}
			select {
			case <-childWaited:
			case <-time.After(250 * time.Millisecond):
				t.Fatalf("%s exit returned before reaping child pid %d", exitClass, childPID)
			}
			if procinfo.Alive(childPID) {
				t.Fatalf("%s exit left real AMQ child pid %d alive", exitClass, childPID)
			}
			if exitClass != "ownership_lost" {
				assertInactiveWatcherTombstone(t, path)
			}
		})
	}
}

func TestNotificationWatcherStopCancelsRealCollectWaitingOnJournalLock(t *testing.T) {
	project, _, base := notificationWatcherTeam(t, team.DefaultProfile, "s")
	execution := notificationWatcherExecution{
		ProjectDir: project, Profile: team.DefaultProfile, Session: "s", BaseRoot: base,
	}
	amqCtx := notificationAMQContext(execution, team.DefaultProfile, "user")
	journal := newCollectJournal(amqCtx)
	if err := os.MkdirAll(journal.Root, collectJournalDirectoryPerm); err != nil {
		t.Fatal(err)
	}
	held, err := flock.AcquireExclusive(filepath.Join(journal.Root, ".lock"))
	if errors.Is(err, flock.ErrUnsupported) {
		t.Skip("platform does not provide required advisory locks")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()

	stop := make(chan os.Signal, 1)
	done := make(chan error, 1)
	collectStarted := make(chan struct{})
	go func() {
		done <- executeNotificationWatcher(notificationWatcherExecution{
			ProjectDir: project, Profile: team.DefaultProfile, Session: "s", BaseRoot: base, Token: "real-collect-stop",
			TTL: time.Second, Heartbeat: 20 * time.Millisecond, Rescan: time.Hour,
			Stop: stop,
			WatchAMQ: func(context.Context, amqContext, time.Duration) (bool, error) {
				return true, nil
			},
			CollectAMQ: func(ctx context.Context, got amqContext) (bool, error) {
				close(collectStarted)
				return collectNotificationAMQ(ctx, got)
			},
			AMQWatchTimeout: time.Hour, AMQWatchMaxRetries: 3,
			Deliver: func(context.Context, time.Time) (notifyDeliverySummary, error) {
				return notifyDeliverySummary{}, nil
			},
		})
	}()
	select {
	case <-collectStarted:
	case <-time.After(time.Second):
		t.Fatal("real collect did not start")
	}
	started := time.Now()
	stop <- os.Interrupt
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watcher stop did not cancel real collect waiting on the journal lock")
	}
	if elapsed := time.Since(started); elapsed >= 2*time.Second {
		t.Fatalf("real collect stop exceeded supervisor budget: %s", elapsed)
	}
	assertInactiveWatcherTombstone(t, notificationWatcherRuntimePath(project, team.DefaultProfile, "s"))
}

func TestValidateNotificationAMQContextRejectsWrongRoot(t *testing.T) {
	project := t.TempDir()
	execution := notificationWatcherExecution{
		ProjectDir: project,
		Profile:    team.DefaultProfile,
		Session:    "run",
		BaseRoot:   filepath.Join(project, "mail"),
	}
	ctx := notificationAMQContext(execution, team.DefaultProfile, "user")
	ctx.Root = filepath.Join(project, "other", "run")
	if err := validateNotificationAMQContext(ctx, execution, team.DefaultProfile, "user"); err == nil || !strings.Contains(err.Error(), "exact project/profile/session/operator") {
		t.Fatalf("wrong-root validation error = %v", err)
	}
}

func TestRunNotificationAMQWatchWaitsForMissingRootWithoutCreatingIt(t *testing.T) {
	project := t.TempDir()
	root := filepath.Join(project, "mail", "run")
	signaled, err := runNotificationAMQWatch(context.Background(), amqContext{
		ProjectDir: project,
		Root:       root,
		Me:         "user",
		PinMode:    amqPinExactRoot,
	}, time.Millisecond)
	if err != nil || signaled {
		t.Fatalf("missing-root watch signaled=%v err=%v", signaled, err)
	}
	if _, statErr := os.Stat(root); !os.IsNotExist(statErr) {
		t.Fatalf("managed watch created or altered missing root: %v", statErr)
	}
}

func TestParseNotificationAMQWatchOutputRequiresValidSignaledEvent(t *testing.T) {
	for _, valid := range []string{
		`{"event":"existing","messages":[{"id":"m1"}]}`,
		`{"event":"new_message","messages":[{"id":"m2"}]}`,
	} {
		signaled, err := parseNotificationAMQWatchOutput([]byte(valid))
		if err != nil || !signaled {
			t.Fatalf("valid output %s signaled=%t err=%v", valid, signaled, err)
		}
	}
	for _, invalid := range []string{
		``,
		`{"event":`,
		`{"event":"existing"}`,
		`{"event":"new_message","messages":[]}`,
		`{"event":"existing","messages":[null]}`,
		`{"event":"existing","messages":[{"id":""}]}`,
		`{"event":"timeout"}`,
		`{"event":"unknown","messages":[{"id":"m3"}]}`,
		`{"messages":[{"id":"m4"}]}`,
	} {
		if signaled, err := parseNotificationAMQWatchOutput([]byte(invalid)); err == nil || signaled {
			t.Fatalf("invalid output %q signaled=%t err=%v", invalid, signaled, err)
		}
	}
}

func TestExecuteCollectDrainContextStopsBeforeJournalMutation(t *testing.T) {
	project := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	amqCtx := amqContext{
		ProjectDir: project,
		Profile:    team.DefaultProfile,
		Env:        amqEnv{SessionName: "run"},
		Root:       filepath.Join(project, "mail", "run"),
		Me:         "user",
		Session:    "run",
		PinMode:    amqPinExactRoot,
	}
	if _, err := executeCollectDrainContext(ctx, io.Discard, amqCtx, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled collect error = %v", err)
	}
	if _, err := os.Stat(newCollectJournal(amqCtx).Root); !os.IsNotExist(err) {
		t.Fatalf("canceled collect mutated journal: %v", err)
	}
}

func TestNotificationWatcherViewIncludesManagedAMQBackendState(t *testing.T) {
	now := time.Now().UTC()
	rec := notificationWatcherRecord{
		SchemaVersion:   notificationWatcherSchema,
		Expected:        true,
		LeaseTTL:        time.Minute.String(),
		WatchBackend:    "amq-watch",
		WatchRoot:       "/tmp/mail/run",
		WatchMailbox:    "user",
		WatchTimeout:    "30s",
		WatchRunning:    true,
		WatchRestarts:   2,
		WatchFailures:   1,
		CollectPending:  true,
		CollectRetries:  3,
		WatchMaxRetries: 5,
		LastWatchAt:     now.Add(-2 * time.Second),
		LastCollectAt:   now.Add(-time.Second),
	}
	status := notificationWatcherStatus{
		Enabled:         true,
		Health:          "degraded",
		PID:             42,
		LeaseExpiresAt:  now.Add(time.Minute),
		WatchBackend:    rec.WatchBackend,
		WatchRoot:       rec.WatchRoot,
		WatchMailbox:    rec.WatchMailbox,
		WatchRunning:    rec.WatchRunning,
		WatchRestarts:   rec.WatchRestarts,
		WatchFailures:   rec.WatchFailures,
		CollectPending:  rec.CollectPending,
		CollectRetries:  rec.CollectRetries,
		WatchMaxRetries: rec.WatchMaxRetries,
		LastWatchAt:     rec.LastWatchAt,
		LastCollectAt:   rec.LastCollectAt,
		record:          rec,
	}
	view := buildNotificationWatcherView(true, status, now)
	if !view.Running || view.WatchBackend != "amq-watch" || view.WatchMailbox != "user" ||
		!view.WatchRunning || view.WatchRestarts != 2 || view.WatchFailures != 1 || !view.CollectPending ||
		view.CollectRetries != 3 || view.WatchMaxRetries != 5 ||
		!view.LastWatchAt.Equal(rec.LastWatchAt) || !view.LastCollectAt.Equal(rec.LastCollectAt) {
		t.Fatalf("managed watcher view = %+v", view)
	}
}

func TestNotificationWatcherDuplicateAMQSignalsDoNotNotifyWithoutCollection(t *testing.T) {
	project, _, base := notificationWatcherTeam(t, team.DefaultProfile, "s")
	stop := make(chan os.Signal, 1)
	done := make(chan error, 1)
	var watchCalls atomic.Int32
	var collectCalls atomic.Int32
	var scans atomic.Int32
	go func() {
		done <- executeNotificationWatcher(notificationWatcherExecution{
			ProjectDir: project, Profile: team.DefaultProfile, Session: "s", BaseRoot: base, Token: "amq-duplicate",
			TTL: time.Second, Heartbeat: 20 * time.Millisecond, Rescan: time.Hour,
			Stop: stop,
			WatchAMQ: func(ctx context.Context, _ amqContext, _ time.Duration) (bool, error) {
				if watchCalls.Add(1) <= 2 {
					return true, nil
				}
				<-ctx.Done()
				return false, ctx.Err()
			},
			CollectAMQ: func(context.Context, amqContext) (bool, error) {
				collectCalls.Add(1)
				return false, nil
			},
			AMQWatchTimeout: time.Second, AMQWatchMaxRetries: 3,
			Deliver: func(context.Context, time.Time) (notifyDeliverySummary, error) {
				scans.Add(1)
				return notifyDeliverySummary{}, nil
			},
		})
	}()
	path := notificationWatcherRuntimePath(project, team.DefaultProfile, "s")
	rec := waitWatcherRecord(t, path, time.Second, func(r notificationWatcherRecord) bool {
		return r.Health == "healthy" && collectCalls.Load() == 3 && !r.LastCollectAt.IsZero()
	})
	if scans.Load() != 1 {
		t.Fatalf("duplicate empty AMQ signals triggered %d notification scans; record=%+v", scans.Load(), rec)
	}
	stopTestNotificationWatcher(t, stop, done)
}

func TestNotificationWatcherAMQExhaustionIsVisibleAndKeepsFallbackActive(t *testing.T) {
	project, _, base := notificationWatcherTeam(t, team.DefaultProfile, "s")
	stop := make(chan os.Signal, 1)
	done := make(chan error, 1)
	go func() {
		done <- executeNotificationWatcher(notificationWatcherExecution{
			ProjectDir: project, Profile: team.DefaultProfile, Session: "s", BaseRoot: base, Token: "amq-exhausted",
			TTL: time.Second, Heartbeat: 20 * time.Millisecond, Rescan: 20 * time.Millisecond,
			Stop: stop,
			WatchAMQ: func(context.Context, amqContext, time.Duration) (bool, error) {
				return false, errors.New("amq watch unsupported")
			},
			CollectAMQ: func(context.Context, amqContext) (bool, error) {
				return false, nil
			},
			AMQWatchTimeout: time.Second, AMQWatchMaxRetries: 2,
			AMQWatchBackoff: time.Millisecond, AMQWatchMaxBackoff: time.Millisecond,
			Deliver: func(context.Context, time.Time) (notifyDeliverySummary, error) {
				return notifyDeliverySummary{}, nil
			},
		})
	}()
	path := notificationWatcherRuntimePath(project, team.DefaultProfile, "s")
	rec := waitWatcherRecord(t, path, time.Second, func(r notificationWatcherRecord) bool {
		return r.Health == "degraded" && !r.WatchRunning &&
			r.WatchRestarts == 1 && r.WatchFailures == 2 &&
			strings.Contains(r.LastError, "managed AMQ watch exhausted") &&
			!r.LastScanAt.IsZero()
	})
	if rec.WatchBackend != "amq-watch" || rec.WatchMailbox != "user" ||
		rec.WatchMaxRetries != 2 || !strings.Contains(rec.LastError, "fsnotify/rescan fallback remains active") {
		t.Fatalf("exhausted watcher record = %+v", rec)
	}
	view := buildNotificationWatcherView(true, notificationWatcherStatus{
		Enabled: true, Health: rec.Health, PID: rec.PID,
		LeaseExpiresAt: rec.LeaseExpiresAt, record: rec,
	}, time.Now())
	if view.Running || view.WatchRunning {
		t.Fatalf("exhausted managed backend rendered running: %+v", view)
	}
	firstScan := rec.LastScanAt
	waitWatcherRecord(t, path, time.Second, func(r notificationWatcherRecord) bool {
		return r.LastScanAt.After(firstScan)
	})
	stopTestNotificationWatcher(t, stop, done)
}
