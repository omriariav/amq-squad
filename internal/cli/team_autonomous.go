package cli

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

// teamAutonomousAfterRead is a deterministic test seam. It runs while the
// profile writer lock is held, after the command has loaded the only snapshot
// it may mutate.
var teamAutonomousAfterRead = func() {}

func runTeamAutonomous(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, `amq-squad team autonomous - inspect or control opt-in Autonomous mode

Usage:
  amq-squad team autonomous show [--project DIR] [--profile NAME] [--json]
  amq-squad team autonomous pause [--project DIR] [--profile NAME]
  amq-squad team autonomous resume [--project DIR] [--profile NAME]
  amq-squad team autonomous disable [--project DIR] [--profile NAME]

Autonomous mode is opt-in only. pause/resume/disable mutate only the profile's
autonomous policy flags; they do not spawn, prune, launch, stop, merge, release,
or perform external side effects.
`)
		if len(args) == 0 {
			return usageErrorf("team autonomous requires a subcommand (show, pause, resume, disable)")
		}
		return nil
	}
	switch args[0] {
	case "show":
		return runTeamAutonomousShow(args[1:])
	case "pause", "resume", "disable":
		return runTeamAutonomousMutation(args[1:], args[0])
	default:
		return usageErrorf("unknown 'team autonomous' subcommand: %q. Try show, pause, resume, or disable.", args[0])
	}
}

func runTeamAutonomousShow(args []string) error {
	fs := flag.NewFlagSet("team autonomous show", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory to inspect (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to inspect (default: default profile)")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned autonomous_status envelope")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("team autonomous show takes no positional arguments")
	}
	t, profile, err := readAutonomousTeam(*projectFlag, *profileFlag, fs)
	if err != nil {
		return err
	}
	status := team.EffectiveAutonomousStatus(t)
	if *jsonOut {
		return printJSONEnvelope("autonomous_status", status)
	}
	fmt.Printf("composition: %s\n", status.Composition)
	fmt.Printf("enabled: %t\n", status.Enabled)
	fmt.Printf("paused: %t\n", status.Paused)
	fmt.Printf("disabled: %t\n", status.Disabled)
	fmt.Printf("active_agents: %d\n", status.ActiveAgents)
	fmt.Printf("profile: %s\n", profile)
	if status.Policy != nil {
		fmt.Printf("max_active_agents: %d\n", status.MaxActiveAgents)
		fmt.Printf("max_total_spawns: %d\n", status.MaxTotalSpawns)
		fmt.Printf("total_spawns: %d\n", status.TotalSpawns)
		fmt.Printf("budget_turns: %d\n", status.BudgetTurns)
		fmt.Printf("budget_turns_used: %d\n", status.BudgetTurnsUsed)
		fmt.Printf("allowed_roles: %s\n", strings.Join(status.Policy.AllowedRoles, ","))
		fmt.Printf("allowed_role_classes: %s\n", strings.Join(status.Policy.AllowedRoleClasses, ","))
	}
	return nil
}

func runTeamAutonomousMutation(args []string, action string) error {
	fs := flag.NewFlagSet("team autonomous "+action, flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory to mutate (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to mutate (default: default profile)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("team autonomous %s takes no positional arguments", action)
	}
	projectDir, profile, err := resolveAutonomousProjectProfile(*projectFlag, *profileFlag, fs)
	if err != nil {
		return err
	}
	if err := team.WithProfileLock(projectDir, profile, func() error {
		t, err := team.ReadProfile(projectDir, profile)
		if err != nil {
			return fmt.Errorf("read team: %w", err)
		}
		if team.EffectiveComposition(t) != team.CompositionAutonomous || t.Autonomous == nil {
			return usageErrorf("team autonomous %s requires a profile with composition=autonomous", action)
		}
		teamAutonomousAfterRead()
		switch action {
		case "pause":
			t.Autonomous.Paused = true
		case "resume":
			t.Autonomous.Paused = false
			if t.Autonomous.Disabled {
				return usageErrorf("team autonomous resume cannot resume a disabled policy; reconfigure the profile explicitly")
			}
		case "disable":
			t.Autonomous.Disabled = true
		}
		return team.WriteProfileUnderLock(projectDir, profile, t)
	}); err != nil {
		return err
	}
	fmt.Printf("autonomous %s for profile %s\n", action, profile)
	return nil
}

func readAutonomousTeam(projectFlag, profileFlag string, fs *flag.FlagSet) (team.Team, string, error) {
	projectDir, profile, err := resolveAutonomousProjectProfile(projectFlag, profileFlag, fs)
	if err != nil {
		return team.Team{}, "", err
	}
	t, err := team.ReadProfile(projectDir, profile)
	if err != nil {
		return team.Team{}, "", fmt.Errorf("read team: %w", err)
	}
	return t, profile, nil
}

func resolveAutonomousProjectProfile(projectFlag, profileFlag string, fs *flag.FlagSet) (string, string, error) {
	ctx, err := resolveCanonicalContext(contextResolveOptions{
		ProjectFlag: projectFlag, ProfileFlag: profileFlag,
		ProjectExplicit: flagWasSet(fs, "project"), ProfileExplicit: flagWasSet(fs, "profile"),
	})
	if err != nil {
		return "", "", err
	}
	emitContextDiagnostics(ctx)
	return ctx.ProjectDir, ctx.Profile, nil
}
