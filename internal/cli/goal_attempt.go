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
}

var (
	goalAttemptNow    = func() time.Time { return time.Now().UTC() }
	goalAttemptCreate = createGoalAttempt
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
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", fmt.Errorf("create goal attempt: %w", err)
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("write goal attempt: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close goal attempt: %w", err)
	}
	return path, nil
}

// claimGoalAttempt uses O_EXCL as the cross-process compare-and-swap. A second
// native/AMQ activation sees the existing claim and must become a no-op.
func claimGoalAttempt(projectDir, profile, session, attemptID, route string, now time.Time) (bool, goalAttemptClaim, error) {
	path, err := goalAttemptPath(projectDir, profile, session, attemptID)
	if err != nil {
		return false, goalAttemptClaim{}, err
	}
	if _, err := os.Stat(path); err != nil {
		return false, goalAttemptClaim{}, fmt.Errorf("read goal attempt %q: %w", attemptID, err)
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
	claimPath := strings.TrimSuffix(path, ".json") + ".claim.json"
	f, err := os.OpenFile(claimPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		existingBytes, readErr := os.ReadFile(claimPath)
		if readErr != nil {
			return false, goalAttemptClaim{}, fmt.Errorf("read existing goal attempt claim: %w", readErr)
		}
		var existing goalAttemptClaim
		if err := json.Unmarshal(existingBytes, &existing); err != nil {
			return false, goalAttemptClaim{}, fmt.Errorf("parse existing goal attempt claim: %w", err)
		}
		return false, existing, nil
	}
	if err != nil {
		return false, goalAttemptClaim{}, fmt.Errorf("claim goal attempt: %w", err)
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		_ = f.Close()
		_ = os.Remove(claimPath)
		return false, goalAttemptClaim{}, fmt.Errorf("write goal attempt claim: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(claimPath)
		return false, goalAttemptClaim{}, fmt.Errorf("close goal attempt claim: %w", err)
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

Exactly one route can claim an attempt. A second claim exits successfully with
status already_claimed and MUST be treated as a no-op by the goal runtime.
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
	claimed, existing, err := claimGoalAttempt(projectDir, profile, session, *attemptFlag, *routeFlag, goalAttemptNow())
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
		AttemptID: strings.TrimSpace(*attemptFlag),
		Route:     strings.ToLower(strings.TrimSpace(*routeFlag)),
		Status:    status,
	}
	if !claimed {
		data.ExistingRoute = existing.Route
	}
	if *jsonOut {
		return printJSONEnvelope("goal_claim", data)
	}
	if claimed {
		fmt.Printf("claimed goal attempt %s via %s\n", data.AttemptID, data.Route)
	} else {
		fmt.Printf("goal attempt %s already claimed via %s; no-op\n", data.AttemptID, data.ExistingRoute)
	}
	return nil
}
