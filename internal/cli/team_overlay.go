package cli

import (
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

// overlaysDirName is the team-home subdirectory that holds generated
// per-member Claude Code settings overlays. Generated and wired by
// `team overlay init`; the files stay human-editable afterwards.
const overlaysDirName = "overlays"

// overlayPath returns the canonical overlay file for a role under the
// team-home: .amq-squad/<overlays>/<role>.claude.json.
func overlayPath(projectDir, role string) string {
	return filepath.Join(projectDir, team.DirName, overlaysDirName, role+".claude.json")
}

// claudeSettingsOverlay is the subset of Claude Code's settings schema the
// generator writes. The file accepts the full schema; users can extend it
// freely after generation (the generator never clobbers without --force).
//
// enabledPlugins is deliberately always emitted, even empty: Claude's
// settings merge treats it as a SPARSE override map (absent keys inherit
// from the project/user config), so {} is a no-op placeholder that shows
// users where disables go. If Claude ever changed the semantics to "empty
// map replaces the set", this would need omitempty — pinned here so the
// assumption is explicit.
type claudeSettingsOverlay struct {
	EnabledPlugins  map[string]bool `json:"enabledPlugins"`
	DisableAllHooks bool            `json:"disableAllHooks,omitempty"`
}

// teamOverlayAfterRead is a deterministic test seam. It runs while a real
// overlay mutation holds the profile writer lock, after reading the snapshot
// that will be validated and written.
var teamOverlayAfterRead = func() {}

// generatedPolicyApplyFault injects a deterministic failure after a published
// target file. A retained recovery journal plus staged/backup paths make the
// partial apply exactly reconcilable.
var generatedPolicyApplyFault = func(index int, path string) error { return nil }

func runTeamOverlay(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, `amq-squad team overlay - per-member Claude settings overlays

Usage:
  amq-squad team overlay init (--role ROLE | --workers) [--profile NAME]
      [--project DIR] [--disable-plugins id@market,...] [--disable-all-hooks]
      [--tool-profile minimal|coding|browser|data|custom]
      [--allow-tools mcp:name,plugin:id] [--disable-mcp name,...]
      [--force] [--dry-run]

Generates .amq-squad/overlays/<role>.claude.json and wires the member's
claude_args (["--settings", <path relative to the member cwd>]) in team.json,
so that member launches with a trimmed plugin/hook surface while the rest of
the squad keeps the full project configuration. The flagship use: a same-cwd
orchestrated squad where only the lead needs every plugin and hook.

With --tool-profile, this is the first-class cross-binary policy generator.
Claude members receive a generated settings overlay. Codex members receive a
$CODEX_HOME/<profile>.config.toml plus top-precedence -c revocations for every
discovered inherited MCP not explicitly preserved by --allow-tools. The team
profile is wired without hand-editing team.json.

Examples:
  amq-squad team overlay init --role analyst --disable-all-hooks
  amq-squad team overlay init --role analyst --disable-plugins gws@ws,doc@anthropic
  amq-squad team overlay init --workers --disable-all-hooks
  amq-squad team overlay init --workers --dry-run
`)
		if len(args) == 0 {
			return usageErrorf("overlay requires a subcommand (e.g. 'init')")
		}
		return nil
	}
	switch args[0] {
	case "init":
		return runTeamOverlayInit(args[1:])
	default:
		return usageErrorf("unknown 'team overlay' subcommand: %q. Try 'init'.", args[0])
	}
}

func runTeamOverlayInit(args []string) error {
	fs := flag.NewFlagSet("team overlay init", flag.ContinueOnError)
	roleFlag := fs.String("role", "", "team role to generate + wire an overlay for")
	workers := fs.Bool("workers", false, "target every claude member except the orchestration lead")
	profileFlag := fs.String("profile", "", "team profile to update (default: default profile)")
	projectFlag := fs.String("project", "", "project/team-home directory to update (default: cwd)")
	disablePluginsRaw := fs.String("disable-plugins", "", "comma-separated plugin ids (name@marketplace) to disable in the overlay")
	disableAllHooks := fs.Bool("disable-all-hooks", false, "set disableAllHooks: true in the overlay")
	toolProfile := fs.String("tool-profile", "", "generate and wire a first-class least-required policy for Claude and Codex members")
	allowToolsRaw := fs.String("allow-tools", "", "comma-separated audited enabled set using mcp:name or plugin:id")
	disableMCPRaw := fs.String("disable-mcp", "", "additional inherited Codex MCP server names to revoke")
	force := fs.Bool("force", false, "overwrite an existing overlay file / replace a foreign --settings entry")
	dryRun := fs.Bool("dry-run", false, "print the plan without writing the overlay or team.json")
	fs.Usage = func() { _ = runTeamOverlay(nil) }
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if (*roleFlag == "") == !*workers {
		return usageErrorf("pass exactly one of --role ROLE or --workers")
	}
	firstClass := flagWasSet(fs, "tool-profile")
	if firstClass {
		switch strings.TrimSpace(*toolProfile) {
		case team.ToolProfileMinimal, team.ToolProfileCoding, team.ToolProfileBrowser, team.ToolProfileData, team.ToolProfileCustom:
		default:
			return usageErrorf("--tool-profile must be minimal, coding, browser, data, or custom")
		}
	}

	ctx, err := resolveCanonicalContext(contextResolveOptions{
		ProjectFlag: *projectFlag, ProfileFlag: *profileFlag,
		ProjectExplicit: flagWasSet(fs, "project"), ProfileExplicit: flagWasSet(fs, "profile"),
	})
	if err != nil {
		return err
	}
	emitContextDiagnostics(ctx)
	projectDir, profile := ctx.ProjectDir, ctx.Profile
	overlay := claudeSettingsOverlay{EnabledPlugins: map[string]bool{}}
	for _, id := range splitCommaList(*disablePluginsRaw) {
		overlay.EnabledPlugins[id] = false
	}
	overlay.DisableAllHooks = *disableAllHooks
	body, err := json.MarshalIndent(overlay, "", "  ")
	if err != nil {
		return fmt.Errorf("encode overlay: %w", err)
	}
	body = append(body, '\n')

	apply := func(t team.Team, writeProfile func(team.Team) error) error {
		if firstClass {
			return applyGeneratedToolPolicies(t, writeProfile, generatedToolPolicyOptions{
				Role: *roleFlag, Workers: *workers, TeamProfile: profile, Profile: strings.TrimSpace(*toolProfile),
				AllowTools: splitCommaList(*allowToolsRaw), DisableMCP: splitCommaList(*disableMCPRaw),
				DisablePlugins: splitCommaList(*disablePluginsRaw), DisableAllHooks: *disableAllHooks,
				Force: *force, DryRun: *dryRun,
			})
		}
		targets, err := overlayTargets(t, *roleFlag, *workers)
		if err != nil {
			return err
		}
		changed := false
		for _, idx := range targets {
			m := &t.Members[idx]
			path := overlayPath(t.Project, m.Role)
			rel, err := overlayRelPath(m.EffectiveCWD(t.Project), path)
			if err != nil {
				return fmt.Errorf("member %s: %w", m.Role, err)
			}

			// 1. The overlay file. No-clobber: an existing (possibly user-edited)
			// overlay is kept unless --force; wiring still proceeds either way.
			fileAction := "write"
			if _, statErr := os.Stat(path); statErr == nil && !*force {
				fileAction = "keep existing"
			}
			// 2. The team.json wiring. Runs before the dry-run gate on purpose:
			// a foreign --settings conflict would block any real run, so the
			// preview should fail the same way instead of printing a plan that
			// could never execute.
			wireAction, newArgs, err := wireSettingsArg(m.ClaudeArgs, rel, *force)
			if err != nil {
				return fmt.Errorf("member %s: %w", m.Role, err)
			}

			fmt.Printf("%s:\n  overlay: %s (%s)\n  claude_args: %s (%s)\n",
				m.Role, path, fileAction, joinedAgentArgs(newArgs), wireAction)

			if *dryRun {
				continue
			}
			if fileAction == "write" {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					return fmt.Errorf("create overlays dir: %w", err)
				}
				if err := os.WriteFile(path, body, 0o644); err != nil {
					return fmt.Errorf("write overlay: %w", err)
				}
			}
			if wireAction != "unchanged" {
				m.ClaudeArgs = newArgs
				changed = true
			}
		}

		if *dryRun {
			fmt.Println("\n(dry run - nothing written)")
			return nil
		}
		if changed {
			if err := writeProfile(t); err != nil {
				return fmt.Errorf("write team.json: %w", err)
			}
		}
		fmt.Printf("\nnext: amq-squad up --dry-run   # the wired members launch with --settings\n")
		return nil
	}
	if *dryRun {
		t, err := team.ReadProfile(projectDir, profile)
		if err != nil {
			return fmt.Errorf("read team: %w", err)
		}
		return apply(t, nil)
	}
	return team.WithProfileLock(projectDir, profile, func() error {
		t, err := team.ReadProfile(projectDir, profile)
		if err != nil {
			return fmt.Errorf("read team: %w", err)
		}
		teamOverlayAfterRead()
		return apply(t, func(updated team.Team) error {
			return team.WriteProfileUnderLock(projectDir, profile, updated)
		})
	})
}

type generatedToolPolicyOptions struct {
	Role            string
	Workers         bool
	TeamProfile     string
	Profile         string
	AllowTools      []string
	DisableMCP      []string
	DisablePlugins  []string
	DisableAllHooks bool
	Force           bool
	DryRun          bool
}

func applyGeneratedToolPolicies(t team.Team, writeProfile func(team.Team) error, opts generatedToolPolicyOptions) error {
	targets, err := generatedPolicyTargets(t, opts.Role, opts.Workers)
	if err != nil {
		return err
	}
	allow := dedupeSortedStrings(opts.AllowTools)
	for _, entry := range allow {
		if !strings.HasPrefix(entry, "mcp:") && !strings.HasPrefix(entry, "plugin:") {
			return fmt.Errorf("--allow-tools entry %q must use mcp:name or plugin:id", entry)
		}
	}
	var plans []generatedPolicyPlan
	for _, idx := range targets {
		plan, planErr := buildGeneratedPolicyPlan(t, idx, opts, allow)
		if planErr != nil {
			return planErr
		}
		plans = append(plans, plan)
	}
	return applyGeneratedToolPolicyPlans(t, writeProfile, plans, opts.DryRun)
}

// applyGeneratedToolPolicyPlans validates and publishes a complete set of
// per-member plans as one profile transaction. Callers may build each plan
// with a different accepted tool profile, but no target is written until all
// members and all materializations have passed validation.
func applyGeneratedToolPolicyPlans(t team.Team, writeProfile func(team.Team) error, plans []generatedPolicyPlan, dryRun bool) error {
	// Validate every target and every existing materialization before the first
	// write. A bad later target can never orphan an earlier generated profile.
	if err := validateGeneratedToolPolicyPlans(plans); err != nil {
		return err
	}
	for i := range plans {
		fmt.Printf("%s:\n  tool_profile: %s\n  enabled_set: %s\n  revoked_set: %s\n  sources: %s\n  precedence: %s\n",
			plans[i].After.Role, plans[i].After.ToolProfile, strings.Join(plans[i].After.ToolEnabledSet(), ","),
			strings.Join(plans[i].After.ToolBlocklist, ","), strings.Join(plans[i].After.ToolPolicySources, ","), plans[i].After.ToolPolicyPrecedence())
		for _, file := range plans[i].Files {
			fmt.Printf("  policy: %s (%s)\n", file.Path, file.Action)
		}
	}
	if dryRun {
		fmt.Println("\n(dry run - nothing written)")
		return nil
	}
	manifest := filepath.Join(t.Project, team.DirName, "evidence", "tool-policy-transaction.json")
	if err := stageGeneratedPolicyFiles(plans); err != nil {
		return err
	}
	if err := writeGeneratedPolicyRecovery(manifest, plans); err != nil {
		return err
	}
	published := 0
	for _, plan := range plans {
		for _, file := range plan.Files {
			if file.Action == "write" {
				if err := os.Rename(file.StagedPath, file.Path); err != nil {
					return fmt.Errorf("apply generated tool policy; recovery evidence retained at %s: %w", manifest, err)
				}
				if err := generatedPolicyApplyFault(published, file.Path); err != nil {
					return fmt.Errorf("apply generated tool policy; recovery evidence retained at %s: %w", manifest, err)
				}
				published++
			}
		}
		t.Members[plan.Index] = plan.After
	}
	changed := false
	for _, plan := range plans {
		changed = changed || !reflect.DeepEqual(plan.Before, plan.After)
	}
	if changed {
		if err := writeProfile(t); err != nil {
			return fmt.Errorf("write team.json; recovery evidence retained at %s: %w", manifest, err)
		}
	}
	if err := os.Remove(manifest); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("tool policies applied but recovery manifest cleanup failed: %w", err)
	}
	cleanupGeneratedPolicyStaging(plans)
	fmt.Println("\nnext: amq-squad up --dry-run --json   # inspect enabled_set, source, precedence, and launch overrides")
	return nil
}

// validateGeneratedToolPolicyPlans is the shared read-only half of generated
// policy application. Preparation proposals call this exact validator before
// roster creation so discovered-config errors and foreign target files cannot
// surface after unrelated coordination artifacts have already been written.
func validateGeneratedToolPolicyPlans(plans []generatedPolicyPlan) error {
	for i := range plans {
		for j := range plans[i].Files {
			action, actionErr := generatedPolicyFileAction(plans[i].Files[j].Path, plans[i].Files[j].Body, plans[i].Force)
			if actionErr != nil {
				return fmt.Errorf("member %s: %w", plans[i].After.Role, actionErr)
			}
			plans[i].Files[j].Action = action
			plans[i].Files[j].AfterSHA256 = contentSHA256(plans[i].Files[j].Body)
			if before, readErr := os.ReadFile(plans[i].Files[j].Path); readErr == nil {
				plans[i].Files[j].BeforeExists = true
				plans[i].Files[j].BeforeSHA256 = contentSHA256(before)
			} else if !os.IsNotExist(readErr) {
				return readErr
			}
		}
	}
	return nil
}

type generatedPolicyFile struct {
	Path         string `json:"path"`
	Action       string `json:"action"`
	Body         []byte `json:"-"`
	BeforeExists bool   `json:"before_exists"`
	BeforeSHA256 string `json:"before_sha256,omitempty"`
	AfterSHA256  string `json:"after_sha256"`
	StagedPath   string `json:"staged_path,omitempty"`
	BackupPath   string `json:"backup_path,omitempty"`
}

type generatedPolicyPlan struct {
	Index  int                   `json:"-"`
	Force  bool                  `json:"-"`
	Before team.Member           `json:"before"`
	After  team.Member           `json:"after"`
	Files  []generatedPolicyFile `json:"files"`
}

func buildGeneratedPolicyPlan(t team.Team, idx int, opts generatedToolPolicyOptions, allow []string) (generatedPolicyPlan, error) {
	before := t.Members[idx]
	after := before
	after.ToolAllowlist = append([]string(nil), allow...)
	after.ToolPolicyDrift = nil
	after.ToolDisableAllHooks = opts.DisableAllHooks
	allowSet := map[string]bool{}
	for _, entry := range allow {
		allowSet[entry] = true
	}
	var discovered, sources, block []string
	var files []generatedPolicyFile
	var err error
	switch normalizedAgentBinary(after.Binary) {
	case "claude":
		home, homeErr := modelUserHomeDir()
		if homeErr != nil {
			return generatedPolicyPlan{}, homeErr
		}
		claudeDir := strings.TrimSpace(modelGetenv("CLAUDE_CONFIG_DIR"))
		if claudeDir == "" {
			claudeDir = filepath.Join(home, ".claude")
		}
		jsonSources := []string{
			filepath.Join(claudeDir, "settings.json"),
			filepath.Join(home, ".claude.json"),
			filepath.Join(t.Project, ".claude", "settings.json"),
			filepath.Join(t.Project, ".claude", "settings.local.json"),
			filepath.Join(t.Project, ".mcp.json"),
		}
		var mcpDefs map[string]json.RawMessage
		discovered, sources, mcpDefs, err = discoverClaudeCapabilities(jsonSources)
		if err != nil {
			return generatedPolicyPlan{}, fmt.Errorf("member %s: %w", after.Role, err)
		}
		for _, id := range opts.DisablePlugins {
			discovered = append(discovered, "plugin:"+id)
		}
		discovered = dedupeSortedStrings(discovered)
		if err := validateRequestedAllowlist(after.Role, allow, discovered); err != nil {
			return generatedPolicyPlan{}, err
		}
		overlay := claudeSettingsOverlay{EnabledPlugins: map[string]bool{}, DisableAllHooks: opts.DisableAllHooks}
		for _, entry := range discovered {
			if allowSet[entry] {
				if id, ok := strings.CutPrefix(entry, "plugin:"); ok {
					overlay.EnabledPlugins[id] = true
				}
				continue
			}
			block = append(block, entry)
			if id, ok := strings.CutPrefix(entry, "plugin:"); ok {
				overlay.EnabledPlugins[id] = false
			}
		}
		settingsBody, marshalErr := json.MarshalIndent(overlay, "", "  ")
		if marshalErr != nil {
			return generatedPolicyPlan{}, marshalErr
		}
		settingsBody = append(settingsBody, '\n')
		settingsPath := overlayPath(t.Project, after.Role)
		after.ToolConfig, err = overlayRelPath(after.EffectiveCWD(t.Project), settingsPath)
		if err != nil {
			return generatedPolicyPlan{}, err
		}
		mcpPath := strings.TrimSuffix(settingsPath, ".claude.json") + ".claude.mcp.json"
		after.ToolMCPConfig, err = overlayRelPath(after.EffectiveCWD(t.Project), mcpPath)
		if err != nil {
			return generatedPolicyPlan{}, err
		}
		mcpBody, marshalErr := renderClaudeStrictMCP(allow, mcpDefs)
		if marshalErr != nil {
			return generatedPolicyPlan{}, marshalErr
		}
		files = []generatedPolicyFile{{Path: settingsPath, Body: settingsBody}, {Path: mcpPath, Body: mcpBody}}
		after.ClaudeArgs, err = removeNativePolicyArg(after.ClaudeArgs, "--settings", opts.Force)
	case "codex":
		home, homeErr := generatedCodexHome()
		if homeErr != nil {
			return generatedPolicyPlan{}, fmt.Errorf("member %s: %w", after.Role, homeErr)
		}
		for _, path := range []string{filepath.Join(home, "config.toml"), filepath.Join(t.Project, ".codex", "config.toml")} {
			entries, present, discoverErr := discoverCodexCapabilities(path)
			if discoverErr != nil {
				return generatedPolicyPlan{}, fmt.Errorf("member %s: %w", after.Role, discoverErr)
			}
			discovered = append(discovered, entries...)
			if present {
				sources = append(sources, path)
			}
		}
		for _, name := range opts.DisableMCP {
			discovered = append(discovered, "mcp:"+name)
		}
		discovered = dedupeSortedStrings(discovered)
		if err := validateRequestedAllowlist(after.Role, allow, discovered); err != nil {
			return generatedPolicyPlan{}, err
		}
		for _, entry := range discovered {
			if !allowSet[entry] {
				block = append(block, entry)
			}
		}
		after.ToolConfig = generatedCodexProfileName(opts.TeamProfile, after.Role)
		after.ToolMCPConfig = ""
		path := filepath.Join(home, after.ToolConfig+".config.toml")
		files = []generatedPolicyFile{{Path: path, Body: renderCodexToolProfile(opts.Profile, allow, block)}}
		after.CodexArgs, err = removeNativePolicyArg(after.CodexArgs, "--profile", opts.Force)
	default:
		return generatedPolicyPlan{}, fmt.Errorf("member %s uses unsupported binary %q", after.Role, after.Binary)
	}
	if err != nil {
		return generatedPolicyPlan{}, fmt.Errorf("member %s: %w", after.Role, err)
	}
	after.ToolProfile = opts.Profile
	after.ToolBlocklist = dedupeSortedStrings(block)
	after.ToolPolicySources = dedupeSortedStrings(sources)
	return generatedPolicyPlan{Index: idx, Force: opts.Force, Before: before, After: after, Files: files}, nil
}

func validateRequestedAllowlist(role string, allow, discovered []string) error {
	seen := map[string]bool{}
	for _, entry := range discovered {
		seen[entry] = true
	}
	for _, entry := range allow {
		if !seen[entry] {
			return fmt.Errorf("member %s: explicitly allowed tool %q was not found in any supported user/project source; refusing to claim it is enabled", role, entry)
		}
	}
	return nil
}

func discoverClaudeCapabilities(paths []string) ([]string, []string, map[string]json.RawMessage, error) {
	var entries, sources []string
	mcp := map[string]json.RawMessage{}
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, nil, nil, err
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(b, &raw); err != nil {
			return nil, nil, nil, fmt.Errorf("unsupported or invalid Claude config %s: %w", path, err)
		}
		sources = append(sources, path)
		if value, ok := raw["enabledPlugins"]; ok {
			var plugins map[string]bool
			if err := json.Unmarshal(value, &plugins); err != nil {
				return nil, nil, nil, fmt.Errorf("unsupported enabledPlugins syntax in %s", path)
			}
			for id := range plugins {
				entries = append(entries, "plugin:"+id)
			}
		}
		if value, ok := raw["mcpServers"]; ok {
			var defs map[string]json.RawMessage
			if err := json.Unmarshal(value, &defs); err != nil {
				return nil, nil, nil, fmt.Errorf("unsupported mcpServers syntax in %s", path)
			}
			for name, def := range defs {
				entries = append(entries, "mcp:"+name)
				mcp[name] = def
			}
		}
	}
	return dedupeSortedStrings(entries), dedupeSortedStrings(sources), mcp, nil
}

func renderClaudeStrictMCP(allow []string, defs map[string]json.RawMessage) ([]byte, error) {
	selected := map[string]json.RawMessage{}
	for _, entry := range allow {
		if name, ok := strings.CutPrefix(entry, "mcp:"); ok {
			selected[name] = defs[name]
		}
	}
	b, err := json.MarshalIndent(map[string]any{"mcpServers": selected}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func discoverCodexCapabilities(path string) ([]string, bool, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var entries []string
	for _, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "mcp_servers") || strings.HasPrefix(line, "plugins") {
			return nil, true, fmt.Errorf("unsupported inline Codex capability syntax in %s: %s", path, line)
		}
		if !strings.HasPrefix(line, "[") {
			continue
		}
		kind := ""
		prefix := ""
		switch {
		case strings.HasPrefix(line, "[mcp_servers."):
			kind, prefix = "mcp:", "[mcp_servers."
		case strings.HasPrefix(line, "[plugins."):
			kind, prefix = "plugin:", "[plugins."
		default:
			continue
		}
		if !strings.HasSuffix(line, "]") {
			return nil, true, fmt.Errorf("unsupported Codex capability table syntax in %s: %s", path, line)
		}
		name := strings.TrimSuffix(strings.TrimPrefix(line, prefix), "]")
		name = strings.Trim(strings.TrimSpace(name), "\"")
		if name == "" || strings.Contains(name, ".") {
			return nil, true, fmt.Errorf("unsupported Codex capability name syntax in %s: %s", path, line)
		}
		entries = append(entries, kind+name)
	}
	return dedupeSortedStrings(entries), true, nil
}

func writeGeneratedPolicyRecovery(path string, plans []generatedPolicyPlan) error {
	b, err := json.MarshalIndent(map[string]any{"schema": 1, "state": "applying", "plans": plans}, "", "  ")
	if err != nil {
		return err
	}
	return writeGeneratedPolicyFile(path, append(b, '\n'))
}

func stageGeneratedPolicyFiles(plans []generatedPolicyPlan) error {
	for i := range plans {
		for j := range plans[i].Files {
			file := &plans[i].Files[j]
			if file.Action != "write" {
				continue
			}
			file.StagedPath = file.Path + ".amq-squad.next"
			file.BackupPath = file.Path + ".amq-squad.bak"
			if file.BeforeExists {
				before, err := os.ReadFile(file.Path)
				if err != nil {
					return err
				}
				if contentSHA256(before) != file.BeforeSHA256 {
					return fmt.Errorf("tool policy target %s changed after validation; no files published", file.Path)
				}
				if err := writeGeneratedPolicyFile(file.BackupPath, before); err != nil {
					return err
				}
			} else {
				file.BackupPath = ""
			}
			if err := writeGeneratedPolicyFile(file.StagedPath, file.Body); err != nil {
				return err
			}
		}
	}
	return nil
}

func cleanupGeneratedPolicyStaging(plans []generatedPolicyPlan) {
	for _, plan := range plans {
		for _, file := range plan.Files {
			for _, path := range []string{file.StagedPath, file.BackupPath} {
				if path != "" {
					_ = os.Remove(path)
				}
			}
		}
	}
}

func contentSHA256(body []byte) string {
	sum := sha256.Sum256(body)
	return fmt.Sprintf("%x", sum[:])
}

func writeGeneratedPolicyFile(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func generatedPolicyTargets(t team.Team, role string, workers bool) ([]int, error) {
	if role != "" {
		for i, member := range t.Members {
			if strings.EqualFold(member.Role, role) {
				return []int{i}, nil
			}
		}
		return nil, fmt.Errorf("role %q is not a team member", role)
	}
	var out []int
	for i, member := range t.Members {
		if t.Orchestrated && strings.EqualFold(member.Role, t.Lead) {
			continue
		}
		if binary := normalizedAgentBinary(member.Binary); binary == "claude" || binary == "codex" {
			out = append(out, i)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--workers matched no Claude or Codex members%s", workersHint(t))
	}
	return out, nil
}

func generatedCodexHome() (string, error) {
	if home := strings.TrimSpace(modelGetenv("CODEX_HOME")); home != "" {
		return home, nil
	}
	home, err := modelUserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "", fmt.Errorf("CODEX_HOME or a user home is required to generate a Codex tool profile")
	}
	return filepath.Join(home, ".codex"), nil
}

func generatedCodexProfileName(teamProfile, role string) string {
	profile := sanitizeWorkstreamName(teamProfile)
	if profile == "" {
		profile = team.DefaultProfile
	}
	return "amq-squad-" + profile + "-" + sanitizeWorkstreamName(role)
}

func discoverCodexMCPServers(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "[mcp_servers.") || !strings.HasSuffix(line, "]") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(line, "[mcp_servers."), "]")
		name = strings.Trim(strings.TrimSpace(name), "\"")
		if name != "" {
			out = append(out, name)
		}
	}
	return dedupeSortedStrings(out)
}

func renderCodexToolProfile(profile string, allow, block []string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# Generated by amq-squad for tool profile %s.\n", profile)
	fmt.Fprintf(&b, "# Effective enabled set: %s\n", strings.Join(allow, ","))
	for _, entry := range block {
		name, ok := strings.CutPrefix(entry, "mcp:")
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "\n[mcp_servers.%q]\nenabled = false\n", name)
	}
	return []byte(b.String())
}

func generatedPolicyFileAction(path string, expected []byte, force bool) (string, error) {
	actual, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "write", nil
	}
	if err != nil {
		return "", err
	}
	if string(actual) == string(expected) {
		return "unchanged", nil
	}
	if !force {
		return "", fmt.Errorf("generated policy %s exists with different content; inspect it and pass --force to replace", path)
	}
	return "write", nil
}

func removeNativePolicyArg(args []string, flagName string, force bool) ([]string, error) {
	var out []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg != flagName && !strings.HasPrefix(arg, flagName+"=") {
			out = append(out, arg)
			continue
		}
		if !force {
			return nil, fmt.Errorf("native args already carry %s; pass --force to replace it with first-class tool policy", flagName)
		}
		if arg == flagName && i+1 < len(args) {
			i++
		}
	}
	return out, nil
}

func dedupeSortedStrings(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

// overlayTargets resolves --role/--workers to member indexes. --workers means
// every claude-binary member except the orchestration lead; codex members are
// skipped with a notice (their native overlay is a codex --profile).
func overlayTargets(t team.Team, role string, workers bool) ([]int, error) {
	if role != "" {
		for i, m := range t.Members {
			if !strings.EqualFold(m.Role, role) {
				continue
			}
			if normalizedAgentBinary(m.Binary) != "claude" {
				return nil, fmt.Errorf("member %q runs %s, not claude; Codex's overlay equivalent is a config profile: create $CODEX_HOME/<name>.config.toml and wire codex_args: [\"--profile\", \"<name>\"]", role, m.Binary)
			}
			return []int{i}, nil
		}
		return nil, fmt.Errorf("role %q is not a team member", role)
	}
	var out []int
	for i, m := range t.Members {
		if t.Orchestrated && strings.EqualFold(m.Role, t.Lead) {
			continue
		}
		if normalizedAgentBinary(m.Binary) != "claude" {
			quietNotice("skipping %s (binary %s): use a codex --profile instead\n", m.Role, m.Binary)
			continue
		}
		out = append(out, i)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--workers matched no claude members%s", workersHint(t))
	}
	return out, nil
}

func workersHint(t team.Team) string {
	if t.Orchestrated {
		return " (the lead is excluded by design)"
	}
	return ""
}

// overlayRelPath computes the --settings value: the overlay path relative to
// the member's launch cwd (the emitted command does `cd <cwd> && ...`, and
// Claude resolves a relative --settings against its cwd). Absolute paths are
// avoided so team.json stays machine-portable.
func overlayRelPath(memberCWD, overlayAbs string) (string, error) {
	rel, err := filepath.Rel(memberCWD, overlayAbs)
	if err != nil {
		return "", fmt.Errorf("compute --settings path relative to member cwd %s: %w", memberCWD, err)
	}
	return rel, nil
}

// wireSettingsArg returns the member's claude_args with ["--settings", rel]
// wired in. Idempotent when the args already point at rel; a foreign
// --settings value is replaced only under --force.
func wireSettingsArg(current []string, rel string, force bool) (action string, out []string, err error) {
	for i, a := range current {
		isPair := a == "--settings" && i+1 < len(current)
		isInline := strings.HasPrefix(a, "--settings=")
		if !isPair && !isInline {
			continue
		}
		existing := ""
		if isPair {
			existing = current[i+1]
		} else {
			existing = strings.TrimPrefix(a, "--settings=")
		}
		if existing == rel {
			return "unchanged", append([]string(nil), current...), nil
		}
		if !force {
			return "", nil, fmt.Errorf("claude_args already carries --settings %s; pass --force to replace it", existing)
		}
		out = append([]string(nil), current...)
		if isPair {
			out[i+1] = rel
		} else {
			out[i] = "--settings=" + rel
		}
		return "replaced", out, nil
	}
	out = append(append([]string(nil), current...), "--settings", rel)
	return "wired", out, nil
}

// memberSettingsRefs extracts the file paths referenced by --settings entries
// in a member's claude_args. Inline JSON values (Claude accepts file-or-json)
// are skipped: only path-shaped values are returned.
func memberSettingsRefs(args []string) []string {
	var refs []string
	for i, a := range args {
		val := ""
		switch {
		case a == "--settings" && i+1 < len(args):
			val = args[i+1]
		case strings.HasPrefix(a, "--settings="):
			val = strings.TrimPrefix(a, "--settings=")
		default:
			continue
		}
		if val == "" || strings.HasPrefix(strings.TrimSpace(val), "{") {
			continue
		}
		refs = append(refs, val)
	}
	return refs
}

// validateMemberOverlayPaths fails the plan when a member's claude_args
// reference a --settings file that does not exist (resolved against the
// member's launch cwd). Catching it at plan time keeps the failure out of the
// pane: Claude would otherwise error at startup inside tmux.
func validateMemberOverlayPaths(t team.Team, members []team.Member) error {
	for _, m := range members {
		effectiveArgs := append(m.ToolArgs(), m.ClaudeArgs...)
		// A bare trailing "--settings" (hand-edit damage) would make Claude
		// read the NEXT token as the settings value at launch — silent
		// corruption inside a pane, exactly what this validator exists to
		// stop. Reject it here by name.
		if n := len(effectiveArgs); n > 0 && effectiveArgs[n-1] == "--settings" {
			return fmt.Errorf("member %s: claude_args ends with a dangling --settings (no value); fix team.json or re-run 'amq-squad team overlay init --role %s --force'", m.Role, m.Role)
		}
		cwd := m.EffectiveCWD(t.Project)
		for _, ref := range memberSettingsRefs(effectiveArgs) {
			abs := ref
			if !filepath.IsAbs(abs) {
				abs = filepath.Join(cwd, ref)
			}
			if _, err := os.Stat(abs); err != nil {
				return fmt.Errorf("member %s: claude_args --settings file not found: %s (run 'amq-squad team overlay init --role %s' or fix the path)", m.Role, abs, m.Role)
			}
		}
		if strings.EqualFold(strings.TrimSpace(m.Binary), "claude") && strings.TrimSpace(m.ToolMCPConfig) != "" {
			abs := m.ToolMCPConfig
			if !filepath.IsAbs(abs) {
				abs = filepath.Join(cwd, abs)
			}
			if _, err := os.Stat(abs); err != nil {
				return fmt.Errorf("member %s: Claude strict MCP config not found: %s (regenerate the role policy before launch)", m.Role, abs)
			}
		}
		if strings.EqualFold(strings.TrimSpace(m.Binary), "codex") && strings.TrimSpace(m.ToolConfig) != "" {
			paths := codexConfigPaths(m.ToolArgs())
			if len(paths) < 2 {
				return fmt.Errorf("member %s: cannot resolve Codex tool profile %q; set CODEX_HOME or install the profile", m.Role, m.ToolConfig)
			}
			if _, err := os.Stat(paths[0]); err != nil {
				return fmt.Errorf("member %s: Codex tool profile not found: %s (generate the role profile before launch)", m.Role, paths[0])
			}
		}
		if err := validateMemberToolPolicyDrift(t, m); err != nil {
			return err
		}
	}
	return nil
}

func validateMemberToolPolicyDrift(t team.Team, m team.Member) error {
	if m.ToolPolicySource() != "member_generated_profile" {
		return nil
	}
	var discovered, sources []string
	switch normalizedAgentBinary(m.Binary) {
	case "claude":
		home, err := modelUserHomeDir()
		if err != nil {
			return fmt.Errorf("member %s: inspect Claude tool policy: %w", m.Role, err)
		}
		claudeDir := strings.TrimSpace(modelGetenv("CLAUDE_CONFIG_DIR"))
		if claudeDir == "" {
			claudeDir = filepath.Join(home, ".claude")
		}
		var defs map[string]json.RawMessage
		discovered, sources, defs, err = discoverClaudeCapabilities([]string{
			filepath.Join(claudeDir, "settings.json"), filepath.Join(home, ".claude.json"),
			filepath.Join(t.Project, ".claude", "settings.json"), filepath.Join(t.Project, ".claude", "settings.local.json"), filepath.Join(t.Project, ".mcp.json"),
		})
		if err != nil {
			return fmt.Errorf("member %s: tool policy drift/not-ready: %w", m.Role, err)
		}
		overlay := claudeSettingsOverlay{EnabledPlugins: map[string]bool{}, DisableAllHooks: m.ToolDisableAllHooks}
		for _, entry := range m.ToolAllowlist {
			if id, ok := strings.CutPrefix(entry, "plugin:"); ok {
				overlay.EnabledPlugins[id] = true
			}
		}
		for _, entry := range m.ToolBlocklist {
			if id, ok := strings.CutPrefix(entry, "plugin:"); ok {
				overlay.EnabledPlugins[id] = false
			}
		}
		expectedSettings, marshalErr := json.MarshalIndent(overlay, "", "  ")
		if marshalErr != nil {
			return marshalErr
		}
		expectedSettings = append(expectedSettings, '\n')
		for label, ref := range map[string]string{"settings": m.ToolConfig, "strict MCP": m.ToolMCPConfig} {
			path := ref
			if !filepath.IsAbs(path) {
				path = filepath.Join(m.EffectiveCWD(t.Project), path)
			}
			actual, readErr := os.ReadFile(path)
			if readErr != nil {
				return fmt.Errorf("member %s: tool policy drift/not-ready: read Claude %s materialization: %w", m.Role, label, readErr)
			}
			expected := expectedSettings
			if label == "strict MCP" {
				expected, marshalErr = renderClaudeStrictMCP(m.ToolAllowlist, defs)
				if marshalErr != nil {
					return marshalErr
				}
			}
			if string(actual) != string(expected) {
				return fmt.Errorf("member %s: tool policy drift/not-ready: Claude %s materialization differs from audited effective policy", m.Role, label)
			}
		}
	case "codex":
		home, err := generatedCodexHome()
		if err != nil {
			return fmt.Errorf("member %s: %w", m.Role, err)
		}
		for _, path := range []string{filepath.Join(home, "config.toml"), filepath.Join(t.Project, ".codex", "config.toml")} {
			entries, present, discoverErr := discoverCodexCapabilities(path)
			if discoverErr != nil {
				return fmt.Errorf("member %s: tool policy drift/not-ready: %w", m.Role, discoverErr)
			}
			discovered = append(discovered, entries...)
			if present {
				sources = append(sources, path)
			}
		}
		paths := codexConfigPaths(m.ToolArgs())
		if len(paths) == 0 {
			return fmt.Errorf("member %s: tool policy drift/not-ready: selected Codex profile path is unresolved", m.Role)
		}
		actual, err := os.ReadFile(paths[0])
		if err != nil {
			return fmt.Errorf("member %s: tool policy drift/not-ready: %w", m.Role, err)
		}
		expected := renderCodexToolProfile(m.EffectiveToolProfile(), m.ToolAllowlist, m.ToolBlocklist)
		if string(actual) != string(expected) {
			return fmt.Errorf("member %s: tool policy drift/not-ready: selected Codex profile content differs from audited effective policy", m.Role)
		}
	}
	covered := map[string]bool{}
	for _, entry := range append(append([]string{}, m.ToolAllowlist...), m.ToolBlocklist...) {
		covered[entry] = true
	}
	for _, entry := range dedupeSortedStrings(discovered) {
		if !covered[entry] {
			return fmt.Errorf("member %s: tool policy drift/not-ready: inherited capability %q is neither enabled nor revoked; regenerate policy", m.Role, entry)
		}
	}
	if !reflect.DeepEqual(dedupeSortedStrings(sources), dedupeSortedStrings(m.ToolPolicySources)) {
		return fmt.Errorf("member %s: tool policy drift/not-ready: capability source set changed from %v to %v; regenerate policy", m.Role, m.ToolPolicySources, sources)
	}
	return nil
}

// splitCommaList splits a comma-separated flag value, trimming blanks.
func splitCommaList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}
