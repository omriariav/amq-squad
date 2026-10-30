package cli

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gatetarget"
	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/seatexec"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// The Buzz-signed seat clear (phase 5, plan 4.3 and 7.3; amendment 28's
// second factor). The owner's pty (requireOwnerTerminal) is the request
// half: it writes a quarantine_clear gate (nonce, handle, incarnation, the
// config diff's digest) to the gate store and posts it to the owner over
// Buzz like the four typed gates. The clear completes only when the owner's
// signed answer to that gate verifies (gateverify, as the typed gates'
// answers do), and the signed clear record then carries the gate id and the
// answer's event id. With no answer the clear waits as an open gate: there
// is no fallback when Buzz is unreachable.

// errClearUnsigned is the refusal of a clear with no accepted owner answer;
// the quarantine stays.
var errClearUnsigned = errors.New(gateverify.RejectClearUnsigned)

// clearEnrolled says whether the owner's Buzz enrolment exists. A binding
// file that is present but unreadable still counts: a damaged enrolment
// never lowers the clear to the pty alone.
func clearEnrolled() bool {
	_, err := os.Lstat(gateBindingPath())
	return err == nil
}

// clearModeInForce is the clear.signature mode in force: the owner grants
// file's own, else required whenever the enrolment exists. An owner file
// that does not parse, or says pty_only beside an enrolment, is
// grants_invalid.
func clearModeInForce() (string, error) {
	r, err := grants.Load("")
	if err != nil {
		return "", fmt.Errorf("grants_invalid: %w", err)
	}
	return r.ClearMode(clearEnrolled()), nil
}

// clearIncarnation names one quarantine of a seat: the quarantined task and
// the quarantine's time.
func clearIncarnation(taskID string, at time.Time) string {
	return taskID + "@" + strconv.FormatInt(at.UnixNano(), 10)
}

// clearConfigDigest is the digest of a quarantine's config diff.
func clearConfigDigest(changes []seatexec.ConfigChange) string {
	raw, _ := json.Marshal(append([]seatexec.ConfigChange{}, changes...))
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// clearIdentity is what a request names about the quarantine.
type clearIdentity struct {
	incarnation, config string
	ledger              bool                         // the quarantine is on the seat's commit ledger
	seed                *seatcommit.LedgerQuarantine // a task-store quarantine, recorded on the ledger by the clear
}

// quarantineIdentity reads the seat's quarantine: the commit ledger's when
// it holds an uncleared one, else the task store's.
func quarantineIdentity(root string, store task.Store, seat string) (clearIdentity, error) {
	l, err := seatcommit.ReadLedger(root, seat)
	switch {
	case err == nil && l.Quarantine != nil && l.Quarantine.ClearedAt == nil:
		q := l.Quarantine
		return clearIdentity{incarnation: clearIncarnation(q.Task, q.At), config: clearConfigDigest(q.Config), ledger: true}, nil
	case err != nil && !os.IsNotExist(err):
		return clearIdentity{}, err
	}
	list, err := task.Quarantined(store.Dir)
	if err != nil {
		return clearIdentity{}, err
	}
	for _, q := range list {
		if q.Handle == seat {
			launch := ""
			if rec, err := seatcommit.StagedSeat(root, seat); err == nil {
				launch = rec.LaunchID
			}
			return clearIdentity{incarnation: clearIncarnation(q.TaskID, q.At), config: clearConfigDigest(nil),
				seed: &seatcommit.LedgerQuarantine{Task: q.TaskID, Reason: q.Reason, At: q.At, Launch: launch, Config: []seatexec.ConfigChange{}}}, nil
		}
	}
	return clearIdentity{}, fmt.Errorf("seat %s is not quarantined", seat)
}

// gatedClear is the request half and the verification of the answer.
type gatedClear struct {
	id       clearIdentity
	store    gatestore.Store
	verifier gateverify.Verifier
	seat     string
	gateID   string
	root     string
}

// authorize decides the clear of the quarantine q now (the ledger lock is
// held by the caller). It writes nothing: the sealed ClearRecord that the
// caller signs and writes in one durable ledger write is what uses the
// answer, so a failure before that write leaves the answer usable.
func (g *gatedClear) authorize(q *seatcommit.LedgerQuarantine) (seatcommit.ClearGate, error) {
	id := clearIdentity{incarnation: clearIncarnation(q.Task, q.At), config: clearConfigDigest(q.Config)}
	p, err := g.verifier.AuthorizeClear(g.store, g.seat, g.gateID, id.incarnation, id.config, g.used)
	if err != nil {
		return seatcommit.ClearGate{}, err
	}
	return seatcommit.ClearGate{GateID: p.GateID, EventID: p.EventID, ConfigDigest: id.config}, nil
}

// used asks the seat's sealed ledger whether a ClearRecord carries the event.
func (g *gatedClear) used(eventID string) (bool, error) {
	return seatcommit.ClearEventUsed(g.root, g.seat, eventID)
}

// requestClear is the request half for a quarantined seat: it reuses the
// seat's open request for this quarantine or raises a new one (record first,
// then the question over Buzz), and then asks the verifier whether the
// owner's signed answer is in. With none, it returns errClearUnsigned and
// writes nothing else: the quarantine stays.
func requestClear(ctx context.Context, sel storeSelection, f taskFlags, seat string, out io.Writer) (*gatedClear, error) {
	id, err := quarantineIdentity(sel.root, sel.store, seat)
	if err != nil {
		return nil, fmt.Errorf("seat clear: %w", err)
	}
	cx := sel.ctx
	if cx == nil {
		c, err := resolveScopedCommandContext(*f.project, *f.profile, *f.session, "", f.fs)
		if err != nil {
			return nil, fmt.Errorf("seat clear: the Buzz-signed clear needs the session (--project, --profile, --session): %w", err)
		}
		cx = &c
	}
	core, terr := gateCore(ctx, *cx, true)
	if core == nil {
		return nil, fmt.Errorf("seat clear: clear.signature is required and the Buzz enrolment cannot be read (%v); there is no pty-only fallback", terr)
	}
	store, err := gatestore.Open(sel.root)
	if err != nil {
		return nil, fmt.Errorf("seat clear: %w", err)
	}
	core.Store = store
	trust, err := operatorauth.LoadAuthorizationTrustStore(gateTrustStorePath())
	if err != nil {
		return nil, fmt.Errorf("seat clear: the owner's trust store: %w", err)
	}
	g := &gatedClear{id: id, store: store, seat: seat, root: sel.root, verifier: gateverify.Verifier{Trust: trust, Pin: core.Pin}}

	req, err := store.ReadClearRequest(seat)
	if err != nil {
		return nil, fmt.Errorf("seat clear: %w", err)
	}
	reuse := false
	if req != nil && req.Incarnation == id.incarnation && req.ConfigDigest == id.config {
		if rec, lerr := store.Load(req.GateID); lerr == nil && store.State(rec, taskNow()) != gatestore.StateExpired && store.State(rec, taskNow()) != gatestore.StateDenied {
			reuse = true
		}
	}
	if !reuse {
		if req, err = raiseClear(ctx, core, seat, id, terr); err != nil {
			return nil, fmt.Errorf("seat clear: %w", err)
		}
	}
	g.gateID = req.GateID
	if _, err := g.verifier.AuthorizeClear(store, seat, req.GateID, id.incarnation, id.config, g.used); err != nil {
		rec, _ := store.Load(req.GateID)
		sent := store.QuestionPublished(req.GateID)
		if !sent && terr == nil {
			if _, perr := store.PostQuestion(req.GateID, core.QuestionSender(ctx)); perr == nil {
				sent = true
			} else {
				terr = perr
			}
		}
		fmt.Fprintf(out, "seat %s stays quarantined: clear request %s (until %s)\n", seat, req.GateID, rec.NotAfter)
		if sent {
			fmt.Fprintf(out, "  the owner's answer is awaited on Buzz; after it, run this verb again to complete the clear\n")
		} else {
			fmt.Fprintf(out, "  the question is not on Buzz yet (%v); send it with: amq-squad gate post --gate %s\n", terr, req.GateID)
		}
		switch gateverify.Code(err) {
		case gateverify.RejectClearUnsigned:
			return nil, fmt.Errorf("seat clear: %w: %v", errClearUnsigned, err)
		}
		return nil, fmt.Errorf("seat clear: %w", err)
	}
	return g, nil
}

// raiseClear writes a new request for the quarantine and raises its gate.
func raiseClear(ctx context.Context, core *gateverify.Core, seat string, id clearIdentity, terr error) (*gatestore.ClearRequest, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	nonce := hex.EncodeToString(b)
	spec := gatestore.Spec{GateID: "clear-" + nonce, Action: "quarantine_clear", Target: gatetarget.QuarantineClear(seat, nonce, id.incarnation, id.config),
		RaisedBy: "owner", Squad: core.Store.Squad()}
	var err error
	if terr == nil {
		// a failed post leaves the record; the question is re-posted on the next run
		_, _, err = core.Raise(ctx, spec)
		if err != nil {
			if _, lerr := core.Store.Load(spec.GateID); lerr != nil {
				return nil, err
			}
		}
	} else {
		var rec gatestore.Record
		if rec, err = gatestore.Render(spec, taskNow(), core.Identity()); err == nil {
			err = core.Store.Create(rec)
		}
		if err != nil {
			return nil, err
		}
	}
	r := gatestore.ClearRequest{Handle: seat, Nonce: nonce, GateID: spec.GateID, Incarnation: id.incarnation, ConfigDigest: id.config, At: taskNow().UTC().Format(time.RFC3339)}
	if err := core.Store.PutClearRequest(r); err != nil {
		return nil, err
	}
	return &r, nil
}

// checkClearRecord is the commit-time half: a clear record that carries a
// gate must have that gate's verified owner answer, and in required mode a
// record without one is the pty-only clear and does not count.
func checkClearRecord(root string, payload []byte) error {
	raw, ok := strings.CutPrefix(string(payload), "amq-squad seat clear\n")
	if !ok {
		return errors.New("not a seat clear record")
	}
	var rec seatcommit.ClearRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return err
	}
	if rec.GateID == "" {
		mode, err := clearModeInForce()
		if err != nil {
			return err
		}
		if mode == grants.ClearRequired {
			return fmt.Errorf("%w: the clear carries no owner answer and clear.signature is required", errClearUnsigned)
		}
		return nil
	}
	if root == "" {
		return fmt.Errorf("%w: the clear names gate %s and no session root is known to check it", errClearUnsigned, rec.GateID)
	}
	store, err := gatestore.Open(root)
	if err != nil {
		return err
	}
	_, pin, err := gateverify.LoadBinding(gateBindingPath())
	if err != nil {
		return fmt.Errorf("%w: %v", errClearUnsigned, err)
	}
	trust, err := operatorauth.LoadAuthorizationTrustStore(gateTrustStorePath())
	if err != nil {
		return fmt.Errorf("%w: %v", errClearUnsigned, err)
	}
	v := gateverify.Verifier{Trust: trust, Pin: pin}
	if err := v.CheckClear(store, rec.Seat, rec.GateID, rec.EventID, clearIncarnation(rec.Task, rec.QuarantinedAt), rec.ConfigDigest); err != nil {
		return fmt.Errorf("%w: %v", errClearUnsigned, err)
	}
	return nil
}

// doctorClearChecks names the clear mode in force and, for each seat clear
// still waiting for the owner, why.
func doctorClearChecks(root string) []doctorCheck {
	check := doctorCheck{Name: "seat clear signature", Status: doctorOK}
	mode, err := clearModeInForce()
	switch {
	case err != nil:
		check.Status, check.Detail = doctorFail, err.Error()
	case mode == grants.ClearRequired:
		check.Detail = "required: a clear completes only on the owner's signed Buzz answer (no fallback when Buzz is unreachable)"
	default:
		check.Detail = "pty_only: no Buzz enrolment exists, so the owner's real pty alone clears (enrol with amq-squad remote to require the signed answer)"
	}
	out := []doctorCheck{check}
	if root == "" || mode != grants.ClearRequired {
		return out
	}
	store, serr := gatestore.Open(root)
	if serr != nil {
		return out
	}
	ids, _ := store.IDs()
	for _, id := range ids {
		rec, err := store.Load(id)
		if err != nil || rec.Class != "quarantine_clear" || store.State(rec, time.Now()) != gatestore.StateOpen {
			continue
		}
		handle, _, _, _, _ := gatetarget.ParseQuarantineClear(rec.Target)
		c := doctorCheck{Name: "seat clear " + handle, Status: doctorWarn}
		if store.QuestionPublished(id) {
			c.Detail = "gate " + id + " is open: waiting for the owner's signed answer on Buzz (until " + rec.NotAfter + ")"
		} else {
			c.Detail = "gate " + id + " is open and its question never reached Buzz (relay unreachable or not enrolled): send it with amq-squad gate post --gate " + id + "; the clear waits, there is no fallback"
		}
		out = append(out, c)
	}
	return out
}

// withClearChecks adds the clear checks to doctor's gate-store hook.
func withClearChecks(hook func() []doctorCheck, root string) func() []doctorCheck {
	return func() []doctorCheck {
		var out []doctorCheck
		if hook != nil {
			out = hook()
		}
		return append(out, doctorClearChecks(root)...)
	}
}
