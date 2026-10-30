//go:build !unix

package cli

import "os/exec"

// guardGroup: off unix the guard child is killed by its process handle.
func guardGroup(*exec.Cmd) {}

func killGuardGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
