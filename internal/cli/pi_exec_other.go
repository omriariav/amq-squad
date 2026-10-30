//go:build !unix

package cli

import "errors"

// runProcess: the built-in pi runs on unix hosts only (the payload is
// darwin-arm64; plan 8 names the Mac as the supported host).
func runProcess(string, []string, []string) (int, error) {
	return -1, errors.New("the built-in pi runs on unix hosts only")
}
