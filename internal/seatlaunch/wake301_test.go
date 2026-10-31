package seatlaunch

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/avivsinai/agent-message-queue/launchapi"

	"github.com/omriariav/amq-squad/v3/internal/seatexec"
)

// readyHook fakes AMQ's Codex dialog hook as present, so no test reads a
// real Codex home.
func readyHook(o *Options) {
	o.CodexDialogHook = func(string) seatexec.CodexDialogHook { return seatexec.CodexDialogHook{} }
}

// Brief 31-1, row 5: Claude Code and Codex seats compile with require_wake
// true and AMQ's raw injector with no wake-condition read beyond the Codex
// dialog hook (Q3); a pi seat keeps its wake disabled with its own audit
// reason.
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
	// The staged launch record carries the compiled wake as a fact.
	for _, rec := range c.Records {
		if rec.WakeRequired != (rec.Handle != "pi1") {
			t.Errorf("%s record WakeRequired %t", rec.Handle, rec.WakeRequired)
		}
	}
}

// echoWakes makes the fake AMQ answer Prepare as AMQ does: the preview echoes
// each runnable participant's execution options (launchapi/prepare.go:200-203
// at v0.116.2). mutate edits a participant's echo; a participant whose
// handle mutate clears is left out of the preview.
func echoWakes(f *fakeAMQ, mutate func(*launchapi.ParticipantPreviewV1)) AMQRunner {
	return func(ctx context.Context, amq string, args []string, stdin []byte, env []string, dir string) ([]byte, []byte, int, error) {
		if slices.Contains(args, "--prepare") {
			var req launchapi.PrepareRequestV1
			if err := json.Unmarshal(stdin, &req); err != nil {
				f.t.Fatal(err)
			}
			f.prepare.Preview.Participants = []launchapi.ParticipantPreviewV1{}
			for _, p := range req.Intent.Participants {
				pv := launchapi.ParticipantPreviewV1{Handle: p.Handle, Runnable: p.Runnable, PlannedOutcome: "create"}
				if p.Runnable && p.Execution != nil {
					e := *p.Execution
					pv.Execution = &e
				}
				if mutate != nil {
					mutate(&pv)
				}
				if pv.Handle != "" {
					f.prepare.Preview.Participants = append(f.prepare.Preview.Participants, pv)
				}
			}
		}
		return f.run(ctx, amq, args, stdin, env, dir)
	}
}

// The launch result carries, per seat, the wake AMQ admitted (Prepare's
// echo; AMQ's ApplyResult carries no wake field).
func TestLaunchRecordsTheWakeAMQAdmitted(t *testing.T) {
	q := testSquad(t)
	f := &fakeAMQ{t: t, prepare: preparedResult(), apply: launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "applied", Disposition: "committed"}}
	o := launchOpts(t, f, map[string]string{"sha256:a1": "trust_exact_subject"})
	readyHook(&o)
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
	b, _ := json.Marshal(res)
	if !strings.Contains(string(b), `"wake":{"require_wake":true,"mode":"enabled","injector":"raw","source":"amq_prepare_preview"}`) {
		t.Fatalf("result json lacks the wake facts:\n%s", b)
	}
}

// Brief 31-1, row 5: a Prepare preview that admits another wake than the
// compiled one, or lists a seat without execution options, is recorded as
// AMQ admitted it and never refuses the launch (wake_not_admitted is gone).
func TestLaunchRecordsAWakeAMQAdmittedDifferently(t *testing.T) {
	for name, tc := range map[string]struct {
		edit func(*launchapi.ParticipantPreviewV1)
		want func(*SeatWakeFacts) bool
	}{
		"disabled": {func(p *launchapi.ParticipantPreviewV1) {
			p.Execution.RequireWake = false
			p.Execution.Wake = launchapi.WakeOptionsV1{Mode: launchapi.WakeDisabled, AuditReason: "adapter consumes its own inbox"}
		}, func(w *SeatWakeFacts) bool {
			return w != nil && !w.RequireWake && w.Mode == "disabled" && w.AuditReason == "adapter consumes its own inbox"
		}},
		"paste": {func(p *launchapi.ParticipantPreviewV1) {
			p.Execution.Wake.Injector = &launchapi.InjectorOptionsV1{Mode: launchapi.InjectorPaste}
		}, func(w *SeatWakeFacts) bool { return w != nil && w.Injector == "paste" }},
		"no execution options": {func(p *launchapi.ParticipantPreviewV1) { p.Execution = nil },
			func(w *SeatWakeFacts) bool { return w == nil }},
	} {
		t.Run(name, func(t *testing.T) {
			q := testSquad(t)
			f := &fakeAMQ{t: t, prepare: preparedResult(), apply: launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "applied", Disposition: "committed"}}
			o := launchOpts(t, f, map[string]string{"sha256:a1": "trust_exact_subject"})
			readyHook(&o)
			o.RunAMQ, o.Wait = echoWakes(f, func(p *launchapi.ParticipantPreviewV1) {
				if p.Handle == "dev" {
					tc.edit(p)
				}
			}), 50*time.Millisecond
			res, err := Launch(context.Background(), q, o)
			if err != nil || res.Stage != "applied" || f.applied == nil {
				t.Fatalf("refused: %v (stage %s)", err, res.Stage)
			}
			res.proof.close()
			for _, st := range res.Seats {
				if st.Handle == "dev" && !tc.want(st.Wake) {
					t.Fatalf("dev wake facts %+v", st.Wake)
				}
			}
		})
	}
}

// Q3 of brief 31-1: a doorbell at a Codex approval prompt cancels the
// pending command, so the Codex wake keeps AMQ's dialog hook as its one
// condition. The launch reads the hook once, on the Codex home the seats
// read ($CODEX_HOME of the launch environment), only for a squad with a
// Codex seat; without it the Codex seat launches with its wake off and the
// reason, never a refusal. The worktree trust is no wake condition.
func TestLaunchReadsTheCodexDialogHook(t *testing.T) {
	for _, tc := range []struct {
		name        string
		hookMissing bool
		want        bool
		reason      string
	}{
		{"hook present", false, true, ""},
		{"hook missing", true, false, "amq wake install-dialog-hook --harness codex"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := testSquad(t)
			f := &fakeAMQ{t: t, prepare: preparedResult(), apply: launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "applied", Disposition: "committed"}}
			o := launchOpts(t, f, map[string]string{"sha256:a1": "trust_exact_subject"})
			o.Env = append(o.Env, "CODEX_HOME=/fake/codex-home")
			var hookRead []string
			o.CodexDialogHook = func(dir string) seatexec.CodexDialogHook {
				hookRead = append(hookRead, dir)
				if tc.hookMissing {
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
			if !slices.Equal(hookRead, []string{"/fake/codex-home"}) {
				t.Fatalf("hook reads %q", hookRead)
			}
			var dev *SeatState
			for i := range res.Seats {
				if res.Seats[i].Handle == "dev" {
					dev = &res.Seats[i]
				}
			}
			if dev == nil || dev.Wake == nil || dev.Wake.RequireWake != tc.want || (dev.Wake.Mode == "enabled") != tc.want {
				t.Fatalf("dev wake %+v", dev)
			}
			if !strings.Contains(dev.Wake.AuditReason, tc.reason) {
				t.Fatalf("dev wake reason %q lacks %q", dev.Wake.AuditReason, tc.reason)
			}
		})
	}
	// A squad with no Codex seat reads neither.
	q := testSquad(t)
	q.Seats = q.Seats[:1]
	f := &fakeAMQ{t: t, prepare: preparedResult(), apply: launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "applied", Disposition: "committed"}}
	o := launchOpts(t, f, nil)
	o.Wait = 50 * time.Millisecond
	o.CodexDialogHook = func(string) seatexec.CodexDialogHook {
		t.Fatal("read the Codex hook for a squad without a Codex seat")
		return seatexec.CodexDialogHook{}
	}
	if _, err := Launch(context.Background(), q, o); err != nil {
		t.Fatal(err)
	}
}
