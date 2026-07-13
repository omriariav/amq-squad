package cli

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

var (
	teamMemberLaunch = runResume
	teamMemberStop   = runStop
)

// runTeamMember dispatches `amq-squad team member <add|rm|list>`: runtime roster
// mutation. This is the durable-roster primitive the goal-first composition
// model rests on — a lead (any binary) grows or shrinks its team mid-session,
// and the change persists to team.json so resume rebuilds the team it built.
func runTeamMember(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, `amq-squad team member - add or remove a roster member at runtime

Usage:
  amq-squad team member add <role> --binary <claude|codex> [--handle H]
      [--session S] [--model M] [--effort E] [--claude-args "…"] [--codex-args "…"]
      [--spawn-origin NAME] [--spawn-depth N]
      [--project DIR] [--profile NAME] [--launch] [--target new-window] [--dry-run] [--json]
  amq-squad team member rm <role> [--project DIR] [--profile NAME]
      [--stop] [--force] [--close-panes] [--dry-run] [--json]
  amq-squad team member list [--json] [--project DIR] [--profile NAME]

Mutates the persisted team profile (team.json) atomically and under an
exclusive lock, then re-validates it (orchestration constraints included).
The new member is NOT launched; 'add' prints how to start it (a managed pane
via 'resume --exec --target new-window', or 'agent up' for an unmanaged one-off).

Examples:
  amq-squad team member add researcher --binary codex
  amq-squad team member add qa --binary claude --model sonnet
  amq-squad team member rm researcher
`)
		if len(args) == 0 {
			return usageErrorf("member requires a subcommand ('add', 'list', or 'rm')")
		}
		return nil
	}
	switch args[0] {
	case "add":
		return runTeamMemberAdd(args[1:])
	case "rm", "remove":
		return runTeamMemberRemove(args[1:])
	case "list", "ls":
		return runTeamMemberList(args[1:])
	default:
		return usageErrorf("unknown 'team member' subcommand: %q. Try 'add', 'list', or 'rm'.", args[0])
	}
}

// runTeamMemberList prints the current roster — the read companion to add/rm,
// so a lead can see the team it has built without opening team.json.
func runTeamMemberList(args []string) error {
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
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to mutate (default: default profile)")
	launchFlag := fs.Bool("launch", false, "after adding, launch pending members with resume --exec")
	targetFlag := fs.String("target", "new-window", "launch target for --launch (current-window|new-window|new-session)")
	dryRunFlag := fs.Bool("dry-run", false, "preview roster and launch actions without mutating")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned mutation result envelope")
	if err := parseFlags(fs, rest); err != nil {
		return err
	}
	if !*launchFlag && flagWasSet(fs, "target") {
		return usageErrorf("--target requires --launch")
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
			SpawnOrigin: origin,
			SpawnDepth:  depth,
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
			fmt.Printf("# preview: would launch with:\n  %s\n", teamMemberLaunchCommand(projectDir, profile, added.Session, *targetFlag))
		}
		return nil
	}
	if err := withProfileLock(projectDir, profile, func() error {
		t, err := team.ReadProfile(projectDir, profile)
		if err != nil {
			return fmt.Errorf("read team: %w", err)
		}
		added, err = buildAdded(t)
		if err != nil {
			return err
		}
		t.Members = append(t.Members, added)
		// WriteProfileUnderLock re-validates the whole team (orchestration, per-member
		// binary-match, duplicate handles) before the atomic rename, so an
		// invalid add never persists.
		if err := team.WriteProfileUnderLock(projectDir, profile, t); err != nil {
			return err
		}
		return nil
	}); err != nil {
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
		fmt.Printf("launching pending members with:\n  %s\n", teamMemberLaunchCommand(projectDir, profile, added.Session, *targetFlag))
		if err := teamMemberLaunch(teamMemberLaunchArgs(projectDir, profile, added.Session, *targetFlag)); err != nil {
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

func runTeamMemberRemove(args []string) error {
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
		return fmt.Errorf("role %q is not a team member", role)
	}
	if *dryRunFlag {
		if *stopFlag {
			fmt.Printf("# preview: would stop with:\n  %s\n", teamMemberStopCommand(projectDir, profile, role, removedMember.Session, *forceFlag, *closePanesFlag))
		}
		fmt.Printf("# preview: would remove %s from profile %s\n", role, profile)
		return nil
	}
	if *stopFlag {
		if err := teamMemberStop(teamMemberStopArgs(projectDir, profile, role, removedMember.Session, *forceFlag, *closePanesFlag)); err != nil {
			return fmt.Errorf("stop before remove: %w", err)
		}
	}

	var removed bool
	if err := withProfileLock(projectDir, profile, func() error {
		t, err := team.ReadProfile(projectDir, profile)
		if err != nil {
			return fmt.Errorf("read team: %w", err)
		}
		// Removing the lead of an orchestrated team would leave a dangling
		// lead reference that fails validation; refuse with a clear pointer.
		if t.Orchestrated && t.Lead == role {
			return fmt.Errorf("role %q is the orchestration lead; reassign the lead before removing it", role)
		}
		kept := t.Members[:0:0]
		for _, m := range t.Members {
			if m.Role == role {
				removed = true
				continue
			}
			kept = append(kept, m)
		}
		if !removed {
			return fmt.Errorf("role %q is not a team member", role)
		}
		t.Members = kept
		if err := team.WriteProfileUnderLock(projectDir, profile, t); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}

	if *jsonOut {
		return printJSONEnvelope("team_member_rm", mutationResult{
			Command: "team member rm",
			Status:  "removed",
			Project: projectDir,
			Profile: profile,
			Role:    role,
			Actions: []mutationAction{
				followUp("stop", "close live pane", "amq-squad stop --project "+shellQuote(projectDir)+" --profile "+shellQuote(profile)+" --role "+shellQuote(role)+" --close-panes"),
			},
		})
	}
	fmt.Printf("removed %s from the team.\n", role)
	// rm is roster-only; it never touches the agent's tmux pane. Point at the
	// pane-closing teardown so a pruned worker's window doesn't linger as an
	// orphan (stop keeps the pane by default; --close-panes closes it).
	fmt.Printf("if it is live, stop it AND close its pane with:\n  amq-squad stop --role %s --close-panes\n", role)
	return nil
}

func teamMemberLaunchArgs(projectDir, profile, session, target string) []string {
	args := []string{"--exec", "--target", strings.TrimSpace(target), "--project", projectDir, "--profile", profile}
	if strings.TrimSpace(session) != "" {
		args = append(args, "--session", session)
	}
	return args
}

func teamMemberLaunchCommand(projectDir, profile, session, target string) string {
	return "amq-squad resume " + shellJoin(teamMemberLaunchArgs(projectDir, profile, session, target))
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
	return "amq-squad stop " + shellJoin(teamMemberStopArgs(projectDir, profile, role, session, force, closePanes))
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
