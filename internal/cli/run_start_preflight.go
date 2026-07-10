package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/catalog"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

const (
	runStartPreflightInvalidProject          = "invalid_project"
	runStartPreflightInvalidSession          = "invalid_session"
	runStartPreflightInvalidVisibility       = "invalid_visibility"
	runStartPreflightInvalidProfile          = "invalid_profile"
	runStartPreflightNamespaceCollision      = "profile_session_root_collision"
	runStartPreflightDefaultProfileShadow    = "default_profile_shadowed"
	runStartPreflightExistingProfileSession  = "existing_profile_session_mismatch"
	runStartPreflightMissingRoster           = "missing_roster"
	runStartPreflightInvalidLeadMode         = "invalid_lead_mode"
	runStartPreflightExistingProfileLeadMode = "existing_profile_lead_mode"
	runStartPreflightInvalidEffort           = "invalid_effort"
	runStartPreflightInvalidOperatorMode     = "invalid_operator_mode"
	runStartPreflightExistingOperatorMode    = "existing_profile_operator_mode"
)

type runStartPreflightInput struct {
	Project         string
	Profile         string
	ProfileExplicit bool
	Session         string
	Roles           string
	Binary          string
	Visibility      string
	LeadMode        string
	LeadModeSet     bool
	Effort          string
	EffortSet       bool
	OperatorMode    string
	OperatorModeSet bool
}

// runStartPreflightIssue is intentionally structured so wizard adapters can
// render a code, detail, and safe edits without parsing CLI error strings.
type runStartPreflightIssue struct {
	Code           string   `json:"code"`
	Detail         string   `json:"detail"`
	SuggestedFixes []string `json:"suggested_fixes,omitempty"`
}

type runStartPreflightResult struct {
	Project     string                   `json:"project"`
	Profile     string                   `json:"profile"`
	Session     string                   `json:"session"`
	Visibility  string                   `json:"visibility"`
	LeadMode    string                   `json:"lead_mode"`
	TeamPresent bool                     `json:"team_present"`
	FreshRoster bool                     `json:"fresh_roster"`
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
	visibility, err := normalizeLaunchVisibility(input.Visibility)
	if err != nil {
		return add(runStartPreflightInvalidVisibility, err.Error(), "choose sibling-tabs, detached, or current")
	}
	if visibility == visibilityPlan {
		return add(runStartPreflightInvalidVisibility, "--visibility plan is not valid for run start; it previews by default and creates with --go", "omit --visibility for sibling-tabs or choose detached/current")
	}
	r.Visibility = visibility
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
	r.FreshRoster = rolesText != "" && !r.TeamPresent
	if issue := runStartExistingProfileSessionIssue(r.Project, input.Profile, r.Session, input.Roles, r.TeamPresent, r.FreshRoster); issue != nil {
		r.Issues = append(r.Issues, *issue)
		return r
	}
	leadMode, leadErr := normalizeLeadMode(input.LeadMode)
	if leadErr != nil {
		return add(runStartPreflightInvalidLeadMode, leadErr.Error(), "choose builder or planner")
	}
	r.LeadMode = leadMode
	if !r.TeamPresent && rolesText == "" {
		return add(runStartPreflightMissingRoster,
			fmt.Sprintf("no team profile %q in %s and no --roles given; pass --roles to create one or create the team first", profileOrDefault(input.Profile), r.Project),
			"enter roles for a fresh roster",
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
			return add(runStartPreflightInvalidOperatorMode, "invalid --operator-mode: "+modeErr.Error(), "choose lead_pane, separate_terminal, or noc")
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
	if input.EffortSet {
		var effortErr error
		if r.TeamPresent && !r.FreshRoster {
			efforts, parseErr := parseEffortOverrides(input.Effort)
			if parseErr != nil {
				effortErr = parseErr
			} else if existing, readErr := team.ReadProfile(r.Project, r.Profile); readErr != nil {
				effortErr = fmt.Errorf("read team profile %q: %w", r.Profile, readErr)
			} else {
				_, effortErr = applyLaunchEffortOverrides(existing.Members, efforts)
			}
		} else {
			effortErr = validateRunStartFreshEffort(input.Roles, input.Binary, input.Effort)
		}
		if effortErr != nil {
			return add(runStartPreflightInvalidEffort, effortErr.Error(), "use role=automatic|low|medium|high assignments")
		}
	}
	return r
}

func validateRunStartFreshEffort(rolesRaw, binaryRaw, effortRaw string) error {
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
		if _, err := effortArgsForBinary(binary, effort); err != nil {
			return fmt.Errorf("--effort %s=%s: %w", role, effort, err)
		}
	}
	return nil
}

func runStartExistingProfileSessionIssue(project, profile, session, roles string, teamPresent, freshRoster bool) *runStartPreflightIssue {
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
	firstPinned := pins[0]
	return &runStartPreflightIssue{
		Code:   runStartPreflightExistingProfileSession,
		Detail: strings.TrimRight(b.String(), "\n"),
		SuggestedFixes: []string{
			fmt.Sprintf("run the existing roster on its pinned session: amq-squad run start --project %s%s --session %s", shellQuote(project), runStartProfileFixArg(profile), shellQuote(firstPinned)),
			fmt.Sprintf("create a session-pinned roster under a named profile: amq-squad run start --project %s --profile <name> --session %s --roles %s", shellQuote(project), shellQuote(session), runStartRolesFixArg(roles)),
		},
	}
}
