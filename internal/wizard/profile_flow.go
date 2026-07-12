package wizard

import (
	"fmt"
	"strings"
)

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
	Live           int
	Restore        int
	Fresh          int
	Blocked        int
}

func (s SessionSummary) Label() string {
	state := strings.ReplaceAll(string(s.Classification.State), "_", " ")
	if state == "" {
		state = "blocked"
	}
	counts := fmt.Sprintf("live=%d restore=%d fresh=%d blocked=%d", s.Live, s.Restore, s.Fresh, s.Blocked)
	return fmt.Sprintf("%s · %s · %s · %s", s.Name, s.Source, state, counts)
}

func profileSessions(profile ProfileSummary, suggestion string) []SessionSummary {
	if len(profile.Sessions) > 0 {
		return append([]SessionSummary(nil), profile.Sessions...)
	}
	if pinned := strings.TrimSpace(profile.PinnedSession); pinned != "" {
		return []SessionSummary{{
			Name:           pinned,
			Source:         SessionSourceMemberPin,
			Classification: ClassifyExistingRun(profile.MemberCount, 0, freshActions(profile.MemberCount), false),
			Fresh:          profile.MemberCount,
		}}
	}
	if strings.TrimSpace(suggestion) == "" {
		return []SessionSummary{{
			Source:         SessionSourceSuggestedFirst,
			Classification: blockedClassification("the project did not produce a valid first-session suggestion"),
			Blocked:        profile.MemberCount,
		}}
	}
	return []SessionSummary{{
		Name:           strings.TrimSpace(suggestion),
		Source:         SessionSourceSuggestedFirst,
		Classification: ClassifyExistingRun(profile.MemberCount, 0, freshActions(profile.MemberCount), false),
		Fresh:          profile.MemberCount,
	}}
}

func freshActions(count int) []MemberAction {
	actions := make([]MemberAction, count)
	for i := range actions {
		actions[i] = MemberActionFresh
	}
	return actions
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
	s.Session = session.Name
	s.SessionSource = session.Source
	s.RunState = session.Classification.State
	s.Backend = session.Classification.Backend
	s.RunExecutable = session.Classification.Executable
	s.RestoreExisting = session.Classification.RestoreExisting
	s.DiscoveryFingerprint = session.Fingerprint
	if changed {
		s.clearDownstreamRunAnswers()
		// restore the just-selected authoritative facts cleared above
		s.Session = session.Name
		s.SessionSource = session.Source
		s.RunState = session.Classification.State
		s.Backend = session.Classification.Backend
		s.RunExecutable = session.Classification.Executable
		s.RestoreExisting = session.Classification.RestoreExisting
		s.DiscoveryFingerprint = session.Fingerprint
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
	s.DiscoveryFingerprint = ""
}

func (s *Spec) clearProfileAndRun() {
	s.Profile = ""
	s.ProfileBranch = ""
	s.clearFreshProfileAnswers()
	s.clearAuthoritativeRunAnswers()
	s.clearDownstreamRunAnswers()
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
	s.DiscoveryFingerprint = ""
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
	s.Visibility = ""
	s.LayoutPreset = ""
	s.LauncherPane = ""
	s.ExternalLead = false
	s.Goal = ""
	s.SeedFrom = ""
}
