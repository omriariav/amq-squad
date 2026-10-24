package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
	"github.com/omriariav/amq-squad/v3/internal/panechain"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// The owner-only verb seat clear (R1 of the milestone B first pass):
// owner-only and lead-only verbs never take identity from flags or the
// environment. The owner is proved by where the process runs, as guard
// proves the owner for grants: no AM_ME, none of the seat or lead
// environment markers, and the invoking process does not descend from any
// seat pane (internal/panechain: the pane lookup and the stamped parent
// chain of amendment 28's proof). The clear is a record
// signed with the core's issuer key, which the commit step verifies against
// the owner's trust store.

// errNotOwner is every refusal of the owner proof.
var errNotOwner = errors.New("not_owner_terminal")

// Seams for tests: the seat panes, the chain walk, this process.
var (
	seatPanes    = panechain.List
	chainReaches = panechain.Reaches
	selfPID      = os.Getpid
	selfStart    = procinfo.StartToken
)

// seatMarkers are the environment names a seat or the lead carries (seat-exec
// and the pi runtime set them from the launch record).
var seatMarkers = []string{"AM_ME", "AMQ_SQUAD_SEAT", "AMQ_SQUAD_LEAD", "AMQ_SQUAD_ROLE_PATH"}

// requireOwnerTerminal refuses unless this process is the owner's terminal:
// no seat or lead marker, and its parent chain (procinfo.ParentPID with the
// start stamp re-read at every hop) reaches no seat pane's shell. A pane
// listing or a chain that cannot be read is a refusal, never an absence.
func requireOwnerTerminal(verb string) error {
	for _, name := range seatMarkers {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return fmt.Errorf("%s: %w: %s is set; this verb runs only from the owner's own terminal, never inside a seat or from the lead", verb, errNotOwner, name)
		}
	}
	panes, err := seatPanes()
	if err != nil {
		return fmt.Errorf("%s: %w: %v", verb, errNotOwner, err)
	}
	if len(panes) == 0 {
		return nil
	}
	pid := selfPID()
	start, ok := selfStart(pid)
	if !ok {
		return fmt.Errorf("%s: %w: this process's start stamp is unreadable", verb, errNotOwner)
	}
	pane, known := chainReaches(pid, start, panes)
	switch {
	case !known:
		return fmt.Errorf("%s: %w: the process chain cannot be read", verb, errNotOwner)
	case pane != nil:
		return fmt.Errorf("%s: %w: this process descends from seat %s's pane; this verb runs only from the owner's own terminal", verb, errNotOwner, pane.Agent)
	}
	return nil
}

// Seams for tests: the issuer key that signs a clear and the trust store
// that verifies one.
var seatClearIssuerKey = issuerKeyPath

// issuerKeyPath is the binding's issuer key when the owner enrolled (4-5),
// else the default ~/.amq-squad/issuer-key.pem.
func issuerKeyPath() string {
	if b, _, err := gateverify.LoadBinding(gateBindingPath()); err == nil && b.IssuerKey != "" {
		return b.IssuerKey
	}
	return gateverify.DefaultIssuerKeyPath()
}

// clearVerifier is the owner's trust store; nil (no clear counts) when it
// cannot be read.
func clearVerifier() seatcommit.ClearVerifier {
	store, err := operatorauth.LoadAuthorizationTrustStore(gateTrustStorePath())
	if err != nil {
		return nil
	}
	return store
}

// ownerTrust verifies a signed clear against the owner's trust store as it
// stands at each check (the store is read every time).
type ownerTrust struct{}

func (ownerTrust) Verify(keyID string, payload, signature []byte) error {
	v := clearVerifier()
	if v == nil {
		return errors.New("the owner's trust store cannot be read")
	}
	return v.Verify(keyID, payload, signature)
}

// prBoundTo answers which task the store binds a PR number to.
func prBoundTo(s task.Store) func(int) string {
	return func(n int) string {
		list, err := s.List()
		if err != nil {
			return "unknown (the task store is unreadable)"
		}
		for _, t := range list {
			if t.PRNumber == n {
				return t.ID
			}
		}
		return ""
	}
}
