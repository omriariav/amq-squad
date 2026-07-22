package cli

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/liveidentity"
	"github.com/omriariav/amq-squad/v2/internal/runtimecontrol"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// R5 (#505 review D3): a client name usable as a -t selector must not start
// with "-", which tmux would parse as a flag rather than the target value.
func TestValidateTmuxControlClientNameRejectsLeadingDash(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  bool
	}{
		{name: "ordinary tty", value: "/dev/ttys001", want: true},
		{name: "leading dash", value: "-t", want: false},
		{name: "leading dash bare", value: "-", want: false},
		{name: "empty", value: "", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateTmuxControlClientName(tc.value)
			if (err == nil) != tc.want {
				t.Fatalf("validateTmuxControlClientName(%q) err=%v, want ok=%t", tc.value, err, tc.want)
			}
		})
	}
}

func TestContinueExactTmuxControlClientRejectsUnsafeSelectorAndDocumentsVersionFloor(t *testing.T) {
	if err := continueExactTmuxControlClient(tmuxControlContinueTarget{Client: "-x", PaneID: "%3"}, func(string, ...string) error {
		t.Fatal("run must not be called for an unsafe selector")
		return nil
	}); err == nil || !strings.Contains(err.Error(), "not a safe -t selector") {
		t.Fatalf("unsafe client selector error = %v", err)
	}
	if err := continueExactTmuxControlClient(tmuxControlContinueTarget{Client: "/dev/ttys001", PaneID: "%3"}, func(string, ...string) error {
		return fmt.Errorf("unknown option -- A")
	}); err == nil || !strings.Contains(err.Error(), "tmux 3.2 or later") {
		t.Fatalf("version-floor hint missing from error: %v", err)
	}
}

func controlContinueFixture(t *testing.T) (string, memberRuntime, liveidentity.Result) {
	t.Helper()
	project := t.TempDir()
	terminal := &launch.TerminalInfo{Backend: runtimecontrol.BackendTmux, Target: "new-window", Session: "terminal-s", WindowID: "@2", PaneID: "%3"}
	rec := launch.Record{
		Schema: launch.SchemaVersion, CWD: project, Binary: "codex", Session: "workstream", SharedWorkstream: true,
		Handle: "qa", Role: "qa", TeamProfile: team.DefaultProfile, TeamHome: project, Root: filepath.Join(project, ".agent-mail", "workstream"),
		AgentPID: 1234, StartedAt: time.Now().UTC(), Tmux: &launch.TmuxInfo{Target: terminal.Target, Session: terminal.Session, WindowID: terminal.WindowID, PaneID: terminal.PaneID}, Terminal: terminal,
	}
	mr := memberRuntime{Member: team.Member{Role: "qa", Handle: "qa", Binary: "codex", Session: "workstream"}, Profile: team.DefaultProfile, Handle: "qa", CWD: project, HasRecord: true, Record: rec}
	canonical, err := liveidentity.CanonicalProject(project)
	if err != nil {
		t.Fatal(err)
	}
	verified := liveidentity.Verified{
		Key: liveidentity.Key{Project: canonical, Profile: team.DefaultProfile, Session: "workstream", Handle: "qa"}, Role: "qa",
		Terminal: liveIdentityTerminal(rec),
	}
	return project, mr, liveidentity.Result{SchemaVersion: liveidentity.SchemaVersion, Verified: &verified}
}

func TestResolveExactTmuxControlContinueFailsClosed(t *testing.T) {
	project, mr, verified := controlContinueFixture(t)
	goodOutput := func(_ string, args ...string) (string, error) {
		switch args[0] {
		case "list-clients":
			return "/dev/ttys001\t/dev/ttys001\t1\tcontrol-mode,pause-after=120\tterminal-s\n", nil
		case "display-message":
			return "%3\t@2\tterminal-s\n", nil
		default:
			return "", fmt.Errorf("unexpected tmux args: %v", args)
		}
	}
	goodDeps := tmuxControlContinueDeps{
		Verify: func(_, _, _, _ string) (liveidentity.Result, error) { return verified, nil },
		Output: goodOutput,
	}
	target, err := resolveExactTmuxControlContinue(project, team.DefaultProfile, "workstream", "qa", "/dev/ttys001", mr, goodDeps)
	if err != nil || target.Client != "/dev/ttys001" || target.Session != "terminal-s" || target.WindowID != "@2" || target.PaneID != "%3" {
		t.Fatalf("exact control target=%+v err=%v", target, err)
	}

	tests := []struct {
		name     string
		client   string
		mutateMR func(*memberRuntime)
		verify   func(string, string, string, string) (liveidentity.Result, error)
		output   func(string, ...string) (string, error)
		want     string
	}{
		{name: "wrong caller client", client: "/dev/ttys999", output: goodOutput, want: "differs from unique resolved client"},
		{name: "zero clients", client: "/dev/ttys001", output: func(_ string, args ...string) (string, error) {
			if args[0] == "list-clients" {
				return "/dev/ttys001\t/dev/ttys001\t0\tattached\tterminal-s\n", nil
			}
			return "%3\t@2\tterminal-s\n", nil
		}, want: "has 0 control-mode clients"},
		{name: "multiple clients", client: "/dev/ttys001", output: func(_ string, args ...string) (string, error) {
			if args[0] == "list-clients" {
				return "/dev/ttys001\t/dev/ttys001\t1\tcontrol-mode\tterminal-s\n/dev/ttys002\t/dev/ttys002\t1\tcontrol-mode\tterminal-s\n", nil
			}
			return "%3\t@2\tterminal-s\n", nil
		}, want: "has 2 control-mode clients"},
		{name: "malformed shifted client row", client: "/dev/ttys001", output: func(_ string, args ...string) (string, error) {
			if args[0] == "list-clients" {
				return "\t/dev/ttys001\t1\tterminal-s\n", nil
			}
			return "%3\t@2\tterminal-s\n", nil
		}, want: "unexpected tmux list-clients row"},
		{name: "contradictory control fields", client: "/dev/ttys001", output: func(_ string, args ...string) (string, error) {
			if args[0] == "list-clients" {
				return "/dev/ttys001\t/dev/ttys001\t0\tattached,control-mode\tterminal-s\n", nil
			}
			return "%3\t@2\tterminal-s\n", nil
		}, want: "contradictory control-mode client row"},
		{name: "wrong terminal session", client: "/dev/ttys001", output: func(_ string, args ...string) (string, error) {
			if args[0] == "list-clients" {
				return "/dev/ttys001\t/dev/ttys001\t1\tcontrol-mode\tother-terminal\n", nil
			}
			return "%3\t@2\tterminal-s\n", nil
		}, want: "has 0 control-mode clients"},
		{name: "wrong pane", client: "/dev/ttys001", output: func(_ string, args ...string) (string, error) {
			if args[0] == "list-clients" {
				return "/dev/ttys001\t/dev/ttys001\t1\tcontrol-mode\tterminal-s\n", nil
			}
			return "%4\t@2\tterminal-s\n", nil
		}, want: "pane inspection differs"},
		{name: "verified terminal mismatch", client: "/dev/ttys001", output: goodOutput, verify: func(_, _, _, _ string) (liveidentity.Result, error) {
			copy := *verified.Verified
			copy.Terminal.PaneID = "%99"
			return liveidentity.Result{SchemaVersion: liveidentity.SchemaVersion, Verified: &copy}, nil
		}, want: "authoritative live identity differs"},
		{name: "record session mismatch", client: "/dev/ttys001", output: goodOutput, mutateMR: func(m *memberRuntime) { m.Record.Session = "other" }, want: "managed launch record does not match"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			candidate := mr
			if tc.mutateMR != nil {
				tc.mutateMR(&candidate)
			}
			verify := tc.verify
			if verify == nil {
				verify = goodDeps.Verify
			}
			_, err := resolveExactTmuxControlContinue(project, team.DefaultProfile, "workstream", "qa", tc.client, candidate, tmuxControlContinueDeps{Verify: verify, Output: tc.output})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v, want %q", err, tc.want)
			}
		})
	}
	if _, err := resolveExactTmuxControlContinue(project, team.DefaultProfile, "workstream", "qa", "/dev/ttys001", mr, tmuxControlContinueDeps{}); err == nil || !strings.Contains(err.Error(), "dependencies are incomplete") {
		t.Fatalf("incomplete dependency error=%v", err)
	}
}

func TestContinueExactTmuxControlClientUsesOnlyExactArgv(t *testing.T) {
	var name string
	var args []string
	err := continueExactTmuxControlClient(tmuxControlContinueTarget{Client: "/dev/ttys001", PaneID: "%3"}, func(gotName string, gotArgs ...string) error {
		name, args = gotName, append([]string(nil), gotArgs...)
		return nil
	})
	if err != nil || name != "tmux" || !reflect.DeepEqual(args, []string{"refresh-client", "-t", "/dev/ttys001", "-A", "%3:continue"}) {
		t.Fatalf("mutation name=%q args=%v err=%v", name, args, err)
	}
	if err := continueExactTmuxControlClient(tmuxControlContinueTarget{Client: "bad\nclient", PaneID: "%3"}, func(string, ...string) error { t.Fatal("mutation called"); return nil }); err == nil {
		t.Fatal("invalid client reached mutation")
	}
	if err := continueExactTmuxControlClient(tmuxControlContinueTarget{Client: "/dev/ttys001", PaneID: "%3"}, nil); err == nil || !strings.Contains(err.Error(), "dependency is incomplete") {
		t.Fatalf("nil mutation dependency error=%v", err)
	}
}

func TestTmuxControlContinueStatusActionRequiresExactVerifiedIdentityAndUniqueClient(t *testing.T) {
	project, mr, verified := controlContinueFixture(t)
	row := statusRecord{
		Role: "qa", Handle: "qa", Session: "workstream", LiveIdentityMode: "managed_verified", LiveIdentity: &verified,
		Terminal: &terminalRuntimeJSON{Backend: runtimecontrol.BackendTmux, Target: mr.Record.Terminal.Target, Session: mr.Record.Terminal.Session,
			WindowID: mr.Record.Terminal.WindowID, PaneID: mr.Record.Terminal.PaneID, PaneAlive: true},
	}
	oldOutput := tmuxOutputCommand
	tmuxOutputCommand = func(_ string, args ...string) (string, error) {
		if args[0] != "list-clients" || !containsString(args, "-t") || !containsString(args, "terminal-s") {
			t.Fatalf("status discovery args=%v", args)
		}
		return "/dev/ttys001\t/dev/ttys001\t1\tcontrol-mode,pause-after=120\tterminal-s\n", nil
	}
	t.Cleanup(func() { tmuxOutputCommand = oldOutput })
	actions := tmuxControlContinueActionsForStatusRow(project, team.DefaultProfile, "workstream", row)
	if len(actions) != 1 || actions[0].Kind != "control_continue" || actions[0].ActionKind != "run" || !actions[0].Mutates || !actions[0].NeedsConfirmation || !actions[0].Available {
		t.Fatalf("control continue action=%+v", actions)
	}
	for _, want := range []string{"team member control-continue qa", "--client /dev/ttys001", "--project " + shellQuote(project), "--profile default", "--session workstream"} {
		if !strings.Contains(actions[0].Command, want) {
			t.Fatalf("action command missing %q: %s", want, actions[0].Command)
		}
	}
	row.LiveIdentity = nil
	if got := tmuxControlContinueActionsForStatusRow(project, team.DefaultProfile, "workstream", row); len(got) != 0 {
		t.Fatalf("mode string without verified identity emitted action: %+v", got)
	}
	row.LiveIdentity = &verified
	copy := *verified.Verified
	copy.Key.Session = "other-workstream"
	row.LiveIdentity = &liveidentity.Result{SchemaVersion: liveidentity.SchemaVersion, Verified: &copy}
	if got := tmuxControlContinueActionsForStatusRow(project, team.DefaultProfile, "workstream", row); len(got) != 0 {
		t.Fatalf("mismatched verified identity emitted action: %+v", got)
	}
}

func TestRunTeamMemberControlContinueHoldsAdmissionAndDoubleVerifies(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	project := seedTeam(t, team.Team{Orchestrated: true, Lead: "cto", Members: []team.Member{
		{Role: "cto", Handle: "cto", Binary: "codex", Session: "workstream"},
		{Role: "qa", Handle: "qa", Binary: "codex", Session: "workstream"},
	}})
	env, err := resolveAMQEnvForTeamLaunchProfile(project, team.DefaultProfile, "workstream", "qa")
	if err != nil {
		t.Fatal(err)
	}
	root := absoluteAMQRoot(project, env.Root)
	agentDir := filepath.Join(root, "agents", "qa")
	rec := launch.Record{
		Schema: launch.SchemaVersion, CWD: project, Binary: "codex", Session: "workstream", SharedWorkstream: true,
		Handle: "qa", Role: "qa", TeamProfile: team.DefaultProfile, TeamHome: project, Root: root,
		AgentPID: 1234, StartedAt: time.Now().UTC(),
		Tmux: &launch.TmuxInfo{Target: "new-window", Session: "terminal-s", WindowID: "@2", PaneID: "%3"},
	}
	rec.Terminal = launch.TerminalInfoFromTmux(rec.Tmux)
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	canonical, err := liveidentity.CanonicalProject(project)
	if err != nil {
		t.Fatal(err)
	}
	verified := liveidentity.Result{SchemaVersion: liveidentity.SchemaVersion, Verified: &liveidentity.Verified{
		Key:  liveidentity.Key{Project: canonical, Profile: team.DefaultProfile, Session: "workstream", Handle: "qa"},
		Role: "qa", Terminal: liveIdentityTerminal(rec),
	}}
	verifyCalls, outputCalls, listCalls, runCalls := 0, 0, 0, 0
	driftSecondClient := false
	oldDeps := productionTmuxControlContinueDeps
	productionTmuxControlContinueDeps = func() tmuxControlContinueDeps {
		return tmuxControlContinueDeps{
			Verify: func(gotProject, gotProfile, gotSession, gotHandle string) (liveidentity.Result, error) {
				verifyCalls++
				if gotProject != project || gotProfile != team.DefaultProfile || gotSession != "workstream" || gotHandle != "qa" {
					t.Fatalf("verify scope=%q/%q/%q/%q", gotProject, gotProfile, gotSession, gotHandle)
				}
				return verified, nil
			},
			Output: func(_ string, args ...string) (string, error) {
				outputCalls++
				switch args[0] {
				case "list-clients":
					listCalls++
					if driftSecondClient && listCalls == 2 {
						return "/dev/ttys002\t/dev/ttys002\t1\tcontrol-mode,pause-after=120\tterminal-s\n", nil
					}
					return "/dev/ttys001\t/dev/ttys001\t1\tcontrol-mode,pause-after=120\tterminal-s\n", nil
				case "display-message":
					return "%3\t@2\tterminal-s\n", nil
				default:
					return "", fmt.Errorf("unexpected output args: %v", args)
				}
			},
			Run: func(name string, args ...string) error {
				runCalls++
				if name != "tmux" || !reflect.DeepEqual(args, []string{"refresh-client", "-t", "/dev/ttys001", "-A", "%3:continue"}) {
					t.Fatalf("mutation name=%q args=%v", name, args)
				}
				return nil
			},
		}
	}
	t.Cleanup(func() { productionTmuxControlContinueDeps = oldDeps })
	_, _, err = captureOutput(t, func() error {
		return runTeamMember([]string{"control-continue", "qa", "--client", "/dev/ttys001", "--project", project, "--profile", team.DefaultProfile, "--session", "workstream", "--json"})
	})
	if err != nil {
		t.Fatal(err)
	}
	// R4 (#505 review): the product action repeats the idempotent continue
	// once, since a single refresh-client continue was empirically observed
	// to silently no-op.
	if verifyCalls != 2 || outputCalls != 4 || listCalls != 2 || runCalls != 2 {
		t.Fatalf("calls verify=%d output=%d list=%d run=%d", verifyCalls, outputCalls, listCalls, runCalls)
	}

	verifyCalls, outputCalls, listCalls, runCalls = 0, 0, 0, 0
	driftSecondClient = true
	_, _, err = captureOutput(t, func() error {
		return runTeamMember([]string{"control-continue", "qa", "--client", "/dev/ttys001", "--project", project, "--profile", team.DefaultProfile, "--session", "workstream", "--json"})
	})
	if err == nil || !strings.Contains(err.Error(), "differs from unique resolved client") || runCalls != 0 || verifyCalls != 2 || listCalls != 2 {
		t.Fatalf("inter-pass client drift err=%v verify=%d list=%d run=%d", err, verifyCalls, listCalls, runCalls)
	}
}
