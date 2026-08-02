//go:build !windows

package cli

func canonicalPathVolume(volume string) string {
	return volume
}
