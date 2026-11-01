//go:build !unix

package cli

import "os/exec"

// guardGroup: off unix the guard child is killed by its process handle.
func guardGroup(*exec.Cmd) {}

// guardGroupAlive: off unix no group outlives the child's reap here.
func guardGroupAlive(*exec.Cmd) bool { return false }

func killGuardGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
