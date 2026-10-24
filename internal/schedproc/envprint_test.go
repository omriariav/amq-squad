package schedproc

import (
	"fmt"
	"strings"
	"testing"
)

// oneAMQMe returns the one AM_ME entry of an environment, or an error that
// names only the AM_ME entries: a failure never prints the environment,
// which carries the caller's credentials (the 2b-2 B credential exposure of
// commit 3, where a test failure printed the whole environment into a
// committed evidence log).
func oneAMQMe(env []string) (string, error) {
	me := ""
	for _, kv := range env {
		if strings.HasPrefix(kv, "AM_ME=") {
			if me != "" {
				return "", fmt.Errorf("two AM_ME entries: %q and %q", me, kv)
			}
			me = kv
		}
	}
	return me, nil
}

// A failing environment check names the AM_ME entries only, never another
// variable or its value.
func TestEnvironmentCheckNeverPrintsTheEnvironment(t *testing.T) {
	_, err := oneAMQMe([]string{"AM_ME=a", "GEMINI_API_KEY=canary-secret-2b2b", "AM_ME=b"})
	if err == nil || strings.Contains(err.Error(), "canary-secret-2b2b") || strings.Contains(err.Error(), "GEMINI_API_KEY") {
		t.Fatalf("the failure prints the environment: %v", err)
	}
}
