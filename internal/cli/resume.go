package cli

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

var (
	resumeStdinIsTerminal  = stdinIsTerminal
	resumeStderrIsTerminal = stderrIsTerminal
)

func runResume(args []string) error {
	fs := flag.NewFlagSet("resume", flag.ContinueOnError)
	sessionFlag := fs.String("session", "", "AMQ workstream session name to resume into (default: team workstream)")
	restoreExisting := fs.Bool("restore-existing", false, "fail if no team member has restorable launch records for the workstream")
	dryRun := fs.Bool("dry-run", false, "plan-only; default behavior is already plan-only and exists for parity with other commands")
	forceDuplicate := fs.Bool("force-duplicate", false, "include commands even when a live agent is detected for a member")
	noBootstrap := fs.Bool("no-bootstrap", false, "emit fresh launch commands that skip the generated bootstrap prompt")
	trustRaw := fs.String("trust", "", "Codex trust profile for fresh members: approve-for-me (default), sandboxed, or trusted")
	modelFlag := fs.String("model", "", "per-persona model overrides for fresh members, e.g. cto=gpt-5.6-sol,fullstack=sonnet")
	effortFlag := fs.String("effort", "", "per-persona effort overrides for launch-fresh members, e.g. cto=xhigh,fullstack=max")
	codexArgsRaw := fs.String("codex-args", "", "extra Codex args for fresh members, e.g. '--enable goals'")
	claudeArgsRaw := fs.String("claude-args", "", "extra Claude args for fresh members, e.g. '--chrome'")
	projectFlag := fs.String("project", "", "project/team-home directory to resume (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to resume (default: default profile)")
	registerScopedFlagAliases(fs, projectFlag, sessionFlag, profileFlag)
	roleFlag := fs.String("role", "", "comma-separated subset of roles to resume (default: all members)")
	execMode := fs.Bool("exec", false, "open the planned launch commands in the terminal backend (tmux) instead of printing them")
	redeliverGoal := fs.Bool("redeliver-goal", false, "after a verified fresh lead re-orient, deliver the saved goal as a new claim-once attempt")
	suppressGoalPrompt := fs.Bool("no-redeliver-goal-prompt", false, "preserve an upstream wizard No without prompting again")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned resume_plan envelope (liveness + tmux metadata) instead of the human plan")
	terminal := fs.String("terminal", "tmux", "terminal backend to use with --exec")
	target := fs.String("target", "current-window", "terminal target with --exec (tmux: current-window, new-window, or new-session)")
	layout := fs.String("layout", "vertical", "terminal layout with --exec (tmux: vertical, horizontal, or tiled)")
	terminalSession := fs.String("terminal-session", "", "terminal session name when --exec --target new-session")
	stagger := fs.Duration("stagger", 750*time.Millisecond, "delay between starting agent panes with --exec")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad resume - bring the team back from launch records

Usage:
  amq-squad resume [--project DIR] [--profile NAME] [--session name] [--role a,b] [--restore-existing]
                   [--dry-run] [--json] [--force-duplicate]
                   [--no-bootstrap] [--trust sandboxed|approve-for-me|trusted]
                   [--model role=model,...]
                   [--effort role=level,...]
                   [--codex-args args] [--claude-args args]
                   [--exec [--redeliver-goal] [--terminal tmux] [--target current-window|new-window|new-session]
                           [--layout vertical|horizontal|tiled]
                           [--terminal-session name] [--stagger 750ms]]

Resume an existing session. Inspects .amq-squad/team.json plus local launch
history and live-agent signals (wake locks, agent PID liveness, presence) to
choose a per-member action: restore from launch.json, launch fresh from team
intent, skip if live, or refuse if blocked.
--project targets another team-home without changing directories.

If an agent has a saved conversation, amq-squad reattaches to it. Otherwise it
starts the agent fresh and re-orients it: bootstrap is re-run so the agent
re-reads its session brief and AMQ history. In the re-orient case prior hidden
reasoning is not replayed -- only persisted session files and messages are used.

Default behavior is plan-only: prints the per-member action table plus
copy-pasteable commands. With --exec, opens those commands through the
selected terminal backend (same path as 'up'), skipping members that are
already live and refusing to start if any member is in the 'blocked'
action without --force-duplicate. Use --role a,b to resume only a subset
of members (e.g. bring up two workers without relaunching a live lead).
With --json, emits a schema-versioned
resume_plan envelope for clients: per-member action plus a liveness block
(status/detail/signals) consistent with 'status --json', and -- where available
-- the copy-ready command (omitted for members already live) and tmux runtime
metadata including pane_alive (present only for members launched in tmux).
--json is a read-only preview and cannot be combined with --exec.

Fresh / new-session behavior belongs to 'amq-squad fork --from S --as T'.

Examples:
  amq-squad resume
  amq-squad resume --project ~/Code/app --session issue-96
  amq-squad resume --session issue-96 --restore-existing
  amq-squad resume --session issue-96 --json
  amq-squad resume --exec
  amq-squad resume --exec --role fullstack,qa
  amq-squad resume --exec --target new-session --terminal-session squad
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *execMode && *dryRun {
		return usageErrorf("--exec and --dry-run are mutually exclusive")
	}
	if *jsonOut && *execMode {
		return usageErrorf("--json is a read-only plan preview; it cannot be combined with --exec")
	}

	// Positional session, consistent with up/rm/archive.
	requestedSession := *sessionFlag
	explicitSession := flagWasSet(fs, "session")
	if fs.NArg() > 1 {
		return usageErrorf("resume takes at most one session positional; got %d", fs.NArg())
	}
	if fs.NArg() == 1 {
		positional := strings.TrimSpace(fs.Arg(0))
		if flagWasSet(fs, "session") {
			return usageErrorf("pass the session name either positionally or via --session, not both")
		}
		if err := validateWorkstreamName(positional); err != nil {
			return err
		}
		requestedSession = positional
		explicitSession = true
	}

	resolvedContext, err := resolveCanonicalContext(contextResolveOptions{
		ProjectFlag: *projectFlag, ProfileFlag: *profileFlag, SessionFlag: requestedSession,
		ProjectExplicit: flagWasSet(fs, "project"), ProfileExplicit: flagWasSet(fs, "profile"), SessionExplicit: explicitSession,
	})
	if err != nil {
		return err
	}
	emitContextDiagnostics(resolvedContext)
	profile := resolvedContext.Profile
	projectDir := resolvedContext.ProjectDir
	if !explicitSession {
		requestedSession = resolvedContext.Session
	}
	if !team.ExistsProfile(projectDir, profile) {
		return fmt.Errorf("no team configured for profile %q. Run '%s' first.", profile, profileInitCommand(profile))
	}
	mode := resumeModeDefault
	if *restoreExisting {
		mode = resumeModeRestoreExisting
	}
	exec := resumeExecOptions{
		RedeliverGoal:      *redeliverGoal,
		RedeliveryExplicit: flagWasSet(fs, "redeliver-goal"),
	}
	if *execMode {
		exec = resumeExecOptions{
			Enabled:            true,
			Terminal:           *terminal,
			Target:             *target,
			Layout:             *layout,
			TerminalSession:    *terminalSession,
			Stagger:            *stagger,
			RedeliverGoal:      *redeliverGoal,
			RedeliveryExplicit: flagWasSet(fs, "redeliver-goal"),
			PromptGoal:         !flagWasSet(fs, "redeliver-goal") && !*suppressGoalPrompt && resumeStdinIsTerminal() && resumeStderrIsTerminal(),
			PromptIn:           os.Stdin,
			PromptOut:          os.Stderr,
		}
	}
	return executeResume(resumeExecution{
		ProjectDir:       projectDir,
		RequestedSession: requestedSession,
		ExplicitSession:  explicitSession,
		ExplicitProfile:  flagWasSet(fs, "profile"),
		RolesRaw:         *roleFlag,
		Mode:             mode,
		Force:            *forceDuplicate,
		NoBootstrap:      *noBootstrap,
		TrustRaw:         *trustRaw,
		ExplicitTrust:    flagWasSet(fs, "trust"),
		ModelRaw:         *modelFlag,
		EffortRaw:        *effortFlag,
		CodexArgsRaw:     *codexArgsRaw,
		ClaudeArgsRaw:    *claudeArgsRaw,
		DryRun:           *dryRun,
		Profile:          profile,
		JSON:             *jsonOut,
		GoalRedelivery:   true,
		Style:            resumePrinterStyle{Label: "resume", FooterVerb: "up"},
		Exec:             exec,
	})
}
