package wizard

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// NumberedOptions supplies the preview-only line UI with defaults and an
// injected project inspector. The callback keeps this package independent from
// git and team persistence packages.
type NumberedOptions struct {
	Defaults       Spec
	InspectProject func(project string) (ProjectContext, error)
	ProfileExists  func(project, profile string) bool
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
	Name          string
	MemberCount   int
	PinnedSession string
	Lead          string
	LeadMode      string
	Members       []MemberSummary
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
	r := bufio.NewReader(in)
	s := opts.Defaults

	fmt.Fprintln(out, "amq-squad run start wizard")
	fmt.Fprintln(out, "Preview only: this flow cannot launch agents.")
	fmt.Fprintln(out)

	var err error
	if s.Project, err = promptText(r, out, "Project directory", s.Project); err != nil {
		return Spec{}, err
	}
	ctx := ProjectContext{Project: s.Project}
	if opts.InspectProject != nil {
		ctx, err = opts.InspectProject(s.Project)
		if err != nil {
			return Spec{}, err
		}
		if strings.TrimSpace(ctx.Project) != "" {
			s.Project = ctx.Project
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
	s.Profile = selectedProfile
	sessionDefault := s.Session
	if strings.TrimSpace(sessionDefault) == "" && existingProfile != nil {
		sessionDefault = existingProfile.PinnedSession
	}
	if strings.TrimSpace(sessionDefault) == "" {
		sessionDefault = ctx.SessionSuggestion
	}
	if s.Session, err = promptText(r, out, "Workstream session", sessionDefault); err != nil {
		return Spec{}, err
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
				model, promptErr := promptOptionalOverride(r, out, member.Role+" model override", defaultString(member.Model, "automatic"))
				if promptErr != nil {
					return Spec{}, promptErr
				}
				if model != "" {
					modelOverrides[member.Role] = model
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
			model, promptErr := promptText(r, out, role+" model", defaultString(modelPrefill[role], "automatic"))
			if promptErr != nil {
				return Spec{}, promptErr
			}
			if model != "" && !strings.EqualFold(model, "automatic") {
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
	if s.Goal, err = promptText(r, out, "Goal text (optional)", s.Goal); err != nil {
		return Spec{}, err
	}
	if s.SeedFrom, err = promptText(r, out, "Seed brief from file:/issue:/gh: reference (optional)", s.SeedFrom); err != nil {
		return Spec{}, err
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, "Answers collected. Running the canonical read-only preview next.")
	return s, nil
}

const effortAutomatic = "automatic"

func promptProfile(r *bufio.Reader, out io.Writer, current string, ctx ProjectContext) (string, *ProfileSummary, error) {
	if len(ctx.Profiles) == 0 {
		profile, err := promptText(r, out, "Team profile", defaultString(current, "default"))
		return profile, nil, err
	}
	byName := make(map[string]*ProfileSummary, len(ctx.Profiles))
	choices := make([]choice, 0, len(ctx.Profiles)+1)
	defaultProfile := strings.TrimSpace(current)
	for i := range ctx.Profiles {
		profile := &ctx.Profiles[i]
		byName[profile.Name] = profile
		pinned := defaultString(profile.PinnedSession, "un-pinned")
		choices = append(choices, choice{
			value: profile.Name,
			label: fmt.Sprintf("%s: %d member(s), session %s", profile.Name, profile.MemberCount, pinned),
		})
		if defaultProfile == "" && profile.Name == "default" {
			defaultProfile = profile.Name
		}
	}
	if defaultProfile == "" || byName[defaultProfile] == nil {
		defaultProfile = ctx.Profiles[0].Name
	}
	choices = append(choices, choice{value: "__create__", label: "create a named profile: define a fresh roster"})
	selected, err := promptChoice(r, out, "Team profile", choices, defaultProfile)
	if err != nil {
		return "", nil, err
	}
	if selected != "__create__" {
		return selected, byName[selected], nil
	}
	suggestion := defaultString(ctx.NewProfileSuggestion, "squad-"+defaultString(ctx.SessionSuggestion, "project"))
	profile, err := promptText(r, out, "New profile name", suggestion)
	return profile, nil, err
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
	value string
	label string
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
		fmt.Fprintf(out, "  %d) %s%s\n", i+1, item.label, marker)
	}
	fmt.Fprintf(out, "Choose [default %d]: ", defaultIndex+1)
	line, err := r.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read %s: %w", strings.ToLower(label), err)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return choices[defaultIndex].value, nil
	}
	for i, item := range choices {
		if line == fmt.Sprint(i+1) || strings.EqualFold(line, item.value) {
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
