package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

type teamLaunchOptions struct {
	Terminal        string
	Target          string
	Layout          string
	Workstream      string
	TerminalSession string
	Fresh           bool
	NoBootstrap     bool
	Stagger         time.Duration
	DryRun          bool
	SquadBin        string
	BinaryArgs      map[string][]string
	Trust           string
	ModelOverrides  map[string]string
	EffortOverrides map[string]string
	ForceDuplicate  bool
	NoGitignore     bool
	Symphony        bool
	WakeInjectVia   string
	WakeInjectArgs  []string
	WakeInjectMode  string
	// SeedBriefContent, when non-empty, is the rendered active brief that
	// the live launch path should write to .amq-squad/briefs/<workstream>.md
	// AFTER all team-launch validations and preflight pass. Empty means no
	// seeded brief was requested for this run. SeedBriefForce permits
	// overwriting an existing brief.
	SeedBriefContent string
	SeedBriefForce   bool
	// Profile is the named team profile this launch represents. Empty means
	// the implicit default profile. Propagated to emitted launch commands
	// via --team-profile so each agent's launch record carries the same
	// profile identity for bootstrap routing and status display.
	Profile string
	// WarnStubBrief, when true, makes the live launch emit a warn-if-stub
	// notice on stderr (silenced by --quiet) after a successful launch when
	// the brief is an untouched generated stub. `up` sets this when no brief
	// source (--seed-from) was supplied so CI / send-keys flows keep working
	// without a hard error, while nudging the operator to fill in the goal.
	WarnStubBrief bool
	// ExternalLeadRole names a profile lead that is already bound to the
	// caller's current pane. Launch backends skip it and spawn only the
	// remaining workers. This explicit path lets run start --external-lead
	// validate previews without writing launch records or starting wake.
	ExternalLeadRole string
	// AllowNoMembersAfterExternalLead treats a lead-only run as a successful
	// bind instead of an error after ExternalLeadRole filters out the lead.
	AllowNoMembersAfterExternalLead bool
	// ResultSink is used only by run start layout finalization. Backends that
	// can return exact runtime IDs call it synchronously before Launch returns.
	ResultSink            func(teamLaunchResult)
	PreserveLauncherFocus bool
	AllowExistingSession  bool
	SimpleStart           bool
	CanonicalRoot         string
	StartupPrompts        map[string]string
	RestoreConversations  map[string]string
	ComposedPanes         []teamLaunchPane
	AfterCheckpoint       func(simpleStartCheckpoint) error
	// ResolvedAMQPreflights is a complete, floor-validated snapshot prepared
	// by a parent command while it still owns the namespace admission and
	// before any destructive reset. A non-nil slice prevents executeTeamLaunch
	// from re-resolving AMQ after that parent's mutation boundary.
	ResolvedAMQPreflights []agentLaunchPreflight
	// LaunchVia selects the launch orchestration path independent of
	// Terminal. As of v2.31.0 (gh#755), empty (or "auto") defaults to
	// launchapiTeamLaunchBackend whenever Terminal resolves to tmux; a
	// non-tmux Terminal still falls back to the legacy per-terminal lookup.
	// "launchapi" opts in explicitly (same selection, same tmux-only
	// requirement). "legacy" opts out to the pre-gh#755 tmux pane driver,
	// byte-identical to v2.30.1's empty/"auto" behavior; deprecated and
	// deleted in v2.32.0.
	LaunchVia string
	// LaunchapiDecisions carries explicit operator answers (ACTION_ID ->
	// CHOICE) to launchapi RequiredActionV1 gates, from repeated
	// --launchapi-decision flags. Only consulted by launchapiTeamLaunchBackend;
	// nil/empty means no decisions were supplied and every required action
	// surfaces as an operator gate instead.
	LaunchapiDecisions map[string]string
}

type teamLaunchResult struct {
	Panes []teamLaunchResultPane
}

type teamLaunchResultPane struct {
	Role         string
	PaneID       string
	WindowID     string
	ChildCommand string
}

type teamLaunchPane struct {
	Role    string
	CWD     string
	Command string
	// Engine is the normalized agent binary expected to be the pane's current
	// command once bootstrap succeeds. It is what lets the launcher tell "agent
	// is up" from "agent died and left a shell prompt" (#540).
	Engine string
}

type teamLaunchBackend interface {
	Name() string
	Validate(teamLaunchOptions) error
	DryRun(team.Team, teamLaunchOptions) error
	Launch(team.Team, teamLaunchOptions) error
}

type teamLaunchResultBackend interface {
	LaunchWithResult(team.Team, teamLaunchOptions) (teamLaunchResult, error)
}

// Terminal support is intentionally backend-based. A new terminal integration
// should live in its own team_launch_<name>.go file and call
// registerTeamLaunchBackend from init.
var teamLaunchBackends = map[string]teamLaunchBackend{}

// resolveTeamLaunchAMQEnv is the single injectable resolution seam for parent
// team launch/reset paths. Every production call validates the returned AMQ
// version before any persistent mutation or dry-run preview.
var resolveTeamLaunchAMQEnv = resolveAMQEnvForTeamLaunchProfile

func registerTeamLaunchBackend(backend teamLaunchBackend) {
	name := backend.Name()
	if name == "" {
		panic("team launch backend has empty name")
	}
	if _, exists := teamLaunchBackends[name]; exists {
		panic("duplicate team launch backend: " + name)
	}
	teamLaunchBackends[name] = backend
}

// runTeamLaunch is the parser/setup wrapper for the live team launcher. The
// `team launch` subcommand is legacy in favor of `up`; this body is retained
// internal-only so the live-launch backend path stays exercised by tests.
// User-facing live launch flows through runUp -> executeTeamLaunch.
func runTeamLaunch(args []string) error {
	fs := flag.NewFlagSet("team launch", flag.ContinueOnError)
	pf := registerPreviewFlags(fs)
	lf := registerLiveLaunchFlags(fs)
	dryRun := fs.Bool("dry-run", false, "print terminal commands without executing them")
	profileFlag := fs.String("profile", "", "team profile to launch (default: default profile)")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `amq-squad team launch - open the configured team in a terminal

Usage:
  amq-squad team launch [--profile NAME] [--session workstream] [--fresh] [--terminal tmux]
    [--target current-window|new-window|new-session] [--layout vertical|horizontal|tiled]
    [--terminal-session name] [--stagger 750ms] [--no-bootstrap]
    [--trust sandboxed|approve-for-me|trusted] [--model role=model,...]
    [--effort role=level,...]
    [--codex-args args] [--claude-args args]
    [--force-duplicate] [--no-gitignore] [--symphony] [--dry-run]

Supported terminal backends: %s

tmux defaults to splitting the current tmux window into one pane per agent. Use
--target new-window for one tmux window (iTerm2 tab under -CC) per agent — a
full-size terminal each, better for many agents. Use --target new-session to
create a detached squad session. The whole roster is preflighted for live
duplicates before any tmux command runs; --force-duplicate overrides.

--symphony is an opt-in Codex-only lifecycle hook: each launched Codex member
patches its existing WORKFLOW.md with AMQ Symphony hooks pinned to the resolved
AMQ root and handle. If WORKFLOW.md is absent, the AMQ adapter error is
propagated and launch stops. amq-squad never creates WORKFLOW.md itself.

Examples:
  amq-squad team launch
  amq-squad team launch --target new-session --terminal-session squad
`, strings.Join(registeredTeamLaunchTerminals(), ", "))
	}
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fs.Usage()
		return nil
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	resolvedContext, err := resolveCanonicalContext(contextResolveOptions{
		ProfileFlag: *profileFlag, SessionFlag: *pf.session,
		ProfileExplicit: flagWasSet(fs, "profile"), SessionExplicit: flagWasSet(fs, "session"),
	})
	if err != nil {
		return err
	}
	emitContextDiagnostics(resolvedContext)
	if !flagWasSet(fs, "session") && resolvedContext.Sources["session"] != contextSourceDefault {
		*pf.session = resolvedContext.Session
	}
	opts, err := buildLiveLaunchOptions(fs, pf, lf)
	if err != nil {
		return err
	}
	opts.DryRun = *dryRun
	opts.Profile = resolvedContext.Profile
	return executeTeamLaunch(opts, flagWasSet(fs, "session"), flagWasSet(fs, "trust"))
}

// executeTeamLaunch is the post-parse body shared by the live team launcher and live
// `up`. opts must already carry the resolved binary args, model overrides,
// trust, and live backend fields; the explicit-* bools mirror flagWasSet so
// trust/session resolution against team.json defaults stays correct.
func executeTeamLaunch(opts teamLaunchOptions, explicitSession bool, explicitTrust bool) error {
	backend, err := resolveTeamLaunchBackend(opts)
	if err != nil {
		return err
	}
	if err := backend.Validate(opts); err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	t, err := team.ReadProfile(cwd, opts.Profile)
	if err != nil {
		return fmt.Errorf("read team: %w", err)
	}
	if len(t.Members) == 0 {
		return fmt.Errorf("team has no members")
	}
	agentCatalog := loadAgentCatalogAndWarn(cwd)
	workstream, err := resolveTeamWorkstreamName(t, opts.Workstream, explicitSession)
	if err != nil {
		return err
	}
	opts.Workstream = workstream
	if !opts.DryRun && opts.ResolvedAMQPreflights == nil {
		if err := validateResolvedTeamAMQVersions(t, opts.Profile, workstream, resolveTeamLaunchAMQEnv); err != nil {
			return err
		}
	}
	if !opts.DryRun {
		initialIdentity, err := captureNamespaceEndpointIdentity(squadnamespace.Resolve(cwd, opts.Profile, workstream), "")
		if err != nil {
			return err
		}
		admission, err := acquireNamespaceWriterAdmission(cwd, opts.Profile, workstream)
		if err != nil {
			return err
		}
		defer admission.close()
		currentTeam, err := team.ReadProfile(cwd, opts.Profile)
		if err != nil {
			return fmt.Errorf("team launch refused: reread team under admission: %w", err)
		}
		currentWorkstream, err := resolveTeamWorkstreamName(currentTeam, opts.Workstream, true)
		if err != nil {
			return err
		}
		if opts.ResolvedAMQPreflights == nil {
			if err := validateResolvedTeamAMQVersions(currentTeam, opts.Profile, currentWorkstream, resolveTeamLaunchAMQEnv); err != nil {
				return err
			}
		}
		currentIdentity, err := captureNamespaceEndpointIdentity(squadnamespace.Resolve(cwd, opts.Profile, currentWorkstream), "")
		if err != nil {
			return err
		}
		if err := validateReResolvedEndpointIdentity("team launch", initialIdentity, currentIdentity); err != nil {
			return err
		}
		t, workstream, opts.Workstream = currentTeam, currentWorkstream, currentWorkstream
		if err := ensureNoNamespaceMigration("team launch", cwd, opts.Profile, workstream); err != nil {
			return err
		}
	}
	active, skipped := filterMembersBySession(t.Members, workstream)
	for _, m := range skipped {
		quietNotice("notice: skipping %s: pinned to session %q, not %q\n", m.Role, m.Session, workstream)
	}
	if len(active) == 0 {
		return fmt.Errorf("no team members are pinned to session %q (all %d member(s) belong to other sessions)", workstream, len(t.Members))
	}
	t.Members = active
	t.Members, err = applyLaunchEffortOverridesCatalog(t.Members, opts.EffortOverrides, agentCatalog)
	if err != nil {
		return err
	}
	trustMode, err := resolveTeamTrustMode(t, opts.Trust, explicitTrust)
	if err != nil {
		return err
	}
	opts.Trust = trustMode
	// Apply the trust + binary-args contradiction check before opening any
	// pane, so a misconfigured team never partially launches into runLaunch
	// errors per pane.
	mergedBinaryArgs := mergeBinaryArgs(t.BinaryArgs, opts.BinaryArgs)
	if err := validateTrustCombination(trustMode, explicitTrust || strings.TrimSpace(t.Trust) != "", false, mergedBinaryArgs); err != nil {
		return err
	}
	if err := validateMembersTrust(trustMode, explicitTrust || strings.TrimSpace(t.Trust) != "", t.Members); err != nil {
		return err
	}
	if err := validateMemberOverlayPaths(t, t.Members); err != nil {
		return err
	}
	// Reject --model role=model entries whose role is not on the team.
	memberRoles := make(map[string]bool, len(t.Members))
	for _, m := range t.Members {
		memberRoles[strings.ToLower(m.Role)] = true
	}
	if err := validateModelOverrideKeys(opts.ModelOverrides, memberRoles); err != nil {
		return err
	}

	// Resolve and floor-validate the complete active roster before the
	// external-lead path can stamp a pane or write launch.json. Filtering the
	// lead happens only after this read-only snapshot exists; the filtered
	// preflight list is then reused without another AMQ resolution.
	preflights := append([]agentLaunchPreflight(nil), opts.ResolvedAMQPreflights...)
	if opts.ResolvedAMQPreflights == nil {
		preflights, err = buildTeamPreflights(t, opts)
		if err != nil {
			return err
		}
	} else if err := validateResolvedTeamPreflights(t, opts.Workstream, preflights); err != nil {
		return err
	}
	authorityPreflights := append([]agentLaunchPreflight(nil), preflights...)
	authorityHandles := amqAuthorityHandles(t)
	if opts.Fresh {
		exists, root, err := teamWorkstreamExists(t, opts.Profile, opts.Workstream)
		if err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("workstream session %q already exists at %s", opts.Workstream, root)
		}
	}
	externalLeadFiltered := false
	if strings.TrimSpace(opts.ExternalLeadRole) != "" {
		filtered, skipped, err := filterExplicitExternalLead(t, opts.ExternalLeadRole)
		if err != nil {
			return err
		}
		if skipped {
			t = filtered
			externalLeadFiltered = true
		}
	} else if filtered, skipped, err := maybeFilterCurrentExternalLeadFromPreflights(t, opts.Workstream, opts.Profile, trustMode, mergedBinaryArgs, opts.ModelOverrides, preflights, !opts.DryRun); err != nil {
		return err
	} else if skipped {
		t = filtered
		externalLeadFiltered = true
	}
	if len(t.Members) == 0 {
		if opts.AllowNoMembersAfterExternalLead && externalLeadFiltered {
			if !opts.DryRun {
				createdAMQPaths, err := prepareSelectedAMQRoots(authorityPreflights, authorityHandles)
				if err != nil {
					return errors.Join(err, cleanupCreatedLaunchDirectories(createdAMQPaths))
				}
				cleanupAuthority := func(cause error) error {
					return errors.Join(cause, cleanupCreatedLaunchDirectories(createdAMQPaths))
				}
				if opts.SeedBriefContent != "" {
					if _, err := writeSeedBriefForProfile(t.Project, opts.Profile, opts.Workstream, opts.SeedBriefContent, opts.SeedBriefForce); err != nil {
						return cleanupAuthority(err)
					}
				}
				if _, _, err := ensureBriefStubForProfile(t.Project, opts.Profile, opts.Workstream); err != nil {
					return cleanupAuthority(fmt.Errorf("ensure brief: %w", err))
				}
				// An external lead can be the only live member. Notifications are
				// still operational infrastructure and must not depend on spawning a
				// worker pane (or on operator poll_required).
				if err := reconcileNotificationWatcherStarted(t, opts.Profile, opts.Workstream, ""); err != nil {
					return cleanupAuthority(err)
				}
			}
			quietNotice("lead bound; no remaining workers to spawn for session %s.\n", opts.Workstream)
			if opts.ResultSink != nil {
				opts.ResultSink(teamLaunchResult{})
			}
			// An external-lead-only launch is still a launch: the operator is
			// on the hook for its gates exactly as they would be for a full
			// roster, so this success path gets the handoff card too (#493).
			printOperatorHandoffCard(os.Stdout, t, opts.Profile, opts.Workstream)
			return nil
		}
		return fmt.Errorf("no team members to launch after external lead filtering")
	}
	if opts.Symphony {
		if err := validateTeamSymphonyMembers(t, t.Members); err != nil {
			return err
		}
	}
	// Preflight the whole roster before any tmux command (or dry-run output)
	// so a partially-launched team never appears.
	preflights = filterResolvedTeamPreflights(preflights, t.Members)
	if err := preflightTeam(preflights, defaultDuplicateLaunchProbe); err != nil {
		return err
	}

	if opts.DryRun {
		renderTeamAMQFloorViolations(os.Stdout, teamAMQFloorViolationsForPreflights(preflights))
		return backend.DryRun(t, opts)
	}
	// Validate every configured custom launcher up front so a missing or
	// non-executable wrapper aborts the whole launch before any pane opens,
	// rather than failing mid-roster after other members have already started.
	for _, m := range t.Members {
		if m.Launcher == "" {
			continue
		}
		if err := ensureLauncherExecutable(m.Launcher); err != nil {
			return fmt.Errorf("%s: %w", m.Role, err)
		}
	}
	briefSnapshot, err := captureLaunchFileSnapshot(briefPathForProfile(t.Project, opts.Profile, opts.Workstream))
	if err != nil {
		return fmt.Errorf("snapshot active brief: %w", err)
	}
	createdAMQDirs, err := prepareSelectedAMQRoots(authorityPreflights, authorityHandles)
	if err != nil {
		return errors.Join(err, cleanupCreatedLaunchDirectories(createdAMQDirs))
	}
	createdWatcherToken := ""
	rollbackLaunchPreparation := func(cause error) error {
		cleanupErrs := []error{cause}
		if createdWatcherToken != "" {
			if err := cleanupCreatedNotificationWatcherAfterLaunchFailure(t, opts.Profile, opts.Workstream, createdWatcherToken, defaultDuplicateLaunchProbe); err != nil {
				cleanupErrs = append(cleanupErrs, fmt.Errorf("notification watcher cleanup after clean launch failure: %w", err))
			}
		}
		if err := briefSnapshot.restore(); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("restore active brief after clean launch failure: %w", err))
		}
		if err := cleanupCreatedLaunchDirectories(createdAMQDirs); err != nil {
			cleanupErrs = append(cleanupErrs, err)
		}
		return errors.Join(cleanupErrs...)
	}
	// Live launch. If the caller (up --seed-from) requested a seeded brief,
	// write it now: all team-launch validations and preflight have passed,
	// so we are committed to opening backend panes. Doing the write here
	// (rather than upfront in runUp) means a fresh/existing-workstream
	// rejection, model/trust validation failure, or duplicate-live
	// preflight refusal does not mutate the brief.
	if opts.SeedBriefContent != "" {
		if _, err := writeSeedBriefForProfile(t.Project, opts.Profile, opts.Workstream, opts.SeedBriefContent, opts.SeedBriefForce); err != nil {
			return rollbackLaunchPreparation(err)
		}
	}
	// Ensure the team-home active brief exists once before the backend
	// opens panes. ensureBriefStub is idempotent and preserves any existing
	// brief content (including the seed we may have just written), so this
	// is safe across reruns and parallel member launches.
	if _, _, err := ensureBriefStubForProfile(t.Project, opts.Profile, opts.Workstream); err != nil {
		return rollbackLaunchPreparation(fmt.Errorf("ensure brief: %w", err))
	}
	watcherBefore := notificationWatcherGeneration(t, opts.Profile, opts.Workstream)
	watcherBaseRoot := ""
	if len(preflights) > 0 && strings.TrimSpace(preflights[0].Root) != "" {
		watcherBaseRoot = filepath.Dir(preflights[0].Root)
	}
	if err := reconcileNotificationWatcherStarted(t, opts.Profile, opts.Workstream, watcherBaseRoot); err != nil {
		return rollbackLaunchPreparation(err)
	}
	watcherAfter := notificationWatcherGeneration(t, opts.Profile, opts.Workstream)
	if watcherAfter != "" && watcherAfter != watcherBefore {
		createdWatcherToken = watcherAfter
	}
	if opts.ResultSink != nil {
		if resultBackend, ok := backend.(teamLaunchResultBackend); ok {
			result, err := resultBackend.LaunchWithResult(t, opts)
			if err != nil {
				return rollbackLaunchPreparation(err)
			}
			opts.ResultSink(result)
		} else {
			if err := backend.Launch(t, opts); err != nil {
				return rollbackLaunchPreparation(err)
			}
			opts.ResultSink(teamLaunchResult{})
		}
	} else if err := backend.Launch(t, opts); err != nil {
		return rollbackLaunchPreparation(err)
	}
	quietNotice("started %s using profile %s in %s\n", opts.Workstream, opts.Profile, t.Project)
	if len(preflights) > 0 && preflights[0].Root != "" {
		quietNotice("AM_ROOT: %s\n", preflights[0].Root)
	}
	profileArg := commandProfileArg(opts.Profile)
	quietNotice("next: amq-squad status%s --session %s | amq-squad doctor%s | amq-squad down%s --all --session %s\n",
		profileArg, shellQuote(opts.Workstream), profileArg, profileArg, shellQuote(opts.Workstream))
	// Post-launch warn-if-stub nudge: `up` without a brief source auto-stubs
	// the brief above and asks us to flag it so non-interactive automation
	// keeps working while still being told to set the goal. Only fire when the
	// brief on disk is genuinely an untouched stub (a --seed-from authored
	// brief, or one the operator already edited, classifies as briefReal).
	if opts.WarnStubBrief {
		if _, kind := classifyBriefForProfile(t.Project, opts.Profile, opts.Workstream); kind == briefStub {
			quietNotice("notice: started %s with a stub brief — edit %s or pass --seed-from to set the goal.\n",
				opts.Workstream, briefPathForProfile(t.Project, opts.Profile, opts.Workstream))
		}
	}
	// The handoff card is the last thing a successful launch prints (#493).
	// It goes to stdout, not quietNotice, so --quiet cannot silence the one
	// output that tells the human they are the operator.
	printOperatorHandoffCard(os.Stdout, t, opts.Profile, opts.Workstream)
	return nil
}

func validateCompleteTeamLaunchResult(panes []teamLaunchPane, target string, result teamLaunchResult) error {
	if len(result.Panes) != len(panes) {
		return fmt.Errorf("team launch result has %d role(s), want %d", len(result.Panes), len(panes))
	}
	expected := make(map[string]struct{}, len(panes))
	for _, pane := range panes {
		role := strings.TrimSpace(pane.Role)
		if role == "" {
			return fmt.Errorf("team launch plan contains an empty role")
		}
		if _, exists := expected[role]; exists {
			return fmt.Errorf("team launch plan contains duplicate role %q", role)
		}
		expected[role] = struct{}{}
	}
	seenRoles := make(map[string]struct{}, len(result.Panes))
	seenPanes := make(map[string]struct{}, len(result.Panes))
	seenWindows := make(map[string]struct{}, len(result.Panes))
	for _, pane := range result.Panes {
		role := strings.TrimSpace(pane.Role)
		if _, ok := expected[role]; !ok {
			return fmt.Errorf("team launch result contains unexpected role %q", role)
		}
		if _, exists := seenRoles[role]; exists {
			return fmt.Errorf("team launch result contains duplicate role %q", role)
		}
		seenRoles[role] = struct{}{}
		paneID, err := exactTmuxPaneID(pane.PaneID)
		if err != nil {
			return fmt.Errorf("team launch result for role %s: %w", role, err)
		}
		if _, exists := seenPanes[paneID]; exists {
			return fmt.Errorf("team launch result contains duplicate pane %s", paneID)
		}
		seenPanes[paneID] = struct{}{}
		windowID, err := exactTmuxWindowID(pane.WindowID)
		if err != nil {
			return fmt.Errorf("team launch result for role %s: %w", role, err)
		}
		if target == "new-window" {
			if _, exists := seenWindows[windowID]; exists {
				return fmt.Errorf("new-window team launch result contains duplicate window %s", windowID)
			}
			seenWindows[windowID] = struct{}{}
		}
	}
	return nil
}

func commandProfileArg(profile string) string {
	profile = strings.TrimSpace(profile)
	if profile == "" || profile == team.DefaultProfile {
		return ""
	}
	return " --profile " + shellQuote(profile)
}

func validateTeamSymphonyMembers(t team.Team, members []team.Member) error {
	byCWD := map[string][]string{}
	codexCount := 0
	for _, m := range members {
		if normalizedAgentBinary(m.Binary) != "codex" {
			continue
		}
		codexCount++
		cwd := filepath.Clean(m.EffectiveCWD(t.Project))
		label := m.Role
		if handle := memberHandle(m); handle != "" && handle != m.Role {
			label += " (" + handle + ")"
		}
		byCWD[cwd] = append(byCWD[cwd], label)
	}
	if codexCount == 0 {
		return usageErrorf("--symphony requires at least one Codex member")
	}
	for cwd, labels := range byCWD {
		if len(labels) <= 1 {
			continue
		}
		sort.Strings(labels)
		return usageErrorf("--symphony cannot launch multiple Codex members sharing cwd %s (%s): AMQ Symphony manages one --me per WORKFLOW.md; launch one Codex member per cwd or run agent up --symphony explicitly", cwd, strings.Join(labels, ", "))
	}
	return nil
}

// buildTeamPreflights computes the agent-identity tuples team launch would
// produce so preflightTeam can refuse before any pane is created. dryRun
// passes through to each preflight so a --dry-run team launch never mutates
// disk state during stale-artifact handling.
func buildTeamPreflights(t team.Team, opts teamLaunchOptions) ([]agentLaunchPreflight, error) {
	members := orderedTeamMembers(t.Members)
	out := make([]agentLaunchPreflight, 0, len(members))
	for _, m := range members {
		cwd := m.EffectiveCWD(t.Project)
		env, err := resolveTeamLaunchAMQEnv(cwd, opts.Profile, opts.Workstream, m.Handle)
		if err != nil {
			return nil, fmt.Errorf("resolve amq env for %s: %w", m.Handle, err)
		}
		floorViolation := ""
		if err := validateLaunchAMQVersion(env.AMQVersion); err != nil {
			if !opts.DryRun {
				return nil, fmt.Errorf("%s: %w", m.Handle, err)
			}
			floorViolation = err.Error()
		}
		root := absoluteAMQRoot(cwd, env.Root)
		handle := m.Handle
		if env.Me != "" {
			handle = env.Me
		}
		agentDir := filepath.Join(root, "agents", handle)
		out = append(out, agentLaunchPreflight{
			Role:              m.Role,
			CWD:               cwd,
			AgentDir:          agentDir,
			Handle:            handle,
			Workstream:        env.SessionName,
			Root:              root,
			BaseRoot:          absoluteAMQRoot(cwd, env.BaseRoot),
			RootSource:        env.RootSource,
			Binary:            m.Binary,
			AMQVersion:        env.AMQVersion,
			AMQFloorViolation: floorViolation,
			Force:             opts.ForceDuplicate,
			DryRun:            opts.DryRun,
		})
	}
	return out, nil
}

func validateResolvedTeamPreflights(t team.Team, workstream string, preflights []agentLaunchPreflight) error {
	active, _ := filterMembersBySession(t.Members, workstream)
	expected := orderedTeamMembers(active)
	if len(preflights) != len(expected) {
		return fmt.Errorf("team launch refused: pinned AMQ preflight roster changed")
	}
	byRole := make(map[string]agentLaunchPreflight, len(preflights))
	for _, preflight := range preflights {
		role := strings.TrimSpace(preflight.Role)
		if role == "" || strings.TrimSpace(preflight.CWD) == "" || strings.TrimSpace(preflight.Root) == "" {
			return fmt.Errorf("team launch refused: pinned AMQ preflight is incomplete")
		}
		if _, exists := byRole[role]; exists {
			return fmt.Errorf("team launch refused: duplicate pinned AMQ preflight role %q", role)
		}
		byRole[role] = preflight
	}
	for _, member := range expected {
		preflight, ok := byRole[strings.TrimSpace(member.Role)]
		if !ok || !sameResolvedDir(preflight.CWD, member.EffectiveCWD(t.Project)) ||
			preflight.Workstream != workstream || strings.TrimSpace(preflight.Binary) != strings.TrimSpace(member.Binary) {
			return fmt.Errorf("team launch refused: pinned AMQ preflight no longer matches role %q", member.Role)
		}
	}
	return nil
}

func filterResolvedTeamPreflights(preflights []agentLaunchPreflight, members []team.Member) []agentLaunchPreflight {
	active := make(map[string]bool, len(members))
	for _, member := range members {
		active[strings.TrimSpace(member.Role)] = true
	}
	out := make([]agentLaunchPreflight, 0, len(members))
	for _, preflight := range preflights {
		if active[strings.TrimSpace(preflight.Role)] {
			out = append(out, preflight)
		}
	}
	return out
}

type teamAMQEnvResolver func(cwd, profile, session, handle string) (amqEnv, error)

// validateResolvedTeamAMQVersions applies the same fail-closed floor as
// `agent up` to every member that belongs to the selected workstream. Parent
// launch, resume, and reset paths call it before their first persistent side
// effect so an incompatible child can never reject only after the parent has
// changed roots, briefs, watchers, panes, or session state.
func validateResolvedTeamAMQVersions(t team.Team, profile, workstream string, resolve teamAMQEnvResolver) error {
	active, _ := filterMembersBySession(t.Members, workstream)
	for _, m := range orderedTeamMembers(active) {
		cwd := m.EffectiveCWD(t.Project)
		env, err := resolve(cwd, profile, workstream, m.Handle)
		if err != nil {
			return fmt.Errorf("resolve amq env for %s: %w", m.Handle, err)
		}
		if err := validateLaunchAMQVersion(env.AMQVersion); err != nil {
			return fmt.Errorf("%s: %w", m.Handle, err)
		}
	}
	return nil
}

func registeredTeamLaunchTerminals() []string {
	names := make([]string, 0, len(teamLaunchBackends))
	for name := range teamLaunchBackends {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func buildTeamLaunchPanes(t team.Team, opts teamLaunchOptions) []teamLaunchPane {
	members := orderedTeamMembers(t.Members)
	binaryArgs := mergeBinaryArgs(t.BinaryArgs, opts.BinaryArgs)
	panes := make([]teamLaunchPane, 0, len(members))
	for _, m := range members {
		cwd := m.EffectiveCWD(t.Project)
		conversation := strings.TrimSpace(opts.RestoreConversations[m.Role])
		startupPrompt := opts.StartupPrompts[m.Role]
		if conversation != "" {
			startupPrompt = ""
		}
		panes = append(panes, teamLaunchPane{
			Role:   m.Role,
			CWD:    cwd,
			Engine: normalizedAgentBinary(m.Binary),
			Command: emitTeamCommand(emitTeamCommandInput{
				CWD:            cwd,
				SquadBin:       opts.SquadBin,
				TeamHome:       t.Project,
				Member:         m,
				NoBootstrap:    opts.NoBootstrap,
				Workstream:     opts.Workstream,
				BinaryArgs:     binaryArgs,
				TrustMode:      opts.Trust,
				Model:          memberResolvedModel(m, opts.ModelOverrides, binaryArgs),
				ForceDuplicate: opts.ForceDuplicate,
				NoGitignore:    opts.NoGitignore,
				Symphony:       opts.Symphony,
				Profile:        opts.Profile,
				WakeInjectVia:  opts.WakeInjectVia,
				WakeInjectArgs: opts.WakeInjectArgs,
				WakeInjectMode: opts.WakeInjectMode,
				SimpleStart:    opts.SimpleStart,
				CanonicalRoot:  opts.CanonicalRoot,
				StartupPrompt:  startupPrompt,
				Conversation:   conversation,
			}),
		})
	}
	return panes
}

func resolvedTeamLaunchPanes(t team.Team, opts teamLaunchOptions) []teamLaunchPane {
	if opts.ComposedPanes != nil {
		return append([]teamLaunchPane(nil), opts.ComposedPanes...)
	}
	return buildTeamLaunchPanes(t, opts)
}

type externalLeadAMQResolver func(team.Member, string, string, string, string) (amqEnv, error)

func maybeFilterCurrentExternalLead(t team.Team, workstream, profile, trustMode string, binaryArgs map[string][]string, modelOverrides map[string]string, write bool) (team.Team, bool, error) {
	return maybeFilterCurrentExternalLeadUsing(
		t,
		workstream,
		profile,
		trustMode,
		binaryArgs,
		modelOverrides,
		func(_ team.Member, cwd, profile, workstream, handle string) (amqEnv, error) {
			return resolveTeamLaunchAMQEnv(cwd, profile, workstream, handle)
		},
		write,
	)
}

// maybeFilterCurrentExternalLeadFromPreflights is the only external-lead path
// reachable from executeTeamLaunch. In particular, up --reset calls it after
// deleting the prior session, so it must consume the pre-reset snapshot and
// must never resolve AMQ again.
func maybeFilterCurrentExternalLeadFromPreflights(t team.Team, workstream, profile, trustMode string, binaryArgs map[string][]string, modelOverrides map[string]string, preflights []agentLaunchPreflight, write bool) (team.Team, bool, error) {
	return maybeFilterCurrentExternalLeadUsing(
		t,
		workstream,
		profile,
		trustMode,
		binaryArgs,
		modelOverrides,
		func(lead team.Member, cwd, _ string, workstream, _ string) (amqEnv, error) {
			for _, preflight := range preflights {
				if !strings.EqualFold(strings.TrimSpace(preflight.Role), strings.TrimSpace(lead.Role)) {
					continue
				}
				if !sameResolvedDir(preflight.CWD, cwd) || preflight.Workstream != workstream {
					return amqEnv{}, fmt.Errorf("pinned AMQ preflight no longer matches external lead %s", lead.Role)
				}
				return amqEnv{
					AMQVersion:  preflight.AMQVersion,
					Root:        preflight.Root,
					BaseRoot:    preflight.BaseRoot,
					SessionName: preflight.Workstream,
					Me:          preflight.Handle,
					RootSource:  preflight.RootSource,
				}, nil
			}
			return amqEnv{}, fmt.Errorf("pinned AMQ preflight is missing external lead %s", lead.Role)
		},
		write,
	)
}

func maybeFilterCurrentExternalLeadUsing(t team.Team, workstream, profile, trustMode string, binaryArgs map[string][]string, modelOverrides map[string]string, resolve externalLeadAMQResolver, write bool) (team.Team, bool, error) {
	if !t.Orchestrated || strings.TrimSpace(t.Lead) == "" {
		return t, false, nil
	}
	lead, ok := memberByRole(t, strings.ToLower(strings.TrimSpace(t.Lead)))
	if !ok {
		return t, false, nil
	}
	id, err := currentPaneIdentity()
	if err != nil || id == nil || strings.TrimSpace(id.PaneID) == "" {
		return t, false, nil
	}
	cwd := lead.EffectiveCWD(t.Project)
	handle := memberHandle(lead)
	env, err := resolve(lead, cwd, profile, workstream, handle)
	if err != nil {
		if !write {
			return t, false, nil
		}
		return t, false, fmt.Errorf("resolve amq env for external lead %s: %w", handle, err)
	}
	if err := validateLaunchAMQVersion(env.AMQVersion); err != nil {
		if !write {
			return t, false, nil
		}
		return t, false, fmt.Errorf("%s: %w", handle, err)
	}
	if env.Me != "" {
		handle = env.Me
	}
	root := absoluteAMQRoot(cwd, env.Root)
	agentDir := filepath.Join(root, "agents", handle)
	if rec, err := launch.Read(agentDir); err == nil && externalRecordMatchesPane(rec, id.PaneID) {
		return filterLaunchMember(t, lead.Role, id.PaneID), true, nil
	}
	if _, rec, ok := findLaunchRecordByPane(root, id.PaneID); ok &&
		!launchRecordMatchesIdentity(rec, lead.Role, handle, profile, env.SessionName, root) {
		return t, false, nil
	}
	if !write || !currentEnvIdentifiesExternalLead(lead, handle, root) {
		return t, false, nil
	}
	if err := stampCapturedLaunchPane(id.PaneID, env.SessionName, lead.Role); err != nil {
		return t, false, fmt.Errorf("stamp external lead pane %s: %w", id.PaneID, err)
	}
	rec := externalLeadRecordForLaunch(lead, cwd, handle, root, env, id, profile, trustMode, binaryArgs, modelOverrides)
	if err := launch.Write(agentDir, rec); err != nil {
		return t, false, fmt.Errorf("write external lead record: %w", err)
	}
	return filterLaunchMember(t, lead.Role, id.PaneID), true, nil
}

func filterExplicitExternalLead(t team.Team, role string) (team.Team, bool, error) {
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "" {
		return t, false, nil
	}
	if !t.Orchestrated {
		return t, false, usageErrorf("--external-lead requires an orchestrated team profile")
	}
	if configured := strings.TrimSpace(t.Lead); configured != "" && configured != role {
		return t, false, usageErrorf("--external-lead role %q is not the configured profile lead %q", role, configured)
	}
	lead, ok := memberByRole(t, role)
	if !ok {
		return t, false, fmt.Errorf("lead role %q is not a team member", role)
	}
	id, err := currentPaneIdentity()
	if err != nil {
		return t, false, err
	}
	if id == nil || strings.TrimSpace(id.PaneID) == "" {
		return t, false, usageErrorf("--external-lead requires a current tmux pane (TMUX/TMUX_PANE unset)")
	}
	return filterLaunchMember(t, lead.Role, id.PaneID), true, nil
}

func externalRecordMatchesPane(rec launch.Record, paneID string) bool {
	if !rec.External || rec.Tmux == nil || strings.TrimSpace(rec.Tmux.PaneID) != strings.TrimSpace(paneID) {
		return false
	}
	return classifyLaunchRuntimeIdentity(
		rec,
		rec.Binary,
		paneID,
		launchRuntimeProbeFromDuplicate(defaultDuplicateLaunchProbe),
	).PaneLive
}

func currentEnvIdentifiesExternalLead(m team.Member, handle, root string) bool {
	me := strings.TrimSpace(os.Getenv("AM_ME"))
	if me == "" || (me != handle && me != m.Role) {
		return false
	}
	envRoot := strings.TrimSpace(os.Getenv("AM_ROOT"))
	if envRoot == "" || strings.TrimSpace(root) == "" || !rootsMatch(envRoot, root) {
		return false
	}
	return true
}

func externalLeadRecordForLaunch(m team.Member, cwd, handle, root string, env amqEnv, pane *tmuxpane.PaneIdentity, profile, trustMode string, binaryArgs map[string][]string, modelOverrides map[string]string) launch.Record {
	rec := launch.Record{
		CWD:              cwd,
		Binary:           m.Binary,
		Session:          env.SessionName,
		SharedWorkstream: true,
		Handle:           handle,
		Role:             m.Role,
		Root:             root,
		BaseRoot:         absoluteAMQRoot(cwd, env.BaseRoot),
		RootSource:       env.RootSource,
		AMQVersion:       env.AMQVersion,
		Model:            memberResolvedModel(m, modelOverrides, binaryArgs),
		ToolProfile:      m.EffectiveToolProfile(),
		ToolConfig:       strings.TrimSpace(m.ToolConfig),
		Trust:            trustMode,
		External:         true,
		AdoptionMode:     adoptionModeForExternalLeadRecord(m),
		LauncherPaneID:   pane.PaneID,
		AgentTTY:         currentLaunchTTY(),
		StartedAt:        time.Now().UTC(),
		TeamProfile:      profile,
		Tmux: &launch.TmuxInfo{
			Session:    pane.Session,
			WindowID:   pane.WindowID,
			WindowName: pane.WindowName,
			PaneID:     pane.PaneID,
			Target:     "external",
		},
	}
	rec.Terminal = launch.TerminalInfoFromTmux(rec.Tmux)
	extra := composeBinaryArgs(m.Binary, binaryArgsFor(m.Binary, binaryArgs), m.ExtraArgs())
	switch normalizedAgentBinary(m.Binary) {
	case "codex":
		rec.CodexArgs = extra
	case "claude":
		rec.ClaudeArgs = extra
	}
	return rec
}

func adoptionModeForExternalLeadRecord(m team.Member) string {
	if strings.TrimSpace(m.Role) == goalOrchestratorRole {
		return adoptionModeExternal
	}
	return adoptionModeExternalProjectLead
}

func filterLaunchMember(t team.Team, role, paneID string) team.Team {
	filtered := t
	filtered.Members = make([]team.Member, 0, len(t.Members))
	for _, m := range t.Members {
		if m.Role == role {
			continue
		}
		filtered.Members = append(filtered.Members, m)
	}
	quietNotice("notice: treating %s as external lead at pane %s; not spawning a duplicate lead\n", role, paneID)
	return filtered
}

// withTmuxTargetEnv wraps a per-pane launch command so the launched agent's
// record can persist how its pane was created (current-window / new-window /
// new-session). The assignment is exported inside a subshell so it reaches the
// amq-squad process (a plain `VAR=val cmd` would scope it to `cd` only) but
// does NOT leak into the operator's pane shell after the agent exits — a leak
// would make a later manual `agent up` in that pane record a stale target.
// target is a controlled enum, never user text, and is shell-quoted defensively.
// An empty target returns the command unchanged. Only the live tmux send-keys
// paths use this, so dry-run / copy-paste commands stay clean.
func withTmuxTargetEnv(target, command string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return command
	}
	assignments := []string{envTmuxTarget + "=" + shellQuote(target)}
	if launcherPane := strings.TrimSpace(os.Getenv("TMUX_PANE")); launcherPane != "" {
		assignments = append(assignments, envTmuxLauncherPane+"="+shellQuote(launcherPane))
	}
	// #577 finding 4: respawn-pane changes env inheritance. send-keys typed into an
	// INTERACTIVE shell, which had already sourced the operator's profile; respawn-pane runs
	// the command under a fresh non-interactive /bin/sh that inherits only tmux's session and
	// global environment. So PATH additions from .zshrc/.bash_profile -- commonly where
	// amq-squad itself lives, via a version manager or ~/.local/bin -- are simply absent, and
	// the agent command fails to resolve the binary.
	//
	// CWD was already protected by the explicit `cd`; env was not, and the difference is easy
	// to miss because the two look equally handled at the call site.
	//
	// The LAUNCHER's PATH is carried explicitly rather than trusting inheritance. It is the
	// process that resolved amq-squad to get here, so by construction it can resolve it again.
	if launcherPath := strings.TrimSpace(os.Getenv("PATH")); launcherPath != "" {
		assignments = append(assignments, "PATH="+shellQuote(launcherPath))
	}
	return "(export " + strings.Join(assignments, " ") + "; " + command + ")"
}

func teamSquadBin() string {
	if p, err := os.Executable(); err == nil {
		return p
	}
	return "amq-squad"
}
