package cli

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/team"
)

func runTeamOperator(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, "Usage:\n  amq-squad team operator set --mode lead_pane|separate_terminal|noc\n")
		return nil
	}
	switch args[0] {
	case "set":
		return runTeamOperatorSet(args[1:])
	default:
		return unknownSubcommandError("team operator", args[0], "set")
	}
}

func runTeamOperatorSet(args []string) error {
	fs := flag.NewFlagSet("team operator set", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory")
	profileFlag := fs.String("profile", "", "team profile")
	modeFlag := fs.String("mode", "", "operator interaction mode: lead_pane, separate_terminal or noc")
	sessionFlag := fs.String("session", "", "workstream session")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	ctx, err := resolveCanonicalContext(contextResolveOptions{
		ProjectFlag: *projectFlag, ProfileFlag: *profileFlag, SessionFlag: *sessionFlag,
		ProjectExplicit: flagWasSet(fs, "project"), ProfileExplicit: flagWasSet(fs, "profile"), SessionExplicit: flagWasSet(fs, "session"),
	})
	if err != nil {
		return err
	}
	emitContextDiagnostics(ctx)
	mode := strings.TrimSpace(*modeFlag)
	if err := team.ValidateOperatorInteractionMode(mode); err != nil {
		return usageErrorf("--mode: %v", err)
	}
	return mutateOperatorProfile(ctx.ProjectDir, ctx.Profile, func(cfg *team.Team) error {
		if cfg.Operator == nil || !cfg.Operator.Enabled {
			return usageErrorf("team operator set requires an enabled operator")
		}
		cfg.Operator.InteractionMode = mode
		return nil
	})
}

func mutateOperatorProfile(projectDir, profile string, mutate func(*team.Team) error) error {
	return team.WithProfileLock(projectDir, profile, func() error {
		cfg, err := team.ReadProfile(projectDir, profile)
		if err != nil {
			return err
		}
		if err := mutate(&cfg); err != nil {
			return err
		}
		if err := team.WriteProfileUnderLock(projectDir, profile, cfg); err != nil {
			return err
		}
		fmt.Printf("operator mode=%s\n", cfg.Operator.InteractionMode)
		return nil
	})
}
