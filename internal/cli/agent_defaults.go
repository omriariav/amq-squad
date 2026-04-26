package cli

func defaultChildArgsForBinary(binary string) []string {
	switch defaultHandleFor(binary) {
	case "codex":
		return []string{"--dangerously-bypass-approvals-and-sandbox"}
	case "claude":
		return []string{"--permission-mode", "auto"}
	default:
		return nil
	}
}

func ensureDefaultChildArgs(binary string, childArgs []string) []string {
	defaultArgs := defaultChildArgsForBinary(binary)
	if len(defaultArgs) == 0 || hasLeadingDefaultChildArgs(binary, childArgs) {
		return childArgs
	}
	out := append([]string(nil), defaultArgs...)
	return append(out, childArgs...)
}

func hasLeadingDefaultChildArgs(binary string, childArgs []string) bool {
	defaultArgs := defaultChildArgsForBinary(binary)
	if len(defaultArgs) == 0 || len(childArgs) < len(defaultArgs) {
		return false
	}
	for i := range defaultArgs {
		if childArgs[i] != defaultArgs[i] {
			return false
		}
	}
	return true
}

func shouldAppendBootstrap(binary string, childArgs []string) bool {
	if len(childArgs) == 0 {
		return true
	}
	defaultArgs := defaultChildArgsForBinary(binary)
	if len(childArgs) != len(defaultArgs) {
		return false
	}
	for i := range childArgs {
		if childArgs[i] != defaultArgs[i] {
			return false
		}
	}
	return true
}
