package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

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

// errNotOwner is every refusal of the owner proof; errOwnerUnknown is a
// proof that cannot be read (R5: an unreadable tmux server, an unreadable
// chain or terminal), a refusal too, never an absence of panes.
var (
	errNotOwner     = errors.New("not_owner_terminal")
	errOwnerUnknown = errors.New("owner_proof_unknown")
)

// Seams for tests: the seat panes, the chain walk, this process, its
// controlling terminal and a pane tty's device.
var (
	seatPanes    = panechain.List
	chainReaches = panechain.Reaches
	selfPID      = os.Getpid
	selfStart    = procinfo.StartToken
	openOwnerTTY = openControllingTTY
	ttyDevice    = deviceOf
)

// confirmTimeout bounds the wait for the typed confirmation (R9).
var confirmTimeout = 60 * time.Second

// ownerTTY is the terminal the owner confirms on: the real pty of stdin.
type ownerTTY interface {
	io.ReadWriteCloser
	Device() (uint64, error)
}

// seatMarkers are the environment names a seat or the lead carries (seat-exec
// and the pi runtime set them from the launch record).
var seatMarkers = []string{"AM_ME", "AMQ_SQUAD_SEAT", "AMQ_SQUAD_LEAD", "AMQ_SQUAD_ROLE_PATH"}

// requireOwnerTerminal refuses unless this process is the owner's terminal
// (R1, R5): no seat or lead marker; a controlling terminal (/dev/tty) that is
// not any seat pane's pty; a parent chain (procinfo.ParentPID with the start
// stamp re-read at every hop) that reaches no seat pane's shell (a process
// reparented to init has no pane ancestor, so the terminal check is what
// holds it); and the confirmation typed on that terminal: the seat's handle.
// A pane listing, a chain or a terminal that cannot be read refuses
// (owner_proof_unknown).
func requireOwnerTerminal(verb, handle string) error {
	for _, name := range seatMarkers {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return fmt.Errorf("%s: %w: %s is set; this verb runs only from the owner's own terminal, never inside a seat or from the lead", verb, errNotOwner, name)
		}
	}
	tty, err := openOwnerTTY()
	switch {
	case errors.Is(err, errNoPTY):
		return fmt.Errorf("%s: %w: stdin is not a real pty; the owner runs this verb in a terminal and confirms there", verb, errNotOwner)
	case err != nil:
		return fmt.Errorf("%s: %w: the owner's terminal cannot be opened: %v", verb, errOwnerUnknown, err)
	}
	defer tty.Close()
	dev, err := tty.Device()
	if err != nil {
		return fmt.Errorf("%s: %w: the controlling terminal's device cannot be read: %v", verb, errOwnerUnknown, err)
	}
	panes, err := seatPanes()
	if err != nil {
		return fmt.Errorf("%s: %w: %v", verb, errOwnerUnknown, err)
	}
	for _, p := range panes {
		pd, err := ttyDevice(p.TTY)
		if err != nil {
			return fmt.Errorf("%s: %w: the tty of seat %s's pane cannot be read: %v", verb, errOwnerUnknown, p.Agent, err)
		}
		if pd == dev {
			return fmt.Errorf("%s: %w: the controlling terminal is seat %s's pane", verb, errNotOwner, p.Agent)
		}
	}
	if len(panes) > 0 {
		pid := selfPID()
		start, ok := selfStart(pid)
		if !ok {
			return fmt.Errorf("%s: %w: this process's start stamp is unreadable", verb, errOwnerUnknown)
		}
		pane, known := chainReaches(pid, start, panes)
		switch {
		case !known:
			return fmt.Errorf("%s: %w: the process chain cannot be read", verb, errOwnerUnknown)
		case pane != nil:
			return fmt.Errorf("%s: %w: this process descends from seat %s's pane; this verb runs only from the owner's own terminal", verb, errNotOwner, pane.Agent)
		}
	}
	fmt.Fprintf(tty, "%s: type the seat handle %q to confirm (echo off, %s): ", verb, handle, confirmTimeout)
	type read struct {
		line string
		err  error
	}
	got := make(chan read, 1)
	go func() {
		line, err := bufio.NewReader(io.LimitReader(tty, 256)).ReadString('\n')
		got <- read{line, err}
	}()
	var line string
	select {
	case r := <-got:
		if r.err != nil && r.line == "" {
			return fmt.Errorf("%s: %w: no confirmation read from the terminal: %v", verb, errNotOwner, r.err)
		}
		line = r.line
	case <-time.After(confirmTimeout):
		return fmt.Errorf("%s: %w: no confirmation within %s", verb, errNotOwner, confirmTimeout)
	}
	if strings.TrimSpace(line) != handle {
		return fmt.Errorf("%s: %w: the confirmation typed on the terminal was not the seat handle", verb, errNotOwner)
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

// prBoundTo answers which task the store binds a repository's PR number to.
func prBoundTo(s task.Store) func(string, int) string {
	return func(repository string, n int) string {
		list, err := s.List()
		if err != nil {
			return "unknown (the task store is unreadable)"
		}
		for _, t := range list {
			if t.PRNumber == n && strings.EqualFold(t.PRRepository, repository) {
				return t.ID
			}
		}
		return ""
	}
}
