package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func readTeamMember(t *testing.T, dir, role string) team.Member {
	t.Helper()
	cfg, err := team.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range cfg.Members {
		if m.Role == role {
			return m
		}
	}
	t.Fatalf("role %q not found in team.json", role)
	return team.Member{}
}

func TestOverlayInitWiresClaudeMember(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "analyst", Binary: "claude", Handle: "analyst", Session: "s"},
		},
	})
	_, _, err := captureOutput(t, func() error {
		return runTeamOverlay([]string{"init", "--role", "analyst",
			"--disable-plugins", "gws@workspace,docs@anthropic", "--disable-all-hooks"})
	})
	if err != nil {
		t.Fatalf("overlay init: %v", err)
	}

	// The overlay file exists with exactly the requested settings.
	path := overlayPath(dir, "analyst")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("overlay file not written: %v", err)
	}
	var got claudeSettingsOverlay
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("overlay is not valid JSON: %v\n%s", err, data)
	}
	if !got.DisableAllHooks {
		t.Error("disableAllHooks not set")
	}
	if v, ok := got.EnabledPlugins["gws@workspace"]; !ok || v {
		t.Errorf("gws@workspace should be disabled, got %v (present=%v)", v, ok)
	}
	if v, ok := got.EnabledPlugins["docs@anthropic"]; !ok || v {
		t.Errorf("docs@anthropic should be disabled, got %v (present=%v)", v, ok)
	}

	// team.json carries the wiring, relative to the member cwd (== team-home).
	m := readTeamMember(t, dir, "analyst")
	want := filepath.Join(team.DirName, overlaysDirName, "analyst.claude.json")
	if len(m.ClaudeArgs) != 2 || m.ClaudeArgs[0] != "--settings" || m.ClaudeArgs[1] != want {
		t.Fatalf("claude_args = %v, want [--settings %s]", m.ClaudeArgs, want)
	}
}

func TestOverlayInitIdempotent(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "analyst", Binary: "claude", Handle: "analyst", Session: "s"},
		},
	})
	for i := 0; i < 2; i++ {
		if _, _, err := captureOutput(t, func() error {
			return runTeamOverlay([]string{"init", "--role", "analyst"})
		}); err != nil {
			t.Fatalf("overlay init run %d: %v", i+1, err)
		}
	}
	m := readTeamMember(t, dir, "analyst")
	count := 0
	for _, a := range m.ClaudeArgs {
		if a == "--settings" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("re-running overlay init must not duplicate --settings: %v", m.ClaudeArgs)
	}
}

func TestOverlayInitCodexMemberRefused(t *testing.T) {
	seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"},
		},
	})
	_, _, err := captureOutput(t, func() error {
		return runTeamOverlay([]string{"init", "--role", "cto"})
	})
	if err == nil || !strings.Contains(err.Error(), "--profile") {
		t.Fatalf("codex member should be refused with codex --profile guidance, got %v", err)
	}
}

func TestOverlayInitWorkersSkipsLeadAndCodex(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Orchestrated: true,
		Lead:         "copilot",
		Members: []team.Member{
			{Role: "copilot", Binary: "claude", Handle: "copilot", Session: "s"},
			{Role: "analyst", Binary: "claude", Handle: "analyst", Session: "s"},
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"},
		},
	})
	_, _, err := captureOutput(t, func() error {
		return runTeamOverlay([]string{"init", "--workers", "--disable-all-hooks"})
	})
	if err != nil {
		t.Fatalf("overlay init --workers: %v", err)
	}
	if args := readTeamMember(t, dir, "copilot").ClaudeArgs; len(args) != 0 {
		t.Errorf("the lead must not be wired by --workers, got %v", args)
	}
	if args := readTeamMember(t, dir, "analyst").ClaudeArgs; len(args) != 2 {
		t.Errorf("worker analyst should be wired, got %v", args)
	}
	if args := readTeamMember(t, dir, "cto").CodexArgs; len(args) != 0 {
		t.Errorf("codex member must be skipped, got codex_args %v", args)
	}
	if _, err := os.Stat(overlayPath(dir, "copilot")); !os.IsNotExist(err) {
		t.Error("no overlay file should be generated for the lead")
	}
}

func TestOverlayInitWorkersOnFlatTeamTargetsAllClaude(t *testing.T) {
	// Non-orchestrated team: --workers has no lead to exclude, so every
	// claude member is targeted.
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "s"},
			{Role: "qa", Binary: "claude", Handle: "qa", Session: "s"},
		},
	})
	_, _, err := captureOutput(t, func() error {
		return runTeamOverlay([]string{"init", "--workers"})
	})
	if err != nil {
		t.Fatalf("overlay init --workers on flat team: %v", err)
	}
	for _, role := range []string{"fullstack", "qa"} {
		if args := readTeamMember(t, dir, role).ClaudeArgs; len(args) != 2 {
			t.Errorf("flat-team member %s should be wired, got %v", role, args)
		}
	}
}

func TestUpDryRunRejectsDanglingSettings(t *testing.T) {
	// Hand-edit damage: claude_args ending in a bare --settings would make
	// Claude swallow the next token as its value. The plan must refuse it.
	seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "analyst", Binary: "claude", Handle: "analyst", Session: "s",
				ClaudeArgs: []string{"--settings"}},
		},
	})
	_, _, err := captureOutput(t, func() error {
		return runUp([]string{"--dry-run", "--no-bootstrap"})
	})
	if err == nil || !strings.Contains(err.Error(), "dangling --settings") {
		t.Fatalf("dangling --settings should fail the plan by name, got %v", err)
	}
}

func TestOverlayInitForeignSettingsRequiresForce(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "analyst", Binary: "claude", Handle: "analyst", Session: "s",
				ClaudeArgs: []string{"--settings", "custom/overlay.json"}},
		},
	})
	_, _, err := captureOutput(t, func() error {
		return runTeamOverlay([]string{"init", "--role", "analyst"})
	})
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("foreign --settings must require --force, got %v", err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runTeamOverlay([]string{"init", "--role", "analyst", "--force"})
	}); err != nil {
		t.Fatalf("overlay init --force: %v", err)
	}
	m := readTeamMember(t, dir, "analyst")
	want := filepath.Join(team.DirName, overlaysDirName, "analyst.claude.json")
	if len(m.ClaudeArgs) != 2 || m.ClaudeArgs[1] != want {
		t.Fatalf("--force should replace the foreign --settings value, got %v", m.ClaudeArgs)
	}
}

func TestOverlayInitNoClobberExistingFile(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "analyst", Binary: "claude", Handle: "analyst", Session: "s"},
		},
	})
	path := overlayPath(dir, "analyst")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	userEdited := []byte("{\"enabledPlugins\": {\"keep@me\": true}}\n")
	if err := os.WriteFile(path, userEdited, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runTeamOverlay([]string{"init", "--role", "analyst", "--disable-all-hooks"})
	}); err != nil {
		t.Fatalf("overlay init: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(userEdited) {
		t.Fatalf("existing overlay must be kept without --force:\n%s", data)
	}
	// Wiring still proceeds.
	if args := readTeamMember(t, dir, "analyst").ClaudeArgs; len(args) != 2 {
		t.Fatalf("wiring should proceed even when the file is kept, got %v", args)
	}
}

func TestOverlayInitDryRunWritesNothing(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "analyst", Binary: "claude", Handle: "analyst", Session: "s"},
		},
	})
	stdout, _, err := captureOutput(t, func() error {
		return runTeamOverlay([]string{"init", "--role", "analyst", "--dry-run"})
	})
	if err != nil {
		t.Fatalf("overlay init --dry-run: %v", err)
	}
	if !strings.Contains(stdout, "dry run") {
		t.Errorf("dry-run should say so:\n%s", stdout)
	}
	if _, err := os.Stat(overlayPath(dir, "analyst")); !os.IsNotExist(err) {
		t.Error("dry-run must not write the overlay file")
	}
	if args := readTeamMember(t, dir, "analyst").ClaudeArgs; len(args) != 0 {
		t.Errorf("dry-run must not modify team.json, got %v", args)
	}
}

func TestOverlayInitRequiresExactlyOneTarget(t *testing.T) {
	seedTeam(t, team.Team{
		Members: []team.Member{{Role: "analyst", Binary: "claude", Handle: "analyst", Session: "s"}},
	})
	for _, args := range [][]string{
		{"init"},
		{"init", "--role", "analyst", "--workers"},
	} {
		if _, _, err := captureOutput(t, func() error {
			return runTeamOverlay(args)
		}); err == nil || !strings.Contains(err.Error(), "exactly one") {
			t.Errorf("runTeamOverlay(%v) = %v, want exactly-one-target usage error", args, err)
		}
	}
}

func TestUpDryRunFailsOnMissingOverlayFile(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "analyst", Binary: "claude", Handle: "analyst", Session: "s",
				ClaudeArgs: []string{"--settings", "missing/overlay.json"}},
		},
	})
	_, _, err := captureOutput(t, func() error {
		return runUp([]string{"--dry-run", "--no-bootstrap"})
	})
	if err == nil || !strings.Contains(err.Error(), "--settings file not found") {
		t.Fatalf("missing overlay should fail the plan, got %v", err)
	}

	// Creating the file clears the failure.
	path := filepath.Join(dir, "missing", "overlay.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runUp([]string{"--dry-run", "--no-bootstrap"})
	}); err != nil {
		t.Fatalf("plan should pass once the overlay exists: %v", err)
	}
}

func TestWireSettingsArg(t *testing.T) {
	// wired: fresh args gain the pair.
	action, out, err := wireSettingsArg(nil, "o.json", false)
	if err != nil || action != "wired" || len(out) != 2 {
		t.Fatalf("fresh wire = (%s, %v, %v)", action, out, err)
	}
	// unchanged: already pointing at the overlay.
	action, _, err = wireSettingsArg([]string{"--settings", "o.json"}, "o.json", false)
	if err != nil || action != "unchanged" {
		t.Fatalf("idempotent wire = (%s, %v)", action, err)
	}
	// inline = form.
	action, out, err = wireSettingsArg([]string{"--settings=other.json"}, "o.json", true)
	if err != nil || action != "replaced" || out[0] != "--settings=o.json" {
		t.Fatalf("inline replace = (%s, %v, %v)", action, out, err)
	}
	// foreign without force errors.
	if _, _, err := wireSettingsArg([]string{"--settings", "other.json"}, "o.json", false); err == nil {
		t.Fatal("foreign --settings without --force must error")
	}
}

func TestMemberSettingsRefsSkipsInlineJSON(t *testing.T) {
	refs := memberSettingsRefs([]string{
		"--settings", `{"enabledPlugins":{}}`,
		"--settings", "real/path.json",
		"--settings=also/real.json",
	})
	if len(refs) != 2 || refs[0] != "real/path.json" || refs[1] != "also/real.json" {
		t.Fatalf("refs = %v, want the two path-shaped values only", refs)
	}
}
