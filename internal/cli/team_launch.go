package cli

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/internal/team"
)

type teamLaunchOptions struct {
	Terminal    string
	Target      string
	Layout      string
	Session     string
	NoBootstrap bool
	NoAttach    bool
	Stagger     time.Duration
	DryRun      bool
	SquadBin    string
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
	sessionName := fs.String("session", "", "terminal session name")
	noBootstrap := fs.Bool("no-bootstrap", false, "launch agents without the generated bootstrap prompt")
	noAttach := fs.Bool("no-attach", false, "create the terminal session without attaching")
	stagger := fs.Duration("stagger", 750*time.Millisecond, "delay between starting agent panes")
	dryRun := fs.Bool("dry-run", false, "print terminal commands without executing them")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `amq-squad team launch - open the configured team in a terminal

Usage:
  amq-squad team launch [--terminal tmux] [--target current-window|new-session] [--layout vertical|horizontal|tiled] [--session name] [--stagger 750ms] [--no-bootstrap] [--no-attach] [--dry-run]

Supported terminal backends: %s

tmux defaults to splitting the current tmux window. Use --target new-session
to create a detached squad session.
`, strings.Join(registeredTeamLaunchTerminals(), ", "))
	}
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fs.Usage()
		return nil
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	opts := teamLaunchOptions{
		Terminal:    *terminal,
		Target:      *target,
		Layout:      *layout,
		Session:     *sessionName,
		NoBootstrap: *noBootstrap,
		NoAttach:    *noAttach,
		Stagger:     *stagger,
		DryRun:      *dryRun,
		SquadBin:    teamSquadBin(),
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

	if opts.DryRun {
		return backend.DryRun(t, opts)
	}
	return backend.Launch(t, opts)
}

func registeredTeamLaunchTerminals() []string {
	names := make([]string, 0, len(teamLaunchBackends))
	for name := range teamLaunchBackends {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func buildTeamLaunchPanes(t team.Team, squadBin string, noBootstrap bool) []teamLaunchPane {
	members := orderedTeamMembers(t.Members)
	panes := make([]teamLaunchPane, 0, len(members))
	for _, m := range members {
		cwd := m.EffectiveCWD(t.Project)
		panes = append(panes, teamLaunchPane{
			Role:    m.Role,
			CWD:     cwd,
			Command: emitTeamCommand(cwd, squadBin, t.Project, m, noBootstrap),
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
