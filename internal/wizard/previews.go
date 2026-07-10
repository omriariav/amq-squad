package wizard

import "strings"

// TopologyPreview renders the current run-start topology using plain box
// drawing characters. It is intentionally semantic rather than decorative:
// the same visibility value that Spec.Args emits selects the diagram.
func TopologyPreview(visibility string) string {
	switch strings.TrimSpace(visibility) {
	case "current":
		return "┌──── lead ────┬── worker 1 ──┐\n" +
			"├── worker 2 ──┼── worker 3 ──┤\n" +
			"└──────────────┴──────────────┘"
	case "detached":
		return "operator terminal\n" +
			"       │\n" +
			"       └── [ detached squad · attach to view ]"
	default:
		return "[ lead ]  [ worker 1 ]  [ worker 2 ]  [ worker 3 ]"
	}
}

func topologyConsequence(visibility string) string {
	switch strings.TrimSpace(visibility) {
	case "current":
		return "Split the current tmux window into visible agent panes."
	case "detached":
		return "Run agents hidden in a separate tmux session until you attach."
	default:
		return "Open one visible tmux window per agent."
	}
}
