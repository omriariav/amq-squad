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
)

// 3.0.1 t9: the scheduler's doorbell is the seat's next turn. Claude Code and
// Codex seats compile with require_wake true and AMQ's raw injector; a pi
// seat keeps its wake disabled with its own audit reason. Reverting compile.go
// to RequireWake false (the 3.0 line) fails this row.
func TestSeatWakePerRuntime(t *testing.T) {
	q := testSquad(t)
	q.Seats = append(q.Seats, Seat{Handle: "pi1", Runtime: "pi", Role: "# Role\n\npi seat role text here.\n", Worktree: "/w/pi"})
	q.PiShim = "/home/u/.amq-squad/pi-launch/pi"
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
	o.RunAMQ, o.Wait = echoWakes(f, nil), 50*time.Millisecond
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
