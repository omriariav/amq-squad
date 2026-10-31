// Package seatexec is `amq-squad seat-exec`, the wrapper AMQ runs in front of
// every Claude, Codex and pi seat (plan 4.3, R4, R16, R52). It takes only
// --seat; AM_ME and AM_ROOT come from the environment AMQ sets.
package seatexec

import (
	"context"
	"errors"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Options carries the inputs of one seat-exec run; zero fields take the
// process defaults (tests replace them).
type Options struct {
	Seat     string
	Argv     []string // the provider argv after --seat
	Env      []string
	Home     string
	UID      int
	Now      func() time.Time
	LookPath func(string) (string, error)
	RunAMQ   func(ctx context.Context, amq string, args, env []string, dir string) ([]byte, error)
	Exec     func(path string, argv, env []string) error
	// StartToken reads a process start stamp (procinfo.StartToken).
	StartToken func(pid int) (string, bool)
	// SendProof sends the seat's proof to the launcher (default: the Unix
	// socket named in the staged ticket).
	SendProof func(t launch.ProofTicket, msg launch.ProofMessage) (launch.ProofReply, error)
	// Self is the absolute path of this amq-squad binary (AMQ_SQUAD_BIN in the
	// seat environment); default os.Executable with symlinks resolved.
	Self string
	// MutateEnv is a test seam applied to the final environment before the
	// identity proof (the S6 finding A case removes or changes AM_SESSION here).
	MutateEnv func([]string) []string
	// BeforeRecheck is a test seam run after the identity proof and before
	// the last re-hash of role and rules.
	BeforeRecheck func()
}

func (o Options) withDefaults() Options {
	if o.Env == nil {
		o.Env = os.Environ()
	}
	if o.Home == "" {
		o.Home, _ = os.UserHomeDir()
	}
	if o.UID == 0 && os.Getuid() != 0 {
		o.UID = os.Getuid()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.LookPath == nil {
		o.LookPath = exec.LookPath
	}
	if o.RunAMQ == nil {
		o.RunAMQ = runAMQ
	}
	if o.Exec == nil {
		o.Exec = execProvider
	}
	if o.StartToken == nil {
		o.StartToken = procinfo.StartToken
	}
	if o.SendProof == nil {
		o.SendProof = sendProofDefault(o.UID)
	}
	if o.Self == "" {
		if self, err := os.Executable(); err == nil {
			if real, err := filepath.EvalSymlinks(self); err == nil {
				self = real
			}
			o.Self = self
		}
	}
	return o
}

var handlePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// proofWaitDelay bounds how long the identity proof's output may stay open
// after amq exits or its context ends (phase 5 re-pass P2-5): a descendant of
// `amq env` that holds the pipes never holds seat-exec past it.
const proofWaitDelay = 5 * time.Second

// runAMQ runs amq in its own process group; when the context ends or the
// output outlives the process by proofWaitDelay, the whole group is killed
// (the caller reads that as identity_proof_timeout).
func runAMQ(ctx context.Context, amq string, args, env []string, dir string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, amq, args...)
	cmd.Env, cmd.Dir = env, dir
	cmd.WaitDelay = proofWaitDelay
	ownGroup(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil && (ctx.Err() != nil || errors.Is(err, exec.ErrWaitDelay)) {
		killGroup(cmd)
	}
	return out, err
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func trimOut(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 400 {
		s = s[:400] + "..."
	}
	return s
}
