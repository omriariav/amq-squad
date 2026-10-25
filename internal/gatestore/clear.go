package gatestore

import (
	"errors"
	"fmt"
	"os"
	"time"
)

// The Buzz-signed seat clear (phase 5, plan 4.3): a clear request is a typed
// gate of action quarantine_clear. Beside the gate, the store keeps the
// seat's one current request and the single-use marks of gates a clear has
// consumed:
//
//	clears/<handle>.json        the current request (replaced by a new quarantine's)
//	clears/used/<gate id>.json  the clear that consumed the gate, created once

// ClearRequestSchema names the request file format.
const ClearRequestSchema = "amq-squad-clear-request/1"

// ClearRequest is a seat's current clear request.
type ClearRequest struct {
	Schema       string `json:"schema"`
	Handle       string `json:"handle"`
	Nonce        string `json:"nonce"`
	GateID       string `json:"gate_id"`
	Incarnation  string `json:"incarnation"`
	ConfigDigest string `json:"config_digest"`
	At           string `json:"at"`
}

// Valid checks the request's shape.
func (r ClearRequest) Valid() error {
	if r.Schema != ClearRequestSchema || !handleRe.MatchString(r.Handle) || ValidGateID(r.GateID) != nil || r.Nonce == "" || r.Incarnation == "" || r.ConfigDigest == "" {
		return errors.New("clear request is malformed")
	}
	if _, err := time.Parse(time.RFC3339, r.At); err != nil {
		return errors.New("clear request time is invalid")
	}
	return nil
}

func (s Store) clearPath(handle string) (string, error) {
	if !handleRe.MatchString(handle) {
		return "", fmt.Errorf("%q is not a handle", handle)
	}
	return s.Dir("clears", handle+".json"), nil
}

// ReadClearRequest is the seat's current request, nil when none.
func (s Store) ReadClearRequest(handle string) (*ClearRequest, error) {
	p, err := s.clearPath(handle)
	if err != nil {
		return nil, err
	}
	var r ClearRequest
	if err := readStrict(p, &r); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("clear request %s: %w", handle, err)
	}
	if err := r.Valid(); err != nil || r.Handle != handle {
		return nil, fmt.Errorf("clear request %s is malformed", handle)
	}
	return &r, nil
}

// PutClearRequest makes r the seat's current request (replacing an older
// one; the older gate stays in the store and can no longer clear).
func (s Store) PutClearRequest(r ClearRequest) error {
	r.Schema = ClearRequestSchema
	if err := r.Valid(); err != nil {
		return err
	}
	p, err := s.clearPath(r.Handle)
	if err != nil {
		return err
	}
	return writeAtomic(p, mustJSON(r))
}

// ClearUse is the mark of a gate a clear consumed.
type ClearUse struct {
	GateID  string `json:"gate_id"`
	Handle  string `json:"handle"`
	EventID string `json:"event_id"`
	At      string `json:"at"`
}

func (s Store) usedPath(gateID string) (string, error) {
	if err := ValidGateID(gateID); err != nil {
		return "", err
	}
	return s.Dir("clears", "used", gateID+".json"), nil
}

// ClearUsed reads the mark of a gate, nil when no clear used it.
func (s Store) ClearUsed(gateID string) (*ClearUse, error) {
	p, err := s.usedPath(gateID)
	if err != nil {
		return nil, err
	}
	var u ClearUse
	if err := readStrict(p, &u); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("clear mark %s: %w", gateID, err)
	}
	return &u, nil
}

// MarkClearUsed records that a clear consumed the gate; a second mark for the
// same gate fails (os.ErrExist): an answer lifts one quarantine, once.
func (s Store) MarkClearUsed(u ClearUse) error {
	p, err := s.usedPath(u.GateID)
	if err != nil {
		return err
	}
	if !handleRe.MatchString(u.Handle) || !hex64.MatchString(u.EventID) {
		return errors.New("clear mark names no handle or event")
	}
	return writeOnce(p, mustJSON(u))
}
