//go:build !unix

package cli

import "errors"

// execProcess: the built-in pi runs on unix hosts only (the payload is
// darwin-arm64; plan 8 names the Mac as the supported host).
func execProcess(string, []string, []string) error {
	return errors.New("the built-in pi runs on unix hosts only")
}
