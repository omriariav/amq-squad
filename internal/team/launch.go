package team

import (
	"fmt"
	"slices"
	"strings"

	"github.com/avivsinai/agent-message-queue/launchapi"
)

// squad.yaml compiles to one AMQ launch request (plan 4.3). LaunchSeat is the
// typed seat list of the 2b-2 compiler (seatlaunch.Seat: handle, runtime,
// model, effort, role, worktree); the adapter that hands it to
// seatlaunch.Compile, with the role.md text for Role, lands when 2b-2 merges.

// LaunchSeat is one seat ready for the launch compiler.
type LaunchSeat struct {
	Handle   string `json:"handle"`
	Runtime  string `json:"runtime"`
	Model    string `json:"model,omitempty"`
	Effort   string `json:"effort,omitempty"`
	Role     string `json:"role"`     // role id; the compiler takes its role.md text
	Worktree string `json:"worktree"` // absolute
}

// CodexSandboxArgs are the Codex sandbox and approval flags (plan 4.3, 7.3).
var CodexSandboxArgs = []string{"--sandbox", "workspace-write", "--ask-for-approval", "on-request"}

// LaunchSeats resolves the squad's seats against its repository root.
func (s Squad) LaunchSeats(repoRoot string) []LaunchSeat {
	var out []LaunchSeat
	for _, seat := range s.Resolved() {
		out = append(out, LaunchSeat{Handle: seat.Handle, Runtime: seat.Runtime, Model: seat.Model, Effort: seat.Effort,
			Role: seat.Role, Worktree: SeatWorktree(repoRoot, seat)})
	}
	return out
}

// ProviderArgs are a seat's provider arguments: model and effort (and Codex's
// sandbox and approval flags) for Claude and Codex, none for pi, which admits
// no provider arguments (amendment 23). They travel in the launch request,
// so they enter the trust digest.
func ProviderArgs(s LaunchSeat) []string {
	var args []string
	if s.Runtime != "claude" && s.Runtime != "codex" {
		return nil
	}
	if s.Model != "" {
		args = append(args, "--model", s.Model)
	}
	switch {
	case s.Runtime == "claude" && s.Effort != "":
		args = append(args, "--effort", s.Effort)
	case s.Runtime == "codex" && s.Effort != "":
		args = append(args, "-c", "model_reasoning_effort="+s.Effort)
	}
	if s.Runtime == "codex" {
		args = append(args, CodexSandboxArgs...)
	}
	return args
}

// CheckArgumentForms refuses, before any launch, a seat whose provider
// arguments use a form AMQ does not advertise for that provider. amq-squad
// keeps no list of forms of its own (amendment 20 (b)): the forms are the
// ones AMQ derives from its validation tables (ProviderCapabilitiesV1).
func CheckArgumentForms(seats []LaunchSeat, caps []launchapi.ProviderCapabilitiesV1) error {
	byProvider := map[string]launchapi.ProviderCapabilitiesV1{}
	for _, c := range caps {
		byProvider[c.Provider] = c
	}
	for _, s := range seats {
		args := ProviderArgs(s)
		c, ok := byProvider[s.Runtime]
		if len(args) > 0 && !ok {
			return fmt.Errorf("seat %s: AMQ advertised no argument forms for %s", s.Handle, s.Runtime)
		}
		for i := 0; i < len(args); i++ {
			if !strings.HasPrefix(args[i], "-") {
				continue
			}
			if !slices.Contains(c.AllowedArgumentForms, args[i]) {
				return fmt.Errorf("seat %s: AMQ does not admit %s for %s (admitted: %v)", s.Handle, args[i], s.Runtime, c.AllowedArgumentForms)
			}
			if args[i] == "-c" && i+1 < len(args) {
				key, value, _ := strings.Cut(args[i+1], "=")
				if !slices.ContainsFunc(c.ConfigOverrides, func(o launchapi.ConfigOverrideCapabilityV1) bool {
					return o.Key == key && (len(o.AllowedValues) == 0 || slices.Contains(o.AllowedValues, value))
				}) {
					return fmt.Errorf("seat %s: AMQ does not admit -c %s for %s", s.Handle, args[i+1], s.Runtime)
				}
			}
		}
	}
	return nil
}
