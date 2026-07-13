package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
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

func runTeamOverlay(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, `amq-squad team overlay - per-member Claude settings overlays

Usage:
  amq-squad team overlay init (--role ROLE | --workers) [--profile NAME]
      [--project DIR] [--disable-plugins id@market,...] [--disable-all-hooks]
      [--force] [--dry-run]

Generates .amq-squad/overlays/<role>.claude.json and wires the member's
claude_args (["--settings", <path relative to the member cwd>]) in team.json,
so that member launches with a trimmed plugin/hook surface while the rest of
the squad keeps the full project configuration. The flagship use: a same-cwd
orchestrated squad where only the lead needs every plugin and hook.

Codex members are not covered by this generator: Codex's native equivalent is
a config profile. Create $CODEX_HOME/<name>.config.toml (e.g. with
[plugins."x@y"] enabled = false) and wire codex_args: ["--profile", "<name>"].

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
	force := fs.Bool("force", false, "overwrite an existing overlay file / replace a foreign --settings entry")
	dryRun := fs.Bool("dry-run", false, "print the plan without writing the overlay or team.json")
	fs.Usage = func() { _ = runTeamOverlay(nil) }
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if (*roleFlag == "") == !*workers {
		return usageErrorf("pass exactly one of --role ROLE or --workers")
	}

	profile, err := resolveProfileFlag(*profileFlag)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	projectDir, err := resolveProjectDirFlag(cwd, *projectFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
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
		// A bare trailing "--settings" (hand-edit damage) would make Claude
		// read the NEXT token as the settings value at launch — silent
		// corruption inside a pane, exactly what this validator exists to
		// stop. Reject it here by name.
		if n := len(m.ClaudeArgs); n > 0 && m.ClaudeArgs[n-1] == "--settings" {
			return fmt.Errorf("member %s: claude_args ends with a dangling --settings (no value); fix team.json or re-run 'amq-squad team overlay init --role %s --force'", m.Role, m.Role)
		}
		cwd := m.EffectiveCWD(t.Project)
		for _, ref := range memberSettingsRefs(m.ClaudeArgs) {
			abs := ref
			if !filepath.IsAbs(abs) {
				abs = filepath.Join(cwd, ref)
			}
			if _, err := os.Stat(abs); err != nil {
				return fmt.Errorf("member %s: claude_args --settings file not found: %s (run 'amq-squad team overlay init --role %s' or fix the path)", m.Role, abs, m.Role)
			}
		}
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
