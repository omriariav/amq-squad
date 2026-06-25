package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

var goalGhRun = func(args ...string) ([]byte, error) {
	cmd := exec.Command("gh", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
		return nil, err
	}
	return out, nil
}

type goalDraftData struct {
	Goal               string                 `json:"goal"`
	Repo               string                 `json:"repo,omitempty"`
	Milestone          string                 `json:"milestone,omitempty"`
	Session            string                 `json:"session"`
	Profile            string                 `json:"profile"`
	Composition        string                 `json:"composition"`
	Visibility         string                 `json:"visibility"`
	AutonomousPolicy   *team.AutonomousPolicy `json:"autonomous_policy,omitempty"`
	PreviewOnly        bool                   `json:"preview_only"`
	CodexOnly          bool                   `json:"codex_only,omitempty"`
	IssueSources       []goalIssueSource      `json:"issue_sources,omitempty"`
	BriefSkeleton      string                 `json:"brief_skeleton"`
	Roster             []goalRosterMember     `json:"roster"`
	Tasks              []goalTaskPlan         `json:"tasks"`
	SpawnGates         []goalCommandPlan      `json:"spawn_gates"`
	Dispatches         []goalDispatchPlan     `json:"dispatches"`
	ApplyableMutations []goalCommandPlan      `json:"applyable_mutations"`
	OrchestratorPrompt string                 `json:"orchestrator_prompt"`
	Notes              []string               `json:"notes"`
}

type goalIssueSource struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	State  string `json:"state,omitempty"`
}

type goalRosterMember struct {
	Role      string   `json:"role"`
	Handle    string   `json:"handle"`
	Binary    string   `json:"binary"`
	Reason    string   `json:"reason"`
	CodexArgs []string `json:"codex_args,omitempty"`
}

type goalTaskPlan struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Assignee  string   `json:"assignee"`
	DependsOn []string `json:"depends_on,omitempty"`
	SourceURL string   `json:"source_url,omitempty"`
}

type goalCommandPlan struct {
	Title   string `json:"title"`
	Command string `json:"command"`
	Reason  string `json:"reason,omitempty"`
}

type goalDispatchPlan struct {
	TaskID  string `json:"task_id"`
	Role    string `json:"role"`
	Thread  string `json:"thread"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
	Command string `json:"command"`
}

func runGoal(args []string) error {
	if len(args) == 0 {
		printGoalUsage()
		return nil
	}
	switch args[0] {
	case "-h", "--help":
		printGoalUsage()
		return nil
	case "draft":
		return runGoalDraft(args[1:])
	case "apply":
		return usageErrorf("goal apply is not implemented yet; run `amq-squad goal draft` and review the preview first")
	default:
		return usageErrorf("unknown 'goal' subcommand: %q. Try 'draft'.", args[0])
	}
}

func printGoalUsage() {
	fmt.Fprint(os.Stderr, `amq-squad goal - draft or apply a preview-first goal setup plan

Usage:
  amq-squad goal draft --goal TEXT [--repo owner/repo] [--milestone NAME] [--session NAME] [--profile NAME] [--visibility sibling-tabs|detached|current|plan] [--composition seeded|autonomous] [--max-agents N --max-total-spawns N --allowed-roles role,... --budget-turns N] [--codex-only] [--json]

Examples:
  amq-squad goal draft --goal "deliver GitHub milestone v2.7.0" --repo omriariav/amq-squad --milestone v2.7.0 --session v2-7-0 --profile codex-v2-7-0
  amq-squad goal draft --goal "fix issue 96" --session issue-96 --json
`)
}

func runGoalDraft(args []string) error {
	fs := flag.NewFlagSet("goal draft", flag.ContinueOnError)
	goalFlag := fs.String("goal", "", "high-level goal to turn into a setup draft")
	repoFlag := fs.String("repo", "", "GitHub repo owner/name for milestone lookup")
	milestoneFlag := fs.String("milestone", "", "GitHub milestone title to include issue titles and URLs")
	sessionFlag := fs.String("session", "", "AMQ workstream session name")
	profileFlag := fs.String("profile", "", "team profile name for the proposed setup")
	compositionFlag := fs.String("composition", team.CompositionSeeded, "composition mode: seeded (default) or autonomous")
	maxAgentsFlag := fs.Int("max-agents", 0, "autonomous guardrail: maximum active agents")
	maxTotalSpawnsFlag := fs.Int("max-total-spawns", 0, "autonomous guardrail: maximum total autonomous spawns")
	allowedRolesFlag := fs.String("allowed-roles", "", "autonomous guardrail: comma-separated role allowlist")
	allowedRoleClassesFlag := fs.String("allowed-role-classes", "", "autonomous guardrail: comma-separated role-class allowlist")
	budgetTurnsFlag := fs.Int("budget-turns", 0, "autonomous guardrail: maximum lead turns before operator review")
	idleReapMinutesFlag := fs.Int("idle-reap-minutes", 0, "autonomous guardrail: idle minutes before prune is allowed")
	visibilityFlag := fs.String("visibility", visibilitySiblingTabs, "launch topology: sibling-tabs (default), detached, current, or plan")
	codexOnly := fs.Bool("codex-only", false, "propose Codex binaries for every role")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned goal_draft envelope instead of Markdown")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad goal draft - produce a preview-only setup plan from a goal

Usage:
  amq-squad goal draft --goal TEXT [--repo owner/repo] [--milestone NAME] [--session NAME] [--profile NAME] [--visibility sibling-tabs|detached|current|plan] [--composition seeded|autonomous] [--max-agents N --max-total-spawns N --allowed-roles role,... --budget-turns N] [--codex-only] [--json]

The draft is read-only. It prints proposed briefs, roster entries, task-store
items, spawn gates, dispatches, and the orchestrator prompt, but it does not
write files, mutate rosters, send AMQ messages, launch agents, or create tasks.

Examples:
  amq-squad goal draft --goal "deliver GitHub milestone v2.7.0" --repo omriariav/amq-squad --milestone v2.7.0 --session v2-7-0 --profile codex-v2-7-0
  amq-squad goal draft --goal "fix issue 96" --session issue-96 --json
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("goal draft takes no positional arguments; use --goal TEXT")
	}
	goal := strings.TrimSpace(*goalFlag)
	if goal == "" {
		return usageErrorf("goal draft requires --goal TEXT")
	}
	if strings.TrimSpace(*milestoneFlag) != "" && strings.TrimSpace(*repoFlag) == "" {
		return usageErrorf("goal draft --milestone requires --repo owner/repo")
	}
	data, err := buildGoalDraft(goalDraftOptions{
		Goal:               goal,
		Repo:               strings.TrimSpace(*repoFlag),
		Milestone:          strings.TrimSpace(*milestoneFlag),
		Session:            strings.TrimSpace(*sessionFlag),
		Profile:            strings.TrimSpace(*profileFlag),
		CodexOnly:          *codexOnly,
		Composition:        strings.TrimSpace(*compositionFlag),
		MaxAgents:          *maxAgentsFlag,
		MaxTotalSpawns:     *maxTotalSpawnsFlag,
		AllowedRoles:       strings.TrimSpace(*allowedRolesFlag),
		AllowedRoleClasses: strings.TrimSpace(*allowedRoleClassesFlag),
		BudgetTurns:        *budgetTurnsFlag,
		IdleReapMinutes:    *idleReapMinutesFlag,
		Visibility:         strings.TrimSpace(*visibilityFlag),
	})
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSONEnvelope("goal_draft", data)
	}
	writeGoalDraftMarkdown(os.Stdout, data)
	return nil
}

type goalDraftOptions struct {
	Goal               string
	Repo               string
	Milestone          string
	Session            string
	Profile            string
	CodexOnly          bool
	Composition        string
	MaxAgents          int
	MaxTotalSpawns     int
	AllowedRoles       string
	AllowedRoleClasses string
	BudgetTurns        int
	IdleReapMinutes    int
	Visibility         string
}

func buildGoalDraft(opts goalDraftOptions) (goalDraftData, error) {
	session := strings.TrimSpace(opts.Session)
	if session == "" {
		seed := opts.Milestone
		if seed == "" {
			seed = opts.Goal
		}
		session = sanitizeWorkstreamName(seed)
	}
	if err := validateWorkstreamName(session); err != nil {
		return goalDraftData{}, fmt.Errorf("invalid session: %w", err)
	}
	profile := strings.TrimSpace(opts.Profile)
	if profile == "" {
		if opts.CodexOnly {
			profile = "codex-" + session
		} else {
			profile = session
		}
	}
	if err := validateProfileName(profile); err != nil {
		return goalDraftData{}, fmt.Errorf("invalid profile: %w", err)
	}
	composition := strings.TrimSpace(opts.Composition)
	if composition == "" {
		composition = team.CompositionSeeded
	}
	autonomousPolicy, err := resolveAutonomousPolicy(composition, opts.MaxAgents, opts.MaxTotalSpawns, opts.AllowedRoles, opts.AllowedRoleClasses, opts.BudgetTurns, opts.IdleReapMinutes)
	if err != nil {
		return goalDraftData{}, err
	}
	visibility, err := normalizeLaunchVisibility(opts.Visibility)
	if err != nil {
		return goalDraftData{}, err
	}
	issues, err := resolveGoalMilestoneIssues(opts.Repo, opts.Milestone)
	if err != nil {
		return goalDraftData{}, err
	}
	data := goalDraftData{
		Goal:             opts.Goal,
		Repo:             opts.Repo,
		Milestone:        opts.Milestone,
		Session:          session,
		Profile:          profile,
		Composition:      composition,
		Visibility:       visibility,
		AutonomousPolicy: autonomousPolicy,
		PreviewOnly:      true,
		CodexOnly:        opts.CodexOnly,
		IssueSources:     issues,
		Roster:           defaultGoalRoster(opts.CodexOnly, len(issues)),
		Notes: []string{
			"Seeded composition remains the default; autonomous composition requires explicit opt-in and policy limits.",
			"This draft is preview-only and does not mutate team.json, briefs, task files, AMQ mailboxes, launch records, wake locks, or panes.",
			"Default visibility is sibling-tabs: launch from an existing visible tmux pane so the lead and workers open as sibling tmux windows in that same session.",
			"Use --visibility detached only when a separate tmux session is intentional; use --visibility current for split panes in the current window; use --visibility plan when you want commands only.",
			"Merge, push, release, destructive filesystem actions, external communications, and provider side effects remain operator-owned.",
		},
	}
	data.BriefSkeleton = renderGoalBriefSkeleton(data)
	data.Tasks = defaultGoalTasks(data)
	data.SpawnGates = defaultGoalSpawnGates(data)
	data.Dispatches = defaultGoalDispatches(data)
	data.ApplyableMutations = defaultGoalMutations(data)
	data.OrchestratorPrompt = renderGoalOrchestratorPrompt(data)
	return data, nil
}

func validateProfileName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("profile name cannot be empty")
	}
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			continue
		}
		return fmt.Errorf("invalid profile name %q: use lowercase a-z, 0-9, - or _", name)
	}
	return nil
}

func resolveGoalMilestoneIssues(repo, milestone string) ([]goalIssueSource, error) {
	repo = strings.TrimSpace(repo)
	milestone = strings.TrimSpace(milestone)
	if milestone == "" {
		return nil, nil
	}
	out, err := goalGhRun("issue", "list", "--repo", repo, "--milestone", milestone, "--state", "all", "--limit", "200", "--json", "number,title,url,state")
	if err != nil {
		return nil, fmt.Errorf("goal draft milestone %q in %s: gh: %w", milestone, repo, err)
	}
	var issues []goalIssueSource
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil, fmt.Errorf("goal draft milestone %q in %s: parse gh output: %w", milestone, repo, err)
	}
	sort.SliceStable(issues, func(i, j int) bool { return issues[i].Number < issues[j].Number })
	return issues, nil
}

func defaultGoalRoster(codexOnly bool, issueCount int) []goalRosterMember {
	binary := func(defaultBinary string) string {
		if codexOnly {
			return "codex"
		}
		return defaultBinary
	}
	roster := []goalRosterMember{
		{
			Role:      "cto",
			Handle:    "cto",
			Binary:    "codex",
			Reason:    "Lead orchestration, scope control, architecture, final sign-off, and operator escalation.",
			CodexArgs: []string{"-c", "model_reasoning_effort=high"},
		},
		{
			Role:   "fullstack",
			Handle: "fullstack",
			Binary: binary("claude"),
			Reason: "Primary implementation owner for the drafted task plan.",
		},
		{
			Role:      "senior-dev",
			Handle:    "senior-dev",
			Binary:    "codex",
			Reason:    "Independent implementation-shape and risk review before merge-ready claims.",
			CodexArgs: []string{"-c", "model_reasoning_effort=high"},
		},
	}
	if issueCount > 3 {
		roster = append(roster, goalRosterMember{
			Role:   "qa",
			Handle: "qa",
			Binary: binary("claude"),
			Reason: "Milestone-sized work benefits from explicit regression and release-risk coverage.",
		})
	}
	for i := range roster {
		if roster[i].Binary == "codex" && len(roster[i].CodexArgs) == 0 {
			roster[i].CodexArgs = []string{"-c", "model_reasoning_effort=medium"}
		}
	}
	return roster
}

func renderGoalBriefSkeleton(data goalDraftData) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", data.Session)
	fmt.Fprintf(&b, "## Goal\n%s\n\n", data.Goal)
	if data.Repo != "" || data.Milestone != "" {
		b.WriteString("## Source\n")
		if data.Repo != "" {
			fmt.Fprintf(&b, "- Repo: %s\n", data.Repo)
		}
		if data.Milestone != "" {
			fmt.Fprintf(&b, "- Milestone: %s\n", data.Milestone)
		}
		for _, issue := range data.IssueSources {
			fmt.Fprintf(&b, "- #%d %s - %s\n", issue.Number, issue.Title, issue.URL)
		}
		b.WriteString("\n")
	}
	b.WriteString("## Scope\n- Deliver the goal through amq-squad orchestration.\n- Keep AMQ, the task store, and the workstream brief as durable coordination records.\n")
	fmt.Fprintf(&b, "- Composition mode: %s.\n\n", data.Composition)
	fmt.Fprintf(&b, "- Visibility: %s.\n\n", data.Visibility)
	if data.AutonomousPolicy != nil {
		b.WriteString("## Autonomous policy\n")
		fmt.Fprintf(&b, "- Max active agents: %d\n", data.AutonomousPolicy.MaxActiveAgents)
		fmt.Fprintf(&b, "- Max total spawns: %d\n", data.AutonomousPolicy.MaxTotalSpawns)
		fmt.Fprintf(&b, "- Allowed roles: %s\n", strings.Join(data.AutonomousPolicy.AllowedRoles, ", "))
		fmt.Fprintf(&b, "- Allowed role classes: %s\n", strings.Join(data.AutonomousPolicy.AllowedRoleClasses, ", "))
		fmt.Fprintf(&b, "- Budget turns: %d\n\n", data.AutonomousPolicy.BudgetTurns)
	}
	b.WriteString("## Out of scope\n- No autonomous action outside the declared policy envelope.\n- No child-authored spawn or prune authority.\n- No merge, release, destructive filesystem action, external communication, or provider side effect without operator approval.\n\n")
	b.WriteString("## Acceptance\n- Preview is reviewed before any setup mutation.\n- Spawn gates are explicit and durable.\n- Tasks, dispatches, review evidence, and final verification are recorded before merge-ready claims.\n")
	return b.String()
}

func defaultGoalTasks(data goalDraftData) []goalTaskPlan {
	if len(data.IssueSources) == 0 {
		return []goalTaskPlan{
			{ID: "t1", Title: "Confirm scope and acceptance from the goal", Assignee: "cto"},
			{ID: "t2", Title: "Implement the goal against the agreed scope", Assignee: "fullstack", DependsOn: []string{"t1"}},
			{ID: "t3", Title: "Review implementation and test evidence", Assignee: "senior-dev", DependsOn: []string{"t2"}},
		}
	}
	tasks := make([]goalTaskPlan, 0, len(data.IssueSources)+1)
	for i, issue := range data.IssueSources {
		tasks = append(tasks, goalTaskPlan{
			ID:        "t" + strconv.Itoa(i+1),
			Title:     fmt.Sprintf("Resolve #%d: %s", issue.Number, issue.Title),
			Assignee:  "fullstack",
			SourceURL: issue.URL,
		})
	}
	deps := make([]string, 0, len(tasks))
	for _, task := range tasks {
		deps = append(deps, task.ID)
	}
	tasks = append(tasks, goalTaskPlan{
		ID:        "t" + strconv.Itoa(len(tasks)+1),
		Title:     "Milestone integration review and merge-gate evidence",
		Assignee:  "senior-dev",
		DependsOn: deps,
	})
	return tasks
}

func defaultGoalSpawnGates(data goalDraftData) []goalCommandPlan {
	gates := make([]goalCommandPlan, 0, len(data.Roster))
	for _, member := range data.Roster {
		if member.Role == "cto" {
			continue
		}
		gates = append(gates, goalCommandPlan{
			Title:   "spawn " + member.Role,
			Command: fmt.Sprintf("amq send --to user --thread gate/spawn-%s --kind question --subject %q --body %q", member.Role, "APPROVAL: spawn "+member.Role+" ("+member.Binary+")", "The goal needs "+member.Role+" to "+member.Reason+" Approve?"),
			Reason:  member.Reason,
		})
	}
	return gates
}

func defaultGoalDispatches(data goalDraftData) []goalDispatchPlan {
	dispatches := make([]goalDispatchPlan, 0, len(data.Tasks))
	for _, task := range data.Tasks {
		if task.Assignee == "cto" {
			continue
		}
		thread := canonicalP2PThread("cto", task.Assignee)
		subject := "Task: " + task.Title
		body := task.Title + "\n\nPush progress, blockers, review requests, and DONE reports to cto over AMQ. Treat this durable AMQ task as the source of truth."
		if task.SourceURL != "" {
			body += "\n\nSource: " + task.SourceURL
		}
		dispatches = append(dispatches, goalDispatchPlan{
			TaskID:  task.ID,
			Role:    task.Assignee,
			Thread:  thread,
			Subject: subject,
			Body:    body,
			Command: fmt.Sprintf("amq-squad dispatch --session %s --role %s --thread %s --kind todo --subject %q --body %q", data.Session, task.Assignee, thread, subject, body),
		})
	}
	return dispatches
}

func defaultGoalMutations(data goalDraftData) []goalCommandPlan {
	roles := make([]string, 0, len(data.Roster))
	binaries := make([]string, 0, len(data.Roster))
	for _, member := range data.Roster {
		roles = append(roles, member.Role)
		binaries = append(binaries, member.Role+"="+member.Binary)
	}
	compositionArgs := ""
	if data.Composition == team.CompositionAutonomous && data.AutonomousPolicy != nil {
		compositionArgs = fmt.Sprintf(" --composition autonomous --max-agents %d --max-total-spawns %d --allowed-roles %s --budget-turns %d",
			data.AutonomousPolicy.MaxActiveAgents,
			data.AutonomousPolicy.MaxTotalSpawns,
			strings.Join(data.AutonomousPolicy.AllowedRoles, ","),
			data.AutonomousPolicy.BudgetTurns,
		)
		if len(data.AutonomousPolicy.AllowedRoleClasses) > 0 {
			compositionArgs += " --allowed-role-classes " + strings.Join(data.AutonomousPolicy.AllowedRoleClasses, ",")
		}
		if data.AutonomousPolicy.IdleReapMinutes > 0 {
			compositionArgs += fmt.Sprintf(" --idle-reap-minutes %d", data.AutonomousPolicy.IdleReapMinutes)
		}
	}
	mutations := []goalCommandPlan{
		{
			Title:   "initialize profile",
			Command: fmt.Sprintf("amq-squad team init --profile %s --session %s --roles %s --binary %s --orchestrated --lead cto%s --dry-run", data.Profile, data.Session, strings.Join(roles, ","), strings.Join(binaries, ","), compositionArgs),
			Reason:  "Preview the proposed roster and orchestration metadata before writing team config.",
		},
		{
			Title:   "write brief",
			Command: fmt.Sprintf("amq-squad brief seed --session %s --seed-from file:<approved-brief.md> --dry-run", data.Session),
			Reason:  "Preview the workstream brief before writing .amq-squad/briefs.",
		},
	}
	for _, task := range data.Tasks {
		cmd := fmt.Sprintf("amq-squad task add --session %s --title %q --assign %s", data.Session, task.Title, task.Assignee)
		if len(task.DependsOn) > 0 {
			cmd += " --depends-on " + strings.Join(task.DependsOn, ",")
		}
		mutations = append(mutations, goalCommandPlan{Title: "add " + task.ID, Command: cmd, Reason: "Create the native task-store item after preview approval."})
	}
	mutations = append(mutations, goalVisibilityMutation(data))
	return mutations
}

func goalVisibilityMutation(data goalDraftData) goalCommandPlan {
	switch data.Visibility {
	case visibilityDetached:
		return goalCommandPlan{
			Title:   "launch detached team",
			Command: fmt.Sprintf("amq-squad up %s --profile %s --visibility detached", data.Session, data.Profile),
			Reason:  "Explicitly create a detached tmux session for background work; attach/open it deliberately before treating the team as visible.",
		}
	case visibilityCurrent:
		return goalCommandPlan{
			Title:   "launch in current window",
			Command: fmt.Sprintf("amq-squad up %s --profile %s --visibility current", data.Session, data.Profile),
			Reason:  "Split the current visible tmux window into agent panes; this is compact but not the default sibling-window topology.",
		}
	case visibilityPlan:
		return goalCommandPlan{
			Title:   "preview visible launch",
			Command: fmt.Sprintf("amq-squad up %s --profile %s --visibility sibling-tabs --dry-run", data.Session, data.Profile),
			Reason:  "Preview launch commands only; do not open panes or windows until the operator approves a concrete visibility mode.",
		}
	default:
		return goalCommandPlan{
			Title:   "launch visible team",
			Command: fmt.Sprintf("amq-squad up %s --profile %s --visibility sibling-tabs", data.Session, data.Profile),
			Reason:  "Run from a visible tmux pane; opens the lead and workers as sibling tmux windows in the same session and refuses outside tmux before spawning hidden workers.",
		}
	}
}

func renderGoalOrchestratorPrompt(data goalDraftData) string {
	args := []string{"/goal", "--goal", strconv.Quote(data.Goal), "--session", data.Session, "--profile", data.Profile}
	if data.Repo != "" {
		args = append(args, "--repo", data.Repo)
	}
	if data.Milestone != "" {
		args = append(args, "--milestone", data.Milestone)
	}
	if data.CodexOnly {
		args = append(args, "--codex-only")
	}
	if data.Composition == team.CompositionAutonomous && data.AutonomousPolicy != nil {
		args = append(args, "--composition", "autonomous", "--max-agents", strconv.Itoa(data.AutonomousPolicy.MaxActiveAgents), "--max-total-spawns", strconv.Itoa(data.AutonomousPolicy.MaxTotalSpawns), "--allowed-roles", strings.Join(data.AutonomousPolicy.AllowedRoles, ","), "--budget-turns", strconv.Itoa(data.AutonomousPolicy.BudgetTurns))
	}
	return strings.Join(args, " ")
}

func writeGoalDraftMarkdown(out *os.File, data goalDraftData) {
	fmt.Fprintln(out, "# amq-squad goal draft")
	fmt.Fprintf(out, "# preview_only: %t\n", data.PreviewOnly)
	fmt.Fprintf(out, "# composition: %s\n", data.Composition)
	fmt.Fprintf(out, "# visibility: %s\n", data.Visibility)
	fmt.Fprintf(out, "# session: %s\n", data.Session)
	fmt.Fprintf(out, "# profile: %s\n", data.Profile)
	if data.Repo != "" {
		fmt.Fprintf(out, "# repo: %s\n", data.Repo)
	}
	if data.Milestone != "" {
		fmt.Fprintf(out, "# milestone: %s\n", data.Milestone)
	}
	if data.AutonomousPolicy != nil {
		fmt.Fprintf(out, "# autonomous.max_active_agents: %d\n", data.AutonomousPolicy.MaxActiveAgents)
		fmt.Fprintf(out, "# autonomous.max_total_spawns: %d\n", data.AutonomousPolicy.MaxTotalSpawns)
		fmt.Fprintf(out, "# autonomous.budget_turns: %d\n", data.AutonomousPolicy.BudgetTurns)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "## Brief Skeleton")
	fmt.Fprintln(out)
	fmt.Fprint(out, data.BriefSkeleton)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "## Roster")
	for _, member := range data.Roster {
		fmt.Fprintf(out, "- %s (%s): %s\n", member.Role, member.Binary, member.Reason)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "## Task Plan")
	for _, task := range data.Tasks {
		deps := ""
		if len(task.DependsOn) > 0 {
			deps = " after " + strings.Join(task.DependsOn, ",")
		}
		fmt.Fprintf(out, "- %s [%s%s]: %s\n", task.ID, task.Assignee, deps, task.Title)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "## Spawn Gates")
	for _, gate := range data.SpawnGates {
		fmt.Fprintf(out, "- `%s`\n", gate.Command)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "## Proposed Mutations")
	for _, mutation := range data.ApplyableMutations {
		fmt.Fprintf(out, "- `%s`\n", mutation.Command)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "## Dispatches")
	for _, dispatch := range data.Dispatches {
		fmt.Fprintf(out, "- `%s`\n", dispatch.Command)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "## Orchestrator Prompt")
	fmt.Fprintf(out, "`%s`\n", data.OrchestratorPrompt)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "## Notes")
	for _, note := range data.Notes {
		fmt.Fprintf(out, "- %s\n", note)
	}
}
