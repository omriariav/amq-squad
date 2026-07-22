package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

type downStatus string

const (
	// downStatusStopped means the live, binary-matched agent PID received the
	// stop signal (SIGTERM by default, SIGKILL under --force). The on-disk
	// state (launch record, mailbox, brief) is preserved, so the session is
	// recoverable via `amq-squad resume`.
	downStatusStopped   downStatus = "stopped"
	downStatusNotLive   downStatus = "not-live"
	downStatusMaybeLive downStatus = "maybe-live"
	downStatusFailed    downStatus = "failed"
	// downStatusCleaned means the agent PID was already dead but stale
	// runtime artifacts (orphan wake process, wake.lock, active presence)
	// were reaped so the next `up` cannot collide with them.
	downStatusCleaned downStatus = "cleaned"
)

type downReport struct {
	Role     string
	Handle   string
	Binary   string
	AgentDir string
	Root     string
	PID      int
	// PaneID is the agent's recorded tmux pane id, used for the optional
	// pane-close on teardown (--close-panes). Empty when no tmux record exists.
	PaneID string
	// CWD is the member's resolved working dir, used to identity-check the pane
	// before closing it (guards against pane-id reuse).
	CWD    string
	Status downStatus
	Detail string
	Pane   PaneCleanupResult `json:"pane"`
}

// processTerminator abstracts process-termination so tests can substitute a
// fake. Production uses signalTerminator. SignalName reports the human label
// of the signal it sends ("SIGTERM"/"SIGKILL") so per-member reports stay
// honest about what was actually delivered.
type processTerminator interface {
	Terminate(pid int) error
	SignalName() string
}

type signalTerminator struct {
	sig syscall.Signal
}

type stopTerminatorFactory func(force bool) processTerminator

var runExactWakeRetire = runAMQCommand

// newSignalTerminator returns a terminator that sends SIGTERM by default, or
// SIGKILL when force is set. `stop` genuinely terminates the agent: SIGTERM
// asks it to exit, --force escalates to an unignorable SIGKILL for agents
// that swallow SIGTERM.
func newSignalTerminator(force bool) signalTerminator {
	if force {
		return signalTerminator{sig: syscall.SIGKILL}
	}
	return signalTerminator{sig: syscall.SIGTERM}
}

func (s signalTerminator) Terminate(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid %d", pid)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	sig := s.sig
	if sig == 0 {
		sig = syscall.SIGTERM
	}
	return proc.Signal(sig)
}

func (s signalTerminator) SignalName() string {
	if s.sig == syscall.SIGKILL {
		return "SIGKILL"
	}
	return "SIGTERM"
}

// signalNameOf returns the terminator's signal label, defaulting to SIGTERM
// for nil/zero terminators so report wording never reads empty.
func signalNameOf(term processTerminator) string {
	if term == nil {
		return "SIGTERM"
	}
	if name := term.SignalName(); name != "" {
		return name
	}
	return "SIGTERM"
}

// runStop is the primary teardown verb. With no flag it genuinely terminates
// the live, binary-matched agent PID with SIGTERM, reaps the wake sidecar, and
// flips presence offline. Because the agent is actually being stopped, flipping
// presence and reaping the sidecar are honest, not a status lie. The on-disk
// state (launch record, mailbox, brief) is PRESERVED, so the session is
// recoverable via `amq-squad resume`. --force escalates to SIGKILL for agents
// that ignore SIGTERM.
func runStop(args []string) error {
	return runStopWithDeps(args, func(force bool) processTerminator {
		return newSignalTerminator(force)
	}, defaultDuplicateLaunchProbe)
}

// runStopWithDeps keeps the production stop dependencies immutable while
// allowing parser-to-execution tests to supply inert process controls.
func runStopWithDeps(args []string, terminatorForForce stopTerminatorFactory, probe duplicateLaunchProbe) error {
	return runStopWithPaneDeps(args, terminatorForForce, probe, PaneCleanupDependencies{})
}

func runStopWithPaneDeps(args []string, terminatorForForce stopTerminatorFactory, probe duplicateLaunchProbe, paneDeps PaneCleanupDependencies) error {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	sessionName := fs.String("session", "", "AMQ workstream session name (default: team workstream)")
	role := fs.String("role", "", "narrow to a single configured role")
	all := fs.Bool("all", false, "target every configured member of the team")
	force := fs.Bool("force", false, "escalate to SIGKILL for agents that ignore SIGTERM")
	closePanes := fs.Bool("close-panes", false, "also close each stopped agent's tmux pane (default: keep, so final output stays readable; resume re-creates panes)")
	jsonOut := fs.Bool("json", false, "emit machine-readable stop results")
	projectFlag := fs.String("project", "", "project/team-home directory to target (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to target (default: default profile)")
	registerScopedFlagAliases(fs, projectFlag, sessionName, profileFlag)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, stopUsage())
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	if *role != "" && *all {
		return usageErrorf("--role and --all are mutually exclusive")
	}
	if *role == "" && !*all {
		return usageErrorf("stop requires a target selector: pass --role <role> or --all")
	}

	ctx, err := resolveScopedCommandContext(*projectFlag, *profileFlag, *sessionName, "", fs)
	if err != nil {
		return err
	}
	emitContextDiagnostics(ctx)
	if !team.ExistsProfile(ctx.ProjectDir, ctx.Profile) {
		return fmt.Errorf("no team configured for profile %q. Run '%s' first.", ctx.Profile, profileInitCommand(ctx.Profile))
	}
	return executeDown(downExecution{
		Verb:             "stop",
		ProjectDir:       ctx.ProjectDir,
		ExplicitProject:  flagWasSet(fs, "project"),
		RequestedSession: ctx.Session,
		ExplicitSession:  flagWasSet(fs, "session"),
		ExplicitProfile:  flagWasSet(fs, "profile"),
		Role:             *role,
		All:              *all,
		Profile:          ctx.Profile,
		// default=SIGTERM; --force=SIGKILL escalation for agents that ignore
		// SIGTERM. The PID-liveness + binary-match guards still apply, so a
		// reused/foreign PID is never signaled regardless of --force.
		Terminator: terminatorForForce(*force),
		Probe:      probe,
		Out:        os.Stdout,
		ClosePanes: *closePanes,
		JSON:       *jsonOut,
		PaneDeps:   paneDeps,
	})
}

func stopUsage() string {
	var b strings.Builder
	b.WriteString("amq-squad stop - stop configured team members (the session stays resumable)\n\n")
	b.WriteString("Usage:\n  amq-squad stop (--role R | --all) [--project DIR] [--force] [--close-panes] [--profile NAME] [--session NAME] [--json]\n\n")
	b.WriteString(`Exactly one selector is required: --role R or --all. --all targets the
configured members from this project's team.json in the resolved session
(default: the team's workstream). --project targets another team-home without
changing directories.

stop GENUINELY TERMINATES each live, binary-matched agent: it sends SIGTERM to
the launch-record PID, reaps the wake sidecar, and flips presence offline. It
only signals PIDs that verify alive AND match the expected agent binary, so a
reused PID is never touched. --force escalates to SIGKILL for agents that
ignore SIGTERM.

The on-disk state (launch record, mailbox, brief) is PRESERVED, so the session
is recoverable: bring it back with 'amq-squad resume'.

--close-panes requests fail-closed exact-identity pane cleanup after signaling.
--json emits one machine-readable result with separate agent and pane outcomes.

Exit codes: a successful stop exits 0; a mixed run (some stopped, some failed
or unconfirmed) exits 3.

Examples:
  amq-squad stop --role cto
  amq-squad stop --project ~/Code/app --all --session issue-96
  amq-squad stop --all --session issue-96
  amq-squad stop --role cto --force   # SIGKILL an agent that ignores SIGTERM
`)
	return b.String()
}

type downExecution struct {
	Verb             string
	ProjectDir       string
	ExplicitProject  bool
	RequestedSession string
	ExplicitSession  bool
	ExplicitProfile  bool
	Role             string
	All              bool
	Profile          string
	Terminator       processTerminator
	Probe            duplicateLaunchProbe
	Out              io.Writer
	// ClosePanes closes each downed agent's recorded tmux pane after teardown.
	// stop defaults this OFF (final output stays readable; --close-panes opts in).
	ClosePanes bool
	JSON       bool
	PaneDeps   PaneCleanupDependencies
}

func executeDown(d downExecution) error {
	verb := d.Verb
	if verb == "" {
		verb = "stop"
	}
	t, err := team.ReadProfile(d.ProjectDir, d.Profile)
	if err != nil {
		return fmt.Errorf("read team: %w", err)
	}
	if len(t.Members) == 0 {
		return fmt.Errorf("team has no members")
	}
	if strings.TrimSpace(d.Role) != "" {
		if err := ensureTargetIsNotOperator(t, verb, d.Role); err != nil {
			return err
		}
	}
	workstream, err := resolveTeamWorkstreamName(t, d.RequestedSession, d.ExplicitSession)
	if err != nil {
		return err
	}
	initialIdentity, err := captureNamespaceEndpointIdentity(squadnamespace.Resolve(d.ProjectDir, d.Profile, workstream), "")
	if err != nil {
		return err
	}
	admission, err := acquireNamespaceWriterAdmission(d.ProjectDir, d.Profile, workstream)
	if err != nil {
		return err
	}
	defer admission.close()
	currentTeam, err := team.ReadProfile(d.ProjectDir, d.Profile)
	if err != nil {
		return fmt.Errorf("%s refused: reread team under admission: %w", verb, err)
	}
	currentWorkstream, err := resolveTeamWorkstreamName(currentTeam, d.RequestedSession, d.ExplicitSession)
	if err != nil {
		return err
	}
	currentIdentity, err := captureNamespaceEndpointIdentity(squadnamespace.Resolve(d.ProjectDir, d.Profile, currentWorkstream), "")
	if err != nil {
		return err
	}
	if err := validateReResolvedEndpointIdentity(verb, initialIdentity, currentIdentity); err != nil {
		return err
	}
	t, workstream = currentTeam, currentWorkstream
	exactStopScope := exactStopNamespaceScope{
		Verb:            verb,
		ProjectDir:      d.ProjectDir,
		Profile:         d.Profile,
		Session:         workstream,
		Role:            d.Role,
		All:             d.All,
		ExplicitProject: d.ExplicitProject,
		ExplicitProfile: d.ExplicitProfile,
		ExplicitSession: d.ExplicitSession,
	}
	exceptionUsed, err := ensureNoNamespaceConflictForStop(exactStopScope)
	if err != nil {
		return err
	}
	targets, err := selectDownMembers(t, d.Role, d.All)
	if err != nil {
		return err
	}
	// The exact-stop namespace exception is deliberately stricter than the
	// historical stop path. Before any watcher, PID, wake, presence, or pane
	// mutation, prove that every selected launch record belongs to the requested
	// named namespace. The validation is exception-only so legacy records keep
	// their established compatibility outside this narrow recovery path.
	if exceptionUsed {
		if err := validateExactStopLaunchRecords(t, d.Profile, workstream, targets); err != nil {
			return err
		}
	}
	finalStop := d.All || noOperationalUntargetedMembers(t, d.Profile, workstream, targets, d.Probe)
	watcherStopped := false
	if finalStop && team.EffectiveOperatorNotifications(t.Operator).Enabled {
		if err := stopNotificationWatcher(t.Project, d.Profile, workstream); err != nil {
			return fmt.Errorf("stop notification watcher before final agent teardown: %w", err)
		}
		watcherStopped = true
	}

	reports := make([]downReport, 0, len(targets))
	var exceptionScope *exactStopNamespaceScope
	if exceptionUsed {
		exceptionScope = &exactStopScope
	}
	for _, m := range targets {
		reports = append(reports, terminateMember(t, d.ProjectDir, d.Profile, m, workstream, d.Terminator, d.Probe, exceptionScope, d.ClosePanes, d.PaneDeps))
	}
	renderErr := renderDownReportsScoped(d.Out, verb, d.ProjectDir, d.Profile, workstream, reports, d.JSON)
	if watcherStopped && !downReportsConfirmed(reports) {
		restartErr := reconcileNotificationWatcherStarted(t, d.Profile, workstream, "")
		if restartErr != nil {
			if renderErr != nil {
				return fmt.Errorf("%v; final stop was incomplete and notification watcher restart failed: %w", renderErr, restartErr)
			}
			return fmt.Errorf("final stop was incomplete and notification watcher restart failed: %w", restartErr)
		}
	}
	return renderErr
}

func validateExactStopLaunchRecords(t team.Team, profile, workstream string, targets []team.Member) error {
	for _, m := range targets {
		cwd := m.EffectiveCWD(t.Project)
		env, err := resolveAMQEnvForTeamProfile(cwd, profile, workstream, m.Handle)
		if err != nil {
			return fmt.Errorf("stop refused: resolve named namespace for role %q: %w", m.Role, err)
		}
		handle := m.Handle
		if env.Me != "" {
			handle = env.Me
		}
		expectedRoot := absoluteAMQRoot(cwd, env.Root)
		agentDir := filepath.Join(expectedRoot, "agents", handle)
		rec, err := launch.Read(agentDir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("stop refused: read launch record for role %q before exact named-profile teardown: %w", m.Role, err)
		}
		if err := validateExactStopLaunchRecord(rec, m, handle, profile, workstream, expectedRoot); err != nil {
			return fmt.Errorf("stop refused: launch record for role %q failed exact named-profile identity validation: %w", m.Role, err)
		}
	}
	return nil
}

func validateExactStopLaunchRecord(rec launch.Record, member team.Member, handle, profile, workstream, expectedRoot string) error {
	if !squadnamespace.ProfilesEqual(profile, rec.TeamProfile) {
		return fmt.Errorf("profile %q does not match requested profile %q", squadnamespace.NormalizeProfile(rec.TeamProfile), squadnamespace.NormalizeProfile(profile))
	}
	if strings.TrimSpace(rec.Session) != strings.TrimSpace(workstream) {
		return fmt.Errorf("session %q does not match requested session %q", rec.Session, workstream)
	}
	if !sameResolvedDir(rec.Root, expectedRoot) {
		return fmt.Errorf("root %q does not match requested root %q", rec.Root, expectedRoot)
	}
	if rec.Role != "" && !strings.EqualFold(strings.TrimSpace(rec.Role), strings.TrimSpace(member.Role)) {
		return fmt.Errorf("role %q does not match requested role %q", rec.Role, member.Role)
	}
	if rec.Handle != "" && !strings.EqualFold(strings.TrimSpace(rec.Handle), strings.TrimSpace(handle)) {
		return fmt.Errorf("handle %q does not match requested handle %q", rec.Handle, handle)
	}
	return nil
}

func downReportsConfirmed(reports []downReport) bool {
	for _, report := range reports {
		switch report.Status {
		case downStatusStopped, downStatusCleaned, downStatusNotLive:
		default:
			return false
		}
	}
	return true
}

func noOperationalUntargetedMembers(t team.Team, profile, workstream string, targeted []team.Member, probe duplicateLaunchProbe) bool {
	return shouldStopNotificationWatcherAfterDown(false, targeted, buildStatusRows(t, profile, workstream, probe))
}

func shouldStopNotificationWatcherAfterDown(all bool, targeted []team.Member, rows []statusRecord) bool {
	if all {
		return true
	}
	targetedRoles := make(map[string]bool, len(targeted))
	for _, m := range targeted {
		targetedRoles[strings.ToLower(strings.TrimSpace(m.Role))] = true
	}
	for _, row := range rows {
		if targetedRoles[strings.ToLower(strings.TrimSpace(row.Role))] {
			continue
		}
		if row.Status == statusStateLive || row.Status == statusStateWakeLive {
			return false
		}
	}
	return true
}

func selectDownMembers(t team.Team, role string, all bool) ([]team.Member, error) {
	members := orderedTeamMembers(t.Members)
	if all {
		return members, nil
	}
	role = strings.ToLower(strings.TrimSpace(role))
	for _, m := range members {
		if strings.EqualFold(m.Role, role) {
			return []team.Member{m}, nil
		}
	}
	names := make([]string, 0, len(members))
	for _, m := range members {
		names = append(names, m.Role)
	}
	return nil, fmt.Errorf("unknown role %q; team has: %s", role, strings.Join(names, ", "))
}

func terminateMember(t team.Team, projectDir, profile string, m team.Member, workstream string, term processTerminator, probe duplicateLaunchProbe, exactStopScope *exactStopNamespaceScope, closePanes bool, paneDeps PaneCleanupDependencies) downReport {
	report := downReport{Role: m.Role, Handle: m.Handle, Binary: m.Binary}
	report.Pane = paneCleanupUnavailableWithoutRecord(closePanes, "launch record unavailable")
	cwd := m.EffectiveCWD(t.Project)
	env, err := resolveAMQEnvForTeamProfile(cwd, profile, workstream, m.Handle)
	if err != nil {
		report.Status = downStatusNotLive
		report.Detail = "amq env unresolved: " + err.Error()
		return report
	}
	handle := m.Handle
	if env.Me != "" {
		handle = env.Me
	}
	report.Handle = handle
	root := absoluteAMQRoot(cwd, env.Root)
	report.Root = root
	report.AgentDir = filepath.Join(root, "agents", handle)
	rec, err := launch.Read(report.AgentDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			report.Status = downStatusNotLive
			report.Detail = "no launch record"
			return report
		}
		report.Status = downStatusFailed
		report.Detail = "read launch record: " + err.Error()
		return report
	}
	if exactStopScope != nil {
		if err := validateExactStopLaunchRecord(rec, m, handle, exactStopScope.Profile, exactStopScope.Session, root); err != nil {
			report.Status = downStatusFailed
			report.Detail = "launch record failed exact named-profile identity validation: " + err.Error()
			return report
		}
	}
	if rec.Tmux != nil {
		report.PaneID = rec.Tmux.PaneID
	}
	report.CWD = compareCWD(cwd, rec.CWD)
	report.PID = rec.AgentPID
	baseRoot := absoluteAMQRoot(cwd, env.BaseRoot)
	if strings.TrimSpace(workstream) != "" && sameResolvedDir(baseRoot, root) {
		baseRoot = filepath.Dir(root)
	}
	request := paneCleanupRequestForMember(t, projectDir, profile, workstream, m, handle, cwd, root, baseRoot, rec, closePanes, PaneCleanupAgentAttestation{})
	prepare := func(att PaneCleanupAgentAttestation) PaneCleanupPreparation {
		request.Attestation = att
		return PreparePaneCleanup(request, paneDeps)
	}
	strictWakeRoot := exactStopScope != nil
	if recordIsExternal(rec) {
		report.Pane = prepare(PaneCleanupAgentAttestation{}).Result
		report.Status = downStatusMaybeLive
		if report.PaneID != "" {
			report.Detail = fmt.Sprintf("external/adopted pane %s is operator-owned; stop it manually if needed", report.PaneID)
		} else {
			report.Detail = "external/adopted session is operator-owned; stop it manually if needed"
		}
		return report
	}
	if rec.AgentPID <= 0 {
		report.Pane = prepare(PaneCleanupAgentAttestation{}).Result
		// No pid was captured at launch (e.g. codex seats never recorded one).
		// There is nothing to signal, so consult presence before implying the
		// member is gone: a fresh heartbeat means it may well still be running.
		if lastSeen, fresh := presenceFreshFor(report.AgentDir, handle, probe); fresh {
			report.Status = downStatusMaybeLive
			report.Detail = fmt.Sprintf("no pid captured at launch — may still be live (fresh presence, last seen %s); cannot signal", lastSeen.UTC().Format(time.RFC3339))
			return report
		}
		cleaned := reapStaleArtifacts(report.AgentDir, handle, report.Root, strictWakeRoot, rec, term, probe)
		if cleaned.failed() {
			report.Status = downStatusFailed
			report.Detail = "no pid captured at launch; " + cleaned.summary()
			return report
		}
		if cleaned.any() {
			report.Status = downStatusCleaned
			report.Detail = "no pid captured at launch; " + cleaned.summary()
			return report
		}
		report.Status = downStatusNotLive
		report.Detail = "no pid captured at launch and presence is not fresh — treating as not live"
		return report
	}
	if !probe.PIDAlive(rec.AgentPID) {
		report.Pane = prepare(PaneCleanupAgentAttestation{PID: rec.AgentPID, Binary: rec.Binary, Live: false}).Result
		cleaned := reapStaleArtifacts(report.AgentDir, handle, report.Root, strictWakeRoot, rec, term, probe)
		if cleaned.failed() {
			report.Status = downStatusFailed
			report.Detail = fmt.Sprintf("recorded pid %d is not alive; %s", rec.AgentPID, cleaned.summary())
			return report
		}
		if cleaned.any() {
			report.Status = downStatusCleaned
			report.Detail = fmt.Sprintf("recorded pid %d is not alive; %s", rec.AgentPID, cleaned.summary())
			return report
		}
		report.Status = downStatusNotLive
		report.Detail = fmt.Sprintf("recorded pid %d is not alive", rec.AgentPID)
		return report
	}
	binary := strings.TrimSpace(rec.Binary)
	if binary == "" {
		binary = m.Binary
	}
	binaryMatch := binary != "" && probe.ProcessMatch(rec.AgentPID, agentProcessMatcher(binary))
	if !binaryMatch {
		report.Pane = prepare(PaneCleanupAgentAttestation{PID: rec.AgentPID, Binary: binary, Live: true, BinaryMatch: false}).Result
		cleaned := reapStaleArtifacts(report.AgentDir, handle, report.Root, strictWakeRoot, rec, term, probe)
		if cleaned.failed() {
			report.Status = downStatusFailed
			report.Detail = fmt.Sprintf("pid %d does not match expected binary %q (PID reuse); %s", rec.AgentPID, binary, cleaned.summary())
			return report
		}
		if cleaned.any() {
			report.Status = downStatusCleaned
			report.Detail = fmt.Sprintf("pid %d does not match expected binary %q (PID reuse); %s", rec.AgentPID, binary, cleaned.summary())
			return report
		}
		report.Status = downStatusNotLive
		report.Detail = fmt.Sprintf("pid %d does not match expected binary %q (PID reuse)", rec.AgentPID, binary)
		return report
	}
	prepared := prepare(PaneCleanupAgentAttestation{PID: rec.AgentPID, Binary: binary, Live: true, BinaryMatch: true})
	report.Pane = prepared.Result
	// Full actor identity is required only at the live-agent signal boundary.
	// Dead/no-PID cleanup remains independently bound by exact wake retirement
	// evidence and never sends an unverified agent signal.
	if _, _, err := verifyRuntimeActionWithRecord("stop", projectDir, profile, workstream, handle, rec); err != nil {
		report.Status = downStatusFailed
		report.Detail = err.Error()
		return report
	}
	sigName := signalNameOf(term)
	if err := term.Terminate(rec.AgentPID); err != nil {
		if prepared.Ready {
			report.Pane = paneCleanupPreservedAfterPreparation(prepared, "agent signal failed; pane preserved")
		}
		report.Status = downStatusFailed
		report.Detail = fmt.Sprintf("terminate pid %d: %v", rec.AgentPID, err)
		return report
	}
	if prepared.Ready {
		report.Pane = ClosePreparedPane(prepared, paneDeps)
	}
	// The agent itself just received the stop signal. Reap the wake sidecar and
	// flip presence offline up front so a racing `up` cannot collide on
	// artifacts whose owner is in the process of exiting. Because the agent is
	// genuinely being stopped, flipping presence offline is honest, not a
	// status lie. A reap failure (live matching wake we could not signal) is
	// itself a partial-stop: the agent is dying but the wake is still running
	// and the on-disk lock + presence are intentionally preserved. Surface
	// that as downStatusFailed so renderDownReports counts it correctly;
	// without this, the per-member detail says "wake survived" but the summary
	// still reads as a clean success.
	cleaned := reapStaleArtifacts(report.AgentDir, handle, report.Root, strictWakeRoot, rec, term, probe)
	if cleaned.failed() {
		report.Status = downStatusFailed
		report.Detail = fmt.Sprintf("%s sent to pid %d; %s", sigName, rec.AgentPID, cleaned.summary())
		return report
	}
	report.Status = downStatusStopped
	report.Detail = fmt.Sprintf("%s sent to pid %d", sigName, rec.AgentPID)
	if cleaned.any() {
		report.Detail += "; " + cleaned.summary()
	}
	return report
}

// reapResult records which orphan artifacts were cleaned during teardown so
// the per-member report can surface them. WakeSignalFailed is set when a
// matching live wake was found but Terminate returned an error; in that
// case the lock and presence are preserved so the next preflight still sees
// the live writer.
type reapResult struct {
	WakeKilled       int
	WakeSignalName   string
	WakeSignalFailed int
	WakeSignalError  string
	LockRemoved      bool
	PresenceFlip     bool
	WakeRetirement   string
	RetirementDetail string
}

func (r reapResult) any() bool {
	return r.WakeKilled > 0 || r.LockRemoved || r.PresenceFlip
}

func (r reapResult) failed() bool {
	return r.WakeSignalFailed > 0 || r.WakeRetirement == "amq_0_45_exact_refused" || r.WakeRetirement == "amq_0_45_exact_lock_remaining"
}

func (r reapResult) summary() string {
	parts := make([]string, 0, 3)
	if r.WakeKilled > 0 {
		sig := r.WakeSignalName
		if sig == "" {
			sig = "SIGTERM"
		}
		parts = append(parts, fmt.Sprintf("%s sent to wake pid %d", sig, r.WakeKilled))
	}
	if r.WakeRetirement != "" {
		parts = append(parts, fmt.Sprintf("wake retirement=%s (%s)", r.WakeRetirement, r.RetirementDetail))
	}
	if r.WakeSignalFailed > 0 {
		parts = append(parts, fmt.Sprintf("failed to signal wake pid %d (%s); lock and presence left intact", r.WakeSignalFailed, r.WakeSignalError))
	}
	if r.LockRemoved {
		parts = append(parts, "removed stale .wake.lock")
	}
	if r.PresenceFlip {
		parts = append(parts, "flipped presence to offline")
	}
	if len(parts) == 0 {
		return "no orphan artifacts found"
	}
	return strings.Join(parts, "; ")
}

// reapStaleArtifacts cleans up the runtime side-effects an agent leaves
// behind when its process dies but its wake sidecar and/or presence file
// survive. Returns a reapResult describing what was done so the caller can
// include it in user-visible reports. Errors during cleanup are best-effort
// and do not propagate: the goal is to unblock the next launch, not to
// guarantee atomicity.
func reapStaleArtifacts(agentDir, handle, root string, strictRoot bool, rec launch.Record, term processTerminator, probe duplicateLaunchProbe) reapResult {
	var result reapResult
	if agentDir == "" {
		return result
	}

	lockPath := wakeLockPath(agentDir)
	lockData, lockErr := os.ReadFile(lockPath)
	exactRetired := false
	if lockErr == nil && semverMeetsStableFloor(rec.AMQVersion, "0.45.0") && strings.TrimSpace(rec.WakeInjectVia) != "" {
		retired, retireErr := retireWakeWithAMQ045(rec, root, handle)
		if retireErr != nil {
			// AMQ >=0.45 can race a gracefully SIGTERMed wake that removes
			// its own lock before the retirer re-checks. Recognize that exact
			// terminal state instead of reporting a spurious refusal.
			if rec.WakePID > 0 && wakeSelfCleanedAfterRetire(lockPath, rec.WakePID, probe) {
				result.WakeKilled = rec.WakePID
				result.WakeSignalName = "amq wake retire"
				result.WakeRetirement = nativeWakeRetireSelfCleaned
				result.RetirementDetail = "wake removed its own lock after graceful termination; end state verified: pid dead, lock absent"
				result.LockRemoved = true
				exactRetired = true
			} else {
				result.WakeSignalFailed = retired.PID
				if result.WakeSignalFailed <= 0 {
					result.WakeSignalFailed = rec.WakePID
				}
				result.WakeSignalError = retireErr.Error()
				result.WakeRetirement = "amq_0_45_exact_refused"
				result.RetirementDetail = retireErr.Error()
				return result
			}
		} else {
			result.WakeKilled = retired.PID
			result.WakeSignalName = "amq wake retire"
			result.WakeRetirement = "amq_0_45_exact"
			result.RetirementDetail = retired.Reason
			exactRetired = true
			if _, statErr := os.Stat(lockPath); os.IsNotExist(statErr) {
				result.LockRemoved = true
			} else {
				result.WakeSignalFailed = retired.PID
				result.WakeSignalError = "native retirement returned success but the wake lock is still present; legacy signaling suppressed"
				result.WakeRetirement = "amq_0_45_exact_lock_remaining"
				result.RetirementDetail = result.WakeSignalError
			}
		}
	} else if lockErr == nil {
		result.WakeRetirement = "legacy_signal_fallback"
		switch {
		case strings.TrimSpace(rec.WakeInjectVia) == "":
			result.RetirementDetail = "wake is raw or has no persisted inject-via identity"
		default:
			result.RetirementDetail = "recorded AMQ " + versionOrUnknown(rec.AMQVersion) + " predates wake retire"
		}
	}
	// canRemoveLock tracks whether we've confirmed the lock is safe to
	// remove: confirmed stale (dead PID / PID-reused / corrupt), or we
	// successfully signaled the live matching wake. If a matching wake is
	// live and we FAIL to signal it, leaving the lock in place keeps the
	// next preflight honest — operators must not be told the system is
	// clean when a foreign-uid wake is still running.
	canRemoveLock := false
	if !exactRetired && lockErr == nil {
		var lock wakeLockFile
		switch jsonErr := json.Unmarshal(lockData, &lock); {
		case jsonErr != nil:
			// Corrupt lock: no PID to verify, safe to remove.
			canRemoveLock = true
		case lock.PID <= 0:
			canRemoveLock = true
		case strictRoot && strings.TrimSpace(lock.Root) != "" && !rootsMatch(lock.Root, root):
			// The exact named-profile stop exception trusts only the selected
			// root. A lock copied or poisoned with an explicit legacy/foreign
			// root is stale for this named agent dir; never inspect or signal
			// the PID it names. Empty roots retain the historical selected-root
			// fallback for older locks.
			canRemoveLock = true
		case !probe.PIDAlive(lock.PID):
			canRemoveLock = true
		default:
			expectedRoot := root
			if lock.Root != "" {
				expectedRoot = lock.Root
			}
			if !probe.ProcessMatch(lock.PID, wakeProcessMatcher(handle, expectedRoot)) {
				// PID-reuse by an unrelated process: lock is stale.
				canRemoveLock = true
			} else if termErr := term.Terminate(lock.PID); termErr == nil {
				result.WakeKilled = lock.PID
				result.WakeSignalName = signalNameOf(term)
				canRemoveLock = true
			} else {
				// Live matching wake we could not signal. Surface the
				// failure and leave both lock and presence intact so
				// preflight keeps blocking the next `up`.
				result.WakeSignalFailed = lock.PID
				result.WakeSignalError = termErr.Error()
				return result
			}
		}
		if canRemoveLock {
			if rmErr := os.Remove(lockPath); rmErr == nil {
				result.LockRemoved = true
			}
		}
	}

	presencePath := filepath.Join(agentDir, "presence.json")
	if data, err := os.ReadFile(presencePath); err == nil {
		var pres presenceFile
		if jsonErr := json.Unmarshal(data, &pres); jsonErr == nil {
			if pres.Handle != "" && pres.Handle != handle {
				// Foreign handle wrote this file; leave it alone.
				return result
			}
			// Only flip ACTIVE+FRESH presence: a stale "active" file is not
			// blocking anyone (preflight ignores anything past the freshness
			// window), so touching it is pure noise. A fresh active file with
			// a dead agent/wake is the zombie-heartbeat case #38 / #44 — that
			// is what needs flipping so the next `up` is not blocked.
			if strings.EqualFold(pres.Status, "active") && !pres.LastSeen.IsZero() && probe.Now().Sub(pres.LastSeen) <= presenceFreshness {
				pres.Status = "offline"
				pres.LastSeen = probe.Now().UTC()
				if pres.Schema == 0 {
					pres.Schema = 1
				}
				if pres.Handle == "" {
					pres.Handle = handle
				}
				if newData, marshErr := json.Marshal(pres); marshErr == nil {
					if writeErr := os.WriteFile(presencePath, newData, 0o600); writeErr == nil {
						result.PresenceFlip = true
					}
				}
			}
		}
	}

	return result
}

type nativeWakeRetireResult struct {
	Status string `json:"status"`
	Agent  string `json:"agent"`
	Root   string `json:"root"`
	PID    int    `json:"pid"`
	Reason string `json:"reason"`
}

const nativeWakeRetireSelfCleaned = "amq_0_45_exact_self_cleaned"

func retireWakeWithAMQ045(rec launch.Record, root, handle string) (nativeWakeRetireResult, error) {
	args := []string{"wake", "retire", "--root", root, "--me", handle, "--inject-via", rec.WakeInjectVia}
	for _, arg := range rec.WakeInjectArgs {
		args = append(args, "--inject-arg", arg)
	}
	args = append(args, "--json")
	out, err := runExactWakeRetire(amqCommandRequest{Dir: rec.CWD, Env: os.Environ(), Arg: args})
	var result nativeWakeRetireResult
	if jsonErr := json.Unmarshal(out, &result); jsonErr != nil {
		if err != nil {
			return result, fmt.Errorf("amq 0.45 exact wake retirement: %w", err)
		}
		return result, fmt.Errorf("amq 0.45 exact wake retirement returned invalid JSON: %w", jsonErr)
	}
	if err != nil {
		return result, fmt.Errorf("amq 0.45 exact wake retirement status %s: %w", result.Status, err)
	}
	if result.Status != "retired" || result.Agent != handle || !rootsMatch(result.Root, root) {
		return result, fmt.Errorf("amq 0.45 exact wake retirement returned mismatched result status=%s agent=%s root=%s", result.Status, result.Agent, result.Root)
	}
	if rec.WakePID <= 0 || result.PID != rec.WakePID {
		return result, fmt.Errorf("amq 0.45 exact wake retirement returned mismatched pid=%d, want persisted wake pid=%d", result.PID, rec.WakePID)
	}
	return result, nil
}

// wakeSelfCleanedAfterRetire polls briefly because the SIGTERMed wake may
// still be mid-exit when exact retirement returns refused.
func wakeSelfCleanedAfterRetire(lockPath string, wakePID int, probe duplicateLaunchProbe) bool {
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, statErr := os.Stat(lockPath)
		if os.IsNotExist(statErr) && !probe.PIDAlive(wakePID) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// presenceFreshFor reports the agent's last heartbeat and whether it is recent
// enough to treat the member as possibly still running. It mirrors the
// freshness rule status and preflight use, so down agrees with them.
func presenceFreshFor(agentDir, handle string, probe duplicateLaunchProbe) (time.Time, bool) {
	pres, err := readPresenceForEntry(agentDir)
	if err != nil {
		return time.Time{}, false
	}
	// Honor the same handle rule status and preflight use: a presence file
	// written for a different handle is not evidence this member is live.
	if pres.Handle != "" && pres.Handle != handle {
		return time.Time{}, false
	}
	if !strings.EqualFold(pres.Status, "active") || pres.LastSeen.IsZero() {
		return time.Time{}, false
	}
	return pres.LastSeen, probe.Now().Sub(pres.LastSeen) <= presenceFreshness
}

func renderDownReports(out io.Writer, verb, workstream string, reports []downReport) error {
	return renderDownReportsScoped(out, verb, "", "", workstream, reports, false)
}

type downAgentJSON struct {
	Outcome downStatus `json:"outcome"`
	Detail  string     `json:"detail"`
}

type downReportJSON struct {
	Role   string            `json:"role"`
	Handle string            `json:"handle"`
	Agent  downAgentJSON     `json:"agent"`
	Pane   PaneCleanupResult `json:"pane"`
}

type downEnvelopeData struct {
	Project string           `json:"project,omitempty"`
	Profile string           `json:"profile,omitempty"`
	Session string           `json:"session"`
	Root    string           `json:"root,omitempty"`
	Reports []downReportJSON `json:"reports"`
	Summary rmPaneSummary    `json:"pane_summary"`
}

func renderDownReportsScoped(out io.Writer, verb, project, profile, workstream string, reports []downReport, jsonOut bool) error {
	paneFailures := 0
	paneSummary := rmPaneSummary{}
	jsonReports := make([]downReportJSON, 0, len(reports))
	for i := range reports {
		if reports[i].Pane.Outcome == "" {
			reports[i].Pane = PaneCleanupResult{Outcome: PaneCleanupNotRequested, Detail: "pane cleanup was not requested"}
		}
		if reports[i].Pane.Outcome != PaneCleanupClosed && reports[i].Pane.Outcome != PaneCleanupAlreadyGone && reports[i].Pane.Outcome != PaneCleanupNotRequested {
			paneFailures++
		}
		addPaneOutcomeSummary(&paneSummary, reports[i].Pane.Outcome)
		jsonReports = append(jsonReports, downReportJSON{Role: reports[i].Role, Handle: reports[i].Handle,
			Agent: downAgentJSON{Outcome: reports[i].Status, Detail: reports[i].Detail}, Pane: reports[i].Pane})
	}
	if jsonOut {
		if err := writeJSONEnvelope(out, verb, downEnvelopeData{Project: project, Profile: profile, Session: workstream, Root: firstDownRoot(reports), Reports: jsonReports, Summary: paneSummary}); err != nil {
			return err
		}
		return downResultError(verb, reports, paneFailures)
	}
	fmt.Fprintf(out, "# amq-squad %s\n", verb)
	fmt.Fprintf(out, "# workstream: %s\n", workstream)
	if project != "" {
		fmt.Fprintf(out, "# project:    %s\n", project)
	}
	if profile != "" {
		fmt.Fprintf(out, "# profile:    %s\n", profile)
	}
	if root := firstDownRoot(reports); root != "" {
		fmt.Fprintf(out, "# AM_ROOT:    %s\n", root)
	}
	fmt.Fprintf(out, "# targets:    %d\n", len(reports))
	fmt.Fprintln(out)
	policy := outputPolicyCurrent()
	var stopped, notLive, maybeLive, failed, cleaned int
	for _, r := range reports {
		fmt.Fprintf(out, "%-12s agent=%-10s pane=%-30s %s\n", r.Role, colorStatus(policy, string(r.Status)), r.Pane.Outcome, r.Detail)
		if r.Pane.Detail != "" {
			fmt.Fprintf(out, "  pane detail: %s\n", r.Pane.Detail)
		}
		for _, mismatch := range r.Pane.Mismatches {
			fmt.Fprintf(out, "  pane mismatch %s expected=%q actual=%q\n", mismatch.Field, mismatch.Expected, mismatch.Actual)
		}
		if r.Pane.Recovery != nil {
			fmt.Fprintf(out, "  pane recovery: pane=%s session=%s window=%s\n", r.Pane.Recovery.Identity.PaneID, r.Pane.Recovery.Identity.TmuxSession, r.Pane.Recovery.Identity.WindowID)
		}
		switch r.Status {
		case downStatusStopped:
			stopped++
		case downStatusNotLive:
			notLive++
		case downStatusMaybeLive:
			maybeLive++
		case downStatusFailed:
			failed++
		case downStatusCleaned:
			cleaned++
		}
	}
	fmt.Fprintln(out)
	fmt.Fprintf(out, "# summary: %d stopped, %d cleaned, %d not-live, %d maybe-live, %d failed\n", stopped, cleaned, notLive, maybeLive, failed)
	fmt.Fprintf(out, "# pane cleanup: %d closed, %d already_gone, %d not_requested, %d preserved, %d close_failed, %d inspection_unavailable\n",
		paneSummary.Closed, paneSummary.AlreadyGone, paneSummary.NotRequested, paneSummary.Preserved, paneSummary.CloseFailed, paneSummary.InspectionUnavailable)
	if paneFailures > 0 {
		fmt.Fprintln(out, "# recovery: preserved panes require explicit operator review; use mismatch/recovery identity above before manual action")
	}
	// State-aware resumable hint: a stop preserves on-disk state, so make it
	// explicit that the session can be brought back.
	if stopped > 0 {
		fmt.Fprintf(out, "# stopped %s; bring it back with 'amq-squad resume'\n", workstream)
	}
	if maybeLive > 0 {
		fmt.Fprintln(out)
		fmt.Fprintf(out, "WARN: %d member(s) had no pid to signal but still report fresh presence — they may still be running.\n", maybeLive)
		fmt.Fprintf(out, "      %s can only signal pids it recorded at launch. Stop the underlying tmux pane / terminal\n", verb)
		fmt.Fprintln(out, "      manually, then re-run 'amq-squad status' to confirm (AM_ROOT above shows where presence lives).")
	}
	if paneFailures > 0 {
		return &PartialError{Message: fmt.Sprintf("%s: %d pane cleanup(s) were not completed", verb, paneFailures)}
	}
	if failed > 0 {
		msg := fmt.Sprintf("%s: %d of %d target(s) failed", verb, failed, len(reports))
		if maybeLive > 0 {
			msg += fmt.Sprintf("; %d may still be live (no pid to signal)", maybeLive)
		}
		// Partial (exit 3) when there is either progress (a stop signal was
		// sent, an orphan was reaped) or an unconfirmed stop (maybe-live):
		// neither is a clean success nor a wholesale breakage. Only a pure
		// all-failed run stays a plain error.
		if stopped > 0 || cleaned > 0 || maybeLive > 0 {
			return &PartialError{Message: msg}
		}
		return errors.New(msg)
	}
	// Members we could not confirm stopped must not read as a clean success:
	// surface them as partial so the exit code (3) signals "not fully down".
	if maybeLive > 0 {
		return &PartialError{Message: fmt.Sprintf("%s: %d member(s) may still be live (no pid to signal)", verb, maybeLive)}
	}
	return nil
}

func downResultError(verb string, reports []downReport, paneFailures int) error {
	var stopped, cleaned, maybeLive, failed int
	for _, report := range reports {
		switch report.Status {
		case downStatusStopped:
			stopped++
		case downStatusCleaned:
			cleaned++
		case downStatusMaybeLive:
			maybeLive++
		case downStatusFailed:
			failed++
		}
	}
	if paneFailures > 0 {
		return &PartialError{Message: fmt.Sprintf("%s: %d pane cleanup(s) were not completed", verb, paneFailures)}
	}
	if failed > 0 {
		msg := fmt.Sprintf("%s: %d of %d target(s) failed", verb, failed, len(reports))
		if stopped > 0 || cleaned > 0 || maybeLive > 0 {
			return &PartialError{Message: msg}
		}
		return errors.New(msg)
	}
	if maybeLive > 0 {
		return &PartialError{Message: fmt.Sprintf("%s: %d member(s) may still be live (no pid to signal)", verb, maybeLive)}
	}
	return nil
}

func firstDownRoot(reports []downReport) string {
	for _, r := range reports {
		if r.Root != "" {
			return r.Root
		}
	}
	return ""
}
