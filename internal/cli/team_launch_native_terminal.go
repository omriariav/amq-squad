package cli

import "strings"

const (
	nativeTerminalWindowIDPlaceholder  = "__AMQ_SQUAD_TERMINAL_WINDOW_ID__"
	nativeTerminalTabIDPlaceholder     = "__AMQ_SQUAD_TERMINAL_TAB_ID__"
	nativeTerminalSessionIDPlaceholder = "__AMQ_SQUAD_TERMINAL_SESSION_ID__"
	nativeTerminalTTYPlaceholder       = "__AMQ_SQUAD_TERMINAL_TTY__"
)

type nativeTerminalEnv struct {
	Key   string
	Value string
	Raw   bool
}

func nativeTerminalWindowName(workstream, role string) string {
	return "amq:" + workstream + ":" + role
}

func nativeTerminalLaunchPayload(agentCommand string, env []nativeTerminalEnv) string {
	assignments := make([]string, 0, len(env))
	for _, item := range env {
		value := item.Value
		if !item.Raw {
			value = posixSingleQuote(item.Value)
		}
		assignments = append(assignments, item.Key+"="+value)
	}
	return "export " + strings.Join(assignments, " ") + "; " + agentCommand
}

func posixSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
