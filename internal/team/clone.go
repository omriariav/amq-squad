package team

import (
	"encoding/json"
	"fmt"
	"time"
)

// CloneRosterForSession builds the Team for a new session-pinned profile by
// copying an existing profile's roster shape (members, binaries, models,
// efforts, lead, launch shape, trust) and restamping every member to
// newSession (#523). Goal binding, brief, prepared generations, and launch
// records are never fields on Team, so a roster-level copy can never carry
// them forward; the deprecated legacy Workstream shim, schema, project path,
// and creation timestamp are always reset for the new profile instead of
// copied, matching the same-shape defaults `team init` produces for a fresh
// roster.
//
// A cloned profile drops any v2 operator.self_operator residue (3.0 rejects
// it). GoalSupervisionPolicy also resets:
// safe-auto is per profile/run and must never carry into a new session without
// an explicit operator choice.
func CloneRosterForSession(source Team, newSession string) (Team, error) {
	if err := ValidateSessionName(newSession); err != nil {
		return Team{}, fmt.Errorf("invalid session for cloned roster: %w", err)
	}
	b, err := json.Marshal(source)
	if err != nil {
		return Team{}, fmt.Errorf("clone roster: %w", err)
	}
	var clone Team
	if err := json.Unmarshal(b, &clone); err != nil {
		return Team{}, fmt.Errorf("clone roster: %w", err)
	}
	clone.Project = ""
	clone.Workstream = ""
	clone.Schema = 0
	clone.CreatedAt = time.Time{}
	for i := range clone.Members {
		clone.Members[i].Session = newSession
	}
	if clone.Operator != nil {
		clone.Operator.SelfOperator = nil
	}
	clone.GoalSupervision = nil
	return clone, nil
}
