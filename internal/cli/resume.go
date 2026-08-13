package cli

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

var (
	resumeStdinIsTerminal  = stdinIsTerminal
	resumeStderrIsTerminal = stderrIsTerminal
)

func stderrIsTerminal() bool {
	info, err := os.Stderr.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// resolveLastSessionForProfile answers gh#722's "resume the latest session"
// friction point: the operator previously had to run 'status --json' and
// jq-filter it just to rediscover a session name. It picks the profile's
// most recently active session (by launch-record activity, same signal the
// status board uses), breaking ties on name for determinism.
func resolveLastSessionForProfile(projectDir, profile string) (string, error) {
	baseRoot, err := scanBaseRootForProject(projectDir)
	if err != nil || strings.TrimSpace(baseRoot) == "" {
		return "", fmt.Errorf("--last: no AMQ sessions found for profile %q", profile)
	}
	snap, err := state.Build(projectDir, baseRoot, state.Probe{})
	if err != nil {
		return "", fmt.Errorf("--last: scan sessions for profile %q: %w", profile, err)
	}
	now := time.Now()
	type candidate struct {
		name string
		last time.Time
	}
	var candidates []candidate
	for _, sess := range snap.Sessions {
		if strings.TrimSpace(sess.Name) == "" || !squadnamespace.ProfilesEqual(sess.TeamProfile, profile) {
			continue
		}
		row := boardRowFor(projectDir, sess, now)
		candidates = append(candidates, candidate{name: sess.Name, last: row.LastActivity})
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("--last: no sessions found for profile %q; run 'amq-squad up' or 'amq-squad start' first", profile)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if !candidates[i].last.Equal(candidates[j].last) {
			return candidates[i].last.After(candidates[j].last)
		}
		return candidates[i].name < candidates[j].name
	})
	return candidates[0].name, nil
}

func runResume(args []string) error {
	fs := flag.NewFlagSet("resume", flag.ContinueOnError)
	sessionFlag := fs.String("session", "", "AMQ workstream session name to resume into (default: team workstream)")
	lastFlag := fs.Bool("last", false, "resume the profile's most recently active session instead of naming one (auto-picks the only session when there is exactly one)")
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
	skipLeadCheck := fs.Bool("skip-lead-check", false, "with --exec: launch dependent members without verifying the configured lead's live pane (recovery escape hatch for a stale lead record)")
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
  amq-squad resume [--project DIR] [--profile NAME] [--session name | --last] [--role a,b] [--restore-existing]
                   [--dry-run] [--json] [--force-duplicate]
                   [--no-bootstrap] [--trust sandboxed|approve-for-me|trusted]
                   [--model role=model,...]
                   [--effort role=level,...]
                   [--codex-args args] [--claude-args args]
                   [--exec [--redeliver-goal] [--terminal tmux] [--target current-window|new-window|new-session]
                           [--layout vertical|horizontal|tiled]
                           [--terminal-session name] [--stagger 750ms]
                           [--skip-lead-check]]

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
Orchestrated resumes verify the configured lead is live and operator-
addressable before launching dependent roles; --skip-lead-check bypasses
that gate (with a warning) when a stale lead record blocks recovery.
When an orchestrated resume adds members to the current window while the
lead is already live (the mid-run member-add flow), the window arranges as
main-vertical: the launching pane keeps a full-height left column and the
added members stack in rows to its right. Pass --layout explicitly to keep
the legacy even-split behavior.
With --json, emits a schema-versioned
resume_plan envelope for clients: per-member action plus a liveness block
(status/detail/signals) consistent with 'status --json', and -- where available
-- the copy-ready command (omitted for members already live) and tmux runtime
metadata including pane_alive (present only for members launched in tmux).
--json is a read-only preview and cannot be combined with --exec.

Fresh / new-session behavior belongs to an explicitly selected roster session
followed by 'amq-squad start'.

--last resolves the profile's most recently active session automatically
(the only session when there is exactly one) instead of requiring the
operator to rediscover the session name via 'status --json' first. It
prints which session it picked and cannot be combined with a positional
session or --session.

Examples:
  amq-squad resume
  amq-squad resume --project ~/Code/app --session issue-96
  amq-squad resume --session issue-96 --restore-existing
  amq-squad resume --session issue-96 --json
  amq-squad resume --profile squad --last --exec
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
	if *skipLeadCheck && !*execMode {
		return usageErrorf("--skip-lead-check only applies to --exec launches")
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
	if *lastFlag && explicitSession {
		return usageErrorf("--last cannot be combined with a session (positional or --session)")
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
	if !team.ExistsProfile(projectDir, profile) {
		return fmt.Errorf("no team configured for profile %q. Run '%s' first.", profile, profileInitCommand(profile))
	}
	if *lastFlag {
		last, err := resolveLastSessionForProfile(projectDir, profile)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "resume: --last picked session %q for profile %q (use --session to target a different one)\n", last, profile)
		requestedSession = last
		explicitSession = true
	}
	if !explicitSession {
		requestedSession = resolvedContext.Session
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
			SkipLeadCheck:      *skipLeadCheck,
			LayoutExplicit:     flagWasSet(fs, "layout"),
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
