package compoundrelease

// Test data for the superseded and multi-generation states a v2 release left
// on disk. The successor writer (PrepareSuccessor) is gone from 3.0 (3-2
// ruling: a changed release is a new tag, a new parent gate and a new
// series); the store still reads and validates these states, so the reader
// tests write them directly, file by file, under the store lock.

import "github.com/omriariav/amq-squad/v3/internal/operatorauth"

// writePointerAheadSuccessor writes the state a successor left when it
// stopped after tombstoning the pointer: the successor's prepared manifest
// and the superseded pointer naming it; the old generation record is not yet
// superseded.
func writePointerAheadSuccessor(s *Store, spec operatorauth.ReleaseSpec) (Pointer, operatorauth.PreparedReleaseManifest, error) {
	var pointer Pointer
	var prepared operatorauth.PreparedReleaseManifest
	err := s.withLock(func() error {
		var err error
		if pointer, err = s.readPointer(); err != nil {
			return err
		}
		if prepared, err = operatorauth.DerivePreparedRelease(spec, pointer.Generation+1); err != nil {
			return err
		}
		if err := s.writePrepared(prepared); err != nil {
			return err
		}
		pointer.Revision++
		pointer.State, pointer.SuccessorGenerationID = operatorauth.ReleaseStateSuperseded, prepared.GenerationID
		return s.writePointer(pointer)
	})
	return pointer, prepared, err
}

// writeSuccessorState writes a completed successor generation as test data:
// the old record superseded, the successor's planned generation record and
// the pointer selecting it.
func writeSuccessorState(s *Store, expectedGenerationID string, spec operatorauth.ReleaseSpec) (Snapshot, error) {
	if current, err := s.readPointer(); err != nil || current.GenerationID != expectedGenerationID {
		return Snapshot{}, errNotCurrent(err)
	}
	tombstone, prepared, err := writePointerAheadSuccessor(s, spec)
	if err != nil {
		return Snapshot{}, err
	}
	var snap Snapshot
	err = s.withLock(func() error {
		old, err := s.readGeneration(tombstone.Generation)
		if err != nil {
			return err
		}
		old.Revision++
		old.State, old.SuccessorGenerationID = operatorauth.ReleaseStateSuperseded, prepared.GenerationID
		if err := s.writeGeneration(old); err != nil {
			return err
		}
		record := newGenerationRecord(s.seriesID, prepared, tombstone.GenerationID)
		if err := s.writeGeneration(record); err != nil {
			return err
		}
		pointer := pointerFromRecord(record, tombstone.Revision+1)
		if err := s.writePointer(pointer); err != nil {
			return err
		}
		snap = Snapshot{Pointer: pointer, Prepared: prepared}
		return nil
	})
	return snap, err
}

type notCurrentError struct{ cause error }

func (e notCurrentError) Error() string { return "current release generation changed" }

func errNotCurrent(cause error) error { return notCurrentError{cause} }
