package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/team"
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
)

// rosterPreparedAcceptance reports whether a roster mutation replaced an
// already-ready prepared generation. Warning is non-nil when the roster write
// succeeded but the replacement generation could not be prepared; callers
// retain the historical successful-mutation behavior and surface the recovery
// guidance instead of silently claiming readiness.
type rosterPreparedAcceptance struct {
	Refreshed bool
	Warning   error
}

var rosterPreparedBeforeMutation = func() {}

// mutateRosterWithPreparedAcceptance serializes a one-step roster mutation
// against live prepared launches. When the affected namespace has a valid,
// ready accepted generation before the mutation, the same goal, launch shape,
// topology, and environment contract are republished against the new roster
// before the admission locks are released.
//
// This deliberately does not create accepted state for an unprepared or
// already-drifted namespace. In those cases the roster command keeps its
// historical behavior and the existing preparation workflow remains the
// authority boundary.
func mutateRosterWithPreparedAcceptance(project, profile, session string, mutate func(expectedProfileDigest string) error) (rosterPreparedAcceptance, error) {
	session = strings.TrimSpace(session)
	if session == "" {
		return rosterPreparedAcceptance{}, mutate("")
	}
	manifestPath := preparedRunPath(project, profile, session)
	var outcome rosterPreparedAcceptance
	_, err := executeRunPreparationTransaction(project, profile, session, []string{manifestPath}, manifestPath, func() (runReadinessResult, error) {
		if _, statErr := os.Stat(manifestPath); statErr != nil {
			if os.IsNotExist(statErr) {
				return runReadinessResult{}, mutate("")
			}
			return runReadinessResult{}, fmt.Errorf("inspect accepted preparation before roster mutation: %w", statErr)
		}

		manifest, _, readErr := readPreparedRunManifestSnapshot(project, profile, session)
		eligible := false
		context := acceptedRunContext{}
		if readErr == nil {
			context = acceptedRunContext{Version: manifest.Environment.BinaryVersion, Topology: manifest.Topology}
			eligible = calculateRunReadinessWithContext(project, profile, session, context).Ready
		} else {
			outcome.Warning = fmt.Errorf("inspect current accepted preparation: %w", readErr)
		}
		expectedProfileDigest := ""
		if eligible {
			expectedProfileDigest = manifest.ArtifactDigests["profile"]
		}
		rosterPreparedBeforeMutation()
		if err := mutate(expectedProfileDigest); err != nil {
			return runReadinessResult{}, err
		}
		if !eligible {
			return runReadinessResult{}, nil
		}

		staged := append([]string(nil), manifest.StagedRoster...)
		if manifest.LaunchShape == runwizard.LaunchShapeLeadOnlyStaged {
			current, err := team.ReadProfile(project, profile)
			if err != nil {
				outcome.Warning = fmt.Errorf("refresh accepted preparation after roster mutation: read team: %w", err)
				return runReadinessResult{}, nil
			}
			staged = staged[:0]
			for _, member := range current.Members {
				if (member.Session == "" || member.Session == session) && member.Role != current.Lead {
					staged = append(staged, member.Role)
				}
			}
		}

		result, prepareErr := prepareRunArtifacts(
			project,
			profile,
			session,
			manifest.LaunchShape,
			strings.Join(staged, ","),
			manifest.GoalText,
			manifest.GoalSource,
			manifest.GoalDigest,
			"",
			context,
			true,
		)
		if prepareErr != nil {
			outcome.Warning = fmt.Errorf("refresh accepted preparation after roster mutation: %w", prepareErr)
			return runReadinessResult{}, nil
		}
		outcome.Refreshed = true
		return result, nil
	})
	return outcome, err
}

func verifyAcceptedProfileDigestBeforeRosterMutation(profilePath, expected string) error {
	if strings.TrimSpace(expected) == "" {
		return nil
	}
	current, err := digestFile(profilePath)
	if err != nil {
		return fmt.Errorf("verify team profile before roster mutation: %w", err)
	}
	if current != expected {
		return fmt.Errorf("team profile changed after accepted-readiness verification; retry the roster edit")
	}
	return nil
}

func printRosterPreparedAcceptance(outcome rosterPreparedAcceptance, profile, session string, jsonOut bool) {
	if outcome.Refreshed && !jsonOut {
		fmt.Printf("accepted preparation refreshed for %s/%s; no separate prepare/accept round trip is required.\n", profile, session)
	}
	if outcome.Warning != nil {
		fmt.Fprintf(os.Stderr, "warning: roster changed, but its accepted preparation was not refreshed: %v\n", outcome.Warning)
		fmt.Fprintf(os.Stderr, "run preparation again before launching %s/%s.\n", profile, session)
	}
}
