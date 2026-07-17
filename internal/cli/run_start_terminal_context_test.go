package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/runtimecontrol"
)

func TestProjectRunStartTerminalContextPreservesRuntimeContract(t *testing.T) {
	host := runtimecontrol.DetectHostContext([]string{"TERM_PROGRAM=iTerm.app", "TMUX=/private/secret/socket", "SSH_CONNECTION=secret endpoint"}, true)
	projected := projectRunStartTerminalContext(host)
	if projected.SchemaVersion != runtimecontrol.HostContextSchemaVersion || projected.Backend != runtimecontrol.BackendTmux || projected.HostProgram != "iterm2" || !projected.InsideTmux || !projected.ControlMode || !projected.Remote || projected.Tier != runtimecontrol.TierA {
		t.Fatalf("projected terminal context = %+v", projected)
	}
	if len(projected.Operations) != len(host.Operations) {
		t.Fatalf("projected operations = %d, want %d", len(projected.Operations), len(host.Operations))
	}
	for name, state := range host.Operations {
		got := projected.Operations[name]
		if got.State != state.State || got.ReasonCode != state.ReasonCode || got.Reason != state.Reason {
			t.Fatalf("projected operation %s = %+v, want %+v", name, got, state)
		}
	}
}

func TestRunStartTerminalDoctorCheckBindsSchemaAndOperations(t *testing.T) {
	tmux := doctorCheck{Name: "tmux", Status: doctorOK, Detail: "/usr/bin/tmux"}
	host := runtimecontrol.DetectHostContext([]string{"TERM_PROGRAM=Apple_Terminal"}, false)
	check := runStartTerminalDoctorCheck(host, tmux)
	if check.Status != doctorOK {
		t.Fatalf("terminal check = %+v", check)
	}
	for _, want := range []string{"backend=terminal_app", "tier=C", "launch_current_window=unsupported", "launch_new_window=supported", "tmux=/usr/bin/tmux"} {
		if !strings.Contains(check.Detail, want) {
			t.Fatalf("terminal detail missing %q: %s", want, check.Detail)
		}
	}

	host.SchemaVersion++
	check = runStartTerminalDoctorCheck(host, tmux)
	if check.Status != doctorFail || !strings.Contains(check.Detail, "schema=") {
		t.Fatalf("schema mismatch check = %+v", check)
	}

	missingTmux := doctorCheck{Name: "tmux", Status: doctorFail, Detail: "missing"}
	if got := runStartTerminalDoctorCheck(runtimecontrol.HostContext{}, missingTmux); got != missingTmux {
		t.Fatalf("tmux failure was not preserved: %+v", got)
	}
}

func TestPrepareRunStartWizardSuppliesCLIProjectedTerminalContext(t *testing.T) {
	oldObserve := observeRunStartTerminalContext
	observeRunStartTerminalContext = func() runtimecontrol.HostContext {
		return runtimecontrol.DetectHostContext([]string{"TERM_PROGRAM=iTerm.app"}, false)
	}
	t.Cleanup(func() { observeRunStartTerminalContext = oldObserve })

	_, opts, err := prepareRunStartWizard([]string{"--project", "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.TerminalContext.Backend != runtimecontrol.BackendITerm2 || opts.TerminalContext.HostProgram != "iterm2" || opts.TerminalContext.InsideTmux || opts.TerminalContext.Tier != runtimecontrol.TierB {
		t.Fatalf("wizard terminal context = %+v", opts.TerminalContext)
	}
}
