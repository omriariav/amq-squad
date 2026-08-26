package cli

import (
	"flag"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// previewFlags is the shared flag surface for printing a team's launch-command
// plan. Both `team show` and `up --dry-run` register it so the two preview
// entry points cannot drift.
type previewFlags struct {
	noBootstrap    *bool
	session        *string
	fresh          *bool
	trustRaw       *string
	model          *string
	effort         *string
	codexArgsRaw   *string
	claudeArgsRaw  *string
	forceDuplicate *bool
	noGitignore    *bool
	symphony       *bool
	wakeInjectVia  *string
	wakeInjectMode *string
	wakeInjectArgs stringListFlag
}

func registerPreviewFlags(fs *flag.FlagSet) *previewFlags {
	p := &previewFlags{
		noBootstrap:    fs.Bool("no-bootstrap", false, "emit launch commands that skip the generated bootstrap prompt"),
		session:        fs.String("session", "", "AMQ workstream session name (default: sanitized team-home directory name; lowercase a-z, 0-9, -, _)"),
		fresh:          fs.Bool("fresh", false, "fail if the selected workstream session already exists"),
		trustRaw:       fs.String("trust", "", "Codex trust profile for this run: approve-for-me (default), sandboxed, or trusted"),
		model:          fs.String("model", "", "per-persona model overrides for this run, e.g. cto=gpt-5.6-sol,fullstack=sonnet"),
		effort:         fs.String("effort", "", "per-persona launch-only effort overrides, e.g. cto=high,qa=medium"),
		codexArgsRaw:   fs.String("codex-args", "", "extra Codex args for this run, e.g. '--enable goals'"),
		claudeArgsRaw:  fs.String("claude-args", "", "extra Claude args for this run, e.g. '--chrome'"),
		forceDuplicate: fs.Bool("force-duplicate", false, "include --force-duplicate in emitted launch commands"),
		noGitignore:    fs.Bool("no-gitignore", false, "forward --no-gitignore to every amq coop exec launch"),
		symphony:       fs.Bool("symphony", false, "Codex only: emit launch commands that patch the existing WORKFLOW.md with AMQ Symphony lifecycle hooks"),
		wakeInjectVia:  fs.String("wake-inject-via", "", "absolute executable forwarded to every agent launch as amq coop exec --wake-inject-via"),
		wakeInjectMode: fs.String("wake-inject-mode", "", "wake injection mode forwarded to every agent launch: auto, raw, paste, or none"),
	}
	fs.Var(&p.wakeInjectArgs, "wake-inject-arg", "argument forwarded to every agent launch as amq coop exec --wake-inject-arg (repeatable; requires --wake-inject-via)")
	return p
}

func (p *previewFlags) toEmitOptions(fs *flag.FlagSet) (emitTeamOptions, error) {
	trustMode, err := normalizeTrustMode(*p.trustRaw)
	if err != nil {
		return emitTeamOptions{}, err
	}
	modelOverrides, err := parseKV(*p.model)
	if err != nil {
		return emitTeamOptions{}, fmt.Errorf("parse --model: %w", err)
	}
	modelOverrides = lowercaseKeys(modelOverrides)
	effortOverrides, err := parseEffortOverrides(*p.effort)
	if err != nil {
		return emitTeamOptions{}, err
	}
	binaryArgs, err := parseBinaryArgFlags(*p.codexArgsRaw, *p.claudeArgsRaw)
	if err != nil {
		return emitTeamOptions{}, err
	}
	wakeInjectVia := strings.TrimSpace(*p.wakeInjectVia)
	wakeInjectArgs := append([]string(nil), p.wakeInjectArgs...)
	wakeInjectMode, err := normalizeWakeInjectMode(*p.wakeInjectMode)
	if err != nil {
		return emitTeamOptions{}, err
	}
	if err := validateWakeInjectConfig(wakeInjectMode, wakeInjectVia, wakeInjectArgs, ""); err != nil {
		return emitTeamOptions{}, err
	}
	if wakeInjectVia != "" && !filepath.IsAbs(wakeInjectVia) {
		return emitTeamOptions{}, usageErrorf("--wake-inject-via must be an absolute path")
	}
	return emitTeamOptions{
		NoBootstrap:      *p.noBootstrap,
		RequestedSession: *p.session,
		ExplicitSession:  flagWasSet(fs, "session"),
		Fresh:            *p.fresh,
		ExtraBinaryArgs:  binaryArgs,
		RequestedTrust:   trustMode,
		ExplicitTrust:    flagWasSet(fs, "trust"),
		ModelOverrides:   modelOverrides,
		EffortOverrides:  effortOverrides,
		ForceDuplicate:   *p.forceDuplicate,
		NoGitignore:      *p.noGitignore,
		Symphony:         *p.symphony,
		WakeInjectVia:    wakeInjectVia,
		WakeInjectArgs:   wakeInjectArgs,
		WakeInjectMode:   wakeInjectMode,
	}, nil
}

// liveLaunchFlags is the backend-specific flag surface shared by `team launch`
// and live `up`. It does not include --dry-run: each command owns its own
// dry-run flag because the two semantics differ (terminal-plan dry-run for
// team launch, launch-command preview for up).
type liveLaunchFlags struct {
	terminal        *string
	target          *string
	layout          *string
	terminalSession *string
	stagger         *time.Duration
	// noAttach is parsed for compatibility but has no behavioral effect.
	noAttach *bool
	// launchVia opts into an alternate launch orchestration path (gh#733).
	// Empty is the legacy default: byte-identical to pre-gh#733 behavior.
	launchVia *string
	// launchapiDecisions carries explicit operator answers to launchapi's
	// RequiredActionV1 gates, one ACTION_ID=CHOICE pair per repeated flag.
	// Only meaningful with --launch-via launchapi; never auto-populated.
	launchapiDecisions stringListFlag
}

func registerLiveLaunchFlags(fs *flag.FlagSet) *liveLaunchFlags {
	lf := &liveLaunchFlags{
		terminal:        fs.String("terminal", "tmux", "terminal backend to use"),
		target:          fs.String("target", "current-window", "terminal target, backend-specific"),
		layout:          fs.String("layout", "vertical", "terminal layout, backend-specific"),
		terminalSession: fs.String("terminal-session", "", "terminal session name when the backend creates one"),
		stagger:         fs.Duration("stagger", 750*time.Millisecond, "delay between starting agent panes"),
		noAttach:        fs.Bool("no-attach", false, "legacy no-op; new-session never attaches automatically"),
		launchVia:       fs.String("launch-via", "", "opt-in alternate launch orchestration path: launchapi (tmux only); default is legacy"),
	}
	fs.Var(&lf.launchapiDecisions, "launchapi-decision", "explicit operator answer to a launchapi required action, ACTION_ID=CHOICE (repeatable; --launch-via launchapi only)")
	return lf
}

// buildLiveLaunchOptions composes a teamLaunchOptions from the shared preview
// and live flag sets. The caller fills in DryRun separately so the two
// callers can keep distinct dry-run semantics.
func buildLiveLaunchOptions(fs *flag.FlagSet, pf *previewFlags, lf *liveLaunchFlags) (teamLaunchOptions, error) {
	emit, err := pf.toEmitOptions(fs)
	if err != nil {
		return teamLaunchOptions{}, err
	}
	launchapiDecisions, err := parseLaunchapiDecisions(lf.launchapiDecisions)
	if err != nil {
		return teamLaunchOptions{}, err
	}
	return teamLaunchOptions{
		Terminal:           *lf.terminal,
		Target:             *lf.target,
		Layout:             *lf.layout,
		Workstream:         emit.RequestedSession,
		TerminalSession:    *lf.terminalSession,
		Fresh:              emit.Fresh,
		NoBootstrap:        emit.NoBootstrap,
		Stagger:            *lf.stagger,
		SquadBin:           teamSquadBin(),
		BinaryArgs:         emit.ExtraBinaryArgs,
		Trust:              emit.RequestedTrust,
		ModelOverrides:     emit.ModelOverrides,
		EffortOverrides:    emit.EffortOverrides,
		ForceDuplicate:     emit.ForceDuplicate,
		NoGitignore:        emit.NoGitignore,
		Symphony:           emit.Symphony,
		WakeInjectVia:      emit.WakeInjectVia,
		WakeInjectArgs:     emit.WakeInjectArgs,
		WakeInjectMode:     emit.WakeInjectMode,
		LaunchVia:          *lf.launchVia,
		LaunchapiDecisions: launchapiDecisions,
	}, nil
}

// parseLaunchapiDecisions parses repeated --launchapi-decision
// ACTION_ID=CHOICE flags into a map. A malformed entry (missing "=", or an
// empty action id/choice) is a usage error, not a silently dropped flag.
func parseLaunchapiDecisions(entries []string) (map[string]string, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(entries))
	for _, entry := range entries {
		actionID, choice, ok := strings.Cut(entry, "=")
		actionID, choice = strings.TrimSpace(actionID), strings.TrimSpace(choice)
		if !ok || actionID == "" || choice == "" {
			return nil, usageErrorf("--launchapi-decision %q: want ACTION_ID=CHOICE", entry)
		}
		if _, dup := out[actionID]; dup {
			return nil, usageErrorf("--launchapi-decision specified twice for action %q", actionID)
		}
		out[actionID] = choice
	}
	return out, nil
}
