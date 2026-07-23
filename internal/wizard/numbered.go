package wizard

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/agentcatalog"
)

// ErrCancelled reports an explicit q/quit/cancel answer from the numbered
// adapter without turning cancellation into a usage failure.
var ErrCancelled = errors.New("wizard cancelled")

// NumberedOptions supplies the answer-collection line UI with defaults and an
// injected project inspector. The callback keeps this package independent from
// git and team persistence packages.
type NumberedOptions struct {
	Defaults        Spec
	InspectProject  func(project string) (ProjectContext, error)
	LoadCatalog     func(root string) agentcatalog.Catalog
	ProfileExists   func(project, profile string) bool
	Capabilities    CapabilitySet
	TerminalContext TerminalContext
	StartAtProfile  bool
	RestartMessage  string
}

type ProjectContext struct {
	Project              string
	OriginSlug           string
	Branch               string
	SessionSuggestion    string
	NewProfileSuggestion string
	Profiles             []ProfileSummary
	PreferredBinaries    map[string]string
	Catalog              agentcatalog.Catalog
}

type ProfileSummary struct {
	Name        string
	MemberCount int
	// PinnedSession is the one shared member session (or "" when members
	// disagree, have none, or the profile is a genuine template). Unpinned is
	// the unambiguous signal for "genuine #451 template profile" specifically
	// — never infer that from PinnedSession=="" alone.
	PinnedSession         string
	Unpinned              bool
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
	s := opts.Defaults.Clone()

	fmt.Fprintln(out, "amq-squad run start wizard")
	fmt.Fprintln(out, "Answers are previewed first. Launch requires a separate explicit Yes after preview succeeds.")
	fmt.Fprintln(out, opts.TerminalContext.Summary())
	fmt.Fprintln(out)

	var err error
	if opts.StartAtProfile {
		fmt.Fprintln(out, defaultString(opts.RestartMessage, "The selected profile or run changed while the wizard was open. Review the refreshed facts before continuing."))
	} else {
		fmt.Fprintln(out, "Project runs use one repository's profiles and sessions. Global/NOC starts one coordinator and does not own project wake mailboxes.")
		s.Scope, err = promptChoice(r, out, "What do you want to run?", []choice{
			{value: "project", label: "Project squad"},
			{value: "global", label: "Global / NOC orchestrator"},
		}, defaultString(strings.ToLower(strings.TrimSpace(s.Scope)), "project"))
		if err != nil {
			return Spec{}, err
		}
		if s.Scope == "global" {
			s.Backend = BackendGlobalStart
			return runNumberedGlobal(r, out, s, opts.LoadCatalog)
		}
		s.Backend = BackendRunStart
		if s.Project, err = promptText(r, out, "Project directory", s.Project); err != nil {
			return Spec{}, err
		}
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
	cloning := false
	if existingProfile != nil {
		s.SelectExistingProfile(selectedProfile)
		session, wantsClone, selectErr := promptExistingSession(r, out, *existingProfile, ctx.SessionSuggestion)
		if selectErr != nil {
			return Spec{}, selectErr
		}
		if wantsClone {
			cloning = true
			cloneSessionDefault := defaultString(ctx.SessionSuggestion, "issue-1")
			newSession, promptErr := promptText(r, out, "Name the new workstream session", cloneSessionDefault)
			if promptErr != nil {
				return Spec{}, promptErr
			}
			newSession = strings.ToLower(strings.TrimSpace(newSession))
			if newSession == "" {
				return Spec{}, fmt.Errorf("session cannot be empty")
			}
			newProfile, promptErr := promptText(r, out, "Name the new profile", existingProfile.Name+"-"+newSession)
			if promptErr != nil {
				return Spec{}, promptErr
			}
			s.FromProfile = existingProfile.Name
			s.SelectNewProfile(newProfile)
			s.SelectNewSession(newSession)
			fmt.Fprintf(out, "Cloning profile %q's roster into new profile %q for session %q.\n", existingProfile.Name, s.Profile, s.Session)
		} else {
			s.SelectExistingSession(session)
			fmt.Fprintf(out, "Derived session %q from %s; existing profiles never accept an arbitrary session name.\n", s.Session, s.SessionSource)
			if !s.RunExecutable {
				fmt.Fprintf(out, "Selected run state: %s. Backend: %s. This run is read-only in the wizard; nothing will be previewed or launched.\n", s.RunState, defaultString(string(s.Backend), "none"))
				return s, nil
			}
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
		if cloning {
			fmt.Fprintf(out, "Cloning roster from %q into new profile %q; roster and lead mode remain authoritative from the source.\n", s.FromProfile, s.Profile)
			if existingProfile.OperatorMode == "self_operator" {
				fmt.Fprintln(out, "Note: self-operator policy is exact-session scoped and is not carried over by the clone; configure it for the new session with 'amq-squad team operator set' if needed.")
			}
		} else {
			fmt.Fprintf(out, "Using existing profile %q; roster and lead mode remain authoritative.\n", s.Profile)
		}
		s.Roles = ""
		s.Binary = ""
		s.Model = ""
		s.Effort = ""
		s.Lead = ""
		s.LeadMode = ""
		if existingProfile != nil && s.Backend == BackendResume {
			modelOverrides := map[string]string{}
			effortOverrides := map[string]string{}
			memberOrder := make([]string, 0, len(s.ResumeMembers))
			for _, member := range s.ResumeMembers {
				memberOrder = append(memberOrder, member.Role)
				switch member.Action {
				case MemberActionLive:
					fmt.Fprintf(out, "%s is already live; resume keeps its model=%s effort=%s unchanged.\n", member.Role, defaultString(member.Model, "automatic"), defaultString(member.Effort, "automatic"))
				case MemberActionRestore:
					fmt.Fprintf(out, "%s restores saved launch %s read-only (binary=%s model=%s effort=%s saved extra args=%s).\n", member.Role, defaultString(member.SavedLaunchIdentity, "recorded"), defaultString(member.SavedBinary, member.Binary), defaultString(member.SavedModel, "automatic"), defaultString(member.SavedEffort, "automatic"), FormatSavedNativeArgs(member.SavedNativeArgs))
				case MemberActionFresh:
					summary := MemberSummary{Role: member.Role, Binary: member.Binary, Model: member.Model, Effort: member.Effort}
					modelSel, promptErr := promptChoice(r, out, member.Role+" fresh-launch model", existingOverrideModelChoicesCatalog(summary, ctx.Catalog), modelKeepChoice)
					if promptErr != nil {
						return Spec{}, promptErr
					}
					if modelSel == modelCustomChoice {
						custom, customErr := promptOptionalOverride(r, out, member.Role+" fresh-launch model", defaultString(member.Model, "automatic"))
						if customErr != nil {
							return Spec{}, customErr
						}
						if custom != "" {
							modelOverrides[member.Role] = custom
						}
					} else if modelSel != modelKeepChoice {
						modelOverrides[member.Role] = modelSel
					}
					effortSel, effortErr := promptChoice(r, out, member.Role+" fresh-launch effort", existingOverrideEffortChoices(summary, ctx.Catalog), effortKeepChoice)
					if effortErr != nil {
						return Spec{}, effortErr
					}
					if effortSel == effortCustomChoice {
						custom, customErr := promptOptionalOverride(r, out, member.Role+" fresh-launch effort", defaultString(member.Effort, effortAutomatic))
						if customErr != nil {
							return Spec{}, customErr
						}
						if custom != "" && !strings.EqualFold(custom, effortAutomatic) {
							effortOverrides[member.Role] = custom
						}
					} else if effortSel != effortKeepChoice {
						effortOverrides[member.Role] = effortSel
					}
				}
			}
			s.Model = renderAssignments(memberOrder, modelOverrides)
			s.Effort = renderAssignments(memberOrder, effortOverrides)
			s.OperatorMode = defaultString(existingProfile.OperatorMode, "unspecified")
			s.OperatorNotifications = existingProfile.OperatorNotifications
		} else if existingProfile != nil {
			fmt.Fprintf(out, "Lead: %s (%s)\n", defaultString(existingProfile.Lead, "(not configured)"), defaultString(existingProfile.LeadMode, "builder"))
			for _, member := range existingProfile.Members {
				fmt.Fprintf(out, "  - %s: %s, model=%s, effort=%s\n", member.Role, member.Binary, defaultString(member.Model, "automatic"), defaultString(member.Effort, "automatic"))
			}
		}
		fmt.Fprintln(out)
		if existingProfile != nil && s.Backend != BackendResume {
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
				modelSel, promptErr := promptChoice(r, out, member.Role+" model override", existingOverrideModelChoicesCatalog(member, ctx.Catalog), modelKeepChoice)
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
				effort, promptErr := promptChoice(r, out, member.Role+" effort override", existingOverrideEffortChoices(member, ctx.Catalog), effortKeepChoice)
				if promptErr != nil {
					return Spec{}, promptErr
				}
				if effort == effortCustomChoice {
					custom, customErr := promptOptionalOverride(r, out, member.Role+" effort override", defaultString(member.Effort, effortAutomatic))
					if customErr != nil {
						return Spec{}, customErr
					}
					if custom != "" {
						effortOverrides[member.Role] = custom
					}
				} else if effort != effortKeepChoice {
					effortOverrides[member.Role] = effort
				}
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
			model, promptErr := promptChoice(r, out, role+" model", modelChoicesCatalog(binaryValues[role], ctx.Catalog), defaultModelChoiceCatalog(modelPrefill[role], binaryValues[role], ctx.Catalog))
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
			effort, promptErr := promptChoice(r, out, role+" effort", effortChoicesCatalog(binaryValues[role], ctx.Catalog), defaultEffortChoiceCatalog(effortPrefill[role], binaryValues[role], ctx.Catalog, effortAutomatic))
			if promptErr != nil {
				return Spec{}, promptErr
			}
			if effort == effortCustomChoice {
				if effort, promptErr = promptText(r, out, role+" effort tier", defaultString(effortPrefill[role], effortAutomatic)); promptErr != nil {
					return Spec{}, promptErr
				}
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
		if s.LaunchShape, err = promptChoice(r, out, "Launch shape", []choice{
			{value: LaunchShapeWorkingTeamTogether, label: "Start the working team together: every selected initial member launches after the final approval"},
			{value: LaunchShapeLeadOnlyStaged, label: "Lead-only staged bootstrap: only the lead launches; every later role requires its own durable spawn gate"},
		}, s.LaunchShape); err != nil {
			return Spec{}, err
		}
		if err := s.ApplyLaunchShape(); err != nil {
			return Spec{}, err
		}
		roles = splitAssignmentsList(s.Roles)
		if s.StagedRoles, err = promptText(r, out, "Staged later roles (comma-separated; optional)", s.StagedRoles); err != nil {
			return Spec{}, err
		}
		if err := s.ApplyLaunchShape(); err != nil {
			return Spec{}, err
		}
		recommended := recommendedToolProfileAssignments(roles, s.Lead)
		full := fullToolProfileAssignments(roles)
		if s.ToolPolicyMode, err = promptChoice(r, out, "Tool policy", []choice{
			{value: "recommended", label: "Recommended: broad lead + catalog-minimum lean workers · " + recommended},
			{value: "full_all", label: "Full for all: explicit broad access (warning: 2+ full workers increase duplicated MCP context and memory/concurrency cost) · " + full},
		}, defaultString(s.ToolPolicyMode, "recommended")); err != nil {
			return Spec{}, err
		}
		if s.ToolPolicyMode == "full_all" {
			s.ToolProfile = full
			fmt.Fprintln(out, "Warning: multiple full workers duplicate MCP/plugin context and increase memory and concurrency pressure.")
		} else {
			s.ToolProfile = recommended
		}
	}

	if s.Visibility, err = promptChoice(r, out, "Topology", []choice{
		{value: "sibling-tabs", label: annotateTopologyChoice(opts.TerminalContext, "sibling-tabs", "sibling-tabs: one visible tmux window per agent")},
		{value: "detached", label: annotateTopologyChoice(opts.TerminalContext, "detached", "detached: hidden tmux session")},
		{value: "current", label: annotateTopologyChoice(opts.TerminalContext, "current", "current: split the current tmux window")},
	}, recommendedTopology(s.Visibility, s.VisibilityExplicit, opts.TerminalContext)); err != nil {
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
	if s.Backend == BackendResume {
		s.LauncherPane = ""
		s.Goal = ""
		s.SeedFrom = ""
		fmt.Fprintln(out, "This wizard pane stays open; resume only opens missing agent panes.")
		fmt.Fprintf(out, "Brief preserved for resume: path=%s\ngoal excerpt:\n%s\nseed source=%s\n", displayValue(s.BriefPath), GoalExcerpt(s.BriefGoal), displayValue(s.BriefSeed))
		if s.ResumeGoalPlan.Eligible {
			fmt.Fprintf(out, "Recorded goal evidence (bounded):\n%s\n", GoalExcerpt(s.ResumeGoalPlan.Goal))
			defaultChoice := "no"
			if s.RedeliverGoal {
				defaultChoice = "yes"
			}
			choice, choiceErr := promptChoice(r, out, "Re-deliver the recorded lead goal after verified re-orientation?", []choice{
				{value: "no", label: "No · keep the restored binding without creating a new attempt"},
				{value: "yes", label: "Yes · create one new claim-once attempt after launch verification"},
			}, defaultChoice)
			if choiceErr != nil {
				return Spec{}, choiceErr
			}
			s.RedeliverGoal = choice == "yes"
		} else {
			s.RedeliverGoal = false
			fmt.Fprintf(out, "Recorded goal redelivery: %s · %s\n", defaultString(s.ResumeGoalPlan.Action, "unavailable"), s.ResumeGoalPlan.Reason)
		}
	} else {
		if s.LauncherPane, err = promptChoice(r, out, "Launcher pane", launcherPaneChoices(s.Visibility, s.ExternalLead), defaultLauncherPane(s.LauncherPane, s.Visibility, s.ExternalLead)); err != nil {
			return Spec{}, err
		}
		if s.Goal, err = promptText(r, out, "Goal text (optional)", s.Goal); err != nil {
			return Spec{}, err
		}
		if s.SeedFrom, err = promptText(r, out, "Seed brief from file:/issue:/gh: reference (optional)", s.SeedFrom); err != nil {
			return Spec{}, err
		}
		// Resolve accepted-brief bindings for review. A missing/stub binding is
		// rendered as unverified here and rejected by the final CLI readiness
		// gate before canonical preview or Launch now is available.
		_ = s.ResolveGoalBinding()
	}

	previewCommand, liveCommand, commandErr := s.CommandForms()
	if commandErr != nil {
		return Spec{}, commandErr
	}
	goal, seed := s.Goal, s.SeedFrom
	if s.Backend == BackendResume {
		goal, seed = s.BriefGoal, s.BriefSeed
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Review")
	fmt.Fprintln(out, topologyDiagnostic(opts.TerminalContext, s.Visibility))
	for _, warning := range effortCatalogWarnings(s, ctx) {
		fmt.Fprintln(out, warning)
	}
	fmt.Fprintf(out, "Goal excerpt:\n%s\nSeed source: %s\n", GoalExcerpt(goal), displayValue(seed))
	if s.Backend != BackendResume {
		fmt.Fprintf(out, "Goal binding: %s\n", s.GoalBindingReview())
	}
	if s.ToolProfile != "" {
		fmt.Fprintf(out, "Tool policy: %s · %s\n", defaultString(s.ToolPolicyMode, "recommended"), s.ToolProfile)
		if countFullToolProfiles(s.ToolProfile) >= 2 {
			fmt.Fprintln(out, "WARNING: 2+ full workers duplicate MCP/plugin context and increase memory/concurrency pressure.")
		}
	}
	if s.Roles != "" {
		fmt.Fprintln(out, s.LaunchRosterReview())
	}
	fmt.Fprintf(out, "Preview command: %s\nLive command: %s\n", previewCommand, liveCommand)
	fmt.Fprintln(out, "Answers collected. Running the canonical preview next; live launch is a separate default-No decision.")
	return s, nil
}

func runNumberedGlobal(r *bufio.Reader, out io.Writer, s Spec, loadCatalog func(string) agentcatalog.Catalog) (Spec, error) {
	var err error
	fmt.Fprintln(out, "This is a neutral control root, not a project profile or session.")
	if s.GlobalRoot, err = promptText(r, out, "Where should the global orchestrator run?", s.GlobalRoot); err != nil {
		return Spec{}, err
	}
	if strings.TrimSpace(s.GlobalRoot) == "" {
		return Spec{}, fmt.Errorf("global root cannot be empty")
	}
	catalog := agentcatalog.Builtins()
	if loadCatalog != nil {
		catalog = loadCatalog(s.GlobalRoot)
	}
	if s.GlobalAgent, err = promptChoice(r, out, "Which agent should run the global orchestrator?", []choice{
		{value: "claude", label: "Claude"},
		{value: "codex", label: "Codex"},
	}, defaultString(strings.ToLower(strings.TrimSpace(s.GlobalAgent)), "claude")); err != nil {
		return Spec{}, err
	}
	model, err := promptChoice(r, out, "Model", modelChoicesCatalog(s.GlobalAgent, catalog), defaultModelChoiceCatalog(s.GlobalModel, s.GlobalAgent, catalog))
	if err != nil {
		return Spec{}, err
	}
	if model == modelCustomChoice {
		if model, err = promptText(r, out, "Custom model", s.GlobalModel); err != nil {
			return Spec{}, err
		}
	}
	if strings.EqualFold(model, effortAutomatic) {
		model = ""
	}
	s.GlobalModel = strings.TrimSpace(model)
	effort, err := promptChoice(r, out, "Effort", effortChoicesCatalog(s.GlobalAgent, catalog), defaultEffortChoiceCatalog(s.GlobalEffort, s.GlobalAgent, catalog, effortAutomatic))
	if err != nil {
		return Spec{}, err
	}
	if effort == effortCustomChoice {
		if effort, err = promptText(r, out, "Custom effort", s.GlobalEffort); err != nil {
			return Spec{}, err
		}
	}
	if strings.EqualFold(effort, effortAutomatic) {
		effort = ""
	}
	s.GlobalEffort = strings.TrimSpace(effort)
	if s.GlobalAgent == "codex" {
		if s.GlobalCodexArgs, err = promptText(r, out, "Codex extra native args (excluding effort)", s.GlobalCodexArgs); err != nil {
			return Spec{}, err
		}
		s.GlobalClaudeArgs = ""
	} else {
		if s.GlobalClaudeArgs, err = promptText(r, out, "Claude extra native args (excluding effort)", s.GlobalClaudeArgs); err != nil {
			return Spec{}, err
		}
		s.GlobalCodexArgs = ""
	}
	if s.GlobalWindow, err = promptText(r, out, "Window name", defaultString(s.GlobalWindow, "global-orch")); err != nil {
		return Spec{}, err
	}
	if strings.TrimSpace(s.GlobalWindow) == "" {
		return Spec{}, fmt.Errorf("window name cannot be empty")
	}

	previewCommand, liveCommand, commandErr := s.CommandForms()
	if commandErr != nil {
		return Spec{}, commandErr
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Review")
	for _, warning := range effortCatalogWarnings(s, ProjectContext{Catalog: catalog}) {
		fmt.Fprintln(out, warning)
	}
	fmt.Fprintf(out, "Scope: Global / NOC orchestrator\nNeutral root: %s\nAgent: %s\nModel: %s\nEffort: %s\nWindow: %s\nNOC contract: poll explicit project/profile/session namespaces; this global orchestrator owns no wake mailbox.\nPreview command: %s\nLive command: %s\n", s.GlobalRoot, s.GlobalAgent, displayValue(s.GlobalModel), defaultString(s.GlobalEffort, effortAutomatic), s.GlobalWindow, previewCommand, liveCommand)
	fmt.Fprintln(out, "Answers collected. Running the canonical preview next; live launch is a separate default-No decision.")
	return s, nil
}

func displayValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "not provided"
	}
	return value
}

const (
	effortAutomatic    = "automatic"
	modelCustomChoice  = "custom"
	modelKeepChoice    = "keep"
	effortCustomChoice = "custom"
	effortKeepChoice   = "keep"
)

// modelChoices offers common models per binary. Models pass through to the
// binary verbatim, so this list is a convenience, never an allowlist; the
// custom row keeps free-text entry available.
func modelChoices(binary string) []choice {
	return modelChoicesCatalog(binary, agentcatalog.Builtins())
}

func modelChoicesCatalog(binary string, catalog agentcatalog.Catalog) []choice {
	choices := []choice{{value: effortAutomatic, label: "automatic: let the binary choose"}}
	for _, entry := range catalog.Entries(binary, agentcatalog.Models) {
		choices = append(choices, choice{value: entry.Value, label: entry.Label})
	}
	return append(choices, choice{value: modelCustomChoice, label: "custom: type a model name"})
}

// existingOverrideModelChoices frames the same list for a launch-only override:
// keep replaces automatic because clearing the override falls back to the
// stored profile value, not to the binary's default.
func existingOverrideModelChoices(member MemberSummary) []choice {
	return existingOverrideModelChoicesCatalog(member, agentcatalog.Builtins())
}

func existingOverrideModelChoicesCatalog(member MemberSummary, catalog agentcatalog.Catalog) []choice {
	choices := []choice{{value: modelKeepChoice, label: "keep profile model: " + defaultString(member.Model, "automatic")}}
	for _, item := range modelChoicesCatalog(member.Binary, catalog) {
		if item.value != effortAutomatic {
			choices = append(choices, item)
		}
	}
	return choices
}

func defaultModelChoice(prefill, binary string) string {
	return defaultModelChoiceCatalog(prefill, binary, agentcatalog.Builtins())
}

func defaultModelChoiceCatalog(prefill, binary string, catalog agentcatalog.Catalog) string {
	prefill = strings.TrimSpace(prefill)
	if prefill == "" {
		return effortAutomatic
	}
	for _, item := range modelChoicesCatalog(binary, catalog) {
		if strings.EqualFold(item.value, prefill) {
			return item.value
		}
	}
	return modelCustomChoice
}

func effortChoicesCatalog(binary string, catalog agentcatalog.Catalog) []choice {
	choices := []choice{{value: effortAutomatic, label: "automatic: let the binary choose"}}
	for _, entry := range catalog.Entries(binary, agentcatalog.Efforts) {
		choices = append(choices, choice{value: entry.Value, label: entry.Label})
	}
	return append(choices, choice{value: effortCustomChoice, label: "custom: type an effort tier"})
}

func existingOverrideEffortChoices(member MemberSummary, catalog agentcatalog.Catalog) []choice {
	choices := []choice{{value: effortKeepChoice, label: "keep profile effort: " + defaultString(member.Effort, effortAutomatic)}}
	for _, entry := range catalog.Entries(member.Binary, agentcatalog.Efforts) {
		choices = append(choices, choice{value: entry.Value, label: entry.Label})
	}
	return append(choices, choice{value: effortCustomChoice, label: "custom: type an effort tier"})
}

func defaultEffortChoiceCatalog(prefill, binary string, catalog agentcatalog.Catalog, fallback string) string {
	prefill = strings.TrimSpace(prefill)
	if prefill == "" {
		return fallback
	}
	if strings.EqualFold(prefill, effortAutomatic) {
		return effortAutomatic
	}
	for _, entry := range catalog.Entries(binary, agentcatalog.Efforts) {
		if strings.EqualFold(entry.Value, prefill) {
			return entry.Value
		}
	}
	return effortCustomChoice
}

func effortCatalogWarnings(s Spec, ctx ProjectContext) []string {
	type target struct {
		role, binary, effort string
	}
	var targets []target
	if strings.EqualFold(s.Scope, "global") {
		targets = append(targets, target{role: "global", binary: s.GlobalAgent, effort: s.GlobalEffort})
	} else if s.Backend == BackendResume {
		efforts := parseAssignments(s.Effort)
		for _, member := range s.ResumeMembers {
			if member.Action == MemberActionFresh {
				targets = append(targets, target{role: member.Role, binary: member.Binary, effort: efforts[member.Role]})
			}
		}
	} else if s.ProfileBranch == ProfileBranchExisting {
		efforts := parseAssignments(s.Effort)
		if index := findProfile(ctx.Profiles, s.Profile); index >= 0 {
			for _, member := range ctx.Profiles[index].Members {
				targets = append(targets, target{role: member.Role, binary: member.Binary, effort: efforts[member.Role]})
			}
		}
	} else {
		binaries := parseAssignments(s.Binary)
		efforts := parseAssignments(s.Effort)
		for _, role := range splitAssignmentsList(s.Roles) {
			targets = append(targets, target{role: role, binary: binaries[role], effort: efforts[role]})
		}
	}

	seen := map[string]bool{}
	var warnings []string
	for _, item := range targets {
		binary := strings.ToLower(strings.TrimSpace(item.binary))
		effort := strings.TrimSpace(item.effort)
		if effort == "" || strings.EqualFold(effort, effortAutomatic) || (binary != "codex" && binary != "claude") {
			continue
		}
		if _, ok := ctx.Catalog.Resolve(binary, agentcatalog.Efforts, effort); ok {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(item.role)) + "\x00" + strings.ToLower(effort)
		if seen[key] {
			continue
		}
		seen[key] = true
		warnings = append(warnings, fmt.Sprintf("Warning: effort %s=%s is not in the merged catalog for %s; it will be passed through exactly and may be rejected by the underlying binary.", item.role, effort, binary))
	}
	return warnings
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
		trailer := "roster and contract stay authoritative"
		if isTemplateProfile(*profile) {
			trailer = "roster and contract stay authoritative; pick any workstream session"
		}
		choices = append(choices, choice{
			value: profile.Name,
			label: fmt.Sprintf("%s · %d members · %s · %s", profile.Name, profile.MemberCount, profileRunSummary(*profile, ctx.SessionSuggestion), trailer),
		})
		if defaultProfile == "" && profile.Name == "default" {
			defaultProfile = profile.Name
		}
	}
	if defaultProfile == "" {
		defaultProfile = ctx.Profiles[0].Name
	} else if byName[defaultProfile] == nil {
		defaultProfile = "__create__"
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

// promptExistingSession resolves the session for a previously picked existing
// profile. An unpinned template profile (#451) accepts any typed workstream
// directly, since it has no pin to conflict with. A pinned profile still only
// derives from its own known sessions, but the choice list always offers a
// clone escape hatch (#523) instead of silently forcing one of those sessions
// or erroring closed — the returned bool reports that the caller chose to
// clone into a new profile rather than reuse this one's session.
func promptExistingSession(r *bufio.Reader, out io.Writer, profile ProfileSummary, suggestion string) (SessionSummary, bool, error) {
	if isTemplateProfile(profile) {
		fmt.Fprintf(out, "%q is an unpinned template profile: it can launch for any new workstream.\n", profile.Name)
		name, err := promptText(r, out, "Workstream session for this launch", defaultString(suggestion, "issue-1"))
		if err != nil {
			return SessionSummary{}, false, err
		}
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			return SessionSummary{}, false, fmt.Errorf("session cannot be empty")
		}
		return SessionSummary{
			Name:           name,
			Source:         SessionSourceSuggestedFirst,
			Classification: RunClassification{State: RunStateNotStarted, Backend: BackendRunStart, Executable: true},
		}, false, nil
	}
	sessions := profileSessions(profile, suggestion)
	if len(sessions) == 0 {
		return SessionSummary{}, false, fmt.Errorf("profile %q has no derivable session", profile.Name)
	}
	// The common case (no conflicting desired session, or exactly one known
	// session that already matches it) stays frictionless: no prompt at all.
	// The clone escape hatch (#523) only needs to surface when there is a
	// real, detectable mismatch to resolve — a bare suggestion (usually
	// derived from the current git branch) that differs from every known
	// session, or genuinely more than one known session to pick between.
	if len(sessions) == 1 && (strings.TrimSpace(suggestion) == "" || suggestion == sessions[0].Name) {
		fmt.Fprintf(out, "Known run: %s\n", sessions[0].Label())
		return sessions[0], false, nil
	}
	choices := make([]choice, 0, len(sessions)+1)
	byName := make(map[string]SessionSummary, len(sessions))
	for _, session := range sessions {
		choices = append(choices, choice{value: session.Name, label: session.Label()})
		byName[session.Name] = session
	}
	choices = append(choices, choice{value: cloneRosterChoiceValue, label: "Clone this roster into a new profile for a different session"})
	selected, err := promptChoice(r, out, "Which existing run do you want?", choices, sessions[0].Name)
	if err != nil {
		return SessionSummary{}, false, err
	}
	if selected == cloneRosterChoiceValue {
		return SessionSummary{}, true, nil
	}
	return byName[selected], false, nil
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
	if strings.EqualFold(line, "q") || strings.EqualFold(line, "quit") || strings.EqualFold(line, "cancel") {
		return "", ErrCancelled
	}
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
