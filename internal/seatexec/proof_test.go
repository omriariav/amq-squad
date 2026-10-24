//go:build unix

package seatexec

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
)

// Item 4: seat-exec's last step before exec is the proof; it carries the
// ticket, this pid and its raw start stamp, and the token reaches neither
// the provider's environment nor the published record.
func TestProofIsTheLastStepAndTheTokenStaysOut(t *testing.T) {
	f := newFixture(t, "claude")
	f.rec.LaunchID, f.rec.RequestID = "launch-1", "seat-req-inc1-1"
	f.stage()
	if err := f.run(Options{Env: f.env("AMQ_SQUAD_PROOF_TOKEN=tok-0123456789")}); err != nil {
		t.Fatal(err)
	}
	start, _ := procinfo.StartToken(os.Getpid())
	if len(f.proofs) != 1 {
		t.Fatalf("proofs sent: %d", len(f.proofs))
	}
	m := f.proofs[0]
	if m.LaunchID != "launch-1" || m.RequestID != "seat-req-inc1-1" || m.Handle != "cto" || m.Token != "tok-0123456789" ||
		m.PID != os.Getpid() || m.Start != start || m.Root != f.root || m.Session != "s1" {
		t.Fatalf("proof %+v", m)
	}
	for _, kv := range f.execEnv {
		if strings.Contains(kv, "tok-0123456789") {
			t.Fatalf("the token reached the provider: %s", kv)
		}
	}
	b, _ := os.ReadFile(launch.Path(filepath.Join(f.root, "agents", "cto")))
	if strings.Contains(string(b), "tok-0123456789") || strings.Contains(string(b), `"proof"`) {
		t.Fatalf("the published record carries the ticket:\n%s", b)
	}
}

// Item 4: no exec without the launcher's OK.
func TestSeatExecRefusesWithoutAnAcceptedProof(t *testing.T) {
	f := newFixture(t, "claude")
	f.stage()
	f.proofReply = &launch.ProofReply{Code: "identity_forged", Detail: "proof_replayed"}
	if err := f.run(Options{}); refusalCode(err) != "identity_forged" || f.execPath != "" {
		t.Fatalf("a refused proof: %v (exec %q)", err, f.execPath)
	}
	g := newFixture(t, "claude")
	g.stage()
	err := g.run(Options{SendProof: func(string, launch.ProofMessage) (launch.ProofReply, error) {
		return launch.ProofReply{}, errors.New("connect: no such file")
	}})
	if refusalCode(err) != "proof_unacknowledged" || g.execPath != "" {
		t.Fatalf("no launcher: %v", err)
	}
	h := newFixture(t, "claude")
	h.rec.Proof = nil
	h.stage()
	if err := h.run(Options{}); refusalCode(err) != "proof_ticket_missing" || h.execPath != "" {
		t.Fatalf("no ticket: %v", err)
	}
}
