package schedproc

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

type okExits struct{}

func (okExits) CheckExit(string, string, string) scheduler.ExitResult {
	return scheduler.ExitResult{OK: true}
}

// The plan reviewer's identity rule on #909: a cancelled attempt's clean
// runs only once the seat's launch identity (the accepted proof's pid and
// start stamp) is seen gone. A restart adopts the seat up, closes it (the
// launcher says the close is confirmed), and then reads the identity
// through the launch ledger's own Liveness: with no proof, or with a pid
// that is alive but whose start stamp cannot be read, the identity is
// unknown and nothing is cleaned. The worktree's edit stays, no discard is
// written, the attempt stays uncleaned, and the lead is told once
// (seat_may_be_writing) over every tick; so is an identity still live
// after the launcher reported its close confirmed. The control: a proof whose pid now
// carries another start stamp (reused) is gone, and the clean runs.
// Reverted (unknown read as gone), the unknown cases are cleaned.
func TestCancelledSeatWithNoReadableIdentityIsNeverCleaned(t *testing.T) {
	for _, tc := range []struct {
		name  string
		proof bool
		token func(int) (string, bool)
		alive func(int) bool
		gone  bool
	}{
		{name: "no proof", gone: false},
		{name: "unreadable start stamp", proof: true, token: func(int) (string, bool) { return "", false }, alive: func(int) bool { return true }, gone: false},
		{name: "still live after the launcher's close", proof: true, token: func(int) (string, bool) { return "seat-start", true }, alive: func(int) bool { return true }, gone: false},
		{name: "control: the pid reused", proof: true, token: func(int) (string, bool) { return "another-start", true }, alive: func(int) bool { return true }, gone: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.token != nil {
				prevToken, prevAlive := startTokenOf, aliveOf
				startTokenOf, aliveOf = tc.token, tc.alive
				t.Cleanup(func() { startTokenOf, aliveOf = prevToken, prevAlive })
			}
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
			if tc.proof {
				if err := launch.WriteProof(root, launch.ProofEntry{Handle: seat.Handle, LaunchID: "launch-1", PID: 4242, Start: "seat-start", At: now}); err != nil {
					t.Fatal(err)
				}
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
			edit := filepath.Join(seat.Worktree, "a.txt")
			if err := os.WriteFile(edit, []byte("the seat may still be writing this\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Cancel(tk.ID, "lead", "stop", now.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			cleaner := &CleanSeats{Root: root, Git: "git"}
			launcher := &fakeLauncher{}
			s, err := scheduler.New(scheduler.Options{Store: store, Now: func() time.Time { return now.Add(2 * time.Second) }, Launcher: launcher, Exits: okExits{}, Preparer: cleaner, Liveness: &Liveness{Root: root}, Config: scheduler.Config{IdleTimeout: time.Hour}, Incarnation: "identity"})
			if err != nil {
				t.Fatal(err)
			}
			launcher.s = s
			s.AdoptUp(seat.Handle, sonnet, now.Add(2*time.Second))
			stops, cleaned := 0, 0
			for i := 0; i < 4; i++ {
				ds, err := s.Tick()
				if err != nil {
					t.Fatal(err)
				}
				for _, d := range ds {
					switch d.Kind {
					case "seat_may_be_writing":
						stops++
						if d.ReasonCode != scheduler.ReasonSeatIdentityUnproven || d.Seat != seat.Handle || d.TaskID != tk.ID {
							t.Fatalf("the stop's record: %+v", d)
						}
					case "seat_cleaned":
						cleaned++
					}
				}
			}
			if len(launcher.closed) != 1 {
				t.Fatalf("the adopted seat was not closed first: closed=%v", launcher.closed)
			}
			ledger, err := seatcommit.ReadLedger(root, seat.Handle)
			if err != nil {
				t.Fatal(err)
			}
			got, err := store.Get(tk.ID)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := os.ReadFile(edit)
			if tc.gone {
				if cleaned != 1 || stops != 0 || len(ledger.Discarded) != 1 || got.Attempts[0].CleanedAt == nil {
					t.Fatalf("a gone identity was not cleaned: cleaned=%d stops=%d discards=%d attempt=%+v", cleaned, stops, len(ledger.Discarded), got.Attempts[0])
				}
				return
			}
			if cleaned != 0 || len(ledger.Discarded) != 0 || string(raw) != "the seat may still be writing this\n" || got.Attempts[0].CleanedAt != nil {
				t.Fatalf("an unknown identity was cleaned: cleaned=%d discards=%+v edit=%q attempt=%+v", cleaned, ledger.Discarded, raw, got.Attempts[0])
			}
			if stops != 1 {
				t.Fatalf("the lead stop was said %d times over four ticks", stops)
			}
		})
	}
}
