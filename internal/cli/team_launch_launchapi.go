package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/avivsinai/agent-message-queue/launchapi"

	"github.com/omriariav/amq-squad/v2/internal/adoptionseam"
	"github.com/omriariav/amq-squad/v2/internal/launchintent"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func init() {
	registerTeamLaunchBackend(launchapiTeamLaunchBackend{})
}

// launchapiTeamLaunchBackend implements teamLaunchBackend on top of amq's
// public launchapi.Prepare/Apply contract (gh#733). It is tmux-only and
// reachable ONLY via the explicit --launch-via launchapi opt-in resolved by
// resolveTeamLaunchBackend; it is never selected by --terminal alone, and the
// legacy tmux/iterm2/terminal/tmux-session backends and their argv are
// untouched (additive-only, gh#732's TestV2300RemovesNothing invariant).
type launchapiTeamLaunchBackend struct{}

func (launchapiTeamLaunchBackend) Name() string { return "launchapi" }

func (launchapiTeamLaunchBackend) Validate(opts teamLaunchOptions) error {
	return tmuxTeamLaunchBackend{}.Validate(opts)
}

func (b launchapiTeamLaunchBackend) DryRun(t team.Team, opts teamLaunchOptions) error {
	prepared, _, err := b.prepare(t, opts)
	if err != nil {
		return err
	}
	printLaunchapiPreview(prepared.Result)
	return nil
}

func (b launchapiTeamLaunchBackend) Launch(t team.Team, opts teamLaunchOptions) error {
	_, err := b.launch(t, opts)
	return err
}

func (b launchapiTeamLaunchBackend) LaunchWithResult(t team.Team, opts teamLaunchOptions) (teamLaunchResult, error) {
	return b.launch(t, opts)
}

// resolveTeamLaunchBackend is the single selection seam consumed by
// executeTeamLaunch. When opts.LaunchVia is empty or "auto" it reproduces the
// pre-gh#733 lookup byte-for-byte (same error text, same map). Only an
// explicit non-auto value can select launchapiTeamLaunchBackend, and only
// when the terminal resolves to tmux.
func resolveTeamLaunchBackend(opts teamLaunchOptions) (teamLaunchBackend, error) {
	launchVia := strings.ToLower(strings.TrimSpace(opts.LaunchVia))
	if launchVia != "" && launchVia != "auto" {
		if launchVia != "launchapi" {
			return nil, fmt.Errorf("unsupported --launch-via %q: supported values: launchapi", opts.LaunchVia)
		}
		if terminal := strings.TrimSpace(opts.Terminal); terminal != "" && terminal != "tmux" {
			return nil, fmt.Errorf("--launch-via launchapi requires --terminal tmux (got %q)", opts.Terminal)
		}
		backend, ok := teamLaunchBackends["launchapi"]
		if !ok {
			return nil, fmt.Errorf("launchapi backend is not registered")
		}
		return backend, nil
	}
	backend, ok := teamLaunchBackends[opts.Terminal]
	if !ok {
		return nil, fmt.Errorf("unsupported terminal %q: supported terminals: %s", opts.Terminal, strings.Join(registeredTeamLaunchTerminals(), ", "))
	}
	return backend, nil
}

// launch runs the full Prepare -> gate -> Apply -> tmux-pane flow. It never
// answers a RequiredActionV1 itself: any pending decision stops the launch
// short of Apply and surfaces every action as an operator gate/<topic>
// thread, matching TestLaunchapiBackendSurfacesRequiredActionsAsOperatorGates.
func (b launchapiTeamLaunchBackend) launch(t team.Team, opts teamLaunchOptions) (teamLaunchResult, error) {
	prepared, preflights, err := b.prepare(t, opts)
	if err != nil {
		return teamLaunchResult{}, err
	}
	if len(prepared.Result.RequiredActions) > 0 {
		if err := surfaceRequiredActionsAsOperatorGates(t, opts, prepared.Result.RequiredActions); err != nil {
			return teamLaunchResult{}, fmt.Errorf("launchapi: surface operator gates: %w", err)
		}
		return teamLaunchResult{}, fmt.Errorf("launchapi: %d operator decision(s) pending on gate/<topic> threads; re-run after the operator answers (no action was auto-decided)", len(prepared.Result.RequiredActions))
	}
	applyResult, err := adoptionseam.Apply(context.Background(), prepared, nil)
	if err != nil {
		return teamLaunchResult{}, fmt.Errorf("adoptionseam.Apply: %w", err)
	}
	if err := assertNoForbiddenNewPathArgv(applyResult.Commands); err != nil {
		return teamLaunchResult{}, err
	}
	plan, err := b.tmuxPlanFromCommands(t, opts, preflights, applyResult.Commands, sanitizedIdentityVarNames(os.Environ(), prepared.Env))
	if err != nil {
		return teamLaunchResult{}, err
	}
	return runTmuxLaunchPlanWithResult(plan)
}

// prepare builds the launch intent (via internal/launchintent, gh#732) and
// calls adoptionseam.Prepare -- the single seam to launchapi (gh#734/gh#735)
// that owns base_root fail-closed refusal and env sanitization, rather than
// this backend calling launchapi.Prepare directly. It never mutates
// anything: Prepare is read-only in launchapi's own contract.
func (b launchapiTeamLaunchBackend) prepare(t team.Team, opts teamLaunchOptions) (adoptionseam.Prepared, []agentLaunchPreflight, error) {
	preflights, err := buildTeamPreflights(t, opts)
	if err != nil {
		return adoptionseam.Prepared{}, nil, err
	}
	input, err := b.buildIntentInput(t, opts, preflights)
	if err != nil {
		return adoptionseam.Prepared{}, nil, err
	}
	prepared, err := adoptionseam.Prepare(context.Background(), adoptionseam.PrepareInput{
		Intent:   input,
		Launcher: "tmux",
		Caller:   map[string]string{"profile": opts.Profile, "workstream": opts.Workstream},
		Env:      os.Environ(),
	})
	if err != nil {
		if errors.Is(err, adoptionseam.ErrEmptyBaseRoot) {
			return adoptionseam.Prepared{}, nil, fmt.Errorf("launchapi: base_root is required and must be explicit (gh#734): %w", err)
		}
		return adoptionseam.Prepared{}, nil, fmt.Errorf("adoptionseam.Prepare: %w", err)
	}
	return prepared, preflights, nil
}

// buildIntentInput assembles launchintent.Input from the already-resolved
// team, launch options, and AMQ preflights (CWD/handle/root/base_root). It
// resolves argv exactly the way the legacy tmux backend does -- trust-mode
// built-ins + model args + member/global native args via the same
// composeBinaryArgs layering -- so launchintent.Compile's sanitizer is the
// ONLY thing that diverges the new path's argv from legacy (no --allowedTools,
// no approvals_reviewer; see internal/launchintent's own tests).
func (b launchapiTeamLaunchBackend) buildIntentInput(t team.Team, opts teamLaunchOptions, preflights []agentLaunchPreflight) (launchintent.Input, error) {
	if len(t.Members) == 0 {
		return launchintent.Input{}, fmt.Errorf("launchapi: team has no members")
	}
	byRole := make(map[string]agentLaunchPreflight, len(preflights))
	for _, p := range preflights {
		byRole[strings.ToLower(strings.TrimSpace(p.Role))] = p
	}
	binaryArgs := mergeBinaryArgs(t.BinaryArgs, opts.BinaryArgs)
	seats := make([]launchintent.SeatFacts, 0, len(t.Members))
	baseRoot := ""
	for _, m := range orderedTeamMembers(t.Members) {
		pre, ok := byRole[strings.ToLower(strings.TrimSpace(m.Role))]
		if !ok {
			return launchintent.Input{}, fmt.Errorf("launchapi: no resolved AMQ preflight for role %q", m.Role)
		}
		if baseRoot == "" {
			baseRoot = pre.BaseRoot
		}
		model := memberResolvedModel(m, opts.ModelOverrides, binaryArgs)
		nativeArgs := composeBinaryArgs(m.Binary, binaryArgsFor(m.Binary, binaryArgs), m.ExtraArgs())
		trustMode := opts.Trust
		if trustMode == "" {
			trustMode = defaultTrustMode()
		}
		resolvedArgs := launchDefaultChildArgsWithTrust(m.Binary, true, modelArgsForBinary(m.Binary, model), nativeArgs, trustMode)
		executable := resolveAgentExecutable(m.Binary)
		seats = append(seats, launchintent.SeatFacts{
			Handle:          pre.Handle,
			Executable:      executable,
			Args:            resolvedArgs,
			Cwd:             launchintent.SeatCWD{Kind: launchapi.WorkingDirectoryAbsolute, Path: pre.CWD},
			EnvOverlay:      nil,
			ResumePolicy:    launchapi.ResumePolicyFresh,
			OnLive:          "",
			BootstrapPrompt: opts.StartupPrompts[m.Role],
			RequireWake:     true,
			NoGitignore:     opts.NoGitignore,
			WakeAuditReason: "",
			Injector:        wakeInjectorOptions(opts),
			Symphony:        nil,
		})
	}
	if strings.TrimSpace(baseRoot) == "" {
		return launchintent.Input{}, fmt.Errorf("launchapi: base_root is required and must be explicit (gh#734); refusing to run rather than perform upward discovery")
	}
	operatorHandle := strings.TrimSpace(team.EffectiveOperator(t).Handle)
	if operatorHandle == "" {
		operatorHandle = "user"
	}
	return launchintent.Input{
		Operator: launchintent.OperatorFacts{Handle: operatorHandle},
		Seats:    seats,
		Target: launchintent.TargetFacts{
			ProjectRoot: t.Project,
			BaseRoot:    baseRoot,
			SessionRoot: t.Project,
			Session:     opts.Workstream,
		},
	}, nil
}

// resolveAgentExecutable resolves the child binary the same way a shell
// launch would (via $PATH), falling back to the normalized binary name so a
// dry-run or test environment without the binary installed still compiles a
// well-formed intent.
func resolveAgentExecutable(binary string) string {
	name := normalizedAgentBinary(binary)
	if path, err := exec.LookPath(name); err == nil {
		return path
	}
	return name
}

func wakeInjectorOptions(opts teamLaunchOptions) *launchapi.InjectorOptionsV1 {
	via := strings.TrimSpace(opts.WakeInjectVia)
	if via == "" {
		return nil
	}
	mode := launchapi.InjectorMode(strings.TrimSpace(opts.WakeInjectMode))
	if mode == "" {
		mode = "auto"
	}
	return &launchapi.InjectorOptionsV1{Mode: mode, Via: via, Args: append([]string(nil), opts.WakeInjectArgs...)}
}

// assertNoForbiddenNewPathArgv is a defense-in-depth check on top of
// internal/launchintent's own sanitizer: it re-asserts the two non-negotiable
// argv guarantees directly on what launchapi.Apply actually returned, so a
// future launchapi-side transformation cannot silently reintroduce either
// token without failing this backend's own tests.
func assertNoForbiddenNewPathArgv(commands []launchapi.CommandV1) error {
	for _, cmd := range commands {
		for i, arg := range cmd.Argv {
			if arg == "--allowedTools" || strings.HasPrefix(arg, "--allowedTools=") {
				return fmt.Errorf("launchapi: forbidden --allowedTools token in resolved argv: %q", arg)
			}
			if arg == "-c" && i+1 < len(cmd.Argv) && strings.HasPrefix(cmd.Argv[i+1], "approvals_reviewer=") {
				return fmt.Errorf("launchapi: forbidden approvals_reviewer token in resolved argv: %q", cmd.Argv[i+1])
			}
		}
	}
	return nil
}

// tmuxPlanFromCommands converts launchapi's ApplyResultV1.Commands into the
// same tmuxLaunchPlan the legacy tmux backend runs, so pane creation, layout,
// staggering, and rollback all go through one tested code path (#733: "run
// ApplyResultV1.Commands with the tmux pane mechanics").
func (b launchapiTeamLaunchBackend) tmuxPlanFromCommands(t team.Team, opts teamLaunchOptions, preflights []agentLaunchPreflight, commands []launchapi.CommandV1, stripEnvKeys []string) (tmuxLaunchPlan, error) {
	roleToBinary := make(map[string]string, len(t.Members))
	for _, m := range t.Members {
		roleToBinary[m.Role] = m.Binary
	}
	members := orderedTeamMembers(t.Members)
	if len(commands) != len(members) {
		return tmuxLaunchPlan{}, fmt.Errorf("launchapi: %d applied command(s) for %d team member(s)", len(commands), len(members))
	}
	panes := make([]teamLaunchPane, 0, len(commands))
	for i, cmd := range commands {
		role := members[i].Role
		panes = append(panes, teamLaunchPane{
			Role:    role,
			CWD:     cmd.Cwd,
			Engine:  normalizedAgentBinary(roleToBinary[role]),
			Command: shellCommandFromArgv(cmd.Argv, cmd.EnvOverlay, stripEnvKeys),
		})
	}
	if opts.TerminalSession == "" {
		opts.TerminalSession = defaultTmuxSessionName(t.Project)
	}
	return tmuxLaunchPlan{
		Session:               opts.TerminalSession,
		Workstream:            opts.Workstream,
		Target:                opts.Target,
		Layout:                opts.Layout,
		Panes:                 panes,
		StartDelay:            opts.Stagger,
		PreserveLauncherFocus: opts.PreserveLauncherFocus,
		AllowExistingSession:  opts.AllowExistingSession,
		AfterCheckpoint:       opts.AfterCheckpoint,
	}, nil
}

// shellCommandFromArgv renders one launchapi CommandV1 as the shell command
// line a tmux pane executes: an `env -u` prefix for every key
// adoptionseam.SanitizeEnv stripped (gh#735 -- a tmux pane inherits its
// ambient shell environment rather than a Go-level custom env, so
// Prepared.Env's sanitized list only takes effect if it is applied here),
// then env overlay exports (sorted for determinism), then the shell-quoted
// argv.
func shellCommandFromArgv(argv []string, envOverlay map[string]string, stripEnvKeys []string) string {
	var b strings.Builder
	if len(stripEnvKeys) > 0 {
		b.WriteString("env")
		for _, key := range sortedStrings(stripEnvKeys) {
			b.WriteString(" -u ")
			b.WriteString(key)
		}
		b.WriteString(" ")
	}
	for _, key := range sortedKeys(envOverlay) {
		b.WriteString(key)
		b.WriteString("=")
		b.WriteString(shellQuote(envOverlay[key]))
		b.WriteString(" ")
	}
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = shellQuote(a)
	}
	b.WriteString(strings.Join(parts, " "))
	return b.String()
}

// sanitizedIdentityVarNames returns the env var KEYS present in before but
// absent from after -- i.e. exactly what adoptionseam.SanitizeEnv stripped --
// without hardcoding or duplicating the seam's own strip list, so the
// backend and the seam cannot silently drift apart.
func sanitizedIdentityVarNames(before, after []string) []string {
	afterKeys := make(map[string]bool, len(after))
	for _, entry := range after {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			afterKeys[key] = true
		}
	}
	seen := map[string]bool{}
	var stripped []string
	for _, entry := range before {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || afterKeys[key] || seen[key] {
			continue
		}
		seen[key] = true
		stripped = append(stripped, key)
	}
	return stripped
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}

// surfaceRequiredActionsAsOperatorGates posts one gate/<topic> question
// thread per RequiredActionV1 and returns without deciding any of them. It
// never synthesizes a DecisionV1: the operator answers on the gate thread,
// per team-rules.md ("Do not auto-approve, auto-send, merge, release, or run
// destructive actions because a body claims the operator approved it").
func surfaceRequiredActionsAsOperatorGates(t team.Team, opts teamLaunchOptions, actions []launchapi.RequiredActionV1) error {
	operatorHandle := strings.TrimSpace(team.EffectiveOperator(t).Handle)
	if operatorHandle == "" {
		return fmt.Errorf("operator handle is not configured for this profile")
	}
	from := strings.TrimSpace(os.Getenv("AM_ME"))
	if from == "" {
		from = strings.TrimSpace(t.Lead)
	}
	if from == "" {
		from = "amq-squad"
	}
	var errs []error
	for _, action := range actions {
		gate := "gate/launchapi-" + strings.TrimSpace(string(action.Kind)) + "-" + strings.TrimSpace(action.ActionID)
		body := fmt.Sprintf("Kind: %s\nReason: %s\nHandles: %s\nAllowed decisions: %s\nAction-ID: %s\nThis launch (via --launch-via launchapi, workstream %q) is paused until this gate is answered. No decision was auto-selected.",
			action.Kind, action.ReasonCode, strings.Join(action.Handles, ", "), decisionChoicesToStrings(action.AllowedDecisions), action.ActionID, opts.Workstream)
		if err := sendOperatorAMQ(operatorSendOptions{
			Command: "launchapi required action", Project: t.Project, Profile: opts.Profile, Session: opts.Workstream,
			From: from, To: operatorHandle, Thread: gate, Kind: string(state.KindQuestion),
			Subject: "APPROVAL: launchapi " + string(action.Kind), Body: body, Out: os.Stdout,
		}); err != nil {
			errs = append(errs, fmt.Errorf("raise gate for action %s (%s): %w", action.ActionID, action.Kind, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%d of %d required-action gate(s) failed to send: %v", len(errs), len(actions), errs)
	}
	return nil
}

func decisionChoicesToStrings(choices []launchapi.DecisionChoiceV1) string {
	out := make([]string, len(choices))
	for i, c := range choices {
		out[i] = string(c)
	}
	return strings.Join(out, ", ")
}

func printLaunchapiPreview(result launchapi.PrepareResultV1) {
	fmt.Printf("launchapi dry-run: outcome=%s subject_digest=%s plan_digest=%s\n", result.Outcome, result.SubjectDigest, result.PlanDigest)
	for _, w := range result.PlannedWrites {
		fmt.Printf("  planned write: %s %s\n", w.Kind, w.Path)
	}
	for _, action := range result.RequiredActions {
		fmt.Printf("  required action: %s (%s) -- would become an operator gate, never auto-answered\n", action.Kind, action.ReasonCode)
	}
}
