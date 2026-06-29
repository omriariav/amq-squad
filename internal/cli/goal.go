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

	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
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
	TargetContract     string                 `json:"target_contract,omitempty"`
	Session            string                 `json:"session"`
	Profile            string                 `json:"profile"`
	Lead               string                 `json:"lead"`
	Mode               string                 `json:"mode"`
	ControlRoot        string                 `json:"control_root,omitempty"`
	TargetProjectRoot  string                 `json:"target_project_root,omitempty"`
	Namespace          squadnamespace.Ref     `json:"namespace"`
	Execution          executionModeData      `json:"execution"`
	GoalBinding        goalBindingData        `json:"goal_binding"`
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

type goalBindingData struct {
	Mode         string `json:"mode"`
	NativeGoal   bool   `json:"native_goal"`
	Verified     bool   `json:"verified"`
	Source       string `json:"source"`
	Detail       string `json:"detail"`
	BriefPath    string `json:"brief_path,omitempty"`
	TasksPath    string `json:"tasks_path,omitempty"`
	NativeSource string `json:"native_source,omitempty"`
	Command      string `json:"command,omitempty"`
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
	return runGoalWithVersion(args, "dev")
}

func runGoalWithVersion(args []string, version string) error {
	if len(args) == 0 {
		printGoalUsage()
		return nil
	}
	switch args[0] {
	case "-h", "--help":
		printGoalUsage()
		return nil
	case "draft":
		return runGoalDraftWithVersion(args[1:], version)
	case "deliver":
		return runGoalDeliver(args[1:])
	case "apply":
		return usageErrorf("goal apply is not implemented yet; run `amq-squad goal draft` and review the preview first")
	default:
		return usageErrorf("unknown 'goal' subcommand: %q. Try 'draft'.", args[0])
	}
}

func printGoalUsage() {
	fmt.Fprint(os.Stderr, `amq-squad goal - draft or apply a preview-first goal setup plan

Usage:
  amq-squad goal draft --goal TEXT [--repo owner/repo] [--milestone NAME] [--session NAME] [--profile NAME] [--lead ROLE] [--mode project_lead|project_team|direct_lead_session|global_orchestrator] [--visibility sibling-tabs|detached|current|plan] [--composition seeded|autonomous] [--max-agents N --max-total-spawns N --allowed-roles role,... --budget-turns N] [--codex-only] [--json]
  amq-squad goal deliver --goal TEXT --session NAME [--profile NAME] [--role ROLE] [--json]

Examples:
  amq-squad goal draft --goal "deliver GitHub milestone v2.7.0" --repo omriariav/amq-squad --milestone v2.7.0 --session v2-7-0 --profile codex-v2-7-0
  amq-squad goal draft --goal "fix issue 96" --session issue-96 --json
`)
}

func runGoalDeliver(args []string) error {
	fs := flag.NewFlagSet("goal deliver", flag.ContinueOnError)
	goalFlag := fs.String("goal", "", "goal text to deliver as a native /goal control command")
	sessionFlag := fs.String("session", "", "workstream session of the visible lead")
	roleFlag := fs.String("role", "", "role to receive the native /goal command (default: configured lead)")
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile (default: default profile)")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned mutation result envelope")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad goal deliver - deliver native /goal as a control action

Usage:
  amq-squad goal deliver [--project DIR] [--profile NAME] --session S [--role ROLE] --goal TEXT [--json]

Delivers a native Codex /goal command to the visible lead as a first-class
control action. This is not an ordinary prompt send: it preserves the busy guard
for amq-squad send, but /goal delivery may target a busy Codex lead because the
runtime accepts goal control messages safely.
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	goal := strings.TrimSpace(*goalFlag)
	if goal == "" {
		return usageErrorf("goal deliver requires --goal TEXT")
	}
	projectDir, profile, err := resolveProjectProfile(*projectFlag, *profileFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	if !team.ExistsProfile(projectDir, profile) {
		return fmt.Errorf("no team configured for profile %q. Run '%s' first.", profile, profileInitCommand(profile))
	}
	t, err := team.ReadProfile(projectDir, profile)
	if err != nil {
		return fmt.Errorf("read team: %w", err)
	}
	workstream, err := resolveTeamWorkstreamName(t, *sessionFlag, flagWasSet(fs, "session"))
	if err != nil {
		return err
	}
	if err := ensureNoNamespaceConflict("goal deliver", projectDir, profile, workstream); err != nil {
		return err
	}
	role := strings.TrimSpace(*roleFlag)
	if role == "" {
		role = strings.TrimSpace(t.Lead)
	}
	if role == "" {
		return usageErrorf("goal deliver requires --role when the team has no configured lead")
	}
	if err := ensureTargetIsNotOperator(t, "goal deliver", role); err != nil {
		return err
	}
	member, ok := teamMemberByRole(t, role)
	if !ok {
		return fmt.Errorf("no team member with role %q in this team", role)
	}
	prompt := nativeGoalControlPrompt(goal, t, profile, workstream, role)
	receipt := newDeliveryReceipt(projectDir, profile, workstream, role, member.Handle, effectiveTeamExecutionMode(t), "native_goal")
	receipt.Method = "native_goal_control"
	receipt.addStage("queued", "native /goal control delivery accepted by amq-squad")

	mr, resolvedWorkstream, err := resolveMemberRuntime(projectDir, profile, workstream, true, role)
	if err != nil {
		return err
	}
	panes, err := statusPaneLister()
	if err != nil {
		if tmuxpane.IsPermissionDenied(err) {
			return errTmuxAccessDenied()
		}
		panes = nil
	}
	paneID, _, ok := resolveControlTarget(mr, resolvedWorkstream, panes)
	if !ok || strings.TrimSpace(paneID) == "" {
		return fmt.Errorf("no live tmux pane found for role %q; cannot deliver native /goal", role)
	}
	receipt.PaneID = paneID
	receipt.addStage("control_delivery_started", "resolved exact target pane for native /goal control")
	if err := sendPromptToPane(paneID, prompt); err != nil {
		receipt.Status = "failed"
		receipt.Detail = err.Error()
		receipt.addStage("failed", err.Error())
		_ = writeDeliveryReceipt(projectDir, profile, workstream, &receipt)
		return err
	}
	receipt.Status = "native_goal_delivered"
	receipt.addStage("native_goal_delivered", "native /goal command delivered without ordinary prompt busy-guard semantics")
	if mr.HasRecord {
		rec := mr.Record
		rec.GoalBinding = &launch.GoalBinding{
			Mode:       "native_goal",
			NativeGoal: true,
			Source:     "goal-control",
			Command:    prompt,
			Detail:     "native /goal delivered as a first-class control action",
		}
		if err := launch.Write(mr.AgentDir, rec); err != nil {
			return fmt.Errorf("update launch goal binding: %w", err)
		}
		receipt.addStage("launch_record_updated", "launch record goal_binding updated from native /goal control delivery")
	}
	if err := writeDeliveryReceipt(projectDir, profile, workstream, &receipt); err != nil {
		return err
	}
	if *jsonOut {
		return printJSONEnvelope("goal_deliver", mutationResult{
			Command:         "goal deliver",
			Status:          receipt.Status,
			Project:         projectDir,
			Session:         workstream,
			Profile:         profile,
			Namespace:       squadnamespace.Resolve(projectDir, profile, workstream),
			Role:            role,
			Handle:          member.Handle,
			DeliveryReceipt: &receipt,
		})
	}
	fmt.Printf("Delivered native /goal to %s pane %s (attempt %s).\n", role, paneID, receipt.AttemptID)
	return nil
}

func nativeGoalControlPrompt(goal string, t team.Team, profile, session, role string) string {
	args := []string{"/goal", "--goal", strconv.Quote(goal), "--session", session, "--profile", profile, "--mode", effectiveTeamExecutionMode(t)}
	if role != "" && role != "cto" {
		args = append(args, "--lead", role)
	}
	if target := strings.TrimSpace(t.TargetContract); target != "" {
		args = append(args, "--target-contract", target)
	}
	return strings.Join(args, " ")
}

func runGoalDraft(args []string) error {
	return runGoalDraftWithVersion(args, "dev")
}

func runGoalDraftWithVersion(args []string, version string) error {
	fs := flag.NewFlagSet("goal draft", flag.ContinueOnError)
	goalFlag := fs.String("goal", "", "high-level goal to turn into a setup draft")
	repoFlag := fs.String("repo", "", "GitHub repo owner/name for milestone lookup")
	milestoneFlag := fs.String("milestone", "", "GitHub milestone title to include issue titles and URLs")
	targetContractFlag := fs.String("target-contract", "", "target amq-squad contract version for compatibility checks (default: milestone if semver, else 2.10.0)")
	sessionFlag := fs.String("session", "", "AMQ workstream session name")
	profileFlag := fs.String("profile", "", "team profile name for the proposed setup")
	leadFlag := fs.String("lead", "cto", "operator-visible goal lead role")
	modeFlag := fs.String("mode", executionModeProjectLead, "execution mode: global_orchestrator, project_lead, project_team, or direct_lead_session")
	controlRootFlag := fs.String("control-root", "", "control-plane root directory (default: cwd)")
	targetProjectRootFlag := fs.String("target-project-root", "", "target project root directory (default: cwd)")
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
  amq-squad goal draft --goal TEXT [--repo owner/repo] [--milestone NAME] [--session NAME] [--profile NAME] [--lead ROLE] [--mode project_lead|project_team|direct_lead_session|global_orchestrator] [--visibility sibling-tabs|detached|current|plan] [--composition seeded|autonomous] [--max-agents N --max-total-spawns N --allowed-roles role,... --budget-turns N] [--codex-only] [--json]

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
		TargetContract:     strings.TrimSpace(*targetContractFlag),
		Session:            strings.TrimSpace(*sessionFlag),
		Profile:            strings.TrimSpace(*profileFlag),
		Lead:               strings.TrimSpace(*leadFlag),
		Mode:               strings.TrimSpace(*modeFlag),
		ControlRoot:        strings.TrimSpace(*controlRootFlag),
		TargetProjectRoot:  strings.TrimSpace(*targetProjectRootFlag),
		CodexOnly:          *codexOnly,
		RuntimeVersion:     version,
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
	TargetContract     string
	Session            string
	Profile            string
	Lead               string
	Mode               string
	ControlRoot        string
	TargetProjectRoot  string
	CodexOnly          bool
	RuntimeVersion     string
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
	lead := strings.TrimSpace(opts.Lead)
	if lead == "" {
		lead = "cto"
	}
	if err := validateProfileName(lead); err != nil {
		return goalDraftData{}, fmt.Errorf("invalid lead: %w", err)
	}
	mode, err := normalizeExecutionMode(opts.Mode)
	if err != nil {
		return goalDraftData{}, err
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
	targetContract := inferGoalTargetContract(opts.TargetContract, opts.Milestone)
	controlRoot := cleanRootOrDefault(opts.ControlRoot, cwdOrEmpty())
	targetRoot := cleanRootOrDefault(opts.TargetProjectRoot, cwdOrEmpty())
	data := goalDraftData{
		Goal:              opts.Goal,
		Repo:              opts.Repo,
		Milestone:         opts.Milestone,
		TargetContract:    targetContract,
		Session:           session,
		Profile:           profile,
		Lead:              lead,
		Mode:              mode,
		ControlRoot:       controlRoot,
		TargetProjectRoot: targetRoot,
		Namespace:         squadnamespace.Resolve("", profile, session),
		Composition:       composition,
		Visibility:        visibility,
		AutonomousPolicy:  autonomousPolicy,
		PreviewOnly:       true,
		CodexOnly:         opts.CodexOnly,
		IssueSources:      issues,
		Roster:            defaultGoalRoster(lead, opts.CodexOnly, len(issues)),
		Notes: []string{
			"Seeded composition remains the default; autonomous composition requires explicit opt-in and policy limits.",
			"This draft is preview-only and does not mutate team.json, briefs, task files, AMQ mailboxes, launch records, wake locks, or panes.",
			"Default visibility is sibling-tabs: launch the visible lead from an existing visible tmux pane with the generated native /goal prompt; workers remain behind spawn gates.",
			"Step 1 / Step 2 / Step 3: preview first, create or register the visible goal lead, then monitor the run through that lead.",
			"Execution mode is explicit: global_orchestrator monitors only; project_lead and project_team mutate through their project-root lead; direct_lead_session is an explicit exception.",
			"The top-level orchestrator dispatches to the visible goal lead; child agents stay implementation details unless an approval gate, blocker, release risk, or final evidence requires surfacing them.",
			"Leads must immediately surface any blocker or approval request to the operator/orchestrator-visible surface; never leave it only in an internal pane or hidden gate.",
			"When wake is unavailable, the parent orchestrator or NOC polls each visible lead's inbox, gates, and status on a cadence; one /goal maps to one visible lead.",
			"Visible lead binding is explicit: launch the visible lead with the generated native /goal prompt when possible; status falls back to AMQ task + active brief + task store until launch evidence exists.",
			"Generated prompts preserve team rules and custom role contracts across profile/session namespaces.",
			"Use --visibility detached only when a separate tmux session is intentional; use --visibility current for split panes in the current window; use --visibility plan when you want commands only.",
			"Merge, push, release, destructive filesystem actions, external communications, and provider side effects remain operator-owned.",
		},
	}
	data.OrchestratorPrompt = renderGoalOrchestratorPrompt(data)
	data.GoalBinding = goalBindingForDraft(data.Namespace, data.OrchestratorPrompt)
	data.Execution = executionContract(mode, controlRoot, targetRoot, profile, session, data.Namespace.ID, lead, data.GoalBinding.Mode, visibility, opts.RuntimeVersion, targetContract, goalVisibleMembers(mode, data.Roster, lead))
	data.BriefSkeleton = renderGoalBriefSkeleton(data)
	data.Tasks = defaultGoalTasks(data)
	data.SpawnGates = defaultGoalSpawnGates(data)
	data.Dispatches = defaultGoalDispatches(data)
	data.ApplyableMutations = defaultGoalMutations(data)
	return data, nil
}

func goalBindingForDraft(ns squadnamespace.Ref, command string) goalBindingData {
	binding := goalBindingForNamespace(ns)
	binding.Mode = "native_goal_pending"
	binding.NativeGoal = true
	binding.Verified = false
	binding.Source = "orchestrator-prompt"
	binding.NativeSource = "generated-/goal"
	binding.Command = command
	binding.Detail = "The generated visible-lead prompt is a native /goal command; status reports native_goal only after the lead launch record records that command, otherwise AMQ task + brief fallback remains explicit."
	return binding
}

func inferGoalTargetContract(explicit, milestone string) string {
	if strings.TrimSpace(explicit) != "" {
		return strings.TrimPrefix(strings.TrimSpace(explicit), "v")
	}
	if _, ok := parseSemverParts(milestone); ok {
		return strings.TrimPrefix(strings.TrimSpace(milestone), "v")
	}
	return "2.10.0"
}

func goalVisibleMembers(mode string, roster []goalRosterMember, lead string) []string {
	switch mode {
	case executionModeProjectTeam:
		out := make([]string, 0, len(roster))
		for _, member := range roster {
			if strings.TrimSpace(member.Role) != "" {
				out = append(out, member.Role)
			}
		}
		return out
	default:
		if strings.TrimSpace(lead) == "" {
			return nil
		}
		return []string{lead}
	}
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

func defaultGoalRoster(lead string, codexOnly bool, issueCount int) []goalRosterMember {
	binary := func(defaultBinary string) string {
		if codexOnly {
			return "codex"
		}
		return defaultBinary
	}
	leadReason := "Visible goal lead: owns Step 1 preview, Step 2 setup/register, Step 3 monitoring, final evidence, and operator escalation."
	if lead == "cto" {
		leadReason = "Lead orchestration, scope control, architecture, final sign-off, and operator escalation."
	}
	roster := []goalRosterMember{{
		Role:      lead,
		Handle:    lead,
		Binary:    "codex",
		Reason:    leadReason,
		CodexArgs: []string{"-c", "model_reasoning_effort=high"},
	}}
	appendWorker := func(member goalRosterMember) {
		for _, existing := range roster {
			if existing.Role == member.Role {
				return
			}
		}
		roster = append(roster, member)
	}
	appendWorker(goalRosterMember{
		Role:   "fullstack",
		Handle: "fullstack",
		Binary: binary("claude"),
		Reason: "Primary implementation owner for the drafted task plan.",
	})
	appendWorker(goalRosterMember{
		Role:      "senior-dev",
		Handle:    "senior-dev",
		Binary:    "codex",
		Reason:    "Independent implementation-shape and risk review before merge-ready claims.",
		CodexArgs: []string{"-c", "model_reasoning_effort=high"},
	})
	if issueCount > 3 {
		appendWorker(goalRosterMember{
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
	fmt.Fprintf(&b, "- Execution mode: %s. Mutable actor: %s. Implementation allowed: %t.\n", data.Execution.Mode, data.Execution.MutableActor, data.Execution.ImplementationAllowed)
	fmt.Fprintf(&b, "- Visible lead binding: %s (%s).\n", data.GoalBinding.Mode, data.GoalBinding.Source)
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
	b.WriteString("## Acceptance\n- Preview is reviewed before any setup mutation.\n- Spawn gates are explicit and durable.\n- Visible lead binding is declared as native /goal when available, otherwise AMQ task + active brief + task store.\n- Tasks, dispatches, review evidence, and final verification are recorded before merge-ready claims.\n")
	return b.String()
}

func defaultGoalTasks(data goalDraftData) []goalTaskPlan {
	if len(data.IssueSources) == 0 {
		return []goalTaskPlan{
			{ID: "t1", Title: "Confirm scope and acceptance from the goal", Assignee: data.Lead},
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
		if member.Role == data.Lead {
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
		if task.Assignee == data.Lead {
			continue
		}
		thread := canonicalP2PThread(data.Lead, task.Assignee)
		subject := "Task: " + task.Title
		body := task.Title + "\n\nPush progress, blockers, review requests, and DONE reports to " + data.Lead + " over AMQ. Treat this durable AMQ task as the source of truth."
		if task.SourceURL != "" {
			body += "\n\nSource: " + task.SourceURL
		}
		dispatches = append(dispatches, goalDispatchPlan{
			TaskID:  task.ID,
			Role:    task.Assignee,
			Thread:  thread,
			Subject: subject,
			Body:    body,
			Command: fmt.Sprintf("amq-squad dispatch --profile %s --session %s --role %s --thread %s --kind todo --subject %q --body %q", data.Profile, data.Session, task.Assignee, thread, subject, body),
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
	executionArgs := fmt.Sprintf(" --mode %s", data.Mode)
	if data.ControlRoot != "" {
		executionArgs += " --control-root " + shellQuote(data.ControlRoot)
	}
	if data.TargetProjectRoot != "" {
		executionArgs += " --target-project-root " + shellQuote(data.TargetProjectRoot)
	}
	if data.TargetContract != "" {
		executionArgs += " --target-contract " + shellQuote(data.TargetContract)
	}
	mutations := []goalCommandPlan{
		{
			Title:   "initialize profile",
			Command: fmt.Sprintf("amq-squad team init --profile %s --session %s --roles %s --binary %s --orchestrated --lead %s%s%s --dry-run", data.Profile, data.Session, strings.Join(roles, ","), strings.Join(binaries, ","), data.Lead, executionArgs, compositionArgs),
			Reason:  "Preview the proposed roster and orchestration metadata before writing team config.",
		},
		{
			Title:   "write brief",
			Command: fmt.Sprintf("amq-squad brief seed --profile %s --session %s --seed-from file:<approved-brief.md> --dry-run", data.Profile, data.Session),
			Reason:  "Preview the workstream brief before writing .amq-squad/briefs.",
		},
	}
	for _, task := range data.Tasks {
		cmd := fmt.Sprintf("amq-squad task add --profile %s --session %s --title %q --assign %s", data.Profile, data.Session, task.Title, task.Assignee)
		if len(task.DependsOn) > 0 {
			cmd += " --depends-on " + strings.Join(task.DependsOn, ",")
		}
		mutations = append(mutations, goalCommandPlan{Title: "add " + task.ID, Command: cmd, Reason: "Create the native task-store item after preview approval."})
	}
	mutations = append(mutations, goalVisibilityMutation(data))
	return mutations
}

func goalVisibilityMutation(data goalDraftData) goalCommandPlan {
	command := visibleLeadLaunchCommand(data, false)
	switch data.Visibility {
	case visibilityDetached:
		return goalCommandPlan{
			Title:   "launch detached visible lead",
			Command: command,
			Reason:  "Start the operator-visible lead with the native /goal prompt, then attach/open its pane deliberately before treating the run as observable.",
		}
	case visibilityCurrent:
		return goalCommandPlan{
			Title:   "launch visible lead in current pane",
			Command: command,
			Reason:  "Start the visible goal lead from the current operator pane with the native /goal prompt; workers remain gated/internal.",
		}
	case visibilityPlan:
		return goalCommandPlan{
			Title:   "preview visible lead launch",
			Command: visibleLeadLaunchCommand(data, true),
			Reason:  "Preview the native /goal lead launch command only; do not open a pane until the operator approves a concrete visibility mode.",
		}
	default:
		return goalCommandPlan{
			Title:   "launch visible lead",
			Command: command,
			Reason:  "Run from a visible tmux pane so the lead receives the native /goal prompt; workers are launched later only after their spawn gates are approved.",
		}
	}
}

func visibleLeadLaunchCommand(data goalDraftData, dryRun bool) string {
	lead := data.Roster[0]
	for _, member := range data.Roster {
		if member.Role == data.Lead {
			lead = member
			break
		}
	}
	args := []string{
		"agent", "up", lead.Binary,
		"--role", lead.Role,
		"--session", data.Session,
	}
	if dryRun {
		args = append(args, "--dry-run")
	}
	if root := launchRootForProfile(".", data.Profile, data.Session); root != "" {
		args = append(args, "--root", root)
	}
	args = append(args, "--team-workstream", "--team-home", ".")
	if data.Profile != "" && data.Profile != team.DefaultProfile {
		args = append(args, "--team-profile", data.Profile)
	}
	if lead.Handle != "" {
		args = append(args, "--me", lead.Handle)
	}
	if len(lead.CodexArgs) > 0 && normalizedAgentBinary(lead.Binary) == "codex" {
		args = append(args, "--codex-args="+joinedAgentArgs(lead.CodexArgs))
	}
	if data.OrchestratorPrompt != "" {
		args = append(args, "--", data.OrchestratorPrompt)
	}
	quoted := make([]string, 0, len(args)+1)
	quoted = append(quoted, "amq-squad")
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func renderGoalOrchestratorPrompt(data goalDraftData) string {
	args := []string{"/goal", "--goal", strconv.Quote(data.Goal), "--session", data.Session, "--profile", data.Profile, "--mode", data.Mode}
	if data.ControlRoot != "" {
		args = append(args, "--control-root", data.ControlRoot)
	}
	if data.TargetProjectRoot != "" {
		args = append(args, "--target-project-root", data.TargetProjectRoot)
	}
	if data.TargetContract != "" {
		args = append(args, "--target-contract", data.TargetContract)
	}
	if data.Lead != "" && data.Lead != "cto" {
		args = append(args, "--lead", data.Lead)
	}
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
	fmt.Fprintf(out, "# mode: %s\n", data.Mode)
	fmt.Fprintf(out, "# visibility: %s\n", data.Visibility)
	fmt.Fprintf(out, "# session: %s\n", data.Session)
	fmt.Fprintf(out, "# profile: %s\n", data.Profile)
	fmt.Fprintf(out, "# lead: %s\n", data.Lead)
	fmt.Fprintf(out, "# namespace: %s\n", data.Namespace.ID)
	if data.ControlRoot != "" {
		fmt.Fprintf(out, "# control_root: %s\n", data.ControlRoot)
	}
	if data.TargetProjectRoot != "" {
		fmt.Fprintf(out, "# target_project_root: %s\n", data.TargetProjectRoot)
	}
	if data.Execution.MutableActor != "" {
		fmt.Fprintf(out, "# mutable_actor: %s\n", data.Execution.MutableActor)
	}
	fmt.Fprintf(out, "# implementation_allowed: %t\n", data.Execution.ImplementationAllowed)
	if data.Execution.ModeError != "" {
		fmt.Fprintf(out, "# mode_error: %s\n", data.Execution.ModeError)
	}
	if data.Execution.VersionCompatibility.Detail != "" {
		fmt.Fprintf(out, "# version_compatibility: %s\n", data.Execution.VersionCompatibility.Detail)
	}
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
	fmt.Fprintln(out, "## Execution Boundary")
	fmt.Fprintf(out, "- Mode: %s\n", data.Execution.Mode)
	fmt.Fprintf(out, "- Control root: %s\n", data.Execution.ControlRoot)
	fmt.Fprintf(out, "- Target project root: %s\n", data.Execution.TargetProjectRoot)
	fmt.Fprintf(out, "- Visible lead: %s\n", data.Execution.VisibleLead)
	fmt.Fprintf(out, "- Visible team members: %s\n", strings.Join(data.Execution.VisibleTeamMembers, ", "))
	fmt.Fprintf(out, "- Mutable actor: %s\n", data.Execution.MutableActor)
	fmt.Fprintf(out, "- Implementation allowed: %t\n", data.Execution.ImplementationAllowed)
	fmt.Fprintf(out, "- Boundary: %s\n", data.Execution.Boundary)
	if data.Execution.ModeError != "" {
		fmt.Fprintf(out, "- Mode error: %s\n", data.Execution.ModeError)
	}
	fmt.Fprintf(out, "- Version compatibility: %s\n", data.Execution.VersionCompatibility.Detail)
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
