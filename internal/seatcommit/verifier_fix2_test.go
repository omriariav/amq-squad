package seatcommit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/flock"
)

// The rows of the second fix round (v3-5-verifier).

// P1-1: the tick-path users of a seat's commit ledger never wait for it: the
// exit check defers and CleanSeat answers ErrLedgerBusy at once while a
// commit step holds the ledger.
func TestTickPathLedgerUsersNeverWait(t *testing.T) {
	f := newFixture(t)
	tk, g := f.reported("cmd/**"), gitBin(t)
	held, ok, err := flock.TryExclusive(filepath.Join(LedgerDir(f.root), f.handle+".lock"), true)
	if err != nil || !ok {
		t.Skipf("no advisory lock: %v", err)
	}
	defer held.Close()
	type out struct {
		res Result
		err error
	}
	done := make(chan out, 2)
	go func() { done <- out{res: ExitCheck(f.opts(tk), "gone")} }()
	go func() {
		_, err := CleanSeat(f.root, f.handle, g, nil, f.trust(), f.now)
		done <- out{err: err}
	}()
	for i := 0; i < 2; i++ {
		select {
		case o := <-done:
			if o.err == nil && !o.res.Deferred {
				t.Fatalf("a busy ledger: deferred %v, err %v", o.res.Deferred, o.err)
			}
			if o.err != nil && !errors.Is(o.err, ErrLedgerBusy) {
				t.Fatalf("CleanSeat on a busy ledger: %v", o.err)
			}
		case <-time.After(10 * time.Second):
			held.Close()
			t.Fatal("a tick-path ledger user waited for the commit step's lock")
		}
	}
}

// P1-2: a credential whose value is split from its name across lines (a
// pretty-printed JSON object, a YAML key line and its value line) reaches
// neither the evidence ledger nor the lead's tail.
func TestMultiLineCredentialNeverReachesTheEvidence(t *testing.T) {
	value := "Ab9" + "xY7qR" + "Lm2Wq8ZtPv4" // built at run time, never a literal
	for name, print := range map[string]string{
		"json": "printf '%s\\n' '{\"password\":' '  \"" + value + "\"}'",
		"yaml": "printf '%s\\n' 'password:' '  " + value + "'",
		// a quoted scalar with a colon and a blank (the third round's item 2)
		"yaml_quoted": "printf '%s\\n' 'password:' \"  '" + value + ": uv'\"",
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n\n// split\n")
			f.verifying(f.script("v", print+"\nexit 1\n"))
			res := f.run(f.reportedBare("cmd/**"))
			l, _ := f.evidence()
			if res.ReasonCode != "verdict_failed" || l.Payload.Output == "" {
				t.Fatalf("the run: %s", res.ReasonCode)
			}
			if strings.Contains(l.Payload.Output, value[:8]) || strings.Contains(res.Detail, value[:8]) {
				t.Fatal("a split credential reached the ledger or the block detail")
			}
		})
	}
}

// P2-1: a step cancelled while its verifier runs kills the verifier's group,
// records no verdict and pushes nothing.
func TestCancelledStepKillsTheVerifier(t *testing.T) {
	f := newFixture(t)
	pidf := filepath.Join(f.dir, "verifier.pid")
	t.Cleanup(func() { killPIDFile(pidf) })
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n\n// cancel\n")
	f.verifying(f.script("v", "echo $$ > "+pidf+"\nexec /bin/sleep 600\n"))
	tk := f.reportedBare("cmd/**")
	ctx, cancel := context.WithCancel(context.Background())
	o := f.opts(tk)
	o.Ctx = ctx
	done := make(chan Result, 1)
	go func() { done <- Run(o) }()
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if raw, err := os.ReadFile(pidf); err == nil && len(strings.TrimSpace(string(raw))) > 0 {
			break
		}
	}
	cancel()
	var res Result
	select {
	case res = <-done:
	case <-time.After(15 * time.Second):
		killPIDFile(pidf)
		<-done
		t.Fatal("a cancelled step's verifier kept running")
	}
	if !res.Deferred || res.ReasonCode != ReasonStepCancelled || f.lease(tk.ID).Verdict != nil || f.originBranch() != "" {
		t.Fatalf("a cancelled step: deferred %v %s, verdict %v, pushed %q", res.Deferred, res.ReasonCode, f.lease(tk.ID).Verdict != nil, f.originBranch())
	}
}
