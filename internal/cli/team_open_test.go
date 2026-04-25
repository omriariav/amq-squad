package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/internal/team"
)

func TestSplitVerbFor(t *testing.T) {
	cases := map[string]string{
		"vertical":   "split vertically",
		"v":          "split vertically",
		"Vertical":   "split vertically", // case-insensitive
		"horizontal": "split horizontally",
		"h":          "split horizontally",
		"H":          "split horizontally",
	}
	for in, want := range cases {
		got, err := splitVerbFor(in)
		if err != nil {
			t.Errorf(`splitVerbFor(%q) err: %v`, in, err)
			continue
		}
		if got != want {
			t.Errorf(`splitVerbFor(%q) = %q, want %q`, in, got, want)
		}
	}
	if _, err := splitVerbFor("sideways"); err == nil {
		t.Error("splitVerbFor(invalid): expected error")
	}
}

func TestApplescriptStringEscapes(t *testing.T) {
	cases := map[string]string{
		"hello":             `"hello"`,
		`with "quotes"`:     `"with \"quotes\""`,
		`back\slash`:        `"back\\slash"`,
		`'single'`:          `"'single'"`,
		`/path/with spaces`: `"/path/with spaces"`,
	}
	for in, want := range cases {
		if got := applescriptString(in); got != want {
			t.Errorf("applescriptString(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildITermScriptShape(t *testing.T) {
	members := []team.Member{
		{Role: "cto", Binary: "codex", Handle: "cto", Session: "cto"},
		{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "fullstack"},
	}
	script := buildITermScript("/home/u/proj", "/usr/local/bin/amq-squad", members, "split horizontally")

	wantSubstrings := []string{
		`tell application "iTerm"`,
		`activate`,
		`create window with default profile`,
		`set pane1 to (current session of current tab of newWindow)`,
		`tell pane1 to write text`,
		`amq-squad launch --role cto`,
		// Splits target a session (the preceding pane), never a tab.
		// iTerm2's `split ...` is a session command.
		`tell pane1 to set pane2 to (split horizontally with default profile)`,
		`tell pane2 to write text`,
		`amq-squad launch --role fullstack`,
		`end tell`,
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q. Script:\n%s", want, script)
		}
	}

	// Regression guard: the previous incarnation invoked `split` on
	// `current tab of newWindow`, which is a tab command and not valid
	// for split. Nothing in the new script should split off a tab.
	if strings.Contains(script, "tell current tab of newWindow to set") {
		t.Errorf("split must target a session, not a tab. Script:\n%s", script)
	}

	// First member must NOT get a split; only subsequent members split.
	firstSplitIdx := strings.Index(script, "split horizontally")
	firstPane1Idx := strings.Index(script, "pane1 to write text")
	if firstSplitIdx < firstPane1Idx {
		t.Error("first member must be written into the initial pane, not a split")
	}
}

func TestBuildITermScriptCascadesOffPreviousPane(t *testing.T) {
	// Three members should cascade: pane2 splits off pane1, pane3 off pane2.
	members := []team.Member{
		{Role: "cpo", Binary: "codex", Handle: "cpo", Session: "cpo"},
		{Role: "cto", Binary: "codex", Handle: "cto", Session: "cto"},
		{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "fullstack"},
	}
	script := buildITermScript("/p", "amq-squad", members, "split vertically")
	wants := []string{
		`tell pane1 to set pane2 to (split vertically with default profile)`,
		`tell pane2 to set pane3 to (split vertically with default profile)`,
	}
	for _, w := range wants {
		if !strings.Contains(script, w) {
			t.Errorf("cascade missing %q. Script:\n%s", w, script)
		}
	}
}

func TestBuildITermScriptUsesMemberCWD(t *testing.T) {
	members := []team.Member{
		{Role: "qa", Binary: "claude", Handle: "qa", Session: "qa", CWD: "/other/project-b"},
	}
	script := buildITermScript("/home/u/project-a", "amq-squad", members, "split vertically")
	if !strings.Contains(script, "cd /other/project-b") {
		t.Errorf("expected member CWD in cd prefix. Script:\n%s", script)
	}
}
