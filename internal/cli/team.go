package cli

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/internal/catalog"
	"github.com/omriariav/amq-squad/internal/rules"
	"github.com/omriariav/amq-squad/internal/team"
)

func runTeam(args []string) error {
	if len(args) == 0 {
		return runTeamSmart()
	}
	switch args[0] {
	case "-h", "--help":
		printTeamUsage()
		return nil
	case "init":
		return runTeamInit(args[1:])
	case "show":
		return runTeamShow(args[1:])
	case "rules":
		return runTeamRules(args[1:])
	case "sync":
		return runTeamSync(args[1:])
	default:
		// Unknown subcommand. Treat as flags to the smart default so
		// `amq-squad team --help` and similar still work.
		return usageErrorf("unknown 'team' subcommand: %q. Try 'init', 'show', 'rules', or 'sync'.", args[0])
	}
}

// runTeamSmart: if team.json exists, print the launch commands. If not,
// run interactive init and then print.
func runTeamSmart() error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	if team.Exists(cwd) {
		return emitTeamCommands(cwd)
	}
	fmt.Fprintln(os.Stderr, "No team configured for this project yet. Let's set one up.")
	fmt.Fprintln(os.Stderr)
	if err := runTeamInit(nil); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr)
	return emitTeamCommands(cwd)
}

func runTeamInit(args []string) error {
	fs := flag.NewFlagSet("team init", flag.ContinueOnError)
	rolesFlag := fs.String("roles", "", "comma-separated role IDs to include (skips interactive prompt)")
	binaryFlag := fs.String("binary", "", "per-role binary overrides, e.g. qa=codex,pm=codex")
	sessionFlag := fs.String("session", "", "per-role session overrides, e.g. cpo=stream1,cto=stream2")
	cwdFlag := fs.String("cwd", "", "per-role working directory overrides, e.g. qa=/path/to/sibling-project")
	force := fs.Bool("force", false, "overwrite an existing team.json")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad team init - set up this project's agent team

Usage:
  amq-squad team init [--roles id1,id2,...] [--binary role=bin,...] [--session role=name,...] [--force]

Without --roles, prompts interactively. Writes <cwd>/.amq-squad/team.json.
The directory where this runs becomes the team-home. Members can live in
other directories via --cwd role=/path.

Known roles:
`)
		for _, r := range catalog.All() {
			fmt.Fprintf(os.Stderr, "  %-10s %s (default: %s)\n", r.ID, r.Label, r.PreferredBinary)
		}
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	if team.Exists(cwd) && !*force {
		return fmt.Errorf("team.json already exists at %s. Use --force to overwrite.", team.Path(cwd))
	}

	var picked []string
	if *rolesFlag != "" {
		picked = splitCSV(*rolesFlag)
	} else {
		picked, err = promptRoleSelection()
		if err != nil {
			return err
		}
	}
	if len(picked) == 0 {
		return fmt.Errorf("no roles selected, aborting")
	}

	binaryOverrides, err := parseKV(*binaryFlag)
	if err != nil {
		return fmt.Errorf("parse --binary: %w", err)
	}
	sessionOverrides, err := parseKV(*sessionFlag)
	if err != nil {
		return fmt.Errorf("parse --session: %w", err)
	}
	cwdOverrides, err := parseKV(*cwdFlag)
	if err != nil {
		return fmt.Errorf("parse --cwd: %w", err)
	}

	members := make([]team.Member, 0, len(picked))
	seen := make(map[string]bool)
	for _, id := range picked {
		id = strings.TrimSpace(strings.ToLower(id))
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		r := catalog.Lookup(id)
		if r == nil {
			return fmt.Errorf("unknown role %q. Known roles: %s", id, strings.Join(catalog.IDs(), ", "))
		}
		binary := r.PreferredBinary
		if b, ok := binaryOverrides[id]; ok {
			binary = b
		}
		session := id
		if s, ok := sessionOverrides[id]; ok {
			session = s
		}
		m := team.Member{
			Role:    id,
			Binary:  binary,
			Handle:  id,
			Session: session,
		}
		if c, ok := cwdOverrides[id]; ok {
			abs, err := expandPath(c)
			if err != nil {
				return fmt.Errorf("resolve cwd for %s: %w", id, err)
			}
			if abs != cwd {
				m.CWD = abs
			}
		}
		members = append(members, m)
	}

	t := team.Team{
		Project: cwd,
		Members: members,
	}
	if err := team.Write(cwd, t); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Wrote %s with %d members.\n", team.Path(cwd), len(members))
	return nil
}

func runTeamShow(args []string) error {
	fs := flag.NewFlagSet("team show", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad team show - print the launch commands for this project's team

Usage:
  amq-squad team show
`)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	if !team.Exists(cwd) {
		return fmt.Errorf("no team configured. Run 'amq-squad team init' first.")
	}
	return emitTeamCommands(cwd)
}

func emitTeamCommands(projectDir string) error {
	t, err := team.Read(projectDir)
	if err != nil {
		return fmt.Errorf("read team: %w", err)
	}
	if len(t.Members) == 0 {
		return fmt.Errorf("team has no members")
	}

	// Stable display order: catalog order, not file order. Keeps output
	// consistent regardless of how the user listed roles at init.
	idx := make(map[string]int, len(catalog.IDs()))
	for i, id := range catalog.IDs() {
		idx[id] = i
	}
	members := append([]team.Member(nil), t.Members...)
	sort.SliceStable(members, func(i, j int) bool {
		return idx[members[i].Role] < idx[members[j].Role]
	})

	fmt.Println("# amq-squad team - run each command in its own terminal tab")
	fmt.Println("#")
	fmt.Printf("# team-home: %s\n", t.Project)
	fmt.Printf("# members:   %d\n", len(members))
	// List unique member cwds so a multi-project team is obvious at a glance.
	uniqueCWDs := uniqueMemberCWDs(t.Project, members)
	if len(uniqueCWDs) > 1 {
		fmt.Printf("# cwds:      %s\n", strings.Join(uniqueCWDs, ", "))
	}
	rulesPath := rules.Path(t.Project)
	if _, err := os.Stat(rulesPath); err == nil {
		fmt.Printf("# rules:     %s\n", rulesPath)
	} else {
		fmt.Printf("# rules:     (not set; run 'amq-squad team rules init')\n")
	}
	fmt.Println()
	// Use the running amq-squad's absolute path so emitted commands work
	// even when amq-squad isn't on PATH.
	squadBin := "amq-squad"
	if p, err := os.Executable(); err == nil {
		squadBin = p
	}

	for i, m := range members {
		label := m.Role
		if r := catalog.Lookup(m.Role); r != nil {
			label = r.Label
		}
		cwd := m.EffectiveCWD(t.Project)
		fmt.Printf("# %d. %s - %s (session: %s, cwd: %s)\n", i+1, label, m.Binary, m.Session, cwd)
		fmt.Println(emitTeamCommand(cwd, squadBin, m))
		fmt.Println()
	}
	return nil
}

func uniqueMemberCWDs(projectDir string, members []team.Member) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, m := range members {
		cwd := m.EffectiveCWD(projectDir)
		if seen[cwd] {
			continue
		}
		seen[cwd] = true
		out = append(out, cwd)
	}
	sort.Strings(out)
	return out
}

func emitTeamCommand(cwd, squadBin string, m team.Member) string {
	var b strings.Builder
	b.WriteString("cd ")
	b.WriteString(shellQuote(cwd))
	b.WriteString(" && ")
	b.WriteString(shellQuote(squadBin))
	b.WriteString(" launch")
	b.WriteString(" --role ")
	b.WriteString(shellQuote(m.Role))
	b.WriteString(" --session ")
	b.WriteString(shellQuote(m.Session))
	if m.Handle != "" {
		// Always explicit: a role-named handle avoids collisions when the
		// same binary (e.g. codex) hosts multiple roles in one project.
		b.WriteString(" --me ")
		b.WriteString(shellQuote(m.Handle))
	}
	b.WriteString(" ")
	b.WriteString(shellQuote(m.Binary))
	return b.String()
}

func promptRoleSelection() ([]string, error) {
	fmt.Fprintln(os.Stderr, "Available roles:")
	for _, r := range catalog.All() {
		skills := ""
		if len(r.Skills) > 0 {
			skills = "  [" + strings.Join(r.Skills, ", ") + "]"
		}
		fmt.Fprintf(os.Stderr, "  %-10s %s (%s)%s\n", r.ID, r.Label, r.PreferredBinary, skills)
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprint(os.Stderr, "Which roles do you want on this team? (comma-separated IDs, or 'all'): ")

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("read selection: %w", err)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, fmt.Errorf("no selection provided")
	}
	if strings.EqualFold(line, "all") {
		return catalog.IDs(), nil
	}
	return splitCSV(line), nil
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// expandPath resolves a user-supplied path: expands a leading "~" or "~/"
// to the user's home directory, then makes the result absolute.
func expandPath(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("home dir: %w", err)
		}
		if p == "~" {
			p = home
		} else {
			p = filepath.Join(home, p[2:])
		}
	}
	return filepath.Abs(p)
}

func parseKV(s string) (map[string]string, error) {
	out := map[string]string{}
	if s == "" {
		return out, nil
	}
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		eq := strings.IndexByte(pair, '=')
		if eq <= 0 || eq == len(pair)-1 {
			return nil, fmt.Errorf("expected key=value, got %q", pair)
		}
		k := strings.TrimSpace(pair[:eq])
		v := strings.TrimSpace(pair[eq+1:])
		out[k] = v
	}
	return out, nil
}

func runTeamRules(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, `amq-squad team rules - manage .amq-squad/team-rules.md

Usage:
  amq-squad team rules init   Seed team-rules.md with a stub (won't clobber)
`)
		if len(args) == 0 {
			return usageErrorf("rules requires a subcommand (e.g. 'init')")
		}
		return nil
	}
	switch args[0] {
	case "init":
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getwd: %w", err)
		}
		wrote, err := rules.EnsureStub(cwd)
		if err != nil {
			return fmt.Errorf("seed team-rules.md: %w", err)
		}
		if wrote {
			fmt.Fprintf(os.Stderr, "Wrote %s\n", rules.Path(cwd))
		} else {
			fmt.Fprintf(os.Stderr, "%s already exists, leaving it alone.\n", rules.Path(cwd))
		}
		return nil
	default:
		return usageErrorf("unknown 'rules' subcommand: %q", args[0])
	}
}

func runTeamSync(args []string) error {
	fs := flag.NewFlagSet("team sync", flag.ContinueOnError)
	apply := fs.Bool("apply", false, "write the planned changes (default: preview only)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad team sync - sync CLAUDE.md and AGENTS.md from team-rules.md

Usage:
  amq-squad team sync            Preview what would change (exit 1 if drift)
  amq-squad team sync --apply    Write the managed block into both files

Existing content in CLAUDE.md / AGENTS.md is preserved. On first run,
existing content is adopted as the user region and a managed block is
appended between markers. Subsequent runs only refresh the managed block.

When team members span multiple directories, sync walks every unique cwd
in team.json and syncs CLAUDE.md + AGENTS.md in each.
`)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	body, err := rules.Read(cwd)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no team-rules.md at %s. Run 'amq-squad team rules init' first.", rules.Path(cwd))
		}
		return err
	}

	// Walk every unique cwd the team spans so each project's CLAUDE.md and
	// AGENTS.md picks up the managed block.
	targetDirs := []string{cwd}
	if team.Exists(cwd) {
		if t, err := team.Read(cwd); err == nil {
			targetDirs = uniqueMemberCWDs(cwd, t.Members)
			if !containsString(targetDirs, cwd) {
				// Team-home cwd may not host a member, but it still owns
				// team-rules.md so its own CLAUDE.md/AGENTS.md should sync.
				targetDirs = append(targetDirs, cwd)
				sort.Strings(targetDirs)
			}
		}
	}

	var allPlans []rules.SyncPlan
	for _, dir := range targetDirs {
		plans, err := rules.Plan(dir, body)
		if err != nil {
			return fmt.Errorf("plan sync for %s: %w", dir, err)
		}
		allPlans = append(allPlans, plans...)
	}

	drift := false
	var currentDir string
	for _, p := range allPlans {
		dir := filepath.Dir(p.Target)
		if dir != currentDir {
			if currentDir != "" {
				fmt.Println()
			}
			fmt.Printf("# %s\n", dir)
			currentDir = dir
		}
		status := describePlan(p)
		fmt.Printf("  %-12s %s\n", p.Basename, status)
		if !p.Unchanged {
			drift = true
		}
	}

	if !*apply {
		if drift {
			fmt.Fprintln(os.Stderr, "\nPreview only. Re-run with --apply to write.")
			return fmt.Errorf("drift detected")
		}
		return nil
	}

	n, err := rules.Apply(allPlans)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Wrote %d file(s).\n", n)
	return nil
}

func containsString(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func describePlan(p rules.SyncPlan) string {
	switch {
	case p.Unchanged:
		return "up to date"
	case p.Creating:
		return "will create"
	case p.Adopting:
		return "will adopt existing content and add managed block"
	default:
		return "will refresh managed block"
	}
}

func printTeamUsage() {
	fmt.Print(`amq-squad team - manage this project's agent team

Usage:
  amq-squad team                      Smart default: show commands, or init if none exists
  amq-squad team init [options]       Set up .amq-squad/team.json
  amq-squad team show                 Print launch commands for configured team
  amq-squad team rules init           Seed .amq-squad/team-rules.md with a stub
  amq-squad team sync [--apply]       Sync CLAUDE.md and AGENTS.md from team-rules.md
                                      (default: preview; --apply writes)

Roles come from the built-in catalog. Run 'amq-squad team init --help' to
see them and the available flags.
`)
}
