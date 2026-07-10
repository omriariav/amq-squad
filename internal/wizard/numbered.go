package wizard

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// NumberedOptions supplies the preview-only line UI with defaults and the one
// filesystem fact it needs. The callback keeps this package independent from
// the team persistence package.
type NumberedOptions struct {
	Defaults      Spec
	ProfileExists func(project, profile string) bool
}

// RunNumbered collects a project run using plain numbered/text prompts. It is
// intended only for a real terminal; the CLI owns that guard. Enter accepts
// every displayed default. The result contains no live-launch bit.
func RunNumbered(in io.Reader, out io.Writer, opts NumberedOptions) (Spec, error) {
	if in == nil || out == nil {
		return Spec{}, fmt.Errorf("wizard numbered UI requires input and output")
	}
	r := bufio.NewReader(in)
	s := opts.Defaults

	fmt.Fprintln(out, "amq-squad run start wizard")
	fmt.Fprintln(out, "Preview only: this flow cannot launch agents.")
	fmt.Fprintln(out)

	var err error
	if s.Project, err = promptText(r, out, "Project directory", s.Project); err != nil {
		return Spec{}, err
	}
	if s.Profile, err = promptText(r, out, "Team profile", defaultString(s.Profile, "default")); err != nil {
		return Spec{}, err
	}
	if s.Session, err = promptText(r, out, "Workstream session", s.Session); err != nil {
		return Spec{}, err
	}

	existing := opts.ProfileExists != nil && opts.ProfileExists(s.Project, s.Profile)
	if existing {
		fmt.Fprintf(out, "Using existing profile %q; roster and lead mode remain unchanged.\n\n", s.Profile)
		s.Roles = ""
		s.Binary = ""
		s.LeadMode = ""
	} else {
		if s.Roles, err = promptText(r, out, "Roles (comma-separated)", defaultString(s.Roles, "cto,senior-dev,qa")); err != nil {
			return Spec{}, err
		}
		if s.Lead, err = promptText(r, out, "Lead role", defaultString(s.Lead, "cto")); err != nil {
			return Spec{}, err
		}
		if s.LeadMode, err = promptChoice(r, out, "Lead mode", []choice{
			{value: "builder", label: "builder: lead may implement and delegate"},
			{value: "planner", label: "planner: lead must delegate mutations"},
		}, defaultString(s.LeadMode, "builder")); err != nil {
			return Spec{}, err
		}
	}

	if s.Visibility, err = promptChoice(r, out, "Topology", []choice{
		{value: "sibling-tabs", label: "sibling-tabs: one visible tmux window per agent"},
		{value: "detached", label: "detached: hidden tmux session"},
		{value: "current", label: "current: split the current tmux window"},
	}, defaultString(s.Visibility, "sibling-tabs")); err != nil {
		return Spec{}, err
	}
	if s.Goal, err = promptText(r, out, "Goal text (optional)", s.Goal); err != nil {
		return Spec{}, err
	}
	if s.SeedFrom, err = promptText(r, out, "Seed brief from file:/issue:/gh: reference (optional)", s.SeedFrom); err != nil {
		return Spec{}, err
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, "Answers collected. Running the canonical read-only preview next.")
	return s, nil
}

type choice struct {
	value string
	label string
}

func promptText(r *bufio.Reader, out io.Writer, label, current string) (string, error) {
	fmt.Fprintf(out, "%s [%s]: ", label, current)
	line, err := r.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read %s: %w", strings.ToLower(label), err)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return current, nil
	}
	return line, nil
}

func promptChoice(r *bufio.Reader, out io.Writer, label string, choices []choice, current string) (string, error) {
	fmt.Fprintln(out)
	fmt.Fprintln(out, label+":")
	defaultIndex := 0
	for i, item := range choices {
		marker := ""
		if item.value == current {
			defaultIndex = i
			marker = " (default)"
		}
		fmt.Fprintf(out, "  %d) %s%s\n", i+1, item.label, marker)
	}
	fmt.Fprintf(out, "Choose [default %d]: ", defaultIndex+1)
	line, err := r.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read %s: %w", strings.ToLower(label), err)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return choices[defaultIndex].value, nil
	}
	for i, item := range choices {
		if line == fmt.Sprint(i+1) || strings.EqualFold(line, item.value) {
			return item.value, nil
		}
	}
	return "", fmt.Errorf("invalid %s choice %q", strings.ToLower(label), line)
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
