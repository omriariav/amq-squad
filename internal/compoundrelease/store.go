// Package compoundrelease persists the crash-recoverable publication state
// for strict compound releases. It deliberately contains no AMQ send or
// authorization policy wiring.
package compoundrelease

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"syscall"

	"github.com/omriariav/amq-squad/v2/internal/flock"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

const storeSchemaVersion = 1

const (
	childPublicationPlanned   = "planned"
	childPublicationSending   = "sending"
	childPublicationPublished = "published"
	childPublicationAdopted   = "adopted"
	childPublicationConflict  = "conflict"
)

var (
	ErrSpecChangeRequiresSuccessor = errors.New("release spec changed; explicit successor preparation is required")
	storeContainmentHook           = func(string, string) error { return nil }
	storeFault                     = func(string) error { return nil }
	storeFileSync                  = func(f *os.File) error { return f.Sync() }
	storeDirectorySync             = syncDirectoryFile
)

type Scope struct {
	ProjectDir          string
	Profile             string
	Session             string
	NamespaceGeneration string
	ParentGate          string
}

type Pointer struct {
	SchemaVersion           int    `json:"schema_version"`
	Revision                uint64 `json:"revision"`
	SeriesID                string `json:"series_id"`
	State                   string `json:"state"`
	Generation              uint64 `json:"generation"`
	GenerationID            string `json:"generation_id"`
	PreparedManifestID      string `json:"prepared_manifest_id"`
	PreparedSHA256          string `json:"prepared_sha256"`
	ActiveManifestID        string `json:"active_manifest_id,omitempty"`
	ActiveSHA256            string `json:"active_sha256,omitempty"`
	PredecessorGenerationID string `json:"predecessor_generation_id,omitempty"`
	SuccessorGenerationID   string `json:"successor_generation_id,omitempty"`
}

type childPublicationRecord struct {
	Role               string                                    `json:"role"`
	Ordinal            int                                       `json:"ordinal"`
	AttemptID          string                                    `json:"attempt_id"`
	State              string                                    `json:"state"`
	ClaimRevision      uint64                                    `json:"claim_revision,omitempty"`
	ClaimToken         string                                    `json:"claim_token,omitempty"`
	QuestionMessageID  string                                    `json:"question_message_id,omitempty"`
	ReceiptPath        string                                    `json:"receipt_path,omitempty"`
	ReceiptSHA256      string                                    `json:"receipt_sha256,omitempty"`
	Receipt            *operatorauth.ReleaseDeliveryReceiptTuple `json:"receipt,omitempty"`
	ConflictReason     string                                    `json:"conflict_reason,omitempty"`
	ObservedMessageIDs []string                                  `json:"observed_message_ids,omitempty"`
}

type generationRecord struct {
	SchemaVersion           int                      `json:"schema_version"`
	Revision                uint64                   `json:"revision"`
	SeriesID                string                   `json:"series_id"`
	State                   string                   `json:"state"`
	Generation              uint64                   `json:"generation"`
	GenerationID            string                   `json:"generation_id"`
	PreparedManifestID      string                   `json:"prepared_manifest_id"`
	PreparedSHA256          string                   `json:"prepared_sha256"`
	ActiveManifestID        string                   `json:"active_manifest_id,omitempty"`
	ActiveSHA256            string                   `json:"active_sha256,omitempty"`
	PredecessorGenerationID string                   `json:"predecessor_generation_id,omitempty"`
	SuccessorGenerationID   string                   `json:"successor_generation_id,omitempty"`
	Children                []childPublicationRecord `json:"children"`
}

type Snapshot struct {
	Pointer  Pointer
	Prepared operatorauth.PreparedReleaseManifest
	Active   *operatorauth.ActiveReleaseManifest
}

type Store struct {
	scope    Scope
	seriesID string
	dir      *os.Root
	dirPath  string
}

func Open(scope Scope, create bool) (*Store, error) {
	scope.Profile = squadnamespace.NormalizeProfile(scope.Profile)
	scope.Session = strings.TrimSpace(scope.Session)
	if scope.Profile != team.DefaultProfile {
		if err := team.ValidateProfileName(scope.Profile); err != nil {
			return nil, err
		}
	}
	if err := team.ValidateSessionName(scope.Session); err != nil {
		return nil, err
	}
	if err := operatorauth.ValidateCanonicalGateThread(scope.ParentGate); err != nil {
		return nil, err
	}
	if err := operatorauth.ValidateCanonicalSingleLineField("namespace generation", scope.NamespaceGeneration, true); err != nil {
		return nil, err
	}
	if strings.TrimSpace(scope.ProjectDir) == "" {
		return nil, fmt.Errorf("project directory is required")
	}
	seriesID := seriesIdentity(scope)
	rel := filepath.Join(team.DirName, "evidence", scope.Profile, scope.Session, "compound-release", seriesID)
	dir, path, err := openContainedRoot(scope.ProjectDir, rel, create)
	if err != nil {
		return nil, err
	}
	store := &Store{scope: scope, seriesID: seriesID, dir: dir, dirPath: path}
	if create {
		lock, lockErr := store.openLeaf("store.lock", os.O_CREATE|os.O_RDWR, 0o600, true)
		if lockErr != nil {
			dir.Close()
			return nil, lockErr
		}
		if syncErr := storeFileSync(lock); syncErr != nil {
			lock.Close()
			dir.Close()
			return nil, syncErr
		}
		lock.Close()
		if syncErr := store.syncDirectory(); syncErr != nil {
			dir.Close()
			return nil, syncErr
		}
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.dir == nil {
		return nil
	}
	return s.dir.Close()
}

func (s *Store) Create(spec operatorauth.ReleaseSpec) (Snapshot, error) {
	if err := s.validateSpecScope(spec); err != nil {
		return Snapshot{}, err
	}
	var result Snapshot
	err := s.withLock(func() error {
		pointer, err := s.readPointer()
		if err == nil {
			prepared, readErr := s.readPrepared(pointer.Generation)
			if readErr != nil {
				return readErr
			}
			want, deriveErr := operatorauth.DerivePreparedRelease(spec, pointer.Generation)
			if deriveErr != nil {
				return deriveErr
			}
			if !reflect.DeepEqual(prepared, want) {
				return ErrSpecChangeRequiresSuccessor
			}
			result, err = s.snapshotFrom(pointer, prepared)
			return err
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		prepared, err := operatorauth.DerivePreparedRelease(spec, 1)
		if err != nil {
			return err
		}
		if err := s.writePrepared(prepared); err != nil {
			return err
		}
		if err := storeFault("after_prepared_write"); err != nil {
			return err
		}
		record := newGenerationRecord(s.seriesID, prepared, "")
		if err := s.writeGeneration(record); err != nil {
			return err
		}
		pointer = pointerFromRecord(record, 1)
		if err := s.writePointer(pointer); err != nil {
			return err
		}
		result = Snapshot{Pointer: pointer, Prepared: prepared}
		return nil
	})
	return result, err
}

func (s *Store) StartPublishing(expectedGenerationID string) (Snapshot, error) {
	var result Snapshot
	err := s.withLock(func() error {
		pointer, err := s.readPointer()
		if err != nil {
			return err
		}
		if pointer.GenerationID != expectedGenerationID {
			return fmt.Errorf("current release generation changed")
		}
		prepared, err := s.readPrepared(pointer.Generation)
		if err != nil {
			return err
		}
		if pointer.State == operatorauth.ReleaseStatePublishing {
			result, err = s.snapshotFrom(pointer, prepared)
			return err
		}
		if pointer.State != operatorauth.ReleaseStatePlanned {
			return fmt.Errorf("release generation is %s, not planned", pointer.State)
		}
		record, err := s.readGeneration(pointer.Generation)
		if err != nil {
			return err
		}
		if record.State != operatorauth.ReleaseStatePlanned && record.State != operatorauth.ReleaseStatePublishing {
			return fmt.Errorf("release generation record cannot enter publishing from %s", record.State)
		}
		if record.State == operatorauth.ReleaseStatePlanned {
			if err := s.validateLifecycleSnapshot(pointer, record, prepared, nil); err != nil {
				return err
			}
			record.Revision++
			record.State = operatorauth.ReleaseStatePublishing
			if err := s.writeGeneration(record); err != nil {
				return err
			}
		} else {
			ahead := pointer
			ahead.State = operatorauth.ReleaseStatePublishing
			if err := s.validateLifecycleSnapshot(ahead, record, prepared, nil); err != nil {
				return fmt.Errorf("publishing record-ahead recovery: %w", err)
			}
		}
		pointer.Revision++
		pointer.State = operatorauth.ReleaseStatePublishing
		if err := s.writePointer(pointer); err != nil {
			return err
		}
		result = Snapshot{Pointer: pointer, Prepared: prepared}
		return nil
	})
	return result, err
}

func (s *Store) Activate(active operatorauth.ActiveReleaseManifest) (Snapshot, error) {
	return s.activate(active, nil)
}

func (s *Store) activate(active operatorauth.ActiveReleaseManifest, transitionHook func(string) error) (Snapshot, error) {
	var result Snapshot
	err := s.withLock(func() error {
		pointer, err := s.readPointer()
		if err != nil {
			return err
		}
		if pointer.GenerationID != active.GenerationID {
			return fmt.Errorf("active manifest is not for current generation")
		}
		prepared, err := s.readPrepared(pointer.Generation)
		if err != nil {
			return err
		}
		if err := operatorauth.ValidateActiveRelease(prepared, active); err != nil {
			return err
		}
		if pointer.State == operatorauth.ReleaseStateActive {
			snapshot, readErr := s.snapshotFrom(pointer, prepared)
			if readErr != nil {
				return readErr
			}
			if snapshot.Active == nil || !reflect.DeepEqual(*snapshot.Active, active) {
				return fmt.Errorf("current generation already has a different active manifest")
			}
			result = snapshot
			return nil
		}
		if pointer.State != operatorauth.ReleaseStatePublishing {
			return fmt.Errorf("release generation is %s, not publishing", pointer.State)
		}
		record, err := s.readGeneration(pointer.Generation)
		if err != nil {
			return err
		}
		if record.State != operatorauth.ReleaseStatePublishing && record.State != operatorauth.ReleaseStateActive {
			return fmt.Errorf("release generation record cannot activate from %s", record.State)
		}
		if record.State == operatorauth.ReleaseStatePublishing {
			if err := s.validateLifecycleSnapshot(pointer, record, prepared, nil); err != nil {
				return err
			}
			if err := validatePublishedChildrenForActivation(record, prepared, active); err != nil {
				return err
			}
		} else {
			ahead := pointer
			ahead.State = operatorauth.ReleaseStateActive
			ahead.ActiveManifestID = record.ActiveManifestID
			ahead.ActiveSHA256 = record.ActiveSHA256
			if err := s.validateLifecycleSnapshot(ahead, record, prepared, &active); err != nil {
				return fmt.Errorf("active record-ahead recovery: %w", err)
			}
		}
		if transitionHook != nil {
			if err := transitionHook("active-write"); err != nil {
				return err
			}
		}
		if err := s.writeActive(active); err != nil {
			return err
		}
		if err := storeFault("after_active_manifest_write"); err != nil {
			return err
		}
		if record.State == operatorauth.ReleaseStatePublishing {
			record.Revision++
			record.State = operatorauth.ReleaseStateActive
			record.ActiveManifestID = active.ActiveManifestID
			record.ActiveSHA256 = operatorauth.ActiveReleaseSHA256(active)
			record.Children = adoptedChildRecords(record.Children, prepared, active)
			if err := s.writeGeneration(record); err != nil {
				return err
			}
		}
		pointer.Revision++
		pointer.State = operatorauth.ReleaseStateActive
		pointer.ActiveManifestID = record.ActiveManifestID
		pointer.ActiveSHA256 = record.ActiveSHA256
		if transitionHook != nil {
			if err := transitionHook("pointer-update"); err != nil {
				return err
			}
		}
		if err := s.writePointer(pointer); err != nil {
			return err
		}
		result = Snapshot{Pointer: pointer, Prepared: prepared, Active: &active}
		return nil
	})
	return result, err
}

// PrepareSuccessor is the explicit re-raise transaction. It durably publishes
// the fresh immutable preparation before it tombstones old authority. A crash
// after tombstoning leaves the successor identity in the monotonic pointer.
func (s *Store) PrepareSuccessor(expectedGenerationID string, spec operatorauth.ReleaseSpec) (Snapshot, error) {
	if err := s.validateSpecScope(spec); err != nil {
		return Snapshot{}, err
	}
	var result Snapshot
	err := s.withLock(func() error {
		pointer, err := s.readPointer()
		if err != nil {
			return err
		}
		if pointer.GenerationID != expectedGenerationID {
			// An identical retry after pointer advancement is idempotent.
			prepared, readErr := s.readPrepared(pointer.Generation)
			if readErr != nil {
				return fmt.Errorf("current release generation changed")
			}
			want, deriveErr := operatorauth.DerivePreparedRelease(spec, pointer.Generation)
			if deriveErr == nil && reflect.DeepEqual(prepared, want) {
				result, err = s.snapshotFrom(pointer, prepared)
				return err
			}
			return fmt.Errorf("current release generation changed")
		}
		if pointer.State == operatorauth.ReleaseStateSuperseded && pointer.SuccessorGenerationID != "" {
			return s.completeSuccessor(&result, pointer, spec)
		}
		currentPrepared, err := s.readPrepared(pointer.Generation)
		if err != nil {
			return err
		}
		if _, err := s.snapshotFrom(pointer, currentPrepared); err != nil {
			return err
		}
		nextGeneration := pointer.Generation + 1
		prepared, err := operatorauth.DerivePreparedRelease(spec, nextGeneration)
		if err != nil {
			return err
		}
		if err := s.writePrepared(prepared); err != nil {
			return err
		}
		if err := storeFault("after_successor_prepared"); err != nil {
			return err
		}
		// Tombstone through the authority-selecting pointer before any old-state
		// bookkeeping. Old active authority can no longer revive after this sync.
		pointer.Revision++
		pointer.State = operatorauth.ReleaseStateSuperseded
		pointer.SuccessorGenerationID = prepared.GenerationID
		if err := s.writePointer(pointer); err != nil {
			return err
		}
		if err := storeFault("after_old_terminalized"); err != nil {
			return err
		}
		return s.completeSuccessor(&result, pointer, spec)
	})
	return result, err
}

func (s *Store) completeSuccessor(result *Snapshot, tombstone Pointer, spec operatorauth.ReleaseSpec) error {
	nextGeneration := tombstone.Generation + 1
	prepared, err := s.readPrepared(nextGeneration)
	if err != nil {
		return err
	}
	want, err := operatorauth.DerivePreparedRelease(spec, nextGeneration)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(prepared, want) || tombstone.SuccessorGenerationID != prepared.GenerationID {
		return fmt.Errorf("prepared successor does not exactly match requested spec")
	}
	old, err := s.readGeneration(tombstone.Generation)
	if err != nil {
		return err
	}
	oldPrepared, err := s.readPrepared(tombstone.Generation)
	if err != nil {
		return err
	}
	var oldActive *operatorauth.ActiveReleaseManifest
	if old.ActiveManifestID != "" {
		value, readErr := s.readActive(old.Generation, oldPrepared)
		if readErr != nil {
			return readErr
		}
		oldActive = &value
	}
	if old.State == operatorauth.ReleaseStateSuperseded {
		if old.SuccessorGenerationID != prepared.GenerationID {
			return fmt.Errorf("superseded generation names a different successor")
		}
		if err := s.validateLifecycleSnapshot(tombstone, old, oldPrepared, oldActive); err != nil {
			return err
		}
	} else {
		if err := s.validatePointerAheadSupersession(tombstone, old, oldPrepared, oldActive); err != nil {
			return err
		}
		old.Revision++
		old.State = operatorauth.ReleaseStateSuperseded
		old.SuccessorGenerationID = prepared.GenerationID
		if err := s.writeGeneration(old); err != nil {
			return err
		}
		if err := s.validateLifecycleSnapshot(tombstone, old, oldPrepared, oldActive); err != nil {
			return fmt.Errorf("supersession repair: %w", err)
		}
	}
	if err := storeFault("before_successor_pointer"); err != nil {
		return err
	}
	record := newGenerationRecord(s.seriesID, prepared, tombstone.GenerationID)
	if existing, readErr := s.readGeneration(nextGeneration); readErr == nil {
		if err := s.validateLifecycleSnapshot(pointerFromRecord(record, 1), existing, prepared, nil); err != nil {
			return fmt.Errorf("successor generation record conflicts")
		}
		record = existing
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	} else if err := s.writeGeneration(record); err != nil {
		return err
	}
	pointer := pointerFromRecord(record, tombstone.Revision+1)
	if err := s.writePointer(pointer); err != nil {
		return err
	}
	*result = Snapshot{Pointer: pointer, Prepared: prepared}
	return nil
}

func (s *Store) ReadCurrent() (Snapshot, error) {
	pointer, err := s.readPointer()
	if err != nil {
		return Snapshot{}, err
	}
	if pointer.State == operatorauth.ReleaseStateSuperseded {
		return Snapshot{}, fmt.Errorf("current release pointer is terminalized awaiting successor %s", pointer.SuccessorGenerationID)
	}
	prepared, err := s.readPrepared(pointer.Generation)
	if err != nil {
		return Snapshot{}, err
	}
	return s.snapshotFrom(pointer, prepared)
}

func (s *Store) snapshotFrom(pointer Pointer, prepared operatorauth.PreparedReleaseManifest) (Snapshot, error) {
	record, err := s.readGeneration(pointer.Generation)
	if err != nil {
		return Snapshot{}, err
	}
	result := Snapshot{Pointer: pointer, Prepared: prepared}
	if pointer.State == operatorauth.ReleaseStateActive {
		active, err := s.readActive(pointer.Generation, prepared)
		if err != nil {
			return Snapshot{}, err
		}
		if pointer.ActiveManifestID != active.ActiveManifestID || pointer.ActiveSHA256 != operatorauth.ActiveReleaseSHA256(active) {
			return Snapshot{}, fmt.Errorf("active pointer does not select exact immutable manifest")
		}
		result.Active = &active
	}
	if err := s.validateLifecycleSnapshot(pointer, record, prepared, result.Active); err != nil {
		return Snapshot{}, err
	}
	return result, nil
}

func (s *Store) validateSpecScope(spec operatorauth.ReleaseSpec) error {
	if err := operatorauth.ValidateReleaseSpec(spec); err != nil {
		return err
	}
	wantNamespace := operatorauth.NamespaceBinding{ProjectDir: s.scope.ProjectDir, Profile: s.scope.Profile, Session: s.scope.Session, NamespaceID: squadnamespace.ID(s.scope.Profile, s.scope.Session), Generation: s.scope.NamespaceGeneration}
	if spec.Namespace != wantNamespace || spec.ParentGate != s.scope.ParentGate {
		return fmt.Errorf("release spec does not match store scope")
	}
	return nil
}

func newGenerationRecord(seriesID string, prepared operatorauth.PreparedReleaseManifest, predecessor string) generationRecord {
	children := make([]childPublicationRecord, 0, len(prepared.Children))
	for _, child := range prepared.Children {
		children = append(children, childPublicationRecord{Role: child.Role, Ordinal: child.Ordinal, AttemptID: child.Receipt.AttemptID, State: childPublicationPlanned})
	}
	return generationRecord{SchemaVersion: storeSchemaVersion, Revision: 1, SeriesID: seriesID, State: operatorauth.ReleaseStatePlanned, Generation: prepared.Generation, GenerationID: prepared.GenerationID, PreparedManifestID: prepared.PreparedManifestID, PreparedSHA256: operatorauth.PreparedReleaseSHA256(prepared), PredecessorGenerationID: predecessor, Children: children}
}

func pointerFromRecord(record generationRecord, revision uint64) Pointer {
	return Pointer{SchemaVersion: storeSchemaVersion, Revision: revision, SeriesID: record.SeriesID, State: record.State, Generation: record.Generation, GenerationID: record.GenerationID, PreparedManifestID: record.PreparedManifestID, PreparedSHA256: record.PreparedSHA256, ActiveManifestID: record.ActiveManifestID, ActiveSHA256: record.ActiveSHA256, PredecessorGenerationID: record.PredecessorGenerationID, SuccessorGenerationID: record.SuccessorGenerationID}
}

func adoptedChildRecords(existing []childPublicationRecord, prepared operatorauth.PreparedReleaseManifest, active operatorauth.ActiveReleaseManifest) []childPublicationRecord {
	children := make([]childPublicationRecord, 0, len(prepared.Children))
	for i, child := range prepared.Children {
		record := existing[i]
		record.Role, record.Ordinal, record.AttemptID = child.Role, child.Ordinal, child.Receipt.AttemptID
		record.State = childPublicationAdopted
		record.QuestionMessageID = active.Children[i].QuestionMessageID
		record.ReceiptPath = active.Children[i].Receipt.Path
		receipt := cloneDeliveryReceipt(active.Children[i].Receipt)
		record.Receipt = &receipt
		children = append(children, record)
	}
	return children
}

func validatePublishedChildrenForActivation(record generationRecord, prepared operatorauth.PreparedReleaseManifest, active operatorauth.ActiveReleaseManifest) error {
	if len(record.Children) != len(prepared.Children) || len(active.Children) != len(prepared.Children) {
		return fmt.Errorf("activation requires exactly two published children")
	}
	for i := range record.Children {
		stored, observed := record.Children[i], active.Children[i]
		receiptSHA, err := operatorauth.ReleaseDeliveryReceiptSHA256(observed.Receipt)
		if err != nil {
			return fmt.Errorf("activation child %d receipt: %w", i, err)
		}
		if stored.State != childPublicationPublished || stored.Receipt == nil || stored.QuestionMessageID != observed.QuestionMessageID || stored.ReceiptPath != observed.Receipt.Path || stored.AttemptID != observed.Receipt.AttemptID || stored.ReceiptSHA256 != receiptSHA || !deliveryReceiptTupleEqual(*stored.Receipt, observed.Receipt) {
			return fmt.Errorf("activation child %d is not the exact stable published evidence", i)
		}
	}
	return nil
}

func validateLifecycleExact(seriesID string, pointer Pointer, record generationRecord, prepared operatorauth.PreparedReleaseManifest, active *operatorauth.ActiveReleaseManifest) error {
	if pointer.SchemaVersion != storeSchemaVersion || pointer.Revision == 0 || pointer.SeriesID != seriesID || record.SchemaVersion != storeSchemaVersion || record.Revision == 0 || record.SeriesID != seriesID {
		return fmt.Errorf("release lifecycle schema or series identity mismatch")
	}
	preparedSHA := operatorauth.PreparedReleaseSHA256(prepared)
	if pointer.Generation != prepared.Generation || record.Generation != prepared.Generation || pointer.GenerationID != prepared.GenerationID || record.GenerationID != prepared.GenerationID || pointer.PreparedManifestID != prepared.PreparedManifestID || record.PreparedManifestID != prepared.PreparedManifestID || pointer.PreparedSHA256 != preparedSHA || record.PreparedSHA256 != preparedSHA {
		return fmt.Errorf("release lifecycle does not bind exact prepared manifest")
	}
	if pointer.State != record.State || pointer.ActiveManifestID != record.ActiveManifestID || pointer.ActiveSHA256 != record.ActiveSHA256 || pointer.PredecessorGenerationID != record.PredecessorGenerationID || pointer.SuccessorGenerationID != record.SuccessorGenerationID {
		return fmt.Errorf("release pointer and generation record diverge")
	}
	if prepared.Generation == 1 && pointer.PredecessorGenerationID != "" || prepared.Generation > 1 && pointer.PredecessorGenerationID == "" {
		return fmt.Errorf("release predecessor binding is invalid")
	}
	if err := validateChildPublications(pointer.State, record.Children, prepared, active); err != nil {
		return err
	}
	switch pointer.State {
	case operatorauth.ReleaseStatePlanned, operatorauth.ReleaseStatePublishing, operatorauth.ReleaseStateAborted, operatorauth.ReleaseStateConflict:
		if pointer.ActiveManifestID != "" || pointer.ActiveSHA256 != "" || pointer.SuccessorGenerationID != "" || active != nil {
			return fmt.Errorf("inactive release lifecycle carries active or successor identity")
		}
	case operatorauth.ReleaseStateActive:
		if pointer.ActiveManifestID == "" || pointer.ActiveSHA256 == "" || pointer.SuccessorGenerationID != "" || active == nil || pointer.ActiveManifestID != active.ActiveManifestID || pointer.ActiveSHA256 != operatorauth.ActiveReleaseSHA256(*active) {
			return fmt.Errorf("active release lifecycle omits or diverges from active manifest")
		}
	case operatorauth.ReleaseStateSuperseded:
		if pointer.SuccessorGenerationID == "" {
			return fmt.Errorf("superseded release omits successor")
		}
		if (pointer.ActiveManifestID == "") != (pointer.ActiveSHA256 == "") {
			return fmt.Errorf("superseded active identity is partial")
		}
		if pointer.ActiveManifestID == "" && active != nil || pointer.ActiveManifestID != "" && (active == nil || pointer.ActiveManifestID != active.ActiveManifestID || pointer.ActiveSHA256 != operatorauth.ActiveReleaseSHA256(*active)) {
			return fmt.Errorf("superseded release active provenance diverges")
		}
	default:
		return fmt.Errorf("unsupported release lifecycle state %q", pointer.State)
	}
	return nil
}

func validatePointerAheadSupersession(seriesID string, pointer Pointer, record generationRecord, prepared operatorauth.PreparedReleaseManifest, active *operatorauth.ActiveReleaseManifest) error {
	if pointer.State != operatorauth.ReleaseStateSuperseded || pointer.SuccessorGenerationID == "" || record.State == operatorauth.ReleaseStateSuperseded || record.SuccessorGenerationID != "" {
		return fmt.Errorf("not the single allowed pointer-ahead supersession")
	}
	synthetic := pointer
	synthetic.State = record.State
	synthetic.SuccessorGenerationID = ""
	if err := validateLifecycleExact(seriesID, synthetic, record, prepared, active); err != nil {
		return fmt.Errorf("pointer-ahead predecessor invalid: %w", err)
	}
	return nil
}

// validateLifecycleSnapshot adds the bounded on-disk predecessor proof to the
// pure current-generation validator. It intentionally examines exactly one
// prior generation: the current pointer and record cannot nominate a path or
// trigger attacker-controlled traversal.
func (s *Store) validateLifecycleSnapshot(pointer Pointer, record generationRecord, prepared operatorauth.PreparedReleaseManifest, active *operatorauth.ActiveReleaseManifest) error {
	if err := validateLifecycleExact(s.seriesID, pointer, record, prepared, active); err != nil {
		return err
	}
	return s.validateBoundedPredecessorProof(record, prepared)
}

func (s *Store) validatePointerAheadSupersession(pointer Pointer, record generationRecord, prepared operatorauth.PreparedReleaseManifest, active *operatorauth.ActiveReleaseManifest) error {
	if err := validatePointerAheadSupersession(s.seriesID, pointer, record, prepared, active); err != nil {
		return err
	}
	return s.validateBoundedPredecessorProof(record, prepared)
}

// validateBoundedPredecessorProof validates the current link and the link
// encoded inside the actual prior record. That fixed two-link proof catches a
// forged or cyclic prior relation without recursively walking attacker-sized
// generation histories.
func (s *Store) validateBoundedPredecessorProof(current generationRecord, prepared operatorauth.PreparedReleaseManifest) error {
	prior, priorPrepared, err := s.validateImmediatePredecessorLink(current, prepared)
	if err != nil || prior == nil || prior.Generation == 1 {
		return err
	}
	_, _, err = s.validateImmediatePredecessorLink(*prior, priorPrepared)
	if err != nil {
		return fmt.Errorf("immediate predecessor chain invalid: %w", err)
	}
	return nil
}

func (s *Store) validateImmediatePredecessorLink(current generationRecord, prepared operatorauth.PreparedReleaseManifest) (*generationRecord, operatorauth.PreparedReleaseManifest, error) {
	if prepared.Generation == 1 {
		if _, err := s.dir.Lstat(s.generationName(0)); err == nil {
			return nil, operatorauth.PreparedReleaseManifest{}, fmt.Errorf("first release generation has an unexpected prior record")
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, operatorauth.PreparedReleaseManifest{}, fmt.Errorf("inspect first release predecessor: %w", err)
		}
		return nil, operatorauth.PreparedReleaseManifest{}, nil
	}

	priorGeneration := prepared.Generation - 1
	priorPrepared, err := s.readPrepared(priorGeneration)
	if err != nil {
		return nil, operatorauth.PreparedReleaseManifest{}, fmt.Errorf("read immediate predecessor prepared manifest: %w", err)
	}
	prior, err := s.readGeneration(priorGeneration)
	if err != nil {
		return nil, operatorauth.PreparedReleaseManifest{}, fmt.Errorf("read immediate predecessor generation record: %w", err)
	}
	var priorActive *operatorauth.ActiveReleaseManifest
	if prior.ActiveManifestID != "" {
		active, readErr := s.readActive(priorGeneration, priorPrepared)
		if readErr != nil {
			return nil, operatorauth.PreparedReleaseManifest{}, fmt.Errorf("read immediate predecessor active manifest: %w", readErr)
		}
		priorActive = &active
	}
	priorPointer := pointerFromRecord(prior, prior.Revision)
	if err := validateLifecycleExact(s.seriesID, priorPointer, prior, priorPrepared, priorActive); err != nil {
		return nil, operatorauth.PreparedReleaseManifest{}, fmt.Errorf("immediate predecessor lifecycle invalid: %w", err)
	}
	if current.PredecessorGenerationID != prior.GenerationID || current.GenerationID == prior.GenerationID {
		return nil, operatorauth.PreparedReleaseManifest{}, fmt.Errorf("release predecessor does not bind the actual prior generation")
	}
	if prior.State != operatorauth.ReleaseStateSuperseded || prior.SuccessorGenerationID != current.GenerationID {
		return nil, operatorauth.PreparedReleaseManifest{}, fmt.Errorf("release predecessor does not reciprocally select current generation")
	}
	if prior.Generation == 1 {
		if _, err := s.dir.Lstat(s.generationName(0)); err == nil {
			return nil, operatorauth.PreparedReleaseManifest{}, fmt.Errorf("first release generation has an unexpected prior record")
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, operatorauth.PreparedReleaseManifest{}, fmt.Errorf("inspect first release predecessor: %w", err)
		}
	}
	return &prior, priorPrepared, nil
}

func validateChildPublications(topState string, records []childPublicationRecord, prepared operatorauth.PreparedReleaseManifest, active *operatorauth.ActiveReleaseManifest) error {
	if len(records) != 2 || len(prepared.Children) != 2 {
		return fmt.Errorf("release lifecycle requires exactly two child publication records")
	}
	if active != nil && len(active.Children) != len(records) {
		return fmt.Errorf("active release child count diverges from publication records")
	}
	conflicts := 0
	for i, record := range records {
		child := prepared.Children[i]
		activeReceiptSHA := ""
		if active != nil {
			var err error
			activeReceiptSHA, err = operatorauth.ReleaseDeliveryReceiptSHA256(active.Children[i].Receipt)
			if err != nil {
				return fmt.Errorf("release child %d active receipt: %w", i, err)
			}
		}
		if record.Role != child.Role || record.Ordinal != child.Ordinal || record.AttemptID != child.Receipt.AttemptID || record.AttemptID != child.ReleaseChild.AttemptID {
			return fmt.Errorf("release child publication identity mismatch at ordinal %d", i)
		}
		if err := validateChildClaim(record); err != nil {
			return fmt.Errorf("release child %d claim: %w", i, err)
		}
		if err := validateChildConflict(record); err != nil {
			return fmt.Errorf("release child %d conflict: %w", i, err)
		}
		switch record.State {
		case childPublicationPlanned, childPublicationSending:
			if record.QuestionMessageID != "" || record.ReceiptPath != "" || record.ReceiptSHA256 != "" || record.Receipt != nil {
				return fmt.Errorf("unpublished child carries message or receipt identity")
			}
		case childPublicationPublished, childPublicationAdopted:
			if err := validateStoredChildReceipt(record, child); err != nil {
				return fmt.Errorf("published child receipt identity: %w", err)
			}
		case childPublicationConflict:
			present := record.QuestionMessageID != "" || record.ReceiptPath != "" || record.ReceiptSHA256 != "" || record.Receipt != nil
			if present {
				if err := validateStoredChildReceipt(record, child); err != nil {
					return fmt.Errorf("conflict child prior publication identity: %w", err)
				}
			}
		default:
			return fmt.Errorf("unsupported child publication state %q", record.State)
		}
		if record.State == childPublicationConflict {
			conflicts++
		}
		switch topState {
		case operatorauth.ReleaseStatePlanned:
			if record.State != childPublicationPlanned {
				return fmt.Errorf("planned release has non-planned child")
			}
		case operatorauth.ReleaseStatePublishing:
			if record.State == childPublicationAdopted || record.State == childPublicationConflict {
				return fmt.Errorf("publishing release has adopted or conflict child")
			}
		case operatorauth.ReleaseStateActive:
			if record.State != childPublicationAdopted || active == nil || record.Receipt == nil || record.QuestionMessageID != active.Children[i].QuestionMessageID || record.ReceiptPath != active.Children[i].Receipt.Path || record.ReceiptSHA256 != activeReceiptSHA || !deliveryReceiptTupleEqual(*record.Receipt, active.Children[i].Receipt) {
				return fmt.Errorf("active release child does not match active manifest")
			}
		case operatorauth.ReleaseStateSuperseded:
			if active != nil {
				if record.State != childPublicationAdopted || record.Receipt == nil || record.QuestionMessageID != active.Children[i].QuestionMessageID || record.ReceiptPath != active.Children[i].Receipt.Path || record.ReceiptSHA256 != activeReceiptSHA || !deliveryReceiptTupleEqual(*record.Receipt, active.Children[i].Receipt) {
					return fmt.Errorf("superseded active child provenance diverges")
				}
			} else if record.State == childPublicationAdopted || record.State == childPublicationConflict {
				return fmt.Errorf("superseded inactive release has adopted or conflict child")
			}
		case operatorauth.ReleaseStateAborted:
			if record.State == childPublicationAdopted || record.State == childPublicationConflict {
				return fmt.Errorf("aborted release has adopted or conflict child")
			}
		case operatorauth.ReleaseStateConflict:
			// At least one child carries bounded durable conflict evidence.
			if record.State == childPublicationAdopted {
				return fmt.Errorf("conflict release has adopted child without active authority")
			}
		}
	}
	if topState == operatorauth.ReleaseStateConflict && conflicts == 0 {
		return fmt.Errorf("conflict release has no conflict child")
	}
	return nil
}

func validateStoredChildReceipt(record childPublicationRecord, child operatorauth.ReleaseChildPlan) error {
	if record.QuestionMessageID == "" || record.ReceiptPath == "" || !validReceiptSHA256(record.ReceiptSHA256) || record.Receipt == nil {
		return fmt.Errorf("stored receipt evidence is incomplete")
	}
	if err := validateChildReceipt(child, *record.Receipt); err != nil {
		return err
	}
	digest, err := operatorauth.ReleaseDeliveryReceiptSHA256(*record.Receipt)
	if err != nil {
		return err
	}
	if record.QuestionMessageID != record.Receipt.MessageID || record.ReceiptPath != record.Receipt.Path || record.AttemptID != record.Receipt.AttemptID || record.ReceiptSHA256 != digest {
		return fmt.Errorf("stored receipt tuple and digest diverge")
	}
	return nil
}

func cloneDeliveryReceipt(receipt operatorauth.ReleaseDeliveryReceiptTuple) operatorauth.ReleaseDeliveryReceiptTuple {
	receipt.Recipients = append([]string(nil), receipt.Recipients...)
	return receipt
}

func deliveryReceiptTupleEqual(a, b operatorauth.ReleaseDeliveryReceiptTuple) bool {
	return a.AttemptID == b.AttemptID && a.Kind == b.Kind && a.Sender == b.Sender && slices.Equal(a.Recipients, b.Recipients) && a.Thread == b.Thread && a.MessageID == b.MessageID && a.Path == b.Path && a.Root == b.Root && a.NamespaceID == b.NamespaceID && a.TargetIdentity == b.TargetIdentity && a.AdoptedGeneration == b.AdoptedGeneration
}

func validateChildClaim(record childPublicationRecord) error {
	switch record.State {
	case childPublicationPlanned:
		if record.ClaimToken != "" {
			return fmt.Errorf("planned child retains a live claim token")
		}
	case childPublicationSending, childPublicationPublished, childPublicationAdopted:
		if record.ClaimRevision == 0 || !validClaimToken(record.ClaimToken) {
			return fmt.Errorf("invoked-capable child omits its exact claim revision or token")
		}
	case childPublicationConflict:
		if record.ClaimToken != "" && (record.ClaimRevision == 0 || !validClaimToken(record.ClaimToken)) {
			return fmt.Errorf("conflict child has malformed claim identity")
		}
		// A planned child may retain a non-zero historical revision after a
		// definitely-uninvoked rollback. Terminal conflict preserves that
		// history without resurrecting the cleared live claim token.
	}
	return nil
}

func validClaimToken(value string) bool {
	const prefix = "release-claim-v1-"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+64 {
		return false
	}
	for _, r := range value[len(prefix):] {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func validReceiptSHA256(value string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+64 {
		return false
	}
	for _, r := range value[len(prefix):] {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func validateChildConflict(record childPublicationRecord) error {
	if record.State != childPublicationConflict {
		if record.ConflictReason != "" || len(record.ObservedMessageIDs) != 0 {
			return fmt.Errorf("non-conflict child carries conflict evidence")
		}
		return nil
	}
	if len(record.ConflictReason) > 512 {
		return fmt.Errorf("reason exceeds 512 bytes")
	}
	if err := operatorauth.ValidateCanonicalSingleLineField("conflict reason", record.ConflictReason, true); err != nil {
		return err
	}
	if len(record.ObservedMessageIDs) > 16 || !slices.IsSorted(record.ObservedMessageIDs) {
		return fmt.Errorf("observed message ids are not bounded and sorted")
	}
	for i, id := range record.ObservedMessageIDs {
		if err := operatorauth.ValidateCanonicalSingleLineField("observed message id", id, true); err != nil {
			return err
		}
		if i > 0 && record.ObservedMessageIDs[i-1] == id {
			return fmt.Errorf("observed message ids are not deduplicated")
		}
	}
	return nil
}

func (s *Store) preparedName(generation uint64) string {
	return fmt.Sprintf("prepared-g%020d.json", generation)
}
func (s *Store) activeName(generation uint64) string {
	return fmt.Sprintf("active-g%020d.json", generation)
}
func (s *Store) generationName(generation uint64) string {
	return fmt.Sprintf("generation-g%020d.json", generation)
}

func (s *Store) writePrepared(prepared operatorauth.PreparedReleaseManifest) error {
	if err := operatorauth.ValidatePreparedRelease(prepared); err != nil {
		return err
	}
	b, err := json.MarshalIndent(prepared, "", "  ")
	if err != nil {
		return err
	}
	return s.writeImmutable(s.preparedName(prepared.Generation), append(b, '\n'))
}

func (s *Store) writeActive(active operatorauth.ActiveReleaseManifest) error {
	b, err := json.MarshalIndent(active, "", "  ")
	if err != nil {
		return err
	}
	return s.writeImmutable(s.activeName(active.Generation), append(b, '\n'))
}

func (s *Store) writeGeneration(record generationRecord) error {
	b, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return s.writeMutable(s.generationName(record.Generation), append(b, '\n'))
}

func (s *Store) writePointer(pointer Pointer) error {
	b, err := json.MarshalIndent(pointer, "", "  ")
	if err != nil {
		return err
	}
	return s.writeMutable("current.json", append(b, '\n'))
}

func (s *Store) readPrepared(generation uint64) (operatorauth.PreparedReleaseManifest, error) {
	var prepared operatorauth.PreparedReleaseManifest
	if err := s.readStrict(s.preparedName(generation), &prepared); err != nil {
		return prepared, err
	}
	return prepared, operatorauth.ValidatePreparedRelease(prepared)
}

func (s *Store) readActive(generation uint64, prepared operatorauth.PreparedReleaseManifest) (operatorauth.ActiveReleaseManifest, error) {
	var active operatorauth.ActiveReleaseManifest
	if err := s.readStrict(s.activeName(generation), &active); err != nil {
		return active, err
	}
	return active, operatorauth.ValidateActiveRelease(prepared, active)
}

func (s *Store) readGeneration(generation uint64) (generationRecord, error) {
	var record generationRecord
	if err := s.readStrict(s.generationName(generation), &record); err != nil {
		return record, err
	}
	if record.SchemaVersion != storeSchemaVersion || record.Revision == 0 || record.SeriesID != s.seriesID || record.Generation != generation {
		return record, fmt.Errorf("release generation record identity mismatch")
	}
	return record, nil
}

func (s *Store) readPointer() (Pointer, error) {
	var pointer Pointer
	if err := s.readStrict("current.json", &pointer); err != nil {
		return pointer, err
	}
	if pointer.SchemaVersion != storeSchemaVersion || pointer.Revision == 0 || pointer.SeriesID != s.seriesID {
		return pointer, fmt.Errorf("release pointer identity mismatch")
	}
	return pointer, nil
}

func (s *Store) readStrict(name string, dst any) error {
	b, err := s.readLeaf(name)
	if err != nil {
		return err
	}
	if err := operatorauth.DecodeStrictJSON(b, dst); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func (s *Store) withLock(fn func() error) error {
	lock, err := s.openLeaf("store.lock", os.O_RDWR, 0, false)
	if err != nil {
		return err
	}
	defer lock.Close()
	return flock.WithFile(lock, filepath.Join(s.dirPath, "store.lock"), fn)
}

func (s *Store) readLeaf(name string) ([]byte, error) {
	f, err := s.openLeaf(name, os.O_RDONLY, 0, false)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

func (s *Store) openLeaf(name string, flags int, perm os.FileMode, create bool) (*os.File, error) {
	before, err := s.dir.Lstat(name)
	missing := errors.Is(err, os.ErrNotExist)
	if err != nil && !missing {
		return nil, err
	}
	if missing && !create {
		return nil, os.ErrNotExist
	}
	if !missing {
		if err := validateRegularSingleLink(before, filepath.Join(s.dirPath, name)); err != nil {
			return nil, err
		}
	}
	if err := storeContainmentHook("after_leaf_validation", filepath.Join(s.dirPath, name)); err != nil {
		return nil, err
	}
	f, err := s.dir.OpenFile(name, flags, perm)
	if err != nil {
		return nil, err
	}
	opened, openErr := f.Stat()
	after, afterErr := s.dir.Lstat(name)
	if openErr != nil || afterErr != nil || validateRegularSingleLink(opened, name) != nil || validateRegularSingleLink(after, name) != nil || !os.SameFile(opened, after) || (!missing && !os.SameFile(before, opened)) {
		f.Close()
		return nil, fmt.Errorf("contained release leaf identity changed during open: %s", filepath.Join(s.dirPath, name))
	}
	return f, nil
}

func (s *Store) writeImmutable(name string, data []byte) error {
	tempName := immutableTempName(name, data)
	temps, err := s.immutableTemps()
	if err != nil {
		return err
	}
	mutableTemps, err := s.mutableTemps()
	if err != nil {
		return err
	}
	targetInfo, targetErr := s.dir.Lstat(name)
	targetMissing := errors.Is(targetErr, os.ErrNotExist)
	if targetErr != nil && !targetMissing {
		return targetErr
	}
	if !targetMissing {
		nlink, ok := fileLinkCount(targetInfo)
		if !ok {
			return fmt.Errorf("immutable release target link count unavailable")
		}
		switch nlink {
		case 1:
			if len(temps) != 0 {
				return fmt.Errorf("immutable release replay has unjournaled temp artifacts")
			}
			current, opened, readErr := s.readLinkedLeaf(name)
			if readErr != nil || validateImmutableInfo(opened, name, int64(len(data)), 1) != nil || !bytes.Equal(current, data) {
				return fmt.Errorf("immutable release artifact %s already differs or is invalid", name)
			}
			return s.syncDirectory()
		case 2:
			if len(mutableTemps) != 0 {
				return fmt.Errorf("immutable release recovery is blocked by unresolved mutable temp artifacts")
			}
			if len(temps) != 1 || temps[0] != tempName {
				return fmt.Errorf("immutable release recovery is ambiguous")
			}
			tempInfo, statErr := s.dir.Lstat(tempName)
			if statErr != nil || !os.SameFile(targetInfo, tempInfo) || validateImmutableInfo(targetInfo, name, int64(len(data)), 2) != nil || validateImmutableInfo(tempInfo, tempName, int64(len(data)), 2) != nil {
				return fmt.Errorf("immutable release recovery identity mismatch")
			}
			current, _, readErr := s.readLinkedLeaf(name)
			if readErr != nil || !bytes.Equal(current, data) {
				return fmt.Errorf("immutable release recovery digest mismatch")
			}
			if err := s.dir.Remove(tempName); err != nil {
				return err
			}
			if err := s.syncDirectory(); err != nil {
				return err
			}
			published, statErr := s.dir.Lstat(name)
			if statErr != nil || !os.SameFile(targetInfo, published) || validateImmutableInfo(published, name, int64(len(data)), 1) != nil {
				return fmt.Errorf("immutable release recovery publication changed")
			}
			return nil
		default:
			return fmt.Errorf("immutable release target has unexpected link count %d", nlink)
		}
	}
	if len(mutableTemps) != 0 {
		return fmt.Errorf("immutable release publication is blocked by unresolved mutable temp artifacts")
	}
	if len(temps) > 1 || len(temps) == 1 && temps[0] != tempName {
		return fmt.Errorf("immutable release publication has unjournaled temp artifacts")
	}

	var temp *os.File
	if len(temps) == 1 {
		temp, err = s.dir.OpenFile(tempName, os.O_RDWR, 0)
		if err != nil {
			return err
		}
		content, opened, readErr := s.readLinkedLeaf(tempName)
		if readErr != nil || validateImmutableInfo(opened, tempName, int64(len(data)), 1) != nil || !bytes.Equal(content, data) {
			temp.Close()
			return fmt.Errorf("immutable release temp does not match exact derived publication")
		}
	} else {
		temp, err = s.dir.OpenFile(tempName, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if err != nil {
			return err
		}
		if _, err := temp.Write(data); err != nil {
			temp.Close()
			_ = s.dir.Remove(tempName)
			return err
		}
		if err := storeFileSync(temp); err != nil {
			temp.Close()
			_ = s.dir.Remove(tempName)
			return err
		}
	}
	preserve := false
	defer func() {
		_ = temp.Close()
		if !preserve {
			_ = s.dir.Remove(tempName)
		}
	}()
	opened, err := temp.Stat()
	if err != nil || validateImmutableInfo(opened, tempName, int64(len(data)), 1) != nil {
		return fmt.Errorf("immutable temp identity invalid")
	}
	if err := storeFault("after_file_sync"); err != nil {
		return err
	}
	current, err := s.dir.Lstat(tempName)
	if err != nil || !os.SameFile(opened, current) || validateImmutableInfo(current, tempName, int64(len(data)), 1) != nil {
		return fmt.Errorf("immutable temp identity changed before publication")
	}
	if err := storeContainmentHook("before_immutable_link", filepath.Join(s.dirPath, name)); err != nil {
		return err
	}
	if err := s.dir.Link(tempName, name); err != nil {
		return fmt.Errorf("publish immutable release artifact %s: %w", name, err)
	}
	preserve = true
	if err := s.syncDirectory(); err != nil {
		return err
	}
	if err := storeFault("after_immutable_link"); err != nil {
		return err
	}
	if err := s.dir.Remove(tempName); err != nil {
		return err
	}
	preserve = false
	if err := s.syncDirectory(); err != nil {
		return err
	}
	published, err := s.dir.Lstat(name)
	if err != nil || !os.SameFile(opened, published) || validateImmutableInfo(published, name, int64(len(data)), 1) != nil {
		return fmt.Errorf("immutable publication identity changed")
	}
	if err := storeFault("after_immutable_publish"); err != nil {
		return err
	}
	return nil
}

func immutableTempName(name string, data []byte) string {
	digest := sha256.Sum256(append(append([]byte(name), 0), data...))
	return ".release-immutable-" + hex.EncodeToString(digest[:]) + ".tmp"
}

func (s *Store) immutableTemps() ([]string, error) {
	return s.classifiedTemps(".release-immutable-")
}

func (s *Store) mutableTemps() ([]string, error) {
	return s.classifiedTemps(".release-mutable-")
}

func (s *Store) classifiedTemps(prefix string) ([]string, error) {
	dir, err := s.dir.Open(".")
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix) && strings.HasSuffix(entry.Name(), ".tmp") {
			out = append(out, entry.Name())
		}
	}
	slices.Sort(out)
	return out, nil
}

func (s *Store) readLinkedLeaf(name string) ([]byte, os.FileInfo, error) {
	f, err := s.dir.Open(name)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	visible, err := s.dir.Lstat(name)
	if err != nil || visible.Mode()&os.ModeSymlink != 0 || !visible.Mode().IsRegular() || !os.SameFile(opened, visible) {
		return nil, nil, fmt.Errorf("immutable release leaf identity changed")
	}
	b, err := io.ReadAll(f)
	return b, opened, err
}

func validateImmutableInfo(info os.FileInfo, label string, size int64, links uint64) error {
	if info == nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() != size {
		return fmt.Errorf("immutable release artifact %s mode, type, or size mismatch", label)
	}
	nlink, ok := fileLinkCount(info)
	if !ok || nlink != links {
		return fmt.Errorf("immutable release artifact %s link count mismatch", label)
	}
	if uid, gid, ok := fileOwner(info); ok && (uid != uint64(os.Geteuid()) || gid != uint64(os.Getegid())) {
		return fmt.Errorf("immutable release artifact %s owner mismatch", label)
	}
	return nil
}

func (s *Store) writeMutable(name string, data []byte) error {
	immutableTemps, err := s.immutableTemps()
	if err != nil {
		return err
	}
	if len(immutableTemps) != 0 {
		return fmt.Errorf("mutable release publication is blocked by unresolved immutable temp artifacts")
	}
	tempName := mutableTempName(name, data)
	temps, err := s.mutableTemps()
	if err != nil {
		return err
	}
	if len(temps) > 1 || len(temps) == 1 && temps[0] != tempName {
		return fmt.Errorf("mutable release publication has unexpected temp artifacts")
	}
	if len(temps) == 0 {
		if current, info, readErr := s.readLinkedLeaf(name); readErr == nil && validateImmutableInfo(info, name, int64(len(data)), 1) == nil && bytes.Equal(current, data) {
			return s.syncDirectory()
		} else if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			// A non-regular, linked, or swapped target remains a conflict.
			if _, statErr := s.dir.Lstat(name); statErr == nil {
				return readErr
			}
		}
	}
	var temp *os.File
	created := len(temps) == 0
	if created {
		temp, err = s.dir.OpenFile(tempName, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if err != nil {
			return err
		}
		if _, err := temp.Write(data); err != nil {
			temp.Close()
			_ = s.dir.Remove(tempName)
			return err
		}
		if err := storeFileSync(temp); err != nil {
			temp.Close()
			_ = s.dir.Remove(tempName)
			return err
		}
	} else {
		temp, err = s.dir.OpenFile(tempName, os.O_RDWR, 0)
		if err != nil {
			return err
		}
		content, info, readErr := s.readLinkedLeaf(tempName)
		if readErr != nil || validateImmutableInfo(info, tempName, int64(len(data)), 1) != nil || !bytes.Equal(content, data) {
			temp.Close()
			return fmt.Errorf("mutable release retry temp does not match exact derived publication")
		}
	}
	preserve := true
	defer func() {
		_ = temp.Close()
		if !preserve {
			_ = s.dir.Remove(tempName)
		}
	}()
	opened, err := temp.Stat()
	if err != nil || validateImmutableInfo(opened, tempName, int64(len(data)), 1) != nil {
		return fmt.Errorf("mutable release temp identity invalid")
	}
	if err := storeFault("after_mutable_file_sync:" + name); err != nil {
		return err
	}
	current, err := s.dir.Lstat(tempName)
	if err != nil || !os.SameFile(opened, current) || validateImmutableInfo(current, tempName, int64(len(data)), 1) != nil {
		return fmt.Errorf("mutable temp identity changed before rename")
	}
	targetBefore, targetErr := s.dir.Lstat(name)
	missing := errors.Is(targetErr, os.ErrNotExist)
	if targetErr != nil && !missing {
		return targetErr
	}
	if !missing {
		if err := validateRegularSingleLink(targetBefore, name); err != nil {
			return err
		}
	}
	if err := storeContainmentHook("before_mutable_rename", filepath.Join(s.dirPath, name)); err != nil {
		return err
	}
	targetAfter, targetErr := s.dir.Lstat(name)
	if missing {
		if !errors.Is(targetErr, os.ErrNotExist) {
			return fmt.Errorf("mutable target appeared before rename")
		}
	} else if targetErr != nil || !os.SameFile(targetBefore, targetAfter) || validateRegularSingleLink(targetAfter, name) != nil {
		return fmt.Errorf("mutable target identity changed before rename")
	}
	if err := s.dir.Rename(tempName, name); err != nil {
		return err
	}
	preserve = false
	published, err := s.dir.Lstat(name)
	if err != nil || !os.SameFile(opened, published) || validateImmutableInfo(published, name, int64(len(data)), 1) != nil {
		return fmt.Errorf("mutable publication identity changed")
	}
	if err := storeFault("after_mutable_rename:" + name); err != nil {
		return err
	}
	return s.syncDirectory()
}

func mutableTempName(name string, data []byte) string {
	digest := sha256.Sum256(append(append([]byte(name), 0), data...))
	return ".release-mutable-" + hex.EncodeToString(digest[:]) + ".tmp"
}

func (s *Store) syncDirectory() error {
	f, err := s.dir.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	return storeDirectorySync(f)
}

func openContainedRoot(projectDir, rel string, create bool) (*os.Root, string, error) {
	projectAbs, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, "", err
	}
	before, err := os.Lstat(projectAbs)
	if err != nil {
		return nil, "", err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
		return nil, "", fmt.Errorf("release project root must be a non-symlink directory")
	}
	if err := storeContainmentHook("after_project_validation", projectAbs); err != nil {
		return nil, "", err
	}
	root, err := os.OpenRoot(projectAbs)
	if err != nil {
		return nil, "", err
	}
	opened, err := statRoot(root)
	after, afterErr := os.Lstat(projectAbs)
	if err != nil || afterErr != nil || !os.SameFile(before, opened) || !os.SameFile(after, opened) || after.Mode()&os.ModeSymlink != 0 {
		root.Close()
		return nil, "", fmt.Errorf("release project root identity changed during open")
	}
	currentPath := projectAbs
	for _, component := range strings.Split(filepath.Clean(rel), string(os.PathSeparator)) {
		if component == "" || component == "." || component == ".." {
			root.Close()
			return nil, "", fmt.Errorf("unsafe release store path component")
		}
		componentPath := filepath.Join(currentPath, component)
		info, statErr := root.Lstat(component)
		if errors.Is(statErr, os.ErrNotExist) && !create {
			root.Close()
			return nil, "", os.ErrNotExist
		}
		if errors.Is(statErr, os.ErrNotExist) && create {
			if mkdirErr := root.Mkdir(component, 0o700); mkdirErr != nil && !errors.Is(mkdirErr, os.ErrExist) {
				root.Close()
				return nil, "", mkdirErr
			}
			if syncErr := syncRootDirectory(root); syncErr != nil {
				root.Close()
				return nil, "", syncErr
			}
			info, statErr = root.Lstat(component)
		}
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			root.Close()
			return nil, "", fmt.Errorf("release store ancestor must be a non-symlink directory: %s", componentPath)
		}
		if err := storeContainmentHook("after_component_validation", componentPath); err != nil {
			root.Close()
			return nil, "", err
		}
		child, openErr := root.OpenRoot(component)
		if openErr != nil {
			root.Close()
			return nil, "", openErr
		}
		childInfo, childErr := statRoot(child)
		visible, visibleErr := root.Lstat(component)
		if childErr != nil || visibleErr != nil || visible.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, childInfo) || !os.SameFile(visible, childInfo) {
			child.Close()
			root.Close()
			return nil, "", fmt.Errorf("release store ancestor identity changed during open: %s", componentPath)
		}
		root.Close()
		root = child
		currentPath = componentPath
	}
	return root, currentPath, nil
}

func statRoot(root *os.Root) (os.FileInfo, error) {
	f, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Stat()
}

func syncRootDirectory(root *os.Root) error {
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	return storeDirectorySync(f)
}

func syncDirectoryFile(f *os.File) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	if err := f.Sync(); err != nil && !errors.Is(err, syscall.EINVAL) && !errors.Is(err, syscall.ENOTSUP) {
		return err
	}
	return nil
}

func validateRegularSingleLink(info os.FileInfo, label string) error {
	if info == nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("release artifact %s must be a non-symlink regular file", label)
	}
	nlink, ok := fileLinkCount(info)
	if !ok || nlink != 1 {
		return fmt.Errorf("release artifact %s link count must be one", label)
	}
	return nil
}

func fileLinkCount(info os.FileInfo) (uint64, bool) {
	if info == nil || info.Sys() == nil {
		return 0, false
	}
	v := reflect.ValueOf(info.Sys())
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return 0, false
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return 0, false
	}
	f := v.FieldByName("Nlink")
	if !f.IsValid() {
		return 0, false
	}
	switch f.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return f.Uint(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if f.Int() < 0 {
			return 0, false
		}
		return uint64(f.Int()), true
	default:
		return 0, false
	}
}

func fileOwner(info os.FileInfo) (uint64, uint64, bool) {
	if info == nil || info.Sys() == nil {
		return 0, 0, false
	}
	v := reflect.ValueOf(info.Sys())
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return 0, 0, false
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return 0, 0, false
	}
	read := func(name string) (uint64, bool) {
		f := v.FieldByName(name)
		if !f.IsValid() {
			return 0, false
		}
		switch f.Kind() {
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return f.Uint(), true
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			if f.Int() < 0 {
				return 0, false
			}
			return uint64(f.Int()), true
		default:
			return 0, false
		}
	}
	uid, uidOK := read("Uid")
	gid, gidOK := read("Gid")
	return uid, gid, uidOK && gidOK
}

func seriesIdentity(scope Scope) string {
	payload := strings.Join([]string{scope.ProjectDir, scope.Profile, scope.Session, scope.NamespaceGeneration, scope.ParentGate}, "\x00")
	digest := sha256.Sum256([]byte(payload))
	return "release-series-v1-" + hex.EncodeToString(digest[:])
}
