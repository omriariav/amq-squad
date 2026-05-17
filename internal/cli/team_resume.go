package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/omriariav/amq-squad/internal/launch"
	"github.com/omriariav/amq-squad/internal/team"
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
	Action  resumeAction
	Wake    string
	Note    string
	Command string
	// HasRestoreRecord is true when a launch record matched this member's
	// (cwd, role, handle, workstream). Tracked separately from Action so
	// --restore-existing can verify record existence even when the final
	// action came out as live.
	HasRestoreRecord bool
}

func runTeamResume(args []string) error {
	fs := flag.NewFlagSet("team resume", flag.ContinueOnError)
	sessionFlag := fs.String("session", "", "AMQ workstream session name to resume into (defaults to the team workstream)")
	restoreExisting := fs.Bool("restore-existing", false, "fail if no team member has restorable launch records for the workstream")
	fresh := fs.Bool("fresh", false, "ignore restore history; plan every member from team.json (use with --session for a new workstream)")
	dryRun := fs.Bool("dry-run", false, "plan-only; default behavior is already plan-only and exists for parity with other commands")
	forceDuplicate := fs.Bool("force-duplicate", false, "include commands even when a live agent is detected for a member")
	noBootstrap := fs.Bool("no-bootstrap", false, "emit fresh launch commands that skip the generated bootstrap prompt")
	trustRaw := fs.String("trust", "", "Codex trust profile for fresh members: sandboxed (default) or trusted")
	modelFlag := fs.String("model", "", "per-persona model overrides for fresh members, e.g. cto=gpt-5,fullstack=sonnet")
	codexArgsRaw := fs.String("codex-args", "", "extra Codex args for fresh members, e.g. '--enable goals'")
	claudeArgsRaw := fs.String("claude-args", "", "extra Claude args for fresh members, e.g. '--chrome'")
	profileFlag := fs.String("profile", "", "team profile to plan (default: default profile)")

	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad team resume - plan how to bring the team back

Usage:
  amq-squad team resume [--profile NAME] [--session name] [--fresh]
                        [--restore-existing] [--dry-run] [--force-duplicate]
                        [--no-bootstrap] [--trust sandboxed|trusted]
                        [--model role=model,...]
                        [--codex-args args] [--claude-args args]

Inspects .amq-squad/team.json plus local launch history and live-agent
signals (wake locks, agent PID liveness, presence) to print a per-member
plan plus copy-pasteable commands.

Per-member action labels:
  live          Matching agent appears live; command suppressed unless
                --force-duplicate is set.
  restore       Restorable launch.json exists for this workstream; emits
                'amq-squad launch ...' that replays the saved record.
  launch fresh  No matching launch history; emits the same command shape
                'amq-squad team launch' would use for this member.
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
use 'amq-squad team launch' to open them in tmux from team intent.
`)
	}
	if err := fs.Parse(args); err != nil {
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
	if !team.ExistsProfile(cwd, profile) {
		return fmt.Errorf("no team configured for profile %q. Run 'amq-squad team init%s' first.", profile, profileInitHint(profile))
	}
	return executeResume(resumeExecution{
		ProjectDir:       cwd,
		RequestedSession: *sessionFlag,
		ExplicitSession:  flagWasSet(fs, "session"),
		Mode:             mode,
		Force:            *forceDuplicate,
		NoBootstrap:      *noBootstrap,
		TrustRaw:         *trustRaw,
		ExplicitTrust:    flagWasSet(fs, "trust"),
		ModelRaw:         *modelFlag,
		CodexArgsRaw:     *codexArgsRaw,
		ClaudeArgsRaw:    *claudeArgsRaw,
		DryRun:           *dryRun,
		Profile:          profile,
	})
}

// resumeExecution is the shared input for the resume planner. team resume,
// top-level resume, and fork all build one and call executeResume so the
// planner stays the single source of truth.
type resumeExecution struct {
	ProjectDir       string
	RequestedSession string
	ExplicitSession  bool
	Mode             resumeMode
	Force            bool
	NoBootstrap      bool
	TrustRaw         string
	ExplicitTrust    bool
	ModelRaw         string
	CodexArgsRaw     string
	ClaudeArgsRaw    string
	DryRun           bool
	Profile          string
	// Style controls the printer header label and footer verb so the same
	// planner can present its output as team resume, resume, or fork without
	// duplicating logic.
	Style resumePrinterStyle
}

// resumePrinterStyle parameterizes the per-entry-point output surface. The
// zero value is the legacy `team resume` shape (preserved for old tests and
// existing scripts). Top-level resume and fork supply non-zero values.
type resumePrinterStyle struct {
	// Label is the verb that appears in the header and footer (e.g.
	// "team resume", "resume", "fork"). Empty falls back to "team resume".
	Label string
	// FooterVerb is the suggested tmux-launch verb in the footer. Empty
	// falls back to "team launch".
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
		return "team launch"
	}
	return s.FooterVerb
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
	mergedBinaryArgs := mergeBinaryArgs(t.BinaryArgs, binaryArgs)
	resolvedTrust, err := resolveTeamTrustMode(t, trustMode, r.ExplicitTrust)
	if err != nil {
		return err
	}
	if err := validateTrustCombination(resolvedTrust, r.ExplicitTrust || strings.TrimSpace(t.Trust) != "", false, mergedBinaryArgs); err != nil {
		return err
	}
	memberRoles := make(map[string]bool, len(t.Members))
	for _, m := range t.Members {
		memberRoles[strings.ToLower(m.Role)] = true
	}
	if err := validateModelOverrideKeys(modelOverrides, memberRoles); err != nil {
		return err
	}

	squadBin := teamSquadBin()
	plans := make([]resumePlan, 0, len(t.Members))
	recordCount := 0
	for _, m := range orderedTeamMembers(t.Members) {
		plan, err := planMemberResume(memberPlanInput{
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
		})
		if err != nil {
			return err
		}
		if plan.HasRestoreRecord {
			recordCount++
		}
		plans = append(plans, plan)
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

	style := r.Style
	style.Profile = r.Profile
	printResumePlan(t, workstream, r.Mode, plans, r.DryRun, r.Force, style)
	return nil
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
}

// planMemberResume classifies one team member and emits the appropriate
// command. Disk state is never mutated: preflight runs in DryRun mode and
// stale lock cleanup is deferred to the real launch path.
func planMemberResume(in memberPlanInput) (resumePlan, error) {
	m := in.Member
	cwd := m.EffectiveCWD(in.Team.Project)
	plan := resumePlan{Role: m.Role, Wake: "-"}

	// Resolve the AMQ env per-member so multi-cwd teams work.
	env, err := resolveAMQEnvInDir(cwd, "", in.Workstream, m.Handle)
	if err != nil {
		// Without the AMQ env we cannot inspect restore history, wake
		// locks, presence, or duplicate risk. A safety-oriented planner
		// must classify this as blocked, not silently emit fresh.
		plan.Action = resumeBlocked
		plan.Note = fmt.Sprintf("amq env unavailable: %v", err)
		if in.Force {
			plan.Action = resumeFresh
			plan.Note = "force-duplicate: " + plan.Note
			plan.Command = freshLaunchCommand(in)
		}
		return plan, nil
	}
	root := env.Root
	handle := m.Handle
	if env.Me != "" {
		handle = env.Me
	}
	agentDir := filepath.Join(root, "agents", handle)

	// Find a restorable launch record for this member in this workstream
	// and current cwd. Without the cwd anchor a sibling repo with the
	// same role/handle/session would let team resume emit the wrong
	// repo's restore command.
	rec, recFound := findMemberRestoreRecord(env.BaseRoot, in.Team.Project, cwd, env.SessionName, m.Role, handle)
	plan.HasRestoreRecord = recFound
	wakeLabel := wakeHealthForMember(agentDir, root, handle, rec, recFound)
	plan.Wake = wakeLabel

	// Run preflight in dry-run mode for live-signal classification.
	pf := agentLaunchPreflight{
		AgentDir:   agentDir,
		Handle:     handle,
		Workstream: env.SessionName,
		Root:       root,
		Binary:     m.Binary,
		Force:      false,
		DryRun:     true,
	}
	blocker, perr := pf.check(defaultDuplicateLaunchProbe)
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
				plan.Command = emitCommandWithOptions(rec, emitCommandOptions{Force: true})
			}
			plan.Note = "force-duplicate: " + plan.Note
		}
		return plan, nil
	}

	if blocker != nil {
		// Live signal detected.
		if in.Force {
			plan.Action = resumeLive
			plan.Note = "force-duplicate: " + summarizeBlocker(blocker)
			if in.Mode == resumeModeFresh || !recFound {
				plan.Command = freshLaunchCommand(in)
			} else {
				// Forced restore must carry --force-duplicate so the
				// printed command bypasses launch-time preflight.
				plan.Command = emitCommandWithOptions(rec, emitCommandOptions{Force: true})
			}
			return plan, nil
		}
		plan.Action = resumeLive
		plan.Note = summarizeBlocker(blocker)
		// Suppress the command by default.
		plan.Command = ""
		return plan, nil
	}

	// No live signal. Choose restore vs fresh based on mode + record presence.
	if in.Mode == resumeModeFresh {
		plan.Action = resumeFresh
		plan.Command = freshLaunchCommand(in)
		return plan, nil
	}
	if recFound {
		plan.Action = resumeRestore
		plan.Command = emitCommand(rec)
		return plan, nil
	}
	plan.Action = resumeFresh
	plan.Command = freshLaunchCommand(in)
	return plan, nil
}

// findMemberRestoreRecord returns the most recent launch.Record for the
// given (member project, member cwd, workstream, role, handle) tuple under
// baseRoot. memberCWD anchors identity to the current team member's
// project; records whose CWD does not resolve to the same path are
// rejected so a sibling repo with the same role/handle/session cannot
// leak its restore command into this team's plan. Records with empty
// CWD (legacy AMQ-only inference) are accepted as fallback only when no
// CWD-matching record exists.
func findMemberRestoreRecord(baseRoot, projectDir, memberCWD, workstream, role, handle string) (launch.Record, bool) {
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
		if !matchesRestoreFilters(rec, role, handle, workstream, "") {
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

// summarizeBlocker compresses a duplicateBlocker into a one-line note.
func summarizeBlocker(b *duplicateBlocker) string {
	if b == nil || len(b.Reasons) == 0 {
		return "live"
	}
	parts := make([]string, 0, len(b.Reasons))
	for _, r := range b.Reasons {
		parts = append(parts, r.Source)
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
		Model:          memberEffectiveModel(in.Member, in.ModelOverrides),
		ForceDuplicate: in.Force,
		Profile:        in.Profile,
	})
}

func printResumePlan(t team.Team, workstream string, mode resumeMode, plans []resumePlan, dryRun, force bool, style resumePrinterStyle) {
	fmt.Printf("# amq-squad %s\n", style.label())
	fmt.Println("#")
	fmt.Printf("# team-home:  %s\n", t.Project)
	if style.ForkFrom != "" {
		fmt.Printf("# from:       %s\n", style.ForkFrom)
	}
	if style.ForkTo != "" {
		fmt.Printf("# to:         %s\n", style.ForkTo)
	}
	fmt.Printf("# workstream: %s\n", workstream)
	fmt.Printf("# mode:       %s\n", describeResumeMode(mode, dryRun))
	fmt.Printf("# members:    %d\n", len(plans))
	fmt.Println()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ROLE\tACTION\tWAKE\tNOTE")
	for _, p := range plans {
		note := p.Note
		if note == "" {
			note = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", p.Role, p.Action, p.Wake, note)
	}
	w.Flush()
	fmt.Println()

	for _, p := range plans {
		if p.Command == "" {
			fmt.Printf("# %s (%s) - no command (use --force-duplicate to override)\n", p.Role, p.Action)
			continue
		}
		fmt.Printf("# %s (%s)\n", p.Role, p.Action)
		fmt.Println(p.Command)
		fmt.Println()
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
		fmt.Printf("# Note: '%s' would re-emit fresh commands from team intent,\n", verb)
		fmt.Println("# which is not equivalent to the per-member plan above.")
		return
	}
	profileSuffix := ""
	if style.Profile != "" && style.Profile != team.DefaultProfile {
		// Only verbs that accept --profile can carry the selection into the
		// suggested footer command. up and team launch both accept --profile
		// in Step 9A. If a future verb does not, suppress the footer rather
		// than print a command that would fall back to the default profile.
		if verb == "up" || verb == "team launch" {
			profileSuffix = " --profile " + shellQuote(style.Profile)
		} else {
			fmt.Printf("# Note: '%s' has no --profile flag yet; rerun the per-member commands above to bring up the %s profile.\n", verb, style.Profile)
			return
		}
	}
	fmt.Println("# Alternative: open the whole team in tmux from team intent")
	suffix := ""
	if force {
		suffix = " --force-duplicate"
	}
	if mode == resumeModeFresh {
		fmt.Printf("# %s %s --fresh --session %s%s%s\n", filepath.Base(teamSquadBin()), verb, shellQuote(workstream), suffix, profileSuffix)
	} else {
		fmt.Printf("# %s %s --session %s%s%s\n", filepath.Base(teamSquadBin()), verb, shellQuote(workstream), suffix, profileSuffix)
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
