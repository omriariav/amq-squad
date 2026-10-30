//go:build !unix

package seatexec

import (
	"errors"
	"os/exec"

	"github.com/omriariav/amq-squad/v3/internal/launch"
)

// Run is unix-only: the anchored traversal needs openat with O_NOFOLLOW and
// the provider is exec'd in place. amq-squad supports the Mac (R45).
func Run(o Options) error {
	return errors.New("seat-exec is supported on unix hosts only")
}

func execProvider(path string, argv, env []string) error {
	return errors.New("seat-exec is supported on unix hosts only")
}

func sendProofDefault(uid int) func(launch.ProofTicket, launch.ProofMessage) (launch.ProofReply, error) {
	return func(launch.ProofTicket, launch.ProofMessage) (launch.ProofReply, error) {
		return launch.ProofReply{}, errors.New("seat-exec is supported on unix hosts only")
	}
}

func ownGroup(cmd *exec.Cmd) {}

func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
