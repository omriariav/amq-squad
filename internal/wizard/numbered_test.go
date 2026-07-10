package wizard

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunNumberedEnterThroughDefaults(t *testing.T) {
	var out bytes.Buffer
	got, err := RunNumbered(strings.NewReader(strings.Repeat("\n", 9)), &out, NumberedOptions{
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
	text := out.String()
	for _, want := range []string{"Preview only", "Project directory [/repo]", "builder: lead may implement and delegate (default)", "sibling-tabs: one visible tmux window per agent (default)"} {
		if !strings.Contains(text, want) {
			t.Fatalf("output missing %q:\n%s", want, text)
		}
	}
}

func TestRunNumberedAcceptsNumberedChoices(t *testing.T) {
	input := strings.Join([]string{
		"",  // project
		"",  // profile
		"",  // session
		"",  // roles
		"",  // lead
		"2", // planner
		"3", // current
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
}

func TestRunNumberedExistingProfileKeepsRosterAuthoritative(t *testing.T) {
	input := strings.Repeat("\n", 6)
	got, err := RunNumbered(strings.NewReader(input), &bytes.Buffer{}, NumberedOptions{
		Defaults: Spec{
			Project:    "/repo",
			Profile:    "review",
			Session:    "s",
			Roles:      "cto,qa",
			Binary:     "qa=claude",
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
	if got.Roles != "" || got.Binary != "" || got.LeadMode != "" {
		t.Fatalf("existing profile mutations not cleared: %+v", got)
	}
}

func TestRunNumberedRejectsUnknownChoice(t *testing.T) {
	input := strings.Join([]string{"", "", "", "", "", "99"}, "\n") + "\n"
	_, err := RunNumbered(strings.NewReader(input), &bytes.Buffer{}, NumberedOptions{
		Defaults:      Spec{Project: "/repo", Profile: "default", Session: "s"},
		ProfileExists: func(string, string) bool { return false },
	})
	if err == nil || !strings.Contains(err.Error(), "invalid lead mode choice") {
		t.Fatalf("expected lead mode choice error, got %v", err)
	}
}
