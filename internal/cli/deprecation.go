package cli

import (
	"fmt"
	"os"
)

// deprecationWarning emits a single one-line deprecation warning to stderr.
// The warning never goes to stdout, never alters return values, and never
// affects JSON envelopes. Old verbs continue to work through 1.x; this
// helper is the bridge until the verbs are removed in 2.0.
//
// Format: "warning: '<old>' is deprecated and will be removed in 2.0; use '<new>' instead"
//
// Callers should invoke this once at the top of the deprecated handler,
// gated on isHelpInvocation so `--help` paths stay quiet per Step 12 bar.
// --quiet is NOT honored here because deprecation is a warning, not a
// status notice.
func deprecationWarning(old, replacement string) {
	fmt.Fprintf(os.Stderr, "warning: '%s' is deprecated and will be removed in 2.0; use '%s' instead\n", old, replacement)
}

// isHelpInvocation reports whether args carry a top-level help token
// (-h, --help, or the literal subcommand "help") before any "--" child
// boundary. Used by deprecated handlers to suppress the deprecation warning
// on help paths.
func isHelpInvocation(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "-h" || a == "--help" || a == "help" {
			return true
		}
	}
	return false
}

// argsContains reports whether token appears in args before any "--"
// child boundary.
func argsContains(args []string, token string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == token {
			return true
		}
	}
	return false
}
