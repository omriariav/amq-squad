package cli

import (
	"fmt"
	"path/filepath"

	"github.com/omriariav/amq-squad/v3/internal/team"
)

// teamLaunchOptions carries the inputs the kept-dead launchapi backend
// (team_launch_launchapi.go) reads. 3.0 (2a-2): the v2 struct also held the
// tmux, terminal, layout, seeding and external-lead fields of the cut team
// launch driver; only the fields the launchapi ancestor uses remain.
type teamLaunchOptions struct {
	Workstream     string
	Profile        string
	DryRun         bool
	BinaryArgs     map[string][]string
	Trust          string
	ModelOverrides map[string]string
	ForceDuplicate bool
	NoGitignore    bool
	WakeInjectVia  string
	WakeInjectArgs []string
	WakeInjectMode string
	StartupPrompts map[string]string
	// RestoreConversations and GoalPrompts: a seat with a recorded
	// conversation resumes; otherwise its recovered goal rides on the fresh
	// launch's InitialInput (gh#758/t11).
	RestoreConversations map[string]string
	GoalPrompts          map[string]string
	// LaunchapiDecisions: explicit operator answers (ACTION_ID -> CHOICE) to
	// launchapi RequiredActionV1 gates; nil means every action surfaces as a gate.
	LaunchapiDecisions map[string]string
	// ExpectedSubjectDigest binds Apply to a previously printed
	// PrepareResultV1.SubjectDigest (gh#757); empty means no binding.
	ExpectedSubjectDigest string
}

// resolveTeamLaunchAMQEnv is the single injectable resolution seam for parent
// team launch/reset paths. Every production call validates the returned AMQ
// version before any persistent mutation or dry-run preview.
var resolveTeamLaunchAMQEnv = resolveAMQEnvForTeamLaunchProfile

// buildTeamPreflights computes the agent-identity tuples team launch would
// produce so preflightTeam can refuse before any pane is created. dryRun
// passes through to each preflight so a --dry-run team launch never mutates
// disk state during stale-artifact handling.
func buildTeamPreflights(t team.Team, opts teamLaunchOptions) ([]agentLaunchPreflight, error) {
	members := orderedTeamMembers(t.Members)
	out := make([]agentLaunchPreflight, 0, len(members))
	for _, m := range members {
		cwd := m.EffectiveCWD(t.Project)
		env, err := resolveTeamLaunchAMQEnv(cwd, opts.Profile, opts.Workstream, m.Handle)
		if err != nil {
			return nil, fmt.Errorf("resolve amq env for %s: %w", m.Handle, err)
		}
		floorViolation := ""
		if err := validateLaunchAMQVersion(env.AMQVersion); err != nil {
			if !opts.DryRun {
				return nil, fmt.Errorf("%s: %w", m.Handle, err)
			}
			floorViolation = err.Error()
		}
		root := absoluteAMQRoot(cwd, env.Root)
		handle := m.Handle
		if env.Me != "" {
			handle = env.Me
		}
		agentDir := filepath.Join(root, "agents", handle)
		out = append(out, agentLaunchPreflight{
			Role:              m.Role,
			CWD:               cwd,
			AgentDir:          agentDir,
			Handle:            handle,
			Workstream:        env.SessionName,
			Root:              root,
			BaseRoot:          absoluteAMQRoot(cwd, env.BaseRoot),
			RootSource:        env.RootSource,
			Binary:            m.Binary,
			AMQVersion:        env.AMQVersion,
			AMQFloorViolation: floorViolation,
			Force:             opts.ForceDuplicate,
			DryRun:            opts.DryRun,
		})
	}
	return out, nil
}
