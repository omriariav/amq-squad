package cli

import (
	"testing"
	"time"

	"github.com/omriariav/amq-squad/internal/launch"
)

func TestListRecordsFromEntriesIncludesSource(t *testing.T) {
	when := time.Date(2026, 4, 25, 5, 48, 0, 0, time.UTC)
	entries := []launch.Entry{
		{
			Record: launch.Record{
				Role:      "cto",
				Handle:    "cto",
				Binary:    "codex",
				Session:   "cto",
				CWD:       "/p",
				StartedAt: when,
			},
			Source: launch.FileName,
		},
		{
			Record: launch.Record{
				Handle:  "claude",
				Binary:  "claude",
				Session: "stream1",
				CWD:     "/legacy",
			},
			Source: "amq history",
		},
	}

	rows := listRecordsFromEntries(entries)
	if rows[0].Source != "amq-squad" {
		t.Errorf("Source[0] = %q, want amq-squad", rows[0].Source)
	}
	if rows[1].Source != "amq" {
		t.Errorf("Source[1] = %q, want amq", rows[1].Source)
	}
	if !rows[0].StartedAt.Equal(when) {
		t.Errorf("StartedAt = %v, want %v", rows[0].StartedAt, when)
	}
}

func TestFormatListTime(t *testing.T) {
	if got := formatListTime(time.Time{}); got != "" {
		t.Errorf("zero time formatted as %q, want empty", got)
	}
	when := time.Date(2026, 4, 25, 5, 48, 0, 0, time.UTC)
	if got := formatListTime(when); got != "2026-04-25 05:48" {
		t.Errorf("formatListTime = %q, want 2026-04-25 05:48", got)
	}
}
