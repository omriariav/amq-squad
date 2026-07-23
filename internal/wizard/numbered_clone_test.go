package wizard

import (
	"bytes"
	"strings"
	"testing"
)

// TestRunNumberedOffersCloneWhenSuggestionMismatchesPinnedSession covers
// #523's original operator complaint: an existing profile pinned to one
// workstream, requested for a different one. The wizard must offer cloning
// into a new profile instead of a dead end.
func TestRunNumberedOffersCloneWhenSuggestionMismatchesPinnedSession(t *testing.T) {
	input := strings.Join([]string{
		"",  // project
		"",  // profile choice -> "release" (only option)
		"2", // session choice -> "Clone this roster..." sentinel
		"",  // new workstream session -> default (suggestion "v2")
		"",  // new profile name -> default ("release-v2")
		"",  // per-member override for cto: keep
		"",  // topology
		"",  // layout
		"",  // goal
		"",  // seed
		"",  // spare
		"",  // spare
	}, "\n") + "\n"
	var out bytes.Buffer
	got, err := RunNumbered(strings.NewReader("\n"+input), &out, NumberedOptions{
		Defaults: Spec{Project: "/repo", Visibility: "sibling-tabs"},
		InspectProject: func(string) (ProjectContext, error) {
			return ProjectContext{
				Project:           "/repo",
				SessionSuggestion: "v2",
				Profiles: []ProfileSummary{
					{
						Name: "release", MemberCount: 1,
						PinnedSession: "v1",
						Members:       []MemberSummary{{Role: "cto", Binary: "codex"}},
						Sessions:      []SessionSummary{discoveredFreshSession("v1", SessionSourceMemberPin, 1)},
					},
				},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("RunNumbered: %v\n%s", err, out.String())
	}
	if got.FromProfile != "release" {
		t.Fatalf("FromProfile = %q, want release", got.FromProfile)
	}
	if got.ProfileBranch != ProfileBranchNew {
		t.Fatalf("ProfileBranch = %q, want %q", got.ProfileBranch, ProfileBranchNew)
	}
	if got.Profile != "release-v2" || got.Session != "v2" {
		t.Fatalf("Profile/Session = %q/%q, want release-v2/v2", got.Profile, got.Session)
	}
	if got.Roles != "" || got.Binary != "" || got.Lead != "" {
		t.Fatalf("clone must not collect fresh-roster fields: %+v", got)
	}
	if !strings.Contains(out.String(), `Cloning profile "release"'s roster into new profile "release-v2" for session "v2"`) {
		t.Fatalf("output missing clone confirmation:\n%s", out.String())
	}
}

// TestRunNumberedSingleSessionMatchingSuggestionStaysFrictionless guards the
// deliberate choice that #523's clone offer never adds a prompt to the common
// case: when the desired session already matches the profile's one known
// session, there is nothing to resolve.
func TestRunNumberedSingleSessionMatchingSuggestionStaysFrictionless(t *testing.T) {
	input := strings.Join([]string{
		"", // project
		"", // profile choice
		"", // session: known run auto-accepted, no choice list shown
		"", // per-member override: keep
		"", // topology
		"", // layout
		"", // goal
		"", // seed
	}, "\n") + "\n"
	var out bytes.Buffer
	got, err := RunNumbered(strings.NewReader("\n"+input), &out, NumberedOptions{
		Defaults: Spec{Project: "/repo", Visibility: "sibling-tabs"},
		InspectProject: func(string) (ProjectContext, error) {
			return ProjectContext{
				Project:           "/repo",
				SessionSuggestion: "v1",
				Profiles: []ProfileSummary{
					{
						Name: "release", MemberCount: 1,
						PinnedSession: "v1",
						Members:       []MemberSummary{{Role: "cto", Binary: "codex"}},
						Sessions:      []SessionSummary{discoveredFreshSession("v1", SessionSourceMemberPin, 1)},
					},
				},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("RunNumbered: %v\n%s", err, out.String())
	}
	if got.FromProfile != "" || got.Session != "v1" {
		t.Fatalf("expected frictionless accept of the matching known session, got %+v", got)
	}
	if strings.Contains(out.String(), "Which existing run do you want?") {
		t.Fatalf("matching suggestion must not show a session choice prompt:\n%s", out.String())
	}
}

// TestRunNumberedTemplateProfileAcceptsAnyTypedSession covers #451: an
// unpinned template profile lets the operator type any workstream directly,
// with no clone involved (the profile itself already supports any session).
func TestRunNumberedTemplateProfileAcceptsAnyTypedSession(t *testing.T) {
	input := strings.Join([]string{
		"",         // project
		"",         // profile choice -> "pm-squad" (only option)
		"issue-42", // typed workstream session
		"",         // per-member override: keep
		"",         // topology
		"",         // layout
		"",         // goal
		"",         // seed
		"",         // spare
		"",         // spare
	}, "\n") + "\n"
	var out bytes.Buffer
	got, err := RunNumbered(strings.NewReader("\n"+input), &out, NumberedOptions{
		Defaults: Spec{Project: "/repo", Visibility: "sibling-tabs"},
		InspectProject: func(string) (ProjectContext, error) {
			return ProjectContext{
				Project: "/repo",
				Profiles: []ProfileSummary{
					{Name: "pm-squad", MemberCount: 1, Unpinned: true, Members: []MemberSummary{{Role: "cto", Binary: "codex"}}},
				},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("RunNumbered: %v\n%s", err, out.String())
	}
	if got.Session != "issue-42" {
		t.Fatalf("Session = %q, want issue-42", got.Session)
	}
	if got.FromProfile != "" || got.Profile != "pm-squad" {
		t.Fatalf("template launch must not clone: Profile=%q FromProfile=%q", got.Profile, got.FromProfile)
	}
	if !strings.Contains(out.String(), "unpinned template profile") {
		t.Fatalf("output missing template framing:\n%s", out.String())
	}
}

// TestRunNumberedTemplateProfilePresentedAsTemplateInPicker covers #451 item
// 3: the profile picker itself should frame an unpinned profile as a
// reusable template, not a run in progress.
func TestRunNumberedTemplateProfilePresentedAsTemplateInPicker(t *testing.T) {
	var out bytes.Buffer
	_, err := RunNumbered(strings.NewReader(strings.Repeat("\n", 14)), &out, NumberedOptions{
		Defaults: Spec{Project: "/repo", Visibility: "sibling-tabs"},
		InspectProject: func(string) (ProjectContext, error) {
			return ProjectContext{
				Project:           "/repo",
				SessionSuggestion: "issue-42",
				Profiles: []ProfileSummary{
					{Name: "pm-squad", MemberCount: 2, Unpinned: true, Members: []MemberSummary{{Role: "cto", Binary: "codex"}, {Role: "fullstack", Binary: "claude"}}},
				},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("RunNumbered: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "template — launch for a new workstream") {
		t.Fatalf("picker did not present the profile as a template:\n%s", out.String())
	}
}
