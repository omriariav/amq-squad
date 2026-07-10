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
	for _, want := range []string{"Preview only", "Project directory [/repo]", "builder: lead may implement and delegate (default)", "sibling-tabs: one visible tmux window per agent (default)"} {
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
					{Name: "default", MemberCount: 2, PinnedSession: "main-work"},
					{Name: "review", MemberCount: 3, PinnedSession: "review-work", Lead: "cto", LeadMode: "planner", OperatorMode: "noc", Members: []MemberSummary{{Role: "cto", Binary: "codex", Effort: "high"}}},
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
	for _, want := range []string{"origin omriariav/amq-squad", "review: 3 member(s), session review-work", "cto: codex, model=automatic, effort=high", "Operator interaction (authoritative): noc", "ships in v2.19.0: #391"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, out.String())
		}
	}
}

func TestPromptOperatorChoiceCapabilityGating(t *testing.T) {
	var out bytes.Buffer
	_, err := promptOperatorChoice(bufio.NewReader(strings.NewReader("4\n")), &out, nil, "lead_pane")
	if err == nil || !strings.Contains(err.Error(), "self_operator") {
		t.Fatalf("disabled choice error = %v", err)
	}
	for _, want := range []string{"Self-operator / delegated approval", "ships in v2.19.0: #391", "Notification add-on", "ships in v2.19.0: #390"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("capability rows missing %q:\n%s", want, out.String())
		}
	}

	caps := DefaultCapabilities()
	self := caps[CapabilitySelfOperator]
	self.Available = true
	caps[CapabilitySelfOperator] = self
	mode, err := promptOperatorChoice(bufio.NewReader(strings.NewReader("4\n")), &bytes.Buffer{}, caps, "lead_pane")
	if err != nil {
		t.Fatal(err)
	}
	if mode != "self_operator" {
		t.Fatalf("enabled capability result = mode %q", mode)
	}

	notifyCap := caps[CapabilityOperatorNotifications]
	notifyCap.Available = true
	caps[CapabilityOperatorNotifications] = notifyCap
	_, err = promptOperatorChoice(bufio.NewReader(strings.NewReader("5\n")), &bytes.Buffer{}, caps, "lead_pane")
	if err == nil || !strings.Contains(err.Error(), "operator_notifications") {
		t.Fatalf("notification slot must remain blocked without canonical serialization: %v", err)
	}
}

func TestRunNumberedExistingProfileCollectsLaunchOnlyOverrides(t *testing.T) {
	input := strings.Join([]string{
		"",             // project
		"",             // existing profile
		"",             // pinned session
		"2",            // override cto
		"launch-model", // launch-only model
		"4",            // high effort
		"",             // topology
		"",             // one-window layout
		"",             // close launcher
		"",             // goal
		"",             // seed
	}, "\n") + "\n"
	profile := ProfileSummary{
		Name: "review", MemberCount: 1, PinnedSession: "review-work",
		Members: []MemberSummary{{Role: "cto", Binary: "codex", Model: "stored-model", Effort: "low"}},
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
