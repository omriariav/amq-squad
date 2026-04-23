package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/internal/team"
)

func TestSplitVerbFor(t *testing.T) {
	if v, err := splitVerbFor("vertical"); err != nil || v != "split vertically" {
		t.Errorf(`splitVerbFor("vertical") = %q, %v`, v, err)
	}
	if v, err := splitVerbFor("horizontal"); err != nil || v != "split horizontally" {
		t.Errorf(`splitVerbFor("horizontal") = %q, %v`, v, err)
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
		`set pane2 to (split horizontally with default profile)`,
		`tell pane2 to write text`,
		`amq-squad launch --role fullstack`,
		`end tell`,
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q. Script:\n%s", want, script)
		}
	}

	// First member must NOT get a split; only subsequent members split.
	firstSplitIdx := strings.Index(script, "split horizontally")
	firstPane1Idx := strings.Index(script, "pane1 to write text")
	if firstSplitIdx < firstPane1Idx {
		t.Error("first member must be written into the initial pane, not a split")
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
