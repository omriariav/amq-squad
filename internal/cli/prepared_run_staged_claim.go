package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/liveidentity"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

const (
	preparedRunStagedClaimSchema = 1
	stagedClaimStatePending      = "pending"
	stagedClaimStateAdmitted     = "admitted"
	stagedClaimStateConsumed     = "consumed"
	stagedClaimStateSuperseded   = "superseded"
	stagedClaimStateFailed       = "failed"
	stagedClaimStateAbandoned    = "abandoned"
)

type preparedRunStagedAdmissionRequest struct {
	Role              string
	Handle            string
	AuthorizingRole   string
	AuthorizingHandle string
	ActorMode         string
	SupersedesClaimID string
	LifecycleReason   string
}

type preparedRunStagedAuthorizer struct {
	Role                string                `json:"role"`
	Handle              string                `json:"handle"`
	LaunchID            string                `json:"launch_id"`
	ParentLaunchAttempt string                `json:"parent_launch_attempt"`
	GenerationRef       preparedRunToken      `json:"generation_ref"`
	Verified            liveidentity.Verified `json:"verified_identity"`
	VerificationResult  liveidentity.Result   `json:"verification_result"`
	VerificationDigest  string                `json:"verification_result_digest"`
	VerifiedAt          time.Time             `json:"verified_at"`
}

type preparedRunStagedLifecycle struct {
	State                string `json:"state"`
	SupersedesClaimID    string `json:"supersedes_claim_id,omitempty"`
	RequiresTargetAbsent bool   `json:"requires_target_absent"`
	Reason               string `json:"reason,omitempty"`
}

// preparedRunStagedClaim is immutable admission authority. The accepted
// manifest remains unchanged; a narrowing replacement records the effective
// runtime identity here and links to the exact prior claim.
type preparedRunStagedClaim struct {
	SchemaVersion   int                         `json:"schema_version"`
	ClaimID         string                      `json:"claim_id"`
	GenerationRef   preparedRunToken            `json:"generation_ref"`
	Namespace       string                      `json:"namespace"`
	Profile         string                      `json:"profile"`
	Session         string                      `json:"session"`
	Role            string                      `json:"role"`
	Handle          string                      `json:"handle"`
	Effective       preparedRunMemberIdentity   `json:"effective_identity"`
	Accepted        preparedRunMemberIdentity   `json:"accepted_identity"`
	BootstrapDigest string                      `json:"bootstrap_digest"`
	LaunchStrategy  preparedRunTopology         `json:"launch_strategy"`
	Authorizer      preparedRunStagedAuthorizer `json:"authorizer"`
	Lifecycle       preparedRunStagedLifecycle  `json:"lifecycle"`
	CreatedAt       time.Time                   `json:"created_at"`
}

type preparedRunStagedClaimPointer struct {
	SchemaVersion     int                           `json:"schema_version"`
	GenerationRef     preparedRunToken              `json:"generation_ref"`
	Role              string                        `json:"role"`
	Handle            string                        `json:"handle"`
	ClaimID           string                        `json:"claim_id"`
	ClaimDigest       string                        `json:"claim_digest"`
	ActivationID      string                        `json:"activation_id"`
	LifecycleState    string                        `json:"lifecycle_state"`
	EffectiveIdentity preparedRunMemberIdentity     `json:"effective_identity"`
	Consumption       *preparedRunStagedConsumption `json:"consumption,omitempty"`
	UpdatedAt         time.Time                     `json:"updated_at"`
}

type preparedRunStagedTransition struct {
	SchemaVersion int              `json:"schema_version"`
	TransitionID  string           `json:"transition_id"`
	ActivationID  string           `json:"activation_id,omitempty"`
	GenerationRef preparedRunToken `json:"generation_ref"`
	Role          string           `json:"role"`
	Handle        string           `json:"handle"`
	ClaimID       string           `json:"claim_id"`
	ClaimDigest   string           `json:"claim_digest"`
	State         string           `json:"state"`
	SupersededBy  string           `json:"superseded_by,omitempty"`
	Reason        string           `json:"reason,omitempty"`
	CreatedAt     time.Time        `json:"created_at"`
}

type preparedRunStagedConsumption struct {
	LaunchAttempt string    `json:"launch_attempt"`
	EventDigest   string    `json:"event_digest"`
	ConsumedAt    time.Time `json:"consumed_at"`
}

var preparedRunStagedClaimBeforeActivate = func() error { return nil }
var preparedRunStagedTargetAbsent = provePreparedRunStagedTargetAbsent
var preparedRunStagedVerifyAuthorizer = verifyLiveIdentityAuthorizer
var preparedRunStagedReplaceCurrent = durableReplace
var preparedRunStagedConsumptionBeforeEvent = func() error { return nil }
var preparedRunStagedConsumptionBeforeTransition = func() error { return nil }

func preparedRunStagedClaimsDir(project, profile, session, generation, role string) string {
	return filepath.Join(preparedRunEventsDir(project, profile, session, generation), "staged", role, "claims")
}

func preparedRunStagedClaimArtifactPath(project, profile, session, generation, role, claimID string) string {
	return filepath.Join(preparedRunStagedClaimsDir(project, profile, session, generation, role), claimID+".json")
}

func preparedRunStagedClaimActivePath(project, profile, session, generation, role string) string {
	return filepath.Join(preparedRunEventsDir(project, profile, session, generation), "staged", role, "active.json")
}

func preparedRunStagedTransitionsDir(project, profile, session, generation, role, claimID string) string {
	return filepath.Join(preparedRunEventsDir(project, profile, session, generation), "staged", role, "transitions", claimID)
}

func preparedRunStagedTransitionPath(project, profile, session, generation, role, claimID, transitionID string) string {
	return filepath.Join(preparedRunStagedTransitionsDir(project, profile, session, generation, role, claimID), transitionID+".json")
}

func preparedRunStagedConsumptionPath(project, profile, session, generation, role, claimID string) string {
	return filepath.Join(preparedRunStagedClaimsDir(project, profile, session, generation, role), claimID+".consumed.json")
}

func readPreparedRunStagedClaim(path string) (preparedRunStagedClaim, error) {
	var claim preparedRunStagedClaim
	data, err := os.ReadFile(path)
	if err != nil {
		return preparedRunStagedClaim{}, err
	}
	if err := json.Unmarshal(data, &claim); err != nil {
		return preparedRunStagedClaim{}, err
	}
	if claim.SchemaVersion != preparedRunStagedClaimSchema || claim.ClaimID == "" || claim.Role == "" || claim.Handle == "" || claim.BootstrapDigest == "" || !claim.GenerationRef.complete() {
		return preparedRunStagedClaim{}, fmt.Errorf("invalid staged claim %s", path)
	}
	return claim, nil
}

func readPreparedRunStagedClaimPointer(path string) (preparedRunStagedClaimPointer, error) {
	var pointer preparedRunStagedClaimPointer
	data, err := os.ReadFile(path)
	if err != nil {
		return preparedRunStagedClaimPointer{}, err
	}
	if err := json.Unmarshal(data, &pointer); err != nil {
		return preparedRunStagedClaimPointer{}, err
	}
	if pointer.SchemaVersion != preparedRunStagedClaimSchema || pointer.ClaimID == "" || pointer.Role == "" || pointer.Handle == "" || pointer.ClaimDigest == "" || pointer.ActivationID == "" || !pointer.GenerationRef.complete() {
		return preparedRunStagedClaimPointer{}, fmt.Errorf("invalid staged claim pointer %s", path)
	}
	return pointer, nil
}

func currentPreparedRunStagedClaim(project, profile, session string, token preparedRunToken, role string) (preparedRunStagedClaim, error) {
	pointerPath := preparedRunStagedClaimActivePath(project, profile, session, token.Generation, role)
	pointer, err := readPreparedRunStagedClaimPointer(pointerPath)
	if err != nil {
		return preparedRunStagedClaim{}, err
	}
	claim, err := preparedRunStagedClaimForPointer(project, profile, session, token, role, pointer)
	if err != nil {
		return preparedRunStagedClaim{}, err
	}
	if pointer.LifecycleState != stagedClaimStateAdmitted && pointer.LifecycleState != stagedClaimStateConsumed {
		return preparedRunStagedClaim{}, preparedRunIdentityMismatchf("staged claim for %s is %s, not active", role, pointer.LifecycleState)
	}
	if err := validatePreparedRunStagedTransition(project, profile, session, token, claim, pointer.ActivationID, stagedClaimStateAdmitted, ""); err != nil {
		return preparedRunStagedClaim{}, err
	}
	if priorID := strings.TrimSpace(claim.Lifecycle.SupersedesClaimID); priorID != "" {
		prior, err := readPreparedRunStagedClaim(preparedRunStagedClaimArtifactPath(project, profile, session, token.Generation, role, priorID))
		if err != nil {
			return preparedRunStagedClaim{}, preparedRunIdentityMismatchf("active staged replacement %s has no immutable prior claim %s", claim.ClaimID, priorID)
		}
		if err := validatePreparedRunStagedTransition(project, profile, session, token, prior, pointer.ActivationID, stagedClaimStateSuperseded, claim.ClaimID); err != nil {
			return preparedRunStagedClaim{}, err
		}
	}
	if pointer.LifecycleState == stagedClaimStateConsumed {
		if pointer.Consumption == nil || pointer.Consumption.LaunchAttempt != claim.ClaimID || pointer.Consumption.EventDigest == "" {
			return preparedRunStagedClaim{}, preparedRunIdentityMismatchf("active staged claim for %s has invalid consumption evidence", role)
		}
		if err := validatePreparedRunStagedTransition(project, profile, session, token, claim, pointer.ActivationID, stagedClaimStateConsumed, ""); err != nil {
			return preparedRunStagedClaim{}, err
		}
	}
	return claim, nil
}

func preparedRunStagedClaimForPointer(project, profile, session string, token preparedRunToken, role string, pointer preparedRunStagedClaimPointer) (preparedRunStagedClaim, error) {
	if !samePreparedRunGeneration(pointer.GenerationRef, token) || pointer.Role != role {
		return preparedRunStagedClaim{}, preparedRunIdentityMismatchf("active staged claim for %s belongs to different generation or role", role)
	}
	claimPath := preparedRunStagedClaimArtifactPath(project, profile, session, token.Generation, role, pointer.ClaimID)
	claim, err := readPreparedRunStagedClaim(claimPath)
	if err != nil {
		return preparedRunStagedClaim{}, err
	}
	data, err := marshalPreparedRunArtifact(claim)
	if err != nil {
		return preparedRunStagedClaim{}, err
	}
	if digestRunArtifactBytes(data) != pointer.ClaimDigest || claim.ClaimID != pointer.ClaimID || claim.Handle != pointer.Handle || !samePreparedRunGeneration(claim.GenerationRef, token) ||
		!reflect.DeepEqual(pointer.EffectiveIdentity, claim.Effective) {
		return preparedRunStagedClaim{}, preparedRunIdentityMismatchf("active staged claim for %s failed immutable digest validation", role)
	}
	return claim, nil
}

func appendPreparedRunStagedTransition(project, profile, session string, token preparedRunToken, claim preparedRunStagedClaim, claimDigest, activationID, state, supersededBy, reason string) (preparedRunStagedTransition, error) {
	transitionID, err := newPreparedRunGeneration()
	if err != nil {
		return preparedRunStagedTransition{}, err
	}
	transition := preparedRunStagedTransition{
		SchemaVersion: preparedRunStagedClaimSchema, TransitionID: transitionID, ActivationID: activationID,
		GenerationRef: token.generationRef(), Role: claim.Role, Handle: claim.Handle, ClaimID: claim.ClaimID,
		ClaimDigest: claimDigest, State: state, SupersededBy: supersededBy, Reason: strings.TrimSpace(reason), CreatedAt: time.Now().UTC(),
	}
	data, err := marshalPreparedRunArtifact(transition)
	if err != nil {
		return preparedRunStagedTransition{}, err
	}
	if err := durableCreateExclusive(preparedRunStagedTransitionPath(project, profile, session, token.Generation, claim.Role, claim.ClaimID, transitionID), data); err != nil {
		return preparedRunStagedTransition{}, fmt.Errorf("publish staged claim %s transition: %w", state, err)
	}
	return transition, nil
}

func validatePreparedRunStagedTransition(project, profile, session string, token preparedRunToken, claim preparedRunStagedClaim, activationID, state, supersededBy string) error {
	entries, err := os.ReadDir(preparedRunStagedTransitionsDir(project, profile, session, token.Generation, claim.Role, claim.ClaimID))
	if err != nil {
		return err
	}
	claimData, err := marshalPreparedRunArtifact(claim)
	if err != nil {
		return err
	}
	claimDigest := digestRunArtifactBytes(claimData)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(preparedRunStagedTransitionsDir(project, profile, session, token.Generation, claim.Role, claim.ClaimID), entry.Name()))
		if readErr != nil {
			return readErr
		}
		var transition preparedRunStagedTransition
		if json.Unmarshal(data, &transition) != nil {
			continue
		}
		if transition.SchemaVersion == preparedRunStagedClaimSchema && transition.TransitionID == strings.TrimSuffix(entry.Name(), ".json") &&
			transition.ActivationID == activationID && transition.State == state && transition.ClaimID == claim.ClaimID && transition.ClaimDigest == claimDigest &&
			transition.Role == claim.Role && transition.Handle == claim.Handle && transition.SupersededBy == supersededBy && samePreparedRunGeneration(transition.GenerationRef, token) {
			return nil
		}
	}
	return preparedRunIdentityMismatchf("staged claim %s lacks exact append-only %s transition", claim.ClaimID, state)
}

func admitPreparedRunStagedClaim(project, profile, session string, token preparedRunToken, request preparedRunStagedAdmissionRequest) (preparedRunStagedClaim, error) {
	if err := validatePreparedRunTokenPathIDs(token, false); err != nil {
		return preparedRunStagedClaim{}, err
	}
	if token.LaunchAttempt != "" {
		return preparedRunStagedClaim{}, preparedRunIdentityMismatchf("staged admission requires an unbound generation reference")
	}
	request.Role = strings.TrimSpace(request.Role)
	request.Handle = strings.TrimSpace(request.Handle)
	request.AuthorizingRole = strings.TrimSpace(request.AuthorizingRole)
	request.AuthorizingHandle = strings.TrimSpace(request.AuthorizingHandle)
	request.ActorMode = strings.TrimSpace(request.ActorMode)
	request.SupersedesClaimID = strings.TrimSpace(request.SupersedesClaimID)
	if err := team.ValidateRoleID(request.Role); err != nil {
		return preparedRunStagedClaim{}, preparedRunIdentityMismatchf("staged role %q is not canonical", request.Role)
	}
	if request.ActorMode != team.ActorModeReview && request.ActorMode != team.ActorModeImplementation {
		return preparedRunStagedClaim{}, preparedRunIdentityMismatchf("staged actor mode must be explicitly %s or %s", team.ActorModeReview, team.ActorModeImplementation)
	}
	if request.SupersedesClaimID != "" {
		if err := validatePreparedRunPathID("superseded staged claim", request.SupersedesClaimID); err != nil {
			return preparedRunStagedClaim{}, err
		}
	}

	var admitted preparedRunStagedClaim
	err := withPreparedRunStateLock(project, profile, session, token.Generation, func() error {
		manifest, err := currentPreparedRunManifestForToken(project, profile, session, token)
		if err != nil {
			return err
		}
		accepted, ok := manifest.StagedMembers[request.Role]
		if !ok || !containsRole(manifest.StagedRoster, request.Role) || accepted.Role != request.Role || accepted.Handle != request.Handle {
			return preparedRunIdentityMismatchf("generation %s has no exact staged actor identity for %s/%s", token.Generation, request.Role, request.Handle)
		}
		if err := validateCurrentPreparedStagedIdentity(project, manifest, request.Role); err != nil {
			return err
		}
		if err := preparedRunStagedTargetAbsent(project, profile, session, request.Handle); err != nil {
			return preparedRunIdentityMismatchf("staged target %s is not absent: %v", request.Handle, err)
		}
		terminal, err := readPreparedRunEvent(preparedRunTerminalEventPath(project, profile, session, token.Generation))
		if err != nil || terminal.Kind != preparedRunEventLaunched || !samePreparedRunGeneration(terminal.Token, token) || terminal.LaunchAttempt == "" {
			return preparedRunIdentityMismatchf("generation %s staged admission requires exact completed initial-launch evidence", token.Generation)
		}
		effective, err := narrowedPreparedStagedIdentity(accepted, request.ActorMode)
		if err != nil {
			return err
		}
		bootstrapDigest, err := preparedRunStagedBootstrapDigest(project, manifest, request.Role, request.Handle, accepted, effective)
		if err != nil {
			return err
		}
		authorizer, err := preparedRunStagedAuthorizerForRequest(project, profile, session, manifest, token, terminal, request)
		if err != nil {
			return err
		}

		var prior preparedRunStagedClaim
		activePath := preparedRunStagedClaimActivePath(project, profile, session, token.Generation, request.Role)
		pointer, pointerErr := readPreparedRunStagedClaimPointer(activePath)
		switch {
		case pointerErr == nil:
			prior, err = preparedRunStagedClaimForPointer(project, profile, session, token, request.Role, pointer)
			if err != nil {
				return err
			}
			if err := validatePreparedRunStagedPointerLifecycle(project, profile, session, token, prior, pointer); err != nil {
				return err
			}
			if request.SupersedesClaimID == "" {
				return preparedRunIdentityMismatchf("staged role %s already has active claim %s; replacement must name it explicitly", request.Role, pointer.ClaimID)
			}
			if pointer.ClaimID != request.SupersedesClaimID {
				return preparedRunIdentityMismatchf("staged replacement for %s names claim %s, current active claim is %s", request.Role, request.SupersedesClaimID, pointer.ClaimID)
			}
		case os.IsNotExist(pointerErr):
			if request.SupersedesClaimID != "" {
				return preparedRunIdentityMismatchf("staged replacement for %s names %s but no active claim exists", request.Role, request.SupersedesClaimID)
			}
		default:
			return pointerErr
		}

		claimID, err := newPreparedRunGeneration()
		if err != nil {
			return err
		}
		admitted = preparedRunStagedClaim{
			SchemaVersion:   preparedRunStagedClaimSchema,
			ClaimID:         claimID,
			GenerationRef:   token.generationRef(),
			Namespace:       manifest.Namespace,
			Profile:         manifest.Profile,
			Session:         manifest.Session,
			Role:            request.Role,
			Handle:          request.Handle,
			Effective:       effective,
			Accepted:        accepted,
			BootstrapDigest: bootstrapDigest,
			LaunchStrategy:  manifest.Topology,
			Authorizer:      authorizer,
			Lifecycle: preparedRunStagedLifecycle{
				State:                stagedClaimStatePending,
				SupersedesClaimID:    request.SupersedesClaimID,
				RequiresTargetAbsent: true,
				Reason:               strings.TrimSpace(request.LifecycleReason),
			},
			CreatedAt: time.Now().UTC(),
		}
		claimData, err := marshalPreparedRunArtifact(admitted)
		if err != nil {
			return err
		}
		claimPath := preparedRunStagedClaimArtifactPath(project, profile, session, token.Generation, request.Role, claimID)
		if err := durableCreateExclusive(claimPath, claimData); err != nil {
			return fmt.Errorf("publish immutable staged claim: %w", err)
		}
		activationID, err := newPreparedRunGeneration()
		if err != nil {
			return err
		}
		if _, err := appendPreparedRunStagedTransition(project, profile, session, token, admitted, digestRunArtifactBytes(claimData), activationID, stagedClaimStateAdmitted, "", request.LifecycleReason); err != nil {
			return err
		}
		if prior.ClaimID != "" {
			priorData, marshalErr := marshalPreparedRunArtifact(prior)
			if marshalErr != nil {
				return marshalErr
			}
			if _, err := appendPreparedRunStagedTransition(project, profile, session, token, prior, digestRunArtifactBytes(priorData), activationID, stagedClaimStateSuperseded, claimID, request.LifecycleReason); err != nil {
				return err
			}
		}
		if err := preparedRunStagedClaimBeforeActivate(); err != nil {
			_, transitionErr := appendPreparedRunStagedTransition(project, profile, session, token, admitted, digestRunArtifactBytes(claimData), activationID, stagedClaimStateFailed, "", err.Error())
			return errors.Join(err, transitionErr)
		}
		pointer = preparedRunStagedClaimPointer{
			SchemaVersion: preparedRunStagedClaimSchema, GenerationRef: token.generationRef(), Role: request.Role,
			Handle: request.Handle, ClaimID: claimID, ClaimDigest: digestRunArtifactBytes(claimData), ActivationID: activationID,
			LifecycleState: stagedClaimStateAdmitted, EffectiveIdentity: effective, UpdatedAt: time.Now().UTC(),
		}
		pointerData, err := marshalPreparedRunArtifact(pointer)
		if err != nil {
			return err
		}
		if err := preparedRunStagedReplaceCurrent(activePath, pointerData); err != nil {
			_, transitionErr := appendPreparedRunStagedTransition(project, profile, session, token, admitted, digestRunArtifactBytes(claimData), activationID, stagedClaimStateFailed, "", "active pointer update failed: "+err.Error())
			return errors.Join(fmt.Errorf("activate staged claim: %w", err), transitionErr)
		}
		return nil
	})
	return admitted, err
}

func preparedRunStagedBootstrapDigest(project string, manifest preparedRunManifest, role, handle string, accepted, effective preparedRunMemberIdentity) (string, error) {
	tm, err := team.ReadProfile(project, manifest.Profile)
	if err != nil {
		return "", fmt.Errorf("read staged admission profile: %w", err)
	}
	claim := preparedRunStagedClaim{Role: role, Handle: handle, Accepted: accepted, Effective: effective}
	projected := projectPreparedRunStagedMember(tm, claim)
	member, err := preparedRunStagedProjectedMember(projected, claim)
	if err != nil {
		return "", err
	}
	binding := acceptedGoalBinding{Text: manifest.GoalText, Source: manifest.GoalSource, Namespace: manifest.GoalNamespace, Digest: manifest.GoalDigest}
	if err := validateAcceptedGoalBinding(binding); err != nil {
		return "", err
	}
	bindingLine, err := expectedPreparedBootstrapBindingLine(projected, manifest.Profile, manifest.Session, member, binding)
	if err != nil {
		return "", err
	}
	if bindingLine != manifest.BootstrapBindings[role] {
		return "", preparedRunIdentityMismatchf("staged actor %s effective bootstrap binding differs from accepted preparation", role)
	}
	prompt, err := preparedBootstrap(project, manifest.Profile, manifest.Session, binding, projected, member, acceptedRunContext{Version: manifest.Environment.BinaryVersion, Topology: manifest.Topology})
	if err != nil {
		return "", fmt.Errorf("render staged effective bootstrap: %w", err)
	}
	return digestRunArtifactBytes([]byte(prompt)), nil
}

func narrowedPreparedStagedIdentity(accepted preparedRunMemberIdentity, actorMode string) (preparedRunMemberIdentity, error) {
	effective := accepted
	switch {
	case accepted.ActorMode == actorMode:
		if actorMode == team.ActorModeReview {
			return reviewOnlyPreparedStagedIdentity(effective), nil
		}
		return effective, nil
	case accepted.ActorMode == team.ActorModeImplementation && actorMode == team.ActorModeReview:
		return reviewOnlyPreparedStagedIdentity(effective), nil
	default:
		return preparedRunMemberIdentity{}, preparedRunIdentityMismatchf("staged permission widening refused: accepted actor_mode=%s requested actor_mode=%s", accepted.ActorMode, actorMode)
	}
}

func reviewOnlyPreparedStagedIdentity(identity preparedRunMemberIdentity) preparedRunMemberIdentity {
	identity.ActorMode = team.ActorModeReview
	identity.TaskOwnership = "read_only_review"
	identity.Trust = trustModeSandboxed
	identity.PermissionAllowlist = nil
	identity.LauncherAuthority = nil
	identity.NoPreauthorize = true
	identity.NativeArgs = reviewOnlyPreparedStagedArgs(identity.Binary, identity.NativeArgs)
	identity.EffectiveArgs = reviewOnlyPreparedStagedArgs(identity.Binary, identity.EffectiveArgs)
	return identity
}

func reviewOnlyPreparedStagedArgs(binary string, input []string) []string {
	args := append([]string(nil), input...)
	switch normalizedAgentBinary(binary) {
	case "codex":
		args = removeNativeBooleanArgs(args, "--dangerously-bypass-approvals-and-sandbox", "--dangerously-bypass-hook-trust")
		args = composeReviewOnlyNativeArgs("codex", args, []string{"--sandbox", "read-only", "--ask-for-approval", "on-request"})
	case "claude":
		args = removeNativeBooleanArgs(args, "--dangerously-skip-permissions", "--allow-dangerously-skip-permissions")
		args = replaceClaudeAllowedTools(args, nil)
		args = composeReviewOnlyNativeArgs("claude", args, []string{"--permission-mode", "plan"})
	}
	return args
}

func composeReviewOnlyNativeArgs(binary string, args, policy []string) []string {
	for index, arg := range args {
		if arg != "--" {
			continue
		}
		composed := composeBinaryArgs(binary, args[:index], policy)
		return append(composed, args[index:]...)
	}
	return composeBinaryArgs(binary, args, policy)
}

func removeNativeBooleanArgs(args []string, denied ...string) []string {
	blocked := make(map[string]bool, len(denied))
	for _, arg := range denied {
		blocked[arg] = true
	}
	out := make([]string, 0, len(args))
	for index, arg := range args {
		if arg == "--" {
			out = append(out, args[index:]...)
			break
		}
		deniedArg := blocked[arg]
		if !deniedArg {
			for name := range blocked {
				if strings.HasPrefix(arg, name+"=") {
					deniedArg = true
					break
				}
			}
		}
		if !deniedArg {
			out = append(out, arg)
		}
	}
	return out
}

func bindPreparedRunStagedLaunch(rec *launch.Record, context *preparedLaunchRecordContext, token preparedRunToken, expectedClaimID string) (preparedRunStagedClaim, error) {
	if context == nil || rec == nil {
		return preparedRunStagedClaim{}, preparedRunIdentityMismatchf("staged launch requires an accepted prepared context")
	}
	project := strings.TrimSpace(rec.TeamHome)
	if project == "" {
		project = strings.TrimSpace(rec.CWD)
	}
	claim, err := currentPreparedRunStagedClaim(project, rec.TeamProfile, rec.Session, token, rec.Role)
	if err != nil {
		return preparedRunStagedClaim{}, err
	}
	if claim.ClaimID != strings.TrimSpace(expectedClaimID) || claim.Handle != rec.Handle {
		return preparedRunStagedClaim{}, preparedRunIdentityMismatchf("staged launch claim changed; retry with exact active claim %s", claim.ClaimID)
	}
	contextIdentity := acceptedMemberIdentity(context.Team, context.Member, context.Manifest.Profile, context.Manifest.Session)
	if !reflect.DeepEqual(claim.Accepted, contextIdentity) {
		return preparedRunStagedClaim{}, preparedRunIdentityMismatchf("staged launch accepted identity differs from immutable claim")
	}
	if err := preparedRunStagedTargetAbsent(project, rec.TeamProfile, rec.Session, rec.Handle); err != nil {
		return preparedRunStagedClaim{}, preparedRunIdentityMismatchf("staged target %s is not absent: %v", rec.Handle, err)
	}
	if _, err := reverifyPreparedRunStagedAuthorizer(project, rec.TeamProfile, rec.Session, token, claim); err != nil {
		return preparedRunStagedClaim{}, err
	}
	applyPreparedRunStagedEffectiveIdentity(rec, claim.Effective)
	return claim, nil
}

func reverifyPreparedRunStagedAuthorizer(project, profile, session string, token preparedRunToken, claim preparedRunStagedClaim) (liveidentity.Result, error) {
	verified, err := preparedRunStagedVerifyAuthorizer(project, profile, session, claim.Authorizer.Handle)
	if err != nil || verified.Verified == nil {
		if err == nil {
			err = fmt.Errorf("runtime resolver returned no verified authorizer")
		}
		return liveidentity.Result{}, fmt.Errorf("verify staged launch authorizer: %w", err)
	}
	key := verified.Verified.Key
	if key.Handle != claim.Authorizer.Handle || key.LaunchID != claim.Authorizer.LaunchID || key.PreparedGeneration != token.Generation || key.PreparedDigest != token.ManifestDigest {
		return liveidentity.Result{}, preparedRunIdentityMismatchf("verified authorizer does not match claim launch and generation authority")
	}
	resultData, err := marshalPreparedRunArtifact(verified)
	if err != nil {
		return liveidentity.Result{}, err
	}
	if !reflect.DeepEqual(verified, claim.Authorizer.VerificationResult) || !reflect.DeepEqual(*verified.Verified, claim.Authorizer.Verified) || digestRunArtifactBytes(resultData) != claim.Authorizer.VerificationDigest {
		return liveidentity.Result{}, preparedRunIdentityMismatchf("verified authorizer changed since staged admission")
	}
	return verified, nil
}

func applyPreparedRunStagedEffectiveIdentity(rec *launch.Record, identity preparedRunMemberIdentity) {
	rec.Binary = identity.Binary
	rec.Model = identity.Model
	rec.Argv = append([]string(nil), identity.EffectiveArgs...)
	rec.ClaudeArgs = nil
	rec.CodexArgs = nil
	if identity.Binary == "codex" {
		rec.CodexArgs = append([]string(nil), identity.NativeArgs...)
	} else if identity.Binary == "claude" {
		rec.ClaudeArgs = append([]string(nil), identity.NativeArgs...)
	}
	rec.ToolProfile = identity.ToolProfile
	rec.ToolConfig = identity.ToolConfig
	rec.ToolMCPConfig = identity.ToolMCPConfig
	rec.ToolAllowlist = append([]string(nil), identity.ToolAllowlist...)
	rec.ToolBlocklist = append([]string(nil), identity.ToolBlocklist...)
	rec.Trust = identity.Trust
	rec.NoPreauthorizeInScope = identity.NoPreauthorize
	rec.PreauthorizedActions = nil
	rec.LauncherPreauthorizedActions = append([]string(nil), identity.LauncherAuthority...)
	rec.ExplicitAllowedTools = nil
}

func preparedRunStagedAuthorizerForRequest(project, profile, session string, manifest preparedRunManifest, token preparedRunToken, terminal preparedRunEvent, request preparedRunStagedAdmissionRequest) (preparedRunStagedAuthorizer, error) {
	accepted, ok := manifest.Members[request.AuthorizingRole]
	if !ok || !containsRole(manifest.InitialRoster, request.AuthorizingRole) || accepted.Handle != request.AuthorizingHandle {
		return preparedRunStagedAuthorizer{}, preparedRunIdentityMismatchf("staged admission authorizer %s/%s is not an exact initial-roster actor", request.AuthorizingRole, request.AuthorizingHandle)
	}
	env, err := resolveAMQEnvForTeamLaunchProfile(project, profile, session, request.AuthorizingHandle)
	if err != nil {
		return preparedRunStagedAuthorizer{}, err
	}
	agentDir := filepath.Join(absoluteAMQRoot(project, env.Root), "agents", request.AuthorizingHandle)
	rec, err := launch.Read(agentDir)
	if err != nil {
		return preparedRunStagedAuthorizer{}, preparedRunIdentityMismatchf("read staged admission authorizer launch record: %v", err)
	}
	recordToken := preparedRunTokenFromRecord(rec)
	if rec.Role != request.AuthorizingRole || rec.Handle != request.AuthorizingHandle || rec.TeamProfile != profile || rec.Session != session ||
		!samePreparedRunGeneration(recordToken, token) || recordToken.LaunchAttempt != terminal.LaunchAttempt || rec.BootstrapExpectation == nil || strings.TrimSpace(rec.BootstrapExpectation.LaunchID) == "" {
		return preparedRunStagedAuthorizer{}, preparedRunIdentityMismatchf("staged admission authorizer %s/%s lacks exact parent generation, launch, and launch-ID evidence", request.AuthorizingRole, request.AuthorizingHandle)
	}
	result, err := preparedRunStagedVerifyAuthorizer(project, profile, session, request.AuthorizingHandle)
	if err != nil || result.Verified == nil {
		if err == nil {
			err = fmt.Errorf("runtime resolver returned no verified identity")
		}
		return preparedRunStagedAuthorizer{}, fmt.Errorf("verify staged admission authorizer live identity: %w", err)
	}
	if err := validatePreparedRunStagedVerifiedAuthorizer(project, profile, session, token, request, rec, result); err != nil {
		return preparedRunStagedAuthorizer{}, err
	}
	resultData, err := marshalPreparedRunArtifact(result)
	if err != nil {
		return preparedRunStagedAuthorizer{}, err
	}
	return preparedRunStagedAuthorizer{
		Role: request.AuthorizingRole, Handle: request.AuthorizingHandle,
		LaunchID: rec.BootstrapExpectation.LaunchID, ParentLaunchAttempt: terminal.LaunchAttempt,
		GenerationRef: recordToken, Verified: *result.Verified, VerificationResult: result,
		VerificationDigest: digestRunArtifactBytes(resultData), VerifiedAt: time.Now().UTC(),
	}, nil
}

func validatePreparedRunStagedVerifiedAuthorizer(project, profile, session string, token preparedRunToken, request preparedRunStagedAdmissionRequest, rec launch.Record, result liveidentity.Result) error {
	if result.Verified == nil {
		return preparedRunIdentityMismatchf("verified staged authorizer result is empty")
	}
	verified := *result.Verified
	canonicalProject, err := liveidentity.CanonicalProject(project)
	if err != nil {
		return err
	}
	key := verified.Key
	if key.Project != canonicalProject || key.Profile != profile || key.Session != session || key.Handle != request.AuthorizingHandle ||
		key.PreparedGeneration != token.Generation || key.PreparedDigest != token.ManifestDigest || key.LaunchID == "" || key.LaunchID != rec.BootstrapExpectation.LaunchID ||
		verified.Role != request.AuthorizingRole || verified.Binary == "" || verified.Binary != normalizedAgentBinary(rec.Binary) || verified.Model == "" || verified.Model != rec.Model ||
		verified.PID <= 0 || verified.PID != rec.AgentPID || verified.Terminal.Backend == "" || (verified.Terminal.PaneID == "" && verified.Terminal.SessionID == "") || verified.Terminal != liveIdentityTerminal(rec) {
		return preparedRunIdentityMismatchf("verified staged authorizer is empty or differs from exact project/profile/session/handle/generation/launch/process/terminal identity")
	}
	if verified.WakePolicy == liveidentity.WakeRequired && (verified.WakePID <= 0 || verified.WakeTarget == "" || verified.WakeRecordID == "" || verified.ConsumerCount != 1 || result.LaunchRecord.WakeRecordDigest == "") {
		return preparedRunIdentityMismatchf("verified staged authorizer has incomplete exact wake consumer identity")
	}
	if verified.WakePolicy != liveidentity.WakeRequired && verified.WakePolicy != liveidentity.WakeDisabled {
		return preparedRunIdentityMismatchf("verified staged authorizer has invalid wake policy")
	}
	return nil
}

func validatePreparedRunStagedPointerLifecycle(project, profile, session string, token preparedRunToken, claim preparedRunStagedClaim, pointer preparedRunStagedClaimPointer) error {
	if err := validatePreparedRunStagedTransition(project, profile, session, token, claim, pointer.ActivationID, stagedClaimStateAdmitted, ""); err != nil {
		return err
	}
	switch pointer.LifecycleState {
	case stagedClaimStateAdmitted:
		if pointer.Consumption != nil {
			return preparedRunIdentityMismatchf("admitted staged claim carries consumption evidence")
		}
		return nil
	case stagedClaimStateConsumed:
		if pointer.Consumption == nil {
			return preparedRunIdentityMismatchf("consumed staged claim lacks consumption evidence")
		}
		return validatePreparedRunStagedTransition(project, profile, session, token, claim, pointer.ActivationID, stagedClaimStateConsumed, "")
	case stagedClaimStateAbandoned:
		if pointer.Consumption != nil {
			return preparedRunIdentityMismatchf("abandoned staged claim carries consumption evidence")
		}
		return validatePreparedRunStagedTransition(project, profile, session, token, claim, pointer.ActivationID, stagedClaimStateAbandoned, "")
	default:
		return preparedRunIdentityMismatchf("staged supersession source %s has invalid lifecycle state %s", claim.ClaimID, pointer.LifecycleState)
	}
}

func consumePreparedRunStagedClaimLocked(project, profile, session string, token preparedRunToken, role, handle string) error {
	claim, err := currentPreparedRunStagedClaim(project, profile, session, token, role)
	if err != nil {
		return err
	}
	pointerPath := preparedRunStagedClaimActivePath(project, profile, session, token.Generation, role)
	pointer, err := readPreparedRunStagedClaimPointer(pointerPath)
	if err != nil {
		return err
	}
	if pointer.LifecycleState == stagedClaimStateConsumed || pointer.Consumption != nil {
		return preparedRunIdentityMismatchf("staged launch claim replay refused for %s/%s", role, handle)
	}
	if claim.ClaimID != token.LaunchAttempt || claim.Role != role || claim.Handle != handle || pointer.LifecycleState != stagedClaimStateAdmitted {
		return preparedRunIdentityMismatchf("staged claim for %s/%s is stale, inactive, or belongs to different launch evidence", role, handle)
	}
	event := newPreparedRunEvent(preparedRunEventStagedClaim, token, token.LaunchAttempt)
	event.Role, event.Handle = role, handle
	event.Detail = "staged_launch_consumed"
	eventData, err := marshalPreparedRunArtifact(event)
	if err != nil {
		return err
	}
	if err := preparedRunStagedConsumptionBeforeEvent(); err != nil {
		return err
	}
	consumptionPath := preparedRunStagedConsumptionPath(project, profile, session, token.Generation, role, token.LaunchAttempt)
	if err := durableCreateExclusive(consumptionPath, eventData); err != nil {
		existing, readErr := os.ReadFile(consumptionPath)
		var recorded preparedRunEvent
		if readErr != nil || json.Unmarshal(existing, &recorded) != nil || recorded.SchemaVersion != event.SchemaVersion || recorded.Kind != event.Kind ||
			recorded.Role != event.Role || recorded.Handle != event.Handle || recorded.LaunchAttempt != event.LaunchAttempt || recorded.Detail != event.Detail || !samePreparedRunGeneration(recorded.Token, event.Token) {
			return preparedRunIdentityMismatchf("staged launch claim replay refused: %v", err)
		}
		eventData = existing
	}
	pointer.LifecycleState = stagedClaimStateConsumed
	pointer.Consumption = &preparedRunStagedConsumption{LaunchAttempt: token.LaunchAttempt, EventDigest: digestRunArtifactBytes(eventData), ConsumedAt: time.Now().UTC()}
	pointer.UpdatedAt = time.Now().UTC()
	pointerData, err := marshalPreparedRunArtifact(pointer)
	if err != nil {
		return err
	}
	if err := preparedRunStagedConsumptionBeforeTransition(); err != nil {
		return err
	}
	if err := validatePreparedRunStagedTransition(project, profile, session, token, claim, pointer.ActivationID, stagedClaimStateConsumed, ""); err != nil {
		if _, appendErr := appendPreparedRunStagedTransition(project, profile, session, token, claim, pointer.ClaimDigest, pointer.ActivationID, stagedClaimStateConsumed, "", "verified target launch consumed"); appendErr != nil {
			return appendErr
		}
	}
	if err := preparedRunStagedReplaceCurrent(pointerPath, pointerData); err != nil {
		return err
	}
	return nil
}

func consumePreparedRunStagedClaim(project, profile, session string, token preparedRunToken, role, handle string) error {
	return withPreparedRunStateLock(project, profile, session, token.Generation, func() error {
		return consumePreparedRunStagedClaimLocked(project, profile, session, token, role, handle)
	})
}

func abandonPreparedRunStagedClaim(project, profile, session string, token preparedRunToken, role, claimID, reason string) error {
	return withPreparedRunStateLock(project, profile, session, token.Generation, func() error {
		pointerPath := preparedRunStagedClaimActivePath(project, profile, session, token.Generation, role)
		pointer, err := readPreparedRunStagedClaimPointer(pointerPath)
		if err != nil {
			return err
		}
		claim, err := currentPreparedRunStagedClaim(project, profile, session, token, role)
		if err != nil {
			return err
		}
		if claim.ClaimID != claimID || pointer.LifecycleState != stagedClaimStateAdmitted || pointer.Consumption != nil {
			return preparedRunIdentityMismatchf("only the exact unconsumed active staged claim may be abandoned")
		}
		if _, err := appendPreparedRunStagedTransition(project, profile, session, token, claim, pointer.ClaimDigest, pointer.ActivationID, stagedClaimStateAbandoned, "", reason); err != nil {
			return err
		}
		pointer.LifecycleState = stagedClaimStateAbandoned
		pointer.UpdatedAt = time.Now().UTC()
		data, err := marshalPreparedRunArtifact(pointer)
		if err != nil {
			return err
		}
		return preparedRunStagedReplaceCurrent(pointerPath, data)
	})
}

func provePreparedRunStagedTargetAbsent(project, profile, session, handle string) error {
	env, err := resolveAMQEnvForTeamLaunchProfile(project, profile, session, handle)
	if err != nil {
		return err
	}
	agentDir := filepath.Join(absoluteAMQRoot(project, env.Root), "agents", handle)
	wake, wakeErr := readWakeLock(agentDir)
	if wakeErr == nil {
		if wake.PID <= 0 {
			return fmt.Errorf("wake consumer record has no valid PID")
		}
		if defaultDuplicateLaunchProbe.PIDAlive(wake.PID) {
			return fmt.Errorf("target has live wake consumer PID %d", wake.PID)
		}
	} else if !os.IsNotExist(wakeErr) {
		return fmt.Errorf("target wake consumer state is unverifiable: %w", wakeErr)
	}
	rec, err := launch.Read(agentDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if rec.AgentPID > 0 && defaultDuplicateLaunchProbe.PIDAlive(rec.AgentPID) {
		return fmt.Errorf("launch record has live agent PID %d", rec.AgentPID)
	}
	if rec.Tmux != nil && strings.TrimSpace(rec.Tmux.PaneID) != "" {
		if _, ok := statusPaneInspector(rec.Tmux.PaneID); ok {
			return fmt.Errorf("launch record still owns live pane %s", rec.Tmux.PaneID)
		}
	}
	return nil
}

func stagedClaimIdentityIsExact(claim preparedRunStagedClaim, identity preparedRunMemberIdentity) bool {
	return reflect.DeepEqual(claim.Effective, identity)
}
