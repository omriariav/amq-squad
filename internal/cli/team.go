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
  amq-squad team show [--json]           Print squad.yaml and the effective grant
  amq-squad team check                   Validate squad.yaml (strict; widening is an error)
  amq-squad team draft-rules [--template T]
                                         Draft the team-rules prose fragment (stdout)

Options for propose and apply:
  --from FILE            a squad.yaml draft to propose (else built from the flags below)
  --roles a,b            seats, one per catalog or staged custom role (handle = role)
  --runtime r=claude     per-role runtime (claude, codex, pi; default the role's)
  --model r=m --effort r=e --worktree r=path --tools r=coding --actor-mode r=review
  --lead ROLE --lead-mode builder|planner --execution-mode MODE --session NAME
  --control-root DIR --target-project-root DIR --operator-interaction MODE
  --template auto|dev-only|product-squad|scrum|custom   team-rules template
  --rules-prose FILE     a team-rules prose fragment from team draft-rules
  --project DIR          the repository root (default: cwd)   --json   JSON output

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
	template, rulesProse, digest                                              *string
	json                                                                      *bool
}

func runTeamPropose(args []string, apply bool) error {
	name := map[bool]string{false: "team propose", true: "team apply"}[apply]
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	o := teamProposeFlags{
		project: fs.String("project", "", ""), from: fs.String("from", "", ""), roles: fs.String("roles", "", ""),
		runtime: fs.String("runtime", "", ""), model: fs.String("model", "", ""), effort: fs.String("effort", "", ""),
		worktree: fs.String("worktree", "", ""), tools: fs.String("tools", "", ""), actorMode: fs.String("actor-mode", "", ""),
		lead: fs.String("lead", "", ""), leadMode: fs.String("lead-mode", "", ""), executionMode: fs.String("execution-mode", "", ""),
		session: fs.String("session", "", ""), controlRoot: fs.String("control-root", "", ""), targetRoot: fs.String("target-project-root", "", ""),
		operator: fs.String("operator-interaction", "", ""), template: fs.String("template", "auto", ""),
		rulesProse: fs.String("rules-prose", "", ""), digest: fs.String("digest", "", ""), json: fs.Bool("json", false, ""),
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
	p, err := buildTeamProposal(o)
	if err != nil {
		return err
	}
	if apply {
		if err := p.Apply(*o.digest); err != nil {
			return err
		}
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

// buildTeamProposal computes every planned write and the digest over them;
// it writes nothing.
func buildTeamProposal(o teamProposeFlags) (team.Plan, error) {
	projectDir := strings.TrimSpace(*o.project)
	if projectDir == "" {
		projectDir = "."
	}
	projectDir, err := filepath.Abs(projectDir)
	if err != nil {
		return team.Plan{}, err
	}
	owner, err := grants.Load("")
	if err != nil {
		return team.Plan{}, err
	}
	text := ""
	if *o.from != "" {
		raw, err := os.ReadFile(*o.from)
		if err != nil {
			return team.Plan{}, err
		}
		text = string(raw)
	} else if text, err = squadFromFlags(projectDir, o); err != nil {
		return team.Plan{}, err
	}
	s, err := team.ParseSquad(text, team.SquadPath(projectDir), owner.Grants)
	if err != nil {
		return team.Plan{}, err
	}
	t := team.FromSquad(projectDir, s)
	for _, m := range t.Members {
		if catalog.Lookup(m.Role) == nil {
			if _, err := os.Stat(team.CustomRolePath(projectDir, m.Role)); err != nil {
				return team.Plan{}, fmt.Errorf("seat %s: role %q is neither a catalog role nor staged at %s (amq-squad role draft)", m.Handle, m.Role, team.CustomRolePath(projectDir, m.Role))
			}
		}
	}
	var prose *teamRulesProse
	if *o.rulesProse != "" {
		raw, err := os.ReadFile(*o.rulesProse)
		if err != nil {
			return team.Plan{}, err
		}
		parsed, err := validateTeamRulesDraft(string(raw), customTeamRulesRoles(t))
		if err != nil {
			return team.Plan{}, fmt.Errorf("--rules-prose: %w", err)
		}
		prose = &parsed
	}
	rulesText, err := renderTeamRulesWithTemplateDraft(t, *o.template, prose)
	if err != nil {
		return team.Plan{}, err
	}
	return team.NewPlan(projectDir, text, t, rulesText)
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
