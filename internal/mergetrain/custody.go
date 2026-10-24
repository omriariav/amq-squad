package mergetrain

import (
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/seatexec"
)

// ForgeCredentialNames are the GitHub and release credential variables a
// seat must never carry (R33): gh and the GitHub API, git over HTTPS and SSH,
// and the package registries a release publishes to.
var ForgeCredentialNames = []string{
	"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN", "GITHUB_PAT", "GH_CONFIG_DIR",
	"GIT_ASKPASS", "SSH_ASKPASS", "GIT_CONFIG_GLOBAL", "SSH_AUTH_SOCK", "SSH_AGENT_PID",
	"GORELEASER_GITHUB_TOKEN", "HOMEBREW_GITHUB_API_TOKEN", "NPM_TOKEN", "NODE_AUTH_TOKEN", "NPM_CONFIG__AUTH",
	"CARGO_REGISTRY_TOKEN", "TWINE_PASSWORD", "GEM_HOST_API_KEY", "NETRC",
}

// SeatCredentialLeaks runs the seat-exec scrub (2b-2's SeatEnv, with no
// declared needs) over an owner environment and returns every GitHub or
// release credential name that would reach a seat. Empty is the custody
// claim: the train and the harness push hold the owner's identity, the seat
// process does not (plan 7.3). What the scrub cannot cover is the same-UID
// path to the keychain and the launchd agent (the 3-2 measurement); guard
// denies those reads (credential_read) and the server's protection is the
// enforcement.
func SeatCredentialLeaks(ownerEnv []string) []string {
	env, _ := seatexec.SeatEnv(ownerEnv, nil, nil, nil)
	var leaks []string
	for _, e := range env {
		name, _, _ := strings.Cut(e, "=")
		upper := strings.ToUpper(name)
		for _, c := range ForgeCredentialNames {
			if upper == c {
				leaks = append(leaks, name)
			}
		}
	}
	sort.Strings(leaks)
	return leaks
}
