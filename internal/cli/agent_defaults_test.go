package cli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestClaudeInScopePreauthAllowlistContents(t *testing.T) {
	allow := claudeInScopePreauthAllowlist("v2-14-0")
	// Narrowed slice (#296): PR creation ONLY. Feature-branch push is deliberately
	// not pre-authorized (no safe Claude pattern form), so the list is exactly one
	// PR-domain pattern and cannot — by construction — authorize push/tags/etc.
	if len(allow) != 1 || allow[0] != "Bash(gh pr create:*)" {
		t.Fatalf("allowlist = %v, want exactly [Bash(gh pr create:*)]", allow)
	}
	joined := strings.Join(allow, "\n")
	for _, forbidden := range []string{
		"git push", "origin main", "git tag", "gh release", "--tags", "--follow-tags",
		"Bash(git push:*)", "Bash(git:*)", "Bash(gh:*)", "Bash(:*)", "Bash(*)",
	} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("allowlist must never include %q:\n%s", forbidden, joined)
		}
	}
	if claudeInScopePreauthAllowlist("") != nil {
		t.Fatal("empty session must yield no allowlist")
	}
}

func TestClaudePreauthChildArgs(t *testing.T) {
	if claudePreauthChildArgs(nil) != nil {
		t.Fatal("empty allowlist must yield no child args (back-compat)")
	}
	args := claudePreauthChildArgs([]string{"Bash(gh pr create:*)", "Bash(git push origin codex/s-:*)"})
	if len(args) != 1 || !strings.HasPrefix(args[0], "--allowedTools=") {
		t.Fatalf("child args = %v, want one equals-joined --allowedTools token", args)
	}
	if !strings.Contains(args[0], "gh pr create") || !strings.Contains(args[0], "git push origin codex/s-") {
		t.Fatalf("allowedTools value missing patterns: %q", args[0])
	}
	if spec, inline, ok := nativeValueSpecForArg("claude", args[0]); !ok || !inline || spec.Canonical != "--allowed-tools" {
		t.Fatalf("native parser does not recognize safe equals form: spec=%+v inline=%v ok=%v", spec, inline, ok)
	}
}

func TestClaudeWorkerPreauthEligible(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"},
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "s"},
			{Role: "senior-dev", Binary: "codex", Handle: "senior-dev", Session: "s"},
		},
		Orchestrated: true,
		Lead:         "cto",
	})
	cases := []struct {
		name   string
		role   string
		binary string
		want   bool
	}{
		{"claude non-lead worker", "fullstack", "claude", true},
		{"claude lead excluded", "cto", "claude", false},
		{"codex worker out of scope", "fullstack", "codex", false},
		{"empty role", "", "claude", false},
		// Unknown/ad-hoc role not configured as a team member: rejected.
		{"unknown role rejected", "scratch", "claude", false},
		// Role configured for codex must not be pre-authorized when launched as claude.
		{"codex-configured role launched as claude", "senior-dev", "claude", false},
	}
	for _, tc := range cases {
		if got := claudeWorkerPreauthEligible(dir, "", tc.role, tc.binary); got != tc.want {
			t.Errorf("%s: eligible=%v, want %v", tc.name, got, tc.want)
		}
	}

	// Non-orchestrated team: never eligible (conservative default).
	flat := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "s"}},
	})
	if claudeWorkerPreauthEligible(flat, "", "fullstack", "claude") {
		t.Fatal("non-orchestrated team must not pre-authorize")
	}
}

func TestConfiguredClaudePermissionAllowlistIsStrictlyRoleScoped(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"},
			{Role: "qa", Binary: "claude", Handle: "qa", Session: "s", PermissionAllowlist: []string{"Bash(rm -rf /tmp/qa-review/*:*)"}},
			{Role: "reviewer", Binary: "claude", Handle: "reviewer", Session: "s", PermissionAllowlist: []string{"Bash(rm -rf /tmp/reviewer-work/*:*)"}},
		},
		Orchestrated: true,
		Lead:         "cto",
	})

	qaArgs, qaActions, added := applyClaudeWorkerPreauth(dir, "", "qa", "claude", "s", nil, true)
	if !added || !containsString(qaActions, "Bash(gh pr create:*)") || !containsString(qaActions, "Bash(rm -rf /tmp/qa-review/*:*)") {
		t.Fatalf("qa actions = %v, added=%v", qaActions, added)
	}
	if containsString(qaActions, "Bash(rm -rf /tmp/reviewer-work/*:*)") || strings.Contains(strings.Join(qaArgs, " "), "reviewer-work") {
		t.Fatalf("reviewer allowlist leaked into qa launch: actions=%v args=%v", qaActions, qaArgs)
	}

	reviewerArgs, reviewerActions, _ := applyClaudeWorkerPreauth(dir, "", "reviewer", "claude", "s", nil, false)
	if len(reviewerActions) != 1 || reviewerActions[0] != "Bash(rm -rf /tmp/reviewer-work/*:*)" {
		t.Fatalf("configured actions with built-in opt-out = %v", reviewerActions)
	}
	if strings.Contains(strings.Join(reviewerArgs, " "), "gh pr create") {
		t.Fatalf("built-in opt-out leaked PR grant: %v", reviewerArgs)
	}

	if args, actions, added := applyClaudeWorkerPreauth(dir, "", "qa", "codex", "s", nil, true); len(args) != 0 || len(actions) != 0 || added {
		t.Fatalf("binary mismatch must not receive qa allowlist: args=%v actions=%v added=%v", args, actions, added)
	}
}

func TestConfiguredClaudePermissionAllowlistMergesExplicitAllowedTools(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"},
			{Role: "qa", Binary: "claude", Handle: "qa", Session: "s", PermissionAllowlist: []string{"Bash(rm -rf /tmp/qa-review/*:*)"}},
		},
		Orchestrated: true,
		Lead:         "cto",
	})

	args, actions, added := applyClaudeWorkerPreauth(dir, "", "qa", "claude", "s", []string{
		"--allowedTools", "Read(/tmp/qa-review/**)",
		"--allowed-tools=Edit(/tmp/qa-review/**)",
		"--", "--allowedTools", "prompt text",
	}, true)
	for _, want := range []string{"Read(/tmp/qa-review/**)", "Bash(gh pr create:*)", "Bash(rm -rf /tmp/qa-review/*:*)"} {
		if !containsString(actions, want) {
			t.Fatalf("effective actions missing %q: %v", want, actions)
		}
	}
	if !containsString(actions, "Edit(/tmp/qa-review/**)") {
		t.Fatalf("effective actions missing kebab-case inline grant: %v", actions)
	}
	boundary := -1
	for i, arg := range args {
		if arg == "--" {
			boundary = i
			break
		}
	}
	if !added || boundary != 1 || !strings.HasPrefix(args[0], "--allowedTools=") || strings.Count(strings.Join(args[:boundary], " "), "--allowedTools") != 1 || strings.Contains(strings.Join(args[:boundary], " "), "--allowed-tools") {
		t.Fatalf("merged args = %v, added=%v", args, added)
	}
	if got := args[boundary:]; !reflect.DeepEqual(got, []string{"--", "--allowedTools", "prompt text"}) {
		t.Fatalf("literal prompt boundary changed: %v", got)
	}
}

func TestStripRecordedLauncherPreauthRecognizesHistoricalAndSafeForms(t *testing.T) {
	actions := []string{"Read(/tmp/qa/**)", "Bash(gh pr create:*)"}
	joined := strings.Join(actions, ",")
	tests := []struct {
		name string
		args []string
	}{
		{name: "historical camel", args: []string{"--chrome", "--allowedTools", joined}},
		{name: "historical kebab", args: []string{"--chrome", "--allowed-tools", joined}},
		{name: "safe camel", args: []string{"--chrome", "--allowedTools=" + joined}},
		{name: "safe kebab", args: []string{"--chrome", "--allowed-tools=" + joined}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := stripRecordedLauncherPreauth(tc.args, actions)
			if !reflect.DeepEqual(got, []string{"--chrome"}) {
				t.Fatalf("stripRecordedLauncherPreauth(%v) = %v", tc.args, got)
			}
		})
	}

	other := "Read(/tmp/other/**)"
	args := []string{"--allowedTools=" + other, "--", "--allowedTools=" + joined}
	if got := stripRecordedLauncherPreauth(args, actions); !reflect.DeepEqual(got, args) {
		t.Fatalf("different/boundary grants changed: got %v want %v", got, args)
	}
}
