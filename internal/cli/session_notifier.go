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
	"github.com/omriariav/amq-squad/v2/internal/procinfo"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

const (
	sessionNotifierSchema        = 1
	sessionNotifierTTL           = 15 * time.Second
	sessionNotifierHeartbeat     = 3 * time.Second
	sessionNotifierStartupBudget = 5 * time.Second
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
}

type sessionNotifierStatus struct {
	RuntimePath string
	Health      string
	Reason      string
	Record      sessionNotifierRecord
}

type sessionNotifierSendKeys func(paneID, keys string) error

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

	watcher, err := n.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()
	// Snapshot before installing watches, then once more afterwards. A file
	// already in inbox/new is still pending work and may have arrived while the
	// notifier was down, so startup nudges it. This deliberately provides
	// at-least-once delivery across notifier restarts: a crash before the agent
	// drains the message may cause a duplicate wake. Within one process, seen
	// still guarantees at most one successful nudge per message. The second
	// snapshot closes the watcher-install race, and queued events dedupe through
	// the same map.
	pending, err := snapshotSessionInboxMessages(n.Root)
	if err != nil {
		return err
	}
	if err := addNotificationWatchTree(watcher, n.Root); err != nil {
		return fmt.Errorf("watch canonical session root: %w", err)
	}
	seen := make(map[string]struct{}, len(pending))
	for messagePath := range pending {
		nudged, err := notifySessionInboxArrival(n.Root, n.Profile, n.Session, messagePath, seen, n.SendKeys)
		if err != nil {
			return fmt.Errorf("nudge pending inbox arrival during notifier startup: %w", err)
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
		if _, existed := seen[messagePath]; existed {
			continue
		}
		nudged, err := notifySessionInboxArrival(n.Root, n.Profile, n.Session, messagePath, seen, n.SendKeys)
		if err != nil {
			return fmt.Errorf("nudge inbox arrival during notifier startup: %w", err)
		}
		if nudged {
			rec.LastNudgeAt, rec.LastError = n.Now().UTC(), ""
		}
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
			nudged, nudgeErr := notifySessionInboxArrival(n.Root, n.Profile, n.Session, event.Name, seen, n.SendKeys)
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
			nudged, rescanErr := rescanSessionInboxMessages(n.Root, n.Profile, n.Session, seen, n.SendKeys)
			heartbeatErr = errors.Join(heartbeatErr, rescanErr)
			if heartbeatErr != nil {
				rec.LastError = heartbeatErr.Error()
			} else if nudged {
				rec.LastNudgeAt, rec.LastError = n.Now().UTC(), ""
			}
		}
		now = n.Now().UTC()
		rec.HeartbeatAt, rec.LeaseExpiresAt = now, now.Add(n.TTL)
		if err := refreshSessionNotifier(path, n.Token, rec); err != nil {
			return err
		}
	}
}

func rescanSessionInboxMessages(root, profile, session string, seen map[string]struct{}, sendKeys sessionNotifierSendKeys) (bool, error) {
	pending, err := snapshotSessionInboxMessages(root)
	if err != nil {
		return false, err
	}
	var (
		nudgedAny bool
		nudgeErrs []error
	)
	for messagePath := range pending {
		nudged, err := notifySessionInboxArrival(root, profile, session, messagePath, seen, sendKeys)
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

func notifySessionInboxArrival(root, profile, session, messagePath string, seen map[string]struct{}, sendKeys sessionNotifierSendKeys) (bool, error) {
	path := canonicalFilesystemPath(messagePath)
	rel, err := filepath.Rel(canonicalFilesystemPath(root), path)
	if err != nil {
		return false, nil
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) != 5 || parts[0] != "agents" || parts[2] != "inbox" || parts[3] != "new" || parts[4] == "" {
		return false, nil
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false, nil
	}
	if _, ok := seen[path]; ok {
		return false, nil
	}
	handle := parts[1]
	rec, err := launch.Read(filepath.Join(root, "agents", handle))
	if err != nil {
		return false, fmt.Errorf("resolve notifier target %s from launch record: %w", handle, err)
	}
	if rec.Handle != handle || rec.Session != session || squadnamespace.NormalizeProfile(rec.TeamProfile) != squadnamespace.NormalizeProfile(profile) || !sameResolvedDir(rec.Root, root) {
		return false, fmt.Errorf("resolve notifier target %s: launch record scope mismatch", handle)
	}
	if rec.Tmux == nil || strings.TrimSpace(rec.Tmux.PaneID) == "" {
		return false, fmt.Errorf("resolve notifier target %s: launch record has no pane", handle)
	}
	if err := sendKeys(rec.Tmux.PaneID, dispatchNudgePrompt(root)); err != nil {
		return false, err
	}
	// Mark the arrival handled only after tmux accepted the nudge. A transient
	// send-keys failure can therefore be retried by a later fsnotify event,
	// while a successful delivery can never double-nudge the pane.
	seen[path] = struct{}{}
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
		now := n.Now().UTC()
		rec.PID, rec.OwnerToken, rec.Expected = 0, "", false
		rec.Health, rec.LastError = "inactive", ""
		rec.HeartbeatAt, rec.LeaseExpiresAt = now, now
		return writeSessionNotifierRecord(path, rec)
	})
}
