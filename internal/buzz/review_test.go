package buzz

import (
	"context"
	"fmt"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
	"github.com/omriariav/amq-squad/v3/internal/team"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeLeads is a LeadRegistry over a fixed set.
type fakeLeads map[string]bool

func (f fakeLeads) Lead(root, handle string) (Destination, error) {
	if f[handle] {
		return Destination{Root: root, Handle: handle}, nil
	}
	return Destination{}, fmt.Errorf("%s is not a lead seat", handle)
}

// writeLaunchRecord stages a roster seat's record through the real
// producer (seatlaunch.Compile and Stage, as a roster launch does): the
// record the core stages for a seat's launch (launch.StagedDir), the one
// lead routing reads (C5a), which never carries lead authority (3.3.1).
func writeLaunchRecord(t *testing.T, root, handle, role string) {
	t.Helper()
	project := realTempDir(t)
	q := seatlaunch.Squad{ProjectRoot: project, BaseRoot: filepath.Dir(root), Session: filepath.Base(root), Operator: "user", Launcher: "tmux",
		Wrapper: "/usr/bin/amq-squad", AMQPath: "/usr/bin/amq",
		Seats: []seatlaunch.Seat{{Handle: handle, Runtime: "codex", RoleName: role, Role: "role text", Worktree: filepath.Join(project, ".worktrees", handle)}}}
	c, err := seatlaunch.Compile(q, "", func(string) (string, error) { return "/usr/bin/codex", nil }, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := seatlaunch.Stage(c); err != nil {
		t.Fatal(err)
	}
}

// writeLeadRecord makes handle root's squad lead through the real producer
// (seatlaunch.StageLead, then PromoteLead: what a lead's start does), from
// project ("": a new repository whose squad.yaml names handle its lead).
func writeLeadRecord(t *testing.T, root, handle, project string) string {
	t.Helper()
	if project == "" {
		project = realTempDir(t)
		writeFile(t, filepath.Join(project, "squad.yaml"), "schema: 1\nsession: "+filepath.Base(root)+"\nlead: "+handle+"\nseats:\n  - handle: "+handle+"\n    role: cto\n    runtime: pi\n")
	}
	q := seatlaunch.Squad{ProjectRoot: project, BaseRoot: filepath.Dir(root), Session: filepath.Base(root),
		Seats: []seatlaunch.Seat{{Handle: handle, Runtime: "pi", RoleName: "cto"}}}
	rec, err := seatlaunch.StageLead(q, handle, os.Getpid(), time.Now(), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := seatlaunch.PromoteLead(root, handle, rec.LaunchID); err != nil {
		t.Fatal(err)
	}
	return project
}

// squadYAMLLead is the current lead the CLI wires (squad.yaml's lead).
func squadYAMLLead(project string) (string, error) {
	s, err := team.LoadSquad(project)
	if err != nil {
		return "", err
	}
	if s.Lead == "" {
		return "", fmt.Errorf("%s names no lead", project)
	}
	return s.Lead, nil
}

// 4-5 review item 3: a handle given to the attachment as a lead is routed to
// only when the authoritative seat roles say it leads; an implementer named
// in --leads gets the mailbox copy and no forward.
func TestRoutingOnlyToLeadSeats(t *testing.T) {
	root := realTempDir(t)
	// 3.3.1: lead-a is the squad's lead (its start's record, squad.yaml's
	// lead); lead-b's role id "lead" alone grants nothing
	writeLeadRecord(t, root, "lead-a", "")
	writeLaunchRecord(t, root, "lead-b", "lead")
	writeLaunchRecord(t, root, "worker", "agent")
	reg := LaunchRecordLeads{Current: squadYAMLLead}
	for h, want := range map[string]bool{"lead-a": true, "lead-b": false, "worker": false, "ghost": false} {
		if _, err := reg.Lead(root, h); (err == nil) != want {
			t.Errorf("Lead(%s) = %v, want lead %v", h, err, want)
		}
	}
	if _, err := reg.Lead(root, "worker"); err == nil || !strings.Contains(err.Error(), `"agent" seat, not the squad's lead`) {
		t.Errorf("worker reason = %v", err)
	}
	m := &fakeMailer{}
	tg := Target{Root: root, Handle: "lead-a", Leads: []string{"lead-a", "lead-b", "worker"}, Registry: reg}
	d, err := Deliver(context.Background(), m, tg, strings.Repeat("cd", 32), "@worker run the migration")
	if err != nil || d.Lead != "" || d.Refused == "" || len(m.sent) != 1 || m.sent[0].To != "lead-a" {
		t.Fatalf("@worker = %+v, sent %+v, %v", d, m.sent, err)
	}
	d, err = Deliver(context.Background(), m, tg, strings.Repeat("ce", 32), "@lead-b ship it")
	if err != nil || d.Lead != "" || d.Refused == "" || len(m.sent) != 2 {
		t.Fatalf("@lead-b (a role id alone) = %+v, sent %d, %v", d, len(m.sent), err)
	}
	// no registry, no forward
	tg.Registry = nil
	if d, _ := Deliver(context.Background(), m, tg, strings.Repeat("cf", 32), "@lead-b again"); d.Lead != "" || d.Refused == "" {
		t.Fatalf("no registry = %+v", d)
	}
}
