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

	"github.com/omriariav/amq-squad/v2/internal/flock"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
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
	goalAttemptNow         = func() time.Time { return time.Now().UTC() }
	goalAttemptCreate      = createGoalAttempt
	goalAttemptLink        = os.Link
	goalBeforeRetrySendCAS = func() {}
)

type goalRetryCASSnapshot struct {
	TeamDigest    string
	TeamModTime   int64
	LaunchDigest  string
	LaunchModTime int64
}

// goalRetryPostSendIdentityError reports an identity/generation change that
// happened while the native retry was in flight. The prompt may already be in
// the pane, so creating another attempt or retrying blindly would be unsafe.
type goalRetryPostSendIdentityError struct {
	AttemptID string
	Cause     error
}

func (e *goalRetryPostSendIdentityError) Error() string {
	return fmt.Sprintf("goal retry-attempt %s was sent but current identity/generation changed afterward: %v; do not create or retry another attempt", e.AttemptID, e.Cause)
}

func (e *goalRetryPostSendIdentityError) Unwrap() error   { return e.Cause }
func (e *goalRetryPostSendIdentityError) RetrySafe() bool { return false }

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

// runGoalRetryAttempt is the narrow recovery path used after resume has
// already launched a lead and reserved a new attempt but pane delivery failed.
// It reuses that exact durable attempt and can never create a third one.
func runGoalRetryAttempt(args []string) error {
	fs := flag.NewFlagSet("goal retry-attempt", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile (default: default profile)")
	sessionFlag := fs.String("session", "", "workstream containing the recorded attempt")
	roleFlag := fs.String("role", "", "lead role whose launch binding reserves the attempt")
	attemptFlag := fs.String("attempt-id", "", "existing unclaimed goal attempt id")
	yes := fs.Bool("yes", false, "confirm same-attempt pane delivery without an interactive prompt")
	registerScopedFlagAliases(fs, projectFlag, sessionFlag, profileFlag)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad goal retry-attempt - recover one reserved goal delivery

Usage:
  amq-squad goal retry-attempt --project DIR --profile P --session S --role ROLE --attempt-id ID --yes

This recovery command never creates or resets an attempt. It verifies that the
current lead binding points to the same durable, still-unclaimed attempt, then
re-delivers that exact claim-once control prompt.
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("goal retry-attempt takes no positional arguments")
	}
	if strings.TrimSpace(*sessionFlag) == "" || strings.TrimSpace(*attemptFlag) == "" {
		return usageErrorf("goal retry-attempt requires --session and --attempt-id")
	}
	if !*yes {
		return usageErrorf("goal retry-attempt requires --yes after inspecting the reserved attempt")
	}
	opts, err := resolveGoalTargetOptions(*projectFlag, *profileFlag, *sessionFlag, *roleFlag, flagWasSet(fs, "project"), flagWasSet(fs, "profile"), true, "goal retry-attempt", namespaceConflictOverrideOptions{})
	if err != nil {
		return err
	}
	attemptID := strings.TrimSpace(*attemptFlag)
	if err := os.MkdirAll(goalAttemptDir(opts.Project, opts.Profile, opts.Session), 0o755); err != nil {
		return err
	}
	return flock.WithLock(goalDeliveryLockPath(opts), func() error {
		return runGoalRetryAttemptLocked(opts, attemptID)
	})
}

func runGoalRetryAttemptLocked(opts goalDeliveryOptions, attemptID string) error {
	currentTeam, err := team.ReadProfile(opts.Project, opts.Profile)
	if err != nil {
		return fmt.Errorf("goal retry-attempt refused: reread team: %w", err)
	}
	lead := strings.TrimSpace(currentTeam.Lead)
	if lead == "" && len(currentTeam.Members) == 1 {
		lead = currentTeam.Members[0].Role
	}
	member, ok := teamMemberByRole(currentTeam, opts.Role)
	if !ok || lead != opts.Role || memberHandle(member) != opts.Member.Handle {
		return fmt.Errorf("goal retry-attempt refused: role is no longer the exact current lead")
	}
	if pinned := strings.TrimSpace(member.Session); pinned != "" && pinned != opts.Session {
		return fmt.Errorf("goal retry-attempt refused: lead session pin changed")
	}
	opts.Team = currentTeam
	opts.Member = member
	opts.Namespace = squadnamespace.Resolve(opts.Project, opts.Profile, opts.Session)
	attemptPath, err := goalAttemptPath(opts.Project, opts.Profile, opts.Session, attemptID)
	if err != nil {
		return err
	}
	attempt, err := readGoalAttempt(attemptPath, attemptID)
	if err != nil {
		return err
	}
	if err := validateResumeGoalAttempt(attempt, opts.Project, opts.Profile, opts.Session, opts.Role, opts.Member.Handle, attempt.Goal, attemptID, opts.Namespace); err != nil {
		return fmt.Errorf("goal retry-attempt refused: recorded attempt mismatch: %w", err)
	}
	claimPath := goalAttemptClaimPath(attemptPath)
	if claimBytes, claimErr := os.ReadFile(claimPath); claimErr == nil {
		var claim goalAttemptClaim
		if err := json.Unmarshal(claimBytes, &claim); err != nil {
			return fmt.Errorf("goal retry-attempt refused: existing claim is corrupt")
		}
		if err := validateResumeGoalClaim(claim, attempt); err != nil {
			return fmt.Errorf("goal retry-attempt refused: existing claim is invalid: %w", err)
		}
		return usageErrorf("goal retry-attempt refused: attempt %s is already claimed via %s", attemptID, claim.Route)
	} else if !os.IsNotExist(claimErr) {
		return fmt.Errorf("goal retry-attempt refused: inspect claim: %w", claimErr)
	}
	mr, resolvedWorkstream, err := resolveMemberRuntime(opts.Project, opts.Profile, opts.Session, true, opts.Role)
	if err != nil {
		return err
	}
	if !mr.HasRecord || mr.Record.GoalBinding == nil {
		return fmt.Errorf("goal retry-attempt refused: current lead launch has no reserved goal binding")
	}
	ns := opts.Namespace
	rec := mr.Record
	if rec.Role != opts.Role || rec.Handle != memberHandle(member) || rec.Session != opts.Session ||
		!squadnamespace.ProfilesEqual(rec.TeamProfile, opts.Profile) || canonicalPath(rec.TeamHome) != canonicalPath(opts.Project) ||
		canonicalPath(rec.Root) != canonicalPath(ns.AMQRoot) || canonicalPath(rec.CWD) != canonicalPath(member.EffectiveCWD(currentTeam.Project)) ||
		rec.Binary != member.Binary || rec.Conversation != "" || rec.BootstrapExpectation == nil || !rec.BootstrapExpectation.Required ||
		strings.TrimSpace(rec.BootstrapExpectation.LaunchID) == "" || rec.StartedAt.IsZero() || rec.Tmux == nil || rec.Tmux.Target == "adopted" {
		return fmt.Errorf("goal retry-attempt refused: current launch identity is not the exact fresh lead launch")
	}
	if mr.Record.GoalBinding.Mode != "native_goal" || !mr.Record.GoalBinding.NativeGoal || mr.Record.GoalBinding.Source != "goal-control" {
		return fmt.Errorf("goal retry-attempt refused: current lead binding is not a native goal-control reservation")
	}
	goal, boundAttemptID, err := parseGeneratedGoalBinding(mr.Record.GoalBinding.Command)
	if err != nil || boundAttemptID != attemptID || goal != attempt.Goal {
		return fmt.Errorf("goal retry-attempt refused: current lead binding does not match attempt %s", attemptID)
	}
	if expected := nativeGoalControlPrompt(goal, opts.Team, opts.Profile, opts.Session, opts.Role, attemptID); mr.Record.GoalBinding.Command != expected {
		return fmt.Errorf("goal retry-attempt refused: current lead binding is not the exact generated command")
	}
	teamDigest, teamMod, err := readGoalFileGeneration(team.ProfilePath(opts.Project, opts.Profile))
	if err != nil {
		return fmt.Errorf("goal retry-attempt refused: capture team generation: %w", err)
	}
	launchDigest, launchMod, err := readGoalFileGeneration(launch.ExistingPath(mr.AgentDir))
	if err != nil {
		return fmt.Errorf("goal retry-attempt refused: capture launch generation: %w", err)
	}
	cas := goalRetryCASSnapshot{TeamDigest: teamDigest, TeamModTime: teamMod, LaunchDigest: launchDigest, LaunchModTime: launchMod}
	if reason, disabled := mr.nativePromptInjectionDisabledReason(); disabled {
		return fmt.Errorf("%s", reason)
	}
	// The exact generation/identity check is intentionally a short critical
	// section. Pane discovery, native input, AMQ fallback, and output run only
	// after the profile and launch-record locks are released.
	var sendRuntime memberRuntime
	if err := withGoalIdentityWriterLocks(opts, mr.AgentDir, func() error {
		sendRuntime, err = validateGoalRetrySendCAS(opts, attempt, attemptID, cas)
		if err != nil {
			return err
		}
		goalBeforeRetrySendCAS()
		return nil
	}); err != nil {
		return err
	}
	panes, err := statusPaneLister()
	if err != nil {
		if tmuxpane.IsPermissionDenied(err) {
			return errTmuxAccessDenied()
		}
		panes = nil
	}
	paneID, _, ok := resolveControlTarget(sendRuntime, resolvedWorkstream, panes)
	if !ok || strings.TrimSpace(paneID) == "" {
		return fmt.Errorf("goal retry-attempt refused: exact live pane disappeared before send")
	}
	// Claim publication does not share the delivery lock, so re-read at the
	// last possible point before pane mutation. This check is outside the
	// identity locks by design: the claim protocol itself is the at-most-once
	// arbiter, and no writer may be stalled behind tmux work.
	if claimBytes, claimErr := os.ReadFile(claimPath); claimErr == nil {
		var claim goalAttemptClaim
		if json.Unmarshal(claimBytes, &claim) != nil || validateResumeGoalClaim(claim, attempt) != nil {
			return fmt.Errorf("goal retry-attempt refused: claim changed to invalid evidence before delivery")
		}
		return usageErrorf("goal retry-attempt refused: attempt %s became claimed via %s before delivery", attemptID, claim.Route)
	} else if !os.IsNotExist(claimErr) {
		return fmt.Errorf("goal retry-attempt refused: recheck claim: %w", claimErr)
	}
	opts.Goal = attempt.Goal
	opts.AttemptID = attemptID
	if err := sendPromptToPane(paneID, sendRuntime.Record.GoalBinding.Command); err != nil {
		var queued *tmuxpane.QueuedInputError
		if errors.As(err, &queued) {
			fmt.Fprintf(os.Stderr, "warning: existing goal attempt %s is queued in the lead input; do not retry again\n", attemptID)
			return nil
		}
		var unconfirmed *tmuxpane.SubmitUnconfirmedError
		if errors.As(err, &unconfirmed) {
			fallback, fallbackErr := goalFallbackAMQSend(opts)
			if fallbackErr != nil {
				return &goalFallbackDurabilityError{DeliveryErr: err, FallbackErr: fallbackErr}
			}
			fmt.Fprintf(os.Stderr, "warning: native retry was unconfirmed; durable fallback %s reuses attempt %s\n", fallback.MessageID, attemptID)
			return nil
		}
		return err
	}
	if err := validateGoalRetryAfterExternalSend(opts, sendRuntime.AgentDir, attempt, attemptID, cas); err != nil {
		return &goalRetryPostSendIdentityError{AttemptID: attemptID, Cause: err}
	}
	fmt.Printf("Re-delivered existing goal attempt %s to %s; no new attempt was created.\n", attemptID, opts.Role)
	return nil
}

func validateGoalRetryAfterExternalSend(opts goalDeliveryOptions, agentDir string, attempt goalAttemptRecord, attemptID string, expected goalRetryCASSnapshot) error {
	return withGoalIdentityWriterLocks(opts, agentDir, func() error {
		_, err := validateGoalRetrySendCAS(opts, attempt, attemptID, expected)
		return err
	})
}

func validateGoalRetrySendCAS(opts goalDeliveryOptions, attempt goalAttemptRecord, attemptID string, expected goalRetryCASSnapshot) (memberRuntime, error) {
	teamDigest, teamMod, err := readGoalFileGeneration(team.ProfilePath(opts.Project, opts.Profile))
	if err != nil || teamDigest != expected.TeamDigest || teamMod != expected.TeamModTime {
		return memberRuntime{}, fmt.Errorf("goal retry-attempt refused: team generation changed immediately before send")
	}
	currentTeam, err := team.ReadProfile(opts.Project, opts.Profile)
	if err != nil {
		return memberRuntime{}, fmt.Errorf("goal retry-attempt refused: reread team: %w", err)
	}
	lead := strings.TrimSpace(currentTeam.Lead)
	if lead == "" && len(currentTeam.Members) == 1 {
		lead = currentTeam.Members[0].Role
	}
	member, ok := teamMemberByRole(currentTeam, opts.Role)
	if !ok || lead != opts.Role || memberHandle(member) != opts.Member.Handle || canonicalPath(currentTeam.Project) != canonicalPath(opts.Project) ||
		member.Session != opts.Member.Session || (member.Session != "" && member.Session != opts.Session) || member.Binary != opts.Member.Binary || canonicalPath(member.EffectiveCWD(currentTeam.Project)) != canonicalPath(opts.Member.EffectiveCWD(opts.Project)) {
		return memberRuntime{}, fmt.Errorf("goal retry-attempt refused: lead team identity changed immediately before send")
	}
	mr, _, err := resolveMemberRuntime(opts.Project, opts.Profile, opts.Session, true, opts.Role)
	if err != nil || !mr.HasRecord || mr.Record.GoalBinding == nil {
		return memberRuntime{}, fmt.Errorf("goal retry-attempt refused: launch record unavailable immediately before send")
	}
	launchDigest, launchMod, err := readGoalFileGeneration(launch.ExistingPath(mr.AgentDir))
	if err != nil || launchDigest != expected.LaunchDigest || launchMod != expected.LaunchModTime {
		return memberRuntime{}, fmt.Errorf("goal retry-attempt refused: launch generation changed immediately before send")
	}
	rec := mr.Record
	ns := squadnamespace.Resolve(opts.Project, opts.Profile, opts.Session)
	goal, boundAttemptID, parseErr := parseGeneratedGoalBinding(rec.GoalBinding.Command)
	if parseErr != nil || boundAttemptID != attemptID || goal != attempt.Goal || rec.GoalBinding.Mode != "native_goal" || !rec.GoalBinding.NativeGoal || rec.GoalBinding.Source != "goal-control" ||
		rec.Role != opts.Role || rec.Handle != memberHandle(member) || rec.Session != opts.Session || !squadnamespace.ProfilesEqual(rec.TeamProfile, opts.Profile) || canonicalPath(rec.TeamHome) != canonicalPath(opts.Project) ||
		canonicalPath(rec.Root) != canonicalPath(ns.AMQRoot) || canonicalPath(rec.CWD) != canonicalPath(member.EffectiveCWD(currentTeam.Project)) || rec.Binary != member.Binary || rec.Conversation != "" ||
		rec.BootstrapExpectation == nil || !rec.BootstrapExpectation.Required || strings.TrimSpace(rec.BootstrapExpectation.LaunchID) == "" || rec.StartedAt.IsZero() || rec.Tmux == nil || rec.Tmux.Target == "adopted" ||
		rec.GoalBinding.Command != nativeGoalControlPrompt(goal, currentTeam, opts.Profile, opts.Session, opts.Role, attemptID) {
		return memberRuntime{}, fmt.Errorf("goal retry-attempt refused: exact launch/binding identity changed immediately before send")
	}
	claimPath := goalAttemptClaimPath(mustGoalAttemptPathForRetry(opts, attemptID))
	if _, err := os.Stat(claimPath); err == nil {
		return memberRuntime{}, fmt.Errorf("goal retry-attempt refused: attempt became claimed immediately before send")
	} else if !os.IsNotExist(err) {
		return memberRuntime{}, fmt.Errorf("goal retry-attempt refused: inspect claim immediately before send: %w", err)
	}
	return mr, nil
}

func mustGoalAttemptPathForRetry(opts goalDeliveryOptions, attemptID string) string {
	path, _ := goalAttemptPath(opts.Project, opts.Profile, opts.Session, attemptID)
	return path
}
