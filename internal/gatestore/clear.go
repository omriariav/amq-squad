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
//
// Whether an owner answer was used is not recorded here: the sealed
// ClearRecord on the seat's commits ledger carries the answer's event id and
// is the only use mark (seatcommit.ClearEventUsed).

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
