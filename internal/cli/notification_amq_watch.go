package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/amqexec"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
)

type notificationAMQWatchFunc func(context.Context, amqContext, time.Duration) (bool, error)
type notificationAMQCollectFunc func(context.Context, amqContext) (bool, error)

var errNotificationAMQOwnershipLost = errors.New("notification watcher managed AMQ ownership lost")

type notificationAMQWatchResult struct {
	At               time.Time
	Signaled         bool
	CollectAttempted bool
	Collected        bool
	PendingCollect   bool
	FailureStreak    int
	WatchRestarts    int
	CollectRetries   int
	RetryStarted     bool
	Exhausted        bool
	Fatal            bool
	Err              error
}

type notificationAMQWatchOutput struct {
	Event    string                        `json:"event"`
	Messages []notificationAMQWatchMessage `json:"messages,omitempty"`
}

type notificationAMQWatchMessage struct {
	ID string `json:"id"`
}

func notificationAMQWatchSettings(w notificationWatcherExecution) (time.Duration, int, time.Duration, time.Duration) {
	timeout := w.AMQWatchTimeout
	if timeout <= 0 {
		timeout = defaultNotificationAMQWatchTimeout
	}
	maxRetries := w.AMQWatchMaxRetries
	if maxRetries <= 0 {
		maxRetries = defaultNotificationAMQWatchRetries
	}
	backoff := w.AMQWatchBackoff
	if backoff <= 0 {
		backoff = defaultNotificationAMQWatchBackoff
	}
	maxBackoff := w.AMQWatchMaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = maxNotificationAMQWatchBackoff
	}
	if maxBackoff < backoff {
		maxBackoff = backoff
	}
	return timeout, maxRetries, backoff, maxBackoff
}

func notificationAMQContext(w notificationWatcherExecution, profile, mailbox string) amqContext {
	root := filepath.Clean(filepath.Join(w.BaseRoot, w.Session))
	return amqContext{
		ProjectDir: w.ProjectDir,
		Profile:    squadnamespace.NormalizeProfile(profile),
		Env: amqEnv{
			Root:        root,
			BaseRoot:    filepath.Clean(w.BaseRoot),
			SessionName: w.Session,
			Me:          mailbox,
			Project:     w.ProjectDir,
		},
		Root:    root,
		Me:      strings.TrimSpace(mailbox),
		Session: w.Session,
		PinMode: amqPinExactRoot,
	}
}

func validateNotificationAMQContext(ctx amqContext, w notificationWatcherExecution, profile, mailbox string) error {
	wantRoot := filepath.Clean(filepath.Join(w.BaseRoot, w.Session))
	if !filepath.IsAbs(wantRoot) ||
		ctx.PinMode != amqPinExactRoot ||
		filepath.Clean(ctx.ProjectDir) != filepath.Clean(w.ProjectDir) ||
		ctx.Profile != squadnamespace.NormalizeProfile(profile) ||
		ctx.Session != w.Session ||
		filepath.Clean(ctx.Root) != wantRoot ||
		filepath.Clean(ctx.Env.Root) != wantRoot ||
		strings.TrimSpace(ctx.Me) == "" ||
		ctx.Me != strings.TrimSpace(mailbox) {
		return fmt.Errorf("notification watcher managed AMQ context does not match exact project/profile/session/operator mailbox binding")
	}
	return nil
}

func runNotificationAMQWatch(ctx context.Context, amqCtx amqContext, timeout time.Duration) (bool, error) {
	if _, err := os.Stat(amqCtx.Root); err != nil {
		if !os.IsNotExist(err) {
			return false, fmt.Errorf("managed AMQ watch root: %w", err)
		}
		delay := timeout
		if delay > 500*time.Millisecond {
			delay = 500 * time.Millisecond
		}
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
			return false, nil
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return false, ctx.Err()
		}
	}
	args := []string{"watch", "--root", amqCtx.Root, "--me", amqCtx.Me, "--timeout", timeout.String(), "--json"}
	cmd := exec.CommandContext(ctx, "amq", args...)
	cmd.Dir = amqCtx.ProjectDir
	cmd.Env = amqexec.NoUpdateCheckEnv(amqCommandEnv(amqCtx))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	err := cmd.Run()
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if err != nil {
		if isCollectWatchTimeout(err) {
			return false, nil
		}
		return false, fmt.Errorf("managed AMQ watch: %w", err)
	}
	signaled, err := parseNotificationAMQWatchOutput(stdout.Bytes())
	if err != nil {
		return false, fmt.Errorf("managed AMQ watch output: %w", err)
	}
	return signaled, nil
}

func parseNotificationAMQWatchOutput(data []byte) (bool, error) {
	var result notificationAMQWatchOutput
	if err := json.Unmarshal(data, &result); err != nil {
		return false, fmt.Errorf("parse JSON: %w", err)
	}
	switch result.Event {
	case "existing", "new_message":
		if len(result.Messages) == 0 {
			return false, fmt.Errorf("event %q contained no messages", result.Event)
		}
		for i, message := range result.Messages {
			if strings.TrimSpace(message.ID) == "" {
				return false, fmt.Errorf("event %q message %d is missing id", result.Event, i)
			}
		}
		return true, nil
	case "timeout":
		return false, fmt.Errorf("unexpected successful timeout event")
	case "":
		return false, fmt.Errorf("missing event")
	default:
		return false, fmt.Errorf("unsupported event %q", result.Event)
	}
}

func collectNotificationAMQ(ctx context.Context, amqCtx amqContext) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	collected, err := executeCollectDrainContext(ctx, io.Discard, amqCtx, false)
	if err != nil {
		return collected, fmt.Errorf("managed AMQ collect: %w", err)
	}
	return collected, nil
}

func runManagedNotificationAMQWatch(
	ctx context.Context,
	amqCtx amqContext,
	collectFirst bool,
	timeout time.Duration,
	maxRetries int,
	initialBackoff time.Duration,
	maxBackoff time.Duration,
	watch notificationAMQWatchFunc,
	collect notificationAMQCollectFunc,
	results chan<- notificationAMQWatchResult,
) {
	defer close(results)
	failureStreak := 0
	watchRestarts := 0
	collectRetries := 0
	pendingCollect := collectFirst
	backoff := initialBackoff
	emit := func(result notificationAMQWatchResult) bool {
		select {
		case results <- result:
			return true
		case <-ctx.Done():
			return false
		}
	}
	for {
		result := notificationAMQWatchResult{
			At:             time.Now().UTC(),
			PendingCollect: pendingCollect,
			WatchRestarts:  watchRestarts,
			CollectRetries: collectRetries,
		}
		var err error
		if pendingCollect {
			result.CollectAttempted = true
			result.Collected, err = collect(ctx, amqCtx)
		} else {
			result.Signaled, err = watch(ctx, amqCtx, timeout)
			if err == nil && result.Signaled {
				pendingCollect = true
				result.PendingCollect = true
				result.CollectAttempted = true
				result.Collected, err = collect(ctx, amqCtx)
			}
		}
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			if result.CollectAttempted {
				pendingCollect = false
			}
			recovered := failureStreak > 0
			failureStreak = 0
			backoff = initialBackoff
			result.PendingCollect = pendingCollect
			result.FailureStreak = 0
			result.WatchRestarts = watchRestarts
			result.CollectRetries = collectRetries
			if result.Signaled || result.CollectAttempted || recovered {
				if !emit(result) {
					return
				}
			}
			continue
		}
		failureStreak++
		result.FailureStreak = failureStreak
		result.PendingCollect = pendingCollect
		result.Fatal = errors.Is(err, errNotificationAMQOwnershipLost)
		result.Exhausted = result.Fatal || failureStreak >= maxRetries
		result.WatchRestarts = watchRestarts
		result.CollectRetries = collectRetries
		result.Err = err
		if !emit(result) || result.Exhausted {
			return
		}
		timer := time.NewTimer(backoff)
		select {
		case <-timer.C:
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		}
		if result.CollectAttempted {
			collectRetries++
		} else {
			watchRestarts++
		}
		if !emit(notificationAMQWatchResult{
			At:             time.Now().UTC(),
			PendingCollect: pendingCollect,
			FailureStreak:  failureStreak,
			WatchRestarts:  watchRestarts,
			CollectRetries: collectRetries,
			RetryStarted:   true,
			Err:            err,
		}) {
			return
		}
		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}
