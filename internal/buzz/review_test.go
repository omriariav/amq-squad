package buzz

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeLeads is a LeadRegistry over a fixed set.
type fakeLeads map[string]bool

func (f fakeLeads) Lead(root, handle string) (Destination, error) {
	if f[handle] {
		return Destination{Root: root, Handle: handle}, nil
	}
	return Destination{}, fmt.Errorf("%s is not a lead seat", handle)
}

// writeLaunchRecord writes the launch record a launch leaves for a seat.
func writeLaunchRecord(t *testing.T, root, handle, role string) {
	t.Helper()
	dir := filepath.Join(root, "agents", handle, "extensions", "io.github.omriariav.amq-squad")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	rec := fmt.Sprintf(`{"schema":1,"handle":%q,"role":%q,"binary":"codex","cwd":"/repo","agent_pid":0,"started_at":"2026-10-24T11:00:00Z","root":%q}`, handle, role, root)
	if err := os.WriteFile(filepath.Join(dir, "launch.json"), []byte(rec), 0o600); err != nil {
		t.Fatal(err)
	}
}

// 4-5 review item 3: a handle given to the attachment as a lead is routed to
// only when the authoritative seat roles say it leads; an implementer named
// in --leads gets the mailbox copy and no forward.
func TestRoutingOnlyToLeadSeats(t *testing.T) {
	root := realTempDir(t)
	writeLaunchRecord(t, root, "lead-a", "cto")
	writeLaunchRecord(t, root, "lead-b", "lead")
	writeLaunchRecord(t, root, "worker", "agent")
	reg := LaunchRecordLeads{Roles: DefaultLeadRoles}
	for h, want := range map[string]bool{"lead-a": true, "lead-b": true, "worker": false, "ghost": false} {
		if _, err := reg.Lead(root, h); (err == nil) != want {
			t.Errorf("Lead(%s) = %v, want lead %v", h, err, want)
		}
	}
	if _, err := reg.Lead(root, "worker"); err == nil || !strings.Contains(err.Error(), `"agent" seat, not a lead`) {
		t.Errorf("worker reason = %v", err)
	}
	m := &fakeMailer{}
	tg := Target{Root: root, Handle: "lead-a", Leads: []string{"lead-a", "lead-b", "worker"}, Registry: reg}
	d, err := Deliver(context.Background(), m, tg, strings.Repeat("cd", 32), "@worker run the migration")
	if err != nil || d.Lead != "" || d.Refused == "" || len(m.sent) != 1 || m.sent[0].To != "lead-a" {
		t.Fatalf("@worker = %+v, sent %+v, %v", d, m.sent, err)
	}
	d, err = Deliver(context.Background(), m, tg, strings.Repeat("ce", 32), "@lead-b ship it")
	if err != nil || d.Lead != "lead-b" || d.Refused != "" || len(m.sent) != 3 {
		t.Fatalf("@lead-b = %+v, sent %d, %v", d, len(m.sent), err)
	}
	// no registry, no forward
	tg.Registry = nil
	if d, _ := Deliver(context.Background(), m, tg, strings.Repeat("cf", 32), "@lead-b again"); d.Lead != "" || d.Refused == "" {
		t.Fatalf("no registry = %+v", d)
	}
}
