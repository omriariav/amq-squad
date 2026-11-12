package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	created, err := s.Add(task.AddInput{
		Title:         "title\x1b]0;t\x07 end",
		Description:   "first\x1b]0;d\x07\nsecond",
		Class:         "implement",
		Pool:          &task.Pool{Runtime: "codex", Model: "m\x1b]0;p\x07", Effort: "high"},
		DeclaredFiles: []string{"docs/a\u009bb.md"},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	created, err = s.Block(created.ID, "", "why\x1b]0;b\x07\u009b31m", "route\x1b]0;br\x07", task.RouteLead, now)
	if err != nil {
		t.Fatal(err)
	}
	writeStored := func(tk task.Task) {
		t.Helper()
		raw, err := json.MarshalIndent(tk, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(s.Dir, tk.ID+".json"), append(raw, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// These values model text already on disk. AddInput above exercises the
	// store paths for pool and declared files; no store call accepts the
	// legacy dependency or the control-bearing attempt fields.
	created.DependsOn = []string{"t\x1b]0;dep\x07"}
	created.Attempts = []task.Attempt{{N: 1, Holder: "dev\x1b]0;ah\x07", Outcome: "failed\x1b]0;ao\x07", Detail: "detail\x1b]0;ad\x07"}}
	writeStored(created)

	// A current holder with controls can only be legacy text already on disk:
	// SeatHandle now refuses it before any current store call can write it.
	leased, err := s.Add(task.AddInput{Title: "leased", Class: "implement"}, now)
	if err != nil {
		t.Fatal(err)
	}
	claimed := now
	leased.State = task.StateInProgress
	leased.Lease = &task.Lease{Holder: "dev\x1b]0;h\x07", ClaimedAt: &claimed, ExpiresAt: now.Add(time.Minute)}
	writeStored(leased)

	evidenced, err := s.Add(task.AddInput{Title: "evidence", Class: "summarize"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Grant(evidenced.ID, "worker-e", nil, time.Minute, now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Claim(evidenced.ID, "worker-e", nil, time.Minute, now); err != nil {
		t.Fatal(err)
	}
	if evidenced, err = s.Done(evidenced.ID, "worker-e", "proof\x1b]0;e\x07", "", now); err != nil {
		t.Fatal(err)
	}

	cancelled, err := s.Add(task.AddInput{Title: "cancelled", Class: "implement"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled, err = s.Cancel(cancelled.ID, "lead", "cancel\x1b]0;c\x07", now); err != nil {
		t.Fatal(err)
	}

	reported, err := s.Add(task.AddInput{Title: "reported", Class: "implement", DeclaredFiles: []string{"src/"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Grant(reported.ID, "worker-r", nil, time.Minute, now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Claim(reported.ID, "worker-r", nil, time.Minute, now); err != nil {
		t.Fatal(err)
	}
	if reported, err = s.Done(reported.ID, "worker-r", "", "", now); err != nil {
		t.Fatal(err)
	}
	reported.Report.By = "author\x1b]0;r\x07"
	writeStored(reported)

	listOut, err := taskCLI(t, "list")
	if err != nil {
		t.Fatal(err)
	}
	var shown strings.Builder
	for _, id := range []string{created.ID, leased.ID, evidenced.ID, cancelled.ID, reported.ID} {
		out, err := taskCLI(t, "show", id)
		if err != nil {
			t.Fatal(err)
		}
		shown.WriteString(out)
	}
	for name, out := range map[string]string{"list": listOut, "show": shown.String()} {
		t.Run(name, func(t *testing.T) {
			for _, tag := range []string{"]0;t", "]0;p", "]0;h", "]0;b"} {
				if !strings.Contains(out, tag) {
					t.Fatalf("%s output lacks %q: %q", name, tag, out)
				}
			}
			if name == "show" {
				for _, tag := range []string{"]0;dep", "]0;ah", "]0;ao", "]0;ad", "]0;br", "]0;e", "]0;c", "]0;r", "]0;d"} {
					if !strings.Contains(out, tag) {
						t.Fatalf("%s output lacks %q: %q", name, tag, out)
					}
				}
				if !strings.Contains(out, "first ]0;d \nsecond") {
					t.Fatalf("description did not keep its newline: %q", out)
				}
			}
			if !strings.Contains(out, "]0;t") || !strings.Contains(out, "]0;b") {
				t.Fatalf("%s output = %q", name, out)
			}
			for _, r := range out {
				if r < 0x20 && r != '\n' || r >= 0x7f && r <= 0x9f {
					t.Fatalf("%s output has control %#x: %q", name, r, out)
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
