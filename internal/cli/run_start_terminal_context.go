package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/runtimecontrol"
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
)

var observeRunStartTerminalContext = func() runtimecontrol.HostContext {
	controlMode := false
	if strings.TrimSpace(os.Getenv("TMUX")) != "" {
		controlMode = len(tmuxControlModeClients()) > 0
	}
	return runtimecontrol.DetectHostContext(os.Environ(), controlMode)
}

func projectRunStartTerminalContext(context runtimecontrol.HostContext) runwizard.TerminalContext {
	operations := make(map[string]runwizard.TerminalOperation, len(context.Operations))
	for name, state := range context.Operations {
		operations[name] = runwizard.TerminalOperation{State: state.State, ReasonCode: state.ReasonCode, Reason: state.Reason}
	}
	return runwizard.TerminalContext{
		SchemaVersion: context.SchemaVersion,
		Backend:       context.Backend,
		HostProgram:   context.HostProgram,
		InsideTmux:    context.InsideTmux,
		ControlMode:   context.ControlMode,
		Remote:        context.Remote,
		Tier:          context.Tier,
		Operations:    operations,
	}
}

func runStartTerminalDoctorCheck(context runtimecontrol.HostContext, tmux doctorCheck) doctorCheck {
	if tmux.Status != doctorOK {
		return tmux
	}
	if context.SchemaVersion != runtimecontrol.HostContextSchemaVersion {
		return doctorCheck{Name: "terminal context", Status: doctorFail, Detail: fmt.Sprintf("runtime terminal context schema=%d, want=%d", context.SchemaVersion, runtimecontrol.HostContextSchemaVersion)}
	}
	operationNames := make([]string, 0, len(context.Operations))
	for name := range context.Operations {
		operationNames = append(operationNames, name)
	}
	sort.Strings(operationNames)
	operations := make([]string, 0, len(operationNames))
	for _, name := range operationNames {
		operations = append(operations, name+"="+context.Operations[name].State)
	}
	return doctorCheck{
		Name:   "terminal context",
		Status: doctorOK,
		Detail: fmt.Sprintf("backend=%s host=%s tier=%s inside_tmux=%t control_mode=%t remote=%t operations=[%s] tmux=%s", context.Backend, context.HostProgram, context.Tier, context.InsideTmux, context.ControlMode, context.Remote, strings.Join(operations, ","), tmux.Detail),
	}
}
