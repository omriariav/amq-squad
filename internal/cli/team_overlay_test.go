package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestOverlayInitSerializesProfileReadModifyWrite(t *testing.T) {
	dir := seedTeam(t, team.Team{Members: []team.Member{{Role: "analyst", Binary: "claude", Handle: "analyst", Session: "s"}}})
	chdir(t, dir)
	oldHook := teamOverlayAfterRead
	writerDone := make(chan error, 1)
	teamOverlayAfterRead = func() {
		started := make(chan struct{})
		go func() {
			close(started)
			writerDone <- team.WithProfileLock(dir, team.DefaultProfile, func() error {
				current, err := team.ReadProfile(dir, team.DefaultProfile)
				if err != nil {
					return err
				}
				current.Members[0].Model = "concurrent-profile-update"
				return team.WriteProfileUnderLock(dir, team.DefaultProfile, current)
			})
		}()
		<-started
		select {
		case err := <-writerDone:
			t.Fatalf("profile writer interleaved with overlay RMW: %v", err)
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Cleanup(func() { teamOverlayAfterRead = oldHook })
	if _, _, err := captureOutput(t, func() error { return runTeamOverlay([]string{"init", "--role", "analyst"}) }); err != nil {
		t.Fatalf("overlay init: %v", err)
	}
	select {
	case err := <-writerDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent profile writer did not resume")
	}
	current, err := team.ReadProfile(dir, team.DefaultProfile)
	if err != nil {
		t.Fatal(err)
	}
	if current.Members[0].Model != "concurrent-profile-update" || len(current.Members[0].ClaudeArgs) != 2 {
		t.Fatalf("lost concurrent or overlay update: %+v", current.Members[0])
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

func TestGeneratedToolPoliciesDiscoverInheritancePreserveAllowAndRevokeConflicts(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("HOME", t.TempDir())
	dir := seedTeam(t, team.Team{
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "claude", Handle: "cto", Session: "s", ToolProfile: team.ToolProfileFull},
			{Role: "designer", Binary: "claude", Handle: "designer", Session: "s"},
			{Role: "backend", Binary: "codex", Handle: "backend", Session: "s"},
		},
	})
	writeFile(t, filepath.Join(dir, ".claude", "settings.json"), `{"enabledPlugins":{"gws@workspace":true,"docs@anthropic":true}}`)
	writeFile(t, filepath.Join(dir, ".mcp.json"), `{"mcpServers":{"browser":{"command":"browser"}}}`)
	writeFile(t, filepath.Join(codexHome, "config.toml"), "[mcp_servers.github]\ncommand = \"github\"\n")
	writeFile(t, filepath.Join(dir, ".codex", "config.toml"), "[plugins.\"project-helper\"]\nenabled = true\n")

	if _, _, err := captureOutput(t, func() error {
		return runTeamOverlay([]string{"init", "--role", "designer", "--tool-profile", "browser", "--allow-tools", "plugin:gws@workspace,mcp:browser"})
	}); err != nil {
		t.Fatalf("generate Claude policy: %v", err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runTeamOverlay([]string{"init", "--role", "backend", "--tool-profile", "coding", "--allow-tools", "mcp:github"})
	}); err != nil {
		t.Fatalf("generate Codex policy: %v", err)
	}
	claude := readTeamMember(t, dir, "designer")
	if got := strings.Join(claude.ToolEnabledSet(), ","); got != "mcp:browser,plugin:gws@workspace" || !containsString(claude.ToolBlocklist, "plugin:docs@anthropic") {
		t.Fatalf("Claude effective policy enabled=%v revoked=%v", claude.ToolEnabledSet(), claude.ToolBlocklist)
	}
	if args := claude.ToolArgs(); !containsString(args, "--strict-mcp-config") || !containsString(args, "--mcp-config") {
		t.Fatalf("Claude launch args lack strict MCP enforcement: %v", args)
	}
	codex := readTeamMember(t, dir, "backend")
	if got := strings.Join(codex.ToolEnabledSet(), ","); got != "mcp:github" || !containsString(codex.ToolBlocklist, "plugin:project-helper") {
		t.Fatalf("Codex effective policy enabled=%v revoked=%v", codex.ToolEnabledSet(), codex.ToolBlocklist)
	}
	args := codex.ToolArgs()
	if !containsString(args, "plugins.\"project-helper\".enabled=false") {
		t.Fatalf("Codex project conflict lacks top-precedence revocation: %v", args)
	}
	if lead := readTeamMember(t, dir, "cto"); lead.EffectiveToolProfile() != team.ToolProfileFull || len(lead.ToolBlocklist) != 0 {
		t.Fatalf("broad lead policy changed: %+v", lead)
	}
	stdout, _, err := captureOutput(t, func() error { return runUp([]string{"--dry-run", "--no-bootstrap", "--json"}) })
	if err != nil {
		t.Fatalf("up dry-run with generated policies: %v", err)
	}
	plan := decodeJSONEnvelope[teamPlan](t, stdout).Data
	for _, row := range plan.Plan {
		switch row.Role {
		case "designer":
			if row.ToolPolicySource != "member_generated_profile" || row.ToolPolicyPrecedence != "binary_native_profile" || !containsString(row.ToolLaunchArgs, "--strict-mcp-config") {
				t.Fatalf("Claude plan lacks effective policy evidence: %+v", row)
			}
		case "backend":
			if row.ToolPolicySource != "member_generated_profile" || !strings.HasPrefix(row.ToolPolicyPrecedence, "cli_override>") || !containsString(row.ToolLaunchArgs, "plugins.\"project-helper\".enabled=false") {
				t.Fatalf("Codex plan lacks precedence/argv evidence: %+v", row)
			}
		}
	}
	settingsPath := overlayPath(dir, "designer")
	mcpPath := strings.TrimSuffix(settingsPath, ".claude.json") + ".claude.mcp.json"
	originalSettings, _ := os.ReadFile(settingsPath)
	originalMCP, _ := os.ReadFile(mcpPath)
	t.Run("tampered settings", func(t *testing.T) {
		writeFile(t, settingsPath, `{"enabledPlugins":{"docs@anthropic":true}}`)
		_, _, err := captureOutput(t, func() error { return runUp([]string{"--dry-run", "--no-bootstrap", "--json"}) })
		if err == nil || !strings.Contains(err.Error(), "Claude settings materialization differs") {
			t.Fatalf("settings tamper must fail readiness: %v", err)
		}
		writeFile(t, settingsPath, string(originalSettings))
	})
	t.Run("tampered strict MCP", func(t *testing.T) {
		writeFile(t, mcpPath, `{"mcpServers":{"browser":{"command":"evil"},"extra":{"command":"extra"}}}`)
		_, _, err := captureOutput(t, func() error { return runUp([]string{"--dry-run", "--no-bootstrap", "--json"}) })
		if err == nil || !strings.Contains(err.Error(), "Claude strict MCP materialization differs") {
			t.Fatalf("strict MCP tamper must fail readiness: %v", err)
		}
		writeFile(t, mcpPath, string(originalMCP))
	})
	t.Run("same-name source definition drift", func(t *testing.T) {
		writeFile(t, filepath.Join(dir, ".mcp.json"), `{"mcpServers":{"browser":{"command":"changed-browser"}}}`)
		_, _, err := captureOutput(t, func() error { return runUp([]string{"--dry-run", "--no-bootstrap", "--json"}) })
		if err == nil || !strings.Contains(err.Error(), "Claude strict MCP materialization differs") {
			t.Fatalf("same-name source definition drift must fail readiness: %v", err)
		}
	})
}

func TestGeneratedToolPoliciesFailClosedWithoutPartialWrites(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	dir := seedTeam(t, team.Team{Members: []team.Member{
		{Role: "backend", Binary: "codex", Handle: "backend", Session: "s"},
		{Role: "qa", Binary: "codex", Handle: "qa", Session: "s"},
	}})
	writeFile(t, filepath.Join(codexHome, "config.toml"), "[mcp_servers.github]\ncommand = \"github\"\n")
	conflict := filepath.Join(codexHome, generatedCodexProfileName(team.DefaultProfile, "qa")+".config.toml")
	writeFile(t, conflict, "# operator-owned different policy\n")
	_, _, err := captureOutput(t, func() error {
		return runTeamOverlay([]string{"init", "--workers", "--tool-profile", "coding"})
	})
	if err == nil || !strings.Contains(err.Error(), "different content") {
		t.Fatalf("later target conflict must fail closed: %v", err)
	}
	first := filepath.Join(codexHome, generatedCodexProfileName(team.DefaultProfile, "backend")+".config.toml")
	if _, statErr := os.Stat(first); !os.IsNotExist(statErr) {
		t.Fatalf("validation failure partially wrote first target: %v", statErr)
	}
	if got := readTeamMember(t, dir, "backend"); got.ToolProfile != "" || got.ToolConfig != "" {
		t.Fatalf("validation failure partially persisted team member: %+v", got)
	}
}

func TestGeneratedToolPoliciesRejectUnsupportedCodexCapabilitySyntax(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	seedTeam(t, team.Team{Members: []team.Member{{Role: "backend", Binary: "codex", Handle: "backend", Session: "s"}}})
	writeFile(t, filepath.Join(codexHome, "config.toml"), "mcp_servers = { github = { command = \"gh\" } }\n")
	_, _, err := captureOutput(t, func() error {
		return runTeamOverlay([]string{"init", "--role", "backend", "--tool-profile", "coding"})
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported inline Codex capability syntax") {
		t.Fatalf("unsupported config must fail closed: %v", err)
	}
}

func TestGeneratedToolPolicyDriftBlocksLaunchPlan(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	dir := seedTeam(t, team.Team{Members: []team.Member{{Role: "backend", Binary: "codex", Handle: "backend", Session: "s"}}})
	writeFile(t, filepath.Join(codexHome, "config.toml"), "[mcp_servers.github]\ncommand = \"github\"\n")
	if _, _, err := captureOutput(t, func() error {
		return runTeamOverlay([]string{"init", "--role", "backend", "--tool-profile", "coding"})
	}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, ".codex", "config.toml"), "[plugins.\"late-project-helper\"]\nenabled = true\n")
	_, _, err := captureOutput(t, func() error { return runUp([]string{"--dry-run", "--no-bootstrap", "--json"}) })
	if err == nil || !strings.Contains(err.Error(), "tool policy drift/not-ready") || !strings.Contains(err.Error(), "late-project-helper") {
		t.Fatalf("new inherited capability must block launch plan: %v", err)
	}
}

func TestGeneratedToolPolicyMidApplyFailureRetainsExactRecoveryEvidence(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	dir := seedTeam(t, team.Team{Members: []team.Member{
		{Role: "backend", Binary: "codex", Handle: "backend", Session: "s"},
		{Role: "qa", Binary: "codex", Handle: "qa", Session: "s"},
	}})
	writeFile(t, filepath.Join(codexHome, "config.toml"), "[mcp_servers.github]\ncommand = \"github\"\n")
	previousFault := generatedPolicyApplyFault
	generatedPolicyApplyFault = func(index int, _ string) error {
		if index == 0 {
			return errors.New("injected mid-apply stop")
		}
		return nil
	}
	t.Cleanup(func() { generatedPolicyApplyFault = previousFault })
	_, _, err := captureOutput(t, func() error {
		return runTeamOverlay([]string{"init", "--workers", "--tool-profile", "coding"})
	})
	if err == nil || !strings.Contains(err.Error(), "recovery evidence retained") {
		t.Fatalf("mid-apply failure = %v", err)
	}
	manifest := filepath.Join(dir, team.DirName, "evidence", "tool-policy-transaction.json")
	b, readErr := os.ReadFile(manifest)
	if readErr != nil {
		t.Fatalf("recovery journal missing: %v", readErr)
	}
	for _, want := range []string{"before_exists", "after_sha256", "staged_path", "backend", "qa"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("recovery journal missing %q: %s", want, b)
		}
	}
	first := filepath.Join(codexHome, generatedCodexProfileName(team.DefaultProfile, "backend")+".config.toml")
	secondStaged := filepath.Join(codexHome, generatedCodexProfileName(team.DefaultProfile, "qa")+".config.toml.amq-squad.next")
	if _, statErr := os.Stat(first); statErr != nil {
		t.Fatalf("first publish point not represented: %v", statErr)
	}
	if _, statErr := os.Stat(secondStaged); statErr != nil {
		t.Fatalf("remaining intended body is not staged for recovery: %v", statErr)
	}
	if member := readTeamMember(t, dir, "backend"); member.ToolConfig != "" {
		t.Fatalf("team profile must remain pre-transaction until all files publish: %+v", member)
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
