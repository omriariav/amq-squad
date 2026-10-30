package seatlaunch

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/avivsinai/agent-message-queue/launchapi"

	"github.com/omriariav/amq-squad/v3/internal/seatexec"
)

// hookReady fakes the host read of AMQ's Codex dialog hook: every entry
// present. No test reads a real Codex home.
func hookReady(string) seatexec.CodexDialogHook { return seatexec.CodexDialogHook{} }

// 3.0.1 t9: the scheduler's doorbell is the seat's next turn. Claude Code and
// Codex seats compile with require_wake true and AMQ's raw injector; a pi
// seat keeps its wake disabled with its own audit reason. Reverting compile.go
// to RequireWake false (the 3.0 line) fails this row.
func TestSeatWakePerRuntime(t *testing.T) {
	q := testSquad(t)
	q.Seats = append(q.Seats, Seat{Handle: "pi1", Runtime: "pi", Role: "# Role\n\npi seat role text here.\n", Worktree: "/w/pi"})
	q.PiShim = "/home/u/.amq-squad/pi-launch/pi"
	q.CodexDialogHookReady = true
	c, err := Compile(q, "", lookPath, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Request.Validate(); err != nil {
		t.Fatalf("AMQ's request validation (its wake grammar included): %v", err)
	}
	body, err := json.Marshal(c.Request)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Intent struct {
			Participants []struct {
				Handle    string `json:"handle"`
				Execution *struct {
					RequireWake bool `json:"require_wake"`
					Wake        struct {
						Mode        string `json:"mode"`
						AuditReason string `json:"audit_reason"`
						Injector    *struct {
							Mode string `json:"mode"`
						} `json:"injector"`
					} `json:"wake"`
				} `json:"execution"`
			} `json:"participants"`
		} `json:"intent"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, p := range wire.Intent.Participants {
		if p.Execution == nil {
			continue
		}
		seen[p.Handle] = true
		e := p.Execution
		switch p.Handle {
		case "cto", "dev":
			if !e.RequireWake || e.Wake.Mode != "enabled" || e.Wake.AuditReason != "" || e.Wake.Injector == nil || e.Wake.Injector.Mode != "raw" {
				t.Errorf("%s: the prepare request carries %+v, want require_wake true, mode enabled, injector raw", p.Handle, e)
			}
		case "pi1":
			if e.RequireWake || e.Wake.Mode != "disabled" || e.Wake.AuditReason != PiWakeAuditReason || e.Wake.Injector != nil {
				t.Errorf("pi1: the prepare request carries %+v, want the disabled wake with the pi audit reason", e)
			}
		}
	}
	for _, h := range []string{"cto", "dev", "pi1"} {
		if !seen[h] {
			t.Errorf("%s has no execution options in the request", h)
		}
	}
	if !strings.Contains(string(body), `"require_wake":true`) {
		t.Fatalf("no require_wake true on the wire:\n%s", body)
	}
}

// echoWakes makes the fake AMQ answer Prepare as AMQ does: the preview echoes
// each runnable participant's execution options (launchapi/prepare.go:200-203
// at v0.116.2). mutate edits the echo before it is returned.
func echoWakes(f *fakeAMQ, mutate func(*launchapi.ParticipantPreviewV1)) AMQRunner {
	return func(ctx context.Context, amq string, args []string, stdin []byte, env []string, dir string) ([]byte, []byte, int, error) {
		if slices.Contains(args, "--prepare") {
			var req launchapi.PrepareRequestV1
			if err := json.Unmarshal(stdin, &req); err != nil {
				f.t.Fatal(err)
			}
			f.prepare.Preview.Participants = nil
			for _, p := range req.Intent.Participants {
				pv := launchapi.ParticipantPreviewV1{Handle: p.Handle, Runnable: p.Runnable, PlannedOutcome: "create"}
				if p.Runnable && p.Execution != nil {
					e := *p.Execution
					pv.Execution = &e
				}
				if mutate != nil {
					mutate(&pv)
				}
				f.prepare.Preview.Participants = append(f.prepare.Preview.Participants, pv)
			}
		}
		return f.run(ctx, amq, args, stdin, env, dir)
	}
}

// The launch result carries, per seat, the wake AMQ admitted (Prepare's
// echo; AMQ's ApplyResult carries no wake field), and the report prints it.
func TestLaunchRecordsTheWakeAMQAdmitted(t *testing.T) {
	q := testSquad(t)
	f := &fakeAMQ{t: t, prepare: preparedResult(), apply: launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "applied", Disposition: "committed"}}
	o := launchOpts(t, f, map[string]string{"sha256:a1": "trust_exact_subject"})
	o.RunAMQ, o.Wait, o.CodexDialogHook = echoWakes(f, nil), 50*time.Millisecond, hookReady
	res, err := Launch(context.Background(), q, o)
	if err != nil {
		t.Fatal(err)
	}
	defer res.proof.close()
	if res.Stage != "applied" || len(res.Seats) != 2 {
		t.Fatalf("stage %s, seats %+v", res.Stage, res.Seats)
	}
	for _, s := range res.Seats {
		w := s.Wake
		if w == nil || !w.RequireWake || w.Mode != "enabled" || w.Injector != "raw" || w.Source != "amq_prepare_preview" {
			t.Errorf("seat %s wake facts %+v", s.Handle, w)
		}
	}
	var out bytes.Buffer
	Report(&out, res)
	if !strings.Contains(out.String(), "seat dev wake: mode enabled, require_wake true, injector raw (AMQ prepare)") {
		t.Fatalf("report:\n%s", out.String())
	}
	b, _ := json.Marshal(res)
	if !strings.Contains(string(b), `"wake":{"require_wake":true,"mode":"enabled","injector":"raw","source":"amq_prepare_preview"}`) {
		t.Fatalf("result json lacks the wake facts:\n%s", b)
	}
}

// A Prepare preview that admits another wake than the compiled one refuses
// the launch before Apply: Codex's wake disabled, required no longer, or
// typed with another injector.
func TestLaunchRefusesAWakeAMQDidNotAdmit(t *testing.T) {
	for name, edit := range map[string]func(*launchapi.ExecutionOptionsV1){
		"disabled": func(e *launchapi.ExecutionOptionsV1) {
			e.RequireWake = false
			e.Wake = launchapi.WakeOptionsV1{Mode: launchapi.WakeDisabled, AuditReason: "adapter consumes its own inbox"}
		},
		"not required": func(e *launchapi.ExecutionOptionsV1) { e.RequireWake = false },
		"paste": func(e *launchapi.ExecutionOptionsV1) {
			e.Wake.Injector = &launchapi.InjectorOptionsV1{Mode: launchapi.InjectorPaste}
		},
		// Only the mode differs (AMQ's grammar never echoes this pair; the
		// check compares each field on its own).
		"mode only": func(e *launchapi.ExecutionOptionsV1) { e.Wake.Mode = launchapi.WakeDisabled },
	} {
		t.Run(name, func(t *testing.T) {
			q := testSquad(t)
			f := &fakeAMQ{t: t, prepare: preparedResult(), apply: launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "applied", Disposition: "committed"}}
			o := launchOpts(t, f, map[string]string{"sha256:a1": "trust_exact_subject"})
			o.CodexDialogHook = hookReady
			o.RunAMQ = echoWakes(f, func(p *launchapi.ParticipantPreviewV1) {
				if p.Handle == "dev" {
					edit(p.Execution)
				}
			})
			_, err := Launch(context.Background(), q, o)
			if err == nil || !strings.Contains(err.Error(), "seat dev: "+ReasonWakeNotAdmitted) || f.applied != nil {
				t.Fatalf("a wake AMQ did not admit: %v (applied %v)", err, f.applied != nil)
			}
		})
	}
}

// Reviewer ruling (3.0.1): the wake is required only for a seat that never
// prompts unattended. Claude Code: only in a mode that never prompts
// (dontAsk); Codex: only with AMQ's Codex dialog hook on the host. Each
// branch, through SeatWake and through Compile.
func TestSeatWakeIsGatedOnTheSeatNeverPrompting(t *testing.T) {
	for _, tc := range []struct {
		runtime, mode string
		hook, want    bool
		reason        string
	}{
		{"claude", "dontAsk", false, true, ""},
		{"claude", "default", true, false, ClaudeWakeAuditReason},
		{"claude", "acceptEdits", true, false, ClaudeWakeAuditReason},
		{"codex", "", true, true, ""},
		{"codex", "", false, false, CodexWakeAuditReason},
		{"pi", "", true, false, PiWakeAuditReason},
	} {
		req, inj, reason := SeatWake(tc.runtime, tc.mode, tc.hook)
		if req != tc.want || reason != tc.reason || (inj != nil) != tc.want || (inj != nil && inj.Mode != launchapi.InjectorRaw) {
			t.Errorf("SeatWake(%s, %q, hook %t) = %t, %+v, %q", tc.runtime, tc.mode, tc.hook, req, inj, reason)
		}
	}
	if !strings.Contains(CodexWakeAuditReason, "amq wake install-dialog-hook --harness codex") || !strings.Contains(CodexWakeAuditReason, "AMQ_DIALOG_HOOK") {
		t.Fatalf("the Codex reason does not name the owner's step: %s", CodexWakeAuditReason)
	}
	q := testSquad(t) // cto claude (dontAsk), dev codex
	for _, ready := range []bool{false, true} {
		q.CodexDialogHookReady = ready
		c, err := Compile(q, "", lookPath, time.Unix(1, 0))
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Request.Validate(); err != nil {
			t.Fatal(err)
		}
		for _, p := range c.Request.Intent.Participants[1:] {
			e := p.Execution
			switch {
			case p.Handle == "cto" && !(e.RequireWake && e.Wake.Mode == launchapi.WakeEnabled):
				t.Errorf("hook %t: the dontAsk Claude seat %+v", ready, e)
			case p.Handle == "dev" && ready && !(e.RequireWake && e.Wake.Mode == launchapi.WakeEnabled && e.Wake.Injector != nil):
				t.Errorf("hook present: the Codex seat %+v", e)
			case p.Handle == "dev" && !ready && (e.RequireWake || e.Wake.Mode != launchapi.WakeDisabled || e.Wake.AuditReason != CodexWakeAuditReason || e.Wake.Injector != nil):
				t.Errorf("hook absent: the Codex seat %+v", e)
			}
		}
	}
}

// The launch reads the hook on the Codex home the seats read ($CODEX_HOME
// of the launch environment) and launches a Codex seat without its wake
// when the hook is missing; the report names the owner's step.
func TestLaunchChecksTheCodexHookBeforeTheCodexWake(t *testing.T) {
	for _, missing := range []bool{true, false} {
		q := testSquad(t)
		f := &fakeAMQ{t: t, prepare: preparedResult(), apply: launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "applied", Disposition: "committed"}}
		o := launchOpts(t, f, map[string]string{"sha256:a1": "trust_exact_subject"})
		o.Env = append(o.Env, "CODEX_HOME=/fake/codex-home")
		var read []string
		o.CodexDialogHook = func(dir string) seatexec.CodexDialogHook {
			read = append(read, dir)
			if missing {
				return seatexec.CodexDialogHook{Missing: []string{"PermissionRequest"}}
			}
			return seatexec.CodexDialogHook{}
		}
		o.RunAMQ, o.Wait = echoWakes(f, nil), 50*time.Millisecond
		res, err := Launch(context.Background(), q, o)
		if err != nil {
			t.Fatal(err)
		}
		res.proof.close()
		if len(read) != 1 || read[0] != "/fake/codex-home" {
			t.Fatalf("hook read %q, want once on CODEX_HOME", read)
		}
		var dev *SeatState
		for i := range res.Seats {
			if res.Seats[i].Handle == "dev" {
				dev = &res.Seats[i]
			}
		}
		if dev == nil || dev.Wake == nil || dev.Wake.RequireWake == missing || (dev.Wake.Mode == "disabled") != missing {
			t.Fatalf("missing %t: dev wake %+v", missing, dev)
		}
		var out bytes.Buffer
		Report(&out, res)
		if missing && !strings.Contains(out.String(), "seat dev wake: mode disabled") || missing && !strings.Contains(out.String(), "amq wake install-dialog-hook --harness codex") {
			t.Fatalf("report:\n%s", out.String())
		}
	}
	// A squad with no Codex seat reads nothing.
	q := testSquad(t)
	q.Seats = q.Seats[:1]
	f := &fakeAMQ{t: t, prepare: preparedResult()}
	o := launchOpts(t, f, nil)
	o.CodexDialogHook = func(string) seatexec.CodexDialogHook {
		t.Fatal("read the Codex hook for a squad without a Codex seat")
		return seatexec.CodexDialogHook{}
	}
	if _, err := Launch(context.Background(), q, o); err != nil {
		t.Fatal(err)
	}
}
