//go:build !(darwin || freebsd || netbsd || openbsd || dragonfly || linux)

package cli

// isTerminal: the built-in pi runs on unix hosts only, so a bare invocation
// elsewhere always prints the help.
func isTerminal(int) bool { return false }
