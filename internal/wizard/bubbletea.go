package wizard

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type bubbleStage int

const (
	stageProject bubbleStage = iota
	stageProfile
	stageNewProfile
	stageSession
	stageExistingOverride
	stageExistingModel
	stageExistingModelCustom
	stageExistingEffort
	stageRoles
	stageRoleBinary
	stageRoleModel
	stageRoleModelCustom
	stageRoleEffort
	stageLead
	stageLeadMode
	stageOperator
	stageSelfOperatorAllow
	stageOperatorNotifications
	stageTopology
	stageLayoutPreset
	stageLauncherPane
	stageGoal
	stageSeed
	stageConfirm
)

type BubbleResult struct {
	Spec      Spec
	Cancelled bool
}

type bubbleSnapshot struct {
	spec          Spec
	ctx           ProjectContext
	stage         bubbleStage
	cursor        int
	existingIndex int
	roleOrder     []string
	roleIndex     int
	inputValue    string
}

// BubbleModel is the full-screen adapter over the same Spec and project facts
// used by RunNumbered. It owns navigation only; it cannot preview or launch.
type BubbleModel struct {
	opts NumberedOptions
	spec Spec
	ctx  ProjectContext

	stage         bubbleStage
	cursor        int
	existingIndex int
	roleOrder     []string
	roleIndex     int
	input         textinput.Model
	history       []bubbleSnapshot

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
	s := opts.Defaults
	ctx := ProjectContext{Project: s.Project}
	var err error
	if opts.InspectProject != nil {
		ctx, err = opts.InspectProject(s.Project)
		if err != nil {
			return BubbleModel{}, err
		}
		if strings.TrimSpace(ctx.Project) != "" {
			s.Project = ctx.Project
		}
	}
	input := textinput.New()
	input.CharLimit = 512
	input.Width = 64
	m := BubbleModel{
		opts:          opts,
		spec:          s,
		ctx:           ctx,
		stage:         stageProject,
		existingIndex: -1,
		input:         input,
		width:         88,
		height:        28,
	}
	m.configureStage()
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
	labels := []string{"Project", "Team", "Topology", "Goal", "Review"}
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
	case stageProject:
		return "Choose the project root"
	case stageProfile:
		return "Choose a team profile"
	case stageNewProfile:
		return "Name the new profile"
	case stageSession:
		return "Choose the workstream session"
	case stageExistingOverride:
		return "Launch-time override · " + m.currentMember().Role
	case stageExistingModel:
		return "Model override · " + m.currentMember().Role
	case stageExistingModelCustom:
		return "Custom model override · " + m.currentMember().Role
	case stageExistingEffort:
		return "Effort override · " + m.currentMember().Role
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
	case stageLead:
		return "Choose the visible lead"
	case stageLeadMode:
		return "Choose the lead posture"
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
		return "Optional goal text"
	case stageSeed:
		return "Optional brief source"
	case stageConfirm:
		return "Review answers before canonical preview"
	default:
		return "Run start wizard"
	}
}

func (m BubbleModel) note() string {
	switch m.stage {
	case stageProject:
		if m.ctx.OriginSlug != "" {
			return "Detected origin " + m.ctx.OriginSlug + ". The nearest git root is the default."
		}
		return "The nearest git root is the default; no network access is used."
	case stageExistingOverride:
		member := m.currentMember()
		return fmt.Sprintf("Profile values stay untouched: model=%s · effort=%s", defaultString(member.Model, "automatic"), defaultString(member.Effort, "automatic"))
	case stageExistingModel:
		return "Keep uses the stored profile value; a pick applies to this launch only."
	case stageExistingModelCustom:
		return "Enter a model for this launch only, or leave blank to keep the profile value."
	case stageExistingEffort:
		return "This replaces only the native effort args in the in-memory launch plan."
	case stageRoles:
		return "Comma-separated role ids. Defaults are shown in the field."
	case stageRoleModel:
		return "Models pass through to the selected binary verbatim; custom accepts any name."
	case stageRoleModelCustom:
		return "Enter any model name, or leave blank for automatic."
	case stageTopology:
		return "The diagram is the topology that the canonical visibility flag selects."
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
		return "Enter collects these answers and runs preview. Live launch is a later default-No prompt."
	case stageSeed:
		return "Accepted forms: file:path · issue:393 · gh:owner/repo#393"
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
	parts := []string{
		"Project   " + m.spec.Project,
		"Profile   " + defaultString(m.spec.Profile, "default"),
		"Session   " + m.spec.Session,
	}
	if m.spec.Roles != "" {
		parts = append(parts, "Roster    "+m.spec.Roles, "Lead      "+m.spec.Lead+" · "+m.spec.LeadMode)
	} else {
		parts = append(parts, "Roster    existing profile (authoritative)")
	}
	if m.spec.Model != "" {
		parts = append(parts, "Models    "+m.spec.Model+" (launch only)")
	}
	if m.spec.Effort != "" {
		parts = append(parts, "Effort    "+m.spec.Effort+" (launch only)")
	}
	parts = append(parts, "Operator  "+defaultString(m.spec.OperatorMode, "unspecified")+" · "+operatorContractSummary(m.spec.OperatorMode))
	parts = append(parts, fmt.Sprintf("Alerts    attention-only notifications=%t", m.spec.OperatorNotifications))
	parts = append(parts, "Topology  "+m.spec.Visibility)
	if m.spec.LayoutPreset != "" {
		parts = append(parts, "Layout    "+m.spec.LayoutPreset)
	}
	parts = append(parts, "Launcher  "+defaultString(m.spec.LauncherPane, "legacy keep"), "", TopologyPreview(m.spec.Visibility))
	return strings.Join(parts, "\n")
}

func (m BubbleModel) isTextStage() bool {
	switch m.stage {
	case stageProject, stageNewProfile, stageSession, stageExistingModelCustom, stageRoles, stageRoleModelCustom, stageLead, stageGoal, stageSeed:
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
	case stageNewProfile:
		value = defaultString(m.ctx.NewProfileSuggestion, "squad-"+defaultString(m.ctx.SessionSuggestion, "project"))
	case stageSession:
		value = m.spec.Session
		if value == "" && m.existingIndex >= 0 {
			value = m.ctx.Profiles[m.existingIndex].PinnedSession
		}
		if value == "" {
			value = m.ctx.SessionSuggestion
		}
	case stageExistingModelCustom:
		value = parseAssignments(m.spec.Model)[m.currentMember().Role]
		placeholder = "leave blank to keep " + defaultString(m.currentMember().Model, "automatic")
	case stageRoles:
		value = defaultString(m.spec.Roles, "cto,senior-dev,qa")
	case stageRoleModelCustom:
		value = parseAssignments(m.spec.Model)[m.currentRole()]
		placeholder = "leave blank for automatic"
	case stageLead:
		value = defaultString(m.spec.Lead, defaultLead(m.roleOrder))
	case stageGoal:
		value = m.spec.Goal
		placeholder = "optional"
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
	case stageProfile:
		choices := make([]choice, 0, len(m.ctx.Profiles)+2)
		for _, profile := range m.ctx.Profiles {
			pinned := defaultString(profile.PinnedSession, "un-pinned")
			choices = append(choices, choice{value: profile.Name, label: fmt.Sprintf("%s · %d member(s) · session %s", profile.Name, profile.MemberCount, pinned)})
		}
		if len(m.ctx.Profiles) == 0 || findProfile(m.ctx.Profiles, "default") < 0 {
			choices = append(choices, choice{value: "default", label: "default · create a fresh roster"})
		}
		choices = append(choices, choice{value: "__create__", label: "create a named profile"})
		return choices
	case stageExistingOverride:
		return []choice{{value: "keep", label: "Keep profile model and effort"}, {value: "override", label: "Override this role for this launch only"}}
	case stageExistingModel:
		return existingOverrideModelChoices(m.currentMember())
	case stageExistingEffort:
		return effortChoices(m.currentMember().Binary)
	case stageRoleBinary:
		return []choice{{value: "codex", label: "Codex"}, {value: "claude", label: "Claude"}}
	case stageRoleModel:
		return modelChoices(parseAssignments(m.spec.Binary)[m.currentRole()])
	case stageRoleEffort:
		return effortChoices(parseAssignments(m.spec.Binary)[m.currentRole()])
	case stageLeadMode:
		return []choice{{value: "builder", label: "Builder · lead may implement and delegate"}, {value: "planner", label: "Planner · lead dispatches and reviews; workers mutate"}}
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
		return []choice{{value: "sibling-tabs", label: "One window per agent"}, {value: "current", label: "Panes in this window"}, {value: "detached", label: "Detached squad"}}
	case stageLayoutPreset:
		return layoutPresetChoices(m.spec.Visibility)
	case stageLauncherPane:
		if m.spec.ExternalLead || m.spec.Visibility == "detached" {
			return []choice{{value: "keep", label: "Keep this pane · required by the selected topology"}}
		}
		return []choice{{value: "close-after-start", label: "Close after successful start"}, {value: "keep", label: "Keep this launcher pane"}}
	case stageConfirm:
		return []choice{{value: "preview", label: "Run canonical preview, then decide launch separately"}}
	default:
		return nil
	}
}

func effortChoices(binary string) []choice {
	choices := []choice{{value: "automatic", label: "Automatic"}, {value: "low", label: "Low"}, {value: "medium", label: "Medium"}, {value: "high", label: "High"}}
	if strings.EqualFold(binary, "codex") {
		choices = append(choices, choice{value: "minimal", label: "Minimal"}, choice{value: "xhigh", label: "Extra high"})
	}
	return choices
}

func (m BubbleModel) defaultCursor() int {
	choices := m.choices()
	want := ""
	switch m.stage {
	case stageProfile:
		want = defaultString(m.spec.Profile, "default")
	case stageExistingModel:
		// An empty override maps to automatic, which this list omits; the
		// find-loop then falls through to keep at index zero.
		want = defaultModelChoice(parseAssignments(m.spec.Model)[m.currentMember().Role], m.currentMember().Binary)
	case stageExistingEffort:
		want = defaultString(m.currentMember().Effort, effortAutomatic)
	case stageRoleModel:
		want = defaultModelChoice(parseAssignments(m.spec.Model)[m.currentRole()], parseAssignments(m.spec.Binary)[m.currentRole()])
	case stageRoleBinary:
		want = parseAssignments(m.spec.Binary)[m.currentRole()]
		if want == "" {
			want = m.ctx.PreferredBinaries[m.currentRole()]
		}
	case stageRoleEffort:
		want = defaultString(parseAssignments(m.spec.Effort)[m.currentRole()], effortAutomatic)
	case stageLeadMode:
		want = defaultString(m.spec.LeadMode, "builder")
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
		want = defaultString(m.spec.Visibility, "sibling-tabs")
	case stageLayoutPreset:
		want = defaultLayoutPreset(m.spec.LayoutPreset, m.spec.Visibility)
	case stageLauncherPane:
		want = defaultLauncherPane(m.spec.LauncherPane, m.spec.Visibility, m.spec.ExternalLead)
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
		m.ctx = ctx
		m.spec.Project = defaultString(ctx.Project, value)
		m.transition(stageProfile)
	case stageNewProfile:
		if value == "" {
			m.err = fmt.Errorf("profile cannot be empty")
			m.history = m.history[:len(m.history)-1]
			return m, nil
		}
		m.spec.Profile = value
		m.existingIndex = -1
		m.transition(stageSession)
	case stageSession:
		if value == "" {
			m.err = fmt.Errorf("session cannot be empty")
			m.history = m.history[:len(m.history)-1]
			return m, nil
		}
		if m.existingIndex >= 0 {
			if mismatch := pinnedSessionMismatch(m.ctx.Profiles[m.existingIndex], value); mismatch != nil {
				m.err = mismatch
				m.history = m.history[:len(m.history)-1]
				return m, nil
			}
		}
		m.spec.Session = value
		if m.existingIndex >= 0 {
			m.spec.Roles, m.spec.Binary, m.spec.Model, m.spec.Effort, m.spec.Lead, m.spec.LeadMode = "", "", "", "", "", ""
			profile := m.ctx.Profiles[m.existingIndex]
			m.spec.OperatorMode = defaultString(profile.OperatorMode, "unspecified")
			m.spec.OperatorNotifications = profile.OperatorNotifications
			m.roleIndex = 0
			if len(m.ctx.Profiles[m.existingIndex].Members) == 0 {
				m.transition(stageTopology)
			} else {
				m.transition(stageExistingOverride)
			}
		} else {
			m.transition(stageRoles)
		}
	case stageExistingModelCustom:
		if value != "" {
			m.spec.Model = setAssignment(m.spec.Model, m.currentMember().Role, value)
		} else {
			m.spec.Model = removeAssignment(m.spec.Model, m.currentMember().Role)
		}
		m.transition(stageExistingEffort)
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
	case stageLead:
		if value == "" {
			m.err = fmt.Errorf("lead cannot be empty")
			m.history = m.history[:len(m.history)-1]
			return m, nil
		}
		m.spec.Lead = value
		m.transition(stageLeadMode)
	case stageGoal:
		m.spec.Goal = value
		m.transition(stageSeed)
	case stageSeed:
		m.spec.SeedFrom = value
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
	case stageProfile:
		if selected == "__create__" {
			m.transition(stageNewProfile)
			break
		}
		m.spec.Profile = selected
		m.existingIndex = findProfile(m.ctx.Profiles, selected)
		m.transition(stageSession)
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
		m.spec.Effort = setAssignment(m.spec.Effort, m.currentMember().Role, selected)
		m.nextExistingMember()
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
		m.transition(stageLauncherPane)
	case stageTopology:
		m.spec.Visibility = selected
		m.transition(stageLayoutPreset)
	case stageLayoutPreset:
		m.spec.LayoutPreset = selected
		m.transition(stageOperator)
	case stageLauncherPane:
		m.spec.LauncherPane = selected
		m.transition(stageGoal)
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

func (m *BubbleModel) transition(stage bubbleStage) {
	m.stage = stage
	m.configureStage()
}

func (m *BubbleModel) pushHistory() {
	m.history = append(m.history, bubbleSnapshot{
		spec:          m.spec,
		ctx:           m.ctx,
		stage:         m.stage,
		cursor:        m.cursor,
		existingIndex: m.existingIndex,
		roleOrder:     append([]string(nil), m.roleOrder...),
		roleIndex:     m.roleIndex,
		inputValue:    m.input.Value(),
	})
}

func (m BubbleModel) back() (tea.Model, tea.Cmd) {
	if len(m.history) == 0 {
		m.cancelled = true
		return m, tea.Quit
	}
	last := m.history[len(m.history)-1]
	m.history = m.history[:len(m.history)-1]
	m.spec = last.spec
	m.ctx = last.ctx
	m.stage = last.stage
	m.cursor = last.cursor
	m.existingIndex = last.existingIndex
	m.roleOrder = append([]string(nil), last.roleOrder...)
	m.roleIndex = last.roleIndex
	m.configureStage()
	m.cursor = last.cursor
	if m.isTextStage() {
		m.input.SetValue(last.inputValue)
		m.input.CursorEnd()
	}
	return m, textinput.Blink
}

func (m BubbleModel) phaseIndex() int {
	switch m.stage {
	case stageProject:
		return 0
	case stageProfile, stageNewProfile, stageSession, stageExistingOverride, stageExistingModel, stageExistingModelCustom, stageExistingEffort, stageRoles, stageRoleBinary, stageRoleModel, stageRoleModelCustom, stageRoleEffort, stageLead, stageLeadMode:
		return 1
	case stageTopology, stageLayoutPreset, stageOperator, stageSelfOperatorAllow, stageOperatorNotifications, stageLauncherPane:
		return 2
	case stageGoal, stageSeed:
		return 3
	default:
		return 4
	}
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
