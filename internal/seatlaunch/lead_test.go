package seatlaunch

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/avivsinai/agent-message-queue/launchapi"
	"github.com/omriariav/amq-squad/v3/internal/flock"
	"github.com/omriariav/amq-squad/v3/internal/launch"
)

// 3.3.1 (Codex pass 1 on the lead's staging): the lead's staging (StageLead,
// which removes role.md) and a seat's (Stage, which writes it) change the
// record and its role under the one record lock: while another writer holds
// it, neither touches role.md or the record.
func TestStageLeadAndStageHoldTheRecordLockForTheRoleToo(t *testing.T) {
	project := t.TempDir()
	q := Squad{ProjectRoot: project, BaseRoot: filepath.Join(project, ".agent-mail"), Session: "s1",
		Seats: []Seat{{Handle: "cto", Runtime: "pi", RoleName: "cto", Role: "role text"}}}
	root := q.SessionRoot()
	dir := launch.StagedDir(root, "cto")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	rolePath := filepath.Join(dir, launch.RoleFileName)
	if err := os.WriteFile(rolePath, []byte("seat role\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(dir, launch.FileName) + ".lock"
	hold := func(during func()) {
		t.Helper()
		release, held := make(chan struct{}), make(chan struct{})
		go func() {
			_ = flock.WithLock(lockPath, func() error {
				close(held)
				<-release
				return nil
			})
		}()
		<-held
		during()
		close(release)
	}
	done := make(chan error, 1)
	hold(func() {
		go func() {
			_, err := StageLead(q, "cto", os.Getpid(), time.Now(), false)
			done <- err
		}()
		time.Sleep(200 * time.Millisecond)
		if _, err := os.Stat(rolePath); err != nil {
			t.Errorf("StageLead removed role.md while another writer held the record lock: %v", err)
		}
	})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(rolePath); !os.IsNotExist(err) {
		t.Errorf("StageLead left role.md: %v", err)
	}
	hold(func() {
		go func() {
			done <- Stage(Compiled{Records: []launch.Record{{Handle: "cto", Root: root}}, Roles: [][]byte{[]byte("seat role\n")}})
		}()
		time.Sleep(200 * time.Millisecond)
		if _, err := os.Stat(rolePath); !os.IsNotExist(err) {
			t.Errorf("Stage wrote role.md while another writer held the record lock: %v", err)
		}
	})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(rolePath); err != nil || string(b) != "seat role\n" {
		t.Errorf("Stage's role.md %q %v", b, err)
	}
}

// 3.3.1 (Codex pass 2 on the lead's staging): an AMQ apply that returns
// after a lead's start restaged the handle does not attach itself to the
// lead's record (recordApply writes only its own launch's record, as the
// proof server's markToken does): the lead's record stays LeadOnly.
func TestALateApplyDoesNotJoinTheLeadsRecord(t *testing.T) {
	project := t.TempDir()
	q := Squad{ProjectRoot: project, BaseRoot: filepath.Join(project, ".agent-mail"), Session: "s1",
		Seats: []Seat{{Handle: "cto", Runtime: "pi", RoleName: "cto", Role: "role text"}}}
	root := q.SessionRoot()
	seat := Compiled{Records: []launch.Record{{Handle: "cto", Root: root, LaunchID: "L1", Proof: &launch.ProofTicket{Token: "t"}}}, Roles: [][]byte{[]byte("seat role\n")}}
	if err := Stage(seat); err != nil {
		t.Fatal(err)
	}
	lead, err := StageLead(q, "cto", os.Getpid(), time.Now(), false)
	if err != nil {
		t.Fatal(err)
	}
	recordApply(seat, launchapi.ApplyResultV1{Outcome: "applied", BindingGeneration: "g1"}, "", time.Now())
	rec, err := launch.ReadStaged(root, "cto")
	if err != nil || rec.LaunchID != lead.LaunchID || rec.Apply != nil || rec.LaunchNonce != "" || !rec.LeadOnly() {
		t.Errorf("a late apply joined the lead's record: %+v %v", rec, err)
	}
	// the launch's own record still gets its apply
	if err := Stage(seat); err != nil {
		t.Fatal(err)
	}
	recordApply(seat, launchapi.ApplyResultV1{Outcome: "applied", BindingGeneration: "g1"}, "", time.Now())
	if rec, err := launch.ReadStaged(root, "cto"); err != nil || rec.Apply == nil || rec.LaunchNonce != "g1" {
		t.Errorf("the launch's own record lost its apply: %+v %v", rec, err)
	}
}

// 3.3.1 (Codex pass 3 on the lead's staging): an undo takes back only its
// own start: a record a later start staged (another launch id) is kept; a
// launched lead's record the start marked is unmarked, not removed.
func TestUnstageLeadTakesBackOnlyItsOwnStart(t *testing.T) {
	project := t.TempDir()
	q := Squad{ProjectRoot: project, BaseRoot: filepath.Join(project, ".agent-mail"), Session: "s1",
		Seats: []Seat{{Handle: "cto", Runtime: "pi", RoleName: "cto", Role: "role text"}}}
	root := q.SessionRoot()
	first, err := StageLead(q, "cto", os.Getpid(), time.Now(), false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := StageLead(q, "cto", os.Getpid(), time.Now(), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := UnstageLead(root, "cto", first.LaunchID); err != nil {
		t.Fatal(err)
	}
	if rec, err := launch.ReadStaged(root, "cto"); err != nil || rec.LaunchID != second.LaunchID || rec.SquadProject != project {
		t.Errorf("an earlier start's undo took a later start's record: %+v %v", rec, err)
	}
	if err := UnstageLead(root, "cto", second.LaunchID); err != nil {
		t.Fatal(err)
	}
	if rec, err := launch.ReadStaged(root, "cto"); !os.IsNotExist(err) {
		t.Errorf("the start's own lead-only record is kept: %+v %v", rec, err)
	}
	// a launched lead's record, marked by the start
	if err := Stage(Compiled{Records: []launch.Record{{Handle: "cto", Root: root, LaunchID: "L-amq", Proof: &launch.ProofTicket{Token: "t"}}}, Roles: [][]byte{[]byte("r\n")}}); err != nil {
		t.Fatal(err)
	}
	marked, err := StageLead(q, "cto", os.Getpid(), time.Now(), true)
	if err != nil || marked.LaunchID != "L-amq" || marked.SquadProject != project || marked.SquadLead {
		t.Fatalf("marked (pending) %+v %v", marked, err)
	}
	if err := PromoteLead(root, "cto", marked.LaunchID); err != nil {
		t.Fatal(err)
	}
	if err := UnstageLead(root, "cto", marked.LaunchID); err != nil {
		t.Fatal(err)
	}
	if rec, err := launch.ReadStaged(root, "cto"); err != nil || rec.LaunchID != "L-amq" || rec.SquadLead || rec.SquadProject != "" {
		t.Errorf("the launched lead's record after the undo %+v %v", rec, err)
	}
}

// 3.3.1 round 2 (Codex r2 pass 1): a lead start's record is pending (no
// lead authority) until the start promotes it; a promotion is its own
// start's only; a roster launch's records (Compile, Stage) never carry the
// lead's marks, whatever the seat's role id.
func TestALeadsRecordIsPendingUntilItsStartPromotesIt(t *testing.T) {
	project := t.TempDir()
	q := Squad{ProjectRoot: project, BaseRoot: filepath.Join(project, ".agent-mail"), Session: "s1",
		Seats: []Seat{{Handle: "cto", Runtime: "pi", RoleName: "cto", Role: "role text"}}}
	root := q.SessionRoot()
	first, err := StageLead(q, "cto", os.Getpid(), time.Now(), false)
	if err != nil {
		t.Fatal(err)
	}
	if rec, _ := launch.ReadStaged(root, "cto"); rec.SquadLead || rec.HasLeadAuthority("cto") || !rec.LeadOnly() {
		t.Errorf("a pending lead record has lead authority: %+v", rec)
	}
	second, err := StageLead(q, "cto", os.Getpid(), time.Now(), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := PromoteLead(root, "cto", first.LaunchID); err == nil {
		t.Error("an earlier start promoted a later start's record")
	}
	if err := PromoteLead(root, "cto", second.LaunchID); err != nil {
		t.Fatal(err)
	}
	if rec, _ := launch.ReadStaged(root, "cto"); !rec.HasLeadAuthority("cto") {
		t.Errorf("the promoted record %+v", rec)
	}
	for _, role := range []string{"lead", "cto"} {
		rq := Squad{ProjectRoot: project, BaseRoot: filepath.Join(project, ".agent-mail"), Session: "s1", AMQPath: "/usr/bin/amq", Wrapper: "/usr/bin/amq-squad", Operator: "user", Launcher: "tmux",
			Seats: []Seat{{Handle: "dev", Runtime: "codex", RoleName: role, Role: "role text", Worktree: filepath.Join(project, ".worktrees", "dev")}}}
		c, err := Compile(rq, "", func(string) (string, error) { return "/usr/bin/codex", nil }, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err := Stage(c); err != nil {
			t.Fatal(err)
		}
		if rec, err := launch.ReadStaged(root, "dev"); err != nil || rec.SquadLead || rec.SquadProject != "" || rec.HasLeadAuthority("dev") {
			t.Errorf("a roster seat whose role is %q: %+v %v", role, rec, err)
		}
	}
}
