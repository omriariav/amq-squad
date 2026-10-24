package seatexec

import (
	"fmt"
	"sort"
	"strings"
)

// Refusal is a seat-exec refusal: a stable reason code and a detail line.
type Refusal struct {
	Code   string
	Detail string
}

func (r *Refusal) Error() string { return fmt.Sprintf("seat-exec refused (%s): %s", r.Code, r.Detail) }

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
}

// Scrubbed reports whether a variable name is removed from a seat's
// environment (plan 4.3, R16): *_API_KEY, *_TOKEN, *_SECRET, names with
// _SECRET_ or _PASSWORD inside, and the known vendor names.
func Scrubbed(name string) bool {
	upper := strings.ToUpper(name)
	switch {
	case scrubbedNames[upper]:
		return true
	case strings.HasSuffix(upper, "_API_KEY"), strings.HasSuffix(upper, "_TOKEN"), strings.HasSuffix(upper, "_SECRET"):
		return true
	case strings.Contains(upper, "_SECRET_"), strings.HasSuffix(upper, "_PASSWORD"):
		return true
	}
	return false
}

// SeatEnv builds the provider environment: the parent environment minus the
// scrubbed names (a runtime keeps the names it declares in needs) and minus
// any inherited AMQ identity, then the overlay (identity and amq-squad
// variables) on top. It returns the environment and the removed names.
func SeatEnv(parent []string, needs []string, overlay map[string]string) ([]string, []string) {
	keep := map[string]bool{}
	for _, n := range needs {
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
		if Scrubbed(name) && !keep[name] {
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
