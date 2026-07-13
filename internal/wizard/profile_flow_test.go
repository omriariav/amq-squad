package wizard

import (
	"fmt"
	"strings"
	"testing"
)

func TestProfileFlowClearsIncompatibleAnswers(t *testing.T) {
	tests := []struct {
		name  string
		act   func(*Spec)
		check func(*testing.T, Spec)
	}{
		{
			name: "existing profile clears fresh profile and old run",
			act:  func(s *Spec) { s.SelectExistingProfile("release") },
			check: func(t *testing.T, s Spec) {
				if s.ProfileBranch != ProfileBranchExisting || s.Profile != "release" || s.Roles != "" || s.Lead != "" || s.OperatorMode != "" || s.Session != "" || s.Goal != "" {
					t.Fatalf("incompatible answers survived: %+v", s)
				}
			},
		},
		{
			name: "new profile clears authoritative run",
			act: func(s *Spec) {
				s.ProfileBranch = ProfileBranchExisting
				s.SelectNewProfile("squad-next")
			},
			check: func(t *testing.T, s Spec) {
				if s.ProfileBranch != ProfileBranchNew || s.Session != "" || s.RunState != "" || s.Backend != "" || s.DiscoveryFingerprint != "" || s.Goal != "" {
					t.Fatalf("authoritative answers survived: %+v", s)
				}
			},
		},
		{
			name: "project change clears every project dependent",
			act:  func(s *Spec) { s.SelectProject("/other") },
			check: func(t *testing.T, s Spec) {
				if s.Project != "/other" || s.Profile != "" || s.ProfileBranch != "" || s.Session != "" || s.Visibility != "" || s.Goal != "" {
					t.Fatalf("project dependents survived: %+v", s)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := Spec{
				Project: "/repo", Profile: "old", Session: "old-session", SessionSource: SessionSourceLaunchHistory,
				Backend: BackendResume, RunState: RunStateStopped, RunExecutable: true, RestoreExisting: true, DiscoveryFingerprint: "old-fp",
				Roles: "cto,qa", Binary: "cto=codex", Lead: "cto", LeadMode: "planner", OperatorMode: "lead_pane",
				Model: "cto=x", Effort: "cto=high", Visibility: "current", LayoutPreset: "lead-left", LauncherPane: "keep", Goal: "old goal", SeedFrom: "issue:1",
			}
			tc.act(&s)
			tc.check(t, s)
		})
	}
}

func TestExistingSessionChangeClearsDownstreamAndDerivesBackend(t *testing.T) {
	s := Spec{ProfileBranch: ProfileBranchExisting, Profile: "release", Session: "old", Model: "qa=x", Visibility: "current", Goal: "stale"}
	s.SelectExistingSession(SessionSummary{
		Name: "history-b", Source: SessionSourceLaunchHistory, Fingerprint: "fp-b",
		RecordCount: 1, Members: []SessionMemberSummary{{Role: "cto", Action: MemberActionRestore}},
		Classification: RunClassification{State: RunStateStopped, Backend: BackendResume, Executable: true, RestoreExisting: true},
	})
	if s.Session != "history-b" || s.SessionSource != SessionSourceLaunchHistory || s.Backend != BackendResume || !s.RestoreExisting || !s.RunExecutable {
		t.Fatalf("authoritative session not selected: %+v", s)
	}
	if s.Model != "" || s.Visibility != "sibling-tabs" || s.LayoutPreset != "one-window-per-agent" || s.Goal != "" {
		t.Fatalf("session-dependent answers survived: %+v", s)
	}
}

func TestResumePlacementDefaultsUseOnlyUnanimousSavedTargets(t *testing.T) {
	record := func(target string) SessionMemberSummary {
		return SessionMemberSummary{Role: target, Action: MemberActionRestore, SavedLaunchIdentity: "record-" + target, SavedTarget: target}
	}
	tests := []struct {
		name    string
		members []SessionMemberSummary
		want    string
	}{
		{name: "agreed current", members: []SessionMemberSummary{record("current-window"), record("current-window")}, want: "current"},
		{name: "agreed detached", members: []SessionMemberSummary{record("new-session")}, want: "detached"},
		{name: "mixed", members: []SessionMemberSummary{record("current-window"), record("new-window")}, want: "sibling-tabs"},
		{name: "live divergent ignored", members: []SessionMemberSummary{{Role: "lead", Action: MemberActionLive, SavedLaunchIdentity: "live", SavedTarget: "new-session"}, record("current-window")}, want: "current"},
		{name: "all live has no placed records", members: []SessionMemberSummary{{Role: "lead", Action: MemberActionLive, SavedLaunchIdentity: "live", SavedTarget: "new-session"}}, want: "sibling-tabs"},
		{name: "absent", members: []SessionMemberSummary{{Role: "cto", Action: MemberActionRestore, SavedLaunchIdentity: "record"}}, want: "sibling-tabs"},
		{name: "zero", members: []SessionMemberSummary{{Role: "cto", Action: MemberActionFresh}}, want: "sibling-tabs"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResumeDefaultVisibility(tt.members); got != tt.want {
				t.Fatalf("visibility=%q want=%q", got, tt.want)
			}
		})
	}
}

func TestSelectExistingSessionExplicitPlacementPrefillWinsDerivedDefault(t *testing.T) {
	summary := SessionSummary{Name: "s", Fingerprint: "fp", RecordCount: 1, Classification: RunClassification{State: RunStateStopped, Backend: BackendResume, Executable: true, RestoreExisting: true}, Members: []SessionMemberSummary{{Role: "cto", Action: MemberActionRestore, SavedLaunchIdentity: "record", SavedTarget: "current-window"}}}
	s := Spec{Profile: "other", Visibility: "detached", VisibilityExplicit: true, LayoutPreset: "tiled", LayoutExplicit: true}
	s.SelectExistingProfile("release")
	s.SelectExistingSession(summary)
	if s.Visibility != "detached" || s.LayoutPreset != "tiled" {
		t.Fatalf("explicit placement overwritten: %+v", s)
	}
	derived := Spec{ProfileBranch: ProfileBranchExisting, Profile: "release"}
	derived.SelectExistingSession(summary)
	if derived.Visibility != "current" || derived.LayoutPreset != "lead-left" {
		t.Fatalf("derived placement=%q/%q", derived.Visibility, derived.LayoutPreset)
	}
}

func TestGoalExcerptAndCommandFormsPreserveReviewEvidence(t *testing.T) {
	goal := strings.Repeat("a", 170) + "\nsecond\nthird"
	excerpt := GoalExcerpt(goal)
	if len([]rune(excerpt)) > maxGoalExcerptRunes || !strings.HasSuffix(excerpt, "...") || strings.Count(excerpt, "\n") > 1 {
		t.Fatalf("excerpt=%q runes=%d", excerpt, len([]rune(excerpt)))
	}
	s := Spec{Backend: BackendResume, Project: "/repo", Profile: "release", ProfileBranch: ProfileBranchExisting, Session: "s", RunExecutable: true, RecordCount: 1, RestoreExisting: true, DiscoveryFingerprint: "fp", ResumeMembers: []SessionMemberSummary{{Role: "cto", Action: MemberActionRestore}}, Visibility: "current", LayoutPreset: "lead-top"}
	preview, live, err := s.CommandForms()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview, "resume --project /repo") || strings.Contains(preview, "--exec") || live != preview+" --exec" {
		t.Fatalf("preview=%q live=%q", preview, live)
	}
}

func TestSelectedSessionAndSpecCloneDeepCopyResumeMembers(t *testing.T) {
	summary := SessionSummary{Name: "s", Fingerprint: "fp", RecordCount: 1, Classification: RunClassification{State: RunStateStopped, Backend: BackendResume, Executable: true, RestoreExisting: true}, Members: []SessionMemberSummary{{Role: "cto", Action: MemberActionRestore, SavedNativeArgs: []string{"saved"}}}}
	s := Spec{ProfileBranch: ProfileBranchExisting, Profile: "release"}
	s.SelectExistingSession(summary)
	summary.Members[0].SavedNativeArgs[0] = "mutated-source"
	if s.ResumeMembers[0].SavedNativeArgs[0] != "saved" {
		t.Fatalf("selection aliased discovery members: %+v", s.ResumeMembers)
	}
	clone := s.Clone()
	clone.ResumeMembers[0].SavedNativeArgs[0] = "mutated-clone"
	if s.ResumeMembers[0].SavedNativeArgs[0] != "saved" {
		t.Fatalf("Spec.Clone aliased resume members: %+v", s.ResumeMembers)
	}
}

func TestInvalidateExistingRunClearsPolicyAndAllDownstreamState(t *testing.T) {
	s := Spec{Scope: "project", Project: "/repo", Profile: "release", ProfileBranch: ProfileBranchExisting, Session: "s", SessionSource: SessionSourceMemberPin, Backend: BackendResume, RunState: RunStateStopped, RunExecutable: true, RestoreExisting: true, RecordCount: 1, DiscoveryFingerprint: "fp", ResumeMembers: []SessionMemberSummary{{Role: "cto", Action: MemberActionRestore}}, BriefPath: "/repo/brief.md", BriefGoal: "goal", BriefSeed: "issue:1", Model: "cto=x", Effort: "cto=high", OperatorMode: "lead_pane", SelfOperatorLead: "cto", SelfOperatorAllow: "merge", OperatorNotifications: true, OperatorNotificationsRequested: true, OperatorNotificationsSet: true, Visibility: "current", LayoutPreset: "lead-left", LauncherPane: "keep", Goal: "g", SeedFrom: "issue:1"}
	s.InvalidateExistingRun()
	if s.Scope != "project" || s.Project != "/repo" || s.Profile != "release" || s.ProfileBranch != ProfileBranchExisting {
		t.Fatalf("invalidation lost upstream selection: %+v", s)
	}
	if s.Session != "" || s.Backend != "" || s.RunExecutable || s.RecordCount != 0 || len(s.ResumeMembers) != 0 || s.BriefPath != "" || s.BriefGoal != "" || s.BriefSeed != "" || s.Model != "" || s.Effort != "" || s.OperatorMode != "" || s.OperatorNotifications || !s.OperatorNotificationsRequested || !s.OperatorNotificationsSet || s.Visibility != "" || s.LayoutPreset != "" || s.LauncherPane != "" || s.Goal != "" || s.SeedFrom != "" {
		t.Fatalf("invalidation retained stale state: %+v", s)
	}
	s.SelectExistingSession(SessionSummary{Name: "fresh", Fingerprint: "new", Members: []SessionMemberSummary{{Role: "cto", Action: MemberActionFresh}}, Classification: RunClassification{State: RunStateNotStarted, Backend: BackendRunStart, Executable: true}})
	if !s.OperatorNotificationsRequested || !s.OperatorNotificationsSet {
		t.Fatalf("explicit notification intent was lost across refreshed reselection: %+v", s)
	}
}

func TestFormatSavedNativeArgsIsBoundedAndTerminalSafe(t *testing.T) {
	args := []string{"line1\nline2", "\x1b]52;c;clipboard\x07", strings.Repeat("x", 1000)}
	for i := 0; i < 20; i++ {
		args = append(args, fmt.Sprintf("arg-%02d", i))
	}
	got := FormatSavedNativeArgs(args)
	if len(got) > maxSavedNativeArgsDisplay {
		t.Fatalf("formatted args length=%d: %q", len(got), got)
	}
	if strings.ContainsAny(got, "\n\r\x1b\x07") {
		t.Fatalf("formatted args contain raw terminal controls: %q", got)
	}
	for _, want := range []string{`\n`, `\x1b`, `\a`} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatted args %q missing escaped %q", got, want)
		}
	}
}

func TestNoSessionProfileConsumesCLIDiscoveredSuggestedFirstRun(t *testing.T) {
	profile := ProfileSummary{Name: "unused", MemberCount: 2, Sessions: []SessionSummary{discoveredFreshSession("issue-431", SessionSourceSuggestedFirst, 2)}}
	sessions := profileSessions(profile, "issue-431")
	if len(sessions) != 1 || sessions[0].Name != "issue-431" || sessions[0].Source != SessionSourceSuggestedFirst || sessions[0].Classification.Backend != BackendRunStart || !sessions[0].Classification.Executable {
		t.Fatalf("suggested first run = %+v", sessions)
	}
}

func TestProfileSessionsNeverSynthesizesExecutableDiscovery(t *testing.T) {
	if sessions := profileSessions(ProfileSummary{Name: "unused", MemberCount: 2}, "issue-431"); len(sessions) != 0 {
		t.Fatalf("wizard synthesized discovery: %+v", sessions)
	}
}

func discoveredFreshSession(name string, source SessionSource, count int) SessionSummary {
	return SessionSummary{
		Name: name, Source: source, Fingerprint: "fingerprint-" + name,
		Classification: RunClassification{State: RunStateNotStarted, Backend: BackendRunStart, Executable: true},
		Fresh:          count,
	}
}
