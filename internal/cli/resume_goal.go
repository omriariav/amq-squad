package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
)

const resumeGoalPlanSchemaVersion = 1

const resumeGoalTransitionSchemaVersion = 1

// resumeGoalNativeBinding is the exact assessment binding authorized by a native goal-resume
// reservation that is not already represented by the transition record's shared flat fields.
//
// Every field is required for native reservations and deliberately lacks omitempty. The containing
// pointer is the compatibility boundary: it is absent for legacy/redeliver records, while a broken
// native writer leaves visibly invalid evidence that readers refuse instead of silently omitting
// the missing identity.
//
// GoalAttemptID intentionally duplicates the VALUE in the flat NewAttemptID under a different
// semantic contract. GoalAttemptID is the assessment identity that authorized the reservation;
// NewAttemptID is the lifecycle identity carried through bound/consumed artifacts. Native
// construction and recovery require them to agree.
type resumeGoalNativeBinding struct {
	NamespaceID             string `json:"namespace_id"`
	PaneID                  string `json:"pane_id"`
	GoalMode                string `json:"goal_mode"`
	GoalAttemptID           string `json:"goal_attempt_id"`
	GoalBindingDigest       string `json:"goal_binding_digest"`
	GoalCommandDigest       string `json:"goal_command_digest"`
	BlockerID               string `json:"blocker_id"`
	BlockerResolutionDigest string `json:"blocker_resolution_digest"`
	PolicyMode              string `json:"policy_mode"`
	PolicyRevision          int    `json:"policy_revision"`
}

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

	// PR5 / #498. ADDITIVE top-level fields that let this record serve every recovery kind rather
	// than redelivery alone. Every top-level addition is omitempty, and that is a compatibility
	// REQUIREMENT, not a style choice: a legacy record on disk carries none of them, so they must
	// stay absent from its round trip instead of appearing as empty values a reader could mistake
	// for deliberate evidence. NativeBinding's required interior fields are the intentional inverse:
	// once the pointer is present, an empty field stays visible so native validation can refuse it.
	//
	// READ/WRITE ASYMMETRY (ruled): on READ, absence means legacy/redeliver, identified by the
	// legacy key. On WRITE, absence of a field the kind requires REFUSES -- a defaulted identity
	// field is indistinguishable on disk from a legacy record, so a lenient writer would silently
	// enrol new records into the legacy population.

	// RecoveryKind is what this reservation is FOR. It lives here AND in the filename prefix, and
	// the scan requires the two to AGREE: the prefix keeps the kind-agnostic scan cheap and the
	// directory readable, the body makes the kind durable evidence rather than a naming
	// convention, and disagreement is ambiguous evidence that refuses rather than a tie to break.
	RecoveryKind string `json:"recovery_kind,omitempty"`

	// PauseGeneration is CONSUMED from the assessment, never recomputed here. PR4 derives it from
	// captured LaunchID + Goal.BindingDigest + Goal.AttemptID + Goal.Mode; a second derivation
	// owner reading a different snapshot is the one-identity-two-owners failure whose symptom is
	// DOUBLE DELIVERY. Redeliver transitions must NOT carry one: that path holds no assessment,
	// so any value here would have been recomputed or invented.
	PauseGeneration string `json:"pause_generation,omitempty"`

	// PreclaimFingerprint is staleness EVIDENCE and deliberately NOT part of the claim key. It
	// rotates when claim evidence changes, and writing the claim is itself such a change, so a
	// fingerprint-keyed claim would rotate its own identity at the moment of being recorded and
	// become unmatchable on read.
	PreclaimFingerprint string `json:"preclaim_fingerprint,omitempty"`

	// Supervisor is WHO AUTHORISED this resume, recorded because accountability that exists only in a
	// live process cannot be audited after it. The pre-mutation gates already refuse a blank or
	// placeholder supervisor, so the value is validated before it gets here -- but validating an
	// identity and then discarding it means the claim on disk cannot answer the one question an
	// operator asks about an unexpected resume.
	//
	// NEVER INFERRED FROM Role/Handle. The lead and the supervisor are two different actors with two
	// different accountabilities; deriving one from the other would durably record the wrong actor,
	// and a wrong attribution is worse than a missing one because it looks like an answer.
	//
	// REDELIVER MUST NOT CARRY ONE, same asymmetry as PauseGeneration: that path holds no assessment
	// and no supervisor, so a value there was invented rather than observed.
	Supervisor string `json:"supervisor,omitempty"`

	// NativeBinding is nil for legacy/redeliver and non-nil exactly for native goal-resume
	// reservations. The constructor owns this entire block: Base.NativeBinding is never a valid
	// source (even when its values would agree), and accepted native input is copied into a fresh
	// value rather than retaining a caller or Base pointer.
	//
	// Existing shared identities deliberately stay flat: Project/Profile/Session, Role/Handle,
	// LaunchID and launch generation, SchemaVersion, transition/kind, pause/fingerprint,
	// Supervisor, and NewAttemptID. Duplicating them here would create permanent disagreement
	// states between two durable owners of the same fact.
	NativeBinding *resumeGoalNativeBinding `json:"native_binding,omitempty"`

	// BindingReserved is runtime-only recovery state. It records that a prior
	// process durably published this transition's exact new binding before it
	// crashed, so continuation must reuse the same attempt rather than require
	// the old binding CAS or create a third attempt.
	BindingReserved bool `json:"-"`
}

type resumeGoalTransitionConsumed struct {
	SchemaVersion int       `json:"schema_version"`
	TransitionID  string    `json:"transition_id"`
	NewAttemptID  string    `json:"new_attempt_id"`
	ConsumedAt    time.Time `json:"consumed_at"`
}

// resumeGoalTransitionBound seals the exact launch-record generation after a
// transition has installed its new attempt binding. It lets a restarted
// process distinguish the deliberate post-reservation generation from a later
// stale/ABA writer without rewriting the immutable transition reservation.
type resumeGoalTransitionBound struct {
	SchemaVersion       int       `json:"schema_version"`
	TransitionID        string    `json:"transition_id"`
	NewAttemptID        string    `json:"new_attempt_id"`
	LaunchRecordDigest  string    `json:"launch_record_digest"`
	LaunchRecordModTime int64     `json:"launch_record_mod_time_unix_nano"`
	BoundAt             time.Time `json:"bound_at"`
}

type resumeGoalSendSnapshot struct {
	TeamDigest    string
	TeamModTime   int64
	LaunchDigest  string
	LaunchModTime int64
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

func resumeGoalTransitionBoundPath(path string) string {
	return strings.TrimSuffix(path, ".json") + ".bound.json"
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
	case claim.Route != goalClaimRouteNative && claim.Route != goalClaimRoutePrompt && claim.Route != "amq":
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
	goal, attemptID, err := parseNativeGoalBindingCommand(command)
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(attemptID) == "" {
		return "", "", fmt.Errorf("command must contain exactly one non-empty --attempt-id")
	}
	return goal, attemptID, nil
}

// parseNativeGoalBindingCommand accepts the generated Claude /goal grammar.
// Legacy launch bindings may contain only --goal (and optionally --attempt-id),
// while contextual bindings must carry session/profile/mode as one complete
// tuple. All flags are single-use and remain in generator order.
func parseNativeGoalBindingCommand(command string) (string, string, error) {
	tokens, err := splitGeneratedGoalTokens(command)
	if err != nil {
		return "", "", err
	}
	if len(tokens) == 0 || tokens[0] != "/goal" {
		return "", "", fmt.Errorf("command is not a generated /goal")
	}
	ranks := map[string]int{
		"--goal": 1, "--session": 2, "--profile": 3, "--mode": 4,
		"--lead": 5, "--lead-mode": 6, "--target-contract": 7, "--attempt-id": 8,
	}
	values := make(map[string]string, len(ranks))
	lastRank := 0
	for i := 1; i < len(tokens); i++ {
		flag := tokens[i]
		rank, ok := ranks[flag]
		if !ok {
			return "", "", fmt.Errorf("unsupported generated /goal token %q", flag)
		}
		if rank <= lastRank {
			return "", "", fmt.Errorf("generated /goal flag %s is duplicated or out of order", flag)
		}
		if i+1 >= len(tokens) || strings.TrimSpace(tokens[i+1]) == "" {
			return "", "", fmt.Errorf("%s has no value", flag)
		}
		i++
		values[flag] = tokens[i]
		lastRank = rank
	}
	goal := values["--goal"]
	if goal == "" {
		return "", "", fmt.Errorf("command must contain exactly one non-empty --goal")
	}
	contextFields := 0
	for _, flag := range []string{"--session", "--profile", "--mode"} {
		if values[flag] != "" {
			contextFields++
		}
	}
	if contextFields != 0 && contextFields != 3 {
		return "", "", fmt.Errorf("generated /goal context requires session, profile, and mode together")
	}
	if contextFields == 0 && (values["--lead"] != "" || values["--lead-mode"] != "" || values["--target-contract"] != "") {
		return "", "", fmt.Errorf("generated /goal optional context requires session, profile, and mode")
	}
	if contextFields == 3 {
		if normalized, err := normalizeExecutionMode(values["--mode"]); err != nil || normalized != values["--mode"] {
			return "", "", fmt.Errorf("generated /goal mode is invalid")
		}
		if leadMode := values["--lead-mode"]; leadMode != "" {
			if normalized, err := normalizeLeadMode(leadMode); err != nil || normalized != leadMode || normalized == team.LeadModeBuilder {
				return "", "", fmt.Errorf("generated /goal lead mode is invalid")
			}
		}
	}
	return goal, values["--attempt-id"], nil
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
			value, err := unquoteGoalPromptValue(command[start:i])
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

// resumeGoalRecoveryScanOptions carries the delivery identity used to decide
// whether CONSUMED recovery evidence belongs to this pause. UNCONSUMED evidence
// is directory-wide and always blocks; unlike consumed audit history it is
// transient, operator-recoverable evidence that another delivery may still be
// in flight.
type resumeGoalRecoveryScanOptions struct {
	LegacyKey       string
	TargetNamespace string
	TargetAttemptID string
	OwnPath         string
	OwnTransitionID string
}

type resumeGoalRecoveryCompanion struct {
	Base string
	Path string
}

// scanResumeGoalRecoveryTransitions is the redelivery-side reader for both
// recovery-transition derivations. Recognition always goes through the shared
// tri-state parser: ordinary attempt files are ignored, transition-like malformed
// names block, and recognized legacy/current reservations are inspected.
func scanResumeGoalRecoveryTransitions(dir string, opts resumeGoalRecoveryScanOptions) (*recoveryTransitionBlocker, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read recovery transition directory %s: %w", dir, err)
	}

	reservations := make(map[string]struct{})
	var companions []resumeGoalRecoveryCompanion
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if base, ok := companionReservationBase(name); ok {
			_, recognition := recognizeRecoveryTransitionName(base + ".json")
			switch recognition {
			case recoveryNameNotATransition:
				continue
			case recoveryNameMalformed:
				return &recoveryTransitionBlocker{
					Path:   filepath.Join(dir, name),
					Reason: "a recovery-transition companion has a malformed, unknown-kind or damaged reservation name",
				}, nil
			}
			companions = append(companions, resumeGoalRecoveryCompanion{
				Base: base,
				Path: filepath.Join(dir, name),
			})
			continue
		}

		parsed, recognition := recognizeRecoveryTransitionName(name)
		switch recognition {
		case recoveryNameNotATransition:
			continue
		case recoveryNameMalformed:
			return &recoveryTransitionBlocker{
				Path:   filepath.Join(dir, name),
				Reason: "a recovery-transition-like file is malformed, unknown-kind or missing structure",
			}, nil
		}
		path := filepath.Join(dir, name)
		reservations[name] = struct{}{}
		record, identityErr := readResumeGoalRecoveryIdentity(path, parsed)
		if identityErr != nil {
			return &recoveryTransitionBlocker{Path: path, Reason: identityErr.Error()}, nil
		}
		if path == opts.OwnPath &&
			record.TransitionID == opts.OwnTransitionID &&
			strings.TrimSpace(record.RecoveryKind) == string(recoveryTransitionKindRedeliver) {
			// Exact path is not enough. Only the body identity of the reservation
			// this redelivery wrote permits self-exclusion; a replaced or renamed
			// record remains a blocker.
			continue
		}

		consumedPath := resumeGoalTransitionConsumedPath(path)
		if _, statErr := os.Stat(consumedPath); statErr == nil {
			matches, matchErr := resumeGoalConsumedRecoveryMatchesDelivery(parsed, record, opts)
			if matchErr != nil {
				return &recoveryTransitionBlocker{
					Path:   consumedPath,
					Reason: "consumed recovery-transition identity is indeterminate: " + matchErr.Error(),
				}, nil
			}
			if !matches {
				// A consumed transition proven to belong to a different pause is
				// durable audit history, not a poison pill for all future delivery.
				continue
			}
		} else if !os.IsNotExist(statErr) {
			return &recoveryTransitionBlocker{
				Path:   consumedPath,
				Reason: "cannot stat the consumption record, so recovery-transition identity is indeterminate",
			}, nil
		}

		// Unconsumed evidence blocks directory-wide; consumed evidence reaches
		// here only after exact delivery-identity matching.
		if blocker := reservationBlocker(path); blocker != nil {
			return blocker, nil
		}
	}

	for _, companion := range companions {
		if _, ok := reservations[companion.Base+".json"]; ok {
			continue
		}
		return &recoveryTransitionBlocker{
			Path:   companion.Path,
			Reason: "ORPHAN recovery-transition companion: its reservation is absent, so prior delivery cannot be disproved",
		}, nil
	}
	return nil, nil
}

func readResumeGoalRecoveryIdentity(path string, parsed parsedRecoveryTransitionName) (resumeGoalTransitionRecord, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return resumeGoalTransitionRecord{}, fmt.Errorf("cannot read recovery reservation identity: %w", err)
	}
	var record resumeGoalTransitionRecord
	if err := json.Unmarshal(payload, &record); err != nil {
		return resumeGoalTransitionRecord{}, fmt.Errorf("recovery reservation identity does not parse: %w", err)
	}
	if strings.TrimSpace(record.TransitionID) != parsed.ClaimKey {
		return resumeGoalTransitionRecord{}, fmt.Errorf(
			"recovery reservation filename key %q disagrees with record transition id %q",
			parsed.ClaimKey, record.TransitionID,
		)
	}
	recordKind := recoveryTransitionKind(strings.TrimSpace(record.RecoveryKind))
	if parsed.Legacy {
		// Pre-PR5 legacy records have no kind field; current redelivery writes
		// carry the explicit redeliver kind while retaining the legacy filename.
		if recordKind != "" && recordKind != recoveryTransitionKindRedeliver {
			return resumeGoalTransitionRecord{}, fmt.Errorf(
				"legacy redelivery reservation record carries incompatible kind %q", record.RecoveryKind,
			)
		}
	} else if recordKind != parsed.Kind {
		return resumeGoalTransitionRecord{}, fmt.Errorf(
			"recovery reservation filename kind %q disagrees with record kind %q",
			parsed.Kind, record.RecoveryKind,
		)
	}
	if !parsed.Legacy {
		// A current record may be classified as belonging to a different pause
		// only after its own durable identity is proved. In particular, using an
		// unvalidated PauseGeneration to recompute the delivery-side match would
		// turn a body/filename identity mismatch into "different" and hide prior
		// consumption evidence.
		if err := validateRecoveryTransitionRecordContract(record); err != nil {
			return resumeGoalTransitionRecord{}, fmt.Errorf(
				"current recovery reservation violates the durable record contract: %w", err,
			)
		}
	}
	return record, nil
}

func resumeGoalConsumedRecoveryMatchesDelivery(
	parsed parsedRecoveryTransitionName,
	record resumeGoalTransitionRecord,
	opts resumeGoalRecoveryScanOptions,
) (bool, error) {
	if parsed.Legacy {
		if strings.TrimSpace(opts.LegacyKey) == "" {
			return false, fmt.Errorf("delivery has no exact legacy attempt+binding key")
		}
		return parsed.ClaimKey == opts.LegacyKey, nil
	}
	expected, err := supervisionClaimKey(
		opts.TargetNamespace,
		record.PauseGeneration,
		opts.TargetAttemptID,
	)
	if err != nil {
		return false, fmt.Errorf("recompute current claim key from delivery namespace, record pause generation, and delivery attempt: %w", err)
	}
	return parsed.ClaimKey == expected, nil
}

func validateResumeGoalTransitionBound(bound resumeGoalTransitionBound, tr resumeGoalTransitionRecord, launchDigest string, launchMod int64) error {
	switch {
	case bound.SchemaVersion != resumeGoalTransitionSchemaVersion:
		return fmt.Errorf("schema differs")
	case bound.TransitionID != tr.TransitionID, bound.NewAttemptID != tr.NewAttemptID:
		return fmt.Errorf("transition identity differs")
	case bound.LaunchRecordDigest == "", bound.LaunchRecordModTime == 0, bound.BoundAt.IsZero():
		return fmt.Errorf("generation evidence is incomplete")
	case bound.LaunchRecordDigest != launchDigest || bound.LaunchRecordModTime != launchMod:
		return fmt.Errorf("launch generation differs")
	}
	return nil
}
