package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/agentcatalog"
	"github.com/omriariav/amq-squad/v2/internal/catalog"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

const (
	runStartPreflightInvalidProject                = "invalid_project"
	runStartPreflightInvalidSession                = "invalid_session"
	runStartPreflightInvalidVisibility             = "invalid_visibility"
	runStartPreflightInvalidProfile                = "invalid_profile"
	runStartPreflightNamespaceCollision            = "profile_session_root_collision"
	runStartPreflightDefaultProfileShadow          = "default_profile_shadowed"
	runStartPreflightExistingProfileSession        = "existing_profile_session_mismatch"
	runStartPreflightMissingRoster                 = "missing_roster"
	runStartPreflightInvalidLeadMode               = "invalid_lead_mode"
	runStartPreflightExistingProfileLeadMode       = "existing_profile_lead_mode"
	runStartPreflightInvalidEffort                 = "invalid_effort"
	runStartPreflightInvalidOperatorMode           = "invalid_operator_mode"
	runStartPreflightExistingOperatorMode          = "existing_profile_operator_mode"
	runStartPreflightExistingOperatorNotifications = "existing_profile_operator_notifications"
	runStartPreflightInvalidLayout                 = "invalid_layout"
	runStartPreflightConflictingRosterSource       = "conflicting_roster_source"
	runStartPreflightFromProfileNotFound           = "from_profile_not_found"
)

type runStartPreflightInput struct {
	Project                  string
	Profile                  string
	ProfileExplicit          bool
	Session                  string
	Roles                    string
	FromProfile              string
	FromProfileSet           bool
	Binary                   string
	Visibility               string
	LeadMode                 string
	Lead                     string
	LeadModeSet              bool
	Effort                   string
	EffortSet                bool
	OperatorMode             string
	OperatorModeSet          bool
	SelfOperatorLead         string
	SelfOperatorAllow        string
	SelfOperatorPolicySet    bool
	OperatorNotifications    bool
	OperatorNotificationsSet bool
	LayoutPreset             string
	LayoutPresetSet          bool
	LauncherPane             string
	LauncherPaneSet          bool
	VisibilitySet            bool
	ExternalLead             bool
}

// runStartPreflightIssue is intentionally structured so wizard adapters can
// render a code, detail, and safe edits without parsing CLI error strings.
type runStartPreflightIssue struct {
	Code           string   `json:"code"`
	Detail         string   `json:"detail"`
	SuggestedFixes []string `json:"suggested_fixes,omitempty"`
}

type runStartPreflightResult struct {
	Project      string `json:"project"`
	Profile      string `json:"profile"`
	Session      string `json:"session"`
	Visibility   string `json:"visibility"`
	LeadMode     string `json:"lead_mode"`
	LayoutPreset string `json:"layout_preset,omitempty"`
	LauncherPane string `json:"launcher_pane,omitempty"`
	TeamPresent  bool   `json:"team_present"`
	FreshRoster  bool   `json:"fresh_roster"`
	// FromProfile is set only when this run creates profile as a clone of an
	// existing roster (#523): FreshRoster is true, Roles was empty, and
	// --from-profile named a readable source profile.
	FromProfile string                   `json:"from_profile,omitempty"`
	Issues      []runStartPreflightIssue `json:"issues,omitempty"`
}

func (r runStartPreflightResult) Err() error {
	if len(r.Issues) == 0 {
		return nil
	}
	issue := r.Issues[0]
	var b strings.Builder
	b.WriteString(issue.Detail)
	if len(issue.SuggestedFixes) > 0 && !strings.Contains(issue.Detail, "Fixes:\n") {
		b.WriteString("\nFixes:\n")
		for _, fix := range issue.SuggestedFixes {
			fmt.Fprintf(&b, "  - %s\n", fix)
		}
	}
	return usageErrorf("%s", strings.TrimRight(b.String(), "\n"))
}

func runStartPreflight(input runStartPreflightInput) runStartPreflightResult {
	r := runStartPreflightResult{
		Project: strings.TrimSpace(input.Project),
		Session: strings.TrimSpace(input.Session),
	}
	add := func(code, detail string, fixes ...string) runStartPreflightResult {
		r.Issues = append(r.Issues, runStartPreflightIssue{Code: code, Detail: detail, SuggestedFixes: fixes})
		return r
	}
	if r.Project == "" {
		return add(runStartPreflightInvalidProject, "run start requires --project (-p)", "choose an existing project directory")
	}
	if r.Session == "" {
		return add(runStartPreflightInvalidSession, "run start requires --session (-s)", "choose a valid workstream session")
	}
	if err := team.ValidateSessionName(r.Session); err != nil {
		return add(runStartPreflightInvalidSession, fmt.Sprintf("invalid --session: %v", err), "use lowercase a-z, digits, hyphen, or underscore")
	}
	if info, err := os.Stat(r.Project); err != nil || !info.IsDir() {
		return add(runStartPreflightInvalidProject, fmt.Sprintf("project directory does not exist: %s", r.Project), "choose an existing project directory")
	}
	layout, err := resolveRunStartLayout(runStartLayoutInput{
		Visibility: input.Visibility, VisibilitySet: input.VisibilitySet,
		Preset: input.LayoutPreset, PresetSet: input.LayoutPresetSet,
		LauncherPane: input.LauncherPane, LauncherPaneSet: input.LauncherPaneSet,
		ExternalLead: input.ExternalLead,
	})
	if err != nil {
		return add(runStartPreflightInvalidLayout, err.Error(), "choose a compatible topology, layout preset, and launcher-pane policy")
	}
	visibility := layout.Visibility
	if visibility == visibilityPlan {
		return add(runStartPreflightInvalidVisibility, "--visibility plan is not valid for run start; it previews by default and creates with --go", "omit --visibility for sibling-tabs or choose detached/current")
	}
	r.Visibility = visibility
	r.LayoutPreset = layout.Preset
	r.LauncherPane = layout.LauncherPane
	profile, err := resolveProfileFlag(input.Profile)
	if err != nil {
		return add(runStartPreflightInvalidProfile, err.Error(), "choose default or a valid named profile")
	}
	r.Profile = profile
	if collision := namespaceCreationCollision(r.Project, profile, r.Session); collision != nil {
		return add(runStartPreflightNamespaceCollision, "run start refused: "+collision.Detail,
			fmt.Sprintf("choose a non-overlapping profile such as %s", suggestedCollisionProfile(profile, r.Session)),
			"choose a different session")
	}
	if shadow, shadowErr := defaultProfileShadowConflict(r.Project, profile, r.Session, input.ProfileExplicit); shadowErr != nil {
		return add(runStartPreflightDefaultProfileShadow, fmt.Sprintf("inspect default-profile ownership: %v", shadowErr))
	} else if shadow != nil {
		return add(runStartPreflightDefaultProfileShadow, "run start refused: "+shadow.Detail,
			"select the named profile that owns this session",
			"pass --profile default only when the legacy/default namespace is intentional")
	}
	r.TeamPresent = teamExistsForProfile(r.Project, input.Profile)
	rolesText := strings.TrimSpace(input.Roles)
	fromProfileText := strings.TrimSpace(input.FromProfile)
	if rolesText != "" && fromProfileText != "" {
		return add(runStartPreflightConflictingRosterSource,
			"--roles and --from-profile both name a roster source; run start creates a fresh roster from exactly one",
			"drop --roles to clone the existing roster from --from-profile",
			"drop --from-profile to declare a fresh roster with --roles")
	}
	r.FreshRoster = (rolesText != "" || fromProfileText != "") && !r.TeamPresent
	if issue := runStartExistingProfileSessionIssue(r.Project, input.Profile, r.Session, input.Roles, input.FromProfile, r.TeamPresent, r.FreshRoster); issue != nil {
		r.Issues = append(r.Issues, *issue)
		return r
	}
	if fromProfileText != "" && !r.TeamPresent {
		targetProfile := profileOrDefault(input.Profile)
		if fromProfileText == targetProfile {
			return add(runStartPreflightInvalidProfile,
				fmt.Sprintf("--from-profile %q must name a different profile than --profile %q; a clone always targets a new profile name", fromProfileText, targetProfile),
				"choose a new --profile name for the cloned roster")
		}
		if !team.ExistsProfile(r.Project, fromProfileText) {
			return add(runStartPreflightFromProfileNotFound,
				fmt.Sprintf("--from-profile %q not found in %s; cannot clone a roster that does not exist", fromProfileText, r.Project),
				"choose an existing profile to clone from",
				"omit --from-profile and pass --roles to declare a fresh roster instead")
		}
		r.FromProfile = fromProfileText
	}
	leadMode, leadErr := normalizeLeadMode(input.LeadMode)
	if leadErr != nil {
		return add(runStartPreflightInvalidLeadMode, leadErr.Error(), "choose builder or planner")
	}
	r.LeadMode = leadMode
	if !r.TeamPresent && rolesText == "" && fromProfileText == "" {
		return add(runStartPreflightMissingRoster,
			fmt.Sprintf("no team profile %q in %s and no --roles or --from-profile given; pass --roles for a fresh roster or --from-profile to clone an existing one, or create the team first", profileOrDefault(input.Profile), r.Project),
			"enter roles for a fresh roster",
			"clone an existing profile's roster with --from-profile",
			"choose an existing profile")
	}
	if input.LeadModeSet && !r.FreshRoster {
		return add(runStartPreflightExistingProfileLeadMode,
			fmt.Sprintf("--lead-mode applies only when run start creates a new roster; for an existing profile use `amq-squad team lead set <role> --lead-mode %s` first", leadMode),
			"keep the existing profile lead mode",
			fmt.Sprintf("run amq-squad team lead set <role> --lead-mode %s before starting", leadMode))
	}
	if input.OperatorModeSet {
		mode, modeErr := validateCanonicalOperatorMode(input.OperatorMode)
		if modeErr != nil {
			return add(runStartPreflightInvalidOperatorMode, "invalid --operator-mode: "+modeErr.Error(), "choose lead_pane, separate_terminal, noc, or self_operator")
		}
		if mode == team.OperatorInteractionSelfOperator {
			if r.TeamPresent && !r.FreshRoster {
				if input.SelfOperatorPolicySet {
					return add(runStartPreflightExistingOperatorMode, "run start never rewrites an existing self-operator policy", "omit self-operator policy flags and use the authoritative profile", "use amq-squad team operator set to change policy")
				}
				existing, readErr := team.ReadProfile(r.Project, r.Profile)
				if readErr != nil {
					return add(runStartPreflightExistingOperatorMode, readErr.Error())
				}
				view := team.EffectiveSelfOperator(existing, r.Session)
				if view.LeadRole == "" || len(view.AllowedGateKinds) == 0 {
					return add(runStartPreflightExistingOperatorMode, "existing self_operator profile has no exact-session policy; run start fails closed", "use amq-squad team operator set for this exact session")
				}
			} else {
				allow := splitCSV(input.SelfOperatorAllow)
				if strings.TrimSpace(input.SelfOperatorLead) == "" || len(allow) != 1 || allow[0] != "merge" {
					return add(runStartPreflightInvalidOperatorMode, "self_operator requires an exact-session lead and explicit merge-only allowlist; spawn remains human-only", "pass --self-operator-lead <lead> --self-operator-allow merge")
				}
				wantLead := strings.TrimSpace(input.Lead)
				if wantLead == "" {
					wantLead = "cto"
				}
				if strings.TrimSpace(input.SelfOperatorLead) != wantLead {
					return add(runStartPreflightInvalidOperatorMode, "self-operator lead must equal the configured fresh-roster lead")
				}
			}
		}
		if r.TeamPresent && !r.FreshRoster {
			existing, readErr := team.ReadProfile(r.Project, r.Profile)
			if readErr != nil {
				return add(runStartPreflightExistingOperatorMode, fmt.Sprintf("read team profile %q: %v", r.Profile, readErr))
			}
			persisted := team.EffectiveOperator(existing).InteractionMode
			if mode != persisted {
				return add(runStartPreflightExistingOperatorMode,
					fmt.Sprintf("--operator-mode %s does not match existing profile %q interaction mode %s; run start never rewrites an existing operator contract", mode, r.Profile, persisted),
					"omit --operator-mode to use the existing profile contract",
					"create a new named profile with the requested operator mode")
			}
		}
	}
	if input.SelfOperatorPolicySet && !input.OperatorModeSet {
		return add(runStartPreflightInvalidOperatorMode, "self-operator policy flags require --operator-mode self_operator")
	}
	if input.SelfOperatorPolicySet && input.OperatorModeSet && strings.TrimSpace(input.OperatorMode) != team.OperatorInteractionSelfOperator {
		return add(runStartPreflightInvalidOperatorMode, "self-operator policy flags require --operator-mode self_operator")
	}
	if r.TeamPresent && !r.FreshRoster {
		existing, readErr := team.ReadProfile(r.Project, r.Profile)
		if readErr != nil {
			return add(runStartPreflightExistingOperatorMode, readErr.Error())
		}
		if team.EffectiveOperator(existing).InteractionMode == team.OperatorInteractionSelfOperator {
			view := team.EffectiveSelfOperator(existing, r.Session)
			if view.LeadRole == "" || len(view.AllowedGateKinds) == 0 {
				return add(runStartPreflightExistingOperatorMode, "existing self_operator profile has no exact-session policy; run start fails closed", "use amq-squad team operator set for this exact session")
			}
		}
	}
	if input.OperatorNotificationsSet && r.TeamPresent && !r.FreshRoster {
		existing, readErr := team.ReadProfile(r.Project, r.Profile)
		if readErr != nil {
			return add(runStartPreflightExistingOperatorNotifications, fmt.Sprintf("read team profile %q: %v", r.Profile, readErr))
		}
		if input.OperatorNotifications != team.EffectiveOperatorNotifications(existing.Operator).Enabled {
			return add(runStartPreflightExistingOperatorNotifications, fmt.Sprintf("--operator-notifications does not match existing profile %q notification policy; run start never rewrites it", r.Profile), "omit --operator-notifications to use the authoritative profile policy")
		}
	}
	if input.EffortSet {
		var effortErr error
		agentCatalog := loadAgentCatalogAndWarn(r.Project)
		if r.TeamPresent && !r.FreshRoster {
			efforts, parseErr := parseEffortOverrides(input.Effort)
			if parseErr != nil {
				effortErr = parseErr
			} else if existing, readErr := team.ReadProfile(r.Project, r.Profile); readErr != nil {
				effortErr = fmt.Errorf("read team profile %q: %w", r.Profile, readErr)
			} else {
				_, effortErr = applyLaunchEffortOverridesCatalogMode(existing.Members, efforts, agentCatalog, false)
			}
		} else {
			effortErr = validateRunStartFreshEffort(input.Roles, input.Binary, input.Effort, agentCatalog)
		}
		if effortErr != nil {
			return add(runStartPreflightInvalidEffort, effortErr.Error(), "use role=automatic|low|medium|high assignments")
		}
	}
	return r
}

func validateRunStartFreshEffort(rolesRaw, binaryRaw, effortRaw string, agentCatalog agentcatalog.Catalog) error {
	efforts, err := parseEffortOverrides(effortRaw)
	if err != nil {
		return err
	}
	binaries, err := parseKV(binaryRaw)
	if err != nil {
		return fmt.Errorf("parse --binary: %w", err)
	}
	binaries = lowercaseKeys(binaries)
	selected := map[string]bool{}
	specialSelection := false
	for _, role := range splitCSV(rolesRaw) {
		role = strings.ToLower(strings.TrimSpace(role))
		if role == "all" {
			specialSelection = true
		}
		if _, numberErr := strconv.Atoi(role); numberErr == nil {
			specialSelection = true
		}
		if role != "" {
			selected[role] = true
		}
	}
	if !specialSelection {
		if err := validateEffortOverrideKeys(efforts, selected); err != nil {
			return err
		}
	}
	for role, effort := range efforts {
		binary := strings.TrimSpace(binaries[role])
		if binary == "" {
			if entry := catalog.Lookup(role); entry != nil {
				binary = entry.PreferredBinary
			}
		}
		if binary == "" {
			continue // team init owns the missing-binary error for custom roles.
		}
		if _, _, err := effortArgsForBinaryCatalog(binary, effort, agentCatalog); err != nil {
			return fmt.Errorf("--effort %s=%s: %w", role, effort, err)
		}
	}
	return nil
}

func runStartExistingProfileSessionIssue(project, profile, session, roles, fromProfile string, teamPresent, freshRoster bool) *runStartPreflightIssue {
	if !teamPresent || freshRoster {
		return nil
	}
	profileName := profileOrDefault(profile)
	t, err := team.ReadProfile(project, profileName)
	if err != nil {
		return &runStartPreflightIssue{
			Code:   runStartPreflightExistingProfileSession,
			Detail: fmt.Sprintf("read team profile %q: %v", profileName, err),
		}
	}
	active, skipped := filterMembersBySession(t.Members, session)
	if len(active) > 0 || len(t.Members) == 0 || len(skipped) == 0 {
		return nil
	}
	pins := pinnedSessionList(skipped)
	pinned := strings.Join(pins, ", ")
	if len(pins) == 1 {
		pinned = pins[0]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "run start refused: profile %q in %s is pinned to workstream %s, not %q; no team members would run for the requested session.\n", profileName, project, pinned, session)
	if strings.TrimSpace(roles) != "" {
		fmt.Fprintf(&b, "--roles %q would be ignored because profile %q already exists; run start uses the existing roster instead of replacing it.\n", roles, profileName)
	}
	if strings.TrimSpace(fromProfile) != "" {
		fmt.Fprintf(&b, "--from-profile %q would be ignored because profile %q already exists; run start uses the existing roster instead of cloning a new one.\n", fromProfile, profileName)
	}
	firstPinned := pins[0]
	return &runStartPreflightIssue{
		Code:   runStartPreflightExistingProfileSession,
		Detail: strings.TrimRight(b.String(), "\n"),
		SuggestedFixes: []string{
			fmt.Sprintf("run the existing roster on its pinned session: amq-squad run start --project %s%s --session %s", shellQuote(project), runStartProfileFixArg(profile), shellQuote(firstPinned)),
			fmt.Sprintf("create a session-pinned roster under a named profile: amq-squad run start --project %s --profile <name> --session %s --roles %s", shellQuote(project), shellQuote(session), runStartRolesFixArg(roles)),
			fmt.Sprintf("clone this roster into a new session-pinned profile: amq-squad run start --project %s --profile <new-name> --from-profile %s --session %s", shellQuote(project), shellQuote(profileName), shellQuote(session)),
		},
	}
}
