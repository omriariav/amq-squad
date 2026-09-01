package cli

import (
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

var (
	teamMemberLaunch = runResume
	teamMemberStop   = runDown

	teamMemberBeforeRosterMutation = func() {}
)

// runTeamMember dispatches `amq-squad team member <add|update|control-continue|rm|list>`: runtime roster
// mutation. This is the durable-roster primitive the goal-first composition
// model rests on — a lead (any binary) grows or shrinks its team mid-session,
// and the change persists to team.json so resume rebuilds the team it built.
// teamMemberUsageText is the shared help block for `team member` and every
// third-level subcommand under it. gh#762 found that `team member add
// --help` / `team member rm --help` exited 1 with a validation error instead
// of showing help (peelPositional ran before any -h/--help check), and
// `team member list --help` fell through to Go's default flag.Usage banner
// instead of this repo's custom-formatted help -- both are one class of bug
// (a third-level subcommand's own args, not just the second-level
// dispatcher, must intercept -h/--help before any other parsing). Every
// subcommand handler below now checks wantsHelp(args) first and prints this
// same block, so the class cannot regress subcommand-by-subcommand.
const teamMemberUsageText = `amq-squad team member - add or remove a roster member at runtime

Usage:
  amq-squad team member add <role> --binary <claude|codex> [--handle H]
      [--session S] [--model M] [--effort E] [--claude-args "…"] [--codex-args "…"]
      [--actor-mode review|implementation]
      [--spawn-origin NAME] [--spawn-depth N]
      [--project DIR] [--profile NAME] [--launch] [--target new-window] [--skip-lead-check] [--dry-run] [--json]
  amq-squad team member control-continue <role> --client EXACT_CLIENT
      [--session S] [--project DIR] [--profile NAME] [--json]
  amq-squad team member update <role> [--binary <claude|codex>] [--handle H]
      [--session S | --no-session-pin]
      [--model M] [--effort E] [--claude-args "…"] [--codex-args "…"]
      [--actor-mode review|implementation] [--project DIR] [--profile NAME]
      [--dry-run] [--json]
  amq-squad team member rm <role> [--project DIR] [--profile NAME]
      [--stop] [--force] [--close-panes] [--dry-run] [--json]
  amq-squad team member list [--json] [--project DIR] [--profile NAME]

Mutates the persisted team profile (team.json) atomically and under an
exclusive lock, then re-validates it (orchestration constraints included).
The new member is NOT launched; run 'start' to reconcile the roster and spawn
only missing roles. Replace a running role with 'down <role>' followed by
'start'.

'update' changes an existing member in place (binary, session pin, model,
effort, native args, handle, actor-mode) without the remove-then-add dance; the only
way today to adjust the orchestration lead, since 'rm' refuses to remove it.
Only the flags you pass are changed; the rest of the member is untouched.
Changing a self_operator lead's --session does NOT reconfigure its exact-session
self-operator policy (SelfOperatorPolicy is keyed by session name); run
'amq-squad team operator set' for the new session afterward if that mode is in use.

Examples:
  amq-squad team member add researcher --binary codex
  amq-squad team member add qa --binary claude --model sonnet
  amq-squad team member update qa --model opus --effort high
  amq-squad team member update cto --session issue-97
  amq-squad team member update pm --no-session-pin
  amq-squad team member rm researcher
`

// wantsHelp reports whether args opens with -h/--help. A third-level
// subcommand (add/update/rm/list/control-continue) must check this BEFORE
// peeling a required positional or parsing flags, or --help fails closed as
// a validation error / falls through to Go's own default usage banner
// instead of this repo's help text (gh#762).
func wantsHelp(args []string) bool {
	return len(args) > 0 && (args[0] == "-h" || args[0] == "--help")
}

func runTeamMember(args []string) error {
	if len(args) == 0 || wantsHelp(args) {
		fmt.Fprint(os.Stderr, teamMemberUsageText)
		if len(args) == 0 {
			return usageErrorf("member requires a subcommand ('add', 'update', 'list', or 'rm')")
		}
		return nil
	}
	switch args[0] {
	case "add":
		return runTeamMemberAdd(args[1:])
	case "update":
		return runTeamMemberUpdate(args[1:])
	case "control-continue":
		return runTeamMemberControlContinue(args[1:])
	case "rm", "remove":
		return runTeamMemberRemove(args[1:])
	case "list", "ls":
		return runTeamMemberList(args[1:])
	default:
		return unknownSubcommandError(
			"team member", args[0],
			"add", "update", "control-continue", "rm", "remove", "list", "ls",
		)
	}
}

// runTeamMemberList prints the current roster — the read companion to add/rm,
// so a lead can see the team it has built without opening team.json.
func runTeamMemberList(args []string) error {
	if wantsHelp(args) {
		fmt.Fprint(os.Stderr, teamMemberUsageText)
		return nil
	}
	fs := flag.NewFlagSet("team member list", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to read (default: default profile)")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned roster envelope")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("unexpected argument %q", fs.Arg(0))
	}
	projectDir, profile, err := resolveExistingTeamProfile(*projectFlag, *profileFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	t, err := team.ReadProfile(projectDir, profile)
	if err != nil {
		return fmt.Errorf("read team: %w", err)
	}
	members := orderedTeamMembers(t.Members)
	if *jsonOut {
		return printJSONEnvelope("team_roster", teamRosterData{
			Profile: profile, Orchestrated: t.Orchestrated, Lead: t.Lead, Members: members,
		})
	}
	if len(members) == 0 {
		fmt.Println("(no members)")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	// The LEAD column only carries information for an orchestrated team; omit it
	// entirely for a flat team rather than printing an always-blank column.
	if t.Orchestrated {
		fmt.Fprintln(w, "ROLE\tBINARY\tHANDLE\tMODEL\tSESSION\tLEAD")
	} else {
		fmt.Fprintln(w, "ROLE\tBINARY\tHANDLE\tMODEL\tSESSION")
	}
	for _, m := range members {
		model := orDash(m.Model)
		session := orDash(m.Session)
		if t.Orchestrated {
			lead := ""
			if m.Role == t.Lead {
				lead = "lead"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", m.Role, m.Binary, m.Handle, model, session, lead)
		} else {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", m.Role, m.Binary, m.Handle, model, session)
		}
	}
	return w.Flush()
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// teamRosterData is the `team member list --json` payload.
type teamRosterData struct {
	Profile      string        `json:"profile"`
	Orchestrated bool          `json:"orchestrated"`
	Lead         string        `json:"lead,omitempty"`
	Members      []team.Member `json:"members"`
}

// peelPositional splits a leading positional argument from the remaining flag
// args (Go's flag parser stops at the first non-flag, so a positional that
// precedes flags must be peeled first). ok is false when the first arg is
// missing or is itself a flag; the caller supplies the context-specific error.
func peelPositional(args []string) (val string, rest []string, ok bool) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return "", nil, false
	}
	return args[0], args[1:], true
}

func runTeamMemberAdd(args []string) error {
	if wantsHelp(args) {
		fmt.Fprint(os.Stderr, teamMemberUsageText)
		return nil
	}
	role, rest, ok := peelPositional(args)
	if !ok {
		return usageErrorf("a role is required, e.g. 'team member add researcher --binary codex'")
	}
	fs := flag.NewFlagSet("team member add", flag.ContinueOnError)
	binaryFlag := fs.String("binary", "", "agent CLI for this member: claude or codex (required)")
	handleFlag := fs.String("handle", "", "AMQ handle (defaults to the role)")
	sessionFlag := fs.String("session", "", "AMQ workstream session (defaults to the team's existing session)")
	modelFlag := fs.String("model", "", "native model name passed to the binary")
	effortFlag := fs.String("effort", "", "native effort tier for this member; automatic emits no effort arg")
	spawnOriginFlag := fs.String("spawn-origin", "", "override recorded composition origin (default: AM_ME or operator/manual)")
	spawnDepthFlag := fs.Int("spawn-depth", -1, "override recorded composition depth (default: inferred from origin)")
	claudeArgsRaw := fs.String("claude-args", "", "extra Claude args for this member")
	codexArgsRaw := fs.String("codex-args", "", "extra Codex args for this member")
	actorModeFlag := fs.String("actor-mode", team.ActorModeReview, "actor execution capability: review (default, read-only) or implementation")
	// #538: worktree_isolation readiness tells the operator to give each
	// mutation-capable member its own working directory. That was reachable only
	// at roster creation (team init / new profile --cwd), so fixing an EXISTING
	// roster meant hand-editing team.json -- the exact thing the CLI exists to
	// prevent. This is the post-creation path.
	cwdFlag := fs.String("cwd", "", "working directory (isolated worktree) for this member; 2+ mutation-capable members sharing one directory blocks worktree_isolation readiness")
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to mutate (default: default profile)")
	launchFlag := fs.Bool("launch", false, "after adding, launch pending members with resume --exec")
	targetFlag := fs.String("target", "new-window", "launch target for --launch (current-window|new-window|new-session)")
	skipLeadCheckFlag := fs.Bool("skip-lead-check", false, "with --launch: launch without verifying the configured lead's live pane (recovery escape hatch for a stale lead record)")
	dryRunFlag := fs.Bool("dry-run", false, "preview roster and launch actions without mutating")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned mutation result envelope")
	if err := parseFlags(fs, rest); err != nil {
		return err
	}
	if !*launchFlag && flagWasSet(fs, "target") {
		return usageErrorf("--target requires --launch")
	}
	if !*launchFlag && *skipLeadCheckFlag {
		return usageErrorf("--skip-lead-check requires --launch")
	}

	bin := normalizedAgentBinary(*binaryFlag)
	if bin != "claude" && bin != "codex" {
		return usageErrorf("--binary is required and must be claude or codex (got %q)", *binaryFlag)
	}
	// Normalize the role at the boundary so dedup and validation match the
	// stored (always-lowercase) roles, and so 'Researcher' is accepted rather
	// than failing the slug check with a confusing error.
	role = strings.ToLower(strings.TrimSpace(role))
	if err := team.ValidateRoleID(role); err != nil {
		return fmt.Errorf("role: %w", err)
	}

	var err error
	var claudeArgs, codexArgs []string
	if strings.TrimSpace(*claudeArgsRaw) != "" {
		if claudeArgs, err = parseAgentArgs(*claudeArgsRaw); err != nil {
			return fmt.Errorf("parse --claude-args: %w", err)
		}
	}
	if strings.TrimSpace(*codexArgsRaw) != "" {
		if codexArgs, err = parseAgentArgs(*codexArgsRaw); err != nil {
			return fmt.Errorf("parse --codex-args: %w", err)
		}
	}
	// Per-member args are bound to the member's binary (matching the team.json
	// binary-match rule). Reject a mismatch rather than silently dropping it.
	if bin == "codex" && len(claudeArgs) > 0 {
		return usageErrorf("--claude-args applies only to claude members")
	}
	if bin == "claude" && len(codexArgs) > 0 {
		return usageErrorf("--codex-args applies only to codex members")
	}
	if *spawnDepthFlag < -1 {
		return usageErrorf("--spawn-depth cannot be negative")
	}
	actorMode := strings.ToLower(strings.TrimSpace(*actorModeFlag))
	if actorMode != team.ActorModeReview && actorMode != team.ActorModeImplementation {
		return usageErrorf("--actor-mode must be %s or %s", team.ActorModeReview, team.ActorModeImplementation)
	}

	projectDir, profile, err := resolveExistingTeamProfile(*projectFlag, *profileFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	agentCatalog := loadAgentCatalogAndWarn(projectDir)

	var added team.Member
	buildAdded := func(t team.Team) (team.Member, error) {
		for _, m := range t.Members {
			if m.Role == role {
				return team.Member{}, fmt.Errorf("role %q is already on the team", role)
			}
		}
		handle := strings.ToLower(strings.TrimSpace(*handleFlag))
		if handle == "" {
			handle = role
		}
		for _, m := range t.Members {
			if m.Handle == handle {
				return team.Member{}, fmt.Errorf("handle %q is already in use; pass a distinct --handle", handle)
			}
		}
		origin, depth, err := inferRuntimeSpawn(t, *spawnOriginFlag, *spawnDepthFlag)
		if err != nil {
			return team.Member{}, err
		}
		session := strings.ToLower(strings.TrimSpace(*sessionFlag))
		if session == "" {
			session = inheritedSession(t)
		}
		added := team.Member{
			Role:        role,
			Binary:      bin,
			Handle:      handle,
			Session:     session,
			Model:       strings.TrimSpace(*modelFlag),
			ActorMode:   actorMode,
			SpawnOrigin: origin,
			SpawnDepth:  depth,
		}
		if flagWasSet(fs, "cwd") {
			resolved, cwdErr := memberCWDOverride(t.Project, *cwdFlag)
			if cwdErr != nil {
				return team.Member{}, cwdErr
			}
			added.CWD = resolved
		}
		if bin == "claude" {
			added.ClaudeArgs = claudeArgs
		} else {
			added.CodexArgs = codexArgs
		}
		if flagWasSet(fs, "effort") {
			if bin == "claude" {
				added.ClaudeArgs = stripNativeEffortArgs(added.ClaudeArgs, bin)
			} else {
				added.CodexArgs = stripNativeEffortArgs(added.CodexArgs, bin)
			}
			if err := applyMemberEffortCatalog(&added, *effortFlag, agentCatalog); err != nil {
				return team.Member{}, err
			}
		}
		return added, nil
	}
	if *dryRunFlag {
		t, err := team.ReadProfile(projectDir, profile)
		if err != nil {
			return fmt.Errorf("read team: %w", err)
		}
		added, err = buildAdded(t)
		if err != nil {
			return err
		}
		if *jsonOut {
			return printJSONEnvelope("team_member_add", mutationResult{
				Command: "team member add", Status: "preview", Project: projectDir,
				Session: added.Session, Profile: profile, Role: added.Role, Handle: added.Handle,
			})
		}
		fmt.Printf("# preview: would add %s (%s) to profile %s\n", added.Role, added.Binary, profile)
		if *launchFlag {
			fmt.Printf("# preview: would launch with:\n  %s\n", teamMemberLaunchCommand(projectDir, profile, added.Session, *targetFlag, *skipLeadCheckFlag))
		}
		return nil
	}
	currentTeam, err := team.ReadProfile(projectDir, profile)
	if err != nil {
		return fmt.Errorf("read team: %w", err)
	}
	predictedSession := strings.ToLower(strings.TrimSpace(*sessionFlag))
	if predictedSession == "" {
		predictedSession = inheritedSession(currentTeam)
	}
	mutation := func(expectedProfileDigest string) error {
		return withProfileLock(projectDir, profile, func() error {
			if err := verifyAcceptedProfileDigestBeforeRosterMutation(team.ProfilePath(projectDir, profile), expectedProfileDigest); err != nil {
				return err
			}
			t, err := team.ReadProfile(projectDir, profile)
			if err != nil {
				return fmt.Errorf("read team: %w", err)
			}
			oldTeam := t
			oldTeam.Members = append([]team.Member(nil), t.Members...)
			added, err = buildAdded(t)
			if err != nil {
				return err
			}
			if added.Session != predictedSession {
				return fmt.Errorf("team profile changed concurrently while adding %q; retry", role)
			}
			t.Members = append(t.Members, added)
			// WriteProfileUnderLock re-validates the whole team (orchestration, per-member
			// binary-match, duplicate handles) before the atomic rename, so an
			// invalid add never persists.
			if err := writeTeamProfileWithAMQRosterSyncUnderLock(projectDir, profile, oldTeam, t, resolveAMQEnvForTeamProfile); err != nil {
				return err
			}
			return nil
		})
	}
	if err := mutateRosterWithProfileCAS(projectDir, profile, mutation); err != nil {
		return err
	}

	if *jsonOut {
		return printJSONEnvelope("team_member_add", mutationResult{
			Command: "team member add",
			Status:  "created",
			Project: projectDir,
			Session: added.Session,
			Profile: profile,
			Role:    added.Role,
			Handle:  added.Handle,
			Actions: []mutationAction{
				followUp("resume", "launch managed member", "amq-squad resume --project "+shellQuote(projectDir)+" --profile "+shellQuote(profile)+" --exec --target new-window"),
				followUp("agent_up", "launch unmanaged member", agentUpHint(added)),
			},
		})
	}
	fmt.Printf("added %s (%s) to the team.\n", added.Role, added.Binary)
	// Steer the launch into a managed tmux pane: only then can amq-squad
	// focus/send/close the agent (the pane-lifecycle work). A bare `agent up`
	// TTY-execs with no managed pane, which is fine for a one-off but leaves an
	// orchestrator's worker unmanaged — the gap the first 2.0 dogfood hit.
	fmt.Printf("launch it in a managed tmux pane (run from inside tmux so amq-squad can focus/send/close it):\n")
	fmt.Printf("  amq-squad resume --exec --target new-window\n")
	fmt.Printf("  (brings up newly-added members in their own window and skips any already live)\n")
	fmt.Printf("or run it directly in this terminal, without a managed pane:\n  %s\n", agentUpHint(added))
	if *launchFlag {
		fmt.Printf("launching pending members with:\n  %s\n", teamMemberLaunchCommand(projectDir, profile, added.Session, *targetFlag, *skipLeadCheckFlag))
		if err := teamMemberLaunch(teamMemberLaunchArgs(projectDir, profile, added.Session, *targetFlag, *skipLeadCheckFlag)); err != nil {
			return fmt.Errorf("launch after add: %w", err)
		}
	}
	return nil
}

func inferRuntimeSpawn(t team.Team, originFlag string, depthFlag int) (string, int, error) {
	origin := strings.TrimSpace(originFlag)
	if origin == "" {
		origin = strings.TrimSpace(os.Getenv("AM_ME"))
	}
	if origin == "" {
		origin = "operator"
	}
	depth := depthFlag
	caller, callerIsMember := findMemberByOrigin(t, origin)
	if depth < 0 {
		if callerIsMember {
			depth = caller.SpawnDepth + 1
		} else {
			depth = 0
		}
	}
	if t.Orchestrated && callerIsMember && !memberIsLead(t, caller) {
		return "", 0, fmt.Errorf("spawn guard: member %q is not the orchestration lead; child-spawns-child is disabled", origin)
	}
	if depth > team.EffectiveMaxSpawnDepth(t) {
		return "", 0, fmt.Errorf("spawn guard: depth %d exceeds max_spawn_depth %d", depth, team.EffectiveMaxSpawnDepth(t))
	}
	return origin, depth, nil
}

func findMemberByOrigin(t team.Team, origin string) (team.Member, bool) {
	for _, m := range t.Members {
		if origin == m.Role || origin == memberHandle(m) {
			return m, true
		}
	}
	return team.Member{}, false
}

func memberIsLead(t team.Team, m team.Member) bool {
	return t.Orchestrated && strings.EqualFold(m.Role, t.Lead)
}

// runTeamMemberUpdate changes an existing member in place: only the flags
// passed are applied, everything else on the member is left untouched. This
// is #451's fix for the "cannot modify the lead" corner (`rm` refuses to
// remove the orchestration lead, and `add` cannot re-add an existing role),
// and the general remove-then-add friction for session/model/args changes.
func runTeamMemberUpdate(args []string) error {
	if wantsHelp(args) {
		fmt.Fprint(os.Stderr, teamMemberUsageText)
		return nil
	}
	role, rest, ok := peelPositional(args)
	if !ok {
		return usageErrorf("a role is required, e.g. 'team member update qa --model sonnet'")
	}
	role = strings.ToLower(strings.TrimSpace(role))
	fs := flag.NewFlagSet("team member update", flag.ContinueOnError)
	binaryFlag := fs.String("binary", "", "new agent CLI for this member: claude or codex")
	handleFlag := fs.String("handle", "", "new AMQ handle for this member")
	sessionFlag := fs.String("session", "", "new AMQ workstream session pin for this member")
	noSessionPinFlag := fs.Bool("no-session-pin", false, "clear this member's session pin instead of setting one")
	modelFlag := fs.String("model", "", "new native model name")
	effortFlag := fs.String("effort", "", "new native effort tier for this member; automatic clears the effort arg")
	claudeArgsRaw := fs.String("claude-args", "", "replace this member's extra Claude args (claude members only)")
	codexArgsRaw := fs.String("codex-args", "", "replace this member's extra Codex args (codex members only)")
	actorModeFlag := fs.String("actor-mode", "", "new actor execution capability: review or implementation")
	// #538: the post-creation path for worktree_isolation. `--cwd ""` clears the
	// override and returns the member to the team-home, so the flag can undo
	// itself rather than being a one-way door.
	cwdFlag := fs.String("cwd", "", "new working directory (isolated worktree) for this member; empty string clears the override")
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to mutate (default: default profile)")
	dryRunFlag := fs.Bool("dry-run", false, "preview the update without mutating")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned mutation result envelope")
	if err := parseFlags(fs, rest); err != nil {
		return err
	}
	if *noSessionPinFlag && flagWasSet(fs, "session") {
		return usageErrorf("use either --session or --no-session-pin, not both")
	}
	changing := []string{"binary", "handle", "session", "no-session-pin", "model", "effort", "claude-args", "codex-args", "actor-mode", "cwd"}
	changed := false
	for _, name := range changing {
		if flagWasSet(fs, name) {
			changed = true
			break
		}
	}
	if !changed {
		return usageErrorf("no changes given; pass at least one of --binary, --handle, --session, --no-session-pin, --model, --effort, --claude-args, --codex-args, --actor-mode")
	}
	if flagWasSet(fs, "binary") {
		binary := normalizedAgentBinary(*binaryFlag)
		if binary != "claude" && binary != "codex" {
			return usageErrorf("--binary must be claude or codex (got %q)", *binaryFlag)
		}
	}
	if flagWasSet(fs, "actor-mode") {
		mode := strings.ToLower(strings.TrimSpace(*actorModeFlag))
		if mode != team.ActorModeReview && mode != team.ActorModeImplementation {
			return usageErrorf("--actor-mode must be %s or %s", team.ActorModeReview, team.ActorModeImplementation)
		}
	}

	projectDir, profile, err := resolveExistingTeamProfile(*projectFlag, *profileFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	agentCatalog := loadAgentCatalogAndWarn(projectDir)

	buildUpdated := func(t team.Team) (team.Member, team.Team, error) {
		idx := -1
		for i, m := range t.Members {
			if m.Role == role {
				idx = i
				break
			}
		}
		if idx == -1 {
			return team.Member{}, team.Team{}, fmt.Errorf("role %q is not a team member", role)
		}
		m := t.Members[idx]
		if flagWasSet(fs, "binary") {
			binary := normalizedAgentBinary(*binaryFlag)
			if binary != m.Binary {
				m.Binary = binary
				if binary == "claude" {
					m.CodexArgs = nil
				} else {
					m.ClaudeArgs = nil
				}
			}
		}
		if flagWasSet(fs, "handle") {
			handle := strings.ToLower(strings.TrimSpace(*handleFlag))
			if handle == "" {
				return team.Member{}, team.Team{}, usageErrorf("--handle cannot be empty")
			}
			for i, other := range t.Members {
				if i != idx && other.Handle == handle {
					return team.Member{}, team.Team{}, fmt.Errorf("handle %q is already in use; pass a distinct --handle", handle)
				}
			}
			m.Handle = handle
		}
		if *noSessionPinFlag {
			m.Session = ""
		} else if flagWasSet(fs, "session") {
			m.Session = strings.ToLower(strings.TrimSpace(*sessionFlag))
		}
		if flagWasSet(fs, "model") {
			m.Model = strings.TrimSpace(*modelFlag)
		}
		if flagWasSet(fs, "claude-args") {
			if m.Binary != "claude" {
				return team.Member{}, team.Team{}, usageErrorf("--claude-args applies only to claude members (role %q is %s)", role, m.Binary)
			}
			claudeArgs, err := parseAgentArgs(*claudeArgsRaw)
			if err != nil {
				return team.Member{}, team.Team{}, fmt.Errorf("parse --claude-args: %w", err)
			}
			m.ClaudeArgs = claudeArgs
		}
		if flagWasSet(fs, "codex-args") {
			if m.Binary != "codex" {
				return team.Member{}, team.Team{}, usageErrorf("--codex-args applies only to codex members (role %q is %s)", role, m.Binary)
			}
			codexArgs, err := parseAgentArgs(*codexArgsRaw)
			if err != nil {
				return team.Member{}, team.Team{}, fmt.Errorf("parse --codex-args: %w", err)
			}
			m.CodexArgs = codexArgs
		}
		if flagWasSet(fs, "actor-mode") {
			m.ActorMode = strings.ToLower(strings.TrimSpace(*actorModeFlag))
		}
		if flagWasSet(fs, "cwd") {
			resolved, cwdErr := memberCWDOverride(t.Project, *cwdFlag)
			if cwdErr != nil {
				return team.Member{}, team.Team{}, cwdErr
			}
			m.CWD = resolved
		}
		if flagWasSet(fs, "effort") {
			if m.Binary == "claude" {
				m.ClaudeArgs = stripNativeEffortArgs(m.ClaudeArgs, m.Binary)
			} else {
				m.CodexArgs = stripNativeEffortArgs(m.CodexArgs, m.Binary)
			}
			if err := applyMemberEffortCatalog(&m, *effortFlag, agentCatalog); err != nil {
				return team.Member{}, team.Team{}, err
			}
		}
		t.Members[idx] = m
		return m, t, nil
	}

	if *dryRunFlag {
		t, err := team.ReadProfile(projectDir, profile)
		if err != nil {
			return fmt.Errorf("read team: %w", err)
		}
		// Capture the member as it stands BEFORE buildUpdated runs. buildUpdated
		// mutates t.Members[idx] in place, so reading the current member
		// afterwards would compare the proposed member against itself and report
		// an empty diff for every edit (#616).
		current, ok := teamMemberByRole(t, role)
		if !ok {
			return fmt.Errorf("role %q is not a team member", role)
		}
		updated, _, err := buildUpdated(t)
		if err != nil {
			return err
		}
		changes := memberFieldChanges(current, updated)
		if *jsonOut {
			return printJSONEnvelope("team_member_update", mutationResult{
				Command: "team member update", Status: "preview", Project: projectDir,
				Session: updated.Session, Profile: profile, Role: updated.Role, Handle: updated.Handle,
				Changes: changes,
			})
		}
		writeMemberUpdatePreview(os.Stdout, updated.Role, profile, changes)
		return nil
	}

	currentTeam, err := team.ReadProfile(projectDir, profile)
	if err != nil {
		return fmt.Errorf("read team: %w", err)
	}
	currentMember, ok := teamMemberByRole(currentTeam, role)
	if !ok {
		return fmt.Errorf("role %q is not a team member", role)
	}
	predicted, _, err := buildUpdated(currentTeam)
	if err != nil {
		return err
	}
	preparedSession := currentMember.Session
	if predicted.Session != currentMember.Session {
		preparedSession = ""
	}

	var updated team.Member
	mutation := func(expectedProfileDigest string) error {
		return withProfileLock(projectDir, profile, func() error {
			if err := verifyAcceptedProfileDigestBeforeRosterMutation(team.ProfilePath(projectDir, profile), expectedProfileDigest); err != nil {
				return err
			}
			t, err := team.ReadProfile(projectDir, profile)
			if err != nil {
				return fmt.Errorf("read team: %w", err)
			}
			oldTeam := t
			oldTeam.Members = append([]team.Member(nil), t.Members...)
			if preparedSession != "" {
				current, exists := teamMemberByRole(t, role)
				if !exists || current.Session != preparedSession {
					return fmt.Errorf("team profile changed concurrently while updating %q; retry", role)
				}
			}
			var newTeam team.Team
			updated, newTeam, err = buildUpdated(t)
			if err != nil {
				return err
			}
			if preparedSession != "" && updated.Session != preparedSession {
				return fmt.Errorf("team profile changed concurrently while updating %q; retry", role)
			}
			// WriteProfileUnderLock re-validates the whole team (orchestration,
			// per-member binary-match, duplicate handles) before the atomic
			// rename, so an invalid update never persists.
			return writeTeamProfileWithAMQRosterSyncUnderLock(projectDir, profile, oldTeam, newTeam, resolveAMQEnvForTeamProfile)
		})
	}
	if err := mutateRosterWithProfileCAS(projectDir, profile, mutation); err != nil {
		return err
	}

	if *jsonOut {
		return printJSONEnvelope("team_member_update", mutationResult{
			Command: "team member update",
			Status:  "updated",
			Project: projectDir,
			Session: updated.Session,
			Profile: profile,
			Role:    updated.Role,
			Handle:  updated.Handle,
		})
	}
	fmt.Printf("updated %s (%s) in the team.\n", updated.Role, updated.Binary)
	return nil
}

func runTeamMemberRemove(args []string) error {
	if wantsHelp(args) {
		fmt.Fprint(os.Stderr, teamMemberUsageText)
		return nil
	}
	role, rest, ok := peelPositional(args)
	if !ok {
		return usageErrorf("a role is required, e.g. 'team member add researcher --binary codex'")
	}
	role = strings.ToLower(strings.TrimSpace(role))
	fs := flag.NewFlagSet("team member rm", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to mutate (default: default profile)")
	stopFlag := fs.Bool("stop", false, "stop the member before removing it from the roster")
	forceFlag := fs.Bool("force", false, "with --stop, escalate to SIGKILL")
	closePanesFlag := fs.Bool("close-panes", false, "with --stop, close the member's tmux pane after stopping")
	dryRunFlag := fs.Bool("dry-run", false, "preview stop and roster actions without mutating")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned mutation result envelope")
	if err := parseFlags(fs, rest); err != nil {
		return err
	}
	if !*stopFlag && (flagWasSet(fs, "force") || flagWasSet(fs, "close-panes")) {
		return usageErrorf("--force and --close-panes require --stop")
	}
	projectDir, profile, err := resolveExistingTeamProfile(*projectFlag, *profileFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	t, err := team.ReadProfile(projectDir, profile)
	if err != nil {
		return fmt.Errorf("read team: %w", err)
	}
	if t.Orchestrated && t.Lead == role {
		return fmt.Errorf("role %q is the orchestration lead; reassign the lead before removing it", role)
	}
	removedMember, ok := teamMemberByRole(t, role)
	if !ok {
		// rm is idempotent (#689): a retry after a partially-failed removal
		// must converge with an unambiguous roster answer, not an error that
		// reads like the removal may have failed.
		if *jsonOut {
			return printJSONEnvelope("team_member_rm", mutationResult{
				Command: "team member rm",
				Status:  "already_absent",
				Project: projectDir,
				Profile: profile,
				Role:    role,
			})
		}
		fmt.Printf("role %q is not a team member; roster unchanged (already removed or never added).\n", role)
		return nil
	}
	if *dryRunFlag {
		if *stopFlag {
			fmt.Printf("# preview: would stop with:\n  %s\n", teamMemberStopCommand(projectDir, profile, role, removedMember.Session, *forceFlag, *closePanesFlag))
		}
		fmt.Printf("# preview: would remove %s from profile %s\n", role, profile)
		return nil
	}
	// A PARTIAL stop (progress made, but e.g. pane cleanups incomplete) no
	// longer aborts the removal: the operator asked for the roster mutation,
	// and bailing out here left the roster outcome ambiguous (#689). The
	// partial error is carried through and reported next to the definitive
	// roster answer instead. Hard refusals (namespace conflicts, identity
	// validation) still abort: they mean the stop request itself was unsafe,
	// so the roster must not mutate on top of that uncertainty.
	var stopErr error
	if *stopFlag {
		if err := teamMemberStop(teamMemberStopArgs(projectDir, profile, role, removedMember.Session, *forceFlag, *closePanesFlag)); err != nil {
			var partial *PartialError
			if !errors.As(err, &partial) {
				return fmt.Errorf("stop before remove: %w", err)
			}
			stopErr = err
			fmt.Fprintf(os.Stderr, "stop before remove was only partially completed: %v\nproceeding with the roster removal; its outcome is reported explicitly below.\n", err)
		}
	}

	var removed bool
	mutation := func(expectedProfileDigest string) error {
		return withProfileLock(projectDir, profile, func() error {
			if err := verifyAcceptedProfileDigestBeforeRosterMutation(team.ProfilePath(projectDir, profile), expectedProfileDigest); err != nil {
				return err
			}
			t, err := team.ReadProfile(projectDir, profile)
			if err != nil {
				return fmt.Errorf("read team: %w", err)
			}
			oldTeam := t
			oldTeam.Members = append([]team.Member(nil), t.Members...)
			// Removing the lead of an orchestrated team would leave a dangling
			// lead reference that fails validation; refuse with a clear pointer.
			if t.Orchestrated && t.Lead == role {
				return fmt.Errorf("role %q is the orchestration lead; reassign the lead before removing it", role)
			}
			kept := t.Members[:0:0]
			for _, m := range t.Members {
				if m.Role == role {
					if m.Session != removedMember.Session {
						return fmt.Errorf("team profile changed concurrently while removing %q; retry", role)
					}
					removed = true
					continue
				}
				kept = append(kept, m)
			}
			if !removed {
				return fmt.Errorf("role %q is not a team member", role)
			}
			t.Members = kept
			if err := writeTeamProfileWithAMQRosterSyncUnderLock(projectDir, profile, oldTeam, t, resolveAMQEnvForTeamProfile); err != nil {
				return err
			}
			return nil
		})
	}
	if err := mutateRosterWithProfileCAS(projectDir, profile, mutation); err != nil {
		if stopErr != nil {
			return fmt.Errorf("%w (roster NOT removed; stop before remove had also failed: %v)", err, stopErr)
		}
		return err
	}

	if *jsonOut {
		if err := printJSONEnvelope("team_member_rm", mutationResult{
			Command: "team member rm",
			Status:  "removed",
			Project: projectDir,
			Profile: profile,
			Role:    role,
			Actions: []mutationAction{
				followUp("down", "close live pane", "amq-squad down --project "+shellQuote(projectDir)+" --profile "+shellQuote(profile)+" --role "+shellQuote(role)+" --close-panes"),
			},
		}); err != nil {
			return err
		}
		if stopErr != nil {
			return &PartialError{Message: fmt.Sprintf("removed %s from the team roster, but stop/pane cleanup was incomplete: %v", role, stopErr), Cause: stopErr}
		}
		return nil
	}
	fmt.Printf("removed %s from the team.\n", role)
	if stopErr != nil {
		// The roster answer is definitive even though the stop was partial;
		// the partial exit code says "roster removed, runtime cleanup needs
		// attention", never "the removal may not have happened".
		return &PartialError{Message: fmt.Sprintf("removed %s from the team roster, but stop/pane cleanup was incomplete: %v", role, stopErr), Cause: stopErr}
	}
	// rm is roster-only; it never touches the agent's tmux pane. Point at the
	// pane-closing teardown so a pruned worker's window doesn't linger as an
	// orphan (down keeps the pane by default; --close-panes closes it).
	fmt.Printf("if it is live, stop it AND close its pane with:\n  amq-squad down --role %s --close-panes\n", role)
	return nil
}

func teamMemberLaunchArgs(projectDir, profile, session, target string, skipLeadCheck bool) []string {
	args := []string{"--exec", "--target", strings.TrimSpace(target), "--project", projectDir, "--profile", profile}
	if strings.TrimSpace(session) != "" {
		args = append(args, "--session", session)
	}
	if skipLeadCheck {
		args = append(args, "--skip-lead-check")
	}
	return args
}

func teamMemberLaunchCommand(projectDir, profile, session, target string, skipLeadCheck bool) string {
	return "amq-squad resume " + shellJoin(teamMemberLaunchArgs(projectDir, profile, session, target, skipLeadCheck))
}

func teamMemberStopArgs(projectDir, profile, role, session string, force, closePanes bool) []string {
	args := []string{"--role", role, "--project", projectDir, "--profile", profile}
	if strings.TrimSpace(session) != "" {
		args = append(args, "--session", session)
	}
	if force {
		args = append(args, "--force")
	}
	if closePanes {
		args = append(args, "--close-panes")
	}
	return args
}

func teamMemberStopCommand(projectDir, profile, role, session string, force, closePanes bool) string {
	return "amq-squad down " + shellJoin(teamMemberStopArgs(projectDir, profile, role, session, force, closePanes))
}

func shellJoin(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

// resolveExistingTeamProfile resolves the project dir + profile (reusing the
// shared resolver) and requires the profile to already exist — roster
// mutation needs a team to mutate.
func resolveExistingTeamProfile(projectFlag, profileFlag string, projectSet bool) (string, string, error) {
	projectDir, profile, err := resolveProjectProfile(projectFlag, profileFlag, projectSet)
	if err != nil {
		return "", "", err
	}
	if !team.ExistsProfile(projectDir, profile) {
		return "", "", fmt.Errorf("no team configured for profile %q. Run '%s' first.", profile, profileInitCommand(profile))
	}
	return projectDir, profile, nil
}

// withProfileLock serializes a read-modify-write of a team profile across
// concurrent amq-squad processes via an exclusive lock on a sidecar file, so
// a lead and a worker mutating the roster at once cannot lose an update.
func withProfileLock(projectDir, profile string, fn func() error) error {
	return team.WithProfileLock(projectDir, profile, fn)
}

// mutateRosterWithProfileCAS preserves concurrent profile changes made after
// the command has planned its mutation. The digest is ordinary optimistic
// concurrency control; it does not certify or authorize the roster edit.
func mutateRosterWithProfileCAS(projectDir, profile string, mutate func(expectedProfileDigest string) error) error {
	expected, err := teamProfileDigest(team.ProfilePath(projectDir, profile))
	if err != nil {
		return fmt.Errorf("capture team profile before roster mutation: %w", err)
	}
	teamMemberBeforeRosterMutation()
	return mutate(expected)
}

func verifyAcceptedProfileDigestBeforeRosterMutation(profilePath, expected string) error {
	if strings.TrimSpace(expected) == "" {
		return nil
	}
	current, err := teamProfileDigest(profilePath)
	if err != nil {
		return fmt.Errorf("verify team profile before roster mutation: %w", err)
	}
	if current != expected {
		return fmt.Errorf("team profile changed after roster mutation planning; retry the roster edit")
	}
	return nil
}

func teamProfileDigest(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return fmt.Sprintf("%x", sum), nil
}

// inheritedSession returns the workstream a new member should join: the
// session shared by existing members (so the roster stays one workstream),
// or empty when the team has no members or they disagree (resolved at launch).
func inheritedSession(t team.Team) string {
	session := ""
	for _, m := range t.Members {
		s := strings.TrimSpace(m.Session)
		if s == "" {
			continue
		}
		if session == "" {
			session = s
		} else if session != s {
			return ""
		}
	}
	return session
}

// agentUpHint builds the direct unmanaged `agent up` fallback command with the
// member's roster config (binary, role, session, model, per-member args).
func agentUpHint(m team.Member) string {
	var b strings.Builder
	fmt.Fprintf(&b, "amq-squad agent up %s --role %s", m.Binary, m.Role)
	if s := strings.TrimSpace(m.Session); s != "" {
		fmt.Fprintf(&b, " --session %s", s)
	}
	if model := strings.TrimSpace(m.Model); model != "" {
		fmt.Fprintf(&b, " --model %s", model)
	}
	fmt.Fprintf(&b, " --me %s", m.Handle)
	if len(m.ClaudeArgs) > 0 {
		fmt.Fprintf(&b, " --claude-args %q", strings.Join(m.ClaudeArgs, " "))
	}
	if len(m.CodexArgs) > 0 {
		fmt.Fprintf(&b, " --codex-args %q", strings.Join(m.CodexArgs, " "))
	}
	return b.String()
}

// memberCWDOverride is the SINGLE resolver for a member's --cwd, shared by
// roster creation (team init), `team member add` and `team member update`.
//
// #538, second review F2: an earlier version of this claimed to be shared but was
// not -- team init kept its own copy -- and it resolved a relative value with
// filepath.Abs, i.e. against the SHELL working directory. So
// `--project /repo --cwd ../wt` run from /tmp recorded /tmp/wt here and
// /repo/../wt on the create path. That is precisely the two-writers/two-origins
// defect #539 and #540 were about, reintroduced in the fix for a different bug.
// Both problems have one fix: genuinely one function, anchored to the PROJECT.
//
// A relative path means "relative to the project", never to the caller's shell.
// A value naming the team-home itself records as no override, so the member stays
// clean rather than pinning the default explicitly.
func memberCWDOverride(projectDir, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	abs := absoluteFilesystemPathIn(projectDir, raw)
	if abs == "" {
		return "", fmt.Errorf("resolve --cwd %q", raw)
	}
	if sameFilesystemPath(abs, projectDir) {
		return "", nil
	}
	return abs, nil
}
