package cli

import (
	"fmt"
	"strings"
)

func exactTmuxPaneID(value string) (string, error) {
	return exactTmuxID(value, '%', "pane")
}

func exactTmuxWindowID(value string) (string, error) {
	return exactTmuxID(value, '@', "window")
}

func exactTmuxID(value string, prefix byte, kind string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) < 2 || value[0] != prefix {
		return "", fmt.Errorf("tmux %s id %q is not an exact %c<digits> id", kind, value, prefix)
	}
	for _, ch := range value[1:] {
		if ch < '0' || ch > '9' {
			return "", fmt.Errorf("tmux %s id %q is not an exact %c<digits> id", kind, value, prefix)
		}
	}
	return value, nil
}
