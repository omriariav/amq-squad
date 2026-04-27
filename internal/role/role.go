// Package role reads and writes role.md, the per-agent markdown doc that
// lives alongside launch.json in the agent's mailbox directory. role.md is
// the human-editable source of a role's description, peers, system prompt
// guidance, and priming notes.
package role

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const FileName = "role.md"

// Stub is the data used to seed a new role.md. Fields come from the catalog
// entry at team init time; the user can edit the rendered file freely.
type Stub struct {
	Label       string
	RoleID      string
	Description string
	Skills      []string
	Peers       []string
}

// Path returns the role.md path inside an agent's mailbox directory.
func Path(agentDir string) string {
	return filepath.Join(agentDir, FileName)
}

// EnsureStub writes a role.md for the given agent if one doesn't already
// exist. It also upgrades untouched placeholder stubs from older amq-squad
// versions. It never overwrites existing user edits. Returns true if it wrote
// a file, false if usable content was already there.
func EnsureStub(agentDir string, s Stub) (bool, error) {
	p := Path(agentDir)
	if _, err := os.Stat(p); err == nil {
		body, err := os.ReadFile(p)
		if err != nil {
			return false, fmt.Errorf("read %s: %w", p, err)
		}
		if string(body) == renderLegacyPlaceholder(s) {
			if err := writeFile(p, render(s)); err != nil {
				return false, err
			}
			return true, nil
		}
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("stat %s: %w", p, err)
	}
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		return false, fmt.Errorf("ensure agent dir: %w", err)
	}
	if err := writeFile(p, render(s)); err != nil {
		return false, err
	}
	return true, nil
}

func writeFile(path, body string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

func render(s Stub) string {
	var b strings.Builder
	label := s.Label
	if label == "" {
		label = s.RoleID
	}
	fmt.Fprintf(&b, "# Role: %s\n\n", label)

	b.WriteString("## Description\n")
	if s.Description != "" {
		fmt.Fprintf(&b, "%s\n\n", s.Description)
	} else {
		b.WriteString("No catalog description is configured for this custom role. Follow team rules and ask the user to clarify scope before taking broad work.\n\n")
	}

	b.WriteString("## Peers\n")
	if len(s.Peers) > 0 {
		for _, p := range s.Peers {
			fmt.Fprintf(&b, "- %s\n", p)
		}
		b.WriteString("\n")
	} else {
		b.WriteString("No default peers are configured. Use the current team routing block from the launch prompt for live messages and handoffs.\n\n")
	}

	b.WriteString("## Skills\n")
	if len(s.Skills) > 0 {
		for _, sk := range s.Skills {
			fmt.Fprintf(&b, "- %s\n", sk)
		}
		b.WriteString("- amq-squad for team setup, launch, AMQ routing, inbox drains, acknowledgements, review requests, handoffs, and decision threads.\n")
		b.WriteString("- amq-cli only for raw AMQ debugging or non-squad AMQ usage.\n")
		b.WriteString("\n")
	} else {
		b.WriteString("No role-specific slash skills are configured. Use `amq-squad` for team setup, launch, AMQ routing, inbox drains, acknowledgements, review requests, handoffs, and decision threads. Use `amq-cli` only for raw AMQ debugging or non-squad AMQ usage.\n\n")
	}

	b.WriteString("## System Prompt\n")
	b.WriteString("Use the binary default system behavior together with team-rules.md and this role file. Stay within the role scope, use the amq-squad protocol for team handoffs, and treat old AMQ history as context unless the user asks to resume it.\n\n")

	b.WriteString("## Priming Template\n")
	b.WriteString("At launch, amq-squad injects identity, startup file paths, current team routing, first steps, and the path to this role file. Read those startup files, summarize relevant context, then stop and wait for the user's instruction.\n")

	return b.String()
}

func renderLegacyPlaceholder(s Stub) string {
	var b strings.Builder
	label := s.Label
	if label == "" {
		label = s.RoleID
	}
	fmt.Fprintf(&b, "# Role: %s\n\n", label)

	b.WriteString("## Description\n")
	if s.Description != "" {
		fmt.Fprintf(&b, "%s\n\n", s.Description)
	} else {
		b.WriteString("TODO: describe this role in one paragraph.\n\n")
	}

	b.WriteString("## Peers\n")
	if len(s.Peers) > 0 {
		for _, p := range s.Peers {
			fmt.Fprintf(&b, "- %s\n", p)
		}
		b.WriteString("\n")
	} else {
		b.WriteString("TODO: list the role IDs this agent talks to most.\n\n")
	}

	b.WriteString("## Skills\n")
	if len(s.Skills) > 0 {
		for _, sk := range s.Skills {
			fmt.Fprintf(&b, "- %s\n", sk)
		}
		b.WriteString("\n")
	} else {
		b.WriteString("TODO: list slash commands or skills this role should invoke.\n\n")
	}

	b.WriteString("## System Prompt\n")
	b.WriteString("TODO: optional override. Leave blank to use the binary default.\n\n")

	b.WriteString("## Priming Template\n")
	b.WriteString("TODO: the prompt text used when this agent boots. Slice B will\n")
	b.WriteString("compose this with active peers and active thread at restore time.\n")

	return b.String()
}
