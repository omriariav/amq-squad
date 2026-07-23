package wizard

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/omriariav/amq-squad/v2/internal/agentcatalog"
)

type bubbleStage int

const (
	stageScope bubbleStage = iota
	stageProject
	stageGlobalRoot
	stageGlobalAgent
	stageGlobalModel
	stageGlobalModelCustom
	stageGlobalEffort
	stageGlobalEffortCustom
	stageGlobalNativeArgs
	stageGlobalWindow
	stageProfile
	stageNewProfile
	stageExistingSession
	// stageTemplateSession: type any workstream directly for an unpinned
	// template profile (#451) — it has no pin to conflict with.
	stageTemplateSession
	// stageCloneSession / stageCloneProfile: the #523 clone-offer escape
	// hatch from stageExistingSession — type the new workstream session, then
	// the new profile name, cloning the picked profile's roster into it.
	stageCloneSession
	stageCloneProfile
	stageSession
	stageExistingOverride
	stageExistingModel
	stageExistingModelCustom
	stageExistingEffort
	stageExistingEffortCustom
	stageResumeMember
	stageResumeModelCustom
	stageResumeEffort
	stageResumeEffortCustom
	stageRoles
	stageRoleBinary
	stageRoleModel
	stageRoleModelCustom
	stageRoleEffort
	stageRoleEffortCustom
	stageLead
	stageLeadMode
	stageLaunchShape
	stageStagedRoles
	stageToolPolicy
	stageOperator
	stageSelfOperatorAllow
	stageOperatorNotifications
	stageTopology
	stageLayoutPreset
	stageLauncherPane
	stageGoal
	stageSeed
	stageResumeBrief
	stageResumeGoal
	stageConfirm
)

type BubbleResult struct {
	Spec      Spec
	Cancelled bool
}

type bubbleSnapshot struct {
	spec                Spec
	ctx                 ProjectContext
	stage               bubbleStage
	cursor              int
	existingIndex       int
	roleOrder           []string
	roleIndex           int
	inputValue          string
	newProfilePrefill   string
	newSessionPrefill   string
	clonePendingSession string
}

// BubbleModel is the full-screen adapter over the same Spec and project facts
// used by RunNumbered. It owns navigation only; it cannot preview or launch.
type BubbleModel struct {
	opts NumberedOptions
	spec Spec
	ctx  ProjectContext

	stage               bubbleStage
	cursor              int
	existingIndex       int
	roleOrder           []string
	roleIndex           int
	input               textinput.Model
	history             []bubbleSnapshot
	newProfilePrefill   string
	newSessionPrefill   string
	clonePendingSession string

	width     int
	height    int
	err       error
	done      bool
	cancelled bool
}

var (
	wizardAccent = lipgloss.AdaptiveColor{Light: "#416B88", Dark: "#78A9C4"}
	wizardSignal = lipgloss.AdaptiveColor{Light: "#8A641E", Dark: "#D6A550"}
	wizardText   = lipgloss.AdaptiveColor{Light: "#20272D", Dark: "#D8DEE4"}
	wizardMuted  = lipgloss.AdaptiveColor{Light: "#66717A", Dark: "#7D8790"}
	wizardBorder = lipgloss.AdaptiveColor{Light: "#AAB5BC", Dark: "#4E5A63"}

	styleWizardTitle = lipgloss.NewStyle().Bold(true).Foreground(wizardAccent)
	styleWizardStep  = lipgloss.NewStyle().Bold(true).Foreground(wizardText)
	styleWizardMuted = lipgloss.NewStyle().Foreground(wizardMuted)
	styleWizardPick  = lipgloss.NewStyle().Bold(true).Foreground(wizardSignal)
	styleWizardPanel = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(wizardBorder).Padding(1, 2)
)

func NewBubbleModel(opts NumberedOptions) (BubbleModel, error) {
	s := opts.Defaults.Clone()
	ctx := ProjectContext{Project: s.Project, SessionSuggestion: s.Session}
	var err error
	if opts.StartAtProfile && opts.InspectProject != nil {
		ctx, err = opts.InspectProject(s.Project)
		if err != nil {
			return BubbleModel{}, err
		}
		if strings.TrimSpace(ctx.Project) != "" {
			s.Project = ctx.Project
		}
		if strings.TrimSpace(ctx.SessionSuggestion) == "" {
			ctx.SessionSuggestion = s.Session
		}
	}
	input := textinput.New()
	input.CharLimit = 512
	input.Width = 64
	startStage := stageScope
	if opts.StartAtProfile {
		startStage = stageProfile
	}
	m := BubbleModel{
		opts:              opts,
		spec:              s,
		ctx:               ctx,
		stage:             startStage,
		existingIndex:     -1,
		input:             input,
		width:             88,
		height:            28,
		newSessionPrefill: s.Session,
	}
	if strings.TrimSpace(s.Profile) != "" && findProfile(ctx.Profiles, s.Profile) < 0 {
		m.newProfilePrefill = s.Profile
	}
	m.configureStage()
	if opts.StartAtProfile {
		m.err = fmt.Errorf("%s", defaultString(opts.RestartMessage, "The selected profile or run changed while the wizard was open. Review the refreshed facts before continuing."))
	}
	return m, nil
}

func RunBubbleTea(in io.Reader, out io.Writer, opts NumberedOptions) (BubbleResult, error) {
	m, err := NewBubbleModel(opts)
	if err != nil {
		return BubbleResult{}, err
	}
	program := tea.NewProgram(m, tea.WithInput(in), tea.WithOutput(out), tea.WithAltScreen())
	final, err := program.Run()
	if err != nil {
		return BubbleResult{}, err
	}
	result, ok := final.(BubbleModel)
	if !ok {
		return BubbleResult{}, fmt.Errorf("wizard returned unexpected model %T", final)
	}
	return BubbleResult{Spec: result.spec, Cancelled: result.cancelled}, nil
}

func (m BubbleModel) Init() tea.Cmd {
	if m.isTextStage() {
		return textinput.Blink
	}
	return nil
}

func (m BubbleModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.Width = maxInt(24, minInt(72, msg.Width-12))
		return m, nil
	case tea.KeyMsg:
		key := msg.String()
		if key == "ctrl+c" {
			m.cancelled = true
			return m, tea.Quit
		}
		if key == "esc" {
			return m.back()
		}
		if m.isTextStage() {
			if key == "enter" {
				return m.commitText()
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
		if key == "q" {
			m.cancelled = true
			return m, tea.Quit
		}
		choices := m.choices()
		switch key {
		case "up", "k":
			if len(choices) > 0 {
				m.cursor = (m.cursor - 1 + len(choices)) % len(choices)
			}
		case "down", "j", "tab":
			if len(choices) > 0 {
				m.cursor = (m.cursor + 1) % len(choices)
			}
		case "enter", " ":
			return m.commitChoice()
		}
	}
	return m, nil
}

func (m BubbleModel) View() string {
	if m.cancelled {
		return "Wizard cancelled. Nothing changed.\n"
	}
	if m.done {
		return "Preview choices collected.\n"
	}
	width := maxInt(48, minInt(96, m.width-4))
	var b strings.Builder
	b.WriteString(styleWizardTitle.Render("amq-squad · run start control deck"))
	b.WriteString("\n")
	b.WriteString(m.renderRail())
	b.WriteString("\n\n")
	b.WriteString(styleWizardPanel.Width(width - 6).Render(m.renderPanel()))
	b.WriteString("\n\n")
	b.WriteString(styleWizardMuted.Render(m.footer()))
	if m.err != nil {
		b.WriteString("\n" + styleWizardPick.Render("Check: "+m.err.Error()))
	}
	return b.String()
}

func (m BubbleModel) renderRail() string {
	current := m.phaseIndex()
	labels := m.phaseLabels()
	parts := make([]string, 0, len(labels))
	for i, label := range labels {
		marker := "○"
		style := styleWizardMuted
		if i < current {
			marker = "●"
		}
		if i == current {
			marker = "◆"
			style = styleWizardPick
		}
		parts = append(parts, style.Render(fmt.Sprintf("%s %d %s", marker, i+1, label)))
	}
	return strings.Join(parts, styleWizardMuted.Render("  ─  "))
}

func (m BubbleModel) renderPanel() string {
	var b strings.Builder
	b.WriteString(styleWizardStep.Render(m.title()))
	if note := m.note(); note != "" {
		b.WriteString("\n" + styleWizardMuted.Render(note))
	}
	b.WriteString("\n\n")
	if m.isTextStage() {
		b.WriteString(m.input.View())
		return b.String()
	}
	if m.stage == stageConfirm {
		b.WriteString(m.summary())
		b.WriteString("\n\n")
	}
	choices := m.choices()
	for i, item := range choices {
		cursor := "  "
		line := item.label
		if item.disabled {
			cursor = "× "
			line = styleWizardMuted.Render(line)
		}
		if i == m.cursor {
			cursor = "› "
			if item.disabled {
				line = styleWizardMuted.Render(line)
			} else {
				line = styleWizardPick.Render(line)
			}
		}
		b.WriteString(cursor + line + "\n")
	}
	if m.stage == stageTopology && len(choices) > 0 {
		selected := choices[minInt(m.cursor, len(choices)-1)].value
		b.WriteString("\n" + styleWizardMuted.Render(topologyConsequence(selected)) + "\n\n")
		b.WriteString(TopologyPreview(selected))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m BubbleModel) title() string {
	switch m.stage {
	case stageScope:
		return "What do you want to run?"
	case stageProject:
		return "Which project owns this squad?"
	case stageGlobalRoot:
		return "Where should the global orchestrator run?"
	case stageGlobalAgent:
		return "Which agent should run the global orchestrator?"
	case stageGlobalModel:
		return "Model for the global orchestrator"
	case stageGlobalModelCustom:
		return "Custom model for the global orchestrator"
	case stageGlobalEffort:
		return "Effort for the global orchestrator"
	case stageGlobalEffortCustom:
		return "Custom effort for the global orchestrator"
	case stageGlobalNativeArgs:
		return "Extra native arguments"
	case stageGlobalWindow:
		return "Window name"
	case stageProfile:
		return "Use an existing team setup or create a new one?"
	case stageNewProfile:
		return "Name the new profile"
	case stageExistingSession:
		return "Which existing run do you want?"
	case stageTemplateSession:
		return "Workstream session for this launch"
	case stageCloneSession:
		return "Name the new workstream session"
	case stageCloneProfile:
		return "Name the new profile"
	case stageSession:
		return "Name the new session"
	case stageExistingOverride:
		return "Launch-time override · " + m.currentMember().Role
	case stageExistingModel:
		return "Model override · " + m.currentMember().Role
	case stageExistingModelCustom:
		return "Custom model override · " + m.currentMember().Role
	case stageExistingEffort:
		return "Effort override · " + m.currentMember().Role
	case stageExistingEffortCustom:
		return "Custom effort override · " + m.currentMember().Role
	case stageResumeMember:
		member := m.currentResumeMember()
		if member.Action == MemberActionFresh {
			return "Model for fresh " + member.Role
		}
		return "Resume action · " + member.Role
	case stageResumeModelCustom:
		return "Custom model for fresh " + m.currentResumeMember().Role
	case stageResumeEffort:
		return "Effort for fresh " + m.currentResumeMember().Role
	case stageResumeEffortCustom:
		return "Custom effort for fresh " + m.currentResumeMember().Role
	case stageRoles:
		return "Choose fresh-roster roles"
	case stageRoleBinary:
		return "Binary · " + m.currentRole()
	case stageRoleModel:
		return "Model · " + m.currentRole()
	case stageRoleModelCustom:
		return "Custom model · " + m.currentRole()
	case stageRoleEffort:
		return "Effort · " + m.currentRole()
	case stageRoleEffortCustom:
		return "Custom effort · " + m.currentRole()
	case stageLead:
		return "Choose the visible lead"
	case stageLeadMode:
		return "Choose the lead posture"
	case stageLaunchShape:
		return "Choose the explicit initial launch shape"
	case stageStagedRoles:
		return "Name roles staged for later"
	case stageToolPolicy:
		return "Choose per-agent tool policy"
	case stageOperator:
		return "Choose the operator interaction contract"
	case stageOperatorNotifications:
		return "Choose the independent notification add-on"
	case stageSelfOperatorAllow:
		return "Explicit self-operator allowlist"
	case stageTopology:
		return "Choose where agents appear"
	case stageLayoutPreset:
		return "Choose the deterministic layout preset"
	case stageLauncherPane:
		return "Choose what happens to this launcher pane"
	case stageGoal:
		return "Goal text or accepted brief binding"
	case stageSeed:
		return "Brief source"
	case stageResumeBrief:
		return "Brief preserved for resume"
	case stageResumeGoal:
		return "Recorded lead goal"
	case stageConfirm:
		return "Review answers before canonical preview"
	default:
		return "Run start wizard"
	}
}

func (m BubbleModel) note() string {
	switch m.stage {
	case stageScope:
		return "Project runs use one repository's profiles and sessions. Global/NOC starts one coordinator and does not own project wake mailboxes. " + m.opts.TerminalContext.Summary()
	case stageProject:
		if m.ctx.OriginSlug != "" {
			return "Detected origin " + m.ctx.OriginSlug + ". The nearest git root is the default."
		}
		return "Choose the repository root that owns .amq-squad. The nearest git root is suggested; no network access is used."
	case stageGlobalRoot:
		return "This is a neutral control root, not a project profile or session."
	case stageGlobalAgent:
		return "The global orchestrator coordinates project namespaces but owns no project wake mailbox."
	case stageGlobalModel:
		return "Catalog choices are suggestions; Custom accepts any model name."
	case stageGlobalModelCustom:
		return "Enter any model name, or leave blank for automatic."
	case stageGlobalEffort:
		return "Catalog choices are suggestions; Custom accepts any effort tier."
	case stageGlobalEffortCustom:
		return "Unknown tiers are passed through exactly and may still be rejected by the underlying binary."
	case stageGlobalNativeArgs:
		return "These arguments pass to the selected agent; effort is controlled separately."
	case stageGlobalWindow:
		return "This names the tmux window used by the global orchestrator."
	case stageProfile:
		return "An existing profile keeps its roster, lead, and operator contract. A new profile lets you choose them."
	case stageExistingSession:
		return "These sessions belong to the selected profile. To launch a different session, clone this roster into a new profile."
	case stageTemplateSession:
		return "This is an unpinned template profile: it has no session pin, so it can launch for any new workstream directly."
	case stageCloneSession:
		return "This becomes the new profile's launch session; the source profile keeps its own pin unchanged."
	case stageCloneProfile:
		return "This name identifies the cloned roster. Members, binaries, models, effort, lead, and trust carry over from the source; nothing is written until the final command is approved."
	case stageNewProfile:
		return "This name identifies a reusable team setup. Nothing is written until the final command is approved."
	case stageSession:
		return "This is the new run's mailbox, brief, task, and launch-history namespace."
	case stageExistingOverride:
		member := m.currentMember()
		return fmt.Sprintf("Profile values stay untouched: model=%s · effort=%s", defaultString(member.Model, "automatic"), defaultString(member.Effort, "automatic"))
	case stageExistingModel:
		return "Keep uses the stored profile value; a pick applies to this launch only."
	case stageExistingModelCustom:
		return "Enter a model for this launch only, or leave blank to keep the profile value."
	case stageExistingEffort:
		return "Catalog choices are suggestions. This replaces only the native effort args in the in-memory launch plan."
	case stageExistingEffortCustom:
		return "Enter any effort tier for this launch only; amq-squad warns but passes unknown tiers through."
	case stageResumeMember:
		member := m.currentResumeMember()
		switch member.Action {
		case MemberActionLive:
			return "Already live: resume keeps this member running and offers no model or effort override."
		case MemberActionRestore:
			return fmt.Sprintf("Saved launch is read-only: binary=%s · model=%s · effort=%s · saved extra args=%s", defaultString(member.SavedBinary, member.Binary), defaultString(member.SavedModel, "automatic"), defaultString(member.SavedEffort, "automatic"), FormatSavedNativeArgs(member.SavedNativeArgs))
		default:
			return "Only launch-fresh members may receive model or effort overrides."
		}
	case stageResumeModelCustom:
		return "This model applies only to the fresh member; leave blank to keep the stored profile model."
	case stageResumeEffort:
		return "This effort applies only to the launch-fresh member; catalog choices are advisory."
	case stageResumeEffortCustom:
		return "Unknown tiers pass through exactly; restored and live members remain immutable."
	case stageRoles:
		return "Comma-separated role ids. Defaults are shown in the field."
	case stageRoleModel:
		if note := m.roleRecommendationNote(); note != "" {
			return note
		}
		return "Models pass through to the selected binary verbatim; custom accepts any name."
	case stageRoleModelCustom:
		return "Enter any model name, or leave blank for automatic."
	case stageRoleEffort:
		if note := m.roleRecommendationNote(); note != "" {
			return note
		}
		return "Catalog choices are suggestions; Custom accepts any tier for the selected binary."
	case stageRoleEffortCustom:
		return "Enter any effort tier, or leave blank for automatic."
	case stageLeadMode:
		if strings.TrimSpace(m.spec.LeadMode) == "" {
			if mode, rationale := RecommendLeadMode(len(m.roleOrder)); mode == "planner" {
				return "Recommended: planner -- " + rationale
			}
		}
		return "Choose the lead posture"
	case stageToolPolicy:
		return "Recommended keeps the visible lead broad and uses each built-in role's minimum profile. Full for all is explicit and warns about duplicated MCP context plus memory/concurrency pressure."
	case stageLaunchShape:
		note := "Working-team-together launches the displayed initial roster. Lead-only-staged launches only the lead; every later role requires its own durable spawn gate. Orchestration never chooses this implicitly."
		if recommend, _, rationale := RecommendWorktreeIsolation(m.roleOrder, m.spec.Lead, m.spec.LeadMode); recommend {
			note = "Worktree isolation recommended: " + rationale + " " + note
		}
		return note
	case stageStagedRoles:
		return "Comma-separated roles that have contracts prepared but are absent from the initial profile and bootstrap. Leave blank when nobody is staged."
	case stageTopology:
		return "The diagram is the topology that the canonical visibility flag selects. " + topologyDiagnostic(m.opts.TerminalContext, m.spec.Visibility)
	case stageLayoutPreset:
		return "Preset mappings use exact tmux pane/window IDs; display names are never control targets."
	case stageLauncherPane:
		if m.spec.ExternalLead {
			return "This pane is the external lead and is always kept."
		}
		if m.spec.Visibility == "detached" {
			return "Detached runs keep the launcher as the visible control point."
		}
		return "Close is scheduled only after successful spawn, goal delivery, and final output."
	case stageOperator:
		if m.existingIndex >= 0 {
			mode := defaultString(m.spec.OperatorMode, "unspecified")
			return "Existing profile contract is authoritative: " + mode + " · " + operatorContractSummary(mode) + ". Change it with 'amq-squad team operator set', then relaunch."
		}
		return "Unavailable capability rows stay visible so the future contract is explicit."
	case stageOperatorNotifications:
		if m.existingIndex >= 0 {
			return fmt.Sprintf("Existing profile notification policy is authoritative: %t", m.spec.OperatorNotifications)
		}
		return "Attention-only notifications never approve gates or send pane input."
	case stageSelfOperatorAllow:
		return "No gate is preselected. Selecting merge opts in explicitly; spawn, release, tag, publish, external send, and destructive filesystem remain human-only. A second verified actor must execute the merge."
	case stageConfirm:
		if m.spec.Backend == BackendResume {
			return "Enter runs the resume preview. Execution is a later default-No Resume now? prompt."
		}
		return "Enter collects these answers and runs preview. Live launch is a later default-No prompt."
	case stageSeed:
		return "Accepted forms: file:path · issue:393 · gh:owner/repo#393"
	case stageResumeBrief:
		return fmt.Sprintf("Resume keeps the existing brief unchanged. Path=%s · goal excerpt=%s · seed=%s", displayValue(m.spec.BriefPath), GoalExcerpt(m.spec.BriefGoal), displayValue(m.spec.BriefSeed))
	case stageResumeGoal:
		if m.spec.ResumeGoalPlan.Eligible {
			return "Default is No. Yes creates one new claim-once attempt only after the restored lead and original binding/claim evidence are revalidated. Goal: " + GoalExcerpt(m.spec.ResumeGoalPlan.Goal)
		}
		return fmt.Sprintf("Redelivery is %s: %s", defaultString(m.spec.ResumeGoalPlan.Action, "unavailable"), m.spec.ResumeGoalPlan.Reason)
	}
	return ""
}

func (m BubbleModel) footer() string {
	if m.isTextStage() {
		return "enter continue · esc back · ctrl+c cancel"
	}
	return "↑/↓ choose · enter continue · esc back · q cancel"
}

func (m BubbleModel) summary() string {
	if strings.EqualFold(m.spec.Scope, "global") {
		parts := []string{
			"Scope     global / NOC orchestrator",
			"Root      " + m.spec.GlobalRoot,
			"Agent     " + defaultString(m.spec.GlobalAgent, "claude"),
			"Model     " + defaultString(m.spec.GlobalModel, "automatic"),
			"Effort    " + defaultString(m.spec.GlobalEffort, "automatic"),
		}
		if strings.EqualFold(m.spec.GlobalAgent, "codex") {
			parts = append(parts, "Native    "+displayValue(m.spec.GlobalCodexArgs))
		} else {
			parts = append(parts, "Native    "+displayValue(m.spec.GlobalClaudeArgs))
		}
		parts = append(parts,
			"Window    "+defaultString(m.spec.GlobalWindow, "global-orch"),
			"Backend   "+string(BackendGlobalStart),
			"NOC       polls explicit project/profile/session namespaces; owns no wake mailbox",
		)
		if previewCommand, liveCommand, err := m.spec.CommandForms(); err == nil {
			parts = append(parts, "Preview   "+previewCommand, "Live      "+liveCommand)
		} else {
			parts = append(parts, "Commands  unavailable: "+err.Error())
		}
		parts = append(parts, effortCatalogWarnings(m.spec, m.ctx)...)
		return strings.Join(parts, "\n")
	}
	parts := []string{
		"Project   " + m.spec.Project,
		"Profile   " + defaultString(m.spec.Profile, "default"),
		"Session   " + m.spec.Session,
		"Backend   " + defaultString(string(m.spec.Backend), "run_start"),
	}
	if m.spec.Roles != "" {
		parts = append(parts, "Roster    "+m.spec.Roles, "Lead      "+m.spec.Lead+" · "+m.spec.LeadMode)
		parts = append(parts, "Shape     "+defaultString(m.spec.LaunchShape, "legacy/unspecified"), "Staged    "+displayValue(m.spec.StagedRoles))
	} else {
		parts = append(parts, "Roster    existing profile (authoritative)")
	}
	if m.spec.ToolProfile != "" {
		parts = append(parts, "Tools     "+defaultString(m.spec.ToolPolicyMode, "recommended")+" · "+m.spec.ToolProfile)
		if countFullToolProfiles(m.spec.ToolProfile) >= 2 {
			parts = append(parts, "WARNING   2+ full workers duplicate MCP/plugin context and increase memory/concurrency pressure")
		}
	}
	if m.spec.Model != "" {
		parts = append(parts, "Models    "+m.spec.Model+" (launch only)")
	}
	if m.spec.Effort != "" {
		parts = append(parts, "Effort    "+m.spec.Effort+" (launch only)")
	}
	if m.spec.Backend == BackendResume {
		parts = append(parts, fmt.Sprintf("Records   %d · restore-existing=%t", m.spec.RecordCount, m.spec.RecordCount > 0))
		parts = append(parts, fmt.Sprintf("Goal plan %s · eligible=%t · selected=%t", defaultString(m.spec.ResumeGoalPlan.Action, "unavailable"), m.spec.ResumeGoalPlan.Eligible, m.spec.RedeliverGoal))
		for _, member := range m.spec.ResumeMembers {
			value := fmt.Sprintf("  %-10s %s · model=%s · effort=%s", member.Role, member.Action, defaultString(member.Model, "automatic"), defaultString(member.Effort, "automatic"))
			if member.Action == MemberActionRestore {
				value = fmt.Sprintf("  %-10s restore · saved model=%s effort=%s extra args=%s", member.Role, defaultString(member.SavedModel, "automatic"), defaultString(member.SavedEffort, "automatic"), FormatSavedNativeArgs(member.SavedNativeArgs))
			}
			parts = append(parts, value)
		}
	}
	parts = append(parts, "Operator  "+defaultString(m.spec.OperatorMode, "unspecified")+" · "+operatorContractSummary(m.spec.OperatorMode))
	parts = append(parts, fmt.Sprintf("Alerts    attention-only notifications=%t", m.spec.OperatorNotifications))
	parts = append(parts, "Terminal  "+topologyDiagnostic(m.opts.TerminalContext, m.spec.Visibility))
	parts = append(parts, "Topology  "+m.spec.Visibility)
	if m.spec.LayoutPreset != "" {
		parts = append(parts, "Layout    "+m.spec.LayoutPreset)
	}
	if m.spec.Backend == BackendResume {
		parts = append(parts, "Launcher  kept · resume opens only missing members")
	} else {
		parts = append(parts, "Launcher  "+defaultString(m.spec.LauncherPane, "legacy keep"))
	}
	goal, seed := m.spec.Goal, m.spec.SeedFrom
	if m.spec.Backend == BackendResume {
		goal, seed = m.spec.BriefGoal, m.spec.BriefSeed
		parts = append(parts, "Brief     "+displayValue(m.spec.BriefPath)+" (preserved)")
	}
	parts = append(parts, "Goal      "+GoalExcerpt(goal), "Seed      "+displayValue(seed))
	if m.spec.Backend != BackendResume {
		parts = append(parts, "Binding   "+m.spec.GoalBindingReview())
	}
	if previewCommand, liveCommand, err := m.spec.CommandForms(); err == nil {
		parts = append(parts, "Preview   "+previewCommand, "Live      "+liveCommand)
	} else {
		parts = append(parts, "Commands  unavailable: "+err.Error())
	}
	parts = append(parts, effortCatalogWarnings(m.spec, m.ctx)...)
	parts = append(parts, "", TopologyPreview(m.spec.Visibility))
	return strings.Join(parts, "\n")
}

func (m BubbleModel) isTextStage() bool {
	switch m.stage {
	case stageProject, stageGlobalRoot, stageGlobalModelCustom, stageGlobalEffortCustom, stageGlobalNativeArgs, stageGlobalWindow, stageNewProfile, stageSession, stageTemplateSession, stageCloneSession, stageCloneProfile, stageExistingModelCustom, stageExistingEffortCustom, stageResumeModelCustom, stageResumeEffortCustom, stageRoles, stageRoleModelCustom, stageRoleEffortCustom, stageLead, stageStagedRoles, stageGoal, stageSeed:
		return true
	default:
		return false
	}
}

func (m *BubbleModel) configureStage() {
	m.err = nil
	m.cursor = 0
	m.input.Blur()
	value := ""
	placeholder := ""
	switch m.stage {
	case stageProject:
		value = m.spec.Project
	case stageGlobalRoot:
		value = m.spec.GlobalRoot
	case stageGlobalModelCustom:
		value = m.spec.GlobalModel
		placeholder = "leave blank for automatic"
	case stageGlobalEffortCustom:
		value = m.spec.GlobalEffort
		placeholder = "leave blank for automatic"
	case stageGlobalNativeArgs:
		if strings.EqualFold(m.spec.GlobalAgent, "codex") {
			value = m.spec.GlobalCodexArgs
		} else {
			value = m.spec.GlobalClaudeArgs
		}
		placeholder = "optional"
	case stageGlobalWindow:
		value = defaultString(m.spec.GlobalWindow, "global-orch")
	case stageNewProfile:
		value = defaultString(m.newProfilePrefill, defaultString(m.ctx.NewProfileSuggestion, "squad-"+defaultString(m.ctx.SessionSuggestion, "project")))
	case stageSession:
		value = defaultString(m.newSessionPrefill, m.ctx.SessionSuggestion)
	case stageTemplateSession:
		value = defaultString(m.ctx.SessionSuggestion, "issue-1")
	case stageCloneSession:
		value = defaultString(m.ctx.SessionSuggestion, "issue-1")
	case stageCloneProfile:
		source := ""
		if m.existingIndex >= 0 && m.existingIndex < len(m.ctx.Profiles) {
			source = m.ctx.Profiles[m.existingIndex].Name
		}
		value = source + "-" + m.clonePendingSession
	case stageExistingModelCustom:
		value = defaultString(parseAssignments(m.spec.Model)[m.currentMember().Role], m.currentMember().Model)
		placeholder = "leave blank to keep " + defaultString(m.currentMember().Model, "automatic")
	case stageExistingEffortCustom:
		value = defaultString(parseAssignments(m.spec.Effort)[m.currentMember().Role], m.currentMember().Effort)
		placeholder = "leave blank to keep " + defaultString(m.currentMember().Effort, effortAutomatic)
	case stageResumeModelCustom:
		value = defaultString(parseAssignments(m.spec.Model)[m.currentResumeMember().Role], m.currentResumeMember().Model)
		placeholder = "leave blank to keep " + defaultString(m.currentResumeMember().Model, "automatic")
	case stageResumeEffortCustom:
		value = defaultString(parseAssignments(m.spec.Effort)[m.currentResumeMember().Role], m.currentResumeMember().Effort)
		placeholder = "leave blank to keep " + defaultString(m.currentResumeMember().Effort, effortAutomatic)
	case stageRoles:
		value = defaultString(m.spec.Roles, "cto,senior-dev,qa")
	case stageRoleModelCustom:
		value = parseAssignments(m.spec.Model)[m.currentRole()]
		placeholder = "leave blank for automatic"
	case stageRoleEffortCustom:
		value = parseAssignments(m.spec.Effort)[m.currentRole()]
		placeholder = "leave blank for automatic"
	case stageLead:
		value = defaultString(m.spec.Lead, defaultLead(m.roleOrder))
	case stageStagedRoles:
		value = m.spec.StagedRoles
		placeholder = "optional"
	case stageGoal:
		value = m.spec.Goal
		placeholder = "blank only with a real accepted brief"
	case stageSeed:
		value = m.spec.SeedFrom
		placeholder = "optional"
	}
	if m.isTextStage() {
		m.input.SetValue(value)
		m.input.Placeholder = placeholder
		m.input.CursorEnd()
		m.input.Focus()
	}
	m.cursor = m.defaultCursor()
}

func (m BubbleModel) choices() []choice {
	switch m.stage {
	case stageScope:
		return []choice{{value: "project", label: "Project squad"}, {value: "global", label: "Global / NOC orchestrator"}}
	case stageGlobalAgent:
		return []choice{{value: "claude", label: "Claude"}, {value: "codex", label: "Codex"}}
	case stageGlobalModel:
		return modelChoicesCatalog(m.spec.GlobalAgent, m.ctx.Catalog)
	case stageGlobalEffort:
		return effortChoicesCatalog(m.spec.GlobalAgent, m.ctx.Catalog)
	case stageProfile:
		choices := make([]choice, 0, len(m.ctx.Profiles)+1)
		for _, profile := range m.ctx.Profiles {
			trailer := "roster and contract stay authoritative"
			if isTemplateProfile(profile) {
				trailer = "roster and contract stay authoritative; pick any workstream session"
			}
			choices = append(choices, choice{value: profile.Name, label: fmt.Sprintf("%s · %d members · %s · %s", profile.Name, profile.MemberCount, profileRunSummary(profile, m.ctx.SessionSuggestion), trailer)})
		}
		choices = append(choices, choice{value: "__create__", label: "Create a new profile · choose a fresh roster and contract"})
		return choices
	case stageExistingSession:
		if m.existingIndex < 0 || m.existingIndex >= len(m.ctx.Profiles) {
			return nil
		}
		sessions := profileSessions(m.ctx.Profiles[m.existingIndex], m.ctx.SessionSuggestion)
		choices := make([]choice, 0, len(sessions)+1)
		for _, session := range sessions {
			choices = append(choices, choice{value: session.Name, label: session.Label()})
		}
		choices = append(choices, choice{value: cloneRosterChoiceValue, label: "Clone this roster into a new profile for a different session"})
		return choices
	case stageExistingOverride:
		return []choice{{value: "keep", label: "Keep profile model and effort"}, {value: "override", label: "Override this role for this launch only"}}
	case stageExistingModel:
		return existingOverrideModelChoicesCatalog(m.currentMember(), m.ctx.Catalog)
	case stageExistingEffort:
		return existingOverrideEffortChoices(m.currentMember(), m.ctx.Catalog)
	case stageResumeMember:
		member := m.currentResumeMember()
		if member.Action == MemberActionFresh {
			return existingOverrideModelChoicesCatalog(MemberSummary{Role: member.Role, Binary: member.Binary, Model: member.Model, Effort: member.Effort}, m.ctx.Catalog)
		}
		return []choice{{value: "continue", label: fmt.Sprintf("Continue · %s remains %s", member.Role, member.Action)}}
	case stageResumeEffort:
		member := m.currentResumeMember()
		return existingOverrideEffortChoices(MemberSummary{Role: member.Role, Binary: member.Binary, Model: member.Model, Effort: member.Effort}, m.ctx.Catalog)
	case stageRoleBinary:
		return []choice{{value: "codex", label: "Codex"}, {value: "claude", label: "Claude"}}
	case stageRoleModel:
		return modelChoicesCatalog(parseAssignments(m.spec.Binary)[m.currentRole()], m.ctx.Catalog)
	case stageRoleEffort:
		return effortChoicesCatalog(parseAssignments(m.spec.Binary)[m.currentRole()], m.ctx.Catalog)
	case stageLeadMode:
		return []choice{{value: "builder", label: "Builder · lead may implement and delegate"}, {value: "planner", label: "Planner · lead dispatches and reviews; workers mutate"}}
	case stageToolPolicy:
		recommended := recommendedToolProfileAssignments(m.roleOrder, m.spec.Lead)
		full := fullToolProfileAssignments(m.roleOrder)
		return []choice{
			{value: "recommended", label: "Recommended · broad lead + catalog-minimum lean workers · " + recommended},
			{value: "full_all", label: "Full for all · WARNING: duplicated MCP context and higher memory/concurrency cost · " + full},
		}
	case stageLaunchShape:
		return []choice{
			{value: LaunchShapeWorkingTeamTogether, label: "Start the working team together · all displayed initial members launch"},
			{value: LaunchShapeLeadOnlyStaged, label: "Lead-only staged bootstrap · only the lead launches; later roles need spawn gates"},
		}
	case stageOperator:
		if m.existingIndex >= 0 {
			p := m.ctx.Profiles[m.existingIndex]
			label := "Continue with authoritative profile contract · " + defaultString(m.spec.OperatorMode, "unspecified")
			if m.spec.OperatorMode == "self_operator" {
				label += fmt.Sprintf(" · lead=%s allow=%s revision=%d paused=%t notifications=%t", p.SelfOperatorLead, p.SelfOperatorAllow, p.SelfOperatorRevision, p.SelfOperatorPaused, p.OperatorNotifications)
			}
			choices := []choice{{value: "continue", label: label}}
			for _, item := range operatorChoices(m.opts.Capabilities) {
				if item.capability {
					item.disabled = true
					item.label += " [locked: the stored profile contract decides]"
					choices = append(choices, item)
				}
			}
			return choices
		}
		return operatorChoices(m.opts.Capabilities)
	case stageOperatorNotifications:
		if m.existingIndex >= 0 {
			return []choice{{value: "continue", label: fmt.Sprintf("Continue with authoritative policy · enabled=%t", m.spec.OperatorNotifications)}}
		}
		return []choice{{value: "no", label: "No notifications"}, {value: "yes", label: "Attention-only desktop notifications"}}
	case stageSelfOperatorAllow:
		if m.spec.SelfOperatorAllow == "merge" {
			return []choice{{value: "remove", label: "☑ merge · selected explicitly"}, {value: "continue", label: "Continue with explicit merge allowlist"}}
		}
		return []choice{{value: "merge", label: "☐ merge · select explicitly (second actor executes)"}}
	case stageTopology:
		return []choice{
			{value: "sibling-tabs", label: annotateTopologyChoice(m.opts.TerminalContext, "sibling-tabs", "One window per agent")},
			{value: "current", label: annotateTopologyChoice(m.opts.TerminalContext, "current", "Panes in this window")},
			{value: "detached", label: annotateTopologyChoice(m.opts.TerminalContext, "detached", "Detached squad")},
		}
	case stageLayoutPreset:
		return layoutPresetChoices(m.spec.Visibility)
	case stageLauncherPane:
		if m.spec.ExternalLead || m.spec.Visibility == "detached" {
			return []choice{{value: "keep", label: "Keep this pane · required by the selected topology"}}
		}
		return []choice{{value: "close-after-start", label: "Close after successful start"}, {value: "keep", label: "Keep this launcher pane"}}
	case stageConfirm:
		return []choice{{value: "preview", label: "Run canonical preview, then decide launch separately"}}
	case stageResumeBrief:
		return []choice{{value: "continue", label: "Continue with the preserved brief"}}
	case stageResumeGoal:
		if m.spec.ResumeGoalPlan.Eligible {
			return []choice{{value: "no", label: "No · preserve binding only"}, {value: "yes", label: "Yes · redeliver as one new claim-once attempt"}}
		}
		return []choice{{value: "continue", label: fmt.Sprintf("Continue · %s", defaultString(m.spec.ResumeGoalPlan.Action, "unavailable"))}}
	default:
		return nil
	}
}

func (m BubbleModel) defaultCursor() int {
	choices := m.choices()
	want := ""
	switch m.stage {
	case stageScope:
		want = defaultString(strings.ToLower(strings.TrimSpace(m.spec.Scope)), "project")
	case stageGlobalAgent:
		want = defaultString(strings.ToLower(strings.TrimSpace(m.spec.GlobalAgent)), "claude")
	case stageGlobalModel:
		want = defaultModelChoiceCatalog(m.spec.GlobalModel, m.spec.GlobalAgent, m.ctx.Catalog)
	case stageGlobalEffort:
		want = defaultEffortChoiceCatalog(m.spec.GlobalEffort, m.spec.GlobalAgent, m.ctx.Catalog, effortAutomatic)
	case stageProfile:
		want = m.spec.Profile
		if findProfile(m.ctx.Profiles, want) < 0 {
			want = "__create__"
		}
	case stageExistingSession:
		want = m.spec.Session
	case stageExistingModel:
		// An empty override maps to automatic, which this list omits; the
		// find-loop then falls through to keep at index zero.
		want = defaultModelChoiceCatalog(parseAssignments(m.spec.Model)[m.currentMember().Role], m.currentMember().Binary, m.ctx.Catalog)
	case stageExistingEffort:
		prefill := parseAssignments(m.spec.Effort)[m.currentMember().Role]
		if strings.TrimSpace(prefill) == "" {
			want = effortKeepChoice
		} else {
			want = defaultEffortChoiceCatalog(prefill, m.currentMember().Binary, m.ctx.Catalog, effortKeepChoice)
		}
	case stageResumeMember:
		if m.currentResumeMember().Action == MemberActionFresh {
			want = defaultModelChoiceCatalog(parseAssignments(m.spec.Model)[m.currentResumeMember().Role], m.currentResumeMember().Binary, m.ctx.Catalog)
		} else {
			want = "continue"
		}
	case stageResumeEffort:
		prefill := parseAssignments(m.spec.Effort)[m.currentResumeMember().Role]
		if strings.TrimSpace(prefill) == "" {
			want = effortKeepChoice
		} else {
			want = defaultEffortChoiceCatalog(prefill, m.currentResumeMember().Binary, m.ctx.Catalog, effortKeepChoice)
		}
	case stageRoleModel:
		role := m.currentRole()
		binary := parseAssignments(m.spec.Binary)[role]
		want = defaultModelChoiceCatalog(parseAssignments(m.spec.Model)[role], binary, m.ctx.Catalog)
		if strings.TrimSpace(parseAssignments(m.spec.Model)[role]) == "" && strings.TrimSpace(parseAssignments(m.spec.Effort)[role]) == "" {
			if rec := RecommendModelEffort(binary, DefaultWorkClassForRole(role), TaskProperties{}, m.ctx.Catalog); rec.Model != "" {
				want = rec.Model
			}
		}
	case stageRoleBinary:
		want = parseAssignments(m.spec.Binary)[m.currentRole()]
		if want == "" {
			want = m.ctx.PreferredBinaries[m.currentRole()]
		}
	case stageRoleEffort:
		role := m.currentRole()
		binary := parseAssignments(m.spec.Binary)[role]
		want = defaultEffortChoiceCatalog(parseAssignments(m.spec.Effort)[role], binary, m.ctx.Catalog, effortAutomatic)
		if strings.TrimSpace(parseAssignments(m.spec.Model)[role]) == "" && strings.TrimSpace(parseAssignments(m.spec.Effort)[role]) == "" {
			if rec := RecommendModelEffort(binary, DefaultWorkClassForRole(role), TaskProperties{}, m.ctx.Catalog); rec.Effort != "" {
				if _, ok := m.ctx.Catalog.Resolve(binary, agentcatalog.Efforts, rec.Effort); ok {
					want = rec.Effort
				}
			}
		}
	case stageLeadMode:
		want = defaultString(m.spec.LeadMode, "builder")
		if strings.TrimSpace(m.spec.LeadMode) == "" {
			if mode, _ := RecommendLeadMode(len(m.roleOrder)); mode != "" {
				want = mode
			}
		}
	case stageToolPolicy:
		want = defaultString(m.spec.ToolPolicyMode, "recommended")
	case stageOperator:
		if m.existingIndex >= 0 {
			want = "continue"
		} else {
			want = defaultOperatorMode(m.spec.OperatorMode, m.spec.Visibility)
		}
	case stageOperatorNotifications:
		want = map[bool]string{true: "yes", false: "no"}[m.spec.OperatorNotifications]
		if m.existingIndex >= 0 {
			want = "continue"
		}
	case stageTopology:
		want = recommendedTopology(m.spec.Visibility, m.spec.VisibilityExplicit, m.opts.TerminalContext)
	case stageLayoutPreset:
		want = defaultLayoutPreset(m.spec.LayoutPreset, m.spec.Visibility)
	case stageLauncherPane:
		want = defaultLauncherPane(m.spec.LauncherPane, m.spec.Visibility, m.spec.ExternalLead)
	case stageResumeGoal:
		if m.spec.RedeliverGoal {
			want = "yes"
		} else if m.spec.ResumeGoalPlan.Eligible {
			want = "no"
		} else {
			want = "continue"
		}
	}
	for i, item := range choices {
		if item.value == want {
			return i
		}
	}
	return 0
}

func (m BubbleModel) commitText() (tea.Model, tea.Cmd) {
	value := strings.TrimSpace(m.input.Value())
	m.pushHistory()
	switch m.stage {
	case stageProject:
		if value == "" {
			m.err = fmt.Errorf("project cannot be empty")
			m.history = m.history[:len(m.history)-1]
			return m, nil
		}
		ctx := ProjectContext{Project: value}
		var err error
		if m.opts.InspectProject != nil {
			ctx, err = m.opts.InspectProject(value)
			if err != nil {
				m.err = err
				m.history = m.history[:len(m.history)-1]
				return m, nil
			}
		}
		if strings.TrimSpace(ctx.SessionSuggestion) == "" {
			ctx.SessionSuggestion = m.ctx.SessionSuggestion
		}
		m.ctx = ctx
		m.spec.SelectProject(defaultString(ctx.Project, value))
		m.transition(stageProfile)
	case stageGlobalRoot:
		if value == "" {
			m.err = fmt.Errorf("global root cannot be empty")
			m.history = m.history[:len(m.history)-1]
			return m, nil
		}
		m.spec.GlobalRoot = value
		if m.opts.LoadCatalog != nil {
			m.ctx.Catalog = m.opts.LoadCatalog(value)
		}
		m.transition(stageGlobalAgent)
	case stageGlobalModelCustom:
		m.spec.GlobalModel = strings.TrimSpace(value)
		m.transition(stageGlobalEffort)
	case stageGlobalEffortCustom:
		m.spec.GlobalEffort = strings.TrimSpace(value)
		m.transition(stageGlobalNativeArgs)
	case stageGlobalNativeArgs:
		if strings.EqualFold(m.spec.GlobalAgent, "codex") {
			m.spec.GlobalCodexArgs = value
			m.spec.GlobalClaudeArgs = ""
		} else {
			m.spec.GlobalClaudeArgs = value
			m.spec.GlobalCodexArgs = ""
		}
		m.transition(stageGlobalWindow)
	case stageGlobalWindow:
		if value == "" {
			m.err = fmt.Errorf("window name cannot be empty")
			m.history = m.history[:len(m.history)-1]
			return m, nil
		}
		m.spec.GlobalWindow = value
		m.transition(stageConfirm)
	case stageNewProfile:
		if value == "" {
			m.err = fmt.Errorf("profile cannot be empty")
			m.history = m.history[:len(m.history)-1]
			return m, nil
		}
		m.spec.SelectNewProfile(value)
		m.newProfilePrefill = value
		m.existingIndex = -1
		m.transition(stageSession)
	case stageSession:
		if value == "" {
			m.err = fmt.Errorf("session cannot be empty")
			m.history = m.history[:len(m.history)-1]
			return m, nil
		}
		m.spec.SelectNewSession(value)
		m.newSessionPrefill = value
		m.transition(stageRoles)
	case stageTemplateSession:
		name := strings.ToLower(strings.TrimSpace(value))
		if name == "" {
			m.err = fmt.Errorf("session cannot be empty")
			m.history = m.history[:len(m.history)-1]
			return m, nil
		}
		m.selectExistingSession(SessionSummary{
			Name:           name,
			Source:         SessionSourceSuggestedFirst,
			Classification: RunClassification{State: RunStateNotStarted, Backend: BackendRunStart, Executable: true},
		})
		if m.done {
			return m, tea.Quit
		}
	case stageCloneSession:
		name := strings.ToLower(strings.TrimSpace(value))
		if name == "" {
			m.err = fmt.Errorf("session cannot be empty")
			m.history = m.history[:len(m.history)-1]
			return m, nil
		}
		m.clonePendingSession = name
		m.transition(stageCloneProfile)
	case stageCloneProfile:
		if value == "" {
			m.err = fmt.Errorf("profile cannot be empty")
			m.history = m.history[:len(m.history)-1]
			return m, nil
		}
		source := m.ctx.Profiles[m.existingIndex]
		m.spec.FromProfile = source.Name
		m.spec.SelectNewProfile(value)
		m.spec.SelectNewSession(m.clonePendingSession)
		m.spec.OperatorMode = defaultString(source.OperatorMode, "unspecified")
		m.spec.OperatorNotifications = source.OperatorNotifications
		m.roleIndex = 0
		if len(source.Members) == 0 {
			m.done = true
			return m, tea.Quit
		}
		m.transition(stageExistingOverride)
	case stageExistingModelCustom:
		if value != "" {
			m.spec.Model = setAssignment(m.spec.Model, m.currentMember().Role, value)
		} else {
			m.spec.Model = removeAssignment(m.spec.Model, m.currentMember().Role)
		}
		m.transition(stageExistingEffort)
	case stageExistingEffortCustom:
		if value != "" && !strings.EqualFold(value, effortAutomatic) {
			m.spec.Effort = setAssignment(m.spec.Effort, m.currentMember().Role, value)
		} else {
			m.spec.Effort = removeAssignment(m.spec.Effort, m.currentMember().Role)
		}
		m.nextExistingMember()
	case stageResumeModelCustom:
		if value != "" {
			m.spec.Model = setAssignment(m.spec.Model, m.currentResumeMember().Role, value)
		} else {
			m.spec.Model = removeAssignment(m.spec.Model, m.currentResumeMember().Role)
		}
		m.transition(stageResumeEffort)
	case stageResumeEffortCustom:
		if value != "" && !strings.EqualFold(value, effortAutomatic) {
			m.spec.Effort = setAssignment(m.spec.Effort, m.currentResumeMember().Role, value)
		} else {
			m.spec.Effort = removeAssignment(m.spec.Effort, m.currentResumeMember().Role)
		}
		m.nextResumeMember()
	case stageRoles:
		m.roleOrder = splitAssignmentsList(value)
		if len(m.roleOrder) == 0 {
			m.err = fmt.Errorf("choose at least one role")
			m.history = m.history[:len(m.history)-1]
			return m, nil
		}
		m.spec.Roles = strings.Join(m.roleOrder, ",")
		m.spec.Binary = renderAssignments(m.roleOrder, parseAssignments(m.spec.Binary))
		m.spec.Model = renderAssignments(m.roleOrder, parseAssignments(m.spec.Model))
		m.spec.Effort = renderAssignments(m.roleOrder, parseAssignments(m.spec.Effort))
		if !containsString(m.roleOrder, m.spec.Lead) {
			m.spec.Lead = ""
		}
		m.roleIndex = 0
		m.transition(stageRoleBinary)
	case stageRoleModelCustom:
		if value == "" || strings.EqualFold(value, effortAutomatic) {
			m.spec.Model = removeAssignment(m.spec.Model, m.currentRole())
		} else {
			m.spec.Model = setAssignment(m.spec.Model, m.currentRole(), value)
		}
		m.transition(stageRoleEffort)
	case stageRoleEffortCustom:
		if value == "" || strings.EqualFold(value, effortAutomatic) {
			m.spec.Effort = removeAssignment(m.spec.Effort, m.currentRole())
		} else {
			m.spec.Effort = setAssignment(m.spec.Effort, m.currentRole(), value)
		}
		m.roleIndex++
		if m.roleIndex < len(m.roleOrder) {
			m.transition(stageRoleBinary)
		} else {
			m.transition(stageLead)
		}
	case stageLead:
		if value == "" {
			m.err = fmt.Errorf("lead cannot be empty")
			m.history = m.history[:len(m.history)-1]
			return m, nil
		}
		m.spec.Lead = value
		m.transition(stageLeadMode)
	case stageStagedRoles:
		m.spec.StagedRoles = value
		if err := m.spec.ApplyLaunchShape(); err != nil {
			m.err = err
			m.history = m.history[:len(m.history)-1]
			return m, nil
		}
		m.roleOrder = splitAssignmentsList(m.spec.Roles)
		m.transition(stageToolPolicy)
	case stageGoal:
		m.spec.clearGoalBinding()
		m.spec.Goal = value
		m.transition(stageSeed)
	case stageSeed:
		m.spec.SeedFrom = value
		// Keep the review stage available so the operator can see an
		// unverified binding. finishRunStartWizard fails closed before preview
		// and before Launch now when this remains unresolved.
		_ = m.spec.ResolveGoalBinding()
		m.transition(stageConfirm)
	}
	return m, textinput.Blink
}

func (m BubbleModel) commitChoice() (tea.Model, tea.Cmd) {
	choices := m.choices()
	if len(choices) == 0 {
		return m, nil
	}
	selected := choices[minInt(m.cursor, len(choices)-1)].value
	if choices[minInt(m.cursor, len(choices)-1)].disabled {
		m.err = fmt.Errorf("%s is not available in this release", selected)
		return m, nil
	}
	m.pushHistory()
	switch m.stage {
	case stageScope:
		m.spec.Scope = selected
		if selected == "global" {
			m.spec.Backend = BackendGlobalStart
			m.transition(stageGlobalRoot)
		} else {
			m.spec.Backend = BackendRunStart
			m.transition(stageProject)
		}
	case stageGlobalAgent:
		m.spec.GlobalAgent = selected
		m.transition(stageGlobalModel)
	case stageGlobalModel:
		switch selected {
		case modelCustomChoice:
			m.transition(stageGlobalModelCustom)
		case effortAutomatic:
			m.spec.GlobalModel = ""
			m.transition(stageGlobalEffort)
		default:
			m.spec.GlobalModel = selected
			m.transition(stageGlobalEffort)
		}
	case stageGlobalEffort:
		switch selected {
		case effortCustomChoice:
			m.transition(stageGlobalEffortCustom)
		case effortAutomatic:
			m.spec.GlobalEffort = ""
			m.transition(stageGlobalNativeArgs)
		default:
			m.spec.GlobalEffort = selected
			m.transition(stageGlobalNativeArgs)
		}
	case stageProfile:
		if selected == "__create__" {
			if strings.TrimSpace(m.spec.Profile) != "" && findProfile(m.ctx.Profiles, m.spec.Profile) < 0 {
				m.newProfilePrefill = m.spec.Profile
			}
			m.spec.SelectNewProfile("")
			m.existingIndex = -1
			m.transition(stageNewProfile)
			break
		}
		m.spec.SelectExistingProfile(selected)
		m.existingIndex = findProfile(m.ctx.Profiles, selected)
		if isTemplateProfile(m.ctx.Profiles[m.existingIndex]) {
			m.transition(stageTemplateSession)
			break
		}
		sessions := profileSessions(m.ctx.Profiles[m.existingIndex], m.ctx.SessionSuggestion)
		if len(sessions) == 0 {
			m.spec.clearSelectedRun()
			m.spec.RunState = RunStateBlocked
			m.err = fmt.Errorf("profile %q has no CLI-discovered session summary", selected)
			m.done = true
			return m, tea.Quit
		}
		// The common case stays frictionless: no prompt at all when there is
		// nothing to resolve (no conflicting desired session, or exactly one
		// known session that already matches it). The clone escape hatch
		// (#523) only needs to surface on a real, detectable mismatch.
		suggestion := strings.TrimSpace(m.ctx.SessionSuggestion)
		if len(sessions) == 1 && (suggestion == "" || suggestion == sessions[0].Name) {
			m.selectExistingSession(sessions[0])
			if m.done {
				return m, tea.Quit
			}
		} else {
			m.transition(stageExistingSession)
		}
	case stageExistingSession:
		if selected == cloneRosterChoiceValue {
			m.transition(stageCloneSession)
			break
		}
		sessions := profileSessions(m.ctx.Profiles[m.existingIndex], m.ctx.SessionSuggestion)
		for _, session := range sessions {
			if session.Name == selected {
				m.selectExistingSession(session)
				if m.done {
					return m, tea.Quit
				}
				break
			}
		}
	case stageExistingOverride:
		if selected == "override" {
			m.transition(stageExistingModel)
		} else {
			m.nextExistingMember()
		}
	case stageExistingModel:
		switch selected {
		case modelCustomChoice:
			m.transition(stageExistingModelCustom)
		case modelKeepChoice:
			m.spec.Model = removeAssignment(m.spec.Model, m.currentMember().Role)
			m.transition(stageExistingEffort)
		default:
			m.spec.Model = setAssignment(m.spec.Model, m.currentMember().Role, selected)
			m.transition(stageExistingEffort)
		}
	case stageExistingEffort:
		switch selected {
		case effortCustomChoice:
			m.transition(stageExistingEffortCustom)
		case effortKeepChoice:
			m.spec.Effort = removeAssignment(m.spec.Effort, m.currentMember().Role)
			m.nextExistingMember()
		default:
			m.spec.Effort = setAssignment(m.spec.Effort, m.currentMember().Role, selected)
			m.nextExistingMember()
		}
	case stageResumeMember:
		member := m.currentResumeMember()
		if member.Action != MemberActionFresh {
			m.nextResumeMember()
			break
		}
		switch selected {
		case modelCustomChoice:
			m.transition(stageResumeModelCustom)
		case modelKeepChoice:
			m.spec.Model = removeAssignment(m.spec.Model, member.Role)
			m.transition(stageResumeEffort)
		default:
			m.spec.Model = setAssignment(m.spec.Model, member.Role, selected)
			m.transition(stageResumeEffort)
		}
	case stageResumeEffort:
		member := m.currentResumeMember()
		switch selected {
		case effortCustomChoice:
			m.transition(stageResumeEffortCustom)
		case effortKeepChoice:
			m.spec.Effort = removeAssignment(m.spec.Effort, member.Role)
			m.nextResumeMember()
		default:
			m.spec.Effort = setAssignment(m.spec.Effort, member.Role, selected)
			m.nextResumeMember()
		}
	case stageRoleBinary:
		m.spec.Binary = setAssignment(m.spec.Binary, m.currentRole(), selected)
		m.transition(stageRoleModel)
	case stageRoleModel:
		switch selected {
		case modelCustomChoice:
			m.transition(stageRoleModelCustom)
		case effortAutomatic:
			m.spec.Model = removeAssignment(m.spec.Model, m.currentRole())
			m.transition(stageRoleEffort)
		default:
			m.spec.Model = setAssignment(m.spec.Model, m.currentRole(), selected)
			m.transition(stageRoleEffort)
		}
	case stageRoleEffort:
		if selected == effortCustomChoice {
			m.transition(stageRoleEffortCustom)
			break
		}
		if selected == effortAutomatic {
			m.spec.Effort = removeAssignment(m.spec.Effort, m.currentRole())
		} else {
			m.spec.Effort = setAssignment(m.spec.Effort, m.currentRole(), selected)
		}
		m.roleIndex++
		if m.roleIndex < len(m.roleOrder) {
			m.transition(stageRoleBinary)
		} else {
			m.transition(stageLead)
		}
	case stageLeadMode:
		m.spec.LeadMode = selected
		m.transition(stageLaunchShape)
	case stageLaunchShape:
		m.spec.LaunchShape = selected
		if err := m.spec.ApplyLaunchShape(); err != nil {
			m.err = err
			return m, nil
		}
		m.roleOrder = splitAssignmentsList(m.spec.Roles)
		m.transition(stageStagedRoles)
	case stageToolPolicy:
		m.spec.ToolPolicyMode = selected
		if selected == "full_all" {
			m.spec.ToolProfile = fullToolProfileAssignments(m.roleOrder)
		} else {
			m.spec.ToolProfile = recommendedToolProfileAssignments(m.roleOrder, m.spec.Lead)
		}
		m.transition(stageTopology)
	case stageOperator:
		if selected == "continue" {
			m.transition(stageOperatorNotifications)
		} else {
			m.spec.OperatorMode = selected
			if selected == "self_operator" {
				m.spec.SelfOperatorLead = m.spec.Lead
				m.transition(stageSelfOperatorAllow)
			} else {
				m.transition(stageOperatorNotifications)
			}
		}
	case stageSelfOperatorAllow:
		if selected == "merge" {
			m.spec.SelfOperatorAllow = "merge"
			m.transition(stageSelfOperatorAllow)
		} else if selected == "remove" {
			m.spec.SelfOperatorAllow = ""
			m.transition(stageSelfOperatorAllow)
		} else {
			m.transition(stageOperatorNotifications)
		}
	case stageOperatorNotifications:
		if selected != "continue" {
			m.spec.OperatorNotifications = selected == "yes"
		}
		if m.spec.Backend == BackendResume {
			m.spec.LauncherPane = ""
			m.spec.Goal = ""
			m.spec.SeedFrom = ""
			m.transition(stageResumeBrief)
		} else {
			m.transition(stageLauncherPane)
		}
	case stageTopology:
		m.spec.Visibility = selected
		m.transition(stageLayoutPreset)
	case stageLayoutPreset:
		m.spec.LayoutPreset = selected
		m.transition(stageOperator)
	case stageLauncherPane:
		m.spec.LauncherPane = selected
		m.transition(stageGoal)
	case stageResumeBrief:
		if m.spec.ResumeGoalPlan.Eligible {
			m.transition(stageResumeGoal)
		} else {
			m.spec.RedeliverGoal = false
			m.transition(stageConfirm)
		}
	case stageResumeGoal:
		m.spec.RedeliverGoal = m.spec.ResumeGoalPlan.Eligible && selected == "yes"
		m.transition(stageConfirm)
	case stageConfirm:
		m.done = true
		return m, tea.Quit
	}
	return m, textinput.Blink
}

func (m *BubbleModel) nextExistingMember() {
	m.roleIndex++
	if m.roleIndex < len(m.ctx.Profiles[m.existingIndex].Members) {
		m.transition(stageExistingOverride)
	} else {
		m.transition(stageTopology)
	}
}

func (m *BubbleModel) nextResumeMember() {
	m.roleIndex++
	if m.roleIndex < len(m.spec.ResumeMembers) {
		m.transition(stageResumeMember)
	} else {
		m.transition(stageTopology)
	}
}

func (m *BubbleModel) selectExistingSession(session SessionSummary) {
	m.spec.SelectExistingSession(session)
	profile := m.ctx.Profiles[m.existingIndex]
	m.spec.OperatorMode = defaultString(profile.OperatorMode, "unspecified")
	m.spec.OperatorNotifications = profile.OperatorNotifications
	m.roleIndex = 0
	if !m.spec.RunExecutable {
		m.done = true
		return
	}
	if m.spec.Backend == BackendResume {
		m.spec.Model = ""
		m.spec.Effort = ""
		if len(m.spec.ResumeMembers) == 0 {
			m.done = true
			return
		}
		m.transition(stageResumeMember)
		return
	}
	if len(profile.Members) == 0 {
		m.done = true
		return
	}
	m.transition(stageExistingOverride)
}

func (m *BubbleModel) transition(stage bubbleStage) {
	m.stage = stage
	m.configureStage()
}

func (m *BubbleModel) pushHistory() {
	m.history = append(m.history, bubbleSnapshot{
		spec:                m.spec.Clone(),
		ctx:                 cloneProjectContext(m.ctx),
		stage:               m.stage,
		cursor:              m.cursor,
		existingIndex:       m.existingIndex,
		roleOrder:           append([]string(nil), m.roleOrder...),
		roleIndex:           m.roleIndex,
		inputValue:          m.input.Value(),
		newProfilePrefill:   m.newProfilePrefill,
		newSessionPrefill:   m.newSessionPrefill,
		clonePendingSession: m.clonePendingSession,
	})
}

func (m BubbleModel) back() (tea.Model, tea.Cmd) {
	if len(m.history) == 0 {
		m.cancelled = true
		return m, tea.Quit
	}
	last := m.history[len(m.history)-1]
	m.history = m.history[:len(m.history)-1]
	refreshedExisting := false
	if last.spec.ProfileBranch == ProfileBranchExisting && strings.TrimSpace(last.spec.Session) != "" && m.opts.InspectProject != nil {
		project := defaultString(m.ctx.Project, last.spec.Project)
		refreshed, refreshErr := m.opts.InspectProject(project)
		if refreshErr == nil || projectContextHasFacts(refreshed) {
			if strings.TrimSpace(refreshed.Project) == "" {
				refreshed.Project = project
			}
			if strings.TrimSpace(refreshed.SessionSuggestion) == "" {
				refreshed.SessionSuggestion = m.ctx.SessionSuggestion
			}
			m.ctx = refreshed
			refreshedExisting = true
		}
		if refreshErr != nil {
			m.rejectExistingSnapshot(last, stageProject, fmt.Errorf("refresh project discovery before Back: %w", refreshErr))
			return m, textinput.Blink
		}
		for i := range m.history {
			if strings.TrimSpace(m.history[i].spec.Project) == strings.TrimSpace(last.spec.Project) {
				m.history[i].ctx = m.ctx
			}
		}
	}
	if !snapshotCompatible(last, m.ctx) {
		m.rejectExistingSnapshot(last, stageProfile, fmt.Errorf("the selected profile or run changed while the wizard was open; review the refreshed facts before continuing"))
		return m, textinput.Blink
	}
	m.spec = last.spec.Clone()
	if !refreshedExisting {
		m.ctx = cloneProjectContext(last.ctx)
	}
	m.stage = last.stage
	m.cursor = last.cursor
	if refreshedExisting && m.spec.ProfileBranch == ProfileBranchExisting {
		m.existingIndex = findProfile(m.ctx.Profiles, m.spec.Profile)
	} else {
		m.existingIndex = last.existingIndex
	}
	m.roleOrder = append([]string(nil), last.roleOrder...)
	m.roleIndex = last.roleIndex
	m.newProfilePrefill = last.newProfilePrefill
	m.newSessionPrefill = last.newSessionPrefill
	m.clonePendingSession = last.clonePendingSession
	m.configureStage()
	m.cursor = last.cursor
	if m.isTextStage() {
		m.input.SetValue(last.inputValue)
		m.input.CursorEnd()
	}
	return m, textinput.Blink
}

func (m *BubbleModel) rejectExistingSnapshot(last bubbleSnapshot, stage bubbleStage, err error) {
	m.spec = last.spec.Clone()
	m.spec.InvalidateExistingRun()
	m.stage = stage
	m.existingIndex = findProfile(m.ctx.Profiles, m.spec.Profile)
	m.roleOrder = nil
	m.roleIndex = 0
	m.history = nil
	m.configureStage()
	m.err = err
}

func projectContextHasFacts(ctx ProjectContext) bool {
	return strings.TrimSpace(ctx.Project) != "" || strings.TrimSpace(ctx.OriginSlug) != "" || strings.TrimSpace(ctx.Branch) != "" ||
		strings.TrimSpace(ctx.SessionSuggestion) != "" || strings.TrimSpace(ctx.NewProfileSuggestion) != "" || len(ctx.Profiles) > 0 || len(ctx.PreferredBinaries) > 0
}

func snapshotCompatible(snapshot bubbleSnapshot, current ProjectContext) bool {
	if snapshot.spec.ProfileBranch != ProfileBranchExisting || snapshot.spec.Session == "" {
		return true
	}
	if snapshot.spec.DiscoveryFingerprint == "" {
		return false
	}
	index := findProfile(current.Profiles, snapshot.spec.Profile)
	if index < 0 {
		return false
	}
	for _, session := range profileSessions(current.Profiles[index], current.SessionSuggestion) {
		if session.Name == snapshot.spec.Session {
			return session.Fingerprint != "" && session.Fingerprint == snapshot.spec.DiscoveryFingerprint
		}
	}
	return false
}

func (m BubbleModel) phaseIndex() int {
	if strings.EqualFold(m.spec.Scope, "global") {
		switch m.stage {
		case stageScope, stageGlobalRoot:
			return 0
		case stageGlobalAgent, stageGlobalModel, stageGlobalModelCustom:
			return 1
		case stageGlobalEffort, stageGlobalEffortCustom, stageGlobalNativeArgs, stageGlobalWindow:
			return 2
		default:
			return 3
		}
	}
	switch m.stage {
	case stageScope, stageProject:
		return 0
	case stageProfile, stageNewProfile, stageExistingSession, stageTemplateSession, stageCloneSession, stageCloneProfile, stageSession:
		return 1
	case stageExistingOverride, stageExistingModel, stageExistingModelCustom, stageExistingEffort, stageExistingEffortCustom,
		stageResumeMember, stageResumeModelCustom, stageResumeEffort, stageResumeEffortCustom,
		stageRoleModel, stageRoleModelCustom, stageRoleEffort, stageRoleEffortCustom,
		stageRoles, stageRoleBinary, stageLead, stageLeadMode, stageLaunchShape, stageStagedRoles, stageToolPolicy:
		return 2
	case stageTopology, stageLayoutPreset, stageOperator, stageSelfOperatorAllow, stageOperatorNotifications, stageLauncherPane:
		return 3
	case stageGoal, stageSeed, stageResumeBrief, stageResumeGoal:
		return 4
	default:
		return 5
	}
}

func (m BubbleModel) phaseLabels() []string {
	if strings.EqualFold(m.spec.Scope, "global") {
		return []string{"Scope", "Agent", "Run controls", "Review"}
	}
	return []string{"Scope", "Profile & run", "Team", "Run controls", "Brief", "Review"}
}

func operatorContractSummary(mode string) string {
	switch mode {
	case "lead_pane":
		return "type in lead pane; mirror to durable gates"
	case "separate_terminal":
		return "operator polls and answers durable gates"
	case "noc":
		return "NOC/global orchestrator owns polling"
	case "self_operator":
		return "exact-session merge-only delegation; human exclusions and second actor remain"
	default:
		return "legacy poll-required compatibility"
	}
}

func layoutPresetChoices(visibility string) []choice {
	switch visibility {
	case "current":
		return []choice{{value: "lead-left", label: "Lead left · main-vertical, 60% width"}, {value: "lead-top", label: "Lead top · main-horizontal, 60% height"}, {value: "even-grid", label: "Even grid · tiled"}}
	case "detached":
		return []choice{{value: "", label: "Detached topology · no visible layout preset"}}
	default:
		return []choice{{value: "one-window-per-agent", label: "One window per agent"}}
	}
}

func defaultLayoutPreset(current, visibility string) string {
	if strings.TrimSpace(current) != "" {
		return current
	}
	if visibility == "current" {
		return "lead-left"
	}
	if visibility == "sibling-tabs" {
		return "one-window-per-agent"
	}
	return ""
}

func defaultLauncherPane(current, visibility string, external bool) string {
	if external || visibility == "detached" {
		return "keep"
	}
	if strings.TrimSpace(current) != "" {
		return current
	}
	return "close-after-start"
}

func (m BubbleModel) currentRole() string {
	if m.roleIndex < 0 || m.roleIndex >= len(m.roleOrder) {
		return "role"
	}
	return m.roleOrder[m.roleIndex]
}

// roleRecommendationNote returns an advisory model/effort recommendation
// line for the current fresh-roster role (#496), or "" when the role already
// has an explicit model/effort prefill (that always wins and needs no
// nudging note).
func (m BubbleModel) roleRecommendationNote() string {
	role := m.currentRole()
	if strings.TrimSpace(parseAssignments(m.spec.Model)[role]) != "" || strings.TrimSpace(parseAssignments(m.spec.Effort)[role]) != "" {
		return ""
	}
	binary := parseAssignments(m.spec.Binary)[role]
	rec := RecommendModelEffort(binary, DefaultWorkClassForRole(role), TaskProperties{}, m.ctx.Catalog)
	if rec.Model == "" && rec.Effort == "" {
		return ""
	}
	return fmt.Sprintf("Recommended for %s: %s/%s -- %s", role, defaultString(rec.Model, "automatic"), defaultString(rec.Effort, "automatic"), rec.Rationale)
}

func (m BubbleModel) currentMember() MemberSummary {
	if m.existingIndex < 0 || m.existingIndex >= len(m.ctx.Profiles) {
		return MemberSummary{Role: "role"}
	}
	members := m.ctx.Profiles[m.existingIndex].Members
	if m.roleIndex < 0 || m.roleIndex >= len(members) {
		return MemberSummary{Role: "role"}
	}
	return members[m.roleIndex]
}

func (m BubbleModel) currentResumeMember() SessionMemberSummary {
	if m.roleIndex < 0 || m.roleIndex >= len(m.spec.ResumeMembers) {
		return SessionMemberSummary{Role: "role", Action: MemberActionBlocked}
	}
	return m.spec.ResumeMembers[m.roleIndex]
}

func cloneProjectContext(ctx ProjectContext) ProjectContext {
	out := ctx
	out.PreferredBinaries = make(map[string]string, len(ctx.PreferredBinaries))
	for key, value := range ctx.PreferredBinaries {
		out.PreferredBinaries[key] = value
	}
	out.Profiles = append([]ProfileSummary(nil), ctx.Profiles...)
	for i := range out.Profiles {
		out.Profiles[i].Members = append([]MemberSummary(nil), ctx.Profiles[i].Members...)
		out.Profiles[i].Sessions = append([]SessionSummary(nil), ctx.Profiles[i].Sessions...)
		for j := range out.Profiles[i].Sessions {
			out.Profiles[i].Sessions[j].Members = cloneSessionMembers(ctx.Profiles[i].Sessions[j].Members)
		}
	}
	return out
}

func findProfile(profiles []ProfileSummary, name string) int {
	for i := range profiles {
		if profiles[i].Name == name {
			return i
		}
	}
	return -1
}

func defaultLead(roles []string) string {
	for _, role := range roles {
		if role == "cto" {
			return "cto"
		}
	}
	if len(roles) > 0 {
		return roles[0]
	}
	return "cto"
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func setAssignment(raw, key, value string) string {
	values := parseAssignments(raw)
	values[key] = value
	order := splitAssignmentsList(rawAssignmentKeys(raw) + "," + key)
	return renderAssignments(order, values)
}

func removeAssignment(raw, key string) string {
	values := parseAssignments(raw)
	delete(values, key)
	return renderAssignments(splitAssignmentsList(rawAssignmentKeys(raw)), values)
}

func rawAssignmentKeys(raw string) string {
	keys := make([]string, 0)
	for _, item := range strings.Split(raw, ",") {
		key, _, ok := strings.Cut(item, "=")
		if ok && strings.TrimSpace(key) != "" {
			keys = append(keys, strings.TrimSpace(key))
		}
	}
	return strings.Join(keys, ",")
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
