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
	restoreExisting := fs.Bool("restore-existing", false, "fail if no team member has restorable launch records for the workstream (plan-only; refused with --exec)")
	dryRun := fs.Bool("dry-run", false, "plan-only; default behavior is already plan-only and exists for parity with other commands")
	forceDuplicate := fs.Bool("force-duplicate", false, "include commands even when a live agent is detected for a member")
	fs.Bool("no-bootstrap", false, "emit fresh launch commands that skip the generated bootstrap prompt")
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
	fs.Bool("redeliver-goal", false, "not yet supported with --exec (refused outright); reserved for a future goal-redelivery fold")
	suppressGoalPrompt := fs.Bool("no-redeliver-goal-prompt", false, "not yet supported with --exec (refused outright); reserved for a future goal-redelivery fold")
	jsonOut := fs.Bool("json", false, "emit the same schema-versioned kind=\"plan\" envelope 'amq-squad plan --json' emits (plan-only; refused with --exec)")
	terminal := fs.String("terminal", "tmux", "terminal backend to use with --exec")
	target := fs.String("target", "current-window", "terminal target with --exec (tmux: current-window, new-window, or new-session)")
	layout := fs.String("layout", "vertical", "terminal layout with --exec (tmux: vertical, horizontal, or tiled)")
	terminalSession := fs.String("terminal-session", "", "terminal session name when --exec --target new-session")
	stagger := fs.Duration("stagger", 750*time.Millisecond, "delay between starting agent panes with --exec")
	launchVia := fs.String("launch-via", "", "with --exec: launch orchestration path, launchapi (default) or legacy (required today to restore a role with a pre-launchapi conversation; gh#758/t11)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad resume - bring the team back from launch records

Usage:
  amq-squad resume [--project DIR] [--profile NAME] [--session name | --last] [--role a,b] [--restore-existing]
                   [--dry-run] [--json] [--force-duplicate]
                   [--no-bootstrap] [--trust sandboxed|approve-for-me|trusted]
                   [--model role=model,...]
                   [--effort role=level,...]
                   [--codex-args args] [--claude-args args]
                   [--exec [--terminal tmux] [--target current-window|new-window|new-session]
                           [--layout vertical|horizontal|tiled]
                           [--terminal-session name] [--stagger 750ms]
                           [--skip-lead-check]]

Without --exec, resume is a thin, byte-identical alias for
'amq-squad plan SESSION': it calls the exact same planPrepareFiltered/
printPlanResult seam plan itself uses (compiling the profile and its
active brief through launchintent.Compile into a launchapi.PrepareRequestV1,
then adoptionseam.Prepare) and prints the resulting PrepareResultV1 --
target/outcome/roster/capabilities, plus a reattach note per participant
launchapi's own session Inspect reports a saved conversation for (never the
raw conversation id). It never writes to disk, AMQ, or launch state.
--project targets another team-home without changing directories. --role
narrows the previewed roster to a subset (e.g. two workers, without a live
lead in the preview). With --json, emits the identical kind="plan" envelope
'amq-squad plan --json' emits (the exact PrepareRequestV1 sent, so replaying
it through 'amq launch --plan - --prepare --json' reproduces the identical
subject_digest/plan_digest); --json is read-only and cannot be combined
with --exec. --trust/--model/--effort/--codex-args/--claude-args/
--no-bootstrap no longer apply to this plan-only preview (edit team.json
for a permanent change); passing any of them without --exec is refused,
naming the flag.

With --exec, resume drives the same shared launch machinery 'start' uses
(runSimpleStartWithRequest, digest-gated the same way 'start --apply' is):
members already live are skipped rather than relaunched, and a role stuck
on a required action (an orchestrated lead that is not live and
operator-addressable, or a dead externally-adopted lead record) refuses
the whole run naming it. --skip-lead-check bypasses only the first of
those (with a warning) when a stale lead record blocks recovery; the
second has no bypass. When an orchestrated resume adds members to the
current window while the lead is already live (the mid-run member-add
flow), the window arranges as main-vertical: the launching pane keeps a
full-height left column and the added members stack in rows to its right.
Pass --layout explicitly to keep the legacy even-split behavior. A seat
whose prior launch record still names a goal carries it into its relaunch
automatically, unless that same seat is reattaching to a saved conversation
(the goal already lives in the restored transcript there).
--restore-existing, --redeliver-goal, and --no-redeliver-goal-prompt do not
yet apply to --exec and are refused outright if passed with it.

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
	if flagWasSet(fs, "launch-via") && !*execMode {
		return usageErrorf("--launch-via only applies to --exec launches")
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

	// With --last, resolve project/profile first without asking for a
	// session winner: two equal-rank live sessions for the same profile
	// would otherwise make the generic session resolution below return an
	// ambiguous-session error before --last's own newest-session pick ever
	// runs. Once the session is picked, re-resolve fully (this time with an
	// explicit session) so diagnostics and downstream fields are complete
	// and consistent with the non---last path.
	resolveOpts := contextResolveOptions{
		ProjectFlag: *projectFlag, ProfileFlag: *profileFlag, SessionFlag: requestedSession,
		ProjectExplicit: flagWasSet(fs, "project"), ProfileExplicit: flagWasSet(fs, "profile"), SessionExplicit: explicitSession,
	}
	if *lastFlag {
		resolveOpts.SkipSessionResolution = true
	}
	resolvedContext, err := resolveCanonicalContext(resolveOpts)
	if err != nil {
		return err
	}
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
		resolvedContext, err = resolveCanonicalContext(contextResolveOptions{
			ProjectFlag: *projectFlag, ProfileFlag: *profileFlag, SessionFlag: requestedSession,
			ProjectExplicit: flagWasSet(fs, "project"), ProfileExplicit: flagWasSet(fs, "profile"), SessionExplicit: true,
		})
		if err != nil {
			return err
		}
	}
	emitContextDiagnostics(resolvedContext)
	if !explicitSession {
		requestedSession = resolvedContext.Session
	}

	// gh#758/t11 slice A: without --exec, resume is now a thin alias for
	// `plan` -- literally the same planPrepare/printPlanResult/
	// printJSONEnvelope plan.go itself calls on the same resolved
	// coordinates, not a reimplementation, so this cannot drift from plan's
	// own output when no --role filter narrows the roster
	// (TestResumeAliasPlanIsByteIdenticalToPlan). This replaces the old
	// per-member classifier (team_resume.go's planMemberResume) for the
	// plan-only path entirely; --exec still goes through executeResume
	// below pending its own fold (slice B).
	if !*execMode {
		// Per-invocation launch overrides are dropped from the plan-only
		// preview (design note, cto-approved on task/t11): a relaunch
		// reproduces the profile's canonical configured shape rather than
		// carrying forward bespoke per-invocation overrides. Refuse
		// explicitly rather than silently ignoring them now that this path
		// no longer renders a per-member exec command to inject them into.
		for _, name := range []string{"trust", "model", "effort", "codex-args", "claude-args", "no-bootstrap"} {
			if flagWasSet(fs, name) {
				return usageErrorf("--%s no longer applies to resume's plan-only preview; edit team.json for a permanent change, or pass --exec (until its own fold lands) for a one-off override", name)
			}
		}
		// Ported from team_resume.go's executeResume (which this branch no
		// longer calls): a named profile whose AMQ root collides with a
		// legacy/default session root holding real durable state must
		// refuse before Prepare ever runs, the same guard `up`/`focus`/
		// `task add` etc. apply via ensureNoNamespaceConflict.
		namespaceConflict := namespaceConflictForProfileSession(resolvedContext.ProjectDir, resolvedContext.Profile, resolvedContext.Session)
		if namespaceConflict == nil {
			var cerr error
			namespaceConflict, cerr = defaultProfileShadowConflict(resolvedContext.ProjectDir, resolvedContext.Profile, resolvedContext.Session, flagWasSet(fs, "profile"))
			if cerr != nil {
				return fmt.Errorf("resume refused: scan named profiles for session %q: %w", resolvedContext.Session, cerr)
			}
		}
		// Unlike team_resume.go's old executeResume, this refuses even in
		// --json mode: that surfaced the conflict as structured data
		// (resumeEnvelopeData.NamespaceConflict) instead of erroring, a
		// shape the plan-only path's JSON envelope (planEnvelopeData) has
		// no equivalent field for. A scoped-down but safe simplification --
		// flagged for review rather than silently dropped -- refusing
		// closed either way is never wrong, only less informative to a
		// --json caller than the old behavior was.
		if namespaceConflict != nil {
			return namespaceConflictError("resume", namespaceConflict)
		}
		prepared, err := planPrepareFiltered(resolvedContext.ProjectDir, resolvedContext.Profile, resolvedContext.Session, parseResumeRoles(*roleFlag))
		if err != nil {
			return err
		}
		if *restoreExisting && len(prepared.Result.Preview.Roster.Present) == 0 {
			return fmt.Errorf("--restore-existing: no team members have restorable launch records for workstream %q", resolvedContext.Session)
		}
		if *jsonOut {
			return printJSONEnvelope("plan", planEnvelopeData{Request: prepared.Request, Result: prepared.Result})
		}
		printPlanResult(os.Stdout, prepared.Result)
		return nil
	}

	// gh#758/t11 slice B: --exec now folds into simple_start's shared
	// machinery (runResumeExec) instead of team_resume.go's executeResume.
	// --restore-existing, --no-bootstrap, and goal-redelivery are not part
	// of this fold yet (see resume_exec.go's own scope note); dropping
	// through to the legacy executeResume path below for those would
	// silently reintroduce the classifier this slice is removing, so they
	// are refused explicitly instead until their own follow-up lands.
	if *restoreExisting {
		return usageErrorf("--restore-existing does not yet apply to --exec (gh#758/t11); drop --exec to see the current plan, or drop --restore-existing")
	}
	if flagWasSet(fs, "no-bootstrap") {
		return usageErrorf("--no-bootstrap does not apply to --exec: the shared start machinery it now drives always emits the same one exact startup instruction start itself uses (gh#758/t11)")
	}
	if flagWasSet(fs, "redeliver-goal") || *suppressGoalPrompt {
		return usageErrorf("--redeliver-goal/--no-redeliver-goal-prompt do not yet apply to --exec (gh#758/t11 follow-up); drop --exec to see the current plan")
	}
	if *terminal != "tmux" {
		return usageErrorf("resume --exec currently requires the managed tmux backend (got --terminal %q)", *terminal)
	}
	return runResumeExec(resumeExecRequest{
		ProjectDir:      projectDir,
		Profile:         profile,
		Session:         requestedSession,
		Roles:           parseResumeRoles(*roleFlag),
		Force:           *forceDuplicate,
		TrustRaw:        *trustRaw,
		ModelRaw:        *modelFlag,
		EffortRaw:       *effortFlag,
		CodexArgsRaw:    *codexArgsRaw,
		ClaudeArgsRaw:   *claudeArgsRaw,
		Target:          *target,
		Layout:          *layout,
		TerminalSession: *terminalSession,
		Stagger:         *stagger,
		LaunchVia:       *launchVia,
		SkipLeadCheck:   *skipLeadCheck,
	}, os.Stdout)
}
