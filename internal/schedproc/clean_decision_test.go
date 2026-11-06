package schedproc

import (
	"bytes"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/panechain"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

type cleanDecisionLiveness struct {
	gone bool
	err  error
}

func (l cleanDecisionLiveness) SeatProcess(string, string) (bool, string, error) {
	return l.gone, "process exited", l.err
}

type cleanDecisionPreparer struct{ calls []string }

func (*cleanDecisionPreparer) PrepareSeat(string) error { return nil }
func (p *cleanDecisionPreparer) CleanStopped(handle string) (bool, error) {
	p.calls = append(p.calls, handle)
	return true, nil
}

// R4's refusals (item 3): a seat with a lease is refused with the task
// named, a seat whose process runs is refused, a seat whose liveness
// cannot be read is refused; only a proved stopped seat is cleaned.
func TestManualCleanRequiresAProvedStoppedSeat(t *testing.T) {
	store, err := task.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := &cleanDecisionPreparer{}
	tk, err := store.Add(task.AddInput{Title: "held", Class: "script", Pool: &sonnet}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Grant(tk.ID, "dev", &sonnet, time.Hour, time.Now()); err != nil {
		t.Fatal(err)
	}
	held := manualCleanDecision(store, cleanDecisionLiveness{gone: true}, p)("lead", CleanRequest{Seat: "dev"})
	if held.OK || held.Code != "seat_running" || !strings.Contains(held.Detail, "seat dev is still running (it holds "+tk.ID+"); stop it first") || len(p.calls) != 0 {
		t.Fatalf("held-lease clean: %+v calls=%v", held, p.calls)
	}
	if _, err := store.Release(tk.ID, "dev", "done with it", time.Now()); err != nil {
		t.Fatal(err)
	}
	live := manualCleanDecision(store, cleanDecisionLiveness{}, p)("lead", CleanRequest{Seat: "dev"})
	if live.OK || live.Code != "seat_running" || !strings.Contains(live.Detail, "seat dev is still running; stop it first") || len(p.calls) != 0 {
		t.Fatalf("live seat clean: %+v calls=%v", live, p.calls)
	}
	unknown := manualCleanDecision(store, cleanDecisionLiveness{err: errors.New("proof unreadable")}, p)("lead", CleanRequest{Seat: "dev"})
	if unknown.OK || unknown.Code != "seat_liveness_unknown" || len(p.calls) != 0 {
		t.Fatalf("unknown seat clean: %+v calls=%v", unknown, p.calls)
	}
	stopped := manualCleanDecision(store, cleanDecisionLiveness{gone: true}, p)("lead", CleanRequest{Seat: "dev"})
	if !stopped.OK || len(p.calls) != 1 || p.calls[0] != "dev" {
		t.Fatalf("stopped seat clean: %+v calls=%v", stopped, p.calls)
	}
}

func TestSeatCleanSocketRefusesASeatProcessAndAcceptsTheLeadPane(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	lead := panechain.Pane{Shell: 80, Start: "lead-start", Agent: "lead"}
	fromLead := false
	cleaned := 0
	cleanRequest := ""
	s, err := ServeDecisions(DecideOptions{
		Root:  root,
		Lead:  "lead",
		Apply: func(string, WidenRequest) DecideReply { return DecideReply{OK: true} },
		Clean: func(gotLead string, req CleanRequest) DecideReply {
			cleaned++
			cleanRequest = gotLead + "/" + req.Seat
			return DecideReply{OK: true}
		},
		LeadPane: func(string, string) (panechain.Pane, error) { return lead, nil },
		Reaches: func(_ int, _ string, _ []panechain.Pane) (*panechain.Pane, bool) {
			if !fromLead {
				return nil, true
			}
			return &lead, true
		},
		StartToken: func(int) (string, bool) { return "sender-start", true },
		Peer:       func(*net.UnixConn) (int, bool) { return 4242, true },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	seatReply, err := SendClean(s.Root(), s.Path(), CleanRequest{Seat: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if seatReply.Code != RefusedNotLead || cleaned != 0 {
		t.Fatalf("seat-process clean = %+v, calls=%d", seatReply, cleaned)
	}
	fromLead = true
	leadReply, err := SendClean(s.Root(), s.Path(), CleanRequest{Seat: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if !leadReply.OK || cleaned != 1 || cleanRequest != "lead/dev" {
		t.Fatalf("lead clean = %+v, calls=%d request=%q", leadReply, cleaned, cleanRequest)
	}
}

func TestDeadCleanerMarkerHelper(t *testing.T) {
	root := os.Getenv("AMQ_TEST_DEAD_CLEANER_ROOT")
	if root == "" {
		return
	}
	store, err := task.Open(root)
	if err != nil {
		os.Exit(2)
	}
	if _, _, err := store.ReserveSeat(os.Getenv("AMQ_TEST_DEAD_CLEANER_SEAT"), time.Now()); err != nil {
		os.Exit(3)
	}
}

func TestNextTickTakesOverADeadCleanerMarkerAndLogsItOnce(t *testing.T) {
	q := squadFixture(t)
	slots := &Slots{Squad: q}
	planned, err := slots.Plan(scheduler.SeatRequest{ID: "setup", Pool: sonnet})
	if err != nil {
		t.Fatal(err)
	}
	seat := planned.Seats[0]
	root := q.SessionRoot()
	now := time.Now().UTC()
	if _, err := seatcommit.RecordWorktreeBase("git", root, seat.Handle, seat.Worktree, seat.Branch, seatcommit.Target{BaseBranch: "main"}, now); err != nil {
		t.Fatal(err)
	}
	store, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Add(task.AddInput{Title: "after dead cleaner", Class: "script", Pool: &sonnet}, now); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestDeadCleanerMarkerHelper$")
	child.Env = append(os.Environ(), "AMQ_TEST_DEAD_CLEANER_ROOT="+root, "AMQ_TEST_DEAD_CLEANER_SEAT="+seat.Handle)
	if out, err := child.CombinedOutput(); err != nil {
		t.Fatalf("dead cleaner helper: %v\n%s", err, out)
	}
	var decisions bytes.Buffer
	cleaner := &CleanSeats{Root: root, Git: "git"}
	cleaner.SetLog(&decisions, func() time.Time { return now.Add(time.Second) })
	launcher := &fakeLauncher{}
	s, err := scheduler.New(scheduler.Options{Store: store, Now: func() time.Time { return now.Add(time.Second) }, Launcher: launcher, Preparer: cleaner, Config: scheduler.Config{IdleTimeout: time.Hour}, Incarnation: "deadcleaner"})
	if err != nil {
		t.Fatal(err)
	}
	launcher.s = s
	s.AdoptUp(seat.Handle, sonnet, now)
	if _, err := s.Tick(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(decisions.String(), `"kind":"cleaner_marker_taken_over"`); got != 1 {
		t.Fatalf("takeover decisions after first tick = %d\n%s", got, decisions.String())
	}
	if _, err := s.Tick(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(decisions.String(), `"kind":"cleaner_marker_taken_over"`); got != 1 {
		t.Fatalf("takeover decisions after second tick = %d\n%s", got, decisions.String())
	}
}

// socketCleanFixture is a real seat worktree with its ledger and staged
// launch record, a task claimed by the seat and cancelled, the seat's
// CleanSeats logging to decisions, and the decision socket serving the
// lead's clean over it (the sender proved as the lead).
type socketCleanFixture struct {
	root, handle, worktree string
	task                   task.Task
	store                  task.Store
	decisions              *bytes.Buffer
	server                 *DecisionServer
}

func newSocketCleanFixture(t *testing.T) socketCleanFixture {
	t.Helper()
	q := squadFixture(t)
	slots := &Slots{Squad: q}
	planned, err := slots.Plan(scheduler.SeatRequest{ID: "setup", Pool: sonnet})
	if err != nil {
		t.Fatal(err)
	}
	seat := planned.Seats[0]
	root := q.SessionRoot()
	now := time.Now().UTC()
	if _, err := seatcommit.RecordWorktreeBase("git", root, seat.Handle, seat.Worktree, seat.Branch, seatcommit.Target{BaseBranch: "main"}, now); err != nil {
		t.Fatal(err)
	}
	staged := launch.StagedDir(root, seat.Handle)
	if err := os.MkdirAll(staged, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := launch.WriteFile(filepath.Join(staged, launch.FileName), launch.Record{Handle: seat.Handle, Root: root, Session: q.Session, Runtime: "claude", Worktree: seat.Worktree, Branch: seat.Branch, Base: seat.Base}); err != nil {
		t.Fatal(err)
	}
	store, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	tk, err := store.Add(task.AddInput{Title: "cancelled", Class: "script", Pool: &sonnet}, now)
	if err != nil {
		t.Fatal(err)
	}
	if tk, err = store.Claim(tk.ID, seat.Handle, &sonnet, time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if err := seatcommit.StartTask(root, seat.Handle, "git", nil, tk, "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Cancel(tk.ID, "lead", "stop", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	decisions := &bytes.Buffer{}
	cleaner := &CleanSeats{Root: root, Git: "git"}
	cleaner.SetLog(decisions, func() time.Time { return now.Add(2 * time.Second) })
	lead := panechain.Pane{Shell: 80, Start: "lead-start", Agent: "lead"}
	s, err := ServeDecisions(DecideOptions{
		Root:       root,
		Lead:       "lead",
		Apply:      func(string, WidenRequest) DecideReply { return DecideReply{OK: true} },
		Clean:      manualCleanDecision(store, cleanDecisionLiveness{gone: true}, cleaner),
		LeadPane:   func(string, string) (panechain.Pane, error) { return lead, nil },
		Reaches:    func(int, string, []panechain.Pane) (*panechain.Pane, bool) { return &lead, true },
		StartToken: func(int) (string, bool) { return "sender-start", true },
		Peer:       func(*net.UnixConn) (int, bool) { return 4242, true },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return socketCleanFixture{root: root, handle: seat.Handle, worktree: seat.Worktree, task: tk, store: store, decisions: decisions, server: s}
}

// R4 through the socket, for real (Codex's E on #898): the lead's `seat
// clean` of a stopped seat with a cancelled attempt and an edit in its
// worktree (on the cancelled task's branch), over the decision socket,
// the preparer being schedproc's own CleanSeats on a real worktree: the
// edit is on the ledger as a discard named by the cancelled task and
// attempt, the worktree is clean and back on the seat branch, the reply
// says so.
func TestSeatCleanThroughTheSocketArchivesAndCleansAStoppedSeat(t *testing.T) {
	f := newSocketCleanFixture(t)
	edit := filepath.Join(f.worktree, "a.txt")
	if err := os.WriteFile(edit, []byte("the seat's edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reply, err := SendClean(f.server.Root(), f.server.Path(), CleanRequest{Seat: f.handle})
	if err != nil {
		t.Fatal(err)
	}
	if !reply.OK || reply.Detail != "seat "+f.handle+"'s worktree was cleaned" || !strings.Contains(string(reply.Data), `"discarded":true`) {
		t.Fatalf("the lead's clean: %+v", reply)
	}
	if raw, _ := os.ReadFile(edit); string(raw) != "a\n" {
		t.Fatalf("the worktree was not cleaned: %q", raw)
	}
	if got := gitRun(t, f.worktree, "status", "--porcelain"); got != "" {
		t.Fatalf("the worktree is not clean: %s", got)
	}
	if got := gitRun(t, f.worktree, "symbolic-ref", "--short", "HEAD"); got != "seat/"+f.handle {
		t.Fatalf("the worktree is on %s, not the seat branch", got)
	}
	ledger, err := seatcommit.ReadLedger(f.root, f.handle)
	if err != nil || len(ledger.Discarded) != 1 || ledger.Discarded[0].TaskID != f.task.ID || ledger.Discarded[0].Attempt != 1 || ledger.Discarded[0].Reason != task.OutcomeCancelled {
		t.Fatalf("the discard named by the cancelled attempt: %+v, %v", ledger.Discarded, err)
	}
	if got := gitRun(t, f.worktree, "cat-file", "-p", ledger.Discarded[0].Tree+":a.txt"); got != "the seat's edit" {
		t.Fatalf("the archived edit: %q", got)
	}
}

// R4 and item 5: `seat clean` archives before it moves. A cancelled task
// branch with an unrecorded commit and an edit on top: the move is
// refused (seat_head_detached_unmerged, the code below the rule, HEAD and
// the file untouched), and the edit is already on the ledger, named by
// the cancelled attempt. Reverted (the archive under the latest attempt
// before the clean), a refused move leaves nothing on the ledger.
func TestSeatCleanArchivesTheEditBeforeARefusedMove(t *testing.T) {
	f := newSocketCleanFixture(t)
	if err := os.WriteFile(filepath.Join(f.worktree, "a.txt"), []byte("committed by the seat\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, f.worktree, "add", "-A")
	gitRun(t, f.worktree, "-c", "user.name=seat", "-c", "user.email=seat@example.invalid", "commit", "-q", "-m", "an unrecorded commit")
	head := gitRun(t, f.worktree, "rev-parse", "HEAD")
	edit := filepath.Join(f.worktree, "a.txt")
	if err := os.WriteFile(edit, []byte("the seat's edit on top\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reply, err := SendClean(f.server.Root(), f.server.Path(), CleanRequest{Seat: f.handle})
	if err != nil {
		t.Fatal(err)
	}
	if reply.OK || reply.Code != "seat_clean_failed" || !strings.Contains(reply.Detail, seatcommit.RefusalSeatHeadDetachedUnmerged) {
		t.Fatalf("the refused move: %+v", reply)
	}
	if got := gitRun(t, f.worktree, "rev-parse", "HEAD"); got != head {
		t.Fatalf("the refused clean moved HEAD from %s to %s", head, got)
	}
	if raw, _ := os.ReadFile(edit); string(raw) != "the seat's edit on top\n" {
		t.Fatalf("the refused clean touched the edit: %q", raw)
	}
	ledger, err := seatcommit.ReadLedger(f.root, f.handle)
	if err != nil || len(ledger.Discarded) != 1 || ledger.Discarded[0].TaskID != f.task.ID || ledger.Discarded[0].Attempt != 1 {
		t.Fatalf("the edit was not archived before the refused move: %+v, %v", ledger.Discarded, err)
	}
	if got := gitRun(t, f.worktree, "cat-file", "-p", ledger.Discarded[0].Tree+":a.txt"); got != "the seat's edit on top" {
		t.Fatalf("the archived edit: %q", got)
	}
}
