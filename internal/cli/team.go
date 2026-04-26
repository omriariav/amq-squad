package cli

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
	case "launch":
		return runTeamLaunch(args[1:])
	case "rules":
		return runTeamRules(args[1:])
	case "sync":
		return runTeamSync(args[1:])
	default:
		// Unknown subcommand. Treat as flags to the smart default so
		// `amq-squad team --help` and similar still work.
		return usageErrorf("unknown 'team' subcommand: %q. Try 'init', 'show', 'launch', 'rules', or 'sync'.", args[0])
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
		return emitTeamCommands(cwd, false)
	}
	fmt.Fprintln(os.Stderr, "No team configured for this project yet. Let's set one up.")
	fmt.Fprintln(os.Stderr)
	if err := runTeamInit(nil); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr)
	return emitTeamCommands(cwd, false)
}

func runTeamInit(args []string) error {
	fs := flag.NewFlagSet("team init", flag.ContinueOnError)
	personasFlag := fs.String("personas", "", "comma-separated persona IDs to include (alias for --roles)")
	rolesFlag := fs.String("roles", "", "comma-separated role/persona IDs to include (skips interactive prompt)")
	binaryFlag := fs.String("binary", "", "per-persona CLI overrides, e.g. fullstack=codex,qa=claude")
	sessionFlag := fs.String("session", "", "per-persona session overrides, e.g. cpo=stream1,cto=stream2")
	conversationFlag := fs.String("conversation", "", "per-persona conversation refs, e.g. cto=thread-name,qa=session-uuid")
	cwdFlag := fs.String("cwd", "", "per-persona working directory overrides, e.g. qa=/path/to/sibling-project")
	force := fs.Bool("force", false, "overwrite an existing team.json")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad team init - set up this project's agent team

Usage:
  amq-squad team init [--personas id1,id2,...] [--binary persona=cli,...] [--session role=name,...] [--conversation role=ref,...] [--force]
  amq-squad team init [--roles id1,id2,...] [--binary role=cli,...] [--session role=name,...] [--conversation role=ref,...] [--force]

Without --personas or --roles, prompts interactively: first choose personas,
then choose the CLI for each persona. Writes <cwd>/.amq-squad/team.json and
seeds <cwd>/.amq-squad/team-rules.md if it does not already exist. The
directory where this runs becomes the team-home. Members can live in other
directories via --cwd role=/path.

Known personas:
`)
		for _, r := range catalog.All() {
			fmt.Fprintf(os.Stderr, "  %-10s %s (default CLI: %s)\n", r.ID, r.Label, r.PreferredBinary)
		}
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *rolesFlag != "" && *personasFlag != "" {
		return fmt.Errorf("use either --personas or --roles, not both")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	if team.Exists(cwd) && !*force {
		return fmt.Errorf("team.json already exists at %s. Use --force to overwrite.", team.Path(cwd))
	}

	binaryOverrides, err := parseKV(*binaryFlag)
	if err != nil {
		return fmt.Errorf("parse --binary: %w", err)
	}
	sessionOverrides, err := parseKV(*sessionFlag)
	if err != nil {
		return fmt.Errorf("parse --session: %w", err)
	}
	conversationOverrides, err := parseKV(*conversationFlag)
	if err != nil {
		return fmt.Errorf("parse --conversation: %w", err)
	}
	cwdOverrides, err := parseKV(*cwdFlag)
	if err != nil {
		return fmt.Errorf("parse --cwd: %w", err)
	}

	var picked []string
	interactive := *rolesFlag == "" && *personasFlag == ""
	if *personasFlag != "" {
		picked = splitCSV(*personasFlag)
	} else if *rolesFlag != "" {
		picked = splitCSV(*rolesFlag)
	} else {
		reader := bufio.NewReader(os.Stdin)
		picked, err = promptPersonaSelection(reader, os.Stderr)
		if err != nil {
			return err
		}
		if err := promptBinarySelection(reader, os.Stderr, picked, binaryOverrides); err != nil {
			return err
		}
	}
	if len(picked) == 0 {
		return fmt.Errorf("no personas selected, aborting")
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
			return fmt.Errorf("unknown persona %q. Known personas: %s", id, strings.Join(catalog.IDs(), ", "))
		}
		binary := r.PreferredBinary
		if b, ok := binaryOverrides[id]; ok {
			binary = b
		}
		if interactive && binary == "" {
			binary = r.PreferredBinary
		}
		session := id
		if s, ok := sessionOverrides[id]; ok {
			session = s
		}
		conversation := ""
		if c, ok := conversationOverrides[id]; ok {
			conversation = c
		}
		m := team.Member{
			Role:         id,
			Binary:       binary,
			Handle:       id,
			Session:      session,
			Conversation: conversation,
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
	wroteRules, err := rules.Ensure(cwd, renderTeamRules(cwd, members))
	if err != nil {
		return fmt.Errorf("seed team-rules.md: %w", err)
	}
	if wroteRules {
		fmt.Fprintf(os.Stderr, "Wrote %s.\n", rules.Path(cwd))
	}
	return nil
}

func runTeamShow(args []string) error {
	fs := flag.NewFlagSet("team show", flag.ContinueOnError)
	noBootstrap := fs.Bool("no-bootstrap", false, "emit launch commands that skip the generated bootstrap prompt")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad team show - print the launch commands for this project's team

Usage:
  amq-squad team show [--no-bootstrap]
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
	return emitTeamCommands(cwd, *noBootstrap)
}

func emitTeamCommands(projectDir string, noBootstrap bool) error {
	t, err := team.Read(projectDir)
	if err != nil {
		return fmt.Errorf("read team: %w", err)
	}
	if len(t.Members) == 0 {
		return fmt.Errorf("team has no members")
	}

	members := orderedTeamMembers(t.Members)

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
		fmt.Println(emitTeamCommand(cwd, squadBin, t.Project, m, noBootstrap))
		fmt.Println()
	}
	return nil
}

func orderedTeamMembers(members []team.Member) []team.Member {
	idx := make(map[string]int, len(catalog.IDs()))
	for i, id := range catalog.IDs() {
		idx[id] = i
	}
	out := append([]team.Member(nil), members...)
	sort.SliceStable(out, func(i, j int) bool {
		left, lok := idx[out[i].Role]
		right, rok := idx[out[j].Role]
		if !lok && !rok {
			return out[i].Role < out[j].Role
		}
		if !lok {
			return false
		}
		if !rok {
			return true
		}
		return left < right
	})
	return out
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

func emitTeamCommand(cwd, squadBin, teamHome string, m team.Member, noBootstrap bool) string {
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
	if teamHome != "" {
		b.WriteString(" --team-home ")
		b.WriteString(shellQuote(teamHome))
	}
	if noBootstrap {
		b.WriteString(" --no-bootstrap")
	}
	if m.Conversation != "" {
		b.WriteString(" --conversation ")
		b.WriteString(shellQuote(m.Conversation))
	}
	if m.Handle != "" {
		// Always explicit: a role-named handle avoids collisions when the
		// same binary (e.g. codex) hosts multiple roles in one project.
		b.WriteString(" --me ")
		b.WriteString(shellQuote(m.Handle))
	}
	b.WriteString(" ")
	b.WriteString(shellQuote(m.Binary))
	if defaultArgs := defaultChildArgsForBinary(m.Binary); len(defaultArgs) > 0 {
		b.WriteString(" --")
		for _, arg := range defaultArgs {
			b.WriteString(" ")
			b.WriteString(shellQuote(arg))
		}
	}
	return b.String()
}

func promptPersonaSelection(reader *bufio.Reader, out io.Writer) ([]string, error) {
	fmt.Fprintln(out, "Squad market")
	printPersonaMarket(out)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Choose personas to hire:")
	fmt.Fprintln(out, "  names or numbers, comma-separated")
	fmt.Fprintln(out, "  examples: cto,junior-dev,qa | 2,8,9 | all")
	fmt.Fprintln(out)
	fmt.Fprint(out, "> ")

	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("read selection: %w", err)
	}
	return parsePersonaSelection(line)
}

func promptBinarySelection(reader *bufio.Reader, out io.Writer, personas []string, overrides map[string]string) error {
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Squad plan")
	printSquadPlan(out, personas, overrides)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "CLI overrides?")
	fmt.Fprintln(out, "  Press Enter to keep defaults.")
	fmt.Fprintln(out, "  Example: fullstack=codex,junior-dev=claude")
	fmt.Fprintln(out)
	fmt.Fprint(out, "> ")
	line, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read CLI overrides: %w", err)
	}
	overrideLine := strings.TrimSpace(line)
	if overrideLine == "" {
		return nil
	}
	parsed, err := parseKV(overrideLine)
	if err != nil {
		return fmt.Errorf("parse CLI overrides: %w", err)
	}
	for k, v := range parsed {
		overrides[strings.ToLower(k)] = v
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Updated squad plan")
	printSquadPlan(out, personas, overrides)
	return nil
}

func printPersonaMarket(out io.Writer) {
	fmt.Fprintf(out, "  %-2s %-12s %-31s %-7s %s\n", "#", "PERSONA", "PROFILE", "CLI", "FIT")
	for i, r := range catalog.All() {
		fmt.Fprintf(out, "  %-2d %-12s %-31s %-7s %s\n", i+1, r.ID, r.Label, r.PreferredBinary, r.Profile)
	}
}

func parsePersonaSelection(line string) ([]string, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, fmt.Errorf("no selection provided")
	}
	if strings.EqualFold(line, "all") {
		return catalog.IDs(), nil
	}
	all := catalog.All()
	picked := splitCSV(line)
	out := make([]string, 0, len(picked))
	for _, p := range picked {
		if n, err := strconv.Atoi(p); err == nil {
			if n < 1 || n > len(all) {
				return nil, fmt.Errorf("persona number %d is out of range", n)
			}
			out = append(out, all[n-1].ID)
			continue
		}
		out = append(out, strings.ToLower(p))
	}
	return out, nil
}

func printSquadPlan(out io.Writer, personas []string, overrides map[string]string) {
	fmt.Fprintf(out, "  %-12s %-31s %-7s %s\n", "PERSONA", "PROFILE", "CLI", "SESSION")
	for _, raw := range personas {
		id := strings.TrimSpace(strings.ToLower(raw))
		if id == "" {
			continue
		}
		r := catalog.Lookup(id)
		if r == nil {
			fmt.Fprintf(out, "  %-12s %-31s %-7s %s\n", id, "(unknown)", "", id)
			continue
		}
		binary := r.PreferredBinary
		if b, ok := overrides[id]; ok {
			binary = b
		}
		fmt.Fprintf(out, "  %-12s %-31s %-7s %s\n", id, r.Label, binary, id)
	}
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
  amq-squad team rules init [--force]   Seed or refresh team-rules.md
`)
		if len(args) == 0 {
			return usageErrorf("rules requires a subcommand (e.g. 'init')")
		}
		return nil
	}
	switch args[0] {
	case "init":
		fs := flag.NewFlagSet("team rules init", flag.ContinueOnError)
		force := fs.Bool("force", false, "overwrite an existing team-rules.md with the generated template")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getwd: %w", err)
		}
		content := rules.StubContent
		if t, err := team.Read(cwd); err == nil {
			content = renderTeamRules(t.Project, t.Members)
		}
		if *force {
			if err := rules.Write(cwd, content); err != nil {
				return fmt.Errorf("write team-rules.md: %w", err)
			}
			fmt.Fprintf(os.Stderr, "Wrote %s\n", rules.Path(cwd))
			return nil
		}
		wrote, err := rules.Ensure(cwd, content)
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
  amq-squad team init [options]       Pick personas, choose CLIs, and seed rules
  amq-squad team show [--no-bootstrap]
                                      Print launch commands for configured team
  amq-squad team launch [options]     Open the configured team in a terminal
  amq-squad team rules init [--force] Seed or refresh team-rules.md
  amq-squad team sync [--apply]       Sync CLAUDE.md and AGENTS.md from team-rules.md
                                      (default: preview; --apply writes)

Personas come from the built-in catalog. Run 'amq-squad team init --help' to
see them and the available flags. Use --binary fullstack=codex to run a
persona with a different CLI.
`)
}
