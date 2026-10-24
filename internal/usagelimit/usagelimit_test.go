package usagelimit

import (
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

func TestMissingDirectoriesAreNotErrors(t *testing.T) {
	d := ForHome(t.TempDir(), "", "")
	if ev, err := d.Scan(time.Time{}); err != nil || len(ev) != 0 {
		t.Fatalf("%v %v", ev, err)
	}
	if d := ForHome("/h", "/c", "/x"); d.ClaudeProjects != filepath.Join("/c", "projects") || d.CodexSessions != filepath.Join("/x", "sessions") {
		t.Fatalf("env dirs %+v", d)
	}
}
