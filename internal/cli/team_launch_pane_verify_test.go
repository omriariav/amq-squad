package cli

import (
	"fmt"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
	"strings"
	"testing"
)

// #577 finding 5: the #571 commit message and PR body both claimed the empty-pid refusal
// "keeps its own dedicated test rather than being inferred from these". IT DID NOT EXIST.
// Every fake returned 4242, so empty, zero, dead, fallback and attribution inputs were all
// untested while the PR asserted otherwise. Claiming a test exists is worse than the missing
// coverage, because it invites a reviewer to trust a check that is not there.
//
// This is that test, written against every input the reviewer named.
func TestVerifyPaneProcessLaunchedRefusesEveryUnprovenPane(t *testing.T) {
	const requested = "%7"

	found := func(pane string, pid int) func(string) tmuxpane.PaneInspection {
		return func(string) tmuxpane.PaneInspection {
			return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionFound, Pane: tmuxpane.TmuxPane{Pane: pane, PID: pid}}
		}
	}
	for _, tc := range []struct {
		name string
		// inspection is what the SHARED resolver reports; dead is the separate #{pane_dead} read.
		inspection func(string) tmuxpane.PaneInspection
		// dead is the #{pane_dead} VALUE; the fake composes the id echo around it.
		dead string
		// echoPane overrides the echoed id, for the vanished-pane substitution case.
		echoPane string
		// rawReply bypasses composition entirely, to model a reply shape production would get
		// from a REGRESSED format rather than one this fixture would ever build.
		rawReply string
		wantErr  string
	}{
		{
			name:       "empty pid means the command never started",
			inspection: found(requested, 0), dead: "0",
			wantErr: "no running process",
		},
		{
			// The launcher-shell case, now caught INSIDE the shared resolver: it verifies the
			// returned row is the requested pane and reports Gone rather than answering about a
			// different one. Round 3 re-derived this check badly; round 4 delegates it.
			name: "resolver reports the pane gone",
			inspection: func(string) tmuxpane.PaneInspection {
				return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionGone, Detail: "no such pane"}
			},
			dead:    "0\n",
			wantErr: "no longer exists",
		},
		{
			// remain-on-exit keeps a dead pane reporting its last pid, so a pid alone cannot
			// distinguish running from just-exited. This is why pane_dead stays a separate read.
			name:       "dead pane still reports its last pid",
			inspection: found(requested, 4242), dead: "1",
			wantErr: "is dead",
		},
		{
			// UNPROVEN is not absent. A transient iTerm2 -CC pause must fail CLOSED here, and
			// must NOT be reported as Gone -- the distinction is the resolver's whole point.
			name: "unavailable is unproven, not gone",
			inspection: func(string) tmuxpane.PaneInspection {
				return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionUnavailable, Detail: "control mode paused"}
			},
			dead:    "0\n",
			wantErr: "unproven evidence",
		},
		{
			name: "malformed is unproven",
			inspection: func(string) tmuxpane.PaneInspection {
				return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionMalformed, Detail: "empty row"}
			},
			dead:    "0\n",
			wantErr: "unproven evidence",
		},
		{
			name:       "blank liveness field cannot confirm the command is running",
			inspection: found(requested, 4242), dead: "",
			wantErr: "no liveness field",
		},
		{
			// #577 round 6 blocker 2: I DECLARED this falsifier in the round-6 authoring
			// message and never wrote the row. The rawReply field was declared and READ with
			// zero initializers, so the plumbing looked like evidence while proving nothing --
			// the same shape as claiming a test that did not exist, except the scaffolding made
			// it harder to notice.
			//
			// A bare "0" is what production receives if the format regresses to a single
			// field: one value, no identity echo. The row above covers a PRESENT-but-empty
			// second field, which is a different reply and a different guard.
			name: "reply with no echo at all is incomplete", rawReply: "0\n",
			inspection: found(requested, 4242),
			wantErr:    "incomplete liveness reply",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// F3 routes identity through the shared resolver, so the fake goes on THAT seam.
			// tmuxpane owns its exec seam privately, which is exactly why inspectPaneExact exists.
			oldInspect, oldOut := inspectPaneExact, tmuxOutputCommand
			t.Cleanup(func() { inspectPaneExact, tmuxOutputCommand = oldInspect, oldOut })
			inspectPaneExact = func(id string) tmuxpane.PaneInspection { return tc.inspection(id) }
			// pane_dead is still read directly, because paneListFormat does not carry it.
			// The echo is composed HERE so each row states only the liveness value it is about.
			// A row carrying the wire format would have to be edited again the next time the query
			// shape changes. Scope, grep-counted rather than recalled: 14 call sites across 5 files
			// use this helper. An earlier claim said "seven files" because it counted the STRING
			// pane_dead -- two of those files use it as a visibility-problem VALUE, not as a liveness
			// fake. Counting the string is not counting the thing.
			tmuxOutputCommand = func(_ string, args ...string) (string, error) {
				if tc.rawReply != "" {
					return tc.rawReply, nil
				}
				if tc.echoPane != "" {
					return tc.echoPane + "\t" + tc.dead, nil
				}
				return requested + "\t" + tc.dead, nil
			}

			pid, err := verifyPaneProcessLaunched(requested)
			if err == nil {
				t.Fatalf("case %q was ACCEPTED and returned pid %q; an unproven pane must refuse", tc.name, pid)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("refusal must name the reason %q so the operator knows what failed; got: %v", tc.wantErr, err)
			}
			if pid != "" {
				t.Errorf("a refused verification must return no pid; got %q", pid)
			}
		})
	}
}

// The companion property, asserted directly rather than inferred from the refusals above: a
// pane that IS the requested one, alive, with a real pid, is accepted. Without this the
// refusal table could all pass while the function rejected everything.
func TestVerifyPaneProcessLaunchedAcceptsTheRequestedLivePane(t *testing.T) {
	oldInspect, oldOut := inspectPaneExact, tmuxOutputCommand
	t.Cleanup(func() { inspectPaneExact, tmuxOutputCommand = oldInspect, oldOut })
	inspectPaneExact = func(string) tmuxpane.PaneInspection {
		return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionFound, Pane: tmuxpane.TmuxPane{Pane: "%7", PID: 4242}}
	}
	tmuxOutputCommand = func(string, ...string) (string, error) { return "%7\t0\n", nil }

	pid, err := verifyPaneProcessLaunched("%7")
	if err != nil {
		t.Fatalf("the requested live pane must verify: %v", err)
	}
	if pid != "4242" {
		t.Errorf("pid = %q, want 4242", pid)
	}
}

// An empty pane identity must refuse rather than ask tmux about nothing. tmux given an empty
// -t target resolves to the ACTIVE pane, which is the launcher -- so a missing identity would
// otherwise verify the launcher's own shell and call the worker launched.
func TestVerifyPaneProcessLaunchedRefusesAnEmptyIdentity(t *testing.T) {
	oldInspect := inspectPaneExact
	t.Cleanup(func() { inspectPaneExact = oldInspect })
	queried := false
	inspectPaneExact = func(string) tmuxpane.PaneInspection {
		queried = true
		return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionFound, Pane: tmuxpane.TmuxPane{Pane: "%1", PID: 4242}}
	}

	if _, err := verifyPaneProcessLaunched("  "); err == nil {
		t.Fatal("an empty pane identity must refuse")
	}
	if queried {
		t.Error("must refuse WITHOUT querying tmux: an empty -t target resolves to the active pane")
	}
}

// #577 round 4: the four cases the review named, table-driven, because the round-3 test could
// not see two of them -- it modelled every --create as adding a pane, so the fixture encoded the
// same assumption as the code it guarded.
//
// The launcher must deliver ONLY to a pane it can attribute to its own --create: novel pane AND
// novel window AND that window named for the role this create targeted. Novelty alone proves
// count, not causality.
func TestTmuxSessionAttributesTheCreatedPaneOrRefuses(t *testing.T) {
	for _, tc := range []struct {
		name string
		// sessions is the list-sessions reply; err is its error.
		sessions    string
		sessionsErr error
		// before/after are SEMANTIC pane rows; the fake renders them into whatever format
		// production requests. nil after = the modeled world is unchanged by --create.
		before      []paneRow
		after       []paneRow
		wantDeliver string // "" = must refuse
		wantErr     string
		// wantNoCreate asserts the refusal happened BEFORE --create. Refusing after creating
		// leaves an abandoned window that the next launch will then have to reason about.
		wantNoCreate bool
	}{
		{
			// F1: a fresh workstream must LAUNCH. The round-3 code refused here, making the
			// backend's first launch structurally impossible.
			//
			// #577 round 5 F4: the evidence here is a SUCCESSFUL list-sessions that does not name
			// this workstream -- real absent-session proof. The earlier version leaned on a
			// successful-EMPTY list-panes instead, so reverting only the vacancy fix stayed green:
			// the row passed for a reason unrelated to what it claimed to test.
			name: "fresh workstream launches", sessions: "other-session\n",
			after: []paneRow{{"issue-96", "%5", "@9", "cto"}}, wantDeliver: "%5",
		},
		{
			// F1: a transient probe failure on a POPULATED session must refuse, not be read as
			// fresh. Read-as-fresh is what let an operator's pane look novel and get destroyed.
			//
			// #577 round 5 F4: this row had NO populated pane fixture, so "populated" was in the
			// comment and not in the evidence. before is now a real operator pane, and the row
			// asserts --create was never invoked -- refusing AFTER creating would leave an
			// abandoned window behind.
			name: "transient probe failure on a populated session refuses", sessionsErr: errTmuxProbeFailed,
			before: []paneRow{{"issue-96", "%23", "@4", "cto"}}, wantErr: "cannot be enumerated", wantNoCreate: true,
		},
		{
			// F1's real falsifier, per the review: a PERMISSION-phrased error is documented in
			// internal/tmuxpane/tmux.go as meaning the socket is BLOCKED, not that the server is
			// absent. Treating it as vacancy is what could destroy an operator's pane.
			name: "permission-denied error is NOT vacancy", sessionsErr: errTmuxPermissionDenied,
			before: []paneRow{{"issue-96", "%23", "@4", "cto"}}, wantErr: "cannot be enumerated", wantNoCreate: true,
		},
		{
			// F2: the session EXISTS (list-sessions names it) and a SUCCESSFUL list-panes comes
			// back empty. A tmux session cannot have zero panes, so this is broken evidence and
			// must refuse BEFORE --create rather than proceed on a confident empty map.
			name: "existing session with an empty successful read refuses", sessions: "issue-96\n",
			before: nil, wantErr: "cannot be enumerated", wantNoCreate: true,
		},
		// #577 round 6 F3: the malformed-row coverage was SINGLE-CAUSE -- one row with an invalid
		// pane id, so reverting the short-row refusal OR deleting the window-id validation kept it
		// failing for the unrelated pane-id reason. Each mutation now has its own falsifier, with
		// exactly ONE defect per fixture.
		{
			name: "invalid PANE id refuses", sessions: "issue-96\n",
			before: []paneRow{{"issue-96", "not-a-pane-id", "@4", "cto"}}, wantErr: "cannot be enumerated", wantNoCreate: true,
		},
		{
			name: "invalid WINDOW id refuses", sessions: "issue-96\n",
			before: []paneRow{{"issue-96", "%23", "not-a-window-id", "cto"}}, wantErr: "cannot be enumerated", wantNoCreate: true,
		},
		{
			// A SHORT row: fewer fields than the format requested. Its pane and window ids are
			// both valid, so only the short-row refusal can catch it.
			name: "short row refuses", sessions: "issue-96\n",
			before: []paneRow{{Session: "issue-96", Pane: "%23"}}, wantErr: "cannot be enumerated", wantNoCreate: true,
		},
		{
			// #577 round 6 F1's falsifier: rows from ANOTHER session. Every id is shape-valid, so
			// only the session-name comparison can reject them -- which is the point, because
			// tmux's prefix/glob resolution is what could deliver them.
			name: "rows from a DIFFERENT session refuse", sessions: "issue-96\n",
			before: []paneRow{{"issue-96-old", "%23", "@4", "cto"}}, wantErr: "cannot be enumerated", wantNoCreate: true,
		},
		{
			// F1: no server at all IS provable vacancy -- no server, no panes.
			name: "no server running is proven vacancy", sessionsErr: errNoTmuxServer,
			after: []paneRow{{"issue-96", "%5", "@9", "cto"}}, wantDeliver: "%5",
		},
		{
			// F2 case A: --create selected an existing role window and created NOTHING. The
			// round-3 fixture could not express this at all.
			// #577 round 6 blocker 3: this row carried an explicit after in the OLD wire format,
			// which the new parser reads as a wrong-session row -- so the no-new-pane error was
			// unreachable and the row passed for an unrelated reason. Omitting after also exercises
			// the F4 retention fix directly: the modeled world keeps the operator pane.
			name: "created nothing refuses", sessions: "issue-96\n",
			before:  []paneRow{{"issue-96", "%23", "@4", "cto"}},
			wantErr: "no new pane in a new window",
		},
		{
			// F2 case B, the interleaving that defeated novelty: --create created nothing AND an
			// operator's pane arrived in a NEW window. Novel pane, novel window, wrong name.
			// Round 3 would have accepted this and killed the operator's pane.
			name: "operator pane in a new window refuses", sessions: "issue-96\n",
			before: []paneRow{{"issue-96", "%23", "@4", "cto"}}, after: []paneRow{{"issue-96", "%23", "@4", "cto"}, {"issue-96", "%77", "@8", "operator-scratch"}},
			wantErr: "no new pane in a new window",
		},
		{
			// The companion: a genuine create IS attributable, so the refusals above are not the
			// function rejecting everything.
			name: "genuine create is attributed", sessions: "issue-96\n",
			before: []paneRow{{"issue-96", "%23", "@4", "qa"}}, after: []paneRow{{"issue-96", "%23", "@4", "qa"}, {"issue-96", "%24", "@5", "cto"}}, wantDeliver: "%24",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldOut, oldRun, oldLook, oldSessionRun := tmuxOutputCommand, tmuxRunCommand, tmuxSessionLookPath, tmuxSessionRunCommand
			t.Cleanup(func() {
				tmuxOutputCommand, tmuxRunCommand, tmuxSessionLookPath, tmuxSessionRunCommand = oldOut, oldRun, oldLook, oldSessionRun
			})
			tmuxSessionLookPath = func(string) (string, error) { return "/usr/bin/tmux-session", nil }
			oldInspect := inspectPaneExact
			t.Cleanup(func() { inspectPaneExact = oldInspect })
			inspectPaneExact = func(id string) tmuxpane.PaneInspection {
				return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionFound, Pane: tmuxpane.TmuxPane{Pane: id, PID: 4242}}
			}

			created := false
			tmuxSessionRunCommand = func(name string, args ...string) error {
				if strings.Contains(strings.Join(args, " "), "--create") {
					created = true
				}
				return nil
			}
			tmuxOutputCommand = func(name string, args ...string) (string, error) {
				call := strings.Join(args, " ")
				switch {
				case strings.Contains(call, "list-sessions"):
					// After --create the session EXISTS, even on a fresh workstream -- that is what
					// the wrapper does. Returning the pre-create listing both times made the
					// post-create lookup see a vacant server and refuse a healthy launch: the
					// fixture, not the fix, was wrong.
					if created {
						return "issue-96\n", nil
					}
					return tc.sessions, tc.sessionsErr
				case strings.Contains(call, "list-panes"):
					// #577 round 6 blocker 1: this fake returned canned wire text regardless of the
					// requested -t and -F, so removing the exact "=" target changed nothing and
					// removing #{session_name} still received an invented session field. The fixture
					// was supplying both belts the code had stopped asking for.
					//
					// The target is PINNED: anything other than the exact-match form is an error, so
					// dropping "=" fails every positive row. The rows are SEMANTIC and the wire text is
					// rendered from the requested format, so dropping #{session_name} shifts every
					// column left -- and the row then fails at the SESSION EQUALITY check, because
					// fields[0] holds a pane id rather than the session name. Traced rather than
					// assumed: my earlier comment credited the field-count guard, which is not the
					// branch that actually rejects it.
					// Scope, target and format are ALL decided by validateListPanesInvocation, the
					// one decider the named rejection tests also call. The fake holds no guard of
					// its own: a guard only the fixture runs is one no test can falsify, which is
					// exactly how r9's first submission shipped a rejection test that passed with
					// the rejection deleted.
					//
					// The fake refuses any invocation real tmux would answer differently, so the
					// fixture cannot supply a belt production has stopped asking for.
					format, formatErr := validateListPanesInvocation(args, "=issue-96")
					if formatErr != nil {
						return "", fmt.Errorf("%w in %s", formatErr, call)
					}
					rows := tc.before
					// #577 round 6 F4: when after is unset the modeled world must not EMPTY itself
					// post-create. A populated before with no after made the operator pane vanish if
					// production wrongly proceeded, so the fixture flattered the code: the pane it
					// should have protected was no longer there to protect.
					if created && tc.after != nil {
						rows = tc.after
					}
					return renderPaneRows(rows, format), nil
				case strings.Contains(call, "#{pane_dead}"):
					// Delegated to the shared helper so the liveness wire format lives in ONE place.
					// #577 round 5 restored the id echo here; a local literal would have needed
					// editing at all 14 call sites across 5 files, which is how they drifted at once.
					return fakePaneIdentityReply(args), nil
				}
				return "", fmt.Errorf("unexpected output command: %s %s", name, call)
			}
			var delivered []string
			tmuxRunCommand = func(name string, args ...string) error {
				for i, a := range args {
					if a == "respawn-pane" {
						for j := i; j < len(args)-1; j++ {
							if args[j] == "-t" {
								delivered = append(delivered, args[j+1])
							}
						}
					}
				}
				return nil
			}

			err := runTmuxSessionLaunchPlan(tmuxSessionLaunchPlan{
				Workstream: "issue-96",
				Panes:      []teamLaunchPane{{Role: "cto", CWD: "/repo", Command: "agent up"}},
			})

			if tc.wantDeliver == "" {
				if err == nil {
					t.Fatalf("must refuse; delivered to %v", delivered)
				}
				if len(delivered) != 0 {
					t.Fatalf("refused but STILL delivered to %v: respawn-pane -k would destroy a pane this launcher does not own", delivered)
				}
				if tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("refusal must say why (%q): %v", tc.wantErr, err)
				}
				if tc.wantNoCreate && created {
					t.Error("refused AFTER invoking --create: an abandoned window is operator litter and the next launch must then reason about it")
				}
				return
			}
			if err != nil {
				t.Fatalf("a launch this launcher can attribute must proceed: %v", err)
			}
			if len(delivered) != 1 || delivered[0] != tc.wantDeliver {
				t.Errorf("delivered to %v, want exactly [%s]", delivered, tc.wantDeliver)
			}
		})
	}
}

// #577 finding 4: respawn-pane runs the command under a fresh non-interactive shell, so the
// operator's profile PATH is absent. The delivered command must carry the launcher's PATH
// explicitly, because a version-managed or ~/.local/bin amq-squad is otherwise unresolvable.
//
// Asserted on the real wrapper, with a PATH value that could only have come from the
// launcher's environment.
func TestDeliveredCommandCarriesTheLauncherPATH(t *testing.T) {
	t.Setenv("PATH", "/opt/version-manager/bin:/usr/bin")

	got := withTmuxTargetEnv("%9", "cd /repo && amq-squad agent up codex")

	if !strings.Contains(got, "PATH=") {
		t.Fatalf("delivered command must export PATH; respawn-pane inherits only tmux env:\n%s", got)
	}
	if !strings.Contains(got, "/opt/version-manager/bin") {
		t.Errorf("must carry the LAUNCHER's PATH, not a default:\n%s", got)
	}
	// The export must precede the command, or the binary resolves before PATH is set.
	if strings.Index(got, "PATH=") > strings.Index(got, "amq-squad agent up") {
		t.Errorf("PATH export must come BEFORE the command:\n%s", got)
	}
}

// fakePaneIdentityReply answers a tmux -p format query by DERIVING the reply from the format
// string production actually sent.
//
// #577 round 6 F2: the previous version manufactured "<pane_id>\t<dead>" regardless of what was
// requested, so reverting production to a bare "#{pane_dead}" -- one field, no identity echo --
// stayed GREEN. The fixture supplied the identity the code had stopped asking for, which made
// the echo test I added in round 5 unable to detect the very regression it exists for.
//
// Deriving from the request means a production format change CHANGES THE REPLY SHAPE, so the
// incomplete-reply and echo-compare guards see it.
func fakePaneIdentityReply(args []string) string {
	target := ""
	format := ""
	for i, a := range args {
		if a == "-t" && i+1 < len(args) {
			target = args[i+1]
		}
		if strings.Contains(a, "#{") {
			format = a
		}
	}
	if format == "" {
		return ""
	}
	// Emit exactly the fields requested, in the order requested, on the separator requested.
	// Unknown fields render EMPTY, as real tmux does -- modelling anything else would be the
	// fixture-lag disease inverted. That is not a silent skip BECAUSE production refuses on a
	// blank or incomplete reply, guarded by the "blank liveness field" and "reply with no echo
	// at all" rows. Deleting either side breaks this named contract.
	values := map[string]string{
		"#{pane_id}":   target,
		"#{pane_pid}":  "4242",
		"#{pane_dead}": "0",
	}
	parts := strings.Split(format, "\t")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if v, ok := values[strings.TrimSpace(part)]; ok {
			out = append(out, v)
			continue
		}
		out = append(out, "")
	}
	return strings.Join(out, "\t") + "\n"
}

// stubExactPaneInspection installs a permissive shared-resolver fake: whatever pane is asked
// about is Found, alive, with a fixed pid.
//
// Every launch test needs it now, because #577 round 4 routed pane identity through
// tmuxpane.InspectPaneExactByID, whose own exec seam is package-private -- so a test that
// fakes only tmuxOutputCommand reaches the REAL resolver and gets Unavailable/Malformed.
// Naming it once beats nine copies drifting apart.
func stubExactPaneInspection(t *testing.T) {
	t.Helper()
	old := inspectPaneExact
	t.Cleanup(func() { inspectPaneExact = old })
	inspectPaneExact = func(id string) tmuxpane.PaneInspection {
		return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionFound, Pane: tmuxpane.TmuxPane{Pane: id, PID: 4242}}
	}
}

// Distinct sentinels so the table can express "the tmux server is absent" (provable vacancy)
// separately from "the probe failed for some other reason" (a refusal). #577 round 4: conflating
// them is exactly what made a failed probe a licence to destroy an operator's pane.
var (
	errNoTmuxServer    = fmt.Errorf("no server running on /tmp/tmux-501/default")
	errTmuxProbeFailed = fmt.Errorf("tmux: connection interrupted")
	// The phrase internal/tmuxpane/tmux.go documents as PERMISSION DENIED, not absence. My
	// round-4 matcher accepted it as proof of no server.
	errTmuxPermissionDenied = fmt.Errorf("error connecting to /tmp/tmux-501/default (Operation not permitted)")
)

// #577 round 5 F3's falsifier. None of the repaired fixtures modelled the TRANSITION: they all
// return Found plus dead=0, so the deleted identity echo was invisible to every one of them.
//
// Here the resolver reports the requested pane Found, and the liveness read then answers about
// a DIFFERENT pane -- exactly what display-message does when the target vanished, because it
// falls back to the active pane instead of erroring. Without the echo comparison the launcher
// reads the launcher's own pane_dead=0 and counts a fast-exited worker as launched.
func TestVerifyRefusesWhenLivenessAnswersAboutADifferentPane(t *testing.T) {
	oldInspect, oldOut := inspectPaneExact, tmuxOutputCommand
	t.Cleanup(func() { inspectPaneExact, tmuxOutputCommand = oldInspect, oldOut })
	inspectPaneExact = func(id string) tmuxpane.PaneInspection {
		return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionFound, Pane: tmuxpane.TmuxPane{Pane: id, PID: 4242}}
	}
	// The fallback: a live, alive pane that is NOT the one we asked about.
	tmuxOutputCommand = func(string, ...string) (string, error) { return "%1\t0\n", nil }

	pid, err := verifyPaneProcessLaunched("%7")
	if err == nil {
		t.Fatalf("liveness answered about %%1 while %%7 was requested and it was ACCEPTED, returning pid %q", pid)
	}
	if !strings.Contains(err.Error(), "DIFFERENT pane") {
		t.Errorf("refusal must name the substitution so the operator knows the pane vanished: %v", err)
	}
	if pid != "" {
		t.Errorf("a refused verification must return no pid; got %q", pid)
	}
}

// paneRow is a SEMANTIC list-panes row. Tests state what exists; the fake renders whatever
// wire format production asked for.
//
// #577 round 6 blocker 1: canned wire text let a fixture supply columns the code no longer
// requested, so removing #{session_name} from production changed nothing observable. A semantic
// row cannot do that -- if the format omits a field, the rendered row omits it too.
type paneRow struct {
	Session string
	Pane    string
	Window  string
	Name    string
}

// renderPaneRows emits exactly the fields the requested format names, in order, space-separated
// as tmux does for this format.
func renderPaneRows(rows []paneRow, format string) string {
	if format == "" {
		return ""
	}
	var out strings.Builder
	for _, row := range rows {
		values := map[string]string{
			"#{session_name}": row.Session,
			"#{pane_id}":      row.Pane,
			"#{window_id}":    row.Window,
			"#{window_name}":  row.Name,
		}
		parts := strings.Fields(format)
		rendered := make([]string, 0, len(parts))
		for _, part := range parts {
			// Unknown variables render EMPTY, which is what real tmux does. Production's
			// field-count and shape checks are the guards; see the blank/incomplete rows.
			rendered = append(rendered, values[part])
		}
		out.WriteString(strings.Join(rendered, " "))
		out.WriteString("\n")
	}
	return out.String()
}

// flagValue is extraction as OUT-OF-BAND state: the value, plus whether one was present at all.
//
// #577 round 8: the previous version marked a dangling flag with an in-band sentinel STRING. I
// had rejected the empty string precisely to avoid collision and then chose another value from
// the SAME DOMAIN, so the decider could not distinguish a real dangling flag from the legitimate
// (if weird) invocation -F "<dangling flag with no value>", which real tmux accepts. An in-band
// sentinel collides with its domain by construction; no better magic string fixes that.
//
// hasValue answers a question the value cannot: "was there a value?" is not a fact about the
// value, so it does not belong in the value.
type flagValue struct {
	Value    string
	HasValue bool
}

// listPanesArgv is the structured result of ONE option-aware walk of a list-panes invocation.
//
// Both -F and -t use the SAME representation, flagValue{Value, HasValue}: a value-less flag is
// still recorded, so it counts toward multiplicity, and its absence of a value is carried out of
// band rather than by any in-domain marker. Without counting it, "exactly one" would be a claim
// about value-bearing pairs rather than about flags (r7 F2).
//
// #577 round 9 (second pass): the previous design had one shared extractor but THREE independent
// CALLS, each walking argv knowing only its own flag. Shared code is not shared parsing. The
// falsifying inputs, all of which real tmux reads differently than independent scans do:
//
//	[-s -F -t -t =issue-96]   -F consumes "-t", so format="-t" and target="=issue-96".
//	                          An independent -t scan re-reads -F's consumed value and reports
//	                          target="-t", skipping the real target entirely.
//	[-F -s -t =issue-96]      -F consumes "-s", so scope is ABSENT. An independent hasFlag sees
//	                          the consumed token and falsely reports scope present.
//	[-s -t -F -F #{pane_id}]  -t consumes "-F", so target="-F" and format="#{pane_id}".
//
// ROLE IS A PROPERTY OF THE WHOLE ARGV, not of a token or of one flag's view of it. That is why
// no number of per-flag scans can be made correct: each is missing the information that decides
// the question. One walk that knows every option's arity is the only shape that can answer it.
//
// This is the fourth level of the same collision in this PR: representation (r8), iteration within
// one flag (r9 first pass), and now iteration ACROSS flags. Each fix addressed the level it was
// shown and left the one above.
type listPanesArgv struct {
	Scope   bool        // -s, boolean
	Targets []flagValue // -t, consumes one token
	Formats []flagValue // -F, consumes one token
	Unknown []string    // tokens that are neither options nor consumed values
}

// parseListPanesArgv walks the argv ONCE, honouring each option's arity and skipping every
// consumed value, and returns the single structured result every guard consumes.
func parseListPanesArgv(args []string) listPanesArgv {
	var parsed listPanesArgv
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-s":
			parsed.Scope = true
		case "-t", "-F":
			flag := args[i]
			if i+1 >= len(args) {
				// Value-less: counted, absence carried OUT OF BAND (r8) so no value can
				// imitate it.
				if flag == "-t" {
					parsed.Targets = append(parsed.Targets, flagValue{})
				} else {
					parsed.Formats = append(parsed.Formats, flagValue{})
				}
				continue
			}
			value := flagValue{Value: args[i+1], HasValue: true}
			if flag == "-t" {
				parsed.Targets = append(parsed.Targets, value)
			} else {
				parsed.Formats = append(parsed.Formats, value)
			}
			// CONSUMED. Skipping is what stops a value that spells another option from being
			// read as one -- by ANY guard, because there is only one walk.
			i++
		default:
			parsed.Unknown = append(parsed.Unknown, args[i])
		}
	}
	return parsed
}

// validateListPanesInvocation is the SINGLE decider: it consumes one parse and answers every
// question the fixture asks of a list-panes invocation -- scope, target, format -- returning the
// requested format when the invocation is one real tmux would answer as this fixture models it.
//
// #577 round 9 GATE REJECT (dev-2), and the reject was right twice over:
//
//  1. BUILD. My rewrite deleted paneListFormatArg while four call sites still called it. gofmt is
//     clean on a tree that cannot compile, which is now the fourth time in this milestone that
//     formatting has been mistaken for correctness. Only a compiler decides that.
//  2. EVIDENCE. TestListPanesFakeRejectsScopeThatIsOnlyAConsumedValue called parseListPanesArgv
//     and asserted parsed.Scope. It never invoked the fake, so deleting the fake's `if
//     !parsed.Scope` guard left the test that NAMES that rejection green. It was also a verbatim
//     duplicate of row two of the parser table -- same argv, same assertion -- so it added no
//     coverage while reading as though it added the decisive coverage. That is the r7
//     plumbing-vs-decision failure returning at the level above the one I fixed.
//
// The structural fix, not the instance fix: there is ONE parse and ONE decision function over its
// result, and the fake and the named rejection tests all call THIS. A guard the tests do not call
// is a guard nothing protects, so the only way to keep that property is to leave no second place
// where the decision could live.
//
// wantTarget is a parameter because the pinned exact-match target is a property of the FIXTURE's
// modeled world, not of tmux. Passing it keeps one decider instead of forking the function.
func validateListPanesInvocation(args []string, wantTarget string) (string, error) {
	parsed := parseListPanesArgv(args)

	// -s SCOPE (r7 F1). Real tmux without -s enumerates only ONE window, so an existing role
	// window could be missing from the before-snapshot and then attributed to this create and
	// destroyed. Decided on the walked result, so -s appearing only as another option's consumed
	// value is correctly ABSENT.
	if !parsed.Scope {
		return "", fmt.Errorf("list-panes must pass -s to enumerate the whole session; without it tmux returns one window only")
	}

	// Three independent properties: exactly ONE -t, it HAS a value, and that value is the
	// exact-match target. Presence is out of band, so no target or format string can imitate an
	// absent one.
	switch {
	case len(parsed.Targets) != 1:
		return "", fmt.Errorf("list-panes must pass exactly one -t; got %d (%v)", len(parsed.Targets), parsed.Targets)
	case !parsed.Targets[0].HasValue:
		return "", fmt.Errorf("list-panes passed -t with no value")
	case parsed.Targets[0].Value != wantTarget:
		return "", fmt.Errorf("list-panes must target the exact-match form %q; got %q", wantTarget, parsed.Targets[0].Value)
	}

	switch {
	case len(parsed.Formats) != 1:
		return "", fmt.Errorf("list-panes must pass exactly one -F; got %d (%v)", len(parsed.Formats), parsed.Formats)
	case !parsed.Formats[0].HasValue:
		return "", fmt.Errorf("list-panes passed -F with no value")
	}
	return parsed.Formats[0].Value, nil
}

// This test must falsify the DECISION, not the plumbing -- the r7 lesson, re-applied one level up
// after r9's first submission failed it again.
//
// It calls validateListPanesInvocation, the SAME function the list-panes fake calls, so deleting
// any clause inside it fails a row here BY NAME. Asserting on parseListPanesArgv instead would
// prove only that a struct field was populated, which is what the rejected version did.
//
// EVERY ROW IS WELL-FORMED EXCEPT IN THE ONE DIMENSION UNDER TEST. This matters more than it
// looks: the rejected version's "duplicate -F" row passed no -s and no -t at all, so under a
// combined decider it would refuse on SCOPE and still satisfy an "exactly one" assertion only by
// accident of message wording. A row that can be rejected for a reason other than the one it
// names is evidence about nothing. Traced clause by clause, not assumed.
func TestListPanesInvocationDeciderRejectsEveryMalformedDimension(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			// The case that slipped a count-only guard: ONE flag, no value.
			name: "lone dangling -F", args: []string{"list-panes", "-s", "-t", "=issue-96", "-F"},
			wantErr: "-F with no value",
		},
		{
			name: "duplicate -F", args: []string{"list-panes", "-s", "-t", "=issue-96", "-F", "#{pane_id}", "-F", "#{window_id}"},
			wantErr: "exactly one -F",
		},
		{
			name: "no -F at all", args: []string{"list-panes", "-s", "-t", "=issue-96"},
			wantErr: "exactly one -F",
		},
		{
			// r7 F1, now falsifiable: delete the scope clause and this row goes green.
			name: "no -s at all", args: []string{"list-panes", "-t", "=issue-96", "-F", "#{pane_id}"},
			wantErr: "must pass -s",
		},
		{
			// The row dev-2 named. -F consumes "-s", so scope is ABSENT however much the token
			// appears in argv. This replaces TestListPanesFakeRejectsScopeThatIsOnlyAConsumedValue,
			// which asserted the same argv against the PARSER and therefore duplicated row two of
			// the parser table while proving nothing about the rejection it was named for.
			name: "scope present only as a consumed value", args: []string{"list-panes", "-F", "-s", "-t", "=issue-96"},
			wantErr: "must pass -s",
		},
		{
			name: "dangling -t", args: []string{"list-panes", "-s", "-F", "#{pane_id}", "-t"},
			wantErr: "-t with no value",
		},
		{
			name: "duplicate -t", args: []string{"list-panes", "-s", "-t", "=issue-96", "-t", "=issue-96", "-F", "#{pane_id}"},
			wantErr: "exactly one -t",
		},
		{
			// Not the exact-match form: tmux resolves a bare name by prefix and then by glob, so
			// an unanchored target can answer for a DIFFERENT session.
			name: "target is not the exact-match form", args: []string{"list-panes", "-s", "-t", "issue-96", "-F", "#{pane_id}"},
			wantErr: "exact-match form",
		},
		{
			// CROSS-FLAG, rejection side. -t consumes the second "-t", so the target VALUE is the
			// literal "-t" -- extraction succeeds and POLICY refuses, because "-t" is not the
			// exact-match target. Recorded as a rejection rather than an acceptance precisely
			// because extraction succeeding is not the contract being tested here; a bare
			// ["-t","-t"] is an extraction fact, and it lives at the parser level.
			name:    "target value spelling its own flag is extracted and then REFUSED",
			args:    []string{"list-panes", "-s", "-t", "-t", "-F", "#{pane_id}"},
			wantErr: "exact-match form",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := validateListPanesInvocation(tc.args, "=issue-96")
			if err == nil {
				t.Fatalf("must be rejected; got format %q", got)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("refusal must name the dimension that failed (%q), or the row proves only "+
					"that SOMETHING refused: %v", tc.wantErr, err)
			}
		})
	}

	// ANTI-VACUITY: well-formed invocations are ACCEPTED and their format returned unchanged.
	// Without these, a decider that refused everything would pass every row above.
	for _, tc := range []struct {
		name       string
		args       []string
		wantFormat string
	}{
		{
			name: "canonical invocation", args: []string{"list-panes", "-s", "-t", "=issue-96", "-F", "#{pane_id}"},
			wantFormat: "#{pane_id}",
		},
		{
			// CROSS-FLAG, acceptance side. -F consumes the second "-F", so the FORMAT is the
			// literal "-F" and the invocation is otherwise canonical. Real tmux reads it exactly
			// this way, so the decider must accept it: the cross-flag walk must not turn a
			// legitimate-if-odd value into a refusal. Note this needs a real -s and a real -t in
			// the argv -- a bare ["-F","-F"] would refuse on scope and prove nothing about format.
			name: "format value spelling its own flag is accepted", args: []string{"list-panes", "-s", "-t", "=issue-96", "-F", "-F"},
			wantFormat: "-F",
		},
	} {
		t.Run("accepts/"+tc.name, func(t *testing.T) {
			got, err := validateListPanesInvocation(tc.args, "=issue-96")
			if err != nil {
				t.Fatalf("a well-formed invocation must be accepted: %v", err)
			}
			if got != tc.wantFormat {
				t.Errorf("format must be returned unchanged; got %q, want %q", got, tc.wantFormat)
			}
		})
	}
}

// #577 round 8: the in-band sentinel made this argv indistinguishable from a dangling flag. Real
// tmux accepts it as a literal (if useless) format value, so the decider must too -- an in-band
// marker cannot tell a domain value from an absence, which is why presence moved out of band.
//
// WHAT THIS ROW PROVES, narrowly: it falsifies REINTRODUCTION OF THE OLD SENTINEL specifically.
// It cannot prevent every future magic string, because it names one. The general property is
// carried by the representation itself -- flagValue keeps presence out of the value domain -- and
// a new in-band marker would need its own row. Stated so the row is not mistaken for a guard
// against the whole class.
func TestListPanesInvocationAcceptsAFormatEqualToTheOldSentinel(t *testing.T) {
	const looksLikeTheOldSentinel = "<dangling flag with no value>"

	got, err := validateListPanesInvocation([]string{"list-panes", "-s", "-t", "=issue-96", "-F", looksLikeTheOldSentinel}, "=issue-96")
	if err != nil {
		t.Fatalf("a literal format value must be accepted even when it spells the old sentinel: %v", err)
	}
	if got != looksLikeTheOldSentinel {
		t.Errorf("format must pass through unchanged; got %q", got)
	}
}

// EXTRACTION FACTS ONLY -- NOT POLICY. This table asserts what the argv SAID: which token became
// which flag's value once each option's arity is honoured. It deliberately makes no claim about
// whether an invocation is acceptable; every acceptance and rejection decision belongs to
// validateListPanesInvocation and is asserted against that function.
//
// The layer split is the r9 gate lesson made structural: a parser-direct assertion that NAMES a
// rejection proves a struct field was populated and nothing more, which is how a test survived
// deletion of the guard it was named for. Rows here are named for the fact, never for a verdict.
//
// #577 round 9 (second pass): ROLE across flags. Each row is an argv real tmux reads one way and
// independent per-flag scans read another, so each falsifies the independent-scan design rather
// than a detail of it.
func TestParseListPanesArgvAssignsRolesByPosition(t *testing.T) {
	for _, tc := range []struct {
		name        string
		args        []string
		wantScope   bool
		wantTarget  string
		wantFormat  string
		wantTargets int
		wantFormats int
	}{
		{
			// -F consumes "-t"; the REAL target follows. An independent -t scan reports
			// target="-t" and never sees =issue-96.
			name:      "format value spelling -t does not become a target",
			args:      []string{"list-panes", "-s", "-F", "-t", "-t", "=issue-96"},
			wantScope: true, wantFormat: "-t", wantTarget: "=issue-96",
			wantTargets: 1, wantFormats: 1,
		},
		{
			// -F consumes "-s", so scope is ABSENT. An independent hasFlag reports it present.
			name:      "scope flag appearing only as a consumed value is NOT scope",
			args:      []string{"list-panes", "-F", "-s", "-t", "=issue-96"},
			wantScope: false, wantFormat: "-s", wantTarget: "=issue-96",
			wantTargets: 1, wantFormats: 1,
		},
		{
			// The symmetric case: -t consumes "-F".
			name:      "target value spelling -F does not become a format",
			args:      []string{"list-panes", "-s", "-t", "-F", "-F", "#{pane_id}"},
			wantScope: true, wantTarget: "-F", wantFormat: "#{pane_id}",
			wantTargets: 1, wantFormats: 1,
		},
		{
			// Same-flag case from the first pass, kept: one option whose value spells itself.
			name:       "one option whose value spells its own flag",
			args:       []string{"-F", "-F"},
			wantFormat: "-F", wantFormats: 1,
		},
		{
			// The -t twin, kept HERE and only here. Extraction yields target "-t"; whether that
			// is acceptable is policy, and the decider refuses it (wantTarget equality). Asserting
			// this argv as an acceptance anywhere would be wrong.
			name:       "the -t twin: one target whose value spells its own flag",
			args:       []string{"-t", "-t"},
			wantTarget: "-t", wantTargets: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parseListPanesArgv(tc.args)
			if got.Scope != tc.wantScope {
				t.Errorf("Scope = %v, want %v -- a consumed value must not be read as an option", got.Scope, tc.wantScope)
			}
			if len(got.Targets) != tc.wantTargets {
				t.Errorf("got %d targets (%v), want %d", len(got.Targets), got.Targets, tc.wantTargets)
			} else if tc.wantTarget != "" && (!got.Targets[0].HasValue || got.Targets[0].Value != tc.wantTarget) {
				t.Errorf("target = %+v, want %q", got.Targets[0], tc.wantTarget)
			}
			if len(got.Formats) != tc.wantFormats {
				t.Errorf("got %d formats (%v), want %d", len(got.Formats), got.Formats, tc.wantFormats)
			} else if tc.wantFormat != "" && (!got.Formats[0].HasValue || got.Formats[0].Value != tc.wantFormat) {
				t.Errorf("format = %+v, want %q", got.Formats[0], tc.wantFormat)
			}
		})
	}
}

// DELETED: TestListPanesFakeRejectsScopeThatIsOnlyAConsumedValue.
//
// It named a rejection by the fake and never called the fake or any decider -- it re-asserted row
// two of the parser table against parseListPanesArgv. Two defects in one test: it was a verbatim
// duplicate of coverage that already existed, and its NAME claimed the coverage that did not.
// Deleting the fake's scope guard left it green.
//
// Its property now lives as the "scope present only as a consumed value" row of
// TestListPanesInvocationDeciderRejectsEveryMalformedDimension, where it runs against the same
// function the fake calls and therefore fails when that clause is removed.
