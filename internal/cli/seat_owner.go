package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// The owner-only verb seat clear (R1 of the milestone B first pass):
// owner-only and lead-only verbs never take identity from flags or the
// environment. The owner is proved by where the process runs, as guard
// proves the owner for grants: no AM_ME, none of the seat or lead
// environment markers, and the invoking process does not descend from any
// seat pane (the pane chain of amendment 28's proof: the shell pid of every
// tmux pane AMQ labelled @amq_pane_agent, from a read-only list-panes; R6:
// amq-squad never creates, moves or closes a pane). The clear is a record
// signed with the core's issuer key, which the commit step verifies against
// the owner's trust store.

// errNotOwner is every refusal of the owner proof.
var errNotOwner = errors.New("not_owner_terminal")

// Seams for tests: the seat panes' shell pids and a pid's parent.
var (
	seatPaneShells = tmuxSeatPaneShells
	parentPID      = psParentPID
	selfPID        = os.Getpid
)

// seatMarkers are the environment names a seat or the lead carries (seat-exec
// and the pi runtime set them from the launch record).
var seatMarkers = []string{"AM_ME", "AMQ_SQUAD_SEAT", "AMQ_SQUAD_LEAD", "AMQ_SQUAD_ROLE_PATH"}

// requireOwnerTerminal refuses unless this process is the owner's terminal.
func requireOwnerTerminal(verb string) error {
	for _, name := range seatMarkers {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return fmt.Errorf("%s: %w: %s is set; this verb runs only from the owner's own terminal, never inside a seat or from the lead", verb, errNotOwner, name)
		}
	}
	shells, err := seatPaneShells()
	if err != nil {
		return fmt.Errorf("%s: %w: the seat panes cannot be listed: %v", verb, errNotOwner, err)
	}
	if len(shells) == 0 {
		return nil
	}
	p := selfPID()
	for i := 0; i < 64 && p > 1; i++ {
		if shells[p] != "" {
			return fmt.Errorf("%s: %w: this process descends from seat %s's pane; this verb runs only from the owner's own terminal", verb, errNotOwner, shells[p])
		}
		next, ok := parentPID(p)
		if !ok {
			return fmt.Errorf("%s: %w: the process chain cannot be read at pid %d", verb, errNotOwner, p)
		}
		if next == p {
			break
		}
		p = next
	}
	return nil
}

// tmuxSeatPaneShells maps the shell pid of every pane AMQ labelled with a
// seat handle to that handle. No tmux, or no tmux server, is no pane.
func tmuxSeatPaneShells() (map[int]string, error) {
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		return nil, nil
	}
	out, err := exec.Command(tmux, "list-panes", "-a", "-F", "#{pane_pid} #{@amq_pane_agent}").Output()
	if err != nil {
		return nil, nil // no server running: no pane
	}
	shells := map[int]string{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		if pid, err := strconv.Atoi(f[0]); err == nil && pid > 1 {
			shells[pid] = f[1]
		}
	}
	return shells, nil
}

// psParentPID is a pid's parent from ps (portable across darwin and Linux).
func psParentPID(pid int) (int, bool) {
	out, err := exec.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	return n, err == nil
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
