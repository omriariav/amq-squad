package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	statusdoc "github.com/omriariav/amq-squad/v3/internal/status"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

func TestStatusExitsStripControlsFromJSONAndPlain(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 11, 11, 6, 0, 0, 0, time.UTC)
	s, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(task.AddInput{Title: "title\x1b]0;t\x07 end", Class: "implement"}, now); err != nil {
		t.Fatal(err)
	}
	reason := "the git dir /tmp/\x1b]0;r\x07 does not belong to the worktree /w\u009b31m"
	if err := schedproc.AppendDecision(root, now, scheduler.Decision{Kind: "tick_error", Seat: "dev\x1b]0;s\x07", Reason: reason, ReasonCode: "code\x1b]0;c\x07"}); err != nil {
		t.Fatal(err)
	}
	t.Run("JSON", func(t *testing.T) {
		out, _, err := captureOutput(t, func() error { return runStatusWithVersion([]string{"--root", root, "--json"}, "test") })
		if err != nil {
			t.Fatal(err)
		}
		var doc statusdoc.Status
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "]0;r") || !strings.Contains(out, "]0;s") || !strings.Contains(out, "]0;c") || strings.Contains(out, `\u001b`) {
			t.Fatalf("json status = %s", out)
		}
		assertNoControls(t, doc, false)
	})
	t.Run("plain", func(t *testing.T) {
		plain, _, err := captureOutput(t, func() error { return runStatusWithVersion([]string{"--root", root}, "test") })
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(plain, "]0;t") || strings.ContainsRune(plain, '\x1b') || strings.ContainsRune(plain, '\x07') {
			t.Fatalf("plain status = %q", plain)
		}
	})
}

func TestTaskListAndShowStripControls(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_ME", "")
	now := withFixedTaskNow(t)
	s, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.Add(task.AddInput{Title: "title\x1b]0;t\x07 end", Class: "implement"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Block(created.ID, "", "why\x1b]0;b\x07\u009b31m", "", task.RouteLead, now); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string][]string{"list": {"list"}, "show": {"show", created.ID}} {
		t.Run(name, func(t *testing.T) {
			out, err := taskCLI(t, args...)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, "]0;t") || !strings.Contains(out, "]0;b") {
				t.Fatalf("%v output = %q", args, out)
			}
			for _, r := range out {
				if r < 0x20 && r != '\n' || r >= 0x7f && r <= 0x9f {
					t.Fatalf("%v output has control %#x: %q", args, r, out)
				}
			}
		})
	}
}

func TestTaskBlockRefusesReservedSchedulerReason(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_ME", "")
	now := withFixedTaskNow(t)
	s, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.Add(task.AddInput{Title: "seat block", Class: "implement"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Grant(created.ID, "dev", nil, time.Minute, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(created.ID, "dev", nil, time.Minute, now); err != nil {
		t.Fatal(err)
	}
	for _, reason := range []string{"ledger_tampered", " ledger_tampered"} {
		_, err := taskCLI(t, "block", created.ID, "--me", "dev", "--reason", reason, "--detail", "seat x: quarantined at /q (seq 1; alarm 0123abcd): y")
		if err == nil || !strings.Contains(err.Error(), "ledger_tampered is the scheduler's; a seat blocks with its own reason") {
			t.Fatalf("reserved reason %q: %v", reason, err)
		}
		got, _ := s.Get(created.ID)
		if got.State != task.StateInProgress || got.Lease == nil || got.BlockReason != "" {
			t.Fatalf("task changed after refusal: %+v", got)
		}
	}
	pending, _ := s.Add(task.AddInput{Title: "scheduler block", Class: "implement"}, now)
	got, err := s.Block(pending.ID, "", "ledger_tampered", "scheduler detail", task.RouteLead, now)
	if err != nil || got.State != task.StateBlocked || got.BlockReason != "ledger_tampered" {
		t.Fatalf("store scheduler block = %+v, %v", got, err)
	}
	if _, err := taskCLI(t, "block", created.ID, "--me", "dev", "--reason", "test_failed"); err != nil {
		t.Fatal(err)
	}
}

func assertNoControls(t *testing.T, value any, inQuestion bool) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	var walk func(any, bool)
	walk = func(v any, question bool) {
		switch x := v.(type) {
		case string:
			for _, r := range x {
				if (r < 0x20 && !(question && r == '\n')) || r >= 0x7f && r <= 0x9f {
					t.Fatalf("control %#x in %q", r, x)
				}
			}
		case []any:
			for _, item := range x {
				walk(item, question)
			}
		case map[string]any:
			for key, item := range x {
				walk(item, key == "question")
			}
		}
	}
	walk(decoded, inQuestion)
}
