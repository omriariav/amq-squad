package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Seat quarantine (2b-2 milestone B, reviewer condition on amendment 28): a
// seat whose run changed the owner's global agent config gets no new lease
// until the owner clears it. One file per seat under <store>/quarantine/,
// written and read under the store lock, so a grant never races a
// quarantine.

// ReasonSeatQuarantined is the refusal of a lease for a quarantined seat.
const ReasonSeatQuarantined = "seat_quarantined"

// ErrQuarantined is returned by Grant and Claim for a quarantined seat.
var ErrQuarantined = errors.New(ReasonSeatQuarantined)

// Quarantine is one quarantined seat.
type Quarantine struct {
	Handle string    `json:"handle"`
	Reason string    `json:"reason"`
	Detail string    `json:"detail,omitempty"`
	TaskID string    `json:"task_id,omitempty"`
	At     time.Time `json:"at"`
}

func quarantineDir(dir string) string { return filepath.Join(dir, "quarantine") }

func readQuarantine(dir, handle string) (*Quarantine, error) {
	raw, err := os.ReadFile(filepath.Join(quarantineDir(dir), handle+".json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var q Quarantine
	if err := json.Unmarshal(raw, &q); err != nil {
		// An unreadable quarantine still quarantines (fail closed).
		return &Quarantine{Handle: handle, Reason: ReasonSeatQuarantined, Detail: "unreadable quarantine record"}, nil
	}
	return &q, nil
}

// quarantined refuses a lease for a quarantined holder.
func quarantined(dir, holder string) error {
	q, err := readQuarantine(dir, holder)
	if err != nil {
		return err
	}
	if q != nil {
		return fmt.Errorf("%w: seat %s is quarantined (%s): %s", ErrQuarantined, holder, q.Reason, q.Detail)
	}
	return nil
}

// Quarantine records a seat quarantine; the seat gets no new lease until
// ClearQuarantine.
func (s Store) Quarantine(q Quarantine) error {
	if !validHandle(q.Handle) || strings.TrimSpace(q.Reason) == "" {
		return fmt.Errorf("quarantine needs a seat handle and a reason")
	}
	return s.withLock(true, func() error {
		if err := os.MkdirAll(quarantineDir(s.Dir), 0o700); err != nil {
			return err
		}
		raw, _ := json.MarshalIndent(q, "", "  ")
		tmp := filepath.Join(quarantineDir(s.Dir), "."+q.Handle+".tmp")
		if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
			return err
		}
		return os.Rename(tmp, filepath.Join(quarantineDir(s.Dir), q.Handle+".json"))
	})
}

// ClearQuarantine lifts a seat quarantine (the owner's decision). It
// returns the record it removed, nil when the seat was not quarantined.
func (s Store) ClearQuarantine(handle string) (*Quarantine, error) {
	if !validHandle(handle) {
		return nil, fmt.Errorf("invalid seat handle %q", handle)
	}
	var out *Quarantine
	err := s.withLock(true, func() error {
		q, err := readQuarantine(s.Dir, handle)
		if err != nil || q == nil {
			return err
		}
		out = q
		return os.Remove(filepath.Join(quarantineDir(s.Dir), handle+".json"))
	})
	return out, err
}

// Quarantined lists the quarantined seats (lock-free read).
func Quarantined(dir string) ([]Quarantine, error) {
	entries, err := os.ReadDir(quarantineDir(dir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Quarantine
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || strings.HasPrefix(name, ".") {
			continue
		}
		if q, err := readQuarantine(dir, name); err == nil && q != nil {
			out = append(out, *q)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Handle < out[j].Handle })
	return out, nil
}

func validHandle(h string) bool {
	if h == "" || len(h) > 64 || strings.ContainsAny(h, "/\\.") {
		return false
	}
	for _, r := range h {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
