package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/amqexec"
	"github.com/omriariav/amq-squad/v2/internal/catalog"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/role"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

// envTmuxTarget carries the tmux launch target (current-window / new-window /
// new-session) from a team-launch backend into each agent's launcher process,
// so the per-agent launch record can persist how its pane was created. It is
// set only on the live tmux launch paths; manual `agent up` launches leave it
// unset and record an empty target.
const envTmuxTarget = "AMQ_SQUAD_TMUX_TARGET"

var launchPlanObserver func(launch.Record, []string)
var preparedLaunchAfterRecordWrite = func(launch.Record) error { return nil }
var amqSyscallExec = syscall.Exec
var launchCurrentPaneIdentity = tmuxpane.CurrentPaneIdentity

// envTmuxLauncherPane carries the pane id that initiated a managed tmux launch.
// The child process runs in the agent pane, so it cannot recover this later
// from TMUX_PANE. Status uses it to detect same-pane lead collapse.
const envTmuxLauncherPane = "AMQ_SQUAD_TMUX_LAUNCHER_PANE"

const (
	envTerminalBackend    = "AMQ_SQUAD_TERMINAL_BACKEND"
	envTerminalSession    = "AMQ_SQUAD_TERMINAL_SESSION"
	envTerminalWindowID   = "AMQ_SQUAD_TERMINAL_WINDOW_ID"
	envTerminalWindowName = "AMQ_SQUAD_TERMINAL_WINDOW_NAME"
	envTerminalTabID      = "AMQ_SQUAD_TERMINAL_TAB_ID"
	envTerminalSessionID  = "AMQ_SQUAD_TERMINAL_SESSION_ID"
	envTerminalTTY        = "AMQ_SQUAD_TERMINAL_TTY"
	envTerminalTarget     = "AMQ_SQUAD_TERMINAL_TARGET"
)

var launchStdinIsTerminal = stdinIsTerminal

type symphonyInitConfig struct {
	Workflow string
	Root     string
	Me       string
}

var runSymphonyInit = defaultRunSymphonyInit

func defaultRunSymphonyInit(cfg symphonyInitConfig) error {
	cmd := exec.Command("amq", "integration", "symphony", "init",
		"--workflow", cfg.Workflow,
		"--root", cfg.Root,
		"--me", cfg.Me,
	)
	cmd.Env = amqexec.NoUpdateCheckEnv(nil)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	detail := strings.TrimSpace(string(out))
	if detail != "" {
		return fmt.Errorf("amq integration symphony init: %s", detail)
	}
	return fmt.Errorf("amq integration symphony init: %w", err)
}

type stringListFlag []string

func (f *stringListFlag) String() string {
	return strings.Join(*f, " ")
}

func (f *stringListFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

// runLaunch is the real single-agent launcher. The top-level `launch` verb is
// legacy; this body now backs `agent up` (via runAgentUp -> translateAgentUpArgs)
// and the replay path (execRestoreRecord). It is internal-only and carries no
// deprecation surface of its own.
func runLaunch(args []string) error {
	token, err := preparedRunTokenFromInternalEnv()
	if err != nil {
		return err
	}
	return runLaunchWithPreparedToken(args, token)
}

func runLaunchWithPreparedToken(args []string, requestedPreparedToken preparedRunToken) error {
	// Split at "--" so launcher flags aren't consumed by amq-squad's parser.
	squadArgs, childArgs := splitDashDash(args)

	fs := flag.NewFlagSet("launch", flag.ContinueOnError)
	roleFlag := fs.String("role", "", "role label for this agent (e.g. cpo, cto, dev, qa)")
	session := fs.String("session", "", "AMQ session name (passed through to coop exec)")
	sharedWorkstream := fs.Bool("team-workstream", false, "mark --session as the shared amq-squad team workstream")
	me := fs.String("me", "", "override the agent handle (defaults to binary basename)")
	rootFlag := fs.String("root", "", "override AMQ root directory")
	projectFlag := fs.String("project", "", "project/team-home directory to launch from (default: cwd)")
	teamHome := fs.String("team-home", "", "team-home directory used to find .amq-squad/team-rules.md for bootstrap")
	teamProfile := fs.String("team-profile", "", "team profile this launch belongs to (default: default profile)")
	fs.StringVar(teamProfile, "profile", *teamProfile, "alias for --team-profile")
	registerScopedFlagAliases(fs, projectFlag, session, teamProfile)
	conversation := fs.String("conversation", "", "resume and store a Codex or Claude conversation name/id")
	conversationID := fs.String("conversation-id", "", "alias for --conversation")
	noBootstrap := fs.Bool("no-bootstrap", false, "do not pass the generated bootstrap prompt to the agent")
	noDefaultArgs := fs.Bool("no-default-args", false, "do not prepend Codex or Claude default permission args")
	noPreauthInScope := fs.Bool("no-preauthorize-inscope", false, "do not pre-authorize gh pr create for an orchestrated Claude worker (#296; feature-branch push is not pre-authorized in this slice)")
	trustRaw := fs.String("trust", "", "Codex trust profile: approve-for-me (default), sandboxed, or trusted (local power mode)")
	model := fs.String("model", "", "native model name to pass to the agent binary, e.g. 'gpt-5.6-terra' or 'sonnet'")
	toolProfile := fs.String("tool-profile", "", "effective role capability policy (minimal|coding|browser|data|full|custom)")
	toolConfig := fs.String("tool-config", "", "binary-native policy config: Claude settings path or Codex profile name")
	toolMCPConfig := fs.String("tool-mcp-config", "", "Claude strict MCP config generated for the role")
	var toolAllowlist stringListFlag
	var toolBlocklist stringListFlag
	fs.Var(&toolAllowlist, "tool-allow", "audited enabled tool entry (repeatable)")
	fs.Var(&toolBlocklist, "tool-block", "audited revoked tool entry (repeatable; materialized at launch)")
	spawnOrigin := fs.String("spawn-origin", "", "runtime composition origin recorded in launch.json")
	spawnDepth := fs.Int("spawn-depth", 0, "runtime composition depth recorded in launch.json")
	codexArgsRaw := fs.String("codex-args", "", "extra Codex args to treat as launch defaults, e.g. '--enable goals'")
	claudeArgsRaw := fs.String("claude-args", "", "extra Claude args to treat as launch defaults, e.g. '--chrome'")
	forceDuplicate := fs.Bool("force-duplicate", false, "launch even when a live agent for the same handle/workstream is detected")
	noRequireWake := fs.Bool("no-require-wake", false, "do not pass --require-wake to amq coop exec (allows launching when the wake sidecar cannot acquire its lock)")
	noGitignore := fs.Bool("no-gitignore", false, "pass --no-gitignore to amq coop exec (leave .gitignore unchanged during AMQ auto-init)")
	symphony := fs.Bool("symphony", false, "Codex only: patch the existing WORKFLOW.md with AMQ Symphony lifecycle hooks for this resolved root and handle")
	wakeInjectVia := fs.String("wake-inject-via", "", "absolute executable passed to amq coop exec --wake-inject-via")
	wakeInjectMode := fs.String("wake-inject-mode", "", "wake injection mode forwarded to amq coop exec: auto, raw, paste, or none")
	var wakeInjectArgs stringListFlag
	fs.Var(&wakeInjectArgs, "wake-inject-arg", "argument passed to amq coop exec --wake-inject-arg (repeatable; requires --wake-inject-via)")
	dryRun := fs.Bool("dry-run", false, "print the coop exec command without executing")
	launcherRaw := fs.String("launcher", "", "custom launcher to exec instead of <binary> (still receives AMQ env/identity, bootstrap, and a launch record)")
	launcherArgsRaw := fs.String("launcher-args", "", "args passed to --launcher before the agent's child args; the launcher must forward trailing args to <binary>")
	restoreGoalBindingRaw := fs.String("restore-goal-binding", "", "internal restore-only goal binding metadata")

	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad agent up - launch an agent with role metadata

Usage:
  amq-squad agent up <binary> [--project DIR] [options] [-- <binary-flags>]

Options:
`)
		fs.PrintDefaults()
		fmt.Fprint(os.Stderr, `
The <binary> is the agent launcher (claude, codex, etc). Flags after "--"
are passed through to that binary via 'amq coop exec'.

Side effects before exec:
  1. Resolves AMQ env via 'amq env --json' for the target session.
  2. Refuses by default when a live agent for the same handle/workstream is
     already running. Override with --force-duplicate.
  3. Writes launch.json under the amq-squad extension namespace.
  4. Writes a role.md stub if one does not already exist.
  5. Prepends Claude default permission args. Codex defaults to Auto plus
     auto_review for --trust approve-for-me, stays empty for --trust sandboxed,
     and uses the bypass flag only for --trust trusted. --no-default-args opts
     out of all built-in defaults.
  6. Inserts --model <name> for codex or claude when resolved from CLI,
     amq-squad config, or local Codex config.
  7. Prepends --codex-args or --claude-args for the matching binary.
  8. Translates --conversation for Codex or Claude resume when provided.
  9. Adds a generated bootstrap prompt unless --no-bootstrap is set or
     non-default binary args were provided.
 10. With --symphony (Codex only), patches the existing <cwd>/WORKFLOW.md with
     AMQ Symphony lifecycle hooks pinned to the resolved AMQ root and handle.
     If WORKFLOW.md is absent, the AMQ adapter error is returned and launch
     stops; amq-squad does not create the file itself.
 11. Execs 'amq coop exec --session <session> <binary> -- <binary-flags>'.
     With supported amq versions, --require-wake is passed so the launch fails at the
     door when the wake sidecar cannot start and acquire its lock (instead
     of surfacing as a stale/orphaned wake later). --no-require-wake opts
     out for environments where wake cannot run but the agent should.
     --wake-inject-via and repeatable --wake-inject-arg are forwarded to
     amq coop exec so AMQ can save a repairable external wake target.
     --wake-inject-mode none keeps wake notices on stderr and guarantees zero
     synthetic terminal input; it cannot be combined with injector flags.

With --dry-run, the resolved coop exec command is printed and amq-squad exits.
Disk state is untouched and no exec occurs.
--project targets another team-home without changing directories; launch records
and relative AMQ config resolution behave as if the command ran from DIR.

When --conversation generates resume args, do not pass additional child args
after "--". Use --codex-args or --claude-args for native flags that should
still combine with --conversation.

Examples:
  amq-squad agent up codex --role cto --session issue-96
  amq-squad agent up codex --project ~/Code/app --session issue-96
  amq-squad agent up codex --dry-run --no-bootstrap
`)
	}

	if err := parseFlags(fs, squadArgs); err != nil {
		return err
	}
	if flagWasSet(fs, "project") {
		project, rest, err := peelProjectFlag(args)
		if err != nil {
			return err
		}
		return runInProject(project, func() error {
			return runLaunchWithPreparedToken(rest, requestedPreparedToken)
		})
	}
	trustExplicit := flagWasSet(fs, "trust")
	trustMode, err := normalizeTrustMode(*trustRaw)
	if err != nil {
		return err
	}
	conversationRef, err := conversationRefFromFlags(*conversation, *conversationID)
	if err != nil {
		return err
	}
	binaryArgs, err := parseBinaryArgFlags(*codexArgsRaw, *claudeArgsRaw)
	if err != nil {
		return err
	}
	if err := validateTrustCombination(trustMode, trustExplicit, *noDefaultArgs, binaryArgs); err != nil {
		return err
	}
	if *spawnDepth < 0 {
		return usageErrorf("--spawn-depth cannot be negative")
	}
	wakeInjectViaValue := strings.TrimSpace(*wakeInjectVia)
	wakeInjectArgValues := append([]string(nil), wakeInjectArgs...)
	wakeInjectModeValue, err := normalizeWakeInjectMode(*wakeInjectMode)
	if err != nil {
		return err
	}
	if err := validateWakeInjectConfig(wakeInjectModeValue, wakeInjectViaValue, wakeInjectArgValues, ""); err != nil {
		return err
	}
	if wakeInjectViaValue != "" && !filepath.IsAbs(wakeInjectViaValue) {
		return usageErrorf("--wake-inject-via must be an absolute path")
	}
	launcher := strings.TrimSpace(*launcherRaw)
	var launcherArgs []string
	if strings.TrimSpace(*launcherArgsRaw) != "" {
		launcherArgs, err = parseAgentArgs(*launcherArgsRaw)
		if err != nil {
			return fmt.Errorf("parse --launcher-args: %w", err)
		}
	}
	if launcher == "" && len(launcherArgs) > 0 {
		return usageErrorf("--launcher-args requires --launcher")
	}
	var restoredGoalBinding *launch.GoalBinding
	if strings.TrimSpace(*restoreGoalBindingRaw) != "" {
		var binding launch.GoalBinding
		if err := json.Unmarshal([]byte(*restoreGoalBindingRaw), &binding); err != nil {
			return usageErrorf("--restore-goal-binding contains invalid JSON: %v", err)
		}
		restoredGoalBinding = &binding
	}
	remaining := fs.Args()
	if len(remaining) == 0 {
		return usageErrorf("agent up requires a binary (e.g. 'amq-squad agent up codex --role cpo')")
	}
	binary := remaining[0]
	effectiveToolProfile := strings.TrimSpace(*toolProfile)
	if effectiveToolProfile == "" {
		effectiveToolProfile = team.ToolProfileFull
	}
	switch effectiveToolProfile {
	case team.ToolProfileMinimal, team.ToolProfileCoding, team.ToolProfileBrowser, team.ToolProfileData, team.ToolProfileFull, team.ToolProfileCustom:
	default:
		return usageErrorf("--tool-profile must be minimal, coding, browser, data, full, or custom")
	}
	if effectiveToolProfile != team.ToolProfileFull && strings.TrimSpace(*toolConfig) == "" {
		return usageErrorf("--tool-config is required for non-full tool profile %q", effectiveToolProfile)
	}
	wakeInjectModeValue = resolveWakeInjectModeForBinary(wakeInjectModeValue, binary)
	if *symphony && normalizedAgentBinary(binary) != "codex" {
		return usageErrorf("--symphony is only supported for Codex agents; got %s", binary)
	}
	// Positional args before "--" get folded into childArgs.
	if len(remaining) > 1 {
		childArgs = append(remaining[1:], childArgs...)
	}
	toolMember := team.Member{Binary: binary, ToolProfile: effectiveToolProfile, ToolConfig: strings.TrimSpace(*toolConfig), ToolMCPConfig: strings.TrimSpace(*toolMCPConfig), ToolAllowlist: append([]string(nil), toolAllowlist...), ToolBlocklist: append([]string(nil), toolBlocklist...)}
	extraDefaultArgs := composeBinaryArgs(binary, toolMember.ToolArgs(), binaryArgsFor(binary, binaryArgs))
	resolvedModel := resolveModelForLaunch(binary, *model, extraDefaultArgs)
	modelArgs := modelArgsForBinary(binary, resolvedModel)
	defaultArgs := launchDefaultChildArgsWithTrust(binary, !*noDefaultArgs, modelArgs, extraDefaultArgs, trustMode)
	childArgs = ensureLeadingChildArgs(defaultArgs, childArgs)
	if conversationRef != "" {
		var err error
		childArgs, err = applyConversationRestoreArgsWithDefaults(binary, childArgs, conversationRef, defaultArgs)
		if err != nil {
			return err
		}
	}
	if restoredGoalBinding != nil && goalBindingFromArgs(binary, childArgs) != nil {
		return usageErrorf("--restore-goal-binding cannot be combined with a goal child argument")
	}

	handle := *me
	if handle == "" {
		handle = strings.ToLower(filepath.Base(binary))
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	profileExplicit := flagWasSet(fs, "profile") || flagWasSet(fs, "team-profile")
	resolvedContext, err := resolveCanonicalContext(contextResolveOptions{
		ProfileFlag: strings.TrimSpace(*teamProfile), SessionFlag: *session, HandleFlag: handle, RootFlag: *rootFlag,
		ProfileExplicit: profileExplicit, SessionExplicit: flagWasSet(fs, "session"), HandleExplicit: flagWasSet(fs, "me"), RootExplicit: flagWasSet(fs, "root") && !flagWasSet(fs, "session"),
	})
	if err != nil {
		return err
	}
	emitContextDiagnostics(resolvedContext)
	teamProfileValue := resolvedContext.Profile
	if !flagWasSet(fs, "session") && resolvedContext.Sources["session"] != contextSourceDefault {
		*session = resolvedContext.Session
	}
	if !flagWasSet(fs, "root") && resolvedContext.Sources["root"] != contextSourceDefault {
		*rootFlag = resolvedContext.Root
	}
	if !flagWasSet(fs, "me") && resolvedContext.Handle != "" {
		handle = resolvedContext.Handle
	}
	rootForLaunch := launchRootFromFlags(cwd, *rootFlag, *session, teamProfileValue)

	// Resolve the AMQ env via the amq CLI so launch.json and the actual
	// mailbox agree. Named profiles use their isolated profile root even when
	// the launcher only supplied --team-profile and --session; otherwise AMQ's
	// --session shorthand recreates the legacy/default .agent-mail/<session>
	// root before the agent can bootstrap.
	env, err := resolveAMQEnvForLaunch(cwd, rootForLaunch, *session, teamProfileValue, handle)
	if err != nil {
		return fmt.Errorf("resolve amq env: %w", err)
	}
	root := env.Root
	if env.Me != "" {
		handle = env.Me
	}
	if err := ensureLaunchTargetIsNotOperator(cwd, teamProfileValue, "agent up", *roleFlag, handle); err != nil {
		return err
	}

	// #296: pre-authorize `gh pr create` for an orchestrated Claude worker so
	// creating its PR never blocks on a permission prompt (the recurring stall this
	// milestone removes). Scoped to configured non-lead orchestrated Claude
	// workers. Feature-branch push is intentionally NOT pre-authorized in this
	// slice (no safe pattern form; see claudeInScopePreauthAllowlist); main push,
	// tags, and releases always stay gated.
	var preauthorizedActions []string
	explicitAllowedTools := childArgsAllowedTools(childArgs)
	launcherPreauthorizedActions := claudeLauncherPreauthActions(launchPreauthProjectDir(cwd, *teamHome), teamProfileValue, *roleFlag, binary, env.SessionName, !*noPreauthInScope)
	childArgs, preauthorizedActions, _ = applyClaudeWorkerPreauthActions(childArgs, launcherPreauthorizedActions)

	agentDir := filepath.Join(root, "agents", handle)
	rec := launch.Record{
		CWD:                          cwd,
		Binary:                       binary,
		Argv:                         childArgs,
		Session:                      env.SessionName,
		SharedWorkstream:             *sharedWorkstream,
		Conversation:                 conversationRef,
		Handle:                       handle,
		Role:                         *roleFlag,
		Root:                         root,
		BaseRoot:                     env.BaseRoot,
		RootSource:                   env.RootSource,
		AMQVersion:                   env.AMQVersion,
		CodexArgs:                    binaryArgs["codex"],
		ClaudeArgs:                   binaryArgs["claude"],
		Launcher:                     launcher,
		LauncherArgs:                 launcherArgs,
		Model:                        resolvedModel,
		ToolProfile:                  effectiveToolProfile,
		ToolConfig:                   strings.TrimSpace(*toolConfig),
		ToolMCPConfig:                strings.TrimSpace(*toolMCPConfig),
		ToolAllowlist:                append([]string(nil), toolAllowlist...),
		ToolBlocklist:                append([]string(nil), toolBlocklist...),
		Trust:                        trustMode,
		NoDefaultArgs:                *noDefaultArgs,
		NoPreauthorizeInScope:        *noPreauthInScope,
		SpawnOrigin:                  strings.TrimSpace(*spawnOrigin),
		SpawnDepth:                   *spawnDepth,
		NoRequireWake:                *noRequireWake,
		NoGitignore:                  *noGitignore,
		Symphony:                     *symphony,
		GoalBinding:                  restoredGoalBinding,
		PreauthorizedActions:         preauthorizedActions,
		LauncherPreauthorizedActions: launcherPreauthorizedActions,
		ExplicitAllowedTools:         explicitAllowedTools,
		WakeInjectVia:                wakeInjectViaValue,
		WakeInjectArgs:               wakeInjectArgValues,
		WakeInjectMode:               wakeInjectModeValue,
		AgentPID:                     os.Getpid(),
		AgentTTY:                     currentLaunchTTY(),
		StartedAt:                    time.Now().UTC(),
		TeamProfile:                  teamProfileValue,
		TeamHome:                     strings.TrimSpace(*teamHome),
	}
	if rec.GoalBinding == nil {
		rec.GoalBinding = goalBindingFromArgs(binary, childArgs)
	}
	if rec.TeamHome == "" {
		rec.TeamHome = rec.CWD
	}
	if !requestedPreparedToken.empty() && !requestedPreparedToken.complete() {
		return fmt.Errorf("agent up refused: prepared run token is incomplete")
	}
	applyPreparedRunTokenToRecord(&rec, requestedPreparedToken)
	// A live prepared launch enters both the namespace writer domain and the
	// prepared-manifest reader domain before reading accepted state. Preparation
	// writers take the matching exclusive manifest admission, so the accepted
	// generation cannot change through record write or exec.
	if !*dryRun && strings.TrimSpace(env.SessionName) != "" {
		admissionProject := rec.TeamHome
		if !filepath.IsAbs(admissionProject) {
			admissionProject = filepath.Join(cwd, admissionProject)
		}
		admissionProject = filepath.Clean(admissionProject)
		initialIdentity, err := captureNamespaceEndpointIdentity(squadnamespace.Resolve(admissionProject, teamProfileValue, env.SessionName), handle)
		if err != nil {
			return err
		}
		admission, err := acquireNamespaceWriterAdmission(admissionProject, teamProfileValue, env.SessionName)
		if err != nil {
			return err
		}
		defer admission.close()
		currentContext, err := resolveCanonicalContext(contextResolveOptions{
			ProfileFlag: strings.TrimSpace(*teamProfile), SessionFlag: *session, HandleFlag: handle, RootFlag: *rootFlag,
			ProfileExplicit: profileExplicit, SessionExplicit: flagWasSet(fs, "session"), HandleExplicit: flagWasSet(fs, "me"), RootExplicit: flagWasSet(fs, "root") && !flagWasSet(fs, "session"),
		})
		if err != nil {
			return fmt.Errorf("agent up refused: context re-resolution under admission failed: %w", err)
		}
		if err := validateReResolvedContext(resolvedContext, currentContext, false); err != nil {
			return err
		}
		currentIdentity, err := captureNamespaceEndpointIdentity(squadnamespace.Resolve(admissionProject, currentContext.Profile, currentContext.Session), currentContext.Handle)
		if err != nil {
			return err
		}
		if err := validateReResolvedEndpointIdentity("agent up", initialIdentity, currentIdentity); err != nil {
			return err
		}
		currentRootForLaunch := launchRootFromFlags(cwd, *rootFlag, *session, currentContext.Profile)
		currentEnv, err := resolveAMQEnvForLaunch(cwd, currentRootForLaunch, *session, currentContext.Profile, currentContext.Handle)
		if err != nil {
			return fmt.Errorf("agent up refused: AMQ identity re-resolution under admission failed: %w", err)
		}
		currentHandle := currentContext.Handle
		if currentEnv.Me != "" {
			currentHandle = currentEnv.Me
		}
		if filepath.Clean(absoluteAMQRoot(cwd, currentEnv.Root)) != filepath.Clean(root) || strings.TrimSpace(currentEnv.SessionName) != strings.TrimSpace(env.SessionName) || currentHandle != handle {
			return fmt.Errorf("agent up refused: AMQ launch identity changed before admission; retry")
		}
		if err := ensureLaunchTargetIsNotOperator(admissionProject, currentContext.Profile, "agent up", *roleFlag, currentHandle); err != nil {
			return err
		}
		if err := ensureNoNamespaceConflict("agent up", admissionProject, teamProfileValue, env.SessionName, profileExplicit); err != nil {
			return err
		}
		manifestAdmission, err := acquirePreparedManifestReaderAdmission(admissionProject, teamProfileValue, env.SessionName)
		if err != nil {
			return err
		}
		defer manifestAdmission.close()
	}
	if !requestedPreparedToken.empty() {
		manifestProject := strings.TrimSpace(rec.TeamHome)
		if manifestProject == "" {
			manifestProject = strings.TrimSpace(rec.CWD)
		}
		manifest, digest, err := readPreparedRunManifestSnapshot(manifestProject, rec.TeamProfile, rec.Session)
		if err != nil {
			return fmt.Errorf("agent up refused: read pinned prepared run identity: %w", err)
		}
		if err := validatePreparedRunToken(requestedPreparedToken, manifest, digest); err != nil {
			return fmt.Errorf("agent up refused: %w", err)
		}
	}
	preparedLaunchContext, err := preparedContextForLaunchRecord(rec)
	if err != nil {
		return fmt.Errorf("load accepted prepared launch identity: %w", err)
	}
	if !requestedPreparedToken.empty() {
		if preparedLaunchContext == nil {
			return fmt.Errorf("agent up refused: pinned prepared run identity disappeared")
		}
		if err := validatePreparedRunToken(requestedPreparedToken, preparedLaunchContext.Manifest, preparedLaunchContext.Digest); err != nil {
			return fmt.Errorf("agent up refused: %w", err)
		}
	}
	if requestedPreparedToken.empty() {
		applyPreparedRunTokenToRecord(&rec, preparedRunTokenForContext(preparedLaunchContext))
	}
	if rec.GoalBinding == nil && rec.Conversation == "" && preparedLaunchContext != nil && preparedLaunchContext.Member.Role == preparedLaunchContext.Team.Lead {
		preparedBinding, err := preparedGoalBinding(preparedLaunchContext.Team, preparedLaunchContext.Manifest.Profile, preparedLaunchContext.Manifest.Session, preparedLaunchContext.Member, preparedLaunchContext.Binding)
		if err != nil {
			return fmt.Errorf("load accepted prepared goal binding: %w", err)
		}
		rec.GoalBinding = preparedBinding
	}

	// Capture exact tmux identity (session/window/pane ids) when launched
	// inside tmux, so clients can target follow-up control by stable pane id
	// instead of re-inferring from window names. Best-effort: a capture failure
	// must never block the launch. This runs before exec while $TMUX/$TMUX_PANE
	// still describe this agent's pane.
	if id, err := launchCurrentPaneIdentity(); err == nil && id != nil {
		target := strings.TrimSpace(os.Getenv(envTmuxTarget))
		launcherPane := strings.TrimSpace(os.Getenv(envTmuxLauncherPane))
		if launcherPane == "" && target == "" {
			launcherPane = id.PaneID
		}
		rec.AdoptionMode = launchAdoptionMode(target, launcherPane, id.PaneID)
		rec.LauncherPaneID = launcherPane
		rec.Tmux = &launch.TmuxInfo{
			Session:    id.Session,
			WindowID:   id.WindowID,
			WindowName: id.WindowName,
			PaneID:     id.PaneID,
			Target:     target,
		}
		rec.Terminal = launch.TerminalInfoFromTmux(rec.Tmux)
	}
	if rec.Terminal == nil {
		rec.Terminal = terminalInfoFromEnv()
		if rec.Terminal != nil {
			rec.AdoptionMode = launchAdoptionMode(rec.Terminal.Target, "", "")
		}
	}
	// Keep generated bootstrap out of launch.json so restore stays compact
	// and does not replay stale startup text.
	effectiveChildArgs := append([]string(nil), childArgs...)
	bootstrapEligibilityArgs := childArgs
	if len(launcherPreauthorizedActions) > 0 {
		if len(explicitAllowedTools) > 0 {
			bootstrapEligibilityArgs = replaceClaudeAllowedTools(childArgs, explicitAllowedTools)
		} else {
			bootstrapEligibilityArgs = stripRecordedLauncherPreauth(childArgs, preauthorizedActions)
		}
	}
	bootstrapAppended := !*noBootstrap && shouldAppendBootstrapWithDefaults(bootstrapEligibilityArgs, defaultArgs)
	bootstrapSuppressedReason := ""
	var preparedPrompt string
	if bootstrapAppended {
		boundary, err := assessNativePromptBoundary(binary, bootstrapEligibilityArgs)
		if err != nil {
			return err
		}
		if !boundary.Safe {
			bootstrapAppended = false
			bootstrapSuppressedReason = boundary.Reason
		}
	}
	expectation, err := bootstrapExpectationForLaunch(rec, bootstrapAppended, *noBootstrap, bootstrapSuppressedReason)
	if err != nil {
		return err
	}
	if *dryRun && bootstrapAppended && !expectation.Required {
		if preparedLaunchContext != nil && !(preparedLaunchContext.Manifest.Topology.ExternalLead && rec.Role == preparedLaunchContext.Team.Lead) {
			expectation.Required = true
			expectation.NotRequiredReason = ""
		}
	}
	rec.BootstrapExpectation = &expectation
	if bootstrapAppended {
		bootstrapContext := bootstrapContextFor(rec, agentDir, *teamHome)
		if preparedLaunchContext != nil {
			bootstrapContext.CurrentTeam, bootstrapContext.Warnings = bootstrapCurrentTeamWithRoster(rec, *teamHome, true)
		}
		prompt, err := buildBootstrapPrompt(bootstrapContext)
		if err != nil {
			return err
		}
		if err := revalidatePreparedBootstrapPromptForLaunch(rec, prompt, preparedLaunchContext); err != nil {
			return fmt.Errorf("prepared bootstrap launch validation: %w", err)
		}
		preparedPrompt = prompt
		// Terminate native option parsing so optional/variadic flags can never
		// consume generated prompt text. The prompt remains the final argv token.
		effectiveChildArgs = appendGeneratedBootstrapPrompt(effectiveChildArgs, prompt)
	} else if preparedLaunchContext != nil {
		return fmt.Errorf("prepared bootstrap launch validation: accepted run member %s cannot launch without its exact bootstrap prompt", rec.Role)
	} else if context, err := preparedContextForLaunchRecord(rec); err != nil {
		return fmt.Errorf("prepared bootstrap launch validation: %w", err)
	} else if context != nil {
		return fmt.Errorf("prepared bootstrap launch validation: prepared run appeared after launch identity capture")
	}
	revalidatePrepared := func(stage string) error {
		if preparedLaunchContext == nil {
			return nil
		}
		if err := revalidatePreparedBootstrapPromptForLaunch(rec, preparedPrompt, preparedLaunchContext); err != nil {
			return fmt.Errorf("prepared launch changed before %s: accepted prepared launch identity no longer matches: %w", stage, err)
		}
		return nil
	}
	if err := revalidatePrepared("launch plan observer"); err != nil {
		return err
	}
	if launchPlanObserver != nil {
		launchPlanObserver(rec, append([]string(nil), effectiveChildArgs...))
	}

	// Build the coop exec invocation. Done before any disk writes so
	// --dry-run is a true preview with zero side effects. --session NAME
	// is amq shorthand for --root .agent-mail/<name>; passing both is
	// rejected by amq, so prefer --session when callers supplied both
	// (matching the resolveAMQEnvInDir boundary policy). The same warning
	// fires once at env resolution time, so this branch stays silent to
	// avoid duplicating the message.
	exactRootPin := launchUsesExplicitRoot(rootForLaunch, *session, teamProfileValue)
	coopArgs := []string{"coop", "exec"}
	if exactRootPin {
		coopArgs = append(coopArgs, "--root", rootForLaunch)
	} else if *session != "" {
		coopArgs = append(coopArgs, "--session", *session)
	} else if rootForLaunch != "" {
		coopArgs = append(coopArgs, "--root", rootForLaunch)
	}
	if *me != "" {
		coopArgs = append(coopArgs, "--me", *me)
	} else if exactRootPin {
		// The exact-root child shim changes the executable AMQ sees from the
		// agent binary to `env`. Keep handle derivation tied to the already
		// resolved agent identity instead of letting AMQ derive "env".
		coopArgs = append(coopArgs, "--me", handle)
	}
	// Fail the launch at the door when the wake sidecar cannot start and
	// acquire its lock, instead of detecting a missing/orphaned wake later
	// (#30). Version-gated: amq grew --require-wake in 0.34.1, and an empty
	// or unparseable reported version omits the flag so older amq builds
	// never see an unknown flag. --no-require-wake is the escape hatch for
	// TIOCSTI-hostile environments where wake can't acquire its lock but the
	// operator wants the agent anyway.
	if !*noRequireWake && amqSupportsRequireWake(env.AMQVersion) {
		coopArgs = append(coopArgs, "--require-wake")
	}
	if *noGitignore {
		if !amqSupportsNoGitignore(env.AMQVersion) {
			return fmt.Errorf("--no-gitignore requires amq %s or newer (found %s)", minNoGitignoreAMQVersion, versionOrUnknown(env.AMQVersion))
		}
		coopArgs = append(coopArgs, "--no-gitignore")
	}
	if wakeInjectViaValue != "" {
		if !amqSupportsWakeInject(env.AMQVersion) {
			return fmt.Errorf("--wake-inject-via requires amq %s or newer (found %s)", minWakeInjectAMQVersion, versionOrUnknown(env.AMQVersion))
		}
		coopArgs = append(coopArgs, "--wake-inject-via", wakeInjectViaValue)
		for _, arg := range wakeInjectArgValues {
			coopArgs = append(coopArgs, "--wake-inject-arg="+arg)
		}
	}
	if wakeInjectModeValue != "" {
		if !amqSupportsWakeInjectMode(env.AMQVersion) {
			return fmt.Errorf("--wake-inject-mode requires amq %s or newer (found %s)", minWakeInjectModeAMQVersion, versionOrUnknown(env.AMQVersion))
		}
		coopArgs = append(coopArgs, "--wake-inject-mode", wakeInjectModeValue)
	}
	// A custom launcher is exec'd in place of the binary. Launcher args precede
	// the agent's normal child args; the launcher is expected to forward the
	// trailing args to the binary so bootstrap and default args still reach it.
	target := binary
	trailing := effectiveChildArgs
	if launcher != "" {
		target = launcher
		trailing = append(append([]string(nil), launcherArgs...), effectiveChildArgs...)
	}
	if exactRootPin {
		target, trailing = exactRootChildCommand(target, trailing)
	}
	coopArgs = append(coopArgs, target)
	if len(trailing) > 0 {
		coopArgs = append(coopArgs, "--")
		coopArgs = append(coopArgs, trailing...)
	}

	if *dryRun {
		fmt.Println(shellCommand("amq", coopArgs...))
		if *symphony {
			fmt.Fprintf(os.Stderr, "(dry run - would patch existing %s with AMQ Symphony hooks pinned to root %s and handle %s; no files written)\n",
				filepath.Join(cwd, "WORKFLOW.md"), root, handle)
		}
		quietNotice("(dry run - no files written, not execing)\n")
		verbosePolicyEcho()
		return nil
	}
	if err := validateManagedTmuxLaunch(rec); err != nil {
		return err
	}

	if launcher != "" {
		if err := ensureLauncherExecutable(launcher); err != nil {
			return err
		}
	}
	if wakeInjectViaValue != "" {
		if err := ensureLauncherExecutable(wakeInjectViaValue); err != nil {
			return fmt.Errorf("wake inject executable: %w", err)
		}
	}

	preflight := agentLaunchPreflight{
		AgentDir:   agentDir,
		Handle:     handle,
		Workstream: env.SessionName,
		Root:       root,
		Binary:     binary,
		Force:      *forceDuplicate,
	}
	if blocker, err := preflight.check(defaultDuplicateLaunchProbe); err != nil {
		return err
	} else if blocker != nil {
		return blocker
	}

	// Ensure the active brief stub exists for this workstream before the
	// launch record is written, so a brief-creation failure does not leave
	// a fresh launch.json for an agent that never started. resolveBriefHome
	// applies the same skip rule bootstrap uses (explicit --team-home or
	// cwd-with-team-rules-md only) so the two sources stay aligned.
	if briefHome := resolveBriefHome(*teamHome, cwd); briefHome != "" {
		if err := revalidatePrepared("brief preparation"); err != nil {
			return err
		}
		if _, _, err := ensureBriefStubForProfile(briefHome, rec.TeamProfile, rec.Session); err != nil {
			return fmt.Errorf("ensure brief: %w", err)
		}
	}

	if rec.Symphony {
		if err := revalidatePrepared("symphony initialization"); err != nil {
			return err
		}
		workflow := filepath.Join(cwd, "WORKFLOW.md")
		if err := runSymphonyInit(symphonyInitConfig{Workflow: workflow, Root: root, Me: handle}); err != nil {
			return err
		}
		quietNotice("symphony: patched %s with AMQ lifecycle hooks for %s (root %s)\n", workflow, handle, root)
	}

	if err := revalidatePrepared("record write"); err != nil {
		return err
	}
	recordWrite, err := writeLaunchRecordWithSnapshot(agentDir, rec)
	if err != nil {
		return fmt.Errorf("write launch record: %w", err)
	}
	rollbackLaunchRecord := func(cause error) error {
		_, rollbackErr := rollbackLaunchRecordIfCurrent(agentDir, recordWrite)
		if rollbackErr != nil {
			return fmt.Errorf("%w; launch record rollback failed: %v", cause, rollbackErr)
		}
		return cause
	}
	if err := preparedLaunchAfterRecordWrite(rec); err != nil {
		return rollbackLaunchRecord(err)
	}
	if err := revalidatePrepared("post-write launch record admission"); err != nil {
		return rollbackLaunchRecord(err)
	}

	// Seed role.md from the catalog when the role is known, or from a staged
	// custom-role document under the team-home. Never overwrites user edits.
	if *roleFlag != "" {
		if err := revalidatePrepared("role seed"); err != nil {
			return rollbackLaunchRecord(err)
		}
		roleHome := resolveBriefHome(*teamHome, cwd)
		if err := seedRoleStub(agentDir, *roleFlag, roleHome); err != nil {
			fmt.Fprintf(os.Stderr, "warning: seed role.md: %v\n", err)
		}
	}
	if err := revalidatePrepared("session rename scheduling"); err != nil {
		return rollbackLaunchRecord(err)
	}
	if err := maybeScheduleClaudeSessionRename(rec); err != nil {
		fmt.Fprintf(os.Stderr, "warning: schedule Claude session rename: %v\n", err)
	}

	amqBin, err := exec.LookPath("amq")
	if err != nil {
		return fmt.Errorf("amq not found in PATH: %w", err)
	}
	if err := revalidatePrepared("exec"); err != nil {
		return rollbackLaunchRecord(err)
	}
	// Strip inherited AMQ identity vars before exec'ing coop exec. We already
	// resolved the right --root/--session/--me on the command line; passing a
	// stale AM_ROOT/AM_ME from the launching shell along to the agent would
	// re-create the identity-leak asymmetry #46 closed for env resolution.
	return execAMQCoop(amqBin, coopArgs)
}

type launchRecordWriteSnapshot struct {
	Previous *launch.Record
	Written  launch.Record
}

func writeLaunchRecordWithSnapshot(agentDir string, rec launch.Record) (launchRecordWriteSnapshot, error) {
	var snapshot launchRecordWriteSnapshot
	err := launch.WithRecordLock(agentDir, func() error {
		previous, err := launch.Read(agentDir)
		switch {
		case err == nil:
			snapshot.Previous = &previous
		case !os.IsNotExist(err):
			return fmt.Errorf("snapshot existing launch record: %w", err)
		}
		if err := launch.WriteUnderRecordLock(agentDir, rec); err != nil {
			return err
		}
		written, err := launch.Read(agentDir)
		if err != nil {
			return fmt.Errorf("read written launch record: %w", err)
		}
		snapshot.Written = written
		return nil
	})
	return snapshot, err
}

func rollbackLaunchRecordIfCurrent(agentDir string, snapshot launchRecordWriteSnapshot) (bool, error) {
	applied := false
	err := launch.WithRecordLock(agentDir, func() error {
		current, err := launch.Read(agentDir)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read current launch record: %w", err)
		}
		if !reflect.DeepEqual(current, snapshot.Written) {
			return nil
		}
		if snapshot.Previous != nil {
			if err := launch.WriteUnderRecordLock(agentDir, *snapshot.Previous); err != nil {
				return err
			}
			applied = true
			return nil
		}
		if err := os.Remove(launch.Path(agentDir)); err != nil && !os.IsNotExist(err) {
			return err
		}
		applied = true
		return nil
	})
	return applied, err
}

// exactRootChildCommand removes AM_SESSION at the final child boundary for a
// named-profile exact-root launch. AMQ 0.43.1 correctly selects the explicit
// root but exports AM_SESSION as an empty variable; amq-squad's context
// contract deliberately distinguishes an omitted session from an explicitly
// empty, malformed identity. Running the real target through env -u preserves
// AMQ's wake/coop setup while ensuring the exec'd agent receives the canonical
// exact-root tuple. All managed up, wizard/run-start, resume, and dynamic member
// paths converge through runLaunch, so the correction lives at one boundary.
func exactRootChildCommand(target string, trailing []string) (string, []string) {
	args := make([]string, 0, len(trailing)+3)
	args = append(args, "-u", "AM_SESSION", target)
	args = append(args, trailing...)
	return "env", args
}

func execAMQCoop(amqBin string, coopArgs []string) error {
	env := amqexec.NoUpdateCheckEnv(envWithoutPreparedRunToken(envWithoutAMQIdentity(os.Environ())))
	return amqSyscallExec(amqBin, append([]string{"amq"}, coopArgs...), env)
}

func terminalInfoFromEnv() *launch.TerminalInfo {
	info := &launch.TerminalInfo{
		Backend:    strings.TrimSpace(os.Getenv(envTerminalBackend)),
		Session:    strings.TrimSpace(os.Getenv(envTerminalSession)),
		WindowID:   strings.TrimSpace(os.Getenv(envTerminalWindowID)),
		WindowName: strings.TrimSpace(os.Getenv(envTerminalWindowName)),
		TabID:      strings.TrimSpace(os.Getenv(envTerminalTabID)),
		SessionID:  strings.TrimSpace(os.Getenv(envTerminalSessionID)),
		TTY:        strings.TrimSpace(os.Getenv(envTerminalTTY)),
		Target:     strings.TrimSpace(os.Getenv(envTerminalTarget)),
	}
	if info.Backend == "" && info.Session == "" && info.WindowID == "" && info.WindowName == "" && info.TabID == "" && info.SessionID == "" && info.TTY == "" && info.Target == "" {
		return nil
	}
	return info
}

func validateManagedTmuxLaunch(rec launch.Record) error {
	target := strings.TrimSpace(os.Getenv(envTmuxTarget))
	if target == "" {
		return nil
	}
	if os.Getenv("TMUX") == "" || strings.TrimSpace(os.Getenv("TMUX_PANE")) == "" {
		return fmt.Errorf("managed tmux launch for %s requires TMUX and TMUX_PANE; refusing to write launch.json", target)
	}
	if !launchStdinIsTerminal() {
		return fmt.Errorf("managed tmux launch for %s requires a real terminal; refusing to write launch.json", target)
	}
	if rec.Tmux == nil || strings.TrimSpace(rec.Tmux.PaneID) == "" {
		return fmt.Errorf("managed tmux launch for %s could not resolve a pane id; refusing to write launch.json", target)
	}
	return nil
}

func launchPreauthProjectDir(cwd, teamHome string) string {
	teamHome = strings.TrimSpace(teamHome)
	if teamHome == "" {
		return cwd
	}
	if abs, err := filepath.Abs(teamHome); err == nil {
		return abs
	}
	return filepath.Clean(teamHome)
}

func launchAdoptionMode(target, launcherPaneID, agentPaneID string) string {
	switch strings.TrimSpace(target) {
	case "new-window":
		return "managed_window"
	case "current-window":
		return "managed_current_window"
	case "new-session":
		return "managed_session"
	case "":
		if strings.TrimSpace(launcherPaneID) != "" && strings.TrimSpace(launcherPaneID) == strings.TrimSpace(agentPaneID) {
			return "bare_agent_up"
		}
		return "unmanaged"
	default:
		return "unmanaged"
	}
}

func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// ensureLauncherExecutable verifies a custom --launcher path exists and is an
// executable file before exec, so a missing or non-executable wrapper fails
// with a clear message instead of an opaque coop exec error.
func ensureLauncherExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("launcher %q not found", path)
		}
		return fmt.Errorf("launcher %q: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("launcher %q is a directory, not an executable", path)
	}
	if info.Mode()&0o111 == 0 {
		return fmt.Errorf("launcher %q is not executable (chmod +x it)", path)
	}
	return nil
}

func resolveAMQEnvForLaunch(cwd, rootFlag, session, profile, handle string) (amqEnv, error) {
	if launchUsesExplicitRoot(rootFlag, session, profile) {
		env, err := resolveAMQEnvInDir(cwd, rootFlag, "", handle)
		if err != nil {
			return amqEnv{}, err
		}
		if strings.TrimSpace(env.SessionName) == "" {
			env.SessionName = strings.TrimSpace(session)
		}
		return env, nil
	}
	return resolveAMQEnvInDir(cwd, rootFlag, session, handle)
}

func launchUsesExplicitRoot(rootFlag, session, profile string) bool {
	return strings.TrimSpace(rootFlag) != "" &&
		strings.TrimSpace(session) != "" &&
		squadnamespace.NormalizeProfile(profile) != team.DefaultProfile
}

func launchRootFromFlags(cwd, rootFlag, session, profile string) string {
	rootFlag = strings.TrimSpace(rootFlag)
	if rootFlag != "" {
		return rootFlag
	}
	if strings.TrimSpace(session) == "" || squadnamespace.NormalizeProfile(profile) == team.DefaultProfile {
		return ""
	}
	return squadnamespace.AMQRoot(cwd, profile, session)
}

func nativeGoalBindingFromArgs(args []string) *launch.GoalBinding {
	return goalBindingFromArgs("claude", args)
}

func goalBindingFromArgs(binary string, args []string) *launch.GoalBinding {
	contract, err := goalDeliveryContractForBinary(binary)
	if err != nil {
		return nil
	}
	for _, arg := range args {
		cmd := strings.TrimSpace(arg)
		if contract.NativeGoal && (cmd == "/goal" || strings.HasPrefix(cmd, "/goal ")) {
			goal, attemptID, parseErr := parseNativeGoalBindingCommand(cmd)
			if parseErr == nil {
				return contract.binding(goal, attemptID, cmd, "launch-argv", "launch argv included a native /goal command for the visible lead")
			}
		}
		if !contract.NativeGoal && strings.HasPrefix(cmd, "AMQ-SQUAD PROMPT GOAL v1\n") {
			goal, attemptID, parseErr := parseCodexGoalControlPrompt(cmd)
			if parseErr == nil && goal != "" {
				return contract.binding(goal, attemptID, cmd, "launch-argv", "launch argv included a structured prompt goal for a Codex visible lead")
			}
		}
	}
	return nil
}

func conversationRefFromFlags(conversation, conversationID string) (string, error) {
	conversation = strings.TrimSpace(conversation)
	conversationID = strings.TrimSpace(conversationID)
	if conversation != "" && conversationID != "" && conversation != conversationID {
		return "", fmt.Errorf("use only one of --conversation or --conversation-id")
	}
	if conversation != "" {
		return conversation, nil
	}
	return conversationID, nil
}

func applyConversationRestoreArgs(binary string, childArgs []string, conversation string) ([]string, error) {
	return applyConversationRestoreArgsWithDefaults(binary, childArgs, conversation, defaultChildArgsForBinary(binary))
}

func applyConversationRestoreArgsWithDefaults(binary string, childArgs []string, conversation string, defaultArgs []string) ([]string, error) {
	conversation = strings.TrimSpace(conversation)
	if conversation == "" {
		return childArgs, nil
	}
	switch normalizedAgentBinary(binary) {
	case "codex":
		if ref, ok := codexResumeRef(childArgs); ok {
			if ref == "" {
				return nil, fmt.Errorf("--conversation cannot be combined with existing codex resume without a ref")
			}
			if ref != conversation {
				return nil, fmt.Errorf("--conversation %q conflicts with existing codex resume %q", conversation, ref)
			}
			if !hasNoExtraConversationArgs(stripCodexResumeRef(childArgs, conversation), defaultArgs) {
				return nil, fmt.Errorf("--conversation cannot be combined with extra codex args; omit --conversation and pass native resume args after --")
			}
			return childArgs, nil
		}
		if !hasNoExtraConversationArgs(childArgs, defaultArgs) {
			return nil, fmt.Errorf("--conversation cannot be combined with extra codex args; omit --conversation and pass native resume args after --")
		}
		out := append([]string(nil), childArgs...)
		return append(out, "resume", conversation), nil
	case "claude":
		if ref, ok := claudeResumeRef(childArgs); ok {
			if ref == "" {
				return nil, fmt.Errorf("--conversation cannot be combined with existing claude resume or continue without a ref")
			}
			if ref != conversation {
				return nil, fmt.Errorf("--conversation %q conflicts with existing claude resume %q", conversation, ref)
			}
			if !hasNoExtraConversationArgs(stripClaudeResumeRef(childArgs, conversation), defaultArgs) {
				return nil, fmt.Errorf("--conversation cannot be combined with extra claude args; omit --conversation and pass native resume args after --")
			}
			return childArgs, nil
		}
		if !hasNoExtraConversationArgs(childArgs, defaultArgs) {
			return nil, fmt.Errorf("--conversation cannot be combined with extra claude args; omit --conversation and pass native resume args after --")
		}
		out := append([]string(nil), childArgs...)
		return append(out, "--resume", conversation), nil
	default:
		return nil, fmt.Errorf("--conversation is supported for codex and claude, got %q", binary)
	}
}

func hasNoExtraConversationArgs(childArgs []string, defaultArgs []string) bool {
	if len(childArgs) == 0 {
		return true
	}
	if len(childArgs) != len(defaultArgs) {
		return false
	}
	for i := range childArgs {
		if childArgs[i] != defaultArgs[i] {
			return false
		}
	}
	return true
}

func stripConversationRestoreArgs(binary string, childArgs []string, conversation string) []string {
	conversation = strings.TrimSpace(conversation)
	if conversation == "" {
		return append([]string(nil), childArgs...)
	}
	switch normalizedAgentBinary(binary) {
	case "codex":
		return stripCodexResumeRef(childArgs, conversation)
	case "claude":
		return stripClaudeResumeRef(childArgs, conversation)
	default:
		return append([]string(nil), childArgs...)
	}
}

func normalizedAgentBinary(binary string) string {
	return strings.ToLower(filepath.Base(binary))
}

func codexResumeRef(args []string) (string, bool) {
	for i, arg := range args {
		if arg != "resume" {
			continue
		}
		if i+1 < len(args) {
			return args[i+1], true
		}
		return "", true
	}
	return "", false
}

func stripCodexResumeRef(args []string, conversation string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "resume" && i+1 < len(args) && args[i+1] == conversation {
			i++
			continue
		}
		out = append(out, args[i])
	}
	return out
}

func claudeResumeRef(args []string) (string, bool) {
	for i, arg := range args {
		switch {
		case arg == "--resume" || arg == "-r" || arg == "--session-id":
			if i+1 < len(args) {
				return args[i+1], true
			}
			return "", true
		case strings.HasPrefix(arg, "--resume="):
			return strings.TrimPrefix(arg, "--resume="), true
		case strings.HasPrefix(arg, "--session-id="):
			return strings.TrimPrefix(arg, "--session-id="), true
		case arg == "--continue" || arg == "-c":
			return "", true
		}
	}
	return "", false
}

func stripClaudeResumeRef(args []string, conversation string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if (arg == "--resume" || arg == "-r" || arg == "--session-id") && i+1 < len(args) && args[i+1] == conversation {
			i++
			continue
		}
		if arg == "--resume="+conversation || arg == "--session-id="+conversation {
			continue
		}
		out = append(out, arg)
	}
	return out
}

// resolveAMQRoot shells out to `amq env --json` to discover the final root path.
func resolveAMQRoot(rootFlag, session, handle string) (string, error) {
	return resolveAMQRootInDir("", rootFlag, session, handle)
}

func resolveAMQRootInDir(cwd, rootFlag, session, handle string) (string, error) {
	env, err := resolveAMQEnvInDir(cwd, rootFlag, session, handle)
	if err != nil {
		return "", err
	}
	return env.Root, nil
}

// seedRoleStub writes a role.md for the given agent directory. Precedence:
//  1. a custom role authored in a file and staged at
//     <teamHome>/.amq-squad/roles/<id>.md during team init (verbatim),
//  2. the built-in catalog entry for roleID,
//  3. a minimal fallback stub with label = roleID.
//
// It never overwrites existing user edits.
func seedRoleStub(agentDir, roleID, teamHome string) error {
	if catalog.Lookup(roleID) == nil && strings.TrimSpace(teamHome) != "" {
		docPath := team.CustomRolePath(teamHome, roleID)
		body, err := os.ReadFile(docPath)
		if err == nil {
			_, werr := role.EnsureContent(agentDir, string(body))
			return werr
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("read custom role doc %s: %w", docPath, err)
		}
	}
	stub := role.Stub{RoleID: roleID, Label: roleID}
	if r := catalog.Lookup(roleID); r != nil {
		stub.Label = r.Label
		stub.Description = r.Description
		stub.Skills = r.Skills
		stub.Peers = r.DefaultPeers
	}
	_, err := role.EnsureStub(agentDir, stub)
	return err
}

// splitDashDash splits argv at the first "--" separator.
func splitDashDash(args []string) ([]string, []string) {
	for i, a := range args {
		if a == "--" {
			return args[:i], args[i+1:]
		}
	}
	return args, nil
}
