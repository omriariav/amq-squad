package state

import (
	"path/filepath"
	"strings"
)

// agentProcessMatcher returns a predicate that recognizes the agent binary in a
// ps args string. Ported from internal/cli/preflight.go so this package agrees
// with status/list/down about what counts as "this agent's process". Anchors on
// a token boundary so "claude-mem" does not false-match "claude".
func agentProcessMatcher(binary string) func(args string) bool {
	binary = strings.TrimSpace(binary)
	return func(args string) bool {
		if args == "" || binary == "" {
			return false
		}
		base := filepath.Base(binary)
		fields := strings.Fields(args)
		if len(fields) == 0 {
			return false
		}
		first := filepath.Base(fields[0])
		return first == base || first == binary
	}
}
