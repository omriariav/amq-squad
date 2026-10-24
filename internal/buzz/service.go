package buzz

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

// ServiceState is amq-remote's login service for a root
// (`amq-remote service status --json`, AMQ v0.107.0): state is current,
// stale (another binary), other_root or missing.
type ServiceState struct {
	State         string `json:"state"`
	Path          string `json:"path"`
	Root          string `json:"root"`
	InstalledRoot string `json:"installed_root"`
	Running       bool   `json:"running"`
}

// LoginService reads the login service state. It is the seam doctor (brief
// 2b-3) calls; the one-line wiring is a follow-up named in the 4-5 log.
type LoginService interface {
	Status(ctx context.Context, root string) (ServiceState, error)
}

// AMQRemoteService asks the installed amq-remote.
type AMQRemoteService struct{ Bin string }

func (a AMQRemoteService) Status(ctx context.Context, root string) (ServiceState, error) {
	bin := a.Bin
	if bin == "" {
		bin = "amq-remote"
	}
	args := []string{"service", "status", "--json"}
	if root != "" {
		args = append(args, "--root", root)
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(cctx, bin, args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return ServiceState{}, fmt.Errorf("amq-remote service status: %v: %s", err, bytes.TrimSpace(errb.Bytes()))
	}
	var st ServiceState
	if err := json.Unmarshal(out.Bytes(), &st); err != nil || st.State == "" {
		return ServiceState{}, fmt.Errorf("amq-remote service status: unreadable output %q", bytes.TrimSpace(out.Bytes()))
	}
	return st, nil
}

// ServiceWarning is the doctor line of plan 8: a warning when the login
// service for amq-remote is absent (nothing restarts the endpoint after a
// reboot), "" when it is installed for this root and running. A check that
// cannot run is a warning too: doctor never reports a service it could not
// read as present.
func ServiceWarning(ctx context.Context, s LoginService, root string) string {
	if s == nil {
		return "amq-remote login service: not checked (no amq-remote on this host)"
	}
	st, err := s.Status(ctx, root)
	if err != nil {
		return "amq-remote login service: unknown (" + err.Error() + "); run amq-remote service status"
	}
	switch st.State {
	case "missing":
		return fmt.Sprintf("amq-remote login service is absent for root %s: nothing restarts the endpoint after a reboot; run amq-remote service install --root %s", st.Root, st.Root)
	case "other_root":
		return fmt.Sprintf("amq-remote login service runs root %s, not %s; there is one per user (amq-remote service install --root %s)", st.InstalledRoot, st.Root, st.Root)
	case "stale":
		return fmt.Sprintf("amq-remote login service runs another amq-remote binary; run amq-remote service install --root %s again", st.Root)
	case "current":
		if !st.Running {
			return fmt.Sprintf("amq-remote login service is installed for root %s but not running; log in again or run amq-remote service install --root %s", st.Root, st.Root)
		}
		return ""
	}
	return "amq-remote login service: unknown state " + st.State
}
