// Package amqexec contains subprocess policy shared by every production AMQ
// invocation in amq-squad.
package amqexec

import (
	"os"
	"strings"
)

const noUpdateCheck = "AMQ_NO_UPDATE_CHECK"

// NoUpdateCheckEnv returns a fresh child environment containing exactly one
// AMQ_NO_UPDATE_CHECK=1 entry. A nil env preserves exec.Cmd's normal inherited
// environment semantics; a non-nil empty env remains otherwise empty.
//
// The input and the process environment are never mutated.
func NoUpdateCheckEnv(env []string) []string {
	if env == nil {
		env = os.Environ()
	}
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		key := entry
		if before, _, ok := strings.Cut(entry, "="); ok {
			key = before
		}
		if key != noUpdateCheck {
			out = append(out, entry)
		}
	}
	return append(out, noUpdateCheck+"=1")
}
