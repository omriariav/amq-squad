package wizard

import (
	"fmt"
	"strconv"
	"strings"
)

const maxSavedNativeArgsDisplay = 240

const maxGoalExcerptRunes = 160

type ProfileBranch string

const (
	ProfileBranchExisting ProfileBranch = "existing"
	ProfileBranchNew      ProfileBranch = "new"
)

type SessionSource string

const (
	SessionSourceMemberPin      SessionSource = "member_pin"
	SessionSourceProfile        SessionSource = "profile_workstream"
	SessionSourceLaunchHistory  SessionSource = "launch_history"
	SessionSourceSuggestedFirst SessionSource = "suggested_first"
)

// SessionSummary is the read-only result consumed by both wizard adapters.
// Slice 2 selects and records it; slice 3 will compose resume controls and run
// the mandatory fingerprint freshness gates.
type SessionSummary struct {
	Name           string
	Source         SessionSource
	Classification RunClassification
	Fingerprint    string
	RecordCount    int
	BriefPath      string
	BriefGoal      string
	BriefSeed      string
	Members        []SessionMemberSummary
	Live           int
	Restore        int
	Fresh          int
	Blocked        int
}

// SessionMemberSummary is the action-scoped member plan retained by the
// answer model. Resume controls are derived from Action: live and restore are
// read-only, while launch-fresh may carry a model-only override.
type SessionMemberSummary struct {
	Role                string
	Binary              string
	Model               string
	Effort              string
	Action              MemberAction
	SavedLaunchIdentity string
	SavedBinary         string
	SavedModel          string
	SavedEffort         string
	SavedNativeArgs     []string
	SavedTarget         string
}

func (s SessionSummary) Label() string {
	state := strings.ReplaceAll(string(s.Classification.State), "_", " ")
	if state == "" {
		state = "blocked"
	}
	counts := fmt.Sprintf("live=%d restore=%d fresh=%d blocked=%d", s.Live, s.Restore, s.Fresh, s.Blocked)
	return fmt.Sprintf("%s · %s · %s · %s", s.Name, s.Source, state, counts)
}

func profileSessions(profile ProfileSummary, _ string) []SessionSummary {
	return append([]SessionSummary(nil), profile.Sessions...)
}

func profileRunSummary(profile ProfileSummary, suggestion string) string {
	sessions := profileSessions(profile, suggestion)
	if len(sessions) == 0 {
		return "no authoritative run facts"
	}
	if len(sessions) == 1 {
		return sessions[0].Name + "/" + strings.ReplaceAll(string(sessions[0].Classification.State), "_", " ")
	}
	return fmt.Sprintf("%d known sessions", len(sessions))
}

// SelectProject invalidates every project-derived and downstream answer. Scope
// and global answers deliberately remain outside this project-only operation.
func (s *Spec) SelectProject(project string) {
	if strings.TrimSpace(s.Project) == strings.TrimSpace(project) {
		return
	}
	s.Project = strings.TrimSpace(project)
	s.clearProfileAndRun()
}

// SelectExistingProfile switches to authoritative profile facts and drops all
// answers that only make sense for a fresh profile or a previously selected
// session.
func (s *Spec) SelectExistingProfile(profile string) {
	changed := s.ProfileBranch != ProfileBranchExisting || s.Profile != strings.TrimSpace(profile)
	s.ProfileBranch = ProfileBranchExisting
	s.Profile = strings.TrimSpace(profile)
	if changed {
		s.clearFreshProfileAnswers()
		s.clearSelectedRun()
	}
}

// SelectNewProfile switches away from authoritative discovery and resume facts.
func (s *Spec) SelectNewProfile(profile string) {
	previousBranch := s.ProfileBranch
	changed := s.ProfileBranch != ProfileBranchNew || s.Profile != strings.TrimSpace(profile)
	s.ProfileBranch = ProfileBranchNew
	s.Profile = strings.TrimSpace(profile)
	if changed {
		s.clearAuthoritativeRunAnswers()
		if previousBranch != "" {
			s.clearDownstreamRunAnswers()
		}
	}
}

// SelectExistingSession derives backend/state from authoritative discovery.
// It never accepts an arbitrary name: callers pass a SessionSummary selected
// from the chosen profile's read-only session set.
func (s *Spec) SelectExistingSession(session SessionSummary) {
	changed := s.Session != session.Name || s.SessionSource != session.Source || s.DiscoveryFingerprint != session.Fingerprint
	explicitVisibility, explicitLayout := s.Visibility, s.LayoutPreset
	s.Session = session.Name
	s.SessionSource = session.Source
	s.RunState = session.Classification.State
	s.Backend = session.Classification.Backend
	s.RunExecutable = session.Classification.Executable
	s.RecordCount = session.RecordCount
	s.RestoreExisting = session.RecordCount > 0
	s.DiscoveryFingerprint = session.Fingerprint
	s.ResumeMembers = cloneSessionMembers(session.Members)
	s.BriefPath = session.BriefPath
	s.BriefGoal = session.BriefGoal
	s.BriefSeed = session.BriefSeed
	if changed {
		s.clearDownstreamRunAnswers()
		// restore the just-selected authoritative facts cleared above
		s.Session = session.Name
		s.SessionSource = session.Source
		s.RunState = session.Classification.State
		s.Backend = session.Classification.Backend
		s.RunExecutable = session.Classification.Executable
		s.RecordCount = session.RecordCount
		s.RestoreExisting = session.RecordCount > 0
		s.DiscoveryFingerprint = session.Fingerprint
		s.ResumeMembers = cloneSessionMembers(session.Members)
		s.BriefPath = session.BriefPath
		s.BriefGoal = session.BriefGoal
		s.BriefSeed = session.BriefSeed
		if s.Backend == BackendResume {
			if s.VisibilityExplicit {
				s.Visibility = explicitVisibility
			} else {
				s.Visibility = ResumeDefaultVisibility(session.Members)
			}
			if s.LayoutExplicit {
				s.LayoutPreset = explicitLayout
			} else {
				s.LayoutPreset = defaultLayoutPreset("", s.Visibility)
			}
		}
	}
	if s.Backend == BackendResume && strings.TrimSpace(s.Visibility) == "" {
		s.Visibility = ResumeDefaultVisibility(session.Members)
		s.LayoutPreset = defaultLayoutPreset("", s.Visibility)
	}
}

func (s *Spec) SelectNewSession(session string) {
	hadSession := strings.TrimSpace(s.Session) != ""
	if hadSession && s.Session != strings.TrimSpace(session) {
		s.clearDownstreamRunAnswers()
	}
	s.Session = strings.TrimSpace(session)
	s.SessionSource = ""
	s.RunState = RunStateNotStarted
	s.Backend = BackendRunStart
	s.RunExecutable = true
	s.RestoreExisting = false
	s.RecordCount = 0
	s.DiscoveryFingerprint = ""
	s.ResumeMembers = nil
	s.BriefPath = ""
	s.BriefGoal = ""
	s.BriefSeed = ""
}

func (s *Spec) clearProfileAndRun() {
	s.Profile = ""
	s.ProfileBranch = ""
	s.clearFreshProfileAnswers()
	s.clearAuthoritativeRunAnswers()
	s.clearDownstreamRunAnswers()
}

// ResumeDefaultVisibility uses the persisted target only when every matching
// launch record carries the same supported target. Zero records, missing
// targets, mixed targets, and unknown targets fall back to one window per
// agent.
func ResumeDefaultVisibility(members []SessionMemberSummary) string {
	target := ""
	records := 0
	for _, member := range members {
		if member.Action != MemberActionRestore && member.Action != MemberActionFresh {
			continue
		}
		if strings.TrimSpace(member.SavedLaunchIdentity) == "" {
			continue
		}
		records++
		current := strings.TrimSpace(member.SavedTarget)
		if current == "" || target != "" && current != target {
			return "sibling-tabs"
		}
		target = current
	}
	if records == 0 {
		return "sibling-tabs"
	}
	switch target {
	case "current-window":
		return "current"
	case "new-session":
		return "detached"
	case "new-window":
		return "sibling-tabs"
	default:
		return "sibling-tabs"
	}
}

// GoalExcerpt returns at most two lines and 160 runes including the ellipsis.
func GoalExcerpt(goal string) string {
	goal = strings.ReplaceAll(goal, "\r\n", "\n")
	goal = strings.ReplaceAll(goal, "\r", "\n")
	lines := strings.Split(goal, "\n")
	truncated := len(lines) > 2
	if len(lines) > 2 {
		lines = lines[:2]
	}
	value := strings.TrimSpace(strings.Join(lines, "\n"))
	if value == "" {
		return "not provided"
	}
	runes := []rune(value)
	if len(runes) > maxGoalExcerptRunes {
		truncated = true
	}
	if truncated {
		limit := maxGoalExcerptRunes - len([]rune("..."))
		if len(runes) > limit {
			runes = runes[:limit]
		}
		return strings.TrimSpace(string(runes)) + "..."
	}
	return value
}

func (s *Spec) clearSelectedRun() {
	s.clearAuthoritativeRunAnswers()
	s.clearDownstreamRunAnswers()
}

func (s *Spec) clearAuthoritativeRunAnswers() {
	s.Session = ""
	s.SessionSource = ""
	s.RunState = ""
	s.Backend = ""
	s.RunExecutable = false
	s.RestoreExisting = false
	s.RecordCount = 0
	s.DiscoveryFingerprint = ""
	s.ResumeMembers = nil
	s.BriefPath = ""
	s.BriefGoal = ""
	s.BriefSeed = ""
}

func (s *Spec) clearFreshProfileAnswers() {
	s.Roles = ""
	s.Binary = ""
	s.Lead = ""
	s.LeadMode = ""
	s.OperatorMode = ""
	s.SelfOperatorLead = ""
	s.SelfOperatorAllow = ""
	s.OperatorNotifications = false
}

func (s *Spec) clearDownstreamRunAnswers() {
	s.Model = ""
	s.Effort = ""
	s.CodexArgs = ""
	s.ClaudeArgs = ""
	if !s.VisibilityExplicit {
		s.Visibility = ""
	}
	if !s.LayoutExplicit {
		s.LayoutPreset = ""
	}
	s.LauncherPane = ""
	s.ExternalLead = false
	s.Goal = ""
	s.SeedFrom = ""
}

// InvalidateExistingRun returns the answer model to Profile & run after an
// authoritative fingerprint delta. Project/profile selection remains the
// default; the reviewed run, action controls, and every downstream answer are
// discarded so a prior Yes cannot be replayed.
func (s *Spec) InvalidateExistingRun() {
	s.clearSelectedRun()
	s.OperatorMode = ""
	s.SelfOperatorLead = ""
	s.SelfOperatorAllow = ""
	s.OperatorNotifications = false
}

func (s Spec) Clone() Spec {
	s.ResumeMembers = cloneSessionMembers(s.ResumeMembers)
	return s
}

func cloneSessionMembers(members []SessionMemberSummary) []SessionMemberSummary {
	out := append([]SessionMemberSummary(nil), members...)
	for i := range out {
		out[i].SavedNativeArgs = append([]string(nil), members[i].SavedNativeArgs...)
	}
	return out
}

// FormatSavedNativeArgs renders only the structured extra-option subset kept
// by discovery. It is deliberately bounded and quoted so terminal controls,
// newlines, and oversized values cannot become wizard UI content.
func FormatSavedNativeArgs(args []string) string {
	if len(args) == 0 {
		return "none"
	}
	parts := make([]string, 0, minInt(len(args), 8))
	used := 0
	for i, arg := range args {
		if i == 8 {
			break
		}
		runes := []rune(arg)
		truncated := len(runes) > 32
		if truncated {
			runes = runes[:32]
		}
		available := maxSavedNativeArgsDisplay - used
		if len(parts) > 0 {
			available--
		}
		for {
			value := string(runes)
			if truncated {
				value += "…"
			}
			quoted := strconv.QuoteToASCII(value)
			if len(quoted) <= available {
				parts = append(parts, quoted)
				used += len(quoted)
				if len(parts) > 1 {
					used++
				}
				break
			}
			if len(runes) == 0 {
				return strings.Join(append(parts, "…"), " ")
			}
			runes = runes[:len(runes)-1]
			truncated = true
		}
	}
	if len(args) > len(parts) {
		candidate := strings.Join(append(append([]string(nil), parts...), "…"), " ")
		if len(candidate) <= maxSavedNativeArgsDisplay {
			return candidate
		}
	}
	return strings.Join(parts, " ")
}
