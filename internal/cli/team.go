package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/omriariav/amq-squad/v3/internal/catalog"
	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/rules"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// The team verbs of 3.0 (brief 2b-5): the lead proposes squad.yaml and the
// files it implies as one digest (R8), the owner (manual) or the grant (auto)
// approves, and apply writes them. show and check read squad.yaml back.
var teamSubcommandHints = []string{"propose", "apply", "show", "check", "draft-rules"}

// removedTeamVerbs maps each v2 team verb to what replaces it.
var removedTeamVerbs = map[string]string{
	"init":     "team propose and team apply",
	"rules":    "team propose (renders .amq-squad/team-rules.md from squad.yaml) and team draft-rules",
	"sync":     "team propose and team apply (they plan CLAUDE.md and AGENTS.md too)",
	"operator": "the operator_interaction key of squad.yaml (team propose --operator-interaction)",
	"resume":   "nothing; there is no resume verb in 3.0",
}

func runTeam(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printTeamUsage()
		return nil
	}
	switch args[0] {
	case "propose":
		return runTeamPropose(args[1:], false)
	case "apply":
		return runTeamPropose(args[1:], true)
	case "show", "check":
		return runTeamShow(args[0], args[1:])
	case "draft-rules":
		return runTeamDraftRules(args[1:])
	}
	if repl, ok := removedTeamVerbs[args[0]]; ok {
		return usageErrorf("team %s was removed in amq-squad 3.0; use %s", args[0], repl)
	}
	return unknownSubcommandError("team", args[0], teamSubcommandHints...)
}

func printTeamUsage() {
	fmt.Fprint(os.Stderr, `amq-squad team - the squad of this repository (<repo root>/squad.yaml)

Usage:
  amq-squad team                         Print this usage
  amq-squad team propose [options]       Print one digest for every file the squad
                                         needs (squad.yaml, team-rules.md, CLAUDE.md,
                                         AGENTS.md); writes nothing
  amq-squad team apply --digest D [same options]
                                         Write those files when the digest still matches
  amq-squad team propose --from PLAN.json  The lead's plan (goal, session, lead, seats,
                                         files, tasks): squad.yaml rendered from it and
                                         checked against the owner grant, printed with the
                                         plan and its digest; the proposal is recorded for
                                         the doors, nothing else is written
  amq-squad team apply --digest D [--project DIR]
                                         Apply a recorded proposal: under the solo lead the
                                         team starts in the lead's process on a confirmation
                                         it witnessed (/squad confirm in its pane, or the
                                         signed Buzz DM "yes <prefix>"); from a shell it runs
                                         only while no lead holds the repository, writes
                                         squad.yaml and the owner starts the team (up)
  amq-squad team propose --add-seat HANDLE:ROLE:RUNTIME:MODEL:EFFORT
  amq-squad team apply --digest D --add-seat HANDLE:ROLE:RUNTIME:MODEL:EFFORT
                                         squad.yaml with one seat appended (the screen's
                                         add key); the team rules stay as they are
  amq-squad team show [--json]           Print squad.yaml and the effective grant
  amq-squad team check                   Validate squad.yaml (strict; widening is an error)
  amq-squad team draft-rules [--template T]
                                         Draft the team-rules prose fragment (stdout)

Options for propose and apply:
  --from FILE            a squad.yaml draft, or the lead's plan as a JSON object, to
                         propose (else built from the flags below)
  --roles a,b            seats, one per catalog or staged custom role (handle = role)
  --runtime r=claude     per-role runtime (claude, codex, pi; default the role's)
  --model r=m --effort r=e --worktree r=path --tools r=coding --actor-mode r=review
  --lead ROLE --lead-mode builder|planner --execution-mode MODE --session NAME
  --control-root DIR --target-project-root DIR --operator-interaction MODE
  --template auto|dev-only|product-squad|scrum|custom   team-rules template
  --rules-prose FILE     a team-rules prose fragment from team draft-rules
  --project DIR          the repository root (default: cwd)   --json   JSON output
  --add-seat H:R:RT:M:E  append one seat to the squad.yaml on disk (handle, role,
                         runtime, model, effort; role empty is the handle, model and
                         effort may be empty, a model may hold colons); refused with
                         --from or --roles. The seat starts at the next scheduler
                         relaunch, which reads squad.yaml again.

The v2 verbs team init, rules, sync and operator were removed; squad.yaml is
the only team config. See docs/squad-yaml.md.

Examples:
  amq-squad team propose --project ~/Code/app --roles cto,fullstack --runtime fullstack=codex --lead cto
  amq-squad team apply --digest sha256:<d> --project ~/Code/app --roles cto,fullstack --runtime fullstack=codex --lead cto
  amq-squad team propose --from draft-squad.yaml
  amq-squad team check
`)
}

type teamProposeFlags struct {
	project, from, roles, runtime, model, effort, worktree, tools, actorMode  *string
	lead, leadMode, executionMode, session, controlRoot, targetRoot, operator *string
	template, rulesProse, digest, addSeat                                     *string
	json                                                                      *bool
}

// perSeatFlag is a per-seat ROLE=VALUE list flag of team propose and apply.
// It takes one occurrence: a second one is refused, never silently kept in
// place of the first, because the result lands in a squad.yaml the owner
// approves by digest.
type perSeatFlag struct {
	name, val string
	set       bool
}

func (f *perSeatFlag) String() string {
	if f == nil {
		return ""
	}
	return f.val
}

func (f *perSeatFlag) Set(v string) error {
	if f.set {
		return fmt.Errorf("--%s is given more than once and would keep only the last; name every seat in one comma list: --%s %s,%s", f.name, f.name, f.val, v)
	}
	f.val, f.set = v, true
	return nil
}

func perSeatString(fs *flag.FlagSet, name string) *string {
	f := &perSeatFlag{name: name}
	fs.Var(f, name, "")
	return &f.val
}

func runTeamPropose(args []string, apply bool) error {
	name := map[bool]string{false: "team propose", true: "team apply"}[apply]
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	o := teamProposeFlags{
		project: fs.String("project", "", ""), from: fs.String("from", "", ""), roles: fs.String("roles", "", ""),
		runtime: perSeatString(fs, "runtime"), model: perSeatString(fs, "model"), effort: perSeatString(fs, "effort"),
		worktree: perSeatString(fs, "worktree"), tools: perSeatString(fs, "tools"), actorMode: perSeatString(fs, "actor-mode"),
		lead: fs.String("lead", "", ""), leadMode: fs.String("lead-mode", "", ""), executionMode: fs.String("execution-mode", "", ""),
		session: fs.String("session", "", ""), controlRoot: fs.String("control-root", "", ""), targetRoot: fs.String("target-project-root", "", ""),
		operator: fs.String("operator-interaction", "", ""), template: fs.String("template", "auto", ""),
		rulesProse: fs.String("rules-prose", "", ""), digest: fs.String("digest", "", ""), json: fs.Bool("json", false, ""),
		addSeat: fs.String("add-seat", "", ""),
	}
	fs.Usage = printTeamUsage
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("%s takes no positional arguments", name)
	}
	if apply != (*o.digest != "") {
		return usageErrorf("--digest is required by team apply and refused by team propose")
	}
	if apply && *o.addSeat == "" && *o.from == "" && *o.roles == "" {
		// the apply of a proposal the lead recorded (brief 31-3)
		return runTeamApplyConfirmed(*o.project, *o.digest, *o.json)
	}
	p, plan, err := buildTeamProposal(o)
	if err != nil {
		return err
	}
	if apply {
		write := func() error { return p.Apply(*o.digest) }
		if plan != nil {
			// R55: the apply of the lead's plan from a shell is the
			// owner's confirmation by the act, so it runs only while no lead
			// (and no model) holds the repository, held from the probe
			// through the write (the 3.0 flag form, which the owner types
			// with the digest of the files in front of them, and the screen's
			// add key, which the owner confirms at the key, stay as they
			// were)
			if err := withNoLead(p.ProjectDir, *o.digest, write); err != nil {
				return err
			}
		} else if err := write(); err != nil {
			return err
		}
	}
	if plan != nil && !apply {
		return printLeadProposal(p, *plan, *o.json)
	}
	if *o.json {
		return printJSONEnvelope(strings.ReplaceAll(name, " ", "_"), p)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, f := range p.Files {
		fmt.Fprintf(w, "%s\t%s\t%s\n", f.Action, f.Path, f.SHA256[:12])
	}
	_ = w.Flush()
	if apply {
		fmt.Printf("applied digest %s (%d seats)\n", p.Digest, p.Seats)
	} else {
		fmt.Printf("digest %s (%d seats); nothing written. Apply after approval:\n  amq-squad team apply --digest %s <same options>\n", p.Digest, p.Seats, p.Digest)
	}
	return nil
}

// teamFlagsFor is the propose and apply flags at their defaults for a
// repository (the apply of a recorded proposal plans it as propose did).
func teamFlagsFor(project string) teamProposeFlags {
	e := func() *string { s := ""; return &s }
	auto, no := "auto", false
	return teamProposeFlags{project: &project, from: e(), roles: e(), runtime: e(), model: e(), effort: e(), worktree: e(), tools: e(), actorMode: e(),
		lead: e(), leadMode: e(), executionMode: e(), session: e(), controlRoot: e(), targetRoot: e(), operator: e(),
		template: &auto, rulesProse: e(), digest: e(), addSeat: e(), json: &no}
}

// printLeadProposal is `team propose --from PLAN`'s output: the plan, the
// planned files, the digest and its 16-hex prefix, and the three doors. The
// proposal is recorded beside the confirmation records (the Buzz door looks
// its prefix up there; the apply plans its squad.yaml again from it).
func printLeadProposal(p team.Plan, lp team.LeadPlan, jsonOut bool) error {
	root, err := soloRootOf(p.ProjectDir)
	if err != nil {
		return err
	}
	lines := team.PlanLines(lp)
	prop := teamProposal{Digest: p.Digest, Project: p.ProjectDir, SquadYAML: team.RenderPlan(lp), Plan: lines, At: confirmNow().UTC()}
	if err := writeProposal(root, prop); err != nil {
		return err
	}
	prefix := team.DigestPrefix(p.Digest)
	doors := doorsText(p.Digest)
	if jsonOut {
		return printJSONEnvelope("team_propose", map[string]any{
			"project_dir": p.ProjectDir, "plan": lines, "digest": p.Digest, "prefix": prefix, "seats": p.Seats, "files": p.Files,
			"squad_yaml": prop.SquadYAML, "doors": doors, "apply": []string{"amq-squad", "team", "apply", "--digest", p.Digest, "--project", p.ProjectDir},
		})
	}
	fmt.Println("plan:")
	for _, l := range lines {
		fmt.Println("  " + termSafe(l))
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, f := range p.Files {
		fmt.Fprintf(w, "%s\t%s\t%s\n", f.Action, f.Path, f.SHA256[:12])
	}
	_ = w.Flush()
	fmt.Printf("digest %s (%d seats), prefix %s; nothing written but the proposal.\n%s.\nThen: amq-squad team apply --digest %s --project %s\n", p.Digest, p.Seats, prefix, doors, p.Digest, p.ProjectDir)
	return nil
}

// runTeamApplyConfirmed is `team apply --digest D` of a recorded proposal
// (brief 31-3, section 3). Under the solo lead (its start socket in the
// environment) the lead's process starts the team on a confirmation it
// witnessed. From a shell the apply is the owner's confirmation by the act
// (the digest is in front of the owner, who typed the apply), so it runs
// only while no lead holds the repository: with no lead there is no model
// under it (noLeadHolds); no file is read for it. squad.yaml is then written
// through the digest flow, and the owner starts the team with amq-squad up.
func runTeamApplyConfirmed(project, digest string, jsonOut bool) error {
	dir, err := teamProjectDir(project)
	if err != nil {
		return err
	}
	if _, err := digestHex(digest); err != nil {
		return err
	}
	if sock := os.Getenv(leadStartSocketEnv); sock != "" {
		return applyThroughLead(sock, dir, strings.TrimSpace(digest), jsonOut)
	}
	root, err := soloRootOf(dir)
	if err != nil {
		return err
	}
	prop, err := readProposal(root, digest)
	if err != nil {
		return err
	}
	b, err := buildTeamProposalText(teamFlagsFor(dir), &prop.SquadYAML)
	if err != nil {
		return err
	}
	if err := withNoLead(dir, strings.TrimSpace(digest), func() error { return b.plan.Apply(strings.TrimSpace(digest)) }); err != nil {
		return err
	}
	if jsonOut {
		return printJSONEnvelope("team_apply", map[string]any{"plan": b.plan, "start": []string{"amq-squad", "up", "--project", dir}})
	}
	fmt.Printf("applied digest %s (%d seats); the lead proposes, the owner starts: amq-squad up --project %s\n", b.plan.Digest, b.plan.Seats, dir)
	return nil
}

// buildTeamProposal computes every planned write and the digest over them;
// it writes nothing.
func buildTeamProposal(o teamProposeFlags) (team.Plan, *team.LeadPlan, error) {
	p, err := buildTeamProposalText(o, nil)
	return p.plan, p.lead, err
}

// teamProposalBuild is a proposal and, for a plan, the plan it came from.
type teamProposalBuild struct {
	plan team.Plan
	lead *team.LeadPlan
}

// buildTeamProposalText is buildTeamProposal; text, when not nil, is the
// squad.yaml to plan in place of the flags' (a recorded proposal's).
func buildTeamProposalText(o teamProposeFlags, given *string) (teamProposalBuild, error) {
	var out teamProposalBuild
	wrap := func(p team.Plan, err error) (teamProposalBuild, error) {
		out.plan = p
		return out, err
	}
	projectDir := strings.TrimSpace(*o.project)
	if projectDir == "" {
		projectDir = "."
	}
	projectDir, err := filepath.Abs(projectDir)
	if err != nil {
		return out, err
	}
	owner, err := grants.Load("")
	if err != nil {
		return out, err
	}
	text := ""
	keepRules := false
	if given != nil {
		text = *given
	} else if *o.addSeat != "" {
		if *o.from != "" || *o.roles != "" {
			return out, usageErrorf("--add-seat appends to the squad.yaml on disk; it is refused with --from or --roles")
		}
		if text, err = squadWithSeat(projectDir, *o.addSeat, owner.Grants); err != nil {
			return out, err
		}
		keepRules = true
	} else if *o.from != "" {
		raw, err := os.ReadFile(*o.from)
		if err != nil {
			return out, err
		}
		text = string(raw)
		if team.IsPlanText(text) {
			// the lead's plan (brief 31-3): squad.yaml rendered from it,
			// then checked exactly as a squad.yaml draft
			lp, err := team.ParsePlan(text)
			if err != nil {
				return out, fmt.Errorf("--from %s: %w", *o.from, err)
			}
			out.lead, text = &lp, team.RenderPlan(lp)
		}
	} else if text, err = squadFromFlags(projectDir, o); err != nil {
		return out, err
	}
	s, err := team.ParseSquad(text, team.SquadPath(projectDir), owner.Grants)
	if err != nil {
		return out, err
	}
	t := team.FromSquad(projectDir, s)
	for _, m := range t.Members {
		if catalog.Lookup(m.Role) == nil {
			if _, err := os.Stat(team.CustomRolePath(projectDir, m.Role)); err != nil {
				return out, fmt.Errorf("seat %s: role %q is neither a catalog role nor staged at %s (amq-squad role draft)", m.Handle, m.Role, team.CustomRolePath(projectDir, m.Role))
			}
		}
	}
	var prose *teamRulesProse
	if *o.rulesProse != "" {
		raw, err := os.ReadFile(*o.rulesProse)
		if err != nil {
			return out, err
		}
		parsed, err := validateTeamRulesDraft(string(raw), customTeamRulesRoles(t))
		if err != nil {
			return out, fmt.Errorf("--rules-prose: %w", err)
		}
		prose = &parsed
	}
	rulesText := ""
	if raw, rerr := os.ReadFile(rules.Path(projectDir)); keepRules && rerr == nil {
		// a seat added from the screen keeps the team rules the owner
		// approved (and their prose); team propose re-renders them on purpose
		rulesText = string(raw)
	} else if rulesText, err = renderTeamRulesWithTemplateDraft(t, *o.template, prose); err != nil {
		return out, err
	}
	return wrap(team.NewPlan(projectDir, text, t, rulesText))
}

// squadWithSeat is the squad.yaml on disk with one seat appended (brief
// 31-2 PR 2, the add key's seat half), rendered in canonical form
// (team.Render; comments in the file are not kept). The spec is
// HANDLE:ROLE:RUNTIME:MODEL:EFFORT: role empty is the handle, model and
// effort may be empty, and the model may itself hold colons (an Ollama
// tag), so the effort is the text after the last colon. The result is
// parsed again by the caller, strictly against the owner grant.
func squadWithSeat(projectDir, spec string, owner grants.Grants) (string, error) {
	parts := strings.SplitN(strings.TrimSpace(spec), ":", 4)
	if len(parts) != 4 || !strings.Contains(parts[3], ":") {
		return "", usageErrorf("--add-seat %q: want HANDLE:ROLE:RUNTIME:MODEL:EFFORT (model and effort may be empty, the colons stay)", spec)
	}
	i := strings.LastIndex(parts[3], ":")
	seat := team.Seat{Handle: strings.TrimSpace(parts[0]), Role: strings.TrimSpace(parts[1]), Runtime: strings.TrimSpace(parts[2]),
		Model: strings.TrimSpace(parts[3][:i]), Effort: strings.TrimSpace(parts[3][i+1:])}
	if seat.Handle == "" || seat.Runtime == "" {
		return "", usageErrorf("--add-seat %q: the handle and the runtime are required", spec)
	}
	if seat.Role == "" {
		seat.Role = seat.Handle
	}
	raw, err := os.ReadFile(team.SquadPath(projectDir))
	if err != nil {
		return "", fmt.Errorf("--add-seat appends to %s: %w", team.SquadPath(projectDir), err)
	}
	s, err := team.ParseSquad(string(raw), team.SquadPath(projectDir), owner)
	if err != nil {
		return "", err
	}
	s.Seats = append(s.Seats, seat)
	return team.Render(s), nil
}

// squadFromFlags renders the squad.yaml text the flags describe.
func squadFromFlags(projectDir string, o teamProposeFlags) (string, error) {
	roles := splitCommaList(*o.roles)
	if len(roles) == 0 {
		return "", usageErrorf("--roles or --from is required")
	}
	per := map[string]map[string]string{}
	for flagName, raw := range map[string]string{"runtime": *o.runtime, "model": *o.model, "effort": *o.effort, "worktree": *o.worktree, "tools": *o.tools, "actor-mode": *o.actorMode} {
		kv, err := parseKV(raw)
		if err != nil {
			return "", usageErrorf("--%s: %v", flagName, err)
		}
		for role := range kv {
			if !containsString(roles, role) {
				return "", usageErrorf("--%s names %q, which --roles does not select", flagName, role)
			}
		}
		per[flagName] = kv
	}
	s := team.Squad{Schema: team.SquadSchema, Session: *o.session, LeadMode: *o.leadMode, ExecutionMode: *o.executionMode,
		ControlRoot: *o.controlRoot, TargetProjectRoot: *o.targetRoot, OperatorInteraction: *o.operator, Lead: *o.lead}
	for _, role := range roles {
		runtime := per["runtime"][role]
		if r := catalog.Lookup(role); runtime == "" && r != nil {
			runtime = r.PreferredBinary
		}
		s.Seats = append(s.Seats, team.Seat{Handle: role, Role: role, Runtime: runtime, Model: per["model"][role], Effort: per["effort"][role],
			Worktree: per["worktree"][role], Tools: per["tools"][role], ActorMode: per["actor-mode"][role]})
	}
	return team.Render(s), nil
}

func runTeamShow(verb string, args []string) error {
	fs := flag.NewFlagSet("team "+verb, flag.ContinueOnError)
	fs.Usage = printTeamUsage
	project := fs.String("project", "", "repository root (default: cwd)")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	projectDir, err := filepath.Abs(map[bool]string{true: ".", false: *project}[*project == ""])
	if err != nil {
		return err
	}
	t, err := team.Read(projectDir)
	if team.IsNotExist(err) {
		return fmt.Errorf("no squad.yaml at %s; the lead proposes one with amq-squad team propose", team.SquadPath(projectDir))
	}
	if err != nil {
		return err
	}
	effective, err := grants.Load(projectDir)
	if err != nil {
		return err
	}
	if verb == "check" {
		fmt.Printf("%s: schema %d, %d seats, valid\n", team.SquadPath(projectDir), team.SquadSchema, len(t.Members))
		return nil
	}
	if *jsonOut {
		return printJSONEnvelope("team_show", struct {
			Path  string          `json:"path"`
			Squad team.Squad      `json:"squad"`
			Grant grants.Resolved `json:"effective_grant"`
		}{team.SquadPath(projectDir), t.Squad, effective})
	}
	fmt.Printf("# %s\n# mode %s, max_seats %d, grant source %s\n", team.SquadPath(projectDir), effective.Mode, effective.MaxSeats, effective.Source)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "HANDLE\tROLE\tRUNTIME\tMODEL\tEFFORT\tWORKTREE")
	for _, m := range t.Members {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", m.Handle, m.Role, m.Binary, m.Model, m.Effort, m.EffectiveCWD(projectDir))
	}
	return w.Flush()
}

func runTeamDraftRules(args []string) error {
	fs := flag.NewFlagSet("team draft-rules", flag.ContinueOnError)
	fs.Usage = printTeamUsage
	project := fs.String("project", "", "repository root (default: cwd)")
	template := fs.String("template", "auto", "team-rules template")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	projectDir, err := filepath.Abs(map[bool]string{true: ".", false: *project}[*project == ""])
	if err != nil {
		return err
	}
	t, err := team.Read(projectDir)
	if err != nil {
		return err
	}
	selected, err := selectTeamRulesTemplate(*template, t)
	if err != nil {
		return err
	}
	draft, err := draftTeamRulesProse(projectDir, selected, t)
	if err != nil {
		return err
	}
	if draft.Manual {
		writeManualDraft(os.Stdout, "No team rules were written.", draft)
		return nil
	}
	fmt.Println(draft.Text)
	return nil
}

// splitCommaList splits a comma-separated flag value, trimming blanks.
func splitCommaList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseKV(s string) (map[string]string, error) {
	out := map[string]string{}
	for _, part := range splitCommaList(s) {
		k, v, ok := strings.Cut(part, "=")
		if !ok || strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			return nil, fmt.Errorf("want key=value, got %q", part)
		}
		out[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
	}
	return out, nil
}

func containsString(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
