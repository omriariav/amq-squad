package wizard

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// NumberedOptions supplies the answer-collection line UI with defaults and an
// injected project inspector. The callback keeps this package independent from
// git and team persistence packages.
type NumberedOptions struct {
	Defaults       Spec
	InspectProject func(project string) (ProjectContext, error)
	ProfileExists  func(project, profile string) bool
	Capabilities   CapabilitySet
}

type ProjectContext struct {
	Project              string
	OriginSlug           string
	Branch               string
	SessionSuggestion    string
	NewProfileSuggestion string
	Profiles             []ProfileSummary
	PreferredBinaries    map[string]string
}

type ProfileSummary struct {
	Name                  string
	MemberCount           int
	PinnedSession         string
	Lead                  string
	LeadMode              string
	OperatorMode          string
	OperatorNotifications bool
	SelfOperatorLead      string
	SelfOperatorAllow     string
	SelfOperatorRevision  int64
	SelfOperatorPaused    bool
	Members               []MemberSummary
	Sessions              []SessionSummary
}

type MemberSummary struct {
	Role   string
	Binary string
	Model  string
	Effort string
}

// RunNumbered collects a project run using plain numbered/text prompts. It is
// intended only for a real terminal; the CLI owns that guard. Enter accepts
// every displayed default. The result contains no live-launch bit.
func RunNumbered(in io.Reader, out io.Writer, opts NumberedOptions) (Spec, error) {
	if in == nil || out == nil {
		return Spec{}, fmt.Errorf("wizard numbered UI requires input and output")
	}
	r, ok := in.(*bufio.Reader)
	if !ok {
		r = bufio.NewReader(in)
	}
	s := opts.Defaults

	fmt.Fprintln(out, "amq-squad run start wizard")
	fmt.Fprintln(out, "Answers are previewed first. Launch requires a separate explicit Yes after preview succeeds.")
	fmt.Fprintln(out)

	var err error
	if s.Project, err = promptText(r, out, "Project directory", s.Project); err != nil {
		return Spec{}, err
	}
	ctx := ProjectContext{Project: s.Project, SessionSuggestion: s.Session}
	if opts.InspectProject != nil {
		ctx, err = opts.InspectProject(s.Project)
		if err != nil {
			return Spec{}, err
		}
		if strings.TrimSpace(ctx.Project) != "" {
			s.Project = ctx.Project
		}
		if strings.TrimSpace(ctx.SessionSuggestion) == "" {
			ctx.SessionSuggestion = s.Session
		}
	}
	if ctx.OriginSlug != "" {
		fmt.Fprintf(out, "Detected git project %s (origin %s)\n", s.Project, ctx.OriginSlug)
	} else {
		fmt.Fprintf(out, "Project root: %s\n", s.Project)
	}

	selectedProfile, existingProfile, err := promptProfile(r, out, s.Profile, ctx)
	if err != nil {
		return Spec{}, err
	}
	if existingProfile != nil {
		s.SelectExistingProfile(selectedProfile)
		session, selectErr := promptExistingSession(r, out, *existingProfile, ctx.SessionSuggestion)
		if selectErr != nil {
			return Spec{}, selectErr
		}
		s.SelectExistingSession(session)
		fmt.Fprintf(out, "Derived session %q from %s; existing profiles never accept an arbitrary session name.\n", s.Session, s.SessionSource)
		if s.Backend != BackendRunStart || !s.RunExecutable {
			fmt.Fprintf(out, "Selected run state: %s. Backend: %s. Execution controls for this state arrive in the resume slice; nothing will be previewed or launched.\n", s.RunState, defaultString(string(s.Backend), "none"))
			return s, nil
		}
	} else {
		freshSessionDefault := defaultString(s.Session, ctx.SessionSuggestion)
		s.SelectNewProfile(selectedProfile)
		newSession, promptErr := promptText(r, out, "Name the new session", freshSessionDefault)
		if promptErr != nil {
			return Spec{}, promptErr
		}
		if strings.TrimSpace(newSession) == "" {
			return Spec{}, fmt.Errorf("session cannot be empty")
		}
		s.SelectNewSession(newSession)
	}

	existing := existingProfile != nil || (opts.ProfileExists != nil && opts.ProfileExists(s.Project, s.Profile))
	if existing {
		fmt.Fprintf(out, "Using existing profile %q; roster and lead mode remain authoritative.\n", s.Profile)
		if existingProfile != nil {
			fmt.Fprintf(out, "Lead: %s (%s)\n", defaultString(existingProfile.Lead, "(not configured)"), defaultString(existingProfile.LeadMode, "builder"))
			for _, member := range existingProfile.Members {
				fmt.Fprintf(out, "  - %s: %s, model=%s, effort=%s\n", member.Role, member.Binary, defaultString(member.Model, "automatic"), defaultString(member.Effort, "automatic"))
			}
		}
		fmt.Fprintln(out)
		s.Roles = ""
		s.Binary = ""
		s.Model = ""
		s.Effort = ""
		s.Lead = ""
		s.LeadMode = ""
		if existingProfile != nil {
			modelOverrides := map[string]string{}
			effortOverrides := map[string]string{}
			memberOrder := make([]string, 0, len(existingProfile.Members))
			for _, member := range existingProfile.Members {
				memberOrder = append(memberOrder, member.Role)
				override, choiceErr := promptChoice(r, out, "Override "+member.Role+" at launch", []choice{
					{value: "keep", label: "keep profile model/effort"},
					{value: "override", label: "override this role's model/effort for this launch only"},
				}, "keep")
				if choiceErr != nil {
					return Spec{}, choiceErr
				}
				if override != "override" {
					continue
				}
				modelSel, promptErr := promptChoice(r, out, member.Role+" model override", existingOverrideModelChoices(member), modelKeepChoice)
				if promptErr != nil {
					return Spec{}, promptErr
				}
				if modelSel == modelCustomChoice {
					custom, customErr := promptOptionalOverride(r, out, member.Role+" model override", defaultString(member.Model, "automatic"))
					if customErr != nil {
						return Spec{}, customErr
					}
					if custom != "" {
						modelOverrides[member.Role] = custom
					}
				} else if modelSel != modelKeepChoice {
					modelOverrides[member.Role] = modelSel
				}
				effortChoices := []choice{{value: "automatic", label: "automatic: ignore stored effort for this launch"}, {value: "low", label: "low"}, {value: "medium", label: "medium"}, {value: "high", label: "high"}}
				if member.Binary == "codex" {
					effortChoices = append(effortChoices, choice{value: "minimal", label: "minimal"}, choice{value: "xhigh", label: "xhigh"})
				}
				effort, promptErr := promptChoice(r, out, member.Role+" effort override", effortChoices, defaultString(member.Effort, effortAutomatic))
				if promptErr != nil {
					return Spec{}, promptErr
				}
				effortOverrides[member.Role] = effort
			}
			s.Model = renderAssignments(memberOrder, modelOverrides)
			s.Effort = renderAssignments(memberOrder, effortOverrides)
			s.OperatorMode = defaultString(existingProfile.OperatorMode, "unspecified")
			s.OperatorNotifications = existingProfile.OperatorNotifications
		}
	} else {
		if s.Roles, err = promptText(r, out, "Roles (comma-separated)", defaultString(s.Roles, "cto,senior-dev,qa")); err != nil {
			return Spec{}, err
		}
		roles := splitAssignmentsList(s.Roles)
		binaryPrefill := parseAssignments(s.Binary)
		modelPrefill := parseAssignments(s.Model)
		effortPrefill := parseAssignments(s.Effort)
		binaryValues := make(map[string]string, len(roles))
		modelValues := make(map[string]string, len(roles))
		effortValues := make(map[string]string, len(roles))
		for _, role := range roles {
			binaryDefault := defaultString(binaryPrefill[role], ctx.PreferredBinaries[role])
			if binaryDefault != "claude" {
				binaryDefault = "codex"
			}
			binaryValues[role], err = promptChoice(r, out, role+" binary", []choice{
				{value: "codex", label: "codex"},
				{value: "claude", label: "claude"},
			}, binaryDefault)
			if err != nil {
				return Spec{}, err
			}
			model, promptErr := promptChoice(r, out, role+" model", modelChoices(binaryValues[role]), defaultModelChoice(modelPrefill[role], binaryValues[role]))
			if promptErr != nil {
				return Spec{}, promptErr
			}
			if model == modelCustomChoice {
				if model, promptErr = promptText(r, out, role+" model name", defaultString(modelPrefill[role], "automatic")); promptErr != nil {
					return Spec{}, promptErr
				}
			}
			if model != "" && !strings.EqualFold(model, effortAutomatic) {
				modelValues[role] = model
			}
			effortChoices := []choice{{value: "automatic", label: "automatic: let the binary choose"}, {value: "low", label: "low"}, {value: "medium", label: "medium"}, {value: "high", label: "high"}}
			if binaryValues[role] == "codex" {
				effortChoices = append(effortChoices, choice{value: "minimal", label: "minimal"}, choice{value: "xhigh", label: "xhigh"})
			}
			effort, promptErr := promptChoice(r, out, role+" effort", effortChoices, defaultString(effortPrefill[role], "automatic"))
			if promptErr != nil {
				return Spec{}, promptErr
			}
			if effort != effortAutomatic {
				effortValues[role] = effort
			}
		}
		s.Binary = renderAssignments(roles, binaryValues)
		s.Model = renderAssignments(roles, modelValues)
		s.Effort = renderAssignments(roles, effortValues)
		if s.Lead, err = promptText(r, out, "Lead role", defaultString(s.Lead, "cto")); err != nil {
			return Spec{}, err
		}
		if s.LeadMode, err = promptChoice(r, out, "Lead mode", []choice{
			{value: "builder", label: "builder: lead may implement and delegate"},
			{value: "planner", label: "planner: lead must delegate mutations"},
		}, defaultString(s.LeadMode, "builder")); err != nil {
			return Spec{}, err
		}
	}

	if s.Visibility, err = promptChoice(r, out, "Topology", []choice{
		{value: "sibling-tabs", label: "sibling-tabs: one visible tmux window per agent"},
		{value: "detached", label: "detached: hidden tmux session"},
		{value: "current", label: "current: split the current tmux window"},
	}, defaultString(s.Visibility, "sibling-tabs")); err != nil {
		return Spec{}, err
	}
	if s.LayoutPreset, err = promptChoice(r, out, "Layout preset", layoutPresetChoices(s.Visibility), defaultLayoutPreset(s.LayoutPreset, s.Visibility)); err != nil {
		return Spec{}, err
	}
	if existing {
		mode := defaultString(s.OperatorMode, "unspecified")
		fmt.Fprintf(out, "Operator interaction (authoritative): %s · %s. Change it with 'amq-squad team operator set', then relaunch.\n", mode, operatorContractSummary(mode))
		if s.OperatorMode == "self_operator" {
			fmt.Fprintf(out, "Self-operator policy (authoritative): lead=%s session=%s allow=%s revision=%d paused=%t notifications=%t\n", existingProfile.SelfOperatorLead, s.Session, existingProfile.SelfOperatorAllow, existingProfile.SelfOperatorRevision, existingProfile.SelfOperatorPaused, s.OperatorNotifications)
		}
		for _, item := range operatorChoices(opts.Capabilities) {
			if item.capability {
				fmt.Fprintf(out, "  - %s [locked: the stored profile contract decides]\n", item.label)
			}
		}
		fmt.Fprintln(out)
	} else if s.OperatorMode, err = promptOperatorChoice(r, out, opts.Capabilities, defaultOperatorMode(s.OperatorMode, s.Visibility)); err != nil {
		return Spec{}, err
	}
	if !existing && s.OperatorMode == "self_operator" {
		fmt.Fprintln(out, "Self-operator exclusions: spawn, release, tag, publish, external send, and destructive filesystem remain human-only. A different verified actor must execute an approved merge.")
		if s.SelfOperatorLead, err = promptText(r, out, "Self-operator lead", defaultString(s.SelfOperatorLead, s.Lead)); err != nil {
			return Spec{}, err
		}
		if s.SelfOperatorAllow, err = promptText(r, out, "Self-operator allowlist (explicitly type merge; no default)", ""); err != nil {
			return Spec{}, err
		}
		if strings.TrimSpace(s.SelfOperatorAllow) != "merge" {
			return Spec{}, fmt.Errorf("self-operator allowlist must explicitly be merge; spawn and immutable exclusions remain human-only")
		}
	}
	if existing {
		fmt.Fprintf(out, "Operator notifications (authoritative): %t\n", s.OperatorNotifications)
	} else {
		choice, choiceErr := promptChoice(r, out, "Operator notification add-on", []choice{{value: "no", label: "No notifications"}, {value: "yes", label: "Attention-only desktop notifications"}}, map[bool]string{true: "yes", false: "no"}[s.OperatorNotifications])
		if choiceErr != nil {
			return Spec{}, choiceErr
		}
		s.OperatorNotifications = choice == "yes"
	}
	if s.LauncherPane, err = promptChoice(r, out, "Launcher pane", launcherPaneChoices(s.Visibility, s.ExternalLead), defaultLauncherPane(s.LauncherPane, s.Visibility, s.ExternalLead)); err != nil {
		return Spec{}, err
	}
	if s.Goal, err = promptText(r, out, "Goal text (optional)", s.Goal); err != nil {
		return Spec{}, err
	}
	if s.SeedFrom, err = promptText(r, out, "Seed brief from file:/issue:/gh: reference (optional)", s.SeedFrom); err != nil {
		return Spec{}, err
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, "Answers collected. Running the canonical preview next; live launch is a separate default-No decision.")
	return s, nil
}

const (
	effortAutomatic   = "automatic"
	modelCustomChoice = "custom"
	modelKeepChoice   = "keep"
)

// modelChoices offers common models per binary. Models pass through to the
// binary verbatim, so this list is a convenience, never an allowlist; the
// custom row keeps free-text entry available.
func modelChoices(binary string) []choice {
	choices := []choice{{value: effortAutomatic, label: "automatic: let the binary choose"}}
	if strings.EqualFold(binary, "claude") {
		choices = append(choices,
			choice{value: "fable", label: "fable"},
			choice{value: "opus", label: "opus"},
			choice{value: "sonnet", label: "sonnet"},
			choice{value: "haiku", label: "haiku"},
		)
	} else {
		choices = append(choices,
			choice{value: "gpt-5.6-sol", label: "gpt-5.6-sol"},
			choice{value: "gpt-5.6-terra", label: "gpt-5.6-terra"},
		)
	}
	return append(choices, choice{value: modelCustomChoice, label: "custom: type a model name"})
}

// existingOverrideModelChoices frames the same list for a launch-only override:
// keep replaces automatic because clearing the override falls back to the
// stored profile value, not to the binary's default.
func existingOverrideModelChoices(member MemberSummary) []choice {
	choices := []choice{{value: modelKeepChoice, label: "keep profile model: " + defaultString(member.Model, "automatic")}}
	for _, item := range modelChoices(member.Binary) {
		if item.value != effortAutomatic {
			choices = append(choices, item)
		}
	}
	return choices
}

func defaultModelChoice(prefill, binary string) string {
	prefill = strings.TrimSpace(prefill)
	if prefill == "" {
		return effortAutomatic
	}
	for _, item := range modelChoices(binary) {
		if strings.EqualFold(item.value, prefill) {
			return item.value
		}
	}
	return modelCustomChoice
}

func promptProfile(r *bufio.Reader, out io.Writer, current string, ctx ProjectContext) (string, *ProfileSummary, error) {
	if len(ctx.Profiles) == 0 {
		profile, err := promptText(r, out, "Name the new profile", defaultString(current, defaultString(ctx.NewProfileSuggestion, "default")))
		return profile, nil, err
	}
	byName := make(map[string]*ProfileSummary, len(ctx.Profiles))
	choices := make([]choice, 0, len(ctx.Profiles)+1)
	defaultProfile := strings.TrimSpace(current)
	for i := range ctx.Profiles {
		profile := &ctx.Profiles[i]
		byName[profile.Name] = profile
		choices = append(choices, choice{
			value: profile.Name,
			label: fmt.Sprintf("%s · %d members · %s · roster and contract stay authoritative", profile.Name, profile.MemberCount, profileRunSummary(*profile, ctx.SessionSuggestion)),
		})
		if defaultProfile == "" && profile.Name == "default" {
			defaultProfile = profile.Name
		}
	}
	if defaultProfile == "" || byName[defaultProfile] == nil {
		defaultProfile = ctx.Profiles[0].Name
	}
	choices = append(choices, choice{value: "__create__", label: "Create a new profile · choose a fresh roster and contract"})
	selected, err := promptChoice(r, out, "Use an existing team setup or create a new one?", choices, defaultProfile)
	if err != nil {
		return "", nil, err
	}
	if selected != "__create__" {
		return selected, byName[selected], nil
	}
	suggestion := defaultString(current, defaultString(ctx.NewProfileSuggestion, "squad-"+defaultString(ctx.SessionSuggestion, "project")))
	profile, err := promptText(r, out, "Name the new profile", suggestion)
	return profile, nil, err
}

func promptExistingSession(r *bufio.Reader, out io.Writer, profile ProfileSummary, suggestion string) (SessionSummary, error) {
	sessions := profileSessions(profile, suggestion)
	if len(sessions) == 0 {
		return SessionSummary{}, fmt.Errorf("profile %q has no derivable session", profile.Name)
	}
	if len(sessions) == 1 {
		fmt.Fprintf(out, "Known run: %s\n", sessions[0].Label())
		return sessions[0], nil
	}
	choices := make([]choice, 0, len(sessions))
	byName := make(map[string]SessionSummary, len(sessions))
	for _, session := range sessions {
		choices = append(choices, choice{value: session.Name, label: session.Label()})
		byName[session.Name] = session
	}
	selected, err := promptChoice(r, out, "Which existing run do you want?", choices, sessions[0].Name)
	if err != nil {
		return SessionSummary{}, err
	}
	return byName[selected], nil
}

func splitAssignmentsList(raw string) []string {
	seen := map[string]bool{}
	var out []string
	for _, item := range strings.Split(raw, ",") {
		item = strings.ToLower(strings.TrimSpace(item))
		if item != "" && !seen[item] {
			seen[item] = true
			out = append(out, item)
		}
	}
	return out
}

func parseAssignments(raw string) map[string]string {
	out := map[string]string{}
	for _, item := range strings.Split(raw, ",") {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			out[key] = value
		}
	}
	return out
}

func renderAssignments(order []string, values map[string]string) string {
	parts := make([]string, 0, len(values))
	for _, key := range order {
		if value := strings.TrimSpace(values[key]); value != "" {
			parts = append(parts, key+"="+value)
		}
	}
	return strings.Join(parts, ",")
}

type choice struct {
	value       string
	label       string
	disabled    bool
	consequence string
	capability  bool
}

func promptOperatorChoice(r *bufio.Reader, out io.Writer, caps CapabilitySet, current string) (string, error) {
	return promptChoice(r, out, "Operator interaction", operatorChoices(caps), current)
}

func promptText(r *bufio.Reader, out io.Writer, label, current string) (string, error) {
	fmt.Fprintf(out, "%s [%s]: ", label, current)
	line, err := r.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read %s: %w", strings.ToLower(label), err)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return current, nil
	}
	return line, nil
}

func promptOptionalOverride(r *bufio.Reader, out io.Writer, label, current string) (string, error) {
	fmt.Fprintf(out, "%s [current %s; Enter keeps it]: ", label, current)
	line, err := r.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read %s: %w", strings.ToLower(label), err)
	}
	return strings.TrimSpace(line), nil
}

func promptChoice(r *bufio.Reader, out io.Writer, label string, choices []choice, current string) (string, error) {
	fmt.Fprintln(out)
	fmt.Fprintln(out, label+":")
	defaultIndex := 0
	for i, item := range choices {
		marker := ""
		if item.value == current {
			defaultIndex = i
			marker = " (default)"
		}
		if item.disabled {
			marker += " (disabled)"
		}
		fmt.Fprintf(out, "  %d) %s%s\n", i+1, item.label, marker)
	}
	fmt.Fprintf(out, "Choose [default %d]: ", defaultIndex+1)
	line, err := r.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read %s: %w", strings.ToLower(label), err)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		if choices[defaultIndex].disabled {
			return "", fmt.Errorf("default %s choice is unavailable", strings.ToLower(label))
		}
		return choices[defaultIndex].value, nil
	}
	for i, item := range choices {
		if line == fmt.Sprint(i+1) || strings.EqualFold(line, item.value) {
			if item.disabled {
				return "", fmt.Errorf("%s choice %q is unavailable", strings.ToLower(label), item.value)
			}
			return item.value, nil
		}
	}
	return "", fmt.Errorf("invalid %s choice %q", strings.ToLower(label), line)
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func defaultOperatorMode(current, visibility string) string {
	if strings.TrimSpace(current) != "" {
		return current
	}
	if visibility == "detached" {
		return "separate_terminal"
	}
	return "lead_pane"
}

func launcherPaneChoices(visibility string, external bool) []choice {
	if external || visibility == "detached" {
		return []choice{{value: "keep", label: "keep: required because the launcher remains the lead/control point"}}
	}
	return []choice{{value: "close-after-start", label: "close-after-start: close only after successful final output"}, {value: "keep", label: "keep: leave this launcher pane open"}}
}
