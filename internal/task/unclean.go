package task

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/atomicfile"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
)

// The unclean marker (v3-5-verifier, the fourth round). CleanSeat's git work
// runs outside the store lock (no lock reachable from the tick is held across
// a subprocess), so the seat is marked unclean, under the lock, before it:
// <store>/reserved/<seat>, one JSON record with a nonce. While the marker
// exists, whatever its age, Grant and Claim refuse the seat (seat_unclean):
// only a completed CleanSeat removes it, and only its own (the nonce). The
// A proved-live marker is never taken over by age. A proved-dead marker is
// taken immediately. The TTL applies only when the marker carries no readable
// process identity. A marker that is malformed or cannot be read is
// seat_unclean too, never a free seat, and status names it to the lead
// (SeatMarkerProblems).
//
// A marker whose time is more than one TTL in the future is invalid like a
// malformed one (status names it, a CleanSeat takes it over): it would
// otherwise hold the seat forever.
//
// Every new lease goes through seatLeasable (grant: Grant, and Claim of a
// pending task). The existing-lease paths (a claim of an already granted
// lease, a widening, a re-armed commit) do not consult the marker: a marker
// beside a live lease is a planted one, the same-UID limit.
//
// Limit, recorded with the others: a same-UID process can plant a marker and
// keep a seat from every lease until the lead removes it or a CleanSeat runs.

// SeatReservationTTL is the takeover wait only for a marker whose cleaner
// identity cannot be read.
const SeatReservationTTL = 10 * time.Minute

// Reason codes of the marker.
var (
	ErrSeatUnclean  = errors.New("seat_unclean")  // a lease refused: the seat's worktree is not known clean
	ErrSeatCleaning = errors.New("seat_cleaning") // a CleanSeat refused: another cleaner holds a fresh marker
	ErrReleaseLost  = errors.New("release_lost")  // a cleaner's release found another cleaner's marker
)

type seatMarker struct {
	Nonce string    `json:"nonce"`
	At    time.Time `json:"at"`
	PID   int       `json:"pid,omitempty"`
	Start string    `json:"start,omitempty"`
	// Cancel names the cancelled attempt (task#n) a scheduler reserved the
	// seat for, its close and its clean (ReserveCancel, the plan
	// reviewer's ruling on #909); empty for a CleanSeat's own marker.
	Cancel string `json:"cancel,omitempty"`
}

// ReservedSeatError refuses a lease for a seat reserved for a cancelled
// attempt's close and clean, in the plan reviewer's words (#909). It is
// seat_unclean underneath.
type ReservedSeatError struct{ Seat string }

func (e *ReservedSeatError) Error() string {
	return fmt.Sprintf("seat %s is being closed and cleaned; dispatch again after it finishes", e.Seat)
}

func (e *ReservedSeatError) Unwrap() error { return ErrSeatUnclean }

var (
	cleanerPID        = os.Getpid
	cleanerStartToken = procinfo.StartToken
	cleanerAlive      = procinfo.Alive
)

func reservedDir(dir string) string { return filepath.Join(dir, "reserved") }

// SeatHandle is the one normalizer of a seat handle for the marker and the
// lease (the fifth round's (f)): trimmed, non-empty, one path component.
func SeatHandle(h string) (string, error) {
	h = strings.TrimSpace(h)
	if h == "" || h == "." || h == ".." || strings.ContainsAny(h, "/\\\x00") {
		return "", fmt.Errorf("seat handle %q is not one plain name", h)
	}
	return h, nil
}

// seatLeasable is the one check a new lease of a seat passes: not unclean.
func seatLeasable(dir, holder string) error {
	return unclean(dir, holder)
}

// futureMarker reports a marker time more than one TTL ahead of now.
func futureMarker(m *seatMarker, now time.Time) bool {
	return m != nil && m.At.After(now.Add(SeatReservationTTL))
}

func markerPath(dir, handle string) string { return filepath.Join(reservedDir(dir), handle) }

// readMarker reads a seat's marker: (nil, nil) when there is none; a
// malformed one is an error naming the file.
func readMarker(dir, handle string) (*seatMarker, error) {
	raw, err := os.ReadFile(markerPath(dir, handle))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("the unclean marker %s cannot be read: %v", markerPath(dir, handle), err)
	}
	var m seatMarker
	if json.Unmarshal(raw, &m) != nil || m.Nonce == "" || m.At.IsZero() {
		return nil, fmt.Errorf("the unclean marker %s is malformed", markerPath(dir, handle))
	}
	return &m, nil
}

// unclean refuses a lease for a seat with any marker, whatever its age; a
// marker that cannot be read or is malformed refuses too (fail closed).
func unclean(dir, holder string) error {
	m, err := readMarker(dir, holder)
	switch {
	case err != nil:
		return fmt.Errorf("%w: seat %s: %v", ErrSeatUnclean, holder, err)
	case m != nil && m.Cancel != "":
		return &ReservedSeatError{Seat: holder}
	case m != nil:
		return fmt.Errorf("%w: seat %s's worktree is not known clean since %s (a CleanSeat runs, or did not finish)", ErrSeatUnclean, holder, m.At.UTC().Format(time.RFC3339))
	}
	return nil
}

// ReserveSeat is CleanSeat's first take of the store lock: the state read
// (the seat holds nothing) and the unclean marker,
// with a fresh nonce, which the caller's ReleaseSeat must present. A live
// cleaner refuses regardless of age; a dead cleaner is taken over at once;
// the TTL applies only to an unreadable identity. A marker file that cannot
// be read refuses.
func (s Store) ReserveSeat(handle string, now time.Time) ([]Task, string, error) {
	list, nonce, _, err := s.ReserveSeatWithDecision(handle, now)
	return list, nonce, err
}

// ReserveSeatWithDecision is ReserveSeat with the one decision a caller
// records when it takes over a marker whose proved cleaner is dead.
func (s Store) ReserveSeatWithDecision(handle string, now time.Time) ([]Task, string, string, error) {
	var out []Task
	var nonce, decision string
	handle, err := SeatHandle(handle)
	if err != nil {
		return nil, "", "", err
	}
	err = s.withLock(true, func() error {
		list, err := readDir(s.Dir)
		if err != nil {
			return err
		}
		if held := HeldBy(handle, list); held != nil {
			return fmt.Errorf("seat %s holds task %s; its worktree is cleaned only between leases", handle, held.ID)
		}
		nonce, decision, err = takeMarker(s.Dir, handle, now, "")
		out = list
		return err
	})
	return out, nonce, decision, err
}

// ReserveCancel is the scheduler's reservation of a seat for one round of
// a cancelled attempt's close or clean (the plan reviewer's ruling on
// #909: never the store's lock held through a launcher close). Under the
// store's lock, the lock every grant takes, it reads the leases: a seat
// with a later lease than the attempt (LaterLease) is returned as later,
// and nothing is reserved. Otherwise it takes the seat's 2b-1 marker, with
// this process's pid and start stamp and the attempt named (Cancel): from
// then on every grant of the seat refuses (ReservedSeatError) until the
// caller's ReleaseSeat, and a crashed scheduler's marker is taken over by
// the next one, as any dead cleaner's is. decision is the takeover line.
func (s Store) ReserveCancel(handle, taskID string, attempt int, now time.Time) (later, nonce, decision string, err error) {
	if handle, err = SeatHandle(handle); err != nil {
		return "", "", "", err
	}
	err = s.withLock(true, func() error {
		list, err := readDir(s.Dir)
		if err != nil {
			return err
		}
		var a *Attempt
		for _, t := range list {
			for i := range t.Attempts {
				if t.ID == taskID && t.Attempts[i].N == attempt && t.Attempts[i].Holder == handle {
					a = &t.Attempts[i]
				}
			}
		}
		if a == nil {
			return fmt.Errorf("seat %s has no attempt %d of %s", handle, attempt, taskID)
		}
		if later = LaterLease(list, handle, taskID, *a); later != "" {
			return nil
		}
		if held := HeldBy(handle, list); held != nil {
			return fmt.Errorf("seat %s holds task %s; its cancelled attempt is handled only between leases", handle, held.ID)
		}
		nonce, decision, err = takeMarker(s.Dir, handle, now, fmt.Sprintf("%s#%d", taskID, attempt))
		return err
	})
	return later, nonce, decision, err
}

// takeMarker takes the seat's marker under the store's lock (the caller
// holds it): a live cleaner refuses regardless of age, a dead one is taken
// over at once (decision), and the TTL applies only to an unreadable
// identity; a marker file that cannot be read refuses.
func takeMarker(dir, handle string, now time.Time, cancel string) (string, string, error) {
	var decision string
	nonce := newNonce()
	m, merr := readMarker(dir, handle)
	if merr != nil {
		if _, rerr := os.ReadFile(markerPath(dir, handle)); rerr != nil {
			return "", "", fmt.Errorf("%w: %v", ErrSeatUnclean, merr) // unreadable: never overwritten blind
		}
	} else if m != nil && !futureMarker(m, now) {
		age := now.Sub(m.At).Round(time.Second)
		if age < 0 {
			age = 0
		}
		live, readable := false, m.PID > 1 && m.Start != ""
		if readable {
			if start, ok := cleanerStartToken(m.PID); ok {
				live = start == m.Start
			} else if cleanerAlive(m.PID) {
				readable = false
			}
		}
		switch {
		case live:
			return "", "", fmt.Errorf("%w: seat %s is being cleaned by pid %d for %s", ErrSeatCleaning, handle, m.PID, age)
		case readable:
			decision = fmt.Sprintf("seat %s's cleaner marker belonged to dead pid %d; taking it over", handle, m.PID)
		case now.Sub(m.At) < SeatReservationTTL:
			return "", "", fmt.Errorf("%w: seat %s is being cleaned for %s; its cleaner identity is unreadable", ErrSeatCleaning, handle, age)
		}
	}
	pid := cleanerPID()
	start, _ := cleanerStartToken(pid)
	raw, _ := json.Marshal(seatMarker{Nonce: nonce, At: now.UTC(), PID: pid, Start: start, Cancel: cancel})
	if err := os.MkdirAll(reservedDir(dir), 0o700); err != nil {
		return "", "", err
	}
	// The one durable writer (temp, fsync, rename, fsync of the directory).
	if err := atomicfile.Write(markerPath(dir, handle), raw, 0o600); err != nil {
		return "", "", err
	}
	return nonce, decision, nil
}

// ReleaseSeat is CleanSeat's deferred second take: it removes the marker
// only when it is the cleaner's own (the nonce), else release_lost (a later
// cleaner took it over; its git work was serialized by the ledger, so this
// is a report, not a corruption), with the seat re-checked.
func (s Store) ReleaseSeat(handle, nonce string) error {
	handle, err := SeatHandle(handle)
	if err != nil {
		return err
	}
	return s.withLock(true, func() error {
		m, err := readMarker(s.Dir, handle)
		switch {
		case err != nil:
			return fmt.Errorf("%w: seat %s: %v", ErrReleaseLost, handle, err)
		case m == nil || m.Nonce != nonce:
			return fmt.Errorf("%w: seat %s's unclean marker is no longer this cleaner's", ErrReleaseLost, handle)
		}
		if err := os.Remove(markerPath(s.Dir, handle)); err != nil {
			return err
		}
		list, err := readDir(s.Dir)
		if err != nil {
			return err
		}
		if held := HeldBy(handle, list); held != nil {
			return fmt.Errorf("seat %s got task %s while its worktree was being cleaned", handle, held.ID)
		}
		return nil
	})
}

// SeatMarkerProblem is a marker status names to the lead: malformed or
// unreadable (its seat is unclean until a CleanSeat takes it over or the lead
// removes the file).
type SeatMarkerProblem struct {
	Handle  string `json:"handle"`
	Path    string `json:"path"`
	Problem string `json:"problem"`
}

// SeatMarkerProblems lists the store's malformed or unreadable markers, and
// those dated more than one TTL after now, sorted by seat.
func SeatMarkerProblems(dir string, now time.Time) []SeatMarkerProblem {
	entries, err := os.ReadDir(reservedDir(dir))
	if err != nil {
		return nil
	}
	var out []SeatMarkerProblem
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m, err := readMarker(dir, e.Name())
		switch {
		case err != nil:
			out = append(out, SeatMarkerProblem{Handle: e.Name(), Path: markerPath(dir, e.Name()), Problem: err.Error()})
		case futureMarker(m, now):
			out = append(out, SeatMarkerProblem{Handle: e.Name(), Path: markerPath(dir, e.Name()),
				Problem: fmt.Sprintf("the unclean marker %s is dated %s, more than %s ahead", markerPath(dir, e.Name()), m.At.UTC().Format(time.RFC3339), SeatReservationTTL)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Handle < out[j].Handle })
	return out
}

func newNonce() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("t%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
