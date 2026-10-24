package seatexec

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Refusal is a seat-exec refusal: a stable reason code and a detail line.
type Refusal struct {
	Code   string
	Detail string
}

func (r *Refusal) Error() string { return fmt.Sprintf("seat-exec refused (%s): %s", r.Code, r.Detail) }

// IdentityIncomplete is the refusal of a launch record that lacks one of
// the four AM_* values. amq 0.107.0 accepts a drain (and an env check) with
// AM_SESSION unset, so the identity proof alone does not catch S6 finding A;
// this refusal
// does, and the verb exits with its own code (ExitIdentityIncomplete in cli).
const IdentityIncomplete = "identity_incomplete"

func refusef(code, format string, args ...any) error {
	return &Refusal{Code: code, Detail: fmt.Sprintf(format, args...)}
}

// IdentityVars are the AMQ identity variables the launch sets from the
// launch record only (R52).
var IdentityVars = []string{"AM_ME", "AM_ROOT", "AM_BASE_ROOT", "AM_SESSION"}

// scrubbedNames are vendor credential names that the suffix rule misses, and
// SSH_AUTH_SOCK (ruling 3: the harness pushes, so seats need no SSH agent).
var scrubbedNames = map[string]bool{
	"AWS_ACCESS_KEY_ID": true, "AWS_SECRET_ACCESS_KEY": true, "AWS_SESSION_TOKEN": true,
	"GOOGLE_APPLICATION_CREDENTIALS": true, "AZURE_OPENAI_KEY": true, "GITHUB_PAT": true,
	"SSH_AUTH_SOCK": true, "SSH_AGENT_PID": true, "DOCKER_AUTH_CONFIG": true, "NPM_CONFIG__AUTH": true,
	// Indirect credential sources (Codex review, fix 1): a variable that
	// points a provider chain at a credential file hands the seat the
	// credential after the key variables are gone. AWS_*_FILE is a rule below.
	"KUBECONFIG": true, "DOCKER_CONFIG": true, "NPM_CONFIG_USERCONFIG": true, "GIT_CONFIG_GLOBAL": true,
	"CLOUDSDK_CONFIG": true, "AZURE_CONFIG_DIR": true, "GH_CONFIG_DIR": true, "NETRC": true,
	// 3-2 custody check: an askpass program answers git's and ssh's credential
	// prompts, so it is a credential source like the files above.
	"GIT_ASKPASS": true, "SSH_ASKPASS": true,
}

// Host agent-session variables (lead ruling at the 2b-2 end checkpoint): the
// launcher's own harness session (a Claude Code or Codex session, or a
// companion plugin) must not hand its identity to a seat. AMQ's tmux server
// inherits the launching process's environment, so these reach the pane
// unless seat-exec drops them. Exact names, then prefixes.
var hostSessionNames = map[string]bool{
	"CLAUDECODE": true, "AI_AGENT": true, "CLAUDE_PID": true, "CLAUDE_EFFORT": true, "CLAUDE_JOB_DIR": true,
	"CLAUDE_PLUGIN_DATA": true, "CLAUDE_PLUGIN_ROOT": true, "CLAUDE_PROJECT_DIR": true, "CLAUDE_ENV_FILE": true,
	"CODEX_THREAD_ID": true, "CODEX_SESSION_ID": true, "CODEX_INTERNAL_ORIGINATOR_OVERRIDE": true,
}

var hostSessionPrefixes = []string{"CLAUDE_CODE_", "CODEX_COMPANION_", "CODEX_SANDBOX"}

// neverNeeded are forge credential carriers no runtime may declare in
// env_needs (3-2 fix 1, P1-5): the GIT_CONFIG_* family (GIT_CONFIG_COUNT,
// GIT_CONFIG_KEY_<n>, GIT_CONFIG_VALUE_<n>, GIT_CONFIG_PARAMETERS can carry an
// http.extraHeader Authorization) and the gh token and config names. They are
// scrubbed even when a runtime declares them, and env_needs or
// credential_needs naming one is refused (env_needs_invalid, ValidateNeeds).
var neverNeeded = map[string]bool{"GH_TOKEN": true, "GITHUB_TOKEN": true, "GH_ENTERPRISE_TOKEN": true, "GITHUB_ENTERPRISE_TOKEN": true, "GH_CONFIG_DIR": true}

// NeverNeeded reports a name that is scrubbed whatever the runtime declares.
func NeverNeeded(name string) bool {
	upper := strings.ToUpper(strings.TrimSpace(name))
	return neverNeeded[upper] || strings.HasPrefix(upper, "GIT_CONFIG_")
}

// Scrubbed reports whether a variable name is removed from a seat's
// environment (plan 4.3, R16): *_API_KEY, *_TOKEN, *_SECRET, names with
// _SECRET_ or _PASSWORD inside, the known vendor names, and the host
// agent-session variables (CLAUDECODE, CLAUDE_CODE_*, CODEX_COMPANION_* and
// the names above).
func Scrubbed(name string) bool {
	upper := strings.ToUpper(name)
	if NeverNeeded(upper) {
		return true
	}
	if hostSession(upper) {
		return true
	}
	switch {
	case scrubbedNames[upper]:
		return true
	case strings.HasSuffix(upper, "_API_KEY"), strings.HasSuffix(upper, "_TOKEN"), strings.HasSuffix(upper, "_SECRET"):
		return true
	case strings.Contains(upper, "_SECRET_"), strings.HasSuffix(upper, "_PASSWORD"):
		return true
	case strings.HasPrefix(upper, "AWS_") && strings.HasSuffix(upper, "_FILE"):
		return true // AWS_SHARED_CREDENTIALS_FILE, AWS_CONFIG_FILE, AWS_WEB_IDENTITY_TOKEN_FILE
	}
	return false
}

// SeatEnv builds the provider environment: the parent environment minus the
// scrubbed names (a runtime keeps the names it declares in needs) and minus
// any inherited AMQ identity, then the overlay (identity and amq-squad
// variables) on top. It returns the environment and the removed names.
func SeatEnv(parent []string, needs, credNeeds []string, overlay map[string]string) ([]string, []string) {
	keep := map[string]bool{}
	for _, n := range credNeeds {
		keep[strings.TrimSpace(n)] = true
	}
	identity := map[string]bool{}
	for _, n := range IdentityVars {
		identity[n] = true
	}
	var out, removed []string
	for _, entry := range parent {
		name, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if _, set := overlay[name]; set || identity[name] {
			continue
		}
		if Scrubbed(name) && (!keep[name] || NeverNeeded(name)) {
			removed = append(removed, name)
			continue
		}
		out = append(out, entry)
	}
	names := make([]string, 0, len(overlay))
	for name := range overlay {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if overlay[name] != "" {
			out = append(out, name+"="+overlay[name])
		}
	}
	sort.Strings(removed)
	return out, removed
}

// Getenv reads one variable from an environment slice.
func Getenv(env []string, name string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(env[i], "="); ok && k == name {
			return v
		}
	}
	return ""
}

var needName = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// ValidateNeeds checks a seat's declared names (fix 3 item 6). Every entry
// is an environment variable name; env_needs may not name a scrubbed-class
// variable (a credential, token, secret, askpass or host-session name); only
// credential_needs, which the registry entry declares, may keep a scrubbed
// credential, and never a host-session variable.
func ValidateNeeds(envNeeds, credNeeds []string) error {
	for _, n := range envNeeds {
		if !needName.MatchString(n) {
			return refusef("env_needs_invalid", "env_needs entry %q is not a variable name", n)
		}
		if Scrubbed(n) {
			return refusef("env_needs_invalid", "env_needs names %s, a scrubbed-class variable; only credential_needs may keep a credential", n)
		}
	}
	for _, n := range credNeeds {
		if !needName.MatchString(n) {
			return refusef("env_needs_invalid", "credential_needs entry %q is not a variable name", n)
		}
		if hostSession(n) {
			return refusef("env_needs_invalid", "credential_needs names %s, a host agent-session variable", n)
		}
		if NeverNeeded(n) {
			return refusef("env_needs_invalid", "credential_needs names %s, a forge credential carrier that never reaches a seat (R33, 3-2)", n)
		}
	}
	return nil
}

func hostSession(name string) bool {
	upper := strings.ToUpper(name)
	for _, p := range hostSessionPrefixes {
		if strings.HasPrefix(upper, p) {
			return true
		}
	}
	return hostSessionNames[upper]
}
