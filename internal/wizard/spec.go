// Package wizard contains the interactive front-end state for run start.
// It deliberately returns canonical flag arguments and never launches agents.
package wizard

import (
	"fmt"
	"strings"
)

// CommandForms returns the exact preview and live command pair represented by
// the answer model. The live form differs only by the backend's explicit
// mutation flag.
func (s Spec) CommandForms() (string, string, error) {
	var prefix, previewArgs, liveArgs []string
	switch s.Backend {
	case BackendResume:
		args, err := s.ResumeArgs()
		if err != nil {
			return "", "", err
		}
		prefix = []string{"resume"}
		previewArgs = args
		liveArgs = append(append([]string(nil), args...), "--exec")
	case BackendGlobalStart:
		prefix = []string{"global", "start"}
		previewArgs = s.GlobalArgs()
		liveArgs = append(append([]string(nil), previewArgs...), "--go")
	case BackendRunStart, "":
		prefix = []string{"run", "start"}
		previewArgs = s.Args()
		liveArgs = append(append([]string(nil), previewArgs...), "--go")
	default:
		return "", "", fmt.Errorf("unsupported wizard backend %q", s.Backend)
	}
	return renderShellCommand(append(prefix, previewArgs...)...), renderShellCommand(append(prefix, liveArgs...)...), nil
}

func renderShellCommand(args ...string) string {
	parts := []string{"amq-squad"}
	for _, arg := range args {
		parts = append(parts, shellQuoteReview(arg))
	}
	return strings.Join(parts, " ")
}

func shellQuoteReview(value string) string {
	if value == "" {
		return "''"
	}
	for _, r := range value {
		if !(r == '/' || r == '.' || r == '-' || r == '_' || r == '=' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
		}
	}
	return value
}

// Backend is the canonical command family selected by the answer model. The
// UI records it explicitly so execution never infers resume-vs-start from a
// profile merely existing at some later point in time.
type Backend string

const (
	BackendRunStart    Backend = "run_start"
	BackendResume      Backend = "resume"
	BackendGlobalStart Backend = "global_start"
)

// Spec is the headless, serializable answer model for a project run. Later UI
// adapters may add richer choices, but execution must always flow through Args
// and the existing run start parser.
type Spec struct {
	Scope                          string
	Backend                        Backend
	Project                        string
	Profile                        string
	ProfileBranch                  ProfileBranch
	Session                        string
	SessionSource                  SessionSource
	RunState                       RunState
	RunExecutable                  bool
	RestoreExisting                bool
	RecordCount                    int
	DiscoveryFingerprint           string
	ResumeMembers                  []SessionMemberSummary
	ResumeGoalPlan                 ResumeGoalPlan
	RedeliverGoal                  bool
	BriefPath                      string
	BriefGoal                      string
	BriefSeed                      string
	Roles                          string
	Binary                         string
	Model                          string
	Effort                         string
	ToolPolicyMode                 string
	ToolProfile                    string
	LaunchShape                    string
	StagedRoles                    string
	AuthoredRoles                  string
	AuthoredBinary                 string
	AuthoredModel                  string
	AuthoredEffort                 string
	AuthoredToolProfile            string
	OperatorMode                   string
	SelfOperatorLead               string
	SelfOperatorAllow              string
	OperatorNotifications          bool
	OperatorNotificationsRequested bool
	OperatorNotificationsSet       bool
	CodexArgs                      string
	ClaudeArgs                     string
	Lead                           string
	LeadMode                       string
	Visibility                     string
	VisibilityExplicit             bool
	LayoutPreset                   string
	LayoutExplicit                 bool
	LauncherPane                   string
	ExternalLead                   bool
	Goal                           string
	SeedFrom                       string
	GoalBindingSource              string
	GoalBindingNamespace           string
	GoalBindingText                string
	GoalBindingDigest              string
	GoalBindingDerived             bool
	GoalBindingVerified            bool
	GlobalRoot                     string
	GlobalAgent                    string
	GlobalModel                    string
	GlobalEffort                   string
	GlobalCodexArgs                string
	GlobalClaudeArgs               string
	GlobalWindow                   string
}

// ResumeArgs renders the canonical plan-only resume argv. The restore guard is
// a direct statement about matching history, and model/effort overrides are
// validated and restricted to launch-fresh members because live and restore
// actions are immutable.
func (s Spec) ResumeArgs() ([]string, error) {
	if s.Backend != BackendResume || !s.RunExecutable || s.ProfileBranch != ProfileBranchExisting {
		return nil, fmt.Errorf("resume arguments require an executable existing-profile resume selection")
	}
	if strings.TrimSpace(s.DiscoveryFingerprint) == "" {
		return nil, fmt.Errorf("resume arguments require a non-empty discovery fingerprint")
	}
	if strings.TrimSpace(s.Project) == "" || strings.TrimSpace(s.Profile) == "" || strings.TrimSpace(s.Session) == "" || len(s.ResumeMembers) == 0 {
		return nil, fmt.Errorf("resume arguments require project, profile, session, and a non-empty member plan")
	}
	if s.RecordCount < 0 || s.RestoreExisting != (s.RecordCount > 0) {
		return nil, fmt.Errorf("resume restore guard is inconsistent with matching record count %d", s.RecordCount)
	}
	if strings.TrimSpace(s.CodexArgs) != "" || strings.TrimSpace(s.ClaudeArgs) != "" || strings.TrimSpace(s.LauncherPane) != "" || strings.TrimSpace(s.Goal) != "" || strings.TrimSpace(s.SeedFrom) != "" {
		return nil, fmt.Errorf("resume answer model contains unsupported native-arg, launcher, goal, or seed controls")
	}
	args := make([]string, 0, 16)
	appendValue := func(name, value string) {
		if value = strings.TrimSpace(value); value != "" {
			args = append(args, name, value)
		}
	}
	appendValue("--project", s.Project)
	appendValue("--profile", s.Profile)
	appendValue("--session", s.Session)
	if s.RecordCount > 0 {
		args = append(args, "--restore-existing")
	}
	models, err := parseResumeAssignments("model", s.Model)
	if err != nil {
		return nil, err
	}
	efforts, err := parseResumeAssignments("effort", s.Effort)
	if err != nil {
		return nil, err
	}
	freshRoles := make([]string, 0, len(s.ResumeMembers))
	allowedModels := make(map[string]string)
	allowedEfforts := make(map[string]string)
	actions := make(map[string]MemberAction, len(s.ResumeMembers))
	runnable := 0
	for _, member := range s.ResumeMembers {
		if _, exists := actions[member.Role]; exists || strings.TrimSpace(member.Role) == "" {
			return nil, fmt.Errorf("resume member plan contains an empty or duplicate role %q", member.Role)
		}
		actions[member.Role] = member.Action
		switch member.Action {
		case MemberActionLive, MemberActionRestore, MemberActionFresh:
			if member.Action != MemberActionLive {
				runnable++
			}
		default:
			return nil, fmt.Errorf("resume member %q has non-executable action %q", member.Role, member.Action)
		}
		if member.Action != MemberActionFresh {
			continue
		}
		freshRoles = append(freshRoles, member.Role)
		if value := strings.TrimSpace(models[member.Role]); value != "" {
			allowedModels[member.Role] = value
		}
		if value := strings.TrimSpace(efforts[member.Role]); value != "" {
			allowedEfforts[member.Role] = value
		}
	}
	if runnable == 0 {
		return nil, fmt.Errorf("resume member plan has no restore or launch-fresh action")
	}
	for role := range models {
		action, exists := actions[role]
		if !exists || action != MemberActionFresh {
			return nil, fmt.Errorf("resume model override for %q is not allowed for action %q", role, action)
		}
	}
	for role := range efforts {
		action, exists := actions[role]
		if !exists || action != MemberActionFresh {
			return nil, fmt.Errorf("resume effort override for %q is not allowed for action %q", role, action)
		}
	}
	appendValue("--model", renderAssignments(freshRoles, allowedModels))
	appendValue("--effort", renderAssignments(freshRoles, allowedEfforts))
	target, layout, err := resumePlacement(s.Visibility, s.LayoutPreset)
	if err != nil {
		return nil, err
	}
	appendValue("--target", target)
	appendValue("--layout", layout)
	if s.RedeliverGoal {
		if !s.ResumeGoalPlan.Eligible {
			reason := strings.TrimSpace(s.ResumeGoalPlan.Reason)
			if reason == "" {
				reason = "eligibility evidence is missing"
			}
			if recovery := strings.TrimSpace(s.ResumeGoalPlan.RecoveryCommand); recovery != "" {
				return nil, fmt.Errorf("resume goal redelivery is unavailable: %s; recover exact attempt %s manually: %s", reason, s.ResumeGoalPlan.RecoveryAttemptID, recovery)
			}
			return nil, fmt.Errorf("resume goal redelivery is unavailable: %s", reason)
		}
		args = append(args, "--redeliver-goal")
	} else if s.ResumeGoalPlan.Eligible {
		// Preserve the wizard's explicit default-No choice when the same args are
		// later executed in a TTY; direct resume without this internal flag keeps
		// its normal default-No prompt.
		args = append(args, "--no-redeliver-goal-prompt")
	}
	return args, nil
}

func resumePlacement(visibility, layout string) (string, string, error) {
	switch strings.TrimSpace(visibility) {
	case "current":
		switch strings.TrimSpace(layout) {
		case "lead-left", "vertical", "":
			return "current-window", "vertical", nil
		case "lead-top", "horizontal":
			return "current-window", "horizontal", nil
		case "even-grid", "tiled":
			return "current-window", "tiled", nil
		default:
			return "", "", fmt.Errorf("unsupported current-window resume layout %q", layout)
		}
	case "detached":
		if layout = strings.TrimSpace(layout); layout != "" && layout != "tiled" {
			return "", "", fmt.Errorf("detached resume placement does not accept layout preset %q", layout)
		}
		return "new-session", "tiled", nil
	case "sibling-tabs", "":
		if layout = strings.TrimSpace(layout); layout != "" && layout != "one-window-per-agent" {
			return "", "", fmt.Errorf("sibling-tabs resume placement requires one-window-per-agent layout, got %q", layout)
		}
		return "new-window", "tiled", nil
	default:
		return "", "", fmt.Errorf("unsupported resume placement %q", visibility)
	}
}

func parseResumeAssignments(kind, raw string) (map[string]string, error) {
	out := map[string]string{}
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		role, value, ok := strings.Cut(item, "=")
		role, value = strings.ToLower(strings.TrimSpace(role)), strings.TrimSpace(value)
		if !ok || role == "" || value == "" {
			return nil, fmt.Errorf("invalid resume %s assignment %q", kind, item)
		}
		if _, exists := out[role]; exists {
			return nil, fmt.Errorf("duplicate resume %s assignment for %q", kind, role)
		}
		out[role] = value
	}
	return out, nil
}

// GlobalArgs renders only global-start flags. Project roster and topology
// fields can never leak into this argv.
func (s Spec) GlobalArgs() []string {
	args := make([]string, 0, 12)
	appendValue := func(name, value string) {
		if value = strings.TrimSpace(value); value != "" {
			args = append(args, name, value)
		}
	}
	appendValue("--root", s.GlobalRoot)
	appendValue("--agent", s.GlobalAgent)
	appendValue("--model", s.GlobalModel)
	effort := strings.TrimSpace(s.GlobalEffort)
	if effort == "automatic" {
		effort = ""
	}
	if strings.EqualFold(strings.TrimSpace(s.GlobalAgent), "codex") {
		native := strings.TrimSpace(s.GlobalCodexArgs)
		if effort != "" {
			native = strings.TrimSpace(native + " -c model_reasoning_effort=" + effort)
		}
		appendValue("--codex-args", native)
	} else {
		native := strings.TrimSpace(s.GlobalClaudeArgs)
		if effort != "" {
			native = strings.TrimSpace(native + " --effort " + effort)
		}
		appendValue("--claude-args", native)
	}
	appendValue("--name", s.GlobalWindow)
	return args
}

// Args renders the canonical run start argv in a stable order. It never emits
// --interactive or --go, which keeps this package preview-only by construction.
func (s Spec) Args() []string {
	args := make([]string, 0, 28)
	appendValue := func(name, value string) {
		if value = strings.TrimSpace(value); value != "" {
			args = append(args, name, value)
		}
	}
	appendValue("--project", s.Project)
	appendValue("--profile", s.Profile)
	appendValue("--session", s.Session)
	appendValue("--roles", s.Roles)
	appendValue("--binary", s.Binary)
	appendValue("--model", s.Model)
	appendValue("--effort", s.Effort)
	appendValue("--tool-profile", s.ToolProfile)
	appendValue("--launch-shape", s.LaunchShape)
	appendValue("--staged-roles", s.StagedRoles)
	if strings.TrimSpace(s.OperatorMode) != "unspecified" {
		appendValue("--operator-mode", s.OperatorMode)
	}
	appendValue("--self-operator-lead", s.SelfOperatorLead)
	appendValue("--self-operator-allow", s.SelfOperatorAllow)
	if s.OperatorNotifications {
		args = append(args, "--operator-notifications")
	}
	appendValue("--codex-args", s.CodexArgs)
	appendValue("--claude-args", s.ClaudeArgs)
	appendValue("--lead", s.Lead)
	appendValue("--lead-mode", s.LeadMode)
	appendValue("--visibility", s.Visibility)
	appendValue("--layout-preset", s.LayoutPreset)
	appendValue("--launcher-pane", s.LauncherPane)
	if s.ExternalLead {
		args = append(args, "--external-lead")
	}
	appendValue("--goal", s.Goal)
	if s.GoalBindingVerified {
		appendValue("--goal-source", s.GoalBindingSource)
		appendValue("--goal-digest", s.GoalBindingDigest)
	}
	appendValue("--seed-from", s.SeedFrom)
	return args
}
