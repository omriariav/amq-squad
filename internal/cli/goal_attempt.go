package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// goalAttemptRecord is the single durable unit consumed by either an injected
// binary-specific goal input or its AMQ fallback. Both delivery paths carry AttemptID;
// the first path to claim the record owns activation and the other is a no-op.
//
// gh#761: the runtime `goal claim`/`retry-attempt` subcommands that used to
// create/claim these records were deleted (goal delivery moved to launch-time
// InitialInput), but this type and its readers/paths survive -- durable goal
// attempt/claim records from prior releases (and, until t11's resume rebuild,
// still-referenced attempt/claim paths in resume's own supervision-resume and
// namespace-migration code) remain readable.
type goalAttemptRecord struct {
	SchemaVersion int                `json:"schema_version"`
	AttemptID     string             `json:"attempt_id"`
	Goal          string             `json:"goal"`
	Project       string             `json:"project"`
	Profile       string             `json:"profile"`
	Session       string             `json:"session"`
	Namespace     squadnamespace.Ref `json:"namespace"`
	Role          string             `json:"role"`
	Handle        string             `json:"handle"`
	CreatedAt     time.Time          `json:"created_at"`
}

type goalAttemptClaim struct {
	AttemptID string    `json:"attempt_id"`
	Route     string    `json:"route"`
	ClaimedAt time.Time `json:"claimed_at"`
}

// goalAttemptLink is a package var so tests can substitute a stub without
// touching the real filesystem's hard-link semantics; publishGoalJSON below
// is the sole caller and survives gh#761 (goal_supervision_resume.go and
// recovery_transition_cas.go still publish through it).
var goalAttemptLink = os.Link

func goalAttemptDir(projectDir, profile, session string) string {
	base := filepath.Join(projectDir, team.DirName, "goal-attempts")
	if squadnamespace.NormalizeProfile(profile) != team.DefaultProfile {
		base = filepath.Join(base, squadnamespace.NormalizeProfile(profile))
	}
	return filepath.Join(base, strings.TrimSpace(session))
}

func goalAttemptPath(projectDir, profile, session, attemptID string) (string, error) {
	attemptID = strings.TrimSpace(attemptID)
	if attemptID == "" || attemptID == "." || attemptID == ".." || filepath.Base(attemptID) != attemptID || strings.ContainsAny(attemptID, `/\\`) {
		return "", fmt.Errorf("invalid goal attempt id %q", attemptID)
	}
	return filepath.Join(goalAttemptDir(projectDir, profile, session), attemptID+".json"), nil
}

func goalAttemptClaimPath(attemptPath string) string {
	return strings.TrimSuffix(attemptPath, ".json") + ".claim.json"
}

// publishGoalJSON is the one CAS (compare-and-swap) publication primitive for
// every durable goal-attempt-shaped record in this package: a same-directory
// temp file, fsync'd, then published via link(2) (not O_EXCL) so a second
// writer's publish attempt fails closed with os.ErrExist rather than
// clobbering the first writer. gh#761 deleted this package's own callers
// (goal claim/retry-attempt), but goal_supervision_resume.go and
// recovery_transition_cas.go still publish through this exact function --
// the CAS discipline is shared infrastructure, not delivery-mechanism-specific.
func publishGoalJSON(path string, payload []byte) (published bool, err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, fmt.Errorf("ensure publish dir: %w", err)
	}
	candidate, err := os.CreateTemp(dir, "."+filepath.Base(path)+".candidate-*")
	if err != nil {
		return false, fmt.Errorf("create publish candidate: %w", err)
	}
	candidatePath := candidate.Name()
	defer func() { _ = os.Remove(candidatePath) }()
	if err := candidate.Chmod(0o644); err != nil {
		_ = candidate.Close()
		return false, fmt.Errorf("chmod publish candidate: %w", err)
	}
	if _, err := candidate.Write(payload); err != nil {
		_ = candidate.Close()
		return false, fmt.Errorf("write publish candidate: %w", err)
	}
	if err := candidate.Sync(); err != nil {
		_ = candidate.Close()
		return false, fmt.Errorf("fsync publish candidate: %w", err)
	}
	if err := candidate.Close(); err != nil {
		return false, fmt.Errorf("close publish candidate: %w", err)
	}
	if err := goalAttemptLink(candidatePath, path); errors.Is(err, os.ErrExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("link publish candidate: %w", err)
	}
	if dirHandle, openErr := os.Open(dir); openErr == nil {
		_ = dirHandle.Sync()
		_ = dirHandle.Close()
	}
	return true, nil
}

func readGoalAttempt(path, attemptID string) (goalAttemptRecord, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return goalAttemptRecord{}, fmt.Errorf("read goal attempt %q: %w", attemptID, err)
	}
	var record goalAttemptRecord
	if err := json.Unmarshal(b, &record); err != nil {
		return goalAttemptRecord{}, fmt.Errorf("goal attempt %q is invalid: %w", attemptID, err)
	}
	if record.AttemptID != attemptID {
		return goalAttemptRecord{}, fmt.Errorf("goal attempt id mismatch: record=%q requested=%q", record.AttemptID, attemptID)
	}
	return record, nil
}
