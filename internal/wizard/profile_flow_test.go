package wizard

import "testing"

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
		Classification: RunClassification{State: RunStateStopped, Backend: BackendResume, Executable: true, RestoreExisting: true},
	})
	if s.Session != "history-b" || s.SessionSource != SessionSourceLaunchHistory || s.Backend != BackendResume || !s.RestoreExisting || !s.RunExecutable {
		t.Fatalf("authoritative session not selected: %+v", s)
	}
	if s.Model != "" || s.Visibility != "" || s.Goal != "" {
		t.Fatalf("session-dependent answers survived: %+v", s)
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
