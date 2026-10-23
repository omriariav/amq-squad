package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/team"
)

// v3_holding.go is the temporary holding file for phase 2a-1 extractions
// (plan 4.1 step 3). Every declaration here was moved verbatim from a file
// deleted in 2a-1 because a surviving non-test caller still needs it. 2a-2
// moves each one to its 3.0 home or deletes it; this file's target at the end
// of 2a is zero lines. Each block names its source file and line range on the
// v3 branch at the time of the cut.

// From internal/cli/up.go:404-411 (C11 cut). Not in the manifest extract field;
// callers: dispatch.go, doctor.go, operator.go, status.go, team.go (kept) and
// runtime_actions.go, team_lead.go, team_member.go (cut later).
// profileInitCommand returns the creation command to suggest when reporting a
// missing team profile.
func profileInitCommand(profile string) string {
	if profile == "" || profile == team.DefaultProfile {
		return "amq-squad init"
	}
	return "amq-squad init --profile " + profile
}

// From internal/cli/resume_shared.go:46-63 (C11 cut). Not in the manifest extract
// field; canonicalPath callers: task.go (kept), team_lead.go; compactNativeValue
// callers: effort.go, model_defaults.go (kept).
// canonicalPath keeps launch/resume identity comparisons on the shared pathnorm
// seam, including symlink and on-disk case normalization for existing paths.
func canonicalPath(p string) string {
	return canonicalFilesystemPath(p)
}

func compactNativeValue(arg string) string {
	if strings.HasPrefix(arg, "-c=") {
		return strings.TrimPrefix(arg, "-c=")
	}
	if strings.HasPrefix(arg, "-c") && len(arg) > 2 {
		return strings.TrimPrefix(arg, "-c")
	}
	if _, value, ok := strings.Cut(arg, "="); ok {
		return value
	}
	return ""
}

// From internal/cli/new.go (C4 cut); the manifest extract field names both. Callers:
// launch.go and team.go (kept).

// From new.go:488-524
func peelProjectFlag(args []string) (string, []string, error) {
	out := make([]string, 0, len(args))
	project := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			out = append(out, args[i:]...)
			break
		}
		if a == "--project" || a == "-p" {
			if i+1 >= len(args) {
				return "", nil, usageErrorf("--project requires a directory")
			}
			if project != "" {
				return "", nil, usageErrorf("--project may be passed only once")
			}
			project = strings.TrimSpace(args[i+1])
			if project == "" {
				return "", nil, usageErrorf("--project requires a directory")
			}
			i++
			continue
		}
		if strings.HasPrefix(a, "--project=") || strings.HasPrefix(a, "-p=") {
			if project != "" {
				return "", nil, usageErrorf("--project may be passed only once")
			}
			project = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(a, "--project="), "-p="))
			if project == "" {
				return "", nil, usageErrorf("--project requires a directory")
			}
			continue
		}
		out = append(out, a)
	}
	return project, out, nil
}

// From new.go:596-620
func runInProject(project string, fn func() error) error {
	if strings.TrimSpace(project) == "" {
		return fn()
	}
	dir, err := expandPath(project)
	if err != nil {
		return fmt.Errorf("resolve --project: %w", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("--project %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("--project %s is not a directory", dir)
	}
	old, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	if err := os.Chdir(dir); err != nil {
		return fmt.Errorf("chdir %s: %w", dir, err)
	}
	defer func() { _ = os.Chdir(old) }()
	return fn()
}

// From internal/cli/list.go (C4 cut). Not in the manifest extract field. Callers:
// restore.go (wakeHealthForEntry, host), liveness.go (readPresenceForEntry, kept),
// namespace_migration_plan.go (C7). wakeHealthForEntry still depends on wake_lock_compat.go
// (C1) and runtime_identity.go (C2); revisit at those cuts.

// From list.go:200-210
func readPresenceForEntry(agentDir string) (presenceFile, error) {
	data, err := os.ReadFile(presencePath(agentDir))
	if err != nil {
		return presenceFile{}, err
	}
	var pres presenceFile
	if err := json.Unmarshal(data, &pres); err != nil {
		return presenceFile{}, err
	}
	return pres, nil
}

// From list.go:212-214
func presencePath(agentDir string) string {
	return filepath.Join(agentDir, "presence.json")
}

// From internal/cli/team_member_diff.go:11-17 (C18 cut). Callers: mutation_json.go (keep).
// memberFieldChange is one field-level before/after pair in a `team member
// update --dry-run` preview (#616).
type memberFieldChange struct {
	Field  string `json:"field"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// From internal/cli/team_member.go:173-178 (C18 cut). Callers: task.go (kept).
func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// From internal/cli/team_member.go:188-197 (C18 cut). Callers: task.go (kept); team_member_control_continue.go (C1).
// peelPositional splits a leading positional argument from the remaining flag
// args (Go's flag parser stops at the first non-flag, so a positional that
// precedes flags must be peeled first). ok is false when the first arg is
// missing or is itself a flag; the caller supplies the context-specific error.
func peelPositional(args []string) (val string, rest []string, ok bool) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return "", nil, false
	}
	return args[0], args[1:], true
}

// From internal/cli/team_member.go:928-940 (C18 cut). Callers: role_draft.go (kept).
// resolveExistingTeamProfile resolves the project dir + profile (reusing the
// shared resolver) and requires the profile to already exist — roster
// mutation needs a team to mutate.
func resolveExistingTeamProfile(projectFlag, profileFlag string, projectSet bool) (string, string, error) {
	projectDir, profile, err := resolveProjectProfile(projectFlag, profileFlag, projectSet)
	if err != nil {
		return "", "", err
	}
	if !team.ExistsProfile(projectDir, profile) {
		return "", "", fmt.Errorf("no team configured for profile %q. Run '%s' first.", profile, profileInitCommand(profile))
	}
	return projectDir, profile, nil
}

// From internal/cli/team_member.go:942-947 (C18 cut). Callers: team_operator.go (kept).
// withProfileLock serializes a read-modify-write of a team profile across
// concurrent amq-squad processes via an exclusive lock on a sidecar file, so
// a lead and a worker mutating the roster at once cannot lose an update.
func withProfileLock(projectDir, profile string, fn func() error) error {
	return team.WithProfileLock(projectDir, profile, fn)
}

// From internal/cli/team_member.go:1024-1051 (C18 cut). Callers: team.go (kept).
// memberCWDOverride is the SINGLE resolver for a member's --cwd, shared by
// roster creation (team init), `team member add` and `team member update`.
//
// #538, second review F2: an earlier version of this claimed to be shared but was
// not -- team init kept its own copy -- and it resolved a relative value with
// filepath.Abs, i.e. against the SHELL working directory. So
// `--project /repo --cwd ../wt` run from /tmp recorded /tmp/wt here and
// /repo/../wt on the create path. That is precisely the two-writers/two-origins
// defect #539 and #540 were about, reintroduced in the fix for a different bug.
// Both problems have one fix: genuinely one function, anchored to the PROJECT.
//
// A relative path means "relative to the project", never to the caller's shell.
// A value naming the team-home itself records as no override, so the member stays
// clean rather than pinning the default explicitly.
func memberCWDOverride(projectDir, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	abs := absoluteFilesystemPathIn(projectDir, raw)
	if abs == "" {
		return "", fmt.Errorf("resolve --cwd %q", raw)
	}
	if sameResolvedDir(abs, projectDir) {
		return "", nil
	}
	return abs, nil
}

// From internal/cli/profiles.go:30-42 (C26 cut). Callers: context_resolution.go, team.go (kept).
// resolveProfileFlag normalizes a --profile value: empty or "default" maps
// to the implicit default profile; non-default names are validated against
// the slug rules. Returns the canonical profile name and any error.
func resolveProfileFlag(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == team.DefaultProfile {
		return team.DefaultProfile, nil
	}
	if err := team.ValidateProfileName(name); err != nil {
		return "", fmt.Errorf("--profile: %w", err)
	}
	return name, nil
}
