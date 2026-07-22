package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/liveidentity"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/procinfo"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

var resolveRuntimeLiveIdentityNow = resolveVerifiedLiveIdentity

// liveIdentityScope is the only production input accepted by the authoritative
// resolver. Callers cannot provide declared, launch, or observed layers.
type liveIdentityScope struct {
	Project             string
	Profile             string
	Session             string
	Handle              string
	AllowAdmittedStaged bool
}

type managedLiveLaunch struct {
	Record   launch.Record
	AgentDir string
	Root     string
	Member   team.Member
}

// unmanagedLiveActorError marks a resolved AMQ actor that is outside the
// configured roster. Runtime identity authority does not exist for that actor,
// so compatibility callers may continue without manufacturing a managed
// identity. A mismatch to another configured actor remains a hard error.
type unmanagedLiveActorError struct {
	Handle string
}

func (e unmanagedLiveActorError) Error() string {
	return fmt.Sprintf("resolved AMQ actor %s is outside the configured managed roster", e.Handle)
}

type preparedLiveActor struct {
	Project, Profile, Session, Handle string
	Generation, Digest                string
	Role, Binary, Model               string
}

type observedLiveActor struct {
	Identity       liveidentity.Observed
	WakePID        int
	WakeRecordID   string
	WakeRecordHash string
}

type liveIdentityResolverDeps struct {
	ReadLaunch      func(liveIdentityScope) (managedLiveLaunch, error)
	ResolvePrepared func(liveIdentityScope, managedLiveLaunch) (preparedLiveActor, error)
	Observe         func(liveIdentityScope, managedLiveLaunch, duplicateLaunchProbe, func() (func(int) []int, error)) (observedLiveActor, error)
	Probe           duplicateLaunchProbe
	ChildrenIndex   func() (func(int) []int, error)
}

// resolveVerifiedLiveIdentity is the single production composition point for
// runtime authority. Every gate must call this function or one of the narrow
// phase wrappers below.
func resolveVerifiedLiveIdentity(scope liveIdentityScope) (liveidentity.Result, error) {
	return resolveVerifiedLiveIdentityWithDeps(scope, productionLiveIdentityResolverDeps())
}

// verifyLiveIdentityAuthorizer is the staged-topology preflight. The scope is
// the already-live authorizing actor from the staged claim, never the unborn
// target.
func verifyLiveIdentityAuthorizer(project, profile, session, handle string) (liveidentity.Result, error) {
	return verifyLiveIdentityAuthorizerWithDeps(liveIdentityScope{Project: project, Profile: profile, Session: session, Handle: handle}, productionLiveIdentityResolverDeps())
}

func verifyLiveIdentityAuthorizerWithDeps(scope liveIdentityScope, deps liveIdentityResolverDeps) (liveidentity.Result, error) {
	return resolveVerifiedLiveIdentityWithDeps(scope, deps)
}

// verifyLiveIdentityTarget is the post-launch/post-prompt gate for the newly
// created target actor.
func verifyLiveIdentityTarget(project, profile, session, handle string) (liveidentity.Result, error) {
	return verifyLiveIdentityTargetWithDeps(liveIdentityScope{Project: project, Profile: profile, Session: session, Handle: handle, AllowAdmittedStaged: true}, productionLiveIdentityResolverDeps())
}

func verifyLiveIdentityTargetWithDeps(scope liveIdentityScope, deps liveIdentityResolverDeps) (liveidentity.Result, error) {
	return resolveVerifiedLiveIdentityWithDeps(scope, deps)
}

// VerifyTerminalActorLiveIdentity is the common terminal mutation preflight.
// It is exported so every terminal controller uses the same authority boundary.
func VerifyTerminalActorLiveIdentity(project, profile, session, handle string) (liveidentity.Result, error) {
	return resolveVerifiedLiveIdentity(liveIdentityScope{Project: project, Profile: profile, Session: session, Handle: handle})
}

func verifyTerminalActorLiveIdentity(project, profile, session, handle string) (liveidentity.Result, error) {
	return VerifyTerminalActorLiveIdentity(project, profile, session, handle)
}

func launchRecordClaimsPreparedIdentity(rec launch.Record) bool {
	// Any prepared tuple field opts the record into fail-closed verification;
	// the resolver rejects partial tuples. BootstrapExpectation alone is not a
	// prepared marker because ordinary managed launches also require bootstrap.
	return strings.TrimSpace(rec.PreparedRunGeneration) != "" || strings.TrimSpace(rec.PreparedRunDigest) != "" ||
		strings.TrimSpace(rec.PreparedRunLaunchAttempt) != ""
}

// verifyRuntimeActionWithRecord gates current managed runtimes while retaining
// an explicit compatibility path for legacy direct records that predate the
// prepared-generation identity contract. Partial modern identity is never
// downgraded to legacy: any modern marker makes full verification mandatory.
func verifyRuntimeActionWithRecord(action, project, profile, session, handle string, rec launch.Record) (liveidentity.Result, bool, error) {
	if !launchRecordClaimsPreparedIdentity(rec) {
		return liveidentity.Result{}, false, nil
	}
	if strings.TrimSpace(rec.PreparedRunGeneration) == "" || strings.TrimSpace(rec.PreparedRunDigest) == "" || strings.TrimSpace(rec.PreparedRunLaunchAttempt) == "" {
		result, baseErr := failedLiveIdentityResult(fmt.Errorf("prepared identity tuple is incomplete"))
		return result, true, fmt.Errorf("%s refused: verified live identity mismatch: %w", action, baseErr)
	}
	result, err := resolveRuntimeLiveIdentityNow(liveIdentityScope{Project: project, Profile: profile, Session: session, Handle: handle})
	if err != nil {
		return result, true, fmt.Errorf("%s refused: verified live identity mismatch: %w", action, err)
	}
	return result, true, nil
}

func verifyRuntimeActionByHandle(action, project, profile, session, handle string) (liveidentity.Result, bool, error) {
	managed, err := readManagedLiveLaunch(liveIdentityScope{Project: project, Profile: profile, Session: session, Handle: handle})
	if err != nil {
		var unmanaged unmanagedLiveActorError
		if errors.As(err, &unmanaged) {
			return liveidentity.Result{}, false, nil
		}
		if errors.Is(err, os.ErrNotExist) {
			return liveidentity.Result{}, false, nil
		}
		// A sender outside the configured managed roster has no amq-squad
		// runtime identity to promote. Existing dispatch authorization remains
		// responsible for that explicitly unmanaged compatibility boundary.
		if strings.Contains(err.Error(), "has no exact handle") {
			return liveidentity.Result{}, false, nil
		}
		return liveidentity.Result{}, true, fmt.Errorf("%s refused: resolve managed live identity: %w; recovery: %s", action, err, liveidentity.RecoveryAction)
	}
	return verifyRuntimeActionWithRecord(action, project, profile, session, handle, managed.Record)
}

func resolveVerifiedLiveIdentityWithDeps(scope liveIdentityScope, deps liveIdentityResolverDeps) (liveidentity.Result, error) {
	project, err := liveidentity.CanonicalProject(scope.Project)
	if err != nil {
		return failedLiveIdentityResult(fmt.Errorf("canonical actor project: %w", err))
	}
	scope.Project = project
	scope.Profile = squadnamespace.NormalizeProfile(scope.Profile)
	scope.Session, scope.Handle = strings.TrimSpace(scope.Session), strings.TrimSpace(scope.Handle)
	if err := team.ValidateProfileName(scope.Profile); err != nil {
		return failedLiveIdentityResult(fmt.Errorf("actor profile: %w", err))
	}
	if err := team.ValidateSessionName(scope.Session); err != nil {
		return failedLiveIdentityResult(fmt.Errorf("actor session: %w", err))
	}
	if err := team.ValidateHandle(scope.Handle); err != nil {
		return failedLiveIdentityResult(fmt.Errorf("actor handle: %w", err))
	}
	if deps.ReadLaunch == nil || deps.ResolvePrepared == nil || deps.Observe == nil || deps.ChildrenIndex == nil || deps.Probe.PIDAlive == nil || deps.Probe.ProcessMatch == nil || deps.Probe.Now == nil {
		return failedLiveIdentityResult(fmt.Errorf("runtime identity resolver dependencies are incomplete"))
	}
	managed, err := deps.ReadLaunch(scope)
	if err != nil {
		return failedLiveIdentityResult(fmt.Errorf("managed launch record: %w", err))
	}
	if err := validateLiveIdentityTerminalProjection(managed.Record); err != nil {
		return failedLiveIdentityResult(err)
	}
	prepared, err := deps.ResolvePrepared(scope, managed)
	if err != nil {
		return failedLiveIdentityResult(fmt.Errorf("accepted prepared actor: %w", err))
	}
	observed, err := deps.Observe(scope, managed, deps.Probe, deps.ChildrenIndex)
	if err != nil {
		return failedLiveIdentityResult(fmt.Errorf("live actor observation: %w", err))
	}
	rec := managed.Record
	if rec.BootstrapExpectation == nil || strings.TrimSpace(rec.BootstrapExpectation.LaunchID) == "" {
		return failedLiveIdentityResult(fmt.Errorf("managed launch record has no exact launch id"))
	}
	key := liveidentity.Key{Project: project, Profile: scope.Profile, Session: scope.Session, Handle: scope.Handle,
		PreparedGeneration: prepared.Generation, PreparedDigest: prepared.Digest, LaunchID: rec.BootstrapExpectation.LaunchID}
	terminal := liveIdentityTerminal(rec)
	wakePolicy, wakeMode, wakeTarget := liveidentity.WakeRequired, strings.TrimSpace(rec.WakeInjectMode), liveIdentityWakeTarget(terminal)
	if strings.TrimSpace(rec.NoWakeReason) != "" {
		wakePolicy, wakeMode, wakeTarget = liveidentity.WakeDisabled, liveidentity.WakeDisabled, ""
	}
	declared := liveidentity.Declared{Key: key, Role: prepared.Role, Binary: prepared.Binary, Model: prepared.Model,
		WakePolicy: wakePolicy, WakeMode: wakeMode, WakeTarget: wakeTarget, Terminal: terminal}
	launchLayer := liveidentity.LaunchRecord{Key: key, Role: rec.Role, Binary: normalizedAgentBinary(rec.Binary), Model: rec.Model,
		PID: rec.AgentPID, WakePID: observed.WakePID, WakePolicy: wakePolicy, WakeMode: wakeMode, WakeTarget: wakeTarget,
		WakeRecordID: observed.WakeRecordID, WakeRecordDigest: observed.WakeRecordHash, Terminal: terminal}
	result := liveidentity.Verify(declared, launchLayer, observed.Identity)
	if result.Verified == nil {
		return result, fmt.Errorf("live identity verification failed: %s; recovery: %s", strings.Join(result.Problems, "; "), liveidentity.RecoveryAction)
	}
	return result, nil
}

func failedLiveIdentityResult(err error) (liveidentity.Result, error) {
	result := liveidentity.Result{SchemaVersion: liveidentity.SchemaVersion, Problems: []string{err.Error()}, Recovery: liveidentity.RecoveryAction}
	return result, fmt.Errorf("%w; recovery: %s", err, liveidentity.RecoveryAction)
}

func productionLiveIdentityResolverDeps() liveIdentityResolverDeps {
	return liveIdentityResolverDeps{ReadLaunch: readManagedLiveLaunch, ResolvePrepared: resolvePreparedLiveActor, Observe: observeManagedLiveActor, Probe: defaultDuplicateLaunchProbe, ChildrenIndex: procinfo.ChildrenIndex}
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
		return managedLiveLaunch{}, fmt.Errorf("profile %s has no exact handle %s", scope.Profile, scope.Handle)
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

func resolvePreparedLiveActor(scope liveIdentityScope, managed managedLiveLaunch) (preparedLiveActor, error) {
	ctx, err := preparedContextForLaunchRecord(managed.Record)
	if err != nil {
		return preparedLiveActor{}, err
	}
	if ctx == nil || ctx.Manifest.Generation == "" || ctx.Digest == "" {
		return preparedLiveActor{}, fmt.Errorf("launch record is not bound to an accepted prepared generation")
	}
	project, err := liveidentity.CanonicalProject(ctx.Manifest.Project)
	if err != nil || project != scope.Project || !squadnamespace.ProfilesEqual(ctx.Manifest.Profile, scope.Profile) || ctx.Manifest.Session != scope.Session {
		return preparedLiveActor{}, fmt.Errorf("accepted prepared generation differs from canonical actor scope")
	}
	rec := managed.Record
	token := preparedRunTokenFromRecord(rec)
	if err := validatePreparedRunToken(token, ctx.Manifest, ctx.Digest); err != nil {
		return preparedLiveActor{}, err
	}
	var identity preparedRunMemberIdentity
	if candidate, ok := ctx.Manifest.Members[rec.Role]; ok && containsRole(ctx.Manifest.InitialRoster, rec.Role) {
		identity = candidate
		event, err := readPreparedRunEvent(preparedRunMemberEventPath(scope.Project, scope.Profile, scope.Session, token.Generation, rec.Role))
		if err != nil || event.Kind != preparedRunEventMember || event.Role != rec.Role || event.Handle != scope.Handle || !samePreparedRunGeneration(event.Token, token) {
			return preparedLiveActor{}, fmt.Errorf("initial actor has no exact prepared member claim")
		}
	} else if _, ok := ctx.Manifest.StagedMembers[rec.Role]; ok && containsRole(ctx.Manifest.StagedRoster, rec.Role) {
		claim, err := currentPreparedRunStagedClaim(scope.Project, scope.Profile, scope.Session, token.generationRef(), rec.Role)
		if err != nil {
			return preparedLiveActor{}, fmt.Errorf("staged actor has no authoritative current claim: %w", err)
		}
		pointer, err := readPreparedRunStagedClaimPointer(preparedRunStagedClaimActivePath(scope.Project, scope.Profile, scope.Session, token.Generation, rec.Role))
		if err != nil || pointer.ClaimID != claim.ClaimID || pointer.Handle != claim.Handle || !samePreparedRunGeneration(pointer.GenerationRef, token) {
			return preparedLiveActor{}, fmt.Errorf("staged actor current claim changed during verification")
		}
		if claim.ClaimID != strings.TrimSpace(rec.PreparedRunLaunchAttempt) || claim.Role != rec.Role || claim.Handle != scope.Handle || !samePreparedRunGeneration(claim.GenerationRef, token) {
			return preparedLiveActor{}, fmt.Errorf("staged launch record does not match exact current claim identity")
		}
		switch pointer.LifecycleState {
		case stagedClaimStateConsumed:
			// Already-live runtime actions require the durable consumed state.
		case stagedClaimStateAdmitted:
			if !scope.AllowAdmittedStaged {
				return preparedLiveActor{}, fmt.Errorf("staged claim %s is admitted but not consumed", claim.ClaimID)
			}
		default:
			return preparedLiveActor{}, fmt.Errorf("staged claim %s is %s, not active", claim.ClaimID, pointer.LifecycleState)
		}
		identity = claim.Effective
	} else {
		return preparedLiveActor{}, fmt.Errorf("actor is outside accepted initial and staged identities")
	}
	if identity.Handle != scope.Handle || identity.Role != rec.Role {
		return preparedLiveActor{}, fmt.Errorf("prepared actor role/handle mismatch")
	}
	return preparedLiveActor{Project: scope.Project, Profile: scope.Profile, Session: scope.Session, Handle: scope.Handle,
		Generation: ctx.Manifest.Generation, Digest: ctx.Digest, Role: identity.Role, Binary: identity.Binary, Model: identity.Model}, nil
}

func observeManagedLiveActor(scope liveIdentityScope, managed managedLiveLaunch, probe duplicateLaunchProbe, childrenIndex func() (func(int) []int, error)) (observedLiveActor, error) {
	rec := managed.Record
	if rec.BootstrapExpectation == nil || strings.TrimSpace(rec.BootstrapExpectation.LaunchID) == "" {
		return observedLiveActor{}, fmt.Errorf("managed launch record has no exact launch id")
	}
	if rec.AgentPID <= 0 || !probe.PIDAlive(rec.AgentPID) || !probe.ProcessMatch(rec.AgentPID, agentProcessMatcher(rec.Binary)) {
		return observedLiveActor{}, fmt.Errorf("agent PID is dead, reused, or does not match binary %s", rec.Binary)
	}
	if strings.TrimSpace(rec.Model) == "" || !probe.ProcessMatch(rec.AgentPID, func(args string) bool { return strings.Contains(args, rec.Model) }) {
		return observedLiveActor{}, fmt.Errorf("live process does not carry the recorded model identity")
	}
	terminal := liveIdentityTerminal(rec)
	observedTerminal := terminal
	switch terminal.Backend {
	case "tmux":
		if rec.Tmux == nil || strings.TrimSpace(rec.Tmux.PaneID) == "" {
			return observedLiveActor{}, fmt.Errorf("managed launch record has no exact tmux pane")
		}
		pane, ok := statusPaneInspector(rec.Tmux.PaneID)
		if !ok || !sameResolvedDir(pane.CWD, rec.CWD) || paneTitledForDifferentAgent(pane.Title, scope.Session, rec.Role) {
			return observedLiveActor{}, fmt.Errorf("recorded pane is missing, reused, or owned by another actor")
		}
		if err := verifyAgentPaneLineage(pane.PID, rec.AgentPID, childrenIndex); err != nil {
			return observedLiveActor{}, err
		}
	case "iterm2":
		if probe.ProcessTTY == nil {
			return observedLiveActor{}, fmt.Errorf("live native process TTY observation is unavailable")
		}
		observedTTY, ok := probe.ProcessTTY(rec.AgentPID)
		if !ok || strings.TrimSpace(observedTTY) == "" || observedTTY != terminal.TTY {
			return observedLiveActor{}, fmt.Errorf("live native process TTY differs from recorded terminal identity")
		}
		observedTerminal = liveidentity.Terminal{Backend: terminal.Backend, Target: terminal.Target, Session: terminal.Session,
			WindowID: terminal.WindowID, TabID: terminal.TabID, SessionID: terminal.SessionID, TTY: observedTTY}
	default:
		return observedLiveActor{}, fmt.Errorf("managed launch record has unsupported terminal backend %q", terminal.Backend)
	}
	key := liveidentity.Key{Project: scope.Project, Profile: scope.Profile, Session: scope.Session, Handle: scope.Handle,
		PreparedGeneration: rec.PreparedRunGeneration, PreparedDigest: rec.PreparedRunDigest, LaunchID: rec.BootstrapExpectation.LaunchID}
	identity := liveidentity.Observed{Key: key, PID: rec.AgentPID, Binary: normalizedAgentBinary(rec.Binary), Model: rec.Model, Terminal: observedTerminal}
	if strings.TrimSpace(rec.NoWakeReason) != "" {
		return observedLiveActor{Identity: identity}, nil
	}
	consumers, err := observeExactWakeConsumers(managed.Root, scope.Handle, liveIdentityWakeTarget(terminal), key.LaunchID, probe)
	if err != nil {
		return observedLiveActor{}, err
	}
	identity.WakeConsumers = consumers
	result := observedLiveActor{Identity: identity}
	if len(consumers) == 1 {
		result.WakePID, result.WakeRecordID, result.WakeRecordHash = consumers[0].PID, consumers[0].RecordID, consumers[0].RecordDigest
	}
	return result, nil
}

func verifyAgentPaneLineage(panePID, agentPID int, childrenIndex func() (func(int) []int, error)) error {
	if panePID <= 0 || agentPID <= 0 || childrenIndex == nil {
		return fmt.Errorf("pane/agent process lineage is incomplete")
	}
	children, err := childrenIndex()
	if err != nil || children == nil {
		if err != nil {
			return fmt.Errorf("process lineage snapshot unavailable: %w", err)
		}
		return fmt.Errorf("process lineage snapshot unavailable")
	}
	if !strictDescendant(children, panePID, agentPID) {
		return fmt.Errorf("verified agent PID %d is not a descendant of recorded pane PID %d", agentPID, panePID)
	}
	return nil
}

func observeExactWakeConsumers(root, handle, target, launchID string, probe duplicateLaunchProbe) ([]liveidentity.WakeConsumer, error) {
	agentsDir := filepath.Join(root, "agents")
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return nil, err
	}
	var consumers []liveidentity.WakeConsumer
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := wakeLockPath(filepath.Join(agentsDir, entry.Name()))
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		lock, err := readWakeLock(filepath.Dir(path))
		if err != nil || lock.PID <= 0 || !probe.PIDAlive(lock.PID) || !probe.ProcessMatch(lock.PID, wakeProcessMatcher(handle, root)) {
			continue
		}
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil {
			canonical = filepath.Clean(path)
		}
		sum := sha256.Sum256(raw)
		consumers = append(consumers, liveidentity.WakeConsumer{PID: lock.PID, Handle: handle, Target: target,
			RecordID: canonical, RecordDigest: "sha256:" + hex.EncodeToString(sum[:]), LaunchID: launchID})
	}
	return consumers, nil
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
		return fmt.Errorf("managed launch record has no exact terminal identity")
	}
	switch strings.TrimSpace(terminal.Backend) {
	case "tmux":
		if rec.Tmux == nil || strings.TrimSpace(terminal.Target) == "" || strings.TrimSpace(terminal.Session) == "" ||
			strings.TrimSpace(terminal.WindowID) == "" || strings.TrimSpace(terminal.PaneID) == "" ||
			terminal.Target != rec.Tmux.Target || terminal.Session != rec.Tmux.Session ||
			terminal.WindowID != rec.Tmux.WindowID || terminal.PaneID != rec.Tmux.PaneID {
			return fmt.Errorf("managed launch tmux and terminal target identities are incomplete or contradictory")
		}
	case "iterm2":
		if rec.Tmux != nil {
			return fmt.Errorf("managed native terminal identity has a contradictory tmux projection")
		}
		if strings.TrimSpace(terminal.Target) == "" || strings.TrimSpace(terminal.Session) == "" ||
			strings.TrimSpace(terminal.WindowID) == "" || strings.TrimSpace(terminal.TabID) == "" ||
			strings.TrimSpace(terminal.SessionID) == "" || strings.TrimSpace(terminal.TTY) == "" ||
			strings.TrimSpace(rec.AgentTTY) == "" || rec.AgentTTY != terminal.TTY || strings.TrimSpace(terminal.PaneID) != "" {
			return fmt.Errorf("managed native terminal target identity is incomplete or contradictory")
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
