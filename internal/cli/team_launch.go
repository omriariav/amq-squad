package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/internal/team"
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

func runTeamLaunch(args []string) error {
	fs := flag.NewFlagSet("team launch", flag.ContinueOnError)
	terminal := fs.String("terminal", "tmux", "terminal backend to use")
	target := fs.String("target", "current-window", "terminal target, backend-specific")
	layout := fs.String("layout", "vertical", "terminal layout, backend-specific")
	sessionName := fs.String("session", "", "AMQ workstream session name (default: sanitized team-home directory name; lowercase a-z, 0-9, -, _)")
	terminalSession := fs.String("terminal-session", "", "terminal session name when the backend creates one")
	fresh := fs.Bool("fresh", false, "fail if the selected workstream session already exists")
	noBootstrap := fs.Bool("no-bootstrap", false, "launch agents without the generated bootstrap prompt")
	trustRaw := fs.String("trust", "", "Codex trust profile for this run: sandboxed or trusted")
	modelFlag := fs.String("model", "", "per-persona model overrides for this run, e.g. cto=gpt-5,fullstack=sonnet")
	codexArgsRaw := fs.String("codex-args", "", "extra Codex args for this run, e.g. '--enable goals'")
	claudeArgsRaw := fs.String("claude-args", "", "extra Claude args for this run, e.g. '--chrome'")
	forceDuplicate := fs.Bool("force-duplicate", false, "launch even when a live agent is detected for any member")
	_ = fs.Bool("no-attach", false, "legacy no-op; new-session never attaches automatically")
	stagger := fs.Duration("stagger", 750*time.Millisecond, "delay between starting agent panes")
	dryRun := fs.Bool("dry-run", false, "print terminal commands without executing them")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `amq-squad team launch - open the configured team in a terminal

Usage:
  amq-squad team launch [--session workstream] [--fresh] [--terminal tmux]
    [--target current-window|new-session] [--layout vertical|horizontal|tiled]
    [--terminal-session name] [--stagger 750ms] [--no-bootstrap]
    [--trust sandboxed|trusted] [--model role=model,...]
    [--codex-args args] [--claude-args args]
    [--force-duplicate] [--dry-run]

Supported terminal backends: %s

tmux defaults to splitting the current tmux window. Use --target new-session
to create a detached squad session. The whole roster is preflighted for live
duplicates before any tmux command runs; --force-duplicate overrides.
`, strings.Join(registeredTeamLaunchTerminals(), ", "))
	}
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fs.Usage()
		return nil
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	requestedTrust, err := normalizeTrustMode(*trustRaw)
	if err != nil {
		return err
	}
	modelOverrides, err := parseKV(*modelFlag)
	if err != nil {
		return fmt.Errorf("parse --model: %w", err)
	}
	modelOverrides = lowercaseKeys(modelOverrides)
	binaryArgs, err := parseBinaryArgFlags(*codexArgsRaw, *claudeArgsRaw)
	if err != nil {
		return err
	}
	opts := teamLaunchOptions{
		Terminal:        *terminal,
		Target:          *target,
		Layout:          *layout,
		Workstream:      *sessionName,
		TerminalSession: *terminalSession,
		Fresh:           *fresh,
		NoBootstrap:     *noBootstrap,
		Stagger:         *stagger,
		DryRun:          *dryRun,
		SquadBin:        teamSquadBin(),
		BinaryArgs:      binaryArgs,
		Trust:           requestedTrust,
		ModelOverrides:  modelOverrides,
		ForceDuplicate:  *forceDuplicate,
	}
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
	t, err := team.Read(cwd)
	if err != nil {
		return fmt.Errorf("read team: %w", err)
	}
	if len(t.Members) == 0 {
		return fmt.Errorf("team has no members")
	}
	workstream, err := resolveTeamWorkstreamName(t, opts.Workstream, flagWasSet(fs, "session"))
	if err != nil {
		return err
	}
	opts.Workstream = workstream
	trustMode, err := resolveTeamTrustMode(t, opts.Trust, flagWasSet(fs, "trust"))
	if err != nil {
		return err
	}
	opts.Trust = trustMode
	// Apply the trust + binary-args contradiction check before opening any
	// pane, so a misconfigured team never partially launches into runLaunch
	// errors per pane.
	mergedBinaryArgs := mergeBinaryArgs(t.BinaryArgs, opts.BinaryArgs)
	if err := validateTrustCombination(trustMode, flagWasSet(fs, "trust") || strings.TrimSpace(t.Trust) != "", false, mergedBinaryArgs); err != nil {
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
	return backend.Launch(t, opts)
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
		env, err := resolveAMQEnvInDir(cwd, "", opts.Workstream, m.Handle)
		if err != nil {
			return nil, fmt.Errorf("resolve amq env for %s: %w", m.Handle, err)
		}
		root := env.Root
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
				Model:          memberEffectiveModel(m, opts.ModelOverrides),
				ForceDuplicate: opts.ForceDuplicate,
			}),
		})
	}
	return panes
}

func teamSquadBin() string {
	if p, err := os.Executable(); err == nil {
		return p
	}
	return "amq-squad"
}
