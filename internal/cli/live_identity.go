package cli

// Legacy PreparedRun launch-record fields remain readable for schema
// compatibility, but runtime classification deliberately treats them as opaque.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/liveidentity"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

type liveIdentityScope struct {
	Project string
	Profile string
	Session string
	Handle  string
}

type managedLiveLaunch struct {
	Record   launch.Record
	AgentDir string
	Root     string
	Member   team.Member
}

// wakeRecordBinding is the persisted identity of an ordinary AMQ wake lock.
// It is independent of the removed prepared-run identity resolver.
type wakeRecordBinding struct {
	PID          int
	RecordID     string
	RecordDigest string
}

var runWakeCheckForBinding = runAMQCommand

type wakeCheckBindingResult struct {
	Schema     int    `json:"schema"`
	Agent      string `json:"agent"`
	Root       string `json:"root"`
	LiveWake   bool   `json:"live_wake"`
	WakeStatus string `json:"wake_status"`
	WakePID    int    `json:"wake_pid"`
}

type unmanagedLiveActorError struct {
	Handle string
}

func (e unmanagedLiveActorError) Error() string {
	return fmt.Sprintf("resolved AMQ actor %s is outside the configured managed roster", e.Handle)
}

// VerifyTerminalActorLiveIdentity classifies the canonical launch record and
// its observed process/pane directly. Legacy PreparedRun fields remain opaque.
func VerifyTerminalActorLiveIdentity(project, profile, session, handle string) (liveidentity.Result, error) {
	managed, err := readManagedLiveLaunch(liveIdentityScope{Project: project, Profile: profile, Session: session, Handle: handle})
	if err != nil {
		return liveidentity.Result{}, err
	}
	return verifyLaunchRecordRuntime(managed.Record)
}

func verifyTerminalActorLiveIdentity(project, profile, session, handle string) (liveidentity.Result, error) {
	return VerifyTerminalActorLiveIdentity(project, profile, session, handle)
}

func verifyRuntimeActionWithRecord(action, project, profile, session, handle string, rec launch.Record, injected ...duplicateLaunchProbe) (liveidentity.Result, bool, error) {
	probe := defaultDuplicateLaunchProbe
	if len(injected) > 0 {
		probe = injected[0]
	}
	result, err := verifyLaunchRecordRuntimeWithProbe(rec, probe)
	if err != nil {
		return result, true, fmt.Errorf("%s refused: recorded runtime identity is not live: %w", action, err)
	}
	return result, true, nil
}

func verifyRuntimeActionByHandle(action, project, profile, session, handle string) (liveidentity.Result, bool, error) {
	managed, err := readManagedLiveLaunch(liveIdentityScope{Project: project, Profile: profile, Session: session, Handle: handle})
	if err != nil {
		var unmanaged unmanagedLiveActorError
		if errors.As(err, &unmanaged) || errors.Is(err, os.ErrNotExist) {
			return liveidentity.Result{}, false, nil
		}
		return liveidentity.Result{}, true, fmt.Errorf("%s refused: resolve managed launch record: %w", action, err)
	}
	return verifyRuntimeActionWithRecord(action, project, profile, session, handle, managed.Record)
}

func verifyLaunchRecordRuntime(rec launch.Record) (liveidentity.Result, error) {
	return verifyLaunchRecordRuntimeWithProbe(rec, defaultDuplicateLaunchProbe)
}

func verifyLaunchRecordRuntimeWithProbe(rec launch.Record, probe duplicateLaunchProbe) (liveidentity.Result, error) {
	paneID := ""
	if rec.Tmux != nil {
		paneID = strings.TrimSpace(rec.Tmux.PaneID)
	}
	identity := classifyLaunchRuntimeIdentity(
		rec, rec.Binary, paneID,
		launchRuntimeProbeFromDuplicate(probe),
	)
	result := liveidentity.Result{SchemaVersion: liveidentity.SchemaVersion}
	if !identity.Live {
		result.Problems = []string{"recorded PID and pane are not live under the canonical runtime classifier"}
		return result, errors.New(result.Problems[0])
	}
	result.Verified = &liveidentity.Verified{
		Key:  liveidentity.Key{Project: rec.TeamHome, Profile: rec.TeamProfile, Session: rec.Session, Handle: rec.Handle},
		Role: rec.Role, Binary: normalizedAgentBinary(rec.Binary), Model: rec.Model,
		PID: rec.AgentPID, WakePID: rec.WakePID, WakeMode: rec.WakeInjectMode,
		WakeRecordID: rec.WakeRecordID, WakeRecordDigest: rec.WakeRecordDigest,
		Terminal: liveIdentityTerminal(rec),
	}
	return result, nil
}

func readManagedLiveLaunch(scope liveIdentityScope) (managedLiveLaunch, error) {
	tm, err := team.ReadProfile(scope.Project, scope.Profile)
	if err != nil {
		return managedLiveLaunch{}, err
	}
	var member team.Member
	for _, candidate := range tm.Members {
		if memberHandle(candidate) != scope.Handle {
			continue
		}
		if member.Role != "" {
			return managedLiveLaunch{}, fmt.Errorf("handle %s resolves to multiple profile members", scope.Handle)
		}
		member = candidate
	}
	if member.Role == "" {
		return managedLiveLaunch{}, unmanagedLiveActorError{Handle: scope.Handle}
	}
	cwd := member.EffectiveCWD(tm.Project)
	env, err := resolveAMQEnvForTeamProfile(cwd, scope.Profile, scope.Session, scope.Handle)
	if err != nil {
		return managedLiveLaunch{}, err
	}
	if env.Me != "" && env.Me != scope.Handle {
		if _, configured := teamMemberByHandleOrRole(tm, env.Me); !configured {
			return managedLiveLaunch{}, unmanagedLiveActorError{Handle: env.Me}
		}
		return managedLiveLaunch{}, fmt.Errorf("AMQ resolved handle %s, want %s", env.Me, scope.Handle)
	}
	root := absoluteAMQRoot(cwd, env.Root)
	agentDir := filepath.Join(root, "agents", scope.Handle)
	rec, err := launch.Read(agentDir)
	if err != nil {
		return managedLiveLaunch{}, err
	}
	if rec.Handle != scope.Handle || !squadnamespace.ProfilesEqual(rec.TeamProfile, scope.Profile) || rec.Session != scope.Session {
		return managedLiveLaunch{}, fmt.Errorf("launch record namespace/handle differs from canonical actor scope")
	}
	return managedLiveLaunch{Record: rec, AgentDir: agentDir, Root: root, Member: member}, nil
}

var errIncompleteLaunchRecord = errors.New("incomplete launch record")

func incompleteLaunchRecordError() error {
	return fmt.Errorf("%w: it carries no exact launch id, "+
		"so live identity cannot be verified either way. The launch did not stamp its identity; "+
		"re-launch the member, or repair the record. This is not an identity conflict", errIncompleteLaunchRecord)
}

func readWakeRecordBinding(agentDir string) (wakeRecordBinding, wakeLockFile, error) {
	path := wakeLockPath(agentDir)
	raw, err := os.ReadFile(path)
	if err != nil {
		return wakeRecordBinding{}, wakeLockFile{}, err
	}
	lock, err := decodeWakeLockFile(raw)
	if err != nil {
		return wakeRecordBinding{}, wakeLockFile{}, err
	}
	recordID, err := filepath.EvalSymlinks(path)
	if err != nil {
		recordID, err = filepath.Abs(filepath.Clean(path))
		if err != nil {
			return wakeRecordBinding{}, wakeLockFile{}, err
		}
	}
	sum := sha256.Sum256(raw)
	return wakeRecordBinding{PID: lock.PID, RecordID: filepath.Clean(recordID), RecordDigest: "sha256:" + hex.EncodeToString(sum[:])}, lock, nil
}

func verifiedWakeRecordBinding(agentDir, root, handle string, probe duplicateLaunchProbe) (wakeRecordBinding, error) {
	binding, lock, err := readWakeRecordBinding(agentDir)
	if err != nil {
		return wakeRecordBinding{}, err
	}
	if binding.PID <= 0 || !probe.PIDAlive(binding.PID) || !probe.ProcessMatch(binding.PID, wakeProcessMatcher(handle, root)) {
		return wakeRecordBinding{}, fmt.Errorf("wake lock PID is dead, reused, or does not match %s at %s", handle, root)
	}
	if strings.TrimSpace(lock.Root) != "" && !sameResolvedDir(lock.Root, root) {
		return wakeRecordBinding{}, fmt.Errorf("wake lock root %s differs from launch root %s", lock.Root, root)
	}
	if wakeLockHasStateBinding(lock) {
		out, checkErr := runWakeCheckForBinding(amqCommandRequest{
			Dir: filepath.Dir(root),
			Env: os.Environ(),
			Arg: []string{"wake", "check", "--root", root, "--me", handle, "--json", "--json-schema", "1"},
		})
		if checkErr != nil {
			return wakeRecordBinding{}, fmt.Errorf("authoritative amq wake check failed for state-bound lock: %w", checkErr)
		}
		var checked wakeCheckBindingResult
		if err := json.Unmarshal(out, &checked); err != nil {
			return wakeRecordBinding{}, fmt.Errorf("authoritative amq wake check returned invalid JSON: %w", err)
		}
		if checked.Schema != 1 || !checked.LiveWake || checked.WakeStatus != "live" || checked.Agent != handle || !rootsMatch(checked.Root, root) || checked.WakePID != binding.PID {
			return wakeRecordBinding{}, fmt.Errorf("authoritative amq wake check did not verify exact live lock: schema=%d status=%s agent=%s root=%s pid=%d", checked.Schema, checked.WakeStatus, checked.Agent, checked.Root, checked.WakePID)
		}
	}
	return binding, nil
}

func bindLaunchWakeRecord(agentDir, root, handle string, agentPID int, probe duplicateLaunchProbe) (wakeRecordBinding, error) {
	if agentPID <= 0 {
		return wakeRecordBinding{}, fmt.Errorf("launch wake binding requires the exact agent PID")
	}
	var bound wakeRecordBinding
	err := launch.WithRecordLock(agentDir, func() error {
		rec, err := launch.Read(agentDir)
		if err != nil {
			return err
		}
		if rec.AgentPID != agentPID || rec.Handle != handle || !sameResolvedDir(rec.Root, root) {
			return fmt.Errorf("launch record changed before wake binding")
		}
		if strings.TrimSpace(rec.NoWakeReason) != "" {
			return fmt.Errorf("launch record explicitly disables wake binding")
		}
		binding, err := verifiedWakeRecordBinding(agentDir, root, handle, probe)
		if err != nil {
			return err
		}
		if rec.WakePID != 0 && rec.WakePID != binding.PID {
			return fmt.Errorf("persisted wake PID %d conflicts with observed PID %d", rec.WakePID, binding.PID)
		}
		if (rec.WakeRecordID == "") != (rec.WakeRecordDigest == "") {
			return fmt.Errorf("persisted wake record identity is partial")
		}
		if rec.WakeRecordID != "" && (rec.WakeRecordID != binding.RecordID || rec.WakeRecordDigest != binding.RecordDigest) {
			return fmt.Errorf("persisted wake record identity conflicts with the live lock")
		}
		rec.WakePID, rec.WakeRecordID, rec.WakeRecordDigest = binding.PID, binding.RecordID, binding.RecordDigest
		if err := launch.WriteUnderRecordLock(agentDir, rec); err != nil {
			return err
		}
		bound = binding
		return nil
	})
	return bound, err
}

func liveIdentityTerminal(rec launch.Record) liveidentity.Terminal {
	if rec.Terminal != nil {
		return liveidentity.Terminal{Backend: rec.Terminal.Backend, Target: rec.Terminal.Target, Session: rec.Terminal.Session, WindowID: rec.Terminal.WindowID,
			PaneID: rec.Terminal.PaneID, TabID: rec.Terminal.TabID, SessionID: rec.Terminal.SessionID, TTY: rec.Terminal.TTY}
	}
	if rec.Tmux != nil {
		return liveidentity.Terminal{Backend: "tmux", Target: rec.Tmux.Target, Session: rec.Tmux.Session, WindowID: rec.Tmux.WindowID, PaneID: rec.Tmux.PaneID}
	}
	return liveidentity.Terminal{}
}

func validateLiveIdentityTerminalProjection(rec launch.Record) error {
	terminal := rec.Terminal
	if terminal == nil {
		return fmt.Errorf("%w: managed launch record has no exact terminal identity", errIncompleteLaunchRecord)
	}
	switch strings.TrimSpace(terminal.Backend) {
	case "tmux":
		// ABSENCE and CONTRADICTION are different failures and must not share a message.
		// This returned one non-sentinel error for both, and it runs BEFORE the "no exact
		// tmux pane" check, so that sentinel was unreachable and the family member still
		// rendered as a mismatch (#575 round 3).
		if rec.Tmux == nil || strings.TrimSpace(terminal.Target) == "" || strings.TrimSpace(terminal.Session) == "" ||
			strings.TrimSpace(terminal.WindowID) == "" || strings.TrimSpace(terminal.PaneID) == "" {
			return fmt.Errorf("%w: managed launch tmux and terminal target identities are not fully recorded", errIncompleteLaunchRecord)
		}
		if terminal.Target != rec.Tmux.Target || terminal.Session != rec.Tmux.Session ||
			terminal.WindowID != rec.Tmux.WindowID || terminal.PaneID != rec.Tmux.PaneID {
			return fmt.Errorf("managed launch tmux and terminal target identities contradict each other")
		}
	case "iterm2":
		if rec.Tmux != nil {
			return fmt.Errorf("managed native terminal identity has a contradictory tmux projection")
		}
		if strings.TrimSpace(terminal.Target) == "" || strings.TrimSpace(terminal.Session) == "" ||
			strings.TrimSpace(terminal.WindowID) == "" || strings.TrimSpace(terminal.TabID) == "" ||
			strings.TrimSpace(terminal.SessionID) == "" || strings.TrimSpace(terminal.TTY) == "" ||
			strings.TrimSpace(rec.AgentTTY) == "" {
			return fmt.Errorf("%w: managed native terminal target identity is not fully recorded", errIncompleteLaunchRecord)
		}
		if rec.AgentTTY != terminal.TTY || strings.TrimSpace(terminal.PaneID) != "" {
			return fmt.Errorf("managed native terminal target identity contradicts the record")
		}
	default:
		return fmt.Errorf("managed launch record has unsupported terminal backend %q", terminal.Backend)
	}
	return nil
}

func liveIdentityWakeTarget(terminal liveidentity.Terminal) string {
	if terminal.PaneID != "" {
		return terminal.PaneID
	}
	return terminal.SessionID
}
