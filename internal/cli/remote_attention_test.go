package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/buzz"
	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
	"github.com/omriariav/amq-squad/v3/internal/status"
)

// stalledRelay answers nothing by id until its context ends.
type stalledRelay struct{ *cliMemRelay }

func (s stalledRelay) FetchByID(ctx context.Context, _ string) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func verifyTimeoutRow(st status.Status, gateID string) *status.Attention {
	for i := range st.Attention {
		if a := st.Attention[i]; a.Kind == buzz.KindVerifyTimeout && a.GateID == gateID {
			return &st.Attention[i]
		}
	}
	return nil
}

// 5-amq-B fix round, ruling 3: a hung verification is a row in the status
// document's attention queue (where the owner looks), with the gate id and
// verify_timeout, while the gate is open; the owner's re-answer, verified,
// takes it away.
func TestHungVerificationIsInTheStatusAttentionQueue(t *testing.T) {
	f := newGateCLIFixture(t)
	serve := f.serve()
	prevD, prevT := remoteDialer, remoteVerifyTimeout
	remoteVerifyTimeout = 200 * time.Millisecond
	remoteDialer = func(gateverify.Binding, gateverify.Pin) buzz.Dialer {
		return func(context.Context) (gateverify.Relay, nostr.SecretKey, []string, func(), error) {
			return stalledRelay{f.relay}, f.body, nil, func() {}, nil
		}
	}
	t.Cleanup(func() { remoteDialer, remoteVerifyTimeout = prevD, prevT })
	if _, _, err := captureOutput(t, func() error {
		return runGate([]string{"raise", "--project", f.project, "--session", "s", "--me", "cto", "--gate", "rel-h", "--action", "tag", "--target", "git tag v3.0.0-h", "--json"})
	}); err != nil {
		t.Fatalf("gate raise: %v", err)
	}
	store, _ := gatestore.Open(f.root)
	rec, _ := store.Load("rel-h")
	q, _ := store.Question("rel-h")
	answer := "approve rel-h release " + strings.TrimPrefix(rec.Digest, "sha256:")[:16]
	f.answer(t, answer, q.EventID)
	msg := serve.inbox[len(serve.inbox)-1].ID
	out := attachOnce(t, f)
	if !strings.Contains(out, `"kind":"attention"`) {
		t.Fatalf("no attention line:\n%s", out)
	}
	if res, n := serve.result(msg); n != 0 {
		t.Fatalf("a hang sent a result %q", res)
	}
	st := buildStatusContract(f.root, f.project, "test", time.Now())
	row := verifyTimeoutRow(st, "rel-h")
	if row == nil || row.Severity != "high" || row.Bucket != status.BucketNeedsYou || !strings.Contains(row.Subject, "rel-h: verify_timeout") {
		t.Fatalf("the status document's attention queue: %+v", st.Attention)
	}
	// the owner answers again once the relay answers: verified, the row goes
	remoteDialer = prevD
	f.answer(t, "Approve"+strings.TrimPrefix(answer, "approve"), q.EventID)
	msg2 := serve.inbox[len(serve.inbox)-1].ID
	attachOnce(t, f)
	if res, _ := serve.result(msg2); res != "accepted rel-h tag" {
		t.Fatalf("re-answer %q", res)
	}
	if row := verifyTimeoutRow(buildStatusContract(f.root, f.project, "test", time.Now()), "rel-h"); row != nil {
		t.Fatalf("the row stayed after the gate was accepted: %+v", row)
	}
}

// A verify_timeout alert about a gate that is no longer open is not shown.
func TestGateAlertShownOnlyWhileTheGateIsOpen(t *testing.T) {
	now := time.Now()
	in := status.Input{Now: now, Alerts: []status.AlertInput{{ID: "verify-timeout-g", Kind: buzz.KindVerifyTimeout, GateID: "g", Subject: "gate g: verify_timeout", At: now}}}
	in.Gates = []status.GateInput{{ID: "g", Class: "tag", State: "open", Since: now}}
	if verifyTimeoutRow(status.Build(in), "g") == nil {
		t.Fatal("open gate: no row")
	}
	in.Gates[0].State = "accepted"
	if verifyTimeoutRow(status.Build(in), "g") != nil {
		t.Fatal("accepted gate: the row is still shown")
	}
}

// B-P6: attach checks the share's binding before it starts anything: a
// share still on the retired fake target is refused and the endpoint is
// never started, even with the bound seat present.
func TestAttachChecksTheBindingBeforeTheEndpoint(t *testing.T) {
	f := newGateCLIFixture(t)
	ep := &remoteFakeEndpoint{}
	prevE := remoteEndpoint
	remoteEndpoint = func(string) buzz.Endpoint { return ep }
	t.Cleanup(func() { remoteEndpoint = prevE })
	raw, _ := json.Marshal(map[string]any{"status": "active", "last_seen": time.Now().UTC()})
	mustWrite(t, filepath.Join(f.root, "agents", "cto", "presence.json"), string(raw))
	writeShareManifest(t, filepath.Join(f.home, ".amq-squad", "buzz", "root"), "wss://relay.test", "dm-chan", "fake")
	_, _, err := captureOutput(t, func() error {
		return runRemote([]string{"attach", "--once", "--json", "--project", f.project, "--session", "s", "--me", "cto", "--lead", "cto"}, "test")
	})
	if err == nil || !strings.Contains(err.Error(), "retired") || ep.starts != 0 {
		t.Fatalf("attach on the fake target: %v, starts %d", err, ep.starts)
	}
	// with the mailbox binding the same run starts the endpoint
	writeShareManifest(t, filepath.Join(f.home, ".amq-squad", "buzz", "root"), "wss://relay.test", "dm-chan", "mailbox")
	if _, _, err := captureOutput(t, func() error {
		return runRemote([]string{"attach", "--once", "--json", "--project", f.project, "--session", "s", "--me", "cto", "--lead", "cto"}, "test")
	}); err != nil || ep.starts != 1 {
		t.Fatalf("attach on the mailbox target: %v, starts %d", err, ep.starts)
	}
}

// Small finding: doctor verifies the enrolled grants locally; placeholder
// signatures are a failing row, and no binding gives no row.
func TestDoctorBuzzGrantRow(t *testing.T) {
	f := newGateCLIFixture(t)
	writeTestShare(t, filepath.Join(f.home, ".amq-squad", "buzz", "root"), "amq-squad", f.owner.PublicHex(), time.Now().Add(time.Hour))
	c, ok := buzzGrantCheck()
	if !ok || c.Status != doctorFail || !strings.Contains(c.Detail, "signature") {
		t.Fatalf("placeholder grants: %+v %v", c, ok)
	}
	t.Setenv("HOME", t.TempDir())
	if c, ok := buzzGrantCheck(); ok {
		t.Fatalf("no binding gave a row: %+v", c)
	}
}
