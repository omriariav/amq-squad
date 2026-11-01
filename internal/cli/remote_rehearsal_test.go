package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
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
// through remote attach, the gate accepted.
func TestRehearsalThroughServe(t *testing.T) {
	f := newGateCLIFixture(t)
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

}
