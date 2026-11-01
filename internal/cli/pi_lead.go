package cli

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/piruntime"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/registry"
	"github.com/omriariav/amq-squad/v3/internal/role"
)

// `amq-squad lead` starts the lead on the built-in pi (plan 3.1, 3.2, R2,
// R7). Under an AMQ launch (AM_ME and AM_ROOT set) it is that seat. Without
// one it is the solo lead of the directory: its AMQ root, role, rules, task
// store and pi config live under ~/.amq-squad/, and nothing is written inside
// the repository (R7). --team gives the team-lead role and the dispatch tool
// (R19: the lead decomposes, labels and converges; it does not code).

//go:embed pi_lead_solo.md
var leadSoloRole string

//go:embed pi_lead_team.md
var leadTeamRole string

//go:embed pi_lead_rules.md
var leadBuiltinRules string

const leadUsage = `amq-squad lead - start the lead on the built-in pi

Usage:
  amq-squad lead [--team] [--model PROVIDER/ID]... [--thinking LEVEL] [--dry-run] [-- PI-ARGS...]

Without an AMQ identity (AM_ME, AM_ROOT) the lead runs in solo mode for this
directory: its state lives under ~/.amq-squad/solo/<dir>-<digest>/ and nothing
is written inside the repository. --team starts the team-lead role, which
decomposes and dispatches through the task store and never edits code.
In a repository with a squad.yaml, --team is the squad's lead: it takes the
root's lead lock (a second lead on the same root is refused lead_running,
naming the first's pid and tmux pane), starts the squad's scheduler process
(amq-squad scheduler run --project <repo>) beside the lead, restarts it if
it crashes (not after a refusal) and stops it when the lead ends. The
handle, the session and the seats come from squad.yaml (its lead: key); the
pi lead uses that session's AMQ root, so its task tools see the scheduler's
store.
--model restricts the lead to these models (default ollama/qwen3.6:27b, the
locally verified entry); pass pi arguments after "--".

Examples:
  amq-squad lead
  amq-squad lead --model ollama/qwen3.6:27b -- -p "What is 17 plus 25?"
  amq-squad lead --team --model ollama/qwen3.6:27b
`

// defaultLeadModel is the fallback lead model only (brief 4-1 ruling 2): the
// local model R12 verified (S5 at N=3, 4-1 at N=5 through `pi run`). Once
// 2b-3's registry is on v3 the registry's model row decides, and this
// constant applies only when the registry has no usable provider.
const defaultLeadModel = "ollama/qwen3.6:27b"

func runLead(args []string) error {
	fs := flag.NewFlagSet("lead", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var f piRunFlags
	team := fs.Bool("team", false, "team-lead role: decompose and dispatch, never edit")
	fs.Var(&f.models, "model", "PROVIDER/ID the lead may use (repeatable)")
	fs.StringVar(&f.thinking, "thinking", "", "pi thinking level")
	fs.BoolVar(&f.dryRun, "dry-run", false, "print the launch as JSON and do not exec")
	fs.Usage = func() { fmt.Fprint(os.Stderr, leadUsage) }
	if err := parseFlags(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fs.Usage()
		}
		return err
	}
	if len(f.models) == 0 {
		// Ruling 2 at the rebase: the registry's model row decides; the
		// constant is the fallback when the registry has no usable pi entry.
		home, _ := os.UserHomeDir()
		model, effort, src := leadModel(home)
		f.models = multiFlag{model}
		if f.thinking == "" {
			f.thinking = effort
		}
		fmt.Fprintf(os.Stderr, "amq-squad lead: model %s (%s)\n", model, src)
	}
	f.lead = *team
	// The team lead's tool set (amendment 26) is applied by piLaunch for
	// every lead launch, here and under an AMQ launch.
	piArgs := fs.Args()
	var teamIdentity piruntime.TeamIdentity
	var teamProject string
	if *team {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		teamProject = projectTop(cwd)
	}
	if *team && squadPresent(teamProject) {
		// Ruling 15 and brief 31-1: in a repository with a squad.yaml the
		// team mode takes the root's lead lock, then starts and supervises
		// the scheduler over that squad.yaml.
		var err error
		if teamIdentity, err = resolveTeamLeadIdentity(teamProject); err != nil {
			return fmt.Errorf("lead: resolve the team identity: %w", err)
		}
		lock, err := UpLeadLock(teamProject, teamIdentity.Root)
		if err != nil {
			return fmt.Errorf("lead: %w", err)
		}
		defer lock.Release()
		if os.Getenv(leadStartEnv) == "" {
			// the start of this launch, for the launch budget (brief 31-1)
			os.Setenv(leadStartEnv, time.Now().UTC().Format(time.RFC3339Nano))
		}
		sup, err := UpScheduler(teamProject, teamIdentity.Root, leadPools(f.models, f.thinking))
		if err != nil {
			return fmt.Errorf("lead: start the scheduler process: %w", err)
		}
		defer sup.stop()
		// A lead that exits non-zero ends through piExit (os.Exit), which
		// skips the deferred stop: the scheduler stops first (brief 31-1).
		prevExit := piExit
		piExit = func(code int) {
			sup.stop()
			lock.Release()
			prevExit(code)
		}
		defer func() { piExit = prevExit }()
	}
	if teamIdentity.Handle != "" {
		overlay := teamIdentity.Overlay()
		// Preserve the pre-fix owner-terminal path's built-in team role,
		// rules and project pointer. Under an existing AMQ launch those are
		// already delivered by seat-exec and remain in the parent environment.
		if os.Getenv("AM_ME") == "" || os.Getenv("AM_ROOT") == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			support, err := prepareSoloLead(home, cwd, leadTeamRole)
			if err != nil {
				return err
			}
			for name, value := range support {
				overlay[name] = value
			}
			for name, value := range teamIdentity.Overlay() {
				overlay[name] = value
			}
		}
		// The repo tools read the squad's repository, not this directory.
		overlay["AMQ_SQUAD_PROJECT"] = teamProject
		return piLaunch(piLaunchSpec{Seat: teamIdentity.Handle, Flags: f, PiArgs: piArgs, Overlay: overlay})
	}
	if me, root := os.Getenv("AM_ME"), os.Getenv("AM_ROOT"); me != "" && root != "" {
		return piLaunch(piLaunchSpec{Seat: me, Flags: f, PiArgs: piArgs})
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	text := leadSoloRole
	if *team {
		text = leadTeamRole
	}
	solo, err := prepareSoloLead(home, cwd, text)
	if err != nil {
		return err
	}
	if !*team && !f.dryRun {
		// The solo lead proposes a team from the conversation and, on the
		// owner's confirmation, starts it in this process (brief 31-3): its
		// pi gets the two team tools and the start socket.
		solo["AMQ_SQUAD_SOLO_LEAD"] = "1"
		starter := newLeadStarter(solo["AM_ROOT"], solo["AMQ_SQUAD_PROJECT"], leadVersion, leadPools(f.models, f.thinking))
		sock, err := serveLeadStart(starter)
		if err != nil {
			return fmt.Errorf("lead: the start socket: %w", err)
		}
		solo[leadStartSocketEnv] = sock
		defer starter.close()
		// the TUI door's one accepted peer: the pi this process spawns
		piStartedHook = func(pid int) {
			stamp, _ := procinfo.StartToken(pid)
			starter.setPi(pid, stamp)
		}
		defer func() { piStartedHook = nil }()
		prevExit := piExit
		piExit = func(code int) {
			starter.close()
			prevExit(code)
		}
		defer func() { piExit = prevExit }()
	}
	return piLaunch(piLaunchSpec{Seat: "lead", Flags: f, PiArgs: piArgs, Overlay: solo})
}

// piStartedHook runs with the pid of the pi process runProcess started
// (the solo lead records it with its start stamp).
var piStartedHook func(pid int)

// serveLeadStart binds the solo lead's start socket (a test seam).
var serveLeadStart = func(s *leadStarter) (string, error) { return s.serve() }

// leadVersion is this build's version for the start's roster read (set by
// the command dispatcher).
var leadVersion = ""

var soloSlugRE = regexp.MustCompile(`[^a-z0-9-]+`)

// soloHome is ~/.amq-squad/solo/<dir>-<sha256(project)[:12]>, the solo
// lead's state for one project (the git top level, else the directory).
func soloHome(home, cwd string) (string, string) {
	project := projectTop(cwd)
	sum := sha256.Sum256([]byte(project))
	slug := strings.Trim(soloSlugRE.ReplaceAllString(strings.ToLower(filepath.Base(project)), "-"), "-")
	if slug == "" {
		slug = "root"
	}
	if len(slug) > 40 {
		slug = slug[:40]
	}
	return filepath.Join(home, ".amq-squad", "solo", slug+"-"+hex.EncodeToString(sum[:])[:12]), project
}

// projectTop walks up from dir to the directory holding .git; without one it
// is dir itself (a directory without git gives solo mode only, plan 3.1).
func projectTop(dir string) string {
	dir = filepath.Clean(dir)
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		if parent := filepath.Dir(d); parent == d {
			return dir
		}
	}
}

// prepareSoloLead creates the solo lead's AMQ root and mailbox, role and
// rules under ~/.amq-squad/solo/..., and returns the identity overlay. It
// writes nothing under cwd (R7).
func prepareSoloLead(home, cwd, roleText string) (map[string]string, error) {
	dir, project := soloHome(home, cwd)
	root := filepath.Join(dir, "root")
	agent := filepath.Join(root, "agents", "lead")
	for _, sub := range []string{"inbox/new", "inbox/cur", "inbox/tmp", "outbox/sent"} {
		if err := os.MkdirAll(filepath.Join(agent, filepath.FromSlash(sub)), 0o700); err != nil {
			return nil, err
		}
	}
	rolePath := role.Path(agent)
	if err := os.MkdirAll(filepath.Dir(rolePath), 0o700); err != nil {
		return nil, err
	}
	if err := writeIfDifferent(rolePath, roleText); err != nil {
		return nil, err
	}
	rulesPath := filepath.Join(dir, "team-rules.md")
	if err := writeIfDifferent(rulesPath, leadBuiltinRules); err != nil {
		return nil, err
	}
	return map[string]string{
		"AM_ME": "lead", "AM_ROOT": root, "AM_BASE_ROOT": root, "AM_SESSION": "solo",
		"AMQ_SQUAD_ROLE_PATH": rolePath, "AMQ_SQUAD_RULES_PATH": rulesPath, "AMQ_SQUAD_PROJECT": project,
	}, nil
}

func writeIfDifferent(p, body string) error {
	if cur, err := os.ReadFile(p); err == nil && string(cur) == body {
		return nil
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// leadModel picks the lead's model from the registry (~/.amq-squad/
// registry.yaml): the first verified pi entry in file order, preferring
// effort medium. With no registry or no verified pi entry it is the fallback
// constant. It returns the model, the effort and where it came from.
func leadModel(home string) (string, string, string) {
	reg, err := registry.Read(registry.Path(home))
	if err == nil {
		var pick *registry.Entry
		for _, e := range reg.Proposable() {
			if e.Runtime != "pi" || !strings.Contains(e.Model, "/") {
				continue
			}
			e := e
			if pick == nil || (e.Effort == "medium" && pick.Effort != "medium") {
				pick = &e
			}
		}
		if pick != nil {
			return pick.Model, pick.Effort, "registry entry " + pick.ID()
		}
		return defaultLeadModel, "", "fallback: the registry has no verified pi entry"
	}
	return defaultLeadModel, "", "fallback: no registry (run amq-squad doctor --write-registry)"
}

// stdinIsTerminal is the TTY seam for the bare invocation.
var stdinIsTerminal = func() bool { return isTerminal(int(os.Stdin.Fd())) }

// runBare is `amq-squad` with no arguments (plan 3.1, brief 4-1 ruling 1):
// the solo lead when this build embeds pi and stdin is a terminal, else the
// usage and a one-line hint.
func runBare() error {
	_, err := piLoad()
	if err == nil && stdinIsTerminal() {
		return runLead(nil)
	}
	printUsage()
	if err != nil {
		fmt.Println("\nThis build has no built-in pi, so bare `amq-squad` cannot start the lead (build with scripts/build-pi-runtime.sh).")
	} else {
		fmt.Println("\nRun `amq-squad` in a terminal, or `amq-squad lead`, to start the lead on the built-in pi.")
	}
	return nil
}
