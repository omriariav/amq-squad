package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

// runNew is the operator-facing creation group. It is intentionally a thin
// layer over the established primitives: `new team` writes the default team
// profile, `new profile` writes a named profile via team init, and `new
// session` starts fresh work via up.
func runNew(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(os.Stderr, newUsage)
		if len(args) == 0 {
			return usageErrorf("new requires a subcommand: 'team', 'profile', or 'session'")
		}
		return nil
	}

	switch args[0] {
	case "team":
		// gh#762: `new team` becomes a deprecation redirect. Notice lives
		// here at the explicit-dispatch site, not inside runNewTeam, since
		// `new profile` also calls runNewTeam internally.
		if !wantsHelp(args[1:]) {
			quietNotice("amq-squad new team is deprecated; use amq-squad init instead.\n")
		}
		return runNewTeam(args[1:])
	case "profile":
		if !wantsHelp(args[1:]) {
			quietNotice("amq-squad new profile is deprecated; use amq-squad init --profile NAME instead.\n")
		}
		return runNewProfile(args[1:])
	case "session":
		// gh#762 task/t12 ruling 2: `new session` redirects to plan+start NOW,
		// not to `brief` -- brief (t13/gh#759) does not exist yet and depends
		// on t12. Re-point this notice at `brief` once t13 lands (cto is
		// posting the acceptance note on task/t13 for that re-point).
		if !wantsHelp(args[1:]) {
			quietNotice("amq-squad new session is deprecated; use amq-squad plan and amq-squad start instead.\n")
		}
		return runNewSession(args[1:])
	default:
		return unknownSubcommandError("new", args[0], "team", "profile", "session")
	}
}

const newUsage = `amq-squad new - create teams, profiles, and workstream sessions

Usage:
  amq-squad new team [--project DIR] [--sync] [--dry-run [--json]] [team init options]
  amq-squad new profile NAME [--project DIR] [--sync] [--dry-run [--json]] [team init options]
  amq-squad new session [--project DIR] [--profile NAME] [<session>] [--goal TEXT | up options]

new team is the create-focused alias for ` + "`team init`" + ` for the default
profile. new profile NAME is the create-focused alias for
` + "`team init --profile NAME`" + `.
Pass --sync to immediately write the managed CLAUDE.md / AGENTS.md pointer
stubs after the team profile and team-rules.md are created.
new session is the create-focused alias for ` + "`up`" + ` and keeps the same
NEW-work safety rule: it refuses a session that already exists.
It supports up's launch options, including --profile and --seed-from for
authoring the workstream brief before launch.
With --goal, the configured drafter turns the one-line goal into a validated
brief, prints the proposed brief before the default-No launch confirmation,
and writes it only after approval. Without an external backend, it prints the
filled prompt and stops before mutation.
--project scopes creation to a team-home without requiring a prior cd.

Examples:
  amq-squad roles
  amq-squad new team --dry-run --roles cto,qa
  amq-squad new team --sync --dry-run --json --roles cto,qa
  amq-squad new team --sync --roles cto,fullstack --binary cto=codex
  amq-squad new team --roles 2,9
  amq-squad new team --roles all
  amq-squad new profile review --project ~/Code/app --roles cto
  amq-squad new profile review --roles cto,qa --sync
  amq-squad new session issue-96
  amq-squad new session issue-96 --goal "Ship the reviewed change"
  amq-squad new session issue-98 --seed-from issue:31
  amq-squad new session --project ~/Code/app issue-97
`

func runNewTeam(args []string) error {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Fprint(os.Stderr, `amq-squad new team - create a team profile

Usage:
  amq-squad new team [--project DIR] [--sync] [--dry-run [--json]] [team init options]

This delegates to 'amq-squad team init' for the default profile, including
interactive persona selection when --roles/--personas is omitted. For named
profiles, prefer 'amq-squad new profile NAME'. --roles and --personas accept
IDs, 1-based market numbers, or all. With --project, the team profile is written
in DIR without changing your shell.

Pass --sync to run 'amq-squad team sync --apply' after the profile is written.
For named profiles, the same --profile is passed through to team sync. If member
cwds are outside the team-home, pass --allow-outside with --sync.
Pass --dry-run to preview the profile and rules paths without writing files.
Add --json to emit a team_profile_plan envelope on stdout.
Operator gates default to virtual non-runnable handle 'user'. Pass
--operator HANDLE to customize it or --no-operator to opt out.
Pass --orchestrated [--lead ROLE] to wire the squad for lead-agent
orchestration (records the lead in team.json + injects the reporting norm into
team-rules.md). Default off; the lead must be a team member, never the operator.

Examples:
  amq-squad roles
  amq-squad new team --dry-run --roles cto,qa
  amq-squad new team --roles cto,qa --operator operator
  amq-squad new team --roles cto,qa --no-operator
  amq-squad new team --roles cto,fullstack,qa --orchestrated --lead cto
  amq-squad new team --sync --dry-run --json --roles cto,qa
  amq-squad new team --sync --roles cto,fullstack --binary cto=codex
  amq-squad new team --roles 2,9
  amq-squad new team --roles all
  amq-squad new team --project ~/Code/app --roles cto,qa
  amq-squad new profile review --roles cto --session review
`)
		return nil
	}
	project, rest, err := peelNewProjectFlag(args)
	if err != nil {
		return err
	}
	sync, allowOutside, rest, err := peelNewTeamSyncFlags(rest)
	if err != nil {
		return err
	}
	if allowOutside && !sync {
		return usageErrorf("--allow-outside only applies with --sync")
	}
	dryRun, err := newTeamDryRunFromArgs(rest)
	if err != nil {
		return err
	}
	return runInProject(project, func() error {
		initOpts := teamInitRunOptions{}
		if dryRun && sync {
			profile, err := newTeamProfileFromArgs(rest)
			if err != nil {
				return err
			}
			initOpts.SyncCommand = newTeamSyncCommand(project, profile, allowOutside)
		}
		if err := runTeamInitWithOptions(rest, initOpts); err != nil {
			return err
		}
		if dryRun {
			return nil
		}
		if !sync {
			return nil
		}
		profile, err := newTeamProfileFromArgs(rest)
		if err != nil {
			return err
		}
		syncArgs := []string{"--apply"}
		if profile != team.DefaultProfile {
			syncArgs = append(syncArgs, "--profile", profile)
		}
		if allowOutside {
			syncArgs = append(syncArgs, "--allow-outside")
		}
		if err := runTeamSync(syncArgs); err != nil {
			return fmt.Errorf("team created, but sync failed: %w", err)
		}
		return nil
	})
}

func newTeamDryRunFromArgs(args []string) (bool, error) {
	return newTeamBoolFlagFromArgs(args, "dry-run")
}

func newTeamBoolFlagFromArgs(args []string, name string) (bool, error) {
	dryRun := false
	long := "--" + name
	short := "-" + name
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		switch {
		case a == long || a == short:
			dryRun = true
		case strings.HasPrefix(a, long+"="):
			v, err := parseNewBoolFlag(long, strings.TrimPrefix(a, long+"="))
			if err != nil {
				return false, err
			}
			dryRun = v
		case strings.HasPrefix(a, short+"="):
			v, err := parseNewBoolFlag(long, strings.TrimPrefix(a, short+"="))
			if err != nil {
				return false, err
			}
			dryRun = v
		}
	}
	return dryRun, nil
}

func newTeamSyncCommand(project, profile string, allowOutside bool) string {
	parts := []string{"amq-squad", "team", "sync", "--apply"}
	if strings.TrimSpace(project) != "" {
		if cwd, err := os.Getwd(); err == nil {
			parts = append(parts, "--project", shellQuote(cwd))
		}
	}
	if profile != team.DefaultProfile {
		parts = append(parts, "--profile", shellQuote(profile))
	}
	if allowOutside {
		parts = append(parts, "--allow-outside")
	}
	return strings.Join(parts, " ")
}

func runNewProfile(args []string) error {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Fprint(os.Stderr, `amq-squad new profile - create a named team profile

Usage:
  amq-squad new profile NAME [--project DIR] [--sync] [--dry-run [--json]] [team init options]

This delegates to 'amq-squad team init --profile NAME'. It is the named-profile
counterpart to 'amq-squad new team', so it inherits role selection, --binary,
--model, --effort, --actor-mode (role=implementation|review execution
capability, echoed per member as actor_mode in the --dry-run --json plan),
--dry-run, --json, --project, and --sync.

Pass --no-session-pin to create an unpinned template profile instead of a
session-pinned one: members carry no session, so 'run start --profile NAME
--session <any>' can launch this roster for any workstream (a day-to-day
reusable squad). Cannot combine with --session or self-operator.

Examples:
  amq-squad roles
  amq-squad new profile review --roles cto,qa
  amq-squad new profile review --sync --roles cto,qa
  amq-squad new profile review --dry-run --json --roles 2,9
  amq-squad new profile review --roles dev,reviewer --actor-mode reviewer=review
  amq-squad new profile --project ~/Code/app review --roles cto
  amq-squad new profile pm-squad --no-session-pin --roles cto,fullstack
`)
		return nil
	}
	teamArgs, err := newProfileTeamArgs(args)
	if err != nil {
		return err
	}
	return runNewTeam(teamArgs)
}

// newProfileValueFlags lists the `team init` flags that take a SEPARATE value,
// so `new profile NAME ...` can tell a flag's value apart from the profile name
// while peeling the positional argument.
//
// #538: this list must stay complete. A value-taking flag missing from here is
// treated as valueless, its value falls through as a positional, and the operator
// gets "new profile takes exactly one profile name; got extra argument" -- an
// error that blames their input for a gap in this map. --actor-mode,
// --tool-profile and --tool-config were all missing, which is how a roster
// intended to have one reviewer and two implementers came out with three
// implementers and a worktree_isolation blocker nobody could explain.
//
// TestNewProfileForwardsEveryValueTakingTeamInitFlag enumerates the real
// `team init` flag set and fails when an entry is missing, so this cannot drift
// again. Do not hand-maintain it against memory; add the flag and let the test
// confirm.
var newProfileValueFlags = map[string]bool{
	"--actor-mode":           true,
	"--allowed-role-classes": true,
	"--allowed-roles":        true,
	"--binary":               true,
	"--budget-turns":         true,
	"--claude-args":          true,
	"--codex-args":           true,
	"--composition":          true,
	"--control-root":         true,
	"--cwd":                  true,
	"--effort":               true,
	"--idle-reap-minutes":    true,
	"--lead":                 true,
	"--lead-mode":            true,
	"--max-agents":           true,
	"--max-total-spawns":     true,
	"--mode":                 true,
	"--model":                true,
	"--operator":             true,
	"--operator-mode":        true,
	"--personas":             true,
	"--project":              true,
	"--role-file":            true,
	"--roles":                true,
	"--self-operator-allow":  true,
	"--self-operator-lead":   true,
	"--session":              true,
	"--shared-cwd-exception": true,
	"--target-contract":      true,
	"--target-project-root":  true,
	"--tool-config":          true,
	"--tool-profile":         true,
	"--trust":                true}

func newProfileTeamArgs(args []string) ([]string, error) {
	profile := ""
	out := make([]string, 0, len(args)+2)
	valueFlags := newProfileValueFlags
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			out = append(out, args[i:]...)
			break
		}
		if a == "--profile" || strings.HasPrefix(a, "--profile=") {
			return nil, usageErrorf("new profile NAME sets --profile automatically; do not pass --profile")
		}
		if strings.HasPrefix(a, "-") {
			out = append(out, a)
			name := a
			hasInlineValue := false
			if idx := strings.Index(a, "="); idx >= 0 {
				name = a[:idx]
				hasInlineValue = true
			}
			if valueFlags[name] && !hasInlineValue {
				if i+1 >= len(args) {
					return nil, usageErrorf("%s requires a value", name)
				}
				out = append(out, args[i+1])
				i++
			}
			continue
		}
		if profile != "" {
			return nil, usageErrorf("new profile takes exactly one profile name; got extra argument %q", a)
		}
		profile = strings.TrimSpace(a)
	}
	if profile == "" {
		return nil, usageErrorf("new profile requires a profile name")
	}
	if profile == team.DefaultProfile {
		return nil, usageErrorf("new profile creates named profiles; use 'new team' for the default profile")
	}
	if err := team.ValidateProfileName(profile); err != nil {
		return nil, usageErrorf("profile %q: %v", profile, err)
	}
	return append([]string{"--profile", profile}, out...), nil
}

func runNewSession(args []string) error {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Fprint(os.Stderr, `amq-squad new session - create a fresh workstream session

Usage:
  amq-squad new session [--project DIR] [--profile NAME] [<session>] [--goal TEXT | up options]

This delegates to 'amq-squad up'. It creates NEW work and refuses an existing
session; use 'amq-squad resume' to continue one or 'amq-squad up --reset' to
start one over. With --project, the session is created for that team-home
without changing your shell.
Use --profile to launch a named team profile. Use --seed-from to author the
workstream brief before launch; supported sources are file:<path>, issue:<n>,
and gh:owner/repo#<n>. With --seed-from --dry-run, only the candidate brief is
printed and nothing is written.
Use --goal for the drafter-backed goal-first path. It validates and previews
the proposed brief before the launch confirmation and writes only after
approval. --goal cannot be combined with up-only --seed-from, --dry-run,
--reset, --force, or --visibility flags.

Examples:
  amq-squad new session issue-96
  amq-squad new session issue-96 --goal "Ship the reviewed change"
  amq-squad new session --project ~/Code/app issue-97
  amq-squad new session --profile review issue-98
  amq-squad new session issue-98 --seed-from issue:31
  amq-squad new session --dry-run --seed-from file:./brief.md issue-98
  amq-squad new session --dry-run --no-bootstrap issue-96
`)
		return nil
	}
	project, rest, err := peelNewProjectFlag(args)
	if err != nil {
		return err
	}
	goal, goalMode, rest, err := peelNewSessionGoal(rest)
	if err != nil {
		return err
	}
	if goalMode {
		for _, arg := range rest {
			name := strings.SplitN(arg, "=", 2)[0]
			switch name {
			case "--seed-from", "--dry-run", "--reset", "--force", "--visibility", "--json":
				return usageErrorf("new session --goal cannot be combined with %s; use the goal-first confirmation path or the deterministic up path", name)
			}
		}
		return runInProject(project, func() error { return runNewGoalSession(rest, goal) })
	}
	return runInProject(project, func() error { return runUp(rest) })
}

var runNewGoalStart = runStart

func runNewGoalSession(args []string, goal string) error {
	startArgs := append(append([]string(nil), args...), "--goal", goal)
	req, err := parseSimpleStartRequest(startArgs)
	if err != nil {
		return err
	}
	tm, err := team.ReadProfile(req.Project, req.Profile)
	if err != nil {
		return fmt.Errorf("read team: %w", err)
	}
	session, err := resolveTeamWorkstreamName(tm, req.Session, req.SessionExplicit)
	if err != nil {
		return err
	}
	exists, root, err := teamWorkstreamExistsOrRestorable(tm, req.Profile, session)
	if err != nil {
		return err
	}
	if exists {
		return existingSessionRefusal(session, root)
	}
	return runNewGoalStart(startArgs)
}

func peelNewSessionGoal(args []string) (string, bool, []string, error) {
	out := make([]string, 0, len(args))
	goal := ""
	found := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			out = append(out, args[i:]...)
			break
		}
		switch {
		case arg == "--goal":
			if found {
				return "", false, nil, usageErrorf("new session --goal may be passed only once")
			}
			if i+1 >= len(args) {
				return "", false, nil, usageErrorf("new session --goal requires text")
			}
			goal = strings.TrimSpace(args[i+1])
			found = true
			i++
		case strings.HasPrefix(arg, "--goal="):
			if found {
				return "", false, nil, usageErrorf("new session --goal may be passed only once")
			}
			goal = strings.TrimSpace(strings.TrimPrefix(arg, "--goal="))
			found = true
		default:
			out = append(out, arg)
		}
	}
	if found && goal == "" {
		return "", false, nil, usageErrorf("new session --goal requires text")
	}
	return goal, found, out, nil
}

func peelNewProjectFlag(args []string) (string, []string, error) {
	return peelProjectFlag(args)
}

func peelProjectFlag(args []string) (string, []string, error) {
	out := make([]string, 0, len(args))
	project := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			out = append(out, args[i:]...)
			break
		}
		if a == "--project" || a == "-p" {
			if i+1 >= len(args) {
				return "", nil, usageErrorf("--project requires a directory")
			}
			if project != "" {
				return "", nil, usageErrorf("--project may be passed only once")
			}
			project = strings.TrimSpace(args[i+1])
			if project == "" {
				return "", nil, usageErrorf("--project requires a directory")
			}
			i++
			continue
		}
		if strings.HasPrefix(a, "--project=") || strings.HasPrefix(a, "-p=") {
			if project != "" {
				return "", nil, usageErrorf("--project may be passed only once")
			}
			project = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(a, "--project="), "-p="))
			if project == "" {
				return "", nil, usageErrorf("--project requires a directory")
			}
			continue
		}
		out = append(out, a)
	}
	return project, out, nil
}

func peelNewTeamSyncFlags(args []string) (bool, bool, []string, error) {
	out := make([]string, 0, len(args))
	sync := false
	allowOutside := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			out = append(out, args[i:]...)
			break
		}
		switch {
		case a == "--sync":
			sync = true
			continue
		case strings.HasPrefix(a, "--sync="):
			v, err := parseNewBoolFlag("--sync", strings.TrimPrefix(a, "--sync="))
			if err != nil {
				return false, false, nil, err
			}
			sync = v
			continue
		case a == "--allow-outside":
			allowOutside = true
			continue
		case strings.HasPrefix(a, "--allow-outside="):
			v, err := parseNewBoolFlag("--allow-outside", strings.TrimPrefix(a, "--allow-outside="))
			if err != nil {
				return false, false, nil, err
			}
			allowOutside = v
			continue
		}
		out = append(out, a)
	}
	return sync, allowOutside, out, nil
}

func parseNewBoolFlag(name, raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "t", "true", "yes", "y", "on":
		return true, nil
	case "0", "f", "false", "no", "n", "off":
		return false, nil
	default:
		return false, usageErrorf("%s expects a boolean value", name)
	}
}

func newTeamProfileFromArgs(args []string) (string, error) {
	profile := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		if a == "--profile" {
			if i+1 >= len(args) {
				return "", usageErrorf("--profile requires a name")
			}
			profile = args[i+1]
			i++
			continue
		}
		if strings.HasPrefix(a, "--profile=") {
			profile = strings.TrimPrefix(a, "--profile=")
		}
	}
	return resolveProfileFlag(profile)
}

func runInProject(project string, fn func() error) error {
	if strings.TrimSpace(project) == "" {
		return fn()
	}
	dir, err := expandPath(project)
	if err != nil {
		return fmt.Errorf("resolve --project: %w", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("--project %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("--project %s is not a directory", dir)
	}
	old, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	if err := os.Chdir(dir); err != nil {
		return fmt.Errorf("chdir %s: %w", dir, err)
	}
	defer func() { _ = os.Chdir(old) }()
	return fn()
}

// teamInitFlagSetSnapshot holds the most recently registered `team init` flag
// set, published by runTeamInitWithOptions.
//
// TEST INTROSPECTION ONLY. Nothing in the normal execution path reads this, and
// it must not become a production API: if you need team-init flags at runtime,
// take them from the flag set you own rather than from this snapshot.
//
// #538: it exists so the `new profile` forwarding test can enumerate the real
// flags instead of a second hand-written copy. Two lists that must agree, with
// nothing enforcing it, is the defect class this milestone keeps surfacing;
// deriving the test's expectation from production registration removes it. The
// cleaner shape is to extract team init's 41 flag registrations into a struct and
// share it with `new profile` directly; that refactor was deliberately deferred
// rather than done inside a bug fix.
var teamInitFlagSetSnapshot *flag.FlagSet

func publishTeamInitFlagSet(fs *flag.FlagSet) { teamInitFlagSetSnapshot = fs }

// newTeamInitFlagSetForTest registers the real team-init flags by driving the
// command's own help path (which registers everything, then returns without
// mutating anything) and returns the resulting flag set.
func newTeamInitFlagSetForTest() (*flag.FlagSet, error) {
	teamInitFlagSetSnapshot = nil
	// --help registers every flag and then reports flag.ErrHelp, which Run
	// swallows as a successful exit. Anything else is a real failure.
	if err := runTeamInit([]string{"--help"}); err != nil && !errors.Is(err, flag.ErrHelp) {
		return nil, err
	}
	if teamInitFlagSetSnapshot == nil {
		return nil, fmt.Errorf("team init did not publish its flag set")
	}
	return teamInitFlagSetSnapshot, nil
}
