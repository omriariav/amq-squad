//go:build !unix

package seatlaunch

import (
	"errors"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
)

// The seat proof needs a Unix socket; seats launch on unix hosts only (R45).
type proofServer struct{}

func startProofServer(q Squad, launchID string, o Options) (*proofServer, error) {
	return nil, errors.New("the seat proof socket needs a unix host")
}

func (s *proofServer) ticket(handle, requestID string) *launch.ProofTicket { return nil }

func (s *proofServer) state(handle string) (bool, int, string, string, string) {
	return false, 0, "", "", ""
}

func (s *proofServer) close() {}

func (s *proofServer) windowEnd() time.Time { return time.Time{} }

func (s *proofServer) wait() {}
