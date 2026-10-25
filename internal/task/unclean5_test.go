package task

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The fifth round of v3-5-verifier on the unclean marker.

// (d): a marker dated more than one TTL ahead is invalid: the seat stays
// unclean, status gets its problem, and a CleanSeat takes it over.
func TestFarFutureMarkerIsInvalidAndTakenOver(t *testing.T) {
	s := newStore(t)
	a := mustAdd(t, s, AddInput{Title: "t", Class: "summarize"})
	raw, _ := json.Marshal(seatMarker{Nonce: "planted", At: t0.Add(24 * time.Hour)})
	os.MkdirAll(reservedDir(s.Dir), 0o700)
	if err := os.WriteFile(filepath.Join(reservedDir(s.Dir), "seat-1"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(a.ID, "seat-1", nil, time.Hour, t0); !errors.Is(err, ErrSeatUnclean) {
		t.Fatalf("a claim over a far-future marker: %v", err)
	}
	if ps := SeatMarkerProblems(s.Dir, t0); len(ps) != 1 || !strings.Contains(ps[0].Problem, "ahead") {
		t.Fatalf("the far-future marker's problem: %+v", ps)
	}
	if _, _, err := s.ReserveSeat("seat-1", t0); err != nil {
		t.Fatalf("a CleanSeat over a far-future marker: %v", err)
	}
}

// (e): the marker is written through the one durable writer: a reader that
// races the writes never sees a partial or empty marker.
func TestMarkerWritesAreNeverSeenPartial(t *testing.T) {
	s := newStore(t)
	var stop atomic.Bool
	var torn atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		for !stop.Load() {
			raw, err := os.ReadFile(filepath.Join(reservedDir(s.Dir), "seat-1"))
			if err == nil && json.Unmarshal(raw, &seatMarker{}) != nil {
				torn.Add(1)
			}
		}
	}()
	now := t0
	for i := 0; i < 1500; i++ {
		now = now.Add(SeatReservationTTL + time.Second) // every reserve takes the last one over
		if _, _, err := s.ReserveSeat("seat-1", now); err != nil {
			stop.Store(true)
			<-done
			t.Fatal(err)
		}
	}
	stop.Store(true)
	<-done
	if n := torn.Load(); n > 0 {
		t.Fatalf("a reader saw %d partial markers", n)
	}
}

// (f): one handle normalizer for the marker and the lease: a seat reserved
// as " seat-1 " is the seat a claim names "seat-1".
func TestMarkerHandleIsNormalizedOnce(t *testing.T) {
	s := newStore(t)
	a := mustAdd(t, s, AddInput{Title: "t", Class: "summarize"})
	if _, _, err := s.ReserveSeat(" seat-1 ", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(a.ID, "seat-1", nil, time.Hour, t0); !errors.Is(err, ErrSeatUnclean) {
		t.Fatalf("a claim of the seat reserved with blanks: %v", err)
	}
	if _, _, err := s.ReserveSeat("../seat-1", t0); err == nil {
		t.Fatal("a handle with a path separator was reserved")
	}
}
