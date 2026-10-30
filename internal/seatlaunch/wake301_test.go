package seatlaunch

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/avivsinai/agent-message-queue/launchapi"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
	"github.com/omriariav/amq-squad/v3/internal/hostcheck"
	"github.com/omriariav/amq-squad/v3/internal/seatexec"
)

// allMet meets every wake condition of every runtime.
var allMet = SeatWakeConditions{ClaudeGuardWired: true, ClaudeScreensClosed: true, CodexHookReady: true, CodexTrusted: true}

// metHost fakes the launch's host reads with every condition met: AMQ's
// Codex dialog hook present, the Codex config trusting every seat worktree,
// and the Claude conditions met. No test reads a real Codex home or writes
// a real Claude config dir through these.
func metHost(q Squad, o *Options) {
	trusted := map[string]bool{}
	for _, s := range q.Seats {
		trusted[s.Worktree] = true
	}
	o.CodexDialogHook = func(string) seatexec.CodexDialogHook { return seatexec.CodexDialogHook{} }
	o.CodexTrust = func(string) (hostcheck.CodexTrust, error) { return hostcheck.CodexTrust{Trusted: trusted}, nil }
	o.ClaudeWakeConditions = func(string, string) SeatWakeConditions { return allMet }
}

// 3.0.1 t9: with every condition met, Claude Code and Codex seats compile
// with require_wake true and AMQ's raw injector; a pi seat keeps its wake
// disabled with its own audit reason.
func TestSeatWakePerRuntime(t *testing.T) {
	q := testSquad(t)
	q.Seats = append(q.Seats, Seat{Handle: "pi1", Runtime: "pi", Role: "# Role\n\npi seat role text here.\n", Worktree: "/w/pi"})
	q.PiShim = "/home/u/.amq-squad/pi-launch/pi"
	q.WakeConditions = map[string]SeatWakeConditions{"cto": allMet, "dev": allMet, "pi1": allMet}
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
	// The staged launch record carries the compiled wake, for seat-exec's
	// re-check on the real config dir.
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
// echo; AMQ's ApplyResult carries no wake field), and the report prints it.
func TestLaunchRecordsTheWakeAMQAdmitted(t *testing.T) {
	q := testSquad(t)
	f := &fakeAMQ{t: t, prepare: preparedResult(), apply: launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "applied", Disposition: "committed"}}
	o := launchOpts(t, f, map[string]string{"sha256:a1": "trust_exact_subject"})
	metHost(q, &o)
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

// Every seat needs AMQ's admission evidence: a Prepare preview that admits
// another wake than the compiled one, lists no participant for a seat, or
// lists one without execution options refuses the launch before Apply,
// whether the seat's wake is on or off.
func TestLaunchRefusesAWakeAMQDidNotAdmit(t *testing.T) {
	evidence := map[string]string{
		"no participant": "lists no participant for the seat", "no participant, wake off": "lists no participant for the seat",
		"no execution options": "carries no execution options", "no execution options, wake off": "carries no execution options",
	}
	for name, edit := range map[string]func(*launchapi.ParticipantPreviewV1){
		"disabled": func(p *launchapi.ParticipantPreviewV1) {
			p.Execution.RequireWake = false
			p.Execution.Wake = launchapi.WakeOptionsV1{Mode: launchapi.WakeDisabled, AuditReason: "adapter consumes its own inbox"}
		},
		"not required": func(p *launchapi.ParticipantPreviewV1) { p.Execution.RequireWake = false },
		"paste": func(p *launchapi.ParticipantPreviewV1) {
			p.Execution.Wake.Injector = &launchapi.InjectorOptionsV1{Mode: launchapi.InjectorPaste}
		},
		// Only the mode differs (AMQ's grammar never echoes this pair; the
		// check compares each field on its own).
		"mode only":                      func(p *launchapi.ParticipantPreviewV1) { p.Execution.Wake.Mode = launchapi.WakeDisabled },
		"no participant":                 func(p *launchapi.ParticipantPreviewV1) { p.Handle = "" },
		"no execution options":           func(p *launchapi.ParticipantPreviewV1) { p.Execution = nil },
		"no participant, wake off":       func(p *launchapi.ParticipantPreviewV1) { p.Handle = "" },
		"no execution options, wake off": func(p *launchapi.ParticipantPreviewV1) { p.Execution = nil },
	} {
		t.Run(name, func(t *testing.T) {
			q := testSquad(t)
			f := &fakeAMQ{t: t, prepare: preparedResult(), apply: launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "applied", Disposition: "committed"}}
			o := launchOpts(t, f, map[string]string{"sha256:a1": "trust_exact_subject"})
			metHost(q, &o)
			if strings.HasSuffix(name, "wake off") {
				o.CodexDialogHook = func(string) seatexec.CodexDialogHook {
					return seatexec.CodexDialogHook{Missing: []string{"Stop"}}
				}
			}
			o.RunAMQ = echoWakes(f, func(p *launchapi.ParticipantPreviewV1) {
				if p.Handle == "dev" {
					edit(p)
				}
			})
			_, err := Launch(context.Background(), q, o)
			if err == nil || !strings.Contains(err.Error(), "seat dev: "+ReasonWakeNotAdmitted) || !strings.Contains(err.Error(), evidence[name]) || f.applied != nil {
				t.Fatalf("a wake AMQ did not admit: %v (applied %v)", err, f.applied != nil)
			}
		})
	}
}

// Reviewer ruling (3.0.1): a seat's wake is a set of fail-closed conditions
// per runtime; an unmet one keeps the wake off with a named reason. Each
// branch through SeatWake, and the zero value meets none.
func TestSeatWakeConditionsPerRuntime(t *testing.T) {
	claudeMet := SeatWakeConditions{ClaudeGuardWired: true, ClaudeScreensClosed: true}
	codexMet := SeatWakeConditions{CodexHookReady: true, CodexTrusted: true}
	for _, tc := range []struct {
		name, runtime, mode string
		c                   SeatWakeConditions
		want                bool
		reason              string
	}{
		{"claude all met", "claude", "dontAsk", claudeMet, true, ""},
		{"claude mode prompts", "claude", "default", claudeMet, false, "permission mode default can prompt"},
		{"claude acceptEdits", "claude", "acceptEdits", claudeMet, false, "permission mode acceptEdits can prompt"},
		{"claude no guard deny", "claude", "dontAsk", SeatWakeConditions{ClaudeScreensClosed: true, ClaudeGuardWhy: "settings.json does not enable x"}, false, "does not deny AskUserQuestion and ExitPlanMode in the seat's config dir (settings.json does not enable x)"},
		{"claude screens open", "claude", "dontAsk", SeatWakeConditions{ClaudeGuardWired: true, ClaudeScreensWhy: claudeseat.OnboardingUnverified}, false, "startup screens are not verified closed (claude_onboarding_unverified)"},
		{"claude zero value", "claude", "dontAsk", SeatWakeConditions{}, false, ClaudeNoWake},
		{"codex all met", "codex", "", codexMet, true, ""},
		{"codex no hook", "codex", "", SeatWakeConditions{CodexTrusted: true, CodexHookWhy: "no AMQ_DIALOG_HOOK entry for Stop"}, false, "AMQ's Codex dialog hook is not on the host (no AMQ_DIALOG_HOOK entry for Stop); fix: amq wake install-dialog-hook --harness codex, then open /hooks in Codex and trust the AMQ_DIALOG_HOOK entries"},
		{"codex untrusted worktree", "codex", "", SeatWakeConditions{CodexHookReady: true, CodexTrustWhy: "no entry"}, false, "the Codex config does not trust the seat's worktree (no entry)"},
		{"codex zero value", "codex", "", SeatWakeConditions{}, false, CodexNoWake},
		{"pi", "pi", "", allMet, false, PiWakeAuditReason},
	} {
		req, inj, reason := SeatWake(tc.runtime, tc.mode, tc.c)
		if req != tc.want || (inj != nil) != tc.want || (inj != nil && inj.Mode != launchapi.InjectorRaw) || !strings.Contains(reason, tc.reason) || (tc.want != (reason == "")) {
			t.Errorf("%s: SeatWake = %t, %+v, %q", tc.name, req, inj, reason)
		}
	}
	// Compile: a seat with no conditions read keeps its wake off.
	q := testSquad(t)
	c, err := Compile(q, "", lookPath, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Request.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, p := range c.Request.Intent.Participants[1:] {
		if e := p.Execution; e.RequireWake || e.Wake.Mode != launchapi.WakeDisabled || e.Wake.Injector != nil || e.Wake.AuditReason == "" {
			t.Errorf("%s with no conditions read: %+v", p.Handle, e)
		}
	}
}

// The launch reads the Codex conditions once, on the Codex home the seats
// read ($CODEX_HOME of the launch environment), only for a squad with a
// Codex seat; an unmet one launches the Codex seat without its wake and the
// report names it.
func TestLaunchReadsTheCodexWakeConditions(t *testing.T) {
	for _, tc := range []struct {
		name          string
		hookMissing   bool
		trusted, want bool
		report        string
	}{
		{"both met", false, true, true, "seat dev wake: mode enabled"},
		{"hook missing", true, true, false, "amq wake install-dialog-hook --harness codex"},
		{"worktree untrusted", false, false, false, "does not trust the seat's worktree"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := testSquad(t)
			f := &fakeAMQ{t: t, prepare: preparedResult(), apply: launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "applied", Disposition: "committed"}}
			o := launchOpts(t, f, map[string]string{"sha256:a1": "trust_exact_subject"})
			metHost(q, &o)
			o.Env = append(o.Env, "CODEX_HOME=/fake/codex-home")
			var hookRead, trustRead []string
			o.CodexDialogHook = func(dir string) seatexec.CodexDialogHook {
				hookRead = append(hookRead, dir)
				if tc.hookMissing {
					return seatexec.CodexDialogHook{Missing: []string{"PermissionRequest"}}
				}
				return seatexec.CodexDialogHook{}
			}
			o.CodexTrust = func(path string) (hostcheck.CodexTrust, error) {
				trustRead = append(trustRead, path)
				return hostcheck.CodexTrust{Trusted: map[string]bool{q.Seats[1].Worktree: tc.trusted}}, nil
			}
			o.RunAMQ, o.Wait = echoWakes(f, nil), 50*time.Millisecond
			res, err := Launch(context.Background(), q, o)
			if err != nil {
				t.Fatal(err)
			}
			res.proof.close()
			if !slices.Equal(hookRead, []string{"/fake/codex-home"}) || !slices.Equal(trustRead, []string{"/fake/codex-home/config.toml"}) {
				t.Fatalf("reads: hook %q, trust %q", hookRead, trustRead)
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
			var out bytes.Buffer
			Report(&out, res)
			if !strings.Contains(out.String(), tc.report) {
				t.Fatalf("report lacks %q:\n%s", tc.report, out.String())
			}
		})
	}
	// A squad with no Codex seat reads neither.
	q := testSquad(t)
	q.Seats = q.Seats[:1]
	f := &fakeAMQ{t: t, prepare: preparedResult()}
	o := launchOpts(t, f, nil)
	metHost(q, &o)
	o.CodexDialogHook = func(string) seatexec.CodexDialogHook {
		t.Fatal("read the Codex hook for a squad without a Codex seat")
		return seatexec.CodexDialogHook{}
	}
	o.CodexTrust = func(string) (hostcheck.CodexTrust, error) {
		t.Fatal("read the Codex trust for a squad without a Codex seat")
		return hostcheck.CodexTrust{}, nil
	}
	if _, err := Launch(context.Background(), q, o); err != nil {
		t.Fatal(err)
	}
}

// The Codex trust condition: the trusted entry for the worktree as given or
// with symlinks resolved; anything else, or an unreadable config, is unmet.
func TestCodexTrustsTheSeatWorktree(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "wt")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	realResolved, _ := filepath.EvalSymlinks(real)
	ct := hostcheck.ParseCodexTrust("[projects.\"" + realResolved + "\"]\ntrust_level = \"trusted\"\n")
	if ok, why := codexTrusts(ct, nil, "/c/config.toml", link); !ok || why != "" {
		t.Fatalf("a trusted worktree through a link: %t %q", ok, why)
	}
	if ok, why := codexTrusts(ct, nil, "/c/config.toml", t.TempDir()); ok || !strings.Contains(why, "trust_level") || !strings.Contains(why, "amq-squad trust") {
		t.Fatalf("an untrusted worktree: %t %q", ok, why)
	}
	if ok, why := codexTrusts(ct, os.ErrPermission, "/c/config.toml", link); ok || why != "cannot read /c/config.toml" {
		t.Fatalf("an unreadable Codex config: %t %q", ok, why)
	}
}

// The default Claude read (3.0.1 t6 in): the config dir claudeseat.Write
// produces wires the guard deny of AskUserQuestion and ExitPlanMode and
// closes the startup screens (claudeseat.StartupScreensClosed), so a Claude
// seat's wake turns on through Launch, and nothing is staged by the read.
// The screens-open branch is the injected "claude screens open" row above.
func TestClaudeWakeTurnsOnOnceTheStartupScreensAreClosed(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	c := scratchClaudeWakeConditions(ClaudePermissionMode, t.TempDir())
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Fatalf("the scratch config dir was left behind: %v", left)
	}
	if !c.ClaudeGuardWired || c.ClaudeGuardWhy != "" {
		t.Fatalf("the seat's config dir does not wire the guard deny: %+v", c)
	}
	if !c.ClaudeScreensClosed || c.ClaudeScreensWhy != "" {
		t.Fatalf("startup screens: %+v", c)
	}
	q := testSquad(t)
	f := &fakeAMQ{t: t, prepare: preparedResult()}
	o := launchOpts(t, f, nil)
	metHost(q, &o)
	o.ClaudeWakeConditions = nil // the default read
	res, err := Launch(context.Background(), q, o)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stage != "decisions_pending" {
		t.Fatalf("stage %s", res.Stage)
	}
	found := false
	for _, p := range res.Prepare.Preview.Participants {
		if p.Handle == "cto" {
			found = true
			if !p.Execution.RequireWake || p.Execution.Wake.Mode != "enabled" || p.Execution.Wake.AuditReason != "" {
				t.Fatalf("the Claude seat's wake: %+v", p.Execution)
			}
		}
	}
	if !found {
		t.Fatal("no cto participant in the preview")
	}
	if _, err := os.Stat(filepath.Join(q.SessionRoot(), "extensions")); !os.IsNotExist(err) {
		t.Fatalf("the Claude read staged something: %v", err)
	}
}
