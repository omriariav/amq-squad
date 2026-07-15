package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/omriariav/amq-squad/v2/internal/flock"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/procinfo"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

const (
	notificationWatcherSchema         = 1
	defaultNotificationWatcherTTL     = 15 * time.Second
	defaultNotificationWatcherBeat    = 3 * time.Second
	defaultNotificationWatcherRescan  = 5 * time.Second
	defaultNotificationWatcherStartup = 5 * time.Second
)

var (
	notificationWatcherPartialLaunchGrace    = 5 * time.Second
	notificationWatcherPartialLaunchInterval = 100 * time.Millisecond
)

// notificationWatcherRecord is the durable runtime identity for exactly one
// project/profile/session notification watcher. It deliberately lives outside
// operator-loop: attention delivery is required even for lead_pane profiles
// whose operator contract has poll_required=false.
type notificationWatcherRecord struct {
	SchemaVersion  int       `json:"schema_version"`
	ProjectDir     string    `json:"project_dir"`
	Profile        string    `json:"profile"`
	Session        string    `json:"session"`
	NamespaceID    string    `json:"namespace_id"`
	PID            int       `json:"pid"`
	Host           string    `json:"host"`
	Owner          string    `json:"owner"`
	OwnerToken     string    `json:"owner_token"`
	LeaseTTL       string    `json:"lease_ttl"`
	LeaseExpiresAt time.Time `json:"lease_expires_at"`
	HeartbeatAt    time.Time `json:"heartbeat_at"`
	LastScanAt     time.Time `json:"last_scan_at,omitempty"`
	LastEventAt    time.Time `json:"last_event_at,omitempty"`
	StatePath      string    `json:"state_path"`
	Expected       bool      `json:"expected"`
	Health         string    `json:"health"`
	LastError      string    `json:"last_error,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type notificationWatcherStatus struct {
	Enabled        bool      `json:"enabled"`
	Health         string    `json:"health"`
	Reason         string    `json:"reason,omitempty"`
	RuntimePath    string    `json:"runtime_path"`
	SchemaVersion  int       `json:"schema_version,omitempty"`
	PID            int       `json:"pid,omitempty"`
	Host           string    `json:"host,omitempty"`
	Owner          string    `json:"owner,omitempty"`
	LeaseExpiresAt time.Time `json:"lease_expires_at,omitempty"`
	HeartbeatAt    time.Time `json:"heartbeat_at,omitempty"`
	LastScanAt     time.Time `json:"last_scan_at,omitempty"`
	StatePath      string    `json:"state_path,omitempty"`
	record         notificationWatcherRecord
}

type notificationWatcherExecution struct {
	ProjectDir string
	Profile    string
	Session    string
	BaseRoot   string
	Token      string
	TTL        time.Duration
	Heartbeat  time.Duration
	Rescan     time.Duration
	Now        func() time.Time
	Out        io.Writer
	Stop       <-chan os.Signal
	NewFSWatch func() (*fsnotify.Watcher, error)
	Deliver    func(context.Context, time.Time) (notifyDeliverySummary, error)
}

type notificationWatcherProcess interface {
	PID() int
	Signal(os.Signal) error
	Release() error
}

type execNotificationWatcherProcess struct{ p *os.Process }

func (p execNotificationWatcherProcess) PID() int                 { return p.p.Pid }
func (p execNotificationWatcherProcess) Signal(s os.Signal) error { return p.p.Signal(s) }
func (p execNotificationWatcherProcess) Release() error           { return p.p.Release() }

var (
	notificationWatcherNow          = time.Now
	notificationWatcherPIDAlive     = procinfo.Alive
	notificationWatcherProcessMatch = procinfo.Match
	notificationWatcherSleep        = time.Sleep
	notificationWatcherSpawn        = spawnNotificationWatcherProcess
	notificationWatcherSignal       = func(pid int, sig os.Signal) error {
		p, err := os.FindProcess(pid)
		if err != nil {
			return err
		}
		return p.Signal(sig)
	}
)

func notificationWatcherRuntimePath(projectDir, profile, session string) string {
	base := filepath.Join(projectDir, team.DirName, "notification-watchers")
	profile = squadnamespace.NormalizeProfile(profile)
	if profile != team.DefaultProfile {
		base = filepath.Join(base, profile)
	}
	return filepath.Join(base, sanitizeWorkstreamName(session)+".json")
}

func notificationWatcherLockPath(projectDir, profile, session string) string {
	return notificationWatcherRuntimePath(projectDir, profile, session) + ".lock"
}

func notificationWatcherLogPath(projectDir, profile, session string) string {
	base := filepath.Join(projectDir, team.DirName, "notification-watchers", "logs")
	profile = squadnamespace.NormalizeProfile(profile)
	if profile != team.DefaultProfile {
		base = filepath.Join(base, profile)
	}
	return filepath.Join(base, sanitizeWorkstreamName(session)+".log")
}

func readNotificationWatcherRecord(path string) (notificationWatcherRecord, error) {
	var rec notificationWatcherRecord
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return rec, nil
		}
		return rec, fmt.Errorf("read notification watcher runtime: %w", err)
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return rec, nil
	}
	if err := json.Unmarshal(b, &rec); err != nil {
		return rec, fmt.Errorf("parse notification watcher runtime %s: %w", path, err)
	}
	if rec.SchemaVersion != notificationWatcherSchema {
		return rec, fmt.Errorf("unsupported notification watcher schema %d", rec.SchemaVersion)
	}
	return rec, nil
}

func writeNotificationWatcherRecord(path string, rec notificationWatcherRecord) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("ensure notification watcher runtime dir: %w", err)
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal notification watcher runtime: %w", err)
	}
	tmp := path + ".tmp-" + rec.OwnerToken
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("write notification watcher runtime: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("publish notification watcher runtime: %w", err)
	}
	return nil
}

func notificationWatcherProcessMatches(rec notificationWatcherRecord) bool {
	host, _ := os.Hostname()
	if strings.TrimSpace(rec.Host) != "" && strings.TrimSpace(rec.Host) != strings.TrimSpace(host) {
		return false
	}
	if rec.PID <= 0 || strings.TrimSpace(rec.OwnerToken) == "" || !notificationWatcherPIDAlive(rec.PID) {
		return false
	}
	token := rec.OwnerToken
	return notificationWatcherProcessMatch(rec.PID, func(args string) bool {
		return strings.Contains(args, "_notification-watch") &&
			strings.Contains(args, token) &&
			strings.Contains(args, rec.ProjectDir) &&
			strings.Contains(args, "--profile "+rec.Profile) &&
			strings.Contains(args, "--session "+rec.Session)
	})
}

func inspectNotificationWatcher(t team.Team, profile, session string, now time.Time) notificationWatcherStatus {
	policy := team.EffectiveOperatorNotifications(t.Operator)
	path := notificationWatcherRuntimePath(t.Project, profile, session)
	result := notificationWatcherStatus{Enabled: policy.Enabled, RuntimePath: path, Health: "disabled"}
	if !policy.Enabled {
		return result
	}
	rec, err := readNotificationWatcherRecord(path)
	if err != nil {
		result.Health = "unhealthy"
		result.Reason = err.Error()
		return result
	}
	if rec.SchemaVersion == notificationWatcherSchema && !rec.Expected {
		result.SchemaVersion = rec.SchemaVersion
		result.StatePath = rec.StatePath
		result.record = rec
		result.Health = "inactive"
		result.Reason = "notification watcher was cleanly stopped with the session"
		return result
	}
	if strings.TrimSpace(rec.OwnerToken) == "" {
		result.Health = "unhealthy"
		result.Reason = "notification watcher runtime is absent"
		return result
	}
	result.SchemaVersion = rec.SchemaVersion
	result.PID = rec.PID
	result.Host = rec.Host
	result.Owner = rec.Owner
	result.LeaseExpiresAt = rec.LeaseExpiresAt
	result.HeartbeatAt = rec.HeartbeatAt
	result.LastScanAt = rec.LastScanAt
	result.StatePath = rec.StatePath
	result.record = rec
	if filepath.Clean(rec.ProjectDir) != filepath.Clean(t.Project) || rec.Profile != squadnamespace.NormalizeProfile(profile) || rec.Session != session || rec.NamespaceID != squadnamespace.ID(profile, session) {
		result.Health = "unhealthy"
		result.Reason = "notification watcher runtime project/profile/session binding does not match the selected namespace"
		return result
	}
	if !now.Before(rec.LeaseExpiresAt) {
		result.Health = "unhealthy"
		result.Reason = "notification watcher lease is stale"
		return result
	}
	host, _ := os.Hostname()
	if strings.TrimSpace(rec.Host) != "" && strings.TrimSpace(rec.Host) != strings.TrimSpace(host) {
		switch rec.Health {
		case "healthy":
			result.Health = "external-active"
			result.Reason = fmt.Sprintf("notification watcher lease is active on host %s", rec.Host)
		case "degraded":
			result.Health = "degraded"
			result.Reason = fmt.Sprintf("notification watcher on host %s is degraded", rec.Host)
			if detail := strings.TrimSpace(rec.LastError); detail != "" {
				result.Reason += ": " + detail
			}
		default:
			result.Health = "unhealthy"
			state := strings.TrimSpace(rec.Health)
			if state == "" {
				state = "unknown"
			}
			result.Reason = fmt.Sprintf("notification watcher on host %s reported %s", rec.Host, state)
			if detail := strings.TrimSpace(rec.LastError); detail != "" {
				result.Reason += ": " + detail
			}
		}
		return result
	}
	if !notificationWatcherProcessMatches(rec) {
		result.Health = "unhealthy"
		result.Reason = "notification watcher PID is absent or does not match its owner token"
		return result
	}
	if rec.Health != "healthy" {
		result.Health = rec.Health
		if result.Health == "" || result.Health == "starting" {
			result.Health = "unhealthy"
		}
		result.Reason = strings.TrimSpace(rec.LastError)
		if result.Reason == "" {
			result.Reason = "notification watcher reported " + rec.Health
		}
		return result
	}
	result.Health = "healthy"
	return result
}

// reconcileNotificationWatcherStarted is the fail-closed lifecycle gate used by
// up/run start/resume. A live launch never proceeds with enabled notification
// policy unless a scoped watcher owns a fresh lease on this host.
func reconcileNotificationWatcherStarted(t team.Team, profile, session, baseRoot string) error {
	teamExisted := team.ExistsProfile(t.Project, profile)
	initialIdentity, err := captureNamespaceEndpointIdentity(squadnamespace.Resolve(t.Project, profile, session), "")
	if err != nil {
		return err
	}
	admission, err := acquireNamespaceWatcherAdmission(t.Project, profile, session)
	if err != nil {
		return err
	}
	defer admission.close()
	currentTeam := t
	if teamExisted {
		currentTeam, err = team.ReadProfile(t.Project, profile)
		if err != nil {
			return fmt.Errorf("notification watcher start refused: reread team under admission: %w", err)
		}
	} else if team.ExistsProfile(t.Project, profile) {
		return fmt.Errorf("notification watcher start refused: team profile appeared before admission; retry")
	}
	currentIdentity, err := captureNamespaceEndpointIdentity(squadnamespace.Resolve(t.Project, profile, session), "")
	if err != nil {
		return err
	}
	if err := validateReResolvedEndpointIdentity("notification watcher start", initialIdentity, currentIdentity); err != nil {
		return err
	}
	t = currentTeam
	if err := ensureNoNamespaceMigration("notification watcher start", t.Project, profile, session); err != nil {
		return err
	}
	policy := team.EffectiveOperatorNotifications(t.Operator)
	if !policy.Enabled {
		return nil
	}
	now := notificationWatcherNow()
	if status := inspectNotificationWatcher(t, profile, session, now); notificationWatcherReady(status) {
		return nil
	}
	if strings.TrimSpace(baseRoot) == "" {
		resolved, err := scanBaseRootForProject(t.Project)
		if err != nil {
			return fmt.Errorf("notification watcher required but AMQ base root cannot be resolved: %w", err)
		}
		baseRoot = resolved
		if squadnamespace.NormalizeProfile(profile) != team.DefaultProfile {
			baseRoot = filepath.Join(baseRoot, squadnamespace.NormalizeProfile(profile))
		}
	}
	status := inspectNotificationWatcher(t, profile, session, now)
	if status.record.OwnerToken != "" && status.record.Expected && now.Before(status.record.LeaseExpiresAt) {
		host, _ := os.Hostname()
		local := strings.TrimSpace(status.record.Host) == "" || strings.TrimSpace(status.record.Host) == strings.TrimSpace(host)
		if local && status.record.PID > 0 && !notificationWatcherPIDAlive(status.record.PID) {
			if err := expireDeadLocalNotificationWatcher(t.Project, profile, session, status.record, now); err != nil {
				return err
			}
			status = inspectNotificationWatcher(t, profile, session, now)
		}
	}
	if status.record.OwnerToken != "" && status.record.Expected && now.Before(status.record.LeaseExpiresAt) {
		return fmt.Errorf("notification watcher is active but unhealthy: %s", status.Reason)
	}
	if err := os.MkdirAll(baseRoot, 0o755); err != nil {
		return fmt.Errorf("notification watcher required but AMQ profile root cannot be prepared: %w", err)
	}
	token := randomToken()
	proc, err := notificationWatcherSpawn(t.Project, profile, session, baseRoot, token)
	if err != nil {
		return fmt.Errorf("notification watcher required but could not be started: %w", err)
	}
	if proc != nil {
		_ = proc.Release()
	}
	deadline := notificationWatcherNow().Add(defaultNotificationWatcherStartup)
	for {
		status := inspectNotificationWatcher(t, profile, session, notificationWatcherNow())
		if notificationWatcherReady(status) {
			return nil
		}
		if !notificationWatcherNow().Before(deadline) {
			if proc != nil {
				_ = proc.Signal(syscall.SIGTERM)
			}
			return fmt.Errorf("notification watcher required but healthy lease was not established within %s: %s", defaultNotificationWatcherStartup, status.Reason)
		}
		notificationWatcherSleep(25 * time.Millisecond)
	}
}

func notificationWatcherReady(status notificationWatcherStatus) bool {
	switch status.Health {
	case "healthy", "external-active":
		return true
	case "degraded":
		return !status.LastScanAt.IsZero()
	default:
		return false
	}
}

func expireDeadLocalNotificationWatcher(projectDir, profile, session string, observed notificationWatcherRecord, now time.Time) error {
	path := notificationWatcherRuntimePath(projectDir, profile, session)
	return flock.WithLock(notificationWatcherLockPath(projectDir, profile, session), func() error {
		current, err := readNotificationWatcherRecord(path)
		if err != nil {
			return err
		}
		if current.OwnerToken != observed.OwnerToken || (current.PID > 0 && notificationWatcherPIDAlive(current.PID)) {
			return nil
		}
		current.LeaseExpiresAt = now.UTC()
		current.Health = "unhealthy"
		current.LastError = "local notification watcher process exited before lease expiry"
		current.UpdatedAt = now.UTC()
		return writeNotificationWatcherRecord(path, current)
	})
}

func spawnNotificationWatcherProcess(projectDir, profile, session, baseRoot, token string) (notificationWatcherProcess, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	logPath := notificationWatcherLogPath(projectDir, profile, session)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	defer logFile.Close()
	cmd := exec.Command(exe, "_notification-watch", "--project", projectDir, "--profile", profile, "--session", session, "--base-root", baseRoot, "--owner-token", token)
	cmd.Dir = projectDir
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = envWithoutAMQIdentity(os.Environ())
	ownExternalLeadWakeProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return execNotificationWatcherProcess{p: cmd.Process}, nil
}

func stopNotificationWatcher(projectDir, profile, session string) error {
	return stopNotificationWatcherGeneration(projectDir, profile, session, "")
}

func stopNotificationWatcherGeneration(projectDir, profile, session, expectedToken string) error {
	initialIdentity, err := captureNamespaceEndpointIdentity(squadnamespace.Resolve(projectDir, profile, session), "")
	if err != nil {
		return err
	}
	admission, err := acquireNamespaceWatcherAdmission(projectDir, profile, session)
	if err != nil {
		return err
	}
	defer admission.close()
	currentIdentity, err := captureNamespaceEndpointIdentity(squadnamespace.Resolve(projectDir, profile, session), "")
	if err != nil {
		return err
	}
	if err := validateReResolvedEndpointIdentity("notification watcher stop", initialIdentity, currentIdentity); err != nil {
		return err
	}
	if err := ensureNoNamespaceMigration("notification watcher stop", projectDir, profile, session); err != nil {
		return err
	}
	path := notificationWatcherRuntimePath(projectDir, profile, session)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("ensure notification watcher runtime dir: %w", err)
	}
	rec, err := readNotificationWatcherRecord(path)
	if err != nil {
		return err
	}
	if expectedToken != "" && rec.OwnerToken != expectedToken {
		return nil
	}
	if strings.TrimSpace(rec.OwnerToken) == "" {
		now := notificationWatcherNow().UTC()
		inactive := notificationWatcherRecord{
			SchemaVersion: notificationWatcherSchema, ProjectDir: projectDir,
			Profile: squadnamespace.NormalizeProfile(profile), Session: session,
			NamespaceID: squadnamespace.ID(profile, session), StatePath: defaultNotifyStatePath(projectDir),
			Expected: false, Health: "inactive", HeartbeatAt: now, LeaseExpiresAt: now, UpdatedAt: now,
		}
		return flock.WithLock(notificationWatcherLockPath(projectDir, profile, session), func() error {
			current, err := readNotificationWatcherRecord(path)
			if err != nil {
				return err
			}
			if strings.TrimSpace(current.OwnerToken) != "" {
				return fmt.Errorf("notification watcher ownership appeared while stopping; retry lifecycle reconciliation")
			}
			if current.SchemaVersion == 0 {
				return writeNotificationWatcherRecord(path, inactive)
			}
			if notificationWatcherInactiveTombstoneForScope(current, projectDir, profile, session, now) {
				return nil
			}
			return fmt.Errorf("notification watcher stop observed invalid blank-owner runtime record for %s/%s; ownership preserved", squadnamespace.NormalizeProfile(profile), session)
		})
	}
	host, _ := os.Hostname()
	local := strings.TrimSpace(rec.Host) == "" || strings.TrimSpace(rec.Host) == strings.TrimSpace(host)
	if !local && notificationWatcherNow().Before(rec.LeaseExpiresAt) {
		return fmt.Errorf("notification watcher is owned by host %s until %s; refusing to stop a remote active watcher", rec.Host, rec.LeaseExpiresAt.UTC().Format(time.RFC3339))
	}
	if local && rec.PID > 0 && notificationWatcherPIDAlive(rec.PID) && !notificationWatcherProcessMatches(rec) {
		return fmt.Errorf("notification watcher pid %d is alive but exact project/profile/session/token identity is unverified; ownership preserved", rec.PID)
	}
	if local && notificationWatcherProcessMatches(rec) {
		if signalErr := notificationWatcherSignal(rec.PID, syscall.SIGTERM); signalErr != nil {
			return finalizeNotificationWatcherStop(projectDir, profile, session, path, rec, signalErr)
		}
		deadline := notificationWatcherNow().Add(2 * time.Second)
		for notificationWatcherPIDAlive(rec.PID) && notificationWatcherNow().Before(deadline) {
			notificationWatcherSleep(20 * time.Millisecond)
		}
		if notificationWatcherPIDAlive(rec.PID) {
			return fmt.Errorf("notification watcher pid %d did not exit after SIGTERM; ownership preserved", rec.PID)
		}
	}
	return finalizeNotificationWatcherStop(projectDir, profile, session, path, rec, nil)
}

func finalizeNotificationWatcherStop(projectDir, profile, session, path string, observed notificationWatcherRecord, signalErr error) error {
	return flock.WithLock(notificationWatcherLockPath(projectDir, profile, session), func() error {
		current, err := readNotificationWatcherRecord(path)
		if err != nil {
			return err
		}
		now := notificationWatcherNow().UTC()
		if notificationWatcherInactiveTombstoneForScope(current, projectDir, profile, session, now) {
			return nil
		}
		if current.OwnerToken != observed.OwnerToken {
			if strings.TrimSpace(current.OwnerToken) == "" {
				return fmt.Errorf("notification watcher stop observed invalid inactive tombstone for %s/%s; ownership preserved", squadnamespace.NormalizeProfile(profile), session)
			}
			return fmt.Errorf("notification watcher ownership changed from %q to %q while stopping; retry lifecycle reconciliation", observed.OwnerToken, current.OwnerToken)
		}
		if !notificationWatcherRecordForScope(current, projectDir, profile, session) {
			return fmt.Errorf("notification watcher observed generation changed project/profile/session scope while stopping; ownership preserved")
		}
		if signalErr != nil {
			return fmt.Errorf("stop notification watcher pid %d: %w", observed.PID, signalErr)
		}
		current.PID = 0
		current.OwnerToken = ""
		current.Expected = false
		current.Health = "inactive"
		current.LastError = ""
		current.HeartbeatAt = now
		current.LeaseExpiresAt = now
		current.UpdatedAt = now
		return writeNotificationWatcherRecord(path, current)
	})
}

func notificationWatcherInactiveTombstoneForScope(rec notificationWatcherRecord, projectDir, profile, session string, now time.Time) bool {
	return notificationWatcherRecordForScope(rec, projectDir, profile, session) &&
		rec.PID == 0 &&
		strings.TrimSpace(rec.OwnerToken) == "" &&
		!rec.Expected &&
		rec.Health == "inactive" &&
		!rec.LeaseExpiresAt.After(now)
}

func notificationWatcherRecordForScope(rec notificationWatcherRecord, projectDir, profile, session string) bool {
	return rec.SchemaVersion == notificationWatcherSchema &&
		filepath.Clean(rec.ProjectDir) == filepath.Clean(projectDir) &&
		rec.Profile == squadnamespace.NormalizeProfile(profile) &&
		rec.Session == session &&
		rec.NamespaceID == squadnamespace.ID(profile, session)
}

func notificationWatcherGeneration(t team.Team, profile, session string) string {
	return inspectNotificationWatcher(t, profile, session, notificationWatcherNow()).record.OwnerToken
}

func cleanupCreatedNotificationWatcherAfterLaunchFailure(t team.Team, profile, session, createdToken string, probe duplicateLaunchProbe) error {
	if strings.TrimSpace(createdToken) == "" {
		return nil
	}
	deadline := notificationWatcherNow().Add(notificationWatcherPartialLaunchGrace)
	for {
		for _, row := range buildStatusRows(t, profile, session, probe) {
			if row.Status == statusStateLive || row.Status == statusStateWakeLive {
				return nil
			}
		}
		if !notificationWatcherNow().Before(deadline) {
			break
		}
		notificationWatcherSleep(notificationWatcherPartialLaunchInterval)
	}
	return stopNotificationWatcherGeneration(t.Project, profile, session, createdToken)
}

func runNotificationWatcher(args []string) error {
	fs := flag.NewFlagSet("_notification-watch", flag.ContinueOnError)
	project := fs.String("project", "", "project directory")
	profile := fs.String("profile", team.DefaultProfile, "team profile")
	session := fs.String("session", "", "session")
	baseRoot := fs.String("base-root", "", "profile AMQ base root")
	token := fs.String("owner-token", "", "unique owner token")
	ttl := fs.Duration("ttl", defaultNotificationWatcherTTL, "lease TTL")
	heartbeat := fs.Duration("heartbeat", defaultNotificationWatcherBeat, "heartbeat cadence")
	rescan := fs.Duration("rescan", defaultNotificationWatcherRescan, "bounded full rescan cadence")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if strings.TrimSpace(*project) == "" || strings.TrimSpace(*session) == "" || strings.TrimSpace(*baseRoot) == "" || strings.TrimSpace(*token) == "" {
		return usageErrorf("_notification-watch requires --project, --session, --base-root, and --owner-token")
	}
	if err := validateWorkstreamName(strings.TrimSpace(*session)); err != nil {
		return err
	}
	if _, err := resolveProfileFlag(*profile); err != nil {
		return err
	}
	if *ttl <= 0 || *heartbeat <= 0 || *rescan <= 0 || *heartbeat > *ttl/2 {
		return usageErrorf("invalid watcher timing: heartbeat must be <= ttl/2 and all durations > 0")
	}
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	return executeNotificationWatcher(notificationWatcherExecution{
		ProjectDir: *project, Profile: *profile, Session: *session, BaseRoot: *baseRoot,
		Token: *token, TTL: *ttl, Heartbeat: *heartbeat, Rescan: *rescan,
		Now: time.Now, Out: os.Stdout, Stop: sigCh, NewFSWatch: fsnotify.NewWatcher,
	})
}

func executeNotificationWatcher(w notificationWatcherExecution) error {
	nowFn := w.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	profile := squadnamespace.NormalizeProfile(w.Profile)
	if err := validateWorkstreamName(strings.TrimSpace(w.Session)); err != nil {
		return err
	}
	if _, err := resolveProfileFlag(profile); err != nil {
		return err
	}
	initialIdentity, err := captureNamespaceEndpointIdentity(squadnamespace.Resolve(w.ProjectDir, profile, w.Session), "")
	if err != nil {
		return err
	}
	admission, err := acquireNamespaceWatcherAdmission(w.ProjectDir, profile, w.Session)
	if err != nil {
		return err
	}
	defer admission.close()
	currentIdentity, err := captureNamespaceEndpointIdentity(squadnamespace.Resolve(w.ProjectDir, profile, w.Session), "")
	if err != nil {
		return err
	}
	if err := validateReResolvedEndpointIdentity("notification watcher", initialIdentity, currentIdentity); err != nil {
		return err
	}
	if err := ensureNoNamespaceMigration("notification watcher", w.ProjectDir, profile, w.Session); err != nil {
		return err
	}
	t, err := team.ReadProfile(w.ProjectDir, profile)
	if err != nil {
		return err
	}
	if !team.EffectiveOperatorNotifications(t.Operator).Enabled {
		return fmt.Errorf("notification watcher refused: notifications are disabled")
	}
	path := notificationWatcherRuntimePath(w.ProjectDir, profile, w.Session)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("ensure notification watcher runtime dir: %w", err)
	}
	host, _ := os.Hostname()
	rec := notificationWatcherRecord{
		SchemaVersion: notificationWatcherSchema, ProjectDir: w.ProjectDir, Profile: profile,
		Session: w.Session, NamespaceID: squadnamespace.ID(profile, w.Session), PID: os.Getpid(),
		Host: host, Owner: "supervised", OwnerToken: w.Token, LeaseTTL: w.TTL.String(),
		StatePath: defaultNotifyStatePath(w.ProjectDir), Expected: true, Health: "starting",
	}
	claimErr := flock.WithLock(notificationWatcherLockPath(w.ProjectDir, profile, w.Session), func() error {
		current, err := readNotificationWatcherRecord(path)
		if err != nil {
			return err
		}
		now := nowFn()
		currentFresh := current.OwnerToken != "" && current.OwnerToken != w.Token && now.Before(current.LeaseExpiresAt)
		if currentFresh {
			return fmt.Errorf("notification watcher lease held by %s pid %d until %s", current.OwnerToken, current.PID, current.LeaseExpiresAt.UTC().Format(time.RFC3339))
		}
		rec.HeartbeatAt = now.UTC()
		rec.LeaseExpiresAt = now.Add(w.TTL).UTC()
		rec.UpdatedAt = now.UTC()
		return writeNotificationWatcherRecord(path, rec)
	})
	if claimErr != nil {
		return claimErr
	}
	newFS := w.NewFSWatch
	if newFS == nil {
		newFS = fsnotify.NewWatcher
	}
	fw, fsErr := newFS()
	if fw != nil {
		defer fw.Close()
	}
	root := filepath.Join(w.BaseRoot, w.Session)
	if fsErr == nil {
		fsErr = addNotificationWatchTree(fw, root)
		if os.IsNotExist(fsErr) {
			// Before the backend opens its first agent, the session root may not
			// exist. Watch the profile root structurally; creation of the scoped
			// session triggers a recursive watch rebuild. Never create the session
			// here, because an empty watcher-created root would make a failed `up`
			// look like an existing run.
			fsErr = fw.Add(w.BaseRoot)
		}
	}
	fsDegraded := ""
	if fsErr != nil {
		fsDegraded = "fsnotify unavailable; periodic rescan remains active: " + fsErr.Error()
		rec.Health, rec.LastError = "degraded", fsDegraded
		if err := refreshNotificationWatcherLease(path, &rec, w.Token, nowFn()); err != nil {
			return err
		}
	}
	deliver := w.Deliver
	if deliver == nil {
		deliver = func(ctx context.Context, now time.Time) (notifyDeliverySummary, error) {
			var out bytes.Buffer
			err := executeNotify(notifyExecution{Context: ctx, ProjectDir: w.ProjectDir, Profile: profile, Session: w.Session, BaseRoot: w.BaseRoot, RenotifyAfter: defaultOperatorRenotifyAfter, Deliver: true, JSON: true, Out: &out, Now: func() time.Time { return now }, Probe: state.DefaultProbe})
			if err != nil {
				return notifyDeliverySummary{}, err
			}
			var env struct {
				Data notifyEnvelopeData `json:"data"`
			}
			if err := json.Unmarshal(out.Bytes(), &env); err != nil {
				return notifyDeliverySummary{}, err
			}
			return env.Data.DeliverySummary, nil
		}
	}
	heartbeat := time.NewTicker(w.Heartbeat)
	defer heartbeat.Stop()
	rescan := time.NewTicker(w.Rescan)
	defer rescan.Stop()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type scanResult struct {
		summary notifyDeliverySummary
		err     error
		at      time.Time
	}
	scanDone := make(chan scanResult, 1)
	inFlight := false
	pendingScan := false
	startScan := func() error {
		if inFlight {
			pendingScan = true
			return nil
		}
		if err := verifyNotificationWatcherOwnership(path, w.Token); err != nil {
			return err
		}
		at := nowFn()
		inFlight = true
		go func() {
			summary, err := deliver(ctx, at)
			scanDone <- scanResult{summary: summary, err: err, at: at}
		}()
		return nil
	}
	if err := startScan(); err != nil {
		return err
	}

	var debounce <-chan time.Time
	var debounceTimer *time.Timer
	var events <-chan fsnotify.Event
	var errorsCh <-chan error
	if fw != nil {
		events = fw.Events
		errorsCh = fw.Errors
	}
	for {
		select {
		case <-w.Stop:
			cancel()
			if inFlight {
				select {
				case <-scanDone:
				case <-time.After(2 * time.Second):
					return fmt.Errorf("notification watcher delivery did not stop after cancellation")
				}
			}
			if err := releaseNotificationWatcherLease(path, &rec, w.Token, nowFn()); err != nil {
				return err
			}
			return nil
		case event, ok := <-events:
			if !ok {
				events = nil
				fsDegraded = "fsnotify event stream closed; periodic rescan remains active"
				rec.Health, rec.LastError = "degraded", fsDegraded
				if e := refreshNotificationWatcherLease(path, &rec, w.Token, nowFn()); e != nil {
					return e
				}
				continue
			}
			rec.LastEventAt = nowFn().UTC()
			if event.Op&fsnotify.Create != 0 {
				if info, e := os.Stat(event.Name); e == nil && info.IsDir() {
					_ = addNotificationWatchTree(fw, event.Name)
				}
			}
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			debounceTimer = time.NewTimer(50 * time.Millisecond)
			debounce = debounceTimer.C
		case err, ok := <-errorsCh:
			if !ok {
				errorsCh = nil
				fsDegraded = "fsnotify error stream closed; periodic rescan remains active"
				rec.Health, rec.LastError = "degraded", fsDegraded
				if e := refreshNotificationWatcherLease(path, &rec, w.Token, nowFn()); e != nil {
					return e
				}
				continue
			}
			rec.Health, rec.LastError = "degraded", err.Error()
			fsDegraded = "fsnotify degraded; periodic rescan remains active: " + err.Error()
			if e := refreshNotificationWatcherLease(path, &rec, w.Token, nowFn()); e != nil {
				return e
			}
		case <-debounce:
			debounce = nil
			if err := startScan(); err != nil {
				return err
			}
		case <-rescan.C:
			if fw != nil {
				if err := addNotificationWatchTree(fw, root); err != nil {
					if os.IsNotExist(err) {
						if addErr := fw.Add(w.BaseRoot); addErr != nil && !strings.Contains(addErr.Error(), "exists") {
							fsDegraded = "fsnotify degraded; periodic rescan remains active: " + addErr.Error()
						}
					} else {
						fsDegraded = "fsnotify degraded; periodic rescan remains active: " + err.Error()
					}
				}
			}
			currentTeam, teamErr := team.ReadProfile(w.ProjectDir, profile)
			if teamErr == nil && !team.EffectiveOperatorNotifications(currentTeam.Operator).Enabled {
				if err := releaseNotificationWatcherLease(path, &rec, w.Token, nowFn()); err != nil {
					return err
				}
				return nil
			}
			if err := startScan(); err != nil {
				return err
			}
		case result := <-scanDone:
			inFlight = false
			rec.LastScanAt = result.at.UTC()
			switch {
			case result.err != nil:
				rec.Health, rec.LastError = "degraded", result.err.Error()
			case result.summary.Failed > 0:
				rec.Health, rec.LastError = "degraded", fmt.Sprintf("%d notification sink delivery attempt(s) failed", result.summary.Failed)
			case fsDegraded != "":
				rec.Health, rec.LastError = "degraded", fsDegraded
			default:
				rec.Health, rec.LastError = "healthy", ""
			}
			if err := refreshNotificationWatcherLease(path, &rec, w.Token, nowFn()); err != nil {
				return err
			}
			if pendingScan {
				pendingScan = false
				if err := startScan(); err != nil {
					return err
				}
			}
		case <-heartbeat.C:
			if err := refreshNotificationWatcherLease(path, &rec, w.Token, nowFn()); err != nil {
				return err
			}
		}
	}
}

func addNotificationWatchTree(w *fsnotify.Watcher, root string) error {
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("notification namespace root %s is not a directory", root)
	}
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if err := w.Add(path); err != nil && !strings.Contains(err.Error(), "exists") {
				return err
			}
		}
		return nil
	})
}

func verifyNotificationWatcherOwnership(path, token string) error {
	// Fence delivery itself, not only the subsequent heartbeat. An expired old
	// child that wakes after another generation claims the lease must exit
	// before touching the shared per-sink reservation state.
	if err := flock.WithLock(path+".lock", func() error {
		current, err := readNotificationWatcherRecord(path)
		if err != nil {
			return err
		}
		if current.OwnerToken != token {
			return fmt.Errorf("notification watcher lease lost to %s", current.OwnerToken)
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func refreshNotificationWatcherLease(path string, rec *notificationWatcherRecord, token string, now time.Time) error {
	return flock.WithLock(path+".lock", func() error {
		current, err := readNotificationWatcherRecord(path)
		if err != nil {
			return err
		}
		if current.OwnerToken != token {
			return fmt.Errorf("notification watcher lease lost to %s", current.OwnerToken)
		}
		rec.HeartbeatAt = now.UTC()
		ttl, err := time.ParseDuration(rec.LeaseTTL)
		if err != nil || ttl <= 0 {
			ttl = defaultNotificationWatcherTTL
		}
		rec.LeaseExpiresAt = now.Add(ttl).UTC()
		rec.UpdatedAt = now.UTC()
		return writeNotificationWatcherRecord(path, *rec)
	})
}

// releaseNotificationWatcherLease publishes an immediately reclaimable
// inactive tombstone for an owned clean exit. Unlike a heartbeat refresh, it
// never extends the lease and never leaves process identity behind.
func releaseNotificationWatcherLease(path string, rec *notificationWatcherRecord, token string, now time.Time) error {
	return flock.WithLock(path+".lock", func() error {
		current, err := readNotificationWatcherRecord(path)
		if err != nil {
			return err
		}
		if current.OwnerToken != token {
			return fmt.Errorf("notification watcher lease lost to %s", current.OwnerToken)
		}
		now = now.UTC()
		current.PID = 0
		current.OwnerToken = ""
		current.Expected = false
		current.Health = "inactive"
		current.LastError = ""
		current.HeartbeatAt = now
		current.LeaseExpiresAt = now
		current.UpdatedAt = now
		*rec = current
		return writeNotificationWatcherRecord(path, current)
	})
}
