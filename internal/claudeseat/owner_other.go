//go:build !unix

package claudeseat

import (
	"os"
	"os/exec"
)

func ownerUID(os.FileInfo) (int, bool) { return 0, false }

func probeGroup(*exec.Cmd) {}

func killProbe(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
