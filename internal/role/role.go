// Package role reads and writes role.md, the per-agent markdown doc that
// lives alongside launch.json in the agent's mailbox directory. role.md is
// the human-editable source of a role's description, peers, system prompt
// override, and priming template. Slice B composes the priming prompt from
// it; slice A (this one) just authors the stub.
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
// exist. It never overwrites existing user edits. Returns true if it wrote
// a new file, false if one was already there.
func EnsureStub(agentDir string, s Stub) (bool, error) {
	p := Path(agentDir)
	if _, err := os.Stat(p); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("stat %s: %w", p, err)
	}
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		return false, fmt.Errorf("ensure agent dir: %w", err)
	}
	body := render(s)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		return false, fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, p); err != nil {
		return false, fmt.Errorf("rename: %w", err)
	}
	return true, nil
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
