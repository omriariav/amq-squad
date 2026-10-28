package buzz

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/flock"
	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/mergetrain"
)

// B-P7: the once-only record is durable: a restarted handler (a new
// process) does not forward the same event twice, and the record is pruned
// to the window.
func TestSeenRecordSurvivesARestart(t *testing.T) {
	hf := newHandlerFixture(t)
	path := filepath.Join(realTempDir(t), "ext", "buzz-seen.json")
	hf.h.SeenPath = path
	id, _ := hf.dm(t, "@lead-b rebase #12", "")
	hf.step(t)
	if len(hf.mail.sent) != 2 {
		t.Fatalf("sent %d", len(hf.mail.sent))
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the record: %v %v", info, err)
	}
	restarted := &Handler{Core: hf.core, Dial: hf.dial.dial, Inbox: hf.serve, Mail: hf.mail, Target: hf.h.Target, SeenPath: path}
	again := hf.serve.deliver(id, hf.core.Pin.Channel, "@lead-b rebase #12")
	if _, err := restarted.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(hf.mail.sent) != 2 {
		t.Fatalf("a restart forwarded the event again: %d sends", len(hf.mail.sent))
	}
	if res, _ := hf.serve.result(again); res != "refused - "+gatestore.RejectReplayed {
		t.Fatalf("reply %q", res)
	}
	// past the window the entry is pruned at the next write
	later := &Handler{Core: hf.core, Dial: hf.dial.dial, Inbox: hf.serve, Mail: hf.mail, Target: hf.h.Target, SeenPath: path,
		Now: func() time.Time { return time.Now().Add(SeenWindow + time.Minute) }}
	s := &seenSet{path: path}
	if err := s.put("x", "-", later.now()); err != nil {
		t.Fatal(err)
	}
	if _, seen, _ := (&seenSet{path: path}).get(id, later.now()); seen {
		t.Fatal("the record is not bounded to the window")
	}
}

// B-P4: a gate lock held by another process does not hang the handler past
// its cap: the lock wait is bounded by the verification's context, the
// timeout path runs (status line, no result, the attention row), and the
// connection is closed.
func TestHeldGateLockIsTheTimeoutPath(t *testing.T) {
	hf := newHandlerFixture(t)
	hf.h.VerifyTimeout = 300 * time.Millisecond
	_, q, answer := raiseRel(t, hf.core, "rel-l")
	lockDir := hf.core.Store.Dir("consumed", "locks")
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	held, err := flock.AcquireExclusive(filepath.Join(lockDir, "rel-l.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	_, msg := hf.dm(t, answer, q.EventID)
	start := time.Now()
	got := hf.step(t)
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("the held lock hung the handler for %s", took)
	}
	if len(got) != 1 || got[0].Kind != "attention" {
		t.Fatalf("handled %+v", got)
	}
	if res, n := hf.serve.result(msg); n != 0 {
		t.Fatalf("a result was sent: %q", res)
	}
	if _, open := hf.dial.counts(); open != 0 {
		t.Fatal("the connection stayed open")
	}
	if _, err := os.Stat(filepath.Join(mergetrain.AlertsDir(hf.core.Store.Root), "verify-timeout-rel-l.json")); err != nil {
		t.Fatalf("no attention row: %v", err)
	}
	held.Close()
	rec, _ := hf.core.Store.Load("rel-l")
	if hf.core.Store.State(rec, time.Now()) != gatestore.StateOpen {
		t.Fatal("the gate closed")
	}
}

// failingInbox answers New with an unreadable-output error until healed.
type failingInbox struct {
	*fakeServe
	broken bool
}

func (f *failingInbox) New(ctx context.Context) ([]Prompt, error) {
	if f.broken {
		return nil, fmt.Errorf("%w: amq list output does not parse", ErrInboxUnreadable)
	}
	return f.fakeServe.New(ctx)
}

// Small finding: an unreadable inbox is an owner alert in the status
// document's attention queue, cleared when the inbox reads again.
func TestUnreadableInboxIsAnAlert(t *testing.T) {
	hf := newHandlerFixture(t)
	in := &failingInbox{fakeServe: hf.serve, broken: true}
	hf.h.Inbox = in
	if _, err := hf.h.Step(context.Background()); err == nil {
		t.Fatal("an unreadable inbox stepped")
	}
	alert := filepath.Join(mergetrain.AlertsDir(hf.core.Store.Root), "buzz-inbox-unreadable.json")
	if _, err := os.Stat(alert); err != nil {
		t.Fatalf("no alert: %v", err)
	}
	in.broken = false
	if _, err := hf.h.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(alert); err == nil {
		t.Fatal("the alert stayed after the inbox read again")
	}
}

// Small finding: the share's grants are verified locally (the doctor row),
// not taken on the relay's AUTH acceptance.
func TestShareGrantsVerifyLocally(t *testing.T) {
	buzzDir := buzzHome(t)
	owner, body := newThrowaway(t), newBody(t)
	root := filepath.Join(buzzDir, "root")
	writeShare(t, root, DefaultShareSession, owner, body, DefaultKinds, time.Now().Add(time.Hour))
	sh, err := LoadShare(root, DefaultShareSession)
	if err != nil {
		t.Fatal(err)
	}
	if err := sh.VerifyGrants(); err != nil {
		t.Fatalf("good grants: %v", err)
	}
	bad := sh
	bad.Tags = append([]Tag(nil), sh.Tags...)
	bad.Tags[2].Conditions = "kind=24200&created_at<1" // not what the owner signed
	if err := bad.VerifyGrants(); err == nil {
		t.Fatal("a grant with other conditions verified")
	}
	other := sh
	other.Body = newBody(t) // the grants were signed for another body
	if err := other.VerifyGrants(); err == nil {
		t.Fatal("grants for another body verified")
	}
}
