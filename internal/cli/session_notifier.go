package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/omriariav/amq-squad/v2/internal/flock"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
	"github.com/omriariav/amq-squad/v2/internal/procinfo"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

const (
	sessionNotifierSchema        = 1
	sessionNotifierTTL           = 15 * time.Second
	sessionNotifierHeartbeat     = 3 * time.Second
	sessionNotifierStartupBudget = 5 * time.Second
	sessionNotifierAttemptLimit  = 512
	sessionNotifierMessageLimit  = 10 * 1024 * 1024
)

type sessionNotifierRecord struct {
	SchemaVersion  int       `json:"schema_version"`
	ProjectDir     string    `json:"project_dir"`
	Profile        string    `json:"profile"`
	Session        string    `json:"session"`
	Root           string    `json:"root"`
	PID            int       `json:"pid"`
	Host           string    `json:"host"`
	OwnerToken     string    `json:"owner_token,omitempty"`
	Expected       bool      `json:"expected"`
	Health         string    `json:"health"`
	HeartbeatAt    time.Time `json:"heartbeat_at"`
	LeaseExpiresAt time.Time `json:"lease_expires_at"`
	LastNudgeAt    time.Time `json:"last_nudge_at,omitempty"`
	LastError      string    `json:"last_error,omitempty"`
	// AttemptedMessageIDs is namespaced by handle; Root on this same record is
	// the namespace half of the key. An ID is reserved before pane input so a
	// crash or failed send can never turn into a duplicate notifier attempt.
	AttemptedMessageIDs map[string][]string `json:"attempted_message_ids,omitempty"`
}

type sessionNotifierStatus struct {
	RuntimePath string
	Health      string
	Reason      string
	Record      sessionNotifierRecord
}

type sessionNotifierSendKeys func(paneID, keys string) error
type sessionNotifierWakeLive func(agentDir, root, profile, session, handle string, rec launch.Record) bool

type sessionNotifierAttemptLedger struct {
	attempted map[string][]string
	reserve   func(handle, messageID string) (bool, map[string][]string, error)
	prune     func(pending map[string]map[string]struct{}) (map[string][]string, error)
}

type sessionNotifierExecution struct {
	ProjectDir string
	Profile    string
	Session    string
	Root       string
	Token      string
	TTL        time.Duration
	Heartbeat  time.Duration
	Stop       <-chan os.Signal
	Now        func() time.Time
	NewWatcher func() (*fsnotify.Watcher, error)
	SendKeys   sessionNotifierSendKeys
	WakeLive   sessionNotifierWakeLive
}

var (
	sessionNotifierNow          = time.Now
	sessionNotifierSleep        = time.Sleep
	sessionNotifierPIDAlive     = procinfo.Alive
	sessionNotifierProcessMatch = procinfo.Match
	sessionNotifierSpawn        = spawnSessionNotifierProcess
)

func sessionNotifierRuntimePath(project, profile, session string) string {
	base := filepath.Join(project, team.DirName, "session-notifiers")
	if normalized := squadnamespace.NormalizeProfile(profile); normalized != team.DefaultProfile {
		base = filepath.Join(base, normalized)
	}
	return filepath.Join(base, sanitizeWorkstreamName(session)+".json")
}

func sessionNotifierLockPath(project, profile, session string) string {
	return sessionNotifierRuntimePath(project, profile, session) + ".lock"
}

func sessionNotifierLogPath(project, profile, session string) string {
	base := filepath.Join(project, team.DirName, "session-notifiers", "logs")
	if normalized := squadnamespace.NormalizeProfile(profile); normalized != team.DefaultProfile {
		base = filepath.Join(base, normalized)
	}
	return filepath.Join(base, sanitizeWorkstreamName(session)+".log")
}

func readSessionNotifierRecord(path string) (sessionNotifierRecord, error) {
	var rec sessionNotifierRecord
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return rec, nil
	}
	if err != nil {
		return rec, err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return rec, nil
	}
	if err := json.Unmarshal(b, &rec); err != nil {
		return rec, fmt.Errorf("parse session notifier runtime: %w", err)
	}
	if rec.SchemaVersion != sessionNotifierSchema {
		return rec, fmt.Errorf("unsupported session notifier schema %d", rec.SchemaVersion)
	}
	return rec, nil
}

func writeSessionNotifierRecord(path string, rec sessionNotifierRecord) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp-" + randomToken()
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func cloneSessionNotifierAttempts(in map[string][]string) map[string][]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]string, len(in))
	for handle, ids := range in {
		out[handle] = append([]string(nil), ids...)
	}
	return out
}

func sessionNotifierAttempted(attempted map[string][]string, handle, messageID string) bool {
	for _, existing := range attempted[strings.TrimSpace(handle)] {
		if existing == messageID {
			return true
		}
	}
	return false
}

func appendSessionNotifierAttempt(attempted map[string][]string, handle, messageID string) (map[string][]string, error) {
	out := cloneSessionNotifierAttempts(attempted)
	if out == nil {
		out = make(map[string][]string)
	}
	handle = strings.TrimSpace(handle)
	ids := out[handle]
	if len(ids) >= sessionNotifierAttemptLimit {
		// Never evict an attempted ID while it may still be pending: doing so
		// would make the next rescan inject that same message again. Pruning is
		// driven by inbox/new departures; until then, a full ledger fails closed
		// and leaves the durable message as the recovery source.
		return nil, fmt.Errorf("session notifier attempt ledger for %s reached limit %d", handle, sessionNotifierAttemptLimit)
	}
	ids = append(ids, messageID)
	out[handle] = ids
	return out, nil
}

func pruneSessionNotifierAttempts(attempted map[string][]string, pending map[string]map[string]struct{}) map[string][]string {
	if len(attempted) == 0 {
		return nil
	}
	out := make(map[string][]string)
	for handle, ids := range attempted {
		keep := pending[handle]
		if len(keep) == 0 {
			continue
		}
		for _, id := range ids {
			if _, ok := keep[id]; ok {
				out[handle] = append(out[handle], id)
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func newSessionNotifierAttemptLedger(seed map[string][]string) *sessionNotifierAttemptLedger {
	return &sessionNotifierAttemptLedger{attempted: cloneSessionNotifierAttempts(seed)}
}

func newPersistentSessionNotifierAttemptLedger(path, token, root string, rec *sessionNotifierRecord) *sessionNotifierAttemptLedger {
	ledger := newSessionNotifierAttemptLedger(rec.AttemptedMessageIDs)
	ledger.reserve = func(handle, messageID string) (bool, map[string][]string, error) {
		var (
			reserved bool
			attempts map[string][]string
		)
		err := flock.WithLock(path+".lock", func() error {
			current, err := readSessionNotifierRecord(path)
			if err != nil {
				return err
			}
			if current.OwnerToken != token || !sameResolvedDir(current.Root, root) {
				return fmt.Errorf("session notifier ownership changed while reserving message %s", messageID)
			}
			attempts = cloneSessionNotifierAttempts(current.AttemptedMessageIDs)
			if sessionNotifierAttempted(attempts, handle, messageID) {
				return nil
			}
			attempts, err = appendSessionNotifierAttempt(attempts, handle, messageID)
			if err != nil {
				return err
			}
			current.AttemptedMessageIDs = cloneSessionNotifierAttempts(attempts)
			if err := writeSessionNotifierRecord(path, current); err != nil {
				return err
			}
			reserved = true
			return nil
		})
		return reserved, attempts, err
	}
	ledger.prune = func(pending map[string]map[string]struct{}) (map[string][]string, error) {
		var attempts map[string][]string
		err := flock.WithLock(path+".lock", func() error {
			current, err := readSessionNotifierRecord(path)
			if err != nil {
				return err
			}
			if current.OwnerToken != token || !sameResolvedDir(current.Root, root) {
				return fmt.Errorf("session notifier ownership changed while pruning message attempts")
			}
			attempts = pruneSessionNotifierAttempts(current.AttemptedMessageIDs, pending)
			current.AttemptedMessageIDs = cloneSessionNotifierAttempts(attempts)
			return writeSessionNotifierRecord(path, current)
		})
		return attempts, err
	}
	return ledger
}

func (l *sessionNotifierAttemptLedger) Reserve(handle, messageID string) (bool, error) {
	if l == nil {
		return false, fmt.Errorf("session notifier message-attempt ledger is required")
	}
	if sessionNotifierAttempted(l.attempted, handle, messageID) {
		return false, nil
	}
	if l.reserve != nil {
		reserved, attempts, err := l.reserve(handle, messageID)
		if err != nil {
			return false, err
		}
		l.attempted = cloneSessionNotifierAttempts(attempts)
		return reserved, nil
	}
	attempted, err := appendSessionNotifierAttempt(l.attempted, handle, messageID)
	if err != nil {
		return false, err
	}
	l.attempted = attempted
	return true, nil
}

func (l *sessionNotifierAttemptLedger) Prune(pending map[string]map[string]struct{}) error {
	if l == nil {
		return fmt.Errorf("session notifier message-attempt ledger is required")
	}
	if l.prune != nil {
		attempts, err := l.prune(pending)
		if err != nil {
			return err
		}
		l.attempted = cloneSessionNotifierAttempts(attempts)
		return nil
	}
	l.attempted = pruneSessionNotifierAttempts(l.attempted, pending)
	return nil
}

func sessionNotifierProcessMatches(rec sessionNotifierRecord) bool {
	if rec.PID <= 0 || strings.TrimSpace(rec.OwnerToken) == "" || !sessionNotifierPIDAlive(rec.PID) {
		return false
	}
	host, _ := os.Hostname()
	if strings.TrimSpace(rec.Host) != "" && strings.TrimSpace(rec.Host) != strings.TrimSpace(host) {
		return false
	}
	return sessionNotifierProcessMatch(rec.PID, func(args string) bool {
		return strings.Contains(args, "_session-notify") &&
			strings.Contains(args, rec.OwnerToken) &&
			strings.Contains(args, rec.ProjectDir) &&
			strings.Contains(args, "--session "+rec.Session)
	})
}

func inspectSessionNotifier(project, profile, session, root string, now time.Time) sessionNotifierStatus {
	path := sessionNotifierRuntimePath(project, profile, session)
	status := sessionNotifierStatus{RuntimePath: path, Health: "inactive"}
	rec, err := readSessionNotifierRecord(path)
	if err != nil {
		status.Health, status.Reason = "unhealthy", err.Error()
		return status
	}
	status.Record = rec
	if !rec.Expected && strings.TrimSpace(rec.OwnerToken) == "" {
		return status
	}
	if rec.ProjectDir != project || rec.Profile != squadnamespace.NormalizeProfile(profile) || rec.Session != session || !sameResolvedDir(rec.Root, root) {
		status.Health, status.Reason = "unhealthy", "session notifier scope does not match project/profile/session/root"
		return status
	}
	if !now.Before(rec.LeaseExpiresAt) {
		status.Health, status.Reason = "unhealthy", "session notifier lease is stale"
		return status
	}
	if !sessionNotifierProcessMatches(rec) {
		status.Health, status.Reason = "unhealthy", "session notifier process is absent or does not match its owner token"
		return status
	}
	status.Health = rec.Health
	if status.Health != "healthy" {
		status.Reason = rec.LastError
	}
	return status
}

func reconcileSessionNotifierStarted(t team.Team, profile, session, _ string) error {
	root := squadnamespace.AMQRoot(t.Project, profile, session)
	now := sessionNotifierNow()
	if status := inspectSessionNotifier(t.Project, profile, session, root, now); status.Health == "healthy" {
		return nil
	}
	path := sessionNotifierRuntimePath(t.Project, profile, session)
	observed, err := readSessionNotifierRecord(path)
	if err != nil {
		return err
	}
	if observed.Expected && strings.TrimSpace(observed.OwnerToken) != "" && now.Before(observed.LeaseExpiresAt) && sessionNotifierProcessMatches(observed) {
		return fmt.Errorf("session notifier is active but unhealthy: %s", observed.LastError)
	}
	token := randomToken()
	proc, err := sessionNotifierSpawn(t.Project, profile, session, root, token)
	if err != nil {
		return fmt.Errorf("start session notifier: %w", err)
	}
	if proc != nil {
		_ = proc.Release()
	}
	deadline := sessionNotifierNow().Add(sessionNotifierStartupBudget)
	for {
		status := inspectSessionNotifier(t.Project, profile, session, root, sessionNotifierNow())
		if status.Health == "healthy" {
			return nil
		}
		if !sessionNotifierNow().Before(deadline) {
			if proc != nil {
				_ = proc.Signal(syscall.SIGTERM)
			}
			return fmt.Errorf("session notifier did not establish a healthy lease: %s", status.Reason)
		}
		sessionNotifierSleep(25 * time.Millisecond)
	}
}

func spawnSessionNotifierProcess(project, profile, session, root, token string) (notificationWatcherProcess, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	logPath := sessionNotifierLogPath(project, profile, session)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	defer logFile.Close()
	cmd := exec.Command(exe, "_session-notify", "--project", project, "--profile", profile, "--session", session, "--root", root, "--owner-token", token)
	cmd.Dir, cmd.Stdout, cmd.Stderr = project, logFile, logFile
	cmd.Env = envWithoutAMQIdentity(os.Environ())
	ownExternalLeadWakeProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return execNotificationWatcherProcess{p: cmd.Process}, nil
}

func stopSessionNotifier(project, profile, session string) (bool, error) {
	path := sessionNotifierRuntimePath(project, profile, session)
	rec, err := readSessionNotifierRecord(path)
	if err != nil {
		return false, err
	}
	wasExpected := rec.Expected || strings.TrimSpace(rec.OwnerToken) != ""
	if !wasExpected {
		return false, nil
	}
	if strings.TrimSpace(rec.OwnerToken) != "" && sessionNotifierProcessMatches(rec) {
		if err := notificationWatcherSignal(rec.PID, syscall.SIGTERM); err != nil {
			return wasExpected, fmt.Errorf("stop session notifier pid %d: %w", rec.PID, err)
		}
		deadline := sessionNotifierNow().Add(2 * time.Second)
		for sessionNotifierPIDAlive(rec.PID) && sessionNotifierNow().Before(deadline) {
			sessionNotifierSleep(20 * time.Millisecond)
		}
		if sessionNotifierPIDAlive(rec.PID) {
			return wasExpected, fmt.Errorf("session notifier pid %d did not exit", rec.PID)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return wasExpected, fmt.Errorf("create session notifier runtime directory while stopping: %w", err)
	}
	err = flock.WithLock(sessionNotifierLockPath(project, profile, session), func() error {
		current, err := readSessionNotifierRecord(path)
		if err != nil {
			return err
		}
		// The notifier may remove its runtime directory while exiting. We already
		// verified the exact owner above, so preserve that record as the baseline
		// when the post-signal path is absent instead of writing a schema-zero file.
		if current.SchemaVersion == 0 {
			if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
				current = rec
			} else if statErr != nil {
				return statErr
			} else {
				return fmt.Errorf("session notifier runtime record became empty while stopping")
			}
		}
		if current.OwnerToken != rec.OwnerToken && strings.TrimSpace(current.OwnerToken) != "" {
			return fmt.Errorf("session notifier ownership changed while stopping")
		}
		now := sessionNotifierNow().UTC()
		current.PID, current.OwnerToken, current.Expected = 0, "", false
		current.Health, current.LastError = "inactive", ""
		current.HeartbeatAt, current.LeaseExpiresAt = now, now
		return writeSessionNotifierRecord(path, current)
	})
	return wasExpected, err
}

func runSessionNotifier(args []string) error {
	fs := flag.NewFlagSet("_session-notify", flag.ContinueOnError)
	project := fs.String("project", "", "project directory")
	profile := fs.String("profile", team.DefaultProfile, "team profile")
	session := fs.String("session", "", "session")
	root := fs.String("root", "", "canonical AMQ root")
	token := fs.String("owner-token", "", "owner token")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if strings.TrimSpace(*project) == "" || strings.TrimSpace(*session) == "" || strings.TrimSpace(*root) == "" || strings.TrimSpace(*token) == "" {
		return usageErrorf("_session-notify requires --project, --session, --root, and --owner-token")
	}
	stop := make(chan os.Signal, 2)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)
	return executeSessionNotifier(sessionNotifierExecution{
		ProjectDir: canonicalFilesystemPath(*project), Profile: squadnamespace.NormalizeProfile(*profile),
		Session: strings.TrimSpace(*session), Root: canonicalFilesystemPath(*root), Token: strings.TrimSpace(*token),
		TTL: sessionNotifierTTL, Heartbeat: sessionNotifierHeartbeat, Stop: stop, Now: time.Now,
		NewWatcher: fsnotify.NewWatcher, SendKeys: tmuxpane.SendPromptToPane,
	})
}

func executeSessionNotifier(n sessionNotifierExecution) (returnErr error) {
	if n.Now == nil {
		n.Now = time.Now
	}
	if n.NewWatcher == nil {
		n.NewWatcher = fsnotify.NewWatcher
	}
	if n.SendKeys == nil {
		n.SendKeys = tmuxpane.SendPromptToPane
	}
	if n.WakeLive == nil {
		n.WakeLive = verifiedSessionNotifierWakeLive
	}
	if n.TTL <= 0 {
		n.TTL = sessionNotifierTTL
	}
	if n.Heartbeat <= 0 {
		n.Heartbeat = sessionNotifierHeartbeat
	}
	expectedRoot := squadnamespace.AMQRoot(n.ProjectDir, n.Profile, n.Session)
	if !sameResolvedDir(n.Root, expectedRoot) {
		return fmt.Errorf("session notifier refused non-canonical root %s (want %s)", n.Root, expectedRoot)
	}
	path := sessionNotifierRuntimePath(n.ProjectDir, n.Profile, n.Session)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create session notifier runtime directory: %w", err)
	}
	host, _ := os.Hostname()
	now := n.Now().UTC()
	rec := sessionNotifierRecord{
		SchemaVersion: sessionNotifierSchema, ProjectDir: n.ProjectDir, Profile: squadnamespace.NormalizeProfile(n.Profile),
		Session: n.Session, Root: n.Root, PID: os.Getpid(), Host: host, OwnerToken: n.Token,
		Expected: true, Health: "healthy", HeartbeatAt: now, LeaseExpiresAt: now.Add(n.TTL),
	}
	if err := flock.WithLock(sessionNotifierLockPath(n.ProjectDir, n.Profile, n.Session), func() error {
		current, err := readSessionNotifierRecord(path)
		if err != nil {
			return err
		}
		if current.OwnerToken != "" && current.OwnerToken != n.Token && n.Now().Before(current.LeaseExpiresAt) {
			return fmt.Errorf("session notifier lease is already held by pid %d", current.PID)
		}
		if current.SchemaVersion == sessionNotifierSchema &&
			current.ProjectDir == n.ProjectDir && current.Profile == squadnamespace.NormalizeProfile(n.Profile) &&
			current.Session == n.Session && sameResolvedDir(current.Root, n.Root) {
			rec.AttemptedMessageIDs = cloneSessionNotifierAttempts(current.AttemptedMessageIDs)
		}
		return writeSessionNotifierRecord(path, rec)
	}); err != nil {
		return err
	}
	defer func() {
		err := releaseSessionNotifier(path, n, rec)
		if err != nil {
			returnErr = errors.Join(returnErr, err)
		}
	}()
	ledger := newPersistentSessionNotifierAttemptLedger(path, n.Token, n.Root, &rec)

	watcher, err := n.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()
	// Snapshot before installing watches, then once more afterwards. A file
	// already in inbox/new is still pending work and may have arrived while the
	// notifier was down, so startup observes it. The durable reservation ledger
	// makes each root+handle+message ID an at-most-once pane-input attempt across
	// notifier restarts. The second snapshot closes the watcher-install race,
	// and queued events dedupe through the same ledger.
	pending, err := snapshotSessionInboxMessages(n.Root)
	if err != nil {
		return err
	}
	if err := ledger.Prune(sessionNotifierPendingIDs(n.Root, pending)); err != nil {
		return fmt.Errorf("prune stale notifier message attempts during startup: %w", err)
	}
	rec.AttemptedMessageIDs = cloneSessionNotifierAttempts(ledger.attempted)
	if err := addNotificationWatchTree(watcher, n.Root); err != nil {
		return fmt.Errorf("watch canonical session root: %w", err)
	}
	var startupErrs []error
	for messagePath := range pending {
		nudged, err := notifySessionInboxArrival(n.Root, n.Profile, n.Session, messagePath, ledger, n.WakeLive, n.SendKeys)
		if err != nil {
			startupErrs = append(startupErrs, fmt.Errorf("observe pending inbox arrival during notifier startup: %w", err))
			continue
		}
		if nudged {
			rec.LastNudgeAt, rec.LastError = n.Now().UTC(), ""
		}
	}
	catchUp, err := snapshotSessionInboxMessages(n.Root)
	if err != nil {
		return err
	}
	for messagePath := range catchUp {
		nudged, err := notifySessionInboxArrival(n.Root, n.Profile, n.Session, messagePath, ledger, n.WakeLive, n.SendKeys)
		if err != nil {
			startupErrs = append(startupErrs, fmt.Errorf("observe inbox arrival during notifier startup: %w", err))
			continue
		}
		if nudged {
			rec.LastNudgeAt, rec.LastError = n.Now().UTC(), ""
		}
	}
	if startupErr := errors.Join(startupErrs...); startupErr != nil {
		rec.LastError = startupErr.Error()
	}
	heartbeat := time.NewTicker(n.Heartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-n.Stop:
			return nil
		case event, ok := <-watcher.Events:
			if !ok {
				return fmt.Errorf("session notifier event stream closed")
			}
			if event.Op&fsnotify.Create != 0 {
				if info, statErr := os.Stat(event.Name); statErr == nil && info.IsDir() {
					_ = addNotificationWatchTree(watcher, event.Name)
				}
			}
			if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Rename) == 0 {
				continue
			}
			nudged, nudgeErr := notifySessionInboxArrival(n.Root, n.Profile, n.Session, event.Name, ledger, n.WakeLive, n.SendKeys)
			if nudgeErr != nil {
				rec.LastError = nudgeErr.Error()
			} else if nudged {
				rec.LastNudgeAt, rec.LastError = n.Now().UTC(), ""
			}
		case watchErr, ok := <-watcher.Errors:
			if !ok {
				return fmt.Errorf("session notifier error stream closed")
			}
			rec.LastError = watchErr.Error()
		case <-heartbeat.C:
			heartbeatErr := addNotificationWatchTree(watcher, n.Root)
			nudged, rescanErr := rescanSessionInboxMessages(n.Root, n.Profile, n.Session, ledger, n.WakeLive, n.SendKeys)
			pending, snapshotErr := snapshotSessionInboxMessages(n.Root)
			var pruneErr error
			if snapshotErr == nil {
				pruneErr = ledger.Prune(sessionNotifierPendingIDs(n.Root, pending))
			}
			heartbeatErr = errors.Join(heartbeatErr, rescanErr, snapshotErr, pruneErr)
			if heartbeatErr != nil {
				rec.LastError = heartbeatErr.Error()
			} else if nudged {
				rec.LastNudgeAt, rec.LastError = n.Now().UTC(), ""
			}
		}
		now = n.Now().UTC()
		rec.AttemptedMessageIDs = cloneSessionNotifierAttempts(ledger.attempted)
		rec.HeartbeatAt, rec.LeaseExpiresAt = now, now.Add(n.TTL)
		if err := refreshSessionNotifier(path, n.Token, rec); err != nil {
			return err
		}
	}
}

func rescanSessionInboxMessages(root, profile, session string, ledger *sessionNotifierAttemptLedger, wakeLive sessionNotifierWakeLive, sendKeys sessionNotifierSendKeys) (bool, error) {
	pending, err := snapshotSessionInboxMessages(root)
	if err != nil {
		return false, err
	}
	var (
		nudgedAny bool
		nudgeErrs []error
	)
	for messagePath := range pending {
		nudged, err := notifySessionInboxArrival(root, profile, session, messagePath, ledger, wakeLive, sendKeys)
		if err != nil {
			nudgeErrs = append(nudgeErrs, err)
			continue
		}
		nudgedAny = nudgedAny || nudged
	}
	return nudgedAny, errors.Join(nudgeErrs...)
}

func snapshotSessionInboxMessages(root string) (map[string]struct{}, error) {
	seen := map[string]struct{}{}
	pattern := filepath.Join(root, "agents", "*", "inbox", "new", "*")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		if info, statErr := os.Stat(path); statErr == nil && !info.IsDir() {
			seen[canonicalFilesystemPath(path)] = struct{}{}
		}
	}
	return seen, nil
}

func sessionNotifierMessageTarget(root, messagePath string) (string, bool) {
	path := canonicalFilesystemPath(messagePath)
	rel, err := filepath.Rel(canonicalFilesystemPath(root), path)
	if err != nil {
		return "", false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) != 5 || parts[0] != "agents" || parts[2] != "inbox" || parts[3] != "new" || parts[4] == "" {
		return "", false
	}
	return parts[1], true
}

func readSessionNotifierMessageID(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular message file")
	}
	if info.Size() > sessionNotifierMessageLimit {
		return "", fmt.Errorf("message exceeds %d-byte notifier limit", sessionNotifierMessageLimit)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	const start = "---json\n"
	if !bytes.HasPrefix(data, []byte(start)) {
		return "", fmt.Errorf("missing ---json frontmatter fence")
	}
	payload := data[len(start):]
	dec := json.NewDecoder(bytes.NewReader(payload))
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return "", fmt.Errorf("parse message frontmatter: %w", err)
	}
	if err := operatorauth.ValidateUnambiguousJSON(raw); err != nil {
		return "", fmt.Errorf("ambiguous message frontmatter: %w", err)
	}
	rest := bytes.TrimLeft(payload[dec.InputOffset():], " \t\r\n")
	if !bytes.HasPrefix(rest, []byte("---\n")) {
		return "", fmt.Errorf("unterminated message frontmatter")
	}
	var header struct {
		Schema int    `json:"schema"`
		ID     string `json:"id"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return "", fmt.Errorf("decode message frontmatter: %w", err)
	}
	id := strings.TrimSpace(header.ID)
	if header.Schema != 1 || id == "" || id != header.ID || strings.ContainsAny(id, "/\\\r\n\x00") || filepath.Base(id) != id {
		return "", fmt.Errorf("message header has noncanonical schema/id")
	}
	if filepath.Base(path) != id+".md" {
		return "", fmt.Errorf("message id %q does not match filename", id)
	}
	return id, nil
}

func sessionNotifierPendingIDs(root string, paths map[string]struct{}) map[string]map[string]struct{} {
	pending := make(map[string]map[string]struct{})
	for path := range paths {
		handle, ok := sessionNotifierMessageTarget(root, path)
		if !ok {
			continue
		}
		name := filepath.Base(path)
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		id := strings.TrimSuffix(name, ".md")
		if id == "" || strings.ContainsAny(id, "/\\\r\n\x00") || filepath.Base(id) != id {
			continue
		}
		if pending[handle] == nil {
			pending[handle] = make(map[string]struct{})
		}
		pending[handle][id] = struct{}{}
	}
	return pending
}

func verifiedSessionNotifierWakeLive(agentDir, root, profile, session, handle string, rec launch.Record) bool {
	role := strings.TrimSpace(rec.Role)
	if role == "" {
		role = handle
	}
	live := classifyAgentLiveness(agentDir, root, profile, handle, role, rec.Binary, session, rec.CWD, defaultDuplicateLaunchProbe)
	return live.Signals.WakeAlive
}

func notifySessionInboxArrival(root, profile, session, messagePath string, ledger *sessionNotifierAttemptLedger, wakeLive sessionNotifierWakeLive, sendKeys sessionNotifierSendKeys) (bool, error) {
	path := canonicalFilesystemPath(messagePath)
	handle, ok := sessionNotifierMessageTarget(root, path)
	if !ok {
		return false, nil
	}
	messageID, err := readSessionNotifierMessageID(path)
	if err != nil {
		return false, fmt.Errorf("resolve notifier message id at %s: %w", path, err)
	}
	agentDir := filepath.Join(root, "agents", handle)
	rec, err := launch.Read(agentDir)
	if err != nil {
		return false, fmt.Errorf("resolve notifier target %s from launch record: %w", handle, err)
	}
	if rec.Handle != handle || rec.Session != session || squadnamespace.NormalizeProfile(rec.TeamProfile) != squadnamespace.NormalizeProfile(profile) || !sameResolvedDir(rec.Root, root) {
		return false, fmt.Errorf("resolve notifier target %s: launch record scope mismatch", handle)
	}
	reserved, err := ledger.Reserve(handle, messageID)
	if err != nil {
		return false, fmt.Errorf("reserve notifier attempt for %s/%s: %w", handle, messageID, err)
	}
	if !reserved {
		return false, nil
	}
	if wakeLive == nil {
		wakeLive = verifiedSessionNotifierWakeLive
	}
	// AMQ's positively verified wake owns terminal input. The session notifier
	// records the ID as observed but must not race a second rooted prompt into
	// the same pane. Ambiguous or absent wake evidence falls through to the
	// one-attempt durable-inbox fallback below.
	if wakeLive(agentDir, root, profile, session, handle, rec) {
		return false, nil
	}
	if rec.Tmux == nil || strings.TrimSpace(rec.Tmux.PaneID) == "" {
		return false, fmt.Errorf("resolve notifier target %s: launch record has no pane", handle)
	}
	if err := sendKeys(rec.Tmux.PaneID, dispatchNudgePrompt(root)); err != nil {
		return false, err
	}
	// The attempt was durably reserved before sendKeys. A transient failure is
	// deliberately not retried: at-most-one pane input wins over a duplicate,
	// while the unread durable message remains the recovery source.
	return true, nil
}

func refreshSessionNotifier(path, token string, rec sessionNotifierRecord) error {
	return flock.WithLock(path+".lock", func() error {
		current, err := readSessionNotifierRecord(path)
		if err != nil {
			return err
		}
		if current.OwnerToken != token {
			return fmt.Errorf("session notifier ownership changed")
		}
		return writeSessionNotifierRecord(path, rec)
	})
}

func releaseSessionNotifier(path string, n sessionNotifierExecution, rec sessionNotifierRecord) error {
	return flock.WithLock(path+".lock", func() error {
		current, err := readSessionNotifierRecord(path)
		if err != nil || current.OwnerToken != n.Token {
			return err
		}
		// The on-disk ledger is authoritative because Reserve writes it before
		// pane input. A stop signal can arrive before the main loop copies that
		// reservation into rec; never let graceful release roll it back.
		rec.AttemptedMessageIDs = cloneSessionNotifierAttempts(current.AttemptedMessageIDs)
		now := n.Now().UTC()
		rec.PID, rec.OwnerToken, rec.Expected = 0, "", false
		rec.Health, rec.LastError = "inactive", ""
		rec.HeartbeatAt, rec.LeaseExpiresAt = now, now
		return writeSessionNotifierRecord(path, rec)
	})
}
