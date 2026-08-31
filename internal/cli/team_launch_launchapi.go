package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
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
// public launchapi.Prepare/Apply contract (gh#733). It is tmux-only. As of
// v2.31.0 (gh#755) it is the default resolveTeamLaunchBackend selects
// whenever the terminal resolves to tmux and --launch-via is empty, "auto",
// or the explicit "launchapi" opt-in; the legacy tmux/iterm2/terminal/
// tmux-session backends and their argv remain untouched and stay reachable
// for one release via the explicit "--launch-via legacy" opt-out (deleted in
// v2.32.0).
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
// executeTeamLaunch (gh#755: v2.31.0's default flip). When opts.LaunchVia is
// empty or "auto" and the terminal resolves to tmux, it now selects
// launchapiTeamLaunchBackend by default. A terminal that does not resolve to
// tmux (iterm2, terminal, tmux-session) still falls back to the legacy
// per-terminal lookup, since the launchapi backend is tmux-only. The legacy
// tmux pane driver stays reachable for one release (v2.31.x) via the
// explicit opt-out "--launch-via legacy", byte-identical to v2.30.1's
// empty/"auto" behavior; it is deleted in v2.32.0. An explicit
// "--launch-via launchapi" keeps working exactly as before. The
// unsupported-terminal error text (legacyTeamLaunchBackend) is unchanged.
func resolveTeamLaunchBackend(opts teamLaunchOptions) (teamLaunchBackend, error) {
	launchVia := strings.ToLower(strings.TrimSpace(opts.LaunchVia))
	switch launchVia {
	case "legacy":
		return legacyTeamLaunchBackend(opts)
	case "", "auto":
		if terminalResolvesToTmux(opts.Terminal) {
			return registeredLaunchapiBackend()
		}
		return legacyTeamLaunchBackend(opts)
	case "launchapi":
		if !terminalResolvesToTmux(opts.Terminal) {
			return nil, fmt.Errorf("--launch-via launchapi requires --terminal tmux (got %q)", opts.Terminal)
		}
		return registeredLaunchapiBackend()
	default:
		return nil, fmt.Errorf("unsupported --launch-via %q: supported values: launchapi, legacy", opts.LaunchVia)
	}
}

// terminalResolvesToTmux reports whether opts.Terminal selects (or, if
// empty, defaults to) the tmux terminal backend. Preserves the pre-gh#755
// tolerance of an empty Terminal value.
func terminalResolvesToTmux(terminal string) bool {
	t := strings.TrimSpace(terminal)
	return t == "" || t == "tmux"
}

// registeredLaunchapiBackend looks up the launchapi backend in
// teamLaunchBackends, shared by both the new default path and the explicit
// "--launch-via launchapi" opt-in so they resolve identically.
func registeredLaunchapiBackend() (teamLaunchBackend, error) {
	backend, ok := teamLaunchBackends["launchapi"]
	if !ok {
		return nil, fmt.Errorf("launchapi backend is not registered")
	}
	return backend, nil
}

// legacyTeamLaunchBackend reproduces the pre-gh#733 terminal-map lookup
// byte-for-byte (same error text, same map), reachable now only via the
// explicit "--launch-via legacy" opt-in or a non-tmux terminal.
func legacyTeamLaunchBackend(opts teamLaunchOptions) (teamLaunchBackend, error) {
	backend, ok := teamLaunchBackends[opts.Terminal]
	if !ok {
		return nil, fmt.Errorf("unsupported terminal %q: supported terminals: %s", opts.Terminal, strings.Join(registeredTeamLaunchTerminals(), ", "))
	}
	return backend, nil
}

// launch runs the full Prepare -> gate/decide -> Apply -> tmux-pane flow. It
// never synthesizes a decision itself: an operator answer only reaches Apply
// when the caller supplied it explicitly via --launchapi-decision, validated
// against that action's AllowedDecisions. Any action left undecided stops
// the launch short of Apply and surfaces (only that action) as an operator
// gate/<topic> thread; re-running with the answer completes the launch.
func (b launchapiTeamLaunchBackend) launch(t team.Team, opts teamLaunchOptions) (teamLaunchResult, error) {
	prepared, preflights, err := b.prepare(t, opts)
	if err != nil {
		return teamLaunchResult{}, err
	}
	decisions, missing, err := resolveLaunchapiDecisions(prepared.Result.RequiredActions, opts.LaunchapiDecisions)
	if err != nil {
		return teamLaunchResult{}, err
	}
	if len(missing) > 0 {
		if err := surfaceRequiredActionsAsOperatorGates(t, opts, missing); err != nil {
			return teamLaunchResult{}, fmt.Errorf("launchapi: surface operator gates: %w", err)
		}
		return teamLaunchResult{}, fmt.Errorf("launchapi: %d of %d operator decision(s) pending on gate/<topic> threads; re-run with --launchapi-decision ACTION_ID=CHOICE after the operator answers (no action was auto-decided)", len(missing), len(prepared.Result.RequiredActions))
	}
	applyResult, err := adoptionseam.Apply(context.Background(), prepared, decisions)
	if err != nil {
		return teamLaunchResult{}, fmt.Errorf("adoptionseam.Apply: %w", err)
	}
	if err := recordAppliedLaunchapiDecisions(t, opts, prepared.Result.RequiredActions, decisions); err != nil {
		fmt.Fprintf(os.Stderr, "launchapi: warning: %v\n", err)
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
// this backend calling launchapi.Prepare directly.
//
// gh#747: launchapi.Negotiate cannot distinguish the scoped-argv-grammar
// floor (docs/amq-0.73.0-adoption-verdict.md section 6), so this runs a
// two-phase Prepare instead of a single call:
//
//  1. Probe with the conservative intent (no capability facts yet, so
//     internal/launchintent's sanitizer drops every gated token in the safe
//     direction). Its Preview.Capabilities carries the seat providers'
//     observed ArgvGrammarVersion and approvals_reviewer support.
//  2. Recompile with those facts threaded into each seat's SeatFacts, then
//     Prepare again.
//
// Both calls are read-only and deterministic (verified directly, see the
// verdict doc), so the probe cannot itself mutate anything or drift from
// what phase 2 later sends to Apply. Apply is always called with the
// phase-2 Prepared this method returns, never the probe's.
func (b launchapiTeamLaunchBackend) prepare(t team.Team, opts teamLaunchOptions) (adoptionseam.Prepared, []agentLaunchPreflight, error) {
	preflights, err := buildTeamPreflights(t, opts)
	if err != nil {
		return adoptionseam.Prepared{}, nil, err
	}

	probeInput, err := b.buildIntentInput(t, opts, preflights, nil)
	if err != nil {
		return adoptionseam.Prepared{}, nil, err
	}
	probePrepared, err := b.callPrepare(opts, probeInput)
	if err != nil {
		return adoptionseam.Prepared{}, nil, err
	}

	capabilities := capabilitiesByProvider(probePrepared.Result.Preview.Capabilities)
	finalInput, err := b.buildIntentInput(t, opts, preflights, capabilities)
	if err != nil {
		return adoptionseam.Prepared{}, nil, err
	}
	finalPrepared, err := b.callPrepare(opts, finalInput)
	if err != nil {
		return adoptionseam.Prepared{}, nil, err
	}
	return finalPrepared, preflights, nil
}

// callPrepare is the single call site to adoptionseam.Prepare, shared by
// both phases so the request-building and error-wrapping stay identical
// between the probe and the final call.
func (b launchapiTeamLaunchBackend) callPrepare(opts teamLaunchOptions, input launchintent.Input) (adoptionseam.Prepared, error) {
	prepared, err := adoptionseam.Prepare(context.Background(), adoptionseam.PrepareInput{
		Intent:   input,
		Launcher: "tmux",
		Caller:   map[string]string{"profile": opts.Profile, "workstream": opts.Workstream},
		Env:      os.Environ(),
	})
	if err != nil {
		if errors.Is(err, adoptionseam.ErrEmptyBaseRoot) {
			return adoptionseam.Prepared{}, fmt.Errorf("launchapi: base_root is required and must be explicit (gh#734): %w", err)
		}
		return adoptionseam.Prepared{}, fmt.Errorf("adoptionseam.Prepare: %w", err)
	}
	return prepared, nil
}

// capabilitiesByProvider indexes a Prepare probe's Preview.Capabilities by
// provider name (e.g. "claude", "codex") for buildIntentInput's per-seat
// lookup. A nil/empty slice yields an empty map, so a missing provider
// entry (including the phase-1 probe call, which passes no capabilities at
// all) naturally reads back as the zero-value ProviderCapabilitiesV1 -- the
// safe, everything-below-floor direction.
func capabilitiesByProvider(caps []launchapi.ProviderCapabilitiesV1) map[string]launchapi.ProviderCapabilitiesV1 {
	out := make(map[string]launchapi.ProviderCapabilitiesV1, len(caps))
	for _, c := range caps {
		out[c.Provider] = c
	}
	return out
}

// reviewerOverrideAllowedFrom reports whether the observed capability
// carries a config_override entry for approvals_reviewer
// (docs/amq-0.73.0-adoption-verdict.md section 4: this is codex's real gate
// for the reviewer override, not GrammarVersion, which stays 1 on codex
// across every measured version).
func reviewerOverrideAllowedFrom(cap launchapi.ProviderCapabilitiesV1) bool {
	for _, override := range cap.ConfigOverrides {
		if override.Key == "approvals_reviewer" {
			return true
		}
	}
	return false
}

// buildIntentInput assembles launchintent.Input from the already-resolved
// team, launch options, and AMQ preflights (CWD/handle/root/base_root). It
// resolves argv exactly the way the legacy tmux backend does -- trust-mode
// built-ins + model args + member/global native args via the same
// composeBinaryArgs layering -- so internal/launchintent.Compile's
// contract-aware sanitizer is the only thing that diverges the new path's
// argv from legacy.
//
// capabilities carries the per-provider facts observed from a prior probe
// Prepare call (gh#747's two-phase design), keyed by provider name; nil on
// the phase-1 probe call itself, which is exactly what makes that call
// conservative -- a missing provider entry reads back as the zero-value
// ProviderCapabilitiesV1, so every gated seat fact defaults to "below
// floor" without any special-casing here.
//
// A claude seat eligible for #296's worker preauth (claudeWorkerPreauthEligible,
// the same eligibility check the legacy backend uses) gets the scoped grant
// candidate appended to its Args unconditionally; internal/launchintent's
// sanitizer is what decides whether it survives, based on the seat's
// ArgvGrammarVersion fact below. This keeps eligibility and grammar-gating
// as two separate, independently testable decisions.
func (b launchapiTeamLaunchBackend) buildIntentInput(t team.Team, opts teamLaunchOptions, preflights []agentLaunchPreflight, capabilities map[string]launchapi.ProviderCapabilitiesV1) (launchintent.Input, error) {
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
		if claudeWorkerPreauthEligible(t.Project, opts.Profile, m.Role, m.Binary) {
			resolvedArgs = append(resolvedArgs, "--allowedTools", launchintent.ScopedPreauthGrant)
		}
		executable := resolveAgentExecutable(m.Binary)
		cap := capabilities[normalizedAgentBinary(m.Binary)]
		// gh#748: SessionName is only ever set for claude seats. Codex has
		// no -n/--name argRules entry on any measured version (t8's own
		// finding), so leaving it empty there is belt-and-suspenders on top
		// of AllowedArgumentForms naturally never containing "-n" for
		// codex; naming still only actually reaches argv when
		// internal/launchintent.Compile also sees that capability fact.
		sessionName := ""
		if normalizedAgentBinary(m.Binary) == "claude" {
			sessionName = opts.Workstream + "/" + pre.Handle
		}
		seats = append(seats, launchintent.SeatFacts{
			Handle:                  pre.Handle,
			Executable:              executable,
			Args:                    resolvedArgs,
			Cwd:                     launchintent.SeatCWD{Kind: launchapi.WorkingDirectoryAbsolute, Path: pre.CWD},
			EnvOverlay:              nil,
			ResumePolicy:            launchapi.ResumePolicyFresh,
			OnLive:                  "",
			BootstrapPrompt:         opts.StartupPrompts[m.Role],
			RequireWake:             true,
			NoGitignore:             opts.NoGitignore,
			WakeAuditReason:         "",
			Injector:                wakeInjectorOptions(opts),
			Symphony:                nil,
			ArgvGrammarVersion:      cap.GrammarVersion,
			ReviewerOverrideAllowed: reviewerOverrideAllowedFrom(cap),
			AllowedArgumentForms:    append([]string(nil), cap.AllowedArgumentForms...),
			SessionName:             sessionName,
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
// internal/launchintent's own sanitizer: it re-asserts gh#747's
// non-negotiables directly on what launchapi.Apply actually returned, so a
// future launchapi-side transformation cannot silently reintroduce a
// forbidden token without failing this backend's own tests.
//
// approvals_reviewer is deliberately no longer forbidden here: at or above
// the floor it is the intended, restored behavior (gh#747). What stays
// forbidden regardless of floor: the equals-joined --allowedTools spelling
// (never accepted upstream at any measured version, see
// docs/amq-0.73.0-adoption-verdict.md section 3), and any --allowedTools
// value other than launchintent.ScopedPreauthGrant exactly -- gh#747's
// non-negotiable is "Bash(gh pr create:*) only, never widen the grant," so
// this mirrors internal/launchintent.sanitizeNewPathArgs's exact-equality
// gate rather than a shape check: a user-supplied value like
// `Bash(rm -rf:*)` must be caught here too, in case a future change to the
// backend ever composes Commands without going through that sanitizer.
func assertNoForbiddenNewPathArgv(commands []launchapi.CommandV1) error {
	for _, cmd := range commands {
		for i, arg := range cmd.Argv {
			if strings.HasPrefix(arg, "--allowedTools=") {
				return fmt.Errorf("launchapi: forbidden equals-joined --allowedTools token in resolved argv: %q", arg)
			}
			if arg != "--allowedTools" {
				continue
			}
			if i+1 >= len(cmd.Argv) {
				return fmt.Errorf("launchapi: --allowedTools with no value in resolved argv")
			}
			if value := cmd.Argv[i+1]; value != launchintent.ScopedPreauthGrant {
				return fmt.Errorf("launchapi: forbidden --allowedTools value in resolved argv, only %q is ever allowed: got %q", launchintent.ScopedPreauthGrant, value)
			}
		}
	}
	return nil
}

// tmuxPlanFromCommands converts launchapi's ApplyResultV1.Commands into the
// same tmuxLaunchPlan the legacy tmux backend runs, so pane creation, layout,
// staggering, and rollback all go through one tested code path (#733: "run
// ApplyResultV1.Commands with the tmux pane mechanics").
//
// Commands are matched to team members by cwd, not by position: launchapi
// v0.70.0's public ApplyResultV1.Commands carries no participant handle and
// its ordering relative to the request's participant list is not documented
// as a contract in the public launchapi package (only internallaunch, which
// this module does not depend on, could confirm it) -- so this backend does
// not assume it. Every resolved preflight cwd is already required to be
// distinct within one launch (buildTeamPreflights resolves one seat per
// role), which makes cwd an unambiguous join key here.
func (b launchapiTeamLaunchBackend) tmuxPlanFromCommands(t team.Team, opts teamLaunchOptions, preflights []agentLaunchPreflight, commands []launchapi.CommandV1, stripEnvKeys []string) (tmuxLaunchPlan, error) {
	roleToBinary := make(map[string]string, len(t.Members))
	for _, m := range t.Members {
		roleToBinary[m.Role] = m.Binary
	}
	roleByCWD := make(map[string]string, len(preflights))
	for _, p := range preflights {
		if other, dup := roleByCWD[p.CWD]; dup {
			return tmuxLaunchPlan{}, fmt.Errorf("launchapi: roles %q and %q share cwd %q; cwd-based command matching requires distinct seat cwds", other, p.Role, p.CWD)
		}
		roleByCWD[p.CWD] = p.Role
	}
	if len(commands) != len(preflights) {
		return tmuxLaunchPlan{}, fmt.Errorf("launchapi: %d applied command(s) for %d team member(s)", len(commands), len(preflights))
	}
	panes := make([]teamLaunchPane, 0, len(commands))
	seenRoles := make(map[string]bool, len(commands))
	for _, cmd := range commands {
		role, ok := roleByCWD[cmd.Cwd]
		if !ok {
			return tmuxLaunchPlan{}, fmt.Errorf("launchapi: applied command cwd %q does not match any resolved preflight cwd", cmd.Cwd)
		}
		if seenRoles[role] {
			return tmuxLaunchPlan{}, fmt.Errorf("launchapi: two applied commands both resolved to role %q via cwd %q", role, cmd.Cwd)
		}
		seenRoles[role] = true
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
		sortedStripKeys := append([]string(nil), stripEnvKeys...)
		sort.Strings(sortedStripKeys)
		for _, key := range sortedStripKeys {
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

func sortedKeys(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// resolveLaunchapiDecisions matches supplied (--launchapi-decision
// ACTION_ID=CHOICE pairs) against Prepare's actual RequiredActions. Every
// action with a valid supplied decision is returned in decisions; every
// action without one is returned in missing, for the caller to surface as a
// gate. A supplied decision for an ActionID Prepare did not return is a
// stale answer and errors rather than being silently ignored; a supplied
// choice not in that action's AllowedDecisions errors naming the allowed
// set.
func resolveLaunchapiDecisions(actions []launchapi.RequiredActionV1, supplied map[string]string) ([]launchapi.DecisionV1, []launchapi.RequiredActionV1, error) {
	byID := make(map[string]launchapi.RequiredActionV1, len(actions))
	for _, a := range actions {
		byID[a.ActionID] = a
	}
	for actionID := range supplied {
		if _, ok := byID[actionID]; !ok {
			return nil, nil, fmt.Errorf("launchapi: --launchapi-decision for action %q does not match any action Prepare returned (stale answer)", actionID)
		}
	}
	var decisions []launchapi.DecisionV1
	var missing []launchapi.RequiredActionV1
	for _, action := range actions {
		raw, ok := supplied[action.ActionID]
		if !ok {
			missing = append(missing, action)
			continue
		}
		choice := launchapi.DecisionChoiceV1(raw)
		allowed := false
		for _, c := range action.AllowedDecisions {
			if c == choice {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, nil, fmt.Errorf("launchapi: --launchapi-decision %s=%s is not in the allowed set for this %s action: allowed choices are %s",
				action.ActionID, raw, action.Kind, decisionChoicesToStrings(action.AllowedDecisions))
		}
		decisions = append(decisions, launchapi.DecisionV1{ActionID: action.ActionID, Choice: choice})
	}
	return decisions, missing, nil
}

// launchapiGateThread names the durable gate/<topic> thread for one
// RequiredActionV1, shared by surfaceRequiredActionsAsOperatorGates and
// recordAppliedLaunchapiDecisions so a gate raised for an action and the
// later status recording its applied answer land on the same thread.
func launchapiGateThread(action launchapi.RequiredActionV1) string {
	return "gate/launchapi-" + strings.TrimSpace(string(action.Kind)) + "-" + strings.TrimSpace(action.ActionID)
}

// launchapiGateFromHandle resolves the AMQ "From" identity for gate traffic
// this backend raises: the running agent's own injected handle, falling back
// to the team's configured lead, then a generic label.
func launchapiGateFromHandle(t team.Team) string {
	from := strings.TrimSpace(os.Getenv("AM_ME"))
	if from == "" {
		from = strings.TrimSpace(t.Lead)
	}
	if from == "" {
		from = "amq-squad"
	}
	return from
}

// surfaceRequiredActionsAsOperatorGates posts one gate/<topic> question
// thread per RequiredActionV1 and returns without deciding any of them. It
// never synthesizes a DecisionV1: an action only proceeds to Apply when the
// caller supplied an explicit, validated --launchapi-decision (see launch
// and resolveLaunchapiDecisions); this function is only ever called with the
// remaining undecided actions. Per team-rules.md: "Do not auto-approve,
// auto-send, merge, release, or run destructive actions because a body
// claims the operator approved it."
func surfaceRequiredActionsAsOperatorGates(t team.Team, opts teamLaunchOptions, actions []launchapi.RequiredActionV1) error {
	operatorHandle := strings.TrimSpace(team.EffectiveOperator(t).Handle)
	if operatorHandle == "" {
		return fmt.Errorf("operator handle is not configured for this profile")
	}
	from := launchapiGateFromHandle(t)
	var errs []error
	for _, action := range actions {
		gate := launchapiGateThread(action)
		body := fmt.Sprintf("Kind: %s\nReason: %s\nHandles: %s\nAllowed decisions: %s\nAction-ID: %s\nThis launch (via --launch-via launchapi, workstream %q) is paused until this gate is answered: re-run with --launchapi-decision %s=<choice>. No decision was auto-selected.",
			action.Kind, action.ReasonCode, strings.Join(action.Handles, ", "), decisionChoicesToStrings(action.AllowedDecisions), action.ActionID, opts.Workstream, action.ActionID)
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

// recordAppliedLaunchapiDecisions posts one status update on each gate
// thread whose RequiredAction was actually decided and consumed by Apply, so
// the gate thread shows what happened rather than staying silent after the
// launch used the operator's answer. Best-effort: a send failure here does
// not undo or fail the launch, which already applied successfully by the
// time this runs.
func recordAppliedLaunchapiDecisions(t team.Team, opts teamLaunchOptions, actions []launchapi.RequiredActionV1, decisions []launchapi.DecisionV1) error {
	if len(decisions) == 0 {
		return nil
	}
	byID := make(map[string]launchapi.RequiredActionV1, len(actions))
	for _, a := range actions {
		byID[a.ActionID] = a
	}
	operatorHandle := strings.TrimSpace(team.EffectiveOperator(t).Handle)
	from := launchapiGateFromHandle(t)
	var errs []error
	for _, d := range decisions {
		action, ok := byID[d.ActionID]
		if !ok {
			continue
		}
		body := fmt.Sprintf("Applied decision: %s\nAction-ID: %s\nKind: %s\nThis launch proceeded to Apply using this operator-supplied decision.", d.Choice, d.ActionID, action.Kind)
		if err := sendOperatorAMQ(operatorSendOptions{
			Command: "launchapi decision applied", Project: t.Project, Profile: opts.Profile, Session: opts.Workstream,
			From: from, To: operatorHandle, Thread: launchapiGateThread(action), Kind: string(state.KindStatus),
			Subject: "DONE: launchapi " + string(action.Kind) + " decided " + string(d.Choice), Body: body, Out: os.Stdout,
		}); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%d applied-decision status update(s) failed to send: %v", len(errs), errs)
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
