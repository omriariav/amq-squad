package buzz

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/nostr"
)

// fakeLeads is a LeadRegistry over a fixed set.
type fakeLeads map[string]bool

func (f fakeLeads) Lead(_, handle string) error {
	if f[handle] {
		return nil
	}
	return fmt.Errorf("%s is not a lead seat", handle)
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
		if err := reg.Lead(root, h); (err == nil) != want {
			t.Errorf("Lead(%s) = %v, want lead %v", h, err, want)
		}
	}
	if err := reg.Lead(root, "worker"); err == nil || !strings.Contains(err.Error(), `"agent" seat, not a lead`) {
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

// 4-5 review item 4: a mark that cannot be written (here a read-only marks
// directory; ENOSPC behaves the same) is not a duplicate: the step fails,
// the DM is not dropped, and the next step handles it.
func TestPumpMarkFailureKeepsTheDM(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory modes")
	}
	f, p, m, _ := pumpFixture(t)
	id := f.ownerDM(t, "hello lead", "")
	if err := os.MkdirAll(p.Marks, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(p.Marks, 0o500)
	got, err := p.Step(context.Background())
	_ = os.Chmod(p.Marks, 0o700)
	if err == nil || len(got) != 0 || len(m.sent) != 0 {
		t.Fatalf("unwritable marks: handled %+v, sent %d, err %v", got, len(m.sent), err)
	}
	got, err = p.Step(context.Background())
	if err != nil || len(got) != 1 || got[0].EventID != id || len(m.sent) != 1 {
		t.Fatalf("after the marks recover: %+v, sent %d, %v", got, len(m.sent), err)
	}
}

// 4-5 review item 5: a DM dated a day ahead is quarantined once and never
// moves the cursor; the messages after it are still read.
func TestPumpQuarantinesAFutureDatedDM(t *testing.T) {
	f, p, m, relay := pumpFixture(t)
	ctx := context.Background()
	future := nostr.Event{CreatedAt: time.Now().Add(24 * time.Hour).Unix(), Kind: 9, Tags: [][]string{{"h", f.core.Pin.Channel}}, Content: "from a fast phone"}
	_ = f.owner.sk.Sign(&future)
	_ = relay.Publish(ctx, future.Marshal())
	got, err := p.Step(ctx)
	if err != nil || len(got) != 1 || got[0].Kind != "quarantined" || len(m.sent) != 0 {
		t.Fatalf("future DM: %+v, %v", got, err)
	}
	if got, _ := p.Step(ctx); len(got) != 0 {
		t.Fatalf("the quarantine was reported twice: %+v", got)
	}
	later := f.ownerDM(t, "now a normal one", "")
	got, err = p.Step(ctx)
	if err != nil || len(got) != 1 || got[0].EventID != later || got[0].Kind != "route" {
		t.Fatalf("the DM after a future-dated one: %+v, %v", got, err)
	}
}
