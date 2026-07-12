package wizard

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func TestRunNumberedEnterThroughDefaults(t *testing.T) {
	var out bytes.Buffer
	got, err := RunNumbered(strings.NewReader(strings.Repeat("\n", 24)), &out, NumberedOptions{
		Defaults: Spec{
			Project:    "/repo",
			Profile:    "default",
			Session:    "issue-393",
			Visibility: "sibling-tabs",
		},
		ProfileExists: func(string, string) bool { return false },
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Roles != "cto,senior-dev,qa" || got.Lead != "cto" || got.LeadMode != "builder" {
		t.Fatalf("default roster = %+v", got)
	}
	if got.ProfileBranch != ProfileBranchNew || got.Profile != "default" || got.Session != "issue-393" || got.Backend != BackendRunStart {
		t.Fatalf("new-profile derivation = %+v", got)
	}
	if got.Visibility != "sibling-tabs" {
		t.Fatalf("visibility = %q", got.Visibility)
	}
	if got.Binary != "cto=codex,senior-dev=codex,qa=codex" || got.Effort != "" {
		t.Fatalf("default binary/effort normalization = %+v", got)
	}
	if got.OperatorMode != "lead_pane" {
		t.Fatalf("visible topology operator default = %q", got.OperatorMode)
	}
	text := out.String()
	for _, want := range []string{"Answers are previewed first", "Project directory [/repo]", "builder: lead may implement and delegate (default)", "sibling-tabs: one visible tmux window per agent (default)"} {
		if !strings.Contains(text, want) {
			t.Fatalf("output missing %q:\n%s", want, text)
		}
	}
}

func TestRunNumberedDetachedDefaultsSeparateOperatorTerminal(t *testing.T) {
	got, err := RunNumbered(strings.NewReader(strings.Repeat("\n", 24)), &bytes.Buffer{}, NumberedOptions{
		Defaults:      Spec{Project: "/repo", Profile: "default", Session: "s", Visibility: "detached"},
		ProfileExists: func(string, string) bool { return false },
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.OperatorMode != "separate_terminal" {
		t.Fatalf("detached operator default = %q", got.OperatorMode)
	}
}

func TestRunNumberedAcceptsNumberedChoices(t *testing.T) {
	input := strings.Join([]string{
		"",  // project
		"",  // profile
		"",  // session
		"",  // roles
		"",  // cto binary
		"",  // cto model
		"",  // cto effort
		"",  // senior-dev binary
		"",  // senior-dev model
		"",  // senior-dev effort
		"",  // qa binary
		"",  // qa model
		"",  // qa effort
		"",  // lead
		"2", // planner
		"3", // current
		"",  // lead-left layout
		"",  // lead-pane operator contract
		"2", // attention-only notifications
		"",  // close launcher after start
		"",  // goal
		"",  // seed
	}, "\n") + "\n"
	got, err := RunNumbered(strings.NewReader(input), &bytes.Buffer{}, NumberedOptions{
		Defaults:      Spec{Project: "/repo", Profile: "default", Session: "s"},
		ProfileExists: func(string, string) bool { return false },
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.LeadMode != "planner" || got.Visibility != "current" {
		t.Fatalf("choices = lead_mode:%q visibility:%q", got.LeadMode, got.Visibility)
	}
	if got.OperatorMode != "lead_pane" {
		t.Fatalf("operator mode = %q", got.OperatorMode)
	}
	if !got.OperatorNotifications {
		t.Fatal("notification add-on was not selected")
	}
}

func TestRunNumberedReusesCallerReaderAndPreservesFollowingConsent(t *testing.T) {
	answers := []string{
		"", "", "", "", // project, profile, session, roles
		"", "", "", // cto binary/model/effort
		"", "", "", // senior-dev
		"", "", "", // qa
		"", "", "", "", "", "", "", "", "", // lead through seed
		"YES",
	}
	reader := bufio.NewReader(strings.NewReader(strings.Join(answers, "\n") + "\n"))
	_, err := RunNumbered(reader, &bytes.Buffer{}, NumberedOptions{
		Defaults:      Spec{Project: "/repo", Profile: "default", Session: "s"},
		ProfileExists: func(string, string) bool { return false },
	})
	if err != nil {
		t.Fatal(err)
	}
	consent, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(consent) != "YES" {
		t.Fatalf("following consent = %q", consent)
	}
}

func TestRunNumberedExistingProfileKeepsRosterAuthoritative(t *testing.T) {
	input := strings.Repeat("\n", 8)
	got, err := RunNumbered(strings.NewReader(input), &bytes.Buffer{}, NumberedOptions{
		Defaults: Spec{
			Project:    "/repo",
			Profile:    "review",
			Session:    "s",
			Roles:      "cto,qa",
			Binary:     "qa=claude",
			Effort:     "qa=high",
			LeadMode:   "planner",
			Visibility: "sibling-tabs",
		},
		ProfileExists: func(project, profile string) bool {
			return project == "/repo" && profile == "review"
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Roles != "" || got.Binary != "" || got.Effort != "" || got.LeadMode != "" {
		t.Fatalf("existing profile mutations not cleared: %+v", got)
	}
}

func TestRunNumberedRejectsUnknownChoice(t *testing.T) {
	answers := make([]string, 15)
	answers[14] = "99"
	input := strings.Join(answers, "\n") + "\n"
	_, err := RunNumbered(strings.NewReader(input), &bytes.Buffer{}, NumberedOptions{
		Defaults:      Spec{Project: "/repo", Profile: "default", Session: "s"},
		ProfileExists: func(string, string) bool { return false },
	})
	if err == nil || !strings.Contains(err.Error(), "invalid lead mode choice") {
		t.Fatalf("expected lead mode choice error, got %v", err)
	}
}

func TestRunNumberedListsExistingProfilesAndUsesPinnedSession(t *testing.T) {
	input := strings.Join([]string{
		"",  // project
		"2", // select review profile
		"",  // pinned session
		"",  // keep cto profile values
		"",  // topology
		"",  // one-window layout
		"",  // close launcher
		"",  // goal
		"",  // seed
	}, "\n") + "\n"
	var out bytes.Buffer
	got, err := RunNumbered(strings.NewReader(input), &out, NumberedOptions{
		Defaults: Spec{Project: "/repo", Visibility: "sibling-tabs"},
		InspectProject: func(string) (ProjectContext, error) {
			return ProjectContext{
				Project:           "/repo",
				OriginSlug:        "omriariav/amq-squad",
				SessionSuggestion: "issue-393",
				Profiles: []ProfileSummary{
					{Name: "default", MemberCount: 2, PinnedSession: "main-work", Sessions: []SessionSummary{discoveredFreshSession("main-work", SessionSourceMemberPin, 2)}},
					{Name: "review", MemberCount: 3, PinnedSession: "review-work", Lead: "cto", LeadMode: "planner", OperatorMode: "noc", Members: []MemberSummary{{Role: "cto", Binary: "codex", Effort: "high"}}, Sessions: []SessionSummary{discoveredFreshSession("review-work", SessionSourceMemberPin, 3)}},
				},
			}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Profile != "review" || got.Session != "review-work" || got.Roles != "" || got.LeadMode != "" {
		t.Fatalf("existing profile result = %+v", got)
	}
	if got.OperatorMode != "noc" {
		t.Fatalf("existing operator mode = %q", got.OperatorMode)
	}
	for _, want := range []string{"origin omriariav/amq-squad", "review · 3 members · review-work/not started · roster and contract stay authoritative", "Derived session \"review-work\" from member_pin", "cto: codex, model=automatic, effort=high", "Operator interaction (authoritative): noc · NOC/global orchestrator owns polling. Change it with 'amq-squad team operator set', then relaunch.", "Self-operator / delegated approval", "[locked: the stored profile contract decides]"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, out.String())
		}
	}
}

func TestRunNumberedOffersModelListWithCustomEscape(t *testing.T) {
	input := strings.Join([]string{
		"",    // project
		"",    // profile
		"",    // session
		"cto", // single role
		"2",   // claude binary
		"4",   // sonnet from the claude list (automatic, fable, opus, sonnet, haiku, custom)
		"",    // effort
		"",    // lead
		"",    // lead mode
		"",    // topology
		"",    // layout
		"",    // operator contract
		"",    // notifications
		"",    // launcher
		"",    // goal
		"",    // seed
	}, "\n") + "\n"
	var out bytes.Buffer
	got, err := RunNumbered(strings.NewReader(input), &out, NumberedOptions{
		Defaults:      Spec{Project: "/repo", Profile: "default", Session: "s"},
		ProfileExists: func(string, string) bool { return false },
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "cto=sonnet" {
		t.Fatalf("curated model pick = %q", got.Model)
	}
	for _, want := range []string{"fable", "opus", "sonnet", "haiku", "custom: type a model name"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("model list missing %q:\n%s", want, out.String())
		}
	}

	custom := strings.Join([]string{
		"", "", "", "cto",
		"",                     // codex binary
		"4",                    // custom escape (automatic, sol, terra, custom)
		"gpt-5.7-experimental", // free text
		// effort, lead, lead mode, topology, layout, operator,
		// notifications, launcher, goal, seed
		"", "", "", "", "", "", "", "", "", "",
	}, "\n") + "\n"
	got, err = RunNumbered(strings.NewReader(custom), &bytes.Buffer{}, NumberedOptions{
		Defaults:      Spec{Project: "/repo", Profile: "default", Session: "s"},
		ProfileExists: func(string, string) bool { return false },
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "cto=gpt-5.7-experimental" {
		t.Fatalf("custom model = %q", got.Model)
	}
}

func TestRunNumberedDerivesPinnedSessionWithoutPrompt(t *testing.T) {
	input := strings.Join([]string{
		"", // project
		"", // profile: default (existing)
		"", // keep cto profile values
		"", // topology
		"", // one-window layout
		"", // close launcher
		"", // goal
		"", // seed
	}, "\n") + "\n"
	var out bytes.Buffer
	got, err := RunNumbered(strings.NewReader(input), &out, NumberedOptions{
		Defaults: Spec{Project: "/repo", Visibility: "sibling-tabs"},
		InspectProject: func(string) (ProjectContext, error) {
			return ProjectContext{Project: "/repo", Profiles: []ProfileSummary{
				{Name: "default", MemberCount: 1, PinnedSession: "issue-136", Members: []MemberSummary{{Role: "cto", Binary: "codex"}}, Sessions: []SessionSummary{discoveredFreshSession("issue-136", SessionSourceMemberPin, 1)}},
			}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Session != "issue-136" {
		t.Fatalf("session = %q, want the pinned issue-136", got.Session)
	}
	for _, want := range []string{"Known run: issue-136", "Derived session \"issue-136\" from member_pin", "never accept an arbitrary session name"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("derived-session output missing %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "Workstream session [") {
		t.Fatalf("pinned profile exposed free-text session input:\n%s", out.String())
	}
}

func TestRunNumberedNoSessionProfileUsesSuggestedFirstRun(t *testing.T) {
	var out bytes.Buffer
	got, err := RunNumbered(strings.NewReader(strings.Repeat("\n", 10)), &out, NumberedOptions{
		Defaults: Spec{Project: "/repo", Profile: "unused", Visibility: "sibling-tabs"},
		InspectProject: func(string) (ProjectContext, error) {
			return ProjectContext{Project: "/repo", SessionSuggestion: "issue-431", Profiles: []ProfileSummary{{Name: "unused", MemberCount: 1, Members: []MemberSummary{{Role: "cto", Binary: "codex"}}, Sessions: []SessionSummary{discoveredFreshSession("issue-431", SessionSourceSuggestedFirst, 1)}}}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Session != "issue-431" || got.SessionSource != SessionSourceSuggestedFirst || got.Backend != BackendRunStart {
		t.Fatalf("unused authoritative profile = %+v", got)
	}
	if strings.Contains(out.String(), "Name the new session [") {
		t.Fatalf("unused existing profile entered generic session input:\n%s", out.String())
	}
}

func TestRunNumberedExistingProfileWithoutCLIDiscoveryFailsClosed(t *testing.T) {
	_, err := RunNumbered(strings.NewReader("\n\n"), &bytes.Buffer{}, NumberedOptions{
		Defaults: Spec{Project: "/repo", Profile: "review"},
		InspectProject: func(string) (ProjectContext, error) {
			return ProjectContext{Project: "/repo", SessionSuggestion: "issue-444", Profiles: []ProfileSummary{{Name: "review", MemberCount: 1}}}, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "no derivable session") {
		t.Fatalf("empty CLI discovery error=%v", err)
	}
}

func TestRunNumberedMultipleSessionsUseKnownRunListAndComposeResume(t *testing.T) {
	profile := ProfileSummary{Name: "release", MemberCount: 2, Members: []MemberSummary{{Role: "cto", Binary: "codex"}, {Role: "qa", Binary: "codex"}}, Sessions: []SessionSummary{
		{Name: "run-a", Source: SessionSourceLaunchHistory, Classification: RunClassification{State: RunStateNotStarted, Backend: BackendRunStart, Executable: true}, Fresh: 2},
		{Name: "run-b", Source: SessionSourceLaunchHistory, Fingerprint: "run-b-fp", RecordCount: 2, Members: []SessionMemberSummary{{Role: "cto", Binary: "codex", Action: MemberActionRestore}, {Role: "qa", Binary: "codex", Action: MemberActionRestore}}, Classification: RunClassification{State: RunStateStopped, Backend: BackendResume, Executable: true, RestoreExisting: true}, Restore: 2},
	}}
	var out bytes.Buffer
	got, err := RunNumbered(strings.NewReader("\n\n2\n\n\n"), &out, NumberedOptions{
		Defaults: Spec{Project: "/repo", Profile: "release"},
		InspectProject: func(string) (ProjectContext, error) {
			return ProjectContext{Project: "/repo", Profiles: []ProfileSummary{profile}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Session != "run-b" || got.Backend != BackendResume || got.RunState != RunStateStopped || got.DiscoveryFingerprint != "run-b-fp" {
		t.Fatalf("known-session selection = %+v", got)
	}
	for _, want := range []string{"Which existing run do you want?", "run-a · launch_history · not started", "run-b · launch_history · stopped", "restores saved launch", "existing brief is preserved"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, out.String())
		}
	}
}

func TestRunNumberedResumeActionScopedControls(t *testing.T) {
	tests := []struct {
		name    string
		records int
		state   RunState
		members []SessionMemberSummary
		input   string
		model   string
	}{
		{name: "all restore", records: 2, state: RunStateStopped, members: []SessionMemberSummary{{Role: "cto", Binary: "codex", Action: MemberActionRestore, SavedBinary: "codex", SavedModel: "saved", SavedEffort: "high", SavedNativeArgs: []string{"--saved"}}, {Role: "qa", Binary: "codex", Action: MemberActionRestore}}, input: "\n\n\n\n"},
		{name: "restore fresh", records: 1, state: RunStateStopped, members: []SessionMemberSummary{{Role: "cto", Binary: "codex", Action: MemberActionRestore}, {Role: "qa", Binary: "codex", Model: "stored", Action: MemberActionFresh}}, input: "\n\n2\n\n\n", model: "qa=gpt-5.6-sol"},
		{name: "live fresh no records", state: RunStatePartly, members: []SessionMemberSummary{{Role: "cto", Binary: "codex", Action: MemberActionLive}, {Role: "qa", Binary: "codex", Action: MemberActionFresh}}, input: "\n\n2\n\n\n", model: "qa=gpt-5.6-sol"},
		{name: "live restore", records: 1, state: RunStatePartly, members: []SessionMemberSummary{{Role: "cto", Binary: "codex", Action: MemberActionLive}, {Role: "qa", Binary: "codex", Action: MemberActionRestore}}, input: "\n\n\n\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			summary := SessionSummary{Name: "s", Source: SessionSourceMemberPin, Fingerprint: "fp", RecordCount: tt.records, Members: tt.members, Classification: RunClassification{State: tt.state, Backend: BackendResume, Executable: true, RestoreExisting: tt.records > 0}}
			profileMembers := make([]MemberSummary, 0, len(tt.members))
			for _, member := range tt.members {
				profileMembers = append(profileMembers, MemberSummary{Role: member.Role, Binary: member.Binary, Model: member.Model, Effort: member.Effort})
			}
			profile := ProfileSummary{Name: "release", MemberCount: len(tt.members), Members: profileMembers, Sessions: []SessionSummary{summary}}
			var out bytes.Buffer
			got, err := RunNumbered(strings.NewReader(tt.input), &out, NumberedOptions{Defaults: Spec{Project: "/repo", Profile: "release"}, InspectProject: func(string) (ProjectContext, error) {
				return ProjectContext{Project: "/repo", Profiles: []ProfileSummary{profile}}, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			if got.Model != tt.model || got.Effort != "" || got.Backend != BackendResume || got.RecordCount != tt.records {
				t.Fatalf("resume answers=%+v", got)
			}
			if strings.Contains(out.String(), "effort override") || strings.Contains(out.String(), "Override cto at launch") {
				t.Fatalf("resume exposed run-start controls:\n%s", out.String())
			}
			if _, err := got.ResumeArgs(); err != nil {
				t.Fatalf("composed resume args: %v", err)
			}
		})
	}
}

func TestRunNumberedRunningAndBlockedExistingRunsRemainNonExecutable(t *testing.T) {
	for _, state := range []RunState{RunStateRunning, RunStateBlocked} {
		t.Run(string(state), func(t *testing.T) {
			summary := SessionSummary{Name: "s", Source: SessionSourceMemberPin, Fingerprint: "fp", Classification: RunClassification{State: state}}
			profile := ProfileSummary{Name: "release", MemberCount: 1, Members: []MemberSummary{{Role: "cto", Binary: "codex"}}, Sessions: []SessionSummary{summary}}
			var out bytes.Buffer
			got, err := RunNumbered(strings.NewReader("\n\n"), &out, NumberedOptions{Defaults: Spec{Project: "/repo", Profile: "release"}, InspectProject: func(string) (ProjectContext, error) {
				return ProjectContext{Project: "/repo", Profiles: []ProfileSummary{profile}}, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			if got.RunExecutable || got.RunState != state || strings.Contains(out.String(), "Topology") || !strings.Contains(out.String(), "read-only") {
				t.Fatalf("state=%s got=%+v output=%s", state, got, out.String())
			}
		})
	}
}

func TestRunNumberedUnknownProfilePrefillDefaultsToCreate(t *testing.T) {
	input := strings.Repeat("\n", 30)
	got, err := RunNumbered(strings.NewReader(input), &bytes.Buffer{}, NumberedOptions{
		Defaults: Spec{Project: "/repo", Profile: "brand-new", Session: "explicit-session"},
		InspectProject: func(string) (ProjectContext, error) {
			return ProjectContext{Project: "/repo", SessionSuggestion: "suggested-session", NewProfileSuggestion: "squad-suggested-session", Profiles: []ProfileSummary{{Name: "release", MemberCount: 1, PinnedSession: "main"}}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ProfileBranch != ProfileBranchNew || got.Profile != "brand-new" || got.Session != "explicit-session" {
		t.Fatalf("unknown prefill was not preserved as new: %+v", got)
	}
}

func TestPromptOperatorChoiceCapabilityGating(t *testing.T) {
	var out bytes.Buffer
	mode, err := promptOperatorChoice(bufio.NewReader(strings.NewReader("4\n")), &out, nil, "lead_pane")
	if err != nil || mode != "self_operator" {
		t.Fatalf("default capability = %q, %v", mode, err)
	}
	for _, want := range []string{"Self-operator / delegated approval"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("capability rows missing %q:\n%s", want, out.String())
		}
	}

	caps := DefaultCapabilities()
	self := caps[CapabilitySelfOperator]
	self.Available = true
	caps[CapabilitySelfOperator] = self
	mode, err = promptOperatorChoice(bufio.NewReader(strings.NewReader("4\n")), &bytes.Buffer{}, caps, "lead_pane")
	if err != nil {
		t.Fatal(err)
	}
	if mode != "self_operator" {
		t.Fatalf("enabled capability result = mode %q", mode)
	}

	if !CapabilityAvailable(caps, CapabilityOperatorNotifications) {
		t.Fatal("notification capability unavailable")
	}
	for _, option := range OperatorOptions() {
		if option.ID == "operator_notifications" || option.Blocked && option.Requires == CapabilityOperatorNotifications {
			t.Fatalf("notification capability remains coupled to blocked operator option: %+v", option)
		}
	}
}

func TestRunNumberedExistingProfileCollectsLaunchOnlyOverrides(t *testing.T) {
	input := strings.Join([]string{
		"",             // project
		"",             // existing profile
		"2",            // override cto
		"4",            // model override: custom
		"launch-model", // custom launch-only model
		"4",            // high effort
		"",             // topology
		"",             // one-window layout
		"",             // close launcher
		"",             // goal
		"",             // seed
	}, "\n") + "\n"
	profile := ProfileSummary{
		Name: "review", MemberCount: 1, PinnedSession: "review-work",
		Members: []MemberSummary{{Role: "cto", Binary: "codex", Model: "stored-model", Effort: "low"}}, Sessions: []SessionSummary{discoveredFreshSession("review-work", SessionSourceMemberPin, 1)},
	}
	got, err := RunNumbered(strings.NewReader(input), &bytes.Buffer{}, NumberedOptions{
		Defaults: Spec{Project: "/repo", Profile: "review", Visibility: "sibling-tabs"},
		InspectProject: func(string) (ProjectContext, error) {
			return ProjectContext{Project: "/repo", Profiles: []ProfileSummary{profile}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "cto=launch-model" || got.Effort != "cto=high" {
		t.Fatalf("launch overrides = model %q effort %q", got.Model, got.Effort)
	}
	if got.Roles != "" || got.Binary != "" || got.Lead != "" || got.LeadMode != "" {
		t.Fatalf("existing roster mutation leaked into spec: %+v", got)
	}
	if profile.Members[0].Model != "stored-model" || profile.Members[0].Effort != "low" {
		t.Fatalf("profile summary mutated: %+v", profile.Members[0])
	}
}
