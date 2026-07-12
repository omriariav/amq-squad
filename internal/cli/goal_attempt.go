package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// goalAttemptRecord is the single durable unit consumed by either an injected
// native /goal prompt or its AMQ fallback. Both delivery paths carry AttemptID;
// the first path to claim the record owns activation and the other is a no-op.
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

type goalAttemptClaimData struct {
	Project       string             `json:"project"`
	Profile       string             `json:"profile"`
	Session       string             `json:"session"`
	Namespace     squadnamespace.Ref `json:"namespace"`
	AttemptID     string             `json:"attempt_id"`
	Route         string             `json:"route"`
	Status        string             `json:"status"`
	ExistingRoute string             `json:"existing_route,omitempty"`
	ClaimedAt     time.Time          `json:"claimed_at,omitempty"`
	ClaimPath     string             `json:"claim_path,omitempty"`
	RecoveryCmd   string             `json:"recovery_command,omitempty"`
}

type invalidExistingGoalClaimError struct {
	Status string
	Path   string
	Detail string
}

func (e *invalidExistingGoalClaimError) Error() string {
	return fmt.Sprintf("goal claim status %s: canonical claim %s is invalid (%s); activation refused", e.Status, e.Path, e.Detail)
}

var (
	goalAttemptNow    = func() time.Time { return time.Now().UTC() }
	goalAttemptCreate = createGoalAttempt
	goalAttemptLink   = os.Link
)

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

func createGoalAttempt(opts goalDeliveryOptions, attemptID string, now time.Time) (string, error) {
	path, err := goalAttemptPath(opts.Project, opts.Profile, opts.Session, attemptID)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("ensure goal attempt dir: %w", err)
	}
	record := goalAttemptRecord{
		SchemaVersion: 1,
		AttemptID:     attemptID,
		Goal:          opts.Goal,
		Project:       opts.Project,
		Profile:       squadnamespace.NormalizeProfile(opts.Profile),
		Session:       opts.Session,
		Namespace:     opts.Namespace,
		Role:          opts.Role,
		Handle:        opts.Member.Handle,
		CreatedAt:     now,
	}
	b, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal goal attempt: %w", err)
	}
	published, err := publishGoalJSON(path, append(b, '\n'))
	if err != nil {
		return "", fmt.Errorf("publish goal attempt: %w", err)
	}
	if !published {
		return "", fmt.Errorf("publish goal attempt: canonical record already exists at %s", path)
	}
	return path, nil
}

// publishGoalJSON writes and fsyncs a same-directory candidate before linking
// it into the canonical path. link(2) is the atomic no-replace publication:
// losers see ErrExist but can never observe an empty/partial canonical file.
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

// claimGoalAttempt uses atomic no-replace publication as the cross-process
// compare-and-swap. This is deliberately at-most-once: a winner that crashes
// after publishing but before activation can lose that activation, but a
// second route can never activate the same attempt again.
func claimGoalAttempt(projectDir, profile, session, attemptID, route string, now time.Time) (bool, goalAttemptClaim, error) {
	path, err := goalAttemptPath(projectDir, profile, session, attemptID)
	if err != nil {
		return false, goalAttemptClaim{}, err
	}
	if _, err := readGoalAttempt(path, attemptID); err != nil {
		return false, goalAttemptClaim{}, err
	}
	route = strings.ToLower(strings.TrimSpace(route))
	if route != "native" && route != "amq" {
		return false, goalAttemptClaim{}, fmt.Errorf("goal attempt route must be native or amq")
	}
	claim := goalAttemptClaim{AttemptID: attemptID, Route: route, ClaimedAt: now}
	b, err := json.MarshalIndent(claim, "", "  ")
	if err != nil {
		return false, goalAttemptClaim{}, fmt.Errorf("marshal goal attempt claim: %w", err)
	}
	claimPath := goalAttemptClaimPath(path)
	published, err := publishGoalJSON(claimPath, append(b, '\n'))
	if err != nil {
		return false, goalAttemptClaim{}, fmt.Errorf("publish goal attempt claim: %w", err)
	}
	if !published {
		existingBytes, readErr := os.ReadFile(claimPath)
		if readErr != nil {
			return false, goalAttemptClaim{}, &invalidExistingGoalClaimError{Status: "invalid_existing_claim", Path: claimPath, Detail: readErr.Error()}
		}
		var existing goalAttemptClaim
		if err := json.Unmarshal(existingBytes, &existing); err != nil {
			return false, goalAttemptClaim{}, &invalidExistingGoalClaimError{Status: "invalid_existing_claim", Path: claimPath, Detail: "malformed JSON: " + err.Error()}
		}
		if existing.AttemptID != attemptID {
			return false, goalAttemptClaim{}, &invalidExistingGoalClaimError{Status: "invalid_existing_claim", Path: claimPath, Detail: fmt.Sprintf("attempt_id mismatch: got %q want %q", existing.AttemptID, attemptID)}
		}
		if existing.Route != "native" && existing.Route != "amq" {
			return false, goalAttemptClaim{}, &invalidExistingGoalClaimError{Status: "invalid_existing_claim", Path: claimPath, Detail: fmt.Sprintf("invalid route %q", existing.Route)}
		}
		return false, existing, nil
	}
	return true, claim, nil
}

func runGoalClaim(args []string) error {
	fs := flag.NewFlagSet("goal claim", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile (default: default profile)")
	sessionFlag := fs.String("session", "", "workstream session containing the goal attempt")
	attemptFlag := fs.String("attempt-id", "", "claim-once goal delivery attempt id")
	routeFlag := fs.String("route", "", "activation route: native or amq")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned claim result")
	registerScopedFlagAliases(fs, projectFlag, sessionFlag, profileFlag)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad goal claim - atomically claim one delivered goal attempt

Usage:
  amq-squad goal claim --attempt-id ID --route native|amq --session S [--profile P] [--project DIR] [--json]

Exactly one route can claim an attempt. This is an at-most-once contract: a
claimant crash before activation may lose the activation, but a second route
can never activate the same attempt. A second claim exits successfully with
status already_claimed, prints the canonical claim evidence and a new-attempt
re-delivery command, and MUST be treated as a no-op by the goal runtime.
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("goal claim takes no positional arguments")
	}
	session := strings.TrimSpace(*sessionFlag)
	if session == "" {
		return usageErrorf("goal claim requires --session")
	}
	if err := team.ValidateSessionName(session); err != nil {
		return usageErrorf("invalid --session: %v", err)
	}
	if strings.TrimSpace(*attemptFlag) == "" {
		return usageErrorf("goal claim requires --attempt-id")
	}
	projectDir, profile, err := resolveProjectProfile(*projectFlag, *profileFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	attemptID := strings.TrimSpace(*attemptFlag)
	attemptPath, err := goalAttemptPath(projectDir, profile, session, attemptID)
	if err != nil {
		return err
	}
	record, err := readGoalAttempt(attemptPath, attemptID)
	if err != nil {
		return err
	}
	claimed, existing, err := claimGoalAttempt(projectDir, profile, session, attemptID, *routeFlag, goalAttemptNow())
	if err != nil {
		return err
	}
	status := "already_claimed"
	if claimed {
		status = "claimed"
	}
	data := goalAttemptClaimData{
		Project:   projectDir,
		Profile:   profile,
		Session:   session,
		Namespace: squadnamespace.Resolve(projectDir, profile, session),
		AttemptID: attemptID,
		Route:     strings.ToLower(strings.TrimSpace(*routeFlag)),
		Status:    status,
		ClaimedAt: existing.ClaimedAt,
		ClaimPath: goalAttemptClaimPath(attemptPath),
	}
	if !claimed {
		data.ExistingRoute = existing.Route
		data.RecoveryCmd = goalAttemptRecoveryCommand(record)
	}
	if *jsonOut {
		return printJSONEnvelope("goal_claim", data)
	}
	if claimed {
		fmt.Printf("claimed goal attempt %s via %s (at-most-once evidence: %s)\n", data.AttemptID, data.Route, data.ClaimPath)
	} else {
		fmt.Printf("goal attempt %s already claimed via %s at %s; no-op\n", data.AttemptID, data.ExistingRoute, data.ClaimedAt.Format(time.RFC3339Nano))
		fmt.Printf("claim evidence: %s\n", data.ClaimPath)
		fmt.Printf("If the claim exists but no goal activated, inspect the evidence and re-deliver as a new attempt:\n  %s\n", data.RecoveryCmd)
	}
	return nil
}

func goalAttemptRecoveryCommand(record goalAttemptRecord) string {
	args := []string{
		"amq-squad", "goal", "deliver",
		"--project", record.Project,
		"--profile", record.Profile,
		"--session", record.Session,
		"--role", record.Role,
		"--goal", record.Goal,
	}
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}
