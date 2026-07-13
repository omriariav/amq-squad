package cli

import (
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

// resumeAction labels what `team resume` would do for one member.
type resumeAction string

const (
	resumeLive    resumeAction = "live"
	resumeRestore resumeAction = "restore"
	resumeFresh   resumeAction = "launch fresh"
	resumeBlocked resumeAction = "blocked"
)

type resumeMode string

const (
	resumeModeDefault         resumeMode = "default"
	resumeModeRestoreExisting resumeMode = "restore-existing"
	resumeModeFresh           resumeMode = "fresh"
)

// resumePlan is the per-member output row.
type resumePlan struct {
	Role    string
	Handle  string
	Action  resumeAction
	Wake    string
	Note    string
	Command string
	// HasRestoreRecord is true when a launch record matched this member's
	// (cwd, role, handle, workstream). Tracked separately from Action so
	// --restore-existing can verify record existence even when the final
	// action came out as live.
	HasRestoreRecord bool
	// Tmux is the persisted tmux identity of the matched restore record, when
	// any. Surfaced for `resume --json` so clients know which pane a restore
	// targets and whether that pane is still alive.
	Tmux *launch.TmuxInfo
	// Liveness is the shared liveness verdict (the SAME classifier status uses),
	// captured so `resume --json` can expose a `liveness` block a client compares
	// to `status --json` instead of inferring from the planning `Action`. nil on
	// the early blocked paths (amq env / preflight error) where no verdict ran.
	Liveness *agentLiveness
	// SavedLaunchIdentity fingerprints the exact record selected by
	// findMemberRestoreRecord. It is read-only discovery evidence; consumers
	// must not reconstruct it from an arbitrary scan order.
	SavedLaunchIdentity string
	Saved               *resumeSavedLaunchSummary
}

type resumeSavedLaunchSummary struct {
	Binary     string
	Model      string
	Effort     string
	NativeArgs []string
}

func runTeamResume(args []string) error {
	fs := flag.NewFlagSet("team resume", flag.ContinueOnError)
	sessionFlag := fs.String("session", "", "AMQ workstream session name to resume into (defaults to the team workstream)")
	restoreExisting := fs.Bool("restore-existing", false, "fail if no team member has restorable launch records for the workstream")
	fresh := fs.Bool("fresh", false, "ignore restore history; plan every member from team.json (use with --session for a new workstream)")
	dryRun := fs.Bool("dry-run", false, "plan-only; default behavior is already plan-only and exists for parity with other commands")
	forceDuplicate := fs.Bool("force-duplicate", false, "include commands even when a live agent is detected for a member")
	noBootstrap := fs.Bool("no-bootstrap", false, "emit fresh launch commands that skip the generated bootstrap prompt")
	trustRaw := fs.String("trust", "", "Codex trust profile for fresh members: approve-for-me (default), sandboxed, or trusted")
	modelFlag := fs.String("model", "", "per-persona model overrides for fresh members, e.g. cto=gpt-5.6-sol,fullstack=sonnet")
	effortFlag := fs.String("effort", "", "per-persona effort overrides for launch-fresh members, e.g. cto=xhigh,fullstack=max")
	codexArgsRaw := fs.String("codex-args", "", "extra Codex args for fresh members, e.g. '--enable goals'")
	claudeArgsRaw := fs.String("claude-args", "", "extra Claude args for fresh members, e.g. '--chrome'")
	projectFlag := fs.String("project", "", "project/team-home directory to plan (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to plan (default: default profile)")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned resume_plan envelope (liveness + tmux metadata) instead of the human plan")

	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad team resume - plan how to bring the team back

Usage:
  amq-squad team resume [--project DIR] [--profile NAME] [--session name] [--fresh]
                        [--restore-existing] [--dry-run] [--json] [--force-duplicate]
                        [--no-bootstrap] [--trust sandboxed|approve-for-me|trusted]
                        [--model role=model,...]
                        [--effort role=level,...]
                        [--codex-args args] [--claude-args args]

Inspects .amq-squad/team.json plus local launch history and live-agent
signals (wake locks, agent PID liveness, presence) to print a per-member
plan plus copy-pasteable commands.
--project targets another team-home without changing directories.

Per-member action labels:
  live          Matching agent appears live; command suppressed unless
                --force-duplicate is set.
  restore       Restorable launch.json exists for this workstream; emits
                'amq-squad agent up ...' that replays the saved record.
  launch fresh  No matching launch history; emits the same command shape
                'amq-squad up' would use for this member.
  blocked       Live signals present but no clear safe action; user must
                pick --force-duplicate or narrow the workstream.

Modes:
  default              Prefer restore for the workstream; fall back to
                       team intent for missing members.
  --restore-existing   Same as default, but error when no member has a
                       restorable record for the workstream.
  --fresh --session X  Ignore restore; plan every member from team.json.
                       Refuses if X already has live state unless
                       --force-duplicate is set.

team resume is plan-only. Run the printed commands in their own panes, or
use 'amq-squad up' to open them in tmux from team intent.

Examples:
  amq-squad team resume
  amq-squad team resume --project ~/Code/app --session issue-96
  amq-squad team resume --fresh --session issue-99
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *restoreExisting && *fresh {
		return usageErrorf("--restore-existing and --fresh are mutually exclusive")
	}
	mode := resumeModeDefault
	if *fresh {
		mode = resumeModeFresh
	} else if *restoreExisting {
		mode = resumeModeRestoreExisting
	}
	profile, err := resolveProfileFlag(*profileFlag)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	projectDir, err := resolveProjectDirFlag(cwd, *projectFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	if !team.ExistsProfile(projectDir, profile) {
		return fmt.Errorf("no team configured for profile %q. Run '%s' first.", profile, profileInitCommand(profile))
	}
	return executeResume(resumeExecution{
		ProjectDir:       projectDir,
		RequestedSession: *sessionFlag,
		ExplicitSession:  flagWasSet(fs, "session"),
		ExplicitProfile:  flagWasSet(fs, "profile"),
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
		Probe:            defaultDuplicateLaunchProbe,
	})
}

// resumeExecution is the shared input for the resume planner. team resume,
// top-level resume, and fork all build one and call executeResume so the
// planner stays the single source of truth.
type resumeExecution struct {
	ProjectDir       string
	RequestedSession string
	ExplicitSession  bool
	ExplicitProfile  bool
	// RolesRaw is the optional comma-separated subset of roles to resume. Empty
	// resumes every team member; a non-empty list restricts the plan (and
	// --exec) to those roles, so a lead can bring up a subset without
	// relaunching itself or other live members.
	RolesRaw      string
	Mode          resumeMode
	Force         bool
	NoBootstrap   bool
	TrustRaw      string
	ExplicitTrust bool
	ModelRaw      string
	EffortRaw     string
	CodexArgsRaw  string
	ClaudeArgsRaw string
	DryRun        bool
	Profile       string
	// JSON emits a schema-versioned resume_plan envelope instead of the human
	// plan. It is a read-only preview, so it is mutually exclusive with Exec.
	JSON bool
	// Probe abstracts liveness/process inspection for the per-member live-signal
	// classification (mirrors how statusExecution takes a probe). Defaults to
	// defaultDuplicateLaunchProbe when unset; tests inject a deterministic probe.
	// It does NOT govern the exec-time launch preflight, which stays on the
	// authoritative defaultDuplicateLaunchProbe (see execResumePlan).
	Probe duplicateLaunchProbe
	// Style controls the printer header label and footer verb so the same
	// planner can present its output as team resume, resume, or fork without
	// duplicating logic.
	Style resumePrinterStyle
	// Out receives the plan when Exec is disabled. Nil falls back to stdout.
	Out io.Writer
	// Exec opts in to the terminal-backend execution path: instead of
	// printing the per-member plan, the planner converts restore/launch-fresh
	// commands into a teamLaunchPlan and runs it through the chosen backend.
	// Members with action=live are skipped; action=blocked aborts the run
	// unless Force is also set.
	Exec resumeExecOptions
}

// resumeExecOptions carries the live-launch backend flags surfaced by
// `resume --exec`. They mirror the subset of liveLaunchFlags that resume
// needs, which keeps the resume entry point free of preview-flag plumbing
// that does not apply (no --fresh, no --json envelope).
type resumeExecOptions struct {
	Enabled         bool
	Terminal        string
	Target          string
	Layout          string
	TerminalSession string
	Stagger         time.Duration
}

type resumeExecLaunchCheck struct {
	Role       string
	CWD        string
	AgentDir   string
	Handle     string
	Workstream string
	Root       string
	Binary     string
	Profile    string
	Force      bool
}

type resumeExecLaunchSnapshot struct {
	Exists    bool
	ModTime   time.Time
	StartedAt time.Time
}

type resumeExecLaunchResult struct {
	Check  resumeExecLaunchCheck
	State  string
	Detail string
}

const (
	resumeExecLaunchStateLaunched    = "launched"
	resumeExecLaunchStateMissing     = "missing"
	resumeExecLaunchStateStaleRecord = "stale-record"
	resumeExecLaunchStateFailed      = "failed"
)

var (
	runTmuxLaunchPlanForResume       = runTmuxLaunchPlan
	verifyResumeExecLaunchRecordsNow = verifyResumeExecLaunchRecords
	resumeExecLaunchVerifyTimeout    = 5 * time.Second
	resumeExecLaunchVerifyInterval   = 100 * time.Millisecond
)

// resumePrinterStyle parameterizes the per-entry-point output surface. The
// zero value is the legacy `team resume` shape (preserved for old tests and
// existing scripts). Top-level resume and fork supply non-zero values.
type resumePrinterStyle struct {
	// Label is the verb that appears in the header and footer (e.g.
	// "team resume", "resume", "fork"). Empty falls back to "team resume".
	Label string
	// FooterVerb is the suggested tmux-launch verb in the footer. Empty
	// falls back to "up" (the modern team launcher; "team launch" is a
	// legacy verb).
	FooterVerb string
	// ForkFrom and ForkTo, when non-empty, add the fork "# from / # to"
	// lines to the header. Used only by `fork`.
	ForkFrom string
	ForkTo   string
	// Profile is the team profile the plan was built from. When non-default
	// the alternative-launch footer either suggests --profile NAME (when
	// the footer verb supports it) or suppresses itself rather than
	// printing a command that would silently fall back to default.
	Profile string
}

func (s resumePrinterStyle) label() string {
	if s.Label == "" {
		return "team resume"
	}
	return s.Label
}

func (s resumePrinterStyle) footerVerb() string {
	if s.FooterVerb == "" {
		return "up"
	}
	return s.FooterVerb
}

// parseResumeRoles splits the --role subset filter into a normalized, de-duped,
// ordered list of lowercase role ids. Empty input yields nil (no filter).
func parseResumeRoles(raw string) []string {
	seen := map[string]bool{}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		role := strings.ToLower(strings.TrimSpace(part))
		if role == "" || seen[role] {
			continue
		}
		seen[role] = true
		out = append(out, role)
	}
	return out
}

// teamRoleList returns the team's roles in canonical order, for error messages.
func teamRoleList(t team.Team) []string {
	out := make([]string, 0, len(t.Members))
	for _, m := range orderedTeamMembers(t.Members) {
		out = append(out, m.Role)
	}
	return out
}

func executeResume(r resumeExecution) error {
	trustMode, err := normalizeTrustMode(r.TrustRaw)
	if err != nil {
		return err
	}
	modelOverrides, err := parseKV(r.ModelRaw)
	if err != nil {
		return fmt.Errorf("parse --model: %w", err)
	}
	modelOverrides = lowercaseKeys(modelOverrides)
	effortOverrides, err := parseEffortOverrides(r.EffortRaw)
	if err != nil {
		return err
	}
	binaryArgs, err := parseBinaryArgFlags(r.CodexArgsRaw, r.ClaudeArgsRaw)
	if err != nil {
		return err
	}
	t, err := team.ReadProfile(r.ProjectDir, r.Profile)
	if err != nil {
		return fmt.Errorf("read team: %w", err)
	}
	if len(t.Members) == 0 {
		return fmt.Errorf("team has no members")
	}
	workstream, err := resolveTeamWorkstreamName(t, r.RequestedSession, r.ExplicitSession)
	if err != nil {
		return err
	}
	namespaceConflict := namespaceConflictForProfileSession(t.Project, r.Profile, workstream)
	if namespaceConflict == nil {
		var cerr error
		namespaceConflict, cerr = defaultProfileShadowConflict(t.Project, r.Profile, workstream, r.ExplicitProfile)
		if cerr != nil {
			return fmt.Errorf("resume refused: scan named profiles for session %q: %w", workstream, cerr)
		}
	}
	if namespaceConflict != nil && !r.JSON {
		return namespaceConflictError("resume", namespaceConflict)
	}
	active, skipped := filterMembersBySession(t.Members, workstream)
	for _, m := range skipped {
		quietNotice("notice: skipping %s: pinned to session %q, not %q\n", m.Role, m.Session, workstream)
	}
	if len(active) == 0 {
		return fmt.Errorf("no team members are pinned to session %q (all %d member(s) belong to other sessions)", workstream, len(t.Members))
	}
	t.Members = active
	mergedBinaryArgs := mergeBinaryArgs(t.BinaryArgs, binaryArgs)
	resolvedTrust, err := resolveTeamTrustMode(t, trustMode, r.ExplicitTrust)
	if err != nil {
		return err
	}
	if err := validateTrustCombination(resolvedTrust, r.ExplicitTrust || strings.TrimSpace(t.Trust) != "", false, mergedBinaryArgs); err != nil {
		return err
	}
	if err := validateMembersTrust(resolvedTrust, r.ExplicitTrust || strings.TrimSpace(t.Trust) != "", t.Members); err != nil {
		return err
	}
	if err := validateMemberOverlayPaths(t, t.Members); err != nil {
		return err
	}
	memberRoles := make(map[string]bool, len(t.Members))
	for _, m := range t.Members {
		memberRoles[strings.ToLower(m.Role)] = true
	}
	if err := validateModelOverrideKeys(modelOverrides, memberRoles); err != nil {
		return err
	}
	if err := validateEffortOverrideKeys(effortOverrides, memberRoles); err != nil {
		return err
	}

	// Optional --role subset: restrict the plan (and --exec) to the named roles.
	// Validate each against the roster up front so a typo fails clearly instead
	// of silently resuming nothing.
	roleFilter := parseResumeRoles(r.RolesRaw)
	if len(roleFilter) > 0 {
		var unknown []string
		for _, role := range roleFilter {
			if err := ensureTargetIsNotOperator(t, "resume", role); err != nil {
				return err
			}
			if !memberRoles[role] {
				unknown = append(unknown, role)
			}
		}
		if len(unknown) > 0 {
			return usageErrorf("--role: no team member(s) with role %s (team roles: %s)",
				strings.Join(unknown, ", "), strings.Join(teamRoleList(t), ", "))
		}
	}
	roleSelected := make(map[string]bool, len(roleFilter))
	for _, role := range roleFilter {
		roleSelected[role] = true
	}

	// Default the probe so callers that build a resumeExecution without one
	// (older entry points, fork) still classify against real liveness, while
	// tests can inject a deterministic probe. This mirrors how status takes a
	// probe and is independent of the exec-time launch preflight.
	probe := r.Probe
	if probe.PIDAlive == nil {
		probe = defaultDuplicateLaunchProbe
	}

	squadBin := teamSquadBin()
	plans := make([]resumePlan, 0, len(t.Members))
	planInputs := make(map[string]memberPlanInput, len(t.Members))
	recordCount := 0
	for _, m := range orderedTeamMembers(t.Members) {
		if len(roleSelected) > 0 && !roleSelected[strings.ToLower(m.Role)] {
			continue
		}
		input := memberPlanInput{
			Member:         m,
			Team:           t,
			Workstream:     workstream,
			Mode:           r.Mode,
			Force:          r.Force,
			NoBootstrap:    r.NoBootstrap,
			SquadBin:       squadBin,
			BinaryArgs:     mergedBinaryArgs,
			Trust:          resolvedTrust,
			ModelOverrides: modelOverrides,
			Profile:        r.Profile,
			Probe:          probe,
		}
		plan, err := planMemberResume(input)
		if err != nil {
			return err
		}
		if plan.HasRestoreRecord {
			recordCount++
		}
		planInputs[strings.ToLower(strings.TrimSpace(plan.Role))] = input
		plans = append(plans, plan)
	}
	if namespaceConflict != nil {
		plans = blockResumePlansForNamespaceConflict(plans, namespaceConflict)
	}
	if len(effortOverrides) > 0 {
		planIndexes := make(map[string]int, len(plans))
		for i, plan := range plans {
			planIndexes[strings.ToLower(strings.TrimSpace(plan.Role))] = i
		}
		var invalid []string
		for role := range effortOverrides {
			index, ok := planIndexes[role]
			if !ok {
				invalid = append(invalid, fmt.Sprintf("%s (not selected)", role))
				continue
			}
			plan := plans[index]
			if plan.Action != resumeFresh {
				invalid = append(invalid, fmt.Sprintf("%s (%s)", role, plan.Action))
			}
		}
		if len(invalid) > 0 {
			sort.Strings(invalid)
			return fmt.Errorf("--effort applies only to launch-fresh members; override target(s) are not launch-fresh: %s", strings.Join(invalid, ", "))
		}
		agentCatalog := loadAgentCatalogAndWarn(r.ProjectDir)
		for role, effort := range effortOverrides {
			input := planInputs[role]
			switch normalizedAgentBinary(input.Member.Binary) {
			case "codex":
				input.Member.CodexArgs = stripNativeEffortArgs(input.Member.CodexArgs, "codex")
			case "claude":
				input.Member.ClaudeArgs = stripNativeEffortArgs(input.Member.ClaudeArgs, "claude")
			}
			if err := applyMemberEffortCatalog(&input.Member, effort, agentCatalog); err != nil {
				return err
			}
			plans[planIndexes[role]].Command = freshLaunchCommand(input)
		}
	}

	// --restore-existing checks that restorable records EXIST for the
	// workstream, independent of whether the final action is restore.
	// Members that match a record but are currently live still satisfy
	// the contract: the records are present and would replay if the live
	// instance went away.
	if r.Mode == resumeModeRestoreExisting && recordCount == 0 {
		return fmt.Errorf("--restore-existing: no team members have restorable launch records for workstream %q", workstream)
	}

	// --fresh into an existing workstream must refuse unless explicitly
	// forced. A workstream is "existing" if either (a) at least one
	// member has a matching restorable record, or (b) the workstream's
	// AMQ root already contains mailbox state. Both shapes can be
	// silently overwritten by emitting fresh launches; the second
	// matches `team launch --fresh` semantics for parity.
	if r.Mode == resumeModeFresh && !r.Force {
		if recordCount > 0 {
			return fmt.Errorf("--fresh --session %q: %d member(s) already have launch records for this workstream; rerun with --force-duplicate to overwrite", workstream, recordCount)
		}
		exists, root, err := teamWorkstreamExists(t, workstream)
		if err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("--fresh --session %q: workstream root %s already exists; rerun with --force-duplicate to reuse", workstream, root)
		}
	}

	if r.JSON {
		out := r.Out
		if out == nil {
			out = os.Stdout
		}
		return writeResumeJSON(out, t, workstream, r.Mode, r.Profile, namespaceConflict, plans)
	}

	if r.Exec.Enabled {
		if namespaceConflict != nil {
			return namespaceConflictError("resume --exec", namespaceConflict)
		}
		return execResumePlan(t, r.Profile, workstream, plans, r.Exec, r.Force)
	}

	style := r.Style
	style.Profile = r.Profile
	out := r.Out
	if out == nil {
		out = os.Stdout
	}
	writeResumePlan(out, t, workstream, r.Mode, plans, r.DryRun, r.Force, style)
	return nil
}

// execResumePlan converts the per-member resume plan into a team launch
// plan and runs it through the selected backend. Members already live are
// skipped; members in the blocked action abort the run unless force was
// requested. The contract matches operator expectation that `resume --exec`
// is the inverse of `down`: it brings what is down back, leaves what is up
// alone, and refuses to silently sidestep duplicate-launch protection.
func execResumePlan(t team.Team, profile, workstream string, plans []resumePlan, exec resumeExecOptions, force bool) error {
	backend, ok := teamLaunchBackends[exec.Terminal]
	if !ok {
		return fmt.Errorf("unsupported terminal %q: supported terminals: %s", exec.Terminal, strings.Join(registeredTeamLaunchTerminals(), ", "))
	}
	// resume --exec runs the built-in tmux plan (runTmuxLaunchPlan) directly from
	// restore records; it does not drive the external window-per-agent
	// `tmux-session` backend. Reject any non-tmux terminal here rather than
	// validating it and silently running the tmux backend instead. Window-per-
	// agent on resume is available natively via `--target new-window`.
	if exec.Terminal != "tmux" {
		return fmt.Errorf("resume --exec runs the built-in tmux backend; --terminal %q is not supported on resume. For one window per agent, use --target new-window.", exec.Terminal)
	}

	var (
		panes   []teamLaunchPane
		skipped []resumePlan
		blocked []resumePlan
	)
	for _, p := range plans {
		switch p.Action {
		case resumeLive:
			// planMemberResume emits a non-empty Command for live members
			// only when the operator passed --force-duplicate; otherwise the
			// command is suppressed. Honor that distinction here: relaunch
			// live+forced members through the backend (parity with the
			// printed plan) and skip the others with a clear notice.
			if p.Command != "" {
				panes = append(panes, teamLaunchPane{
					Role:    p.Role,
					CWD:     planMemberCWD(t, p.Role),
					Command: p.Command,
				})
				continue
			}
			skipped = append(skipped, p)
		case resumeBlocked:
			blocked = append(blocked, p)
		case resumeRestore, resumeFresh:
			if p.Command == "" {
				// Defensive: planner emitted no command for a runnable
				// action. Treat as blocked rather than silently dropping.
				blocked = append(blocked, p)
				continue
			}
			panes = append(panes, teamLaunchPane{
				Role:    p.Role,
				CWD:     planMemberCWD(t, p.Role),
				Command: p.Command,
			})
		}
	}

	if len(blocked) > 0 && !force {
		roles := make([]string, 0, len(blocked))
		for _, p := range blocked {
			roles = append(roles, fmt.Sprintf("%s (%s)", p.Role, p.Note))
		}
		return fmt.Errorf("refusing to exec resume: %d member(s) blocked: %s. Resolve manually or rerun with --force-duplicate.", len(blocked), strings.Join(roles, ", "))
	}
	if len(panes) == 0 {
		// Nothing to do is success: all members are live or there is no
		// recoverable plan. Tell the operator explicitly rather than
		// silently opening an empty tmux window.
		if err := reconcileNotificationWatcherStarted(t, profile, workstream, ""); err != nil {
			return err
		}
		fmt.Printf("# amq-squad resume --exec\n# workstream: %s\n# nothing to launch (%d live, %d blocked)\n", workstream, len(skipped), len(blocked))
		return nil
	}

	opts := teamLaunchOptions{
		Terminal:        exec.Terminal,
		Target:          exec.Target,
		Layout:          exec.Layout,
		Workstream:      workstream,
		TerminalSession: exec.TerminalSession,
		Stagger:         exec.Stagger,
		SquadBin:        teamSquadBin(),
	}
	if err := backend.Validate(opts); err != nil {
		return err
	}

	plan := tmuxLaunchPlan{
		Session:              opts.TerminalSession,
		Workstream:           opts.Workstream,
		Target:               opts.Target,
		Layout:               opts.Layout,
		Panes:                panes,
		StartDelay:           opts.Stagger,
		AllowExistingSession: true,
	}
	if plan.Session == "" {
		plan.Session = defaultTmuxSessionName(t.Project)
	}

	// Roster-level preflight before any pane opens, mirroring `up`'s
	// contract (team_launch.go:243). Without this, each per-pane `agent up`
	// preflights at exec time — but the operator only sees the refusal
	// AFTER tmux has split panes. Run the same aggregate check now so a
	// blocked member aborts cleanly before any backend side effects, and
	// honor --force-duplicate by stamping it into each plan.
	checks, err := buildResumeExecLaunchChecks(t, panes, profile, workstream, force)
	if err != nil {
		return err
	}
	if err := preflightTeam(resumeExecLaunchPreflights(checks), defaultDuplicateLaunchProbe); err != nil {
		return err
	}
	watcherBefore := notificationWatcherGeneration(t, profile, workstream)
	watcherBaseRoot := ""
	if len(checks) > 0 && strings.TrimSpace(checks[0].Root) != "" {
		watcherBaseRoot = filepath.Dir(checks[0].Root)
	}
	if err := reconcileNotificationWatcherStarted(t, profile, workstream, watcherBaseRoot); err != nil {
		return err
	}
	watcherAfter := notificationWatcherGeneration(t, profile, workstream)
	createdWatcherToken := ""
	if watcherAfter != "" && watcherAfter != watcherBefore {
		createdWatcherToken = watcherAfter
	}

	for _, p := range skipped {
		fmt.Fprintf(os.Stderr, "skipping %s: %s\n", p.Role, p.Note)
	}
	snapshots := snapshotResumeExecLaunchRecords(checks)
	if err := runTmuxLaunchPlanForResume(plan); err != nil {
		if cleanupErr := cleanupCreatedNotificationWatcherAfterLaunchFailure(t, profile, workstream, createdWatcherToken, defaultDuplicateLaunchProbe); cleanupErr != nil {
			return fmt.Errorf("%w; notification watcher cleanup after clean resume failure: %v", err, cleanupErr)
		}
		return err
	}
	results := verifyResumeExecLaunchRecordsNow(checks, snapshots)
	if err := resumeExecLaunchError(results); err != nil {
		if cleanupErr := cleanupCreatedNotificationWatcherAfterLaunchFailure(t, profile, workstream, createdWatcherToken, defaultDuplicateLaunchProbe); cleanupErr != nil {
			return fmt.Errorf("%w; notification watcher cleanup after clean resume verification failure: %v", err, cleanupErr)
		}
		return err
	}
	return nil
}

// buildResumeExecPreflights resolves the AMQ identity for each runnable
// pane and constructs an agentLaunchPreflight tuple that preflightTeam can
// use to refuse a blocked roster before tmux opens any pane.
func buildResumeExecPreflights(t team.Team, panes []teamLaunchPane, profile, workstream string, force bool) ([]agentLaunchPreflight, error) {
	checks, err := buildResumeExecLaunchChecks(t, panes, profile, workstream, force)
	if err != nil {
		return nil, err
	}
	return resumeExecLaunchPreflights(checks), nil
}

func buildResumeExecLaunchChecks(t team.Team, panes []teamLaunchPane, profile, workstream string, force bool) ([]resumeExecLaunchCheck, error) {
	byRole := make(map[string]team.Member, len(t.Members))
	for _, m := range t.Members {
		byRole[strings.ToLower(m.Role)] = m
	}
	out := make([]resumeExecLaunchCheck, 0, len(panes))
	for _, pane := range panes {
		m, ok := byRole[strings.ToLower(pane.Role)]
		if !ok {
			// Pane built from a role we cannot resolve back to team.json:
			// preflight cannot inspect identity, so skip it rather than
			// fabricate a tuple that would block on the wrong agent dir.
			continue
		}
		cwd := m.EffectiveCWD(t.Project)
		env, err := resolveAMQEnvForTeamProfile(cwd, profile, workstream, m.Handle)
		if err != nil {
			return nil, fmt.Errorf("resolve amq env for %s: %w", m.Handle, err)
		}
		root := absoluteAMQRoot(cwd, env.Root)
		handle := m.Handle
		if env.Me != "" {
			handle = env.Me
		}
		out = append(out, resumeExecLaunchCheck{
			Role:       pane.Role,
			CWD:        cwd,
			AgentDir:   filepath.Join(root, "agents", handle),
			Handle:     handle,
			Workstream: env.SessionName,
			Root:       root,
			Binary:     m.Binary,
			Profile:    profile,
			Force:      force,
		})
	}
	return out, nil
}

func resumeExecLaunchPreflights(checks []resumeExecLaunchCheck) []agentLaunchPreflight {
	out := make([]agentLaunchPreflight, 0, len(checks))
	for _, c := range checks {
		out = append(out, agentLaunchPreflight{
			AgentDir:   c.AgentDir,
			Handle:     c.Handle,
			Workstream: c.Workstream,
			Root:       c.Root,
			Binary:     c.Binary,
			Force:      c.Force,
		})
	}
	return out
}

func snapshotResumeExecLaunchRecords(checks []resumeExecLaunchCheck) map[string]resumeExecLaunchSnapshot {
	out := make(map[string]resumeExecLaunchSnapshot, len(checks))
	for _, c := range checks {
		snap := resumeExecLaunchSnapshot{}
		path := launch.ExistingPath(c.AgentDir)
		if info, err := os.Stat(path); err == nil {
			snap.Exists = true
			snap.ModTime = info.ModTime()
		}
		if rec, err := launch.Read(c.AgentDir); err == nil {
			snap.Exists = true
			snap.StartedAt = rec.StartedAt
		}
		out[c.Role] = snap
	}
	return out
}

func verifyResumeExecLaunchRecords(checks []resumeExecLaunchCheck, snapshots map[string]resumeExecLaunchSnapshot) []resumeExecLaunchResult {
	deadline := time.Now().Add(resumeExecLaunchVerifyTimeout)
	for {
		results := inspectResumeExecLaunchRecords(checks, snapshots)
		if allResumeExecLaunchesDone(results) {
			return results
		}
		if !time.Now().Before(deadline) {
			if adoptResumeExecLaunchRecords(results) {
				return inspectResumeExecLaunchRecords(checks, snapshots)
			}
			return results
		}
		time.Sleep(resumeExecLaunchVerifyInterval)
	}
}

func adoptResumeExecLaunchRecords(results []resumeExecLaunchResult) bool {
	panes, err := statusPaneLister()
	if err != nil || len(panes) == 0 {
		return false
	}
	adopted := false
	for _, r := range results {
		if r.State != resumeExecLaunchStateStaleRecord {
			continue
		}
		pane, ok := resumeExecAdoptionPane(r.Check, panes)
		if !ok {
			continue
		}
		rec, err := launch.Read(r.Check.AgentDir)
		if err != nil {
			continue
		}
		rec.Role = r.Check.Role
		rec.Handle = r.Check.Handle
		rec.Session = r.Check.Workstream
		rec.Root = r.Check.Root
		rec.CWD = r.Check.CWD
		rec.Binary = r.Check.Binary
		rec.TeamProfile = r.Check.Profile
		rec.StartedAt = time.Now().UTC()
		rec.Tmux = &launch.TmuxInfo{
			Session:    pane.Session,
			WindowID:   pane.WindowID,
			WindowName: pane.WindowName,
			PaneID:     pane.PaneID,
			Target:     "adopted",
		}
		rec.Terminal = launch.TerminalInfoFromTmux(rec.Tmux)
		if err := launch.Write(r.Check.AgentDir, rec); err == nil {
			adopted = true
		}
	}
	return adopted
}

func resumeExecAdoptionPane(c resumeExecLaunchCheck, panes []tmuxpane.TmuxPane) (tmuxpane.TmuxPane, bool) {
	wantTitle := paneTitleToken(c.Workstream, c.Role)
	for _, p := range panes {
		if strings.TrimSpace(p.PaneID) == "" || p.Title != wantTitle {
			continue
		}
		if strings.TrimSpace(c.CWD) != "" && canonicalPath(p.CWD) != canonicalPath(c.CWD) {
			continue
		}
		if !resumeExecPaneCommandMatchesBinary(p.Command, c.Binary) {
			continue
		}
		return p, true
	}
	return tmuxpane.TmuxPane{}, false
}

func resumeExecPaneCommandMatchesBinary(command, binary string) bool {
	b := strings.ToLower(strings.TrimSpace(binary))
	if b == "" {
		return false
	}
	cmd := strings.ToLower(strings.TrimSpace(command))
	if cmd == "" {
		return false
	}
	if i := strings.LastIndexByte(cmd, '/'); i >= 0 {
		cmd = cmd[i+1:]
	}
	return cmd == b || strings.HasPrefix(cmd, b)
}

func inspectResumeExecLaunchRecords(checks []resumeExecLaunchCheck, snapshots map[string]resumeExecLaunchSnapshot) []resumeExecLaunchResult {
	results := make([]resumeExecLaunchResult, 0, len(checks))
	for _, c := range checks {
		res := resumeExecLaunchResult{Check: c, State: resumeExecLaunchStateLaunched}
		rec, err := launch.Read(c.AgentDir)
		if err != nil {
			res.State = resumeExecLaunchStateMissing
			res.Detail = "launch record missing at " + launch.ExistingPath(c.AgentDir)
			results = append(results, res)
			continue
		}
		snap := snapshots[c.Role]
		if snap.Exists {
			path := launch.ExistingPath(c.AgentDir)
			if info, statErr := os.Stat(path); statErr == nil && !info.ModTime().After(snap.ModTime) && !rec.StartedAt.After(snap.StartedAt) {
				res.State = resumeExecLaunchStateStaleRecord
				res.Detail = "launch record was not refreshed at " + path
				results = append(results, res)
				continue
			}
		}
		if !strings.EqualFold(strings.TrimSpace(rec.Role), strings.TrimSpace(c.Role)) {
			res.State = resumeExecLaunchStateFailed
			res.Detail = fmt.Sprintf("launch record role %q does not match requested role %q", rec.Role, c.Role)
			results = append(results, res)
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(rec.Handle), strings.TrimSpace(c.Handle)) {
			res.State = resumeExecLaunchStateFailed
			res.Detail = fmt.Sprintf("launch record handle %q does not match requested handle %q", rec.Handle, c.Handle)
			results = append(results, res)
			continue
		}
		if strings.TrimSpace(rec.Session) != strings.TrimSpace(c.Workstream) {
			res.State = resumeExecLaunchStateFailed
			res.Detail = fmt.Sprintf("launch record workstream %q does not match requested workstream %q", rec.Session, c.Workstream)
			results = append(results, res)
			continue
		}
		if !squadnamespace.ProfilesEqual(c.Profile, rec.TeamProfile) {
			res.State = resumeExecLaunchStateFailed
			res.Detail = fmt.Sprintf("launch record profile %q does not match requested profile %q", squadnamespace.NormalizeProfile(rec.TeamProfile), squadnamespace.NormalizeProfile(c.Profile))
			results = append(results, res)
			continue
		}
		if rec.Tmux == nil || strings.TrimSpace(rec.Tmux.PaneID) == "" {
			res.State = resumeExecLaunchStateFailed
			res.Detail = "launch record did not capture a tmux pane id"
			results = append(results, res)
			continue
		}
		results = append(results, res)
	}
	return results
}

func allResumeExecLaunchesDone(results []resumeExecLaunchResult) bool {
	for _, r := range results {
		if r.State != resumeExecLaunchStateLaunched {
			return false
		}
	}
	return true
}

func resumeExecLaunchError(results []resumeExecLaunchResult) error {
	failed := make([]resumeExecLaunchResult, 0)
	for _, r := range results {
		if r.State != resumeExecLaunchStateLaunched {
			failed = append(failed, r)
		}
	}
	if len(failed) == 0 {
		return nil
	}
	lines := []string{fmt.Sprintf("resume --exec partial launch failure: %d of %d requested member(s) did not publish a fresh launch record:", len(failed), len(results))}
	for _, r := range failed {
		lines = append(lines, fmt.Sprintf("  - %s: %s: %s", r.Check.Role, r.State, r.Detail))
	}
	msg := strings.Join(lines, "\n")
	fmt.Fprintln(os.Stderr, msg)
	return &PartialError{Message: msg}
}

// planMemberCWD resolves the effective cwd for a planned role by looking up
// the team member. Falls back to the team project when the role is no
// longer in team.json (which should not happen because the planner derives
// its members from team.json, but it keeps execResumePlan robust against
// future planner changes).
func planMemberCWD(t team.Team, role string) string {
	for _, m := range t.Members {
		if strings.EqualFold(m.Role, role) {
			return m.EffectiveCWD(t.Project)
		}
	}
	return t.Project
}

func blockResumePlansForNamespaceConflict(plans []resumePlan, conflict *namespaceConflictData) []resumePlan {
	if conflict == nil {
		return plans
	}
	out := append([]resumePlan(nil), plans...)
	for i := range out {
		out[i].Action = resumeBlocked
		out[i].Command = ""
		out[i].Note = "namespace conflict: " + conflict.Detail
	}
	return out
}

type memberPlanInput struct {
	Member         team.Member
	Team           team.Team
	Workstream     string
	Mode           resumeMode
	Force          bool
	NoBootstrap    bool
	SquadBin       string
	BinaryArgs     map[string][]string
	Trust          string
	ModelOverrides map[string]string
	Profile        string
	// Probe abstracts liveness/process inspection for live-signal
	// classification. Zero value falls back to defaultDuplicateLaunchProbe so
	// direct callers and tests that omit it still get real liveness checks.
	Probe duplicateLaunchProbe
}

// planMemberResume classifies one team member and emits the appropriate
// command. Disk state is never mutated: preflight runs in DryRun mode and
// stale lock cleanup is deferred to the real launch path.
func planMemberResume(in memberPlanInput) (resumePlan, error) {
	m := in.Member
	cwd := m.EffectiveCWD(in.Team.Project)
	plan := resumePlan{Role: m.Role, Wake: "-"}

	// Resolve the AMQ env per-member so multi-cwd teams work.
	env, err := resolveAMQEnvForTeamProfile(cwd, in.Profile, in.Workstream, m.Handle)
	if err != nil {
		// Without the AMQ env we cannot inspect restore history, wake
		// locks, presence, or duplicate risk. A safety-oriented planner
		// must classify this as blocked, not silently emit fresh.
		plan.Action = resumeBlocked
		plan.Note = fmt.Sprintf("amq env unavailable: %v", err)
		// Carry a liveness verdict even here so --json always has one (and so it
		// agrees with status, which also reports missing when the env is
		// unresolvable). The env failure means we cannot inspect any signal.
		plan.Liveness = &agentLiveness{Verdict: livenessMissing, Status: statusStateMissing, Detail: plan.Note}
		if in.Force {
			plan.Action = resumeFresh
			plan.Note = "force-duplicate: " + plan.Note
			plan.Command = freshLaunchCommand(in)
		}
		return plan, nil
	}
	root := absoluteAMQRoot(cwd, env.Root)
	handle := m.Handle
	if env.Me != "" {
		handle = env.Me
	}
	agentDir := filepath.Join(root, "agents", handle)

	// Find a restorable launch record for this member in this workstream
	// and current cwd. Without the cwd anchor a sibling repo with the
	// same role/handle/session would let team resume emit the wrong
	// repo's restore command.
	baseRoot := absoluteAMQRoot(cwd, env.BaseRoot)
	rec, recFound := findMemberRestoreRecord(baseRoot, in.Team.Project, cwd, in.Profile, env.SessionName, m.Role, handle)
	plan.HasRestoreRecord = recFound
	plan.Handle = handle
	if recFound {
		plan.Tmux = rec.Tmux
		plan.SavedLaunchIdentity = resumeSavedLaunchIdentity(rec)
		savedMember := team.Member{Binary: rec.Binary}
		if strings.EqualFold(rec.Binary, "claude") {
			savedMember.ClaudeArgs = append([]string(nil), rec.Argv...)
		} else {
			savedMember.CodexArgs = append([]string(nil), rec.Argv...)
		}
		savedEffort := memberEffort(savedMember)
		if savedEffort == effortAutomatic {
			savedMember.CodexArgs = append([]string(nil), rec.CodexArgs...)
			savedMember.ClaudeArgs = append([]string(nil), rec.ClaudeArgs...)
			savedEffort = memberEffort(savedMember)
		}
		extraArgs := rec.CodexArgs
		if strings.EqualFold(rec.Binary, "claude") {
			extraArgs = rec.ClaudeArgs
		}
		plan.Saved = &resumeSavedLaunchSummary{Binary: rec.Binary, Model: rec.Model, Effort: savedEffort, NativeArgs: wizardSavedExtraArgs(rec.Binary, extraArgs)}
	}
	if recFound && projectLeadExternalRecordBoundaryViolation(in.Team, m, rec, in.Profile, env.SessionName, root, handle) {
		plan.Action = resumeBlocked
		plan.Command = ""
		plan.Note = fmt.Sprintf("role boundary violation: current external record for %s is not verified as a project lead; launch/resume %s in a sibling tab/new managed pane, or keep the current pane as global orchestrator only", m.Role, m.Role)
		plan.Liveness = &agentLiveness{Verdict: livenessMissing, Status: statusStateMissing, Detail: plan.Note}
		return plan, nil
	}
	wakeLabel := wakeHealthForMember(agentDir, root, handle, rec, recFound)
	plan.Wake = wakeLabel

	probe := in.Probe
	if probe.PIDAlive == nil {
		probe = defaultDuplicateLaunchProbe
	}

	// Single shared liveness verdict — the same classifier status consumes (the
	// #79 fix: status and resume can never disagree). Computed up front so EVERY
	// return path below — including the forced preflight-error path — carries a
	// liveness block.
	live := classifyAgentLiveness(agentDir, root, in.Profile, handle, m.Role, m.Binary, env.SessionName, cwd, probe)
	plan.Liveness = &live

	// #95: a live agent launched outside amq-squad's tmux backend has no recorded
	// tmux block; adopt its live pane so resume --json exposes the same pane
	// identity status does (focus/attach parity across surfaces). Verified
	// AGENT-live only: that verdict proves Signals.AgentPID is a live process of
	// the right binary, so PID lineage is safe. wake-live/presence-live have no
	// verified agent pid (#95 review).
	if live.Verdict == livenessAgentLive && plan.Tmux == nil {
		if panes, perr := statusPaneLister(); perr == nil {
			if adopted := adoptLivePane(m.Role, handle, m.Binary, cwd, env.SessionName, live.Signals.AgentPID, panes, childrenPidTree()); adopted != nil {
				plan.Tmux = adopted
			}
		}
	}

	// Surface a real I/O inspection error as blocked, preserving the prior
	// safety contract. The preflight is still the authority on read errors; we
	// run it in dry-run mode purely to catch perr (it reaps nothing on disk in
	// dry-run). The live/stale DECISION comes from the shared classifier above.
	pf := agentLaunchPreflight{
		AgentDir:   agentDir,
		Handle:     handle,
		Workstream: env.SessionName,
		Root:       root,
		Binary:     m.Binary,
		Force:      false,
		DryRun:     true,
	}
	blocker, perr := pf.check(probe)
	if perr != nil {
		// I/O error reading state: treat as blocked unless forced.
		plan.Action = resumeBlocked
		plan.Note = fmt.Sprintf("preflight error: %v", perr)
		if in.Force {
			plan.Action = resumeRestore
			if !recFound || in.Mode == resumeModeFresh {
				plan.Action = resumeFresh
				plan.Command = freshLaunchCommand(in)
			} else {
				// Forced restore on preflight error path: same reason
				// to inject --force-duplicate as the live+forced path.
				plan.Command = emitCommandWithOptions(rec, emitCommandOptions{Force: true, NoBootstrap: in.NoBootstrap})
			}
			plan.Note = "force-duplicate: " + plan.Note
		}
		return plan, nil
	}

	if live.Live() {
		// Live signal detected (agent / wake / presence / replacement). Same
		// contract as before: suppress the command unless --force-duplicate.
		note := resumeLiveNote(live, m.Binary)
		if in.Force {
			plan.Action = resumeLive
			plan.Note = "force-duplicate: " + note
			if in.Mode == resumeModeFresh || !recFound {
				plan.Command = freshLaunchCommand(in)
			} else {
				// Forced restore must carry --force-duplicate so the
				// printed command bypasses launch-time preflight.
				plan.Command = emitCommandWithOptions(rec, emitCommandOptions{Force: true, NoBootstrap: in.NoBootstrap})
			}
			return plan, nil
		}
		plan.Action = resumeLive
		plan.Note = note
		// Suppress the command by default.
		plan.Command = ""
		return plan, nil
	}
	if blocker != nil && !in.Force {
		plan.Action = resumeBlocked
		plan.Command = ""
		plan.Note = "preflight blocker: " + strings.ReplaceAll(blocker.Error(), "\n", "; ")
		if strings.Contains(live.Detail, "profile") {
			plan.Note = live.Detail + "; " + plan.Note
		}
		return plan, nil
	}

	// No live signal (verdict stale or missing). Choose restore vs fresh based
	// on mode + record presence.
	if in.Mode == resumeModeFresh {
		plan.Action = resumeFresh
		plan.Command = freshLaunchCommand(in)
		return plan, nil
	}
	if recFound && rec.External {
		plan.Action = resumeBlocked
		plan.Command = ""
		plan.Note = "external/adopted record is not restorable; run 'amq-squad lead register' again from the lead pane"
		return plan, nil
	}
	if recFound {
		plan.Action = resumeRestore
		plan.Command = emitCommandWithOptions(rec, emitCommandOptions{NoBootstrap: in.NoBootstrap})
		// Be honest about what restore will do: a record with a saved
		// conversation truly reattaches the prior thread and skips bootstrap;
		// a record without one re-runs bootstrap so the agent re-orients from
		// its brief and drains AMQ history rather than coming up blank.
		if rec.Conversation != "" {
			plan.Note = "reattach: saved conversation " + rec.Conversation
		} else {
			plan.Note = "fresh agent: re-orient from brief + AMQ history (no saved conversation)"
		}
		return plan, nil
	}
	plan.Action = resumeFresh
	plan.Command = freshLaunchCommand(in)
	return plan, nil
}

func wizardSavedExtraArgs(binary string, args []string) []string {
	binary = normalizedAgentBinary(binary)
	booleans := claudeBooleanArgs
	if binary == "codex" {
		booleans = codexBooleanArgs
	}
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" || !strings.HasPrefix(arg, "-") {
			return out
		}
		spec, inline, known := nativeValueSpecForArg(binary, arg)
		if known {
			if inline {
				if !wizardSeparatelyRenderedNativeValue(binary, spec, compactNativeValue(arg)) {
					out = append(out, arg)
				}
				continue
			}
			values := make([]string, 0, 2)
			switch spec.Cardinality {
			case nativeRequired:
				if i+1 < len(args) && args[i+1] != "--" && !strings.HasPrefix(args[i+1], "-") {
					i++
					values = append(values, args[i])
				}
			case nativeOptional:
				if i+1 < len(args) && args[i+1] != "--" && !strings.HasPrefix(args[i+1], "-") {
					i++
					values = append(values, args[i])
				}
			case nativeVariadic:
				for i+1 < len(args) && args[i+1] != "--" && !strings.HasPrefix(args[i+1], "-") {
					i++
					values = append(values, args[i])
				}
			}
			value := ""
			if len(values) > 0 {
				value = values[0]
			}
			if !wizardSeparatelyRenderedNativeValue(binary, spec, value) {
				out = append(out, arg)
				out = append(out, values...)
			}
			continue
		}
		if booleans[arg] {
			out = append(out, arg)
			continue
		}
		name, _, _ := strings.Cut(arg, "=")
		out = append(out, name)
		return out
	}
	return out
}

func compactNativeValue(arg string) string {
	if strings.HasPrefix(arg, "-c=") {
		return strings.TrimPrefix(arg, "-c=")
	}
	if strings.HasPrefix(arg, "-c") && len(arg) > 2 {
		return strings.TrimPrefix(arg, "-c")
	}
	if _, value, ok := strings.Cut(arg, "="); ok {
		return value
	}
	return ""
}

func wizardSeparatelyRenderedNativeValue(binary string, spec nativeValueSpec, value string) bool {
	if spec.Canonical == "--model" || binary == "claude" && spec.Canonical == "--effort" {
		return true
	}
	if binary != "codex" || spec.Canonical != "--config" {
		return false
	}
	key, _, ok := strings.Cut(strings.TrimSpace(value), "=")
	return ok && (key == "model" || key == "model_reasoning_effort")
}

func resumeSavedLaunchIdentity(rec launch.Record) string {
	payload, _ := json.Marshal(rec)
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", sum)
}

func projectLeadExternalRecordBoundaryViolation(t team.Team, m team.Member, rec launch.Record, profile, session, root, handle string) bool {
	if !projectExecutionMode(effectiveTeamExecutionMode(t)) {
		return false
	}
	lead := strings.TrimSpace(t.Lead)
	if lead == "" && len(t.Members) == 1 {
		lead = t.Members[0].Role
	}
	if strings.TrimSpace(m.Role) != lead {
		return false
	}
	if !rec.External {
		return false
	}
	return !launchRecordAuthorizesProjectLead(rec, m.Role, handle, profile, session, root)
}

// findMemberRestoreRecord returns the most recent launch.Record for the
// given (member project, member cwd, workstream, role, handle) tuple under
// baseRoot. memberCWD anchors identity to the current team member's
// project; records whose CWD does not resolve to the same path are
// rejected so a sibling repo with the same role/handle/session cannot
// leak its restore command into this team's plan. Records with empty
// CWD (legacy AMQ-only inference) are accepted as fallback only when no
// CWD-matching record exists.
func findMemberRestoreRecord(baseRoot, projectDir, memberCWD, profile, workstream, role, handle string) (launch.Record, bool) {
	if baseRoot == "" {
		return launch.Record{}, false
	}
	entries, err := launch.ScanRestorableEntriesInRoot(projectDir, baseRoot)
	if err != nil {
		return launch.Record{}, false
	}
	wantCWD := canonicalPath(memberCWD)
	var bestExact, bestLegacy *launch.Entry
	for i := range entries {
		rec := entries[i].Record
		if !matchesRestoreFiltersForProfile(rec, role, handle, workstream, "", profile) {
			continue
		}
		recCWD := canonicalPath(rec.CWD)
		switch {
		case recCWD != "" && recCWD == wantCWD:
			if bestExact == nil || rec.StartedAt.After(bestExact.Record.StartedAt) {
				bestExact = &entries[i]
			}
		case recCWD == "":
			if bestLegacy == nil || rec.StartedAt.After(bestLegacy.Record.StartedAt) {
				bestLegacy = &entries[i]
			}
		}
	}
	if bestExact != nil {
		return bestExact.Record, true
	}
	if bestLegacy != nil {
		return bestLegacy.Record, true
	}
	return launch.Record{}, false
}

// canonicalPath returns a normalized absolute path or "" when input is
// empty. Symlink resolution is best effort: when EvalSymlinks fails (e.g.
// the path no longer exists) the absolute clean form is returned.
func canonicalPath(p string) string {
	if strings.TrimSpace(p) == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

// wakeHealthForMember returns the wake-health label for a member, using
// the same wake-process matcher as v0.6 preflight so PID reuse by an
// unrelated process never surfaces as pid:N. expectedRoot is the AMQ root
// the member targets; it is used (along with the lock's recorded root)
// to verify the live PID is actually an `amq wake` for this workstream.
func wakeHealthForMember(agentDir, expectedRoot, handle string, rec launch.Record, recFound bool) string {
	if recFound {
		entry := launch.Entry{Record: rec, AgentDir: agentDir}
		if label := wakeHealthForEntry(entry, defaultDuplicateLaunchProbe); label != "" {
			return label
		}
	}
	lockPath := wakeLockPath(agentDir)
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return "-"
	}
	var lock wakeLockFile
	if err := json.Unmarshal(data, &lock); err != nil {
		return "stale"
	}
	if lock.PID <= 0 || !defaultDuplicateLaunchProbe.PIDAlive(lock.PID) {
		return "stale"
	}
	probeRoot := expectedRoot
	if lock.Root != "" {
		probeRoot = lock.Root
	}
	if !defaultDuplicateLaunchProbe.ProcessMatch(lock.PID, wakeProcessMatcher(handle, probeRoot)) {
		// PID reuse by an unrelated process or a foreign-root wake.
		return "stale"
	}
	return fmt.Sprintf("pid:%d", lock.PID)
}

// resumeLiveNote produces the per-member plan Note for a live verdict,
// preserving the exact wording resume emitted before the classifier unification:
//   - replacement-live keeps resume's "recorded pid dead; live <bin> at
//     <target>; relaunch..." phrasing (the form its tests assert), and
//   - the agent/wake/presence verdicts list EVERY live source (not just the
//     highest-precedence verdict) in the preflight blocker order wake+launch+
//     presence, joined with "+", exactly as summarizeBlocker did (so a
//     multi-signal live agent still reads "wake+launch+presence").
func resumeLiveNote(live agentLiveness, binary string) string {
	if live.Verdict == livenessReplacementLive {
		return fmt.Sprintf("recorded pid dead; live %s at %s; relaunch via amq-squad to re-register", binary, live.ReplacementTarget)
	}
	var parts []string
	if live.Signals.WakeAlive {
		parts = append(parts, "wake")
	}
	if live.Signals.AgentAlive && live.Signals.BinaryMatch {
		parts = append(parts, "launch")
	}
	if live.PresenceLive {
		parts = append(parts, "presence")
	}
	if len(parts) == 0 {
		return "live"
	}
	return strings.Join(parts, "+")
}

// freshLaunchCommand emits the same command shape `team launch` would use
// for this member, so trust/model/binary-args behavior matches.
func freshLaunchCommand(in memberPlanInput) string {
	cwd := in.Member.EffectiveCWD(in.Team.Project)
	return emitTeamCommand(emitTeamCommandInput{
		CWD:            cwd,
		SquadBin:       in.SquadBin,
		TeamHome:       in.Team.Project,
		Member:         in.Member,
		NoBootstrap:    in.NoBootstrap,
		Workstream:     in.Workstream,
		BinaryArgs:     in.BinaryArgs,
		TrustMode:      in.Trust,
		Model:          memberResolvedModel(in.Member, in.ModelOverrides, in.BinaryArgs),
		ForceDuplicate: in.Force,
		Profile:        in.Profile,
	})
}

func printResumePlan(t team.Team, workstream string, mode resumeMode, plans []resumePlan, dryRun, force bool, style resumePrinterStyle) {
	writeResumePlan(os.Stdout, t, workstream, mode, plans, dryRun, force, style)
}

func writeResumePlan(out io.Writer, t team.Team, workstream string, mode resumeMode, plans []resumePlan, dryRun, force bool, style resumePrinterStyle) {
	fmt.Fprintf(out, "# amq-squad %s\n", style.label())
	fmt.Fprintln(out, "#")
	fmt.Fprintf(out, "# team-home:  %s\n", t.Project)
	if style.ForkFrom != "" {
		fmt.Fprintf(out, "# from:       %s\n", style.ForkFrom)
	}
	if style.ForkTo != "" {
		fmt.Fprintf(out, "# to:         %s\n", style.ForkTo)
	}
	fmt.Fprintf(out, "# workstream: %s\n", workstream)
	fmt.Fprintf(out, "# mode:       %s\n", describeResumeMode(mode, dryRun))
	if style.label() == "resume" {
		fmt.Fprintf(out, "# preview:    plan-only; run 'amq-squad resume --exec --session %s' to open panes\n", shellQuote(workstream))
	}
	fmt.Fprintf(out, "# members:    %d\n", len(plans))
	fmt.Fprintln(out)

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ROLE\tACTION\tWAKE\tNOTE")
	for _, p := range plans {
		note := p.Note
		if note == "" {
			note = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", p.Role, p.Action, p.Wake, note)
	}
	w.Flush()
	fmt.Fprintln(out)

	for _, p := range plans {
		if p.Command == "" {
			fmt.Fprintf(out, "# %s (%s) - no command (use --force-duplicate to override)\n", p.Role, p.Action)
			continue
		}
		fmt.Fprintf(out, "# %s (%s)\n", p.Role, p.Action)
		fmt.Fprintln(out, p.Command)
		fmt.Fprintln(out)
	}

	// The team-launch alternative replays from team intent (always fresh
	// commands), not from the restore records the per-member plan may
	// have used. The footer is only equivalent when every row is a
	// fresh-from-team-intent row with a real command. Anything else --
	// restore, live (including live+forced+recorded which emits a
	// restore command), blocked, or suppressed -- means the footer would
	// not reproduce the per-member plan, so suppress it conservatively.
	allFresh := true
	for _, p := range plans {
		if p.Action != resumeFresh || p.Command == "" {
			allFresh = false
			break
		}
	}
	verb := style.footerVerb()
	if !allFresh {
		fmt.Fprintf(out, "# Note: '%s' would re-emit fresh commands from team intent,\n", verb)
		fmt.Fprintln(out, "# which is not equivalent to the per-member plan above.")
		return
	}
	profileSuffix := ""
	if style.Profile != "" && style.Profile != team.DefaultProfile {
		// Only verbs that accept --profile can carry the selection into the
		// suggested footer command. up accepts --profile. If a future verb
		// does not, suppress the footer rather than print a command that
		// would fall back to the default profile.
		if verb == "up" {
			profileSuffix = " --profile " + shellQuote(style.Profile)
		} else {
			fmt.Fprintf(out, "# Note: '%s' has no --profile flag yet; rerun the per-member commands above to bring up the %s profile.\n", verb, style.Profile)
			return
		}
	}
	fmt.Fprintln(out, "# Alternative: open the whole team in tmux from team intent")
	suffix := ""
	if force {
		suffix = " --force-duplicate"
	}
	if mode == resumeModeFresh {
		fmt.Fprintf(out, "# %s %s --fresh --session %s%s%s\n", filepath.Base(teamSquadBin()), verb, shellQuote(workstream), suffix, profileSuffix)
	} else {
		fmt.Fprintf(out, "# %s %s --session %s%s%s\n", filepath.Base(teamSquadBin()), verb, shellQuote(workstream), suffix, profileSuffix)
	}
}

func describeResumeMode(mode resumeMode, dryRun bool) string {
	suffix := ""
	if dryRun {
		suffix = " (--dry-run; plan-only)"
	}
	switch mode {
	case resumeModeFresh:
		return "fresh" + suffix
	case resumeModeRestoreExisting:
		return "restore-existing (require restorable records)" + suffix
	default:
		return "default (prefer restore, fall back to team intent)" + suffix
	}
}
