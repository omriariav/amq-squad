package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/worktreeplan"
)

type worktreeEnvelopeData struct {
	Project    string                    `json:"project"`
	Profile    string                    `json:"profile"`
	Session    string                    `json:"session"`
	StorePath  string                    `json:"store_path"`
	Mutation   string                    `json:"mutation,omitempty"`
	Plan       *worktreeplan.Record      `json:"plan,omitempty"`
	Set        *worktreeplan.Set         `json:"set,omitempty"`
	Inspection *worktreeplan.Inspection  `json:"inspection,omitempty"`
	Checks     []worktreeplan.Diagnostic `json:"checks,omitempty"`
}

type worktreeScope struct {
	project string
	profile string
	session string
	team    team.Team
	amqRoot string
}

func runWorktree(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, `amq-squad worktree - deterministic isolated worker worktrees

Usage:
  amq-squad worktree plan --role R --task ID --base SHA --scope PATH... --session S [--profile P] [--path PATH] [--branch REF] [--repo PATH] [--json]
  amq-squad worktree materialize --role R --task ID --base SHA --scope PATH... --session S [--profile P] [--path PATH] [--branch REF] [--repo PATH] --yes [--json]
  amq-squad worktree inspect --session S [--profile P] [--role R] [--json]
  amq-squad worktree activate --role R --session S [--profile P] --yes [--json]
  amq-squad worktree handoff --role R [--sha SHA] --session S [--profile P] --yes [--json]
  amq-squad worktree cleanup --role R --decision accepted|rejected --session S [--profile P] --yes [--json]
  amq-squad worktree exception set --reason WHY --session S [--profile P] --yes [--json]
  amq-squad worktree exception clear --session S [--profile P] --yes [--json]

plan is read-only. Mutating commands are explicit --yes operations. cleanup only
removes a clean, registered worktree whose exact path and branch match the
durable session plan; it never deletes an unknown directory or branch.
`)
		if len(args) == 0 {
			return usageErrorf("worktree requires a subcommand")
		}
		return nil
	}
	switch args[0] {
	case "plan":
		return runWorktreePlan(args[1:], false)
	case "materialize", "create":
		return runWorktreePlan(args[1:], true)
	case "inspect":
		return runWorktreeInspect(args[1:])
	case "activate":
		return runWorktreeTransition(args[1:], "activate")
	case "handoff":
		return runWorktreeTransition(args[1:], "handoff")
	case "cleanup":
		return runWorktreeTransition(args[1:], "cleanup")
	case "exception":
		return runWorktreeException(args[1:])
	default:
		return usageErrorf("unknown 'worktree' subcommand: %q", args[0])
	}
}

func runWorktreePlan(args []string, materialize bool) error {
	name := "worktree plan"
	if materialize {
		name = "worktree materialize"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	role := fs.String("role", "", "team role to plan")
	taskID := fs.String("task", "", "durable task id")
	base := fs.String("base", "", "accepted base commit/ref")
	var scopes stringListFlag
	fs.Var(&scopes, "scope", "owned path/scope (repeatable)")
	path := fs.String("path", "", "explicit worktree path")
	branch := fs.String("branch", "", "explicit branch name")
	repo := fs.String("repo", "", "target Git repository")
	project := fs.String("project", "", "project/team-home directory")
	profile := fs.String("profile", "", "team profile")
	session := fs.String("session", "", "workstream session")
	registerScopedFlagAliases(fs, project, session, profile)
	yes := fs.Bool("yes", false, "confirm materialization")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned JSON envelope")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if strings.TrimSpace(*role) == "" || strings.TrimSpace(*taskID) == "" || strings.TrimSpace(*base) == "" {
		return usageErrorf("%s requires --role, --task, and --base", name)
	}
	if materialize && !*yes {
		return usageErrorf("%s changes Git and team state; rerun with --yes", name)
	}
	scope, err := resolveWorktreeScope(fs, *project, *profile, *session)
	if err != nil {
		return err
	}
	service, err := worktreeplan.NewService(scope.team, scope.profile, scope.session, worktreeplan.ExecGit{}, nil)
	if err != nil {
		return err
	}
	request := worktreeplan.Request{
		Role: *role, TaskID: *taskID, Base: *base, Scope: scopes,
		Path: *path, Branch: *branch, RepoRoot: *repo, AMQRoot: scope.amqRoot,
	}
	var set worktreeplan.Set
	var plan worktreeplan.Record
	if materialize {
		set, plan, err = service.Materialize(request)
	} else {
		set, plan, err = service.Plan(request)
	}
	if err != nil {
		return err
	}
	mutation := "preview"
	if materialize {
		mutation = "materialized"
	}
	return emitWorktreeResult(*jsonOut, worktreeEnvelopeData{
		Project: scope.project, Profile: scope.profile, Session: scope.session,
		StorePath: worktreeplan.StorePath(scope.team, scope.profile, scope.session),
		Mutation:  mutation, Plan: &plan, Set: &set,
	})
}

func runWorktreeInspect(args []string) error {
	fs := flag.NewFlagSet("worktree inspect", flag.ContinueOnError)
	role := fs.String("role", "", "limit output to one team role")
	project := fs.String("project", "", "project/team-home directory")
	profile := fs.String("profile", "", "team profile")
	session := fs.String("session", "", "workstream session")
	registerScopedFlagAliases(fs, project, session, profile)
	jsonOut := fs.Bool("json", false, "emit a schema-versioned JSON envelope")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	scope, err := resolveWorktreeScope(fs, *project, *profile, *session)
	if err != nil {
		return err
	}
	service, err := worktreeplan.NewService(scope.team, scope.profile, scope.session, worktreeplan.ExecGit{}, nil)
	if err != nil {
		return err
	}
	inspection, err := service.Inspect()
	if err != nil {
		return err
	}
	if selected := strings.TrimSpace(*role); selected != "" {
		var members []worktreeplan.MemberStatus
		for _, member := range inspection.Members {
			if member.Role == selected {
				members = append(members, member)
			}
		}
		if len(members) == 0 {
			return fmt.Errorf("role %q has no mutation-capable worktree status", selected)
		}
		inspection.Members = members
	}
	return emitWorktreeResult(*jsonOut, worktreeEnvelopeData{
		Project: scope.project, Profile: scope.profile, Session: scope.session,
		StorePath: inspection.StorePath, Inspection: &inspection, Checks: inspection.Diagnostics,
	})
}

func runWorktreeTransition(args []string, action string) error {
	fs := flag.NewFlagSet("worktree "+action, flag.ContinueOnError)
	role := fs.String("role", "", "team role")
	sha := fs.String("sha", "", "expected handoff HEAD")
	decision := fs.String("decision", "", "cleanup decision: accepted or rejected")
	project := fs.String("project", "", "project/team-home directory")
	profile := fs.String("profile", "", "team profile")
	session := fs.String("session", "", "workstream session")
	registerScopedFlagAliases(fs, project, session, profile)
	yes := fs.Bool("yes", false, "confirm mutation")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned JSON envelope")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if strings.TrimSpace(*role) == "" {
		return usageErrorf("worktree %s requires --role", action)
	}
	if !*yes {
		return usageErrorf("worktree %s changes durable state; rerun with --yes", action)
	}
	scope, err := resolveWorktreeScope(fs, *project, *profile, *session)
	if err != nil {
		return err
	}
	service, err := worktreeplan.NewService(scope.team, scope.profile, scope.session, worktreeplan.ExecGit{}, nil)
	if err != nil {
		return err
	}
	var plan worktreeplan.Record
	switch action {
	case "activate":
		plan, err = service.Activate(*role)
	case "handoff":
		plan, err = service.Handoff(*role, *sha)
	case "cleanup":
		plan, err = service.Cleanup(worktreeplan.CleanupRequest{Role: *role, Decision: *decision})
	}
	if err != nil {
		return err
	}
	return emitWorktreeResult(*jsonOut, worktreeEnvelopeData{
		Project: scope.project, Profile: scope.profile, Session: scope.session,
		StorePath: worktreeplan.StorePath(scope.team, scope.profile, scope.session),
		Mutation:  action, Plan: &plan,
	})
}

func runWorktreeException(args []string) error {
	if len(args) == 0 {
		return usageErrorf("worktree exception requires set or clear")
	}
	operation := args[0]
	if operation != "set" && operation != "clear" {
		return usageErrorf("unknown worktree exception subcommand %q", operation)
	}
	fs := flag.NewFlagSet("worktree exception "+operation, flag.ContinueOnError)
	reason := fs.String("reason", "", "auditable shared-cwd exception reason")
	project := fs.String("project", "", "project/team-home directory")
	profile := fs.String("profile", "", "team profile")
	session := fs.String("session", "", "workstream session")
	registerScopedFlagAliases(fs, project, session, profile)
	yes := fs.Bool("yes", false, "confirm mutation")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned JSON envelope")
	if err := parseFlags(fs, args[1:]); err != nil {
		return err
	}
	if !*yes {
		return usageErrorf("worktree exception %s changes durable state; rerun with --yes", operation)
	}
	scope, err := resolveWorktreeScope(fs, *project, *profile, *session)
	if err != nil {
		return err
	}
	service, err := worktreeplan.NewService(scope.team, scope.profile, scope.session, worktreeplan.ExecGit{}, nil)
	if err != nil {
		return err
	}
	set, err := service.SetSharedCWDExceptionAtAMQRoot(operation == "set", *reason, scope.amqRoot)
	if err != nil {
		return err
	}
	return emitWorktreeResult(*jsonOut, worktreeEnvelopeData{
		Project: scope.project, Profile: scope.profile, Session: scope.session,
		StorePath: worktreeplan.StorePath(scope.team, scope.profile, scope.session),
		Mutation:  "exception_" + operation, Set: &set,
	})
}

func resolveWorktreeScope(fs *flag.FlagSet, project, profile, session string) (worktreeScope, error) {
	if strings.TrimSpace(session) == "" {
		return worktreeScope{}, usageErrorf("worktree command requires --session")
	}
	ctx, err := resolveScopedCommandContext(project, profile, session, "", fs)
	if err != nil {
		return worktreeScope{}, err
	}
	t, err := team.ReadProfile(ctx.ProjectDir, ctx.Profile)
	if err != nil {
		return worktreeScope{}, err
	}
	originalProject := filepath.Clean(ctx.ProjectDir)
	reanchored := false
	if control := worktreeplan.ControlRoot(t); control != originalProject && team.ExistsProfile(control, ctx.Profile) {
		t, err = team.ReadProfile(control, ctx.Profile)
		if err != nil {
			return worktreeScope{}, fmt.Errorf("read canonical team profile: %w", err)
		}
		ctx.ProjectDir = control
		reanchored = true
	}
	amqRoot := strings.TrimSpace(ctx.Root)
	if amqRoot == "" {
		return worktreeScope{}, fmt.Errorf("canonical AMQ root is unresolved")
	}
	if !filepath.IsAbs(amqRoot) {
		amqRoot = filepath.Join(ctx.ProjectDir, amqRoot)
	}
	if reanchored {
		if relative, relErr := filepath.Rel(originalProject, filepath.Clean(amqRoot)); relErr == nil &&
			relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			amqRoot = squadnamespace.AMQRoot(ctx.ProjectDir, ctx.Profile, ctx.Session)
		}
	}
	return worktreeScope{
		project: ctx.ProjectDir, profile: ctx.Profile, session: ctx.Session,
		team: t, amqRoot: filepath.Clean(amqRoot),
	}, nil
}

func emitWorktreeResult(jsonOut bool, data worktreeEnvelopeData) error {
	if jsonOut {
		return writeJSONEnvelope(os.Stdout, "worktree", data)
	}
	if data.Plan != nil {
		fmt.Printf("%s %s: %s @ %s\n", data.Mutation, data.Plan.Role, data.Plan.Path, data.Plan.Branch)
		fmt.Printf("base=%s task=%s state=%s scope=%s\n", data.Plan.BaseSHA, data.Plan.TaskID, data.Plan.State, strings.Join(data.Plan.Scope, ","))
	}
	if data.Inspection != nil {
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ROLE\tSTATE\tWORKTREE\tBRANCH\tHEAD\tCLEAN\tDRIFT\tHANDOFF")
		for _, member := range data.Inspection.Members {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%t\t%t\t%s\n",
				member.Role, printableWorktreeState(member.State), member.Worktree, member.Branch,
				shortWorktreeSHA(member.CurrentHEAD), member.Clean, member.Drifted, shortWorktreeSHA(member.HandoffSHA))
		}
		if err := w.Flush(); err != nil {
			return err
		}
		for _, check := range data.Checks {
			fmt.Printf("%s %s: %s\n", check.Status, check.Kind, check.Detail)
		}
	}
	return nil
}

func printableWorktreeState(state worktreeplan.State) string {
	if state == "" {
		return "unplanned"
	}
	return string(state)
}

func shortWorktreeSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	if sha == "" {
		return "-"
	}
	return sha
}
