//go:build !unix

package registry

import "os/exec"

// Off unix the probe only kills the pi process itself (3.0 hosts are macOS).
func ownProcessGroup(cmd *exec.Cmd) {}

type procWatch struct{}

func watchDescendants(cmd *exec.Cmd) *procWatch { return nil }

func endProcessGroup(cmd *exec.Cmd, w *procWatch) error {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	return nil
}
