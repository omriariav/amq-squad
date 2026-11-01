package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
	"github.com/omriariav/amq-squad/v3/internal/flock"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Brief 31-1, row 11 (the scheduler's half): a scheduler whose squad's
// run.lock another process holds (ErrRunning) is a refusal with the exit
// the lead's supervisor never restarts, so it is attempted once, not five
// times as a crash (TestSupervisorDoesNotRestartARefusal is the
// supervisor's half).
func TestSchedulerErrRunningIsARefusalAttemptedOnce(t *testing.T) {
	_, root, proj := schedulerFixture(t)
	if err := os.MkdirAll(schedproc.Dir(root), 0o700); err != nil {
		t.Fatal(err)
	}
	lock, ok, err := flock.TryExclusive(filepath.Join(schedproc.Dir(root), "run.lock"), true)
	if err != nil || !ok {
		t.Fatalf("hold the lock: %v %v", ok, err)
	}
	defer lock.Close()
	_, _, err = captureOutput(t, func() error {
		return runSchedulerRun(context.Background(), []string{"--project", proj, "--tick", "20ms"}, os.Stdout)
	})
	if ExitCode(err) != ExitSchedulerRefused || !strings.Contains(err.Error(), schedproc.ErrRunning.Error()) {
		t.Fatalf("a second scheduler: %v (exit %d)", err, ExitCode(err))
	}
}

// Brief 31-1, the start stamp: scheduler run records AMQ_SQUAD_START (set
// by `lead --team`, by `up` in brief 31-1's second PR) in its launch record
// as the start of the squad's launch; without it the record names none.
func TestSchedulerRecordsTheLaunchStart(t *testing.T) {
	_, root, proj := schedulerFixture(t)
	t.Setenv(leadStartEnv, "2026-10-31T19:24:30.5Z")
	ctx, cancel := context.WithCancel(context.Background())
	prev := schedulerStarted
	schedulerStarted = func(*scheduler.Scheduler) { cancel() }
	t.Cleanup(func() { schedulerStarted = prev })
	if _, _, err := captureOutput(t, func() error {
		return runSchedulerRun(ctx, []string{"--project", proj, "--tick", "20ms"}, os.Stdout)
	}); err != nil {
		t.Fatal(err)
	}
	rec, err := schedproc.ReadLaunchRecord(root)
	if err != nil || rec.Start == nil || rec.Start.Format("15:04:05.0") != "19:24:30.5" || rec.Lead != "lead" {
		t.Fatalf("launch record %+v %v", rec, err)
	}
	// brief 31-1's second PR: the scheduler's raw start stamp, the identity
	// up checks before it signals the recorded pid
	if want, ok := procinfo.StartToken(os.Getpid()); !ok || rec.ProcessStart != want || rec.PID != os.Getpid() {
		t.Fatalf("process start %q, want %q (pid %d)", rec.ProcessStart, want, rec.PID)
	}
}

// Brief 31-1, row 8: the checked-in squad.yaml (no worktree keys) is the
// squad of scheduler run: every seat but its lead is in the roster, each in
// a worktree derived under <repo>/.worktrees/<handle>, none at the
// repository root, and the lead's handle is squad.yaml's. (It refused with
// "has no seat that can start" before.)
func TestTheCheckedInSquadYAMLLaunchesWithDerivedWorktrees(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	raw, err := os.ReadFile(filepath.Join("..", "..", "squad.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	proj, _ := filepath.EvalSymlinks(t.TempDir())
	mustWrite(t, filepath.Join(proj, "squad.yaml"), string(raw))
	q, lead, err := squadOfProject(proj)
	if err != nil {
		t.Fatal(err)
	}
	q.Wrapper, q.AMQPath = "/bin/amq-squad", "/bin/amq"
	if q, err = q.Normalize(); err != nil {
		t.Fatal(err)
	}
	roster, _ := (&schedproc.Slots{Squad: q}).Roster(lead)
	if lead != "cto" || len(roster) != len(q.Seats)-1 || len(roster) == 0 {
		t.Fatalf("lead %q, roster %+v of %d seats", lead, roster, len(q.Seats))
	}
	for _, rs := range roster {
		if want := filepath.Join(proj, ".worktrees", rs.Handle); rs.Worktree != want || rs.Worktree == proj {
			t.Fatalf("seat %s worktree %s, want %s", rs.Handle, rs.Worktree, want)
		}
	}
}

// Reviewer hold (1) of brief 31-1: the tool-time deny of a Claude seat's
// write to the owner's global agent config stays (hook_guard.go's
// protected-path deny over grants.Implicit's ~/.claude/settings*.json). The
// write is denied, no quarantine is recorded, and the seat goes on: its next
// call is allowed.
func TestClaudeSeatWriteToTheOwnersConfigIsDeniedWithoutQuarantine(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	root, _ := filepath.EvalSymlinks(t.TempDir())
	wt, _ := filepath.EvalSymlinks(t.TempDir())
	os.MkdirAll(filepath.Join(root, "agents", "s1"), 0o700)
	if _, err := claudeseat.Write(claudeseat.Dir(root, "s1", "launch-1"), "dontAsk", wt, time.Now()); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(launch.StagedDir(root, "s1"), 0o700)
	rec := launch.Record{Handle: "s1", Root: root, Session: "s", Runtime: "claude", Worktree: wt, Branch: "seat/s1", LaunchID: "launch-1", PermissionMode: "dontAsk"}
	if err := launch.WriteFile(filepath.Join(launch.StagedDir(root, "s1"), launch.FileName), rec); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AM_ME", "s1")
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_SESSION", "")
	guard := func(args ...string) guardVerdict {
		t.Helper()
		out, _, err := captureOutput(t, func() error { return runGuard(append([]string{"--me", "s1", "--cwd", wt}, args...)) })
		var v guardVerdict
		if err != nil || json.Unmarshal([]byte(out), &v) != nil {
			t.Fatalf("guard: %v %q", err, out)
		}
		return v
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	if v := guard("--tool", "Write", "--arg", "file_path="+settings, "--arg", "content={}"); v.Verdict != "deny" || v.ReasonCode != "protected_path" {
		t.Fatalf("a write to the owner's %s: %+v", settings, v)
	}
	if v := guard("--tool", "Bash", "--arg", "command=echo x > "+settings); v.Verdict != "deny" || v.ReasonCode != "protected_path" {
		t.Fatalf("a shell write to the owner's config: %+v", v)
	}
	if _, err := os.Stat(filepath.Join(task.DirFor(root), "quarantine")); !os.IsNotExist(err) {
		t.Fatalf("a quarantine was written: %v", err)
	}
	if v := guard("--tool", "Bash", "--arg", "command=go test ./..."); v.Verdict == "deny" {
		t.Fatalf("the seat's next call: %+v", v)
	}
}

// The lead's condition (2) on the quarantine's deletion: an owner grants
// file written before 3.1 that still carries clear.signature is accepted
// and ignored, never refused; doctor's grants row warns with the key named.
func TestAnOldClearSignatureKeyIsIgnoredWithADoctorWarning(t *testing.T) {
	home := t.TempDir()
	h := hostWith(t, home, "amq")
	path := filepath.Join(home, ".amq-squad", "grants.yaml")
	hostWrite(t, path, "schema: 1\nmax_seats: 8\nclear:\n  signature: required\n")
	os.Chmod(filepath.Dir(path), 0o700)
	os.Chmod(path, 0o600)
	data, err := runHostDoctor(t, h, t.TempDir())
	if err != nil {
		t.Fatalf("doctor failed on the old key: %v", err)
	}
	c := findCheck(data.Checks, "grants")
	if c.Status != doctorWarn || !strings.Contains(c.Detail, "clear.signature") || !strings.Contains(c.Detail, "seat cap 8") {
		t.Fatalf("grants row: %+v", c)
	}
}

// The reviewer's change on brief 31-1: a gate record of a kind the catalog no
// longer carries (the owner's signed scribe clear of 2026-10-31 is a
// quarantine_clear record in the live store) is listed as legacy and
// skipped with one line naming the kind and the gate id, never a refusal:
// by `gate status`, by `gate status --gate <id>`, and by status (a warning
// line; it is no gate row).
func TestALegacyGateRecordIsListedAndSkipped(t *testing.T) {
	f := newGateCLIFixture(t)
	if _, _, err := captureOutput(t, func() error {
		return runGate([]string{"raise", "--project", f.project, "--session", "s", "--me", "cto", "--gate", "rel-r", "--action", "tag", "--target", "git tag v3.1.0", "--json"})
	}); err != nil {
		t.Fatalf("gate raise: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(f.root, "extensions", "amq-squad", "gates", "rel-r.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rec map[string]any
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	const legacyID = "clear-0a227ca8d88dc97c65c540a4d0fa18a7"
	rec["gate_id"], rec["class"], rec["typed_class"] = legacyID, "quarantine_clear", "clear"
	rec["target"] = "clear scribe nonce=00 incarnation=t2@1 config=sha256:" + strings.Repeat("0", 64)
	legacy, _ := json.MarshalIndent(rec, "", "  ")
	mustWrite(t, filepath.Join(f.root, "extensions", "amq-squad", "gates", legacyID+".json"), string(legacy))
	line := "gate " + legacyID + " is a legacy quarantine_clear gate (a kind this build no longer carries); skipped"
	out, errOut, err := captureOutput(t, func() error {
		return runGate([]string{"status", "--project", f.project, "--session", "s", "--json"})
	})
	if err != nil || !strings.Contains(out, `"rel-r"`) || strings.Contains(out, legacyID) || !strings.Contains(errOut, line) {
		t.Fatalf("gate status: %v\n%s\n%s", err, out, errOut)
	}
	_, errOut, err = captureOutput(t, func() error {
		return runGate([]string{"status", "--project", f.project, "--session", "s", "--gate", legacyID})
	})
	if err != nil || !strings.Contains(errOut, line) {
		t.Fatalf("gate status --gate: %v\n%s", err, errOut)
	}
	doc := buildStatusContract(f.root, "", "3.1-test", time.Now())
	if js, _ := json.Marshal(doc); strings.Contains(string(js), `"id":"`+legacyID+`"`) || !strings.Contains(string(js), line) {
		t.Fatalf("status:\n%s", js)
	}
}
