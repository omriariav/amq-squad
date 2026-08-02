package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/omriariav/amq-squad/v2/internal/launch"
)

// Launch-attempt evidence for the doctor bootstrap row (#598 root cause 3).
//
// doctor reported `bootstrap/<role>: no launch record; skipped` as OK even when
// a launch had just been attempted for that role and the agent had died. During
// the fresh-namespace brick that made doctor report every row ok while the
// namespace was unusable, so no sanctioned diagnostic surfaced the cause.
//
// Turning that row into a failure needs a predicate, because plenty of members
// legitimately have no launch record: never launched, staged but not started,
// an external lead adopted in a pane amq-squad did not spawn. Failing those
// would make doctor noisy for people who did nothing wrong, which is its own
// way of hiding a real problem.
//
// The predicate is the prepared launch RESERVATION. A reservation is durable,
// positive proof that a launch was reserved for this exact namespace and
// generation. No reservation means nothing was attempted and silence is honest.
// A reservation means silence is a lie.

// preparedLaunchAttemptEvidence is the proof that a launch was reserved for a
// namespace, and where that proof lives so the operator can go look at it.
type preparedLaunchAttemptEvidence struct {
	Generation string
	Path       string
}

// findPreparedLaunchAttempt reports the most recent generation of this
// namespace that recorded a launch reservation.
//
// It is deliberately read-only and failure-tolerant: any unreadable prepared
// tree yields "no evidence", which keeps the bootstrap row at its historical
// skip rather than inventing a failure out of a directory listing error. The
// escalation this feeds must only ever fire on positive proof.
func findPreparedLaunchAttempt(project, profile, session string) (preparedLaunchAttemptEvidence, bool) {
	generationsRoot := preparedRunGenerationsPath(project, profile, session)
	entries, err := os.ReadDir(generationsRoot)
	if err != nil {
		return preparedLaunchAttemptEvidence{}, false
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	// Newest-looking generation first, so a namespace relaunched several times
	// reports the reservation an operator is most likely to be looking at.
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for _, generation := range names {
		path := preparedRunReservationPath(project, profile, session, generation)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return preparedLaunchAttemptEvidence{Generation: generation, Path: path}, true
		}
	}
	return preparedLaunchAttemptEvidence{}, false
}

// preparedInitialRosterRoles reports the accepted initial roster of the
// generation that reserved the launch. A member outside it was never part of
// that accepted launch, so the reservation says nothing about whether that
// member should have a record.
//
// This reads the generation's own manifest directly rather than going through
// readPreparedRunManifest, and that is deliberate rather than a shortcut.
// readPreparedRunManifest resolves the CURRENT pointer and validates manifest
// and state digests, so it fails whenever preparation has drifted -- which is
// precisely the #598 situation this row exists to report. Depending on it would
// make the escalation go silent exactly when it matters most, reintroducing the
// defect one layer down. The roster is only used to SCOPE the failure to
// accepted members, never as authority, so an unvalidated read is the right
// tool: it answers "was this member part of the launch that was reserved" from
// the launch that was actually reserved.
//
// An unreadable generation manifest yields no roles, which collapses the
// escalation to the historical skip.
func preparedInitialRosterRoles(project, profile, session, generation string) map[string]bool {
	data, err := os.ReadFile(preparedRunGenerationManifestPath(project, profile, session, generation))
	if err != nil {
		return nil
	}
	var manifest struct {
		InitialRoster []string `json:"initial_roster"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil
	}
	roles := make(map[string]bool, len(manifest.InitialRoster))
	for _, role := range manifest.InitialRoster {
		roles[role] = true
	}
	return roles
}

// bootstrapRecordAbsence distinguishes a launch record that is missing from one
// that is present but unreadable. Absence after a reserved launch means the
// agent died before writing its record; a malformed record means it wrote one
// and something corrupted it. Both are failures once a launch was reserved, and
// they need different remedies, so they must not share a message.
func bootstrapRecordAbsence(agentDir string) string {
	if launch.HasRecord(agentDir) {
		return "malformed"
	}
	return "absent"
}

// bootstrapLaunchRecordPath names where the record was expected, so a failure
// tells the operator the exact path to inspect instead of implying they should
// go read the source.
func bootstrapLaunchRecordPath(agentDir string) string {
	return filepath.Clean(launch.Path(agentDir))
}
