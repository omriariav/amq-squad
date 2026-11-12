package seatcommit

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/sealchain"
)

// appendRaw adds bytes to a seat's hold release file as they are.
func appendRaw(t *testing.T, root, handle string, b []byte) {
	t.Helper()
	f, err := os.OpenFile(AlarmsPath(root, handle), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(b); err != nil {
		t.Fatal(err)
	}
}

// lastLine is the last whole line of a seat's hold release file.
func lastLine(t *testing.T, root, handle string) []byte {
	t.Helper()
	raw := readFile(t, AlarmsPath(root, handle))
	lines := bytes.Split(bytes.TrimRight(raw, "\n"), []byte{'\n'})
	return append([]byte(nil), lines[len(lines)-1]...)
}

// The plan reviewer's ruling on Codex's pass on 8cffafe7: the hold release
// file, sealed over raw bytes. A line written by appendAlarm verifies and
// releases its alarm. A forged line (a seal that is not the key's), a line
// edited by one byte, a line written under another session root's seed,
// and a line sealed for another seat release nothing, each named once as a
// problem, and the lines around them still count. A body with a field this
// build does not know verifies, since the seal is over the bytes as
// written (the skew proof). A valid line followed by a torn tail still
// releases its alarm; the torn tail is a problem; the next append
// terminates it first and its own line verifies. Reverted (the seal not
// checked), the forged line releases; reverted (the seal checked over a
// re-encoding), the line with the unknown field does not.
func TestTheHoldReleaseFileReleasesOnlyVerifiedLines(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	at := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	dec := func(seat, alarm string) AlarmDecision {
		return AlarmDecision{Seat: seat, Alarm: alarm, Reason: "investigated", At: at, Lead: "cto"}
	}
	if err := appendAlarm(root, dec("s1", "aaaaaaa1")); err != nil {
		t.Fatal(err)
	}
	key, ok, err := sealchain.ReadKey(root)
	if err != nil || !ok {
		t.Fatalf("the session seed: %v %v", ok, err)
	}
	// forged: a seal that is not the key's
	appendRaw(t, root, "s1", []byte(`{"seat":"s1","alarm":"bbbbbbb2","at":"2026-11-06T12:00:00Z","lead":"cto"}`+"\t"+strings.Repeat("0", 64)+"\n"))
	// edited by one byte
	if err := appendAlarm(root, dec("s1", "ccccccc3")); err != nil {
		t.Fatal(err)
	}
	edited := lastLine(t, root, "s1")
	raw := readFile(t, AlarmsPath(root, "s1"))
	edited2 := bytes.Replace(edited, []byte("investigated"), []byte("investigatee"), 1)
	if err := os.WriteFile(AlarmsPath(root, "s1"), bytes.Replace(raw, edited, edited2, 1), 0o600); err != nil {
		t.Fatal(err)
	}
	// another session root's seed
	if err := appendAlarm(other, dec("s1", "ddddddd4")); err != nil {
		t.Fatal(err)
	}
	appendRaw(t, root, "s1", append(lastLine(t, other, "s1"), '\n'))
	// a field this build does not know, sealed over its bytes
	body := []byte(`{"seat":"s1","alarm":"eeeeeee5","at":"2026-11-06T12:00:00Z","lead":"cto","by_build":"3.3.0"}`)
	appendRaw(t, root, "s1", append(append(append(body, '\t'), alarmTag(key.Key, body)...), '\n'))
	// sealed, but for another seat
	if err := appendAlarm(root, dec("s2", "9999999a")); err != nil {
		t.Fatal(err)
	}
	appendRaw(t, root, "s1", append(lastLine(t, root, "s2"), '\n'))
	// a valid line, then a torn tail
	if err := appendAlarm(root, dec("s1", "fffffff6")); err != nil {
		t.Fatal(err)
	}
	appendRaw(t, root, "s1", []byte(`{"seat":"s1","alarm":"7777777`))
	log, err := ReadAlarms(root, "s1")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"aaaaaaa1", "eeeeeee5", "fffffff6"} {
		if !log.Released(a) {
			t.Fatalf("%s does not release: %+v", a, log)
		}
	}
	for _, a := range []string{"bbbbbbb2", "ccccccc3", "ddddddd4", "9999999a", "7777777"} {
		if log.Released(a) {
			t.Fatalf("%s releases: %+v", a, log)
		}
	}
	want := []string{"line 2 fails its seal", "line 3 fails its seal", "line 4 fails its seal", "line 6 names seat s2", "a torn last line of 29 bytes (no newline)"}
	for _, w := range want {
		n := 0
		for _, p := range log.Problems {
			if p == w {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("problem %q named %d times: %q", w, n, log.Problems)
		}
	}
	if len(log.Problems) != len(want) {
		t.Fatalf("the problems: %q", log.Problems)
	}
	// the next append terminates the torn tail; its own line verifies
	if err := appendAlarm(root, dec("s1", "1234abcd")); err != nil {
		t.Fatal(err)
	}
	if log, _ = ReadAlarms(root, "s1"); !log.Released("1234abcd") || log.Released("7777777") {
		t.Fatalf("after the torn tail: %+v", log)
	}
	for _, p := range log.Problems {
		if strings.HasPrefix(p, "a torn") {
			t.Fatalf("the torn tail is still torn: %q", log.Problems)
		}
	}
	if fi, err := os.Stat(filepath.Join(LedgerDir(root), "s1.alarms.jsonl")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("the file: %v %v", err, fi)
	}
}

func TestAlarmAppendSyncsNewDirectoryEntryOnce(t *testing.T) {
	root := t.TempDir()
	files, dirs := 0, 0
	var syncedDir string
	var order []string
	oldFile, oldDir := syncAlarmFile, syncAlarmDir
	syncAlarmFile = func(f *os.File) error { files++; order = append(order, "file"); return f.Sync() }
	syncAlarmDir = func(dir string) error { dirs++; syncedDir = dir; order = append(order, "directory"); return nil }
	t.Cleanup(func() { syncAlarmFile, syncAlarmDir = oldFile, oldDir })
	d := AlarmDecision{Seat: "s1", Alarm: "one", At: time.Now().UTC(), Lead: "lead"}
	if err := appendAlarm(root, d); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(order, []string{"file", "directory"}) {
		t.Fatalf("first append sync order = %v", order)
	}
	order = nil
	d.Alarm = "two"
	if err := appendAlarm(root, d); err != nil {
		t.Fatal(err)
	}
	if files != 2 || dirs != 1 || syncedDir != LedgerDir(root) {
		t.Fatalf("syncs: file=%d dir=%d path=%q, want 2, 1, %q", files, dirs, syncedDir, LedgerDir(root))
	}
	if !slices.Equal(order, []string{"file"}) {
		t.Fatalf("second append sync order = %v", order)
	}
}

func TestAlarmAppendReturnsNewDirectorySyncFailure(t *testing.T) {
	f := newFixture(t)
	want := errors.New("directory sync failed")
	oldDir := syncAlarmDir
	syncAlarmDir = func(string) error { return want }
	t.Cleanup(func() { syncAlarmDir = oldDir })
	_, err := RecoverLedger(RecoverOptions{Root: f.root, Handle: f.handle, Alarm: "1234abcd", Lead: "lead", Reason: "reviewed", Now: f.now})
	if recoverCode(t, err) != RecoverWriteFailed || !strings.Contains(err.Error(), want.Error()) {
		t.Fatalf("decision error = %v, want %v", err, want)
	}
}
