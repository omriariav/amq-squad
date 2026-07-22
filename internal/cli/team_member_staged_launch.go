package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/bootstrapack"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/liveidentity"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

type preparedRunStagedLaunchRequest struct {
	Project         string
	Profile         string
	Session         string
	Role            string
	ClaimID         string
	Target          string
	Layout          string
	TerminalSession string
	Timeout         time.Duration
	DryRun          bool
}

type preparedRunStagedOwnedTopology struct {
	Target   string
	PaneID   string
	WindowID string
}

type stagedLaunchData struct {
	Project      string                `json:"project"`
	Profile      string                `json:"profile"`
	Session      string                `json:"session"`
	Role         string                `json:"role"`
	Handle       string                `json:"handle"`
	ClaimID      string                `json:"claim_id"`
	Lifecycle    string                `json:"lifecycle"`
	PaneID       string                `json:"pane_id"`
	WindowID     string                `json:"window_id"`
	Verified     liveidentity.Verified `json:"verified_identity"`
	Verification liveidentity.Result   `json:"verification_result"`
}

type preparedRunStagedCleanupDependencies struct {
	Probe      duplicateLaunchProbe
	Terminator processTerminator
	Now        func() time.Time
	Sleep      func(time.Duration)
}

const (
	preparedRunStagedCleanupTimeout  = 2 * time.Second
	preparedRunStagedCleanupInterval = 25 * time.Millisecond
)

var (
	preparedRunStagedLaunchTopology   = launchPreparedRunStagedTmuxTopology
	preparedRunStagedRollbackTopology = rollbackPreparedRunStagedTmuxTopology
	preparedRunStagedRestoreFocus     = restorePreparedRunStagedTmuxFocus
	preparedRunStagedVerifyTarget     = verifyLiveIdentityTarget
	preparedRunStagedLaunchNow        = time.Now
	preparedRunStagedLaunchSleep      = time.Sleep
	preparedRunStagedTopologyBoundary = func(string) error { return nil }
	preparedRunStagedConsume          = consumePreparedRunStagedClaim
	preparedRunStagedCleanupArtifacts = cleanupPreparedRunStagedArtifacts
	preparedRunStagedCleanupDeps      = preparedRunStagedCleanupDependencies{
		Probe: defaultDuplicateLaunchProbe, Terminator: newSignalTerminator(false), Now: time.Now, Sleep: time.Sleep,
	}
)

func runTeamMemberStagedLaunch(args []string) error {
	roleID, rest, ok := peelPositional(args)
	if !ok {
		return usageErrorf("a staged role is required, e.g. 'team member launch reviewer --claim CLAIM_ID'")
	}
	roleID = strings.ToLower(strings.TrimSpace(roleID))
	if err := team.ValidateRoleID(roleID); err != nil {
		return fmt.Errorf("role: %w", err)
	}
	fs := flag.NewFlagSet("team member launch", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile (default: default profile)")
	sessionFlag := fs.String("session", "", "prepared workstream session")
	claimFlag := fs.String("claim", "", "exact active immutable staged claim ID")
	terminalFlag := fs.String("terminal", "tmux", "managed terminal backend (tmux; includes iTerm2 tmux -CC)")
	targetFlag := fs.String("target", "new-window", "owned tmux target: current-window or new-window")
	layoutFlag := fs.String("layout", "vertical", "tmux split layout for current-window")
	terminalSessionFlag := fs.String("terminal-session", "", "tmux session name when launching outside tmux")
	timeoutFlag := fs.Duration("timeout", resumeLeadReadyTimeout, "maximum wait for prompt acknowledgement and target verification")
	dryRunFlag := fs.Bool("dry-run", false, "print the exact claim-bound launch command without mutating topology")
	jsonFlag := fs.Bool("json", false, "emit the consumed staged launch as JSON")
	if err := parseFlags(fs, rest); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("unexpected argument %q", fs.Arg(0))
	}
	claimID := strings.TrimSpace(*claimFlag)
	if err := validatePreparedRunPathID("staged claim", claimID); err != nil {
		return usageErrorf("--claim must name the exact active immutable claim: %v", err)
	}
	if strings.TrimSpace(*terminalFlag) != "tmux" {
		return usageErrorf("--terminal must be tmux; iTerm2 control-mode is supported through tmux -CC")
	}
	if *targetFlag != "current-window" && *targetFlag != "new-window" {
		return usageErrorf("--target must be current-window or new-window")
	}
	if *timeoutFlag <= 0 {
		return usageErrorf("--timeout must be positive")
	}
	project, profile, err := resolveExistingTeamProfile(*projectFlag, *profileFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	tm, err := team.ReadProfile(project, profile)
	if err != nil {
		return err
	}
	session := strings.TrimSpace(*sessionFlag)
	if session == "" {
		session = inheritedSession(tm)
	}
	if session == "" {
		return usageErrorf("--session is required when the profile has no single inherited workstream")
	}
	// R1 (#508 review): admit/replace resolve the current-pane caller before
	// trusting it as an authority; launch previously did not, so any process
	// holding a claim ID (not necessarily the claim's own authorizing lead)
	// could trigger topology mutation. Mirror the same verified-current-pane
	// resolution here, then require it to be the exact actor that authorized
	// this claim - not merely any live roster member.
	authorizer, err := stagedAdmissionResolveAuthorizer(project, profile, session, tm)
	if err != nil {
		return fmt.Errorf("verify staged launch caller authority: %w", err)
	}
	if squadnamespace.NormalizeProfile(authorizer.Profile) != squadnamespace.NormalizeProfile(profile) || authorizer.Session != session {
		return fmt.Errorf("verified staged launch caller belongs to %s/%s, not %s/%s", authorizer.Profile, authorizer.Session, profile, session)
	}
	manifest, digest, err := readPreparedRunManifestSnapshot(project, profile, session)
	if err != nil {
		return fmt.Errorf("read accepted prepared generation: %w", err)
	}
	claim, err := exactActivePreparedRunStagedClaim(project, profile, session, preparedRunTokenFromSnapshot(manifest, digest), roleID, claimID)
	if err != nil {
		return err
	}
	if authorizer.Role != claim.Authorizer.Role || authorizer.Handle != claim.Authorizer.Handle {
		return fmt.Errorf("staged launch refused: caller %s/%s is not the exact actor (%s/%s) that authorized claim %s", authorizer.Role, authorizer.Handle, claim.Authorizer.Role, claim.Authorizer.Handle, claimID)
	}
	request := preparedRunStagedLaunchRequest{
		Project: project, Profile: profile, Session: session, Role: roleID, ClaimID: claimID,
		Target: *targetFlag, Layout: *layoutFlag, TerminalSession: strings.TrimSpace(*terminalSessionFlag),
		Timeout: *timeoutFlag, DryRun: *dryRunFlag,
	}
	result, err := executePreparedRunStagedLaunch(request)
	if err != nil {
		return err
	}
	if request.DryRun {
		return nil
	}
	if *jsonFlag {
		return printJSONEnvelope("staged_member_launch", result)
	}
	fmt.Fprintf(os.Stdout, "launched staged member %s/%s from claim %s in pane %s (lifecycle=%s)\n", result.Role, result.Handle, result.ClaimID, result.PaneID, result.Lifecycle)
	return nil
}

func executePreparedRunStagedLaunch(request preparedRunStagedLaunchRequest) (stagedLaunchData, error) {
	manifest, digest, err := readPreparedRunManifestSnapshot(request.Project, request.Profile, request.Session)
	if err != nil {
		return stagedLaunchData{}, fmt.Errorf("read accepted prepared generation: %w", err)
	}
	token := preparedRunTokenFromSnapshot(manifest, digest)
	claim, err := exactActivePreparedRunStagedClaim(request.Project, request.Profile, request.Session, token, request.Role, request.ClaimID)
	if err != nil {
		return stagedLaunchData{}, err
	}
	if err := preparedRunStagedTargetAbsent(request.Project, request.Profile, request.Session, claim.Handle); err != nil {
		return stagedLaunchData{}, preparedRunIdentityMismatchf("staged target %s is not absent before topology mutation: %v", claim.Handle, err)
	}
	if _, err := reverifyPreparedRunStagedAuthorizer(request.Project, request.Profile, request.Session, token, claim); err != nil {
		return stagedLaunchData{}, err
	}

	if request.DryRun {
		_, command, err := preparedRunStagedLaunchTeamAndCommand(request, manifest, token, claim)
		if err != nil {
			return stagedLaunchData{}, err
		}
		fmt.Fprintln(os.Stdout, command)
		return stagedLaunchData{Project: request.Project, Profile: request.Profile, Session: request.Session, Role: claim.Role, Handle: claim.Handle, ClaimID: claim.ClaimID, Lifecycle: stagedClaimStateAdmitted}, nil
	}

	// Single-use, crash-safe reservation closes the race between the
	// target-absence check above and topology creation below: two
	// concurrent `team member launch` calls for the same claim ID both pass
	// target-absence (TOCTOU) before either creates a pane, and only the
	// final consume was ever lock-guarded. durableCreateExclusive's
	// hardlink-based create is atomic across processes, so exactly one
	// caller wins this reservation; the loser fails closed before any
	// topology or process side effect. A failed launch abandons the claim
	// (see below), so the claim ID can never be retried - the reservation
	// artifact is kept permanently as immutable evidence, like the claim and
	// transition files it sits beside.
	if err := reservePreparedRunStagedLaunchAttempt(request.Project, request.Profile, request.Session, token, claim); err != nil {
		return stagedLaunchData{}, err
	}

	launcherPane := strings.TrimSpace(os.Getenv("TMUX_PANE"))
	owned, launchErr := preparedRunStagedLaunchTopology(request, manifest, token, claim)
	focusErr := preparedRunStagedRestoreFocus(launcherPane)
	if launchErr != nil {
		cleanupErr := preparedRunStagedCleanupArtifacts(request, token, claim, owned)
		abandonErr := abandonPreparedRunStagedClaim(request.Project, request.Profile, request.Session, token, request.Role, claim.ClaimID, "owned terminal launch failed: "+launchErr.Error())
		return stagedLaunchData{}, errors.Join(launchErr, focusErr, cleanupErr, abandonErr)
	}
	if focusErr != nil {
		rollbackErr := preparedRunStagedRollbackTopology(owned)
		cleanupErr := preparedRunStagedCleanupArtifacts(request, token, claim, owned)
		abandonErr := abandonPreparedRunStagedClaim(request.Project, request.Profile, request.Session, token, request.Role, claim.ClaimID, "launcher focus restoration failed: "+focusErr.Error())
		return stagedLaunchData{}, errors.Join(focusErr, rollbackErr, cleanupErr, abandonErr)
	}

	verification, err := waitForPreparedRunStagedTarget(request, manifest, token, claim, owned)
	if err != nil {
		rollbackErr := preparedRunStagedRollbackTopology(owned)
		cleanupErr := preparedRunStagedCleanupArtifacts(request, token, claim, owned)
		focusErr = preparedRunStagedRestoreFocus(launcherPane)
		abandonErr := abandonPreparedRunStagedClaim(request.Project, request.Profile, request.Session, token, request.Role, claim.ClaimID, "target postflight failed: "+err.Error())
		return stagedLaunchData{}, errors.Join(err, rollbackErr, cleanupErr, focusErr, abandonErr)
	}
	launchToken := token
	launchToken.LaunchAttempt = claim.ClaimID
	if err := preparedRunStagedConsume(request.Project, request.Profile, request.Session, launchToken, claim.Role, claim.Handle); err != nil {
		rollbackErr := preparedRunStagedRollbackTopology(owned)
		cleanupErr := preparedRunStagedCleanupArtifacts(request, token, claim, owned)
		focusErr = preparedRunStagedRestoreFocus(launcherPane)
		abandonErr := abandonPreparedRunStagedClaim(request.Project, request.Profile, request.Session, token, request.Role, claim.ClaimID, "verified target consumption failed: "+err.Error())
		return stagedLaunchData{}, errors.Join(err, rollbackErr, cleanupErr, focusErr, abandonErr)
	}
	return stagedLaunchData{
		Project: request.Project, Profile: request.Profile, Session: request.Session, Role: claim.Role, Handle: claim.Handle,
		ClaimID: claim.ClaimID, Lifecycle: stagedClaimStateConsumed, PaneID: owned.PaneID, WindowID: owned.WindowID,
		Verified: *verification.Verified, Verification: verification,
	}, nil
}

type preparedRunStagedLaunchReservation struct {
	SchemaVersion int              `json:"schema_version"`
	ClaimID       string           `json:"claim_id"`
	GenerationRef preparedRunToken `json:"generation_ref"`
	Role          string           `json:"role"`
	Handle        string           `json:"handle"`
	ReservedAt    time.Time        `json:"reserved_at"`
}

// reservePreparedRunStagedLaunchAttempt is the single-use serialization point
// for B1 (#508 duplicate-launch race). Exactly one caller can win the
// exclusive create for a given claim ID; every other concurrent caller for
// the same claim fails here, before any pane or process is created.
func reservePreparedRunStagedLaunchAttempt(project, profile, session string, token preparedRunToken, claim preparedRunStagedClaim) error {
	reservation := preparedRunStagedLaunchReservation{
		SchemaVersion: preparedRunStagedClaimSchema, ClaimID: claim.ClaimID, GenerationRef: token.generationRef(),
		Role: claim.Role, Handle: claim.Handle, ReservedAt: time.Now().UTC(),
	}
	data, err := marshalPreparedRunArtifact(reservation)
	if err != nil {
		return err
	}
	path := preparedRunStagedLaunchReservationPath(project, profile, session, token.Generation, claim.Role, claim.ClaimID)
	if err := durableCreateExclusive(path, data); err != nil {
		if errors.Is(err, os.ErrExist) {
			return preparedRunIdentityMismatchf("staged launch for claim %s is already reserved by a concurrent attempt", claim.ClaimID)
		}
		return err
	}
	return nil
}

func exactActivePreparedRunStagedClaim(project, profile, session string, token preparedRunToken, role, claimID string) (preparedRunStagedClaim, error) {
	claim, err := currentPreparedRunStagedClaim(project, profile, session, token, role)
	if err != nil {
		return preparedRunStagedClaim{}, err
	}
	pointer, err := readPreparedRunStagedClaimPointer(preparedRunStagedClaimActivePath(project, profile, session, token.Generation, role))
	if err != nil {
		return preparedRunStagedClaim{}, err
	}
	if claim.ClaimID != claimID || pointer.ClaimID != claimID || pointer.LifecycleState != stagedClaimStateAdmitted || pointer.Consumption != nil {
		return preparedRunStagedClaim{}, preparedRunIdentityMismatchf("staged launch requires exact active admitted claim %s", claimID)
	}
	return claim, nil
}

func preparedRunStagedLaunchTeamAndCommand(request preparedRunStagedLaunchRequest, manifest preparedRunManifest, token preparedRunToken, claim preparedRunStagedClaim) (team.Team, string, error) {
	tm, err := team.ReadProfile(request.Project, request.Profile)
	if err != nil {
		return team.Team{}, "", err
	}
	projected := projectPreparedRunStagedMember(tm, claim)
	member, err := preparedRunStagedProjectedMember(projected, claim)
	if err != nil {
		return team.Team{}, "", err
	}
	projected.Members = []team.Member{member}
	opts := teamLaunchOptions{
		Terminal: "tmux", Target: request.Target, Layout: request.Layout, Workstream: request.Session,
		TerminalSession: request.TerminalSession, SquadBin: teamSquadBin(), Profile: request.Profile,
		Trust: claim.Effective.Trust, BinaryArgs: tm.BinaryArgs, PreparedRunToken: token.generationRef(), StagedClaim: claim.ClaimID,
		PreserveLauncherFocus: true,
	}
	panes := buildTeamLaunchPanes(projected, opts)
	if len(panes) != 1 || panes[0].Role != claim.Role {
		return team.Team{}, "", fmt.Errorf("staged launch did not resolve exactly one claimed actor")
	}
	_ = manifest
	return projected, panes[0].Command, nil
}

func launchPreparedRunStagedTmuxTopology(request preparedRunStagedLaunchRequest, manifest preparedRunManifest, token preparedRunToken, claim preparedRunStagedClaim) (preparedRunStagedOwnedTopology, error) {
	tm, _, err := preparedRunStagedLaunchTeamAndCommand(request, manifest, token, claim)
	if err != nil {
		return preparedRunStagedOwnedTopology{}, err
	}
	guard := func(stage, role string) error {
		if role != claim.Role {
			return preparedRunIdentityMismatchf("staged topology guard received unexpected role %s", role)
		}
		if err := preparedRunStagedTopologyBoundary(stage); err != nil {
			return err
		}
		if err := verifyPreparedRunStagedTmuxFocus(strings.TrimSpace(os.Getenv("TMUX_PANE"))); err != nil {
			return fmt.Errorf("staged topology focus invariant before %s: %w", stage, err)
		}
		current, err := exactActivePreparedRunStagedClaim(request.Project, request.Profile, request.Session, token, claim.Role, claim.ClaimID)
		if err != nil {
			return fmt.Errorf("staged topology guard before %s: %w", stage, err)
		}
		if _, err := reverifyPreparedRunStagedAuthorizer(request.Project, request.Profile, request.Session, token, current); err != nil {
			return fmt.Errorf("staged topology guard before %s: %w", stage, err)
		}
		if stage != "command dispatch postcondition" {
			if err := preparedRunStagedTargetAbsent(request.Project, request.Profile, request.Session, current.Handle); err != nil {
				return fmt.Errorf("staged topology guard before %s: target is no longer absent: %w", stage, err)
			}
		}
		return nil
	}
	opts := teamLaunchOptions{
		Terminal: "tmux", Target: request.Target, Layout: request.Layout, Workstream: request.Session,
		TerminalSession: request.TerminalSession, SquadBin: teamSquadBin(), Profile: request.Profile,
		Trust: claim.Effective.Trust, BinaryArgs: tm.BinaryArgs, PreparedRunToken: token.generationRef(),
		PreparedRunGuard: guard, StagedClaim: claim.ClaimID,
		PreserveLauncherFocus: true,
	}
	backend := tmuxTeamLaunchBackend{}
	if err := backend.Validate(opts); err != nil {
		return preparedRunStagedOwnedTopology{}, err
	}
	result, err := backend.LaunchWithResult(tm, opts)
	if err != nil {
		return preparedRunStagedOwnedTopology{}, err
	}
	if len(result.Panes) != 1 || result.Panes[0].Role != claim.Role {
		return preparedRunStagedOwnedTopology{}, fmt.Errorf("staged terminal launch returned non-exact topology ownership")
	}
	return preparedRunStagedOwnedTopology{Target: request.Target, PaneID: result.Panes[0].PaneID, WindowID: result.Panes[0].WindowID}, nil
}

func verifyPreparedRunStagedTmuxFocus(launcherPane string) error {
	launcherPane = strings.TrimSpace(launcherPane)
	if launcherPane == "" {
		return nil
	}
	if _, err := exactTmuxPaneID(launcherPane); err != nil {
		return err
	}
	out, err := tmuxOutputCommand("tmux", "display-message", "-p", "-t", launcherPane, "#{pane_active}\t#{window_active}")
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) != "1\t1" {
		return fmt.Errorf("launcher pane/window is no longer active: %q", strings.TrimSpace(out))
	}
	return nil
}

func rollbackPreparedRunStagedTmuxTopology(owned preparedRunStagedOwnedTopology) error {
	if strings.TrimSpace(owned.PaneID) == "" {
		return nil
	}
	if owned.Target == "new-window" {
		return tmuxRunCommand("tmux", "kill-window", "-t", owned.PaneID)
	}
	return tmuxRunCommand("tmux", "kill-pane", "-t", owned.PaneID)
}

func cleanupPreparedRunStagedArtifacts(request preparedRunStagedLaunchRequest, token preparedRunToken, claim preparedRunStagedClaim, owned preparedRunStagedOwnedTopology) error {
	deps := preparedRunStagedCleanupDeps
	if deps.Probe.PIDAlive == nil || deps.Probe.ProcessMatch == nil || deps.Terminator == nil || deps.Now == nil || deps.Sleep == nil {
		return fmt.Errorf("staged rollback cleanup dependencies are incomplete")
	}
	env, err := resolveAMQEnvForTeamLaunchProfile(request.Project, request.Profile, request.Session, claim.Handle)
	if err != nil {
		return err
	}
	root := absoluteAMQRoot(request.Project, env.Root)
	agentDir := filepath.Join(root, "agents", claim.Handle)
	rec, err := launch.Read(agentDir)
	if os.IsNotExist(err) {
		for _, path := range []string{wakeLockPath(agentDir), bootstrapack.Path(agentDir)} {
			if _, statErr := os.Stat(path); statErr == nil {
				return preparedRunIdentityMismatchf("staged runtime evidence %s exists without an exact launch record; retained as a retry blocker", filepath.Base(path))
			} else if !os.IsNotExist(statErr) {
				return statErr
			}
		}
		return nil
	}
	if err != nil {
		return err
	}
	if !preparedRunStagedCleanupRecordOwned(rec, request, token, claim, owned) {
		return preparedRunIdentityMismatchf("refuse to clean staged runtime artifacts that do not belong to the exact failed claim and topology")
	}
	if rec.AgentPID > 0 && deps.Probe.PIDAlive(rec.AgentPID) {
		if !deps.Probe.ProcessMatch(rec.AgentPID, agentProcessMatcher(rec.Binary)) {
			return fmt.Errorf("owned staged launch PID %d no longer matches %s; authoritative artifacts retained", rec.AgentPID, rec.Binary)
		}
		if err := deps.Terminator.Terminate(rec.AgentPID); err != nil {
			return fmt.Errorf("terminate owned staged launch PID %d: %w; authoritative artifacts retained", rec.AgentPID, err)
		}
		if err := waitForPreparedRunStagedAgentDeath(agentDir, request, token, claim, owned, rec.AgentPID, rec.Binary, deps); err != nil {
			return err
		}
	}
	wakePath := wakeLockPath(agentDir)
	wakeData, wakeErr := os.ReadFile(wakePath)
	if wakeErr != nil && !os.IsNotExist(wakeErr) {
		return fmt.Errorf("read owned staged wake evidence: %w", wakeErr)
	}
	if wakeErr == nil {
		var lock wakeLockFile
		if err := json.Unmarshal(wakeData, &lock); err != nil {
			return fmt.Errorf("owned staged wake evidence is corrupt; authoritative artifacts retained: %w", err)
		}
		if strings.TrimSpace(lock.Root) != "" && !rootsMatch(lock.Root, root) {
			return preparedRunIdentityMismatchf("owned staged wake evidence has foreign root; authoritative artifacts retained")
		}
		if lock.PID > 0 && deps.Probe.PIDAlive(lock.PID) {
			if !deps.Probe.ProcessMatch(lock.PID, wakeProcessMatcher(claim.Handle, root)) {
				// The PID was reused by an unrelated process. The exact wake
				// consumer is absent, so its stale record can be retired.
			} else {
				if err := deps.Terminator.Terminate(lock.PID); err != nil {
					return fmt.Errorf("terminate owned staged wake PID %d: %w; authoritative artifacts retained", lock.PID, err)
				}
				if err := waitForPreparedRunStagedWakeDeath(wakePath, wakeData, lock.PID, claim.Handle, root, deps); err != nil {
					return err
				}
			}
		}
	}
	var cleanup []error
	for _, path := range []string{wakePath, bootstrapack.Path(agentDir), launch.ExistingPath(agentDir)} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			cleanup = append(cleanup, err)
		}
	}
	return errors.Join(cleanup...)
}

func preparedRunStagedCleanupRecordOwned(rec launch.Record, request preparedRunStagedLaunchRequest, token preparedRunToken, claim preparedRunStagedClaim, owned preparedRunStagedOwnedTopology) bool {
	recordToken := preparedRunTokenFromRecord(rec)
	return rec.Role == claim.Role && rec.Handle == claim.Handle && rec.PreparedRunLaunchAttempt == claim.ClaimID && samePreparedRunGeneration(recordToken, token) && rec.Tmux != nil &&
		rec.Terminal != nil && rec.Tmux.Target == request.Target && rec.Terminal.Target == request.Target &&
		(owned.PaneID == "" || (rec.Tmux.PaneID == owned.PaneID && rec.Tmux.WindowID == owned.WindowID && rec.Terminal.PaneID == owned.PaneID && rec.Terminal.WindowID == owned.WindowID))
}

func waitForPreparedRunStagedAgentDeath(agentDir string, request preparedRunStagedLaunchRequest, token preparedRunToken, claim preparedRunStagedClaim, owned preparedRunStagedOwnedTopology, pid int, binary string, deps preparedRunStagedCleanupDependencies) error {
	deadline := deps.Now().Add(preparedRunStagedCleanupTimeout)
	for {
		current, err := launch.Read(agentDir)
		if err != nil || current.AgentPID != pid || !preparedRunStagedCleanupRecordOwned(current, request, token, claim, owned) {
			return preparedRunIdentityMismatchf("owned staged launch evidence changed while waiting for PID %d death; authoritative artifacts retained", pid)
		}
		if !deps.Probe.PIDAlive(pid) {
			return nil
		}
		if !deps.Probe.ProcessMatch(pid, agentProcessMatcher(binary)) {
			return fmt.Errorf("owned staged launch PID %d changed identity before observed death; authoritative artifacts retained", pid)
		}
		if !deps.Now().Before(deadline) {
			return fmt.Errorf("owned staged launch PID %d remained alive after %s; authoritative artifacts retained", pid, deps.Terminator.SignalName())
		}
		deps.Sleep(preparedRunStagedCleanupInterval)
	}
}

func waitForPreparedRunStagedWakeDeath(path string, expected []byte, pid int, handle, root string, deps preparedRunStagedCleanupDependencies) error {
	deadline := deps.Now().Add(preparedRunStagedCleanupTimeout)
	for {
		current, err := os.ReadFile(path)
		if err != nil || string(current) != string(expected) {
			return preparedRunIdentityMismatchf("owned staged wake evidence changed while waiting for PID %d death; authoritative artifacts retained", pid)
		}
		if !deps.Probe.PIDAlive(pid) {
			return nil
		}
		if !deps.Probe.ProcessMatch(pid, wakeProcessMatcher(handle, root)) {
			return fmt.Errorf("owned staged wake PID %d changed identity before observed death; authoritative artifacts retained", pid)
		}
		if !deps.Now().Before(deadline) {
			return fmt.Errorf("owned staged wake PID %d remained alive after %s; authoritative artifacts retained", pid, deps.Terminator.SignalName())
		}
		deps.Sleep(preparedRunStagedCleanupInterval)
	}
}

func restorePreparedRunStagedTmuxFocus(launcherPane string) error {
	launcherPane = strings.TrimSpace(launcherPane)
	if launcherPane == "" {
		return nil
	}
	if _, err := exactTmuxPaneID(launcherPane); err != nil {
		return err
	}
	if err := verifyPreparedRunStagedTmuxFocus(launcherPane); err == nil {
		return nil
	}
	if err := tmuxRunCommand("tmux", "select-window", "-t", launcherPane); err != nil {
		return err
	}
	return tmuxRunCommand("tmux", "select-pane", "-t", launcherPane)
}

func waitForPreparedRunStagedTarget(request preparedRunStagedLaunchRequest, manifest preparedRunManifest, token preparedRunToken, claim preparedRunStagedClaim, owned preparedRunStagedOwnedTopology) (liveidentity.Result, error) {
	deadline := preparedRunStagedLaunchNow().Add(request.Timeout)
	last := "target launch record and bootstrap acknowledgement are not yet available"
	for {
		result, detail, ready := inspectPreparedRunStagedTarget(request, manifest, token, claim, owned)
		if ready {
			return result, nil
		}
		if detail != "" {
			last = detail
		}
		if !preparedRunStagedLaunchNow().Before(deadline) {
			return liveidentity.Result{}, fmt.Errorf("staged target did not pass prompt and identity postflight: %s", last)
		}
		preparedRunStagedLaunchSleep(resumeExecLaunchVerifyInterval)
	}
}

func inspectPreparedRunStagedTarget(request preparedRunStagedLaunchRequest, manifest preparedRunManifest, token preparedRunToken, claim preparedRunStagedClaim, owned preparedRunStagedOwnedTopology) (liveidentity.Result, string, bool) {
	if request.Target != owned.Target {
		return liveidentity.Result{}, "parent-owned topology target differs from the exact staged request", false
	}
	env, err := resolveAMQEnvForTeamLaunchProfile(request.Project, request.Profile, request.Session, claim.Handle)
	if err != nil {
		return liveidentity.Result{}, err.Error(), false
	}
	agentDir := filepath.Join(absoluteAMQRoot(request.Project, env.Root), "agents", claim.Handle)
	rec, err := launch.Read(agentDir)
	if err != nil {
		return liveidentity.Result{}, err.Error(), false
	}
	recordToken := preparedRunTokenFromRecord(rec)
	if rec.Role != claim.Role || rec.Handle != claim.Handle || rec.PreparedRunLaunchAttempt != claim.ClaimID || !samePreparedRunGeneration(recordToken, token) {
		return liveidentity.Result{}, "managed target launch record is not bound to the exact staged claim", false
	}
	if rec.Tmux == nil || rec.Tmux.PaneID != owned.PaneID || rec.Tmux.WindowID != owned.WindowID || rec.Tmux.Target != request.Target || rec.Terminal == nil || rec.Terminal.Target != request.Target {
		return liveidentity.Result{}, "managed target launch record does not own the exact parent-created topology", false
	}
	bootstrap := bootstrapack.Evaluate(rec.BootstrapExpectation, bootstrapack.Identity{
		Handle: rec.Handle, Role: rec.Role, Profile: rec.TeamProfile, Session: rec.Session, Root: rec.Root,
	}, agentDir, preparedRunStagedLaunchNow())
	if !bootstrap.Required || bootstrap.State != "verified" {
		return liveidentity.Result{}, fmt.Sprintf("bootstrap acknowledgement %s: %s", bootstrap.State, bootstrap.Detail), false
	}
	result, err := preparedRunStagedVerifyTarget(request.Project, request.Profile, request.Session, claim.Handle)
	if err != nil || result.Verified == nil {
		if err == nil {
			err = fmt.Errorf("runtime resolver returned no verified target")
		}
		return result, err.Error(), false
	}
	verified := result.Verified
	if verified.Key.PreparedGeneration != token.Generation || verified.Key.PreparedDigest != token.ManifestDigest || verified.Key.LaunchID != rec.BootstrapExpectation.LaunchID ||
		verified.Role != claim.Role || verified.Binary != claim.Effective.Binary || verified.Model != claim.Effective.Model || verified.PID != rec.AgentPID ||
		verified.Terminal.Backend != "tmux" || verified.Terminal.Target != request.Target || verified.Terminal.WindowID != owned.WindowID || verified.Terminal.PaneID != owned.PaneID {
		return result, "verified target differs from the exact claim, launch record, or owned topology", false
	}
	_ = manifest
	return result, "", true
}
