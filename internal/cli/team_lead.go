package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

var currentPaneIdentity = tmuxpane.CurrentPaneIdentity

type teamLeadData struct {
	Profile      string `json:"profile"`
	Orchestrated bool   `json:"orchestrated"`
	Lead         string `json:"lead,omitempty"`
	LeadHandle   string `json:"lead_handle,omitempty"`
}

func runTeamLead(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, `amq-squad team lead - manage orchestration lead metadata

Usage:
  amq-squad team lead set <role> [--project DIR] [--profile NAME]
  amq-squad team lead clear [--project DIR] [--profile NAME]
  amq-squad team lead show [--json] [--project DIR] [--profile NAME]

set marks the existing team profile as orchestrated and records <role> as the
lead. clear returns the profile to a flat team. The lead role must already be a
team member; use 'team member add' first for dynamic teams.
`)
		if len(args) == 0 {
			return usageErrorf("team lead requires a subcommand ('set', 'clear', or 'show')")
		}
		return nil
	}
	switch args[0] {
	case "set":
		return runTeamLeadSet(args[1:])
	case "clear":
		return runTeamLeadClear(args[1:])
	case "show":
		return runTeamLeadShow(args[1:])
	default:
		return usageErrorf("unknown 'team lead' subcommand: %q. Try 'set', 'clear', or 'show'.", args[0])
	}
}

func runTeamLeadSet(args []string) error {
	role, rest, ok := peelPositional(args)
	if !ok {
		return usageErrorf("a lead role is required, e.g. 'team lead set cto'")
	}
	fs := flag.NewFlagSet("team lead set", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to mutate (default: default profile)")
	if err := parseFlags(fs, rest); err != nil {
		return err
	}
	role = strings.ToLower(strings.TrimSpace(role))
	if err := setTeamLead(*projectFlag, *profileFlag, flagWasSet(fs, "project"), role); err != nil {
		return err
	}
	fmt.Printf("orchestrated lead set to %s.\n", role)
	return nil
}

func runTeamLeadClear(args []string) error {
	fs := flag.NewFlagSet("team lead clear", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to mutate (default: default profile)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("unexpected argument %q", fs.Arg(0))
	}
	projectDir, profile, err := resolveExistingTeamProfile(*projectFlag, *profileFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	if err := withProfileLock(projectDir, profile, func() error {
		t, err := team.ReadProfile(projectDir, profile)
		if err != nil {
			return fmt.Errorf("read team: %w", err)
		}
		t.Orchestrated = false
		t.Lead = ""
		return team.WriteProfile(projectDir, profile, t)
	}); err != nil {
		return err
	}
	fmt.Println("orchestrated lead cleared.")
	return nil
}

func runTeamLeadShow(args []string) error {
	fs := flag.NewFlagSet("team lead show", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to read (default: default profile)")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned team_lead envelope")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("unexpected argument %q", fs.Arg(0))
	}
	projectDir, profile, err := resolveExistingTeamProfile(*projectFlag, *profileFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	t, err := team.ReadProfile(projectDir, profile)
	if err != nil {
		return fmt.Errorf("read team: %w", err)
	}
	data := buildTeamLeadData(profile, t)
	if *jsonOut {
		return printJSONEnvelope("team_lead", data)
	}
	if !data.Orchestrated {
		fmt.Println("orchestrated: no")
		return nil
	}
	fmt.Printf("orchestrated: yes\nlead: %s\nlead_handle: %s\n", data.Lead, data.LeadHandle)
	return nil
}

func runLead(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, `amq-squad lead - register an external orchestrator

Usage:
  amq-squad lead register [--role ROLE] [--session S] [--project DIR] [--profile NAME]

register adopts the current tmux pane as the external lead for an existing team
profile. It sets orchestrated/lead when needed and writes an explicit external
runtime record, without pretending amq-squad spawned or owns the pane.
`)
		if len(args) == 0 {
			return usageErrorf("lead requires a subcommand ('register')")
		}
		return nil
	}
	switch args[0] {
	case "register":
		return runLeadRegister(args[1:])
	default:
		return usageErrorf("unknown 'lead' subcommand: %q. Try 'register'.", args[0])
	}
}

func runLeadRegister(args []string) error {
	fs := flag.NewFlagSet("lead register", flag.ContinueOnError)
	roleFlag := fs.String("role", "", "lead role to register (defaults to configured lead, then AM_ME)")
	sessionFlag := fs.String("session", "", "AMQ workstream session (default: team workstream)")
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to mutate (default: default profile)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("unexpected argument %q", fs.Arg(0))
	}
	projectDir, profile, err := resolveExistingTeamProfile(*projectFlag, *profileFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	t, err := team.ReadProfile(projectDir, profile)
	if err != nil {
		return fmt.Errorf("read team: %w", err)
	}
	role := strings.ToLower(strings.TrimSpace(*roleFlag))
	if role == "" {
		role = strings.ToLower(strings.TrimSpace(t.Lead))
	}
	if role == "" {
		role = strings.ToLower(strings.TrimSpace(os.Getenv("AM_ME")))
	}
	if role == "" {
		return usageErrorf("--role is required when the team has no configured lead and AM_ME is unset")
	}
	member, ok := memberByRole(t, role)
	if !ok {
		return fmt.Errorf("lead role %q is not a team member", role)
	}
	workstream, err := resolveTeamWorkstreamName(t, *sessionFlag, flagWasSet(fs, "session"))
	if err != nil {
		return err
	}
	id, err := currentPaneIdentity()
	if err != nil {
		return err
	}
	if id == nil {
		return fmt.Errorf("lead register requires a current tmux pane (TMUX/TMUX_PANE unset)")
	}
	cwd := member.EffectiveCWD(t.Project)
	handle := memberHandle(member)
	env, err := resolveAMQEnvInDir(cwd, "", workstream, handle)
	if err != nil {
		return fmt.Errorf("resolve amq env: %w", err)
	}
	if env.Me != "" {
		handle = env.Me
	}
	root := absoluteAMQRoot(cwd, env.Root)
	agentDir := filepath.Join(root, "agents", handle)
	rec := launch.Record{
		CWD:              cwd,
		Binary:           member.Binary,
		Session:          env.SessionName,
		SharedWorkstream: true,
		Handle:           handle,
		Role:             role,
		Root:             root,
		BaseRoot:         absoluteAMQRoot(cwd, env.BaseRoot),
		RootSource:       env.RootSource,
		AMQVersion:       env.AMQVersion,
		Model:            strings.TrimSpace(member.Model),
		Trust:            strings.TrimSpace(t.Trust),
		External:         true,
		AgentTTY:         currentLaunchTTY(),
		StartedAt:        time.Now().UTC(),
		TeamProfile:      profile,
		Tmux: &launch.TmuxInfo{
			Session:    id.Session,
			WindowID:   id.WindowID,
			WindowName: id.WindowName,
			PaneID:     id.PaneID,
			Target:     "external",
		},
	}
	if err := launch.Write(agentDir, rec); err != nil {
		return fmt.Errorf("write external launch record: %w", err)
	}
	if err := setTeamLeadForProfile(projectDir, profile, role); err != nil {
		return err
	}
	fmt.Printf("registered external lead %s (%s) at pane %s for session %s.\n", role, handle, id.PaneID, env.SessionName)
	return nil
}

func setTeamLead(projectFlag, profileFlag string, projectSet bool, role string) error {
	projectDir, profile, err := resolveExistingTeamProfile(projectFlag, profileFlag, projectSet)
	if err != nil {
		return err
	}
	return setTeamLeadForProfile(projectDir, profile, role)
}

func setTeamLeadForProfile(projectDir, profile, role string) error {
	if err := team.ValidateRoleID(role); err != nil {
		return fmt.Errorf("lead: %w", err)
	}
	return withProfileLock(projectDir, profile, func() error {
		t, err := team.ReadProfile(projectDir, profile)
		if err != nil {
			return fmt.Errorf("read team: %w", err)
		}
		if _, ok := memberByRole(t, role); !ok {
			return fmt.Errorf("lead role %q is not a team member", role)
		}
		t.Orchestrated = true
		t.Lead = role
		return team.WriteProfile(projectDir, profile, t)
	})
}

func memberByRole(t team.Team, role string) (team.Member, bool) {
	for _, m := range t.Members {
		if m.Role == role {
			return m, true
		}
	}
	return team.Member{}, false
}

func buildTeamLeadData(profile string, t team.Team) teamLeadData {
	data := teamLeadData{Profile: profile, Orchestrated: t.Orchestrated}
	if !t.Orchestrated {
		return data
	}
	data.Lead = strings.TrimSpace(t.Lead)
	data.LeadHandle = data.Lead
	if m, ok := memberByRole(t, data.Lead); ok {
		data.LeadHandle = memberHandle(m)
	}
	return data
}
