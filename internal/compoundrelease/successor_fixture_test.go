package compoundrelease

// The successor writer (PrepareSuccessor, completeSuccessor) left the
// production store in 3-2: in 3.0 a changed release spec is a new tag, a new
// parent gate and so a new series, so nothing re-raises a generation. The
// store still reads and validates the superseded and multi-generation states
// a v2 release left on disk (validateLifecycleExact, the predecessor chain,
// series inspection); these tests build those states with the writer below.

import (
	"errors"
	"fmt"
	"os"
	"reflect"

	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
)

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
