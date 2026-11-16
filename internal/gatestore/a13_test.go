package gatestore

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// A1-3: the window re-ask in the store.

func windowReask(id string, at time.Time) Reask {
	return Reask{GateID: id, TriggerEventID: strings.Repeat("d4", 32), EventID: strings.Repeat("e5", 32),
		RawEvent: fmt.Sprintf(`{"created_at":%d}`, at.Unix()), At: at.Format(time.RFC3339)}
}

// S1: one window re-ask per gate, beside the re-ask; the gate is open again
// until the derived end (the re-ask's created_at plus the record's own
// window), expired after it.
func TestA13WindowReaskInTheStore(t *testing.T) {
	s := testStore(t)
	rec := raised(t, s, "g1")
	_, _, na, _ := rec.Window()
	at := na.Add(time.Hour)
	if st := s.State(rec, at); st != StateExpired {
		t.Fatalf("lapsed state: %s", st)
	}
	r, created, err := s.ReserveWindow("g1", func() (Reask, error) { return windowReask("g1", at), nil })
	if err != nil || !created {
		t.Fatalf("reserve: %v %v", created, err)
	}
	if again, created, _ := s.ReserveWindow("g1", func() (Reask, error) { return windowReask("g1", at.Add(time.Hour)), nil }); created || again.RawEvent != r.RawEvent {
		t.Fatalf("a second window re-ask: %+v %v", again, created)
	}
	if re, _ := s.ReadReask("g1"); re != nil {
		t.Fatalf("the window re-ask is a re-ask: %+v", re)
	}
	if s.WindowPublished("g1") {
		t.Fatal("published before the mark")
	}
	if err := s.MarkWindowPublished("g1"); err != nil || !s.WindowPublished("g1") || s.ReaskPublished("g1") {
		t.Fatalf("mark: %v", err)
	}
	wat, end, err := r.WindowEnd(rec)
	if err != nil || !wat.Equal(at) || !end.Equal(at.Add(30*time.Minute)) {
		t.Fatalf("WindowEnd: %s %s %v", wat, end, err)
	}
	for _, c := range []struct {
		now  time.Time
		want string
	}{{at, StateOpen}, {end, StateOpen}, {end.Add(time.Second), StateExpired}} {
		if st := s.State(rec, c.now); st != c.want {
			t.Errorf("state at %s: %s, want %s", c.now, st, c.want)
		}
	}
	if _, _, err := s.ReserveReask("g1", func() (Reask, error) {
		r := windowReask("g1", at)
		r.EventID = strings.Repeat("f6", 32)
		return r, nil
	}); err != nil {
		t.Fatal(err)
	}
	if ids := s.ReaskEventIDs("g1"); !slices.Equal(ids, []string{strings.Repeat("f6", 32), strings.Repeat("e5", 32)}) {
		t.Fatalf("ReaskEventIDs: %v", ids)
	}
	if _, _, err := (Reask{RawEvent: "{}"}).WindowEnd(rec); err == nil {
		t.Fatal("a window re-ask with no created_at has an end")
	}
}

// S2: the store's temp sweep covers windows/: a write temp a killed writer
// left there, older than the lock, is removed; the window re-ask stays.
func TestA13TheSweepCoversWindows(t *testing.T) {
	s := testStore(t)
	raised(t, s, "g1")
	if err := s.PutWindow(windowReask("g1", testNow)); err != nil {
		t.Fatal(err)
	}
	tmp := s.Dir("windows", ".tmp-once-123")
	if err := os.WriteFile(tmp, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(tmp, old, old); err != nil {
		t.Fatal(err)
	}
	if err := s.Recover("g1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tmp); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the left temp in windows/ survived the sweep: %v", err)
	}
	if w, _ := s.ReadWindow("g1"); w == nil {
		t.Fatal("the sweep removed the window re-ask")
	}
}

// S3 (the structural row): every directory the store writes is in TopDirs,
// and every TopDirs entry is written: each writer runs (raise, re-ask,
// window re-ask, an approval and a denial), then the root's directories are
// compared with TopDirs. A new directory written and not listed fails here,
// and so is neither swept nor kept by uninstall.
func TestA13TheStoreWritesExactlyItsTopDirs(t *testing.T) {
	s := testStore(t)
	g1, g2 := raised(t, s, "g1"), raised(t, s, "g2")
	if _, _, err := s.ReserveReask("g1", func() (Reask, error) {
		r := windowReask("g1", testNow)
		r.EventID = strings.Repeat("f6", 32)
		return r, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ReserveWindow("g2", func() (Reask, error) { return windowReask("g2", testNow), nil }); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(answerFile(g1, "approved", strings.Repeat("01", 32))); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(answerFile(g2, "denied", strings.Repeat("02", 32))); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	var written []string
	for _, e := range entries {
		if e.IsDir() {
			written = append(written, e.Name())
		}
	}
	want := slices.Clone(TopDirs)
	slices.Sort(want)
	slices.Sort(written)
	if !slices.Equal(written, want) {
		t.Fatalf("the store wrote %v, TopDirs is %v", written, want)
	}
}
