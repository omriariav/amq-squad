package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
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
	ForceDuplicate  bool
	WakeInjectVia   string
	WakeInjectArgs  []string
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
}

type teamLaunchPane struct {
	Role    string
	CWD     string
	Command string
}

type teamLaunchBackend interface {
	Name() string
	Validate(teamLaunchOptions) error
	DryRun(team.Team, teamLaunchOptions) error
	Launch(team.Team, teamLaunchOptions) error
}

// Terminal support is intentionally backend-based. A new terminal integration
// should live in its own team_launch_<name>.go file and call
// registerTeamLaunchBackend from init.
var teamLaunchBackends = map[string]teamLaunchBackend{}

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
    [--codex-args args] [--claude-args args]
    [--force-duplicate] [--dry-run]

Supported terminal backends: %s

tmux defaults to splitting the current tmux window into one pane per agent. Use
--target new-window for one tmux window (iTerm2 tab under -CC) per agent — a
full-size terminal each, better for many agents. Use --target new-session to
create a detached squad session. The whole roster is preflighted for live
duplicates before any tmux command runs; --force-duplicate overrides.

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
	profile, err := resolveProfileFlag(*profileFlag)
	if err != nil {
		return err
	}
	opts, err := buildLiveLaunchOptions(fs, pf, lf)
	if err != nil {
		return err
	}
	opts.DryRun = *dryRun
	opts.Profile = profile
	return executeTeamLaunch(opts, flagWasSet(fs, "session"), flagWasSet(fs, "trust"))
}

// executeTeamLaunch is the post-parse body shared by the live team launcher and live
// `up`. opts must already carry the resolved binary args, model overrides,
// trust, and live backend fields; the explicit-* bools mirror flagWasSet so
// trust/session resolution against team.json defaults stays correct.
func executeTeamLaunch(opts teamLaunchOptions, explicitSession bool, explicitTrust bool) error {
	backend, ok := teamLaunchBackends[opts.Terminal]
	if !ok {
		return fmt.Errorf("unsupported terminal %q: supported terminals: %s", opts.Terminal, strings.Join(registeredTeamLaunchTerminals(), ", "))
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
	workstream, err := resolveTeamWorkstreamName(t, opts.Workstream, explicitSession)
	if err != nil {
		return err
	}
	opts.Workstream = workstream
	active, skipped := filterMembersBySession(t.Members, workstream)
	for _, m := range skipped {
		quietNotice("notice: skipping %s: pinned to session %q, not %q\n", m.Role, m.Session, workstream)
	}
	if len(active) == 0 {
		return fmt.Errorf("no team members are pinned to session %q (all %d member(s) belong to other sessions)", workstream, len(t.Members))
	}
	t.Members = active
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
	if filtered, skipped, err := maybeFilterCurrentExternalLead(t, opts.Workstream, opts.Profile, trustMode, mergedBinaryArgs, opts.ModelOverrides, !opts.DryRun); err != nil {
		return err
	} else if skipped {
		t = filtered
	}
	if len(t.Members) == 0 {
		return fmt.Errorf("no team members to launch after external lead filtering")
	}
	if opts.Fresh {
		exists, root, err := teamWorkstreamExists(t, opts.Workstream)
		if err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("workstream session %q already exists at %s", opts.Workstream, root)
		}
	}

	// Preflight the whole roster before any tmux command (or dry-run output)
	// so a partially-launched team never appears.
	preflights, err := buildTeamPreflights(t, opts)
	if err != nil {
		return err
	}
	if err := preflightTeam(preflights, defaultDuplicateLaunchProbe); err != nil {
		return err
	}

	if opts.DryRun {
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
	// Live launch. If the caller (up --seed-from) requested a seeded brief,
	// write it now: all team-launch validations and preflight have passed,
	// so we are committed to opening backend panes. Doing the write here
	// (rather than upfront in runUp) means a fresh/existing-workstream
	// rejection, model/trust validation failure, or duplicate-live
	// preflight refusal does not mutate the brief.
	if opts.SeedBriefContent != "" {
		if _, err := writeSeedBriefForProfile(t.Project, opts.Profile, opts.Workstream, opts.SeedBriefContent, opts.SeedBriefForce); err != nil {
			return err
		}
	}
	// Ensure the team-home active brief exists once before the backend
	// opens panes. ensureBriefStub is idempotent and preserves any existing
	// brief content (including the seed we may have just written), so this
	// is safe across reruns and parallel member launches.
	if _, _, err := ensureBriefStubForProfile(t.Project, opts.Profile, opts.Workstream); err != nil {
		return fmt.Errorf("ensure brief: %w", err)
	}
	if err := backend.Launch(t, opts); err != nil {
		return err
	}
	quietNotice("started %s using profile %s in %s\n", opts.Workstream, opts.Profile, t.Project)
	if len(preflights) > 0 && preflights[0].Root != "" {
		quietNotice("AM_ROOT: %s\n", preflights[0].Root)
	}
	profileArg := commandProfileArg(opts.Profile)
	quietNotice("next: amq-squad status%s --session %s | amq-squad console%s --session %s | amq-squad stop%s --all --session %s\n",
		profileArg, shellQuote(opts.Workstream), profileArg, shellQuote(opts.Workstream), profileArg, shellQuote(opts.Workstream))
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
	return nil
}

func commandProfileArg(profile string) string {
	profile = strings.TrimSpace(profile)
	if profile == "" || profile == team.DefaultProfile {
		return ""
	}
	return " --profile " + shellQuote(profile)
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
		env, err := resolveAMQEnvForTeamProfile(cwd, opts.Profile, opts.Workstream, m.Handle)
		if err != nil {
			return nil, fmt.Errorf("resolve amq env for %s: %w", m.Handle, err)
		}
		root := absoluteAMQRoot(cwd, env.Root)
		handle := m.Handle
		if env.Me != "" {
			handle = env.Me
		}
		agentDir := filepath.Join(root, "agents", handle)
		out = append(out, agentLaunchPreflight{
			AgentDir:   agentDir,
			Handle:     handle,
			Workstream: env.SessionName,
			Root:       root,
			Binary:     m.Binary,
			Force:      opts.ForceDuplicate,
			DryRun:     opts.DryRun,
		})
	}
	return out, nil
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
		panes = append(panes, teamLaunchPane{
			Role: m.Role,
			CWD:  cwd,
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
				Profile:        opts.Profile,
				WakeInjectVia:  opts.WakeInjectVia,
				WakeInjectArgs: opts.WakeInjectArgs,
			}),
		})
	}
	return panes
}

func maybeFilterCurrentExternalLead(t team.Team, workstream, profile, trustMode string, binaryArgs map[string][]string, modelOverrides map[string]string, write bool) (team.Team, bool, error) {
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
	env, err := resolveAMQEnvForTeamProfile(cwd, profile, workstream, handle)
	if err != nil {
		if !write {
			return t, false, nil
		}
		return t, false, fmt.Errorf("resolve amq env for external lead %s: %w", handle, err)
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
	rec := externalLeadRecordForLaunch(lead, cwd, handle, root, env, id, profile, trustMode, binaryArgs, modelOverrides)
	if err := launch.Write(agentDir, rec); err != nil {
		return t, false, fmt.Errorf("write external lead record: %w", err)
	}
	return filterLaunchMember(t, lead.Role, id.PaneID), true, nil
}

func externalRecordMatchesPane(rec launch.Record, paneID string) bool {
	return rec.External && rec.Tmux != nil && strings.TrimSpace(rec.Tmux.PaneID) == strings.TrimSpace(paneID)
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
	extra := append(binaryArgsFor(m.Binary, binaryArgs), m.ExtraArgs()...)
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
	return "(export " + strings.Join(assignments, " ") + "; " + command + ")"
}

func teamSquadBin() string {
	if p, err := os.Executable(); err == nil {
		return p
	}
	return "amq-squad"
}
