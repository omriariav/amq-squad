package cli

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/drafter"
	"github.com/omriariav/amq-squad/v2/internal/flock"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/launchintent"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

// simpleStartCheckpoint is the crash-injection boundary shared by the
// additive simple launcher and the tmux backend. It is deliberately process
// local: production leaves AfterCheckpoint nil and tests inject a sentinel
// failure without an environment-variable abort channel.
type simpleStartCheckpoint string

const (
	simpleStartCheckpointNamespaceCreation simpleStartCheckpoint = "namespace_creation"
	simpleStartCheckpointPaneCreation      simpleStartCheckpoint = "pane_creation"
	simpleStartCheckpointChildDispatch     simpleStartCheckpoint = "child_dispatch"
	simpleStartCheckpointLaunchRecordWrite simpleStartCheckpoint = "launch_record_write"
)

type simpleStartCheckpointError struct {
	Checkpoint simpleStartCheckpoint
	Err        error
}

func (e *simpleStartCheckpointError) Error() string {
	return fmt.Sprintf("simple start checkpoint %s: %v", e.Checkpoint, e.Err)
}

func (e *simpleStartCheckpointError) Unwrap() error { return e.Err }

func callSimpleStartCheckpoint(after func(simpleStartCheckpoint) error, checkpoint simpleStartCheckpoint) error {
	if after == nil {
		return nil
	}
	if err := after(checkpoint); err != nil {
		return &simpleStartCheckpointError{Checkpoint: checkpoint, Err: err}
	}
	return nil
}

type simpleStartDependencies struct {
	AfterCheckpoint func(simpleStartCheckpoint) error
	ResolveDrafter  resolveCLIDrafterFunc
	RunDrafter      cliDrafterRunner
	LookPath        func(string) (string, error)
	ResolveAMQEnv   func(string, string, string, string) (amqEnv, error)
	DuplicateProbe  duplicateLaunchProbe
	RuntimeProbe    launchRuntimeProbe
	Launch          func(team.Team, teamLaunchOptions) (teamLaunchResult, error)
	StartWatcher    func(team.Team, string, string, string) error
	DeliverGoal     func(simpleStartPlan, string) error
	ListPanes       tmuxpane.PaneLister
	Sleep           func(time.Duration)
}

// simpleStartLaunch resolves the backend through the same selection seam
// executeTeamLaunch uses (gh#755/gh#757), instead of a bare Terminal-keyed
// lookup, so start defaults to launchapi on tmux exactly like team
// launch/up already do, and honors an explicit --launch-via legacy opt-out.
// opts.LaunchVia is already populated by parseSimpleStartRequest's
// buildLiveLaunchOptions call; before this it was parsed and silently
// ignored for backend selection.
func simpleStartLaunch(t team.Team, opts teamLaunchOptions) (teamLaunchResult, error) {
	backend, err := resolveTeamLaunchBackend(opts)
	if err != nil {
		return teamLaunchResult{}, err
	}
	resultBackend, ok := backend.(teamLaunchResultBackend)
	if !ok {
		return teamLaunchResult{}, fmt.Errorf("terminal %q does not report exact pane identities", opts.Terminal)
	}
	return resultBackend.LaunchWithResult(t, opts)
}

func defaultSimpleStartDependencies() simpleStartDependencies {
	return simpleStartDependencies{
		ResolveDrafter: resolveCLIDrafter,
		RunDrafter:     drafter.Run,
		LookPath:       exec.LookPath,
		ResolveAMQEnv:  resolveAMQEnvForSimpleStart,
		DuplicateProbe: defaultDuplicateLaunchProbe,
		RuntimeProbe:   launchRuntimeProbeFromDuplicate(defaultDuplicateLaunchProbe),
		Launch:         simpleStartLaunch,
		StartWatcher:   reconcileSessionNotifierStarted,
		DeliverGoal:    deliverSimpleStartGoal,
		ListPanes:      statusPaneLister,
		Sleep:          time.Sleep,
	}
}

func normalizeSimpleStartDependencies(deps simpleStartDependencies) simpleStartDependencies {
	defaults := defaultSimpleStartDependencies()
	if deps.ResolveDrafter == nil {
		deps.ResolveDrafter = defaults.ResolveDrafter
	}
	if deps.RunDrafter == nil {
		deps.RunDrafter = defaults.RunDrafter
	}
	if deps.LookPath == nil {
		deps.LookPath = defaults.LookPath
	}
	if deps.ResolveAMQEnv == nil {
		deps.ResolveAMQEnv = defaults.ResolveAMQEnv
	}
	if deps.DuplicateProbe.PIDAlive == nil {
		deps.DuplicateProbe.PIDAlive = defaults.DuplicateProbe.PIDAlive
	}
	if deps.DuplicateProbe.ProcessMatch == nil {
		deps.DuplicateProbe.ProcessMatch = defaults.DuplicateProbe.ProcessMatch
	}
	if deps.DuplicateProbe.ProcessTTY == nil {
		deps.DuplicateProbe.ProcessTTY = defaults.DuplicateProbe.ProcessTTY
	}
	if deps.DuplicateProbe.ProcessStartTime == nil {
		deps.DuplicateProbe.ProcessStartTime = defaults.DuplicateProbe.ProcessStartTime
	}
	if deps.DuplicateProbe.Now == nil {
		deps.DuplicateProbe.Now = defaults.DuplicateProbe.Now
	}
	if deps.RuntimeProbe.PIDAlive == nil {
		deps.RuntimeProbe.PIDAlive = defaults.RuntimeProbe.PIDAlive
	}
	if deps.RuntimeProbe.ProcessMatch == nil {
		deps.RuntimeProbe.ProcessMatch = defaults.RuntimeProbe.ProcessMatch
	}
	if deps.RuntimeProbe.ProcessTTY == nil {
		deps.RuntimeProbe.ProcessTTY = defaults.RuntimeProbe.ProcessTTY
	}
	if deps.RuntimeProbe.ProcessStartTime == nil {
		deps.RuntimeProbe.ProcessStartTime = defaults.RuntimeProbe.ProcessStartTime
	}
	if deps.RuntimeProbe.PaneTitle == nil {
		deps.RuntimeProbe.PaneTitle = defaults.RuntimeProbe.PaneTitle
	}
	if deps.RuntimeProbe.PaneTTY == nil {
		deps.RuntimeProbe.PaneTTY = defaults.RuntimeProbe.PaneTTY
	}
	if deps.Launch == nil {
		deps.Launch = defaults.Launch
	}
	if deps.StartWatcher == nil {
		deps.StartWatcher = defaults.StartWatcher
	}
	if deps.DeliverGoal == nil {
		deps.DeliverGoal = defaults.DeliverGoal
	}
	if deps.ListPanes == nil {
		deps.ListPanes = defaults.ListPanes
	}
	if deps.Sleep == nil {
		deps.Sleep = defaults.Sleep
	}
	return deps
}

type simpleStartRolePlan struct {
	Member team.Member
	State  string
	Detail string
	Record *launch.Record
}

type simpleStartPlan struct {
	Project        string
	Profile        string
	Session        string
	Root           string
	BriefPath      string
	BriefBytes     []byte
	BriefDraft     *simpleStartBriefDraft
	RulesBytes     []byte
	RoleBriefBytes map[string][]byte
	Team           team.Team
	Roles          []simpleStartRolePlan
	// AllRoles is the full, unfiltered reconciliation -- every session
	// member's classification, before RoleFilter narrows Roles down to the
	// requested subset. A caller that needs to know the CURRENT state of a
	// role outside the filter (e.g. resume --exec's lead-liveness gate,
	// which must see the lead's real state even when --role excludes the
	// lead itself) must consult this, not Roles.
	AllRoles      []simpleStartRolePlan
	Removed       []simpleStartRolePlan
	Preflights    []agentLaunchPreflight
	AllPanes      []teamLaunchPane
	SpawnTeam     team.Team
	LaunchOptions teamLaunchOptions
	Goal          string
}

type simpleStartRequest struct {
	Project         string
	Profile         string
	Session         string
	SessionExplicit bool
	TrustExplicit   bool
	Yes             bool
	Goal            string
	Options         teamLaunchOptions
	ReviewedBrief   *simpleStartBriefDraft
	// LaunchapiPath is true when resolveTeamLaunchBackend resolved this
	// request to the launchapi backend (gh#755/gh#757), as opposed to the
	// legacy backend (--launch-via legacy). The digest-gated --apply/
	// --decision flow and its deprecation redirects only ever apply on this
	// path; the legacy path stays byte-identical to pre-gh#757 behavior.
	LaunchapiPath bool
	// DeprecatedFlagNotices are printed once, early, when a flag that has
	// no effect on the launchapi path was explicitly supplied (gh#757).
	DeprecatedFlagNotices []string
	// RoleFilter restricts which reconciled roles this run actually spawns
	// and reports, on top of the existing session filter (gh#758/t11 slice
	// B). start's own CLI path always leaves this nil -- gh#757 just spent
	// a whole task shrinking start's flag surface, and growing it back for
	// a resume-specific need would reverse that without a start-user
	// demand (cto ruling, task/t11). resume --exec is the only caller that
	// populates it. Applied AFTER reconcileSimpleStartRoles, not by
	// pre-filtering team.Members, so an unselected live role is never
	// misclassified as "removed from roster" -- it is simply not part of
	// this invocation's spawn set.
	RoleFilter []string
}

type simpleStartConflictError struct {
	Class  string
	Detail string
}

func (e *simpleStartConflictError) Error() string {
	return fmt.Sprintf("start conflict %s: %s", e.Class, e.Detail)
}

func runStart(args []string) error {
	return runStartWithDependencies(args, defaultSimpleStartDependencies(), os.Stdin, os.Stdout)
}

// simpleStartRefuseLegacyMintedRestoreOnLaunchapi is gh#757's fail-closed
// refusal (cto decision, task/t8): the launchapi path has no mechanism
// today to resume a conversation minted by the legacy composer -- and
// every conversation ID amq-squad has on record for a role start manages
// IS legacy-minted, since the launchapi backend never writes
// launch.Record at all (confirmed on task/t7: only the legacy `agent up`
// path does). launchapi's own ResumePolicy=resume natively resumes a
// conversation it minted itself, but that mechanism is orthogonal to, and
// cannot honor, an ID amq-squad recorded from a different backend. Rather
// than silently falling back to the legacy backend (which would quietly
// re-route the most common command through the backend v2.32.0 deletes)
// or silently dropping the conversation, this refuses closed with an
// explicit remedy naming both real options.
func simpleStartRefuseLegacyMintedRestoreOnLaunchapi(launchapiPath bool, plan simpleStartPlan) error {
	if !launchapiPath {
		return nil
	}
	for _, m := range plan.SpawnTeam.Members {
		conversation := strings.TrimSpace(plan.LaunchOptions.RestoreConversations[m.Role])
		if conversation == "" {
			continue
		}
		return fmt.Errorf("start refused: role %s has a recorded conversation %q; it was minted by the legacy backend (the launchapi path never writes launch records) and the launchapi path cannot resume it yet. Rerun with --launch-via legacy to resume it (supported for one release, removed in v2.32.0), or clear its launch record to accept a fresh conversation instead", m.Role, conversation)
	}
	return nil
}

func runStartWithDependencies(args []string, deps simpleStartDependencies, in io.Reader, out io.Writer) error {
	deps = normalizeSimpleStartDependencies(deps)
	req, err := parseSimpleStartRequest(args)
	if err != nil {
		return err
	}
	_, err = runSimpleStartWithRequest(req, deps, in, out)
	return err
}

// runSimpleStartWithRequest is the shared plan/confirm/lock/launch/verify
// sequence behind `start`, extracted so gh#758/t11 slice B's `resume --exec`
// fold can drive it with a programmatically-built request (RoleFilter set,
// ExpectedSubjectDigest pre-computed from its own preview probe) instead of
// re-invoking start's CLI arg parsing -- there is no precedent elsewhere in
// this codebase for one command shelling into another's args-based entry
// point, and --role has no start equivalent by design (cto ruling,
// task/t11), so this is a shared-seam fold, not an argv re-invocation.
func runSimpleStartWithRequest(req simpleStartRequest, deps simpleStartDependencies, in io.Reader, out io.Writer) (simpleStartPlan, error) {
	accepted, err := buildSimpleStartPlan(req, deps)
	if err != nil {
		return simpleStartPlan{}, err
	}
	if err := simpleStartRefuseLegacyMintedRestoreOnLaunchapi(req.LaunchapiPath, accepted); err != nil {
		return simpleStartPlan{}, err
	}
	for _, notice := range req.DeprecatedFlagNotices {
		fmt.Fprintln(out, notice)
	}
	renderSimpleStartPlan(out, accepted)
	if accepted.BriefDraft != nil && accepted.BriefDraft.Manual {
		fmt.Fprintln(out, "start stopped before mutation; complete and review the manual drafting prompt, save the brief, then run start again")
		return accepted, nil
	}
	if req.LaunchapiPath && len(accepted.SpawnTeam.Members) > 0 {
		// gh#757: the launchapi path confirms (or applies) bound to an
		// exact subject_digest rather than a generic yes/no. The digest
		// printed/checked here is only the FIRST check; deps.Launch (via
		// launchapiTeamLaunchBackend.launch) re-runs Prepare fresh under
		// the session lock below and refuses again if anything -- team,
		// brief, or which roles still need launching -- changed since.
		// (gh#758/t11 slice B: this is also where a resume --exec-supplied
		// ExpectedSubjectDigest, computed over its own --role-filtered
		// preview probe, gets its first comparison -- the re-Prepare below
		// under the session lock re-applies the same RoleFilter and
		// refuses closed if the filtered roster or its liveness changed.)
		probePrepared, _, err := (launchapiTeamLaunchBackend{}).prepare(accepted.SpawnTeam, accepted.LaunchOptions)
		if err != nil {
			return simpleStartPlan{}, fmt.Errorf("preview plan: %w", err)
		}
		printPlanResult(out, probePrepared.Result)
		digest := probePrepared.Result.SubjectDigest
		if supplied := strings.TrimSpace(req.Options.ExpectedSubjectDigest); supplied != "" {
			if supplied != digest {
				return simpleStartPlan{}, fmt.Errorf("start refused: --apply %s does not match the current plan's subject_digest %s; re-run 'amq-squad start' without --apply to see the current digest", supplied, digest)
			}
		} else if !confirmSimpleStartPrompt(out, in, fmt.Sprintf("Apply subject_digest %s? [y/N] ", digest)) {
			fmt.Fprintln(out, "start cancelled")
			return accepted, nil
		}
		req.Options.ExpectedSubjectDigest = digest
	} else if !req.Yes && !confirmSimpleStart(out, in) {
		fmt.Fprintln(out, "start cancelled")
		return accepted, nil
	}
	req.ReviewedBrief = cloneSimpleStartBriefDraft(accepted.BriefDraft)

	lockPath := simpleStartLockPath(accepted.Project, accepted.Profile, accepted.Session)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return simpleStartPlan{}, fmt.Errorf("create start lock directory: %w", err)
	}
	lock, err := flock.AcquireExclusive(lockPath)
	if err != nil {
		return simpleStartPlan{}, fmt.Errorf("acquire session launch lock: %w", err)
	}
	defer lock.Close()

	current, err := buildSimpleStartPlan(req, deps)
	if err != nil {
		return simpleStartPlan{}, err
	}
	if !sameSimpleStartInputs(accepted, current) {
		return simpleStartPlan{}, fmt.Errorf("start plan changed while awaiting approval; review and run start again")
	}
	if _, err := prepareSelectedAMQRoots(current.Preflights, simpleStartAuthorityHandles(current)); err != nil {
		return simpleStartPlan{}, fmt.Errorf("create or adopt canonical namespace: %w", err)
	}
	if err := callSimpleStartCheckpoint(deps.AfterCheckpoint, simpleStartCheckpointNamespaceCreation); err != nil {
		return simpleStartPlan{}, err
	}
	if err := ensureSimpleStartBrief(current.BriefPath, current.BriefBytes); err != nil {
		return simpleStartPlan{}, err
	}
	if len(current.SpawnTeam.Members) > 0 {
		if err := refuseRecordlessSimpleStartPanes(current, deps.ListPanes); err != nil {
			return simpleStartPlan{}, err
		}
		if err := validateSimpleStartRestoreCommands(current); err != nil {
			return simpleStartPlan{}, err
		}
		for _, preflight := range filterResolvedTeamPreflights(current.Preflights, current.SpawnTeam.Members) {
			if blocker, err := preflight.check(deps.DuplicateProbe); err != nil {
				return simpleStartPlan{}, err
			} else if blocker != nil {
				return simpleStartPlan{}, &simpleStartConflictError{Class: "duplicate_live", Detail: blocker.Error()}
			}
		}
		result, err := deps.Launch(current.SpawnTeam, current.LaunchOptions)
		if err != nil {
			return simpleStartPlan{}, err
		}
		if err := validateCompleteTeamLaunchResult(buildTeamLaunchPanes(current.SpawnTeam, current.LaunchOptions), current.LaunchOptions.Target, result); err != nil {
			return simpleStartPlan{}, err
		}
		if err := validateSimpleStartRestoreResultCommands(current, result); err != nil {
			return simpleStartPlan{}, err
		}
		if err := verifySimpleStartRecords(current, result, deps); err != nil {
			return simpleStartPlan{}, err
		}
		if err := callSimpleStartCheckpoint(deps.AfterCheckpoint, simpleStartCheckpointLaunchRecordWrite); err != nil {
			return simpleStartPlan{}, err
		}
	}

	verified, err := buildSimpleStartPlan(req, deps)
	if err != nil {
		return simpleStartPlan{}, err
	}
	for _, role := range verified.Roles {
		if role.State != "live" && role.State != "live/config-diverged" {
			return simpleStartPlan{}, fmt.Errorf("start incomplete: %s is %s (%s)", role.Member.Role, role.State, role.Detail)
		}
	}
	if deps.StartWatcher != nil {
		if err := deps.StartWatcher(verified.Team, verified.Profile, verified.Session, filepath.Dir(verified.Root)); err != nil {
			return simpleStartPlan{}, err
		}
	}
	if len(current.SpawnTeam.Members) > 0 && strings.TrimSpace(current.Goal) != "" {
		if err := deps.DeliverGoal(verified, current.Goal); err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: all agents are live, but goal delivery to the lead failed: %v\n", err)
		}
	}
	if len(current.SpawnTeam.Members) == 0 {
		fmt.Fprintf(out, "already started %s using profile %s in %s\n", current.Session, current.Profile, current.Project)
	} else {
		fmt.Fprintf(out, "started %s using profile %s in %s\n", current.Session, current.Profile, current.Project)
	}
	fmt.Fprintf(out, "AM_ROOT: %s\n", current.Root)
	return verified, nil
}

func refuseRecordlessSimpleStartPanes(plan simpleStartPlan, list tmuxpane.PaneLister) error {
	recordless := false
	for _, row := range plan.Roles {
		if row.State == "unmanaged" && row.Record == nil {
			recordless = true
			break
		}
	}
	if !recordless {
		return nil
	}
	if plan.LaunchOptions.Target == "new-session" && !tmuxSessionExists(plan.LaunchOptions.TerminalSession) {
		return nil
	}
	panes, err := list()
	if err != nil {
		return fmt.Errorf("inspect tmux panes before recordless launch: %w", err)
	}
	return classifyRecordlessSimpleStartPanes(plan, panes)
}

func classifyRecordlessSimpleStartPanes(plan simpleStartPlan, panes []tmuxpane.TmuxPane) error {
	expected := make(map[string]string)
	for _, row := range plan.Roles {
		if row.State == "unmanaged" && row.Record == nil {
			expected[paneTitleToken(plan.Session, row.Member.Role)] = row.Member.Role
		}
	}
	for _, pane := range panes {
		title := strings.TrimSpace(pane.DiscoveryToken)
		if title == "" {
			title = strings.TrimSpace(pane.Title)
		}
		role, ok := expected[title]
		if !ok {
			continue
		}
		identity := strings.TrimSpace(pane.PaneID)
		if identity == "" {
			identity = strings.Trim(strings.Join([]string{pane.Session, pane.Window, pane.Pane}, ":"), ":")
		}
		return &simpleStartConflictError{
			Class:  "unmanaged",
			Detail: fmt.Sprintf("role %s has a live launcher-stamped tmux pane %s but no launch record; refusing to create a second runtime", role, identity),
		}
	}
	return nil
}

func parseSimpleStartRequest(args []string) (simpleStartRequest, error) {
	projectArg, rest, err := peelProjectFlag(args)
	if err != nil {
		return simpleStartRequest{}, err
	}
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	profile := fs.String("profile", team.DefaultProfile, "team profile to start")
	yes := fs.Bool("yes", false, "skip the default-No launch confirmation (legacy path only; no effect on launchapi, use --apply instead)")
	goal := fs.String("goal", "", "optional goal delivered to the lead after every agent verifies live")
	fs.BoolVar(yes, "y", false, "shorthand for --yes")
	applyDigest := fs.String("apply", "", "apply this exact subject_digest from a previously printed plan (gh#757, launchapi path only), skipping interactive confirmation")
	var decisionsFlag stringListFlag
	fs.Var(&decisionsFlag, "decision", "explicit operator answer to a launchapi required action, ACTION_ID=CHOICE (repeatable; same shape as --launchapi-decision)")
	pf := registerPreviewFlags(fs)
	lf := registerLiveLaunchFlags(fs)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad start - reconcile one canonical team workstream

Usage:
	  amq-squad start [SESSION] [--project DIR] [--profile NAME] [--goal TEXT] [--yes|-y]
    [--target current-window|new-window|new-session] [--layout vertical|horizontal|tiled]
    [--trust sandboxed|approve-for-me|trusted] [--model role=model,...]

By default, start previews the complete roster and launch plan, then asks before
launching (default: No); answering No changes nothing. Pass --yes for automation.
Rerunning keeps verified live roles, respawns stopped roles, and rolls forward
partial managed launches without deleting the namespace.
When --goal is set and the workstream has no brief, the configured drafter
produces an exact validated brief that is printed before confirmation and
written only after approval. In-session fallback prints the filled prompt and
stops before mutation.

Examples:
  amq-squad start
  amq-squad start --project ~/Code/app
  amq-squad start issue-96 --goal "Ship the reviewed change"
`)
	}
	rest = allowInterspersedFlags(fs, rest)
	if err := parseFlags(fs, rest); err != nil {
		return simpleStartRequest{}, err
	}
	if fs.NArg() > 1 {
		return simpleStartRequest{}, usageErrorf("start takes at most one session positional")
	}
	if fs.NArg() == 1 {
		if flagWasSet(fs, "session") {
			return simpleStartRequest{}, usageErrorf("pass the session either positionally or with --session, not both")
		}
		*pf.session = strings.TrimSpace(fs.Arg(0))
	}
	if *pf.noBootstrap {
		return simpleStartRequest{}, usageErrorf("start owns one exact startup instruction; --no-bootstrap is not supported")
	}
	if *pf.fresh {
		return simpleStartRequest{}, usageErrorf("start reconciles existing state; --fresh is not supported")
	}
	if strings.TrimSpace(*lf.terminal) != "tmux" {
		return simpleStartRequest{}, usageErrorf("start currently requires the managed tmux backend")
	}
	if !flagWasSet(fs, "target") {
		*lf.target = "new-session"
	}
	canonicalProject := strings.TrimSpace(projectArg)
	if canonicalProject == "" {
		canonicalProject, err = os.Getwd()
		if err != nil {
			return simpleStartRequest{}, err
		}
	}
	canonicalProject = canonicalFilesystemPath(canonicalProject)
	if canonicalProject == "" {
		return simpleStartRequest{}, fmt.Errorf("resolve canonical project path")
	}
	profileValue := squadnamespace.NormalizeProfile(*profile)
	if err := team.ValidateProfileName(profileValue); err != nil {
		return simpleStartRequest{}, err
	}
	goalValue := strings.TrimSpace(*goal)
	if strings.ContainsAny(goalValue, "\x00\r\n") {
		return simpleStartRequest{}, usageErrorf("start --goal must be one line")
	}
	if len(goalValue) > 2000 {
		return simpleStartRequest{}, usageErrorf("start --goal must be at most 2000 bytes")
	}
	opts, err := buildLiveLaunchOptions(fs, pf, lf)
	if err != nil {
		return simpleStartRequest{}, err
	}
	opts.Profile = profileValue
	opts.NoBootstrap = true
	opts.SimpleStart = true
	opts.AllowExistingSession = true

	// gh#757: resolve the backend now (Terminal is pinned to "tmux" above;
	// LaunchVia was just parsed) to decide how --force-duplicate, --apply,
	// and --decision behave. The legacy backend keeps every pre-gh#757
	// behavior byte-identical; only the launchapi path gets the new
	// digest-gated flow and its deprecation redirects.
	backend, err := resolveTeamLaunchBackend(opts)
	if err != nil {
		return simpleStartRequest{}, err
	}
	launchapiPath := backend.Name() == "launchapi"

	if !launchapiPath {
		if *pf.forceDuplicate || strings.TrimSpace(*applyDigest) != "" || len(decisionsFlag) > 0 {
			return simpleStartRequest{}, usageErrorf("start reconciles existing state; --force-duplicate is not supported, and --apply/--decision only apply to the launchapi path (this session resolved to the legacy backend; pass --launch-via launchapi or omit --launch-via legacy)")
		}
	}

	var notices []string
	decisions, err := parseLaunchapiDecisions(decisionsFlag)
	if err != nil {
		return simpleStartRequest{}, err
	}
	if launchapiPath {
		if flagWasSet(fs, "yes") {
			notices = append(notices, "deprecated: --yes has no effect on the launchapi path; review the printed plan and its subject_digest, then re-run with --apply <subject_digest> (or confirm interactively)")
		}
		if flagWasSet(fs, "trust") {
			notices = append(notices, "deprecated: --trust has no effect on the launchapi path; trust is compiled per-seat from team.json, set it there instead")
		}
		if flagWasSet(fs, "launchapi-decision") {
			notices = append(notices, "deprecated: --launchapi-decision is renamed --decision (same ACTION_ID=CHOICE shape); both are honored here, but use --decision going forward")
		}
		if *pf.forceDuplicate {
			notices = append(notices, "deprecated: --force-duplicate has no effect on the launchapi path; a duplicate-conversation required action answers via --decision ACTION_ID=fresh_once like any other required action")
		}
		for actionID, choice := range decisions {
			if opts.LaunchapiDecisions == nil {
				opts.LaunchapiDecisions = make(map[string]string, len(decisions))
			}
			if existing, dup := opts.LaunchapiDecisions[actionID]; dup && existing != choice {
				return simpleStartRequest{}, usageErrorf("--decision and --launchapi-decision both specify action %q with different choices (%q vs %q)", actionID, choice, existing)
			}
			opts.LaunchapiDecisions[actionID] = choice
		}
		opts.ExpectedSubjectDigest = strings.TrimSpace(*applyDigest)
	}
	opts.ForceDuplicate = false

	return simpleStartRequest{
		Project: canonicalProject, Profile: profileValue, Session: strings.TrimSpace(*pf.session),
		SessionExplicit: strings.TrimSpace(*pf.session) != "", TrustExplicit: flagWasSet(fs, "trust"),
		Yes: *yes, Goal: goalValue, Options: opts,
		LaunchapiPath: launchapiPath, DeprecatedFlagNotices: notices,
	}, nil
}

func buildSimpleStartPlan(req simpleStartRequest, deps simpleStartDependencies) (simpleStartPlan, error) {
	t, err := team.ReadProfile(req.Project, req.Profile)
	if err != nil {
		return simpleStartPlan{}, fmt.Errorf("read team: %w", err)
	}
	if len(t.Members) == 0 {
		return simpleStartPlan{}, fmt.Errorf("team has no members")
	}
	session, err := resolveTeamWorkstreamName(t, req.Session, req.SessionExplicit)
	if err != nil {
		return simpleStartPlan{}, err
	}
	active, _ := filterMembersBySession(t.Members, session)
	if len(active) == 0 {
		return simpleStartPlan{}, fmt.Errorf("no team members are pinned to session %q", session)
	}
	t.Members = active
	t.Members, err = applyLaunchEffortOverridesCatalog(t.Members, req.Options.EffortOverrides, loadAgentCatalogAndWarn(req.Project))
	if err != nil {
		return simpleStartPlan{}, err
	}
	trustMode, err := resolveTeamTrustMode(t, req.Options.Trust, req.TrustExplicit)
	if err != nil {
		return simpleStartPlan{}, err
	}
	mergedBinaryArgs := mergeBinaryArgs(t.BinaryArgs, req.Options.BinaryArgs)
	if err := validateTrustCombination(trustMode, strings.TrimSpace(t.Trust) != "" || req.TrustExplicit, false, mergedBinaryArgs); err != nil {
		return simpleStartPlan{}, err
	}
	if err := validateMembersTrust(trustMode, strings.TrimSpace(t.Trust) != "" || req.TrustExplicit, t.Members); err != nil {
		return simpleStartPlan{}, err
	}
	if err := validateMemberOverlayPaths(t, t.Members); err != nil {
		return simpleStartPlan{}, err
	}
	memberRoles := make(map[string]bool, len(t.Members))
	for _, member := range t.Members {
		memberRoles[strings.ToLower(member.Role)] = true
	}
	if err := validateModelOverrideKeys(req.Options.ModelOverrides, memberRoles); err != nil {
		return simpleStartPlan{}, err
	}
	root := squadnamespace.AMQRoot(req.Project, req.Profile, session)
	briefPath := squadnamespace.BriefPath(req.Project, req.Profile, session)
	briefBytes, briefExists, err := readSimpleStartBriefBytes(briefPath)
	if err != nil {
		return simpleStartPlan{}, err
	}
	var briefDraft *simpleStartBriefDraft
	if briefExists && req.ReviewedBrief != nil {
		document, err := validateSimpleStartBriefDraft(string(req.ReviewedBrief.Document), session, req.Goal, t.Members)
		if err != nil {
			return simpleStartPlan{}, fmt.Errorf("validate reviewed workstream brief: %w", err)
		}
		if !bytesEqual(briefBytes, []byte(document)) {
			return simpleStartPlan{}, fmt.Errorf("brief changed after review; review %s and run start again", briefPath)
		}
		briefDraft = cloneSimpleStartBriefDraft(req.ReviewedBrief)
		briefDraft.Document = []byte(document)
	}
	if !briefExists {
		switch {
		case req.ReviewedBrief != nil:
			document, err := validateSimpleStartBriefDraft(string(req.ReviewedBrief.Document), session, req.Goal, t.Members)
			if err != nil {
				return simpleStartPlan{}, fmt.Errorf("validate reviewed workstream brief: %w", err)
			}
			briefDraft = cloneSimpleStartBriefDraft(req.ReviewedBrief)
			briefDraft.Document = []byte(document)
			briefBytes = append([]byte(nil), briefDraft.Document...)
		case strings.TrimSpace(req.Goal) != "":
			briefDraft, err = draftSimpleStartBrief(req.Project, req.Profile, session, req.Goal, t, deps)
			if err != nil {
				return simpleStartPlan{}, err
			}
			briefBytes = append([]byte(nil), briefDraft.Document...)
			if briefDraft.Manual {
				return simpleStartPlan{
					Project: req.Project, Profile: req.Profile, Session: session, Root: root,
					BriefPath: briefPath, BriefDraft: briefDraft, Team: t, Goal: req.Goal,
				}, nil
			}
		default:
			briefBytes = []byte(briefStubContent(session))
		}
	}
	if row := worktreeIsolationCheckForSession(t, req.Profile, session); row.Status == "blocked" {
		return simpleStartPlan{}, fmt.Errorf("worktree isolation blocked: %s; %s", row.Evidence, row.Fix)
	}
	if deps.LookPath == nil {
		deps.LookPath = exec.LookPath
	}
	if _, err := deps.LookPath("tmux"); err != nil {
		return simpleStartPlan{}, fmt.Errorf("managed tmux is unavailable: %w", err)
	}
	for _, member := range t.Members {
		if err := ensureLaunchTargetIsNotOperator(req.Project, req.Profile, "start", member.Role, memberHandle(member)); err != nil {
			return simpleStartPlan{}, err
		}
		if member.Launcher != "" {
			if err := ensureLauncherExecutable(member.Launcher); err != nil {
				return simpleStartPlan{}, fmt.Errorf("%s: %w", member.Role, err)
			}
			continue
		}
		if _, err := deps.LookPath(member.Binary); err != nil {
			return simpleStartPlan{}, fmt.Errorf("%s binary %q is unavailable: %w", member.Role, member.Binary, err)
		}
	}
	if req.Options.Symphony {
		if err := validateTeamSymphonyMembers(t, t.Members); err != nil {
			return simpleStartPlan{}, err
		}
	}
	if req.Options.WakeInjectVia != "" {
		if err := ensureLauncherExecutable(req.Options.WakeInjectVia); err != nil {
			return simpleStartPlan{}, fmt.Errorf("wake inject executable: %w", err)
		}
	}

	rulesPath := filepath.Join(req.Project, ".amq-squad", "team-rules.md")
	rulesBytes, err := os.ReadFile(rulesPath)
	if err != nil {
		return simpleStartPlan{}, fmt.Errorf("read team rules %s: %w", rulesPath, err)
	}
	prompts := make(map[string]string, len(t.Members))
	roleBriefBytes := make(map[string][]byte, len(t.Members))
	for _, member := range t.Members {
		instruction := "Read .amq-squad/team-rules.md and your brief at " + briefPath + "."
		prompts[member.Role] = instruction
		roleBriefBytes[member.Role] = []byte(instruction)
	}

	firstHandle := memberHandle(t.Members[0])
	env, err := deps.ResolveAMQEnv(req.Project, root, session, firstHandle)
	if err != nil {
		return simpleStartPlan{}, fmt.Errorf("resolve canonical AMQ root: %w", err)
	}
	if err := validateLaunchAMQVersion(env.AMQVersion); err != nil {
		return simpleStartPlan{}, err
	}
	preflights := make([]agentLaunchPreflight, 0, len(t.Members))
	for _, member := range orderedTeamMembers(t.Members) {
		handle := memberHandle(member)
		preflights = append(preflights, agentLaunchPreflight{
			Role: member.Role, CWD: canonicalFilesystemPath(member.EffectiveCWD(req.Project)),
			AgentDir: filepath.Join(root, "agents", handle), Handle: handle, Workstream: session,
			Root: root, BaseRoot: filepath.Dir(root), RootSource: "simple_start_canonical",
			Binary: member.Binary, AMQVersion: env.AMQVersion,
		})
	}

	opts := req.Options
	opts.Profile = req.Profile
	opts.Workstream = session
	opts.Trust = trustMode
	opts.CanonicalRoot = root
	opts.StartupPrompts = prompts
	opts.AfterCheckpoint = deps.AfterCheckpoint
	if opts.TerminalSession == "" {
		opts.TerminalSession = defaultTmuxSessionName(req.Project) + "-" + sanitizeTmuxSessionName(session)
	}
	backend, ok := teamLaunchBackends[opts.Terminal]
	if !ok {
		return simpleStartPlan{}, fmt.Errorf("unsupported terminal %q", opts.Terminal)
	}
	if err := backend.Validate(opts); err != nil {
		return simpleStartPlan{}, err
	}
	records, err := readSimpleStartRecords(req.Project, root, req.Profile, session)
	if err != nil {
		return simpleStartPlan{}, err
	}
	if err := validateSimpleStartTmuxTarget(opts, session, records, deps.RuntimeProbe); err != nil {
		return simpleStartPlan{}, err
	}
	roles, removed, err := reconcileSimpleStartRoles(t, req.Profile, session, root, records, opts, deps.RuntimeProbe)
	if err != nil {
		return simpleStartPlan{}, err
	}
	allRoles := roles
	if len(req.RoleFilter) > 0 {
		roles, err = filterSimpleStartRolesBySubset(t, roles, req.RoleFilter)
		if err != nil {
			return simpleStartPlan{}, err
		}
	}
	spawn := t
	spawn.Members = nil
	restoreConversations := make(map[string]string)
	goalPrompts := make(map[string]string)
	for _, row := range roles {
		if row.State == "stopped" || row.State == "unmanaged" {
			spawn.Members = append(spawn.Members, row.Member)
		}
		if row.State == "stopped" && row.Record != nil && strings.TrimSpace(row.Record.Conversation) != "" {
			restoreConversations[row.Member.Role] = strings.TrimSpace(row.Record.Conversation)
		}
		// gh#758/t11 slice C: "a goal survives any relaunch that mints a
		// NEW conversation" (cto's ruling, task/t11) -- every relaunched
		// seat whose prior record still names a goal gets it carried
		// forward, regardless of that binding's Mode (native_goal_blocked
		// or otherwise; a died-mid-goal seat and a cleanly-finished one are
		// treated the same here). buildIntentInput is what applies the
		// native-restore precedence (skips GoalPrompt when
		// RestoreConversations already covers this same role).
		if row.State == "stopped" && row.Record != nil && row.Record.GoalBinding != nil {
			if goal := strings.TrimSpace(row.Record.GoalBinding.Goal); goal != "" {
				goalPrompts[row.Member.Role] = launchintent.NormalizeGoalPrompt(goal)
			}
		}
	}
	opts.RestoreConversations = restoreConversations
	opts.GoalPrompts = goalPrompts
	allPanes := buildTeamLaunchPanes(t, opts)
	opts.ComposedPanes = buildTeamLaunchPanes(spawn, opts)
	return simpleStartPlan{
		Project: req.Project, Profile: req.Profile, Session: session, Root: root,
		BriefPath: briefPath, BriefBytes: briefBytes, BriefDraft: briefDraft, RulesBytes: rulesBytes, RoleBriefBytes: roleBriefBytes,
		Team: t, Roles: roles, AllRoles: allRoles, Removed: removed,
		Preflights: preflights, AllPanes: allPanes, SpawnTeam: spawn, LaunchOptions: opts,
		Goal: req.Goal,
	}, nil
}

type simpleStartRecord struct {
	AgentDir string
	Record   launch.Record
}

func readSimpleStartRecords(project, root, profile, session string) ([]simpleStartRecord, error) {
	agentsDir := filepath.Join(root, "agents")
	entries, err := os.ReadDir(agentsDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read canonical agents directory: %w", err)
	}
	var records []simpleStartRecord
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		agentDir := filepath.Join(agentsDir, entry.Name())
		if !launch.HasRecord(agentDir) {
			continue
		}
		rec, err := launch.Read(agentDir)
		if err != nil {
			return nil, &simpleStartConflictError{Class: "record_invalid", Detail: fmt.Sprintf("%s: %v", launch.ExistingPath(agentDir), err)}
		}
		if rec.Schema != launch.SchemaVersion || strings.TrimSpace(rec.Handle) == "" || strings.TrimSpace(rec.Role) == "" || strings.TrimSpace(rec.Root) == "" || strings.TrimSpace(rec.Session) == "" || strings.TrimSpace(rec.Binary) == "" {
			return nil, &simpleStartConflictError{Class: "record_invalid", Detail: fmt.Sprintf("%s lacks required authoritative coordinates", launch.ExistingPath(agentDir))}
		}
		if rec.Handle != entry.Name() || rec.Session != session || !sameResolvedDir(rec.Root, root) ||
			!sameResolvedDir(rec.TeamHome, project) || squadnamespace.NormalizeProfile(rec.TeamProfile) != squadnamespace.NormalizeProfile(profile) {
			return nil, &simpleStartConflictError{Class: "record_invalid", Detail: fmt.Sprintf("%s does not match canonical project/profile/session/root coordinates", launch.ExistingPath(agentDir))}
		}
		records = append(records, simpleStartRecord{AgentDir: agentDir, Record: rec})
	}
	return records, nil
}

// filterSimpleStartRolesBySubset narrows already-reconciled role rows to
// req.RoleFilter (gh#758/t11 slice B: resume --exec --role a,b). t is the
// full session-filtered team (unfiltered by role), used only to name the
// team's actual roles in an unknown-role error and to run the same
// operator-target guard planPrepareFiltered applies for --role. Filtering
// the already-reconciled rows -- rather than t.Members before
// reconcileSimpleStartRoles runs -- is deliberate: an unselected live role
// must never be misclassified as "removed from roster" by the removed-role
// diffing above, which compares against every configured member.
func filterSimpleStartRolesBySubset(t team.Team, roles []simpleStartRolePlan, roleFilter []string) ([]simpleStartRolePlan, error) {
	present := make(map[string]bool, len(roles))
	for _, row := range roles {
		present[strings.ToLower(row.Member.Role)] = true
	}
	wanted := make(map[string]bool, len(roleFilter))
	var unknown []string
	for _, role := range roleFilter {
		role = strings.ToLower(strings.TrimSpace(role))
		if role == "" {
			continue
		}
		if err := ensureTargetIsNotOperator(t, "resume", role); err != nil {
			return nil, err
		}
		wanted[role] = true
		if !present[role] {
			unknown = append(unknown, role)
		}
	}
	if len(unknown) > 0 {
		return nil, usageErrorf("--role: no team member(s) with role %s (team roles: %s)",
			strings.Join(unknown, ", "), strings.Join(teamRoleList(t), ", "))
	}
	out := make([]simpleStartRolePlan, 0, len(roles))
	for _, row := range roles {
		if wanted[strings.ToLower(row.Member.Role)] {
			out = append(out, row)
		}
	}
	return out, nil
}

func reconcileSimpleStartRoles(t team.Team, profile, session, root string, records []simpleStartRecord, opts teamLaunchOptions, probe launchRuntimeProbe) ([]simpleStartRolePlan, []simpleStartRolePlan, error) {
	used := make(map[int]bool)
	entries := make([]launch.Entry, 0, len(records))
	recordIndexByAgentDir := make(map[string]int, len(records))
	for i, item := range records {
		entries = append(entries, launch.Entry{AgentDir: item.AgentDir, Record: item.Record})
		recordIndexByAgentDir[filepath.Clean(item.AgentDir)] = i
	}
	desiredByHandle := make(map[string]string, len(t.Members))
	desiredByRole := make(map[string]string, len(t.Members))
	for _, member := range t.Members {
		desiredByHandle[memberHandle(member)] = strings.ToLower(member.Role)
		desiredByRole[strings.ToLower(member.Role)] = memberHandle(member)
	}
	for _, item := range records {
		handleRole, handleDesired := desiredByHandle[item.Record.Handle]
		roleHandle, roleDesired := desiredByRole[strings.ToLower(item.Record.Role)]
		if handleDesired && roleDesired && (handleRole != strings.ToLower(item.Record.Role) || roleHandle != item.Record.Handle) {
			return nil, nil, &simpleStartConflictError{Class: "record_invalid", Detail: fmt.Sprintf("record %s binds desired handle %s to desired role %s inconsistently", launch.ExistingPath(item.AgentDir), item.Record.Handle, item.Record.Role)}
		}
	}
	var rows []simpleStartRolePlan
	for _, member := range orderedTeamMembers(t.Members) {
		selection := selectSimpleStartLaunchRecord(t, profile, member, session, probe, entries)
		if len(selection.DuplicatePaths) > 0 {
			return nil, nil, &simpleStartConflictError{Class: "duplicate_live", Detail: fmt.Sprintf("role %s has %d authoritative live launch records: %s", member.Role, len(selection.DuplicatePaths), strings.Join(selection.DuplicatePaths, ", "))}
		}
		if !selection.Found {
			rows = append(rows, simpleStartRolePlan{Member: member, State: "unmanaged", Detail: "no launch record; will create"})
			continue
		}
		selected, ok := recordIndexByAgentDir[filepath.Clean(selection.Entry.AgentDir)]
		if !ok {
			return nil, nil, fmt.Errorf("selected launch record %s was not present in the start snapshot", launch.ExistingPath(selection.Entry.AgentDir))
		}
		used[selected] = true
		rec := records[selected].Record
		copyRec := rec
		paneID := ""
		if rec.Tmux != nil {
			paneID = rec.Tmux.PaneID
		}
		identity := classifyLaunchRuntimeIdentity(rec, "", paneID, probe)
		if !simpleStartRuntimeLive(rec, identity) {
			rows = append(rows, simpleStartRolePlan{Member: member, State: "stopped", Detail: "recorded process and pane are not live; will respawn", Record: &copyRec})
			continue
		}
		if simpleStartRecordDiverged(rec, member, t, profile, session, root, opts) {
			rows = append(rows, simpleStartRolePlan{Member: member, State: "live/config-diverged", Detail: "recorded live invocation differs from current config; keeping recorded runtime", Record: &copyRec})
		} else {
			rows = append(rows, simpleStartRolePlan{Member: member, State: "live", Detail: "recorded process or pane verified live; keeping", Record: &copyRec})
		}
	}
	var removed []simpleStartRolePlan
	for i, item := range records {
		if used[i] {
			continue
		}
		rec := item.Record
		member := team.Member{Role: rec.Role, Handle: rec.Handle, Binary: rec.Binary, CWD: rec.CWD}
		paneID := ""
		if rec.Tmux != nil {
			paneID = rec.Tmux.PaneID
		}
		state, detail := "stopped", "removed from roster; stopped record retained"
		live := simpleStartRuntimeLive(rec, classifyLaunchRuntimeIdentity(rec, "", paneID, probe))
		if live {
			state, detail = "live/config-diverged", "removed from roster; live recorded runtime retained"
		}
		if desiredHandle, aliasesDesiredRole := desiredByRole[strings.ToLower(rec.Role)]; aliasesDesiredRole && desiredHandle != rec.Handle {
			state = "unmanaged"
			runtime := "stopped"
			if live {
				runtime = "live"
			}
			detail = fmt.Sprintf(
				"unconfigured handle %q aliases configured role %q (expected handle %q); %s record retained: %s",
				rec.Handle, rec.Role, desiredHandle, runtime, launch.ExistingPath(item.AgentDir),
			)
		}
		copyRec := rec
		removed = append(removed, simpleStartRolePlan{Member: member, State: state, Detail: detail, Record: &copyRec})
	}
	return rows, removed, nil
}

// selectSimpleStartLaunchRecord applies the same exact profile/session/handle
// identity selection as status, but uses start's stronger managed-runtime
// postcondition: a titled tmux shell does not keep a dead agent child live.
// Explicitly external records have no managed child, so their exact recorded
// pane remains the authoritative runtime coordinate.
func selectSimpleStartLaunchRecord(t team.Team, profile string, member team.Member, session string, probe launchRuntimeProbe, entries []launch.Entry) statusLaunchSelection {
	handle := memberHandle(member)
	var candidates []launch.Entry
	for _, entry := range entries {
		rec := entry.Record
		if strings.TrimSpace(rec.Session) != strings.TrimSpace(session) || !squadnamespace.ProfilesEqual(rec.TeamProfile, profile) {
			continue
		}
		if strings.TrimSpace(rec.TeamHome) != "" && !sameResolvedDir(rec.TeamHome, t.Project) {
			continue
		}
		if strings.TrimSpace(rec.Handle) != strings.TrimSpace(handle) {
			continue
		}
		candidates = append(candidates, entry)
	}
	if len(candidates) == 0 {
		return statusLaunchSelection{}
	}
	sort.Slice(candidates, func(i, j int) bool {
		return launch.ExistingPath(candidates[i].AgentDir) < launch.ExistingPath(candidates[j].AgentDir)
	})
	var live []launch.Entry
	for _, entry := range candidates {
		paneID := ""
		if entry.Record.Tmux != nil {
			paneID = entry.Record.Tmux.PaneID
		}
		identity := classifyLaunchRuntimeIdentity(entry.Record, "", paneID, probe)
		if simpleStartRuntimeLive(entry.Record, identity) {
			live = append(live, entry)
		}
	}
	if len(live) > 1 {
		paths := make([]string, 0, len(live))
		for _, entry := range live {
			paths = append(paths, launch.ExistingPath(entry.AgentDir))
		}
		return statusLaunchSelection{DuplicatePaths: paths}
	}
	if len(live) == 1 {
		return statusLaunchSelection{Entry: live[0], Found: true}
	}
	canonicalRoot := squadnamespace.AMQRoot(t.Project, profile, session)
	for _, entry := range candidates {
		if sameResolvedDir(entry.Record.Root, canonicalRoot) {
			return statusLaunchSelection{Entry: entry, Found: true}
		}
	}
	return statusLaunchSelection{Entry: candidates[0], Found: true}
}

func simpleStartRuntimeLive(rec launch.Record, identity launchRuntimeIdentity) bool {
	return identity.PIDLive || (rec.External && identity.PaneLive)
}

func simpleStartRecordDiverged(rec launch.Record, member team.Member, t team.Team, profile, session, root string, opts teamLaunchOptions) bool {
	project := t.Project
	binaryArgs := mergeBinaryArgs(t.BinaryArgs, opts.BinaryArgs)
	return rec.Handle != memberHandle(member) || !strings.EqualFold(rec.Role, member.Role) ||
		rec.Binary != member.Binary || rec.Session != session ||
		!sameResolvedDir(rec.Root, root) || !sameResolvedDir(rec.TeamHome, project) ||
		squadnamespace.NormalizeProfile(rec.TeamProfile) != squadnamespace.NormalizeProfile(profile) ||
		!sameResolvedDir(rec.CWD, member.EffectiveCWD(project)) || rec.Trust != opts.Trust ||
		rec.Model != memberResolvedModel(member, opts.ModelOverrides, binaryArgs) ||
		rec.Launcher != member.Launcher || rec.ToolProfile != member.EffectiveToolProfile() ||
		rec.ToolConfig != strings.TrimSpace(member.ToolConfig) || rec.ToolMCPConfig != strings.TrimSpace(member.ToolMCPConfig) ||
		!reflect.DeepEqual(rec.ToolAllowlist, member.ToolAllowlist) || !reflect.DeepEqual(rec.ToolBlocklist, member.ToolBlocklist)
}

func simpleStartAuthorityHandles(plan simpleStartPlan) []string {
	handles := amqAuthorityHandles(plan.Team)
	for _, row := range plan.Removed {
		if row.State == "live/config-diverged" && row.Record != nil {
			handles = append(handles, row.Record.Handle)
		}
	}
	return normalizeAMQAuthorityHandles(handles)
}

func validateSimpleStartTmuxTarget(opts teamLaunchOptions, session string, records []simpleStartRecord, probe launchRuntimeProbe) error {
	switch opts.Target {
	case "current-window":
		if strings.TrimSpace(os.Getenv("TMUX")) == "" || strings.TrimSpace(os.Getenv("TMUX_PANE")) == "" {
			return &simpleStartConflictError{Class: "unmanaged", Detail: "current-window requires a managed current tmux pane"}
		}
	case "new-session":
		if !tmuxSessionExists(opts.TerminalSession) {
			return nil
		}
		out, err := tmuxOutputCommand("tmux", "list-panes", "-s", "-t", opts.TerminalSession, "-F", "#{pane_id}\t#{pane_title}\t#{@amq_squad_title}")
		if err != nil {
			return &simpleStartConflictError{Class: "unmanaged", Detail: fmt.Sprintf("cannot verify existing tmux session %s: %v", opts.TerminalSession, err)}
		}
		panes := make(map[string]bool)
		var titleFields []string
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Split(line, "\t")
			if len(fields) > 0 {
				if paneID := strings.TrimSpace(fields[0]); paneID != "" {
					panes[paneID] = true
				}
			}
			if len(fields) > 1 {
				titleFields = append(titleFields, fields[1:]...)
			}
		}
		for _, item := range records {
			rec := item.Record
			if rec.Tmux == nil || strings.TrimSpace(rec.Tmux.Session) != strings.TrimSpace(opts.TerminalSession) {
				continue
			}
			paneID := strings.TrimSpace(rec.Tmux.PaneID)
			if paneID == "" || !panes[paneID] {
				continue
			}
			identity := classifyLaunchRuntimeIdentity(rec, "", paneID, probe)
			if identity.PaneLive && simpleStartRuntimeLive(rec, identity) {
				return nil
			}
		}
		prefix := "amq:" + session + ":"
		for _, title := range titleFields {
			if strings.HasPrefix(strings.TrimSpace(title), prefix) {
				return nil
			}
		}
		return &simpleStartConflictError{Class: "unmanaged", Detail: fmt.Sprintf("tmux session %s exists without a pane owned by workstream %s", opts.TerminalSession, session)}
	}
	return nil
}

func readSimpleStartBriefBytes(path string) ([]byte, bool, error) {
	b, err := os.ReadFile(path)
	if err == nil {
		return b, true, nil
	}
	if !os.IsNotExist(err) {
		return nil, false, fmt.Errorf("read active brief: %w", err)
	}
	return nil, false, nil
}

func ensureSimpleStartBrief(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create brief directory: %w", err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create staged brief temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return fmt.Errorf("set staged brief mode: %w", err)
	}
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return fmt.Errorf("write staged brief: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close staged brief: %w", err)
	}
	if err := os.Link(tmpPath, path); errors.Is(err, os.ErrExist) {
		existing, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("verify concurrently created brief: %w", readErr)
		}
		if !bytesEqual(existing, content) {
			return fmt.Errorf("brief changed before staging; review %s and run start again", path)
		}
		return nil
	} else if err != nil {
		return fmt.Errorf("publish staged brief: %w", err)
	}
	return nil
}

func verifySimpleStartRecords(plan simpleStartPlan, result teamLaunchResult, deps simpleStartDependencies) error {
	for _, pane := range result.Panes {
		member, ok := memberByRole(plan.SpawnTeam, strings.ToLower(pane.Role))
		if !ok {
			return fmt.Errorf("launch result contains unknown role %s", pane.Role)
		}
		agentDir := filepath.Join(plan.Root, "agents", memberHandle(member))
		var rec launch.Record
		var err error
		for attempt := 0; attempt < 40; attempt++ {
			rec, err = launch.Read(agentDir)
			if err == nil && rec.Tmux != nil && rec.Tmux.PaneID == pane.PaneID && classifyLaunchRuntimeIdentity(rec, "", pane.PaneID, deps.RuntimeProbe).PIDLive {
				break
			}
			if attempt < 39 {
				deps.Sleep(25 * time.Millisecond)
			}
		}
		if err != nil {
			return fmt.Errorf("read launch record for %s after verified child dispatch: %w", pane.Role, err)
		}
		if rec.Tmux == nil || rec.Tmux.PaneID != pane.PaneID {
			return fmt.Errorf("launch record for %s does not own pane %s", pane.Role, pane.PaneID)
		}
		if !classifyLaunchRuntimeIdentity(rec, "", pane.PaneID, deps.RuntimeProbe).PIDLive {
			return fmt.Errorf("launch record for %s does not own the verified live child process", pane.Role)
		}
		if expected := strings.TrimSpace(plan.LaunchOptions.RestoreConversations[pane.Role]); expected != "" && rec.Conversation != expected {
			return fmt.Errorf("start restore for %s did not preserve recorded conversation %q", pane.Role, expected)
		}
	}
	return nil
}

func validateSimpleStartRestoreCommands(plan simpleStartPlan) error {
	for _, pane := range plan.LaunchOptions.ComposedPanes {
		expected := strings.TrimSpace(plan.LaunchOptions.RestoreConversations[pane.Role])
		if expected == "" {
			continue
		}
		needle := " --conversation " + shellQuote(expected)
		if !strings.Contains(pane.Command, needle) {
			return fmt.Errorf("start restore for %s refused: composed child command omits recorded conversation %q", pane.Role, expected)
		}
		if !strings.Contains(pane.Command, " --no-bootstrap") {
			return fmt.Errorf("start restore for %s refused: composed child command would replay bootstrap instead of resuming conversation %q", pane.Role, expected)
		}
	}
	return nil
}

func validateSimpleStartRestoreResultCommands(plan simpleStartPlan, result teamLaunchResult) error {
	returned := make(map[string]string, len(result.Panes))
	for _, pane := range result.Panes {
		returned[pane.Role] = pane.ChildCommand
	}
	for role, conversation := range plan.LaunchOptions.RestoreConversations {
		conversation = strings.TrimSpace(conversation)
		if conversation == "" {
			continue
		}
		command := strings.TrimSpace(returned[role])
		if command == "" {
			return fmt.Errorf("start restore for %s refused: launch result omits the composed child command for recorded conversation %q", role, conversation)
		}
		if !strings.Contains(command, " --conversation "+shellQuote(conversation)) {
			return fmt.Errorf("start restore for %s refused: dispatched child command omits recorded conversation %q", role, conversation)
		}
		if !strings.Contains(command, " --no-bootstrap") {
			return fmt.Errorf("start restore for %s refused: dispatched child command would replay bootstrap instead of resuming conversation %q", role, conversation)
		}
	}
	return nil
}

func sameSimpleStartInputs(a, b simpleStartPlan) bool {
	return a.Project == b.Project && a.Profile == b.Profile && a.Session == b.Session &&
		a.Root == b.Root && a.Goal == b.Goal && a.BriefPath == b.BriefPath && bytesEqual(a.BriefBytes, b.BriefBytes) && bytesEqual(a.RulesBytes, b.RulesBytes) &&
		reflect.DeepEqual(a.RoleBriefBytes, b.RoleBriefBytes) && reflect.DeepEqual(a.AllPanes, b.AllPanes)
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func simpleStartLockPath(project, profile, session string) string {
	name := squadnamespace.NormalizeProfile(profile) + "." + session + ".launch.lock"
	return filepath.Join(project, ".amq-squad", "locks", name)
}

func renderSimpleStartPlan(out io.Writer, plan simpleStartPlan) {
	fmt.Fprintln(out, "Start plan")
	fmt.Fprintf(out, "  project: %s\n  profile: %s\n  session: %s\n  root: %s\n  brief: %s\n", plan.Project, plan.Profile, plan.Session, plan.Root, plan.BriefPath)
	for _, row := range plan.Roles {
		fmt.Fprintf(out, "  %-18s %-22s %s\n", row.Member.Role, row.State, row.Detail)
	}
	if plan.Goal != "" {
		fmt.Fprintf(out, "  goal for lead: %s\n", plan.Goal)
	}
	if draft := plan.BriefDraft; draft != nil {
		fmt.Fprintf(out, "  drafter config source: %s\n", draft.ConfigSource)
		writeSimpleStartDrafterAttempts(out, draft)
		if draft.Manual {
			fmt.Fprintln(out, "\nNo brief was staged.")
			fmt.Fprintf(out, "Reason: %s\nRemedy: %s\n\nManual drafting prompt:\n\n%s\n", draft.Reason, draft.Remedy, draft.Prompt)
		} else {
			fmt.Fprintln(out, "\nProposed workstream brief (review before launch):")
			fmt.Fprintln(out)
			fmt.Fprint(out, string(draft.Document))
			if len(draft.Document) > 0 && draft.Document[len(draft.Document)-1] != '\n' {
				fmt.Fprintln(out)
			}
		}
	}
	if len(plan.Removed) > 0 {
		sort.Slice(plan.Removed, func(i, j int) bool { return plan.Removed[i].Member.Role < plan.Removed[j].Member.Role })
		for _, row := range plan.Removed {
			fmt.Fprintf(out, "  %-18s %-22s %s\n", row.Member.Role, row.State, row.Detail)
		}
	}
}

func writeSimpleStartDrafterAttempts(out io.Writer, draft *simpleStartBriefDraft) {
	if draft == nil {
		return
	}
	if len(draft.Attempts) == 0 {
		if command := strings.TrimSpace(draft.Evidence.CommandDisplay); command != "" {
			fmt.Fprintf(out, "  drafter command: %s\n", command)
		}
		return
	}
	for _, attempt := range draft.Attempts {
		fmt.Fprintf(out, "  drafter attempt (%s): %s\n", attempt.Backend, attempt.CommandDisplay)
		if failure := strings.TrimSpace(attempt.Failure); failure != "" {
			fmt.Fprintf(out, "  fall-through: %s\n", failure)
		}
	}
}

func deliverSimpleStartGoal(plan simpleStartPlan, goal string) error {
	leadRole := strings.TrimSpace(plan.Team.Lead)
	if !plan.Team.Orchestrated || leadRole == "" {
		return fmt.Errorf("team has no configured orchestration lead")
	}
	lead, ok := teamMemberByRole(plan.Team, leadRole)
	if !ok {
		return fmt.Errorf("configured lead role %q is not in the active roster", leadRole)
	}
	from := team.EffectiveOperator(plan.Team).Handle
	if strings.TrimSpace(from) == "" {
		from = team.DefaultOperatorHandle
	}
	_, err := runAMQCommand(amqCommandRequest{
		Dir: plan.Project,
		Env: envWithoutAMQIdentity(os.Environ()),
		Arg: []string{"send", "--root", plan.Root, "--me", from, "--to", memberHandle(lead), "--kind", "todo", "--subject", "GOAL: start", "--body", strings.TrimSpace(goal)},
	})
	return err
}

func confirmSimpleStart(out io.Writer, in io.Reader) bool {
	return confirmSimpleStartPrompt(out, in, "Launch now? [y/N] ")
}

// confirmSimpleStartPrompt is confirmSimpleStart with a caller-chosen
// prompt (gh#757: the launchapi path's confirmation names the exact
// subject_digest being applied, not a generic "Launch now?").
func confirmSimpleStartPrompt(out io.Writer, in io.Reader, prompt string) bool {
	fmt.Fprint(out, prompt)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && len(line) == 0 {
		return false
	}
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}
