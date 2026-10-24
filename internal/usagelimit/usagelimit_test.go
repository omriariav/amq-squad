package usagelimit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(name string) Detector {
	return Detector{
		ClaudeProjects: filepath.Join("testdata", name, "claude", "projects"),
		CodexSessions:  filepath.Join("testdata", name, "codex", "sessions"),
	}
}

// Acceptance 8: the S6-shaped session files give no event, one usage-limit
// line gives one event. The reset banner never counts.
func TestS6ShapedFilesHaveNoEvents(t *testing.T) {
	all, err := fixture("s6").Scan(time.Time{})
	if err != nil || len(all) != 0 {
		t.Fatalf("S6 replay: %v %+v", err, all)
	}
}

func TestOneUsageLimitLineIsOneEvent(t *testing.T) {
	d := fixture("limit")
	d.CodexSessions = ""
	events, err := d.UsageLimitEvents(time.Time{})
	if err != nil || len(events) != 1 {
		t.Fatalf("events %v %+v", err, events)
	}
	e := events[0]
	if e.Provider != "claude" || e.Kind != KindUsageLimit || !strings.HasSuffix(e.Source, ".jsonl:2") {
		t.Fatalf("event %+v", e)
	}
	if !e.At.Equal(time.Date(2026, 10, 21, 2, 10, 4, 0, time.UTC)) {
		t.Fatalf("at %v", e.At)
	}
	// "resets 5:00am (Asia/Jerusalem)": 05:00 IDT on 2026-10-21 is 02:00 UTC,
	// before the event, so the next one is 02:00 UTC on 2026-10-22.
	if loc, err := time.LoadLocation("Asia/Jerusalem"); err == nil {
		want := time.Date(2026, 10, 22, 5, 0, 0, 0, loc).UTC()
		if !e.ResetAt.Equal(want) {
			t.Fatalf("reset %v, want %v", e.ResetAt, want)
		}
	}
	all, _ := d.Scan(time.Time{})
	kinds := map[string]int{}
	for _, e := range all {
		kinds[e.Kind]++
	}
	if kinds[KindUsageLimit] != 1 || kinds[KindHTTP429] != 1 || kinds[KindRetry429] != 1 {
		t.Fatalf("kinds %v", kinds)
	}
	if later, _ := d.UsageLimitEvents(e.At.Add(time.Second)); len(later) != 0 {
		t.Fatalf("since filter: %+v", later)
	}
}

func TestCodexRolloutSignals(t *testing.T) {
	d := fixture("limit")
	d.ClaudeProjects = ""
	d.Seats = map[string]string{"/Users/o/code/workspace-cli/.s6-wt/w10": "w10"}
	events, err := d.UsageLimitEvents(time.Time{})
	if err != nil || len(events) != 2 {
		t.Fatalf("codex events %v %+v", err, events)
	}
	at := time.Date(2026, 10, 22, 7, 20, 0, 0, time.UTC)
	if events[0].Seat != "w10" || !events[0].ResetAt.Equal(at.Add(time.Hour)) {
		t.Fatalf("token_count event %+v", events[0])
	}
	if !events[1].ResetAt.Equal(at.Add(time.Second + time.Hour)) {
		t.Fatalf("error event reset %+v", events[1])
	}
	d.Seats = map[string]string{"/Users/o/other": "x"}
	if other, _ := d.UsageLimitEvents(time.Time{}); len(other) != 0 {
		t.Fatalf("another seat's rollout counted: %+v", other)
	}
}

func TestClaudeSeatFilterAndSummary(t *testing.T) {
	d := fixture("limit")
	d.Seats = map[string]string{"/Users/o/code/amq-noc": "lead", "/Users/o/code/workspace-cli/.s6-wt/w10": "w10"}
	all, _ := d.Scan(time.Time{})
	if len(all) != 5 || all[0].Seat != "lead" {
		t.Fatalf("scan %+v", all)
	}
	sum := Summarize(all)
	if len(sum) != 2 || sum[0].Provider != "claude" || sum[0].Counts[KindUsageLimit] != 1 || sum[1].Counts[KindUsageLimit] != 2 {
		t.Fatalf("summary %+v", sum)
	}
	d.Seats = map[string]string{"/Users/o/elsewhere": "x"}
	if none, _ := d.Scan(time.Time{}); len(none) != 0 {
		t.Fatalf("seat filter: %+v", none)
	}
}

// Review P2 (10): a rotated or copied rollout file and a copied transcript
// carry the same events; each counts once. Two sessions stay two.
func TestRotatedCopiesCountOnce(t *testing.T) {
	src := filepath.Join("testdata", "limit", "codex", "sessions", "2026", "10", "22", "rollout-2026-10-22T07-00-00-0199bbbb-cccc-7ddd-8eee-ffffffffffff.jsonl")
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	day := filepath.Join(home, ".codex", "sessions", "2026", "10", "22")
	os.MkdirAll(day, 0o700)
	os.WriteFile(filepath.Join(day, "rollout-a-0199bbbb.jsonl"), raw, 0o600)
	os.WriteFile(filepath.Join(day, "rollout-b-0199bbbb.jsonl"), raw, 0o600)
	other := strings.Replace(string(raw), "0199bbbb-cccc-7ddd-8eee-ffffffffffff", "0199cccc-0000-7000-8000-000000000000", 1)
	os.WriteFile(filepath.Join(day, "rollout-c-0199cccc.jsonl"), []byte(other), 0o600)
	claudeRaw, _ := os.ReadFile(filepath.Join("testdata", "limit", "claude", "projects", "-Users-o-code-amq-noc", "aaaa0000-0000-4000-8000-000000000002.jsonl"))
	proj := filepath.Join(home, ".claude", "projects", "-repo")
	os.MkdirAll(proj, 0o700)
	os.WriteFile(filepath.Join(proj, "a.jsonl"), claudeRaw, 0o600)
	os.WriteFile(filepath.Join(proj, "a-copy.jsonl"), claudeRaw, 0o600)
	events, err := ForHome(home, "", "").UsageLimitEvents(time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	perProvider := map[string]int{}
	for _, e := range events {
		perProvider[e.Provider]++
	}
	// codex: two events per session (token_count, error) x two sessions; claude: one.
	if perProvider["codex"] != 4 || perProvider["claude"] != 1 {
		t.Fatalf("events per provider %v: %+v", perProvider, events)
	}
}

// Second review pass, item 10: distinct events whose first 160 characters
// match stay two, and events without a session id never merge.
func TestDedupeKeepsDistinctEvents(t *testing.T) {
	home := t.TempDir()
	proj := filepath.Join(home, ".claude", "projects", "-repo")
	os.MkdirAll(proj, 0o700)
	prefix := strings.Repeat("You've hit your session limit ", 6) // over 160 characters
	line := func(uuid, session, tail string) string {
		s := `{"type":"assistant","timestamp":"2026-10-21T02:10:04.000Z","isApiErrorMessage":true,"apiErrorStatus":429,`
		if session != "" {
			s += `"sessionId":"` + session + `",`
		}
		if uuid != "" {
			s += `"uuid":"` + uuid + `",`
		}
		return s + `"message":{"content":[{"type":"text","text":"` + prefix + tail + `"}]}}` + "\n"
	}
	os.WriteFile(filepath.Join(proj, "a.jsonl"), []byte(line("u1", "s1", "resets 5am")+line("u2", "s1", "resets 6am")), 0o600)
	// The same line twice without a session id, in two files: two events.
	os.WriteFile(filepath.Join(proj, "b.jsonl"), []byte(line("", "", "x")), 0o600)
	os.WriteFile(filepath.Join(proj, "c.jsonl"), []byte(line("", "", "x")), 0o600)
	// A copy of a.jsonl: its two events count once.
	raw, _ := os.ReadFile(filepath.Join(proj, "a.jsonl"))
	os.WriteFile(filepath.Join(proj, "a-copy.jsonl"), raw, 0o600)
	events, _ := ForHome(home, "", "").UsageLimitEvents(time.Time{})
	if len(events) != 4 {
		t.Fatalf("events = %d, want 4 (u1, u2, and the two session-less lines): %+v", len(events), events)
	}
}

func TestMissingDirectoriesAreNotErrors(t *testing.T) {
	d := ForHome(t.TempDir(), "", "")
	if ev, err := d.Scan(time.Time{}); err != nil || len(ev) != 0 {
		t.Fatalf("%v %v", ev, err)
	}
	if d := ForHome("/h", "/c", "/x"); d.ClaudeProjects != filepath.Join("/c", "projects") || d.CodexSessions != filepath.Join("/x", "sessions") {
		t.Fatalf("env dirs %+v", d)
	}
}
