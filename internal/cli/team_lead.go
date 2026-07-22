package cli

import (
	"errors"
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
	"github.com/omriariav/amq-squad/v2/internal/bootstrapack"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

var currentPaneIdentity = tmuxpane.CurrentPaneIdentity

type leadWakeOptions struct {
	ProjectDir     string
	Profile        string
	Session        string
	Root           string
	Handle         string
	Require        bool
	WakeInjectVia  string
	WakeInjectArgs []string
	WakeInjectMode string
	WakeInjectCmd  string
}

type wakeInjectConfig struct {
	Mode string
	Via  string
	Args []string
}

// wakeDrainInject is the standard instruction amq-squad asks the wake sidecar to
// inject on each durable-message arrival (amq wake --inject-cmd). It re-engages a
// lead or orchestrator even after its active goal reaches a terminal "achieved"
// state: the inbound directive drives an inbox drain through AMQ's sanctioned
// injector instead of a raw tmux send-keys. Shared by lead register --wake (#283)
// and the goal orchestrator registration (#288) so both use one mechanism.
func wakeDrainInject() string {
	return "amq-squad: a durable AMQ message arrived. Run `amq drain --include-body` now and act on the newest item, even if your current goal looks complete. Do not wait to be polled."
}

type leadWakeResult struct {
	PID     int
	Started bool
	Detail  string
}

type preparedExternalLeadRegistration struct {
	RecordWrite launchRecordWriteSnapshot
	Wake        leadWakeResult
}

var leadWakeStarter = startExternalLeadWake
var externalWakeRecordBinder = func(agentDir, root, handle string, expectedPID int, probe duplicateLaunchProbe) (wakeRecordBinding, error) {
	binding, err := verifiedWakeRecordBinding(agentDir, root, handle, probe)
	if err != nil {
		return wakeRecordBinding{}, err
	}
	if expectedPID > 0 && binding.PID != expectedPID {
		return wakeRecordBinding{}, fmt.Errorf("wake lock PID %d differs from started wake PID %d", binding.PID, expectedPID)
	}
	return binding, nil
}
var externalLeadAfterWakeStart = func(leadWakeResult) error { return nil }
var externalLeadAfterRecordWrite = func(string, launch.Record) error { return nil }
var externalLeadWakeSleep = time.Sleep

func rollbackStartedExternalLeadWake(result leadWakeResult) error {
	if !result.Started || result.PID <= 0 {
		return nil
	}
	if err := externalLeadWakeProcessGroupSignal(result.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("stop run-owned external lead wake process group %d: %w", result.PID, err)
	}
	for i := 0; i < 50; i++ {
		if err := externalLeadWakeProcessGroupSignal(result.PID, 0); errors.Is(err, syscall.ESRCH) {
			return nil
		}
		externalLeadWakeSleep(20 * time.Millisecond)
	}
	if err := externalLeadWakeProcessGroupSignal(result.PID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill non-quiescent run-owned external lead wake process group %d: %w", result.PID, err)
	}
	for i := 0; i < 50; i++ {
		if err := externalLeadWakeProcessGroupSignal(result.PID, 0); errors.Is(err, syscall.ESRCH) {
			return nil
		}
		externalLeadWakeSleep(20 * time.Millisecond)
	}
	return fmt.Errorf("run-owned external lead wake process group %d did not quiesce", result.PID)
}

var externalLeadWakeCommand = exec.Command
var externalLeadWakeReadyTimeout = 5 * time.Second
var externalLeadWakePollInterval = 50 * time.Millisecond
var externalLeadWakeStopTimeout = 2 * time.Second
var externalLeadWakeProcessEvent = func(_ string, _ *exec.Cmd, _ error) {}
var externalLeadWakeProcessGroupSignal = func(pgid int, signal syscall.Signal) error {
	return syscall.Kill(-pgid, signal)
}

type teamLeadData struct {
	Profile      string `json:"profile"`
	Orchestrated bool   `json:"orchestrated"`
	Lead         string `json:"lead,omitempty"`
	LeadHandle   string `json:"lead_handle,omitempty"`
	LeadMode     string `json:"lead_mode,omitempty"`
}

func runTeamLead(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, `amq-squad team lead - manage orchestration lead metadata

Usage:
  amq-squad team lead set <role> [--project DIR] [--profile NAME] [--lead-mode builder|planner]
  amq-squad team lead clear [--project DIR] [--profile NAME]
  amq-squad team lead show [--json] [--project DIR] [--profile NAME]

set marks the existing team profile as orchestrated and records <role> as the
lead. clear returns the profile to a flat team. The lead role must already be a
team member; use 'team member add' first for dynamic teams.
`)
		if len(args) == 0 {
			return usageErrorf("team lead requires a subcommand ('set', 'clear', or 'show')")
		}
		return nil
	}
	switch args[0] {
	case "set":
		return runTeamLeadSet(args[1:])
	case "clear":
		return runTeamLeadClear(args[1:])
	case "show":
		return runTeamLeadShow(args[1:])
	default:
		return usageErrorf("unknown 'team lead' subcommand: %q. Try 'set', 'clear', or 'show'.", args[0])
	}
}

func runTeamLeadSet(args []string) error {
	role, rest, ok := peelPositional(args)
	if !ok {
		return usageErrorf("a lead role is required, e.g. 'team lead set cto'")
	}
	fs := flag.NewFlagSet("team lead set", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to mutate (default: default profile)")
	leadModeFlag := fs.String("lead-mode", "", "lead implementation posture: builder (default) or planner")
	if err := parseFlags(fs, rest); err != nil {
		return err
	}
	leadModeSet := flagWasSet(fs, "lead-mode")
	leadMode, err := normalizeLeadMode(*leadModeFlag)
	if err != nil {
		return err
	}
	role = strings.ToLower(strings.TrimSpace(role))
	if err := setTeamLead(*projectFlag, *profileFlag, flagWasSet(fs, "project"), role, leadMode, leadModeSet); err != nil {
		return err
	}
	fmt.Printf("orchestrated lead set to %s.\n", role)
	return nil
}

func runTeamLeadClear(args []string) error {
	fs := flag.NewFlagSet("team lead clear", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to mutate (default: default profile)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("unexpected argument %q", fs.Arg(0))
	}
	projectDir, profile, err := resolveExistingTeamProfile(*projectFlag, *profileFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	if err := withProfileLock(projectDir, profile, func() error {
		t, err := team.ReadProfile(projectDir, profile)
		if err != nil {
			return fmt.Errorf("read team: %w", err)
		}
		t.Orchestrated = false
		t.Lead = ""
		t.LeadMode = ""
		return team.WriteProfileUnderLock(projectDir, profile, t)
	}); err != nil {
		return err
	}
	fmt.Println("orchestrated lead cleared.")
	return nil
}

func runTeamLeadShow(args []string) error {
	fs := flag.NewFlagSet("team lead show", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to read (default: default profile)")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned team_lead envelope")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("unexpected argument %q", fs.Arg(0))
	}
	projectDir, profile, err := resolveExistingTeamProfile(*projectFlag, *profileFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	t, err := team.ReadProfile(projectDir, profile)
	if err != nil {
		return fmt.Errorf("read team: %w", err)
	}
	data := buildTeamLeadData(profile, t)
	if *jsonOut {
		return printJSONEnvelope("team_lead", data)
	}
	if !data.Orchestrated {
		fmt.Println("orchestrated: no")
		return nil
	}
	fmt.Printf("orchestrated: yes\nlead: %s\nlead_handle: %s\nlead_mode: %s\n", data.Lead, data.LeadHandle, data.LeadMode)
	return nil
}

func runLead(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, `amq-squad lead - register an external orchestrator

Usage:
  amq-squad lead register [--role ROLE] [--session S] [--project DIR] [--profile NAME]
                          [--wake|--no-wake] [--require-wake|--no-require-wake]
                          [--adopt-project-lead] [--compat-no-wake --reason TEXT]
                          [--wake-inject-mode auto|raw|paste|none]
                          [--wake-inject-via PATH] [--wake-inject-arg ARG]

register adopts the current tmux pane as the external lead for an existing team
profile. It sets orchestrated/lead when needed and writes an explicit external
runtime record, without pretending amq-squad spawned or owns the pane. By
default it also starts or repairs the AMQ wake sidecar for the lead's resolved
session root, so child reports create the same attention path spawned agents get.
`)
		if len(args) == 0 {
			return usageErrorf("lead requires a subcommand ('register')")
		}
		return nil
	}
	switch args[0] {
	case "register":
		return runLeadRegister(args[1:])
	default:
		return usageErrorf("unknown 'lead' subcommand: %q. Try 'register'.", args[0])
	}
}

func runLeadRegister(args []string) error {
	return runLeadRegisterWithPreparedToken(args, preparedRunToken{})
}

func runLeadRegisterWithPreparedToken(args []string, requestedPreparedToken preparedRunToken, resultSink ...func(preparedExternalLeadRegistration)) (retErr error) {
	var wakeResult leadWakeResult
	wakeCleanupPending := false
	defer func() {
		if wakeCleanupPending {
			retErr = errors.Join(retErr, rollbackStartedExternalLeadWake(wakeResult))
		}
	}()
	fs := flag.NewFlagSet("lead register", flag.ContinueOnError)
	roleFlag := fs.String("role", "", "lead role to register (defaults to configured lead, then AM_ME)")
	sessionFlag := fs.String("session", "", "AMQ workstream session (default: team workstream)")
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to mutate (default: default profile)")
	registerScopedFlagAliases(fs, projectFlag, sessionFlag, profileFlag)
	wake := fs.Bool("wake", false, "start or repair amq wake for the external lead (default)")
	noWake := fs.Bool("no-wake", false, "write the external lead record without starting amq wake")
	adoptProjectLead := fs.Bool("adopt-project-lead", false, "explicitly adopt the current pane as an external project lead after identity checks")
	compatNoWake := fs.Bool("compat-no-wake", false, "allow --no-wake for project-lead adoption when paired with --reason")
	reason := fs.String("reason", "", "required compatibility reason when adopting a project lead with --no-wake")
	requireWake := fs.Bool("require-wake", false, "fail if the external lead wake sidecar cannot become ready (default)")
	noRequireWake := fs.Bool("no-require-wake", false, "warn instead of failing if the external lead wake sidecar cannot become ready")
	wakeInjectVia := fs.String("wake-inject-via", "", "absolute executable passed to amq wake --inject-via for external lead notifications")
	wakeInjectMode := fs.String("wake-inject-mode", "", "wake injection mode: auto, raw, paste, or none (none guarantees zero terminal input)")
	var wakeInjectArgs stringListFlag
	fs.Var(&wakeInjectArgs, "wake-inject-arg", "argument passed to amq wake --inject-arg (repeatable; requires --wake-inject-via)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("unexpected argument %q", fs.Arg(0))
	}
	if *wake && *noWake {
		return usageErrorf("--wake and --no-wake are mutually exclusive")
	}
	if *requireWake && *noRequireWake {
		return usageErrorf("--require-wake and --no-require-wake are mutually exclusive")
	}
	if *compatNoWake && !*noWake {
		return usageErrorf("--compat-no-wake requires --no-wake")
	}
	if strings.TrimSpace(*reason) != "" && !*compatNoWake {
		return usageErrorf("--reason applies only with --compat-no-wake")
	}
	wakeInjectViaValue := strings.TrimSpace(*wakeInjectVia)
	wakeInjectArgValues := append([]string(nil), wakeInjectArgs...)
	wakeInjectModeValue, err := normalizeWakeInjectMode(*wakeInjectMode)
	if err != nil {
		return err
	}
	ctx, err := resolveScopedCommandContext(*projectFlag, *profileFlag, *sessionFlag, "", fs)
	if err != nil {
		return err
	}
	emitContextDiagnostics(ctx)
	projectDir, profile := ctx.ProjectDir, ctx.Profile
	if !team.ExistsProfile(projectDir, profile) {
		return fmt.Errorf("no team configured for profile %q. Run '%s' first.", profile, profileInitCommand(profile))
	}
	t, err := team.ReadProfile(projectDir, profile)
	if err != nil {
		return fmt.Errorf("read team: %w", err)
	}
	role := strings.ToLower(strings.TrimSpace(*roleFlag))
	if role == "" {
		role = strings.ToLower(strings.TrimSpace(t.Lead))
	}
	if role == "" {
		role = strings.ToLower(strings.TrimSpace(os.Getenv("AM_ME")))
	}
	if role == "" {
		return usageErrorf("--role is required when the team has no configured lead and AM_ME is unset")
	}
	member, ok := memberByRole(t, role)
	if !ok {
		return fmt.Errorf("lead role %q is not a team member", role)
	}
	workstream, err := resolveTeamWorkstreamName(t, ctx.Session, flagWasSet(fs, "session"))
	if err != nil {
		return err
	}
	admission, err := acquireNamespaceWriterAdmission(projectDir, profile, workstream)
	if err != nil {
		return err
	}
	defer admission.close()
	currentCtx, err := resolveScopedCommandContext(*projectFlag, *profileFlag, *sessionFlag, "", fs)
	if err != nil {
		return fmt.Errorf("lead register refused: context re-resolution under admission failed: %w", err)
	}
	if err := validateReResolvedContext(ctx, currentCtx, false); err != nil {
		return err
	}
	currentTeam, err := team.ReadProfile(currentCtx.ProjectDir, currentCtx.Profile)
	if err != nil {
		return fmt.Errorf("lead register refused: reread team under admission: %w", err)
	}
	currentMember, ok := memberByRole(currentTeam, role)
	if !ok {
		return fmt.Errorf("lead register refused: lead role %q changed before admission", role)
	}
	currentWorkstream, err := resolveTeamWorkstreamName(currentTeam, currentCtx.Session, flagWasSet(fs, "session"))
	if err != nil {
		return err
	}
	if err := validateReResolvedEndpoint("lead register", squadnamespace.Resolve(projectDir, profile, workstream), squadnamespace.Resolve(currentCtx.ProjectDir, currentCtx.Profile, currentWorkstream), memberHandle(member), memberHandle(currentMember)); err != nil {
		return err
	}
	if member.Binary != currentMember.Binary || member.Session != currentMember.Session || canonicalPath(member.EffectiveCWD(t.Project)) != canonicalPath(currentMember.EffectiveCWD(currentTeam.Project)) {
		return fmt.Errorf("lead register refused: lead identity changed before admission; retry")
	}
	ctx, projectDir, profile, t, member, workstream = currentCtx, currentCtx.ProjectDir, currentCtx.Profile, currentTeam, currentMember, currentWorkstream
	if err := ensureNoNamespaceMigration("lead register", projectDir, profile, workstream); err != nil {
		return err
	}
	manifestAdmission, err := acquirePreparedManifestReaderAdmission(projectDir, profile, workstream)
	if err != nil {
		return err
	}
	defer manifestAdmission.close()
	id, err := currentPaneIdentity()
	if err != nil {
		return err
	}
	if id == nil {
		return fmt.Errorf("lead register requires a current tmux pane (TMUX/TMUX_PANE unset)")
	}
	cwd, err := canonicalDir(member.EffectiveCWD(t.Project))
	if err != nil {
		return fmt.Errorf("resolve external lead cwd: %w", err)
	}
	handle := memberHandle(member)
	env, err := resolveAMQEnvForTeamProfile(cwd, profile, workstream, handle)
	if err != nil {
		return fmt.Errorf("resolve amq env: %w", err)
	}
	if env.Me != "" {
		handle = env.Me
	}
	root := absoluteAMQRoot(cwd, env.Root)
	agentDir := filepath.Join(root, "agents", handle)
	existingRec, existingRecErr := launch.Read(agentDir)
	wakeConfig, err := resolveExternalWakeInjectConfig(wakeInjectConfig{
		Mode: wakeInjectModeValue,
		Via:  wakeInjectViaValue,
		Args: wakeInjectArgValues,
	}, flagWasSet(fs, "wake-inject-mode"), flagWasSet(fs, "wake-inject-via"), flagWasSet(fs, "wake-inject-arg"), existingRec, existingRecErr, member.Binary, role, handle, profile, env.SessionName, root, id.PaneID)
	if err != nil {
		return err
	}
	wakeInjectModeValue = wakeConfig.Mode
	wakeInjectViaValue = wakeConfig.Via
	wakeInjectArgValues = wakeConfig.Args
	if wakeInjectModeValue != "" && !amqSupportsWakeInjectMode(env.AMQVersion) {
		return fmt.Errorf("--wake-inject-mode requires amq %s or newer (found %s)", minWakeInjectModeAMQVersion, versionOrUnknown(env.AMQVersion))
	}
	targetMode := leadRegisterTargetMode(t, role)
	auth, err := authorizeLeadRegister(leadRegisterAuthInput{
		Team:               t,
		Member:             member,
		Role:               role,
		Handle:             handle,
		Profile:            profile,
		Workstream:         env.SessionName,
		Root:               root,
		CWD:                cwd,
		PaneID:             id.PaneID,
		TargetMode:         targetMode,
		ExistingRecord:     existingRec,
		ExistingRecordErr:  existingRecErr,
		AdoptProjectLead:   *adoptProjectLead,
		NoWake:             *noWake,
		CompatNoWake:       *compatNoWake,
		CompatNoWakeReason: strings.TrimSpace(*reason),
	})
	if err != nil {
		return err
	}
	wakeInjectCmdValue := wakeDrainInject()
	if wakeInjectModeValue == "none" {
		wakeInjectCmdValue = ""
	}
	wakePID := 0
	rec := launch.Record{
		CWD:              cwd,
		Binary:           member.Binary,
		Session:          env.SessionName,
		SharedWorkstream: true,
		Handle:           handle,
		Role:             role,
		Root:             root,
		BaseRoot:         absoluteAMQRoot(cwd, env.BaseRoot),
		RootSource:       env.RootSource,
		AMQVersion:       env.AMQVersion,
		Model:            memberResolvedModel(member, nil, t.BinaryArgs),
		ToolProfile:      member.EffectiveToolProfile(),
		ToolConfig:       strings.TrimSpace(member.ToolConfig),
		ToolMCPConfig:    strings.TrimSpace(member.ToolMCPConfig),
		Trust:            strings.TrimSpace(t.Trust),
		External:         true,
		AdoptionMode:     auth.AdoptionMode,
		NoRequireWake:    *noRequireWake,
		NoWakeReason:     auth.NoWakeReason,
		WakeInjectVia:    wakeInjectViaValue,
		WakeInjectArgs:   wakeInjectArgValues,
		WakeInjectMode:   wakeInjectModeValue,
		WakeInjectCmd:    wakeInjectCmdValue,
		WakePID:          wakePID,
		AgentTTY:         currentLaunchTTY(),
		StartedAt:        time.Now().UTC(),
		TeamProfile:      profile,
		TeamHome:         projectDir,
		Tmux: &launch.TmuxInfo{
			Session:    id.Session,
			WindowID:   id.WindowID,
			WindowName: id.WindowName,
			PaneID:     id.PaneID,
			Target:     "external",
		},
	}
	rec.BootstrapExpectation = &bootstrapack.Expectation{Required: false, NotRequiredReason: "external lead is already running in the adopted pane"}
	if !requestedPreparedToken.empty() && !requestedPreparedToken.complete() {
		return fmt.Errorf("lead register refused: prepared run token is incomplete")
	}
	applyPreparedRunTokenToRecord(&rec, requestedPreparedToken)
	acceptedIdentity := acceptedMemberIdentity(t, member, profile, workstream)
	rec.Argv = append([]string(nil), acceptedIdentity.EffectiveArgs...)
	rec.Model = acceptedIdentity.Model
	rec.Trust = acceptedIdentity.Trust
	rec.ToolAllowlist = append([]string(nil), acceptedIdentity.ToolAllowlist...)
	rec.ToolBlocklist = append([]string(nil), acceptedIdentity.ToolBlocklist...)
	rec.LauncherPreauthorizedActions = append([]string(nil), acceptedIdentity.LauncherAuthority...)
	rec.PreauthorizedActions = append([]string(nil), acceptedIdentity.LauncherAuthority...)
	rec.NoPreauthorizeInScope = acceptedIdentity.NoPreauthorize
	effectiveBinaryArgs := acceptedIdentity.NativeArgs
	switch normalizedAgentBinary(member.Binary) {
	case "codex":
		rec.CodexArgs = effectiveBinaryArgs
	case "claude":
		rec.ClaudeArgs = effectiveBinaryArgs
	}
	rec.Terminal = launch.TerminalInfoFromTmux(rec.Tmux)
	preparedContext, err := preparedContextForLaunchRecord(rec)
	if err != nil {
		return fmt.Errorf("load accepted prepared external-lead identity: %w", err)
	}
	if !requestedPreparedToken.empty() {
		if preparedContext == nil {
			return fmt.Errorf("lead register refused: pinned prepared run identity disappeared")
		}
		if err := validatePreparedRunToken(requestedPreparedToken, preparedContext.Manifest, preparedContext.Digest); err != nil {
			return fmt.Errorf("lead register refused: %w", err)
		}
	}
	contextToken := preparedRunTokenForContext(preparedContext)
	contextToken.LaunchAttempt = requestedPreparedToken.LaunchAttempt
	applyPreparedRunTokenToRecord(&rec, contextToken)
	var preparedBinding *launch.GoalBinding
	if preparedContext != nil && preparedContext.Member.Role == preparedContext.Team.Lead {
		preparedBinding, err = preparedGoalBinding(preparedContext.Team, preparedContext.Manifest.Profile, preparedContext.Manifest.Session, preparedContext.Member, preparedContext.Binding)
		if err != nil {
			return fmt.Errorf("load accepted prepared external-lead goal binding: %w", err)
		}
	}
	rec.GoalBinding = preparedBinding
	preparedPrompt := ""
	if preparedContext != nil {
		bootstrapContext := bootstrapContextFor(rec, agentDir, projectDir)
		bootstrapContext.CurrentTeam, bootstrapContext.Warnings = bootstrapCurrentTeamWithRoster(rec, projectDir, true)
		preparedPrompt, err = buildBootstrapPrompt(bootstrapContext)
		if err != nil {
			return err
		}
		if err := revalidatePreparedBootstrapPromptForLaunch(rec, preparedPrompt, preparedContext); err != nil {
			return fmt.Errorf("validate accepted external-lead launch input: %w", err)
		}
	}
	revalidatePrepared := func(stage string) error {
		if preparedContext == nil {
			return nil
		}
		if err := revalidatePreparedBootstrapPromptForLaunch(rec, preparedPrompt, preparedContext); err != nil {
			return fmt.Errorf("prepared external lead changed before %s: %w", stage, err)
		}
		return nil
	}
	if !requestedPreparedToken.empty() {
		if err := consumePreparedRunMember(projectDir, profile, workstream, requestedPreparedToken, role, handle); err != nil {
			return fmt.Errorf("lead register refused before wake or launch-record side effects: %w", err)
		}
	}
	if err := revalidatePrepared("wake start"); err != nil {
		return err
	}
	if !*noWake {
		wakeResult, err = leadWakeStarter(leadWakeOptions{
			ProjectDir:     cwd,
			Profile:        profile,
			Session:        env.SessionName,
			Root:           root,
			Handle:         handle,
			Require:        !*noRequireWake,
			WakeInjectVia:  wakeInjectViaValue,
			WakeInjectArgs: wakeInjectArgValues,
			WakeInjectMode: wakeInjectModeValue,
			WakeInjectCmd:  wakeInjectCmdValue,
		})
		if err != nil {
			return fmt.Errorf("start external lead wake: %w", err)
		}
		wakeCleanupPending = wakeResult.Started
		if err := externalLeadAfterWakeStart(wakeResult); err != nil {
			return err
		}
	}
	wakePID = wakeResult.PID
	var wakeBinding wakeRecordBinding
	if !*noWake {
		wakeBinding, err = externalWakeRecordBinder(agentDir, root, handle, wakeResult.PID, defaultDuplicateLaunchProbe)
		if err != nil && !*noRequireWake {
			return fmt.Errorf("bind external lead wake record: %w", err)
		}
		if err == nil {
			wakePID = wakeBinding.PID
		}
	}
	rec.WakePID = wakePID
	rec.WakeRecordID = wakeBinding.RecordID
	rec.WakeRecordDigest = wakeBinding.RecordDigest
	if err := revalidatePrepared("launch record write"); err != nil {
		return err
	}
	recordWrite, err := writeExternalLeadLaunchRecord(agentDir, rec, role, env.SessionName)
	if err != nil {
		return fmt.Errorf("write external launch record: %w", err)
	}
	rollbackRegistration := func(cause error) error {
		applied, rollbackErr := rollbackLaunchRecordIfCurrent(agentDir, recordWrite)
		wakeCleanupPending = applied
		return errors.Join(cause, rollbackErr)
	}
	if err := externalLeadAfterRecordWrite(agentDir, recordWrite.Written); err != nil {
		return rollbackRegistration(err)
	}
	if err := validateStoredPreparedExternalLeadRecord(agentDir, rec, preparedContext); err != nil {
		return rollbackRegistration(fmt.Errorf("validate stored external launch record: %w", err))
	}
	if err := revalidatePrepared("team lead profile write"); err != nil {
		return rollbackRegistration(err)
	}
	if err := setTeamLeadForProfile(projectDir, profile, role, "", false); err != nil {
		return rollbackRegistration(err)
	}
	if len(resultSink) > 0 && resultSink[0] != nil {
		resultSink[0](preparedExternalLeadRegistration{RecordWrite: recordWrite, Wake: wakeResult})
	}
	wakeCleanupPending = false
	fmt.Printf("registered external lead %s (%s) at pane %s for session %s.\n", role, handle, id.PaneID, env.SessionName)
	if *noWake {
		fmt.Println("wake: skipped (--no-wake); lead must collect manually")
	} else if wakeResult.Detail != "" {
		fmt.Printf("wake: %s\n", wakeResult.Detail)
	}
	return nil
}

func writeExternalLeadLaunchRecord(agentDir string, rec launch.Record, role, session string) (launchRecordWriteSnapshot, error) {
	var snapshot launchRecordWriteSnapshot
	err := launch.WithRecordLock(agentDir, func() error {
		current, currentErr := launch.Read(agentDir)
		if currentErr == nil {
			previous := current
			snapshot.Previous = &previous
		} else if !os.IsNotExist(currentErr) {
			return currentErr
		}
		if preserveExternalGoalBindingForRecord(current, currentErr, rec, role, session) {
			gb := *current.GoalBinding
			rec.GoalBinding = &gb
		}
		if err := launch.WriteUnderRecordLock(agentDir, rec); err != nil {
			return err
		}
		written, err := launch.Read(agentDir)
		if err != nil {
			return err
		}
		snapshot.Written = written
		return nil
	})
	return snapshot, err
}

func resolveExternalWakeInjectConfig(requested wakeInjectConfig, modeExplicit, viaExplicit, argsExplicit bool, existing launch.Record, existingErr error, binary, role, handle, profile, session, root, paneID string) (wakeInjectConfig, error) {
	resolved := wakeInjectConfig{
		Mode: strings.TrimSpace(requested.Mode),
		Via:  strings.TrimSpace(requested.Via),
		Args: append([]string(nil), requested.Args...),
	}
	if !modeExplicit && existingErr == nil && existing.External && launchRecordMatchesSamePaneIdentity(existing, role, handle, profile, session, root, paneID) {
		resolved.Mode = strings.TrimSpace(existing.WakeInjectMode)
		if !viaExplicit && !argsExplicit {
			resolved.Via = strings.TrimSpace(existing.WakeInjectVia)
			resolved.Args = append([]string(nil), existing.WakeInjectArgs...)
		}
	}
	mode, err := normalizeWakeInjectMode(resolved.Mode)
	if err != nil {
		return wakeInjectConfig{}, fmt.Errorf("stored external wake config: %w", err)
	}
	resolved.Mode = resolveWakeInjectModeForBinary(mode, binary)
	if err := validateWakeInjectConfig(resolved.Mode, resolved.Via, resolved.Args, ""); err != nil {
		return wakeInjectConfig{}, err
	}
	if resolved.Via != "" && !filepath.IsAbs(resolved.Via) {
		return wakeInjectConfig{}, usageErrorf("--wake-inject-via must be an absolute path")
	}
	return resolved, nil
}

func preserveExternalGoalBinding(rec launch.Record, err error, role, session string) bool {
	if err != nil || !launchRecordHasGoalBinding(rec) {
		return false
	}
	recRole := strings.TrimSpace(rec.Role)
	if recRole != "" && recRole != strings.TrimSpace(role) {
		return false
	}
	recSession := strings.TrimSpace(rec.Session)
	return recSession == "" || recSession == strings.TrimSpace(session)
}

func preserveExternalGoalBindingForRecord(rec launch.Record, err error, planned launch.Record, role, session string) bool {
	if planned.GoalBinding == nil {
		return preserveExternalGoalBinding(rec, err, role, session)
	}
	if err != nil || !launchRecordHasGoalBinding(rec) {
		return false
	}
	if !samePreparedExternalRecordIdentity(rec, planned, role, session) {
		return false
	}
	recSession := strings.TrimSpace(rec.Session)
	if recSession != "" && recSession != strings.TrimSpace(session) {
		return false
	}
	return exactExternalGoalBindingIdentity(rec.Binary, rec.GoalBinding, planned.GoalBinding)
}

func samePreparedExternalRecordIdentity(current, planned launch.Record, role, session string) bool {
	paneID := ""
	if planned.Tmux != nil {
		paneID = planned.Tmux.PaneID
	}
	return current.External && planned.External &&
		launchRecordMatchesSamePaneIdentity(current, role, planned.Handle, planned.TeamProfile, session, planned.Root, paneID) &&
		normalizedAgentBinary(current.Binary) == normalizedAgentBinary(planned.Binary) &&
		sameFilesystemPath(current.CWD, planned.CWD) &&
		sameOptionalFilesystemPath(current.BaseRoot, planned.BaseRoot) &&
		sameOptionalFilesystemPath(current.TeamHome, planned.TeamHome) &&
		current.SharedWorkstream == planned.SharedWorkstream &&
		strings.TrimSpace(current.RootSource) == strings.TrimSpace(planned.RootSource) &&
		strings.TrimSpace(current.AdoptionMode) == strings.TrimSpace(planned.AdoptionMode) &&
		reflect.DeepEqual(current.Tmux, planned.Tmux) &&
		reflect.DeepEqual(current.Terminal, planned.Terminal)
}

func sameOptionalFilesystemPath(current, planned string) bool {
	if strings.TrimSpace(current) == "" || strings.TrimSpace(planned) == "" {
		return strings.TrimSpace(current) == strings.TrimSpace(planned)
	}
	return sameFilesystemPath(current, planned)
}

func exactExternalGoalBindingIdentity(binary string, delivered, planned *launch.GoalBinding) bool {
	contract, err := goalDeliveryContractForBinary(binary)
	if err != nil || delivered == nil || planned == nil || delivered.Mode != planned.Mode || delivered.NativeGoal != planned.NativeGoal || delivered.Command != planned.Command {
		return false
	}
	deliveredGoal, deliveredAttempt, err := goalBindingPayload(delivered, contract)
	if err != nil {
		return false
	}
	plannedGoal, plannedAttempt, err := goalBindingPayload(planned, contract)
	return err == nil && deliveredGoal == plannedGoal && deliveredAttempt == plannedAttempt
}

func validateStoredPreparedExternalLeadRecord(agentDir string, expected launch.Record, context *preparedLaunchRecordContext) error {
	if context == nil {
		return nil
	}
	stored, err := launch.Read(agentDir)
	if err != nil {
		return err
	}
	currentContext, err := preparedContextForLaunchRecord(stored)
	if err != nil {
		return err
	}
	if currentContext == nil || currentContext.Manifest.Generation != context.Manifest.Generation || currentContext.Digest != context.Digest {
		return fmt.Errorf("stored external lead record no longer matches the accepted manifest generation")
	}
	if reflect.DeepEqual(stored.GoalBinding, expected.GoalBinding) {
		return nil
	}
	if launchRecordHasGoalBinding(stored) && exactExternalGoalBindingIdentity(stored.Binary, stored.GoalBinding, expected.GoalBinding) {
		return nil
	}
	return fmt.Errorf("stored external lead goal binding differs from the newly validated goal/attempt")
}

func validatePreparedExternalLeadStoredBeforeWorkerSpawn(project, profile, session, role string, expectedToken preparedRunToken) error {
	endpointAdmission, err := acquireNamespaceWriterAdmission(project, profile, session)
	if err != nil {
		return err
	}
	defer endpointAdmission.close()
	manifestAdmission, err := acquirePreparedManifestReaderAdmission(project, profile, session)
	if err != nil {
		return err
	}
	defer manifestAdmission.close()
	tm, err := team.ReadProfile(project, profile)
	if err != nil {
		return err
	}
	member, ok := memberByRole(tm, role)
	if !ok {
		return fmt.Errorf("prepared external lead %q disappeared before worker spawn", role)
	}
	cwd, err := canonicalDir(member.EffectiveCWD(tm.Project))
	if err != nil {
		return err
	}
	env, err := resolveAMQEnvForTeamLaunchProfile(cwd, profile, session, memberHandle(member))
	if err != nil {
		return err
	}
	agentDir := filepath.Join(absoluteAMQRoot(cwd, env.Root), "agents", memberHandle(member))
	rec, err := launch.Read(agentDir)
	if err != nil {
		return err
	}
	if !samePreparedRunGeneration(preparedRunTokenFromRecord(rec), expectedToken) {
		return fmt.Errorf("stored external lead prepared run token differs from the parent transaction")
	}
	id, err := currentPaneIdentity()
	if err != nil {
		return err
	}
	if id == nil || strings.TrimSpace(id.PaneID) == "" {
		return fmt.Errorf("prepared external lead pane identity disappeared before worker spawn")
	}
	expectedTmux := &launch.TmuxInfo{
		Session: id.Session, WindowID: id.WindowID, WindowName: id.WindowName,
		PaneID: id.PaneID, Target: "external",
	}
	expectedIdentity := launch.Record{
		CWD: cwd, Binary: member.Binary, Session: env.SessionName, SharedWorkstream: true,
		Handle: memberHandle(member), Role: role, Root: absoluteAMQRoot(cwd, env.Root),
		BaseRoot: absoluteAMQRoot(cwd, env.BaseRoot), RootSource: env.RootSource,
		TeamProfile: profile, TeamHome: project, External: true,
		AdoptionMode: adoptionModeExternalProjectLead, Tmux: expectedTmux,
		Terminal: launch.TerminalInfoFromTmux(expectedTmux),
	}
	if !samePreparedExternalRecordIdentity(rec, expectedIdentity, role, env.SessionName) {
		return fmt.Errorf("stored external lead record identity differs from the registered pane contract")
	}
	context, err := preparedContextForLaunchRecord(rec)
	if err != nil {
		return err
	}
	if context == nil || context.Member.Role != context.Team.Lead {
		return fmt.Errorf("stored external lead record has no accepted prepared context")
	}
	planned, err := preparedGoalBinding(context.Team, context.Manifest.Profile, context.Manifest.Session, context.Member, context.Binding)
	if err != nil {
		return err
	}
	expected := rec
	expected.GoalBinding = planned
	return validateStoredPreparedExternalLeadRecord(agentDir, expected, context)
}

func preparedExternalLeadRecordSnapshot(project, profile, session, role string) (string, *launch.Record, error) {
	tm, err := team.ReadProfile(project, profile)
	if err != nil {
		return "", nil, err
	}
	member, ok := memberByRole(tm, role)
	if !ok {
		return "", nil, fmt.Errorf("prepared external lead %q is not a team member", role)
	}
	cwd, err := canonicalDir(member.EffectiveCWD(tm.Project))
	if err != nil {
		return "", nil, err
	}
	env, err := resolveAMQEnvForTeamLaunchProfile(cwd, profile, session, memberHandle(member))
	if err != nil {
		return "", nil, err
	}
	agentDir := filepath.Join(absoluteAMQRoot(cwd, env.Root), "agents", memberHandle(member))
	rec, err := launch.Read(agentDir)
	if os.IsNotExist(err) {
		return agentDir, nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	return agentDir, &rec, nil
}

func rollbackPreparedExternalLeadRecord(agentDir string, previous *launch.Record, written launch.Record) (bool, error) {
	return rollbackLaunchRecordIfCurrent(agentDir, launchRecordWriteSnapshot{Previous: previous, Written: written})
}

func rollbackPreparedExternalLeadRegistration(agentDir string, registration preparedExternalLeadRegistration) error {
	applied, rollbackErr := rollbackPreparedExternalLeadRecord(agentDir, registration.RecordWrite.Previous, registration.RecordWrite.Written)
	if rollbackErr != nil || !applied {
		return rollbackErr
	}
	return rollbackStartedExternalLeadWake(registration.Wake)
}

type leadRegisterAuthInput struct {
	Team               team.Team
	Member             team.Member
	Role               string
	Handle             string
	Profile            string
	Workstream         string
	Root               string
	CWD                string
	PaneID             string
	TargetMode         string
	ExistingRecord     launch.Record
	ExistingRecordErr  error
	AdoptProjectLead   bool
	NoWake             bool
	CompatNoWake       bool
	CompatNoWakeReason string
}

type leadRegisterAuthResult struct {
	AdoptionMode string
	NoWakeReason string
}

func authorizeLeadRegister(in leadRegisterAuthInput) (leadRegisterAuthResult, error) {
	mode := strings.TrimSpace(in.TargetMode)
	result := leadRegisterAuthResult{AdoptionMode: adoptionModeExternal}
	projectMode := projectExecutionMode(mode)
	if projectMode {
		result.AdoptionMode = adoptionModeExternalProjectLead
	}
	if mode == executionModeGlobalOrchestrator && strings.TrimSpace(in.Role) != goalOrchestratorRole {
		return leadRegisterAuthResult{}, usageErrorf("refusing to register %q from a global_orchestrator profile: keep this pane as global orchestrator only, and launch/resume a real project lead in a sibling tab or managed pane", in.Role)
	}
	if in.NoWake && projectMode {
		if !in.CompatNoWake || strings.TrimSpace(in.CompatNoWakeReason) == "" {
			return leadRegisterAuthResult{}, usageErrorf("lead register --no-wake for project lead %q requires --compat-no-wake --reason <why>; use --wake by default for worker-facing leads", in.Role)
		}
		result.NoWakeReason = strings.TrimSpace(in.CompatNoWakeReason)
	}

	if _, rec, ok := findLaunchRecordByPane(in.Root, in.PaneID); ok {
		if !launchRecordMatchesIdentity(rec, in.Role, in.Handle, in.Profile, in.Workstream, in.Root) {
			return leadRegisterAuthResult{}, usageErrorf("refusing to register current pane as %q: pane already has launch identity role=%q handle=%q session=%q profile=%q. Keep this pane as its existing control-plane identity, or launch/resume %q in a sibling tab/new managed pane.", in.Role, rec.Role, rec.Handle, rec.Session, rec.TeamProfile, in.Role)
		}
		if projectMode && !launchRecordAuthorizesProjectLead(rec, in.Role, in.Handle, in.Profile, in.Workstream, in.Root) {
			return leadRegisterAuthResult{}, usageErrorf("refusing to register current pane as project lead %q: existing pane record is not verified as external_project_lead or native-goal-bound. Relaunch/resume the project lead in a managed pane, or use --adopt-project-lead from the actual project lead pane.", in.Role)
		}
		return result, nil
	}

	if in.ExistingRecordErr == nil {
		if projectMode {
			if launchRecordAuthorizesProjectLeadPane(in.ExistingRecord, in.Role, in.Handle, in.Profile, in.Workstream, in.Root, in.PaneID) {
				return result, nil
			}
		} else if launchRecordMatchesSamePaneIdentity(in.ExistingRecord, in.Role, in.Handle, in.Profile, in.Workstream, in.Root, in.PaneID) {
			return result, nil
		}
	}

	if currentEnvProvesTeamRole(in.Handle, in.Role, in.Root) {
		return result, nil
	}

	if in.AdoptProjectLead {
		if !projectMode {
			return leadRegisterAuthResult{}, usageErrorf("--adopt-project-lead is valid only for project_lead or project_team execution modes")
		}
		if !currentWorkingDirMatches(in.CWD) {
			return leadRegisterAuthResult{}, usageErrorf("refusing --adopt-project-lead for %q: current directory does not match the member project root %s", in.Role, in.CWD)
		}
		return result, nil
	}

	if projectMode {
		return leadRegisterAuthResult{}, usageErrorf("refusing to adopt current pane as project lead %q without verifiable identity for profile/session/role. Launch or resume the project lead in a sibling tab/new managed pane, or rerun from the actual project lead pane with --adopt-project-lead.", in.Role)
	}
	return result, nil
}

func startExternalLeadWake(opts leadWakeOptions) (leadWakeResult, error) {
	if strings.TrimSpace(opts.Root) == "" {
		return leadWakeResult{}, fmt.Errorf("missing AMQ root")
	}
	if strings.TrimSpace(opts.Handle) == "" {
		return leadWakeResult{}, fmt.Errorf("missing lead handle")
	}
	ready, err := os.CreateTemp("", "amq-squad-wake-ready-*")
	if err != nil {
		return leadWakeResult{}, fmt.Errorf("create ready file: %w", err)
	}
	readyPath := ready.Name()
	_ = ready.Close()
	_ = os.Remove(readyPath)
	defer os.Remove(readyPath)

	args := []string{
		"wake",
		"--root", opts.Root,
		"--me", opts.Handle,
		"--accept-existing-wake",
		"--ready-file", readyPath,
	}
	if via := strings.TrimSpace(opts.WakeInjectVia); via != "" {
		args = append(args, "--inject-via", via)
		for _, arg := range opts.WakeInjectArgs {
			args = append(args, "--inject-arg", arg)
		}
	}
	if mode := strings.TrimSpace(opts.WakeInjectMode); mode != "" {
		args = append(args, "--inject-mode", mode)
	}
	if cmd := strings.TrimSpace(opts.WakeInjectCmd); cmd != "" {
		args = append(args, "--inject-cmd", cmd)
	}
	cmd := externalLeadWakeCommand("amq", args...)
	cmd.Dir = opts.ProjectDir
	ctx := amqContext{
		ProjectDir: opts.ProjectDir,
		Profile:    squadnamespace.NormalizeProfile(opts.Profile),
		Root:       absoluteAMQRoot(opts.ProjectDir, opts.Root),
		Me:         opts.Handle,
		Session:    strings.TrimSpace(opts.Session),
		PinMode:    amqPinExactRoot,
	}
	if ctx.Profile == team.DefaultProfile && ctx.Session != "" {
		ctx.PinMode = amqPinSessionful
		ctx.Env.BaseRoot = filepath.Dir(ctx.Root)
	}
	cmd.Env = amqexec.NoUpdateCheckEnv(amqCommandEnv(ctx))
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	ownExternalLeadWakeProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return leadWakeResult{}, err
	}
	externalLeadWakeProcessEvent("process_started", cmd, nil)
	done := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		externalLeadWakeProcessEvent("process_wait_done", cmd, err)
		done <- err
	}()

	deadline := time.Now().Add(externalLeadWakeReadyTimeout)
	for {
		if _, err := os.Stat(readyPath); err == nil {
			return leadWakeResult{PID: cmd.Process.Pid, Started: true, Detail: fmt.Sprintf("ready for %s at %s (pid %d)", opts.Handle, opts.Root, cmd.Process.Pid)}, nil
		}
		select {
		case err := <-done:
			externalLeadWakeProcessEvent("done_observed", cmd, err)
			if err == nil {
				return leadWakeResult{Started: false, Detail: fmt.Sprintf("existing wake accepted for %s at %s", opts.Handle, opts.Root)}, nil
			}
			cleanupDetail := ""
			if stopErr := stopExternalLeadWakeProcessGroupAndWait(cmd); stopErr != nil {
				cleanupDetail = fmt.Sprintf("failed to stop spawned wake process group %d: %v", cmd.Process.Pid, stopErr)
			} else {
				cleanupDetail = fmt.Sprintf("stopped spawned wake process group %d", cmd.Process.Pid)
			}
			if opts.Require {
				return leadWakeResult{}, fmt.Errorf("%w; %s", err, cleanupDetail)
			}
			return leadWakeResult{Started: false, Detail: fmt.Sprintf("wake not ready: %v; %s", err, cleanupDetail)}, nil
		default:
		}
		if time.Now().After(deadline) {
			msg := fmt.Sprintf("wake did not become ready within %s for %s at %s", externalLeadWakeReadyTimeout, opts.Handle, opts.Root)
			if stopErr := stopExternalLeadWakeProcess(cmd, done); stopErr != nil {
				msg = fmt.Sprintf("%s; failed to stop spawned wake process group %d: %v", msg, cmd.Process.Pid, stopErr)
			} else {
				msg = fmt.Sprintf("%s; stopped spawned wake process group %d", msg, cmd.Process.Pid)
			}
			if opts.Require {
				return leadWakeResult{}, fmt.Errorf("%s", msg)
			}
			return leadWakeResult{Started: false, Detail: msg}, nil
		}
		time.Sleep(externalLeadWakePollInterval)
	}
}

func ownExternalLeadWakeProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

func stopExternalLeadWakeProcess(cmd *exec.Cmd, done <-chan error) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	killErr := stopExternalLeadWakeProcessGroup(cmd)
	select {
	case err := <-done:
		externalLeadWakeProcessEvent("done_observed", cmd, err)
	case <-time.After(externalLeadWakeStopTimeout):
		return fmt.Errorf("timed out waiting for process exit")
	}
	// The helper can fork after the first group signal is delivered but before
	// its leader is reaped. Once Wait completes the leader can no longer add a
	// descendant, so sweep the process group again to catch that race.
	finalKillErr := stopExternalLeadWakeProcessGroup(cmd)
	priorKillSucceeded := killErr == nil || errors.Is(killErr, os.ErrProcessDone)
	finalKillSucceeded := finalKillErr == nil || errors.Is(finalKillErr, os.ErrProcessDone)
	if !priorKillSucceeded && !finalKillSucceeded {
		return killErr
	}
	if !finalKillSucceeded && !(priorKillSucceeded && errors.Is(finalKillErr, syscall.EPERM)) {
		return finalKillErr
	}
	return waitExternalLeadWakeProcessGroupGone(cmd, priorKillSucceeded || finalKillSucceeded)
}

func stopExternalLeadWakeProcessGroupAndWait(cmd *exec.Cmd) error {
	killErr := stopExternalLeadWakeProcessGroup(cmd)
	if killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
		return killErr
	}
	return waitExternalLeadWakeProcessGroupGone(cmd, true)
}

func waitExternalLeadWakeProcessGroupGone(cmd *exec.Cmd, priorKillSucceeded bool) error {
	if cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 0 {
		return nil
	}
	pgid := cmd.Process.Pid
	deadline := time.Now().Add(externalLeadWakeStopTimeout)
	for {
		probeErr := externalLeadWakeProcessGroupSignal(pgid, 0)
		externalLeadWakeProcessEvent("quiescence_probe", cmd, probeErr)
		if errors.Is(probeErr, syscall.ESRCH) {
			return nil
		}
		// After a successful SIGKILL, Darwin can report EPERM while only
		// already-killed, unsignalable group members remain.
		if priorKillSucceeded && errors.Is(probeErr, syscall.EPERM) {
			return nil
		}
		if probeErr != nil {
			return fmt.Errorf("probe spawned wake process group %d: %w", pgid, probeErr)
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("timed out waiting for spawned wake process group %d to terminate", pgid)
		}
		resignalErr := stopExternalLeadWakeProcessGroup(cmd)
		if resignalErr == nil || errors.Is(resignalErr, os.ErrProcessDone) {
			priorKillSucceeded = true
		} else if priorKillSucceeded && errors.Is(resignalErr, syscall.EPERM) {
			return nil
		} else if !errors.Is(resignalErr, syscall.ESRCH) {
			return resignalErr
		}
		time.Sleep(externalLeadWakePollInterval)
	}
}

func stopExternalLeadWakeProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return nil
	}
	externalLeadWakeProcessEvent("kill_attempt", cmd, nil)
	err := externalLeadWakeProcessGroupSignal(pid, syscall.SIGKILL)
	if err == nil {
		externalLeadWakeProcessEvent("kill_result", cmd, nil)
		return nil
	}
	if errors.Is(err, syscall.ESRCH) {
		err = cmd.Process.Kill()
		if errors.Is(err, os.ErrProcessDone) {
			externalLeadWakeProcessEvent("kill_result", cmd, nil)
			return nil
		}
	}
	externalLeadWakeProcessEvent("kill_result", cmd, err)
	return err
}

func setTeamLead(projectFlag, profileFlag string, projectSet bool, role string, leadMode string, leadModeSet bool) error {
	projectDir, profile, err := resolveExistingTeamProfile(projectFlag, profileFlag, projectSet)
	if err != nil {
		return err
	}
	return setTeamLeadForProfile(projectDir, profile, role, leadMode, leadModeSet)
}

func setTeamLeadForProfile(projectDir, profile, role string, leadMode string, leadModeSet bool) error {
	if err := team.ValidateRoleID(role); err != nil {
		return fmt.Errorf("lead: %w", err)
	}
	if leadModeSet {
		var err error
		leadMode, err = normalizeLeadMode(leadMode)
		if err != nil {
			return err
		}
	}
	return withProfileLock(projectDir, profile, func() error {
		t, err := team.ReadProfile(projectDir, profile)
		if err != nil {
			return fmt.Errorf("read team: %w", err)
		}
		if _, ok := memberByRole(t, role); !ok {
			return fmt.Errorf("lead role %q is not a team member", role)
		}
		t.Orchestrated = true
		t.Lead = role
		if leadModeSet {
			t.LeadMode = leadModeForPersist(leadMode)
		}
		return team.WriteProfileUnderLock(projectDir, profile, t)
	})
}

func memberByRole(t team.Team, role string) (team.Member, bool) {
	for _, m := range t.Members {
		if m.Role == role {
			return m, true
		}
	}
	return team.Member{}, false
}

func buildTeamLeadData(profile string, t team.Team) teamLeadData {
	data := teamLeadData{Profile: profile, Orchestrated: t.Orchestrated}
	if !t.Orchestrated {
		return data
	}
	data.Lead = strings.TrimSpace(t.Lead)
	data.LeadHandle = data.Lead
	data.LeadMode = team.EffectiveLeadMode(t)
	if m, ok := memberByRole(t, data.Lead); ok {
		data.LeadHandle = memberHandle(m)
	}
	return data
}
