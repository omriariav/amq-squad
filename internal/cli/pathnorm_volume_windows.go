//go:build windows

package cli

import "strings"

// Windows drive and UNC volume names are case-insensitive but have no parent
// directory whose entry spelling the portable walker can inspect. Fold only
// that volume prefix; directory components are still proven with SameFile.
func canonicalPathVolume(volume string) string {
	return strings.ToLower(volume)
}
