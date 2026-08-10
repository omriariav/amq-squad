package cli

import (
	"fmt"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/drafter"
	"github.com/omriariav/amq-squad/v2/internal/userconfig"
)

type resolveCLIDrafterFunc func(*drafter.Config) (drafter.Resolution, error)

var readCLIUserConfig = userconfig.Read

// resolveCLIDrafter applies the shared profile > global > in-session
// precedence for every CLI prose consumer. Keeping resolution here prevents
// role, brief, and team-rules drafting from acquiring subtly different
// configuration or trust behavior.
func resolveCLIDrafter(profile *drafter.Config) (drafter.Resolution, error) {
	if profile != nil {
		resolved, err := drafter.Resolve(profile, nil)
		if err != nil {
			return drafter.Resolution{}, fmt.Errorf("resolve drafter config: %w", err)
		}
		return resolved, nil
	}
	global, err := readCLIUserConfig()
	if err != nil {
		return drafter.Resolution{}, fmt.Errorf("read user drafter config: %w", err)
	}
	resolved, err := drafter.Resolve(profile, global.Drafter)
	if err != nil {
		return drafter.Resolution{}, fmt.Errorf("resolve drafter config: %w", err)
	}
	return resolved, nil
}

func cloneCLIDrafterEvidence(in drafter.Evidence) drafter.Evidence {
	out := in
	out.Command = append([]string(nil), in.Command...)
	return out
}

func cloneCLIDrafterAttempts(in []drafter.Evidence) []drafter.Evidence {
	out := make([]drafter.Evidence, len(in))
	for i, attempt := range in {
		out[i] = cloneCLIDrafterEvidence(attempt)
	}
	return out
}

func cliDrafterFailureEvidence(attempts []drafter.Evidence, evidence drafter.Evidence) string {
	if len(attempts) == 0 {
		if command := strings.TrimSpace(evidence.CommandDisplay); command != "" {
			return "command: " + command
		}
		return ""
	}
	parts := make([]string, 0, len(attempts))
	for i, attempt := range attempts {
		parts = append(parts, fmt.Sprintf(
			"attempt[%d] backend=%s command=%q fall-through=%q",
			i+1,
			strings.TrimSpace(attempt.Backend),
			strings.TrimSpace(attempt.CommandDisplay),
			strings.TrimSpace(attempt.Failure),
		))
	}
	return "attempts: " + strings.Join(parts, "; ")
}

func cliDrafterAttemptsText(attempts []drafter.Evidence, evidence drafter.Evidence) string {
	if len(attempts) == 0 {
		if command := strings.TrimSpace(evidence.CommandDisplay); command != "" {
			return "Drafter command: " + command + "\n"
		}
		return ""
	}
	var out strings.Builder
	for _, attempt := range attempts {
		fmt.Fprintf(&out, "Drafter attempt (%s): %s\n", attempt.Backend, attempt.CommandDisplay)
		if failure := strings.TrimSpace(attempt.Failure); failure != "" {
			fmt.Fprintf(&out, "Fall-through: %s\n", failure)
		}
	}
	return out.String()
}
