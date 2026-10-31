package seatcommit

import (
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/registry"
)

// FetchCredentials is the commit step's push authentication (D2,
// pushCredentials) for a harness fetch of the grant's push URL, such as the
// launch's fetch of the base tip (3.0.2 t22): for an HTTPS URL, the pinned
// gh the launch recorded (t.GhPath and t.GhSHA256), opened through the
// anchored walk and re-hashed now, as the only credential helper, with gh's
// own token variables from env the one pass-through; nothing is read from a
// config file. An SSH or local URL needs nothing. An HTTPS URL with no gh on
// the record fetches anonymously (brief 31-1: the launch no longer needs the
// gh pin; every credential helper off, the harness environment's
// GIT_TERMINAL_PROMPT=0), reported as anonymous so the caller can name a
// private repository's failure; the commit step's push still requires the
// pin. A pinned gh that fails its hash returns why, starting with the
// registry reason code; the caller refuses.
func FetchCredentials(t Target, env []string) (args, outEnv []string, anonymous bool, why string) {
	if grants.PushKind(t.PushURL) != grants.PushHTTPS {
		return nil, nil, false, ""
	}
	if t.GhPath == "" && t.GhSHA256 == "" {
		return []string{"-c", "credential.helper="}, nil, true, ""
	}
	tool, code, detail := registry.OpenPinned("gh", t.GhPath, t.GhSHA256)
	if tool == nil {
		return nil, nil, false, code + ": " + detail + " (the HTTPS push URL's fetch)"
	}
	gh := tool.Path
	tool.Close()
	args = []string{"-c", "credential.helper=", "-c", "credential.helper=!" + shellQuote(gh) + " auth git-credential"}
	for _, kv := range env {
		if name, _, _ := strings.Cut(kv, "="); name == "GH_TOKEN" || name == "GITHUB_TOKEN" {
			outEnv = append(outEnv, kv)
		}
	}
	return args, outEnv, false, ""
}

// HarnessEnv is the environment of every harness git call, the push's
// (harnessEnv: the caller's environment minus credential names and GIT_*
// redirection except keptGitVars, git.go, plus the R2 set), for a harness
// git outside this package such as the launch's base fetch: one list, so the
// two cannot drift.
func HarnessEnv(parent []string) []string { return harnessEnv(parent) }
