package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
)

// attachOnce runs one tick of remote attach to the squad lead cto.
func attachOnce(t *testing.T, f gateCLIFixture) string {
	t.Helper()
	out, _, err := captureOutput(t, func() error {
		return runRemote([]string{"attach", "--once", "--json", "--no-endpoint", "--project", f.project, "--session", "s", "--me", "cto", "--lead", "cto"}, "test")
	})
	if err != nil {
		t.Fatalf("attach: %v\n%s", err, out)
	}
	return out
}

// 5-amq-B acceptance 6, the rehearsal (fake relay, fake serve): the gate
// question, the owner's answer delivered by serve, verified by the core
// through remote attach, the gate accepted; then the Buzz-signed seat
// clear through the same path, its record naming the gate and the event
// serve delivered.
func TestRehearsalThroughServe(t *testing.T) {
	f, _, _ := signedClearSeat(t)
	ownerProcess(t, nil, false)
	serve := f.serve()
	mailer := &remoteFakeMailer{}
	prevM := remoteMailer
	remoteMailer = mailer
	t.Cleanup(func() { remoteMailer = prevM })

	// the typed gate
	if _, _, err := captureOutput(t, func() error {
		return runGate([]string{"raise", "--project", f.project, "--session", "s", "--me", "cto", "--gate", "rel-r", "--action", "tag", "--target", "git tag v3.0.0-r", "--json"})
	}); err != nil {
		t.Fatalf("gate raise: %v", err)
	}
	store, _ := gatestore.Open(f.root)
	rec, _ := store.Load("rel-r")
	q, _ := store.Question("rel-r")
	f.answer(t, "approve rel-r release "+strings.TrimPrefix(rec.Digest, "sha256:")[:16], q.EventID)
	msg := serve.inbox[len(serve.inbox)-1].ID
	out := attachOnce(t, f)
	if !strings.Contains(out, `"outcome":"approved"`) {
		t.Fatalf("attach did not verify the answer:\n%s", out)
	}
	if res, n := serve.result(msg); n != 1 || res != "accepted rel-r tag" {
		t.Fatalf("the result row %q (%d)", res, n)
	}
	if st := store.State(rec, hookNow()); st != gatestore.StateAccepted {
		t.Fatalf("gate state %s", st)
	}
	if _, _, err := captureOutput(t, func() error {
		return runVerify([]string{"action", "--project", f.project, "--session", "s", "--gate", "rel-r", "--action", "tag", "--target", "git tag v3.0.0-r"})
	}); err != nil {
		t.Fatalf("verify action: %v", err)
	}
	if len(mailer.sent) != 0 {
		t.Fatalf("a gate answer was routed: %+v", mailer.sent)
	}

	// the signed seat clear, the same path
	if _, err := signedClear(t, f); !errors.Is(err, errClearUnsigned) {
		t.Fatalf("clear request: %v", err)
	}
	crec, cq := clearGateOf(t, f)
	event := f.answer(t, "approve "+crec.GateID+" clear "+strings.TrimPrefix(crec.Digest, "sha256:")[:16], cq.EventID)
	cmsg := serve.inbox[len(serve.inbox)-1].ID
	out = attachOnce(t, f)
	if res, n := serve.result(cmsg); n != 1 || res != "accepted "+crec.GateID+" quarantine_clear" {
		t.Fatalf("the clear's result row %q (%d)\n%s", res, n, out)
	}
	if out, err := signedClear(t, f); err != nil {
		t.Fatalf("the clear after the answer: %v\n%s", err, out)
	}
	l, _ := seatcommit.ReadLedger(f.root, "s1")
	if c := l.Quarantine.Clear; c == nil || c.GateID != crec.GateID || c.EventID != event {
		t.Fatalf("the clear record: %+v", c)
	}
}
