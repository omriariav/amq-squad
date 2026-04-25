package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/internal/launch"
	"github.com/omriariav/amq-squad/internal/team"
)

func TestBuildBootstrapPrompt(t *testing.T) {
	got, err := buildBootstrapPrompt(bootstrapContext{
		Role:          "cto",
		Handle:        "cto",
		Binary:        "codex",
		Session:       "fresh-cto",
		CWD:           "/repo",
		Root:          "/repo/.agent-mail/fresh-cto",
		TeamRulesPath: "/repo/.amq-squad/team-rules.md",
		RolePath:      "/repo/.agent-mail/fresh-cto/agents/cto/role.md",
		LaunchPath:    "/repo/.agent-mail/fresh-cto/agents/cto/launch.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"You are a fresh amq-squad agent.",
		"Role: cto",
		"Handle: cto",
		"Team rules: /repo/.amq-squad/team-rules.md",
		"Role file: /repo/.agent-mail/fresh-cto/agents/cto/role.md",
		"Launch record: /repo/.agent-mail/fresh-cto/agents/cto/launch.json",
		"Stop and wait for instructions.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("bootstrap prompt missing %q in:\n%s", want, got)
		}
	}
}

func TestBuildBootstrapPromptWithoutRules(t *testing.T) {
	got, err := buildBootstrapPrompt(bootstrapContext{
		Handle: "claude",
		Binary: "claude",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Team rules: not configured") {
		t.Errorf("bootstrap prompt should mention missing team rules:\n%s", got)
	}
	if !strings.Contains(got, "Role: (none)") {
		t.Errorf("bootstrap prompt should default empty role:\n%s", got)
	}
}

func TestBootstrapPromptIncludesCurrentTeamRouting(t *testing.T) {
	teamHome := t.TempDir()
	qaProject := t.TempDir()
	if err := os.WriteFile(filepath.Join(teamHome, ".amqrc"), []byte(`{"root":".agent-mail","project":"pm-context"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(qaProject, ".amqrc"), []byte(`{"root":".agent-mail","project":"omri-pm"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := team.Write(teamHome, team.Team{
		Members: []team.Member{
			{Role: "cpo", Binary: "codex", Handle: "cpo", Session: "fresh-cpo"},
			{Role: "qa", Binary: "claude", Handle: "qa", Session: "fresh-qa", CWD: qaProject},
		},
	}); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(teamHome, ".agent-mail", "fresh-cpo")
	rec := launch.Record{
		Role:    "cpo",
		Handle:  "cpo",
		Binary:  "codex",
		Session: "fresh-cpo",
		CWD:     teamHome,
		Root:    root,
	}
	ctx := bootstrapContextFor(rec, filepath.Join(root, "agents", "cpo"), teamHome)
	got, err := buildBootstrapPrompt(ctx)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"Current team routing:",
		"from the current `.amq-squad/team.json`",
		"- cpo (you): handle cpo, binary codex, session fresh-cpo, project pm-context",
		"send: `amq send --to cpo --session fresh-cpo`",
		"- qa: handle qa, binary claude, session fresh-qa, project omri-pm",
		"send: `amq send --to qa --project omri-pm --session fresh-qa`",
		"Do not resume old sessions or route work to historical agents unless the user explicitly asks.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("bootstrap prompt missing %q in:\n%s", want, got)
		}
	}
}

func TestBootstrapCurrentTeamFallsBackToRoleWhenHandleMissing(t *testing.T) {
	teamHome := t.TempDir()
	if err := team.Write(teamHome, team.Team{
		Members: []team.Member{
			{Role: "qa", Binary: "claude", Session: "fresh-qa"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	rec := launch.Record{Role: "cpo", Handle: "cpo", CWD: teamHome}
	got := bootstrapCurrentTeam(rec, teamHome)
	if len(got) != 1 {
		t.Fatalf("bootstrapCurrentTeam returned %d members, want 1", len(got))
	}
	if got[0].Handle != "qa" {
		t.Fatalf("Handle = %q, want role fallback qa", got[0].Handle)
	}
	if got[0].Route != "amq send --to qa --session fresh-qa" {
		t.Fatalf("Route = %q", got[0].Route)
	}
}

func TestRouteCommandQuotesUnsafeValues(t *testing.T) {
	got := routeCommandFor("project-a", "project b", "qa lead", "fresh qa")
	want := "amq send --to 'qa lead' --project 'project b' --session 'fresh qa'"
	if got != want {
		t.Fatalf("routeCommandFor = %q, want %q", got, want)
	}
}
