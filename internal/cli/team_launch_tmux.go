package cli

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

type tmuxClient struct {
	Name        string
	TTY         string
	ControlMode bool
	Flags       string
	Session     string
}

// paneTitleToken builds the deterministic, machine-parseable pane title that the
// name-first tmux pane resolution matches: "amq:<workstream>:<role>". The
// session component is the AMQ WORKSTREAM (not the tmux/terminal session name),
// because that is the agent identity every resolver caller (status, focus, send)
// keys off — stamping the terminal-session name here would make the title never
// match the resolver's expectedPaneToken and break name-first/exclusion. The
// role is unique per agent within a workstream, so two agents that share a repo
// AND an engine (cpo·codex + cto·codex in one dir) still get distinct titles.
// MUST stay in lockstep with expectedPaneToken in internal/tmuxpane/tmux.go,
// which is called with the workstream.
func paneTitleToken(workstream, role string) string {
	return "amq:" + workstream + ":" + role
}

func tmuxControlModeClients() []tmuxClient {
	out, err := tmuxOutputCommand("tmux", "list-clients", "-F", "#{client_name}\t#{client_tty}\t#{client_control_mode}\t#{client_flags}\t#{client_session}")
	if err != nil {
		return nil
	}
	return parseTmuxClients(out)
}

func parseTmuxClients(out string) []tmuxClient {
	var clients []tmuxClient
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		name, tty, controlField, flags, session := "", "", "", "", ""
		if len(parts) == 5 {
			name, tty, controlField, flags, session = parts[0], parts[1], parts[2], parts[3], parts[4]
		} else if len(parts) == 3 {
			// Compatibility with the original three-column warning parser.
			tty, controlField = parts[0], parts[1]
			name = tty
			flags = parts[2]
		} else {
			continue
		}
		name, tty, controlField, flags, session = strings.TrimSpace(name), strings.TrimSpace(tty), strings.TrimSpace(controlField), strings.TrimSpace(flags), strings.TrimSpace(session)
		control := controlField == "1" || strings.Contains(flags, "control-mode")
		if !control {
			continue
		}
		clients = append(clients, tmuxClient{
			Name:        name,
			TTY:         tty,
			ControlMode: true,
			Flags:       flags,
			Session:     session,
		})
	}
	return clients
}

var tmuxOutputCommand = outputCommand

func outputCommand(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return "", fmt.Errorf("%s: %w: %s", shellCommand(name, args...), err, msg)
		}
		return "", fmt.Errorf("%s: %w", shellCommand(name, args...), err)
	}
	return string(out), nil
}
