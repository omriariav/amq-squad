package cli

import (
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func runTeamOperator(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, "Usage:\n  amq-squad team operator set --mode MODE [--self LEAD --session S --allow kind,...]\n  amq-squad team operator self pause|resume --session S\n")
		return nil
	}
	switch args[0] {
	case "set":
		return runTeamOperatorSet(args[1:])
	case "self":
		return runTeamOperatorSelf(args[1:])
	default:
		return usageErrorf("unknown team operator subcommand %q", args[0])
	}
}

func runTeamOperatorSet(args []string) error {
	fs := flag.NewFlagSet("team operator set", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory")
	profileFlag := fs.String("profile", "", "team profile")
	modeFlag := fs.String("mode", "", "operator mode")
	selfFlag := fs.String("self", "", "delegated lead role")
	sessionFlag := fs.String("session", "", "exact delegated session")
	allowFlag := fs.String("allow", "", "comma-separated gate-kind allowlist")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	projectDir, profile, err := resolveProjectProfile(*projectFlag, *profileFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	mode := strings.TrimSpace(*modeFlag)
	if err := team.ValidateOperatorInteractionMode(mode); err != nil {
		return usageErrorf("--mode: %v", err)
	}
	return mutateOperatorProfile(projectDir, profile, func(cfg *team.Team) error {
		if err := rejectRunnableOperatorPolicyMutation(projectDir, profile, strings.TrimSpace(*sessionFlag), *cfg); err != nil {
			return err
		}
		if cfg.Operator == nil || !cfg.Operator.Enabled {
			return usageErrorf("team operator set requires an enabled operator")
		}
		if mode != team.OperatorInteractionSelfOperator {
			if cfg.Operator.InteractionMode != mode {
				cfg.Operator.InteractionMode = mode
				if cfg.Operator.SelfOperator != nil {
					if err := incrementPolicyRevision(cfg.Operator.SelfOperator); err != nil {
						return err
					}
				}
			}
			return nil
		}
		lead := strings.TrimSpace(*selfFlag)
		session := strings.TrimSpace(*sessionFlag)
		if !cfg.Orchestrated || lead == "" || lead != cfg.Lead {
			return usageErrorf("self_operator requires --self equal to the configured orchestrated lead")
		}
		if err := team.ValidateSessionName(session); err != nil {
			return usageErrorf("--session: %v", err)
		}
		policy := cfg.Operator.SelfOperator
		if policy == nil {
			policy = &team.SelfOperatorPolicy{Sessions: map[string]team.SelfOperatorSessionPolicy{}}
		}
		if policy.Sessions == nil {
			policy.Sessions = map[string]team.SelfOperatorSessionPolicy{}
		}
		entry, exists := policy.Sessions[session]
		beforeEntry := entry
		beforeLead, beforeMode := policy.LeadRole, cfg.Operator.InteractionMode
		if flagWasSet(fs, "allow") {
			allowed, allowErr := operatorauth.ValidateAllowlist(splitCSV(*allowFlag))
			if allowErr != nil || len(allowed) == 0 {
				return usageErrorf("--allow requires a non-empty non-human-only allowlist: %v", allowErr)
			}
			entry.AllowedGateKinds = allowed
		} else if !exists || len(entry.AllowedGateKinds) == 0 {
			return usageErrorf("first self_operator session setup requires --allow with explicit gate kinds")
		}
		entry.Enabled = true
		entry.Paused = false
		policy.LeadRole = lead
		if beforeLead == lead && beforeMode == mode && beforeEntry.Enabled == entry.Enabled && beforeEntry.Paused == entry.Paused && strings.Join(beforeEntry.AllowedGateKinds, "\x00") == strings.Join(entry.AllowedGateKinds, "\x00") {
			return nil
		}
		if err := incrementPolicyRevision(policy); err != nil {
			return err
		}
		policy.Sessions[session] = entry
		cfg.Operator.SelfOperator = policy
		cfg.Operator.InteractionMode = mode
		return nil
	})
}

func runTeamOperatorSelf(args []string) error {
	if len(args) == 0 || (args[0] != "pause" && args[0] != "resume") {
		return usageErrorf("team operator self requires pause or resume")
	}
	pause := args[0] == "pause"
	fs := flag.NewFlagSet("team operator self", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory")
	profileFlag := fs.String("profile", "", "team profile")
	sessionFlag := fs.String("session", "", "exact delegated session")
	if err := parseFlags(fs, args[1:]); err != nil {
		return err
	}
	projectDir, profile, err := resolveProjectProfile(*projectFlag, *profileFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	session := strings.TrimSpace(*sessionFlag)
	return mutateOperatorProfile(projectDir, profile, func(cfg *team.Team) error {
		if err := rejectRunnableOperatorPolicyMutation(projectDir, profile, session, *cfg); err != nil {
			return err
		}
		if cfg.Operator == nil || cfg.Operator.SelfOperator == nil {
			return usageErrorf("self_operator policy is not configured")
		}
		entry, ok := cfg.Operator.SelfOperator.Sessions[session]
		if !ok {
			return usageErrorf("no self_operator policy for exact session %q", session)
		}
		if !pause && !entry.Enabled {
			return usageErrorf("cannot resume disabled session %q", session)
		}
		if entry.Paused == pause {
			return nil
		}
		entry.Paused = pause
		cfg.Operator.SelfOperator.Sessions[session] = entry
		if err := incrementPolicyRevision(cfg.Operator.SelfOperator); err != nil {
			return err
		}
		return nil
	})
}

func incrementPolicyRevision(policy *team.SelfOperatorPolicy) error {
	if policy.PolicyRevision == math.MaxInt64 {
		return usageErrorf("self_operator policy revision overflow")
	}
	policy.PolicyRevision++
	return nil
}

func rejectRunnableOperatorPolicyMutation(projectDir, profile, session string, cfg team.Team) error {
	actor, err := resolveVerifiedCurrentPaneActor(projectDir, profile, session, cfg)
	if err == nil {
		return usageErrorf("verified runnable team actor %q cannot mutate operator self policy; use the human/manual control plane", actor.Handle)
	}
	if !errors.Is(err, errNoVerifiedRosterPane) {
		return usageErrorf("cannot safely determine current-pane policy mutation identity: %v", err)
	}
	return nil
}

func mutateOperatorProfile(projectDir, profile string, mutate func(*team.Team) error) error {
	return withProfileLock(projectDir, profile, func() error {
		cfg, err := team.ReadProfile(projectDir, profile)
		if err != nil {
			return err
		}
		if err := mutate(&cfg); err != nil {
			return err
		}
		if err := team.WriteProfile(projectDir, profile, cfg); err != nil {
			return err
		}
		view := cfg.Operator.SelfOperator
		if view != nil {
			var sessions []string
			for session := range view.Sessions {
				sessions = append(sessions, session)
			}
			sort.Strings(sessions)
			fmt.Printf("operator mode=%s self=%s revision=%d sessions=%s\n", cfg.Operator.InteractionMode, view.LeadRole, view.PolicyRevision, strings.Join(sessions, ","))
		} else {
			fmt.Printf("operator mode=%s\n", cfg.Operator.InteractionMode)
		}
		return nil
	})
}
