package cli

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/liveidentity"
	"github.com/omriariav/amq-squad/v3/internal/runtimecontrol"
	"github.com/omriariav/amq-squad/v3/internal/team"
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
