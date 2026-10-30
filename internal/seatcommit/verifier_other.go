//go:build !unix

package seatcommit

import "os/exec"

// Off unix the verifier has no process group: the bound kills the process
// itself (3.0 hosts are macOS and Linux).
func ownGroup(cmd *exec.Cmd) {}

func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
