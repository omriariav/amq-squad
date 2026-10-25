package seatexec

import (
	"slices"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
)

// Claude seats: the hook and the permission mode are one unit (plan 4.3,
// R36; lead and reviewer rulings 2026-10-24). Every Claude seat runs with its
// own CLAUDE_CONFIG_DIR, written fresh by the core for the launch
// (internal/claudeseat): the hooks plugin at user scope, settings.json with
// the launch's permission mode and no allow rule, nothing else. seat-exec
// refuses claude_permission_mismatch when the argv does not carry exactly
// the recorded mode or the dir is missing, not 0700, not ours, or not what
// the launch wrote (the digest of its managed files), claude_hooks_missing
// when its plugin does not run the guard on PreToolUse, and
// claude_allow_rules_present when the worktree's .claude/settings.json or
// settings.local.json (which Claude still merges) carry allow rules. The
// owner's user-scope settings never reach a seat.

// Reason codes of the Claude preflight.
const (
	ClaudeHooksMissing       = claudeseat.HooksMissing
	ClaudePermissionMismatch = claudeseat.PermissionMismatch
	ClaudeAllowRulesPresent  = claudeseat.AllowRulesPresent
)

// dropVar removes every entry of one variable from an environment.
func dropVar(env []string, name string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if n, _, _ := strings.Cut(kv, "="); n != name {
			out = append(out, kv)
		}
	}
	return out
}

// CheckClaudeHooks: the seat's hooks plugin is enabled in the user scope of
// its own config dir, so an argv that drops that scope or switches hooks off
// would pass every config-dir check and run without the guard (phase 5
// re-pass P1-2). Refused, as claude_hooks_missing, before the proof: a
// --setting-sources list without user, in either form (--setting-sources X,
// --setting-sources=X), and the flags that skip hooks, --bare and
// --safe-mode. (Their variables, CLAUDE_CODE_SIMPLE and CLAUDE_CODE_SAFE_MODE,
// are host-session names the scrub drops.)
func CheckClaudeHooks(argv []string) error {
	for i, a := range argv {
		var sources string
		switch {
		case a == "--bare" || strings.HasPrefix(a, "--bare=") || a == "--safe-mode" || strings.HasPrefix(a, "--safe-mode="):
			return refusef(ClaudeHooksMissing, "Claude argv carries %s, which skips the seat's hooks", a)
		case a == "--setting-sources":
			if i+1 < len(argv) {
				sources = argv[i+1]
			}
		case strings.HasPrefix(a, "--setting-sources="):
			sources = strings.TrimPrefix(a, "--setting-sources=")
		default:
			continue
		}
		if !slices.Contains(strings.Split(strings.ReplaceAll(sources, " ", ""), ","), "user") {
			return refusef(ClaudeHooksMissing, "Claude argv carries --setting-sources %q without user; the seat's hooks plugin is enabled in its user settings", sources)
		}
	}
	return nil
}

// CheckClaudePermission: the argv carries exactly the permission mode the
// launch recorded, once, and no permission bypass.
func CheckClaudePermission(recorded string, argv []string) error {
	if recorded == "" {
		return refusef(ClaudePermissionMismatch, "the launch record names no Claude permission mode; the launch sets it with the hook as one unit")
	}
	var modes []string
	for i, a := range argv {
		switch {
		case a == "--dangerously-skip-permissions" || a == "--allow-dangerously-skip-permissions":
			return refusef(ClaudePermissionMismatch, "Claude argv carries %s", a)
		case a == "--settings" || strings.HasPrefix(a, "--settings="):
			return refusef(ClaudePermissionMismatch, "Claude argv carries %s; the seat's settings come only from its own config dir", a)
		case a == "--permission-mode" && i+1 < len(argv):
			modes = append(modes, argv[i+1])
		case strings.HasPrefix(a, "--permission-mode="):
			modes = append(modes, strings.TrimPrefix(a, "--permission-mode="))
		}
	}
	if len(modes) != 1 || modes[0] != recorded {
		return refusef(ClaudePermissionMismatch, "Claude argv permission mode %v; the launch record says %q", modes, recorded)
	}
	return nil
}
