package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/omriariav/amq-squad/v2/internal/flock"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
)

const resumeGoalPlanSchemaVersion = 1

const resumeGoalTransitionSchemaVersion = 1

type resumeGoalTransitionRecord struct {
	SchemaVersion         int       `json:"schema_version"`
	TransitionID          string    `json:"transition_id"`
	Project               string    `json:"project"`
	Profile               string    `json:"profile"`
	Session               string    `json:"session"`
	Role                  string    `json:"role"`
	Handle                string    `json:"handle"`
	MemberSession         string    `json:"member_session"`
	MemberCWD             string    `json:"member_cwd"`
	MemberBinary          string    `json:"member_binary"`
	GoalDigest            string    `json:"goal_digest"`
	OriginalAttemptID     string    `json:"original_attempt_id"`
	OriginalBindingDigest string    `json:"original_binding_digest"`
	OriginalAttemptDigest string    `json:"original_attempt_digest"`
	OriginalClaimDigest   string    `json:"original_claim_digest"`
	NewAttemptID          string    `json:"new_attempt_id"`
	LaunchID              string    `json:"launch_id"`
	LaunchStartedAt       time.Time `json:"launch_started_at"`
	TeamRecordDigest      string    `json:"team_record_digest"`
	TeamRecordModTime     int64     `json:"team_record_mod_time_unix_nano"`
	LaunchRecordDigest    string    `json:"launch_record_digest"`
	LaunchRecordModTime   int64     `json:"launch_record_mod_time_unix_nano"`
	CreatedAt             time.Time `json:"created_at"`
}

type resumeGoalTransitionConsumed struct {
	SchemaVersion int       `json:"schema_version"`
	TransitionID  string    `json:"transition_id"`
	NewAttemptID  string    `json:"new_attempt_id"`
	ConsumedAt    time.Time `json:"consumed_at"`
}

type resumeGoalSendSnapshot struct {
	TeamDigest    string
	TeamModTime   int64
	LaunchDigest  string
	LaunchModTime int64
}

// buildResumeGoalPlan computes read-only evidence from the exact per-member
// restore selection. It never claims or creates an attempt.
func buildResumeGoalPlan(t team.Team, profile, workstream string, plans []resumePlan, force, noBootstrap bool) runwizard.ResumeGoalPlan {
	result := runwizard.ResumeGoalPlan{SchemaVersion: resumeGoalPlanSchemaVersion, Action: "unavailable"}
	lead := strings.TrimSpace(t.Lead)
	if lead == "" && len(t.Members) == 1 {
		lead = strings.TrimSpace(t.Members[0].Role)
	}
	result.LeadRole = lead
	finish := func(reason string) runwizard.ResumeGoalPlan {
		result.Eligible = false
		result.Reason = reason
		result.EvidenceDigest = resumeGoalEvidenceDigest(result)
		return result
	}
	finishAs := func(action, reason string) runwizard.ResumeGoalPlan {
		result.Action = action
		return finish(reason)
	}
	if lead == "" {
		return finish("team has no configured or inferable lead")
	}
	member, ok := teamMemberByRole(t, lead)
	if !ok {
		return finish("configured lead is not a current team member")
	}
	var selected *resumePlan
	for i := range plans {
		if plans[i].Role == lead {
			selected = &plans[i]
			break
		}
	}
	if selected == nil {
		return finish("lead is not selected by this resume plan")
	}
	result.LeadHandle = selected.Handle
	result.LeadResumeAction = string(selected.Action)
	if force {
		return finish("forced duplicate resume cannot redeliver a saved goal")
	}
	if selected.Action != resumeRestore {
		return finish("lead is not a fresh re-orient restore")
	}
	if selected.RestoreRecord == nil {
		return finish("lead restore record is unavailable")
	}
	rec := *selected.RestoreRecord
	result.SavedConversation = rec.Conversation != ""
	if canonicalPath(rec.CWD) == "" || canonicalPath(rec.CWD) != canonicalPath(member.EffectiveCWD(t.Project)) {
		return finish("lead restore record does not exactly match the configured cwd")
	}
	if rec.Role != lead || rec.Handle != selected.Handle ||
		strings.TrimSpace(rec.Session) != strings.TrimSpace(workstream) || !squadnamespace.ProfilesEqual(rec.TeamProfile, profile) ||
		rec.Binary != member.Binary {
		return finish("lead restore record identity does not match the selected team member")
	}
	ns := squadnamespace.Resolve(t.Project, profile, workstream)
	if canonicalPath(rec.Root) != canonicalPath(ns.AMQRoot) {
		return finish("lead restore record does not exactly match the workstream root")
	}
	if rec.GoalBinding == nil {
		return finish("lead restore record has no saved goal binding")
	}
	result.Action = "blocked"
	binding := *rec.GoalBinding
	result.BindingMode = binding.Mode
	result.BindingNative = binding.NativeGoal
	result.BindingSource = binding.Source
	result.BindingDigest = digestJSON(binding)
	result.BindingCommandDigest = digestBytes([]byte(binding.Command))
	if binding.Mode != "native_goal" || !binding.NativeGoal || binding.Source != "goal-control" {
		return finish("saved goal binding is not an original goal-control delivery")
	}
	goal, attemptID, err := parseGeneratedGoalBinding(binding.Command)
	if err != nil {
		return finish("saved goal binding is invalid: " + err.Error())
	}
	if expected := nativeGoalControlPrompt(goal, t, profile, workstream, lead, attemptID); binding.Command != expected {
		return finish("saved goal binding does not exactly match the generated goal-control command")
	}
	result.Goal = goal
	result.OriginalAttemptID = attemptID
	if result.SavedConversation {
		return finishAs("skip", "lead will reattach its saved conversation and keeps the saved goal in context")
	}
	if rec.External {
		return finish("lead restore record is external and not managed by resume")
	}
	if noBootstrap {
		return finish("resume explicitly disables bootstrap re-orientation")
	}
	if rec.BootstrapExpectation == nil || !rec.BootstrapExpectation.Required {
		return finish("saved lead launch did not have bootstrap re-orientation enabled")
	}
	attemptPath, err := goalAttemptPath(t.Project, profile, workstream, attemptID)
	if err != nil {
		return finish("saved goal attempt id is invalid")
	}
	attemptBytes, err := os.ReadFile(attemptPath)
	if err != nil {
		if os.IsNotExist(err) {
			result.AttemptState = "missing"
			return finish("original goal attempt record is missing")
		}
		result.AttemptState = "unreadable"
		return finish("original goal attempt record is unreadable")
	}
	result.AttemptDigest = digestBytes(attemptBytes)
	var attempt goalAttemptRecord
	if err := json.Unmarshal(attemptBytes, &attempt); err != nil {
		result.AttemptState = "invalid"
		return finish("original goal attempt record is corrupt")
	}
	if err := validateResumeGoalAttempt(attempt, t.Project, profile, workstream, lead, selected.Handle, goal, attemptID, ns); err != nil {
		result.AttemptState = "mismatched"
		return finish("original goal attempt record is mismatched: " + err.Error())
	}
	result.AttemptState = "recorded"
	claimPath := goalAttemptClaimPath(attemptPath)
	claimBytes, err := os.ReadFile(claimPath)
	if err != nil {
		if os.IsNotExist(err) {
			result.ClaimState = "unclaimed"
			return finish("original goal attempt is not settled: claim is missing")
		}
		result.ClaimState = "unreadable"
		return finish("original goal claim is unreadable")
	}
	result.ClaimDigest = digestBytes(claimBytes)
	var claim goalAttemptClaim
	if err := json.Unmarshal(claimBytes, &claim); err != nil {
		result.ClaimState = "invalid"
		return finish("original goal claim is corrupt")
	}
	if err := validateResumeGoalClaim(claim, attempt); err != nil {
		result.ClaimState = "mismatched"
		return finish("original goal claim is mismatched: " + err.Error())
	}
	result.ClaimState = "claimed"
	result.ClaimRoute = claim.Route
	result.TransitionID = resumeGoalTransitionID(result.OriginalAttemptID, result.BindingDigest)
	transitionPath, err := resumeGoalTransitionPath(t.Project, profile, workstream, result.TransitionID)
	if err != nil {
		result.TransitionState = "invalid"
		return finish("goal redelivery transition identity is invalid")
	}
	if transitionBytes, readErr := os.ReadFile(transitionPath); readErr == nil {
		result.TransitionDigest = digestBytes(transitionBytes)
		var transition resumeGoalTransitionRecord
		if json.Unmarshal(transitionBytes, &transition) != nil || validateResumeGoalTransitionPlan(transition, t.Project, profile, workstream, result) != nil {
			result.TransitionState = "mismatched"
			return finish("a mismatched durable goal redelivery transition already exists")
		}
		result.TransitionState = "reserved"
		if _, consumedErr := os.Stat(resumeGoalTransitionConsumedPath(transitionPath)); consumedErr == nil {
			result.TransitionState = "consumed"
		}
		return finish("a durable goal redelivery transition already exists; inspect its exact attempt before retrying")
	} else if !os.IsNotExist(readErr) {
		result.TransitionState = "unreadable"
		return finish("goal redelivery transition evidence is unreadable")
	}
	result.TransitionState = "absent"
	result.Action = "redeliver"
	result.Eligible = true
	result.Reason = "settled original goal can be redelivered as a new claim-once attempt after lead re-orientation"
	result.EvidenceDigest = resumeGoalEvidenceDigest(result)
	return result
}

func resumeGoalTransitionID(attemptID, bindingDigest string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(attemptID) + "\x00" + bindingDigest))
	return hex.EncodeToString(sum[:])
}

func resumeGoalTransitionPath(project, profile, session, transitionID string) (string, error) {
	if len(transitionID) != 64 {
		return "", fmt.Errorf("invalid transition id")
	}
	if _, err := hex.DecodeString(transitionID); err != nil {
		return "", fmt.Errorf("invalid transition id")
	}
	return filepath.Join(goalAttemptDir(project, profile, session), ".resume-redelivery-"+transitionID+".json"), nil
}

func resumeGoalTransitionConsumedPath(path string) string {
	return strings.TrimSuffix(path, ".json") + ".consumed.json"
}

func validateResumeGoalTransitionPlan(tr resumeGoalTransitionRecord, project, profile, session string, plan runwizard.ResumeGoalPlan) error {
	switch {
	case tr.SchemaVersion != resumeGoalTransitionSchemaVersion:
		return fmt.Errorf("schema differs")
	case tr.TransitionID != plan.TransitionID:
		return fmt.Errorf("transition id differs")
	case canonicalPath(tr.Project) != canonicalPath(project):
		return fmt.Errorf("project differs")
	case !squadnamespace.ProfilesEqual(tr.Profile, profile), tr.Session != session:
		return fmt.Errorf("namespace differs")
	case tr.Role != plan.LeadRole, tr.Handle != plan.LeadHandle:
		return fmt.Errorf("lead identity differs")
	case tr.GoalDigest != digestBytes([]byte(plan.Goal)):
		return fmt.Errorf("goal differs")
	case tr.OriginalAttemptID != plan.OriginalAttemptID, tr.OriginalBindingDigest != plan.BindingDigest,
		tr.OriginalAttemptDigest != plan.AttemptDigest, tr.OriginalClaimDigest != plan.ClaimDigest:
		return fmt.Errorf("original evidence differs")
	case strings.TrimSpace(tr.NewAttemptID) == "", strings.TrimSpace(tr.LaunchID) == "", tr.LaunchStartedAt.IsZero(), tr.CreatedAt.IsZero(), tr.MemberCWD == "", tr.MemberBinary == "",
		tr.TeamRecordDigest == "", tr.TeamRecordModTime == 0, tr.LaunchRecordDigest == "", tr.LaunchRecordModTime == 0:
		return fmt.Errorf("fresh launch evidence is missing")
	case tr.NewAttemptID == tr.OriginalAttemptID:
		return fmt.Errorf("new attempt reuses the original id")
	}
	return nil
}

func validateResumeGoalAttempt(attempt goalAttemptRecord, project, profile, session, role, handle, goal, attemptID string, ns squadnamespace.Ref) error {
	switch {
	case attempt.SchemaVersion != 1:
		return fmt.Errorf("schema_version=%d", attempt.SchemaVersion)
	case attempt.AttemptID != attemptID:
		return fmt.Errorf("attempt_id differs")
	case attempt.Goal != goal:
		return fmt.Errorf("goal differs")
	case canonicalPath(attempt.Project) != canonicalPath(project):
		return fmt.Errorf("project differs")
	case !squadnamespace.ProfilesEqual(attempt.Profile, profile):
		return fmt.Errorf("profile differs")
	case attempt.Session != session:
		return fmt.Errorf("session differs")
	case attempt.Namespace != ns:
		return fmt.Errorf("namespace differs")
	case attempt.Role != role:
		return fmt.Errorf("role differs")
	case attempt.Handle != handle:
		return fmt.Errorf("handle differs")
	case attempt.CreatedAt.IsZero():
		return fmt.Errorf("created_at is missing")
	}
	return nil
}

func validateResumeGoalClaim(claim goalAttemptClaim, attempt goalAttemptRecord) error {
	switch {
	case claim.AttemptID != attempt.AttemptID:
		return fmt.Errorf("attempt_id differs")
	case claim.Route != "native" && claim.Route != "amq":
		return fmt.Errorf("route %q is invalid", claim.Route)
	case claim.ClaimedAt.IsZero():
		return fmt.Errorf("claimed_at is missing")
	case claim.ClaimedAt.Before(attempt.CreatedAt):
		return fmt.Errorf("claimed_at predates the attempt")
	}
	return nil
}

// parseGeneratedGoalBinding is deliberately strict and quote-aware. A literal
// "--attempt-id" inside the quoted goal remains goal text and cannot spoof the
// exactly-one generated attempt flag.
func parseGeneratedGoalBinding(command string) (string, string, error) {
	tokens, err := splitGeneratedGoalTokens(command)
	if err != nil {
		return "", "", err
	}
	if len(tokens) == 0 || tokens[0] != "/goal" {
		return "", "", fmt.Errorf("command is not a generated /goal")
	}
	var goal, attemptID string
	goalCount, attemptCount := 0, 0
	for i := 1; i < len(tokens); i++ {
		switch tokens[i] {
		case "--goal":
			goalCount++
			if i+1 >= len(tokens) {
				return "", "", fmt.Errorf("--goal has no value")
			}
			i++
			goal = tokens[i]
		case "--attempt-id":
			attemptCount++
			if i+1 >= len(tokens) {
				return "", "", fmt.Errorf("--attempt-id has no value")
			}
			i++
			attemptID = tokens[i]
		}
	}
	if goalCount != 1 || attemptCount != 1 || goal == "" || strings.TrimSpace(attemptID) == "" {
		return "", "", fmt.Errorf("command must contain exactly one non-empty --goal and --attempt-id")
	}
	return goal, attemptID, nil
}

func splitGeneratedGoalTokens(command string) ([]string, error) {
	var tokens []string
	for i := 0; i < len(command); {
		for i < len(command) && unicode.IsSpace(rune(command[i])) {
			i++
		}
		if i == len(command) {
			break
		}
		if command[i] == '"' {
			start := i
			i++
			escaped := false
			for i < len(command) {
				c := command[i]
				i++
				if escaped {
					escaped = false
					continue
				}
				if c == '\\' {
					escaped = true
					continue
				}
				if c == '"' {
					break
				}
			}
			if i > len(command) || command[i-1] != '"' {
				return nil, fmt.Errorf("unterminated quoted token")
			}
			if i < len(command) && !unicode.IsSpace(rune(command[i])) {
				return nil, fmt.Errorf("quoted token has an invalid suffix")
			}
			value, err := strconv.Unquote(command[start:i])
			if err != nil {
				return nil, fmt.Errorf("invalid quoted token: %w", err)
			}
			tokens = append(tokens, value)
			continue
		}
		start := i
		for i < len(command) && !unicode.IsSpace(rune(command[i])) {
			if command[i] == '"' {
				return nil, fmt.Errorf("unexpected quote in bare token")
			}
			i++
		}
		tokens = append(tokens, command[start:i])
	}
	return tokens, nil
}

func digestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func digestJSON(value any) string {
	payload, _ := json.Marshal(value)
	return digestBytes(payload)
}

func readGoalFileGeneration(path string) (string, int64, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return "", 0, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, err
	}
	return digestBytes(payload), info.ModTime().UnixNano(), nil
}

func resumeGoalEvidenceDigest(plan runwizard.ResumeGoalPlan) string {
	plan.EvidenceDigest = ""
	plan.Selected = false // downstream operator intent is not discovery evidence
	return digestJSON(plan)
}

func cloneGoalBinding(binding *launch.GoalBinding) *launch.GoalBinding {
	if binding == nil {
		return nil
	}
	copy := *binding
	return &copy
}

func writeResumeGoalPlan(out io.Writer, plan runwizard.ResumeGoalPlan) {
	if plan.SchemaVersion == 0 {
		return
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "# recorded goal redelivery")
	fmt.Fprintf(out, "# lead: %s (%s)\n", plan.LeadRole, plan.LeadHandle)
	fmt.Fprintf(out, "# eligible: %t\n", plan.Eligible)
	fmt.Fprintf(out, "# selected: %t\n", plan.Selected)
	fmt.Fprintf(out, "# action: %s\n", plan.Action)
	fmt.Fprintf(out, "# reason: %s\n", boundedResumeDisplay(plan.Reason, 240))
	if plan.Goal != "" {
		fmt.Fprintf(out, "Recorded goal: %s\n", boundedResumeDisplay(plan.Goal, 240))
	}
	if plan.Eligible {
		fmt.Fprintln(out, "To redeliver after the lead is verified live, run resume --exec with --redeliver-goal.")
	}
}

func promptResumeGoalRedelivery(in io.Reader, out io.Writer, plan runwizard.ResumeGoalPlan) (bool, error) {
	if in == nil {
		in = os.Stdin
	}
	if out == nil {
		out = os.Stderr
	}
	fmt.Fprintf(out, "Recorded goal for %s: %s\nRedeliver it as a new claim-once attempt after the lead is verified live? [y/N] ", plan.LeadRole, boundedResumeDisplay(plan.Goal, 240))
	line, err := readWizardLine(in)
	if err != nil && err != io.EOF {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

func boundedResumeDisplay(value string, maxRunes int) string {
	quoted := []rune(strconv.QuoteToGraphic(value))
	if maxRunes > 1 && len(quoted) > maxRunes {
		quoted = append(quoted[:maxRunes-1], '…')
	}
	return string(quoted)
}

func deliverResumeGoalAfterLaunch(t team.Team, profile, workstream string, results []resumeExecLaunchResult, plan runwizard.ResumeGoalPlan) error {
	if !plan.Eligible || !plan.Selected {
		return usageErrorf("resume goal redelivery requires an eligible selected goal plan")
	}
	var result *resumeExecLaunchResult
	for i := range results {
		if results[i].Check.Role == plan.LeadRole {
			result = &results[i]
			break
		}
	}
	if result == nil {
		return &PartialError{Message: "resume launched members but could not identify the selected lead for goal redelivery"}
	}
	check := result.Check
	if err := reserveResumeGoalTransition(t, profile, workstream, *result, plan); err != nil {
		return &PartialError{Message: "resume launched the lead but saved goal evidence changed before redelivery; no new attempt was created: " + err.Error(), Cause: err}
	}
	args := []string{"start", "--project", t.Project, "--profile", profile, "--session", workstream, "--role", plan.LeadRole, "--goal", plan.Goal, "--resume-transition", plan.TransitionID, "--yes"}
	if err := runStartGoalWithVersion(args, "dev"); err != nil {
		message := "resume launched the lead, but post-launch goal redelivery failed: " + err.Error()
		state, attemptID, attemptPath := resumeGoalDeliveryErrorState(err)
		switch state {
		case goalDeliveryStateNativeQueued:
			message += fmt.Sprintf("\nExact attempt %s is durably recorded at %s and native input is known queued. DO NOT retry or rerun resume; wait for/inspect the claim evidence.", attemptID, attemptPath)
		case goalDeliveryStateFallbackSent:
			message += fmt.Sprintf("\nExact attempt %s is durably recorded at %s and the AMQ fallback was already sent. DO NOT retry or rerun resume; inspect that message and claim evidence.", attemptID, attemptPath)
		case goalDeliveryStatePaneDelivered:
			message += fmt.Sprintf("\nExact attempt %s was already delivered. DO NOT retry or rerun resume; inspect its binding and claim evidence.", attemptID)
		default:
			if recovery := resumeGoalRecoveryFromTypedError(t, profile, workstream, plan.LeadRole, check.AgentDir, err); recovery != "" {
				message += "\nA new attempt is already reserved. DO NOT rerun resume --redeliver-goal; retry that same attempt with:\n  " + recovery
			} else {
				message += "\nDO NOT rerun resume --redeliver-goal until the launch binding and goal-attempt evidence are inspected."
			}
		}
		return &PartialError{Message: message, Cause: err}
	}
	return nil
}

func resumeGoalDeliveryErrorState(err error) (state, attemptID, attemptPath string) {
	var attemptErr *goalDeliveryAttemptError
	if errors.As(err, &attemptErr) {
		return attemptErr.State, attemptErr.AttemptID, attemptErr.AttemptPath
	}
	var postErr *goalPostDeliveryBindingError
	if errors.As(err, &postErr) {
		return goalDeliveryStatePaneDelivered, postErr.AttemptID, ""
	}
	return "", "", ""
}

func reserveResumeGoalTransition(t team.Team, profile, workstream string, verified resumeExecLaunchResult, plan runwizard.ResumeGoalPlan) error {
	check := verified.Check
	opts := goalDeliveryOptions{Project: t.Project, Profile: profile, Session: workstream, Role: plan.LeadRole}
	if err := os.MkdirAll(goalAttemptDir(t.Project, profile, workstream), 0o755); err != nil {
		return err
	}
	return flock.WithLock(goalDeliveryLockPath(opts), func() error {
		if info, err := os.Stat(launch.ExistingPath(check.AgentDir)); err != nil || verified.RecordModTime.IsZero() || !info.ModTime().Equal(verified.RecordModTime) {
			return fmt.Errorf("fresh launch record generation changed after verification (ABA redelivery refused)")
		}
		if err := revalidateResumeGoalAfterLaunch(t, profile, workstream, check, plan); err != nil {
			return err
		}
		rec, err := launch.Read(check.AgentDir)
		if err != nil {
			return err
		}
		if !rec.StartedAt.Equal(verified.RecordStarted) {
			return fmt.Errorf("fresh launch identity changed after verification")
		}
		if info, err := os.Stat(launch.ExistingPath(check.AgentDir)); err != nil || !info.ModTime().Equal(verified.RecordModTime) {
			return fmt.Errorf("fresh launch record changed during redelivery reservation (ABA redelivery refused)")
		}
		currentTeam, err := team.ReadProfile(t.Project, profile)
		if err != nil {
			return err
		}
		member, ok := teamMemberByRole(currentTeam, plan.LeadRole)
		if !ok {
			return fmt.Errorf("lead disappeared before transition reservation")
		}
		teamDigest, teamMod, err := readGoalFileGeneration(team.ProfilePath(t.Project, profile))
		if err != nil {
			return fmt.Errorf("capture team generation: %w", err)
		}
		launchDigest, launchMod, err := readGoalFileGeneration(launch.ExistingPath(check.AgentDir))
		if err != nil {
			return fmt.Errorf("capture launch generation: %w", err)
		}
		path, err := resumeGoalTransitionPath(t.Project, profile, workstream, plan.TransitionID)
		if err != nil {
			return err
		}
		tr := resumeGoalTransitionRecord{
			SchemaVersion: resumeGoalTransitionSchemaVersion, TransitionID: plan.TransitionID,
			Project: t.Project, Profile: squadnamespace.NormalizeProfile(profile), Session: workstream,
			Role: plan.LeadRole, Handle: plan.LeadHandle, MemberSession: member.Session, MemberCWD: member.EffectiveCWD(currentTeam.Project), MemberBinary: member.Binary, GoalDigest: digestBytes([]byte(plan.Goal)),
			OriginalAttemptID: plan.OriginalAttemptID, OriginalBindingDigest: plan.BindingDigest,
			OriginalAttemptDigest: plan.AttemptDigest, OriginalClaimDigest: plan.ClaimDigest,
			NewAttemptID: deliveryAttemptID(time.Now().UTC(), "native_goal", plan.LeadRole, plan.LeadHandle),
			LaunchID:     rec.BootstrapExpectation.LaunchID, LaunchStartedAt: rec.StartedAt.UTC(),
			TeamRecordDigest: teamDigest, TeamRecordModTime: teamMod, LaunchRecordDigest: launchDigest, LaunchRecordModTime: launchMod,
			CreatedAt: time.Now().UTC(),
		}
		payload, err := json.MarshalIndent(tr, "", "  ")
		if err != nil {
			return err
		}
		published, err := publishGoalJSON(path, append(payload, '\n'))
		if err != nil {
			return fmt.Errorf("publish durable goal redelivery transition: %w", err)
		}
		if !published {
			return fmt.Errorf("durable goal redelivery transition %s already exists; duplicate/ABA redelivery refused", plan.TransitionID)
		}
		return nil
	})
}

func validateResumeGoalTransitionForDelivery(opts goalDeliveryOptions, mr memberRuntime) (*resumeGoalTransitionRecord, error) {
	dir := goalAttemptDir(opts.Project, opts.Profile, opts.Session)
	if opts.ResumeTransitionID == "" {
		entries, err := os.ReadDir(dir)
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("inspect goal redelivery transitions: %w", err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasPrefix(name, ".resume-redelivery-") || !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".consumed.json") {
				continue
			}
			path := filepath.Join(dir, name)
			if _, err := os.Stat(resumeGoalTransitionConsumedPath(path)); err == nil {
				continue
			}
			return nil, fmt.Errorf("goal delivery refused: an unconsumed durable resume-goal transition already exists at %s", path)
		}
		return nil, nil
	}
	path, err := resumeGoalTransitionPath(opts.Project, opts.Profile, opts.Session, opts.ResumeTransitionID)
	if err != nil {
		return nil, fmt.Errorf("goal delivery refused: %w", err)
	}
	if _, err := os.Stat(resumeGoalTransitionConsumedPath(path)); err == nil {
		return nil, fmt.Errorf("goal delivery refused: resume-goal transition %s was already consumed", opts.ResumeTransitionID)
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("goal delivery refused: inspect transition completion: %w", err)
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("goal delivery refused: read durable resume-goal transition: %w", err)
	}
	var tr resumeGoalTransitionRecord
	if err := json.Unmarshal(payload, &tr); err != nil {
		return nil, fmt.Errorf("goal delivery refused: durable resume-goal transition is corrupt: %w", err)
	}
	currentTeam, err := team.ReadProfile(opts.Project, opts.Profile)
	if err != nil {
		return nil, fmt.Errorf("goal delivery refused: reread team: %w", err)
	}
	lead := strings.TrimSpace(currentTeam.Lead)
	if lead == "" && len(currentTeam.Members) == 1 {
		lead = currentTeam.Members[0].Role
	}
	member, ok := teamMemberByRole(currentTeam, opts.Role)
	if !ok || lead != opts.Role || memberHandle(member) != opts.Member.Handle {
		return nil, fmt.Errorf("goal delivery refused: current lead roster identity changed")
	}
	if canonicalPath(currentTeam.Project) != canonicalPath(opts.Project) || member.Session != tr.MemberSession ||
		(strings.TrimSpace(member.Session) != "" && member.Session != opts.Session) || canonicalPath(member.EffectiveCWD(currentTeam.Project)) != canonicalPath(tr.MemberCWD) || member.Binary != tr.MemberBinary {
		return nil, fmt.Errorf("goal delivery refused: current lead project/session/member identity changed")
	}
	teamDigest, teamMod, err := readGoalFileGeneration(team.ProfilePath(opts.Project, opts.Profile))
	if err != nil || teamDigest != tr.TeamRecordDigest || teamMod != tr.TeamRecordModTime {
		return nil, fmt.Errorf("goal delivery refused: team generation changed after transition reservation")
	}
	if tr.SchemaVersion != resumeGoalTransitionSchemaVersion || tr.TransitionID != opts.ResumeTransitionID ||
		canonicalPath(tr.Project) != canonicalPath(opts.Project) || !squadnamespace.ProfilesEqual(tr.Profile, opts.Profile) || tr.Session != opts.Session ||
		tr.Role != opts.Role || tr.Handle != opts.Member.Handle || tr.GoalDigest != digestBytes([]byte(opts.Goal)) {
		return nil, fmt.Errorf("goal delivery refused: durable resume-goal transition identity changed")
	}
	if tr.NewAttemptID == tr.OriginalAttemptID {
		return nil, fmt.Errorf("goal delivery refused: transition reuses the original attempt id")
	}
	if _, err := goalAttemptPath(opts.Project, opts.Profile, opts.Session, tr.NewAttemptID); err != nil {
		return nil, fmt.Errorf("goal delivery refused: transition new attempt id is invalid")
	}
	if !mr.HasRecord || mr.Record.GoalBinding == nil || digestJSON(*mr.Record.GoalBinding) != tr.OriginalBindingDigest {
		return nil, fmt.Errorf("goal delivery refused: expected old goal binding compare-and-swap failed")
	}
	rec := mr.Record
	ns := squadnamespace.Resolve(opts.Project, opts.Profile, opts.Session)
	if rec.Role != opts.Role || rec.Handle != opts.Member.Handle || rec.Session != opts.Session ||
		!squadnamespace.ProfilesEqual(rec.TeamProfile, opts.Profile) || canonicalPath(rec.Root) != canonicalPath(ns.AMQRoot) ||
		canonicalPath(rec.TeamHome) != canonicalPath(opts.Project) || canonicalPath(rec.CWD) != canonicalPath(member.EffectiveCWD(currentTeam.Project)) || rec.Binary != member.Binary ||
		rec.Conversation != "" || rec.BootstrapExpectation == nil || !rec.BootstrapExpectation.Required ||
		rec.BootstrapExpectation.LaunchID != tr.LaunchID || !rec.StartedAt.Equal(tr.LaunchStartedAt) || rec.Tmux == nil || rec.Tmux.Target == "adopted" {
		return nil, fmt.Errorf("goal delivery refused: fresh lead launch identity changed")
	}
	launchDigest, launchMod, err := readGoalFileGeneration(launch.ExistingPath(mr.AgentDir))
	if err != nil || launchDigest != tr.LaunchRecordDigest || launchMod != tr.LaunchRecordModTime {
		return nil, fmt.Errorf("goal delivery refused: launch generation changed after transition reservation")
	}
	attemptPath, err := goalAttemptPath(opts.Project, opts.Profile, opts.Session, tr.OriginalAttemptID)
	if err != nil {
		return nil, err
	}
	attemptBytes, err := os.ReadFile(attemptPath)
	if err != nil || digestBytes(attemptBytes) != tr.OriginalAttemptDigest {
		return nil, fmt.Errorf("goal delivery refused: original attempt evidence changed")
	}
	claimBytes, err := os.ReadFile(goalAttemptClaimPath(attemptPath))
	if err != nil || digestBytes(claimBytes) != tr.OriginalClaimDigest {
		return nil, fmt.Errorf("goal delivery refused: original claim evidence changed")
	}
	return &tr, nil
}

func consumeResumeGoalTransition(opts goalDeliveryOptions, newAttemptID string) error {
	path, err := resumeGoalTransitionPath(opts.Project, opts.Profile, opts.Session, opts.ResumeTransitionID)
	if err != nil {
		return err
	}
	consumed := resumeGoalTransitionConsumed{SchemaVersion: resumeGoalTransitionSchemaVersion, TransitionID: opts.ResumeTransitionID, NewAttemptID: newAttemptID, ConsumedAt: time.Now().UTC()}
	payload, err := json.MarshalIndent(consumed, "", "  ")
	if err != nil {
		return err
	}
	published, err := publishGoalJSON(resumeGoalTransitionConsumedPath(path), append(payload, '\n'))
	if err != nil {
		return fmt.Errorf("publish resume-goal transition completion: %w", err)
	}
	if !published {
		return fmt.Errorf("resume-goal transition %s was concurrently consumed", opts.ResumeTransitionID)
	}
	return nil
}

func captureResumeGoalSendSnapshot(opts goalDeliveryOptions, tr *resumeGoalTransitionRecord, prompt, attemptID string) (memberRuntime, resumeGoalSendSnapshot, error) {
	if tr == nil {
		return memberRuntime{}, resumeGoalSendSnapshot{}, fmt.Errorf("resume goal send requires a transition")
	}
	teamDigest, teamMod, err := readGoalFileGeneration(team.ProfilePath(opts.Project, opts.Profile))
	if err != nil || teamDigest != tr.TeamRecordDigest || teamMod != tr.TeamRecordModTime {
		return memberRuntime{}, resumeGoalSendSnapshot{}, fmt.Errorf("team generation changed before resume goal send")
	}
	currentTeam, err := team.ReadProfile(opts.Project, opts.Profile)
	if err != nil {
		return memberRuntime{}, resumeGoalSendSnapshot{}, err
	}
	lead := strings.TrimSpace(currentTeam.Lead)
	if lead == "" && len(currentTeam.Members) == 1 {
		lead = currentTeam.Members[0].Role
	}
	member, ok := teamMemberByRole(currentTeam, opts.Role)
	if !ok || lead != opts.Role || memberHandle(member) != opts.Member.Handle || canonicalPath(currentTeam.Project) != canonicalPath(opts.Project) ||
		member.Session != tr.MemberSession || (member.Session != "" && member.Session != opts.Session) || canonicalPath(member.EffectiveCWD(currentTeam.Project)) != canonicalPath(tr.MemberCWD) || member.Binary != tr.MemberBinary {
		return memberRuntime{}, resumeGoalSendSnapshot{}, fmt.Errorf("lead team identity changed before resume goal send")
	}
	mr, _, err := resolveMemberRuntime(opts.Project, opts.Profile, opts.Session, true, opts.Role)
	if err != nil || !mr.HasRecord || mr.Record.GoalBinding == nil {
		return memberRuntime{}, resumeGoalSendSnapshot{}, fmt.Errorf("lead launch record unavailable before resume goal send")
	}
	rec := mr.Record
	ns := squadnamespace.Resolve(opts.Project, opts.Profile, opts.Session)
	if rec.Role != opts.Role || rec.Handle != opts.Member.Handle || rec.Session != opts.Session || !squadnamespace.ProfilesEqual(rec.TeamProfile, opts.Profile) ||
		canonicalPath(rec.TeamHome) != canonicalPath(opts.Project) || canonicalPath(rec.Root) != canonicalPath(ns.AMQRoot) || canonicalPath(rec.CWD) != canonicalPath(member.EffectiveCWD(currentTeam.Project)) ||
		rec.Binary != member.Binary || rec.Conversation != "" || rec.BootstrapExpectation == nil || !rec.BootstrapExpectation.Required || rec.BootstrapExpectation.LaunchID != tr.LaunchID ||
		!rec.StartedAt.Equal(tr.LaunchStartedAt) || rec.Tmux == nil || rec.Tmux.Target == "adopted" || rec.GoalBinding.Mode != "native_goal" || !rec.GoalBinding.NativeGoal ||
		rec.GoalBinding.Source != "goal-control" || rec.GoalBinding.Command != prompt {
		return memberRuntime{}, resumeGoalSendSnapshot{}, fmt.Errorf("lead launch identity/binding changed before resume goal send")
	}
	attemptPath, err := goalAttemptPath(opts.Project, opts.Profile, opts.Session, attemptID)
	if err != nil {
		return memberRuntime{}, resumeGoalSendSnapshot{}, err
	}
	attempt, err := readGoalAttempt(attemptPath, attemptID)
	if err != nil || validateResumeGoalAttempt(attempt, opts.Project, opts.Profile, opts.Session, opts.Role, opts.Member.Handle, opts.Goal, attemptID, opts.Namespace) != nil {
		return memberRuntime{}, resumeGoalSendSnapshot{}, fmt.Errorf("new resume goal attempt changed before send")
	}
	if _, err := os.Stat(goalAttemptClaimPath(attemptPath)); err == nil {
		return memberRuntime{}, resumeGoalSendSnapshot{}, fmt.Errorf("new resume goal attempt was already claimed before send")
	} else if !os.IsNotExist(err) {
		return memberRuntime{}, resumeGoalSendSnapshot{}, fmt.Errorf("inspect new resume goal claim: %w", err)
	}
	launchDigest, launchMod, err := readGoalFileGeneration(launch.ExistingPath(mr.AgentDir))
	if err != nil {
		return memberRuntime{}, resumeGoalSendSnapshot{}, err
	}
	return mr, resumeGoalSendSnapshot{TeamDigest: teamDigest, TeamModTime: teamMod, LaunchDigest: launchDigest, LaunchModTime: launchMod}, nil
}

func validateResumeGoalSendSnapshot(opts goalDeliveryOptions, tr *resumeGoalTransitionRecord, prompt, attemptID string, expected resumeGoalSendSnapshot) (memberRuntime, error) {
	mr, current, err := captureResumeGoalSendSnapshot(opts, tr, prompt, attemptID)
	if err != nil {
		return memberRuntime{}, err
	}
	if current != expected {
		return memberRuntime{}, fmt.Errorf("team or launch generation changed immediately before resume goal send")
	}
	return mr, nil
}

func revalidateResumeGoalAfterLaunch(t team.Team, profile, workstream string, check resumeExecLaunchCheck, plan runwizard.ResumeGoalPlan) error {
	currentTeam, err := team.ReadProfile(t.Project, profile)
	if err != nil {
		return fmt.Errorf("reread team: %w", err)
	}
	lead := strings.TrimSpace(currentTeam.Lead)
	if lead == "" && len(currentTeam.Members) == 1 {
		lead = currentTeam.Members[0].Role
	}
	if lead != plan.LeadRole {
		return fmt.Errorf("team lead changed from %q to %q", plan.LeadRole, lead)
	}
	member, ok := teamMemberByRole(currentTeam, plan.LeadRole)
	if !ok {
		return fmt.Errorf("lead is no longer a team member")
	}
	if canonicalPath(currentTeam.Project) != canonicalPath(t.Project) || member.Role != plan.LeadRole || memberHandle(member) != plan.LeadHandle {
		return fmt.Errorf("current lead roster identity changed")
	}
	if pinned := strings.TrimSpace(member.Session); pinned != "" && pinned != workstream {
		return fmt.Errorf("current lead session pin changed to %q", pinned)
	}
	rec, err := launch.Read(check.AgentDir)
	if err != nil {
		return fmt.Errorf("reread lead launch record: %w", err)
	}
	ns := squadnamespace.Resolve(t.Project, profile, workstream)
	switch {
	case rec.Role != plan.LeadRole:
		return fmt.Errorf("lead role changed")
	case rec.Handle != plan.LeadHandle || rec.Handle != check.Handle:
		return fmt.Errorf("lead handle changed")
	case rec.Session != workstream:
		return fmt.Errorf("lead session changed")
	case !squadnamespace.ProfilesEqual(rec.TeamProfile, profile):
		return fmt.Errorf("lead profile changed")
	case canonicalPath(rec.CWD) != canonicalPath(member.EffectiveCWD(currentTeam.Project)) || canonicalPath(rec.CWD) != canonicalPath(check.CWD):
		return fmt.Errorf("lead cwd changed")
	case canonicalPath(rec.Root) != canonicalPath(ns.AMQRoot) || canonicalPath(rec.Root) != canonicalPath(check.Root):
		return fmt.Errorf("lead root changed")
	case rec.Binary != member.Binary || rec.Binary != check.Binary:
		return fmt.Errorf("lead binary changed")
	case rec.Conversation != "":
		return fmt.Errorf("lead unexpectedly reattached a conversation")
	case rec.BootstrapExpectation == nil || !rec.BootstrapExpectation.Required:
		return fmt.Errorf("lead launch did not enable bootstrap re-orientation")
	case strings.TrimSpace(rec.BootstrapExpectation.LaunchID) == "" || rec.StartedAt.IsZero():
		return fmt.Errorf("lead launch has no fresh launch identity")
	case rec.Tmux == nil || strings.TrimSpace(rec.Tmux.PaneID) == "":
		return fmt.Errorf("lead launch has no tmux pane identity")
	case rec.Tmux.Target == "adopted":
		return fmt.Errorf("adopted lead pane is not a verified fresh bootstrap launch")
	}
	if _, alive := statusPaneInspector(rec.Tmux.PaneID); !alive {
		return fmt.Errorf("verified lead pane %s is not live", rec.Tmux.PaneID)
	}
	if rec.GoalBinding == nil || digestJSON(*rec.GoalBinding) != plan.BindingDigest || digestBytes([]byte(rec.GoalBinding.Command)) != plan.BindingCommandDigest {
		return fmt.Errorf("saved goal binding changed")
	}
	goal, attemptID, err := parseGeneratedGoalBinding(rec.GoalBinding.Command)
	if err != nil || goal != plan.Goal || attemptID != plan.OriginalAttemptID {
		return fmt.Errorf("saved goal command identity changed")
	}
	if expected := nativeGoalControlPrompt(goal, currentTeam, profile, workstream, plan.LeadRole, attemptID); rec.GoalBinding.Command != expected {
		return fmt.Errorf("saved goal command no longer matches the team contract")
	}
	attemptPath, err := goalAttemptPath(t.Project, profile, workstream, attemptID)
	if err != nil {
		return err
	}
	attemptBytes, err := os.ReadFile(attemptPath)
	if err != nil || digestBytes(attemptBytes) != plan.AttemptDigest {
		return fmt.Errorf("original goal attempt changed")
	}
	claimBytes, err := os.ReadFile(goalAttemptClaimPath(attemptPath))
	if err != nil || digestBytes(claimBytes) != plan.ClaimDigest {
		return fmt.Errorf("original goal claim changed")
	}
	return nil
}

func resumeGoalRecoveryFromTypedError(t team.Team, profile, session, role, agentDir string, deliveryErr error) string {
	var attemptErr *goalDeliveryAttemptError
	var postErr *goalPostDeliveryBindingError
	attemptID := ""
	if errors.As(deliveryErr, &attemptErr) {
		if attemptErr.State == goalDeliveryStateNativeQueued || attemptErr.State == goalDeliveryStateFallbackSent || attemptErr.State == goalDeliveryStatePaneDelivered {
			return ""
		}
		if !attemptErr.Sent && attemptErr.AttemptPath == "" {
			return ""
		}
		attemptID = attemptErr.AttemptID
	} else if errors.As(deliveryErr, &postErr) {
		return ""
	} else {
		return ""
	}
	current, err := team.ReadProfile(t.Project, profile)
	if err != nil {
		return ""
	}
	lead := strings.TrimSpace(current.Lead)
	if lead == "" && len(current.Members) == 1 {
		lead = current.Members[0].Role
	}
	if lead != role {
		return ""
	}
	member, ok := teamMemberByRole(current, role)
	if !ok {
		return ""
	}
	path, err := goalAttemptPath(t.Project, profile, session, attemptID)
	if err != nil {
		return ""
	}
	attempt, err := readGoalAttempt(path, attemptID)
	if err != nil || validateResumeGoalAttempt(attempt, t.Project, profile, session, role, memberHandle(member), attempt.Goal, attemptID, squadnamespace.Resolve(t.Project, profile, session)) != nil {
		return ""
	}
	if _, err := os.Stat(goalAttemptClaimPath(path)); !os.IsNotExist(err) {
		return ""
	}
	rec, err := launch.Read(agentDir)
	if err != nil || rec.GoalBinding == nil {
		return ""
	}
	goal, boundID, err := parseGeneratedGoalBinding(rec.GoalBinding.Command)
	ns := squadnamespace.Resolve(t.Project, profile, session)
	if err != nil || boundID != attemptID || goal != attempt.Goal || rec.GoalBinding.Mode != "native_goal" || !rec.GoalBinding.NativeGoal || rec.GoalBinding.Source != "goal-control" ||
		rec.Role != role || rec.Handle != memberHandle(member) || rec.Session != session || !squadnamespace.ProfilesEqual(rec.TeamProfile, profile) ||
		canonicalPath(rec.TeamHome) != canonicalPath(t.Project) || canonicalPath(rec.Root) != canonicalPath(ns.AMQRoot) || canonicalPath(rec.CWD) != canonicalPath(member.EffectiveCWD(current.Project)) ||
		rec.Binary != member.Binary || rec.Conversation != "" || rec.BootstrapExpectation == nil || !rec.BootstrapExpectation.Required || rec.Tmux == nil || rec.Tmux.Target == "adopted" ||
		rec.GoalBinding.Command != nativeGoalControlPrompt(goal, current, profile, session, role, attemptID) {
		return ""
	}
	args := []string{"amq-squad", "goal", "retry-attempt", "--project", t.Project, "--profile", profile, "--session", session, "--role", role, "--attempt-id", attemptID, "--yes"}
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}
