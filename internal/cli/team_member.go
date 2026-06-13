package cli

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/omriariav/amq-squad/internal/flock"
	"github.com/omriariav/amq-squad/internal/team"
)

// runTeamMember dispatches `amq-squad team member <add|rm>`: runtime roster
// mutation. This is the durable-roster primitive the goal-first composition
// model rests on — a lead (any binary) grows or shrinks its team mid-session,
// and the change persists to team.json so resume rebuilds the team it built.
func runTeamMember(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, `amq-squad team member - add or remove a roster member at runtime

Usage:
  amq-squad team member add <role> --binary <claude|codex> [--handle H]
      [--session S] [--model M] [--claude-args "…"] [--codex-args "…"]
      [--project DIR] [--profile NAME]
  amq-squad team member rm <role> [--project DIR] [--profile NAME]

Mutates the persisted team profile (team.json) atomically and under an
exclusive lock, then re-validates it (orchestration constraints included).
The new member is NOT launched; the printed 'agent up' command starts it.

Examples:
  amq-squad team member add researcher --binary codex
  amq-squad team member add qa --binary claude --model sonnet
  amq-squad team member rm researcher
`)
		if len(args) == 0 {
			return usageErrorf("member requires a subcommand ('add' or 'rm')")
		}
		return nil
	}
	switch args[0] {
	case "add":
		return runTeamMemberAdd(args[1:])
	case "rm", "remove":
		return runTeamMemberRemove(args[1:])
	default:
		return usageErrorf("unknown 'team member' subcommand: %q. Try 'add' or 'rm'.", args[0])
	}
}

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

func runTeamMemberAdd(args []string) error {
	role, rest, ok := peelPositional(args)
	if !ok {
		return usageErrorf("a role is required, e.g. 'team member add researcher --binary codex'")
	}
	fs := flag.NewFlagSet("team member add", flag.ContinueOnError)
	binaryFlag := fs.String("binary", "", "agent CLI for this member: claude or codex (required)")
	handleFlag := fs.String("handle", "", "AMQ handle (defaults to the role)")
	sessionFlag := fs.String("session", "", "AMQ workstream session (defaults to the team's existing session)")
	modelFlag := fs.String("model", "", "native model name passed to the binary")
	claudeArgsRaw := fs.String("claude-args", "", "extra Claude args for this member")
	codexArgsRaw := fs.String("codex-args", "", "extra Codex args for this member")
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to mutate (default: default profile)")
	if err := parseFlags(fs, rest); err != nil {
		return err
	}

	bin := normalizedAgentBinary(*binaryFlag)
	if bin != "claude" && bin != "codex" {
		return usageErrorf("--binary is required and must be claude or codex (got %q)", *binaryFlag)
	}
	// Normalize the role at the boundary so dedup and validation match the
	// stored (always-lowercase) roles, and so 'Researcher' is accepted rather
	// than failing the slug check with a confusing error.
	role = strings.ToLower(strings.TrimSpace(role))
	if err := team.ValidateRoleID(role); err != nil {
		return fmt.Errorf("role: %w", err)
	}

	var err error
	var claudeArgs, codexArgs []string
	if strings.TrimSpace(*claudeArgsRaw) != "" {
		if claudeArgs, err = parseAgentArgs(*claudeArgsRaw); err != nil {
			return fmt.Errorf("parse --claude-args: %w", err)
		}
	}
	if strings.TrimSpace(*codexArgsRaw) != "" {
		if codexArgs, err = parseAgentArgs(*codexArgsRaw); err != nil {
			return fmt.Errorf("parse --codex-args: %w", err)
		}
	}
	// Per-member args are bound to the member's binary (matching the team.json
	// binary-match rule). Reject a mismatch rather than silently dropping it.
	if bin == "codex" && len(claudeArgs) > 0 {
		return usageErrorf("--claude-args applies only to claude members")
	}
	if bin == "claude" && len(codexArgs) > 0 {
		return usageErrorf("--codex-args applies only to codex members")
	}

	projectDir, profile, err := resolveExistingTeamProfile(*projectFlag, *profileFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}

	var added team.Member
	if err := withProfileLock(projectDir, profile, func() error {
		t, err := team.ReadProfile(projectDir, profile)
		if err != nil {
			return fmt.Errorf("read team: %w", err)
		}
		for _, m := range t.Members {
			if m.Role == role {
				return fmt.Errorf("role %q is already on the team", role)
			}
		}
		handle := strings.ToLower(strings.TrimSpace(*handleFlag))
		if handle == "" {
			handle = role
		}
		for _, m := range t.Members {
			if m.Handle == handle {
				return fmt.Errorf("handle %q is already in use; pass a distinct --handle", handle)
			}
		}
		session := strings.ToLower(strings.TrimSpace(*sessionFlag))
		if session == "" {
			session = inheritedSession(t)
		}
		added = team.Member{
			Role:    role,
			Binary:  bin,
			Handle:  handle,
			Session: session,
			Model:   strings.TrimSpace(*modelFlag),
		}
		if bin == "claude" {
			added.ClaudeArgs = claudeArgs
		} else {
			added.CodexArgs = codexArgs
		}
		t.Members = append(t.Members, added)
		// WriteProfile re-validates the whole team (orchestration, per-member
		// binary-match, duplicate handles) before the atomic rename, so an
		// invalid add never persists.
		if err := team.WriteProfile(projectDir, profile, t); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}

	fmt.Printf("added %s (%s) to the team.\n", added.Role, added.Binary)
	fmt.Printf("start it with:\n  %s\n", agentUpHint(added))
	return nil
}

func runTeamMemberRemove(args []string) error {
	role, rest, ok := peelPositional(args)
	if !ok {
		return usageErrorf("a role is required, e.g. 'team member add researcher --binary codex'")
	}
	role = strings.ToLower(strings.TrimSpace(role))
	fs := flag.NewFlagSet("team member rm", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to mutate (default: default profile)")
	if err := parseFlags(fs, rest); err != nil {
		return err
	}
	projectDir, profile, err := resolveExistingTeamProfile(*projectFlag, *profileFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}

	var removed bool
	if err := withProfileLock(projectDir, profile, func() error {
		t, err := team.ReadProfile(projectDir, profile)
		if err != nil {
			return fmt.Errorf("read team: %w", err)
		}
		// Removing the lead of an orchestrated team would leave a dangling
		// lead reference that fails validation; refuse with a clear pointer.
		if t.Orchestrated && t.Lead == role {
			return fmt.Errorf("role %q is the orchestration lead; reassign the lead before removing it", role)
		}
		kept := t.Members[:0:0]
		for _, m := range t.Members {
			if m.Role == role {
				removed = true
				continue
			}
			kept = append(kept, m)
		}
		if !removed {
			return fmt.Errorf("role %q is not a team member", role)
		}
		t.Members = kept
		if err := team.WriteProfile(projectDir, profile, t); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}

	fmt.Printf("removed %s from the team.\n", role)
	fmt.Printf("if it is live, stop it with:\n  amq-squad stop --role %s\n", role)
	return nil
}

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

// withProfileLock serializes a read-modify-write of a team profile across
// concurrent amq-squad processes via an exclusive lock on a sidecar file, so
// a lead and a worker mutating the roster at once cannot lose an update.
func withProfileLock(projectDir, profile string, fn func() error) error {
	return flock.WithLock(team.ProfilePath(projectDir, profile)+".lock", fn)
}

// inheritedSession returns the workstream a new member should join: the
// session shared by existing members (so the roster stays one workstream),
// or empty when the team has no members or they disagree (resolved at launch).
func inheritedSession(t team.Team) string {
	session := ""
	for _, m := range t.Members {
		s := strings.TrimSpace(m.Session)
		if s == "" {
			continue
		}
		if session == "" {
			session = s
		} else if session != s {
			return ""
		}
	}
	return session
}

// agentUpHint builds the exact `agent up` command that launches a member with
// its full roster config (binary, role, session, model, per-member args), so
// the printed hint is copy-paste faithful until `--up` (a later slice) lands.
func agentUpHint(m team.Member) string {
	var b strings.Builder
	fmt.Fprintf(&b, "amq-squad agent up %s --role %s", m.Binary, m.Role)
	if s := strings.TrimSpace(m.Session); s != "" {
		fmt.Fprintf(&b, " --session %s", s)
	}
	if model := strings.TrimSpace(m.Model); model != "" {
		fmt.Fprintf(&b, " --model %s", model)
	}
	fmt.Fprintf(&b, " --me %s", m.Handle)
	if len(m.ClaudeArgs) > 0 {
		fmt.Fprintf(&b, " --claude-args %q", strings.Join(m.ClaudeArgs, " "))
	}
	if len(m.CodexArgs) > 0 {
		fmt.Fprintf(&b, " --codex-args %q", strings.Join(m.CodexArgs, " "))
	}
	return b.String()
}
