package team

import (
	"fmt"

	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
)

// squad.yaml compiles to one AMQ launch request (plan 4.3): LaunchSeats hands
// its seats to 2b-2's compiler as the typed seat list (seatlaunch.Seat).
// Provider arguments and the argument-form check against the AMQ
// advertisement are the compiler's (seatlaunch.ProviderArgs,
// seatlaunch.CheckArgumentForms); this package keeps no copy.

// LaunchSeats is the squad's seats as seatlaunch.Seat, resolved against the
// repository root, with Role the role.md text roleText returns for the
// seat's role id. A pi seat carries no model or effort (amendment 23).
func (s Squad) LaunchSeats(repoRoot string, roleText func(roleID string) (string, error)) ([]seatlaunch.Seat, error) {
	var out []seatlaunch.Seat
	for _, seat := range s.Resolved() {
		text, err := roleText(seat.Role)
		if err != nil {
			return nil, fmt.Errorf("seat %s: role %q: %w", seat.Handle, seat.Role, err)
		}
		out = append(out, seatlaunch.Seat{Handle: seat.Handle, Runtime: seat.Runtime, Model: seat.Model, Effort: seat.Effort,
			Role: text, Worktree: SeatWorktree(repoRoot, seat)})
	}
	return out, nil
}
