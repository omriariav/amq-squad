package task

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The fourth round of v3-5-verifier, item 2: a cleaner releases only its own
// marker. A reserves, B takes the marker over after the TTL, A's release is
// release_lost and B's marker stands (the seat stays unclean), B's release
// frees the seat.
func TestOnlyTheCleanersOwnMarkerIsReleased(t *testing.T) {
	s := newStore(t)
	a := mustAdd(t, s, AddInput{Title: "after the clean", Class: "summarize"})
	_, nonceA, err := s.ReserveSeat("seat-1", t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ReserveSeat("seat-1", t0.Add(time.Minute)); !errors.Is(err, ErrSeatCleaning) {
		t.Fatalf("a second cleaner over a fresh marker: %v", err)
	}
	originalStart := cleanerStartToken
	cleanerStartToken = func(int) (string, bool) { return "reused-pid", true }
	t.Cleanup(func() { cleanerStartToken = originalStart })
	_, nonceB, err := s.ReserveSeat("seat-1", t0.Add(SeatReservationTTL+time.Minute))
	if err != nil {
		t.Fatalf("a takeover after the TTL: %v", err)
	}
	if err := s.ReleaseSeat("seat-1", nonceA); !errors.Is(err, ErrReleaseLost) {
		t.Fatalf("A's release after B's takeover: %v", err)
	}
	if _, err := s.Claim(a.ID, "seat-1", nil, time.Hour, t0); !errors.Is(err, ErrSeatUnclean) {
		t.Fatalf("a claim while B's marker stands: %v", err)
	}
	if err := s.ReleaseSeat("seat-1", nonceB); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(a.ID, "seat-1", nil, time.Hour, t0); err != nil {
		t.Fatalf("a claim after B's clean: %v", err)
	}
}

func TestCleanerMarkerUsesProcessIdentityBeforeTTL(t *testing.T) {
	s := newStore(t)
	if err := os.MkdirAll(reservedDir(s.Dir), 0o700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(seatMarker{Nonce: "old", At: t0, PID: 4242, Start: "first"})
	if err := os.WriteFile(markerPath(s.Dir, "seat-1"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	originalStart, originalAlive := cleanerStartToken, cleanerAlive
	cleanerStartToken = func(pid int) (string, bool) { return "first", pid == 4242 }
	cleanerAlive = func(int) bool { return true }
	t.Cleanup(func() { cleanerStartToken, cleanerAlive = originalStart, originalAlive })
	if _, _, _, err := s.ReserveSeatWithDecision("seat-1", t0.Add(2*SeatReservationTTL)); !errors.Is(err, ErrSeatCleaning) {
		t.Fatalf("a live cleaner past the old TTL was taken over: %v", err)
	}
	cleanerStartToken = func(pid int) (string, bool) { return "reused", pid == 4242 }
	_, _, decision, err := s.ReserveSeatWithDecision("seat-1", t0.Add(time.Second))
	if err != nil || !strings.Contains(decision, "dead pid 4242") {
		t.Fatalf("a dead cleaner was not taken over immediately: decision=%q err=%v", decision, err)
	}
}

// Item 3: a malformed or unreadable marker is seat_unclean, never a free
// seat, and SeatMarkerProblems names its file for the lead's attention.
func TestMalformedOrUnreadableMarkerIsUnclean(t *testing.T) {
	s := newStore(t)
	a := mustAdd(t, s, AddInput{Title: "t", Class: "summarize"})
	if err := os.MkdirAll(reservedDir(s.Dir), 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(reservedDir(s.Dir), "seat-1")
	if err := os.WriteFile(p, []byte("not a marker"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(a.ID, "seat-1", nil, time.Hour, t0); !errors.Is(err, ErrSeatUnclean) {
		t.Fatalf("a claim over a malformed marker: %v", err)
	}
	if ps := SeatMarkerProblems(s.Dir, t0); len(ps) != 1 || ps[0].Handle != "seat-1" || ps[0].Path != p || !strings.Contains(ps[0].Problem, "malformed") {
		t.Fatalf("the marker problems: %+v", ps)
	}
	if err := os.Chmod(p, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(p, 0o600) })
	if _, err := os.ReadFile(p); err == nil {
		t.Skip("an unreadable file is readable here (running as root)")
	}
	if _, err := s.Claim(a.ID, "seat-1", nil, time.Hour, t0); !errors.Is(err, ErrSeatUnclean) {
		t.Fatalf("a claim over an unreadable marker: %v", err)
	}
	if ps := SeatMarkerProblems(s.Dir, t0); len(ps) != 1 || !strings.Contains(ps[0].Problem, "cannot be read") {
		t.Fatalf("the unreadable marker's problem: %+v", ps)
	}
}
