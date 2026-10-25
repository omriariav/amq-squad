package ledger

import (
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Phase 5 Codex re-pass on 3-2: one test row per item.

// P1-3: a secret flag followed by an unterminated quote redacts the rest of
// the command, not only its first word.
func TestRepassUnterminatedQuotedSecretIsRedactedWhole(t *testing.T) {
	for _, in := range []string{
		`{"command":"deploy --password 'alpha beta"}`,
		`{"command":"deploy --password \"alpha beta"}`,
		`{"command":"run PASSWORD='alpha beta"}`,
		`{"command":"curl -H 'Authorization: Bearer alpha beta"}`,
	} {
		got := string(Redact([]byte(in)))
		if strings.Contains(got, "alpha") || strings.Contains(got, "beta") {
			t.Errorf("%s -> %s", in, got)
		}
	}
}

// P2-7: the task-filtered reads stream the whole file: an outcome row past
// the first 8 MiB is found, and a ledger line that cannot be read makes the
// result incomplete with the reason, never a silent omission.
func TestRepassTaskEvidenceIsReadWholeOrMarkedIncomplete(t *testing.T) {
	root := t.TempDir()
	filler := `{"task":"t-other","pad":"` + strings.Repeat("x", 1000) + `"}`
	var rows []string
	for len(rows)*len(filler) < 9<<20 {
		rows = append(rows, filler)
	}
	rows = append(rows, `{"task":"t-1","attempt":1,"outcome":"completed"}`)
	write(t, OutcomePath(root), rows...)
	got, incomplete, err := Outcomes(root, "t-1")
	if err != nil || len(got) != 1 || len(incomplete) != 0 {
		t.Fatalf("outcome row past 8 MiB: %d rows, incomplete %v, %v", len(got), incomplete, err)
	}

	t0 := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	at := t0.Add(5 * time.Minute).Format(time.RFC3339)
	long := `{"at":"` + at + `","handle":"s1","pad":"` + strings.Repeat("y", MaxLine) + `"}`
	write(t, Path(root, "s1"), long, `{"at":"`+at+`","handle":"s1","seq":2}`)
	tk := task.Task{ID: "t-1", Attempt: 1, Attempts: []task.Attempt{{N: 1, Holder: "s1", GrantedAt: t0, EndedAt: t0.Add(10 * time.Minute)}}}
	lines, incomplete, err := ForTask(root, tk, t0.Add(time.Hour))
	if err != nil || len(lines) != 1 || len(incomplete) != 1 || !strings.Contains(incomplete[0], "s1.jsonl line 1") {
		t.Fatalf("ledger: %d lines, incomplete %v, %v", len(lines), incomplete, err)
	}
}

// P2-8: the live lease's window ends at its expiry when that is before now:
// a line written after an expiry the scheduler has not processed is not the
// task's evidence.
func TestRepassLiveLeaseWindowEndsAtItsExpiry(t *testing.T) {
	root := t.TempDir()
	t0 := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	at := func(m int) string { return t0.Add(time.Duration(m) * time.Minute).Format(time.RFC3339) }
	write(t, Path(root, "s1"), `{"seq":1,"at":"`+at(5)+`","handle":"s1"}`, `{"seq":2,"at":"`+at(25)+`","handle":"s1"}`)
	tk := task.Task{ID: "t-1", Attempt: 1, Lease: &task.Lease{Holder: "s1", GrantedAt: t0, ExpiresAt: t0.Add(20 * time.Minute)}}
	lines, _, err := ForTask(root, tk, t0.Add(30*time.Minute))
	if err != nil || len(lines) != 1 || !strings.Contains(string(lines[0].Line), `"seq":1`) {
		t.Fatalf("lines %s %v", linesText(lines), err)
	}
}

func linesText(ls []TaskLine) string {
	var b strings.Builder
	for _, l := range ls {
		b.Write(l.Line)
		b.WriteByte('\n')
	}
	return b.String()
}
