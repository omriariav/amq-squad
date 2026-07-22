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

	"github.com/omriariav/amq-squad/v2/internal/flock"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

const (
	preparedRunPointerSchema = 1
	preparedRunStateSchema   = 1
	preparedRunEventSchema   = 1

	preparedRunEventReservation = "launch_reservation"
	preparedRunEventMember      = "member_consumed"
	preparedRunEventGoal        = "goal_consumed"
	preparedRunEventStagedClaim = "staged_spawn_consumed"
	preparedRunEventResume      = "resume_consumed"
	preparedRunEventLaunched    = "launch_completed"
	preparedRunEventFailed      = "launch_failed"
)

type preparedRunPointer struct {
	SchemaVersion  int       `json:"schema_version"`
	Generation     string    `json:"generation"`
	ManifestDigest string    `json:"manifest_digest"`
	StateDigest    string    `json:"initial_state_digest"`
	PublishedAt    time.Time `json:"published_at"`
}

// preparedRunInitialState is immutable and pointer-bound. Actor identities and
// claims are always derived from the immutable manifest, never from state.
type preparedRunInitialState struct {
	SchemaVersion int              `json:"schema_version"`
	Project       string           `json:"project"`
	Profile       string           `json:"profile"`
	Session       string           `json:"session"`
	Namespace     string           `json:"namespace"`
	Token         preparedRunToken `json:"generation_ref"`
	PreparedAt    time.Time        `json:"prepared_at"`
}

// preparedRunEvent is an append-only consumption fact. Every path is unique
// for the authority it consumes and is created with O_EXCL plus fsync.
type preparedRunEvent struct {
	SchemaVersion  int              `json:"schema_version"`
	Kind           string           `json:"kind"`
	Token          preparedRunToken `json:"generation_ref"`
	LaunchAttempt  string           `json:"launch_attempt,omitempty"`
	ResumeAttempt  string           `json:"resume_attempt,omitempty"`
	Role           string           `json:"role,omitempty"`
	Handle         string           `json:"handle,omitempty"`
	RecordDigest   string           `json:"record_digest,omitempty"`
	SemanticDigest string           `json:"semantic_digest,omitempty"`
	Detail         string           `json:"detail,omitempty"`
	CreatedAt      time.Time        `json:"created_at"`
}

var preparedRunPublishAfterArtifact = func(string) error { return nil }
var preparedRunBeforeExclusiveInstall = func(string) error { return nil }

func validatePreparedRunPathID(label, value string) error {
	if len(value) != 32 {
		return preparedRunIdentityMismatchf("%s %q is not a canonical prepared-run id", label, value)
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return preparedRunIdentityMismatchf("%s %q is not a canonical prepared-run id", label, value)
		}
	}
	return nil
}

func validatePreparedRunTokenPathIDs(token preparedRunToken, requireLaunchAttempt bool) error {
	if err := validatePreparedRunPathID("prepared generation", token.Generation); err != nil {
		return err
	}
	if token.LaunchAttempt == "" {
		if requireLaunchAttempt {
			return preparedRunIdentityMismatchf("prepared generation %s launch attempt is missing", token.Generation)
		}
		return nil
	}
	return validatePreparedRunPathID("prepared launch attempt", token.LaunchAttempt)
}

func preparedRunGenerationsPath(project, profile, session string) string {
	return filepath.Join(filepath.Dir(preparedRunPath(project, profile, session)), session+".generations")
}

func preparedRunGenerationDir(project, profile, session, generation string) string {
	return filepath.Join(preparedRunGenerationsPath(project, profile, session), generation)
}

func preparedRunGenerationManifestPath(project, profile, session, generation string) string {
	return filepath.Join(preparedRunGenerationDir(project, profile, session, generation), "manifest.json")
}

func preparedRunInitialStatePath(project, profile, session, generation string) string {
	return filepath.Join(preparedRunGenerationDir(project, profile, session, generation), "initial-state.json")
}

func preparedRunEventsDir(project, profile, session, generation string) string {
	return filepath.Join(preparedRunGenerationDir(project, profile, session, generation), "events")
}

func preparedRunStateLockPath(project, profile, session, generation string) string {
	return filepath.Join(preparedRunGenerationDir(project, profile, session, generation), "events.lock")
}

func preparedRunReservationPath(project, profile, session, generation string) string {
	return filepath.Join(preparedRunEventsDir(project, profile, session, generation), "launch-reservation.json")
}

func preparedRunMemberEventPath(project, profile, session, generation, role string) string {
	return filepath.Join(preparedRunEventsDir(project, profile, session, generation), "members", role+".json")
}

func preparedRunGoalEventPath(project, profile, session, generation string) string {
	return filepath.Join(preparedRunEventsDir(project, profile, session, generation), "goal.json")
}

// preparedRunStagedClaimPath is the retired single-file legacy staged claim
// location. It is kept only so tests can assert the path is never
// (re)created; staged consumption now always routes through the immutable
// claims/active-pointer system in prepared_run_staged_claim.go.
func preparedRunStagedClaimPath(project, profile, session, generation, role string) string {
	return filepath.Join(preparedRunEventsDir(project, profile, session, generation), "staged", role, "claim.json")
}

func preparedRunResumeEventPath(project, profile, session, generation, attempt string) string {
	return filepath.Join(preparedRunEventsDir(project, profile, session, generation), "resume", attempt+".json")
}

func preparedRunTerminalEventPath(project, profile, session, generation string) string {
	return filepath.Join(preparedRunEventsDir(project, profile, session, generation), "terminal.json")
}

func publishPreparedRunGeneration(project, profile, session string, manifest preparedRunManifest) error {
	if err := validatePreparedRunPathID("prepared generation", manifest.Generation); err != nil {
		return err
	}
	manifestData, err := marshalPreparedRunArtifact(manifest)
	if err != nil {
		return err
	}
	manifestDigest := digestRunArtifactBytes(manifestData)
	canonicalProject, err := canonicalDir(project)
	if err != nil {
		return err
	}
	initial := preparedRunInitialState{
		SchemaVersion: preparedRunStateSchema, Project: canonicalProject, Profile: profile, Session: session,
		Namespace: manifest.Namespace, Token: preparedRunTokenFromSnapshot(manifest, manifestDigest).generationRef(), PreparedAt: manifest.PreparedAt,
	}
	stateData, err := marshalPreparedRunArtifact(initial)
	if err != nil {
		return err
	}
	stateDigest := digestRunArtifactBytes(stateData)
	for _, artifact := range []struct {
		stage string
		path  string
		data  []byte
	}{
		{"manifest", preparedRunGenerationManifestPath(project, profile, session, manifest.Generation), manifestData},
		{"initial_state", preparedRunInitialStatePath(project, profile, session, manifest.Generation), stateData},
	} {
		if err := durableCreateExclusive(artifact.path, artifact.data); err != nil {
			return fmt.Errorf("publish prepared generation artifact %s: %w", artifact.path, err)
		}
		if err := preparedRunPublishAfterArtifact(artifact.stage); err != nil {
			return err
		}
	}
	pointer := preparedRunPointer{SchemaVersion: preparedRunPointerSchema, Generation: manifest.Generation, ManifestDigest: manifestDigest, StateDigest: stateDigest, PublishedAt: time.Now().UTC()}
	pointerData, err := marshalPreparedRunArtifact(pointer)
	if err != nil {
		return err
	}
	return durableReplace(preparedRunPath(project, profile, session), pointerData)
}

func reservePreparedRunLaunch(project, profile, session string, token preparedRunToken) (string, error) {
	if err := validatePreparedRunTokenPathIDs(token, false); err != nil {
		return "", err
	}
	if token.LaunchAttempt != "" {
		return "", fmt.Errorf("prepared generation reservation requires an unbound generation reference")
	}
	var attempt string
	err := withPreparedRunStateLock(project, profile, session, token.Generation, func() error {
		if _, err := currentPreparedRunManifestForToken(project, profile, session, token); err != nil {
			return err
		}
		if _, err := os.Stat(preparedRunTerminalEventPath(project, profile, session, token.Generation)); err == nil {
			return preparedRunIdentityMismatchf("prepared generation %s already has terminal launch evidence", token.Generation)
		} else if !os.IsNotExist(err) {
			return err
		}
		var err error
		attempt, err = newPreparedRunGeneration()
		if err != nil {
			return err
		}
		event := newPreparedRunEvent(preparedRunEventReservation, token, attempt)
		return createPreparedRunEvent(preparedRunReservationPath(project, profile, session, token.Generation), event, "prepared launch reservation replay refused")
	})
	return attempt, err
}

func consumePreparedRunMember(project, profile, session string, token preparedRunToken, role, handle string) error {
	if err := validatePreparedRunTokenPathIDs(token, true); err != nil {
		return err
	}
	return withPreparedRunStateLock(project, profile, session, token.Generation, func() error {
		manifest, err := currentPreparedRunManifestForToken(project, profile, session, token)
		if err != nil {
			return err
		}
		if containsRole(manifest.StagedRoster, role) {
			// Staged consumption always goes through the immutable claim
			// system (prepared_run_staged_claim.go) so it stays
			// resume-recognizable via validateConsumedPreparedRunStagedClaim.
			// This branch is reached by any caller that holds an admitted
			// claim bound as the exact launch attempt (token.LaunchAttempt),
			// not only the parent-owned staged launch transaction.
			return consumePreparedRunStagedClaimLocked(project, profile, session, token, role, handle)
		}
		if err := validatePreparedLaunchReservation(project, profile, session, token); err != nil {
			return err
		}
		identity, ok := manifest.Members[role]
		if !ok || !containsRole(manifest.InitialRoster, role) || strings.TrimSpace(identity.Handle) != strings.TrimSpace(handle) {
			return preparedRunIdentityMismatchf("generation %s has no exact initial actor identity for %s/%s", token.Generation, role, handle)
		}
		event := newPreparedRunEvent(preparedRunEventMember, token, token.LaunchAttempt)
		event.Role, event.Handle = role, identity.Handle
		return createPreparedRunEvent(preparedRunMemberEventPath(project, profile, session, token.Generation, role), event, "prepared member claim replay refused")
	})
}

func validateCurrentPreparedStagedIdentity(project string, manifest preparedRunManifest, role string) error {
	tm, err := team.ReadProfile(project, manifest.Profile)
	if err != nil {
		return preparedRunIdentityMismatchf("read staged actor %s profile identity: %v", role, err)
	}
	var member team.Member
	found := false
	for _, candidate := range tm.Members {
		if candidate.Role == role {
			member, found = candidate, true
			break
		}
	}
	accepted, ok := manifest.StagedMembers[role]
	if !found || !ok || !reflect.DeepEqual(accepted, acceptedMemberIdentity(tm, member, manifest.Profile, manifest.Session)) {
		return preparedRunIdentityMismatchf("staged actor %s full binary/model/args/tool identity drifted from generation %s", role, manifest.Generation)
	}
	roleDigest, _, err := roleContractDigest(project, role)
	if err != nil || manifest.RoleDigests[role] != roleDigest {
		return preparedRunIdentityMismatchf("staged actor %s role contract drifted from generation %s", role, manifest.Generation)
	}
	binding := acceptedGoalBinding{Text: manifest.GoalText, Source: manifest.GoalSource, Namespace: manifest.GoalNamespace, Digest: manifest.GoalDigest}
	context := acceptedRunContext{Version: manifest.Environment.BinaryVersion, Topology: manifest.Topology}
	prompt, err := preparedBootstrap(project, manifest.Profile, manifest.Session, binding, tm, member, context)
	if err != nil || manifest.BootstrapDigests[role] != digestRunArtifactBytes([]byte(prompt)) {
		return preparedRunIdentityMismatchf("staged actor %s bootstrap digest drifted from generation %s", role, manifest.Generation)
	}
	bindingLine, err := expectedPreparedBootstrapBindingLine(tm, manifest.Profile, manifest.Session, member, binding)
	if err != nil || manifest.BootstrapBindings[role] != bindingLine {
		return preparedRunIdentityMismatchf("staged actor %s bootstrap binding drifted from generation %s", role, manifest.Generation)
	}
	return nil
}

func consumePreparedRunGoal(project, profile, session string, token preparedRunToken, role string) error {
	if err := validatePreparedRunTokenPathIDs(token, true); err != nil {
		return err
	}
	return withPreparedRunStateLock(project, profile, session, token.Generation, func() error {
		return consumePreparedRunGoalLocked(project, profile, session, token, role)
	})
}

func consumePreparedRunGoalLocked(project, profile, session string, token preparedRunToken, role string) error {
	manifest, err := currentPreparedRunManifestForToken(project, profile, session, token)
	if err != nil {
		return err
	}
	if err := validatePreparedLaunchReservation(project, profile, session, token); err != nil {
		return err
	}
	if strings.TrimSpace(manifest.Lead) != strings.TrimSpace(role) {
		return preparedRunIdentityMismatchf("generation %s accepted goal belongs to lead %s, not %s", token.Generation, manifest.Lead, role)
	}
	event := newPreparedRunEvent(preparedRunEventGoal, token, token.LaunchAttempt)
	event.Role = manifest.Lead
	return createPreparedRunEvent(preparedRunGoalEventPath(project, profile, session, token.Generation), event, "prepared goal claim replay refused")
}

func consumePreparedRunResume(project, profile, session string, token preparedRunToken, rec launch.Record, desc preparedRestoreDescriptor) error {
	if err := validatePreparedRunTokenPathIDs(token, true); err != nil {
		return err
	}
	if err := validatePreparedRunTokenPathIDs(desc.Token, true); err != nil {
		return err
	}
	if err := validatePreparedRunPathID("prepared resume attempt", desc.AttemptID); err != nil {
		return err
	}
	if desc.AttemptID == "" || !samePreparedRunGeneration(desc.Token, token) || desc.SemanticDigest != preparedRestoreSemanticDigest(rec) {
		return preparedRunIdentityMismatchf("prepared resume descriptor changed before generation claim")
	}
	return withPreparedRunStateLock(project, profile, session, token.Generation, func() error {
		manifest, err := currentPreparedRunManifestForToken(project, profile, session, token)
		if err != nil {
			return err
		}
		terminal, err := readPreparedRunEvent(preparedRunTerminalEventPath(project, profile, session, token.Generation))
		if err != nil || terminal.Kind != preparedRunEventLaunched || !samePreparedRunGeneration(terminal.Token, token) {
			return preparedRunIdentityMismatchf("managed resume refused for generation %s without exact completed-launch evidence", token.Generation)
		}
		if containsRole(manifest.StagedRoster, rec.Role) {
			if err := validateConsumedPreparedRunStagedClaim(project, profile, session, token, rec.Role, rec.Handle); err != nil {
				return preparedRunIdentityMismatchf("managed resume refused for staged actor %s/%s without exact staged-spawn evidence", rec.Role, rec.Handle)
			}
		} else {
			if terminal.LaunchAttempt != token.LaunchAttempt {
				return preparedRunIdentityMismatchf("managed resume refused for generation %s without exact completed-launch evidence", token.Generation)
			}
			if _, err := readExactPreparedLaunchReservation(project, profile, session, token); err != nil {
				return preparedRunIdentityMismatchf("managed resume refused for generation %s without exact launch reservation evidence", token.Generation)
			}
		}
		expectedHandle := ""
		if identity, ok := manifest.Members[rec.Role]; ok && containsRole(manifest.InitialRoster, rec.Role) {
			expectedHandle = identity.Handle
		} else if identity, ok := manifest.StagedMembers[rec.Role]; ok && containsRole(manifest.StagedRoster, rec.Role) {
			expectedHandle = identity.Handle
		}
		if expectedHandle == "" || expectedHandle != rec.Handle {
			return preparedRunIdentityMismatchf("managed resume actor %s/%s is outside generation %s", rec.Role, rec.Handle, token.Generation)
		}
		event := newPreparedRunEvent(preparedRunEventResume, token, terminal.LaunchAttempt)
		event.LaunchAttempt = token.LaunchAttempt
		event.ResumeAttempt, event.Role, event.Handle = desc.AttemptID, rec.Role, expectedHandle
		event.RecordDigest, event.SemanticDigest = desc.RecordDigest, desc.SemanticDigest
		return createPreparedRunEvent(preparedRunResumeEventPath(project, profile, session, token.Generation, desc.AttemptID), event, "managed resume attempt replay refused")
	})
}

func completePreparedRunLaunch(project, profile, session string, token preparedRunToken) error {
	if err := validatePreparedRunTokenPathIDs(token, true); err != nil {
		return err
	}
	return withPreparedRunStateLock(project, profile, session, token.Generation, func() error {
		manifest, err := currentPreparedRunManifestForToken(project, profile, session, token)
		if err != nil {
			return err
		}
		if err := validatePreparedLaunchReservation(project, profile, session, token); err != nil {
			return err
		}
		for _, role := range manifest.InitialRoster {
			event, err := readPreparedRunEvent(preparedRunMemberEventPath(project, profile, session, token.Generation, role))
			identity := manifest.Members[role]
			if err != nil || event.Kind != preparedRunEventMember || event.LaunchAttempt != token.LaunchAttempt || !samePreparedRunGeneration(event.Token, token) || event.Role != role || event.Handle != identity.Handle {
				return fmt.Errorf("complete prepared generation %s: initial member %s has no exact consumption evidence", token.Generation, role)
			}
		}
		goal, err := readPreparedRunEvent(preparedRunGoalEventPath(project, profile, session, token.Generation))
		if err != nil || goal.Kind != preparedRunEventGoal || goal.LaunchAttempt != token.LaunchAttempt || !samePreparedRunGeneration(goal.Token, token) || goal.Role != manifest.Lead {
			return fmt.Errorf("complete prepared generation %s: accepted goal has no exact consumption evidence", token.Generation)
		}
		event := newPreparedRunEvent(preparedRunEventLaunched, token, token.LaunchAttempt)
		return createPreparedRunEvent(preparedRunTerminalEventPath(project, profile, session, token.Generation), event, "prepared launch already has terminal evidence")
	})
}

func failPreparedRunLaunch(project, profile, session string, token preparedRunToken, detail string) error {
	if err := validatePreparedRunTokenPathIDs(token, true); err != nil {
		return err
	}
	return withPreparedRunStateLock(project, profile, session, token.Generation, func() error {
		if _, err := currentPreparedRunManifestForToken(project, profile, session, token); err != nil {
			return err
		}
		if err := validatePreparedLaunchReservation(project, profile, session, token); err != nil {
			return err
		}
		event := newPreparedRunEvent(preparedRunEventFailed, token, token.LaunchAttempt)
		event.Detail = strings.TrimSpace(detail)
		return createPreparedRunEvent(preparedRunTerminalEventPath(project, profile, session, token.Generation), event, "prepared launch already has terminal evidence")
	})
}

func currentPreparedRunManifestForToken(project, profile, session string, token preparedRunToken) (preparedRunManifest, error) {
	if err := validatePreparedRunTokenPathIDs(token, false); err != nil {
		return preparedRunManifest{}, err
	}
	manifest, digest, err := readPreparedRunManifestSnapshot(project, profile, session)
	if err != nil {
		return preparedRunManifest{}, preparedRunIdentityMismatchf("prepared generation %s current accepted pointer is unavailable or invalid: %v", token.Generation, err)
	}
	if !samePreparedRunGeneration(token, preparedRunTokenFromSnapshot(manifest, digest)) {
		return preparedRunManifest{}, preparedRunIdentityMismatchf("prepared generation %s is no longer the current accepted generation", token.Generation)
	}
	return manifest, nil
}

func validatePreparedLaunchReservation(project, profile, session string, token preparedRunToken) error {
	if _, err := readExactPreparedLaunchReservation(project, profile, session, token); err != nil {
		return err
	}
	if _, err := os.Stat(preparedRunTerminalEventPath(project, profile, session, token.Generation)); err == nil {
		return preparedRunIdentityMismatchf("prepared generation %s already has terminal launch evidence", token.Generation)
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func readExactPreparedLaunchReservation(project, profile, session string, token preparedRunToken) (preparedRunEvent, error) {
	if err := validatePreparedRunTokenPathIDs(token, true); err != nil {
		return preparedRunEvent{}, err
	}
	event, err := readPreparedRunEvent(preparedRunReservationPath(project, profile, session, token.Generation))
	if err != nil || event.Kind != preparedRunEventReservation || event.LaunchAttempt != token.LaunchAttempt || !samePreparedRunGeneration(event.Token, token) {
		return preparedRunEvent{}, preparedRunIdentityMismatchf("prepared generation %s launch reservation changed or is missing", token.Generation)
	}
	return event, nil
}

func newPreparedRunEvent(kind string, token preparedRunToken, launchAttempt string) preparedRunEvent {
	return preparedRunEvent{SchemaVersion: preparedRunEventSchema, Kind: kind, Token: token.generationRef(), LaunchAttempt: launchAttempt, CreatedAt: time.Now().UTC()}
}

func createPreparedRunEvent(path string, event preparedRunEvent, replay string) error {
	data, err := marshalPreparedRunArtifact(event)
	if err != nil {
		return err
	}
	if err := durableCreateExclusive(path, data); err != nil {
		if errors.Is(err, os.ErrExist) {
			return preparedRunIdentityMismatchf("%s: %s", replay, path)
		}
		return err
	}
	return nil
}

func readPreparedRunEvent(path string) (preparedRunEvent, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return preparedRunEvent{}, err
	}
	var event preparedRunEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return preparedRunEvent{}, err
	}
	if event.SchemaVersion != preparedRunEventSchema || event.Kind == "" || !event.Token.complete() {
		return preparedRunEvent{}, fmt.Errorf("invalid prepared generation event %s", path)
	}
	return event, nil
}

func withPreparedRunStateLock(project, profile, session, generation string, fn func() error) error {
	if err := validatePreparedRunPathID("prepared generation", generation); err != nil {
		return err
	}
	path := preparedRunStateLockPath(project, profile, session, generation)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return flock.WithLock(path, fn)
}

func readPreparedRunInitialState(path string) (preparedRunInitialState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return preparedRunInitialState{}, err
	}
	var state preparedRunInitialState
	if err := json.Unmarshal(data, &state); err != nil {
		return preparedRunInitialState{}, err
	}
	if state.SchemaVersion != preparedRunStateSchema || !state.Token.complete() {
		return preparedRunInitialState{}, fmt.Errorf("invalid prepared generation initial state %s", path)
	}
	return state, nil
}

func marshalPreparedRunArtifact(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func durableCreateExclusive(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".prepared-exclusive-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if err := f.Chmod(0o644); err != nil {
		_ = f.Close()
		return err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := preparedRunBeforeExclusiveInstall(path); err != nil {
		return err
	}
	// Linking a fully written same-directory inode installs the authoritative
	// name atomically and fails with EEXIST instead of replacing a winner.
	if err := os.Link(tmp, path); err != nil {
		return err
	}
	if err := syncPreparedRunDir(dir); err != nil {
		return err
	}
	// The authoritative link is already complete and durable. A crash before
	// this cleanup can leave only a harmless hidden temp link.
	if err := os.Remove(tmp); err == nil {
		_ = syncPreparedRunDir(dir)
	}
	return nil
}

func durableReplace(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".prepared-run-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if err := f.Chmod(0o644); err != nil {
		_ = f.Close()
		return err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return syncPreparedRunDir(filepath.Dir(path))
}

func syncPreparedRunDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
